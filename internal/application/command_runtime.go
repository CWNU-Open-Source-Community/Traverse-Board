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
	"cyberagent-workbench/internal/workspacecheckpoint"
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
	checkpoints  *WorkspaceCheckpointService
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
		capabilities: capabilities,
		checkpoints:  embeddedWorkspaceCheckpointService(store, capabilities)}, nil
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
		boundaryRequest := s.commandRuntimeBoundaryRequest(scope,
			commandRuntimeWorkspaceBoundaryKey("foreground", scope.OperationKey),
			scope.InvocationID)
		if s.checkpoints != nil {
			if _, err := s.checkpoints.BeginBoundary(ctx, boundaryRequest); err != nil {
				return result, err
			}
		}
		value, runErr := s.runForeground(ctx, scope, input, bindings, result)
		boundaryCause := runErr
		if boundaryCause == nil {
			boundaryCause = commandRuntimeMutationResultError(value)
		}
		boundaryErr := s.completeCommandRuntimeBoundary(ctx, boundaryRequest,
			boundaryCause)
		return value, errors.Join(runErr, boundaryErr)
	case toolgateway.CommandRuntimeActionStart:
		if err := s.completeTerminalCommandRuntimeBoundaries(ctx, scope.RunID); err != nil {
			return result, err
		}
		resolved, err := runner.NormalizeCommandRuntimeSpec(input.Commands[0],
			bindings.workspace.RootPath)
		if err != nil {
			return result, commandRuntimeError(err)
		}
		operationDigest, jobID := runner.CommandRuntimeOperationIdentity(scope.RunID,
			scope.OperationKey)
		boundaryRequest := s.commandRuntimeBoundaryRequest(scope, operationDigest, jobID)
		if s.checkpoints != nil {
			if _, err := s.checkpoints.BeginBoundary(ctx, boundaryRequest); err != nil {
				return result, err
			}
		}
		job, replayed, err := s.manager.Start(ctx, runner.CommandRuntimeStartRequest{
			Scope: s.runnerScope(scope, bindings, scope.OperationKey), Spec: resolved})
		if err != nil {
			operationErr := commandRuntimeError(err)
			return result, errors.Join(operationErr,
				s.completeCommandRuntimeBoundary(ctx, boundaryRequest, operationErr))
		}
		result.Jobs = append(result.Jobs, job)
		result.Replayed = replayed
		if job.State.Terminal() {
			if boundaryErr := s.completeCommandRuntimeBoundary(ctx, boundaryRequest,
				commandRuntimeJobStateError(job.State)); boundaryErr != nil {
				return result, boundaryErr
			}
		}
		return result, nil
	case toolgateway.CommandRuntimeActionList:
		jobs, err := s.manager.List(ctx, runner.CommandRuntimeListFilter{
			RunID: scope.RunID, Limit: toolgateway.MaxCommandRuntimeResultJobs})
		result.Jobs = jobs
		return result, commandRuntimeError(err)
	case toolgateway.CommandRuntimeActionRead, toolgateway.CommandRuntimeActionWait:
		record, err := s.authorizeReadableJob(ctx, input.JobID, bindings)
		if err != nil {
			return result, err
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
			if err := s.completeCommandRuntimeJobBoundary(ctx, record); err != nil {
				return result, err
			}
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
		if job.State.Terminal() {
			record, getErr := s.store.GetCommandRuntimeJob(ctx, input.JobID)
			if getErr != nil {
				return result, commandRuntimeError(getErr)
			}
			if err := s.completeCommandRuntimeJobBoundary(ctx, record); err != nil {
				return result, err
			}
		}
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
			if err := s.completeCommandRuntimeJobBoundary(ctx, record); err != nil {
				return result, err
			}
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
		if job.State.Terminal() {
			record, getErr := s.store.GetCommandRuntimeJob(ctx, input.JobID)
			if getErr != nil {
				return result, commandRuntimeError(getErr)
			}
			if err := s.completeCommandRuntimeJobBoundary(ctx, record); err != nil {
				return result, err
			}
		}
		return result, nil
	default:
		return result, apperror.New(apperror.CodeInvalidArgument,
			"command runtime action is unsupported")
	}
}

// cleanupUIEvidenceJob is a cleanup-only capability for a Job that this
// process started for one sealed UI-evidence Attempt. It intentionally does
// not consult the current Run lease: expiry, cancellation, and revocation are
// precisely the states in which the Attempt must still be able to reap its own
// process tree. The full durable identity is checked before Stop, so this path
// cannot start, adopt, read, write to, or stop any other Job.
func (s *CommandRuntimeService) cleanupUIEvidenceJob(ctx context.Context,
	binding uiEvidenceCommandCleanupBinding,
) (runner.CommandRuntimeJobSnapshot, error) {
	if s == nil || s.store == nil || s.manager == nil || ctx == nil ||
		ctx.Err() != nil || binding.Validate() != nil {
		return runner.CommandRuntimeJobSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "UI evidence command cleanup binding is invalid")
	}
	record, err := s.store.GetCommandRuntimeJob(ctx, binding.JobID)
	if err != nil {
		return runner.CommandRuntimeJobSnapshot{}, commandRuntimeError(err)
	}
	if !uiEvidenceCommandCleanupMatches(record, binding) {
		return runner.CommandRuntimeJobSnapshot{}, apperror.New(
			apperror.CodeConflict, "UI evidence command cleanup binding is stale")
	}
	if record.State.Terminal() {
		if !record.TreeReaped {
			return runner.ProjectCommandRuntimeJob(record), apperror.New(
				apperror.CodeConflict, "UI evidence command process tree is not reaped")
		}
		if err := s.completeCommandRuntimeJobBoundary(ctx, record); err != nil {
			return runner.ProjectCommandRuntimeJob(record), err
		}
		return runner.ProjectCommandRuntimeJob(record), nil
	}
	if !s.manager.OwnsActiveJob(record) {
		return runner.ProjectCommandRuntimeJob(record), apperror.New(
			apperror.CodeConflict, "UI evidence command ownership is stale")
	}
	_, stopErr := s.manager.Stop(ctx, record.ID, true, 0)
	for {
		job, _, waitErr := s.manager.Wait(ctx, record.ID, 100*time.Millisecond,
			math.MaxUint64, runner.MinCommandRuntimeOutputRead)
		if waitErr != nil {
			return job, errors.Join(commandRuntimeError(stopErr),
				commandRuntimeError(waitErr))
		}
		if !job.State.Terminal() {
			continue
		}
		if !job.TreeReaped {
			return job, apperror.New(apperror.CodeConflict,
				"UI evidence command process tree is not reaped")
		}
		record, err = s.store.GetCommandRuntimeJob(ctx, binding.JobID)
		if err != nil {
			return job, commandRuntimeError(err)
		}
		if !uiEvidenceCommandCleanupMatches(record, binding) ||
			!record.State.Terminal() || !record.TreeReaped {
			return job, apperror.New(apperror.CodeConflict,
				"UI evidence command cleanup proof is stale")
		}
		if err := s.completeCommandRuntimeJobBoundary(ctx, record); err != nil {
			return runner.ProjectCommandRuntimeJob(record), err
		}
		return runner.ProjectCommandRuntimeJob(record), nil
	}
}

func uiEvidenceCommandCleanupMatches(job runner.CommandRuntimeJob,
	binding uiEvidenceCommandCleanupBinding,
) bool {
	expectedDigest, expectedID := runner.CommandRuntimeOperationIdentity(
		binding.RunID, binding.OperationKey)
	return binding.JobID == expectedID && job.ID == expectedID &&
		job.OperationDigest == expectedDigest &&
		job.InvocationID == binding.InvocationID && job.RunID == binding.RunID &&
		job.MissionID == binding.MissionID && job.SessionID == binding.SessionID &&
		job.WorkspaceID == binding.WorkspaceID && job.RootAgentID == binding.RootAgentID &&
		job.LeaseID == binding.LeaseID && job.LeaseGeneration == binding.LeaseGeneration
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

func (s *CommandRuntimeService) commandRuntimeBoundaryRequest(
	scope toolgateway.CommandRuntimeContext, operationKey, receiptID string,
) WorkspaceMutationBoundaryRequest {
	return WorkspaceMutationBoundaryRequest{RunID: scope.RunID,
		Kind: workspacecheckpoint.TransactionCommandBatch, OperationKey: operationKey,
		TriggerReceiptID: receiptID, InvocationID: scope.InvocationID,
		CapabilityGeneration: scope.CapabilityGeneration, LeaseID: scope.LeaseID,
		LeaseGeneration: scope.LeaseGeneration,
		IncompleteReasons: []string{
			"filesystem watcher attribution is unavailable; shell writes are inferred from bounded manifests and Git state",
		}}
}

func (s *CommandRuntimeService) completeCommandRuntimeBoundary(ctx context.Context,
	request WorkspaceMutationBoundaryRequest, cause error,
) error {
	if s == nil || s.checkpoints == nil {
		return nil
	}
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx),
		30*time.Second)
	defer cancel()
	_, err := s.checkpoints.CompleteBoundary(completionCtx, request, cause)
	return err
}

func (s *CommandRuntimeService) completeCommandRuntimeJobBoundary(ctx context.Context,
	job runner.CommandRuntimeJob,
) error {
	if s == nil || s.checkpoints == nil || !job.State.Terminal() {
		return nil
	}
	operationDigest := workspaceBoundaryOperationDigest(job.RunID,
		workspacecheckpoint.TransactionCommandBatch, job.OperationDigest)
	if _, found, err := s.checkpoints.store.GetWorkspaceCheckpointTransactionByOperation(ctx,
		operationDigest); err != nil {
		return apperror.Normalize(err)
	} else if !found {
		// Foreground batch Jobs are covered by one batch-level boundary. Only
		// background Jobs have a per-Job boundary keyed by OperationDigest.
		return nil
	}
	return s.completeCommandRuntimeBoundary(ctx, WorkspaceMutationBoundaryRequest{
		RunID: job.RunID, Kind: workspacecheckpoint.TransactionCommandBatch,
		OperationKey: job.OperationDigest, TriggerReceiptID: job.ID,
		InvocationID: job.InvocationID}, commandRuntimeJobStateError(job.State))
}

func (s *CommandRuntimeService) completeTerminalCommandRuntimeBoundaries(ctx context.Context,
	runID string,
) error {
	if s == nil || s.checkpoints == nil {
		return nil
	}
	jobs, err := s.store.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{RunID: runID, Limit: 500})
	if err != nil {
		return commandRuntimeError(err)
	}
	for _, job := range jobs {
		if job.State.Terminal() {
			if err := s.completeCommandRuntimeJobBoundary(ctx, job); err != nil {
				return err
			}
		}
	}
	return nil
}

func commandRuntimeMutationResultError(
	result toolgateway.CommandRuntimeExecutionResult,
) error {
	for _, job := range result.Jobs {
		if err := commandRuntimeJobStateError(job.State); err != nil {
			return err
		}
	}
	return nil
}

func commandRuntimeJobStateError(state runner.CommandRuntimeJobState) error {
	if state == runner.CommandRuntimeJobCompleted {
		return nil
	}
	return fmt.Errorf("command runtime workspace mutation ended in state %s", state)
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

// authorizeReadableJob closes the race between observing an active durable Job
// and verifying its process-local owner. A short-lived command may complete in
// that interval; terminal output remains readable after its Run identity is
// authorized, while a still-active Job must retain exact process ownership.
func (s *CommandRuntimeService) authorizeReadableJob(ctx context.Context, jobID string,
	bindings commandRuntimeBindings,
) (runner.CommandRuntimeJob, error) {
	job, err := s.authorizeJob(ctx, jobID, bindings)
	if err != nil || job.State.Terminal() {
		return job, err
	}
	active, activeErr := s.authorizeActiveJob(ctx, jobID, bindings)
	if activeErr == nil {
		return active, nil
	}
	refreshed, refreshErr := s.authorizeJob(ctx, jobID, bindings)
	if refreshErr == nil && refreshed.State.Terminal() {
		return refreshed, nil
	}
	return runner.CommandRuntimeJob{}, activeErr
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
		runner.CommandRuntimeListFilter{Limit: 500})
	if err != nil {
		return 0, commandRuntimeError(err)
	}
	stopped := reconciled
	for _, job := range jobs {
		if job.State.Terminal() {
			if completeErr := s.completeCommandRuntimeJobBoundary(ctx, job); completeErr != nil {
				return stopped, completeErr
			}
			continue
		}
		if !s.manager.OwnsActiveJob(job) {
			continue
		}
		current, bindErr := s.commandRuntimeJobBindingsCurrent(ctx, job)
		if bindErr != nil {
			return stopped, bindErr
		}
		if !current {
			stoppedJob, stopErr := s.manager.Stop(context.WithoutCancel(ctx), job.ID,
				true, 0)
			if stopErr == nil {
				stopped++
				if stoppedJob.State.Terminal() {
					record, getErr := s.store.GetCommandRuntimeJob(ctx, job.ID)
					if getErr != nil {
						return stopped, commandRuntimeError(getErr)
					}
					if completeErr := s.completeCommandRuntimeJobBoundary(ctx,
						record); completeErr != nil {
						return stopped, completeErr
					}
				}
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

func commandRuntimeWorkspaceBoundaryKey(action, operationKey string) string {
	digest := sha256.Sum256([]byte("command-runtime-workspace-boundary.v1\x00" +
		action + "\x00" + operationKey))
	return hex.EncodeToString(digest[:])
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
