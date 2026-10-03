package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

// OperatorCommandRequest carries the operator's exact command. Lease, actor,
// process epoch and native input pins are resolved by the host, never by input.
type OperatorCommandRequest struct {
	RunID, OperationKey, RequestedBy string
	Command                          runner.CommandRuntimeSpec
	ConfirmExecution                 bool
}

type OperatorCommandResult struct {
	Job      runner.CommandRuntimeJob
	Replayed bool
}

type operatorCommandApprovalKey struct{}
type operatorCommandApproval struct {
	confirmed                                                       bool
	input                                                           toolgateway.CommandRuntimeInput
	host                                                            *CommandRuntimeService
	invocationID, operationKey, bindingFingerprint, specFingerprint string
}

// RunOperatorCommand is a foreground adapter to the existing command runtime.
// A terminal receipt is readable without fresh authority. Any previous intent
// without a conclusive result is never turned into another native start.
func (s *CommandRuntimeService) RunOperatorCommand(ctx context.Context, request OperatorCommandRequest) (OperatorCommandResult, error) {
	var result OperatorCommandResult
	if s == nil || s.store == nil {
		return result, apperror.New(apperror.CodeFailedPrecondition, "operator command store is unavailable")
	}
	prepared, err := normalizeOperatorCommandRequest(request)
	if err != nil {
		return result, err
	}
	if receipt, found, err := readOperatorCommandReceipt(ctx, s.store, prepared); found || err != nil {
		return receipt, err
	}
	spec, operationKey, invocationID := prepared.spec, prepared.operationKey, prepared.invocationID
	leaseStore, ok := s.store.(RunExecutionLeaseStore)
	if !ok || s.adapter.Kind != commandruntimeadapter.KindHostUnsandboxed || s.capabilities.RuntimeAuthority == nil {
		return result, apperror.New(apperror.CodeFailedPrecondition, "operator command requires the host command runtime and Run lease service")
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return result, err
	}
	if run.Status != domain.RunRunning {
		return result, apperror.New(apperror.CodeFailedPrecondition, "new operator commands require a Running Run; no Run state was changed")
	}
	err = withRunExecutionLease(ctx, leaseStore, run.ID, idgen.New("operator-command"), DefaultRunExecutionLeasePolicy(), func(leaseCtx context.Context, lease domain.RunExecutionLease) error {
		mission, err := s.store.GetMission(leaseCtx, run.MissionID)
		if err != nil {
			return err
		}
		root, found, err := s.store.GetRootAgent(leaseCtx, run.ID)
		if err != nil || !found {
			return errors.Join(err, errors.New("operator command Root authority anchor is unavailable"))
		}
		permission, err := s.store.GetRunExecutionPermission(leaseCtx, run.ID)
		if err != nil {
			return err
		}
		if !permission.Mode.IsApprovalMode() {
			return apperror.New(apperror.CodePolicyDenied, "new operator commands require ask, auto, or full")
		}
		snapshot, generation, epoch, fence, live := bindAgentCodeRuntime(s.capabilities, permission)
		if !live {
			return apperror.New(apperror.CodePolicyDenied, "operator command runtime authority is unavailable")
		}
		scope := toolgateway.CommandRuntimeContext{InvocationID: invocationID, OperationKey: operationKey,
			RunID: run.ID, MissionID: mission.ID, SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID,
			RootAgentID: root.ID, AgentID: root.ID, PermissionMode: permission.Mode, PermissionRevision: permission.Revision,
			PermissionSnapshotID: snapshot, PermissionGeneration: generation, PermissionRuntimeEpoch: epoch, RunAuthorizationFence: fence,
			LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation, CapabilityGeneration: s.adapter.Generation,
			RequestedBy: toolgateway.CommandRuntimeRequestedByOperator, Adapter: s.adapter,
			PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "operator command still requires native operation authorization"}}
		mode, err := s.store.GetRunMode(leaseCtx, run.ID)
		if err != nil {
			return err
		}
		scope.Surface, scope.Phase, scope.Profile, scope.Role, scope.ModeRevision = mode.Surface, mode.Phase, mode.Profile, root.Role, mode.Revision
		bindings, err := s.loadAuthorizedBindings(leaseCtx, scope, spec.Network == runner.CommandRuntimeNetworkHost)
		if err != nil {
			return err
		}
		resolved, err := s.normalizeCommandRuntimeSpec(spec, bindings.rootPath)
		if err != nil {
			return err
		}
		limit := spec.Output.InlineBytes
		input := toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionRun, Commands: []runner.CommandRuntimeSpec{spec}, FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &limit}
		binding, err := commandOperationBindingFingerprint(s.runnerScope(scope, bindings, scope.OperationKey))
		if err != nil {
			return err
		}
		// Host evidence is private to this invocation and binds the original
		// action as well as native input pins. Public scopes cannot fabricate it.
		leaseCtx = context.WithValue(leaseCtx, operatorCommandApprovalKey{}, operatorCommandApproval{host: s,
			invocationID: invocationID, operationKey: operationKey, bindingFingerprint: binding,
			specFingerprint: runner.CommandRuntimeSpecFingerprint(resolved), input: input, confirmed: request.ConfirmExecution})
		// Reuse the same operation check before preparing a Job as at the native
		// sink. A missing operator review must not consume the operation key.
		start, err := s.authorizedCommandStart(scope, bindings, commandRuntimeBatchOperationKey(scope.OperationKey, 0), resolved)
		if err != nil {
			return err
		}
		if err = start.DispatchCheck(leaseCtx, resolved); err != nil {
			return err
		}
		value, err := s.ExecuteCommandRuntime(leaseCtx, scope, input)
		if len(value.Jobs) == 1 {
			job, readErr := s.store.GetCommandRuntimeJob(context.WithoutCancel(leaseCtx), value.Jobs[0].ID)
			result = OperatorCommandResult{Job: job, Replayed: value.Replayed}
			err = errors.Join(err, readErr)
		}
		return err
	})
	return result, err
}

type operatorCommandReceiptStore interface {
	GetCommandRuntimeJob(context.Context, string) (runner.CommandRuntimeJob, error)
}

type preparedOperatorCommand struct {
	spec                                                      runner.CommandRuntimeSpec
	runID, operationKey, invocationID, operationDigest, jobID string
}

func normalizeOperatorCommandRequest(request OperatorCommandRequest) (preparedOperatorCommand, error) {
	var prepared preparedOperatorCommand
	if !domain.ValidAgentID(request.RunID) || !domain.ValidAgentID(request.OperationKey) || !domain.ValidAgentID(request.RequestedBy) {
		return prepared, apperror.New(apperror.CodeInvalidArgument, "operator command identity is invalid")
	}
	if request.Command.StdinPolicy != runner.CommandRuntimeStdinClosed || request.Command.InitialStdin != "" {
		return prepared, apperror.New(apperror.CodeInvalidArgument, "operator command requires closed stdin")
	}
	var err error
	prepared.spec, err = runner.NormalizeCommandRuntimeIntent(request.Command)
	if err != nil {
		return prepared, err
	}
	raw, err := json.Marshal(prepared.spec)
	if err != nil {
		return prepared, err
	}
	prepared.runID = request.RunID
	prepared.operationKey = "operator-command-" + runmutation.Fingerprint("operator_command_key.v1", request.RunID, request.OperationKey)
	prepared.invocationID = "operator-command-" + runmutation.Fingerprint("operator_command_request.v1", request.RunID, request.OperationKey, request.RequestedBy, string(raw))
	prepared.operationDigest, prepared.jobID = runner.CommandRuntimeOperationIdentity(request.RunID, commandRuntimeBatchOperationKey(prepared.operationKey, 0))
	return prepared, nil
}

// ReadOperatorCommand reads an exact saved command without a runtime adapter,
// lease, executable file or fresh permission activation. It grants no authority.
func ReadOperatorCommand(ctx context.Context, store operatorCommandReceiptStore, request OperatorCommandRequest) (OperatorCommandResult, bool, error) {
	prepared, err := normalizeOperatorCommandRequest(request)
	if err != nil {
		return OperatorCommandResult{}, false, err
	}
	return readOperatorCommandReceipt(ctx, store, prepared)
}

func readOperatorCommandReceipt(ctx context.Context, store operatorCommandReceiptStore, request preparedOperatorCommand) (OperatorCommandResult, bool, error) {
	if ctx == nil || store == nil {
		return OperatorCommandResult{}, false, apperror.New(apperror.CodeInvalidArgument, "operator command receipt reader is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return OperatorCommandResult{}, false, err
	}
	job, err := store.GetCommandRuntimeJob(ctx, request.jobID)
	if errors.Is(err, runner.ErrCommandRuntimeJobNotFound) || errors.Is(err, sql.ErrNoRows) || apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeNotFound {
		return OperatorCommandResult{}, false, nil
	}
	if err != nil {
		return OperatorCommandResult{}, false, err
	}
	if job.RunID != request.runID || job.OperationDigest != request.operationDigest || job.InvocationID != request.invocationID || job.Validate() != nil {
		return OperatorCommandResult{}, true, apperror.New(apperror.CodeConflict, "operator command key was already used for another request")
	}
	result := OperatorCommandResult{Job: job, Replayed: true}
	if !job.State.Terminal() || job.State == runner.CommandRuntimeJobInterrupted {
		return result, true, runner.ErrCommandRuntimeUncertain
	}
	return result, true, nil
}
