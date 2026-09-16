package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func TestThreadPlanHTTPConfirmAndReadOriginalRequestWithoutExecution(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-plan-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Plan and execute same Thread", Profile: "review", Surface: "code", Phase: "plan", ModelRoute: "http-plan/model", Interactive: true, Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &httpPlanProvider{responses: []*llm.ChatResponse{
		{Provider: "http-plan", Model: "model", ToolCalls: []llm.ToolCall{{ID: "thread-http-propose", Name: "plan_delivery_propose", Arguments: json.RawMessage(httpPlanDeliveryPayload)}}, Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}},
		{Provider: "http-plan", Model: "model", Text: httpRootWaitResponse(t), Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}},
		{Provider: "http-plan", Model: "model", Text: `{"version":"root_lifecycle.v1","action":"continue","message":"Proceed within selected scope"}`, Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}},
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	lifecycle := application.NewRunLifecycleControlService(st)
	execution := application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker())
	turns := application.NewThreadTurnService(st, lifecycle, execution)
	api, err := New(st, Config{AccessToken: testAccessToken, ControlToken: testControlToken, RunCreationEnabled: true, SessionMessageEnabled: true, RunLifecycleEnabled: true, RunExecutionEnabled: true, PlanDeliveryControlEnabled: true, PlanDeliveryController: application.NewPlanDeliveryControlService(st), RunLifecycleController: lifecycle, RunExecutionController: execution, ThreadTurnController: turns})
	if err != nil {
		t.Fatal(err)
	}
	threadID := domain.InitialThreadID(run.ID)
	path := ThreadCollectionPath + "/" + threadID + "/plan"
	before, _ := st.ListRunEvents(t.Context(), run.ID)
	get := performSessionMessageRequest(t, api, http.MethodGet, path+"?action=confirm&run_id="+run.ID, testAccessToken, "thread-http-original-confirmation", "", nil)
	var view ThreadPlanControlView
	decodeDataStatus(t, get, http.StatusOK, &view)
	if view.State != "not_received" || view.RunID != run.ID || view.ThreadID != threadID || view.ModelCalled || view.ExecutionStarted {
		t.Fatalf("unseen view=%+v", view)
	}
	after, _ := st.ListRunEvents(t.Context(), run.ID)
	if len(after) != len(before) {
		t.Fatal("GET wrote events")
	}
	if _, err := turns.Execute(t.Context(), application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: threadID, Content: "Prepare a bounded plan", OperationKey: "thread-http-plan-first-message", RequestedBy: "http_thread_operator"}); err != nil {
		t.Fatal(err)
	}
	proposals, err := st.ListPlanDeliveryProposals(t.Context(), run.ID, 2)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals=%v %v", proposals, err)
	}
	body, _ := json.Marshal(ThreadPlanControlRequestView{Version: application.PlanDeliveryControlProtocolVersion, Action: "confirm", RunID: run.ID, ProposalID: proposals[0].ID, Direction: 1, ManualAcceptance: "on_demand", Content: "Confirm this plan and execute the current scope."})
	denied := performSessionMessageRequest(t, api, http.MethodPost, path, testAccessToken, "thread-http-original-confirmation", "application/json", bytes.NewReader(body))
	assertAPIError(t, denied, http.StatusUnauthorized, "POLICY_DENIED")
	post := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-http-original-confirmation", "application/json", bytes.NewReader(body))
	decodeDataStatus(t, post, http.StatusAccepted, &view)
	if view.State != "completed" || view.TurnRequest == nil || view.TurnRequest.ThreadID != threadID || !view.ModelCalled || view.CapabilityGrant {
		t.Fatalf("confirmed=%+v", view)
	}
	before, _ = st.ListRunEvents(t.Context(), run.ID)
	get = performSessionMessageRequest(t, api, http.MethodGet, path+"?action=confirm&run_id="+run.ID, testAccessToken, "thread-http-original-confirmation", "", nil)
	decodeDataStatus(t, get, http.StatusOK, &view)
	if view.State != "completed" || view.ModelCalled || view.ToolCalled || view.ExecutionStarted {
		t.Fatalf("GET claims execution=%+v", view)
	}
	after, _ = st.ListRunEvents(t.Context(), run.ID)
	if len(after) != len(before) {
		t.Fatal("original request GET mutated")
	}
	invalid := performSessionMessageRequest(t, api, http.MethodGet, path+"?action=confirm&run_id="+run.ID+"&content=replace", testAccessToken, "thread-http-original-confirmation", "", nil)
	assertAPIError(t, invalid, http.StatusBadRequest, "INVALID_ARGUMENT")
}
