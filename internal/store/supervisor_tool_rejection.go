package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func insertSupervisorToolRejectionTx(ctx context.Context, tx *sql.Tx, checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt, diagnostic *llm.ToolRequestRejection) error {
	if diagnostic == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO run_supervisor_tool_rejections
		(run_id,turn,attempt_id,model_attempt,diagnostic_json,diagnostic_sha256,created_at) VALUES(?,?,?,?,?,?,?)`,
		checkpoint.RunID, checkpoint.NextTurn, checkpoint.AttemptID, attempt.Number,
		string(diagnostic.DiagnosticJSON()), diagnostic.DiagnosticSHA256(), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func requireSupervisorToolRejectionReplayTx(ctx context.Context, tx *sql.Tx, checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt, diagnostic *llm.ToolRequestRejection) error {
	var saved, digest string
	err := tx.QueryRowContext(ctx, `SELECT diagnostic_json,diagnostic_sha256 FROM run_supervisor_tool_rejections
		WHERE run_id=? AND turn=? AND attempt_id=? AND model_attempt=?`,
		checkpoint.RunID, checkpoint.NextTurn, checkpoint.AttemptID, attempt.Number).Scan(&saved, &digest)
	if errors.Is(err, sql.ErrNoRows) && diagnostic == nil {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// A legacy terminal is not a license to fabricate historical evidence. An
	// exact new replay is idempotent; differing or removed evidence conflicts.
	if diagnostic == nil || errors.Is(err, sql.ErrNoRows) || digest != diagnostic.DiagnosticSHA256() || saved != string(diagnostic.DiagnosticJSON()) {
		return apperror.New(apperror.CodeConflict, "rejected native request replay does not match its original diagnostic")
	}
	return nil
}
