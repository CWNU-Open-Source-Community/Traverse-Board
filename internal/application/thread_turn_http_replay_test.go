package application_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"context"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

func TestThreadTurnHTTPReplayPreservesEachSubmissionContinuation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_event_%t", legacy), func(t *testing.T) { testThreadTurnHTTPReplayContinuation(t, legacy) })
	}
}

func testThreadTurnHTTPReplayContinuation(t *testing.T, legacy bool) {
	path := filepath.Join(t.TempDir(), "thread-http-replay.db")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runs := application.NewRunService(state)
	_, original, err := runs.Create(t.Context(), application.CreateRunRequest{Goal: "Thread request continuation contract", Profile: "review", Interactive: true, ModelRoute: "lifecycle-test/model", Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runs.Start(t.Context(), original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = runs.Fail(t.Context(), original.ID, "fixture requires a new execution context"); err != nil {
		t.Fatal(err)
	}
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionFinish, "First message complete.", "done", ""),
		rootActionResponse(domain.RootActionFinish, "Second message complete.", "done", ""),
		rootActionResponse(domain.RootActionFinish, "Third message complete.", "done", ""),
		rootActionResponse(domain.RootActionFinish, "Fourth message complete.", "done", ""),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	makeAPI := func(st *store.SQLiteStore) *httpapi.API {
		lifecycle := application.NewRunLifecycleControlService(st)
		execution := application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker())
		var threadStore application.ThreadStore = st
		if legacy {
			threadStore = &threadSuccessorCreationStore{SQLiteStore: st, legacy: true}
		}
		turns := application.NewThreadTurnService(threadStore, lifecycle, execution)
		api, err := httpapi.New(st, httpapi.Config{AccessToken: "read-thread-http-replay-token-long-0001", ControlToken: "control-thread-http-replay-token-long-0001", RunCreationEnabled: true, SessionMessageEnabled: true, RunLifecycleEnabled: true, RunExecutionEnabled: true, RunLifecycleController: lifecycle, RunExecutionController: execution, ThreadTurnController: turns})
		if err != nil {
			t.Fatal(err)
		}
		return api
	}
	api := makeAPI(state)
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	replayAPI := makeAPI(other)
	send := func(api *httpapi.API, index int) httpapi.ThreadMessageControlView {
		content := fmt.Sprintf("User requirement %d", index)
		body, _ := json.Marshal(map[string]string{"version": domain.ThreadMessageProtocolVersion, "content": content})
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/threads/"+domain.InitialThreadID(original.ID)+"/turns", strings.NewReader(string(body)))
		request.Host = "127.0.0.1:8765"
		request.RemoteAddr = "127.0.0.1:45000"
		request.Header.Set("Authorization", "Bearer control-thread-http-replay-token-long-0001")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("http-thread-replay-key-%04d", index))
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		var envelope struct {
			Data httpapi.ThreadMessageControlView `json:"data"`
		}
		if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
			t.Fatalf("request %d response=%d %s", index, response.Code, response.Body.String())
		}
		result := envelope.Data
		if result.SuccessorCreated != (index == 1) || (result.PredecessorRunID != "") != (index == 1) || (index == 1 && result.PredecessorRunID != original.ID) {
			t.Fatalf("request %d continuation contract violated: %s", index, response.Body.String())
		}
		return result
	}
	var currentRun string
	for index := 1; index <= 4; index++ {
		first := send(api, index)
		if index == 1 {
			currentRun = first.RunID
		}
		replay := send(replayAPI, index)
		if first.Replayed || !replay.Replayed || replay.RunID != currentRun || replay.SessionID != first.SessionID || replay.Steering.ID != first.Steering.ID || replay.Steering.Sequence != first.Steering.Sequence || provider.calls != index {
			t.Fatalf("request %d replay drifted: first=%+v replay=%+v calls=%d", index, first, replay, provider.calls)
		}
	}
	if replay := send(replayAPI, 1); !replay.Replayed || provider.calls != 4 {
		t.Fatalf("original successor key was reinterpreted: %+v calls=%d", replay, provider.calls)
	}
	bindings, err := state.ListThreadRuns(t.Context(), domain.InitialThreadID(original.ID))
	if err != nil || len(bindings) != 2 {
		t.Fatalf("replay published extra contexts: %+v %v", bindings, err)
	}
}

// Simulates a pre-attribution creator without changing sealed historical rows,
// or a competing process that enqueues after publication but before its creator.
type threadSuccessorCreationStore struct {
	*store.SQLiteStore
	legacy           bool
	afterPublication func()
}

func (s *threadSuccessorCreationStore) EnsureThreadSuccessorForMessage(ctx context.Context, request domain.ThreadMessageIntentRequest,
	predecessor string, mission domain.Mission, candidate domain.Run, mode domain.RunModeSnapshot, linked session.Session, initial []events.Event,
) (domain.Thread, domain.Run, bool, error) {
	var thread domain.Thread
	var run domain.Run
	var created bool
	var err error
	if s.legacy {
		thread, run, created, err = s.SQLiteStore.EnsureThreadSuccessor(ctx, request.ThreadID, predecessor, mission, candidate, mode, linked, initial)
	} else {
		thread, run, created, err = s.SQLiteStore.EnsureThreadSuccessorForMessage(ctx, request, predecessor, mission, candidate, mode, linked, initial)
	}
	if err == nil && created && s.afterPublication != nil {
		s.afterPublication()
	}
	return thread, run, created, err
}

func TestThreadMessageCreationAttributionSurvivesAnotherConnectionEnqueueingFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thread-creation-order.db")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runs := application.NewRunService(state)
	_, original, err := runs.Create(t.Context(), application.CreateRunRequest{Goal: "request owns its continuation receipt", Profile: "review", Interactive: true, Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runs.Start(t.Context(), original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = runs.Fail(t.Context(), original.ID, "new execution context required"); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	request := application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(original.ID), Content: "creator message", OperationKey: "thread-creation-exact-key-0001", RequestedBy: "thread_test_operator"}
	competing := request
	competing.OperationKey = "thread-creation-competing-key-0001"
	competing.Content = "later request enqueues first"
	var competitor application.SubmitThreadMessageResult
	wrapped := &threadSuccessorCreationStore{SQLiteStore: state, afterPublication: func() {
		var err error
		competitor, err = application.NewThreadService(other).Submit(t.Context(), competing)
		if err != nil {
			t.Fatal(err)
		}
	}}
	first, err := application.NewThreadService(wrapped).Submit(t.Context(), request)
	if err != nil || first.Message.Sequence != 2 || !first.SuccessorCreated || first.PredecessorRunID != original.ID || competitor.Message.Sequence != 1 || competitor.SuccessorCreated || competitor.PredecessorRunID != "" {
		t.Fatalf("creation attributed by queue order: creator=%+v competitor=%+v err=%v", first, competitor, err)
	}
	for _, test := range []struct {
		request  application.SubmitThreadMessageRequest
		original application.SubmitThreadMessageResult
	}{{request, first}, {competing, competitor}} {
		replay, err := application.NewThreadService(other).Submit(t.Context(), test.request)
		if err != nil || !replay.Replayed || replay.Message.ID != test.original.Message.ID || replay.SuccessorCreated != test.original.SuccessorCreated || replay.PredecessorRunID != test.original.PredecessorRunID {
			t.Fatalf("replay lost exact request attribution: %+v %v", replay, err)
		}
	}
	changed := request
	changed.Content = "changed creator intent"
	if _, err := application.NewThreadService(other).Submit(t.Context(), changed); err == nil {
		t.Fatal("changed intent reused original creation key")
	}
}
