package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestThreadPlanObservationReopensQueryOnlyWhileAnotherWriterHoldsReservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan-observation.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Observe original mode", Profile: "review", Surface: "code", Phase: "plan", Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	request := application.ThreadPlanControlRequest{Version: application.PlanDeliveryControlProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), RunID: run.ID, Action: "enter_deliver", OperationKey: "plan-mode-original-reopen-observation", RequestedBy: "operator"}
	controlled := application.NewThreadTurnService(st, nil, nil)
	applied, err := controlled.ControlPlan(t.Context(), request)
	if err != nil || applied.State != "completed" {
		t.Fatalf("mode=%+v %v", applied, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	before, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	requestObservationQueryOnly(t, st, true)
	tx, err := writer.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	observed, err := application.NewThreadTurnService(st, nil, nil).InspectPlan(ctx, request)
	if err != nil || observed.State != "completed" || observed.AppliedMode.ID != applied.AppliedMode.ID || observed.ExecutionStarted || observed.ModelCalled {
		t.Fatalf("read acquired writer lock or failed original binding: %+v %v", observed, err)
	}
	stale, err := st.ThreadPlanConfirmationStale(ctx, request.ThreadID, run.ID, "")
	if err != nil || stale {
		t.Fatalf("stale observation=%t %v", stale, err)
	}
	after, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || len(after) != len(before) {
		t.Fatalf("observation changed history: %v", err)
	}
}
