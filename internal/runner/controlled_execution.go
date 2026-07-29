package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	ControlledExecutionProtocolVersion       = "controlled_command_execution.v1"
	ControlledExecutionPolicyVersion         = "controlled_command_execution_policy.v1"
	ControlledExecutionIntentProtocolVersion = "controlled_command_execution_intent.v1"
	MaxControlledProcessMemoryBytes          = 512 * 1024 * 1024
	MaxControlledOutputCaptureBytes          = 64 * 1024
	MaxControlledOutputObservedBytes         = 64 * 1024 * 1024
)

type ControlledExecutionIntent struct {
	ProtocolVersion          string
	PolicyVersion            string
	RequestID                string
	PlanID                   string
	PlanFingerprint          string
	RunID                    string
	WorkspaceID              string
	InteractionSnapshotID    string
	InteractionRevision      int64
	ExecutionProfileRevision int64
	Kind                     ControlledCommandKind
	RequestedBy              string
	CreatedAt                time.Time
}

func NewControlledExecutionIntent(plan ControlledCommandPlan,
	requestedBy string, createdAt time.Time,
) (ControlledExecutionIntent, error) {
	if err := plan.Validate(); err != nil || !validExecutionOperator(requestedBy) ||
		createdAt.IsZero() {
		return ControlledExecutionIntent{}, ErrControlledExecutionBoundary
	}
	intent := ControlledExecutionIntent{
		ProtocolVersion: ControlledExecutionIntentProtocolVersion,
		PolicyVersion:   ControlledExecutionPolicyVersion,
		RequestID:       ControlledExecutionRequestID(plan),
		PlanID:          plan.ID, PlanFingerprint: plan.Fingerprint,
		RunID: plan.RunID, WorkspaceID: plan.WorkspaceID,
		InteractionSnapshotID:    plan.InteractionSnapshotID,
		InteractionRevision:      plan.InteractionRevision,
		ExecutionProfileRevision: plan.ExecutionProfileRevision,
		Kind:                     plan.Kind, RequestedBy: requestedBy,
		CreatedAt: createdAt.UTC(),
	}
	if err := intent.Validate(); err != nil {
		return ControlledExecutionIntent{}, err
	}
	return intent, nil
}

func ControlledExecutionRequestID(plan ControlledCommandPlan) string {
	if !validSHA256(plan.Fingerprint) {
		return ""
	}
	return "controlled-exec-" + plan.Fingerprint[:24]
}

func (i ControlledExecutionIntent) Validate() error {
	if i.ProtocolVersion != ControlledExecutionIntentProtocolVersion ||
		i.PolicyVersion != ControlledExecutionPolicyVersion ||
		!validIdentity(i.RequestID) || !validIdentity(i.PlanID) ||
		!validSHA256(i.PlanFingerprint) ||
		i.RequestID != "controlled-exec-"+i.PlanFingerprint[:24] ||
		!domain.ValidAgentID(i.RunID) || !domain.ValidAgentID(i.WorkspaceID) ||
		!validIdentity(i.InteractionSnapshotID) ||
		i.InteractionRevision <= 0 || i.ExecutionProfileRevision <= 0 ||
		!validExecutionOperator(i.RequestedBy) || i.CreatedAt.IsZero() {
		return ErrControlledExecutionBoundary
	}
	_, err := ParseControlledCommandKind(string(i.Kind))
	return err
}

var (
	ErrControlledExecutionBoundary = errors.New("controlled command execution boundary is invalid")
	ErrControlledExecutionDenied   = errors.New("controlled command execution is denied")
	ErrControlledExecutionPlatform = errors.New("controlled command execution platform is unavailable")
	ErrControlledOutputLimit       = errors.New("controlled command output limit exceeded")
)

// ControlledExecutionRequest is an operator-only, one-shot use of a closed
// command plan. It deliberately repeats the current durable bindings so a
// stale plan cannot be started after a Run mode or profile transition.
type ControlledExecutionRequest struct {
	Plan              ControlledCommandPlan
	WorkspaceRoot     string
	Interaction       domain.RunExecutionInteractionSnapshot
	CurrentProfile    domain.RunExecutionProfileSnapshot
	CurrentSurface    domain.ExecutionSurface
	RequestedBy       string
	OperatorConfirmed bool
}

type ControlledStartSpec struct {
	RequestID       string
	PlanID          string
	PlanFingerprint string
	ExecutableID    string
	Argv            []string
	WorkspaceRoot   string
	Timeout         time.Duration
}

func (s ControlledStartSpec) Validate() error {
	if !validIdentity(s.RequestID) || !validIdentity(s.PlanID) ||
		!validSHA256(s.PlanFingerprint) ||
		strings.TrimSpace(s.ExecutableID) != s.ExecutableID ||
		s.ExecutableID == "" || len(s.Argv) == 0 ||
		!filepath.IsAbs(s.WorkspaceRoot) ||
		filepath.Clean(s.WorkspaceRoot) != s.WorkspaceRoot ||
		strings.ContainsRune(s.WorkspaceRoot, 0) ||
		s.Timeout < time.Millisecond || s.Timeout > MaxControlledCommandTimeout {
		return ErrControlledExecutionBoundary
	}
	for _, argument := range s.Argv {
		if !validControlledArgument(argument) {
			return ErrControlledExecutionBoundary
		}
	}
	return validateControlledStartTemplate(s)
}

func validateControlledStartTemplate(spec ControlledStartSpec) error {
	switch spec.ExecutableID {
	case "git":
		if equalStrings(spec.Argv,
			[]string{"status", "--short", "--branch", "--untracked-files=no"}) ||
			equalStrings(spec.Argv,
				[]string{"diff", "--check", "--no-ext-diff", "--no-textconv"}) {
			return nil
		}
	case "go":
		if equalStrings(spec.Argv, []string{"version"}) {
			return nil
		}
	case "windows-powershell":
		if len(spec.Argv) == 8 &&
			spec.Argv[0] == "-NoLogo" &&
			spec.Argv[1] == "-NoProfile" &&
			spec.Argv[2] == "-NonInteractive" &&
			spec.Argv[3] == "-ExecutionPolicy" &&
			spec.Argv[4] == "Restricted" &&
			spec.Argv[5] == "-Command" &&
			spec.Argv[6] == controlledPowerShellWorkspaceListScript &&
			decodeControlledRelativePath(spec.Argv[7]) != "" {
			return nil
		}
	}
	return fmt.Errorf("%w: start template is not product-owned",
		ErrControlledExecutionBoundary)
}

type ControlledOutput struct {
	Data                 []byte
	ObservedBytes        int64
	CapturedBytes        int
	CapturedPrefixSHA256 string
	Truncated            bool
}

func (o ControlledOutput) Validate() error {
	if o.ObservedBytes < 0 || o.ObservedBytes > MaxControlledOutputObservedBytes ||
		o.CapturedBytes < 0 || o.CapturedBytes > MaxControlledOutputCaptureBytes ||
		o.CapturedBytes != len(o.Data) || int64(o.CapturedBytes) > o.ObservedBytes ||
		!validSHA256(o.CapturedPrefixSHA256) ||
		o.Truncated != (o.ObservedBytes > int64(o.CapturedBytes)) {
		return ErrControlledExecutionBoundary
	}
	digest := sha256.Sum256(o.Data)
	if hex.EncodeToString(digest[:]) != o.CapturedPrefixSHA256 {
		return ErrControlledExecutionBoundary
	}
	expectedCapture := o.ObservedBytes
	if expectedCapture > MaxControlledOutputCaptureBytes {
		expectedCapture = MaxControlledOutputCaptureBytes
	}
	if int64(o.CapturedBytes) != expectedCapture {
		return ErrControlledExecutionBoundary
	}
	return nil
}

type ControlledStartResult struct {
	ExitCode                int
	Stdout                  ControlledOutput
	Stderr                  ControlledOutput
	StartedAt               time.Time
	CompletedAt             time.Time
	TimedOut                bool
	Cancelled               bool
	OutputLimitExceeded     bool
	TreeReaped              bool
	RestrictedToken         bool
	LowIntegrityToken       bool
	JobAssignedAtCreation   bool
	KillOnJobClose          bool
	ActiveProcessLimit      int
	ProcessMemoryLimit      int64
	StdinClosed             bool
	EnvironmentInherited    bool
	NetworkRequested        bool
	PersistentProcess       bool
	ProductExecutionEnabled bool
}

func (r ControlledStartResult) Validate() error {
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) ||
		!r.TreeReaped || !r.RestrictedToken || !r.LowIntegrityToken ||
		!r.JobAssignedAtCreation || !r.KillOnJobClose ||
		r.ActiveProcessLimit != 1 ||
		r.ProcessMemoryLimit != MaxControlledProcessMemoryBytes ||
		!r.StdinClosed || r.EnvironmentInherited || r.NetworkRequested ||
		r.PersistentProcess || !r.ProductExecutionEnabled ||
		(r.TimedOut && r.Cancelled) {
		return ErrControlledExecutionBoundary
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
		return ErrControlledExecutionBoundary
	}
	return nil
}

type ControlledExecutionResult struct {
	ProtocolVersion          string
	PolicyVersion            string
	RequestID                string
	PlanID                   string
	PlanFingerprint          string
	RunID                    string
	WorkspaceID              string
	InteractionSnapshotID    string
	InteractionRevision      int64
	ExecutionProfileRevision int64
	Kind                     ControlledCommandKind
	Backend                  string
	ExitCode                 int
	Stdout                   ControlledOutput
	Stderr                   ControlledOutput
	StartedAt                time.Time
	CompletedAt              time.Time
	TimedOut                 bool
	Cancelled                bool
	OutputLimitExceeded      bool
	TreeReaped               bool
	RestrictedToken          bool
	LowIntegrityToken        bool
	JobAssignedAtCreation    bool
	KillOnJobClose           bool
	ActiveProcessLimit       int
	ProcessMemoryLimit       int64
	StdinClosed              bool
	EnvironmentInherited     bool
	NetworkRequested         bool
	PersistentProcess        bool
	ProductExecutionEnabled  bool
}

// ControlledExecutionReceipt is the metadata-only durable projection of a
// sealed execution result. Raw stdout and stderr deliberately do not belong to
// this type.
type ControlledExecutionReceipt struct {
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
	RestrictedToken         bool
	LowIntegrityToken       bool
	JobAssignedAtCreation   bool
	KillOnJobClose          bool
	ActiveProcessLimit      int
	ProcessMemoryLimit      int64
	StdinClosed             bool
	EnvironmentInherited    bool
	NetworkRequested        bool
	PersistentProcess       bool
	ProductExecutionEnabled bool
}

func (r ControlledExecutionReceipt) Validate() error {
	expectedStdoutCapture := r.StdoutObservedBytes
	if expectedStdoutCapture > MaxControlledOutputCaptureBytes {
		expectedStdoutCapture = MaxControlledOutputCaptureBytes
	}
	expectedStderrCapture := r.StderrObservedBytes
	if expectedStderrCapture > MaxControlledOutputCaptureBytes {
		expectedStderrCapture = MaxControlledOutputCaptureBytes
	}
	if !validIdentity(r.RequestID) ||
		r.ProtocolVersion != ControlledExecutionProtocolVersion ||
		r.PolicyVersion != ControlledExecutionPolicyVersion ||
		!validIdentity(r.Backend) ||
		r.StdoutObservedBytes < 0 ||
		r.StdoutObservedBytes > MaxControlledOutputObservedBytes ||
		r.StdoutCapturedBytes < 0 ||
		r.StdoutCapturedBytes > MaxControlledOutputCaptureBytes ||
		int64(r.StdoutCapturedBytes) > r.StdoutObservedBytes ||
		int64(r.StdoutCapturedBytes) != expectedStdoutCapture ||
		!validSHA256(r.StdoutPrefixSHA256) ||
		r.StdoutTruncated !=
			(r.StdoutObservedBytes > int64(r.StdoutCapturedBytes)) ||
		r.StderrObservedBytes < 0 ||
		r.StderrObservedBytes > MaxControlledOutputObservedBytes ||
		r.StderrCapturedBytes < 0 ||
		r.StderrCapturedBytes > MaxControlledOutputCaptureBytes ||
		int64(r.StderrCapturedBytes) > r.StderrObservedBytes ||
		int64(r.StderrCapturedBytes) != expectedStderrCapture ||
		!validSHA256(r.StderrPrefixSHA256) ||
		r.StderrTruncated !=
			(r.StderrObservedBytes > int64(r.StderrCapturedBytes)) ||
		r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) ||
		(r.TimedOut && r.Cancelled) || !r.TreeReaped ||
		(r.OutputLimitExceeded &&
			r.StdoutObservedBytes != MaxControlledOutputObservedBytes &&
			r.StderrObservedBytes != MaxControlledOutputObservedBytes) ||
		!r.RestrictedToken || !r.LowIntegrityToken ||
		!r.JobAssignedAtCreation || !r.KillOnJobClose ||
		r.ActiveProcessLimit != 1 ||
		r.ProcessMemoryLimit != MaxControlledProcessMemoryBytes ||
		!r.StdinClosed || r.EnvironmentInherited || r.NetworkRequested ||
		r.PersistentProcess || !r.ProductExecutionEnabled {
		return ErrControlledExecutionBoundary
	}
	return nil
}

func (r ControlledExecutionResult) Validate() error {
	if r.ProtocolVersion != ControlledExecutionProtocolVersion ||
		r.PolicyVersion != ControlledExecutionPolicyVersion ||
		!validIdentity(r.RequestID) || !validIdentity(r.PlanID) ||
		!validSHA256(r.PlanFingerprint) || !domain.ValidAgentID(r.RunID) ||
		!domain.ValidAgentID(r.WorkspaceID) ||
		!validIdentity(r.InteractionSnapshotID) ||
		r.InteractionRevision <= 0 || r.ExecutionProfileRevision <= 0 ||
		!validIdentity(r.Backend) {
		return ErrControlledExecutionBoundary
	}
	if _, err := ParseControlledCommandKind(string(r.Kind)); err != nil {
		return err
	}
	started := ControlledStartResult{
		ExitCode: r.ExitCode, Stdout: r.Stdout, Stderr: r.Stderr,
		StartedAt: r.StartedAt, CompletedAt: r.CompletedAt,
		TimedOut: r.TimedOut, Cancelled: r.Cancelled,
		OutputLimitExceeded: r.OutputLimitExceeded, TreeReaped: r.TreeReaped,
		RestrictedToken: r.RestrictedToken, LowIntegrityToken: r.LowIntegrityToken,
		JobAssignedAtCreation: r.JobAssignedAtCreation,
		KillOnJobClose:        r.KillOnJobClose, ActiveProcessLimit: r.ActiveProcessLimit,
		ProcessMemoryLimit: r.ProcessMemoryLimit, StdinClosed: r.StdinClosed,
		EnvironmentInherited: r.EnvironmentInherited,
		NetworkRequested:     r.NetworkRequested, PersistentProcess: r.PersistentProcess,
		ProductExecutionEnabled: r.ProductExecutionEnabled,
	}
	return started.Validate()
}

type ControlledProcessStarter interface {
	Name() string
	Available() bool
	Start(context.Context, ControlledStartSpec) (ControlledStartResult, error)
}

type ControlledExecutor struct {
	starter ControlledProcessStarter
}

func NewControlledExecutor(starter ControlledProcessStarter) (*ControlledExecutor, error) {
	if starter == nil || !validIdentity(starter.Name()) {
		return nil, ErrControlledExecutionBoundary
	}
	return &ControlledExecutor{starter: starter}, nil
}

func NewPlatformControlledExecutor() (*ControlledExecutor, error) {
	return NewControlledExecutor(newPlatformControlledStarter())
}

func (e *ControlledExecutor) Available() bool {
	return e != nil && e.starter != nil && e.starter.Available()
}

func (e *ControlledExecutor) Execute(ctx context.Context,
	request ControlledExecutionRequest,
) (ControlledExecutionResult, error) {
	if e == nil || e.starter == nil || !e.starter.Available() {
		return ControlledExecutionResult{}, ErrControlledExecutionPlatform
	}
	if ctx == nil {
		return ControlledExecutionResult{}, ErrControlledExecutionBoundary
	}
	if err := ctx.Err(); err != nil {
		return ControlledExecutionResult{}, err
	}
	if err := validateControlledExecutionRequest(request); err != nil {
		return ControlledExecutionResult{}, err
	}
	spec := ControlledStartSpec{
		RequestID: ControlledExecutionRequestID(request.Plan),
		PlanID:    request.Plan.ID, PlanFingerprint: request.Plan.Fingerprint,
		ExecutableID:  request.Plan.ExecutableID,
		Argv:          append([]string(nil), request.Plan.Argv...),
		WorkspaceRoot: request.WorkspaceRoot,
		Timeout:       time.Duration(request.Plan.TimeoutMilliseconds) * time.Millisecond,
	}
	if err := spec.Validate(); err != nil {
		return ControlledExecutionResult{}, err
	}
	started, startErr := e.starter.Start(ctx, spec)
	if startErr != nil && started.StartedAt.IsZero() {
		return ControlledExecutionResult{}, startErr
	}
	if validationErr := started.Validate(); validationErr != nil {
		if startErr != nil {
			return ControlledExecutionResult{}, errors.Join(startErr, validationErr)
		}
		return ControlledExecutionResult{}, validationErr
	}
	result := ControlledExecutionResult{
		ProtocolVersion: ControlledExecutionProtocolVersion,
		PolicyVersion:   ControlledExecutionPolicyVersion,
		RequestID:       spec.RequestID, PlanID: request.Plan.ID,
		PlanFingerprint: request.Plan.Fingerprint, RunID: request.Plan.RunID,
		WorkspaceID:              request.Plan.WorkspaceID,
		InteractionSnapshotID:    request.Plan.InteractionSnapshotID,
		InteractionRevision:      request.Plan.InteractionRevision,
		ExecutionProfileRevision: request.Plan.ExecutionProfileRevision,
		Kind:                     request.Plan.Kind, Backend: e.starter.Name(),
		ExitCode: started.ExitCode, Stdout: started.Stdout, Stderr: started.Stderr,
		StartedAt: started.StartedAt, CompletedAt: started.CompletedAt,
		TimedOut: started.TimedOut, Cancelled: started.Cancelled,
		OutputLimitExceeded: started.OutputLimitExceeded,
		TreeReaped:          started.TreeReaped, RestrictedToken: started.RestrictedToken,
		LowIntegrityToken:       started.LowIntegrityToken,
		JobAssignedAtCreation:   started.JobAssignedAtCreation,
		KillOnJobClose:          started.KillOnJobClose,
		ActiveProcessLimit:      started.ActiveProcessLimit,
		ProcessMemoryLimit:      started.ProcessMemoryLimit,
		StdinClosed:             started.StdinClosed,
		EnvironmentInherited:    started.EnvironmentInherited,
		NetworkRequested:        started.NetworkRequested,
		PersistentProcess:       started.PersistentProcess,
		ProductExecutionEnabled: started.ProductExecutionEnabled,
	}
	if err := result.Validate(); err != nil {
		return ControlledExecutionResult{}, err
	}
	return result, startErr
}

func validateControlledExecutionRequest(request ControlledExecutionRequest) error {
	if err := request.Plan.Validate(); err != nil {
		return err
	}
	if !request.OperatorConfirmed || !validExecutionOperator(request.RequestedBy) {
		return ErrControlledExecutionDenied
	}
	if err := request.Interaction.Validate(); err != nil ||
		request.CurrentProfile.Validate() != nil ||
		request.CurrentSurface != domain.ExecutionSurfaceCode ||
		request.Interaction.Mode != domain.RunExecutionInteractionControlled ||
		request.Interaction.Surface != domain.ExecutionSurfaceCode ||
		request.Interaction.ExecutionProfile != domain.RunExecutionProfileLocal ||
		request.Interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted ||
		request.Interaction.CommandForm != domain.ExecutionCommandStructuredArgv ||
		request.Interaction.PersistentTerminal || request.Interaction.AgentInputDefault ||
		request.Interaction.ProcessEnabled || request.Interaction.ExecutionAuthorized ||
		request.Interaction.CapabilityGrant {
		return ErrControlledExecutionBoundary
	}
	if request.Plan.RunID != request.Interaction.RunID ||
		request.Plan.WorkspaceID == "" ||
		request.Plan.InteractionSnapshotID != request.Interaction.ID ||
		request.Plan.InteractionRevision != request.Interaction.Revision ||
		request.Plan.ExecutionProfileRevision != request.CurrentProfile.Revision ||
		request.Interaction.ExecutionProfileRevision != request.CurrentProfile.Revision ||
		request.CurrentProfile.RunID != request.Interaction.RunID ||
		request.CurrentProfile.MissionID != request.Interaction.MissionID ||
		request.CurrentProfile.Profile != domain.RunExecutionProfileLocal {
		return fmt.Errorf("%w: durable execution binding is stale",
			ErrControlledExecutionBoundary)
	}
	root := filepath.Clean(request.WorkspaceRoot)
	if !filepath.IsAbs(root) || root != request.WorkspaceRoot ||
		strings.ContainsRune(root, 0) {
		return ErrControlledExecutionBoundary
	}
	digest := sha256.Sum256([]byte(root))
	if hex.EncodeToString(digest[:]) != request.Plan.WorkspaceRootSHA256 {
		return fmt.Errorf("%w: Workspace root digest changed",
			ErrControlledExecutionBoundary)
	}
	return nil
}

func validControlledArgument(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		!strings.ContainsRune(value, 0)
}

func validExecutionOperator(value string) bool {
	value = strings.TrimSpace(value)
	if !domain.ValidAgentID(value) || strings.ContainsRune(value, 0) {
		return false
	}
	switch strings.ToLower(value) {
	case "agent", "llm", "model", "repository", "repo", "skill",
		"supervisor", "run_supervisor":
		return false
	default:
		return true
	}
}
