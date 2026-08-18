package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

type CommandRuntimeStore interface {
	runner.CommandRuntimeStore
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	GetRootAgent(context.Context, string) (domain.AgentNode, bool, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionProfile(context.Context, string) (
		domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionLease(context.Context, string) (
		domain.RunExecutionLease, bool, error)
}

type CommandRuntimeService struct {
	store        CommandRuntimeStore
	manager      *runner.CommandRuntimeManager
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

type commandRuntimeBindings struct {
	run        domain.Run
	mission    domain.Mission
	workspace  session.WorkspaceRecord
	root       domain.AgentNode
	mode       domain.RunModeSnapshot
	profile    domain.RunExecutionProfileSnapshot
	permission domain.RunExecutionPermissionSnapshot
	lease      domain.RunExecutionLease
	rootSHA256 string
}

func NewCommandRuntimeService(store CommandRuntimeStore,
	manager *runner.CommandRuntimeManager,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) (*CommandRuntimeService, error) {
	if store == nil || manager == nil || !manager.Available() ||
		capabilities.Validate() != nil ||
		!capabilities.Allows(domain.RunExecutionPermissionFullAccess) {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"command runtime requires the danger-full-access startup gate")
	}
	return &CommandRuntimeService{store: store, manager: manager,
		capabilities: capabilities}, nil
}

func (s *CommandRuntimeService) ExecuteCommandRuntime(ctx context.Context,
	scope toolgateway.CommandRuntimeContext, input toolgateway.CommandRuntimeInput,
) (toolgateway.CommandRuntimeExecutionResult, error) {
	if s == nil || s.store == nil || s.manager == nil || ctx == nil ||
		ctx.Err() != nil || scope.Validate() != nil || input.Validate() != nil {
		return toolgateway.CommandRuntimeExecutionResult{}, apperror.New(
			apperror.CodeInvalidArgument, "command runtime request is invalid")
	}
	bindings, err := s.loadAuthorizedBindings(ctx, scope)
	if err != nil {
		return toolgateway.CommandRuntimeExecutionResult{}, err
	}
	result := toolgateway.CommandRuntimeExecutionResult{
		Backend: "run_owned_command_runtime", Action: input.Action,
		Jobs:      []runner.CommandRuntimeJobSnapshot{},
		Pages:     []runner.CommandRuntimeOutputPage{},
		Artifacts: []toolgateway.CommandRuntimeArtifactOutput{},
	}
	switch input.Action {
	case toolgateway.CommandRuntimeActionRun:
		return s.runForeground(ctx, scope, input, bindings, result)
	case toolgateway.CommandRuntimeActionStart:
		resolved, err := runner.NormalizeCommandRuntimeSpec(input.Commands[0],
			bindings.workspace.RootPath)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		job, replayed, err := s.manager.Start(ctx, runner.CommandRuntimeStartRequest{
			Scope: s.runnerScope(scope, bindings, scope.OperationKey), Spec: resolved})
		if err != nil {
			return result, commandRuntimeError(err)
		}
		result.Jobs = append(result.Jobs, job)
		result.Replayed = replayed
		return result, nil
	case toolgateway.CommandRuntimeActionList:
		jobs, err := s.manager.List(ctx, runner.CommandRuntimeListFilter{
			RunID: scope.RunID, Limit: toolgateway.MaxCommandRuntimeResultJobs})
		result.Jobs = jobs
		return result, commandRuntimeError(err)
	case toolgateway.CommandRuntimeActionRead, toolgateway.CommandRuntimeActionWait:
		record, err := s.authorizeJob(ctx, input.JobID, bindings)
		if err != nil {
			return result, err
		}
		if !record.State.Terminal() {
			if record, err = s.authorizeActiveJob(ctx, input.JobID, bindings); err != nil {
				return result, err
			}
		}
		job, page, err := s.manager.Wait(ctx, input.JobID,
			time.Duration(*input.WaitMilliseconds)*time.Millisecond,
			*input.Cursor, *input.MaxBytes)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		result.Jobs = append(result.Jobs, job)
		result.Pages = append(result.Pages, page)
		if job.State.Terminal() {
			record, err = s.store.GetCommandRuntimeJob(ctx, input.JobID)
			if err != nil {
				return result, commandRuntimeError(err)
			}
			appendCommandRuntimeArtifact(&result, record)
		}
		return result, nil
	case toolgateway.CommandRuntimeActionWriteStdin:
		if _, err := s.authorizeActiveJob(ctx, input.JobID, bindings); err != nil {
			return result, err
		}
		job, _, replayed, err := s.manager.WriteStdin(ctx, input.JobID,
			scope.OperationKey, []byte(*input.Stdin), *input.CloseStdin)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		result.Jobs = append(result.Jobs, job)
		result.Replayed = replayed
		return result, nil
	case toolgateway.CommandRuntimeActionCancel, toolgateway.CommandRuntimeActionKill:
		record, err := s.authorizeJob(ctx, input.JobID, bindings)
		if err != nil {
			return result, err
		}
		if record.State.Terminal() {
			result.Jobs = append(result.Jobs, runner.ProjectCommandRuntimeJob(record))
			result.Replayed = true
			appendCommandRuntimeArtifact(&result, record)
			return result, nil
		}
		if _, err := s.authorizeActiveJob(ctx, input.JobID, bindings); err != nil {
			return result, err
		}
		kill := input.Action == toolgateway.CommandRuntimeActionKill
		grace := time.Duration(0)
		if input.WaitMilliseconds != nil {
			grace = time.Duration(*input.WaitMilliseconds) * time.Millisecond
		}
		job, err := s.manager.Stop(ctx, input.JobID, kill, grace)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		result.Jobs = append(result.Jobs, job)
		return result, nil
	default:
		return result, apperror.New(apperror.CodeInvalidArgument,
			"command runtime action is unsupported")
	}
}

func (s *CommandRuntimeService) runForeground(ctx context.Context,
	scope toolgateway.CommandRuntimeContext, input toolgateway.CommandRuntimeInput,
	bindings commandRuntimeBindings, result toolgateway.CommandRuntimeExecutionResult,
) (toolgateway.CommandRuntimeExecutionResult, error) {
	resolvedCommands := make([]runner.CommandRuntimeResolvedSpec, len(input.Commands))
	for index, command := range input.Commands {
		resolved, err := runner.NormalizeCommandRuntimeSpec(command,
			bindings.workspace.RootPath)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		resolvedCommands[index] = resolved
	}
	for index, resolved := range resolvedCommands {
		operationKey := commandRuntimeBatchOperationKey(scope.OperationKey, index)
		job, replayed, err := s.manager.Start(ctx, runner.CommandRuntimeStartRequest{
			Scope: s.runnerScope(scope, bindings, operationKey), Spec: resolved})
		if err != nil {
			return result, commandRuntimeError(err)
		}
		result.Replayed = result.Replayed || replayed
		jobID := job.ID
		job, err = s.waitForTerminal(ctx, jobID)
		if err != nil {
			return result, errors.Join(err, commandRuntimeError(
				s.cancelForegroundJob(jobID)))
		}
		job, page, err := s.manager.Wait(ctx, job.ID, 0, 0, *input.MaxBytes)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		result.Jobs = append(result.Jobs, job)
		result.Pages = append(result.Pages, page)
		record, err := s.store.GetCommandRuntimeJob(ctx, job.ID)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		appendCommandRuntimeArtifact(&result, record)
		if job.State != runner.CommandRuntimeJobCompleted &&
			input.FailurePolicy == toolgateway.CommandRuntimeFailFast {
			break
		}
	}
	return result, nil
}

func (s *CommandRuntimeService) cancelForegroundJob(jobID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(),
		runner.MaxCommandRuntimeCancelGrace+2*time.Second)
	defer cancel()
	if _, err := s.manager.Stop(cleanupCtx, jobID, false, 0); err != nil {
		return err
	}
	for {
		job, _, err := s.manager.Wait(cleanupCtx, jobID, runner.MaxCommandRuntimeWait,
			math.MaxUint64, runner.MinCommandRuntimeOutputRead)
		if err != nil {
			return err
		}
		if job.State.Terminal() {
			return nil
		}
	}
}

func (s *CommandRuntimeService) waitForTerminal(ctx context.Context,
	jobID string,
) (runner.CommandRuntimeJobSnapshot, error) {
	for {
		job, _, err := s.manager.Wait(ctx, jobID, runner.MaxCommandRuntimeWait,
			math.MaxUint64, runner.MinCommandRuntimeOutputRead)
		if err != nil {
			return runner.CommandRuntimeJobSnapshot{}, commandRuntimeError(err)
		}
		if job.State.Terminal() {
			return job, nil
		}
	}
}

func (s *CommandRuntimeService) loadAuthorizedBindings(ctx context.Context,
	scope toolgateway.CommandRuntimeContext,
) (commandRuntimeBindings, error) {
	var value commandRuntimeBindings
	if err := s.capabilities.Validate(); err != nil ||
		!s.capabilities.Allows(domain.RunExecutionPermissionFullAccess) {
		return value, apperror.New(apperror.CodePolicyDenied,
			"command runtime startup capability is disabled")
	}
	var err error
	if value.run, err = s.store.GetRun(ctx, scope.RunID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.mission, err = s.store.GetMission(ctx, value.run.MissionID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.workspace, err = s.store.GetWorkspaceByID(ctx,
		value.mission.WorkspaceID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.root, _, err = s.store.GetRootAgent(ctx, value.run.ID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.mode, err = s.store.GetRunMode(ctx, value.run.ID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.profile, err = s.store.GetRunExecutionProfile(ctx,
		value.run.ID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.permission, err = s.store.GetRunExecutionPermission(ctx,
		value.run.ID); err != nil {
		return value, apperror.Normalize(err)
	}
	var found bool
	if value.lease, found, err = s.store.GetRunExecutionLease(ctx,
		value.run.ID); err != nil {
		return value, apperror.Normalize(err)
	}
	if value.rootSHA256, err = runner.CommandRuntimeWorkspaceRootSHA256(
		value.workspace.RootPath); err != nil {
		return value, commandRuntimeError(err)
	}
	if !found || value.run.Terminal() || value.run.Status != domain.RunRunning ||
		value.run.ID != scope.RunID || value.run.SessionID != scope.SessionID ||
		value.run.MissionID != value.mission.ID ||
		value.mission.WorkspaceID != scope.WorkspaceID ||
		value.workspace.ID != scope.WorkspaceID || value.root.ID != scope.RootAgentID ||
		value.root.ParentID != "" || value.root.Role != domain.AgentRoleRoot ||
		value.mode.RunID != value.run.ID || value.mode.MissionID != value.mission.ID ||
		value.mode.Surface != domain.ExecutionSurfaceCode ||
		value.mode.Phase != domain.ExecutionPhaseDeliver ||
		value.profile.RunID != value.run.ID ||
		value.profile.MissionID != value.mission.ID ||
		value.profile.Profile != domain.RunExecutionProfileLocal ||
		value.profile.NetworkScope != domain.ExecutionNetworkDisabled ||
		value.permission.RunID != value.run.ID ||
		value.permission.MissionID != value.mission.ID ||
		value.permission.Mode != domain.RunExecutionPermissionFullAccess ||
		value.lease.LeaseID != scope.LeaseID ||
		value.lease.Generation != scope.LeaseGeneration ||
		value.lease.Status != domain.RunExecutionLeaseActive ||
		!value.lease.ExpiresAt.After(time.Now().UTC()) {
		return value, apperror.New(apperror.CodeConflict,
			"command runtime durable binding is stale")
	}
	decision, err := executionauth.EvaluateExecutionPermission(value.permission,
		s.capabilities, executionauth.PermissionRequest{
			Kind:           executionauth.PermissionOperationManagedCommand,
			HostFilesystem: true, Network: false, BackgroundProcess: true,
		})
	if err != nil {
		return value, apperror.Wrap(apperror.CodeInvalidArgument,
			"command runtime permission request is invalid", err)
	}
	if !decision.Allowed || !decision.HostFilesystem || decision.Network ||
		!decision.BackgroundProcess || decision.PersistentTerminal ||
		decision.AgentTerminalInput {
		return value, apperror.New(apperror.CodePolicyDenied,
			"command runtime is not authorized by the current permission gate")
	}
	return value, nil
}

func (s *CommandRuntimeService) runnerScope(scope toolgateway.CommandRuntimeContext,
	bindings commandRuntimeBindings, operationKey string,
) runner.CommandRuntimeScope {
	return runner.CommandRuntimeScope{InvocationID: scope.InvocationID,
		OperationKey: operationKey, RunID: bindings.run.ID,
		MissionID: bindings.mission.ID, RootAgentID: bindings.root.ID,
		SessionID: bindings.run.SessionID, WorkspaceID: bindings.workspace.ID,
		WorkspaceRootSHA256: bindings.rootSHA256,
		ModeSnapshotID:      bindings.mode.ID, ModeRevision: bindings.mode.Revision,
		ProfileSnapshotID:    bindings.profile.ID,
		ProfileRevision:      bindings.profile.Revision,
		PermissionSnapshotID: bindings.permission.ID,
		PermissionRevision:   bindings.permission.Revision,
		PermissionMode:       bindings.permission.Mode, LeaseID: bindings.lease.LeaseID,
		LeaseGeneration: bindings.lease.Generation,
		LeaseOwnerID:    bindings.lease.OwnerID}
}

func (s *CommandRuntimeService) authorizeJob(ctx context.Context, jobID string,
	bindings commandRuntimeBindings,
) (runner.CommandRuntimeJob, error) {
	job, err := s.store.GetCommandRuntimeJob(ctx, jobID)
	if err != nil {
		return runner.CommandRuntimeJob{}, commandRuntimeError(err)
	}
	if job.RunID != bindings.run.ID || job.MissionID != bindings.mission.ID ||
		job.SessionID != bindings.run.SessionID ||
		job.WorkspaceID != bindings.workspace.ID || job.RootAgentID != bindings.root.ID {
		return runner.CommandRuntimeJob{}, apperror.New(apperror.CodeNotFound,
			"command runtime job was not found in this Run")
	}
	return job, nil
}

func (s *CommandRuntimeService) authorizeActiveJob(ctx context.Context, jobID string,
	bindings commandRuntimeBindings,
) (runner.CommandRuntimeJob, error) {
	job, err := s.authorizeJob(ctx, jobID, bindings)
	if err != nil {
		return runner.CommandRuntimeJob{}, err
	}
	if job.State.Terminal() || job.WorkspaceRootSHA256 != bindings.rootSHA256 ||
		job.ModeSnapshotID != bindings.mode.ID || job.ModeRevision != bindings.mode.Revision ||
		job.ProfileSnapshotID != bindings.profile.ID ||
		job.ProfileRevision != bindings.profile.Revision ||
		job.PermissionSnapshotID != bindings.permission.ID ||
		job.PermissionRevision != bindings.permission.Revision ||
		!s.manager.OwnsActiveJob(job) {
		return runner.CommandRuntimeJob{}, apperror.New(apperror.CodeConflict,
			"command runtime job ownership is stale")
	}
	return job, nil
}

func (s *CommandRuntimeService) Reconcile(ctx context.Context) (int, error) {
	if s == nil || s.store == nil || s.manager == nil {
		return 0, apperror.New(apperror.CodeFailedPrecondition,
			"command runtime service is unavailable")
	}
	reconciled, err := s.manager.ReconcileStartup(ctx)
	if err != nil {
		return 0, commandRuntimeError(err)
	}
	jobs, err := s.store.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{ActiveOnly: true, Limit: 500})
	if err != nil {
		return 0, commandRuntimeError(err)
	}
	stopped := reconciled
	for _, job := range jobs {
		if !s.manager.OwnsActiveJob(job) {
			continue
		}
		current, bindErr := s.commandRuntimeJobBindingsCurrent(ctx, job)
		if bindErr != nil {
			return stopped, bindErr
		}
		if !current {
			if _, stopErr := s.manager.Stop(context.WithoutCancel(ctx), job.ID,
				true, 0); stopErr == nil {
				stopped++
			}
		}
	}
	return stopped, nil
}

func (s *CommandRuntimeService) commandRuntimeJobBindingsCurrent(ctx context.Context,
	job runner.CommandRuntimeJob,
) (bool, error) {
	if err := s.capabilities.Validate(); err != nil ||
		!s.capabilities.Allows(domain.RunExecutionPermissionFullAccess) {
		return false, nil
	}
	runRecord, err := s.store.GetRun(ctx, job.RunID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	root, found, err := s.store.GetRootAgent(ctx, runRecord.ID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	mode, err := s.store.GetRunMode(ctx, runRecord.ID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, runRecord.ID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	rootSHA256, err := runner.CommandRuntimeWorkspaceRootSHA256(workspace.RootPath)
	if err != nil {
		return false, nil
	}
	return found && !runRecord.Terminal() && runRecord.Status == domain.RunRunning &&
		runRecord.MissionID == job.MissionID && runRecord.SessionID == job.SessionID &&
		mission.ID == job.MissionID && mission.WorkspaceID == job.WorkspaceID &&
		workspace.ID == job.WorkspaceID && root.ID == job.RootAgentID &&
		root.ParentID == "" && root.Role == domain.AgentRoleRoot &&
		rootSHA256 == job.WorkspaceRootSHA256 &&
		mode.ID == job.ModeSnapshotID && mode.Revision == job.ModeRevision &&
		mode.Surface == domain.ExecutionSurfaceCode &&
		mode.Phase == domain.ExecutionPhaseDeliver &&
		profile.ID == job.ProfileSnapshotID && profile.Revision == job.ProfileRevision &&
		profile.Profile == domain.RunExecutionProfileLocal &&
		profile.NetworkScope == domain.ExecutionNetworkDisabled &&
		permission.ID == job.PermissionSnapshotID &&
		permission.Revision == job.PermissionRevision &&
		permission.Mode == domain.RunExecutionPermissionFullAccess, nil
}

func (s *CommandRuntimeService) Shutdown(ctx context.Context) error {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Shutdown(ctx)
}

func (s *CommandRuntimeService) RunReconciler(ctx context.Context,
	interval time.Duration,
) error {
	if s == nil || s.manager == nil || interval < 100*time.Millisecond ||
		interval > time.Minute || ctx == nil {
		return apperror.New(apperror.CodeInvalidArgument,
			"command runtime reconciliation interval is invalid")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(),
				runner.MaxCommandRuntimeCancelGrace+time.Second)
			_ = s.manager.Shutdown(shutdownCtx)
			cancel()
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func appendCommandRuntimeArtifact(result *toolgateway.CommandRuntimeExecutionResult,
	job runner.CommandRuntimeJob,
) {
	if result == nil || !job.State.Terminal() ||
		(job.Stdout == "" && job.Stderr == "") {
		return
	}
	result.Artifacts = append(result.Artifacts,
		toolgateway.CommandRuntimeArtifactOutput{JobID: job.ID,
			Stdout: job.Stdout, Stderr: job.Stderr})
}

func commandRuntimeBatchOperationKey(operationKey string, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("command-runtime-batch.v2:%d:%s",
		index, operationKey)))
	return "command-runtime-" + hex.EncodeToString(digest[:])
}

func commandRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, runner.ErrCommandRuntimeBoundary):
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"command runtime boundary rejected the request", err)
	case errors.Is(err, runner.ErrCommandRuntimeJobNotFound):
		return apperror.Wrap(apperror.CodeNotFound,
			"command runtime job was not found", err)
	case errors.Is(err, runner.ErrCommandRuntimeJobClosed),
		errors.Is(err, runner.ErrCommandRuntimeUnavailable):
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"command runtime is unavailable for this operation", err)
	case errors.Is(err, runner.ErrCommandRuntimeUncertain):
		return apperror.Wrap(apperror.CodeConflict,
			"command runtime replay is uncertain", err)
	default:
		return apperror.Normalize(err)
	}
}

var _ toolgateway.CommandRuntimeExecutor = (*CommandRuntimeService)(nil)
