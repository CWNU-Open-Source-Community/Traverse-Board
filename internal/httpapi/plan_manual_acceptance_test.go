package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
)

func TestPlanDeliveryManualAcceptanceSmallPlanAndExactReplay(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d directions", count), func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "small-plan.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			runs := application.NewRunService(st)
			_, run, err := runs.Create(ctx, application.CreateRunRequest{Goal: "one clear small change", Profile: "review", Phase: "plan", ModelRoute: "http-plan/model", Budget: domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 4}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runs.Start(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			var spec domain.PlanDeliverySpec
			if err := json.Unmarshal([]byte(httpPlanDeliveryPayload), &spec); err != nil {
				t.Fatal(err)
			}
			spec.Directions = spec.Directions[:count]
			payload, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			provider := &httpPlanProvider{responses: []*llm.ChatResponse{
				{Provider: "http-plan", Model: "model", ToolCalls: []llm.ToolCall{{ID: "small-plan", Name: "plan_delivery_propose", Arguments: payload}}},
				{Text: httpRootWaitResponse(t), Provider: "http-plan", Model: "model"},
			}}
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
			router.RegisterProvider(provider)
			if _, err := application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).Step(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			proposals, err := st.ListPlanDeliveryProposals(ctx, run.ID, 2)
			if err != nil || len(proposals) != 1 || len(proposals[0].Spec.Directions) != count {
				t.Fatalf("small proposal: %+v %v", proposals, err)
			}
			proposal := proposals[0]
			api, err := New(st, Config{AccessToken: testAccessToken, ControlToken: testControlToken, PlanDeliveryControlEnabled: true, PlanDeliveryController: application.NewPlanDeliveryControlService(st)})
			if err != nil {
				t.Fatal(err)
			}
			endpoint := "/api/v1/runs/" + run.ID + "/plan/direction"
			body := fmt.Sprintf(`{"version":"plan_delivery_control.v1","proposal_id":%q,"direction":1,"manual_acceptance":"on_demand"}`, proposal.ID)
			const key = "small-plan-confirmed-choice"
			badDirection := fmt.Sprintf(`{"version":"plan_delivery_control.v1","proposal_id":%q,"direction":%d}`, proposal.ID, count+1)
			assertAPIError(t, planControlRequest(t, api, endpoint, badDirection, "small-plan-missing-direction"), http.StatusBadRequest, "INVALID_ARGUMENT")
			for _, invalid := range []string{`null`, `""`, `"model_exempt"`} {
				bad := fmt.Sprintf(`{"version":"plan_delivery_control.v1","proposal_id":%q,"direction":1,"manual_acceptance":%s}`, proposal.ID, invalid)
				assertAPIError(t, planControlRequest(t, api, endpoint, bad, "small-plan-invalid-policy"), http.StatusBadRequest, "INVALID_ARGUMENT")
			}
			var selected PlanDirectionControlView
			decodeDataStatus(t, planControlRequest(t, api, endpoint, body, key), http.StatusAccepted, &selected)
			if selected.ManualAcceptance != "on_demand" || selected.Replayed || selected.PhaseChanged || selected.CapabilityGrant || selected.ModelCalled || selected.ExecutionStarted {
				t.Fatalf("selection: %+v", selected)
			}
			modeBody := `{"version":"plan_delivery_control.v1"}`
			decodeDataStatus(t, planControlRequest(t, api, "/api/v1/runs/"+run.ID+"/plan/deliver", modeBody, "small-plan-enter-deliver"), http.StatusAccepted, &PlanDeliveryTransitionControlView{})
			operation, found, err := st.GetPlanDeliverySelectionOperation(ctx, runmutation.OperationKeyDigest("plan_delivery_select", run.ID, key))
			if err != nil || !found {
				t.Fatalf("operation: %+v %v", operation, err)
			}
			selection, err := st.GetPlanDeliverySelection(ctx, selected.SelectionID)
			if err != nil || selection.ManualAcceptance != domain.PlanDeliveryManualAcceptanceOnDemand {
				t.Fatalf("selection policy: %+v %v", selection, err)
			}
			var detail RunDetailView
			decodeDataStatus(t, performRequest(t, api, http.MethodGet, "/api/v1/runs/"+run.ID, testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil), http.StatusOK, &detail)
			if detail.PlanDelivery == nil || detail.PlanDelivery.Selection == nil || detail.PlanDelivery.Selection.ManualAcceptance != "on_demand" || detail.PlanDelivery.ReadyCheckpoints != 0 {
				t.Fatalf("read selection policy: %+v", detail.PlanDelivery)
			}
			var confirmed PlanDirectionControlView
			decodeDataStatus(t, planControlRequest(t, api, endpoint, body, key), http.StatusAccepted, &confirmed)
			if !confirmed.Replayed || confirmed.SelectionID != selected.SelectionID || confirmed.ManualAcceptance != selected.ManualAcceptance {
				t.Fatalf("late confirmation: %+v", confirmed)
			}
			changed := fmt.Sprintf(`{"version":"plan_delivery_control.v1","proposal_id":%q,"direction":1,"manual_acceptance":"required"}`, proposal.ID)
			assertAPIError(t, planControlRequest(t, api, endpoint, changed, key), http.StatusConflict, "CONFLICT")
			if _, err := application.NewPlanDeliveryService(st).Select(ctx, application.SelectPlanDeliveryDirectionRequest{ProposalID: proposal.ID, Direction: 1, ManualAcceptance: domain.PlanDeliveryManualAcceptanceOnDemand, OperationKey: key, RequestedBy: "different_operator"}); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("changed actor replay: %v", err)
			}
			if _, err := runs.Fail(ctx, run.ID, "end this execution after its operator selection"); err != nil {
				t.Fatal(err)
			}
			decodeDataStatus(t, planControlRequest(t, api, endpoint, body, key), http.StatusAccepted, &confirmed)
			if !confirmed.Replayed || confirmed.SelectionID != selected.SelectionID {
				t.Fatalf("terminal confirmation: %+v", confirmed)
			}
			// A separate SQLite reader that missed the early receipt must still
			// recover under the writer fence, before current phase/status gates.
			second, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			late := &missedPlanSelectionReceipt{SQLiteStore: second}
			replay, err := application.NewPlanDeliveryService(late).Select(ctx, application.SelectPlanDeliveryDirectionRequest{ProposalID: proposal.ID, Direction: 1, ManualAcceptance: domain.PlanDeliveryManualAcceptanceOnDemand, OperationKey: key, RequestedBy: "http_plan_operator"})
			if err != nil || !replay.Replayed || replay.Selection.ID != selected.SelectionID {
				t.Fatalf("fenced late receipt replay: %+v %v", replay, err)
			}
			after, found, err := second.GetPlanDeliverySelectionOperation(ctx, operation.KeyDigest)
			if err != nil || !found || !reflect.DeepEqual(after, operation) {
				t.Fatalf("sealed operation rewritten: %+v %v", after, err)
			}
			stored, err := second.GetPlanDeliverySelection(ctx, selection.ID)
			if err != nil || !reflect.DeepEqual(stored, selection) {
				t.Fatalf("selection rewritten: %+v %v", stored, err)
			}
		})
	}
}

type missedPlanSelectionReceipt struct{ *store.SQLiteStore }

func (*missedPlanSelectionReceipt) GetPlanDeliverySelectionOperation(context.Context, string) (domain.PlanDeliverySelectionOperation, bool, error) {
	return domain.PlanDeliverySelectionOperation{}, false, nil
}
