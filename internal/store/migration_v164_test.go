package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/fileedit"
)

func TestSchemaV164PreservesLegacyAutomaticSourcesAndAdmitsBoundMove(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "automatic-move-v164.db")
	state := openHistoricalTestDatabase(t, path, 163)
	defer state.Close()
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
		fileedit.ApprovalFingerprint(legacy.SessionID, legacy.WorkspaceID, legacy),
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
	stored, found, err := readHistoricalAutomaticFileSource(t, state, legacy.ID)
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
	insertHistoricalAutomaticFileEdit(t, state, move, moveAuth)
	approved, err := state.GetFileEdit(ctx, move.ID)
	if err != nil || approved.Status != fileedit.StatusApproved {
		t.Fatalf("v164 automatic move=%+v err=%v", approved, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func readHistoricalAutomaticFileSource(t *testing.T, state *SQLiteStore, editID string) (fileedit.AutoAuthorization, bool, error) {
	t.Helper()
	// Read the actual pre-v179 row, whose schema intentionally has no Run fence.
	return scanFileEditAutoAuthorization(state.db.QueryRowContext(t.Context(), strings.Replace(fileEditAutoAuthorizationSelect, "run_authorization_fence", "0", 1), editID))
}

// Exact historical rows are inserted under their original SQLite constraints.
// A current application writer must not be revived to manufacture old modes.
func insertHistoricalAutomaticFileEdit(t *testing.T, state *SQLiteStore, edit fileedit.Edit, auth fileedit.AutoAuthorization) {
	t.Helper()
	ctx := t.Context()
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_edits
		(id,session_id,workspace_id,path,operation_kind,destination_path,status,original_text,proposed_text,diff_text,original_hash,proposed_hash,
		destination_original_hash,destination_proposed_hash,reason,secrets_redacted,created_at,updated_at) VALUES(?,?,?,?,?,?,'approved',?,?,?,?,?,?,?,?,?,?,?)`,
		edit.ID, edit.SessionID, edit.WorkspaceID, edit.Path, edit.Operation, edit.DestinationPath, edit.OriginalText, edit.ProposedText, edit.Diff, edit.OriginalHash, edit.ProposedHash,
		edit.DestinationOriginalHash, edit.DestinationProposedHash, edit.Reason, boolInt(edit.SecretsRedacted), ts(edit.CreatedAt), ts(edit.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_edit_auto_authorizations
		(edit_id,operation_key_digest,proposal_fingerprint,run_id,session_id,workspace_id,operation_kind,path,destination_path,original_hash,proposed_hash,
		destination_original_hash,destination_proposed_hash,permission_snapshot_id,permission_revision,mode_revision,runtime_epoch,runtime_generation,agent_id,capability_generation,lease_id,lease_generation,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, edit.ID, auth.OperationKeyDigest, auth.ProposalFingerprint, auth.RunID, auth.SessionID, auth.WorkspaceID,
		edit.Operation, edit.Path, edit.DestinationPath, edit.OriginalHash, edit.ProposedHash, edit.DestinationOriginalHash, edit.DestinationProposedHash,
		auth.PermissionSnapshotID, auth.PermissionRevision, auth.ModeRevision, auth.RuntimeEpoch, auth.RuntimeGeneration, auth.AgentID, auth.CapabilityGeneration, auth.LeaseID, auth.LeaseGeneration, ts(edit.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	tool := fileedit.ApprovalToolName(edit)
	if _, err := tx.ExecContext(ctx, `INSERT INTO tool_approvals
		(id,idempotency_key,proposal_id,run_id,session_id,workspace_id,tool_name,action_class,mode,status,request_fingerprint,decision_reason,requested_by,reviewed_by,version,created_at,updated_at,decided_at)
		VALUES(?,?,?,?,?,?,?,'workspace_write','automatic','approved',?,'Full Access automatically authorized this file edit','tool_gateway','automatic_policy',1,?,?,?)`,
		"historical-"+edit.ID, approval.ProposalIdempotencyKey(tool, edit.ID), edit.ID, auth.RunID, edit.SessionID, edit.WorkspaceID, tool,
		fileedit.ApprovalFingerprint(edit.SessionID, edit.WorkspaceID, edit), ts(edit.CreatedAt), ts(edit.UpdatedAt), ts(edit.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
