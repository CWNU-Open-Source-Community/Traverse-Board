package store

import (
	"context"
	"database/sql"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
)

// The caller already owns the write transaction for selection or phase change.
// No authority is added: only an idle current execution may enter/confirm Plan.
func requireThreadPlanPreparationTx(ctx context.Context, tx *sql.Tx, threadID, runID, proposalID string) error {
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, threadID))
	if err != nil {
		return err
	}
	if thread.Status != domain.ThreadActive || thread.ActiveRunID != runID {
		return apperror.New(apperror.CodeConflict, "Plan control requires the current active Thread execution")
	}
	run, _, err := getCoordinatorRunTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if run.Status != domain.RunCreated && run.Status != domain.RunRunning && run.Status != domain.RunPaused {
		return apperror.New(apperror.CodeFailedPrecondition, "Plan control requires an idle execution")
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
	if err := tx.QueryRowContext(ctx, `SELECT
	 (SELECT COUNT(*) FROM operator_steering_messages WHERE run_id=? AND status='pending') +
	 (SELECT COUNT(*) FROM operator_steering_deliveries WHERE run_id=? AND status='prepared')`, runID, runID).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return apperror.New(apperror.CodeConflict, "Plan control requires all earlier user inputs to settle")
	}
	if err := requireLatestThreadPlanTx(ctx, tx, runID, proposalID); err != nil {
		return err
	}
	if run.Status == domain.RunRunning {
		return transitionSupervisorRunTx(ctx, tx, &run, domain.RunPaused, "operator preparing Thread Plan control", time.Now().UTC())
	}
	return nil
}

func requireLatestThreadPlanTx(ctx context.Context, tx planDeliveryQueryer, runID, proposalID string) error {
	if proposalID != "" {
		proposal, err := getPlanDeliveryProposal(ctx, tx, proposalID)
		if err != nil {
			return err
		}
		var latest string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM plan_delivery_proposals WHERE run_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, runID).Scan(&latest); err != nil {
			return err
		}
		if proposal.RunID != runID || latest != proposalID {
			return apperror.New(apperror.CodeConflict, "The Plan proposal was replaced; review the latest proposal")
		}
		mode, err := getCurrentRunModeSnapshot(ctx, tx, runID)
		if err != nil {
			return err
		}
		validRevision := mode.Phase == domain.ExecutionPhasePlan && mode.Revision == proposal.ModeRevision
		if mode.Phase == domain.ExecutionPhaseDeliver && mode.Revision == proposal.ModeRevision+1 {
			selection, selected, err := getPlanDeliverySelectionByRun(ctx, tx, runID)
			if err != nil {
				return err
			}
			validRevision = selected && selection.ProposalID == proposal.ID
		}
		if !validRevision {
			return apperror.New(apperror.CodeConflict, "The Plan mode revision changed; review a new proposal")
		}
		var newerInput int
		// A correction may have been queued before the proposal was written
		// but consumed afterwards. Bind to delivered input order, not wall time
		// of enqueue; repeated internal segments of the same input do not count
		// as a new operator requirement.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_steering_messages WHERE run_id=? AND sequence >
		 (SELECT COALESCE(MAX(message.sequence),0) FROM operator_steering_deliveries delivery
		 JOIN operator_steering_messages message ON message.id=delivery.message_id AND message.run_id=delivery.run_id
		 WHERE delivery.run_id=? AND delivery.prepared_at<=?)`, runID, runID, ts(proposal.CreatedAt)).Scan(&newerInput); err != nil {
			return err
		}
		if newerInput != 0 {
			return apperror.New(apperror.CodeConflict, "User requirements changed after this Plan; request and review an updated proposal")
		}
	}
	return nil
}

func requireThreadPlanMessageTx(ctx context.Context, tx *sql.Tx, request domain.ThreadMessageIntentRequest, runID, proposalID, selectionKey string) error {
	if err := requireLatestThreadPlanTx(ctx, tx, runID, proposalID); err != nil {
		return err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, runID)
	if err != nil {
		return err
	}
	selection, found, err := getPlanDeliverySelectionByRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	op, exists, err := getPlanDeliverySelectionOperation(ctx, tx, runmutation.Fingerprint("plan_delivery_control.v1", "thread-operation", request.ThreadID, selectionKey))
	if err != nil {
		return err
	}
	if !found || !exists || selection.ProposalID != proposalID || op.SelectionID != selection.ID || op.RequestedBy != request.RequestedBy || mode.Phase != domain.ExecutionPhaseDeliver {
		return apperror.New(apperror.CodeConflict, "Thread confirmation no longer matches its selected Plan and delivery phase")
	}
	return nil
}

// A permanent stale-plan decision is derived from immutable proposals/input
// order and current Thread binding. Busy/lease conditions are not rejection.
func (s *SQLiteStore) ThreadPlanConfirmationStale(ctx context.Context, threadID, runID, proposalID string) (bool, error) {
	tx, finish, err := s.beginThreadRequestObservation(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, threadID))
	if err != nil {
		return false, err
	}
	if thread.ActiveRunID != runID || thread.Status != domain.ThreadActive {
		return true, nil
	}
	if err := requireLatestThreadPlanTx(ctx, tx, runID, proposalID); err != nil {
		if apperror.CodeOf(err) == apperror.CodeConflict {
			return true, nil
		}
		return false, err
	}
	return false, nil
}
