package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

type cliWebFetchContinuationProjection struct {
	state         string
	replayed      bool
	resume        bool
	modelAttempts int
	toolCalls     int
	runStatus     domain.RunStatus
	errorCode     string
}

// The review invocation owns its continuation until the next product boundary.
// There is no worker left behind when the CLI process exits. Runtime support
// here never changes the Run's durable permission selection.
func (a *App) newCLIApprovalExecution(ctx context.Context, runID string) (
	*application.RunExecutionHandoffService, func() error, error,
) {
	drydocks, err := a.newRunFileDrydockService(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	permission, err := a.store.GetRunExecutionPermission(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	matrix, err := permission.CapabilityMatrix()
	if err != nil {
		return nil, nil, err
	}
	var runtime *application.CommandRuntimeService
	closeRuntime := func() error { return nil }
	if matrix.SandboxedCommandRuntime {
		commandManager, commandRuntime, runtimeErr := a.newCLICommandRuntime(ctx, true, true, false)
		if runtimeErr != nil {
			return nil, nil, runtimeErr
		}
		runtime = commandRuntime
		stop := a.startCLICommandRuntimeReconciler(ctx, runtime)
		closeRuntime = func() error {
			return errors.Join(stop(), shutdownCLICommandRuntime(commandManager))
		}
	}
	handoff := application.NewRunExecutionHandoffService(a.store, a.router, a.checker).
		WithActiveCalls(a.calls).WithDrydock(drydocks).
		WithWebEvidence(a.newWebEvidenceService()).WithWebFetchAuthorizationScheduler(true).
		WithExecutionPermissionCapabilities(cliExecutionPermissionCapabilities(true, true, false))
	if runtime != nil {
		handoff.WithCommandRuntime(runtime)
	}
	if _, configured, err := a.store.GetConfiguredStandardCodePresetOperation(ctx, runID); err != nil {
		return nil, nil, errors.Join(err, closeRuntime())
	} else if configured {
		delivery, err := application.NewStandardCodeDeliveryService(a.store, drydocks)
		if err != nil {
			return nil, nil, errors.Join(err, closeRuntime())
		}
		handoff.WithStandardCodeDelivery(delivery)
	}
	if client := a.newMCPClientManager(); client != nil {
		handoff.WithMCPClient(client)
	}
	if engine := a.newLifecycleHookEngine(); engine != nil {
		handoff.WithLifecycleHooks(engine)
	}
	if a.codeIntel != nil {
		handoff.WithCodeIntel(a.codeIntel)
	}
	return handoff, closeRuntime, nil
}

func (a *App) projectCLIWebFetchContinuation(ctx context.Context,
	value domain.WebFetchAuthorization,
) (cliWebFetchContinuationProjection, error) {
	run, err := a.store.GetRun(ctx, value.RunID)
	if err != nil {
		return cliWebFetchContinuationProjection{}, err
	}
	projection := cliWebFetchContinuationProjection{state: "not_started",
		runStatus: run.Status}

	// The handoff operation is bound to the original model attempt. Locate that
	// durable call rather than inferring completion from the Run's current state.
	const pageSize = 101
	for offset := 0; ; offset += pageSize {
		rounds, pageErr := a.store.ListRunSupervisorToolRoundsPage(ctx, value.RunID,
			offset, pageSize)
		if pageErr != nil {
			return projection, pageErr
		}
		for _, round := range rounds {
			if round.Turn != value.SupervisorTurn {
				continue
			}
			for _, call := range round.Calls {
				if call.CallID != value.SupervisorToolCallID || call.ToolName != "web_fetch" {
					continue
				}
				operationKey := "web-fetch-continuation-" + runmutation.Fingerprint(
					"web_fetch_continuation.v1", value.ID, call.AttemptID,
					string(domain.SupervisorTurnStarted))
				digest := runmutation.RunExecutionHandoffOperationDigest(value.RunID,
					operationKey)
				handoff, found, handoffErr := a.store.GetRunExecutionHandoff(ctx, digest)
				if handoffErr != nil {
					return projection, handoffErr
				}
				if found && handoff.Result != nil {
					if handoff.Result.Status == domain.RunExecutionHandoffCompleted {
						projection.replayed = true
						projection.runStatus = handoff.Result.RunStatus
						projection.state = "completed"
						return projection, nil
					}
					if run.Status == domain.RunPaused || run.Status == domain.RunCancelled {
						return projection, nil
					}
					projection.replayed = true
					projection.runStatus = handoff.Result.RunStatus
					projection.errorCode = handoff.Result.ErrorCode
					projection.state = "failed"
					return projection, nil
				}
			}
		}
		if len(rounds) < pageSize {
			break
		}
	}

	recoverable, err := a.store.ListRecoverableWebFetchAuthorizations(ctx,
		value.RunID, 101)
	if err != nil {
		return projection, err
	}
	for _, candidate := range recoverable {
		if candidate.ID == value.ID {
			projection.resume = true
			return projection, nil
		}
	}
	return projection, nil
}

func printCLIWebFetchContinuation(out interface{ Write([]byte) (int, error) },
	projection cliWebFetchContinuationProjection,
) {
	fmt.Fprintf(out, "continuation: %s\ncontinuation_replayed: %t\nbackground_worker: false\nmodel_attempts: %d\ntool_calls: %d\nrun_status: %s\n",
		projection.state, projection.replayed, projection.modelAttempts,
		projection.toolCalls, projection.runStatus)
}

func (a *App) approvalDecideAndContinue(ctx context.Context, actionName string, args []string) (resultErr error) {
	fs := newFlagSet("approval "+actionName, a.errOut)
	operationKey := fs.String("operation-key", "", "stable review operation key; defaults to this approval and decision")
	reason := fs.String("reason", "", "denial reason (deny only)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"operation-key": true, "reason": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cyberagent approval %s <approval-id> [--reason <text>] [--operation-key <key>]", actionName)
	}
	record, err := a.store.GetApproval(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if record.ToolName != string(toolgateway.WebFetchTool) {
		return apperror.New(apperror.CodeInvalidArgument, "This decision command handles web_fetch approvals; file proposals use edit review-approve|review-deny")
	}
	action := application.ApprovalControlAction(strings.ReplaceAll(actionName, "-", "_"))
	key := strings.TrimSpace(*operationKey)
	if key == "" {
		key = "cli-approval-" + record.ID + "-" + actionName
	}
	decision, err := application.NewApprovalControlService(a.store, a.newToolGateway(), a.checker).Decide(ctx,
		application.DecideApprovalControlRequest{Version: application.ApprovalControlProtocolVersion,
			RunID: record.RunID, ApprovalID: record.ID, Action: action, OperationKey: key, ReviewedBy: "cli_operator", Reason: *reason})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "approval: %s\nstatus: %s\ndecision_saved: true\ndecision_replayed: %t\n", record.ID, decision.Approval.Status, decision.Replayed)
	value, err := a.store.GetWebFetchAuthorizationByApproval(ctx, record.ID)
	if err != nil {
		return err
	}
	projection, err := a.projectCLIWebFetchContinuation(ctx, value)
	if err != nil {
		return fmt.Errorf("approval saved; continuation projection failed: %w", err)
	}
	if !projection.resume {
		printCLIWebFetchContinuation(a.out, projection)
		if projection.state == "failed" {
			return fmt.Errorf("review saved; continuation failed (%s)", projection.errorCode)
		}
		return nil
	}
	handoff, closeRuntime, err := a.newCLIApprovalExecution(ctx, record.RunID)
	if err != nil {
		return fmt.Errorf("approval saved; continuation setup failed: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, closeRuntime()) }()
	result, replayed, err := handoff.ResumeWebFetchAuthorization(ctx, record.RunID, value.ID)
	if err != nil {
		fmt.Fprintln(a.out, "continuation: failed\nbackground_worker: false")
		return fmt.Errorf("approval saved; continuation failed: %w", err)
	}
	state := "completed"
	if result.Turn == 0 {
		state = "not_started"
	}
	fmt.Fprintf(a.out, "continuation: %s\ncontinuation_replayed: %t\nbackground_worker: false\nmodel_attempts: %d\ntool_calls: %d\nrun_status: %s\n", state, replayed, result.ModelAttempts, result.ToolCalls, result.RunStatus)
	if result.Text != "" {
		fmt.Fprintln(a.out, result.Text)
	}
	return nil
}

func (a *App) continueReviewedFileEdit(ctx context.Context, runID, editID string) (resultErr error) {
	handoff, closeRuntime, err := a.newCLIApprovalExecution(ctx, runID)
	if err != nil {
		return fmt.Errorf("review saved; continuation setup failed: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, closeRuntime()) }()
	turns := application.NewThreadTurnServiceWithExecutionCapabilities(a.store,
		application.NewRunLifecycleControlService(a.store), handoff,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true})
	result := turns.ResumeApproval(ctx, application.ApprovalContinuationRequest{RunID: runID, Kind: "file_edit", ProposalID: editID})
	fmt.Fprintf(a.out, "continuation: %s\ncontinuation_replayed: %t\nbackground_worker: false\nmodel_called: %t\ntool_called: %t\n", result.State, result.Replayed, result.ModelCalled, result.ToolCalled)
	if result.State == "failed" {
		return fmt.Errorf("review saved; continuation failed (%s): %s", result.ErrorCode, result.Message)
	}
	return nil
}
