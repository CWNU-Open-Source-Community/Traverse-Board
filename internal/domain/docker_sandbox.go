package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DockerSandboxAdmissionProtocolVersion    = "docker_sandbox_admission.v1"
	DockerSandboxDenialProtocolVersion       = "docker_sandbox_denial.v1"
	DockerSandboxStartProtocolVersion        = "docker_sandbox_start.v1"
	DockerSandboxLaunchProtocolVersion       = "docker_sandbox_launch.v1"
	DockerSandboxCancellationProtocolVersion = "docker_sandbox_cancellation.v1"
	DockerSandboxReceiptProtocolVersion      = "docker_sandbox_receipt.v1"

	DockerSandboxAdmissionAuthorized = "authorized"
	DockerSandboxAdmissionDenied     = "denied"

	DockerSandboxOutcomeSucceeded = "succeeded"
	DockerSandboxOutcomeTimedOut  = "timed_out"
	DockerSandboxOutcomeCancelled = "cancelled"
	DockerSandboxOutcomeFailed    = "failed"

	DockerSandboxReasonReady                    = "ready"
	DockerSandboxReasonFeatureDisabled          = "feature_disabled"
	DockerSandboxReasonDaemonUnreachable        = "daemon_unreachable"
	DockerSandboxReasonAPIUnsupported           = "api_unsupported"
	DockerSandboxReasonPlatformUnsupported      = "platform_unsupported"
	DockerSandboxReasonResourceUnavailable      = "resource_unavailable"
	DockerSandboxReasonManagedEgressUnavailable = "managed_egress_unavailable"
	DockerSandboxReasonPolicyDenied             = "policy_denied"
	DockerSandboxReasonApprovalRequired         = "approval_required"
	DockerSandboxReasonPermissionDenied         = "permission_denied"
	DockerSandboxReasonBudgetExhausted          = "budget_exhausted"
	DockerSandboxReasonAuthorityChanged         = "authority_changed"
	DockerSandboxReasonCancelled                = "cancelled"
	DockerSandboxReasonTimedOut                 = "timed_out"
	DockerSandboxReasonDaemonDisconnected       = "daemon_disconnected"
	DockerSandboxReasonProcessFailed            = "process_failed"
	DockerSandboxReasonIOFailed                 = "io_failed"
	DockerSandboxReasonCleanupFailed            = "cleanup_failed"
	DockerSandboxReasonCompleted                = "completed"

	DockerSandboxRemediationNone                = "none"
	DockerSandboxRemediationEnableFeature       = "enable_docker_execution"
	DockerSandboxRemediationStartDocker         = "start_local_docker"
	DockerSandboxRemediationUpdateDocker        = "update_local_docker"
	DockerSandboxRemediationUseLinuxContainers  = "use_linux_containers"
	DockerSandboxRemediationDisableNetwork      = "use_network_none"
	DockerSandboxRemediationReviewPolicy        = "review_policy"
	DockerSandboxRemediationApproveExactRequest = "approve_exact_request"
	DockerSandboxRemediationSelectDockerProfile = "select_docker_profile"
	DockerSandboxRemediationRestoreBudget       = "increase_or_free_budget"
	DockerSandboxRemediationRetryFreshRequest   = "retry_with_fresh_request"
	DockerSandboxRemediationInspectCleanup      = "inspect_cleanup_failure"
	DockerSandboxRemediationCorrectRequest      = "correct_sandbox_request"
	DockerSandboxRemediationEnablePIDsLimit     = "enable_pids_limit"
	DockerSandboxRemediationReduceResources     = "reduce_resource_limits"
	DockerSandboxRemediationProvideImage        = "provide_compatible_image"

	MaxDockerSandboxManifestBytes = 64 * 1024
	MaxDockerSandboxLogBytes      = 256 * 1024
	MaxDockerSandboxLogLines      = 4096
	MaxDockerSandboxDiskBytes     = 16 * 1024 * 1024
	MaxDockerSandboxToolCalls     = 100
)

// DockerSandboxDenial is immutable, metadata-only evidence that one exact
// product admission operation failed closed. It carries no launch, artifact,
// or cleanup authority and exists so budget/policy/readiness failures remain
// stable and auditable across retries.
type DockerSandboxDenial struct {
	ProtocolVersion    string
	OperationKeyDigest string
	RunID              string
	MissionID          string
	WorkspaceID        string
	PlanID             string
	RequestedBy        string
	ReasonCode         string
	RemediationCode    string
	NetworkMode        string
	DenialFingerprint  string
	CreatedAt          time.Time
}

func (value DockerSandboxDenial) Validate() error {
	for _, current := range []string{value.RunID, value.MissionID, value.WorkspaceID,
		value.PlanID, value.RequestedBy} {
		if !ValidAgentID(current) || strings.ContainsRune(current, 0) {
			return errors.New("Docker Sandbox denial identity is invalid")
		}
	}
	if value.ProtocolVersion != DockerSandboxDenialProtocolVersion ||
		!validLowerHexDigest(value.OperationKeyDigest) || value.CreatedAt.IsZero() ||
		value.NetworkMode != "disabled" || value.ReasonCode == DockerSandboxReasonReady ||
		!validDockerSandboxReason(value.ReasonCode) ||
		value.RemediationCode == DockerSandboxRemediationNone ||
		!validDockerSandboxRemediation(value.RemediationCode) ||
		!validLowerHexDigest(value.DenialFingerprint) ||
		value.DenialFingerprint != DockerSandboxDenialFingerprint(value) {
		return errors.New("Docker Sandbox denial is invalid")
	}
	return nil
}

func DockerSandboxDenialFingerprint(value DockerSandboxDenial) string {
	return digestParts(DockerSandboxDenialProtocolVersion, value.OperationKeyDigest,
		value.RunID, value.MissionID, value.WorkspaceID, value.PlanID,
		value.RequestedBy, value.ReasonCode, value.RemediationCode,
		value.NetworkMode, value.CreatedAt.UTC().Format(time.RFC3339Nano))
}

// DockerSandboxAdmission is immutable evidence that one exact product request
// passed every current Go-owned gate. It is not a bearer capability. The
// process epoch is stored only as a digest and must be matched against a fresh
// in-memory capability before any start write.
type DockerSandboxAdmission struct {
	ID                       string
	ProtocolVersion          string
	OperationKeyDigest       string
	RequestFingerprint       string
	LifecycleOperationDigest string
	RunID                    string
	MissionID                string
	WorkspaceID              string
	PlanID                   string
	CandidateID              string
	PreparationID            string
	ManifestJSON             string
	ManifestFingerprint      string
	PlanFingerprint          string
	SpecFingerprint          string
	AuthorityFingerprint     string
	ReadinessFingerprint     string
	ReadinessExpiresAt       time.Time
	RuntimeEpochFingerprint  string
	ProfileSnapshotID        string
	ProfileRevision          int64
	PermissionSnapshotID     string
	PermissionRevision       int64
	PermissionMode           RunExecutionPermissionMode
	ApprovalID               string
	ApprovalVersion          int64
	PolicyFingerprint        string
	NetworkMode              string
	NetworkTargetCount       int
	CPUQuotaMillis           int
	MemoryBytes              int64
	PIDs                     int
	DiskBytes                int64
	WallClockSeconds         int
	LogBytes                 int64
	LogLines                 int
	ToolCallsRemaining       int64
	Decision                 string
	ReasonCode               string
	RemediationCode          string
	ProductEntryEnabled      bool
	ExecutionAuthorized      bool
	ArtifactCommitAuthorized bool
	RequestedBy              string
	CreatedAt                time.Time
	AdmissionFingerprint     string
}

func (value DockerSandboxAdmission) Validate() error {
	for label, current := range map[string]string{
		"id": value.ID, "Run id": value.RunID, "Mission id": value.MissionID,
		"Workspace id": value.WorkspaceID, "plan id": value.PlanID,
		"candidate id": value.CandidateID, "preparation id": value.PreparationID,
		"profile snapshot id":    value.ProfileSnapshotID,
		"permission snapshot id": value.PermissionSnapshotID,
		"requester":              value.RequestedBy,
	} {
		if !ValidAgentID(current) || strings.ContainsRune(current, 0) {
			return fmt.Errorf("Docker Sandbox admission %s is invalid", label)
		}
	}
	for _, digest := range []string{
		value.OperationKeyDigest, value.RequestFingerprint,
		value.LifecycleOperationDigest, value.ManifestFingerprint,
		value.PlanFingerprint, value.SpecFingerprint, value.AuthorityFingerprint,
		value.ReadinessFingerprint, value.RuntimeEpochFingerprint,
		value.PolicyFingerprint, value.AdmissionFingerprint,
	} {
		if !validLowerHexDigest(digest) {
			return errors.New("Docker Sandbox admission fingerprint is invalid")
		}
	}
	if value.ProtocolVersion != DockerSandboxAdmissionProtocolVersion ||
		value.ProfileRevision < 1 || value.PermissionRevision < 1 ||
		value.ApprovalVersion < 1 || value.PermissionMode == RunExecutionPermissionConservative ||
		!value.PermissionMode.Valid() || value.ApprovalID == "" ||
		value.NetworkMode != "disabled" || value.NetworkTargetCount != 0 ||
		value.CPUQuotaMillis < 1 || value.CPUQuotaMillis > 8_000 ||
		value.MemoryBytes < 16*1024*1024 || value.MemoryBytes > 8*1024*1024*1024 ||
		value.PIDs < 1 || value.PIDs > 512 || value.DiskBytes < 1 ||
		value.DiskBytes > MaxDockerSandboxDiskBytes || value.WallClockSeconds < 1 ||
		value.WallClockSeconds > 3_600 || value.LogBytes < 1 ||
		value.LogBytes > MaxDockerSandboxLogBytes || value.LogLines < 1 ||
		value.LogLines > MaxDockerSandboxLogLines || value.ToolCallsRemaining < 1 ||
		value.ToolCallsRemaining > MaxDockerSandboxToolCalls ||
		value.CreatedAt.IsZero() || !value.ReadinessExpiresAt.After(value.CreatedAt) {
		return errors.New("Docker Sandbox admission is outside fixed product bounds")
	}
	if !utf8.ValidString(value.ManifestJSON) || len(value.ManifestJSON) == 0 ||
		len(value.ManifestJSON) > MaxDockerSandboxManifestBytes ||
		!json.Valid([]byte(value.ManifestJSON)) ||
		digestParts("sandbox_manifest.v1", value.ManifestJSON) != value.ManifestFingerprint {
		return errors.New("Docker Sandbox admission Manifest is invalid")
	}
	if value.Decision == DockerSandboxAdmissionAuthorized {
		if value.ReasonCode != DockerSandboxReasonReady ||
			value.RemediationCode != DockerSandboxRemediationNone ||
			!value.ProductEntryEnabled || !value.ExecutionAuthorized ||
			!value.ArtifactCommitAuthorized {
			return errors.New("authorized Docker Sandbox admission flags are invalid")
		}
	} else if value.Decision == DockerSandboxAdmissionDenied {
		if value.ReasonCode == DockerSandboxReasonReady ||
			value.RemediationCode == DockerSandboxRemediationNone ||
			value.ProductEntryEnabled || value.ExecutionAuthorized ||
			value.ArtifactCommitAuthorized {
			return errors.New("denied Docker Sandbox admission flags are invalid")
		}
	} else {
		return errors.New("Docker Sandbox admission decision is invalid")
	}
	if !validDockerSandboxReason(value.ReasonCode) ||
		!validDockerSandboxRemediation(value.RemediationCode) ||
		value.AdmissionFingerprint != DockerSandboxAdmissionFingerprint(value) {
		return errors.New("Docker Sandbox admission decision or aggregate fingerprint is invalid")
	}
	return nil
}

func DockerSandboxAdmissionFingerprint(value DockerSandboxAdmission) string {
	return digestParts(DockerSandboxAdmissionProtocolVersion, value.ID,
		value.OperationKeyDigest, value.RequestFingerprint,
		value.LifecycleOperationDigest, value.RunID, value.MissionID,
		value.WorkspaceID, value.PlanID, value.CandidateID, value.PreparationID,
		value.ManifestJSON, value.ManifestFingerprint, value.PlanFingerprint,
		value.SpecFingerprint, value.AuthorityFingerprint,
		value.ReadinessFingerprint, value.ReadinessExpiresAt.UTC().Format(time.RFC3339Nano),
		value.RuntimeEpochFingerprint, value.ProfileSnapshotID,
		fmt.Sprint(value.ProfileRevision), value.PermissionSnapshotID,
		fmt.Sprint(value.PermissionRevision), string(value.PermissionMode),
		value.ApprovalID, fmt.Sprint(value.ApprovalVersion), value.PolicyFingerprint,
		value.NetworkMode, fmt.Sprint(value.NetworkTargetCount),
		fmt.Sprint(value.CPUQuotaMillis), fmt.Sprint(value.MemoryBytes),
		fmt.Sprint(value.PIDs), fmt.Sprint(value.DiskBytes),
		fmt.Sprint(value.WallClockSeconds), fmt.Sprint(value.LogBytes),
		fmt.Sprint(value.LogLines), fmt.Sprint(value.ToolCallsRemaining),
		value.Decision, value.ReasonCode, value.RemediationCode,
		fmt.Sprint(value.ProductEntryEnabled), fmt.Sprint(value.ExecutionAuthorized),
		fmt.Sprint(value.ArtifactCommitAuthorized), value.RequestedBy,
		value.CreatedAt.UTC().Format(time.RFC3339Nano))
}

// DockerSandboxStartIntent is the append-only write-ahead record for one
// exact request to start an authorized admission. It is not sufficient start
// authority: the process-local runtime epoch and every current gate are still
// rechecked immediately before Docker create/start writes.
type DockerSandboxStartIntent struct {
	AdmissionID             string
	ProtocolVersion         string
	OperationKeyDigest      string
	RequestFingerprint      string
	RuntimeEpochFingerprint string
	RunID                   string
	RequestedBy             string
	CreatedAt               time.Time
	StartFingerprint        string
}

func (value DockerSandboxStartIntent) Validate() error {
	for _, current := range []string{value.AdmissionID, value.RunID,
		value.RequestedBy} {
		if !ValidAgentID(current) || strings.ContainsRune(current, 0) {
			return errors.New("Docker Sandbox start identity is invalid")
		}
	}
	if value.ProtocolVersion != DockerSandboxStartProtocolVersion ||
		value.CreatedAt.IsZero() ||
		!validLowerHexDigest(value.OperationKeyDigest) ||
		!validLowerHexDigest(value.RequestFingerprint) ||
		!validLowerHexDigest(value.RuntimeEpochFingerprint) ||
		!validLowerHexDigest(value.StartFingerprint) ||
		value.StartFingerprint != DockerSandboxStartFingerprint(value) {
		return errors.New("Docker Sandbox start intent is invalid")
	}
	return nil
}

func DockerSandboxStartFingerprint(value DockerSandboxStartIntent) string {
	return digestParts(DockerSandboxStartProtocolVersion, value.AdmissionID,
		value.OperationKeyDigest, value.RequestFingerprint,
		value.RuntimeEpochFingerprint, value.RunID, value.RequestedBy,
		value.CreatedAt.UTC().Format(time.RFC3339Nano))
}

type DockerSandboxLaunch struct {
	AdmissionID                 string
	ProtocolVersion             string
	StartOperationKeyDigest     string
	LifecycleIntentID           string
	LifecycleRequestFingerprint string
	AttemptID                   string
	RunID                       string
	LaunchFingerprint           string
	CreatedAt                   time.Time
}

func (value DockerSandboxLaunch) Validate() error {
	for _, current := range []string{value.AdmissionID, value.LifecycleIntentID,
		value.AttemptID, value.RunID} {
		if !ValidAgentID(current) || strings.ContainsRune(current, 0) {
			return errors.New("Docker Sandbox launch identity is invalid")
		}
	}
	if value.ProtocolVersion != DockerSandboxLaunchProtocolVersion ||
		value.CreatedAt.IsZero() ||
		(value.StartOperationKeyDigest != "" &&
			!validLowerHexDigest(value.StartOperationKeyDigest)) ||
		!validLowerHexDigest(value.LifecycleRequestFingerprint) ||
		!validLowerHexDigest(value.LaunchFingerprint) ||
		value.LaunchFingerprint != DockerSandboxLaunchFingerprint(value) {
		return errors.New("Docker Sandbox launch is invalid")
	}
	return nil
}

func DockerSandboxLaunchFingerprint(value DockerSandboxLaunch) string {
	return digestParts(DockerSandboxLaunchProtocolVersion, value.AdmissionID,
		value.StartOperationKeyDigest,
		value.LifecycleIntentID, value.LifecycleRequestFingerprint,
		value.AttemptID, value.RunID,
		value.CreatedAt.UTC().Format(time.RFC3339Nano))
}

// DockerSandboxCancellation is immutable, append-only evidence that an exact
// product attempt must converge to the cancelled terminal outcome. It grants
// no start authority; after restart it only authorizes taking the cleanup path
// for the already-bound lifecycle intent.
type DockerSandboxCancellation struct {
	ID                      string
	AdmissionID             string
	ProtocolVersion         string
	RunID                   string
	RequestedBy             string
	OperationKeyDigest      string
	ReasonCode              string
	CancellationFingerprint string
	RequestedAt             time.Time
}

func (value DockerSandboxCancellation) Validate() error {
	for _, current := range []string{value.ID, value.AdmissionID, value.RunID,
		value.RequestedBy} {
		if !ValidAgentID(current) || strings.ContainsRune(current, 0) {
			return errors.New("Docker Sandbox cancellation identity is invalid")
		}
	}
	if value.ProtocolVersion != DockerSandboxCancellationProtocolVersion ||
		value.ReasonCode != DockerSandboxReasonCancelled || value.RequestedAt.IsZero() ||
		!validLowerHexDigest(value.OperationKeyDigest) ||
		!validLowerHexDigest(value.CancellationFingerprint) ||
		value.CancellationFingerprint != DockerSandboxCancellationFingerprint(value) {
		return errors.New("Docker Sandbox cancellation is invalid")
	}
	return nil
}

func DockerSandboxCancellationFingerprint(value DockerSandboxCancellation) string {
	return digestParts(DockerSandboxCancellationProtocolVersion, value.ID,
		value.AdmissionID, value.RunID, value.RequestedBy, value.OperationKeyDigest,
		value.ReasonCode, value.RequestedAt.UTC().Format(time.RFC3339Nano))
}

type DockerSandboxReceipt struct {
	ID                       string
	AdmissionID              string
	ProtocolVersion          string
	LifecycleIntentID        string
	AttemptID                string
	RunID                    string
	WorkspaceID              string
	Outcome                  string
	ReasonCode               string
	ExitCode                 *int
	LogReceiptID             string
	OutputStagingReceiptID   string
	OutputCommitReceiptID    string
	ArtifactCount            int
	CleanupComplete          bool
	ArtifactCommitAuthorized bool
	ReceiptFingerprint       string
	CompletedAt              time.Time
}

func (value DockerSandboxReceipt) Validate() error {
	for _, current := range []string{value.ID, value.AdmissionID,
		value.LifecycleIntentID, value.AttemptID, value.RunID, value.WorkspaceID} {
		if !ValidAgentID(current) || strings.ContainsRune(current, 0) {
			return errors.New("Docker Sandbox receipt identity is invalid")
		}
	}
	if value.ProtocolVersion != DockerSandboxReceiptProtocolVersion ||
		!validDockerSandboxOutcome(value.Outcome) ||
		!validDockerSandboxReason(value.ReasonCode) || value.CompletedAt.IsZero() ||
		!value.CleanupComplete || value.ArtifactCount < 0 || value.ArtifactCount > 64 ||
		(value.ExitCode != nil && (*value.ExitCode < 0 || *value.ExitCode > 255)) ||
		!validOptionalAgentID(value.LogReceiptID) ||
		!validOptionalAgentID(value.OutputStagingReceiptID) ||
		!validOptionalAgentID(value.OutputCommitReceiptID) ||
		(value.OutputCommitReceiptID != "" && !value.ArtifactCommitAuthorized) ||
		(value.OutputCommitReceiptID == "" && value.ArtifactCount != 0) ||
		(value.OutputCommitReceiptID != "" && value.ArtifactCount == 0) ||
		(value.Outcome != DockerSandboxOutcomeSucceeded &&
			value.OutputCommitReceiptID != "") ||
		!validLowerHexDigest(value.ReceiptFingerprint) ||
		value.ReceiptFingerprint != DockerSandboxReceiptFingerprint(value) {
		return errors.New("Docker Sandbox receipt is invalid")
	}
	switch value.Outcome {
	case DockerSandboxOutcomeSucceeded:
		if value.ReasonCode != DockerSandboxReasonCompleted {
			return errors.New("successful Docker Sandbox receipt reason is invalid")
		}
	case DockerSandboxOutcomeTimedOut:
		if value.ReasonCode != DockerSandboxReasonTimedOut {
			return errors.New("timed-out Docker Sandbox receipt reason is invalid")
		}
	case DockerSandboxOutcomeCancelled:
		if value.ReasonCode != DockerSandboxReasonCancelled {
			return errors.New("cancelled Docker Sandbox receipt reason is invalid")
		}
	case DockerSandboxOutcomeFailed:
		if value.ReasonCode == DockerSandboxReasonCompleted ||
			value.ReasonCode == DockerSandboxReasonTimedOut ||
			value.ReasonCode == DockerSandboxReasonCancelled {
			return errors.New("failed Docker Sandbox receipt reason is invalid")
		}
	}
	return nil
}

func DockerSandboxReceiptFingerprint(value DockerSandboxReceipt) string {
	exitCode := ""
	if value.ExitCode != nil {
		exitCode = fmt.Sprint(*value.ExitCode)
	}
	return digestParts(DockerSandboxReceiptProtocolVersion, value.ID,
		value.AdmissionID, value.LifecycleIntentID, value.AttemptID, value.RunID,
		value.WorkspaceID, value.Outcome, value.ReasonCode, exitCode,
		value.LogReceiptID, value.OutputStagingReceiptID,
		value.OutputCommitReceiptID, fmt.Sprint(value.ArtifactCount),
		fmt.Sprint(value.CleanupComplete), fmt.Sprint(value.ArtifactCommitAuthorized),
		value.CompletedAt.UTC().Format(time.RFC3339Nano))
}

type DockerSandboxRecord struct {
	Admission DockerSandboxAdmission
	Start     *DockerSandboxStartIntent
	Launch    *DockerSandboxLaunch
	Receipt   *DockerSandboxReceipt
	Replayed  bool
}

func (value DockerSandboxRecord) Validate() error {
	if err := value.Admission.Validate(); err != nil {
		return err
	}
	if value.Start != nil {
		if err := value.Start.Validate(); err != nil ||
			value.Start.AdmissionID != value.Admission.ID ||
			value.Start.RunID != value.Admission.RunID ||
			value.Start.RequestedBy != value.Admission.RequestedBy ||
			value.Start.RuntimeEpochFingerprint !=
				value.Admission.RuntimeEpochFingerprint ||
			value.Start.CreatedAt.Before(value.Admission.CreatedAt) {
			return errors.New("Docker Sandbox start intent does not bind its admission")
		}
	}
	if value.Launch != nil {
		if err := value.Launch.Validate(); err != nil ||
			value.Launch.AdmissionID != value.Admission.ID ||
			value.Launch.RunID != value.Admission.RunID ||
			value.Launch.CreatedAt.Before(value.Admission.CreatedAt) ||
			(value.Start != nil &&
				(value.Launch.StartOperationKeyDigest !=
					value.Start.OperationKeyDigest ||
					value.Launch.CreatedAt.Before(value.Start.CreatedAt))) ||
			(value.Start == nil && value.Launch.StartOperationKeyDigest != "") {
			return errors.New("Docker Sandbox launch does not bind its admission")
		}
	}
	if value.Receipt != nil {
		if value.Launch == nil || value.Receipt.Validate() != nil ||
			value.Receipt.AdmissionID != value.Admission.ID ||
			value.Receipt.LifecycleIntentID != value.Launch.LifecycleIntentID ||
			value.Receipt.AttemptID != value.Launch.AttemptID ||
			value.Receipt.RunID != value.Admission.RunID ||
			value.Receipt.WorkspaceID != value.Admission.WorkspaceID ||
			value.Receipt.ArtifactCommitAuthorized !=
				value.Admission.ArtifactCommitAuthorized ||
			value.Receipt.CompletedAt.Before(value.Launch.CreatedAt) {
			return errors.New("Docker Sandbox receipt does not bind its launch")
		}
	}
	return nil
}

func validOptionalAgentID(value string) bool {
	return value == "" || (ValidAgentID(value) && !strings.ContainsRune(value, 0))
}

func validDockerSandboxOutcome(value string) bool {
	switch value {
	case DockerSandboxOutcomeSucceeded, DockerSandboxOutcomeTimedOut,
		DockerSandboxOutcomeCancelled, DockerSandboxOutcomeFailed:
		return true
	default:
		return false
	}
}

func validDockerSandboxReason(value string) bool {
	switch value {
	case DockerSandboxReasonReady, DockerSandboxReasonFeatureDisabled,
		DockerSandboxReasonDaemonUnreachable, DockerSandboxReasonAPIUnsupported,
		DockerSandboxReasonPlatformUnsupported, DockerSandboxReasonResourceUnavailable,
		DockerSandboxReasonManagedEgressUnavailable, DockerSandboxReasonPolicyDenied,
		DockerSandboxReasonApprovalRequired, DockerSandboxReasonPermissionDenied,
		DockerSandboxReasonBudgetExhausted, DockerSandboxReasonAuthorityChanged,
		DockerSandboxReasonCancelled, DockerSandboxReasonTimedOut,
		DockerSandboxReasonDaemonDisconnected, DockerSandboxReasonIOFailed,
		DockerSandboxReasonProcessFailed,
		DockerSandboxReasonCleanupFailed, DockerSandboxReasonCompleted:
		return true
	default:
		return false
	}
}

func validDockerSandboxRemediation(value string) bool {
	switch value {
	case DockerSandboxRemediationNone, DockerSandboxRemediationEnableFeature,
		DockerSandboxRemediationStartDocker, DockerSandboxRemediationUpdateDocker,
		DockerSandboxRemediationUseLinuxContainers,
		DockerSandboxRemediationDisableNetwork, DockerSandboxRemediationReviewPolicy,
		DockerSandboxRemediationApproveExactRequest,
		DockerSandboxRemediationSelectDockerProfile,
		DockerSandboxRemediationRestoreBudget,
		DockerSandboxRemediationRetryFreshRequest,
		DockerSandboxRemediationInspectCleanup,
		DockerSandboxRemediationCorrectRequest,
		DockerSandboxRemediationEnablePIDsLimit,
		DockerSandboxRemediationReduceResources,
		DockerSandboxRemediationProvideImage:
		return true
	default:
		return false
	}
}

func digestParts(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{'|'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
