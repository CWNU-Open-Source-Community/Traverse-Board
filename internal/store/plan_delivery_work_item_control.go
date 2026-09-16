package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runmutation"
)

func (s *SQLiteStore) TransitionPlanDeliveryWorkItem(ctx context.Context, request domain.PlanDeliveryWorkItemTransition) (domain.WorkItem, bool, error) {
	if err := request.Validate(); err != nil {
		return domain.WorkItem{}, false, apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Fence the lookup and mutation together, including requests from another
	// store/process. Terminal Runs remain readable for exact receipt replay.
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET updated_at = updated_at WHERE id = ?`, request.RunID); err != nil {
		return domain.WorkItem{}, false, err
	}
	conflict := apperror.New(apperror.CodeConflict, "Plan Delivery operation key was already used for different intent")
	eventID := domain.PlanDeliveryControlEventID(request.RunID, request.OperationKey)
	if event, found, err := getRunEventByEventID(ctx, tx, eventID); err != nil {
		return domain.WorkItem{}, false, err
	} else if found {
		var receipt struct {
			Fingerprint string                `json:"request_fingerprint"`
			Version     int64                 `json:"version"`
			To          domain.WorkItemStatus `json:"to"`
		}
		if json.Unmarshal([]byte(event.PayloadJSON), &receipt) != nil || event.RunID != request.RunID ||
			event.SubjectID != request.WorkItemID || event.Type != events.WorkItemChangedEvent || event.Source != "plan_delivery_control" ||
			receipt.Fingerprint != request.Fingerprint() || receipt.Version != request.ExpectedVersion+1 || receipt.To != request.Target {
			return domain.WorkItem{}, false, conflict
		}
		item, err := getWorkItemTx(ctx, tx, request.WorkItemID)
		if err != nil {
			return domain.WorkItem{}, false, err
		}
		if item.RunID != request.RunID {
			return domain.WorkItem{}, false, conflict
		}
		return item, true, nil
	}
	// Check older checkpoint operations too: their original audit event did
	// not use the shared control identity.
	if _, found, err := getDeliveryCheckpointOperation(ctx, tx,
		runmutation.OperationKeyDigest("delivery_checkpoint_record", request.RunID, request.OperationKey)); err != nil {
		return domain.WorkItem{}, false, err
	} else if found {
		return domain.WorkItem{}, false, conflict
	}
	run, _, err := getCoordinatorRunTx(ctx, tx, request.RunID)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, request.RunID)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	if run.Status != domain.RunPaused || mode.Phase != domain.ExecutionPhaseDeliver {
		return domain.WorkItem{}, false, apperror.New(apperror.CodeFailedPrecondition, "Plan Delivery work control requires a paused Deliver-phase Run")
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_execution_leases WHERE run_id = ? AND status = 'active' AND julianday(expires_at) > julianday('now')`, run.ID).Scan(&active); err != nil {
		return domain.WorkItem{}, false, err
	}
	if active != 0 {
		return domain.WorkItem{}, false, apperror.New(apperror.CodeFailedPrecondition, "Plan Delivery work control requires a quiescent Run")
	}
	item, err := getWorkItemTx(ctx, tx, request.WorkItemID)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	selection, found, err := getPlanDeliverySelectionByRun(ctx, tx, run.ID)
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	_, selected := selectedDeliveryItem(selection, item.ID)
	if !found || !selected || item.RunID != run.ID {
		return domain.WorkItem{}, false, apperror.New(apperror.CodeFailedPrecondition, "WorkItem must belong to this Run's selected Plan direction")
	}
	var enrolled int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM delivery_gate_enrollments WHERE run_id = ? AND selection_id = ?)`, run.ID, selection.ID).Scan(&enrolled); err != nil {
		return domain.WorkItem{}, false, err
	}
	if enrolled == 0 {
		return domain.WorkItem{}, false, apperror.New(apperror.CodeFailedPrecondition, "selected Plan is not enrolled in Delivery checkpoint gates")
	}
	if item.Version != request.ExpectedVersion {
		return domain.WorkItem{}, false, apperror.New(apperror.CodeConflict, "Plan Delivery WorkItem version changed")
	}
	if (request.Target == domain.WorkItemInProgress && item.Status != domain.WorkItemPending) ||
		(request.Target == domain.WorkItemCompleted && item.Status != domain.WorkItemInProgress) {
		return domain.WorkItem{}, false, apperror.New(apperror.CodeFailedPrecondition, "Plan Delivery WorkItem is not ready for this transition")
	}
	from := item.Status
	if err := item.Transition(request.Target, "", time.Now().UTC()); err != nil {
		return domain.WorkItem{}, false, apperror.Normalize(err)
	}
	item.Version = request.ExpectedVersion + 1
	if err := item.Validate(); err != nil {
		return domain.WorkItem{}, false, apperror.Normalize(err)
	}
	event, err := events.New(run.ID, run.MissionID, events.WorkItemChangedEvent, "plan_delivery_control", item.ID,
		map[string]any{"from": from, "to": item.Status, "reason": item.BlockedReason, "version": item.Version,
			"request_fingerprint": request.Fingerprint(), "expected_version": request.ExpectedVersion, "requested_by": request.RequestedBy})
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	event.EventID = eventID
	if err := updateWorkItemTx(ctx, tx, item, request.ExpectedVersion, event); err != nil {
		return domain.WorkItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.WorkItem{}, false, err
	}
	return item, false, nil
}
