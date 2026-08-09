package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
)

const (
	HostExecutionProtocolVersion = "host_command_execution.v1"
	HostExecutionPolicyVersion   = "host_command_execution_policy.v1"
)

var ErrHostOutputLimit = errors.New("host command output limit exceeded")

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

func NewApprovedHostExecutionIntent(proposal HostCommandProposal,
	review HostCommandReview, operationKeyDigest string,
	createdAt time.Time,
) (HostExecutionIntent, error) {
	if proposal.Validate() != nil || review.Validate() != nil ||
		review.ProposalID != proposal.ID ||
		review.ProposalFingerprint != proposal.Fingerprint ||
		review.RunID != proposal.RunID ||
		review.Decision != HostCommandReviewApprove ||
		!review.SingleUseExecutionAuthorized {
		return HostExecutionIntent{}, ErrHostCommandBoundary
	}
	intent := HostExecutionIntent{
		ProtocolVersion:    HostCommandIntentProtocolVersion,
		PolicyVersion:      HostExecutionPolicyVersion,
		OperationKeyDigest: strings.ToLower(strings.TrimSpace(operationKeyDigest)),
		RunID:              proposal.RunID, MissionID: proposal.MissionID,
		SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		InteractionSnapshotID:            proposal.InteractionSnapshotID,
		InteractionRevision:              proposal.InteractionRevision,
		ExecutionProfileRevision:         proposal.ExecutionProfileRevision,
		PermissionSnapshotID:             proposal.PermissionSnapshotID,
		PermissionRevision:               proposal.PermissionRevision,
		PermissionMode:                   proposal.PermissionMode,
		AuthorizationProposalID:          proposal.ID,
		AuthorizationProposalFingerprint: proposal.Fingerprint,
		AuthorizationReviewID:            review.ID,
		AuthorizationReviewFingerprint:   review.Fingerprint,
		Spec:                             proposal.Spec, RequestedBy: review.ReviewedBy,
		NonSandboxed: true, AutomaticRetryAllowed: false,
		CreatedAt: createdAt.UTC(),
	}
	intent.RequestID = HostExecutionRequestID(intent.RunID,
		intent.OperationKeyDigest, intent.Spec.Fingerprint)
	if err := intent.Validate(); err != nil {
		return HostExecutionIntent{}, err
	}
	return intent, nil
}

type HostExecutionIntentRequest struct {
	OperationKeyDigest string
	RunID              string
	MissionID          string
	SessionID          string
	WorkspaceID        string
	Interaction        domain.RunExecutionInteractionSnapshot
	Profile            domain.RunExecutionProfileSnapshot
	Permission         domain.RunExecutionPermissionSnapshot
	Spec               HostCommandSpec
	RequestedBy        string
	CreatedAt          time.Time
}

func NewHostExecutionIntent(
	request HostExecutionIntentRequest,
) (HostExecutionIntent, error) {
	if request.Spec.Validate() != nil || request.Interaction.Validate() != nil ||
		request.Profile.Validate() != nil || request.Permission.Validate() != nil {
		return HostExecutionIntent{}, ErrHostCommandBoundary
	}
	if request.Interaction.RunID != request.RunID ||
		request.Interaction.MissionID != request.MissionID ||
		request.Profile.RunID != request.RunID ||
		request.Profile.MissionID != request.MissionID ||
		request.Permission.RunID != request.RunID ||
		request.Permission.MissionID != request.MissionID ||
		request.Interaction.ExecutionProfileRevision != request.Profile.Revision ||
		request.Permission.Mode != domain.RunExecutionPermissionFullAccess {
		return HostExecutionIntent{}, fmt.Errorf(
			"%w: host execution intent bindings do not match",
			ErrHostCommandBoundary)
	}
	intent := HostExecutionIntent{
		ProtocolVersion: HostCommandIntentProtocolVersion,
		PolicyVersion:   HostExecutionPolicyVersion,
		OperationKeyDigest: strings.ToLower(strings.TrimSpace(
			request.OperationKeyDigest)),
		RunID:                    strings.TrimSpace(request.RunID),
		MissionID:                strings.TrimSpace(request.MissionID),
		SessionID:                strings.TrimSpace(request.SessionID),
		WorkspaceID:              strings.TrimSpace(request.WorkspaceID),
		InteractionSnapshotID:    request.Interaction.ID,
		InteractionRevision:      request.Interaction.Revision,
		ExecutionProfileRevision: request.Profile.Revision,
		PermissionSnapshotID:     request.Permission.ID,
		PermissionRevision:       request.Permission.Revision,
		PermissionMode:           request.Permission.Mode,
		Spec:                     request.Spec,
		RequestedBy:              strings.TrimSpace(request.RequestedBy),
		NonSandboxed:             true,
		AutomaticRetryAllowed:    false,
		CreatedAt:                request.CreatedAt.UTC(),
	}
	intent.RequestID = HostExecutionRequestID(intent.RunID,
		intent.OperationKeyDigest, intent.Spec.Fingerprint)
	if err := intent.Validate(); err != nil {
		return HostExecutionIntent{}, err
	}
	return intent, nil
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
		(i.PermissionMode != domain.RunExecutionPermissionFullAccess &&
			i.PermissionMode != domain.RunExecutionPermissionApproval) ||
		i.Spec.Validate() != nil || !validExecutionOperator(i.RequestedBy) ||
		!i.NonSandboxed || i.AutomaticRetryAllowed || i.CreatedAt.IsZero() ||
		i.RequestID != HostExecutionRequestID(
			i.RunID, i.OperationKeyDigest, i.Spec.Fingerprint) {
		return ErrHostCommandBoundary
	}
	if i.PermissionMode == domain.RunExecutionPermissionFullAccess {
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

type HostExecutionRequest struct {
	Intent              HostExecutionIntent
	Environment         []string
	Interaction         domain.RunExecutionInteractionSnapshot
	CurrentProfile      domain.RunExecutionProfileSnapshot
	Permission          domain.RunExecutionPermissionSnapshot
	Runtime             domain.ExecutionPermissionRuntimeCapabilities
	CurrentSurface      domain.ExecutionSurface
	RequestedBy         string
	ExplicitlyConfirmed bool
	Review              *HostCommandReview
}

type HostStartSpec struct {
	RequestID   string
	Command     HostCommandSpec
	Environment []string
}

func (s HostStartSpec) Validate() error {
	if !validIdentity(s.RequestID) || s.Command.Validate() != nil {
		return ErrHostCommandBoundary
	}
	environment, keys, digest, err := normalizeHostEnvironment(s.Environment)
	if err != nil || len(environment) == 0 ||
		!equalStrings(keys, s.Command.EnvironmentKeys) ||
		digest != s.Command.EnvironmentSHA256 {
		return ErrHostCommandBoundary
	}
	return nil
}

type HostStartResult struct {
	ExitCode                int
	Stdout                  ControlledOutput
	Stderr                  ControlledOutput
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

func (r HostStartResult) Validate() error {
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) ||
		!r.TreeReaped || !r.NonSandboxed ||
		r.RestrictedToken || r.LowIntegrityToken ||
		!r.JobAssignedAtCreation || !r.KillOnJobClose ||
		r.ActiveProcessLimit != MaxHostActiveProcesses ||
		r.JobMemoryLimit != MaxHostProcessMemoryBytes ||
		!r.StdinClosed || r.EnvironmentInherited || !r.NetworkRequested ||
		r.PersistentProcess || !r.ProductExecutionEnabled ||
		(r.TimedOut && r.Cancelled) {
		return ErrHostCommandBoundary
	}
	if err := r.Stdout.Validate(); err != nil {
		return err
	}
	if err := r.Stderr.Validate(); err != nil {
		return err
	}
	if r.OutputLimitExceeded &&
		r.Stdout.ObservedBytes != MaxControlledOutputObservedBytes &&
		r.Stderr.ObservedBytes != MaxControlledOutputObservedBytes {
		return ErrHostCommandBoundary
	}
	return nil
}

type HostExecutionResult struct {
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
	SpecFingerprint                  string
	Backend                          string
	ExitCode                         int
	Stdout                           ControlledOutput
	Stderr                           ControlledOutput
	StartedAt                        time.Time
	CompletedAt                      time.Time
	TimedOut                         bool
	Cancelled                        bool
	OutputLimitExceeded              bool
	TreeReaped                       bool
	NonSandboxed                     bool
	RestrictedToken                  bool
	LowIntegrityToken                bool
	JobAssignedAtCreation            bool
	KillOnJobClose                   bool
	ActiveProcessLimit               int
	JobMemoryLimit                   int64
	StdinClosed                      bool
	EnvironmentInherited             bool
	NetworkRequested                 bool
	PersistentProcess                bool
	ProductExecutionEnabled          bool
}

func (r HostExecutionResult) Validate() error {
	for _, value := range []string{
		r.RequestID, r.RunID, r.MissionID, r.SessionID, r.WorkspaceID,
		r.InteractionSnapshotID, r.PermissionSnapshotID, r.Backend,
	} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if r.ProtocolVersion != HostExecutionProtocolVersion ||
		r.PolicyVersion != HostExecutionPolicyVersion ||
		!validSHA256(r.OperationKeyDigest) ||
		!validSHA256(r.SpecFingerprint) ||
		r.InteractionRevision <= 0 || r.ExecutionProfileRevision <= 0 ||
		r.PermissionRevision <= 0 ||
		(r.PermissionMode != domain.RunExecutionPermissionFullAccess &&
			r.PermissionMode != domain.RunExecutionPermissionApproval) {
		return ErrHostCommandBoundary
	}
	if r.PermissionMode == domain.RunExecutionPermissionFullAccess {
		if r.AuthorizationProposalID != "" ||
			r.AuthorizationProposalFingerprint != "" ||
			r.AuthorizationReviewID != "" ||
			r.AuthorizationReviewFingerprint != "" {
			return ErrHostCommandBoundary
		}
	} else if !validIdentity(r.AuthorizationProposalID) ||
		!validSHA256(r.AuthorizationProposalFingerprint) ||
		!validIdentity(r.AuthorizationReviewID) ||
		!validSHA256(r.AuthorizationReviewFingerprint) {
		return ErrHostCommandBoundary
	}
	return (HostStartResult{
		ExitCode: r.ExitCode, Stdout: r.Stdout, Stderr: r.Stderr,
		StartedAt: r.StartedAt, CompletedAt: r.CompletedAt,
		TimedOut: r.TimedOut, Cancelled: r.Cancelled,
		OutputLimitExceeded: r.OutputLimitExceeded, TreeReaped: r.TreeReaped,
		NonSandboxed: r.NonSandboxed, RestrictedToken: r.RestrictedToken,
		LowIntegrityToken:     r.LowIntegrityToken,
		JobAssignedAtCreation: r.JobAssignedAtCreation,
		KillOnJobClose:        r.KillOnJobClose,
		ActiveProcessLimit:    r.ActiveProcessLimit,
		JobMemoryLimit:        r.JobMemoryLimit, StdinClosed: r.StdinClosed,
		EnvironmentInherited:    r.EnvironmentInherited,
		NetworkRequested:        r.NetworkRequested,
		PersistentProcess:       r.PersistentProcess,
		ProductExecutionEnabled: r.ProductExecutionEnabled,
	}).Validate()
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

func ProjectHostExecutionReceipt(
	result HostExecutionResult,
) (HostExecutionReceipt, error) {
	if err := result.Validate(); err != nil {
		return HostExecutionReceipt{}, err
	}
	receipt := HostExecutionReceipt{
		RequestID:       result.RequestID,
		ProtocolVersion: HostCommandReceiptProtocolVersion,
		PolicyVersion:   result.PolicyVersion, Backend: result.Backend,
		ExitCode:            result.ExitCode,
		StdoutObservedBytes: result.Stdout.ObservedBytes,
		StdoutCapturedBytes: result.Stdout.CapturedBytes,
		StdoutPrefixSHA256:  result.Stdout.CapturedPrefixSHA256,
		StdoutTruncated:     result.Stdout.Truncated,
		StderrObservedBytes: result.Stderr.ObservedBytes,
		StderrCapturedBytes: result.Stderr.CapturedBytes,
		StderrPrefixSHA256:  result.Stderr.CapturedPrefixSHA256,
		StderrTruncated:     result.Stderr.Truncated,
		StartedAt:           result.StartedAt, CompletedAt: result.CompletedAt,
		TimedOut: result.TimedOut, Cancelled: result.Cancelled,
		OutputLimitExceeded: result.OutputLimitExceeded,
		TreeReaped:          result.TreeReaped, NonSandboxed: result.NonSandboxed,
		RestrictedToken:       result.RestrictedToken,
		LowIntegrityToken:     result.LowIntegrityToken,
		JobAssignedAtCreation: result.JobAssignedAtCreation,
		KillOnJobClose:        result.KillOnJobClose,
		ActiveProcessLimit:    result.ActiveProcessLimit,
		JobMemoryLimit:        result.JobMemoryLimit, StdinClosed: result.StdinClosed,
		EnvironmentInherited:    result.EnvironmentInherited,
		NetworkRequested:        result.NetworkRequested,
		PersistentProcess:       result.PersistentProcess,
		ProductExecutionEnabled: result.ProductExecutionEnabled,
	}
	if err := receipt.Validate(); err != nil {
		return HostExecutionReceipt{}, err
	}
	return receipt, nil
}

type HostProcessStarter interface {
	Name() string
	Available() bool
	Start(context.Context, HostStartSpec) (HostStartResult, error)
}

type HostExecutor struct {
	starter HostProcessStarter
}

func NewHostExecutor(starter HostProcessStarter) (*HostExecutor, error) {
	if starter == nil || !validIdentity(starter.Name()) {
		return nil, ErrHostCommandBoundary
	}
	return &HostExecutor{starter: starter}, nil
}

func NewPlatformHostExecutor() (*HostExecutor, error) {
	return NewHostExecutor(newPlatformHostStarter())
}

func (e *HostExecutor) Available() bool {
	return e != nil && e.starter != nil && e.starter.Available()
}

func (e *HostExecutor) Execute(
	ctx context.Context,
	request HostExecutionRequest,
) (HostExecutionResult, error) {
	if e == nil || e.starter == nil || !e.starter.Available() {
		return HostExecutionResult{}, ErrHostCommandPlatform
	}
	if ctx == nil {
		return HostExecutionResult{}, ErrHostCommandBoundary
	}
	if err := ctx.Err(); err != nil {
		return HostExecutionResult{}, err
	}
	if err := validateHostExecutionRequest(request); err != nil {
		return HostExecutionResult{}, err
	}
	started, startErr := e.starter.Start(ctx, HostStartSpec{
		RequestID:   request.Intent.RequestID,
		Command:     request.Intent.Spec,
		Environment: append([]string(nil), request.Environment...),
	})
	if startErr != nil && started.StartedAt.IsZero() {
		return HostExecutionResult{}, startErr
	}
	if validationErr := started.Validate(); validationErr != nil {
		if startErr != nil {
			return HostExecutionResult{}, errors.Join(startErr, validationErr)
		}
		return HostExecutionResult{}, validationErr
	}
	intent := request.Intent
	result := HostExecutionResult{
		ProtocolVersion:    HostExecutionProtocolVersion,
		PolicyVersion:      HostExecutionPolicyVersion,
		RequestID:          intent.RequestID,
		OperationKeyDigest: intent.OperationKeyDigest,
		RunID:              intent.RunID, MissionID: intent.MissionID,
		SessionID: intent.SessionID, WorkspaceID: intent.WorkspaceID,
		InteractionSnapshotID:            intent.InteractionSnapshotID,
		InteractionRevision:              intent.InteractionRevision,
		ExecutionProfileRevision:         intent.ExecutionProfileRevision,
		PermissionSnapshotID:             intent.PermissionSnapshotID,
		PermissionRevision:               intent.PermissionRevision,
		PermissionMode:                   intent.PermissionMode,
		AuthorizationProposalID:          intent.AuthorizationProposalID,
		AuthorizationProposalFingerprint: intent.AuthorizationProposalFingerprint,
		AuthorizationReviewID:            intent.AuthorizationReviewID,
		AuthorizationReviewFingerprint:   intent.AuthorizationReviewFingerprint,
		SpecFingerprint:                  intent.Spec.Fingerprint,
		Backend:                          e.starter.Name(), ExitCode: started.ExitCode,
		Stdout: started.Stdout, Stderr: started.Stderr,
		StartedAt: started.StartedAt, CompletedAt: started.CompletedAt,
		TimedOut: started.TimedOut, Cancelled: started.Cancelled,
		OutputLimitExceeded: started.OutputLimitExceeded,
		TreeReaped:          started.TreeReaped, NonSandboxed: started.NonSandboxed,
		RestrictedToken:       started.RestrictedToken,
		LowIntegrityToken:     started.LowIntegrityToken,
		JobAssignedAtCreation: started.JobAssignedAtCreation,
		KillOnJobClose:        started.KillOnJobClose,
		ActiveProcessLimit:    started.ActiveProcessLimit,
		JobMemoryLimit:        started.JobMemoryLimit, StdinClosed: started.StdinClosed,
		EnvironmentInherited:    started.EnvironmentInherited,
		NetworkRequested:        started.NetworkRequested,
		PersistentProcess:       started.PersistentProcess,
		ProductExecutionEnabled: started.ProductExecutionEnabled,
	}
	if err := result.Validate(); err != nil {
		return HostExecutionResult{}, err
	}
	return result, startErr
}

func validateHostExecutionRequest(request HostExecutionRequest) error {
	if request.Intent.Validate() != nil ||
		request.Interaction.Validate() != nil ||
		request.CurrentProfile.Validate() != nil ||
		request.Permission.Validate() != nil ||
		request.Runtime.Validate() != nil {
		return ErrHostCommandBoundary
	}
	if !request.ExplicitlyConfirmed ||
		!validExecutionOperator(request.RequestedBy) ||
		request.RequestedBy != request.Intent.RequestedBy {
		return ErrHostCommandDenied
	}
	if request.Intent.RunID != request.Interaction.RunID ||
		request.Intent.MissionID != request.Interaction.MissionID ||
		request.Intent.InteractionSnapshotID != request.Interaction.ID ||
		request.Intent.InteractionRevision != request.Interaction.Revision ||
		request.Intent.ExecutionProfileRevision != request.CurrentProfile.Revision ||
		request.Intent.PermissionSnapshotID != request.Permission.ID ||
		request.Intent.PermissionRevision != request.Permission.Revision ||
		request.Intent.PermissionMode != request.Permission.Mode ||
		request.CurrentProfile.RunID != request.Intent.RunID ||
		request.CurrentProfile.MissionID != request.Intent.MissionID ||
		request.Permission.RunID != request.Intent.RunID ||
		request.Permission.MissionID != request.Intent.MissionID {
		return fmt.Errorf("%w: durable host execution binding is stale",
			ErrHostCommandBoundary)
	}
	if request.CurrentSurface != domain.ExecutionSurfaceCode ||
		request.Interaction.Mode != domain.RunExecutionInteractionControlled ||
		request.Interaction.Surface != domain.ExecutionSurfaceCode ||
		request.Interaction.ExecutionProfile != domain.RunExecutionProfileLocal ||
		request.Interaction.ExecutionProfileRevision !=
			request.CurrentProfile.Revision ||
		request.Interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted ||
		request.Interaction.CommandForm != domain.ExecutionCommandStructuredArgv ||
		request.Interaction.PersistentTerminal ||
		request.CurrentProfile.Profile != domain.RunExecutionProfileLocal {
		return ErrHostCommandDenied
	}
	operatorApproved := false
	if request.Permission.Mode == domain.RunExecutionPermissionApproval {
		if request.Review == nil || request.Review.Validate() != nil ||
			request.Review.Decision != HostCommandReviewApprove ||
			!request.Review.SingleUseExecutionAuthorized ||
			request.Intent.AuthorizationProposalID != request.Review.ProposalID ||
			request.Intent.AuthorizationProposalFingerprint !=
				request.Review.ProposalFingerprint ||
			request.Intent.AuthorizationReviewID != request.Review.ID ||
			request.Intent.AuthorizationReviewFingerprint !=
				request.Review.Fingerprint ||
			request.Review.ReviewedBy != request.RequestedBy {
			return ErrHostCommandDenied
		}
		operatorApproved = true
	} else if request.Permission.Mode != domain.RunExecutionPermissionFullAccess ||
		request.Review != nil {
		return ErrHostCommandDenied
	}
	decision, err := executionauth.EvaluateExecutionPermission(
		request.Permission, request.Runtime, executionauth.PermissionRequest{
			Kind:           executionauth.PermissionOperationStatelessCommand,
			HostFilesystem: true, Network: true,
			OperatorApproved: operatorApproved,
		})
	if err != nil {
		return err
	}
	if !decision.Allowed || !decision.HostFilesystem || !decision.Network {
		return fmt.Errorf("%w: %s", ErrHostCommandDenied, decision.Reason)
	}
	return (HostStartSpec{
		RequestID:   request.Intent.RequestID,
		Command:     request.Intent.Spec,
		Environment: request.Environment,
	}).Validate()
}
