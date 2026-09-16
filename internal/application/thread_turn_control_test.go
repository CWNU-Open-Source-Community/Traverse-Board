package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func threadControlFixture(t *testing.T, provider llm.Provider) (*store.SQLiteStore, *application.ThreadTurnService, application.ExecuteThreadTurnRequest) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "turn-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "Thread execution control", Profile: "review", ModelRoute: provider.Name() + "/model",
		Interactive: true, Budget: domain.Budget{MaxTurns: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	return st, turns, application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
		Content: "first input", OperationKey: "thread-control-first-operation-0001", RequestedBy: "test_operator",
	}
}

func awaitTurnSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
}

func awaitTurnResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
		return nil
	}
}

func TestThreadTurnInterruptFencesExecutionAndPreservesDurableQueue(t *testing.T) {
	provider := blockingProvider{started: make(chan struct{}, 2)}
	st, turns, request := threadControlFixture(t, provider)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(ctx, request); done <- err }()
	awaitTurnSignal(t, provider.started)
	state, err := turns.ExecutionState(t.Context(), request.ThreadID)
	if err != nil || state.State != "running" || state.ExecutionID == "" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	if _, err := turns.Interrupt(t.Context(), request.ThreadID, "thread-execution-stale"); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale stop was accepted: %v", err)
	}
	queued := request
	queued.Content, queued.OperationKey = "Only edit frontend files next", "thread-control-queued-operation-0002"
	accepted, err := turns.Execute(t.Context(), queued)
	if err != nil || accepted.Submission.Message.Status != domain.OperatorSteeringPending || accepted.ExecutionStarted {
		t.Fatalf("queue admission=%#v err=%v", accepted, err)
	}
	replayed, err := turns.Execute(t.Context(), queued)
	if err != nil || !replayed.Replayed || replayed.Submission.Message.ID != accepted.Submission.Message.ID {
		t.Fatalf("queue replay=%#v err=%v", replayed, err)
	}
	state, _ = turns.ExecutionState(t.Context(), request.ThreadID)
	if state.QueuedMessages != 1 {
		t.Fatalf("duplicate queue: %#v", state)
	}
	stopping, err := turns.Interrupt(t.Context(), request.ThreadID, state.ExecutionID)
	if err != nil || stopping.State != "stopping" {
		t.Fatalf("stop=%#v err=%v", stopping, err)
	}
	if err := awaitTurnResult(t, done); err == nil {
		t.Fatal("interruption was reported as successful execution")
	}
	state, err = turns.ExecutionState(t.Context(), request.ThreadID)
	if err != nil || state.State != "idle" || state.ExecutionID != "" || !state.LastTurnInterrupted {
		t.Fatalf("cleanup=%#v err=%v", state, err)
	}
	message, err := st.GetOperatorSteering(t.Context(), accepted.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringPending || message.Prepared {
		t.Fatalf("queued message was lost or consumed during stop: %#v %v", message, err)
	}
	failure, found, err := st.GetLatestThreadTurnFailure(t.Context(), request.ThreadID)
	if err != nil || !found || failure.ErrorCode != string(apperror.CodeCancelled) {
		t.Fatalf("stop lost durable failed outcome: %#v %v", failure, err)
	}
	// A delayed stop for the first request must not stop the next execution.
	next := request
	next.OperationKey, next.Content = "thread-control-next-operation-0003", "Explicitly continue after stopping"
	nextDone := make(chan error, 1)
	go func() { _, err := turns.Execute(ctx, next); nextDone <- err }()
	awaitTurnSignal(t, provider.started)
	if _, err := turns.Interrupt(t.Context(), request.ThreadID, stopping.ExecutionID); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("old stop reached new execution: %v", err)
	}
	current, _ := turns.ExecutionState(t.Context(), request.ThreadID)
	_, _ = turns.Interrupt(t.Context(), request.ThreadID, current.ExecutionID)
	_ = awaitTurnResult(t, nextDone)
}

type failingThreadQueueStore struct {
	*store.SQLiteStore
	failCancellation atomic.Bool
}

func (s *failingThreadQueueStore) CancelOperatorSteering(ctx context.Context, request domain.CancelOperatorSteeringRequest) (domain.OperatorSteeringCancellationResult, error) {
	if s.failCancellation.Load() {
		return domain.OperatorSteeringCancellationResult{}, errors.New("injected queue cancellation failure")
	}
	return s.SQLiteStore.CancelOperatorSteering(ctx, request)
}

func TestThreadTurnStopDoesNotDependOnCancellingAcceptedInputs(t *testing.T) {
	provider := blockingProvider{started: make(chan struct{}, 1)}
	st, _, request := threadControlFixture(t, provider)
	faultStore := &failingThreadQueueStore{SQLiteStore: st}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(faultStore, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(ctx, request); done <- err }()
	awaitTurnSignal(t, provider.started)
	queued := request
	queued.Content, queued.OperationKey = "Keep this follow-up after stopping", "thread-fault-queued-operation-0002"
	accepted, err := turns.Execute(t.Context(), queued)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := turns.ExecutionState(t.Context(), request.ThreadID)
	faultStore.failCancellation.Store(true)
	_, err = turns.Interrupt(t.Context(), request.ThreadID, state.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if awaitTurnResult(t, done) == nil {
		t.Fatal("failed cleanup was reported as success")
	}
	failed, err := turns.ExecutionState(t.Context(), request.ThreadID)
	if err != nil || failed.State != "idle" || !failed.LastTurnInterrupted {
		t.Fatalf("stop unnecessarily depends on cancellation: %#v %v", failed, err)
	}
	message, err := st.GetOperatorSteering(t.Context(), accepted.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringPending || message.Prepared {
		t.Fatalf("stop did not retain pending input: %#v %v", message, err)
	}
}

type gatedThreadProvider struct {
	lifecycleProvider
	first   sync.Once
	started chan struct{}
	release chan struct{}
}

func (p *gatedThreadProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	first := false
	p.first.Do(func() { first = true })
	if first {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.lifecycleProvider.Chat(ctx, request)
}

func (p *gatedThreadProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 2)
	chunks <- llm.ChatChunk{Text: response.Text}
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}

func TestThreadTurnConsumesAcceptedQueueOnceAtNextBoundary(t *testing.T) {
	provider := &gatedThreadProvider{started: make(chan struct{}), release: make(chan struct{}),
		lifecycleProvider: lifecycleProvider{responses: []string{
			rootActionResponse(domain.RootActionFinish, "First complete", "complete", ""),
			rootActionResponse(domain.RootActionFinish, "Frontend-only follow-up complete", "complete", ""),
		}},
	}
	st, turns, request := threadControlFixture(t, provider)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(ctx, request); done <- err }()
	awaitTurnSignal(t, provider.started)
	queued := request
	queued.Content, queued.OperationKey = "Only edit the frontend next", "thread-control-followup-operation-0002"
	accepted, err := turns.Execute(t.Context(), queued)
	if err != nil || accepted.ExecutionStarted || accepted.Submission.Message.Status != domain.OperatorSteeringPending {
		t.Fatalf("queue=%#v err=%v", accepted, err)
	}
	close(provider.release)
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatal(err)
	}
	message, err := st.GetOperatorSteering(t.Context(), accepted.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringCommitted || provider.calls != 2 {
		t.Fatalf("queued input was not consumed once: %#v calls=%d err=%v", message, provider.calls, err)
	}
	if _, err := turns.Execute(t.Context(), queued); err != nil || provider.calls != 2 {
		t.Fatalf("accepted message retry re-executed the model: calls=%d err=%v", provider.calls, err)
	}
}
