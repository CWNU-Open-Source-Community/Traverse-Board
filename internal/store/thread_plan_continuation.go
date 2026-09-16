package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
)

type threadPlanContinuedItem struct {
	WorkItemID        string `json:"work_item_id"`
	SourceWorkItemID  string `json:"source_work_item_id"`
	SourceVersion     int64  `json:"source_version"`
	ModuleOrdinal     int    `json:"module_ordinal"`
	CheckpointID      string `json:"checkpoint_id"`
	CompletionEventID string `json:"completion_event_id,omitempty"`
}

func (s *SQLiteStore) ListThreadPlanCompletionSources(ctx context.Context, runID string) ([]domain.ThreadPlanCompletionSource, error) {
	if !domain.ValidAgentID(runID) {
		return nil, apperror.New(apperror.CodeInvalidArgument, "Thread Plan source Run id is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT work_item_id,origin_run_id,origin_work_item_id,
		checkpoint_id,handoff_note_id,'' AS completion_event_id,origin_completed_at FROM thread_plan_completed_sources
		WHERE run_id=? UNION ALL SELECT work_item_id,origin_run_id,origin_work_item_id,
		'','',completion_event_id,origin_completed_at FROM thread_plan_on_demand_completed_sources
		WHERE run_id=? ORDER BY work_item_id`, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.ThreadPlanCompletionSource{}
	for rows.Next() {
		var value domain.ThreadPlanCompletionSource
		var completed string
		if err := rows.Scan(&value.WorkItemID, &value.SourceRunID, &value.SourceWorkItemID, &value.CheckpointID, &value.HandoffNoteID, &value.CompletionEventID, &completed); err != nil {
			return nil, err
		}
		value.CompletedAt = parseTS(completed)
		values = append(values, value)
	}
	return values, rows.Err()
}

// continueThreadPlanTx runs only inside the transaction publishing an exact
// Thread successor. Historical manual completion is provenance, not a new test
// result: no checkpoint, command job, approval, or delivery report is copied.
func continueThreadPlanTx(ctx context.Context, tx *sql.Tx, predecessor, candidate domain.Run,
	mode domain.RunModeSnapshot, at time.Time,
) error {
	if err := requireThreadPlanSuccessorTx(ctx, tx, predecessor, candidate, mode); err != nil {
		return err
	}
	selected, found, err := getPlanDeliverySelectionByRun(ctx, tx, predecessor.ID)
	if err != nil {
		return err
	}
	var original domain.PlanDeliveryProposal
	if found {
		original, err = getPlanDeliveryProposal(ctx, tx, selected.ProposalID)
	} else {
		var id string
		err = tx.QueryRowContext(ctx, `SELECT id FROM plan_delivery_proposals WHERE run_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, predecessor.ID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			original, err = getPlanDeliveryProposal(ctx, tx, id)
		}
	}
	if err != nil {
		return err
	}
	if mode.Surface != domain.ExecutionSurfaceCode || (mode.Phase != domain.ExecutionPhasePlan && mode.Phase != domain.ExecutionPhaseDeliver) {
		return apperror.New(apperror.CodeFailedPrecondition, "Thread Plan continuation requires its existing Code phase")
	}
	if !found && mode.Phase != domain.ExecutionPhasePlan {
		return apperror.New(apperror.CodeFailedPrecondition, "An unselected Plan cannot continue directly in Deliver")
	}
	// Selection uses the existing structured-mutation writer fence. A copied
	// proposal has not charged a tool in this Run, so initialize an empty budget
	// row without inventing a call or copying the predecessor's consumption.
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_tool_usage(run_id,consumed,updated_at) VALUES(?,0,?) ON CONFLICT(run_id) DO NOTHING`, candidate.ID, ts(at)); err != nil {
		return err
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, candidate.ID)
	if err != nil {
		return err
	}
	root, _, changed, err := syncRootAgentTx(ctx, tx, run, mission, rootAgentProjection{Status: domain.AgentReady}, at)
	if err != nil {
		return err
	}
	if changed {
		if _, err = createAgentGraphSnapshotTx(ctx, tx, run); err != nil {
			return err
		}
	}
	proposal := domain.ClonePlanDeliveryProposal(original)
	proposal.ID = idgen.New("plan-proposal")
	proposal.RunID, proposal.SessionID, proposal.RootAgentID = candidate.ID, candidate.SessionID, root.ID
	proposal.ModeRevision, proposal.CreatedAt = mode.Revision, at.UTC()
	proposal.Fingerprint = domain.PlanDeliveryProposalFingerprint(proposal)
	if err = proposal.Validate(); err != nil {
		return err
	}
	selection := domain.PlanDeliverySelection{}
	items := []domain.WorkItem{}
	sources := []threadPlanContinuedItem{}
	var handoff domain.Note
	if found {
		selection = domain.ClonePlanDeliverySelection(selected)
		selection.ID = idgen.New("plan-selection")
		selection.ProposalID, selection.RunID, selection.RootAgentID = proposal.ID, candidate.ID, root.ID
		selection.NoteID = idgen.New("note")
		selection.CreatedAt = at.UTC()
		for i := range selection.Items {
			selection.Items[i].WorkItemID = idgen.New("work")
		}
		direction := proposal.Spec.Directions[selection.DirectionOrdinal-1]
		for i, module := range direction.Modules {
			old, err := getWorkItemTx(ctx, tx, selected.Items[i].WorkItemID)
			if err != nil {
				return err
			}
			if err = validateThreadPlanSourceItem(original, selected, old, i); err != nil {
				return err
			}
			item, err := threadPlanProjectionItem(proposal, selection, module, i, at)
			if err != nil {
				return err
			}
			source := threadPlanContinuedItem{WorkItemID: item.ID, SourceWorkItemID: old.ID, SourceVersion: old.Version, ModuleOrdinal: module.Ordinal}
			if old.Status == domain.WorkItemCompleted {
				if selected.EffectiveManualAcceptance() == domain.PlanDeliveryManualAcceptanceOnDemand {
					source.CompletionEventID, err = threadPlanOriginalCompletionEventTx(ctx, tx, old, selected)
				} else {
					source.CheckpointID, err = threadPlanOriginalCheckpointTx(ctx, tx, old, selected)
				}
				if err != nil {
					return err
				}
				item.Status = domain.WorkItemCompleted
				completed := at.UTC()
				item.CompletedAt = &completed
			}
			if err = item.Validate(); err != nil {
				return err
			}
			items = append(items, item)
			sources = append(sources, source)
		}
		handoff = threadPlanHandoffNote(proposal, selection, direction, at)
		if err = handoff.Validate(); err != nil {
			return err
		}
		// Reuse the original exact projection validator on the canonical pending
		// shape; only independently proven historical completions differ in status.
		pending := append([]domain.WorkItem(nil), items...)
		for i := range pending {
			pending[i].Status = domain.WorkItemPending
			pending[i].CompletedAt = nil
		}
		if err = validatePlanDeliverySelectionProjection(proposal, direction, selection, pending, handoff); err != nil {
			return err
		}
	}
	event, err := events.New(candidate.ID, candidate.MissionID, threadPlanContinuationEvent,
		"thread_plan_continuation", proposal.ID, map[string]any{
			"predecessor_run_id": predecessor.ID, "source_proposal_id": original.ID,
			"source_fingerprint": original.Fingerprint, "source_selection_id": selected.ID,
			"selection_id": selection.ID, "items": sources, "automatic_verification_inherited": false,
		})
	if err != nil {
		return err
	}
	event.CreatedAt = at.UTC()
	if _, err = insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	if err = insertThreadContinuedProposalTx(ctx, tx, proposal, original.ID); err != nil {
		return err
	}
	if !found {
		return nil
	}
	for _, item := range items {
		if err = insertNewWorkItemTx(ctx, tx, item); err != nil {
			return err
		}
	}
	if err = insertNewNoteTx(ctx, tx, handoff); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plan_delivery_selections
  (id,proposal_id,run_id,root_agent_id,direction_ordinal,note_id,module_count,requested_by,version,created_at,manual_acceptance)
  VALUES (?,?,?,?,?,?,?,?,1,?,?)`, selection.ID, proposal.ID, candidate.ID, root.ID, selection.DirectionOrdinal,
		handoff.ID, len(selection.Items), selection.RequestedBy, ts(at), selection.EffectiveManualAcceptance()); err != nil {
		return err
	}
	for _, item := range selection.Items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO plan_delivery_selection_items
   (selection_id,ordinal,module_ordinal,work_item_id) VALUES (?,?,?,?)`, selection.ID, item.Ordinal, item.ModuleOrdinal, item.WorkItemID); err != nil {
			return err
		}
	}
	// Test the same source projection used by the completion gate before publish.
	for i, item := range items {
		if item.Status != domain.WorkItemCompleted {
			continue
		}
		query := `SELECT checkpoint_id FROM thread_plan_completed_sources WHERE run_id=? AND work_item_id=?`
		expected := sources[i].CheckpointID
		if selection.EffectiveManualAcceptance() == domain.PlanDeliveryManualAcceptanceOnDemand {
			query = `SELECT completion_event_id FROM thread_plan_on_demand_completed_sources WHERE run_id=? AND work_item_id=?`
			expected = sources[i].CompletionEventID
		}
		var sourceID string
		if err = tx.QueryRowContext(ctx, query, candidate.ID, item.ID).Scan(&sourceID); err != nil {
			return err
		}
		if sourceID == "" || sourceID != expected {
			return apperror.New(apperror.CodeConflict, "Thread Plan completion source changed")
		}
	}
	history, err := threadPlanHistoryNote(ctx, tx, candidate, root.ID, selected, sources, at)
	if err != nil {
		return err
	}
	if err = insertNewNoteTx(ctx, tx, history); err != nil {
		return err
	}
	return nil
}

func requireThreadPlanSuccessorTx(ctx context.Context, tx *sql.Tx, predecessor, candidate domain.Run, mode domain.RunModeSnapshot) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_runs current
  JOIN thread_runs previous ON previous.thread_id=current.thread_id AND previous.ordinal+1=current.ordinal
    AND current.predecessor_run_id=previous.run_id
  JOIN runs next ON next.id=current.run_id JOIN runs prior ON prior.id=previous.run_id
  JOIN sessions session ON session.id=next.session_id AND session.status='active'
  JOIN run_mode_snapshots mode ON mode.id=? AND mode.run_id=next.id AND mode.revision=?
  WHERE next.id=? AND next.session_id=? AND next.status='created'
    AND prior.id=? AND prior.session_id=? AND prior.status IN ('completed','failed','cancelled')
    AND prior.mission_id=next.mission_id
    AND NOT EXISTS(SELECT 1 FROM run_mode_snapshots later WHERE later.run_id=next.id AND later.revision>mode.revision)`,
		mode.ID, mode.Revision, candidate.ID, candidate.SessionID, predecessor.ID, predecessor.SessionID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return apperror.New(apperror.CodeConflict, "Thread Plan continuation requires an exact idle successor")
	}
	actual, err := getCurrentRunModeSnapshot(ctx, tx, candidate.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, mode) {
		return apperror.New(apperror.CodeConflict, "Thread Plan continuation mode changed")
	}
	return nil
}

func threadPlanProjectionItem(proposal domain.PlanDeliveryProposal, selection domain.PlanDeliverySelection, module domain.PlanDeliveryModule, index int, at time.Time) (domain.WorkItem, error) {
	dependencies := make([]string, len(module.Dependencies))
	for i, ordinal := range module.Dependencies {
		dependencies[i] = selection.Items[ordinal-1].WorkItemID
	}
	details, err := domain.NormalizeWorkItemDetails(selection.Items[index].WorkItemID, domain.WorkItemDetails{
		Title: module.Title, Description: module.Objective, Priority: domain.WorkItemPriorityNormal, OwnerAgentID: proposal.RootAgentID,
		AcceptanceCriteria: module.AcceptanceCriteria, Dependencies: dependencies})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return domain.WorkItem{ID: selection.Items[index].WorkItemID, RunID: proposal.RunID, Title: details.Title,
		Description: details.Description, Priority: details.Priority, OwnerAgentID: details.OwnerAgentID, AcceptanceCriteria: details.AcceptanceCriteria,
		Dependencies: details.Dependencies, Status: domain.WorkItemPending, Version: 1, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}, nil
}

func validateThreadPlanSourceItem(proposal domain.PlanDeliveryProposal, selection domain.PlanDeliverySelection, item domain.WorkItem, index int) error {
	if index >= len(selection.Items) || selection.Items[index].WorkItemID != item.ID || item.RunID != proposal.RunID {
		return apperror.New(apperror.CodeConflict, "Thread Plan source item identity changed")
	}
	module := proposal.Spec.Directions[selection.DirectionOrdinal-1].Modules[index]
	expected, err := threadPlanProjectionItem(proposal, selection, module, index, item.CreatedAt)
	if err != nil {
		return err
	}
	if !sameWorkItemDetails(item, expected) {
		return apperror.New(apperror.CodeFailedPrecondition, "Thread Plan item no longer matches its approved criteria or dependencies")
	}
	return nil
}

func threadPlanOriginalCheckpointTx(ctx context.Context, tx *sql.Tx, item domain.WorkItem, selection domain.PlanDeliverySelection) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT checkpoint.id FROM delivery_checkpoints checkpoint
  JOIN delivery_checkpoint_operations operation ON operation.checkpoint_id=checkpoint.id
  JOIN run_mode_snapshots mode ON mode.id=checkpoint.mode_snapshot_id AND mode.run_id=checkpoint.run_id
    AND mode.phase='deliver' AND mode.revision=checkpoint.mode_revision
  WHERE checkpoint.run_id=? AND checkpoint.selection_id=? AND checkpoint.work_item_id=? AND checkpoint.work_item_version=?`,
		item.RunID, selection.ID, item.ID, item.Version-1).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT checkpoint_id FROM thread_plan_completed_sources
  WHERE run_id=? AND selection_id=? AND work_item_id=?`, item.RunID, selection.ID, item.ID).Scan(&id)
	}
	if err != nil {
		return "", apperror.Wrap(apperror.CodeFailedPrecondition, "Completed Plan item has no exact original manual checkpoint", err)
	}
	return id, nil
}

func threadPlanOriginalCompletionEventTx(ctx context.Context, tx *sql.Tx, item domain.WorkItem, selection domain.PlanDeliverySelection) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT completion_event_id FROM plan_on_demand_completion_events
  WHERE run_id=? AND selection_id=? AND work_item_id=?`, item.RunID, selection.ID, item.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT completion_event_id FROM thread_plan_on_demand_completed_sources
  WHERE run_id=? AND selection_id=? AND work_item_id=?`, item.RunID, selection.ID, item.ID).Scan(&id)
	}
	if err != nil {
		return "", apperror.Wrap(apperror.CodeFailedPrecondition, "Completed Plan item has no exact original completion event", err)
	}
	return id, nil
}

func insertThreadContinuedProposalTx(ctx context.Context, tx *sql.Tx, value domain.PlanDeliveryProposal, sourceID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO plan_delivery_proposals
  (id,run_id,root_agent_id,session_id,workspace_id,mode_revision,protocol_version,status,direction_count,proposal_fingerprint,requested_by,version,created_at)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.RunID, value.RootAgentID, value.SessionID, value.WorkspaceID, value.ModeRevision,
		value.Spec.Version, value.Status, len(value.Spec.Directions), value.Fingerprint, value.RequestedBy, value.Version, ts(value.CreatedAt)); err != nil {
		return err
	}
	for _, statement := range []string{
		`INSERT INTO plan_delivery_directions(proposal_id,ordinal,title,summary,tradeoffs_json,module_count)
   SELECT ?,ordinal,title,summary,tradeoffs_json,module_count FROM plan_delivery_directions WHERE proposal_id=? ORDER BY ordinal`,
		`INSERT INTO plan_delivery_modules(proposal_id,direction_ordinal,ordinal,title,objective,acceptance_json,dependencies_json)
   SELECT ?,direction_ordinal,ordinal,title,objective,acceptance_json,dependencies_json FROM plan_delivery_modules WHERE proposal_id=? ORDER BY direction_ordinal,ordinal`,
	} {
		if _, err := tx.ExecContext(ctx, statement, value.ID, sourceID); err != nil {
			return err
		}
	}
	return nil
}

func threadPlanHandoffNote(proposal domain.PlanDeliveryProposal, selection domain.PlanDeliverySelection, direction domain.PlanDeliveryDirection, at time.Time) domain.Note {
	return redactAndNormalizeNote(domain.Note{ID: selection.NoteID, RunID: proposal.RunID, Title: domain.PlanDeliveryHandoffTitle(direction),
		Content: domain.PlanDeliveryHandoffContent(proposal, direction), Category: domain.NoteDecision, Visibility: domain.NoteVisibilityRun,
		OwnerAgentID: proposal.RootAgentID, Tags: []string{"plan-delivery", "selected-direction"}, SourceRefs: []string{"plan_delivery:" + proposal.ID},
		EvidenceIDs: []string{}, Status: domain.NoteActive, Pinned: true, Version: 1, CreatedAt: at.UTC(), UpdatedAt: at.UTC()})
}

func threadPlanHistoryNote(ctx context.Context, tx *sql.Tx, run domain.Run, rootID string, selection domain.PlanDeliverySelection, sources []threadPlanContinuedItem, at time.Time) (domain.Note, error) {
	var content strings.Builder
	fmt.Fprintf(&content, "沿用原对话已批准的计划。来源 Run：%s；选择：%s。\n历史事项完成进度保留；当前执行的自动检查必须重新验证，未复制旧检查结果。\n", selection.RunID, selection.ID)
	refs := []string{"plan_delivery:" + selection.ProposalID}
	for _, source := range sources {
		fmt.Fprintf(&content, "\n计划项 %s → %s", source.SourceWorkItemID, source.WorkItemID)
		if source.CheckpointID != "" {
			var noteID string
			if err := tx.QueryRowContext(ctx, `SELECT checkpoint.handoff_note_id FROM delivery_checkpoints checkpoint
				JOIN notes note ON note.id=checkpoint.handoff_note_id AND note.run_id=checkpoint.run_id WHERE checkpoint.id=?`, source.CheckpointID).Scan(&noteID); err != nil {
				return domain.Note{}, err
			}
			fmt.Fprintf(&content, "；沿用人工完成记录 %s；原始说明保存在 %s，可从计划项的历史来源查看。\n", source.CheckpointID, noteID)
			refs = append(refs, "delivery_checkpoint:"+source.CheckpointID)
		} else if source.CompletionEventID != "" {
			fmt.Fprintf(&content, "；沿用按需验收计划的真实完成事件 %s；未生成或沿用人工验收记录。\n", source.CompletionEventID)
			refs = append(refs, "run_event:"+source.CompletionEventID)
		} else {
			content.WriteString("；尚未完成。\n")
		}
	}
	value := redactAndNormalizeNote(domain.Note{ID: idgen.New("note"), RunID: run.ID, Title: "沿用计划与历史完成来源",
		Content: content.String(), Category: domain.NoteDecision, Visibility: domain.NoteVisibilityRun, OwnerAgentID: rootID,
		Tags: []string{"plan-delivery", "thread-continuation"}, SourceRefs: refs, EvidenceIDs: []string{}, Status: domain.NoteActive,
		Pinned: true, Version: 1, CreatedAt: at.UTC(), UpdatedAt: at.UTC()})
	return value, value.Validate()
}
