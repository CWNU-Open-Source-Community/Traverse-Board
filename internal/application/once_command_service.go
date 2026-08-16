package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

// OnceCommandStore is the bounded store surface the one-shot command
// operator flow touches.
type OnceCommandStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
	RecordOnceCommandExecution(context.Context, string, map[string]any) error
}

// OnceCommandExecutor runs validated requests with whole-tree termination.
type OnceCommandExecutor interface {
	Available() bool
	Execute(context.Context, runner.OnceCommandRequest) (runner.OnceExecutionResult, error)
}

// OnceCommandService is the operator-facing execution path. The four-tier
// permission gate is enforced here: conservative denies, approval requires
// the operator-approved flag, full access runs with audit, and debug never
// creates a persistent shell.
type OnceCommandService struct {
	store        OnceCommandStore
	executor     OnceCommandExecutor
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

func NewOnceCommandService(store OnceCommandStore, executor OnceCommandExecutor,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) *OnceCommandService {
	return &OnceCommandService{store: store, executor: executor, capabilities: capabilities}
}

type OnceCommandRunRequest struct {
	RunID               string
	ExecutablePath      string
	Argv                []string
	WorkingDirectory    string
	Environment         []string
	TimeoutMilliseconds int64
	Purpose             string
	RequestedBy         string
	OperatorApproved    bool
}

type OnceCommandRunResult struct {
	Result             runner.OnceExecutionResult
	SpecFingerprint    string
	RequestFingerprint string
	PermissionMode     domain.RunExecutionPermissionMode
	DecisionReason     string
}

func (s *OnceCommandService) Execute(ctx context.Context, request OnceCommandRunRequest) (OnceCommandRunResult, error) {
	if s == nil || s.store == nil || s.executor == nil || !s.executor.Available() {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"once command service requires a store and an available executor")
	}
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	if strings.TrimSpace(mission.WorkspaceID) == "" {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"once command requires a registered Workspace")
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	spec := runner.OnceCommandSpec{
		ProtocolVersion:     runner.OnceCommandProtocolVersion,
		ExecutablePath:      request.ExecutablePath,
		Argv:                append([]string(nil), request.Argv...),
		WorkingDirectory:    request.WorkingDirectory,
		Environment:         append([]string(nil), request.Environment...),
		TimeoutMilliseconds: request.TimeoutMilliseconds,
		Purpose:             request.Purpose,
	}
	if err := runner.ValidateOnceCommandSpec(spec, workspace.RootPath); err != nil {
		return OnceCommandRunResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"once command boundary is invalid", err)
	}
	decision, err := executionauth.EvaluateExecutionPermission(permission, s.capabilities,
		executionauth.PermissionRequest{
			Kind:             executionauth.PermissionOperationStatelessCommand,
			HostFilesystem:   true,
			Network:          true,
			OperatorApproved: request.OperatorApproved,
		})
	if err != nil {
		return OnceCommandRunResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"once command permission evaluation failed", err)
	}
	if !decision.Allowed {
		return OnceCommandRunResult{}, apperror.New(apperror.CodePolicyDenied,
			"once command denied: "+decision.Reason)
	}
	requestFingerprint := runner.OnceCommandRequestFingerprint(run.ID, workspace.ID, spec)
	result, err := s.executor.Execute(ctx, runner.OnceCommandRequest{
		Spec: spec, RunID: run.ID, MissionID: mission.ID, WorkspaceID: workspace.ID,
		WorkspaceRoot: workspace.RootPath, RequestedBy: request.RequestedBy,
		OperatorApproved: request.OperatorApproved,
	})
	if err != nil && result.CompletedAt.IsZero() {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	if err == nil && result.CompletedAt.IsZero() {
		return OnceCommandRunResult{}, errors.New("once command executor returned no completion evidence")
	}
	duration := time.Duration(0)
	if !result.StartedAt.IsZero() && !result.CompletedAt.IsZero() {
		duration = result.CompletedAt.Sub(result.StartedAt)
	}
	_ = s.store.RecordOnceCommandExecution(ctx, run.ID, map[string]any{
		"request_fingerprint":   requestFingerprint,
		"permission_mode":       string(permission.Mode),
		"exit_code":             result.ExitCode,
		"stdout_observed_bytes": result.Stdout.ObservedBytes,
		"stderr_observed_bytes": result.Stderr.ObservedBytes,
		"output_truncated":      result.Stdout.Truncated || result.Stderr.Truncated,
		"timed_out":             result.TimedOut,
		"cancelled":             result.Cancelled,
		"tree_reaped":           result.TreeReaped,
		"duration_ms":           duration.Milliseconds(),
	})
	return OnceCommandRunResult{
		Result: result, SpecFingerprint: runner.OnceCommandSpecFingerprint(spec),
		RequestFingerprint: requestFingerprint, PermissionMode: permission.Mode,
		DecisionReason: decision.Reason,
	}, nil
}
