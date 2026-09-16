package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

type threadPlanPhaseFaultStore struct {
	*store.SQLiteStore
	fail             bool
	entered, release chan struct{}
	beforeCommit     func(context.Context)
}

func (s *threadPlanPhaseFaultStore) TransitionThreadRunPhase(ctx context.Context, thread, proposal string, mode domain.RunModeSnapshot, op domain.RunModeOperation, event events.Event) (domain.RunModeSnapshot, bool, error) {
	if s.fail {
		s.fail = false
		return domain.RunModeSnapshot{}, false, apperror.New(apperror.CodeUnavailable, "injected failure after durable selection")
	}
	if s.entered != nil {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return domain.RunModeSnapshot{}, false, ctx.Err()
		}
	}
	return s.SQLiteStore.TransitionThreadRunPhase(ctx, thread, proposal, mode, op, event)
}
func (s *threadPlanPhaseFaultStore) CommitThreadPlanMessage(ctx context.Context, request domain.ThreadMessageIntentRequest, run, proposal, key string) (domain.OperatorSteeringEnqueueResult, error) {
	if s.beforeCommit != nil {
		s.beforeCommit(ctx)
		s.beforeCommit = nil
	}
	return s.SQLiteStore.CommitThreadPlanMessage(ctx, request, run, proposal, key)
}
func threadPlanFaultTurns(st *threadPlanPhaseFaultStore, p *scriptedToolProvider) *application.ThreadTurnService {
	router := llm.NewRouter(llm.ModelRef{Provider: p.Name(), Model: "model"})
	router.RegisterProvider(p)
	return application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
}

func TestThreadPlanPreparedRecoveryAndNewRequirementRejection(t *testing.T) {
	for _, corrected := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-key-resumes", true: "new-input-rejects-old-plan"}[corrected], func(t *testing.T) {
			st, run, p, turns := newThreadPlanFixture(t, toolResponse("recovery-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait(), textResponse(rootActionResponse(domain.RootActionContinue, "acknowledged", "", "")))
			sendThreadPlanInput(t, turns, run, "plan", "Prepare the plan")
			proposal := latestThreadPlan(t, st, run.ID)
			request := confirmThreadPlan(run, proposal, "interrupted-confirmation")
			fault := &threadPlanPhaseFaultStore{SQLiteStore: st, fail: true}
			if _, err := threadPlanFaultTurns(fault, p).ControlPlan(t.Context(), request); apperror.CodeOf(err) != apperror.CodeUnavailable {
				t.Fatalf("injected preparation=%v", err)
			}
			eventsBefore, _ := st.ListRunEvents(t.Context(), run.ID)
			inspect, err := turns.InspectPlan(t.Context(), request)
			if err != nil || inspect.State != "prepared" || inspect.SelectionID == "" || inspect.ModelCalled || inspect.TurnRequest.MessageID != "" {
				t.Fatalf("prepared=%+v %v", inspect, err)
			}
			eventsAfter, _ := st.ListRunEvents(t.Context(), run.ID)
			if len(eventsBefore) != len(eventsAfter) || len(p.Requests()) != 2 {
				t.Fatal("inspection wrote or executed")
			}
			if corrected {
				sendThreadPlanInput(t, turns, run, "correction", "Change requirement before execution: preserve original compatibility.")
				inspect, err = turns.InspectPlan(t.Context(), request)
				if err != nil || inspect.State != "rejected" {
					t.Fatalf("stale prepared=%+v %v", inspect, err)
				}
				if _, err := turns.ControlPlan(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("old prepared executed: %v", err)
				}
				replan := application.ThreadPlanControlRequest{Version: application.PlanDeliveryControlProtocolVersion, ThreadID: request.ThreadID, RunID: run.ID, Action: "enter_plan", OperationKey: "replan-after-stale-prepared-confirmation", RequestedBy: "operator"}
				if result, err := turns.ControlPlan(t.Context(), replan); err != nil || result.State != "completed" {
					t.Fatalf("prepared permanently blocked replan: %+v %v", result, err)
				}
			} else {
				if result, err := turns.ControlPlan(t.Context(), request); err != nil || result.State != "completed" || len(p.Requests()) != 3 {
					t.Fatalf("same-key resume=%+v %v", result, err)
				}
			}
		})
	}
}

func TestThreadPlanPreparationStopPreventsModelAndKeepsNewMessageUnqueued(t *testing.T) {
	st, run, p, turns := newThreadPlanFixture(t, toolResponse("stoppable-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait())
	sendThreadPlanInput(t, turns, run, "plan", "Prepare the plan")
	request := confirmThreadPlan(run, latestThreadPlan(t, st, run.ID), "stop-before-delivery")
	fault := &threadPlanPhaseFaultStore{SQLiteStore: st, entered: make(chan struct{}), release: make(chan struct{})}
	controlled := threadPlanFaultTurns(fault, p)
	done := make(chan error, 1)
	go func() { _, err := controlled.ControlPlan(context.Background(), request); done <- err }()
	awaitTurnSignal(t, fault.entered)
	newer := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: request.ThreadID, Content: "Cancel execution and keep this new instruction.", OperationKey: "new-input-during-plan-preparation", RequestedBy: "operator"}
	if _, err := controlled.Execute(t.Context(), newer); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("new input queued ahead=%v", err)
	}
	state, err := controlled.ExecutionState(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlled.Interrupt(t.Context(), request.ThreadID, state.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := awaitTurnResult(t, done); err == nil || (!errors.Is(err, context.Canceled) && apperror.CodeOf(err) != apperror.CodeCancelled) {
		t.Fatalf("stop=%v", err)
	}
	observation, err := st.InspectThreadTurnRequest(t.Context(), request.ThreadID, newer.OperationKey, newer.RequestedBy)
	if err != nil || observation.MessageID != "" {
		t.Fatalf("new input lost/misqueued=%+v %v", observation, err)
	}
	if len(p.Requests()) != 2 {
		t.Fatal("stop started execution")
	}
	mode, _ := st.GetRunMode(t.Context(), run.ID)
	if mode.Phase != domain.ExecutionPhasePlan {
		t.Fatal("stop changed phase")
	}
}

func TestThreadPlanCrossProcessCorrectionBeforeCommitRejectsExecution(t *testing.T) {
	st, run, p, turns := newThreadPlanFixture(t, toolResponse("race-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait())
	sendThreadPlanInput(t, turns, run, "plan", "Prepare the plan")
	request := confirmThreadPlan(run, latestThreadPlan(t, st, run.ID), "race-confirm")
	fault := &threadPlanPhaseFaultStore{SQLiteStore: st, beforeCommit: func(ctx context.Context) {
		_, err := application.NewThreadService(st).Submit(ctx, application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: request.ThreadID, Content: "Newer correction arriving between phase and confirmation enqueue.", OperationKey: "other-process-correction-before-commit", RequestedBy: "operator"})
		if err != nil {
			t.Error(err)
		}
	}}
	if _, err := threadPlanFaultTurns(fault, p).ControlPlan(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("confirmation ignored correction=%v", err)
	}
	if len(p.Requests()) != 2 {
		t.Fatal("stale confirmation executed")
	}
	inspect, err := turns.InspectPlan(t.Context(), request)
	if err != nil || inspect.State != "rejected" || inspect.TurnRequest.MessageID != "" {
		t.Fatalf("stale observation=%+v %v", inspect, err)
	}
}

func TestThreadPlanConcurrentDifferentKeysSelectAndExecuteOnce(t *testing.T) {
	st, run, p, turns := newThreadPlanFixture(t, toolResponse("concurrent-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait(), textResponse(rootActionResponse(domain.RootActionContinue, "executed once", "", "")))
	sendThreadPlanInput(t, turns, run, "plan", "Prepare the plan")
	proposal := latestThreadPlan(t, st, run.ID)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, key := range []string{"concurrent-window-a", "concurrent-window-b"} {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			_, errs[i] = newThreadPlanTurns(st, p).ControlPlan(context.Background(), confirmThreadPlan(run, proposal, key))
		}(i, key)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if apperror.CodeOf(err) != apperror.CodeConflict && apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("unexpected concurrency=%v", err)
		}
	}
	if successes != 1 || len(p.Requests()) != 3 {
		t.Fatalf("successes=%d calls=%d errors=%v", successes, len(p.Requests()), errs)
	}
}

func TestThreadPlanOriginalKeyCannotChangeActionOrSourceRun(t *testing.T) {
	st, run, p, turns := newThreadPlanFixture(t, toolResponse("key-binding-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait(), textResponse(rootActionResponse(domain.RootActionContinue, "confirmed once", "", "")))
	mode := application.ThreadPlanControlRequest{Version: application.PlanDeliveryControlProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), RunID: run.ID, Action: "enter_deliver", OperationKey: "thread-original-mode-operation", RequestedBy: "operator"}
	if _, err := turns.ControlPlan(t.Context(), mode); err != nil {
		t.Fatal(err)
	}
	changed := mode
	changed.Action = "enter_plan"
	if _, err := turns.ControlPlan(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("same key changed mode=%v", err)
	}
	if _, err := turns.InspectPlan(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("GET mismatched action=%v", err)
	}
	changed.OperationKey = "thread-original-plan-mode-operation"
	if _, err := turns.ControlPlan(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	sendThreadPlanInput(t, turns, run, "proposal-for-key-test", "Prepare a plan")
	confirmation := confirmThreadPlan(run, latestThreadPlan(t, st, run.ID), "key-test")
	confirmation.OperationKey = changed.OperationKey
	if _, err := turns.ControlPlan(t.Context(), confirmation); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("mode key selected plan=%v", err)
	}
	confirmation.OperationKey = "thread-original-confirm-plan-operation"
	if _, err := turns.ControlPlan(t.Context(), confirmation); err != nil {
		t.Fatal(err)
	}
	changed.OperationKey = confirmation.OperationKey
	if _, err := turns.ControlPlan(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("confirmation key replanned=%v", err)
	}
	if _, err := turns.InspectPlan(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("GET confirmation as mode=%v", err)
	}
	changed.OperationKey = "thread-original-replan-key-operation"
	if _, err := turns.ControlPlan(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	thread, _ := st.GetThread(t.Context(), changed.ThreadID)
	changed.RunID = thread.ActiveRunID
	if _, err := turns.ControlPlan(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("same key changed source=%v", err)
	}
	if len(p.Requests()) != 3 {
		t.Fatal("key mismatch called model")
	}
}

func TestThreadPlanSameKeyDifferentActionsRaceAtomically(t *testing.T) {
	st, run, p, turns := newThreadPlanFixture(t, toolResponse("atomic-key-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait(), textResponse(rootActionResponse(domain.RootActionContinue, "confirmed once", "", "")))
	sendThreadPlanInput(t, turns, run, "plan", "Prepare the plan")
	confirmation := confirmThreadPlan(run, latestThreadPlan(t, st, run.ID), "atomic-shared-key")
	mode := application.ThreadPlanControlRequest{Version: confirmation.Version, ThreadID: confirmation.ThreadID, RunID: run.ID, Action: "enter_deliver", OperationKey: confirmation.OperationKey, RequestedBy: "operator"}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, request := range []application.ThreadPlanControlRequest{confirmation, mode} {
		wg.Add(1)
		go func(i int, request application.ThreadPlanControlRequest) {
			defer wg.Done()
			_, errs[i] = newThreadPlanTurns(st, p).ControlPlan(context.Background(), request)
		}(i, request)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if apperror.CodeOf(err) != apperror.CodeConflict && apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("race=%v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("mutually exclusive actions=%v", errs)
	}
	selection, selected, _ := st.GetPlanDeliverySelectionByRun(t.Context(), run.ID)
	if errs[0] == nil {
		if !selected || selection.ID == "" || len(p.Requests()) != 3 {
			t.Fatal("confirmation outcome missing")
		}
	} else if selected || len(p.Requests()) != 2 {
		t.Fatal("mode winner also selected or executed")
	}
}
