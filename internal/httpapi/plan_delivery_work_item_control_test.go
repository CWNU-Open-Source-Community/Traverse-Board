package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

// Called from the real SQLite Plan selection/Deliver HTTP integration fixture.
func exercisePlanDeliveryWorkItemHTTP(t *testing.T, api *API, st *store.SQLiteStore, runID string) {
	t.Helper()
	selection, found, err := st.GetPlanDeliverySelectionByRun(t.Context(), runID)
	if err != nil || !found || len(selection.Items) != 1 {
		t.Fatalf("selection=%#v err=%v", selection, err)
	}
	itemID := selection.Items[0].WorkItemID
	base := "/api/v1/runs/" + runID + "/plan/work-items/" + itemID
	startBody := `{"version":"plan_delivery_control.v1","expected_work_item_version":1}`
	for _, requestCase := range []struct {
		token, remote string
		status        int
	}{
		{testAccessToken, "127.0.0.1:45000", http.StatusUnauthorized},
		{testControlToken, "192.0.2.1:45000", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+base+"/start", strings.NewReader(startBody))
		req.Host, req.RemoteAddr = "127.0.0.1:8765", requestCase.remote
		req.Header.Set("Authorization", "Bearer "+requestCase.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "http-plan-work-auth-0001")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		if response.Code != requestCase.status {
			t.Fatalf("auth status=%d body=%s", response.Code, response.Body.String())
		}
	}
	bad := planControlRequest(t, api, base+"/start", `{"version":"plan_delivery_control.v1","expected_work_item_version":1,"expected_work_item_version":1}`, "http-plan-work-duplicate-0001")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("duplicate fields status=%d body=%s", bad.Code, bad.Body.String())
	}
	started := planControlRequest(t, api, base+"/start", startBody, "http-plan-work-start-0001")
	if started.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
	}
	var start struct {
		Data PlanDeliveryWorkItemControlView `json:"data"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	if start.Data.AppliedStatus != "in_progress" || start.Data.AppliedVersion != 2 || start.Data.CurrentWorkItem.Version != 2 || start.Data.ExecutionStarted || start.Data.CapabilityGrant {
		t.Fatalf("start=%#v", start.Data)
	}
	completeBody := `{"version":"plan_delivery_control.v1","expected_work_item_version":2}`
	missing := planControlRequest(t, api, base+"/complete", completeBody, "http-plan-work-complete-0001")
	if missing.Code != http.StatusPreconditionFailed {
		t.Fatalf("missing checkpoint status=%d body=%s", missing.Code, missing.Body.String())
	}
	checkpointBody := `{"version":"plan_delivery_control.v1","expected_work_item_version":2,"focused_verification":"人工观察记录","diff_audit":"人工差异检查","security_audit":"人工边界检查","handoff_summary":"人工陈述，不代表系统测试通过"}`
	missingFull := planControlRequest(t, api, base+"/checkpoint", checkpointBody, "http-plan-work-checkpoint-0001")
	if missingFull.Code != http.StatusBadRequest {
		t.Fatalf("missing final gate status=%d body=%s", missingFull.Code, missingFull.Body.String())
	}
	checkpointBody = strings.TrimSuffix(checkpointBody, "}") + `,"functional_verification":"人工功能检查记录","robustness_audit":"人工异常路径检查记录"}`
	recorded := planControlRequest(t, api, base+"/checkpoint", checkpointBody, "http-plan-work-checkpoint-0001")
	if recorded.Code != http.StatusAccepted {
		t.Fatalf("record status=%d body=%s", recorded.Code, recorded.Body.String())
	}
	var receipt struct {
		Data PlanDeliveryCheckpointControlView `json:"data"`
	}
	if err := json.Unmarshal(recorded.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Data.Checkpoint.GateReady || !receipt.Data.Checkpoint.FullGateRequired || receipt.Data.ExecutionStarted || receipt.Data.ModelCalled || receipt.Data.ToolCalled || receipt.Data.CapabilityGrant ||
		!strings.Contains(receipt.Data.Note.Content, "人工陈述，不代表系统测试通过") || bytes.Contains(recorded.Body.Bytes(), []byte(`"verified":true`)) {
		t.Fatalf("attestation response=%s", recorded.Body.String())
	}
	completed := planControlRequest(t, api, base+"/complete", completeBody, "http-plan-work-complete-0001")
	if completed.Code != http.StatusAccepted {
		t.Fatalf("complete status=%d body=%s", completed.Code, completed.Body.String())
	}
	confirmed := planControlRequest(t, api, base+"/checkpoint", checkpointBody, "http-plan-work-checkpoint-0001")
	if confirmed.Code != http.StatusAccepted || !bytes.Contains(confirmed.Body.Bytes(), []byte(`"replayed":true`)) || !bytes.Contains(confirmed.Body.Bytes(), []byte(receipt.Data.Checkpoint.ID)) {
		t.Fatalf("checkpoint confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	item, err := st.GetWorkItem(t.Context(), itemID)
	if err != nil || item.Status != domain.WorkItemCompleted || item.Version != 3 {
		t.Fatalf("item=%#v err=%v", item, err)
	}
}

func TestPlanDeliveryWorkItemEndpointsRemainDisabledWithoutControl(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, action := range []string{"start", "checkpoint", "complete"} {
		response := planControlRequest(t, fixture.api, "/api/v1/runs/"+fixture.run.ID+"/plan/work-items/work-1/"+action,
			`{"version":"plan_delivery_control.v1","expected_work_item_version":1}`, "plan-work-disabled-0001")
		assertAPIError(t, response, http.StatusNotFound, "NOT_FOUND")
	}
}

func selectedHTTPPlanWorkItem(t *testing.T, st *store.SQLiteStore, runID string) domain.WorkItem {
	t.Helper()
	selection, found, err := st.GetPlanDeliverySelectionByRun(t.Context(), runID)
	if err != nil || !found || len(selection.Items) != 1 {
		t.Fatalf("selected HTTP Plan fixture=%#v err=%v", selection, err)
	}
	item, err := st.GetWorkItem(t.Context(), selection.Items[0].WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	return item
}
