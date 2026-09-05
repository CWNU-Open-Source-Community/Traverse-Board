package application_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func TestThreadTurnExecutesOneOperatorTurnAndContinuesSameRun(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runs := application.NewRunService(st)
	_, created, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "complete one product Thread turn", Profile: "review",
		ModelRoute: "lifecycle-test/model", Interactive: true,
		Budget: domain.Budget{MaxTurns: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionContinue, "Work is in progress.", "", ""),
		rootActionResponse(domain.RootActionWait, "Please confirm the next step.", "",
			"operator confirmation required"),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	lifecycle := application.NewRunLifecycleControlService(st)
	execution := application.NewRunExecutionHandoffService(st, router,
		policy.NewDefaultChecker())
	turns := application.NewThreadTurnService(st, lifecycle, execution)
	request := application.ExecuteThreadTurnRequest{
		Version:      domain.ThreadMessageProtocolVersion,
		ThreadID:     domain.InitialThreadID(created.ID),
		Content:      "Inspect the repository and stop at the next operator boundary.",
		OperationKey: "thread-turn-application-operation-0001",
		RequestedBy:  "thread_turn_test_operator",
	}

	first, err := turns.Execute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Submission.Run.ID != created.ID ||
		first.Submission.Run.Status != domain.RunRunning ||
		first.Submission.Message.Status != domain.OperatorSteeringCommitted ||
		!first.ExecutionStarted || !first.ModelCalled || first.ToolCalled || first.Replayed ||
		first.Execution == nil || first.Execution.Handoff.Result == nil ||
		first.Execution.Handoff.Result.StopReason != "selection_drained" ||
		first.Execution.Handoff.Result.StepsCompleted != 1 || provider.calls != 1 {
		t.Fatalf("unexpected first Thread turn: result=%#v calls=%d", first, provider.calls)
	}
	messages, err := st.ListSessionMessages(ctx, created.SessionID, true)
	if err != nil || len(messages) != 2 || messages[0].Content != request.Content ||
		messages[1].Content != "Work is in progress." {
		t.Fatalf("Thread turn did not persist exactly one operator turn: %#v err=%v",
			messages, err)
	}

	replayed, err := turns.Execute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Submission.Message.ID != first.Submission.Message.ID ||
		replayed.Submission.Run.Status != domain.RunRunning || provider.calls != 1 {
		t.Fatalf("Thread turn replay changed execution: result=%#v calls=%d",
			replayed, provider.calls)
	}

	secondRequest := application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: request.ThreadID,
		Content:      "Continue this same Thread and stop for confirmation.",
		OperationKey: "thread-turn-application-operation-0002",
		RequestedBy:  request.RequestedBy,
	}
	second, err := turns.Execute(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if second.Submission.Run.ID != created.ID || second.Submission.SuccessorCreated ||
		second.Submission.Run.Status != domain.RunPaused ||
		second.Submission.Message.Status != domain.OperatorSteeringCommitted ||
		second.Execution == nil || second.Execution.Handoff.Result == nil ||
		second.Execution.Handoff.Result.StopReason != "root_wait" ||
		second.Execution.Handoff.Result.StepsCompleted != 1 || provider.calls != 2 {
		t.Fatalf("second message did not continue the same Run: result=%#v calls=%d",
			second, provider.calls)
	}
	messages, err = st.ListSessionMessages(ctx, created.SessionID, true)
	if err != nil || len(messages) != 4 || messages[2].Content != secondRequest.Content ||
		messages[3].Content != "Please confirm the next step." {
		t.Fatalf("same-Run continuation history=%#v err=%v", messages, err)
	}
}

func TestThreadTurnRequestedFinishEndsProductTurnWithoutSyntheticContinuation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-finish.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("workspace fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkspace(ctx, store.WorkspaceRecord{ID: "ws-thread-turn-finish",
		Name: "thread-turn-finish", RootPath: workspaceRoot,
		CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "inspect the workspace once", Profile: "code",
			Surface: "code", Phase: "deliver", WorkspaceID: "ws-thread-turn-finish",
			ModelRoute: "tool-loop/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 3}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-list-1", string(toolgateway.WorkspaceListTool),
			`{"version":"agent-code-tools.v1","path":".","limit":20}`),
		textResponse(rootActionResponse(domain.RootActionFinish,
			"Workspace inspected.", "complete", "")),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	request := application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(created.ID),
		Content:      "List the workspace and report the first entry.",
		OperationKey: "thread-turn-requested-finish-operation-0001",
		RequestedBy:  "thread_turn_test_operator",
	}

	first, err := turns.Execute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Submission.Run.Status != domain.RunRunning ||
		first.Submission.Message.Status != domain.OperatorSteeringCommitted ||
		first.Execution == nil || first.Execution.Handoff.Result == nil ||
		first.Execution.Handoff.Result.StopReason != "turn_finish" ||
		len(first.Execution.Execution.Steps) != 1 ||
		first.Execution.Execution.Steps[0].RequestedAction != domain.RootActionFinish ||
		first.Execution.Execution.Steps[0].Action.Kind != domain.RootActionContinue ||
		first.Execution.Execution.Steps[0].ModelAttempts != 2 ||
		first.Execution.Execution.Steps[0].ToolCalls != 1 ||
		len(provider.Requests()) != 2 {
		t.Fatalf("requested finish crossed the product-turn boundary: result=%#v calls=%d",
			first, len(provider.Requests()))
	}
	if !hasToolResult(provider.Requests()[1], `agent-code-tools.v1`) {
		t.Fatalf("workspace_list result did not reach the final model call: %#v",
			provider.Requests())
	}
	messages, err := st.ListSessionMessages(ctx, created.SessionID, true)
	if err != nil || len(messages) != 2 || messages[0].Content != request.Content ||
		messages[1].Content != "Workspace inspected." {
		t.Fatalf("requested finish manufactured a duplicate turn: messages=%#v err=%v",
			messages, err)
	}
	eventItems, err := st.ListRunEvents(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	auditedRewrite := false
	for _, item := range eventItems {
		if item.Type == events.AgentTurnCompletedEvent &&
			strings.Contains(item.PayloadJSON, `"requested_lifecycle_action":"finish"`) &&
			strings.Contains(item.PayloadJSON, `"lifecycle_action":"continue"`) {
			auditedRewrite = true
			break
		}
	}
	if !auditedRewrite {
		t.Fatalf("requested/effective lifecycle actions were not audited: %#v", eventItems)
	}

	replayed, err := turns.Execute(ctx, request)
	if err != nil || !replayed.Replayed || len(provider.Requests()) != 2 {
		t.Fatalf("requested-finish replay called the model: result=%#v calls=%d err=%v",
			replayed, len(provider.Requests()), err)
	}
}

func TestThreadTurnRecoversRequestedFinishAfterHandoffCompletionCrash(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-finish-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "complete one tool-backed turn", Profile: "review",
			ModelRoute: "tool-loop/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 3}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-note-recovery-1", "note_create",
			`{"title":"Durable observation","content":"Tool result committed."}`),
		textResponse(rootActionResponse(domain.RootActionFinish,
			"The requested turn is complete.", "complete", "")),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	failing := &failFirstHandoffCompletionStore{SQLiteStore: st, failNext: true}
	turns := application.NewThreadTurnService(failing,
		application.NewRunLifecycleControlService(failing),
		application.NewRunExecutionHandoffService(failing, router, policy.NewDefaultChecker()))
	request := application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(created.ID),
		Content:      "Use one tool and finish this turn.",
		OperationKey: "thread-turn-finish-recovery-operation-0001",
		RequestedBy:  "thread_turn_test_operator",
	}

	if _, err := turns.Execute(ctx, request); err == nil ||
		!strings.Contains(err.Error(), "injected handoff completion crash") {
		t.Fatalf("handoff completion crash was not injected: %v", err)
	}
	if len(provider.Requests()) != 2 {
		t.Fatalf("initial tool-backed turn calls=%d want=2", len(provider.Requests()))
	}
	messages, err := st.ListSessionMessages(ctx, created.SessionID, true)
	if err != nil || len(messages) != 2 {
		t.Fatalf("Supervisor turn was not durable before handoff crash: %#v err=%v",
			messages, err)
	}

	recovered, err := turns.Execute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Replayed || recovered.Submission.Run.Status != domain.RunRunning ||
		recovered.Execution == nil || recovered.Execution.Handoff.Result == nil ||
		recovered.Execution.Handoff.Result.StopReason != "turn_finish" ||
		len(provider.Requests()) != 2 {
		t.Fatalf("durable requested finish was not recovered: result=%#v calls=%d",
			recovered, len(provider.Requests()))
	}
	messages, err = st.ListSessionMessages(ctx, created.SessionID, true)
	if err != nil || len(messages) != 2 ||
		messages[1].Content != "The requested turn is complete." {
		t.Fatalf("handoff recovery manufactured a duplicate turn: %#v err=%v",
			messages, err)
	}
}

type failFirstHandoffCompletionStore struct {
	*store.SQLiteStore
	failNext bool
}

func (s *failFirstHandoffCompletionStore) CompleteRunExecutionHandoff(ctx context.Context,
	operationID string, lease domain.RunExecutionLease,
	status domain.RunExecutionHandoffStatus, stopReason string, errorCode string,
	stepsCompleted int, modelCalled bool, toolCalled bool,
) (domain.RunExecutionHandoffResult, bool, error) {
	if s.failNext {
		s.failNext = false
		return domain.RunExecutionHandoffResult{}, false,
			errors.New("injected handoff completion crash")
	}
	return s.SQLiteStore.CompleteRunExecutionHandoff(ctx, operationID, lease, status,
		stopReason, errorCode, stepsCompleted, modelCalled, toolCalled)
}

func TestThreadTurnStopsWithoutExecutionWhileRunWaitsForApproval(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runs := application.NewRunService(st)
	mission, created, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "approval boundary", Profile: "review", Interactive: true,
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := runs.Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := started.Status
	at := time.Now().UTC()
	if err := started.Transition(domain.RunWaitingApproval, at); err != nil {
		t.Fatal(err)
	}
	event, err := events.New(started.ID, mission.ID, events.RunStatusChangedEvent,
		"thread_turn_test", started.ID, map[string]any{
			"from": expected, "to": domain.RunWaitingApproval,
		})
	if err != nil {
		t.Fatal(err)
	}
	event.CreatedAt = at
	if err := st.TransitionRun(ctx, started, expected, event); err != nil {
		t.Fatal(err)
	}
	waiting := started
	turns := application.NewThreadTurnService(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, llm.NewDefaultRouter(),
			policy.NewDefaultChecker()))
	result, err := turns.Execute(ctx, application.ExecuteThreadTurnRequest{
		Version:  domain.ThreadMessageProtocolVersion,
		ThreadID: domain.InitialThreadID(waiting.ID), Content: "Do not bypass approval.",
		OperationKey: "thread-turn-approval-operation-0001",
		RequestedBy:  "thread_turn_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Submission.Run.Status != domain.RunWaitingApproval ||
		result.Submission.Message.Status != domain.OperatorSteeringPending ||
		result.Execution != nil || result.ExecutionStarted || result.ModelCalled ||
		result.ToolCalled {
		t.Fatalf("approval boundary was widened by Thread turn: %#v", result)
	}
}

func TestThreadTurnReplayAfterContinueDoesNotCreateSuccessor(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-terminal-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "terminal Thread turn", Profile: "review",
			ModelRoute: "lifecycle-test/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 3}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionContinue, "Completing the task.", "", ""),
		rootActionResponse(domain.RootActionFinish, "Task complete.", "complete", ""),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	request := application.ExecuteThreadTurnRequest{
		Version:  domain.ThreadMessageProtocolVersion,
		ThreadID: domain.InitialThreadID(created.ID), Content: "Finish this task.",
		OperationKey: "thread-turn-terminal-replay-operation-0001",
		RequestedBy:  "thread_turn_test_operator",
	}
	first, err := turns.Execute(ctx, request)
	if err != nil || first.Submission.Run.Status != domain.RunRunning || provider.calls != 1 ||
		first.Execution == nil || first.Execution.Handoff.Result == nil ||
		first.Execution.Handoff.Result.StopReason != "selection_drained" {
		t.Fatalf("continuable Thread turn result=%#v calls=%d err=%v", first,
			provider.calls, err)
	}
	replayed, err := turns.Execute(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Submission.Run.ID != created.ID ||
		provider.calls != 1 || replayed.Submission.Run.Status != domain.RunRunning {
		t.Fatalf("continuable replay result=%#v calls=%d err=%v", replayed,
			provider.calls, err)
	}
	bindings, err := st.ListThreadRuns(ctx, request.ThreadID)
	if err != nil || len(bindings) != 1 || bindings[0].RunID != created.ID {
		t.Fatalf("terminal replay created a successor: %#v err=%v", bindings, err)
	}
}

func TestThreadTurnNextExplicitMessageAutomaticallyAdvancesPastFailedTurn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-auto-continuation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "continue after a provider failure", Profile: "review",
			ModelRoute: "lifecycle-test/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{
		responses: []string{"", rootActionResponse(domain.RootActionWait,
			"The new message continued successfully.", "", "operator boundary")},
		failures: []error{
			apperror.New(apperror.CodeUnavailable, "temporary provider failure"),
		},
	}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	firstRequest := application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(created.ID),
		Content:      "This failed message must not be replayed automatically.",
		OperationKey: "thread-turn-auto-continuation-first-0001",
		RequestedBy:  "thread_turn_test_operator",
	}

	first, err := turns.Execute(ctx, firstRequest)
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition || provider.calls != 1 ||
		first.Execution == nil || first.Execution.Handoff.Result == nil ||
		first.Execution.Handoff.Result.Status != domain.RunExecutionHandoffFailed {
		t.Fatalf("first failed turn=%+v calls=%d code=%s err=%v", first,
			provider.calls, apperror.CodeOf(err), err)
	}
	firstReplay, replayErr := turns.Execute(ctx, firstRequest)
	if apperror.CodeOf(replayErr) != apperror.CodeFailedPrecondition || !firstReplay.Replayed ||
		provider.calls != 1 {
		t.Fatalf("failed turn replay=%+v calls=%d code=%s err=%v", firstReplay,
			provider.calls, apperror.CodeOf(replayErr), replayErr)
	}

	continued, err := turns.Execute(ctx, application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: firstRequest.ThreadID,
		Content:      "Continue with this new message instead.",
		OperationKey: "thread-turn-auto-continuation-second-0001",
		RequestedBy:  firstRequest.RequestedBy,
	})
	if err != nil || !continued.Submission.SuccessorCreated ||
		continued.Submission.PredecessorRunID != created.ID ||
		continued.Submission.Run.ID == created.ID ||
		continued.Submission.Run.Status != domain.RunPaused || provider.calls != 2 {
		t.Fatalf("automatic continuation=%+v calls=%d err=%v", continued,
			provider.calls, err)
	}
	failedRun, err := st.GetRun(ctx, created.ID)
	if err != nil || failedRun.Status != domain.RunFailed {
		t.Fatalf("predecessor Run=%+v err=%v", failedRun, err)
	}
	oldMessage, err := st.GetOperatorSteering(ctx, first.Submission.Message.ID)
	if err != nil || oldMessage.Status != domain.OperatorSteeringCancelled {
		t.Fatalf("failed input was replayed or retained=%+v err=%v", oldMessage, err)
	}
	bindings, err := st.ListThreadRuns(ctx, firstRequest.ThreadID)
	if err != nil || len(bindings) != 2 || bindings[0].RunID != created.ID ||
		bindings[1].RunID != continued.Submission.Run.ID {
		t.Fatalf("Thread successor bindings=%+v err=%v", bindings, err)
	}
}

func TestThreadTurnAutomaticallyAppliesPendingModelAtNextExplicitMessage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-pending-model.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "apply the selected model without Run controls",
			Profile: "review", ModelRoute: "lifecycle-test/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionWait, "The selected model is active.", "",
			"operator boundary"),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	registry := &mutableThreadModelRouteRegistry{router: router,
		snapshot: modelregistry.Snapshot{
			ProtocolVersion: modelregistry.ProtocolVersion, Generation: 1,
			Providers: []modelregistry.ProviderAvailability{{
				Name: provider.Name(), DisplayName: "Lifecycle Provider",
				Kind:   modelregistry.ProviderKindOpenAICompatible,
				Status: modelregistry.ProviderAvailable, Models: []string{"model", "model-next"},
				CredentialSource: "test", NetworkRequired: true, Enabled: true,
				Harnesses: []modelregistry.HarnessAvailability{
					{ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion,
						Model: "model", RootEligible: true,
						LatestQualificationStatus: modelregistry.QualificationStatusAvailable},
					{ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion,
						Model: "model-next", RootEligible: true,
						LatestQualificationStatus: modelregistry.QualificationStatusAvailable},
				},
			}},
			Routes: []modelregistry.RouteAvailability{{Name: "review",
				Provider: provider.Name(), Model: "model", Available: true, HarnessReady: true}},
		}}
	threadID := domain.InitialThreadID(created.ID)
	changed, err := application.NewThreadModelRouteService(st, registry).Change(ctx,
		application.ChangeThreadModelRouteRequest{
			Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: threadID,
			Action: domain.ThreadModelRouteSelect, Provider: provider.Name(), Model: "model-next",
			OperationKey: "thread-turn-select-next-model-0001",
			RequestedBy:  "thread_turn_test_operator",
		})
	if err != nil || changed.AppliesTo != "next_run" {
		t.Fatalf("pending model change=%+v err=%v", changed, err)
	}
	turns := application.NewThreadTurnService(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker())).
		WithModelRouteRegistry(registry)
	result, err := turns.Execute(ctx, application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: threadID,
		Content:      "Use the newly selected model now.",
		OperationKey: "thread-turn-pending-model-message-0001",
		RequestedBy:  "thread_turn_test_operator",
	})
	if err != nil || !result.Submission.SuccessorCreated ||
		result.Submission.PredecessorRunID != created.ID ||
		result.Submission.Run.Config.ModelRoute != provider.Name()+"/model-next" ||
		result.Submission.Session.Route != provider.Name()+"/model-next" || provider.calls != 1 {
		t.Fatalf("pending model was not automatic: result=%+v calls=%d err=%v",
			result, provider.calls, err)
	}
	oldRun, err := st.GetRun(ctx, created.ID)
	if err != nil || oldRun.Status != domain.RunCancelled {
		t.Fatalf("superseded model Run=%+v err=%v", oldRun, err)
	}
}

func TestThreadTurnAutomaticallyAppliesPendingPermissionAtNextExplicitMessage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-pending-permission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "apply deferred permission on the next message",
			Profile: "review", ModelRoute: "lifecycle-test/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := application.NewRunService(st).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	threadID := domain.InitialThreadID(started.ID)
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true,
	}
	changed, err := application.NewThreadExecutionPermissionService(st, capabilities).Change(ctx,
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey:           "thread-turn-select-next-permission-0001",
			RequestedBy:            "thread_turn_test_operator",
			Reason:                 "use bounded Workspace Access on the next execution epoch",
			ConfirmWorkspaceAccess: true,
		})
	if err != nil || changed.CurrentRunID != started.ID ||
		changed.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("pending permission change=%+v err=%v", changed, err)
	}
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionWait, "Workspace Access is active.", "",
			"operator boundary"),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnServiceWithExecutionCapabilities(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()),
		capabilities)
	result, err := turns.Execute(ctx, application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: threadID,
		Content:      "Continue with Workspace Access.",
		OperationKey: "thread-turn-pending-permission-message-0001",
		RequestedBy:  "thread_turn_test_operator",
	})
	if err != nil || !result.Submission.SuccessorCreated ||
		result.Submission.PredecessorRunID != started.ID ||
		result.Submission.Run.ID == started.ID || provider.calls != 1 {
		t.Fatalf("pending permission was not automatic: result=%+v calls=%d err=%v",
			result, provider.calls, err)
	}
	oldRun, err := st.GetRun(ctx, started.ID)
	if err != nil || oldRun.Status != domain.RunCancelled {
		t.Fatalf("superseded permission Run=%+v err=%v", oldRun, err)
	}
	successorPermission, err := st.GetRunExecutionPermission(ctx,
		result.Submission.Run.ID)
	if err != nil || successorPermission.Mode != domain.RunExecutionPermissionWorkspaceAccess {
		t.Fatalf("successor permission=%+v err=%v", successorPermission, err)
	}
	predecessorPermission, err := st.GetRunExecutionPermission(ctx, started.ID)
	if err != nil || predecessorPermission.Mode != domain.RunExecutionPermissionConservative {
		t.Fatalf("predecessor permission mutated=%+v err=%v", predecessorPermission, err)
	}
}

func TestThreadTurnPendingConfigurationNeverTerminatesRunWithActiveLease(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-turn-pending-active-lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, created, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{Goal: "preserve an actively leased execution epoch",
			Profile: "review", ModelRoute: "lifecycle-test/model", Interactive: true,
			Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := application.NewRunService(st).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	threadID := domain.InitialThreadID(started.ID)
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true,
	}
	changed, err := application.NewThreadExecutionPermissionService(st, capabilities).Change(ctx,
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey:           "thread-turn-active-lease-permission-0001",
			RequestedBy:            "thread_turn_test_operator",
			Reason:                 "defer Workspace Access until the next execution epoch",
			ConfirmWorkspaceAccess: true,
		})
	if err != nil || changed.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("active-lease pending permission=%+v err=%v", changed, err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, started.ID)
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionWait, "The retry continued safely.", "",
			"operator boundary"),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnServiceWithExecutionCapabilities(st,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()),
		capabilities)
	request := application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: threadID,
		Content:      "Apply the pending permission only after the current worker stops.",
		OperationKey: "thread-turn-active-lease-message-0001",
		RequestedBy:  "thread_turn_test_operator",
	}
	blocked, err := turns.Execute(ctx, request)
	if apperror.CodeOf(err) != apperror.CodeConflict ||
		blocked.Submission.Thread.ID != "" || blocked.Submission.Run.ID != "" ||
		blocked.Submission.Message.ID != "" || blocked.Execution != nil ||
		blocked.ExecutionStarted || blocked.ModelCalled || blocked.ToolCalled ||
		provider.calls != 0 {
		t.Fatalf("active lease was not fenced: result=%+v calls=%d code=%s err=%v",
			blocked, provider.calls, apperror.CodeOf(err), err)
	}
	storedRun, err := st.GetRun(ctx, started.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("active leased Run was terminalized=%+v err=%v", storedRun, err)
	}
	threadRecord, err := st.GetThread(ctx, threadID)
	if err != nil || threadRecord.ActiveRunID != started.ID ||
		threadRecord.LastRunID != started.ID {
		t.Fatalf("active leased Thread projection changed=%+v err=%v", threadRecord, err)
	}
	bindings, err := st.ListThreadRuns(ctx, threadID)
	if err != nil || len(bindings) != 1 || bindings[0].RunID != started.ID {
		t.Fatalf("active lease created a successor=%+v err=%v", bindings, err)
	}
	if _, _, err := st.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}

	retried, err := turns.Execute(ctx, request)
	if err != nil || !retried.Submission.SuccessorCreated ||
		retried.Submission.PredecessorRunID != started.ID || provider.calls != 1 {
		t.Fatalf("post-lease retry did not continue: result=%+v calls=%d err=%v",
			retried, provider.calls, err)
	}
	oldRun, err := st.GetRun(ctx, started.ID)
	if err != nil || oldRun.Status != domain.RunCancelled {
		t.Fatalf("post-lease predecessor=%+v err=%v", oldRun, err)
	}
	successorPermission, err := st.GetRunExecutionPermission(ctx, retried.Submission.Run.ID)
	if err != nil || successorPermission.Mode != domain.RunExecutionPermissionWorkspaceAccess {
		t.Fatalf("post-lease successor permission=%+v err=%v", successorPermission, err)
	}
}
