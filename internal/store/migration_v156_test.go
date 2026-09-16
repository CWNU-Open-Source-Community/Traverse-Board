package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV156PreservesLegacyPlanAcceptanceAndOriginalReceipts(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "old-plan.db")
	st := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(ctx, st, migrationPlan(), 155); err != nil {
		t.Fatal(err)
	}
	_, _, run, selected := populateStoreDeliveryGateFixture(t, st, "v156-legacy")
	work, err := application.NewWorkItemService(st).Transition(ctx, selected.WorkItems[0].ID, 0, domain.WorkItemInProgress, "")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := recordStoreDeliveryCheckpoint(t, ctx, st, work.ID, "v156-original-checkpoint", false)
	proposal, err := st.GetPlanDeliveryProposal(ctx, selected.Selection.ProposalID)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := st.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	after, err := st.GetPlanDeliveryProposal(ctx, proposal.ID)
	if err != nil || !reflect.DeepEqual(proposal, after) {
		t.Fatalf("migration changed old proposal or fingerprint: %v", err)
	}
	loaded, found, err := st.GetPlanDeliverySelectionByRun(ctx, run.ID)
	if err != nil || !found || loaded.ManualAcceptance != domain.PlanDeliveryManualAcceptanceRequired || !reflect.DeepEqual(loaded, selected.Selection) {
		t.Fatalf("old selection lost required acceptance: %#v %v", loaded, err)
	}
	gotCheckpoint, err := st.GetDeliveryCheckpoint(ctx, checkpoint.ID)
	if err != nil || !reflect.DeepEqual(checkpoint, gotCheckpoint) {
		t.Fatalf("old manual record changed: %v", err)
	}
	afterLedger, err := st.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for version, entry := range ledger {
		if afterLedger[version] != entry {
			t.Fatalf("old migration %d changed", version)
		}
	}
	replayed, err := application.NewPlanDeliveryService(st).Select(ctx, application.SelectPlanDeliveryDirectionRequest{
		ProposalID: proposal.ID, Direction: 2, OperationKey: "store-delivery-choice-v156-legacy", RequestedBy: "operator",
	})
	if err != nil || !replayed.Replayed || replayed.Selection.ID != selected.Selection.ID {
		t.Fatalf("old selection key failed after Deliver/upgrade: %#v %v", replayed, err)
	}
	_, err = application.NewPlanDeliveryService(st).Select(ctx, application.SelectPlanDeliveryDirectionRequest{
		ProposalID: proposal.ID, Direction: 2, OperationKey: "store-delivery-choice-v156-legacy", RequestedBy: "operator",
		ManualAcceptance: domain.PlanDeliveryManualAcceptanceOnDemand,
	})
	if apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("same key changed old manual policy: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE plan_delivery_selections SET manual_acceptance='on_demand' WHERE id=?`, selected.Selection.ID); err == nil {
		t.Fatal("manual policy was mutable")
	}
	var fkTable string
	rows, err := st.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatalf("upgrade left foreign key violations: %s", fkTable)
	}
}

func TestPlanDeliveryOnDemandCompletionKeepsPhaseDependenciesAndFinishGates(t *testing.T) {
	st, ctx, run, selected := createStoreDeliveryGateFixture(t, "on-demand-gates", domain.PlanDeliveryManualAcceptanceOnDemand)
	defer st.Close()
	control := application.NewPlanDeliveryControlService(st)
	request := application.ControlPlanDeliveryWorkItemRequest{
		Version: application.PlanDeliveryControlProtocolVersion,
		PlanDeliveryWorkItemTransition: domain.PlanDeliveryWorkItemTransition{RunID: run.ID,
			WorkItemID: selected.WorkItems[1].ID, ExpectedVersion: 1, OperationKey: "on-demand-dependency", RequestedBy: "operator", Target: domain.WorkItemInProgress},
	}
	if _, err := control.TransitionWorkItem(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("unfinished dependency was bypassed: %v", err)
	}
	request.WorkItemID, request.OperationKey = selected.WorkItems[0].ID, "on-demand-start-first"
	started, err := control.TransitionWorkItem(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedVersion, request.OperationKey, request.Target = started.CurrentWorkItem.Version, "on-demand-complete", domain.WorkItemCompleted
	completed, err := control.TransitionWorkItem(ctx, request)
	if err != nil || completed.CurrentWorkItem.Status != domain.WorkItemCompleted {
		t.Fatalf("on-demand item still required artificial evidence: %#v %v", completed, err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE runs SET status='completed', finished_at=?, updated_at=? WHERE id=?`, ts(time.Now()), ts(time.Now()), run.ID); err == nil {
		t.Fatal("unfinished second item did not block Run finish")
	}
	request.WorkItemID, request.ExpectedVersion, request.OperationKey = selected.WorkItems[1].ID, 1, "on-demand-start-second"
	request.Target = domain.WorkItemInProgress
	started, err = control.TransitionWorkItem(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).ChangePhase(ctx, application.ChangeRunPhaseRequest{
		RunID: run.ID, Phase: "plan", OperationKey: "on-demand-return-plan", RequestedBy: "operator", Reason: "reconsider",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE work_items SET status='completed', version=version+1, completed_at=?, updated_at=? WHERE id=?`, ts(time.Now()), ts(time.Now()), started.CurrentWorkItem.ID); err == nil {
		t.Fatal("on-demand SQL completion bypassed Deliver phase")
	}
	if _, err := application.NewWorkItemService(st).Transition(ctx, started.CurrentWorkItem.ID, started.CurrentWorkItem.Version, domain.WorkItemCompleted, ""); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("on-demand service completion bypassed Deliver phase: %v", err)
	}
	if _, err := application.NewRunService(st).ChangePhase(ctx, application.ChangeRunPhaseRequest{
		RunID: run.ID, Phase: "deliver", OperationKey: "on-demand-return-deliver", RequestedBy: "operator", Reason: "continue accepted plan",
	}); err != nil {
		t.Fatal(err)
	}
	request.ExpectedVersion, request.OperationKey, request.Target = started.CurrentWorkItem.Version, "on-demand-complete-second", domain.WorkItemCompleted
	if _, err := control.TransitionWorkItem(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Complete(ctx, run.ID); err != nil {
		t.Fatalf("on-demand Plan still required manual checkpoint at finish: %v", err)
	}
	checkpoints, err := st.ListDeliveryCheckpoints(ctx, run.ID, 10)
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("on-demand completion invented manual checks: %#v %v", checkpoints, err)
	}
}
