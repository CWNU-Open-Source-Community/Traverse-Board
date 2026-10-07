package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func threadListTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-list.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func threadListTestAPI(t *testing.T, st Store, controller ThreadTurnController) *API {
	t.Helper()
	api, err := New(st, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		RunExecutionEnabled: true, RunExecutionController: runExecutionControllerFake{}, ThreadTurnController: controller})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func readThreadList(t *testing.T, api *API, path string) ([]ThreadView, *Page) {
	t.Helper()
	var items []ThreadView
	envelope := decodeData(t, performSessionMessageRequest(t, api, http.MethodGet,
		path, testAccessToken, "", "", nil), &items)
	return items, envelope.Page
}

func TestThreadHTTPTitleSearchCoversOlderTasksAndBindsCursorToCanonicalQuery(t *testing.T) {
	st := threadListTestStore(t)
	runs := application.NewRunService(st)
	var olderID string
	for index := 0; index < 103; index++ {
		title := fmt.Sprintf("Task %03d", index)
		if index < 2 {
			title = fmt.Sprintf("Old FIND 中文_%% %03d", index)
		}
		_, run, err := runs.Create(t.Context(), application.CreateRunRequest{Goal: title, Profile: "review", ModelRoute: "review"})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			olderID = domain.InitialThreadID(run.ID)
		}
	}
	api := threadListTestAPI(t, st, application.NewThreadTurnService(st, nil, nil))
	first, page := readThreadList(t, api, "/api/v1/threads?limit=1&q="+url.QueryEscape(" FIND 中文_% "))
	if len(first) != 1 || page == nil || page.NextCursor == "" || first[0].ExecutionState != "idle" {
		t.Fatalf("first search page=%+v page=%+v", first, page)
	}
	second, finalPage := readThreadList(t, api, "/api/v1/threads?limit=1&q="+url.QueryEscape("find 中文_%")+"&cursor="+url.QueryEscape(page.NextCursor))
	if len(second) != 1 || second[0].ID != olderID || second[0].ID == first[0].ID || finalPage.NextCursor != "" {
		t.Fatalf("old task missing or duplicate: second=%+v page=%+v", second, finalPage)
	}
	for _, query := range []string{"other", ""} {
		response := performSessionMessageRequest(t, api, http.MethodGet,
			"/api/v1/threads?limit=1&q="+url.QueryEscape(query)+"&cursor="+url.QueryEscape(page.NextCursor), testAccessToken, "", "", nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("changed search accepted old cursor: query=%q status=%d", query, response.Code)
		}
	}
	unfiltered, plainPage := readThreadList(t, api, "/api/v1/threads?limit=1")
	blank, blankPage := readThreadList(t, api, "/api/v1/threads?limit=1&q=%20%20")
	if len(blank) != 1 || blank[0].ID != unfiltered[0].ID || blankPage.NextCursor != plainPage.NextCursor {
		t.Fatal("omitted and blank query lost compatibility")
	}
	for _, query := range []string{"q=one&q=two", "q=" + url.QueryEscape(strings.Repeat("中", 257)), "q=%FF"} {
		response := performSessionMessageRequest(t, api, http.MethodGet, "/api/v1/threads?"+query, testAccessToken, "", "", nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid title query accepted: query=%q status=%d", query, response.Code)
		}
	}
}

func TestThreadListExecutionProjectionRequiresLiveAndDurableEvidence(t *testing.T) {
	for _, test := range []struct {
		live                                string
		run                                 domain.RunStatus
		observed, valid, unsettled, pending bool
		want                                string
	}{
		{live: "running", run: domain.RunRunning, observed: true, valid: true, want: "running"},
		{live: "stopping", observed: true, want: "stopping"},
		{live: "stop_failed", observed: true, want: "stop_failed"},
		{live: "idle", run: domain.RunRunning, observed: true, valid: true, want: "idle"},
		{live: "idle", run: domain.RunRunning, observed: true, valid: true, unsettled: true, want: "unknown"},
		{live: "idle", run: domain.RunRunning, observed: true, valid: true, pending: true, want: "waiting_approval"},
		{live: "idle", run: domain.RunWaitingApproval, observed: true, valid: true, want: "waiting_approval"},
		{live: "idle", run: domain.RunPaused, observed: true, valid: true, want: "paused"},
		{live: "idle", run: domain.RunCompleted, observed: true, valid: true, want: "completed"},
		{live: "idle", run: domain.RunFailed, observed: true, valid: true, want: "failed"},
		{live: "idle", run: domain.RunFailed, observed: true, valid: true, pending: true, want: "failed"},
		{live: "idle", run: domain.RunCancelled, observed: true, valid: true, want: "cancelled"},
		{live: "idle", run: domain.RunRunning, observed: false, valid: true, want: "unknown"},
		{live: "idle", observed: true, want: "unknown"},
		{live: "future_state", observed: true, valid: true, want: "unknown"},
	} {
		got := threadListExecutionState(application.ThreadExecutionState{State: test.live}, test.observed,
			domain.ThreadExecutionFacts{RunStatus: test.run, Unsettled: test.unsettled, PendingApproval: test.pending}, test.valid)
		if got != test.want {
			t.Fatalf("test=%+v got=%q", test, got)
		}
	}
}

type threadListFaultStore struct {
	*store.SQLiteStore
	fail                 bool
	batchReads, runReads int
}

func (s *threadListFaultStore) GetRun(ctx context.Context, id string) (domain.Run, error) {
	s.runReads++
	return s.SQLiteStore.GetRun(ctx, id)
}

func (s *threadListFaultStore) GetThreadExecutionFacts(ctx context.Context, ids []string) (map[string]domain.ThreadExecutionFacts, error) {
	s.batchReads++
	if s.fail {
		return nil, errors.New("private durable observation failure")
	}
	return s.SQLiteStore.GetThreadExecutionFacts(ctx, ids)
}

func TestThreadListBatchesFactsAndPreservesUnknownAfterReadFailureOrExternalLease(t *testing.T) {
	st := threadListTestStore(t)
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "external execution", Profile: "review", ModelRoute: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	faults := &threadListFaultStore{SQLiteStore: st}
	api := threadListTestAPI(t, faults, application.NewThreadTurnService(st, nil, nil))
	items, _ := readThreadList(t, api, "/api/v1/threads")
	if len(items) != 1 || items[0].ExecutionState != "idle" || items[0].ComposerState != "ready" || faults.batchReads != 1 || faults.runReads != 0 {
		t.Fatalf("running durable Run implied live work or page was not batched: %+v reads=%d/%d", items, faults.batchReads, faults.runReads)
	}
	if _, err := st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "external-cli", TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	items, _ = readThreadList(t, api, "/api/v1/threads")
	if items[0].ExecutionState != "unknown" {
		t.Fatalf("external lease claimed idle: %+v", items)
	}
	faults.fail = true
	items, _ = readThreadList(t, api, "/api/v1/threads")
	if len(items) != 1 || items[0].ExecutionState != "unknown" || items[0].ComposerState != "unavailable" {
		t.Fatalf("failed durable read implied idle/ready or hid the task: %+v", items)
	}
	api = threadListTestAPI(t, st, &threadTurnControllerStub{})
	items, _ = readThreadList(t, api, "/api/v1/threads")
	if items[0].ExecutionState != "unknown" {
		t.Fatalf("missing batch observer claimed idle: %+v", items)
	}
}

func TestThreadListObservesRealThreadTurnServiceWhileModelRunsAndAfterInterrupt(t *testing.T) {
	st := threadListTestStore(t)
	provider := &httpControlBlockingProvider{entered: make(chan struct{})}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "live model task", Profile: "review", Interactive: true, ModelRoute: provider.Name() + "/model"})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	api := threadListTestAPI(t, st, turns)
	threadID := domain.InitialThreadID(run.ID)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_, err := turns.Execute(ctx, application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: threadID,
			Content: "start", OperationKey: "thread-list-live-model-operation-0001", RequestedBy: "operator"})
		done <- err
	}()
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	items, _ := readThreadList(t, api, "/api/v1/threads")
	if len(items) != 1 || items[0].ExecutionState != "running" {
		t.Fatalf("live execution not reflected in list: %+v", items)
	}
	state, err := turns.ExecutionState(t.Context(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turns.Interrupt(t.Context(), threadID, state.ExecutionID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interruption claimed execution success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted execution did not settle")
	}
	items, _ = readThreadList(t, api, "/api/v1/threads")
	if items[0].ExecutionState == "running" || items[0].ExecutionState == "stopping" || items[0].ExecutionState == "unknown" {
		t.Fatalf("settled interruption still implied execution or uncertainty: %+v", items)
	}
}
