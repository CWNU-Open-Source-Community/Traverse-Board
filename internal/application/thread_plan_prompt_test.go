package application_test

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestThreadPlanConfirmedReplyPromptDoesNotClaimWorkCompletion(t *testing.T) {
	st, run, provider, turns := newThreadPlanFixture(t,
		toolResponse("plan-for-prompt", "plan_delivery_propose", planDeliveryTestPayload), planWait(),
		textResponse(rootActionResponse(domain.RootActionFinish, "This response is complete; selected work remains pending.", "Reply complete", "")))
	sendThreadPlanInput(t, turns, run, "plan-before-prompt", "Prepare a plan; retain all unfinished requirements.")
	proposal := latestThreadPlan(t, st, run.ID)
	if _, err := turns.ControlPlan(t.Context(), confirmThreadPlan(run, proposal, "confirm-prompt")); err != nil {
		t.Fatal(err)
	}
	requests := provider.Requests()
	if len(requests) != 3 {
		t.Fatalf("model requests=%d", len(requests))
	}
	var planning, delivery strings.Builder
	for _, message := range requests[0].Messages {
		planning.WriteString(message.Content)
	}
	for _, message := range requests[2].Messages {
		delivery.WriteString(message.Content)
	}
	if !strings.Contains(planning.String(), "Never return finish.") {
		t.Fatal("Plan finish restriction was lost")
	}
	for _, contradiction := range []string{"do not use finish while any listed item remains active", "finish only when the mission is complete"} {
		if strings.Contains(delivery.String(), contradiction) {
			t.Errorf("actual confirmed Thread request still contains %q", contradiction)
		}
	}
	for _, required := range []string{"finish ends only the current reply", "does not complete the Run, plan, work items, or acceptance checks", `"version":"work_board.v1"`, `"status":"pending"`} {
		if !strings.Contains(delivery.String(), required) {
			t.Errorf("actual confirmed Thread request lacks %q", required)
		}
	}
	items, err := st.ListWorkItems(t.Context(), domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) == 0 {
		t.Fatalf("selected work missing: %v", err)
	}
	for _, item := range items {
		if item.Status != domain.WorkItemPending || item.Version != 1 || !strings.Contains(delivery.String(), item.ID) {
			t.Fatalf("work evidence changed or missing from request: %+v", item)
		}
	}
}
