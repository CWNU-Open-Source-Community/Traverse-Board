package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type historicalWebFetchFailureStore struct{ *store.SQLiteStore }

func (s *historicalWebFetchFailureStore) PrepareWebFetchAuthorizationHandoff(context.Context, string,
	string, domain.SupervisorPhase,
) (domain.RunExecutionHandoff, bool, error) {
	return domain.RunExecutionHandoff{}, false, nil
}

func restoreHistoricalWebFetchRunState(t *testing.T, st *store.SQLiteStore, runID string) {
	t.Helper()
	before, found, err := st.GetSupervisorCheckpoint(t.Context(), runID)
	if err != nil || !found || before.Phase != domain.SupervisorTurnFailed {
		t.Fatalf("historical fixture has no failed checkpoint: %#v err=%v", before, err)
	}
	// The old resume path neither created a continuation handoff nor paused
	// the Run on failure. Restore that historical status through the normal
	// lifecycle interface; retain the exact failed attempt, input and cause.
	run, err := application.NewRunService(st).Resume(t.Context(), runID)
	if err != nil || run.Status != domain.RunRunning {
		t.Fatalf("restore historical running status: %#v err=%v", run, err)
	}
	after, found, err := st.GetSupervisorCheckpoint(t.Context(), runID)
	if err != nil || !found || !reflect.DeepEqual(before, after) {
		t.Fatalf("historical setup changed the failed checkpoint: before=%#v after=%#v err=%v", before, after, err)
	}
}

func webFetchThreadFailureFixture(t *testing.T, historical bool) (*store.SQLiteStore,
	*application.ThreadTurnService, *application.RunExecutionHandoffService,
	*scriptedToolProvider, *applicationWebFetchBackend,
	application.ExecuteThreadTurnRequest, application.ExecuteThreadTurnResult, domain.WebFetchAuthorization,
) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "web-fetch-thread-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "read an approved source and preserve the result on failure", Profile: "review",
		Surface: "code", Phase: "deliver", ModelRoute: "tool-loop/model", Interactive: true,
		NetworkMode: "disabled", Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("web-fetch-thread-failure", "web_fetch",
			`{"version":"web_fetch.v1","url":"https://new.example.net/report"}`),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	checker := policy.NewDefaultChecker()
	backend := &applicationWebFetchBackend{}
	var handoffStore application.RunExecutionHandoffStore = st
	if historical {
		handoffStore = &historicalWebFetchFailureStore{st}
	}
	handoff := application.NewRunExecutionHandoffService(handoffStore, router, checker).
		WithWebEvidence(webevidence.NewService(st, nil, backend)).WithWebFetchAuthorizationScheduler(true)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), handoff)
	request := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion,
		ThreadID: domain.InitialThreadID(run.ID), Content: "Read the source; retain this original constraint",
		OperationKey: "web-fetch-original-turn", RequestedBy: "test_operator"}
	first, err := turns.Execute(t.Context(), request)
	if err != nil || first.Submission.Run.Status != domain.RunWaitingApproval || backend.calls != 0 ||
		first.Execution == nil || first.Execution.Handoff.Result.Status != domain.RunExecutionHandoffCompleted {
		t.Fatalf("initial approval boundary=%#v fetches=%d err=%v", first, backend.calls, err)
	}
	records, err := st.ListApprovals(t.Context(), approval.ListFilter{RunID: run.ID, Status: approval.StatusPending, Limit: 10})
	if err != nil || len(records) != 1 {
		t.Fatalf("approval records=%#v err=%v", records, err)
	}
	value, err := st.GetWebFetchAuthorizationByApproval(t.Context(), records[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.NewApprovalControlService(st, toolgateway.New(st, checker), checker).
		Decide(t.Context(), application.DecideApprovalControlRequest{
			Version: application.ApprovalControlProtocolVersion, RunID: run.ID, ApprovalID: records[0].ID,
			Action: application.ApprovalControlApproveOnce, OperationKey: "web-fetch-failure-approve-once", ReviewedBy: "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	return st, turns, handoff, provider, backend, request, first, value
}

func TestWebFetchApprovalResumeFailureSealsThreadTurnAndPreservesEvidence(t *testing.T) {
	st, turns, handoff, provider, backend, request, first, authorization := webFetchThreadFailureFixture(t, false)
	queued, err := application.NewThreadService(st).Submit(t.Context(), application.SubmitThreadMessageRequest{
		Version: request.Version, ThreadID: request.ThreadID, Content: "Keep this separately accepted follow-up",
		OperationKey: "web-fetch-queued-followup", RequestedBy: request.RequestedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = handoff.ResumeWebFetchAuthorization(t.Context(), authorization.RunID, authorization.ID)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || backend.calls != 1 {
		t.Fatalf("approval continuation failure was not sealed: fetches=%d err=%v", backend.calls, err)
	}
	failure, found, err := st.GetLatestThreadTurnFailure(t.Context(), request.ThreadID)
	if err != nil || !found || failure.MessageID != first.Submission.Message.ID {
		t.Fatalf("durable Thread outcome=%#v found=%t err=%v", failure, found, err)
	}
	checkpoint, found, err := st.GetSupervisorCheckpoint(t.Context(), authorization.RunID)
	if err != nil || !found || checkpoint.Phase != domain.SupervisorIdle || checkpoint.NextTurn != 2 || checkpoint.AttemptID != "" {
		t.Fatalf("failed turn kept the Run unconfigurable: %#v err=%v", checkpoint, err)
	}
	message, err := st.GetOperatorSteering(t.Context(), queued.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringPending || message.Prepared {
		t.Fatalf("failure consumed a separate follow-up: %#v err=%v", message, err)
	}
	originalHandoff, found, err := st.GetRunExecutionHandoff(t.Context(), first.Execution.Handoff.Operation.KeyDigest)
	if err != nil || !found || !reflect.DeepEqual(originalHandoff, first.Execution.Handoff) {
		t.Fatalf("approval-wait handoff was rewritten: %#v err=%v", originalHandoff, err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), authorization.RunID, 0, 10)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 || rounds[0].Calls[0].Status != domain.SupervisorToolCompleted {
		t.Fatalf("completed fetch journal was lost: %#v err=%v", rounds, err)
	}
	messages, err := st.ListSessionMessages(t.Context(), first.Submission.Run.SessionID, true)
	if err != nil || len(messages) != 2 || messages[0].Content != request.Content ||
		!strings.Contains(messages[1].Content, "web_fetch") || !strings.Contains(messages[1].Content, "result SHA256") {
		t.Fatalf("original input or tool evidence missing: %#v err=%v", messages, err)
	}
	paused, err := st.GetRun(t.Context(), authorization.RunID)
	if err != nil || paused.Status != domain.RunPaused {
		t.Fatalf("settled failure did not pause for configuration: %#v err=%v", paused, err)
	}
	before := len(provider.Requests())
	_, err = turns.Execute(t.Context(), request)
	if !errors.As(err, &failed) || len(provider.Requests()) != before || backend.calls != 1 {
		t.Fatalf("original request replay lost failure or repeated work: calls=%d fetches=%d err=%v", len(provider.Requests()), backend.calls, err)
	}
	provider.mu.Lock()
	provider.responses = append(provider.responses, textResponse(rootActionResponse(domain.RootActionFinish, "Follow-up using the retained fetch", "done", "")))
	provider.mu.Unlock()
	next := request
	next.Content, next.OperationKey = queued.Message.Content, "web-fetch-queued-followup"
	continued, err := turns.Execute(t.Context(), next)
	if err != nil || continued.Submission.Run.ID != authorization.RunID || backend.calls != 1 {
		t.Fatalf("ordinary follow-up failed: %#v fetches=%d err=%v", continued, backend.calls, err)
	}
	lastRequest := provider.Requests()[len(provider.Requests())-1]
	var sawOriginal, sawEvidence bool
	for _, item := range lastRequest.Messages {
		sawOriginal = sawOriginal || item.Role == "user" && strings.Contains(item.Content, request.Content)
		sawEvidence = sawEvidence || strings.Contains(item.Content, "web_fetch") && strings.Contains(item.Content, "result SHA256")
	}
	if !sawOriginal || !sawEvidence {
		t.Fatalf("next turn lost retained history: %#v", lastRequest.Messages)
	}
}

func TestHistoricalWebFetchFailureContinuesThroughOrdinaryThreadMessage(t *testing.T) {
	st, turns, handoff, provider, backend, request, first, authorization := webFetchThreadFailureFixture(t, true)
	if _, _, err := handoff.ResumeWebFetchAuthorization(t.Context(), authorization.RunID, authorization.ID); err == nil {
		t.Fatal("historical continuation unexpectedly succeeded")
	}
	restoreHistoricalWebFetchRunState(t, st, authorization.RunID)
	checkpoint, _, err := st.GetSupervisorCheckpoint(t.Context(), authorization.RunID)
	if err != nil || checkpoint.Phase != domain.SupervisorTurnFailed || backend.calls != 1 {
		t.Fatalf("historical failure boundary missing: %#v fetches=%d err=%v", checkpoint, backend.calls, err)
	}
	if recovery, found, err := st.GetThreadRunRecovery(t.Context(), request.ThreadID); err != nil || found {
		t.Fatalf("historical fixture has a failed handoff: %#v found=%t err=%v", recovery, found, err)
	}
	provider.mu.Lock()
	provider.responses = append(provider.responses,
		textResponse(rootActionResponse(domain.RootActionFinish, "New request completed", "done", "")))
	provider.mu.Unlock()
	// A process upgraded from the historical resume path uses the normal store.
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns = application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	next := request
	next.Content, next.OperationKey = "Continue with the saved fetch result after repair", "historical-web-fetch-next"
	continued, err := turns.Execute(t.Context(), next)
	if err != nil || continued.Submission.Run.ID != authorization.RunID || backend.calls != 1 {
		t.Fatalf("ordinary message did not recover the exact prepared input: %#v fetches=%d err=%v", continued, backend.calls, err)
	}
	for _, id := range []string{first.Submission.Message.ID, continued.Submission.Message.ID} {
		message, err := st.GetOperatorSteering(t.Context(), id)
		if err != nil || message.Status != domain.OperatorSteeringCommitted || message.Prepared {
			t.Fatalf("input not committed once: %#v err=%v", message, err)
		}
	}
	requests := provider.Requests()
	snapshots, err := st.ListWebSnapshots(t.Context(), authorization.RunID, 10)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("saved snapshots=%d err=%v", len(snapshots), err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), authorization.RunID, 0, 10)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("original tool journal unavailable: %#v err=%v", rounds, err)
	}
	digest := session.ContentSHA256(rounds[0].Calls[0].ResultJSON)
	var sawOriginal, sawEvidence, sawSavedReference, sawErrorProvenance bool
	for _, item := range requests[len(requests)-1].Messages {
		sawOriginal = sawOriginal || item.Role == "user" && strings.Contains(item.Content, request.Content)
		sawEvidence = sawEvidence || strings.Contains(item.Content, "web_fetch") && strings.Contains(item.Content, "result SHA256") &&
			strings.Contains(item.Content, digest)
		// The immutable failure preview and the full saved-reference projection
		// are separate messages. A longer tool envelope can truncate the preview
		// before the IDs; the supplemental record must retain their exact digest.
		sawSavedReference = sawSavedReference || strings.Contains(item.Content, "web_fetch") && strings.Contains(item.Content, "result_sha256") &&
			strings.Contains(item.Content, digest) && strings.Contains(item.Content, snapshots[0].ID) && strings.Contains(item.Content, snapshots[0].SourceID)
		sawErrorProvenance = sawErrorProvenance || strings.Contains(item.Content, "not the original model cause") && strings.Contains(item.Content, "provider request failed")
	}
	if len(requests) != 3 || !sawOriginal || !sawEvidence || !sawSavedReference || !sawErrorProvenance {
		t.Fatalf("new model lost preserved history: calls=%d original=%t evidence=%t saved_reference=%t error_provenance=%t", len(requests), sawOriginal, sawEvidence, sawSavedReference, sawErrorProvenance)
	}
	failure, found, err := st.GetThreadTurnFailure(t.Context(), authorization.RunID, first.Submission.Message.ID)
	if err != nil || !found || failure.ErrorCode != string(apperror.CodeFailedPrecondition) {
		t.Fatalf("historical input was not sealed: %#v err=%v", failure, err)
	}
}

func TestHistoricalWebFetchFailureCannotCrossChangedAttemptOrActiveLease(t *testing.T) {
	st, _, handoff, provider, backend, request, first, authorization := webFetchThreadFailureFixture(t, true)
	if _, _, err := handoff.ResumeWebFetchAuthorization(t.Context(), authorization.RunID, authorization.ID); err == nil {
		t.Fatal("historical continuation unexpectedly succeeded")
	}
	restoreHistoricalWebFetchRunState(t, st, authorization.RunID)
	if _, bound, err := st.PrepareWebFetchAuthorizationHandoff(t.Context(), authorization.ID,
		"attempt-from-another-turn", domain.SupervisorTurnFailed); err != nil || bound {
		t.Fatalf("mismatched attempt was selected: bound=%t err=%v", bound, err)
	}
	lease, err := st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{
		RunID: authorization.RunID, OwnerID: "another-current-worker", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _, _ = st.ReleaseRunExecutionLease(context.Background(), lease.Lease) })
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	request.Content, request.OperationKey = "Continue only after the existing worker ends", "web-fetch-conflicting-continue"
	before := len(provider.Requests())
	if _, err := turns.Execute(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("active lease did not fence failure observation: %v", err)
	}
	message, err := st.GetOperatorSteering(t.Context(), first.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringPending || !message.Prepared || backend.calls != 1 ||
		len(provider.Requests()) != before {
		t.Fatalf("rejected observation changed old input or reran work: %#v calls=%d fetches=%d err=%v", message, len(provider.Requests()), backend.calls, err)
	}
}
