package application_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func TestMidTurnCorrectionReachesActualSecondProviderRequestAndHistory(t *testing.T) {
	provider := &gatedThreadProvider{started: make(chan struct{}), release: make(chan struct{}),
		lifecycleProvider: lifecycleProvider{responses: []string{
			rootActionResponse(domain.RootActionFinish, "Outdated answer", "complete", ""),
			rootActionResponse(domain.RootActionFinish, "Corrected answer", "complete", ""),
		}},
	}
	st, turns, request := threadControlFixture(t, provider)
	thread, err := st.GetThread(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.GetRun(t.Context(), thread.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, executeErr := turns.Execute(context.Background(), request); done <- executeErr }()
	awaitTurnSignal(t, provider.started)
	correction := "Only inspect the frontend; do not change backend files"
	submission := application.SubmitSessionMessageRequest{
		Version: domain.SessionMessageSubmissionProtocolVersion, SessionID: run.SessionID,
		Content: correction, OperationKey: "midturn-correction-0001", RequestedBy: "test_operator",
		DeliveryMode: domain.OperatorSteeringCurrentTurn,
	}
	accepted, err := application.NewSessionMessageSubmissionService(st).Submit(t.Context(), submission)
	if err != nil || accepted.Replayed || accepted.Message.TargetAttemptID == "" ||
		accepted.Message.DeliveryMode != domain.OperatorSteeringCurrentTurn {
		t.Fatalf("correction admission=%#v err=%v", accepted, err)
	}
	replayed, err := application.NewSessionMessageSubmissionService(st).Submit(t.Context(), submission)
	if err != nil || !replayed.Replayed || replayed.Message.ID != accepted.Message.ID {
		t.Fatalf("correction replay=%#v err=%v", replayed, err)
	}
	close(provider.release)
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 || len(provider.requests) != 2 {
		t.Fatalf("provider requests=%d calls=%d", len(provider.requests), provider.calls)
	}
	for _, message := range provider.requests[0].Messages {
		if strings.Contains(message.Content, correction) {
			t.Fatalf("correction reached already-started request: %#v", provider.requests[0].Messages)
		}
	}
	found := false
	for _, message := range provider.requests[1].Messages {
		if strings.Contains(message.Content, correction) {
			found = true
		}
	}
	if !found {
		t.Fatalf("correction missing from actual second provider request: %#v", provider.requests[1].Messages)
	}
	stored, err := st.GetOperatorSteering(t.Context(), accepted.Message.ID)
	if err != nil || stored.Status != domain.OperatorSteeringCommitted || stored.Prepared || stored.SessionMessageID == 0 {
		t.Fatalf("correction was not committed with provenance: %#v err=%v", stored, err)
	}
	listed, err := st.ListOperatorSteering(t.Context(), run.ID, 20)
	if err != nil || len(listed) != 2 || listed[1].ID != accepted.Message.ID || listed[1].Prepared {
		t.Fatalf("committed correction cannot be read by Run detail: %#v err=%v", listed, err)
	}
	messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(messages) != 3 || messages[0].Content != request.Content ||
		messages[1].Content != correction || messages[2].Content != "Corrected answer" {
		t.Fatalf("history order=%#v err=%v", messages, err)
	}
}

type pauseBeforeModelStart struct {
	*store.SQLiteStore
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *pauseBeforeModelStart) RecordSupervisorModelStarted(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt,
) (bool, error) {
	s.once.Do(func() {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return s.SQLiteStore.RecordSupervisorModelStarted(ctx, checkpoint, attempt)
}

func TestMidTurnCorrectionAfterMoneyReserveRebindsUnsentAttempt(t *testing.T) {
	provider := &ordinaryMoneyLifecycleProvider{
		text:  rootActionResponse(domain.RootActionContinue, "Corrected completion", "", ""),
		usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
	}
	_, st, run, _ := newOrdinaryMoneyLifecycleFixture(t, provider)
	gate := &pauseBeforeModelStart{SQLiteStore: st, started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(gate.release) }) })
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	window := llm.DefaultContextWindow()
	window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
	if err := router.SetContextWindow(llm.ModelRef{Provider: provider.Name(), Model: "model"}, window); err != nil {
		t.Fatal(err)
	}
	supervisor := application.NewRunSupervisor(gate, router, policy.NewDefaultChecker()).
		WithMonetaryBudget(application.NewMonetaryBudgetService(st))
	done := make(chan error, 1)
	go func() { _, stepErr := supervisor.Step(context.Background(), run.ID); done <- stepErr }()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("reserved model did not reach start gate")
	}
	accepted, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "Add a long correction to change the model request estimate",
		OperationKey: "midturn-money-gate-0001", RequestedBy: "test_operator",
		DeliveryMode: domain.OperatorSteeringCurrentTurn,
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(gate.release) })
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider was invoked %d times", provider.calls.Load())
	}
	eventsFound, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(eventsFound, events.ModelStartedEvent) != 1 ||
		countEventType(eventsFound, events.MonetaryBudgetReleasedEvent) < 1 {
		t.Fatalf("unsent reservation was not released before corrected model start: %#v", eventsFound)
	}
	usage, err := st.GetMonetaryUsage(t.Context(), run.ID)
	if err != nil || usage.SettledMicros <= 0 || usage.ReleasedMicros <= 0 {
		t.Fatalf("monetary accounting=%#v err=%v", usage, err)
	}
	stored, err := st.GetOperatorSteering(t.Context(), accepted.Message.ID)
	if err != nil || stored.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("corrected completion lost correction: %#v err=%v", stored, err)
	}
}

func TestMidTurnCorrectionReopenReleasesUnsentMonetaryReservation(t *testing.T) {
	provider := &ordinaryMoneyLifecycleProvider{
		text:  rootActionResponse(domain.RootActionContinue, "Corrected after reopen", "", ""),
		usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
	}
	path, first, run, _ := newOrdinaryMoneyLifecycleFixture(t, provider)
	lease := acquireTestRunExecutionLease(t, t.Context(), first, run.ID)
	turn, err := first.BeginSupervisorTurn(t.Context(), lease, "")
	if err != nil {
		t.Fatal(err)
	}
	unsent := llm.ModelAttempt{Number: 1, SupervisorAttemptID: turn.Checkpoint.AttemptID,
		Provider: provider.Name(), Model: "model"}
	if _, err := application.NewMonetaryBudgetService(first).ReserveModelCall(t.Context(), run,
		domain.MonetaryScopeRoot, unsent, llm.ChatRequest{Messages: []llm.Message{
			{Role: "user", Content: "old request without correction"}}, MaxTokens: 256}); err != nil {
		t.Fatal(err)
	}
	accepted, err := first.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "Apply this correction after reopening",
		OperationKey: "midturn-money-reopen-0001", RequestedBy: "test_operator",
		DeliveryMode: domain.OperatorSteeringCurrentTurn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.ReleaseRunExecutionLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	window := llm.DefaultContextWindow()
	window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
	if err := router.SetContextWindow(llm.ModelRef{Provider: provider.Name(), Model: "model"}, window); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunSupervisor(reopened, router, policy.NewDefaultChecker()).
		WithMonetaryBudget(application.NewMonetaryBudgetService(reopened)).Step(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after reopen=%d", provider.calls.Load())
	}
	usage, err := reopened.GetMonetaryUsage(t.Context(), run.ID)
	if err != nil || usage.ReleasedMicros <= 0 || usage.SettledMicros <= 0 {
		t.Fatalf("reopened monetary accounting=%#v err=%v", usage, err)
	}
	stored, err := reopened.GetOperatorSteering(t.Context(), accepted.Message.ID)
	if err != nil || stored.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("reopened correction=%#v err=%v", stored, err)
	}
}

func TestMidTurnCorrectionRejectsStoppingExecutionAndKeepsInputUnqueued(t *testing.T) {
	provider := &gatedThreadProvider{started: make(chan struct{}), release: make(chan struct{}),
		lifecycleProvider: lifecycleProvider{responses: []string{
			rootActionResponse(domain.RootActionFinish, "Unused", "complete", ""),
		}},
	}
	st, turns, request := threadControlFixture(t, provider)
	thread, err := st.GetThread(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.GetRun(t.Context(), thread.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, executeErr := turns.Execute(context.Background(), request); done <- executeErr }()
	awaitTurnSignal(t, provider.started)
	originalCorrection := application.SubmitSessionMessageRequest{
		Version: domain.SessionMessageSubmissionProtocolVersion, SessionID: run.SessionID,
		Content: "accepted before stop", OperationKey: "midturn-before-stopping-0001",
		RequestedBy: "test_operator", DeliveryMode: domain.OperatorSteeringCurrentTurn,
	}
	accepted, err := turns.SubmitCurrentSteering(t.Context(), originalCorrection)
	if err != nil || accepted.Message.ID == "" {
		t.Fatalf("prestop correction=%#v err=%v", accepted, err)
	}
	state, err := turns.ExecutionState(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	stopping, err := turns.Interrupt(t.Context(), request.ThreadID, state.ExecutionID)
	if err != nil || stopping.State != "stopping" {
		t.Fatalf("stop=%#v err=%v", stopping, err)
	}
	replayed, err := turns.SubmitCurrentSteering(t.Context(), originalCorrection)
	if err != nil || !replayed.Replayed || replayed.Message.ID != accepted.Message.ID {
		t.Fatalf("stopping replay=%#v err=%v", replayed, err)
	}
	_, err = turns.SubmitCurrentSteering(t.Context(), application.SubmitSessionMessageRequest{
		Version: domain.SessionMessageSubmissionProtocolVersion, SessionID: run.SessionID,
		Content: "correction after stop", OperationKey: "midturn-stopping-0001",
		RequestedBy: "test_operator", DeliveryMode: domain.OperatorSteeringCurrentTurn,
	})
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("stopping admission code=%s err=%v", apperror.CodeOf(err), err)
	}
	if err := awaitTurnResult(t, done); err == nil {
		t.Fatal("stopped request completed as success")
	}
	_, found, err := st.InspectOperatorSteeringOperation(t.Context(), run.SessionID, "midturn-stopping-0001")
	if err != nil || found {
		t.Fatalf("stopping correction was enqueued: found=%t err=%v", found, err)
	}
	stored, err := st.GetOperatorSteering(t.Context(), accepted.Message.ID)
	if err != nil || stored.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("accepted correction was lost on stop: %#v err=%v", stored, err)
	}
}

type pauseAfterFirstToolStart struct {
	*store.SQLiteStore
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *pauseAfterFirstToolStart) RecordSupervisorToolExecutionStartedWithSteering(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, callID string,
) (bool, bool, error) {
	fresh, superseded, err := s.SQLiteStore.RecordSupervisorToolExecutionStartedWithSteering(ctx, checkpoint, callID)
	if err == nil && fresh && !superseded {
		s.once.Do(func() {
			close(s.started)
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	return fresh, superseded, err
}

func TestMidTurnCorrectionPreservesStartedToolAndSupersedesUndispatchedSibling(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "midturn-tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 5})
	first := toolResponse("old-tool-one", "work_item_create", `{"title":"Started item","priority":"high"}`)
	first.ToolCalls = append(first.ToolCalls, llm.ToolCall{
		ID: "old-tool-two", Name: "work_item_create",
		Arguments: json.RawMessage(`{"title":"Superseded item","priority":"high"}`),
	})
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{first,
		textResponse(rootActionResponse(domain.RootActionContinue, "Only the first tool ran", "", ""))}}
	gate := &pauseAfterFirstToolStart{SQLiteStore: st, started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, stepErr := newToolLoopSupervisor(gate, provider).Step(context.Background(), run.ID)
		done <- stepErr
	}()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first tool did not start")
	}
	correction := "Do not create the second work item"
	accepted, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: correction,
		OperationKey: "midturn-tool-sibling-0001", RequestedBy: "test_operator",
		DeliveryMode: domain.OperatorSteeringCurrentTurn,
	})
	if err != nil || accepted.Message.ID == "" {
		t.Fatalf("correction admission=%#v err=%v", accepted, err)
	}
	close(gate.release)
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListWorkItems(t.Context(), domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 || items[0].Title != "Started item" {
		t.Fatalf("started tool was lost or stale sibling ran: %#v err=%v", items, err)
	}
	eventList, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(eventList, events.SupervisorToolExecutionStartedEvent) != 1 ||
		countEventType(eventList, events.SupervisorToolExecutionCompletedEvent) != 1 ||
		countEventType(eventList, events.SupervisorToolResultEvent) != 2 {
		t.Fatalf("tool execution events did not distinguish started and not dispatched: %#v", eventList)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 2)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 2 ||
		rounds[0].Calls[1].ErrorCode != "steering_superseded" ||
		!strings.Contains(rounds[0].Calls[1].ResultJSON, `"outcome":"not_dispatched"`) {
		t.Fatalf("old sibling lacks a genuine not-dispatched receipt: %#v err=%v", rounds, err)
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d", len(requests))
	}
	found := false
	for _, message := range requests[1].Messages {
		found = found || strings.Contains(message.Content, correction)
	}
	if !found {
		t.Fatalf("correction missing from provider continuation: %#v", requests[1].Messages)
	}
}
