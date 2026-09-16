package application_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

type historicalFailedTurnStore struct{ *store.SQLiteStore }

func (s *historicalFailedTurnStore) EndFailedThreadTurn(context.Context, string, string, string) (domain.ThreadTurnFailure, bool, error) {
	return domain.ThreadTurnFailure{}, false, nil
}

func TestThreadTurnFailureClosureIsAtomicAcrossConnectionsAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failed-turn.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "preserve failed inputs", Profile: "review", ModelRoute: "lifecycle-test/model", Interactive: true, Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{failures: []error{apperror.New(apperror.CodeUnavailable, "temporary provider failure")}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	legacy := &historicalFailedTurnStore{st}
	turns := application.NewThreadTurnService(legacy, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	request := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), Content: "Keep my original constraint", OperationKey: "failure-atomic-first", RequestedBy: "test_operator"}
	first, err := turns.Execute(t.Context(), request)
	if err == nil || first.Execution == nil {
		t.Fatalf("fixture did not fail durably: %#v %v", first, err)
	}
	handoff := first.Execution.Handoff
	lease, err := other.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "test-closure-owner", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, closed, err := st.EndFailedThreadTurn(t.Context(), request.ThreadID, run.ID, handoff.Operation.ID); err == nil || closed {
		t.Fatalf("active lease was not fenced: %v %v", closed, err)
	}
	if _, _, err := other.ReleaseRunExecutionLease(t.Context(), lease.Lease); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER test_fail_thread_end BEFORE INSERT ON run_events WHEN NEW.type='thread.turn_failed' BEGIN SELECT RAISE(ABORT,'injected failure event write'); END`); err != nil {
		t.Fatal(err)
	}
	if _, closed, err := st.EndFailedThreadTurn(t.Context(), request.ThreadID, run.ID, handoff.Operation.ID); err == nil || closed {
		t.Fatalf("injected write unexpectedly committed: %v %v", closed, err)
	}
	message, err := st.GetOperatorSteering(t.Context(), first.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringPending || !message.Prepared {
		t.Fatalf("failed closure partially consumed input: %#v %v", message, err)
	}
	messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(messages) != 0 {
		t.Fatalf("failed closure leaked session writes: %#v %v", messages, err)
	}
	if _, err := db.Exec(`DROP TRIGGER test_fail_thread_end`); err != nil {
		t.Fatal(err)
	}
	var results [2]domain.ThreadTurnFailure
	var failures [2]error
	var wg sync.WaitGroup
	for i, conn := range []*store.SQLiteStore{st, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var closed bool
			results[i], closed, failures[i] = conn.EndFailedThreadTurn(t.Context(), request.ThreadID, run.ID, handoff.Operation.ID)
			if failures[i] == nil && !closed {
				failures[i] = errors.New("closure missing")
			}
		}()
	}
	wg.Wait()
	if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("connection race duplicated closure: %#v %#v", results, failures)
	}
	messages, err = st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(messages) != 2 {
		t.Fatalf("closure duplicated session messages: %#v %v", messages, err)
	}
	checkpoint, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found || checkpoint.NextTurn != 2 || checkpoint.AttemptID != "" {
		t.Fatalf("invalid next turn: %#v %v", checkpoint, err)
	}
	stored, found, err := st.GetRunExecutionHandoff(t.Context(), handoff.Operation.KeyDigest)
	if err != nil || !found || !reflect.DeepEqual(stored, handoff) {
		t.Fatalf("failed handoff was rewritten: %#v %v", stored, err)
	}
	upgraded := application.NewThreadTurnService(other, application.NewRunLifecycleControlService(other), application.NewRunExecutionHandoffService(other, router, policy.NewDefaultChecker()))
	_, err = upgraded.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || provider.calls != 1 {
		t.Fatalf("reopen replay forgot failure: calls=%d %v", provider.calls, err)
	}
}

func TestThreadTurnFailureKeepsCompletedToolEvidenceWithoutReexecution(t *testing.T) {
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{toolResponse("note-before-failure", "note_create", `{"title":"Observed once","content":"Do not repeat the completed action."}`)}}
	st, turns, request := threadControlFixture(t, provider)
	first, err := turns.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || first.Execution == nil {
		t.Fatalf("completed tool failure was not sealed: %#v %v", first, err)
	}
	notes, err := st.ListNotes(t.Context(), domain.NoteFilter{RunID: first.Submission.Run.ID})
	if err != nil || len(notes) != 1 {
		t.Fatalf("tool did not execute exactly once: %#v %v", notes, err)
	}
	provider.mu.Lock()
	provider.responses = append(provider.responses, textResponse(rootActionResponse(domain.RootActionFinish, "Continued using the recorded result", "done", "")))
	provider.mu.Unlock()
	next := request
	next.Content = "Continue without repeating the note; inspect its previous result"
	next.OperationKey = "failure-tool-next"
	second, err := turns.Execute(t.Context(), next)
	if err != nil || second.Submission.Run.ID != first.Submission.Run.ID {
		t.Fatalf("new turn failed: %#v %v", second, err)
	}
	notes, err = st.ListNotes(t.Context(), domain.NoteFilter{RunID: first.Submission.Run.ID})
	if err != nil || len(notes) != 1 {
		t.Fatalf("completed tool was replayed: %#v %v", notes, err)
	}
	requests := provider.Requests()
	last := requests[len(requests)-1]
	sawEvidence := false
	sawOriginal := false
	for _, message := range last.Messages {
		sawEvidence = sawEvidence || strings.Contains(message.Content, "note_create") && strings.Contains(message.Content, "result SHA256")
		sawOriginal = sawOriginal || message.Role == "user" && strings.Contains(message.Content, request.Content)
	}
	if !sawEvidence || !sawOriginal {
		t.Fatalf("next model lost prior input or completed tool evidence: %#v", last.Messages)
	}
	_, err = turns.Execute(t.Context(), request)
	if !errors.As(err, &failed) || len(provider.Requests()) != len(requests) {
		t.Fatalf("old key reexecuted or hid failed outcome: %v", err)
	}
}

func TestThreadTurnUnresolvedToolResultCannotStartNewTurn(t *testing.T) {
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{toolResponse("note-uncertain", "note_create", `{"title":"Uncertain result","content":"Its effect may already exist."}`)}}
	st, _, request := threadControlFixture(t, provider)
	fault := &failOnceToolResultStore{SQLiteStore: st, fail: true}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(fault, application.NewRunLifecycleControlService(fault), application.NewRunExecutionHandoffService(fault, router, policy.NewDefaultChecker()))
	first, err := turns.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if err == nil || errors.As(err, &failed) {
		t.Fatalf("uncertain tool was claimed settled: %v", err)
	}
	request.Content = "New requirement must not replay uncertain tools"
	request.OperationKey = "uncertain-new-request"
	_, err = turns.Execute(t.Context(), request)
	if err == nil || errors.As(err, &failed) || len(provider.Requests()) != 1 {
		t.Fatalf("new turn crossed unresolved result: calls=%d err=%v", len(provider.Requests()), err)
	}
	message, err := st.GetOperatorSteering(t.Context(), first.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringPending || !message.Prepared {
		t.Fatalf("uncertain input was discarded: %#v %v", message, err)
	}
}

type gateAfterFailedTurnProvider struct {
	lifecycleProvider
	started chan struct{}
	once    sync.Once
}

func (p *gateAfterFailedTurnProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	if p.calls > 0 {
		p.once.Do(func() { close(p.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.lifecycleProvider.Chat(ctx, request)
}
func (p *gateAfterFailedTurnProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 1)
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}

func TestThreadTurnFailedOriginalKeyStaysFailedWhileNextTurnRuns(t *testing.T) {
	provider := &gateAfterFailedTurnProvider{started: make(chan struct{}), lifecycleProvider: lifecycleProvider{failures: []error{apperror.New(apperror.CodeUnavailable, "temporary model failure")}}}
	_, turns, request := threadControlFixture(t, provider)
	if _, err := turns.Execute(t.Context(), request); err == nil {
		t.Fatal("expected first failure")
	}
	next := request
	next.Content = "New explicit requirement"
	next.OperationKey = "new-turn-after-known-failure"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(ctx, next); done <- err }()
	awaitTurnSignal(t, provider.started)
	_, err := turns.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("active queue admission relabeled old failure: %v", err)
	}
	cancel()
	_ = awaitTurnResult(t, done)
}

func TestThreadTurnNewMessageClosesHistoricalFailedAttemptWithoutRestart(t *testing.T) {
	provider := &lifecycleProvider{responses: []string{"", rootActionResponse(domain.RootActionFinish, "New request completed", "done", "")}, failures: []error{apperror.New(apperror.CodeUnavailable, "historical temporary failure")}}
	st, _, request := threadControlFixture(t, provider)
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	legacy := application.NewThreadTurnService(&historicalFailedTurnStore{st}, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	first, err := legacy.Execute(t.Context(), request)
	if err == nil || first.Execution == nil {
		t.Fatalf("expected durable old failure: %#v %v", first, err)
	}
	if paused, err := st.GetRun(t.Context(), first.Submission.Run.ID); err != nil || paused.Status != domain.RunPaused {
		t.Fatalf("old failed attempt must await new operator input: %#v %v", paused, err)
	}
	upgraded := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	next := request
	next.Content = "Use my new requirement instead of retrying old actions"
	next.OperationKey = "upgraded-new-turn"
	continued, err := upgraded.Execute(t.Context(), next)
	if err != nil || continued.Submission.Run.ID != first.Submission.Run.ID ||
		continued.Submission.Message.Status != domain.OperatorSteeringCommitted || provider.calls != 2 {
		t.Fatalf("upgrade did not close old failed product turn: %#v calls=%d %v", continued, provider.calls, err)
	}
	oldFailure, found, err := st.GetThreadTurnFailure(t.Context(), first.Submission.Run.ID, first.Submission.Message.ID)
	if err != nil || !found || oldFailure.HandoffOperationID != first.Execution.Handoff.Operation.ID {
		t.Fatalf("new message did not preserve the exact old failed input: %#v found=%v %v", oldFailure, found, err)
	}
	_, err = upgraded.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || provider.calls != 2 {
		t.Fatalf("upgrade lost immutable old failure: %v", err)
	}
}
