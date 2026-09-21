package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/runmutation"
)

const fileEditAutoAuthorizationSelect = `SELECT operation_key_digest, proposal_fingerprint,
	run_id, session_id, workspace_id, operation_kind, path, destination_path,
	original_hash, proposed_hash, destination_original_hash, destination_proposed_hash,
	permission_snapshot_id, permission_revision, mode_revision, runtime_epoch,
	runtime_generation, agent_id, capability_generation, lease_id, lease_generation
	FROM file_edit_auto_authorizations WHERE edit_id=?`

func autoFileEditProposalFingerprint(edit fileedit.Edit) string {
	return runmutation.Fingerprint("agent_code_file_edit_proposal.v1",
		edit.ID, edit.SessionID, edit.WorkspaceID, edit.Operation, edit.Path,
		edit.DestinationPath, edit.OriginalHash, edit.ProposedHash,
		edit.DestinationOriginalHash, edit.DestinationProposedHash)
}

func validAutoFileEditDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func bindAutoFileEditAuthorization(edit fileedit.Edit, auth fileedit.AutoAuthorization) (fileedit.AutoAuthorization, error) {
	if auth.Operation == "" {
		auth.Operation = edit.Operation
	}
	if auth.Path == "" {
		auth.Path = edit.Path
	}
	if auth.DestinationPath == "" {
		auth.DestinationPath = edit.DestinationPath
	}
	if auth.OriginalHash == "" {
		auth.OriginalHash = edit.OriginalHash
	}
	if auth.ProposedHash == "" {
		auth.ProposedHash = edit.ProposedHash
	}
	if auth.DestinationOriginalHash == "" {
		auth.DestinationOriginalHash = edit.DestinationOriginalHash
	}
	if auth.DestinationProposedHash == "" {
		auth.DestinationProposedHash = edit.DestinationProposedHash
	}
	if edit.Status != fileedit.StatusProposed ||
		(edit.Operation != fileedit.OperationCreate && edit.Operation != fileedit.OperationReplace &&
			edit.Operation != fileedit.OperationMove) ||
		edit.ID == "" || edit.SessionID == "" || edit.WorkspaceID == "" ||
		auth.RunID == "" || auth.SessionID != edit.SessionID || auth.WorkspaceID != edit.WorkspaceID ||
		auth.Operation != edit.Operation || auth.Path != edit.Path ||
		auth.DestinationPath != edit.DestinationPath ||
		auth.OriginalHash != edit.OriginalHash || auth.ProposedHash != edit.ProposedHash ||
		auth.DestinationOriginalHash != edit.DestinationOriginalHash ||
		auth.DestinationProposedHash != edit.DestinationProposedHash ||
		!validAutoFileEditDigest(auth.OperationKeyDigest) ||
		auth.ProposalFingerprint != autoFileEditProposalFingerprint(edit) ||
		auth.PermissionSnapshotID == "" || auth.PermissionRevision <= 0 || auth.ModeRevision <= 0 ||
		auth.RuntimeEpoch == "" || auth.RuntimeGeneration == 0 || auth.RuntimeGeneration > math.MaxInt64 ||
		auth.AgentID == "" || !validAutoFileEditDigest(auth.CapabilityGeneration) ||
		auth.LeaseID == "" || auth.LeaseGeneration <= 0 {
		return fileedit.AutoAuthorization{}, apperror.New(apperror.CodeInvalidArgument,
			"automatic FileEdit authorization is incomplete or differs from the prepared proposal")
	}
	return auth, nil
}

func sameAutoFileEditAuthorization(left, right fileedit.AutoAuthorization) bool {
	return left == right
}

// CreateAutomaticallyAuthorizedFileEditIfAbsent creates a fresh approved edit
// and its immutable automatic source in one transaction. Existing edits can
// only be replayed when they already have the exact same automatic source.
func (s *SQLiteStore) CreateAutomaticallyAuthorizedFileEditIfAbsent(ctx context.Context,
	edit fileedit.Edit, auth fileedit.AutoAuthorization,
) (fileedit.Edit, bool, error) {
	edit, err := normalizeStoredFileEdit(edit)
	if err != nil {
		return fileedit.Edit{}, false, err
	}
	auth, err = bindAutoFileEditAuthorization(edit, auth)
	if err != nil {
		return fileedit.Edit{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fileedit.Edit{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// This insert acquires the SQLite writer fence before any replay decision.
	approved := edit
	approved.Status = fileedit.StatusApproved
	result, err := tx.ExecContext(ctx, `INSERT INTO file_edits
		(id, session_id, workspace_id, path, operation_kind, destination_path, status,
		 original_text, proposed_text, diff_text, original_hash, proposed_hash,
		 destination_original_hash, destination_proposed_hash,
		 reason, secrets_redacted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		approved.ID, approved.SessionID, approved.WorkspaceID, approved.Path, approved.Operation,
		approved.DestinationPath, approved.Status, approved.OriginalText, approved.ProposedText,
		approved.Diff, approved.OriginalHash, approved.ProposedHash,
		approved.DestinationOriginalHash, approved.DestinationProposedHash,
		approved.Reason, boolInt(approved.SecretsRedacted), ts(approved.CreatedAt), ts(approved.UpdatedAt))
	if err != nil {
		return fileedit.Edit{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fileedit.Edit{}, false, err
	}
	if inserted == 0 {
		existing, readErr := scanFileEdit(tx.QueryRowContext(ctx, `SELECT id, session_id,
			workspace_id, path, operation_kind, destination_path, status, original_text,
			proposed_text, diff_text, original_hash, proposed_hash, destination_original_hash,
			destination_proposed_hash, reason, secrets_redacted, created_at, updated_at
			FROM file_edits WHERE id=?`, edit.ID))
		if readErr != nil {
			return fileedit.Edit{}, false, readErr
		}
		storedAuth, found, readErr := getFileEditAutoAuthorizationTx(ctx, tx, edit.ID)
		if readErr != nil {
			return fileedit.Edit{}, false, readErr
		}
		if !found || !fileedit.SameProposalContent(existing, edit) ||
			!sameAutoFileEditAuthorization(storedAuth, auth) {
			return fileedit.Edit{}, false, apperror.New(apperror.CodeConflict,
				"file edit identity or automatic authorization already belongs to another proposal")
		}
		return existing, true, tx.Commit()
	}
	if err := requireFileEditWorkspaceTx(ctx, tx, edit.SessionID, edit.WorkspaceID, true); err != nil {
		return fileedit.Edit{}, false, err
	}
	if err := projectFileEditTx(ctx, tx, edit, "", false); err != nil {
		return fileedit.Edit{}, false, err
	}
	if err := projectFileEditWithAuthorizationSourceTx(ctx, tx, approved,
		fileedit.StatusProposed, true, "full_access_automatic"); err != nil {
		return fileedit.Edit{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO file_edit_auto_authorizations
		(edit_id, operation_key_digest, proposal_fingerprint, run_id, session_id,
		 workspace_id, operation_kind, path, destination_path, original_hash, proposed_hash,
		 destination_original_hash, destination_proposed_hash,
		 permission_snapshot_id, permission_revision, mode_revision, runtime_epoch,
		 runtime_generation, agent_id, capability_generation, lease_id, lease_generation, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		edit.ID, auth.OperationKeyDigest, auth.ProposalFingerprint, auth.RunID, auth.SessionID,
		auth.WorkspaceID, auth.Operation, auth.Path, auth.DestinationPath,
		auth.OriginalHash, auth.ProposedHash,
		auth.DestinationOriginalHash, auth.DestinationProposedHash,
		auth.PermissionSnapshotID, auth.PermissionRevision, auth.ModeRevision,
		auth.RuntimeEpoch, auth.RuntimeGeneration, auth.AgentID, auth.CapabilityGeneration,
		auth.LeaseID, auth.LeaseGeneration, ts(edit.CreatedAt))
	if err != nil {
		return fileedit.Edit{}, false, apperror.Wrap(apperror.CodeFailedPrecondition,
			"automatic FileEdit source is no longer current", err)
	}
	if err := syncFileEditApprovalTx(ctx, tx, approved, "", false); err != nil {
		return fileedit.Edit{}, false, err
	}
	return approved, false, tx.Commit()
}

func getFileEditAutoAuthorizationTx(ctx context.Context, tx *sql.Tx,
	editID string,
) (fileedit.AutoAuthorization, bool, error) {
	return scanFileEditAutoAuthorization(tx.QueryRowContext(ctx,
		fileEditAutoAuthorizationSelect, editID))
}

func scanFileEditAutoAuthorization(row scanner) (fileedit.AutoAuthorization, bool, error) {
	var auth fileedit.AutoAuthorization
	var generation int64
	err := row.Scan(&auth.OperationKeyDigest, &auth.ProposalFingerprint,
		&auth.RunID, &auth.SessionID, &auth.WorkspaceID, &auth.Operation,
		&auth.Path, &auth.DestinationPath, &auth.OriginalHash, &auth.ProposedHash,
		&auth.DestinationOriginalHash, &auth.DestinationProposedHash,
		&auth.PermissionSnapshotID, &auth.PermissionRevision, &auth.ModeRevision,
		&auth.RuntimeEpoch, &generation, &auth.AgentID,
		&auth.CapabilityGeneration, &auth.LeaseID, &auth.LeaseGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return fileedit.AutoAuthorization{}, false, nil
	}
	if err != nil {
		return fileedit.AutoAuthorization{}, false, err
	}
	if generation <= 0 {
		return fileedit.AutoAuthorization{}, false, apperror.New(apperror.CodeConflict,
			"stored automatic FileEdit authorization generation is invalid")
	}
	auth.RuntimeGeneration = uint64(generation)
	return auth, true, nil
}

// GetFileEditAutoAuthorization reads immutable provenance without granting any
// new permission. Callers must still recheck the current process grant.
func (s *SQLiteStore) GetFileEditAutoAuthorization(ctx context.Context,
	editID string,
) (fileedit.AutoAuthorization, bool, error) {
	auth, found, err := scanFileEditAutoAuthorization(s.db.QueryRowContext(ctx,
		fileEditAutoAuthorizationSelect, strings.TrimSpace(editID)))
	if err != nil || !found {
		return auth, found, err
	}
	edit, err := s.GetFileEdit(ctx, editID)
	if err != nil {
		return fileedit.AutoAuthorization{}, false, err
	}
	if auth.ProposalFingerprint != autoFileEditProposalFingerprint(edit) ||
		auth.SessionID != edit.SessionID || auth.WorkspaceID != edit.WorkspaceID ||
		auth.Operation != edit.Operation || auth.Path != edit.Path ||
		auth.DestinationPath != edit.DestinationPath ||
		auth.OriginalHash != edit.OriginalHash || auth.ProposedHash != edit.ProposedHash ||
		auth.DestinationOriginalHash != edit.DestinationOriginalHash ||
		auth.DestinationProposedHash != edit.DestinationProposedHash {
		return fileedit.AutoAuthorization{}, false, apperror.New(apperror.CodeConflict,
			"automatic FileEdit source no longer matches its proposal")
	}
	return auth, true, nil
}
