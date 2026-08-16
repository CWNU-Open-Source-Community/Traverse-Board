package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
)

// TerminalSessionRecord is the durable session ledger row. Agent input
// status is stored, but tokens, secrets, and raw output never are.
type TerminalSessionRecord struct {
	ID               string
	ProtocolVersion  string
	RunID            string
	WorkspaceID      string
	State            string
	Cwd              string
	Columns          int
	Rows             int
	ProcessPID       int
	AgentInputActive bool
	CreatedAt        time.Time
	ClosedAt         *time.Time
	LastActivityAt   time.Time
}

func (s *SQLiteStore) CreateTerminalSession(ctx context.Context, record TerminalSessionRecord) error {
	if record.ProtocolVersion != "user_terminal_session.v1" || strings.TrimSpace(record.ID) == "" ||
		len(record.ID) > 256 || record.State == "" || record.Columns < 20 || record.Columns > 300 ||
		record.Rows < 5 || record.Rows > 120 {
		return apperror.New(apperror.CodeInvalidArgument, "terminal session record is invalid")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO terminal_sessions
		(id, protocol_version, run_id, workspace_id, state, cwd, columns, rows, process_pid,
		agent_input_active, created_at, last_activity_at)
		VALUES (?, 'user_terminal_session.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.RunID, record.WorkspaceID, record.State, record.Cwd, record.Columns,
		record.Rows, record.ProcessPID, boolInt(record.AgentInputActive),
		ts(record.CreatedAt), ts(record.LastActivityAt))
	return err
}

// UpdateTerminalSession refreshes state/cwd/agent-input status. A session
// that was closed or failed cannot be silently revived: state transitions
// are constrained by the CHECK plus this guard.
func (s *SQLiteStore) UpdateTerminalSession(ctx context.Context, record TerminalSessionRecord) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT state FROM terminal_sessions WHERE id = ?`,
		record.ID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.New(apperror.CodeNotFound, "terminal session was not found")
	}
	if err != nil {
		return err
	}
	if previous == "closed" || previous == "failed" {
		return apperror.New(apperror.CodeConflict, "terminal session is already terminal")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE terminal_sessions SET state = ?, cwd = ?,
		process_pid = ?, agent_input_active = ?, last_activity_at = ?,
		closed_at = CASE WHEN ? IN ('closed', 'exited', 'failed') THEN ? ELSE closed_at END
		WHERE id = ?`,
		record.State, record.Cwd, record.ProcessPID, boolInt(record.AgentInputActive),
		ts(record.LastActivityAt), record.State, ts(record.LastActivityAt), record.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordTerminalSessionEvent appends one bounded, source-attributed audit
// event. Raw output, passwords, and secrets never enter the event stream.
func (s *SQLiteStore) RecordTerminalSessionEvent(ctx context.Context, runID, eventType string,
	payload map[string]any,
) error {
	runID = strings.TrimSpace(runID)
	if runID == "" || strings.ContainsRune(runID, 0) {
		return apperror.New(apperror.CodeInvalidArgument, "terminal audit run id is invalid")
	}
	switch eventType {
	case "terminal.session_started", "terminal.session_stopped", "terminal.agent_input_issued",
		"terminal.agent_input_revoked", "terminal.agent_input_expired", "terminal.resized",
		"terminal.cwd_changed", "terminal.crashed":
	default:
		return apperror.New(apperror.CodeInvalidArgument, "terminal audit event type is invalid")
	}
	for key, value := range payload {
		if len([]byte(key)) > 64 {
			delete(payload, key)
			continue
		}
		if text, ok := value.(string); ok && len([]byte(text)) > 256 {
			payload[key] = "[bounded]"
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`, runID).Scan(&missionID); err != nil {
		return err
	}
	event, err := events.New(runID, missionID, eventType, "debug_terminal", runID, payload)
	if err != nil {
		return err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}
