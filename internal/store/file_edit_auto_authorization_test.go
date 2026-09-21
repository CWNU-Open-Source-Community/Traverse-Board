package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/runmutation"
)

func autoFileEditFixture(t *testing.T) (*SQLiteStore, domain.Run, WorkspaceRecord, fileedit.AutoAuthorization) {
	t.Helper()
	state, err := Open(filepath.Join(t.TempDir(), "auto-file-edit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	run, workspace, auth := populateAutoFileEditFixture(t, state)
	return state, run, workspace, auth
}

func populateAutoFileEditFixture(t *testing.T, state *SQLiteStore) (domain.Run, WorkspaceRecord, fileedit.AutoAuthorization) {
	t.Helper()
	ctx := t.Context()
	workspace := WorkspaceRecord{ID: "auto-file-edit-workspace", Name: "auto-edit", RootPath: t.TempDir()}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "automatic file edit", Profile: "code", Surface: "code", Phase: "plan",
		WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.ChangePhase(ctx, application.ChangeRunPhaseRequest{
		RunID: run.ID, Phase: "deliver", OperationKey: "auto-file-edit-deliver",
		RequestedBy: "operator", Reason: "deliver work",
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "auto-file-edit-full", RequestedBy: "operator",
		Reason: "auto file edit", ConfirmDangerFullAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runs.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := state.RegisterRootAgent(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "auto-file-edit-worker", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run, workspace, fileedit.AutoAuthorization{
		RunID: run.ID, SessionID: run.SessionID, WorkspaceID: workspace.ID,
		PermissionSnapshotID: selected.Permission.ID,
		PermissionRevision:   selected.Permission.Revision, ModeRevision: mode.Revision,
		RuntimeEpoch: "process-epoch-test", RuntimeGeneration: 1, AgentID: root.ID,
		CapabilityGeneration: runmutation.Fingerprint("capability", run.ID),
		LeaseID:              lease.Lease.LeaseID, LeaseGeneration: lease.Lease.Generation,
	}
}

func prepareAutoMove(t *testing.T, state *SQLiteStore, workspace WorkspaceRecord,
	auth fileedit.AutoAuthorization, id, sourcePath, destinationPath, key string,
) (fileedit.Edit, fileedit.AutoAuthorization) {
	t.Helper()
	sourceHash, err := fileedit.CurrentHash(workspace.RootPath, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := fileedit.NewManager(state).PrepareProposal(t.Context(), fileedit.Proposal{
		ID: id, SessionID: auth.SessionID, WorkspaceID: auth.WorkspaceID,
		WorkspaceRoot: workspace.RootPath, Path: sourcePath, Operation: fileedit.OperationMove,
		DestinationPath: destinationPath, ExpectedOriginalHash: sourceHash,
		ExpectedDestinationHash: "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	auth.OperationKeyDigest = runmutation.Fingerprint("auto-file-edit-key", key)
	auth.ProposalFingerprint = autoFileEditProposalFingerprint(edit)
	return edit, auth
}

func prepareAutoFileEdit(t *testing.T, state *SQLiteStore,
	workspace WorkspaceRecord, auth fileedit.AutoAuthorization, id, text, key string,
) (fileedit.Edit, fileedit.AutoAuthorization) {
	t.Helper()
	edit, err := fileedit.NewManager(state).PrepareProposal(t.Context(), fileedit.Proposal{
		ID: id, SessionID: auth.SessionID, WorkspaceID: auth.WorkspaceID,
		WorkspaceRoot: workspace.RootPath, Path: "new.txt", Operation: fileedit.OperationCreate,
		ExpectedOriginalHash: "missing", ProposedText: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	auth.OperationKeyDigest = runmutation.Fingerprint("auto-file-edit-key", key)
	auth.ProposalFingerprint = autoFileEditProposalFingerprint(edit)
	return edit, auth
}

func TestAutomaticFileEditSourceCannotUpgradeOldProposalOrChangeKey(t *testing.T) {
	state, _, workspace, base := autoFileEditFixture(t)
	ctx := t.Context()
	edit, auth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-auto-file-1", "hello\n", "one")
	approved, replay, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, edit, auth)
	if err != nil || replay || approved.Status != fileedit.StatusApproved {
		t.Fatalf("create automatic edit: %+v replay=%t err=%v", approved, replay, err)
	}
	stored, found, err := state.GetFileEditAutoAuthorization(ctx, edit.ID)
	if err != nil || !found || stored.Operation != edit.Operation || stored.Path != edit.Path ||
		stored.OriginalHash != edit.OriginalHash || stored.ProposedHash != edit.ProposedHash {
		t.Fatalf("stored automatic source: %+v found=%t err=%v", stored, found, err)
	}
	var mode, reviewedBy, status, decisionReason string
	if err := state.db.QueryRowContext(ctx, `SELECT mode,reviewed_by,status,decision_reason FROM tool_approvals WHERE proposal_id=?`, edit.ID).
		Scan(&mode, &reviewedBy, &status, &decisionReason); err != nil || mode != "automatic" ||
		reviewedBy != "automatic_policy" || status != "approved" || decisionReason == "" {
		t.Fatalf("automatic approval mode=%q reviewer=%q status=%q reason=%q err=%v",
			mode, reviewedBy, status, decisionReason, err)
	}
	var eventSource string
	if err := state.db.QueryRowContext(ctx, `SELECT json_extract(payload_json,'$.authorization_source')
		FROM run_events WHERE run_id=? AND subject_id=? AND type='file_edit.approved'`,
		base.RunID, edit.ID).Scan(&eventSource); err != nil || eventSource != "full_access_automatic" {
		t.Fatalf("approved event source=%q err=%v", eventSource, err)
	}
	if same, wasReplay, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, edit, auth); err != nil || !wasReplay || same.ID != approved.ID {
		t.Fatalf("exact replay: %+v replay=%t err=%v", same, wasReplay, err)
	}
	changed := auth
	changed.OperationKeyDigest = runmutation.Fingerprint("auto-file-edit-key", "other")
	if _, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, edit, changed); err == nil {
		t.Fatal("same edit accepted a changed authorization key")
	}
	otherEdit, otherAuth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-auto-file-2", "different\n", "one")
	if _, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, otherEdit, otherAuth); err == nil {
		t.Fatal("same operation key accepted different proposal")
	}
	manual, manualAuth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-old-pending", "manual\n", "manual")
	if _, err := state.SaveFileEdit(ctx, manual); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, manual, manualAuth); err == nil {
		t.Fatal("existing pending edit was promoted to automatic approval")
	}
	var count int
	if err := state.db.QueryRowContext(ctx, `SELECT count(*) FROM file_edit_auto_authorizations WHERE edit_id=?`, manual.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old pending edit gained automatic source: count=%d err=%v", count, err)
	}
	denied, deniedAuth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-old-denied", "denied\n", "denied")
	if _, err := state.SaveFileEdit(ctx, denied); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DecideApproval(ctx, approval.DecisionRequest{
		ProposalID: denied.ID, IdempotencyKey: "auto-edit-deny-old",
		Action: approval.ActionDeny, Reason: "operator denied", ReviewedBy: "operator",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fileedit.NewManager(state).Deny(ctx, denied.ID, "operator denied"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, denied, deniedAuth); err == nil {
		t.Fatal("existing denied edit was promoted to automatic approval")
	}
	if err := state.db.QueryRowContext(ctx, `SELECT count(*) FROM file_edit_auto_authorizations WHERE edit_id=?`, denied.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old denied edit gained automatic source: count=%d err=%v", count, err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO tool_approvals
		(id,idempotency_key,proposal_id,run_id,session_id,workspace_id,tool_name,
		 action_class,mode,status,request_fingerprint,decision_reason,requested_by,
		 reviewed_by,version,created_at,updated_at,decided_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"approval-without-source", "auto-without-source", "edit-without-source",
		base.RunID, base.SessionID, base.WorkspaceID, "create_file", "workspace_write",
		"automatic", "approved", runmutation.Fingerprint("missing-source"), "",
		"tool_gateway", "automatic_policy", 1, ts(time.Now().UTC()),
		ts(time.Now().UTC()), ts(time.Now().UTC())); err == nil {
		t.Fatal("automatic file approval without an immutable source was inserted")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE file_edit_auto_authorizations SET runtime_generation=2 WHERE edit_id=?`, edit.ID); err == nil {
		t.Fatal("immutable automatic source was updated")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE tool_approvals SET reviewed_by='operator' WHERE proposal_id=?`, edit.ID); err == nil {
		t.Fatal("automatic approval was rewritten as operator review")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE file_edits SET proposed_text='different' WHERE id=?`, edit.ID); err == nil {
		t.Fatal("authorized proposal body was rewritten")
	}
}

func TestAutomaticFileEditPreWriteCheckFencesWorkspacePublish(t *testing.T) {
	state, _, workspace, base := autoFileEditFixture(t)
	edit, auth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-auto-write", "content\n", "write")
	if _, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(t.Context(), edit, auth); err != nil {
		t.Fatal(err)
	}
	manager := fileedit.NewManager(state)
	path := filepath.Join(workspace.RootPath, "new.txt")
	if _, err := manager.ApproveWithPreWriteCheck(t.Context(), edit.ID, workspace.RootPath,
		func() error { return errors.New("grant revoked") }); err == nil {
		t.Fatal("revoked grant passed pre-write check")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("revoked grant wrote target: %v", err)
	}
	lateChecks := 0
	if _, err := manager.ApproveWithPreWriteCheck(t.Context(), edit.ID, workspace.RootPath,
		func() error {
			lateChecks++
			if lateChecks == 2 {
				return errors.New("grant revoked before publish")
			}
			return nil
		}); err == nil || lateChecks != 2 {
		t.Fatalf("late revocation was not checked before publish: checks=%d err=%v", lateChecks, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("late revocation wrote target: %v", err)
	}
	staged, err := filepath.Glob(filepath.Join(workspace.RootPath, ".cyberagent-edit-*"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("late revocation left staging files: paths=%v err=%v", staged, err)
	}
	checks := 0
	applied, err := manager.ApproveWithPreWriteCheck(t.Context(), edit.ID, workspace.RootPath,
		func() error { checks++; return nil })
	if err != nil || applied.Status != fileedit.StatusApplied || checks < 2 {
		t.Fatalf("checked automatic apply: %+v checks=%d err=%v", applied, checks, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "content\n" {
		t.Fatalf("written content=%q err=%v", contents, err)
	}
	var mode, reviewer string
	if err := state.db.QueryRowContext(context.Background(), `SELECT mode,reviewed_by FROM tool_approvals WHERE proposal_id=?`, edit.ID).
		Scan(&mode, &reviewer); err != nil || mode != "automatic" || reviewer != "automatic_policy" {
		t.Fatalf("applied approval provenance mode=%q reviewer=%q err=%v", mode, reviewer, err)
	}
}

func TestAutomaticMoveAuthorizationBindsDestinationAndRejectsChangedReplay(t *testing.T) {
	state, _, workspace, base := autoFileEditFixture(t)
	if err := os.WriteFile(filepath.Join(workspace.RootPath, "source.txt"),
		[]byte("move\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit, auth := prepareAutoMove(t, state, workspace, base,
		"edit-auto-move-1", "source.txt", "destination.txt", "move-one")
	approved, replay, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(
		t.Context(), edit, auth)
	if err != nil || replay || approved.Status != fileedit.StatusApproved {
		t.Fatalf("create automatic move: %+v replay=%t err=%v", approved, replay, err)
	}
	stored, found, err := state.GetFileEditAutoAuthorization(t.Context(), edit.ID)
	if err != nil || !found || stored.Operation != fileedit.OperationMove ||
		stored.DestinationPath != edit.DestinationPath ||
		stored.DestinationOriginalHash != edit.DestinationOriginalHash ||
		stored.DestinationProposedHash != edit.DestinationProposedHash {
		t.Fatalf("stored automatic move source: %+v found=%t err=%v", stored, found, err)
	}
	changed := auth
	changed.DestinationPath = "other.txt"
	if _, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(
		t.Context(), edit, changed); err == nil {
		t.Fatal("automatic move replay accepted a changed destination")
	}
	var toolName, mode string
	if err := state.db.QueryRowContext(t.Context(),
		`SELECT tool_name, mode FROM tool_approvals WHERE proposal_id=?`, edit.ID).
		Scan(&toolName, &mode); err != nil || toolName != "move_file" || mode != "automatic" {
		t.Fatalf("automatic move approval tool=%q mode=%q err=%v", toolName, mode, err)
	}
}

func TestAutomaticFileEditApplyJournalRequiresCurrentFullAccessAndLiveLease(t *testing.T) {
	state, run, workspace, base := autoFileEditFixture(t)
	ctx := t.Context()
	edit, auth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-auto-journal-1", "journal\n", "journal-1")
	approved, _, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, edit, auth)
	if err != nil {
		t.Fatal(err)
	}
	operation := v153ApplyOperation(run, approved, "auto-journal-1")
	operation.AppliedBy = base.AgentID
	if _, _, _, err := state.PrepareFileEditApply(ctx, operation); err != nil {
		t.Fatalf("current automatic apply journal was rejected: %v", err)
	}
	second, secondAuth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-auto-journal-2", "journal2\n", "journal-2")
	second, _, err = state.CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx, second, secondAuth)
	if err != nil {
		t.Fatal(err)
	}
	currentLease, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("current lease found=%t err=%v", found, err)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, currentLease); err != nil {
		t.Fatal(err)
	}
	secondOperation := v153ApplyOperation(run, second, "auto-journal-2")
	secondOperation.AppliedBy = base.AgentID
	if _, _, _, err := state.PrepareFileEditApply(ctx, secondOperation); err == nil {
		t.Fatal("automatic apply journal accepted a released lease")
	}
	if _, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "auto-file-edit-new-worker", TTL: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := state.PrepareFileEditApply(ctx, secondOperation); err != nil {
		t.Fatalf("new live lease did not continue automatic apply journal: %v", err)
	}
}
