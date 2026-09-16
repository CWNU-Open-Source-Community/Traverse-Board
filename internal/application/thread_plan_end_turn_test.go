package application_test

import (
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestThreadPlanFinishEndsTurnWithoutCompletingSelectedWork(t *testing.T) {
	st, run, provider, turns := newThreadPlanFixture(t,
		toolResponse("plan-for-end-turn", "plan_delivery_propose", planDeliveryTestPayload), planWait(),
		textResponse(rootActionResponse(domain.RootActionFinish, "This reply ends the current turn only.", "Current response complete.", "")))
	sendThreadPlanInput(t, turns, run, "plan-before-end-turn", "Keep the original requirement and prepare a plan.")
	proposal := latestThreadPlan(t, st, run.ID)
	request := confirmThreadPlan(run, proposal, "confirm-current-turn-only")
	result, err := turns.ControlPlan(t.Context(), request)
	if err != nil || result.State != "completed" || !result.ModelCalled {
		t.Fatalf("interactive end-turn was treated as task completion: %+v %v", result, err)
	}
	current, err := st.GetRun(t.Context(), run.ID)
	if err != nil || current.Status != domain.RunRunning {
		t.Fatalf("end-turn closed Run: %+v %v", current, err)
	}
	checkpoint, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found || checkpoint.Phase != domain.SupervisorIdle || checkpoint.AttemptID != "" {
		t.Fatalf("end-turn did not settle: %+v %v", checkpoint, err)
	}
	items, err := st.ListWorkItems(t.Context(), domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) == 0 {
		t.Fatalf("selected planning work was lost: %+v %v", items, err)
	}
	for _, item := range items {
		if item.Status != domain.WorkItemPending || item.Version != 1 {
			t.Fatalf("end-turn fabricated work completion: %+v", item)
		}
	}
	before := len(provider.Requests())
	replay, err := newThreadPlanTurns(st, provider).ControlPlan(t.Context(), request)
	if err != nil || replay.State != "completed" || len(provider.Requests()) != before || before != 3 {
		t.Fatalf("original confirmation replay executed again: %+v %v requests=%d", replay, err, len(provider.Requests()))
	}
}
