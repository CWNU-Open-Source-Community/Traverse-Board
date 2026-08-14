package sandbox

import (
	"errors"
	"fmt"
	"mime"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DockerInputProjectionProtocolVersion = "sandbox_docker_input_projection.v1"
	MaxDockerInputProjectionEntries      = MaxInputArtifacts
	MaxDockerInputProjectionFileBytes    = 4 * 1024 * 1024
	MaxDockerInputProjectionTotalBytes   = MaxCapturedOutputBytes
)

// DockerInputProjectionEntry is one sealed read-only input file. The path is
// a canonical container-relative path, the digest binds the exact bytes, and
// nothing in the entry grants execution authority.
type DockerInputProjectionEntry struct {
	Ordinal   int
	Path      string
	SHA256    string
	SizeBytes int64
	MediaType string
}

func (entry DockerInputProjectionEntry) Validate() error {
	if entry.Ordinal < 1 || entry.Ordinal > MaxDockerInputProjectionEntries ||
		validateContainerRelativePath("docker input projection path", entry.Path) != nil ||
		!validDigest(entry.SHA256) || entry.SizeBytes < 1 ||
		entry.SizeBytes > MaxDockerInputProjectionFileBytes ||
		validateBoundedText("docker input media type", entry.MediaType, 256, false) != nil {
		return errors.New("docker input projection entry is invalid")
	}
	if _, _, err := mime.ParseMediaType(entry.MediaType); err != nil {
		return errors.New("docker input projection media type is invalid")
	}
	return nil
}

// DockerInputProjection seals the read-only input boundary for one container
// lifecycle attempt. It binds the lifecycle generation/attempt identity, the
// exact plan and observation, and a fingerprint over every entry. The daemon
// mount stays read-only and the container cannot modify the original
// Workspace through this boundary.
type DockerInputProjection struct {
	ProtocolVersion       string
	ID                    string
	AttemptID             string
	Generation            int64
	PlanID                string
	ObservationID         string
	RunID                 string
	MissionID             string
	WorkspaceID           string
	InputArtifactDigest   string
	SpecFingerprint       string
	AuthorityFingerprint  string
	MountTarget           string
	MountReadOnly         bool
	Entries               []DockerInputProjectionEntry
	EntryCount            int
	TotalBytes            int64
	ProjectionFingerprint string
	CreatedAt             time.Time
}

func NewDockerInputProjection(id, attemptID string, generation int64, planID,
	observationID, runID, missionID, workspaceID, inputArtifactDigest,
	specFingerprint, authorityFingerprint, mountTarget string,
	entries []DockerInputProjectionEntry, createdAt time.Time,
) (DockerInputProjection, error) {
	projection := DockerInputProjection{
		ProtocolVersion: DockerInputProjectionProtocolVersion, ID: id, AttemptID: attemptID,
		Generation: generation, PlanID: planID, ObservationID: observationID, RunID: runID,
		MissionID: missionID, WorkspaceID: workspaceID, InputArtifactDigest: inputArtifactDigest,
		SpecFingerprint: specFingerprint, AuthorityFingerprint: authorityFingerprint,
		MountTarget: mountTarget, MountReadOnly: true,
		Entries: append([]DockerInputProjectionEntry(nil), entries...), CreatedAt: createdAt,
	}
	projection.EntryCount = len(projection.Entries)
	for _, entry := range projection.Entries {
		projection.TotalBytes += entry.SizeBytes
	}
	projection.ProjectionFingerprint = dockerInputProjectionFingerprint(projection)
	return projection, projection.Validate()
}

func (projection DockerInputProjection) Validate() error {
	for label, value := range map[string]string{
		"docker input projection id":             projection.ID,
		"docker input projection attempt id":     projection.AttemptID,
		"docker input projection plan id":        projection.PlanID,
		"docker input projection observation id": projection.ObservationID,
		"docker input projection Run id":         projection.RunID,
		"docker input projection Mission id":     projection.MissionID,
		"docker input projection workspace id":   projection.WorkspaceID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker input projection identity is invalid")
		}
	}
	if projection.ProtocolVersion != DockerInputProjectionProtocolVersion ||
		projection.Generation != 1 || !validDigest(projection.InputArtifactDigest) ||
		!validDigest(projection.SpecFingerprint) || !validDigest(projection.AuthorityFingerprint) ||
		projection.MountTarget != DockerInputArtifactMountTarget || !projection.MountReadOnly ||
		projection.CreatedAt.IsZero() {
		return errors.New("docker input projection violates a fixed read-only invariant")
	}
	if len(projection.Entries) == 0 || len(projection.Entries) > MaxDockerInputProjectionEntries ||
		projection.EntryCount != len(projection.Entries) {
		return errors.New("docker input projection entry count is invalid")
	}
	var total int64
	previousPath := ""
	for index, entry := range projection.Entries {
		if entry.Ordinal != index+1 || entry.Validate() != nil ||
			(previousPath != "" && previousPath >= entry.Path) {
			return errors.New("docker input projection entry sequence is invalid")
		}
		previousPath = entry.Path
		total += entry.SizeBytes
	}
	if total < 1 || total > MaxDockerInputProjectionTotalBytes || projection.TotalBytes != total ||
		projection.ProjectionFingerprint != dockerInputProjectionFingerprint(projection) {
		return errors.New("docker input projection aggregate is invalid")
	}
	return nil
}

func dockerInputProjectionFingerprint(projection DockerInputProjection) string {
	parts := []string{DockerInputProjectionProtocolVersion, projection.ID, projection.AttemptID,
		strconv.FormatInt(projection.Generation, 10), projection.PlanID, projection.ObservationID,
		projection.RunID, projection.MissionID, projection.WorkspaceID,
		projection.InputArtifactDigest, projection.SpecFingerprint,
		projection.AuthorityFingerprint, projection.MountTarget,
		strconv.FormatBool(projection.MountReadOnly), strconv.Itoa(len(projection.Entries))}
	for _, entry := range projection.Entries {
		parts = append(parts, strconv.Itoa(entry.Ordinal), entry.Path, entry.SHA256,
			strconv.FormatInt(entry.SizeBytes, 10), entry.MediaType)
	}
	return fingerprint(parts...)
}

// validateContainerRelativePath rejects absolute paths, traversal, Windows
// separators, drive letters, NUL, control characters, and non-canonical
// forms. The same rules run on every host OS so a Linux daemon and a Windows
// operator share one path boundary.
func validateContainerRelativePath(label, value string) error {
	if err := validateBoundedText(label, value, 1024, false); err != nil {
		return err
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") ||
		strings.ContainsRune(value, 0) || path.IsAbs(value) || path.Clean(value) != value ||
		value == "." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") ||
		utf8.RuneCountInString(value) > 512 {
		return fmt.Errorf("%s must be a canonical container-relative path", label)
	}
	if len(value) >= 2 && value[1] == ':' {
		return fmt.Errorf("%s must not carry a drive letter", label)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." ||
			strings.HasSuffix(component, " ") || strings.HasSuffix(component, ".") {
			return fmt.Errorf("%s contains an invalid path component", label)
		}
	}
	return nil
}

// DockerContainerMountState is the bounded projection of one daemon mount
// used to verify the read-only input and dedicated writable output boundary
// after a real container has been created.
type DockerContainerMountState struct {
	Target string
	Access string
}

func validDockerContainerMountAccess(value string) bool {
	return value == "ro" || value == "rw"
}

// VerifyDockerContainerMountIsolation checks the real inspection mounts:
// the dedicated output target must be the only writable mount inside the
// output tree, the input target tree must be read-only, and the workspace
// tree must be read-only. Daemon-managed mounts elsewhere stay untouched.
func VerifyDockerContainerMountIsolation(mounts []DockerContainerMountState,
	inputTarget, outputTarget, workspaceTarget string,
) error {
	if len(mounts) < 2 || len(mounts) > MaxMounts+8 ||
		validateVirtualPath("docker input mount target", inputTarget) != nil ||
		validateVirtualPath("docker output mount target", outputTarget) != nil ||
		validateVirtualPath("docker workspace mount target", workspaceTarget) != nil ||
		pathWithin(outputTarget, inputTarget) || pathWithin(outputTarget, workspaceTarget) {
		return errors.New("docker mount isolation request is invalid")
	}
	outputFound := false
	for _, mount := range mounts {
		if validateVirtualPath("docker mount target", mount.Target) != nil ||
			!validDockerContainerMountAccess(mount.Access) {
			return errors.New("docker mount state is invalid")
		}
		if mount.Target == outputTarget {
			if outputFound || mount.Access != "rw" {
				return errors.New("docker dedicated output mount is not unique and writable")
			}
			outputFound = true
			continue
		}
		if pathWithin(mount.Target, outputTarget) {
			return errors.New("docker writable mount escaped into the output tree")
		}
		if pathWithin(mount.Target, inputTarget) || pathWithin(mount.Target, workspaceTarget) {
			if mount.Access != "ro" {
				return errors.New("docker input or workspace mount is writable")
			}
		}
	}
	if !outputFound {
		return errors.New("docker dedicated output mount is missing")
	}
	return nil
}

// SortedDockerInputProjectionEntries returns a stable copy for receipts.
func SortedDockerInputProjectionEntries(projection DockerInputProjection) []DockerInputProjectionEntry {
	entries := append([]DockerInputProjectionEntry(nil), projection.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}
