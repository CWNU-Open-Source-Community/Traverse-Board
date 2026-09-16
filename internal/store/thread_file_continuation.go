package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

func (s *SQLiteStore) GetThreadDrydockBinding(ctx context.Context, runID string) (domain.ThreadDrydockBinding, bool, error) {
	var b domain.ThreadDrydockBinding
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,thread_id,drydock_id,predecessor_run_id,created_at FROM thread_drydock_bindings WHERE run_id=?`, runID).Scan(&b.RunID, &b.ThreadID, &b.DrydockID, &b.PredecessorRunID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	b.CreatedAt = parseTS(created)
	return b, err == nil, err
}

func getRunFileDrydock(ctx context.Context, q drydockQueryer, runID string) (drydock.Workspace, bool, error) {
	var hasView bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='view' AND name='run_file_drydock_bindings')`).Scan(&hasView); err != nil {
		return drydock.Workspace{}, false, err
	}
	if !hasView {
		var hasTable bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='drydock_workspaces')`).Scan(&hasTable); err != nil {
			return drydock.Workspace{}, false, err
		}
		if !hasTable {
			return drydock.Workspace{}, false, nil
		}
		return getDrydockByRun(ctx, q, runID)
	}

	columns := strings.Split(drydockWorkspaceColumns, ",")
	for i := range columns {
		columns[i] = "d." + strings.TrimSpace(columns[i])
	}
	d, err := scanDrydock(q.QueryRowContext(ctx, `SELECT `+strings.Join(columns, ",")+` FROM run_file_drydock_bindings binding JOIN drydock_workspaces d ON d.id=binding.drydock_id WHERE binding.run_id=?`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}
func (s *SQLiteStore) GetRunFileDrydock(ctx context.Context, runID string) (drydock.Workspace, bool, error) {
	return getRunFileDrydock(ctx, s.db, runID)
}

func getDrydockHolder(ctx context.Context, q drydockQueryer, drydockID string) (domain.ThreadDrydockBinding, bool, error) {
	var b domain.ThreadDrydockBinding
	var created string
	err := q.QueryRowContext(ctx, `SELECT binding.run_id,binding.thread_id,binding.drydock_id,COALESCE(tr.predecessor_run_id,''),COALESCE(epoch.created_at,r.created_at)
  FROM run_file_drydock_bindings binding JOIN runs r ON r.id=binding.run_id LEFT JOIN threads thread ON thread.id=binding.thread_id
  LEFT JOIN thread_runs tr ON tr.run_id=binding.run_id LEFT JOIN thread_drydock_bindings epoch ON epoch.run_id=binding.run_id
  WHERE binding.drydock_id=? AND (binding.thread_id='' OR thread.last_run_id=binding.run_id)`, drydockID).Scan(&b.RunID, &b.ThreadID, &b.DrydockID, &b.PredecessorRunID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	b.CreatedAt = parseTS(created)
	return b, err == nil, err
}
func (s *SQLiteStore) GetDrydockHolder(ctx context.Context, drydockID string) (domain.ThreadDrydockBinding, bool, error) {
	return getDrydockHolder(ctx, s.db, drydockID)
}
func (s *SQLiteStore) RunOwnsCurrentDrydock(ctx context.Context, runID, drydockID string) (bool, error) {
	b, found, err := getDrydockHolder(ctx, s.db, drydockID)
	return found && b.RunID == runID, err
}

func (s *SQLiteStore) HasPendingDrydockCleanup(ctx context.Context, drydockID string) (bool, error) {
	var pending bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM drydock_cleanup_operations WHERE drydock_id=? AND status='prepared')`, drydockID).Scan(&pending)
	return pending, err
}

func (s *SQLiteStore) EnsureThreadSuccessorWithFiles(ctx context.Context, request domain.ThreadMessageIntentRequest, predecessorID string, mission domain.Mission, candidate domain.Run, mode domain.RunModeSnapshot, linked session.Session, initial []events.Event, files domain.ThreadFileContinuation) (domain.Thread, domain.Run, bool, error) {
	return s.ensureThreadSuccessor(ctx, request.ThreadID, predecessorID, mission, candidate, mode, linked, initial, &request, &files, nil)
}

func validateThreadFilePreparationTx(ctx context.Context, tx *sql.Tx, p domain.ThreadFileContinuation) error {
	if err := lockRunControlTx(ctx, tx, p.Binding.PredecessorRunID); err != nil {
		return err
	}
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, p.Binding.ThreadID))
	if err != nil {
		return err
	}
	if thread.Status != domain.ThreadActive || thread.ActiveRunID != "" || thread.LastRunID != p.Binding.PredecessorRunID || thread.Version != p.ThreadVersion {
		return apperror.New(apperror.CodeConflict, "Thread changed before working directory continuation")
	}
	old, found, err := getRunFileDrydock(ctx, tx, p.Binding.PredecessorRunID)
	if err != nil {
		return err
	}
	if !found || !reflect.DeepEqual(old, p.Workspace) {
		return apperror.New(apperror.CodeConflict, "Thread working directory ownership changed before continuation")
	}
	holder, found, err := getDrydockHolder(ctx, tx, p.Workspace.ID)
	if err != nil {
		return err
	}
	if !found || holder.RunID != p.Binding.PredecessorRunID || holder.ThreadID != thread.ID {
		return apperror.New(apperror.CodeConflict, "Previous epoch no longer holds this Thread working directory")
	}
	permission, err := getCurrentThreadExecutionPermissionSnapshot(ctx, tx, thread.ID)
	if err != nil {
		return err
	}
	if permission.ID != p.PermissionSnapshotID || permission.Revision != p.PermissionRevision {
		return apperror.New(apperror.CodeConflict, "Thread permission changed before continuation")
	}
	var modelJSON string
	modelErr := tx.QueryRowContext(ctx, `SELECT value FROM provider_setting WHERE key=?`, threadModelRoutePreferenceKeyPrefix+thread.ID).Scan(&modelJSON)
	if errors.Is(modelErr, sql.ErrNoRows) {
		if p.ModelPreference != nil {
			return apperror.New(apperror.CodeConflict, "Thread model changed before continuation")
		}
	} else if modelErr != nil {
		return modelErr
	} else {
		var pref domain.ThreadModelRoutePreference
		if json.Unmarshal([]byte(modelJSON), &pref) != nil || p.ModelPreference == nil || !reflect.DeepEqual(pref, *p.ModelPreference) {
			return apperror.New(apperror.CodeConflict, "Thread model changed before continuation")
		}
	}
	var open bool

	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM drydock_cleanup_operations WHERE drydock_id=? AND status='prepared')`, p.Workspace.ID).Scan(&open); err != nil {
		return err
	}
	if open {
		return apperror.New(apperror.CodeConflict, "Working directory cleanup is pending; confirm the original cleanup before continuing")
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_execution_leases lease JOIN run_file_drydock_bindings owner ON owner.run_id=lease.run_id WHERE owner.drydock_id=? AND lease.status='active' AND julianday(lease.expires_at)>julianday(?))`, p.Workspace.ID, ts(time.Now().UTC())).Scan(&open); err != nil {
		return err
	}
	if open {
		return apperror.New(apperror.CodeConflict, "Working directory still has an execution lease")
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_checkpoint_transactions pending JOIN run_file_drydock_bindings owner ON owner.run_id=pending.run_id WHERE owner.drydock_id=? AND pending.status IN ('prepared','applying'))`, p.Workspace.ID).Scan(&open); err != nil {
		return err
	}
	if open {
		return apperror.New(apperror.CodeConflict, "Working directory still has an unfinished mutation")
	}
	return nil
}

func commitThreadFileContinuationTx(ctx context.Context, tx *sql.Tx, predecessor, candidate domain.Run, mode domain.RunModeSnapshot, p domain.ThreadFileContinuation) error {
	if p.Binding.RunID != candidate.ID || p.Binding.PredecessorRunID != predecessor.ID || p.Binding.DrydockID != p.Workspace.ID {
		return errors.New("Thread working directory publication binding is invalid")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_drydock_bindings(run_id,thread_id,drydock_id,predecessor_run_id,created_at) VALUES(?,?,?,?,?)`, candidate.ID, p.Binding.ThreadID, p.Workspace.ID, predecessor.ID, ts(time.Now().UTC())); err != nil {
		return err
	}
	event, err := events.New(candidate.ID, candidate.MissionID, "thread.working_directory_continued", "thread_continuation", p.Workspace.ID, map[string]any{"predecessor_run_id": predecessor.ID, "physical_owner_run_id": p.Workspace.RunID, "workspace_id": p.Workspace.WorkspaceID, "generation": p.Workspace.Generation, "files_copied": false, "execution_authority_inherited": false, "verification_result_inherited": false})
	if err != nil {
		return err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	if p.Preset.Status == domain.StandardCodePresetConfigured {
		return continueThreadStandardCodeTx(ctx, tx, predecessor, candidate, mode, p.Preset, p.Workspace, candidate.CreatedAt)
	}
	return nil
}
