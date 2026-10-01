package application_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/projectconfig"
	"cyberagent-workbench/internal/store"
)

func TestProjectInstructionDeliveryRequiresConfirmedRefreshAndSurvivesReopen(t *testing.T) {
	ctx := t.Context()
	root, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "delivery.db")
	filename := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(filename, []byte("PINNED_REQUIRED_RULE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.SaveWorkspace(ctx, store.WorkspaceRecord{ID: "delivery-workspace", Name: "delivery", RootPath: root, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := projectconfig.DiscoverInstructions(ctx, root, ".")
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "preserve required rules", Profile: "review", WorkspaceID: "delivery-workspace",
		Budget: domain.DefaultBudget(), ProjectInstructions: &snapshot, RequestedBy: "cli_operator"})
	if err != nil {
		t.Fatal(err)
	}
	svc := application.NewProjectInstructionService(st)
	classes := []projectconfig.InstructionSourceDelivery{{Path: snapshot.Sources[0].Path,
		ContentSHA256: snapshot.Sources[0].ContentSHA256, Requirement: projectconfig.InstructionMandatory}}
	classified, err := projectconfig.ClassifyInstructionSnapshot(snapshot, classes)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "model must not confirm delivery", Profile: "review", WorkspaceID: "delivery-workspace",
		Budget: domain.DefaultBudget(), ProjectInstructions: &classified, RequestedBy: "model",
	}); err == nil {
		t.Fatal("model confirmed classification through initial Run creation")
	}
	unconfirmed, err := svc.RefreshWithDelivery(ctx, run.ID, ".", snapshot.Fingerprint, snapshot.Fingerprint, "cli_operator", false, classes)
	if err != nil || unconfirmed.Pinned.Snapshot.Delivery != nil {
		t.Fatalf("classification bypassed confirmation: %v", err)
	}
	if _, err := svc.RefreshWithDelivery(ctx, run.ID, ".", snapshot.Fingerprint, snapshot.Fingerprint, "model", true, classes); err == nil {
		t.Fatal("model classified project instructions")
	}
	confirmed, err := svc.RefreshWithDelivery(ctx, run.ID, ".", snapshot.Fingerprint, snapshot.Fingerprint, "cli_operator", true, classes)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Stale || !confirmed.RefreshConfirmed || confirmed.Pinned.Revision != 2 {
		t.Fatalf("bad confirmation: %#v", confirmed)
	}
	if err := os.WriteFile(filename, []byte("REFRESHED_REQUIRED_RULE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	drift, err := svc.Inspect(ctx, run.ID, ".")
	if err != nil || !drift.Stale || drift.Pinned.Snapshot.Fingerprint != confirmed.Pinned.Snapshot.Fingerprint {
		t.Fatalf("unpinned drift: %v", err)
	}
	if _, err := svc.Refresh(ctx, run.ID, ".", drift.Pinned.Snapshot.Fingerprint, drift.Live.Fingerprint, "cli_operator", true); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("classification silently lost: %v", err)
	}
	if _, _, err := st.ConfirmRunInstructionSnapshot(ctx, run.ID, drift.Pinned.Snapshot.Fingerprint,
		drift.Live, drift.Diff, "cli_operator", time.Now().UTC()); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("direct store dropped classification: %v", err)
	}
	classes[0].ContentSHA256 = drift.Live.Sources[0].ContentSHA256
	refreshed, err := svc.RefreshWithDelivery(ctx, run.ID, ".", drift.Pinned.Snapshot.Fingerprint, drift.Live.Fingerprint, "cli_operator", true, classes)
	if err != nil || refreshed.Pinned.Revision != 3 {
		t.Fatalf("refresh failed: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := application.NewProjectInstructionService(st).Inspect(ctx, run.ID, ".")
	if err != nil || reopened.Stale || reopened.Pinned.Snapshot.Fingerprint != refreshed.Pinned.Snapshot.Fingerprint ||
		reopened.Pinned.Snapshot.Delivery.Sources[0].Requirement != projectconfig.InstructionMandatory || len(reopened.History) != 3 {
		t.Fatalf("reopen lost pinned classification: %v", err)
	}
}
