package store

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

// FindCommandRuntimeOutputActivity discovers a real persisted activity, rather
// than deriving a call identity from a Job ID. CLI Jobs without a Supervisor
// result remain metadata-only. The application still checks the entire public
// artifact binding shared with GetArtifact before exposing these coordinates.
func (s *SQLiteStore) FindCommandRuntimeOutputActivity(ctx context.Context, runID, jobID string) (
	threadID, activityRef string, found bool, err error,
) {
	if !domain.ValidAgentID(runID) || !domain.ValidAgentID(jobID) {
		return "", "", false, apperror.New(apperror.CodeInvalidArgument,
			"command output source identity is invalid")
	}
	err = s.db.QueryRowContext(ctx, `WITH results AS (
		SELECT binding.thread_id, call.call_id, call.turn, call.round, call.position,
			CASE WHEN json_valid(call.result_json) THEN call.result_json ELSE '{}' END AS result
		FROM thread_runs binding
		JOIN run_supervisor_tool_calls call ON call.run_id = binding.run_id
		WHERE binding.run_id = ? AND call.tool_name = 'command_runtime'
	), projections AS (
		SELECT *, CASE WHEN json_valid(json_extract(result, '$.stdout'))
			THEN json_extract(result, '$.stdout') ELSE '{}' END AS projection
		FROM results
		WHERE json_extract(result, '$.version') = 'supervisor_tool_result.v1'
			AND json_extract(result, '$.tool') = 'command_runtime'
	)
	SELECT thread_id, call_id FROM projections
	WHERE json_extract(projection, '$.version') = ?
		AND EXISTS (SELECT 1 FROM json_each(projection, '$.jobs') job
			WHERE json_extract(job.value, '$.id') = ?)
	ORDER BY turn DESC, round DESC, position DESC LIMIT 1`, runID, runner.CommandRuntimeResultVersion, jobID).
		Scan(&threadID, &activityRef)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return threadID, activityRef, err == nil, err
}
