package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/fileedit"
)

func TestSchemaV164PreservesLegacyAutomaticSourcesAndAdmitsBoundMove(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "automatic-move-v164.db")
	state := openUnmigratedSQLiteStore(t, path)
	defer state.Close()
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 163); err != nil {
		t.Fatal(err)
	}
	_, workspace, base := populateAutoFileEditFixture(t, state)
	legacy, legacyAuth := prepareAutoFileEdit(t, state, workspace, base,
		"edit-v163-auto-create", "legacy\n", "legacy-key")
	legacy.Status = fileedit.StatusApproved
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_edits
		(id, session_id, workspace_id, path, operation_kind, destination_path, status,
		 original_text, proposed_text, diff_text, original_hash, proposed_hash,
		 destination_original_hash, destination_proposed_hash,
		 reason, secrets_redacted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?)`,
		legacy.ID, legacy.SessionID, legacy.WorkspaceID, legacy.Path, legacy.Operation,
		legacy.Status, legacy.OriginalText, legacy.ProposedText, legacy.Diff,
		legacy.OriginalHash, legacy.ProposedHash, legacy.Reason,
		boolInt(legacy.SecretsRedacted), ts(legacy.CreatedAt), ts(legacy.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_edit_auto_authorizations
		(edit_id, operation_key_digest, proposal_fingerprint, run_id, session_id,
		 workspace_id, operation_kind, path, original_hash, proposed_hash,
		 permission_snapshot_id, permission_revision, mode_revision, runtime_epoch,
		 runtime_generation, agent_id, capability_generation, lease_id,
		 lease_generation, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		legacy.ID, legacyAuth.OperationKeyDigest, legacyAuth.ProposalFingerprint,
		legacyAuth.RunID, legacyAuth.SessionID, legacyAuth.WorkspaceID,
		legacy.Operation, legacy.Path, legacy.OriginalHash, legacy.ProposedHash,
		legacyAuth.PermissionSnapshotID, legacyAuth.PermissionRevision,
		legacyAuth.ModeRevision, legacyAuth.RuntimeEpoch, legacyAuth.RuntimeGeneration,
		legacyAuth.AgentID, legacyAuth.CapabilityGeneration, legacyAuth.LeaseID,
		legacyAuth.LeaseGeneration, ts(legacy.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tool_approvals
		(id,idempotency_key,proposal_id,run_id,session_id,workspace_id,tool_name,
		 action_class,mode,status,request_fingerprint,decision_reason,requested_by,
		 reviewed_by,version,created_at,updated_at,decided_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"approval-v163-auto-create", approval.ProposalIdempotencyKey("create_file", legacy.ID),
		legacy.ID, legacyAuth.RunID, legacy.SessionID, legacy.WorkspaceID, "create_file",
		"workspace_write", "automatic", "approved",
		fileEditApprovalFingerprint(legacy.SessionID, legacy.WorkspaceID, legacy),
		"Full Access automatically authorized this file edit", "tool_gateway",
		"automatic_policy", 1, ts(legacy.CreatedAt), ts(legacy.UpdatedAt),
		ts(legacy.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := state.applyMigration(ctx, migrationPlan()[163]); err != nil {
		t.Fatal(err)
	}
	stored, found, err := state.GetFileEditAutoAuthorization(ctx, legacy.ID)
	if err != nil || !found || stored.Operation != fileedit.OperationCreate ||
		stored.DestinationPath != "" || stored.DestinationOriginalHash != "" ||
		stored.DestinationProposedHash != "" {
		t.Fatalf("migrated legacy source=%+v found=%t err=%v", stored, found, err)
	}
	var approvalStatus string
	if err := state.db.QueryRowContext(ctx,
		`SELECT status FROM tool_approvals WHERE proposal_id=?`, legacy.ID).
		Scan(&approvalStatus); err != nil || approvalStatus != "approved" {
		t.Fatalf("legacy approval status=%q err=%v", approvalStatus, err)
	}
	if err := os.WriteFile(filepath.Join(workspace.RootPath, "move-source.txt"),
		[]byte("move after upgrade\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	move, moveAuth := prepareAutoMove(t, state, workspace, base,
		"edit-v164-auto-move", "move-source.txt", "move-destination.txt", "move-key")
	approved, replayed, err := state.CreateAutomaticallyAuthorizedFileEditIfAbsent(
		ctx, move, moveAuth)
	if err != nil || replayed || approved.Status != fileedit.StatusApproved {
		t.Fatalf("v164 automatic move=%+v replayed=%t err=%v", approved, replayed, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}
