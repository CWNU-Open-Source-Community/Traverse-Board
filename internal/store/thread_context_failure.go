package store

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/domain"
)

// The context gate is Go-owned and runs before the next model request. Bind its
// label to the exact sealed handoff; provider messages and generic budget
// exhaustion cannot manufacture this failure stage.
func recordedThreadContextFailureStage(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, handoffID string) (string, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_execution_handoff_results
		WHERE operation_id=? AND status='failed' AND UPPER(error_code)='RESOURCE_EXHAUSTED'
		AND stop_reason=?`, handoffID, domain.ThreadFailureContextWindowExceeded).Scan(&count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if count == 1 {
		return domain.ThreadFailureContextWindowExceeded, nil
	}
	return "", nil
}
