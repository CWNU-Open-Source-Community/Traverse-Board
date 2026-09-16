package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
)

type threadPlanSuccessorBinding struct {
	keyDigest, requestedBy string
	phase                  domain.ExecutionPhase
}

func requireUnusedThreadPlanSuccessorKeyTx(ctx context.Context, tx planDeliveryQueryer, threadID, key string) error {
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_events WHERE thread_id=? AND type='thread.run_successor_created' AND source='thread_continuation' AND json_extract(payload_json,'$.plan_control_operation_digest')=?`, threadID, key).Scan(&used); err != nil {
		return err
	}
	if used != 0 {
		return apperror.New(apperror.CodeConflict, "Thread Plan key already created a mode successor")
	}
	return nil
}

func (s *SQLiteStore) GetThreadPlanInitialMode(ctx context.Context, runID string) (domain.RunModeSnapshot, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM run_mode_snapshots WHERE run_id=? AND revision=1`, runID).Scan(&id); err != nil {
		return domain.RunModeSnapshot{}, err
	}
	return s.GetRunModeSnapshot(ctx, id)
}

func planSuccessorBinding(threadID, runID, key, by string, phase domain.ExecutionPhase) threadPlanSuccessorBinding {
	return threadPlanSuccessorBinding{keyDigest: runmutation.Fingerprint("plan_delivery_control.v1", "thread-operation", threadID, key), requestedBy: by, phase: phase}
}

func (s *SQLiteStore) EnsureThreadPlanSuccessor(ctx context.Context, threadID, previousID, key, by string, mission domain.Mission, candidate domain.Run, mode domain.RunModeSnapshot, linked session.Session, initial []events.Event, files *domain.ThreadFileContinuation) (domain.Thread, domain.Run, bool, error) {
	if !domain.ValidAgentID(threadID) || !domain.ValidAgentID(previousID) || !domain.ValidAgentID(by) || (mode.Phase != domain.ExecutionPhasePlan && mode.Phase != domain.ExecutionPhaseDeliver) {
		return domain.Thread{}, domain.Run{}, false, apperror.New(apperror.CodeInvalidArgument, "Thread mode successor identity is invalid")
	}
	if _, err := domain.NormalizeAgentOperationKey(key); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	binding := planSuccessorBinding(threadID, previousID, key, by, mode.Phase)
	return s.ensureThreadSuccessor(ctx, threadID, previousID, mission, candidate, mode, linked, initial, nil, files, &binding)
}

func (s *SQLiteStore) GetThreadPlanSuccessor(ctx context.Context, threadID, runID, key, by string, phase domain.ExecutionPhase) (domain.Run, bool, error) {
	tx, finish, err := s.beginThreadRequestObservation(ctx)
	if err != nil {
		return domain.Run{}, false, err
	}
	defer finish()
	return readThreadPlanSuccessorTx(ctx, tx, threadID, runID, planSuccessorBinding(threadID, runID, key, by, phase))
}

func readThreadPlanSuccessorTx(ctx context.Context, tx planDeliveryQueryer, threadID, runID string, binding threadPlanSuccessorBinding) (domain.Run, bool, error) {
	var target, requester, phase, source string
	err := tx.QueryRowContext(ctx, `SELECT run_id,json_extract(payload_json,'$.plan_control_requested_by'),json_extract(payload_json,'$.plan_control_phase'),json_extract(payload_json,'$.predecessor_run_id') FROM thread_events WHERE thread_id=? AND type='thread.run_successor_created' AND source='thread_continuation' AND json_extract(payload_json,'$.plan_control_operation_digest')=?`, threadID, binding.keyDigest).Scan(&target, &requester, &phase, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, err
	}
	if requester != binding.requestedBy || phase != string(binding.phase) || source != runID {
		return domain.Run{}, false, apperror.New(apperror.CodeConflict, "Thread mode request key belongs to another operation")
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id,mission_id,session_id,status,config_json,budget_json,started_at,finished_at,created_at,updated_at FROM runs WHERE id=?`, target))
	if err != nil {
		return domain.Run{}, false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_runs WHERE thread_id=? AND run_id=? AND predecessor_run_id=?`, threadID, target, runID).Scan(&count); err != nil {
		return domain.Run{}, false, err
	}
	if count != 1 {
		return domain.Run{}, false, apperror.New(apperror.CodeConflict, "Thread mode successor binding is inconsistent")
	}
	return run, true, nil
}

func prepareThreadPlanSuccessorTx(ctx context.Context, tx *sql.Tx, threadID, runID string, mode domain.RunModeSnapshot, files *domain.ThreadFileContinuation, binding threadPlanSuccessorBinding) error {
	if err := acquireRunModeWriteLockTx(ctx, tx, runID); err != nil {
		return err
	}
	if _, found, err := readThreadPlanSuccessorTx(ctx, tx, threadID, runID, binding); err != nil || found {
		return err
	}
	if _, found, err := getRunModeOperation(ctx, tx, binding.keyDigest); err != nil {
		return err
	} else if found {
		return apperror.New(apperror.CodeConflict, "Thread Plan key already changed a mode")
	}
	if _, found, err := getPlanDeliverySelectionOperation(ctx, tx, binding.keyDigest); err != nil {
		return err
	} else if found {
		return apperror.New(apperror.CodeConflict, "Thread Plan key already confirmed a plan")
	}
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, threadID))
	if err != nil {
		return err
	}
	if thread.Status != domain.ThreadActive || thread.LastRunID != runID || (thread.ActiveRunID != "" && thread.ActiveRunID != runID) {
		return apperror.New(apperror.CodeConflict, "Thread changed before explicit mode continuation")
	}
	run, err := getRunControlRunTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if !run.Terminal() && run.Status != domain.RunCreated && run.Status != domain.RunRunning && run.Status != domain.RunPaused {
		return apperror.New(apperror.CodeFailedPrecondition, "Thread mode continuation requires an idle execution without a pending approval gate")
	}
	if mode.Surface != domain.ExecutionSurfaceCode || mode.Phase != binding.phase {
		return apperror.New(apperror.CodeConflict, "Thread mode continuation target differs")
	}
	if err := requireThreadLifecycleRunQuiescentTx(ctx, tx, runID, time.Now().UTC()); err != nil {
		return err
	}
	if err := requireNoOpenWorkspaceRestoreTx(ctx, tx, runID); err != nil {
		return err
	}
	if err := requireThreadToolEffectsSettledTx(ctx, tx, runID); err != nil {
		return err
	}
	if err := requireApprovalEffectsSettledTx(ctx, tx, runID); err != nil {
		return err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_steering_messages WHERE run_id=? AND status='pending'`, runID).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return apperror.New(apperror.CodeConflict, "Thread mode continuation requires earlier inputs to settle")
	}
	if files != nil && files.ThreadVersion != thread.Version {
		return apperror.New(apperror.CodeConflict, "Thread working directory preparation became stale")
	}
	if !run.Terminal() {
		if err := transitionThreadLifecycleRunTx(ctx, tx, &run, domain.RunCancelled, "operator explicitly changed planning mode; previous plan remains historical", time.Now().UTC()); err != nil {
			return err
		}
		if files != nil {
			current, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, threadID))
			if err != nil {
				return err
			}
			files.ThreadVersion = current.Version
		}
	}
	return nil
}
