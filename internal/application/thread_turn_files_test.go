package application_test

import (
	"context"
	"errors"
	"os"
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
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspace"
)

func threadFilesFixture(t *testing.T, provider llm.Provider) (*store.SQLiteStore, *application.ThreadTurnService, application.ExecuteThreadTurnRequest, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "thread-files.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	for _, name := range []string{"first.txt", "second.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture "+name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "ws-thread-files", Name: "thread-files", RootPath: root, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "Review selected project files", Profile: "review", WorkspaceID: "ws-thread-files",
		ModelRoute: provider.Name() + "/model", Interactive: true, Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	request := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
		Content: "Review these references", OperationKey: "thread-files-operation-0001", RequestedBy: "test_operator"}
	for _, name := range []string{"first.txt", "second.txt"} {
		projection, err := workspace.Explore(root, "ws-thread-files", name)
		if err != nil {
			t.Fatal(err)
		}
		request.Files = append(request.Files, domain.WorkspaceFileReference{SourceKind: session.SourceWorkspaceFile, Path: name, ExpectedSHA256: projection.Provenance.ContentSHA256})
	}
	return st, newThreadFilesService(st, provider), request, root, path
}

func newThreadFilesService(st *store.SQLiteStore, provider llm.Provider) *application.ThreadTurnService {
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	return application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
}

func TestThreadTurnFilesFirstAndSuccessorUseActualRunAndReplaySnapshots(t *testing.T) {
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionFinish, "Reviewed first files", "done", ""),
		rootActionResponse(domain.RootActionFinish, "Reviewed successor files", "done", ""),
	}}
	st, turns, request, root, path := threadFilesFixture(t, provider)
	first, err := turns.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Submission.Message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("first=%#v", first)
	}
	// End this finite Run through the existing lifecycle before continuing the
	// stable Thread; an interactive review's requested finish may keep it live.
	if _, err := application.NewRunService(st).Fail(t.Context(), first.Submission.Run.ID, "fixture predecessor ended"); err != nil {
		t.Fatal(err)
	}
	for _, file := range request.Files {
		found := false
		for _, message := range provider.requests[0].Messages {
			if strings.Contains(message.Content, "fixture "+file.Path) && strings.Contains(message.Content, session.UntrustedContextEnvelopeVersion) {
				found = true
			}
		}
		if !found {
			t.Fatalf("model did not receive untrusted %s", file.Path)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "first.txt"), []byte("changed after accepted"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay, err := newThreadFilesService(reopened, provider).Execute(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Submission.Message.ID != first.Submission.Message.ID || provider.calls != 1 {
		t.Fatalf("replay=%#v err=%v calls=%d", replay, err, provider.calls)
	}
	changed := request
	changed.Files = nil
	if _, err := turns.Execute(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("files omission replay=%v", err)
	}
	changed.Files = append([]domain.WorkspaceFileReference(nil), request.Files...)
	changed.Files[0].ExpectedSHA256 = strings.Repeat("0", 64)
	if _, err := turns.Execute(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed manifest replay=%v", err)
	}
	if _, err := application.NewThreadService(st).Submit(t.Context(), application.SubmitThreadMessageRequest{Version: request.Version, ThreadID: request.ThreadID, Content: request.Content, OperationKey: request.OperationKey, RequestedBy: request.RequestedBy}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("legacy bypass=%v", err)
	}
	next := request
	next.OperationKey = "thread-files-operation-0002"
	next.Files = append([]domain.WorkspaceFileReference(nil), request.Files...)
	projection, _ := workspace.Explore(root, "ws-thread-files", "first.txt")
	next.Files[0].ExpectedSHA256 = projection.Provenance.ContentSHA256
	second, err := turns.Execute(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	if second.Submission.Run.ID == first.Submission.Run.ID || !second.Submission.SuccessorCreated || second.Submission.PredecessorRunID != first.Submission.Run.ID {
		t.Fatalf("successor=%#v", second.Submission)
	}
	items, err := st.ListEvidenceAttachments(t.Context(), second.Submission.Run.ID, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("successor evidence=%#v err=%v", items, err)
	}
	for _, item := range items {
		if item.SessionID != second.Submission.Session.ID {
			t.Fatal("evidence attached to predecessor session")
		}
	}
}

func TestThreadTurnFilesFailureRejectsIntentBeforeLifecycleAndCanCorrectWithNewKey(t *testing.T) {
	provider := &lifecycleProvider{responses: []string{rootActionResponse(domain.RootActionFinish, "Done", "done", "")}}
	st, turns, request, root, path := threadFilesFixture(t, provider)
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := turns.Execute(t.Context(), request)
	var notQueued *application.ThreadMessageNotQueuedError
	if !errors.As(err, &notQueued) {
		t.Fatalf("missing durable negative acknowledgement: %v", err)
	}
	thread, _ := st.GetThread(t.Context(), request.ThreadID)
	run, _ := st.GetRun(t.Context(), thread.LastRunID)
	if run.Status != domain.RunCreated || provider.calls != 0 {
		t.Fatalf("invalid references started Run: %#v calls=%d", run, provider.calls)
	}
	messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(messages) != 0 {
		t.Fatalf("partial evidence leaked: %#v %v", messages, err)
	}
	queued, err := st.ListOperatorSteering(t.Context(), run.ID, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("failed files queued: %#v %v", queued, err)
	}
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("fixture second.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	_, err = newThreadFilesService(reopened, provider).Execute(t.Context(), request)
	if !errors.As(err, &notQueued) {
		t.Fatalf("rejected key was resurrected: %v", err)
	}
	request.OperationKey = "thread-files-corrected-operation-0002"
	result, err := turns.Execute(t.Context(), request)
	if err != nil || result.Submission.Run.ID != run.ID || provider.calls != 1 {
		t.Fatalf("correction=%#v %v calls=%d", result, err, provider.calls)
	}
}

func TestThreadTurnFilesInFlightReplayAndBusyRejectionKeepStopOwnership(t *testing.T) {
	provider := blockingProvider{started: make(chan struct{}, 1)}
	st, turns, request, _, _ := threadFilesFixture(t, provider)
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(t.Context(), request); done <- err }()
	awaitTurnSignal(t, provider.started)
	replay, err := turns.Execute(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Submission.Message.ID == "" {
		t.Fatalf("in-flight replay=%#v %v", replay, err)
	}
	next := request
	next.OperationKey = "thread-files-busy-operation-0002"
	_, err = turns.Execute(t.Context(), next)
	var notQueued *application.ThreadMessageNotQueuedError
	if !errors.As(err, &notQueued) {
		t.Fatalf("busy request not rejected: %v", err)
	}
	items, _ := st.ListEvidenceAttachments(t.Context(), replay.Submission.Run.ID, 10)
	if len(items) != 2 {
		t.Fatalf("duplicate evidence=%#v", items)
	}
	text := next
	text.Files = nil
	text.OperationKey = "thread-files-text-operation-0003"
	queued, err := turns.Execute(t.Context(), text)
	if err != nil || queued.Submission.Message.Status != domain.OperatorSteeringPending {
		t.Fatalf("plain text queue=%#v %v", queued, err)
	}
	state, _ := turns.ExecutionState(t.Context(), request.ThreadID)
	if _, err := turns.Interrupt(t.Context(), request.ThreadID, state.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := awaitTurnResult(t, done); err == nil || errors.As(err, &notQueued) {
		t.Fatalf("accepted interrupted turn falsely not queued: %v", err)
	}
}

type blockedThreadFileStore struct {
	*store.SQLiteStore
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockedThreadFileStore) GetWorkspaceInfo(ctx context.Context, id string) (session.WorkspaceInfo, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return session.WorkspaceInfo{}, ctx.Err()
	}
	return s.SQLiteStore.GetWorkspaceInfo(ctx, id)
}

func TestThreadTurnFilesRetryWhilePreparingDoesNotRejectItsOwner(t *testing.T) {
	provider := &lifecycleProvider{responses: []string{rootActionResponse(domain.RootActionContinue, "Reviewed", "", "")}}
	st, _, request, _, _ := threadFilesFixture(t, provider)
	blocked := &blockedThreadFileStore{SQLiteStore: st, started: make(chan struct{}), release: make(chan struct{})}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(blocked, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(t.Context(), request); done <- err }()
	awaitTurnSignal(t, blocked.started)
	_, err := turns.Execute(t.Context(), request)
	var notQueued *application.ThreadMessageNotQueuedError
	if apperror.CodeOf(err) != apperror.CodeUnavailable || errors.As(err, &notQueued) {
		t.Fatalf("preparing retry rejected owner: %v", err)
	}
	close(blocked.release)
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatalf("owner failed after retry: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("model calls=%d", provider.calls)
	}
}

type blockedThreadSuccessorStore struct {
	*store.SQLiteStore
	started chan struct{}
	release chan struct{}
}

func (s *blockedThreadSuccessorStore) EnsureThreadSuccessorForMessage(ctx context.Context, request domain.ThreadMessageIntentRequest,
	predecessor string, mission domain.Mission, candidate domain.Run, mode domain.RunModeSnapshot,
	linkedSession session.Session, initialEvents []events.Event,
) (domain.Thread, domain.Run, bool, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return domain.Thread{}, domain.Run{}, false, ctx.Err()
	}
	return s.SQLiteStore.EnsureThreadSuccessorForMessage(ctx, request, predecessor, mission, candidate, mode, linkedSession, initialEvents)
}

func TestThreadTurnFilesConcurrentCompletedSuccessorDoesNotCreateAnotherRun(t *testing.T) {
	provider := &lifecycleProvider{responses: []string{rootActionResponse(domain.RootActionContinue, "Reviewed successor", "", "")}}
	st, _, request, _, path := threadFilesFixture(t, provider)
	thread, _ := st.GetThread(t.Context(), request.ThreadID)
	runs := application.NewRunService(st)
	if _, err := runs.Start(t.Context(), thread.LastRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Fail(t.Context(), thread.LastRunID, "predecessor ended"); err != nil {
		t.Fatal(err)
	}
	blocked := &blockedThreadSuccessorStore{SQLiteStore: st, started: make(chan struct{}), release: make(chan struct{})}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	slow := application.NewThreadTurnService(blocked, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	type outcome struct {
		result application.ExecuteThreadTurnResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() { result, err := slow.Execute(t.Context(), request); done <- outcome{result, err} }()
	awaitTurnSignal(t, blocked.started)
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	accepted, err := newThreadFilesService(other, provider).Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(other).Fail(t.Context(), accepted.Submission.Run.ID, "concurrent accepted successor ended"); err != nil {
		t.Fatal(err)
	}
	close(blocked.release)
	select {
	case replay := <-done:
		if replay.err != nil || !replay.result.Replayed || replay.result.Submission.Message.ID != accepted.Submission.Message.ID {
			t.Fatalf("successor race=%#v %v", replay.result, replay.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent successor retry did not finish")
	}
	bindings, err := st.ListThreadRuns(t.Context(), request.ThreadID)
	if err != nil || len(bindings) != 2 || provider.calls != 1 {
		t.Fatalf("duplicate successor: bindings=%#v calls=%d err=%v", bindings, provider.calls, err)
	}
}
