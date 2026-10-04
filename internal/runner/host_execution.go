package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	HostExecutionProtocolVersion = "host_command_execution.v1"
	HostExecutionPolicyVersion   = "host_command_execution_policy.v1"
)

// HostExecutionIntent is the durable write-ahead boundary. It contains the
// exact non-secret command envelope but never carries environment values,
// output, or authority. A stored intent without a receipt is uncertain and
// must not be retried automatically.
type HostExecutionIntent struct {
	ProtocolVersion                  string
	PolicyVersion                    string
	RequestID                        string
	OperationKeyDigest               string
	RunID                            string
	MissionID                        string
	SessionID                        string
	WorkspaceID                      string
	InteractionSnapshotID            string
	InteractionRevision              int64
	ExecutionProfileRevision         int64
	PermissionSnapshotID             string
	PermissionRevision               int64
	PermissionMode                   domain.RunExecutionPermissionMode
	AuthorizationProposalID          string
	AuthorizationProposalFingerprint string
	AuthorizationReviewID            string
	AuthorizationReviewFingerprint   string
	Spec                             HostCommandSpec
	RequestedBy                      string
	NonSandboxed                     bool
	AutomaticRetryAllowed            bool
	CreatedAt                        time.Time
}

func HostExecutionRequestID(
	runID string,
	operationKeyDigest string,
	specFingerprint string,
) string {
	if !validIdentity(runID) || !validSHA256(operationKeyDigest) ||
		!validSHA256(specFingerprint) {
		return ""
	}
	encoded, err := json.Marshal([]string{
		HostCommandIntentProtocolVersion, runID,
		operationKeyDigest, specFingerprint,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "host-exec-" + hex.EncodeToString(digest[:])[:24]
}

func (i HostExecutionIntent) Validate() error {
	for _, value := range []string{
		i.RequestID, i.RunID, i.MissionID, i.SessionID, i.WorkspaceID,
		i.InteractionSnapshotID, i.PermissionSnapshotID, i.RequestedBy,
	} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if i.ProtocolVersion != HostCommandIntentProtocolVersion ||
		i.PolicyVersion != HostExecutionPolicyVersion ||
		!validSHA256(i.OperationKeyDigest) ||
		i.InteractionRevision <= 0 || i.ExecutionProfileRevision <= 0 ||
		i.PermissionRevision <= 0 ||
		(!i.PermissionMode.IncludesFullAccess() &&
			i.PermissionMode != domain.RunExecutionPermissionApproval &&
			i.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess) ||
		i.Spec.Validate() != nil || !validExecutionOperator(i.RequestedBy) ||
		!i.NonSandboxed || i.AutomaticRetryAllowed || i.CreatedAt.IsZero() ||
		i.RequestID != HostExecutionRequestID(
			i.RunID, i.OperationKeyDigest, i.Spec.Fingerprint) {
		return ErrHostCommandBoundary
	}
	if i.PermissionMode.IncludesFullAccess() {
		if i.AuthorizationProposalID != "" ||
			i.AuthorizationProposalFingerprint != "" ||
			i.AuthorizationReviewID != "" ||
			i.AuthorizationReviewFingerprint != "" {
			return ErrHostCommandBoundary
		}
	} else {
		for _, value := range []string{
			i.AuthorizationProposalID, i.AuthorizationReviewID,
		} {
			if !validIdentity(value) {
				return ErrHostCommandBoundary
			}
		}
		if !validSHA256(i.AuthorizationProposalFingerprint) ||
			!validSHA256(i.AuthorizationReviewFingerprint) {
			return ErrHostCommandBoundary
		}
	}
	return nil
}

func HostExecutionIntentFingerprint(intent HostExecutionIntent) string {
	intent.CreatedAt = time.Time{}
	encoded, err := json.Marshal(intent)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// HostExecutionReceipt is metadata-only. Raw output and environment values
// are intentionally absent.
type HostExecutionReceipt struct {
	RequestID               string
	ProtocolVersion         string
	PolicyVersion           string
	Backend                 string
	ExitCode                int
	StdoutObservedBytes     int64
	StdoutCapturedBytes     int
	StdoutPrefixSHA256      string
	StdoutTruncated         bool
	StderrObservedBytes     int64
	StderrCapturedBytes     int
	StderrPrefixSHA256      string
	StderrTruncated         bool
	StartedAt               time.Time
	CompletedAt             time.Time
	TimedOut                bool
	Cancelled               bool
	OutputLimitExceeded     bool
	TreeReaped              bool
	NonSandboxed            bool
	RestrictedToken         bool
	LowIntegrityToken       bool
	JobAssignedAtCreation   bool
	KillOnJobClose          bool
	ActiveProcessLimit      int
	JobMemoryLimit          int64
	StdinClosed             bool
	EnvironmentInherited    bool
	NetworkRequested        bool
	PersistentProcess       bool
	ProductExecutionEnabled bool
}

func (r HostExecutionReceipt) Validate() error {
	expectedStdout := r.StdoutObservedBytes
	if expectedStdout > MaxControlledOutputCaptureBytes {
		expectedStdout = MaxControlledOutputCaptureBytes
	}
	expectedStderr := r.StderrObservedBytes
	if expectedStderr > MaxControlledOutputCaptureBytes {
		expectedStderr = MaxControlledOutputCaptureBytes
	}
	if !validIdentity(r.RequestID) ||
		r.ProtocolVersion != HostCommandReceiptProtocolVersion ||
		r.PolicyVersion != HostExecutionPolicyVersion ||
		!validIdentity(r.Backend) ||
		r.StdoutObservedBytes < 0 ||
		r.StdoutObservedBytes > MaxControlledOutputObservedBytes ||
		r.StdoutCapturedBytes < 0 ||
		int64(r.StdoutCapturedBytes) != expectedStdout ||
		!validSHA256(r.StdoutPrefixSHA256) ||
		r.StdoutTruncated !=
			(r.StdoutObservedBytes > int64(r.StdoutCapturedBytes)) ||
		r.StderrObservedBytes < 0 ||
		r.StderrObservedBytes > MaxControlledOutputObservedBytes ||
		r.StderrCapturedBytes < 0 ||
		int64(r.StderrCapturedBytes) != expectedStderr ||
		!validSHA256(r.StderrPrefixSHA256) ||
		r.StderrTruncated !=
			(r.StderrObservedBytes > int64(r.StderrCapturedBytes)) ||
		r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) ||
		(r.TimedOut && r.Cancelled) || !r.TreeReaped ||
		(r.OutputLimitExceeded &&
			r.StdoutObservedBytes != MaxControlledOutputObservedBytes &&
			r.StderrObservedBytes != MaxControlledOutputObservedBytes) ||
		!r.NonSandboxed || r.RestrictedToken || r.LowIntegrityToken ||
		!r.JobAssignedAtCreation || !r.KillOnJobClose ||
		r.ActiveProcessLimit != MaxHostActiveProcesses ||
		r.JobMemoryLimit != MaxHostProcessMemoryBytes ||
		!r.StdinClosed || r.EnvironmentInherited || !r.NetworkRequested ||
		r.PersistentProcess || !r.ProductExecutionEnabled {
		return ErrHostCommandBoundary
	}
	return nil
}
