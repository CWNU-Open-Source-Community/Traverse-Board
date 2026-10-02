package store

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
)

// Deduplicate before the bound: repeatedly reading one skill must not evict
// another successful read or make budget accounting forget it. Failed/pending
// reads never replace successful pins. The extra row detects an invalid bound.
func (s *SQLiteStore) ListBuiltinSkillReadCalls(ctx context.Context, runID string) ([]domain.SupervisorToolCall, error) {
	if !domain.ValidAgentID(runID) {
		return nil, apperror.New(apperror.CodeInvalidArgument, "invalid Skill read Run")
	}
	rows, err := s.db.QueryContext(ctx, `WITH reads AS (
		SELECT *, ROW_NUMBER() OVER (PARTITION BY CASE
			WHEN json_extract(payload_json,'$.installation_id') IS NOT NULL THEN
				json_array('installed',json_extract(payload_json,'$.installation_id'),json_extract(payload_json,'$.package_id'),json_extract(payload_json,'$.component_id'),json_extract(payload_json,'$.revision'))
			ELSE json_array('bundled',json_extract(payload_json,'$.name')) END
		ORDER BY turn DESC, round DESC, position DESC, rowid DESC) AS rank
		FROM run_supervisor_tool_calls WHERE run_id=? AND tool_name='skill_read'
		AND status='completed' AND completed_at IS NOT NULL
		AND COALESCE(json_extract(payload_json,'$.resource'),'')=''
	) SELECT run_id,turn,attempt_id,round,position,model_attempt,call_id,
		stream_response_id,stream_item_id,stream_call_id,tool_name,payload_json,
		authority_json,status,result_json,error_code,created_at,completed_at
		FROM reads WHERE rank=1 ORDER BY json_extract(payload_json,'$.name') LIMIT ?`, runID, skills.MaxSelectionItems+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var calls []domain.SupervisorToolCall
	for rows.Next() {
		call, err := scanSupervisorToolCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}
