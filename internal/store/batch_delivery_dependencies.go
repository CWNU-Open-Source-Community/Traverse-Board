package store

import (
	"context"
	"database/sql"
	"slices"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/waitgraph"
)

// SettleBatchDeliveryDependencies is a recovery path for an immutable accepted
// review. It settles only the admitted proposal's declared core-task waits;
// unrelated waits for the same target remain pending.
func (s *SQLiteStore) SettleBatchDeliveryDependencies(ctx context.Context, planID string,
	ordinal int, generation int64, receiptID, reviewID string) ([]domain.DependencyWake, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	wakes, err := settleBatchDeliveryDependenciesTx(ctx, tx, planID, ordinal, generation, receiptID, reviewID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return wakes, nil
}

func settleBatchDeliveryDependenciesTx(ctx context.Context, tx *sql.Tx, planID string,
	ordinal int, generation int64, receiptID, reviewID string) ([]domain.DependencyWake, error) {
	plan, found, err := getBatchDeliveryPlanTx(ctx, tx, planID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, apperror.New(apperror.CodeNotFound, "batch dependency plan was not found")
	}
	child, found, err := getBatchDeliveryWorkspaceTx(ctx, tx, planID, ordinal)
	if err != nil {
		return nil, err
	}
	if !found || child.Generation != generation ||
		(child.Status != domain.BatchWorkspaceAccepted && child.Status != domain.BatchWorkspaceMerged) {
		return nil, apperror.New(apperror.CodeConflict, "batch dependency requires the current accepted generation")
	}
	receipt, found, err := getBatchDeliveryReceiptTx(ctx, tx, receiptID)
	if err != nil {
		return nil, err
	}
	if !found || receipt.PlanID != plan.ID || receipt.Ordinal != ordinal || receipt.Generation != generation ||
		receipt.BaseCommit != child.BaseCommit || receipt.HeadCommit != child.HeadCommit {
		return nil, apperror.New(apperror.CodeConflict, "batch dependency receipt binding changed")
	}
	review, found, err := getBatchDeliveryReviewTx(ctx, tx, reviewID)
	if err != nil {
		return nil, err
	}
	if !found || review.PlanID != plan.ID || review.Ordinal != ordinal || review.Generation != generation ||
		review.ReceiptID != receipt.ID || review.Verdict != domain.BatchReviewAccepted ||
		review.BaseCommit != receipt.BaseCommit || review.HeadCommit != receipt.HeadCommit ||
		review.DiffSHA256 != receipt.DiffSHA256 || review.CallChainSHA256 != receipt.CallChainSHA256 ||
		!review.FullDiffReviewed || !review.CallChainReviewed || !review.TestsReviewed || review.Reviewer == child.AgentID {
		return nil, apperror.New(apperror.CodeConflict, "batch dependency review binding changed")
	}
	var targetID string
	err = tx.QueryRowContext(ctx, `SELECT admitted_agent_id FROM child_task_assignments
		WHERE proposal_id=? AND ordinal=? AND status='admitted' AND surface='core'`, plan.ProposalID, ordinal).Scan(&targetID)
	if err != nil {
		return nil, err
	}
	if targetID != child.AgentID {
		return nil, apperror.New(apperror.CodeConflict, "batch dependency target differs from admission")
	}
	_, _, missionID, err := loadMonetaryRunTx(ctx, tx, plan.RunID)
	if err != nil {
		return nil, err
	}
	wakes := []domain.DependencyWake{}
	for _, task := range plan.Spec.Tasks {
		if !slices.Contains(task.DependencyOrdinals, ordinal) {
			continue
		}
		var sourceID string
		err := tx.QueryRowContext(ctx, `SELECT admitted_agent_id FROM child_task_assignments
			WHERE proposal_id=? AND ordinal=? AND status='admitted' AND surface='core'`, plan.ProposalID, task.Ordinal).Scan(&sourceID)
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, dependencyEdgeSelect+` WHERE run_id=? AND source_kind=? AND source_id=?
			AND target_kind=? AND target_id=? AND state='wait' AND reason='child task dependency'
			AND generation=1 AND failure_policy='fail' ORDER BY created_at,id`, plan.RunID,
			string(waitgraph.KindAgent), sourceID, string(waitgraph.KindAgent), targetID)
		if err != nil {
			return nil, err
		}
		edges := []domain.DependencyEdge{}
		for rows.Next() {
			edge, err := scanDependencyEdge(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			edges = append(edges, edge)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		for _, edge := range edges {
			wake, settled, err := settleDependencyEdgeTx(ctx, tx, domain.Run{ID: plan.RunID, MissionID: missionID},
				edge, domain.AgentDependencySatisfied, "accepted batch delivery "+review.ID)
			if err != nil {
				return nil, err
			}
			if settled {
				wakes = append(wakes, wake)
			}
		}
	}
	return wakes, nil
}
