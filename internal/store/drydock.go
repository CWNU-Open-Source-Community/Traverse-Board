package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runworktree"
)

const drydockWorkspaceColumns = `id, protocol_version, run_id, mission_id,
	session_id, source_workspace_id, workspace_id, trust_id, source_identity_sha256,
	root_path, root_path_sha256, source_root_fingerprint, repository_sha256,
	common_dir_sha256, source_branch, base_commit, object_format, name, path,
	path_sha256, branch, root_fingerprint, expected_head,
	expected_binding_fingerprint, create_preview_id, create_git_receipt_id,
	managed_worktree_id, state,
	generation, last_checkpoint_id, last_delivery_id, recovery_reason, expires_at,
	created_at, updated_at, cleaned_at`

const drydockReceiptColumns = `id, protocol_version, operation_key_sha256,
	request_fingerprint, drydock_id, run_id, operation, outcome, generation_before,
	generation_after, source_identity_sha256, root_fingerprint,
	binding_before_sha256, binding_after_sha256, git_receipt_id, checkpoint_id,
	delivery_id, reason_code, summary, grants_process_authority, created_at`

type drydockQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) CreateDrydockTrust(ctx context.Context,
	value runworktree.Trust,
) (runworktree.Trust, bool, error) {
	if err := value.Validate(); err != nil {
		return runworktree.Trust{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return runworktree.Trust{}, false, err
	}
	defer tx.Rollback()
	valueResult, replayed, err := createDrydockTrustTx(ctx, tx, value)
	if err != nil {
		return runworktree.Trust{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runworktree.Trust{}, false, err
	}
	return valueResult, replayed, nil
}

func createDrydockTrustTx(ctx context.Context, tx *sql.Tx, value runworktree.Trust) (runworktree.Trust, bool, error) {
	var err error
	if existing, found, getErr := getDrydockTrustByRun(ctx, tx, value.RunID); getErr != nil {
		return runworktree.Trust{}, false, getErr
	} else if found {
		if !reflect.DeepEqual(existing, value) {
			return runworktree.Trust{}, false, apperror.New(apperror.CodeConflict,
				"Drydock Workspace Trust is already bound to a different source identity")
		}
		return existing, true, nil
	}
	identity := value.Source
	state := value.SourceState
	_, err = tx.ExecContext(ctx, `INSERT INTO drydock_workspace_trust
		(id, protocol_version, run_id, workspace_id, source_identity_sha256,
		 root_path, root_path_sha256, root_fingerprint, repository_sha256,
		 common_dir_sha256, branch, base_commit, object_format, index_sha256,
		 worktree_sha256, status_sha256, source_captured_at, dirty_tracked, dirty_untracked,
		 dirty_ignored, symlink_entries, submodule_entries, confirmed_by,
		 grants_process_authority, confirmed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		value.ID, value.ProtocolVersion, value.RunID, value.WorkspaceID,
		identity.Fingerprint(), identity.RootPath, identity.RootPathSHA256,
		identity.RootFingerprint, identity.RepositorySHA256, identity.CommonDirSHA256,
		identity.Branch, identity.BaseCommit, identity.ObjectFormat, state.IndexSHA256,
		state.WorktreeSHA256, state.StatusSHA256, ts(state.CapturedAt), boolInt(state.DirtyTracked),
		boolInt(state.DirtyUntracked), boolInt(state.DirtyIgnored), state.SymlinkEntries,
		state.SubmoduleEntries, value.ConfirmedBy, ts(value.ConfirmedAt))
	if err != nil {
		return runworktree.Trust{}, false, normalizeDrydockStoreError(err)
	}
	event, err := events.New(value.RunID, drydockMissionIDTx(ctx, tx, value.RunID),
		events.DrydockTrustConfirmedEvent, "drydock", value.ID, map[string]any{
			"trust_id": value.ID, "workspace_id": value.WorkspaceID,
			"source_identity_sha256": identity.Fingerprint(),
			"dirty_tracked":          state.DirtyTracked, "dirty_untracked": state.DirtyUntracked,
			"dirty_ignored": state.DirtyIgnored, "grants_process_authority": false,
		})
	if err != nil {
		return runworktree.Trust{}, false, err
	}
	event.CreatedAt = value.ConfirmedAt
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return runworktree.Trust{}, false, err
	}
	return value, false, nil
}

func (s *SQLiteStore) GetDrydockTrustByRun(ctx context.Context,
	runID string,
) (runworktree.Trust, bool, error) {
	return getDrydockTrustByRun(ctx, s.db, strings.TrimSpace(runID))
}

func (s *SQLiteStore) PrepareDrydock(ctx context.Context,
	value runworktree.Workspace,
) (runworktree.Workspace, bool, error) {
	if err := value.Validate(); err != nil || value.State != runworktree.StatePreparing {
		return runworktree.Workspace{}, false, errors.New("prepared Drydock is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return runworktree.Workspace{}, false, err
	}
	defer tx.Rollback()
	valueResult, replayed, err := prepareDrydockTx(ctx, tx, value)
	if err != nil {
		return runworktree.Workspace{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runworktree.Workspace{}, false, err
	}
	return valueResult, replayed, nil
}

func prepareDrydockTx(ctx context.Context, tx *sql.Tx, value runworktree.Workspace) (runworktree.Workspace, bool, error) {
	var err error
	if existing, found, getErr := getDrydockByRun(ctx, tx, value.RunID); getErr != nil {
		return runworktree.Workspace{}, false, getErr
	} else if found {
		if !sameDrydockIdentity(existing, value) {
			return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
				"Run is already bound to another Drydock")
		}
		return existing, true, nil
	}
	var total, repositoryCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drydock_workspaces
		WHERE state <> 'cleaned'`).Scan(&total); err != nil {
		return runworktree.Workspace{}, false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drydock_workspaces
		WHERE common_dir_sha256 = ? AND state <> 'cleaned'`,
		value.Source.CommonDirSHA256).Scan(&repositoryCount); err != nil {
		return runworktree.Workspace{}, false, err
	}
	if total >= runworktree.MaxActiveTotal || repositoryCount >= runworktree.MaxActivePerRepository {
		return runworktree.Workspace{}, false, apperror.New(apperror.CodeResourceExhausted,
			"Drydock active-capacity limit was reached; exact expired ownership must be reconciled first")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces
		(id, name, root_path, created_at) VALUES (?, ?, ?, ?)`, value.WorkspaceID,
		value.Name, value.Path, ts(value.CreatedAt)); err != nil {
		return runworktree.Workspace{}, false, normalizeDrydockStoreError(err)
	}
	source := value.Source
	_, err = tx.ExecContext(ctx, `INSERT INTO drydock_workspaces
		(id, protocol_version, run_id, mission_id, session_id, source_workspace_id,
		 workspace_id, trust_id, source_identity_sha256, root_path, root_path_sha256,
		 source_root_fingerprint, repository_sha256, common_dir_sha256, source_branch,
		 base_commit, object_format, name, path, path_sha256, branch, root_fingerprint,
		 expected_head, expected_binding_fingerprint, create_preview_id,
		 create_git_receipt_id, managed_worktree_id, state, generation, last_checkpoint_id,
		 last_delivery_id, recovery_reason, expires_at, created_at, updated_at, cleaned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '',
		 '', '', ?, '', '', ?, ?, '', '', '', ?, ?, ?, NULL)`,
		value.ID, value.ProtocolVersion, value.RunID, value.MissionID, value.SessionID,
		value.SourceWorkspaceID, value.WorkspaceID, value.TrustID, source.Fingerprint(),
		source.RootPath, source.RootPathSHA256, source.RootFingerprint,
		source.RepositorySHA256, source.CommonDirSHA256, source.Branch,
		source.BaseCommit, source.ObjectFormat, value.Name, value.Path, value.PathSHA256,
		value.Branch, value.CreatePreviewID, value.State, value.Generation,
		ts(value.ExpiresAt), ts(value.CreatedAt), ts(value.UpdatedAt))
	if err != nil {
		return runworktree.Workspace{}, false, normalizeDrydockStoreError(err)
	}
	event, err := events.New(value.RunID, value.MissionID,
		events.DrydockCreatePreparedEvent, "drydock", value.ID, map[string]any{
			"drydock_id": value.ID, "workspace_id": value.WorkspaceID,
			"source_workspace_id":    value.SourceWorkspaceID,
			"source_identity_sha256": source.Fingerprint(), "state": value.State,
			"generation": value.Generation, "expires_at": value.ExpiresAt,
		})
	if err != nil {
		return runworktree.Workspace{}, false, err
	}
	event.CreatedAt = value.CreatedAt
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return runworktree.Workspace{}, false, err
	}
	return value, false, nil
}

func (s *SQLiteStore) AdvanceDrydock(ctx context.Context, value runworktree.Workspace,
	expectedGeneration int64, receipt runworktree.Receipt,
) (runworktree.Workspace, bool, error) {
	if err := value.Validate(); err != nil || receipt.Validate() != nil ||
		receipt.DrydockID != value.ID ||
		receipt.SourceIdentitySHA256 != value.Source.Fingerprint() ||
		receipt.RootFingerprint != value.RootFingerprint ||
		receipt.GenerationBefore != expectedGeneration ||
		receipt.GenerationAfter != value.Generation {
		return runworktree.Workspace{}, false, errors.New("Drydock transition is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return runworktree.Workspace{}, false, err
	}
	defer tx.Rollback()
	advanced, replayed, err := advanceDrydockTx(ctx, tx, value, expectedGeneration, receipt)
	if err != nil || replayed {
		return advanced, replayed, err
	}
	if err := tx.Commit(); err != nil {
		return runworktree.Workspace{}, false, err
	}
	return advanced, false, nil
}

func (s *SQLiteStore) CreateDrydockDelivery(ctx context.Context,
	proposal runworktree.DeliveryProposal, value runworktree.Workspace,
	expectedGeneration int64, receipt runworktree.Receipt,
) (runworktree.DeliveryProposal, runworktree.Workspace, bool, error) {
	if proposal.Validate() != nil || value.Validate() != nil || receipt.Validate() != nil ||
		proposal.DrydockID != value.ID || proposal.RunID != receipt.RunID ||
		proposal.Generation != value.Generation || receipt.Operation != runworktree.OperationDeliver ||
		proposal.OperationKeySHA256 != receipt.OperationKeySHA256 ||
		proposal.RequestFingerprint != receipt.RequestFingerprint ||
		proposal.SourceIdentitySHA256 != value.Source.Fingerprint() ||
		proposal.RootFingerprint != value.RootFingerprint ||
		proposal.BaseCommit != value.BaseCommit || proposal.MergeBaseCommit != value.BaseCommit ||
		proposal.HeadCommit != value.ExpectedHead ||
		proposal.BindingFingerprint != value.ExpectedBindingFingerprint ||
		proposal.CheckpointID != value.LastCheckpointID ||
		receipt.SourceIdentitySHA256 != value.Source.Fingerprint() ||
		receipt.RootFingerprint != value.RootFingerprint ||
		receipt.DeliveryID != proposal.ID || receipt.GenerationBefore != expectedGeneration ||
		receipt.GenerationAfter != value.Generation {
		return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false,
			errors.New("Drydock delivery transition is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false, err
	}
	defer tx.Rollback()
	if existing, found, getErr := getDrydockReceiptByOperation(ctx, tx,
		receipt.OperationKeySHA256); getErr != nil {
		return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false, getErr
	} else if found {
		if !reflect.DeepEqual(existing, receipt) {
			return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false,
				apperror.New(apperror.CodeConflict, "Drydock delivery operation key was reused")
		}
		storedProposal, proposalFound, proposalErr := getDrydockDelivery(ctx, tx, proposal.ID)
		storedWorkspace, workspaceFound, workspaceErr := getDrydockByID(ctx, tx, value.ID)
		if proposalErr != nil || workspaceErr != nil || !proposalFound || !workspaceFound {
			return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false,
				errors.Join(proposalErr, workspaceErr)
		}
		return storedProposal, storedWorkspace, true, nil
	}
	pathsJSON, err := json.Marshal(proposal.ChangedPaths)
	if err != nil {
		return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drydock_delivery_proposals
		(id, protocol_version, operation_key_sha256, request_fingerprint, drydock_id,
		 run_id, generation, source_identity_sha256, root_fingerprint, base_commit,
		 head_commit, merge_base_commit, binding_fingerprint, diff_sha256, diff_bytes,
		 diff_stat, changed_paths_json, checkpoint_id, created_by, automatic_merge,
		 push_authorized, force_authorized, source_overwrite_allowed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, ?)`,
		proposal.ID, proposal.ProtocolVersion, proposal.OperationKeySHA256,
		proposal.RequestFingerprint, proposal.DrydockID, proposal.RunID,
		proposal.Generation, proposal.SourceIdentitySHA256, proposal.RootFingerprint,
		proposal.BaseCommit, proposal.HeadCommit, proposal.MergeBaseCommit,
		proposal.BindingFingerprint, proposal.DiffSHA256, proposal.DiffBytes,
		proposal.DiffStat, string(pathsJSON), proposal.CheckpointID, proposal.CreatedBy,
		ts(proposal.CreatedAt))
	if err != nil {
		return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false,
			normalizeDrydockStoreError(err)
	}
	advanced, replayed, err := advanceDrydockTx(ctx, tx, value, expectedGeneration, receipt)
	if err != nil || replayed {
		return runworktree.DeliveryProposal{}, advanced, replayed, err
	}
	if err := tx.Commit(); err != nil {
		return runworktree.DeliveryProposal{}, runworktree.Workspace{}, false, err
	}
	return proposal, advanced, false, nil
}

func (s *SQLiteStore) GetDrydockByRun(ctx context.Context,
	runID string,
) (runworktree.Workspace, bool, error) {
	return getDrydockByRun(ctx, s.db, strings.TrimSpace(runID))
}

func (s *SQLiteStore) GetDrydock(ctx context.Context,
	id string,
) (runworktree.Workspace, bool, error) {
	return getDrydockByID(ctx, s.db, strings.TrimSpace(id))
}

func (s *SQLiteStore) ListDrydocks(ctx context.Context,
	filter runworktree.ListFilter,
) ([]runworktree.Workspace, error) {
	if filter.Limit < 1 || filter.Limit > runworktree.MaxList ||
		(filter.State != "" && !filter.State.Valid()) ||
		(filter.RepositorySHA256 != "" && !runworktree.ValidDigest(filter.RepositorySHA256)) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Drydock list filter is invalid")
	}
	query := `SELECT ` + drydockWorkspaceColumns + ` FROM drydock_workspaces WHERE 1=1`
	args := []any{}
	if filter.RunID != "" {
		query += ` AND run_id = ?`
		args = append(args, filter.RunID)
	}
	if filter.RepositorySHA256 != "" {
		query += ` AND repository_sha256 = ?`
		args = append(args, filter.RepositorySHA256)
	}
	if filter.State != "" {
		query += ` AND state = ?`
		args = append(args, filter.State)
	}
	if !filter.IncludeCleaned {
		query += ` AND state <> 'cleaned'`
	}
	if filter.ExpiredBefore != nil {
		query += ` AND expires_at <= ?`
		args = append(args, ts(filter.ExpiredBefore.UTC()))
	}
	query += ` ORDER BY created_at, id LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]runworktree.Workspace, 0)
	for rows.Next() {
		value, err := scanDrydock(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) GetDrydockReceiptByOperation(ctx context.Context,
	operationKeySHA256 string,
) (runworktree.Receipt, bool, error) {
	return getDrydockReceiptByOperation(ctx, s.db, strings.TrimSpace(operationKeySHA256))
}

func (s *SQLiteStore) ListDrydockReceipts(ctx context.Context, drydockID string,
	limit int,
) ([]runworktree.Receipt, error) {
	if strings.TrimSpace(drydockID) == "" || limit < 1 || limit > runworktree.MaxList {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Drydock receipt list request is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+drydockReceiptColumns+`
		FROM drydock_lifecycle_receipts WHERE drydock_id = ?
		ORDER BY created_at, id LIMIT ?`, strings.TrimSpace(drydockID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]runworktree.Receipt, 0)
	for rows.Next() {
		value, err := scanDrydockReceipt(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) GetDrydockDelivery(ctx context.Context,
	id string,
) (runworktree.DeliveryProposal, bool, error) {
	return getDrydockDelivery(ctx, s.db, strings.TrimSpace(id))
}

func advanceDrydockTx(ctx context.Context, tx *sql.Tx, value runworktree.Workspace,
	expectedGeneration int64, receipt runworktree.Receipt,
) (runworktree.Workspace, bool, error) {
	if existing, found, err := getDrydockReceiptByOperation(ctx, tx,
		receipt.OperationKeySHA256); err != nil {
		return runworktree.Workspace{}, false, err
	} else if found {
		if !reflect.DeepEqual(existing, receipt) {
			return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
				"Drydock lifecycle operation key was reused")
		}
		stored, storedFound, storedErr := getDrydockByID(ctx, tx, value.ID)
		return stored, storedFound, storedErr
	}
	current, found, err := getDrydockByID(ctx, tx, value.ID)
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "Drydock was not found")
		}
		return runworktree.Workspace{}, false, err
	}
	if !sameDrydockIdentity(current, value) || current.Generation != expectedGeneration {
		return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
			"Drydock ownership generation or identity changed")
	}
	hasBindings, bindingErr := hasRunFileDrydockBindings(ctx, tx)
	if bindingErr != nil {
		return runworktree.Workspace{}, false, bindingErr
	}
	if hasBindings {
		bound, present, err := getRunFileDrydock(ctx, tx, receipt.RunID)
		if err != nil {
			return runworktree.Workspace{}, false, err
		}
		if !present || bound.ID != value.ID {
			return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict, "Drydock receipt does not belong to this execution context")
		}
		if err := requireCurrentRunDrydockTx(ctx, tx, receipt.RunID); err != nil {
			return runworktree.Workspace{}, false, err
		}
		var cleaning bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM drydock_cleanup_operations cleanup
			WHERE cleanup.drydock_id=? AND cleanup.status='prepared' AND NOT (
			?='cleanup' AND cleanup.operation_key_sha256=? AND cleanup.request_fingerprint=?
			AND cleanup.run_id=? AND cleanup.expected_generation=?))`, value.ID, receipt.Operation,
			receipt.OperationKeySHA256, receipt.RequestFingerprint, receipt.RunID, expectedGeneration).Scan(&cleaning); err != nil {
			return runworktree.Workspace{}, false, err
		}
		if cleaning {
			return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
				"Thread working directory cleanup must be confirmed before changing its lifecycle")
		}
	} else if receipt.RunID != value.RunID {
		return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict, "Drydock receipt owner differs")
	}
	if value.State == runworktree.StateCleaned {
		cleanup := receipt.Operation == runworktree.OperationCleanup &&
			receipt.Outcome == runworktree.OutcomeSucceeded
		unmaterializedCreateFailure := current.State == runworktree.StatePreparing &&
			receipt.Operation == runworktree.OperationCreate &&
			receipt.Outcome == runworktree.OutcomeFailed
		if !cleanup && !unmaterializedCreateFailure {
			return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
				"Drydock cleaned transition lacks an exact cleanup receipt")
		}
	}
	if current.State == runworktree.StateRecoveryRequired && value.State == runworktree.StateReady &&
		(receipt.Operation != runworktree.OperationRecover ||
			receipt.Outcome != runworktree.OutcomeSucceeded) {
		return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
			"Drydock recovery transition lacks a successful recovery receipt")
	}
	cleanedAt := any(nil)
	if value.CleanedAt != nil {
		cleanedAt = ts(value.CleanedAt.UTC())
	}
	result, err := tx.ExecContext(ctx, `UPDATE drydock_workspaces SET
		root_fingerprint = ?, expected_head = ?, expected_binding_fingerprint = ?,
		create_git_receipt_id = ?, managed_worktree_id = ?, state = ?, generation = ?, last_checkpoint_id = ?,
		last_delivery_id = ?, recovery_reason = ?, updated_at = ?, cleaned_at = ?
		WHERE id = ? AND generation = ?`, value.RootFingerprint, value.ExpectedHead,
		value.ExpectedBindingFingerprint, value.CreateGitReceiptID, value.ManagedWorktreeID, value.State,
		value.Generation, value.LastCheckpointID, value.LastDeliveryID,
		value.RecoveryReason, ts(value.UpdatedAt), cleanedAt, value.ID,
		expectedGeneration)
	if err != nil {
		return runworktree.Workspace{}, false, normalizeDrydockStoreError(err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return runworktree.Workspace{}, false, rowsErr
		}
		return runworktree.Workspace{}, false, apperror.New(apperror.CodeConflict,
			"Drydock ownership generation changed")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drydock_lifecycle_receipts
		(id, protocol_version, operation_key_sha256, request_fingerprint, drydock_id,
		 run_id, operation, outcome, generation_before, generation_after,
		 source_identity_sha256, root_fingerprint, binding_before_sha256,
		 binding_after_sha256, git_receipt_id, checkpoint_id, delivery_id,
		 reason_code, summary, grants_process_authority, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		receipt.ID, receipt.ProtocolVersion, receipt.OperationKeySHA256,
		receipt.RequestFingerprint, receipt.DrydockID, receipt.RunID,
		receipt.Operation, receipt.Outcome, receipt.GenerationBefore,
		receipt.GenerationAfter, receipt.SourceIdentitySHA256,
		receipt.RootFingerprint, receipt.BindingBeforeSHA256,
		receipt.BindingAfterSHA256, receipt.GitReceiptID, receipt.CheckpointID,
		receipt.DeliveryID, receipt.ReasonCode, receipt.Summary, ts(receipt.CreatedAt))
	if err != nil {
		return runworktree.Workspace{}, false, normalizeDrydockStoreError(err)
	}
	event, err := drydockReceiptEvent(value, receipt)
	if err != nil {
		return runworktree.Workspace{}, false, err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return runworktree.Workspace{}, false, err
	}
	return value, false, nil
}

func drydockReceiptEvent(value runworktree.Workspace,
	receipt runworktree.Receipt,
) (events.Event, error) {
	eventType := events.DrydockRecoveryRequiredEvent
	if receipt.Operation == runworktree.OperationCreate &&
		receipt.Outcome == runworktree.OutcomeFailed && value.State == runworktree.StateCleaned {
		eventType = events.DrydockCreateFailedEvent
	}
	if receipt.Outcome == runworktree.OutcomeSucceeded {
		switch receipt.Operation {
		case runworktree.OperationCreate:
			eventType = events.DrydockCreatedEvent
		case runworktree.OperationUse:
			eventType = events.DrydockUseAttestedEvent
		case runworktree.OperationCheckpoint:
			eventType = events.DrydockCheckpointRecordedEvent
		case runworktree.OperationRewind:
			eventType = events.DrydockRewindCompletedEvent
		case runworktree.OperationUndo:
			eventType = events.DrydockUndoCompletedEvent
		case runworktree.OperationFork:
			eventType = events.DrydockForkPreparedEvent
		case runworktree.OperationDeliver:
			eventType = events.DrydockDeliveryProposedEvent
		case runworktree.OperationCleanup:
			eventType = events.DrydockCleanupCompletedEvent
		case runworktree.OperationRecover:
			eventType = events.DrydockRecoveredEvent
		}
	}
	event, err := events.New(receipt.RunID, value.MissionID, eventType, "drydock",
		receipt.ID, map[string]any{
			"receipt_id": receipt.ID, "drydock_id": value.ID,
			"workspace_id": value.WorkspaceID, "operation": receipt.Operation,
			"outcome": receipt.Outcome, "state": value.State,
			"generation": value.Generation, "checkpoint_id": receipt.CheckpointID,
			"delivery_id": receipt.DeliveryID, "git_receipt_id": receipt.GitReceiptID,
			"reason_code": receipt.ReasonCode, "grants_process_authority": false,
		})
	if err != nil {
		return events.Event{}, err
	}
	event.CreatedAt = receipt.CreatedAt
	return event, nil
}

func getDrydockTrustByRun(ctx context.Context, queryer drydockQueryer,
	runID string,
) (runworktree.Trust, bool, error) {
	row := queryer.QueryRowContext(ctx, `SELECT id, protocol_version, run_id,
		workspace_id, root_path, root_path_sha256, root_fingerprint,
		repository_sha256, common_dir_sha256, branch, base_commit, object_format,
		index_sha256, worktree_sha256, status_sha256, dirty_tracked,
		source_captured_at, dirty_untracked, dirty_ignored, symlink_entries, submodule_entries,
		confirmed_by, grants_process_authority, confirmed_at
		FROM drydock_workspace_trust WHERE run_id = ?`, runID)
	var value runworktree.Trust
	var dirtyTracked, dirtyUntracked, dirtyIgnored, authority int
	var sourceCapturedAt, confirmedAt string
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.RunID, &value.WorkspaceID,
		&value.Source.RootPath, &value.Source.RootPathSHA256,
		&value.Source.RootFingerprint, &value.Source.RepositorySHA256,
		&value.Source.CommonDirSHA256, &value.Source.Branch, &value.Source.BaseCommit,
		&value.Source.ObjectFormat, &value.SourceState.IndexSHA256,
		&value.SourceState.WorktreeSHA256, &value.SourceState.StatusSHA256,
		&dirtyTracked, &sourceCapturedAt, &dirtyUntracked, &dirtyIgnored,
		&value.SourceState.SymlinkEntries, &value.SourceState.SubmoduleEntries,
		&value.ConfirmedBy, &authority, &confirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runworktree.Trust{}, false, nil
	}
	if err != nil {
		return runworktree.Trust{}, false, err
	}
	value.Source.WorkspaceID = value.WorkspaceID
	value.SourceState.DirtyTracked = dirtyTracked != 0
	value.SourceState.DirtyUntracked = dirtyUntracked != 0
	value.SourceState.DirtyIgnored = dirtyIgnored != 0
	value.GrantsProcessAuthority = authority != 0
	value.SourceState.CapturedAt = parseTS(sourceCapturedAt)
	value.ConfirmedAt = parseTS(confirmedAt)
	if value.Validate() != nil {
		return runworktree.Trust{}, false,
			errors.New("stored Drydock Workspace Trust receipt is invalid")
	}
	return value, true, nil
}

func getDrydockByRun(ctx context.Context, queryer drydockQueryer,
	runID string,
) (runworktree.Workspace, bool, error) {
	return scanDrydockRow(queryer.QueryRowContext(ctx, `SELECT `+
		drydockWorkspaceColumns+` FROM drydock_workspaces WHERE run_id = ?`, runID))
}

func getDrydockByID(ctx context.Context, queryer drydockQueryer,
	id string,
) (runworktree.Workspace, bool, error) {
	return scanDrydockRow(queryer.QueryRowContext(ctx, `SELECT `+
		drydockWorkspaceColumns+` FROM drydock_workspaces WHERE id = ?`, id))
}

type drydockScanner interface{ Scan(...any) error }

func scanDrydockRow(row drydockScanner) (runworktree.Workspace, bool, error) {
	value, err := scanDrydock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return runworktree.Workspace{}, false, nil
	}
	return value, err == nil, err
}

func scanDrydock(row drydockScanner) (runworktree.Workspace, error) {
	var value runworktree.Workspace
	var sourceIdentitySHA string
	var expiresAt, createdAt, updatedAt string
	var cleanedAt sql.NullString
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.RunID, &value.MissionID,
		&value.SessionID, &value.SourceWorkspaceID, &value.WorkspaceID, &value.TrustID,
		&sourceIdentitySHA, &value.Source.RootPath, &value.Source.RootPathSHA256,
		&value.Source.RootFingerprint, &value.Source.RepositorySHA256,
		&value.Source.CommonDirSHA256, &value.Source.Branch, &value.Source.BaseCommit,
		&value.Source.ObjectFormat, &value.Name, &value.Path, &value.PathSHA256,
		&value.Branch, &value.RootFingerprint, &value.ExpectedHead,
		&value.ExpectedBindingFingerprint, &value.CreatePreviewID,
		&value.CreateGitReceiptID, &value.ManagedWorktreeID, &value.State, &value.Generation,
		&value.LastCheckpointID, &value.LastDeliveryID, &value.RecoveryReason,
		&expiresAt, &createdAt, &updatedAt, &cleanedAt)
	if err != nil {
		return runworktree.Workspace{}, err
	}
	value.Source.WorkspaceID = value.SourceWorkspaceID
	value.BaseCommit = value.Source.BaseCommit
	if value.Source.Fingerprint() != sourceIdentitySHA {
		return runworktree.Workspace{}, errors.New("stored Drydock source identity digest changed")
	}
	value.ExpiresAt = parseTS(expiresAt)
	value.CreatedAt = parseTS(createdAt)
	value.UpdatedAt = parseTS(updatedAt)
	if cleanedAt.Valid {
		parsed := parseTS(cleanedAt.String)
		value.CleanedAt = &parsed
	}
	if err := value.Validate(); err != nil {
		return runworktree.Workspace{}, err
	}
	return value, nil
}

func getDrydockReceiptByOperation(ctx context.Context, queryer drydockQueryer,
	digest string,
) (runworktree.Receipt, bool, error) {
	return scanDrydockReceiptRow(queryer.QueryRowContext(ctx, `SELECT `+
		drydockReceiptColumns+` FROM drydock_lifecycle_receipts
		WHERE operation_key_sha256 = ?`, digest))
}

func scanDrydockReceiptRow(row drydockScanner) (runworktree.Receipt, bool, error) {
	value, err := scanDrydockReceipt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return runworktree.Receipt{}, false, nil
	}
	return value, err == nil, err
}

func scanDrydockReceipt(row drydockScanner) (runworktree.Receipt, error) {
	var value runworktree.Receipt
	var authority int
	var createdAt string
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.OperationKeySHA256,
		&value.RequestFingerprint, &value.DrydockID, &value.RunID, &value.Operation,
		&value.Outcome, &value.GenerationBefore, &value.GenerationAfter,
		&value.SourceIdentitySHA256, &value.RootFingerprint,
		&value.BindingBeforeSHA256, &value.BindingAfterSHA256, &value.GitReceiptID,
		&value.CheckpointID, &value.DeliveryID, &value.ReasonCode, &value.Summary,
		&authority, &createdAt)
	if err != nil {
		return runworktree.Receipt{}, err
	}
	value.GrantsProcessAuthority = authority != 0
	value.CreatedAt = parseTS(createdAt)
	if value.Validate() != nil {
		return runworktree.Receipt{},
			errors.New("stored Drydock lifecycle receipt is invalid")
	}
	return value, nil
}

func getDrydockDelivery(ctx context.Context, queryer drydockQueryer,
	id string,
) (runworktree.DeliveryProposal, bool, error) {
	row := queryer.QueryRowContext(ctx, `SELECT id, protocol_version,
		operation_key_sha256, request_fingerprint, drydock_id, run_id, generation,
		source_identity_sha256, root_fingerprint, base_commit, head_commit,
		merge_base_commit, binding_fingerprint, diff_sha256, diff_bytes, diff_stat,
		changed_paths_json, checkpoint_id, created_by, automatic_merge,
		push_authorized, force_authorized, source_overwrite_allowed, created_at
		FROM drydock_delivery_proposals WHERE id = ?`, id)
	var value runworktree.DeliveryProposal
	var changedPathsJSON, createdAt string
	var automaticMerge, push, force, overwrite int
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.OperationKeySHA256,
		&value.RequestFingerprint, &value.DrydockID, &value.RunID, &value.Generation,
		&value.SourceIdentitySHA256, &value.RootFingerprint, &value.BaseCommit,
		&value.HeadCommit, &value.MergeBaseCommit, &value.BindingFingerprint,
		&value.DiffSHA256, &value.DiffBytes, &value.DiffStat, &changedPathsJSON,
		&value.CheckpointID, &value.CreatedBy, &automaticMerge, &push, &force,
		&overwrite, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runworktree.DeliveryProposal{}, false, nil
	}
	if err != nil {
		return runworktree.DeliveryProposal{}, false, err
	}
	if err := json.Unmarshal([]byte(changedPathsJSON), &value.ChangedPaths); err != nil {
		return runworktree.DeliveryProposal{}, false, err
	}
	value.AutomaticMerge = automaticMerge != 0
	value.PushAuthorized = push != 0
	value.ForceAuthorized = force != 0
	value.SourceOverwriteAllowed = overwrite != 0
	value.CreatedAt = parseTS(createdAt)
	if value.Validate() != nil {
		return runworktree.DeliveryProposal{}, false,
			errors.New("stored Drydock delivery proposal is invalid")
	}
	return value, true, nil
}

func sameDrydockIdentity(left, right runworktree.Workspace) bool {
	return left.ID == right.ID && left.RunID == right.RunID &&
		left.MissionID == right.MissionID && left.SessionID == right.SessionID &&
		left.SourceWorkspaceID == right.SourceWorkspaceID &&
		left.WorkspaceID == right.WorkspaceID && left.TrustID == right.TrustID &&
		reflect.DeepEqual(left.Source, right.Source) && left.Name == right.Name &&
		left.Path == right.Path && left.PathSHA256 == right.PathSHA256 &&
		left.Branch == right.Branch && left.BaseCommit == right.BaseCommit &&
		left.CreatePreviewID == right.CreatePreviewID && left.ExpiresAt.Equal(right.ExpiresAt) &&
		left.CreatedAt.Equal(right.CreatedAt)
}

func drydockMissionIDTx(ctx context.Context, tx *sql.Tx, runID string) string {
	var missionID string
	_ = tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`, runID).Scan(&missionID)
	return missionID
}

func normalizeDrydockStoreError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique") || strings.Contains(message, "constraint") ||
		strings.Contains(message, "immutable") || strings.Contains(message, "generation") ||
		strings.Contains(message, "transition") {
		return apperror.Wrap(apperror.CodeConflict, "Drydock durable state conflict", err)
	}
	return fmt.Errorf("persist Drydock state: %w", err)
}
