package store

import (
	"context"
	"database/sql"
	"time"

	"cyberagent-workbench/internal/domain"
)

// A model waiting for the next interactive input is not an operator pause.
// Keep only an already running, owned Job alive; proposals that require a
// paused Run still take the ordinary wait path. No new execution is admitted.
func supervisorBackgroundJobWaitTx(ctx context.Context, tx *sql.Tx, run domain.Run, rootID string, now time.Time) (bool, error) {
	available, err := storeTableExists(ctx, tx, "command_runtime_jobs")
	if err != nil || !available {
		return false, err
	}
	var retain bool
	err = tx.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM command_runtime_jobs job
		 WHERE job.run_id=? AND job.session_id=? AND job.mission_id=? AND job.root_agent_id=?
		 AND job.state='running' AND job.started_at IS NOT NULL AND job.completed_at IS NULL
		 AND job.tree_reaped=0 AND julianday(job.owner_expires_at)>julianday(?))
		AND NOT EXISTS (SELECT 1 FROM tool_approvals approval
		 WHERE approval.run_id=? AND approval.session_id=? AND approval.status='pending')
		AND NOT EXISTS (SELECT 1 FROM host_command_proposals proposal
		 WHERE proposal.run_id=? AND proposal.session_id=? AND NOT EXISTS
		 (SELECT 1 FROM host_command_proposal_reviews review WHERE review.proposal_id=proposal.id))`,
		run.ID, run.SessionID, run.MissionID, rootID, ts(now),
		run.ID, run.SessionID, run.ID, run.SessionID).Scan(&retain)
	return retain, err
}
