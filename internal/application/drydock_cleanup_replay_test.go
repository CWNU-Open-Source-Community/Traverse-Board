package application

import (
	"os"
	"testing"

	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/store"
)

func TestDrydockCleanupReservationSurvivesRestartAfterRemoval(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "reserved cleanup restart")
	created := mustCreateDrydock(t, fixture)
	request := DrydockCleanupRequest{RunID: fixture.run.ID, ExpectedGeneration: created.Generation,
		OperationKey: "reserved-cleanup-restart-1", RequestedBy: "operator", Confirm: true}
	digest := drydockOperationDigest(drydock.OperationCleanup, request.RunID, request.OperationKey)
	if err := fixture.service.beginDrydockCleanup(t.Context(), request, digest); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.executor.PlanRemove(t.Context(), fixture.sourceRoot, created.Name, created.ManagedWorktreeID)
	if err != nil || !preview.Executable() {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	removed, err := fixture.executor.ExecuteRemove(t.Context(), fixture.sourceRoot, preview)
	if err != nil || removed.Status != gitadvanced.ReceiptSucceeded {
		t.Fatalf("remove=%+v err=%v", removed, err)
	}
	if err := fixture.state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	service, err := NewDrydockService(reopened, fixture.executor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	pending, found, err := reopened.GetDrydockByRun(t.Context(), request.RunID)
	if err != nil || !found || pending.Generation != created.Generation || pending.LastCheckpointID != created.LastCheckpointID {
		t.Fatalf("startup changed an unresolved cleanup: %+v %v", pending, err)
	}
	result, err := service.Cleanup(t.Context(), request)
	if err != nil || result.Workspace.State != drydock.StateCleaned || result.Receipt.CheckpointID != "" {
		t.Fatalf("confirm removed directory=%+v err=%v", result, err)
	}
	if result.Workspace.LastCheckpointID != created.LastCheckpointID {
		t.Fatal("cleanup synthesized a content checkpoint")
	}
	replay, err := service.Cleanup(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Receipt.ID != result.Receipt.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := os.Lstat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("unexpected directory after cleanup: %v", err)
	}
}
