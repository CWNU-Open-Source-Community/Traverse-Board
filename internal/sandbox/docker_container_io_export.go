package sandbox

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	DockerOutputStagingProtocolVersion = "sandbox_docker_output_staging.v1"
	DockerOutputCommitProtocolVersion  = "sandbox_docker_output_commit.v1"
	DockerOutputStagingStatusCompleted = "completed"
	DockerOutputStagingStatusTruncated = "truncated_bytes"
	DockerOutputStagingStatusInvalid   = "invalid_archive"
	DockerOutputStagingStatusPath      = "rejected_path"
	DockerOutputStagingStatusLink      = "rejected_link"
	DockerOutputStagingStatusDuplicate = "rejected_duplicate"
	DockerOutputCommitStatusCommitted  = "committed"
	MaxDockerOutputFiles               = 64
	MaxDockerOutputFileBytes           = 4 * 1024 * 1024
	MaxDockerOutputTotalBytes          = 16 * 1024 * 1024
	MaxDockerOutputArchiveHeaderBytes  = 1024 * 1024
)

// DockerOutputExportPlan pins the exact dedicated output mount export for one
// lifecycle attempt. The archive is read through the strict staging walker;
// nothing lands on the host outside the Run-scoped staging directory.
type DockerOutputExportPlan struct {
	ProtocolVersion        string
	AttemptID              string
	Generation             int64
	RunID                  string
	ContainerIDFingerprint string
	OutputMountTarget      string
	MaxFiles               int
	MaxFileBytes           int64
	MaxTotalBytes          int64
	ExportFingerprint      string
}

func NewDockerOutputExportPlan(attemptID string, generation int64, runID,
	containerIDFingerprint, outputMountTarget string, maxFiles int,
	maxFileBytes, maxTotalBytes int64,
) (DockerOutputExportPlan, error) {
	plan := DockerOutputExportPlan{
		ProtocolVersion: DockerOutputStagingProtocolVersion, AttemptID: attemptID,
		Generation: generation, RunID: runID, ContainerIDFingerprint: containerIDFingerprint,
		OutputMountTarget: outputMountTarget, MaxFiles: maxFiles, MaxFileBytes: maxFileBytes,
		MaxTotalBytes: maxTotalBytes,
	}
	plan.ExportFingerprint = dockerOutputExportPlanFingerprint(plan)
	return plan, plan.Validate()
}

func (plan DockerOutputExportPlan) Validate() error {
	for label, value := range map[string]string{
		"docker output export attempt id": plan.AttemptID,
		"docker output export Run id":     plan.RunID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker output export identity is invalid")
		}
	}
	if plan.ProtocolVersion != DockerOutputStagingProtocolVersion || plan.Generation != 1 ||
		!validDigest(plan.ContainerIDFingerprint) ||
		validateVirtualPath("docker output mount target", plan.OutputMountTarget) != nil ||
		plan.MaxFiles < 1 || plan.MaxFiles > MaxDockerOutputFiles ||
		plan.MaxFileBytes < 1 || plan.MaxFileBytes > MaxDockerOutputFileBytes ||
		plan.MaxTotalBytes < plan.MaxFileBytes || plan.MaxTotalBytes > MaxDockerOutputTotalBytes ||
		plan.ExportFingerprint != dockerOutputExportPlanFingerprint(plan) {
		return errors.New("docker output export plan violates a fixed bound")
	}
	return nil
}

func dockerOutputExportPlanFingerprint(plan DockerOutputExportPlan) string {
	return fingerprint(DockerOutputStagingProtocolVersion, plan.AttemptID,
		strconv.FormatInt(plan.Generation, 10), plan.RunID, plan.ContainerIDFingerprint,
		plan.OutputMountTarget, strconv.Itoa(plan.MaxFiles),
		strconv.FormatInt(plan.MaxFileBytes, 10), strconv.FormatInt(plan.MaxTotalBytes, 10))
}

// DockerStagedOutputEntry is one validated, redacted, staged output file. The
// container path is canonical; the digest binds the exact staged bytes.
type DockerStagedOutputEntry struct {
	Path      string
	SHA256    string
	SizeBytes int64
	MediaType string
	Redacted  bool
}

func (entry DockerStagedOutputEntry) Validate() error {
	if validateContainerRelativePath("docker staged output path", entry.Path) != nil ||
		!validDigest(entry.SHA256) || entry.SizeBytes < 1 || entry.SizeBytes > MaxDockerOutputFileBytes ||
		validateBoundedText("docker staged media type", entry.MediaType, 256, false) != nil {
		return errors.New("docker staged output entry is invalid")
	}
	if _, _, err := mime.ParseMediaType(entry.MediaType); err != nil {
		return errors.New("docker staged output media type is invalid")
	}
	return nil
}

// DockerOutputStagingReceipt is the durable, content-free evidence of one
// bounded output export. Raw file bytes stay in the process-local staging
// directory and never enter SQLite or events.
type DockerOutputStagingReceipt struct {
	ProtocolVersion        string
	ID                     string
	AttemptID              string
	Generation             int64
	RunID                  string
	ContainerIDFingerprint string
	Status                 string
	Entries                []DockerStagedOutputEntry
	FileCount              int
	TotalBytes             int64
	RedactedCount          int
	EntryDigestSet         string
	ExportFingerprint      string
	ReceiptFingerprint     string
	CreatedAt              time.Time
}

func NewDockerOutputStagingReceipt(id, attemptID string, generation int64, runID,
	containerIDFingerprint string, plan DockerOutputExportPlan,
	entries []DockerStagedOutputEntry, status string, createdAt time.Time,
) (DockerOutputStagingReceipt, error) {
	receipt := DockerOutputStagingReceipt{
		ProtocolVersion: DockerOutputStagingProtocolVersion, ID: id, AttemptID: attemptID,
		Generation: generation, RunID: runID, ContainerIDFingerprint: containerIDFingerprint,
		Status: status, Entries: append([]DockerStagedOutputEntry(nil), entries...),
		ExportFingerprint: plan.ExportFingerprint, CreatedAt: createdAt,
	}
	receipt.FileCount = len(receipt.Entries)
	for _, entry := range receipt.Entries {
		receipt.TotalBytes += entry.SizeBytes
		if entry.Redacted {
			receipt.RedactedCount++
		}
	}
	receipt.EntryDigestSet = dockerStagedOutputDigestSet(receipt.Entries)
	receipt.ReceiptFingerprint = dockerOutputStagingReceiptFingerprint(receipt)
	return receipt, receipt.Validate()
}

func (receipt DockerOutputStagingReceipt) Validate() error {
	for label, value := range map[string]string{
		"docker staging receipt id": receipt.ID, "docker staging receipt attempt id": receipt.AttemptID,
		"docker staging receipt Run id": receipt.RunID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker output staging receipt identity is invalid")
		}
	}
	if receipt.ProtocolVersion != DockerOutputStagingProtocolVersion || receipt.Generation != 1 ||
		!validDigest(receipt.ContainerIDFingerprint) || !validDigest(receipt.ExportFingerprint) ||
		!validDockerOutputStagingStatus(receipt.Status) || receipt.CreatedAt.IsZero() ||
		receipt.FileCount != len(receipt.Entries) || len(receipt.Entries) > MaxDockerOutputFiles {
		return errors.New("docker output staging receipt is invalid")
	}
	var total int64
	redactedCount := 0
	previous := ""
	for _, entry := range receipt.Entries {
		if entry.Validate() != nil || (previous != "" && previous >= entry.Path) {
			return errors.New("docker staged output entry sequence is invalid")
		}
		previous = entry.Path
		total += entry.SizeBytes
		if entry.Redacted {
			redactedCount++
		}
	}
	if receipt.RedactedCount != redactedCount || receipt.TotalBytes != total ||
		receipt.EntryDigestSet != dockerStagedOutputDigestSet(receipt.Entries) ||
		receipt.ReceiptFingerprint != dockerOutputStagingReceiptFingerprint(receipt) {
		return errors.New("docker output staging receipt aggregate is invalid")
	}
	switch receipt.Status {
	case DockerOutputStagingStatusCompleted, DockerOutputStagingStatusTruncated:
	case DockerOutputStagingStatusInvalid, DockerOutputStagingStatusPath,
		DockerOutputStagingStatusLink, DockerOutputStagingStatusDuplicate:
		if len(receipt.Entries) != 0 {
			return errors.New("rejected docker staging receipt carries entries")
		}
	default:
		return errors.New("docker output staging status is invalid")
	}
	return nil
}

func validDockerOutputStagingStatus(status string) bool {
	switch status {
	case DockerOutputStagingStatusCompleted, DockerOutputStagingStatusTruncated,
		DockerOutputStagingStatusInvalid, DockerOutputStagingStatusPath,
		DockerOutputStagingStatusLink, DockerOutputStagingStatusDuplicate:
		return true
	default:
		return false
	}
}

func dockerOutputStagingReceiptFingerprint(receipt DockerOutputStagingReceipt) string {
	parts := []string{DockerOutputStagingProtocolVersion, receipt.ID, receipt.AttemptID,
		strconv.FormatInt(receipt.Generation, 10), receipt.RunID,
		receipt.ContainerIDFingerprint, receipt.Status, receipt.ExportFingerprint,
		receipt.EntryDigestSet, strconv.Itoa(receipt.FileCount)}
	return fingerprint(parts...)
}

func dockerStagedOutputDigestSet(entries []DockerStagedOutputEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Path+":"+entry.SHA256)
	}
	sort.Strings(parts)
	return fingerprint(parts...)
}

// StageDockerOutputArchive walks the exported tar stream under the plan
// bounds, rejects escaping or linked entries, redacts text content, and writes
// only validated regular files into stagingRoot. The returned receipt records
// metadata and digests; raw bytes remain on the process-local filesystem.
func StageDockerOutputArchive(ctx context.Context, plan DockerOutputExportPlan,
	tarStream io.Reader, stagingRoot, id string, createdAt time.Time,
) (DockerOutputStagingReceipt, error) {
	if err := plan.Validate(); err != nil {
		return DockerOutputStagingReceipt{}, err
	}
	if validateStoredIdentity("docker staging receipt id", id) != nil || createdAt.IsZero() {
		return DockerOutputStagingReceipt{}, errors.New("docker output staging identity is invalid")
	}
	if tarStream == nil {
		return DockerOutputStagingReceipt{}, errors.New("docker output archive is required")
	}
	info, err := os.Stat(stagingRoot)
	if err != nil || !info.IsDir() {
		return DockerOutputStagingReceipt{}, errors.New("docker output staging root is required")
	}
	stagingRoot, err = filepath.Abs(stagingRoot)
	if err != nil {
		return DockerOutputStagingReceipt{}, err
	}
	reader := tar.NewReader(io.LimitReader(tarStream, plan.MaxTotalBytes+MaxDockerOutputArchiveHeaderBytes))
	entries := make([]DockerStagedOutputEntry, 0, plan.MaxFiles)
	seen := make(map[string]bool)
	status := DockerOutputStagingStatusCompleted
	var total int64
	headers := 0
	for {
		if err := ctx.Err(); err != nil {
			return DockerOutputStagingReceipt{}, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			status = DockerOutputStagingStatusInvalid
			break
		}
		headers++
		if headers > MaxDockerOutputFiles*4 {
			status = DockerOutputStagingStatusTruncated
			break
		}
		relative, err := normalizeDockerArchiveName(header.Name)
		if err != nil {
			status = DockerOutputStagingStatusPath
			break
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			status = DockerOutputStagingStatusLink
			break
		}
		if status != DockerOutputStagingStatusCompleted {
			break
		}
		if seen[relative] {
			status = DockerOutputStagingStatusDuplicate
			break
		}
		if header.Size < 0 || header.Size > plan.MaxFileBytes ||
			total+header.Size > plan.MaxTotalBytes || len(entries) >= plan.MaxFiles {
			status = DockerOutputStagingStatusTruncated
			break
		}
		content, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if err != nil || int64(len(content)) != header.Size {
			status = DockerOutputStagingStatusInvalid
			break
		}
		seen[relative] = true
		redacted := utf8.Valid(content) && redact.String(string(content)) != string(content)
		if utf8.Valid(content) {
			redactedContent := redact.String(string(content))
			redacted = redactedContent != string(content)
			content = []byte(redactedContent)
		}
		digest := sha256.Sum256(content)
		mediaType := dockerOutputMediaType(relative, content)
		entry := DockerStagedOutputEntry{
			Path: relative, SHA256: hex.EncodeToString(digest[:]),
			SizeBytes: int64(len(content)), MediaType: mediaType, Redacted: redacted,
		}
		if err := writeDockerStagedFile(stagingRoot, relative, content); err != nil {
			return DockerOutputStagingReceipt{}, err
		}
		entries = append(entries, entry)
		total += entry.SizeBytes
	}
	// Rejected archives carry no trusted manifest: drop any partially staged
	// entries so the receipt can never be used for a commit.
	if status != DockerOutputStagingStatusCompleted && status != DockerOutputStagingStatusTruncated {
		entries = nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return NewDockerOutputStagingReceipt(id, plan.AttemptID, plan.Generation, plan.RunID,
		plan.ContainerIDFingerprint, plan, entries, status, createdAt.UTC())
}

// normalizeDockerArchiveName accepts a plain relative name or a leading "./"
// prefix (which the Docker archive API emits for directory entries) and then
// applies the strict container-relative path rules.
func normalizeDockerArchiveName(name string) (string, error) {
	cleaned := strings.TrimPrefix(name, "./")
	if cleaned == "" || cleaned == name && strings.HasPrefix(name, "../") {
		return "", errors.New("docker archive name is invalid")
	}
	if err := validateContainerRelativePath("docker archive path", cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func writeDockerStagedFile(stagingRoot, relative string, content []byte) error {
	full, err := dockerStagingHostPath(stagingRoot, relative)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, content, 0o600)
}

// dockerStagingHostPath converts the already validated container-relative
// slash path into the current host's path form before applying a lexical
// containment check. filepath.Rel keeps the same boundary on Unix and Windows
// without comparing a Windows backslash path to a container slash path.
func dockerStagingHostPath(stagingRoot, relative string) (string, error) {
	if validateContainerRelativePath("docker staged output path", relative) != nil {
		return "", errors.New("docker staged output path escaped the staging root")
	}
	hostRelative := filepath.Clean(filepath.FromSlash(relative))
	if hostRelative == "." || filepath.IsAbs(hostRelative) ||
		filepath.VolumeName(hostRelative) != "" {
		return "", errors.New("docker staged output path escaped the staging root")
	}
	cleanRoot := filepath.Clean(stagingRoot)
	full := filepath.Clean(filepath.Join(cleanRoot, hostRelative))
	within, err := filepath.Rel(cleanRoot, full)
	if err != nil || within == ".." || filepath.IsAbs(within) ||
		strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", errors.New("docker staged output path escaped the staging root")
	}
	return full, nil
}

func dockerOutputMediaType(relative string, content []byte) string {
	switch strings.ToLower(path.Ext(relative)) {
	case ".json":
		return "application/json"
	case ".txt", ".log":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "application/xml"
	case ".yaml", ".yml":
		return "application/yaml"
	}
	if utf8.Valid(content) {
		return "text/plain"
	}
	return "application/octet-stream"
}

// DockerOutputCommitEntry is one accepted output file. Acceptance requires
// the exact path, digest, size, and media type of a staged entry.
type DockerOutputCommitEntry struct {
	Path      string
	SHA256    string
	SizeBytes int64
	MediaType string
}

func (entry DockerOutputCommitEntry) Validate() error {
	if validateContainerRelativePath("docker commit path", entry.Path) != nil ||
		!validDigest(entry.SHA256) || entry.SizeBytes < 1 || entry.SizeBytes > MaxDockerOutputFileBytes ||
		validateBoundedText("docker commit media type", entry.MediaType, 256, false) != nil {
		return errors.New("docker output commit entry is invalid")
	}
	if _, _, err := mime.ParseMediaType(entry.MediaType); err != nil {
		return errors.New("docker output commit media type is invalid")
	}
	return nil
}

// DockerOutputCommitRequest freezes one explicit acceptance manifest. Only
// entries that exactly match the staging receipt can be committed, and the
// operation key makes the commit idempotent.
type DockerOutputCommitRequest struct {
	ProtocolVersion    string
	AttemptID          string
	Generation         int64
	RunID              string
	WorkspaceID        string
	StagingReceiptID   string
	OperationKeyDigest string
	AcceptedEntries    []DockerOutputCommitEntry
	RequestFingerprint string
}

func NewDockerOutputCommitRequest(attemptID string, generation int64, runID, workspaceID,
	stagingReceiptID, operationKeyDigest string, accepted []DockerOutputCommitEntry,
) (DockerOutputCommitRequest, error) {
	request := DockerOutputCommitRequest{
		ProtocolVersion: DockerOutputCommitProtocolVersion, AttemptID: attemptID,
		Generation: generation, RunID: runID, WorkspaceID: workspaceID,
		StagingReceiptID: stagingReceiptID, OperationKeyDigest: operationKeyDigest,
		AcceptedEntries: append([]DockerOutputCommitEntry(nil), accepted...),
	}
	request.RequestFingerprint = DockerOutputCommitRequestFingerprint(request)
	return request, request.Validate()
}

func (request DockerOutputCommitRequest) Validate() error {
	for label, value := range map[string]string{
		"docker commit attempt id": request.AttemptID, "docker commit Run id": request.RunID,
		"docker commit workspace id":       request.WorkspaceID,
		"docker commit staging receipt id": request.StagingReceiptID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker output commit request identity is invalid")
		}
	}
	if request.ProtocolVersion != DockerOutputCommitProtocolVersion || request.Generation != 1 ||
		!validDigest(request.OperationKeyDigest) ||
		len(request.AcceptedEntries) == 0 || len(request.AcceptedEntries) > MaxDockerOutputFiles ||
		request.RequestFingerprint != DockerOutputCommitRequestFingerprint(request) {
		return errors.New("docker output commit request is invalid")
	}
	previous := ""
	for _, entry := range request.AcceptedEntries {
		if entry.Validate() != nil || (previous != "" && previous >= entry.Path) {
			return errors.New("docker output commit entry sequence is invalid")
		}
		previous = entry.Path
	}
	return nil
}

// Binds checks that every accepted entry exactly matches a staged entry.
func (request DockerOutputCommitRequest) Binds(staging DockerOutputStagingReceipt) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if staging.Status != DockerOutputStagingStatusCompleted ||
		staging.AttemptID != request.AttemptID || staging.Generation != request.Generation ||
		staging.RunID != request.RunID || staging.ID != request.StagingReceiptID {
		return errors.New("docker output commit does not bind the completed staging receipt")
	}
	staged := make(map[string]DockerStagedOutputEntry, len(staging.Entries))
	for _, entry := range staging.Entries {
		staged[entry.Path] = entry
	}
	for _, entry := range request.AcceptedEntries {
		expected, ok := staged[entry.Path]
		if !ok || expected.SHA256 != entry.SHA256 || expected.SizeBytes != entry.SizeBytes ||
			expected.MediaType != entry.MediaType {
			return errors.New("docker output commit accepted an un-staged entry")
		}
	}
	return nil
}

func DockerOutputCommitRequestFingerprint(request DockerOutputCommitRequest) string {
	parts := []string{DockerOutputCommitProtocolVersion, request.AttemptID,
		strconv.FormatInt(request.Generation, 10), request.RunID, request.WorkspaceID,
		request.StagingReceiptID, request.OperationKeyDigest,
		strconv.Itoa(len(request.AcceptedEntries))}
	for _, entry := range request.AcceptedEntries {
		parts = append(parts, entry.Path, entry.SHA256, strconv.FormatInt(entry.SizeBytes, 10),
			entry.MediaType)
	}
	return fingerprint(parts...)
}

// DockerOutputCommitReceipt is the durable, content-free evidence of one
// atomic commit. All entries are inserted with the receipt in one
// transaction; a failed commit leaves no partial rows.
type DockerOutputCommitReceipt struct {
	ProtocolVersion    string
	ID                 string
	AttemptID          string
	Generation         int64
	RunID              string
	WorkspaceID        string
	OperationKeyDigest string
	RequestFingerprint string
	Status             string
	Entries            []DockerOutputCommitEntry
	CommittedCount     int
	CommittedDigestSet string
	ReceiptFingerprint string
	CreatedAt          time.Time
}

func NewDockerOutputCommitReceipt(id, attemptID string, generation int64, runID,
	workspaceID string, request DockerOutputCommitRequest, createdAt time.Time,
) (DockerOutputCommitReceipt, error) {
	receipt := DockerOutputCommitReceipt{
		ProtocolVersion: DockerOutputCommitProtocolVersion, ID: id, AttemptID: attemptID,
		Generation: generation, RunID: runID, WorkspaceID: workspaceID,
		OperationKeyDigest: request.OperationKeyDigest, RequestFingerprint: request.RequestFingerprint,
		Status:    DockerOutputCommitStatusCommitted,
		Entries:   append([]DockerOutputCommitEntry(nil), request.AcceptedEntries...),
		CreatedAt: createdAt,
	}
	receipt.CommittedCount = len(receipt.Entries)
	receipt.CommittedDigestSet = dockerCommittedDigestSet(receipt.Entries)
	receipt.ReceiptFingerprint = dockerOutputCommitReceiptFingerprint(receipt)
	return receipt, receipt.Validate()
}

func (receipt DockerOutputCommitReceipt) Validate() error {
	for label, value := range map[string]string{
		"docker commit receipt id": receipt.ID, "docker commit receipt attempt id": receipt.AttemptID,
		"docker commit receipt Run id": receipt.RunID, "docker commit receipt workspace id": receipt.WorkspaceID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker output commit receipt identity is invalid")
		}
	}
	if receipt.ProtocolVersion != DockerOutputCommitProtocolVersion || receipt.Generation != 1 ||
		!validDigest(receipt.OperationKeyDigest) || !validDigest(receipt.RequestFingerprint) ||
		receipt.Status != DockerOutputCommitStatusCommitted || receipt.CreatedAt.IsZero() ||
		receipt.CommittedCount != len(receipt.Entries) || len(receipt.Entries) == 0 ||
		len(receipt.Entries) > MaxDockerOutputFiles {
		return errors.New("docker output commit receipt is invalid")
	}
	previous := ""
	for _, entry := range receipt.Entries {
		if entry.Validate() != nil || (previous != "" && previous >= entry.Path) {
			return errors.New("docker output commit entry sequence is invalid")
		}
		previous = entry.Path
	}
	if receipt.CommittedDigestSet != dockerCommittedDigestSet(receipt.Entries) ||
		receipt.ReceiptFingerprint != dockerOutputCommitReceiptFingerprint(receipt) {
		return errors.New("docker output commit receipt aggregate is invalid")
	}
	return nil
}

func dockerCommittedDigestSet(entries []DockerOutputCommitEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Path+":"+entry.SHA256)
	}
	sort.Strings(parts)
	return fingerprint(parts...)
}

func dockerOutputCommitReceiptFingerprint(receipt DockerOutputCommitReceipt) string {
	parts := []string{DockerOutputCommitProtocolVersion, receipt.ID, receipt.AttemptID,
		strconv.FormatInt(receipt.Generation, 10), receipt.RunID, receipt.WorkspaceID,
		receipt.OperationKeyDigest, receipt.RequestFingerprint, receipt.Status,
		receipt.CommittedDigestSet, strconv.Itoa(receipt.CommittedCount)}
	return fingerprint(parts...)
}

// VerifyDockerOutputCommit re-reads every accepted file from the staging
// directory and confirms the exact digest and size. It returns the canonical
// committed entries used by the store insert.
func VerifyDockerOutputCommit(stagingRoot string,
	request DockerOutputCommitRequest,
) ([]DockerOutputCommitEntry, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	info, err := os.Stat(stagingRoot)
	if err != nil || !info.IsDir() {
		return nil, errors.New("docker output staging root is unavailable")
	}
	entries := make([]DockerOutputCommitEntry, 0, len(request.AcceptedEntries))
	for _, entry := range request.AcceptedEntries {
		full, pathErr := dockerStagingHostPath(stagingRoot, entry.Path)
		if pathErr != nil {
			return nil, errors.New("docker output commit path escaped the staging root")
		}
		content, err := os.ReadFile(full)
		if err != nil || int64(len(content)) != entry.SizeBytes {
			return nil, errors.New("docker output commit file is unavailable")
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			return nil, errors.New("docker output commit digest does not match staging")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
