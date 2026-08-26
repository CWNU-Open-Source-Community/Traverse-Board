// Package standardcodedelivery defines the public, durable completion contract
// for a Standard Code Run. The contract contains only bounded metadata and
// content digests; terminal output, process environments, private reasoning,
// and host filesystem roots are deliberately outside the model.
package standardcodedelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ProtocolVersion        = "standard_code_delivery.v1"
	MaxChangedFiles        = 2_000
	MaxVerifications       = 64
	MaxArtifactsPerCommand = 4
	MaxReasons             = 64
	MaxUncoveredItems      = 64
	MaxTextRunes           = 512
	MaxPathRunes           = 1_024
	MaxPayloadBytes        = 2 * 1024 * 1024
)

// Status is intentionally closed. In particular, there is no generic
// "success" value that callers can infer from Agent prose or a CI check name.
type Status string

const (
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	StatusPartial Status = "partial"
	StatusNotRun  Status = "not_run"
	StatusBlocked Status = "blocked"
	StatusStale   Status = "stale"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPassed, StatusFailed, StatusPartial, StatusNotRun,
		StatusBlocked, StatusStale:
		return true
	default:
		return false
	}
}

type Declaration string

const (
	DeclarationNone              Declaration = ""
	DeclarationNoApplicableTests Declaration = "no_applicable_tests"
	DeclarationUserSkipped       Declaration = "user_skipped"
	DeclarationBudgetExhausted   Declaration = "budget_exhausted"
	DeclarationMissingDependency Declaration = "missing_dependency"
	DeclarationApprovalDenied    Declaration = "approval_denied"
)

func (d Declaration) Valid() bool {
	switch d {
	case DeclarationNone, DeclarationNoApplicableTests, DeclarationUserSkipped,
		DeclarationBudgetExhausted, DeclarationMissingDependency,
		DeclarationApprovalDenied:
		return true
	default:
		return false
	}
}

const (
	ReasonPassed                             = "verification_passed"
	ReasonVerificationMissing                = "verification_missing"
	ReasonVerificationFailed                 = "verification_failed"
	ReasonWorkspaceModifiedAfterVerification = "workspace_modified_after_verification"
	ReasonOutputTruncated                    = "output_truncated"
	ReasonCommandCancelled                   = "command_cancelled"
	ReasonCommandTimedOut                    = "command_timed_out"
	ReasonCommandInterrupted                 = "command_interrupted"
	ReasonCommandNotTerminal                 = "command_not_terminal"
	ReasonApprovalDenied                     = "approval_denied"
	ReasonNoApplicableTests                  = "no_applicable_tests"
	ReasonUserSkipped                        = "user_skipped"
	ReasonBudgetExhausted                    = "budget_exhausted"
	ReasonMissingDependency                  = "missing_dependency"
	ReasonArtifactMissing                    = "command_artifact_missing"
	ReasonCheckpointIncomplete               = "checkpoint_incomplete"
	ReasonUncoveredItems                     = "uncovered_items"
	ReasonPermissionDrift                    = "permission_generation_drift"
	ReasonBackendDrift                       = "backend_generation_drift"
)

type Binding struct {
	RunID                      string `json:"run_id"`
	MissionID                  string `json:"mission_id"`
	SessionID                  string `json:"session_id"`
	SourceWorkspaceID          string `json:"source_workspace_id"`
	DrydockWorkspaceID         string `json:"drydock_workspace_id"`
	DrydockID                  string `json:"drydock_id"`
	DrydockGeneration          int64  `json:"drydock_generation"`
	PresetOperationSHA256      string `json:"preset_operation_sha256"`
	PermissionSnapshotID       string `json:"permission_snapshot_id"`
	PermissionRevision         int64  `json:"permission_revision"`
	Backend                    string `json:"backend"`
	BackendGenerationSHA256    string `json:"backend_generation_sha256"`
	CapabilityGenerationSHA256 string `json:"capability_generation_sha256"`
	SupervisorMutationEpoch    int    `json:"supervisor_mutation_epoch"`
}

type Checkpoint struct {
	ID                     string    `json:"id"`
	ManifestSHA256         string    `json:"manifest_sha256"`
	IndexSHA256            string    `json:"index_sha256"`
	RootFingerprint        string    `json:"root_fingerprint"`
	RootPathSHA256         string    `json:"root_path_sha256"`
	HeadCommit             string    `json:"head_commit"`
	BranchSHA256           string    `json:"branch_sha256"`
	RevisionSHA256         string    `json:"revision_sha256"`
	RecoveryLevel          string    `json:"recovery_level"`
	IncompleteReasonSHA256 []string  `json:"incomplete_reason_sha256"`
	CreatedAt              time.Time `json:"created_at"`
}

type ChangedFile struct {
	Path            string `json:"path,omitempty"`
	PathSHA256      string `json:"path_sha256"`
	Tracked         bool   `json:"tracked"`
	Committed       bool   `json:"committed"`
	IndexChanged    bool   `json:"index_changed"`
	WorktreeChanged bool   `json:"worktree_changed"`
	Untracked       bool   `json:"untracked"`
	Conflicted      bool   `json:"conflicted"`
	PathRedacted    bool   `json:"path_redacted"`
	FileURL         string `json:"file_url,omitempty"`
}

type Diff struct {
	SHA256         string        `json:"sha256"`
	Bytes          int           `json:"bytes"`
	ChangedCount   int           `json:"changed_count"`
	TrackedCount   int           `json:"tracked_count"`
	CommittedCount int           `json:"committed_count"`
	IndexCount     int           `json:"index_count"`
	WorktreeCount  int           `json:"worktree_count"`
	UntrackedCount int           `json:"untracked_count"`
	ConflictCount  int           `json:"conflict_count"`
	RedactedCount  int           `json:"redacted_count"`
	Files          []ChangedFile `json:"files"`
}

type Artifact struct {
	ID        string `json:"id"`
	Stream    string `json:"stream"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Redacted  bool   `json:"redacted"`
	URL       string `json:"url"`
}

type Verification struct {
	JobID                   string     `json:"job_id"`
	Conclusion              Status     `json:"conclusion"`
	ReasonCode              string     `json:"reason_code"`
	State                   string     `json:"state"`
	ExitCode                *int       `json:"exit_code,omitempty"`
	SpecSHA256              string     `json:"spec_sha256"`
	ExecutableSHA256        string     `json:"executable_sha256"`
	EnvironmentSHA256       string     `json:"environment_sha256"`
	PermissionRevision      int64      `json:"permission_revision"`
	Backend                 string     `json:"backend"`
	BackendGenerationSHA256 string     `json:"backend_generation_sha256"`
	CheckpointID            string     `json:"checkpoint_id"`
	RevisionSHA256          string     `json:"revision_sha256"`
	CurrentRevision         bool       `json:"current_revision"`
	RetryCount              int        `json:"retry_count"`
	StdoutSHA256            string     `json:"stdout_sha256"`
	StderrSHA256            string     `json:"stderr_sha256"`
	StdoutObservedBytes     int64      `json:"stdout_observed_bytes"`
	StderrObservedBytes     int64      `json:"stderr_observed_bytes"`
	OutputTruncated         bool       `json:"output_truncated"`
	TreeReaped              bool       `json:"tree_reaped"`
	Artifacts               []Artifact `json:"artifacts"`
	StartedAt               *time.Time `json:"started_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
}

type Reason struct {
	Code             string `json:"code"`
	ProvenanceSHA256 string `json:"provenance_sha256"`
}

type UncoveredItem struct {
	Summary       string `json:"summary"`
	SummarySHA256 string `json:"summary_sha256"`
}

type Links struct {
	Self               string `json:"self"`
	Checkpoint         string `json:"checkpoint"`
	CheckpointTimeline string `json:"checkpoint_timeline"`
	Undo               string `json:"undo"`
	Rewind             string `json:"rewind"`
	Fork               string `json:"fork"`
}

type Safeguards struct {
	AutomaticCommit        bool `json:"automatic_commit"`
	AutomaticPush          bool `json:"automatic_push"`
	AutomaticMerge         bool `json:"automatic_merge"`
	SourceOverwrite        bool `json:"source_overwrite"`
	RawEnvironmentStored   bool `json:"raw_environment_stored"`
	RawOutputStored        bool `json:"raw_output_stored"`
	PrivateReasoningStored bool `json:"private_reasoning_stored"`
	AbsolutePathsExposed   bool `json:"absolute_paths_exposed"`
}

// Observation is calculated at projection time. It is not part of the
// immutable receipt, so a later Workspace mutation cannot rewrite history.
type Observation struct {
	RevisionSHA256 string    `json:"revision_sha256,omitempty"`
	ReasonCode     string    `json:"reason_code,omitempty"`
	ObservedAt     time.Time `json:"observed_at,omitempty"`
}

type Report struct {
	ID                 string          `json:"id"`
	ProtocolVersion    string          `json:"protocol_version"`
	OperationKeySHA256 string          `json:"operation_key_sha256"`
	RequestFingerprint string          `json:"request_fingerprint"`
	Status             Status          `json:"status"`
	ReceiptStatus      Status          `json:"receipt_status"`
	Verified           bool            `json:"verified"`
	Declaration        Declaration     `json:"declaration,omitempty"`
	Binding            Binding         `json:"binding"`
	BaseCommit         string          `json:"base_commit"`
	HeadCommit         string          `json:"head_commit"`
	Diff               Diff            `json:"diff"`
	FinalCheckpoint    Checkpoint      `json:"final_checkpoint"`
	Verifications      []Verification  `json:"verifications"`
	Reasons            []Reason        `json:"reasons"`
	UncoveredItems     []UncoveredItem `json:"uncovered_items"`
	Links              Links           `json:"links"`
	Safeguards         Safeguards      `json:"safeguards"`
	ReceiptSHA256      string          `json:"receipt_sha256"`
	EventSequence      int64           `json:"event_sequence"`
	CreatedAt          time.Time       `json:"created_at"`
	Observation        Observation     `json:"observation,omitempty"`
}

type Evaluation struct {
	Declaration          Declaration
	Verifications        []Verification
	CheckpointIncomplete bool
	UncoveredCount       int
	RevisionStale        bool
}

// Evaluate computes the one authoritative conclusion from structural facts.
func Evaluate(input Evaluation) (Status, string) {
	if input.RevisionStale {
		return StatusStale, ReasonWorkspaceModifiedAfterVerification
	}
	switch input.Declaration {
	case DeclarationNoApplicableTests:
		return StatusNotRun, ReasonNoApplicableTests
	case DeclarationUserSkipped:
		return StatusNotRun, ReasonUserSkipped
	case DeclarationBudgetExhausted:
		return StatusBlocked, ReasonBudgetExhausted
	case DeclarationMissingDependency:
		return StatusBlocked, ReasonMissingDependency
	case DeclarationApprovalDenied:
		return StatusBlocked, ReasonApprovalDenied
	}
	if len(input.Verifications) == 0 {
		return StatusNotRun, ReasonVerificationMissing
	}
	passed, failed, blocked, partial := 0, 0, 0, 0
	for _, verification := range input.Verifications {
		switch verification.Conclusion {
		case StatusPassed:
			passed++
		case StatusFailed:
			failed++
		case StatusBlocked:
			blocked++
		case StatusPartial:
			partial++
		case StatusStale:
			return StatusStale, ReasonWorkspaceModifiedAfterVerification
		default:
			partial++
		}
	}
	if input.CheckpointIncomplete {
		return StatusPartial, ReasonCheckpointIncomplete
	}
	if input.UncoveredCount > 0 {
		return StatusPartial, ReasonUncoveredItems
	}
	if partial > 0 || (passed > 0 && (failed > 0 || blocked > 0)) {
		return StatusPartial, firstReason(input.Verifications, StatusPartial,
			ReasonVerificationFailed)
	}
	if blocked > 0 {
		return StatusBlocked, firstReason(input.Verifications, StatusBlocked,
			ReasonCommandInterrupted)
	}
	if failed > 0 {
		return StatusFailed, firstReason(input.Verifications, StatusFailed,
			ReasonVerificationFailed)
	}
	return StatusPassed, ReasonPassed
}

func firstReason(values []Verification, status Status, fallback string) string {
	for _, value := range values {
		if value.Conclusion == status && value.ReasonCode != "" {
			return value.ReasonCode
		}
	}
	return fallback
}

func (r Report) Seal(eventSequence int64) (Report, error) {
	r.EventSequence = eventSequence
	r.Status = r.ReceiptStatus
	r.Verified = r.Status == StatusPassed
	r.Observation = Observation{}
	r.ReceiptSHA256 = ""
	digest, err := r.receiptDigest()
	if err != nil {
		return Report{}, err
	}
	r.ReceiptSHA256 = digest
	if err := r.Validate(); err != nil {
		return Report{}, err
	}
	return r, nil
}

func (r Report) WithObservation(revisionSHA256, reason string,
	observedAt time.Time,
) Report {
	r.Observation = Observation{RevisionSHA256: revisionSHA256,
		ReasonCode: reason, ObservedAt: observedAt.UTC()}
	if revisionSHA256 == "" || revisionSHA256 != r.FinalCheckpoint.RevisionSHA256 {
		r.Status = StatusStale
		r.Verified = false
		if r.Observation.ReasonCode == "" {
			r.Observation.ReasonCode = ReasonWorkspaceModifiedAfterVerification
		}
	} else {
		r.Status = r.ReceiptStatus
		r.Verified = r.Status == StatusPassed
	}
	return r
}

func (r Report) Validate() error {
	if r.ProtocolVersion != ProtocolVersion || !r.Status.Valid() ||
		!r.ReceiptStatus.Valid() || !r.Declaration.Valid() ||
		r.Verified != (r.Status == StatusPassed) || r.EventSequence <= 0 ||
		r.CreatedAt.IsZero() || len(r.Verifications) > MaxVerifications ||
		len(r.Reasons) == 0 || len(r.Reasons) > MaxReasons ||
		len(r.UncoveredItems) > MaxUncoveredItems {
		return errors.New("Standard Code delivery envelope is invalid")
	}
	for _, id := range []string{r.ID, r.Binding.RunID, r.Binding.MissionID,
		r.Binding.SessionID, r.Binding.SourceWorkspaceID,
		r.Binding.DrydockWorkspaceID, r.Binding.DrydockID,
		r.Binding.PermissionSnapshotID, r.FinalCheckpoint.ID} {
		if !validIdentity(id) {
			return errors.New("Standard Code delivery identity is invalid")
		}
	}
	for _, digest := range []string{r.OperationKeySHA256, r.RequestFingerprint,
		r.Binding.PresetOperationSHA256, r.Binding.BackendGenerationSHA256,
		r.Binding.CapabilityGenerationSHA256, r.Diff.SHA256,
		r.FinalCheckpoint.ManifestSHA256, r.FinalCheckpoint.IndexSHA256,
		r.FinalCheckpoint.RootFingerprint, r.FinalCheckpoint.RootPathSHA256,
		r.FinalCheckpoint.BranchSHA256, r.FinalCheckpoint.RevisionSHA256,
		r.ReceiptSHA256} {
		if !validDigest(digest) {
			return errors.New("Standard Code delivery digest is invalid")
		}
	}
	if r.Binding.DrydockGeneration <= 0 || r.Binding.PermissionRevision <= 0 ||
		r.Binding.SupervisorMutationEpoch < 0 || r.Binding.Backend == "" ||
		!validText(r.Binding.Backend, 128, false) ||
		!validGitObject(r.BaseCommit) || !validGitObject(r.HeadCommit) ||
		r.FinalCheckpoint.HeadCommit != r.HeadCommit ||
		r.FinalCheckpoint.CreatedAt.IsZero() ||
		!validRecoveryLevel(r.FinalCheckpoint.RecoveryLevel) ||
		(r.FinalCheckpoint.RecoveryLevel == "complete" &&
			len(r.FinalCheckpoint.IncompleteReasonSHA256) != 0) ||
		(r.FinalCheckpoint.RecoveryLevel != "complete" &&
			len(r.FinalCheckpoint.IncompleteReasonSHA256) == 0) ||
		r.Diff.Bytes < 0 || r.Diff.ChangedCount != len(r.Diff.Files) ||
		len(r.Diff.Files) > MaxChangedFiles ||
		len(r.FinalCheckpoint.IncompleteReasonSHA256) > MaxReasons {
		return errors.New("Standard Code delivery binding, Git, or checkpoint is invalid")
	}
	for _, digest := range r.FinalCheckpoint.IncompleteReasonSHA256 {
		if !validDigest(digest) {
			return errors.New("Standard Code delivery checkpoint reason digest is invalid")
		}
	}
	if err := validateFiles(r.Diff); err != nil {
		return err
	}
	seenJobs := map[string]struct{}{}
	if r.Declaration != DeclarationNone && len(r.Verifications) != 0 {
		return errors.New("Standard Code delivery declaration cannot contain verification Jobs")
	}
	for _, verification := range r.Verifications {
		if err := verification.Validate(); err != nil {
			return err
		}
		if _, exists := seenJobs[verification.JobID]; exists {
			return errors.New("Standard Code delivery verification Job is duplicated")
		}
		seenJobs[verification.JobID] = struct{}{}
	}
	for _, reason := range r.Reasons {
		if !validText(reason.Code, 128, false) || !validDigest(reason.ProvenanceSHA256) {
			return errors.New("Standard Code delivery reason is invalid")
		}
	}
	for _, item := range r.UncoveredItems {
		if !validText(item.Summary, MaxTextRunes, false) ||
			!publicSummary(item.Summary) || !validDigest(item.SummarySHA256) ||
			Hash(item.Summary) != item.SummarySHA256 {
			return errors.New("Standard Code delivery uncovered item is invalid")
		}
	}
	for _, link := range []string{r.Links.Self, r.Links.Checkpoint,
		r.Links.CheckpointTimeline, r.Links.Undo, r.Links.Rewind, r.Links.Fork} {
		if !validPublicLink(link) {
			return errors.New("Standard Code delivery link is invalid")
		}
	}
	if r.Safeguards != (Safeguards{}) {
		return errors.New("Standard Code delivery cannot grant mutation or retain private material")
	}
	expectedStatus, expectedReason := Evaluate(Evaluation{Declaration: r.Declaration,
		Verifications:        r.Verifications,
		CheckpointIncomplete: r.FinalCheckpoint.RecoveryLevel != "complete",
		UncoveredCount:       len(r.UncoveredItems)})
	if r.ReceiptStatus != expectedStatus {
		return errors.New("Standard Code delivery receipt status is inconsistent with evidence")
	}
	expectedReasons := []Reason{ReasonFact(expectedReason,
		r.FinalCheckpoint.RevisionSHA256, r.Diff.SHA256, r.RequestFingerprint)}
	seenReasonCodes := map[string]struct{}{expectedReason: {}}
	for _, verification := range r.Verifications {
		if _, seen := seenReasonCodes[verification.ReasonCode]; seen {
			continue
		}
		seenReasonCodes[verification.ReasonCode] = struct{}{}
		expectedReasons = append(expectedReasons, ReasonFact(verification.ReasonCode,
			verification.JobID, verification.SpecSHA256, verification.RevisionSHA256))
	}
	if len(r.Reasons) != len(expectedReasons) {
		return errors.New("Standard Code delivery reason provenance is incomplete")
	}
	for index := range expectedReasons {
		if r.Reasons[index] != expectedReasons[index] {
			return errors.New("Standard Code delivery reason provenance is inconsistent")
		}
	}
	if r.Observation != (Observation{}) {
		if r.Observation.ObservedAt.IsZero() ||
			(r.Observation.RevisionSHA256 != "" && !validDigest(r.Observation.RevisionSHA256)) ||
			!validText(r.Observation.ReasonCode, 128, true) {
			return errors.New("Standard Code delivery observation is invalid")
		}
		current := r.Observation.RevisionSHA256 != "" &&
			r.Observation.RevisionSHA256 == r.FinalCheckpoint.RevisionSHA256
		if (!current && r.Status != StatusStale) ||
			(current && r.Status != r.ReceiptStatus) ||
			(current && r.Observation.ReasonCode != "") ||
			(!current && r.Observation.ReasonCode == "") {
			return errors.New("Standard Code delivery freshness projection is inconsistent")
		}
	} else if r.Status != r.ReceiptStatus {
		return errors.New("Standard Code delivery status changed without an observation")
	}
	digest, err := r.receiptDigest()
	if err != nil || digest != r.ReceiptSHA256 {
		return errors.New("Standard Code delivery receipt digest does not match")
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxPayloadBytes {
		return errors.New("Standard Code delivery payload exceeds its bound")
	}
	return nil
}

func (v Verification) Validate() error {
	if !validIdentity(v.JobID) || !v.Conclusion.Valid() ||
		v.Conclusion == StatusNotRun || !validText(v.ReasonCode, 128, false) ||
		!validVerificationState(v.State) || !validDigest(v.SpecSHA256) ||
		!validDigest(v.ExecutableSHA256) || !validDigest(v.EnvironmentSHA256) ||
		v.PermissionRevision <= 0 || !validText(v.Backend, 128, false) ||
		!validDigest(v.BackendGenerationSHA256) || !validIdentity(v.CheckpointID) ||
		!validDigest(v.RevisionSHA256) || v.RetryCount < 0 ||
		v.StdoutObservedBytes < 0 || v.StderrObservedBytes < 0 ||
		len(v.Artifacts) > MaxArtifactsPerCommand {
		return errors.New("Standard Code delivery verification is invalid")
	}
	for _, digest := range []string{v.StdoutSHA256, v.StderrSHA256} {
		if !validDigest(digest) {
			return errors.New("Standard Code delivery output digest is invalid")
		}
	}
	if v.StartedAt != nil && v.StartedAt.IsZero() ||
		v.CompletedAt != nil && v.CompletedAt.IsZero() ||
		v.CompletedAt != nil && v.StartedAt == nil ||
		v.CompletedAt != nil && v.CompletedAt.Before(*v.StartedAt) ||
		verificationStateTerminal(v.State) && v.CompletedAt == nil {
		return errors.New("Standard Code delivery verification timestamps are invalid")
	}
	stdoutFound := v.StdoutObservedBytes == 0
	stderrFound := v.StderrObservedBytes == 0
	seenArtifacts := make(map[string]struct{}, len(v.Artifacts))
	for _, artifact := range v.Artifacts {
		if !validIdentity(artifact.ID) || !validText(artifact.Stream, 32, false) ||
			!validDigest(artifact.SHA256) || artifact.SizeBytes <= 0 ||
			!validPublicLink(artifact.URL) {
			return errors.New("Standard Code delivery Artifact reference is invalid")
		}
		if _, exists := seenArtifacts[artifact.ID]; exists {
			return errors.New("Standard Code delivery Artifact reference is duplicated")
		}
		seenArtifacts[artifact.ID] = struct{}{}
		switch artifact.Stream {
		case "stdout":
			if artifact.SHA256 != v.StdoutSHA256 {
				return errors.New("Standard Code delivery stdout Artifact digest is inconsistent")
			}
			stdoutFound = true
		case "stderr":
			if artifact.SHA256 != v.StderrSHA256 {
				return errors.New("Standard Code delivery stderr Artifact digest is inconsistent")
			}
			stderrFound = true
		default:
			return errors.New("Standard Code delivery Artifact stream is invalid")
		}
	}
	artifactsComplete := stdoutFound && stderrFound
	if v.Conclusion == StatusPassed && (v.State != "completed" || v.ExitCode == nil ||
		*v.ExitCode != 0 || !v.TreeReaped || !v.CurrentRevision ||
		v.OutputTruncated || !artifactsComplete) {
		return errors.New("passed Standard Code verification lacks terminal current evidence")
	}
	if v.OutputTruncated && v.Conclusion != StatusPartial && v.Conclusion != StatusStale {
		return errors.New("truncated Standard Code verification must be partial or stale")
	}
	if !artifactsComplete && v.Conclusion != StatusPartial && v.Conclusion != StatusStale {
		return errors.New("incomplete Standard Code Artifact evidence must be partial or stale")
	}
	if !validVerificationReason(v.Conclusion, v.ReasonCode) {
		return errors.New("Standard Code delivery verification reason is inconsistent")
	}
	return nil
}

func validateFiles(diff Diff) error {
	seen := map[string]struct{}{}
	counts := struct{ tracked, committed, index, worktree, untracked, conflicted, redacted int }{}
	for _, file := range diff.Files {
		if !validDigest(file.PathSHA256) || file.PathRedacted != (file.Path == "") ||
			(file.Path == "" && file.FileURL != "") {
			return errors.New("Standard Code delivery affected file identity is invalid")
		}
		if file.Path != "" {
			if !validRelativePath(file.Path) || Hash(file.Path) != file.PathSHA256 ||
				!validPublicLink(file.FileURL) {
				return errors.New("Standard Code delivery affected path is invalid")
			}
		}
		if _, exists := seen[file.PathSHA256]; exists {
			return errors.New("Standard Code delivery affected file is duplicated")
		}
		seen[file.PathSHA256] = struct{}{}
		if file.Tracked {
			counts.tracked++
		}
		if file.Committed {
			counts.committed++
		}
		if file.IndexChanged {
			counts.index++
		}
		if file.WorktreeChanged {
			counts.worktree++
		}
		if file.Untracked {
			counts.untracked++
		}
		if file.Conflicted {
			counts.conflicted++
		}
		if file.PathRedacted {
			counts.redacted++
		}
	}
	if diff.TrackedCount != counts.tracked || diff.CommittedCount != counts.committed ||
		diff.IndexCount != counts.index || diff.WorktreeCount != counts.worktree ||
		diff.UntrackedCount != counts.untracked || diff.ConflictCount != counts.conflicted ||
		diff.RedactedCount != counts.redacted {
		return errors.New("Standard Code delivery Diff counts are inconsistent")
	}
	return nil
}

func (r Report) receiptDigest() (string, error) {
	canonical := r
	canonical.ReceiptSHA256 = ""
	canonical.Status = canonical.ReceiptStatus
	canonical.Verified = canonical.ReceiptStatus == StatusPassed
	canonical.Observation = Observation{}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return HashBytes(encoded), nil
}

func Hash(value string) string { return HashBytes([]byte(value)) }

func HashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func RevisionSHA256(manifest, index, root, rootPath, head, branch string) string {
	return Hash(strings.Join([]string{"standard-code-workspace-revision.v1", manifest,
		index, root, rootPath, head, Hash(branch)}, "\x00"))
}

func ReasonFact(code string, provenance ...string) Reason {
	return Reason{Code: code, ProvenanceSHA256: Hash(strings.Join(
		append([]string{"standard-code-delivery-reason.v1", code}, provenance...), "\x00"))}
}

func validIdentity(value string) bool {
	return validText(value, 256, false) && !strings.ContainsAny(value, `/\\:`)
}

func validText(value string, maxRunes int, allowEmpty bool) bool {
	if (!allowEmpty && value == "") || value != strings.TrimSpace(value) ||
		!utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes ||
		strings.ContainsRune(value, 0) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

var (
	redactedPublicMarker = regexp.MustCompile(`\[REDACTED:[A-Za-z0-9_-]+\]`)
	secretLikePublicText = regexp.MustCompile(`(?i)(?:\bsk-[A-Za-z0-9_-]{16,}\b|\b(?:api|access|auth|secret|token|password)[_-]?(?:key|token|secret)?\s*[:=]\s*\S+)`)
	privateHostPathText  = regexp.MustCompile(`(?i)(?:^|[\s"'(=])(?:[a-z]:[\\/][^\s"'<>]*|\\\\[^\\/\s"'<>]+[\\/][^\s"'<>]*|/[^\s"'<>]+)`)
)

func publicSummary(value string) bool {
	withoutMarkers := redactedPublicMarker.ReplaceAllString(value, "")
	return !secretLikePublicText.MatchString(withoutMarkers) &&
		!privateHostPathText.MatchString(value)
}

func validRecoveryLevel(value string) bool {
	return value == "complete" || value == "partial" || value == "unavailable"
}

func validVerificationState(value string) bool {
	switch value {
	case "prepared", "running", "stopping", "completed", "failed", "timed_out",
		"cancelled", "killed", "interrupted":
		return true
	default:
		return false
	}
}

func verificationStateTerminal(value string) bool {
	switch value {
	case "completed", "failed", "timed_out", "cancelled", "killed", "interrupted":
		return true
	default:
		return false
	}
}

func validVerificationReason(status Status, reason string) bool {
	switch status {
	case StatusPassed:
		return reason == ReasonPassed
	case StatusFailed:
		return reason == ReasonVerificationFailed
	case StatusPartial:
		return reason == ReasonOutputTruncated || reason == ReasonArtifactMissing
	case StatusBlocked:
		return reason == ReasonCommandCancelled || reason == ReasonCommandTimedOut ||
			reason == ReasonCommandInterrupted || reason == ReasonCommandNotTerminal
	case StatusStale:
		return reason == ReasonWorkspaceModifiedAfterVerification ||
			reason == ReasonPermissionDrift || reason == ReasonBackendDrift
	default:
		return false
	}
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validGitObject(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == len(value)
}

func validRelativePath(value string) bool {
	if !validText(value, MaxPathRunes, false) || strings.ContainsRune(value, '\\') ||
		strings.ContainsRune(value, ':') || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." &&
		!strings.HasPrefix(clean, "../")
}

func validPublicLink(value string) bool {
	if !validText(value, 2_048, false) || !strings.HasPrefix(value, "/api/v1/") ||
		strings.Contains(value, "..") || strings.ContainsAny(value, "\r\n\\") {
		return false
	}
	return true
}

func MustReason(code string, provenance ...string) Reason {
	value := ReasonFact(code, provenance...)
	if !validText(value.Code, 128, false) {
		panic(fmt.Sprintf("invalid Standard Code delivery reason %q", code))
	}
	return value
}
