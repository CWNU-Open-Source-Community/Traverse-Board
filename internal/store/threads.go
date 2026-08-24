package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
)

const threadSelect = `SELECT id, protocol_version, workspace_id, mission_id, title,
	status, COALESCE(active_run_id, ''), COALESCE(last_run_id, ''), version,
	created_at, updated_at, archived_at, deleted_at FROM threads`

// insertInitialThreadTx is intentionally tolerant of pre-v129 partial schemas:
// migration compatibility tests construct old schema prefixes and exercise Run
// creation before upgrading. A fully-migrated product database always has the
// Thread tables and therefore always commits the projection atomically.
func insertInitialThreadTx(ctx context.Context, tx *sql.Tx, mission domain.Mission,
	run domain.Run,
) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'threads'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	threadID := domain.InitialThreadID(run.ID)
	title := redact.String(mission.Goal)
	if _, err := tx.ExecContext(ctx, `INSERT INTO threads
		(id, protocol_version, workspace_id, mission_id, title, status,
		 active_run_id, last_run_id, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, 0, ?, ?)`, threadID,
		domain.ThreadProtocolVersion, mission.WorkspaceID, mission.ID, title,
		domain.ThreadActive, ts(run.CreatedAt), ts(run.UpdatedAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_runs
		(thread_id, run_id, session_id, ordinal, predecessor_run_id, created_at)
		VALUES (?, ?, ?, 1, NULL, ?)`, threadID, run.ID, run.SessionID,
		ts(run.CreatedAt)); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"run_id": run.ID, "backfilled": false})
	_, err := tx.ExecContext(ctx, `INSERT INTO thread_events
		(thread_id, run_id, type, source, payload_json, created_at)
		VALUES (?, ?, 'thread.created', 'run_creation', ?, ?)`, threadID, run.ID,
		string(payload), ts(run.CreatedAt))
	return err
}

func (s *SQLiteStore) GetThread(ctx context.Context, id string) (domain.Thread, error) {
	return scanThread(s.db.QueryRowContext(ctx, threadSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) GetThreadByRun(ctx context.Context, runID string) (domain.Thread, error) {
	return scanThread(s.db.QueryRowContext(ctx, threadSelect+`
		WHERE id = (SELECT thread_id FROM thread_runs WHERE run_id = ?)`,
		strings.TrimSpace(runID)))
}

func (s *SQLiteStore) GetThreadBySession(ctx context.Context, sessionID string) (domain.Thread, error) {
	return scanThread(s.db.QueryRowContext(ctx, threadSelect+`
		WHERE id = (SELECT thread_id FROM thread_runs WHERE session_id = ?)`,
		strings.TrimSpace(sessionID)))
}

func (s *SQLiteStore) ListThreadsByCreationPage(ctx context.Context, filter domain.ThreadFilter,
	beforeCreatedAt time.Time, beforeID string,
) ([]domain.Thread, error) {
	if err := validateStoreCreationPage(beforeCreatedAt, beforeID, filter.Limit); err != nil {
		return nil, err
	}
	query := threadSelect + ` WHERE 1=1`
	args := make([]any, 0, 5)
	if filter.Status != "" {
		if !domain.ValidThreadStatus(filter.Status) {
			return nil, fmt.Errorf("invalid thread status %q", filter.Status)
		}
		query += ` AND status = ?`
		args = append(args, filter.Status)
	} else if !filter.IncludeDeleted {
		query += ` AND status <> ?`
		args = append(args, domain.ThreadDeleted)
	}
	if !beforeCreatedAt.IsZero() {
		query += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		anchor := ts(beforeCreatedAt)
		args = append(args, anchor, anchor, beforeID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Thread, 0, filter.Limit)
	for rows.Next() {
		item, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) ListThreadRuns(ctx context.Context, threadID string) ([]domain.ThreadRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT thread_id, run_id, session_id, ordinal,
		COALESCE(predecessor_run_id, ''), created_at FROM thread_runs
		WHERE thread_id = ? ORDER BY ordinal`, strings.TrimSpace(threadID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ThreadRun
	for rows.Next() {
		var item domain.ThreadRun
		var created string
		if err := rows.Scan(&item.ThreadID, &item.RunID, &item.SessionID, &item.Ordinal,
			&item.PredecessorRunID, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTS(created)
		if err := item.Validate(); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListThreadMessagesPage(ctx context.Context, threadID string,
	includeCompacted bool, offset, limit int,
) ([]domain.ThreadMessage, error) {
	if err := validateStoreListOffset(offset); err != nil {
		return nil, err
	}
	all := limit == 0 && offset == 0
	if !all && (limit <= 0 || limit > 1000) {
		return nil, errors.New("thread message limit must be between 1 and 1000")
	}
	query := `SELECT identity, thread_id, run_id, session_id, role, content,
		provenance_version, source_kind, source_ref, content_sha256,
		instruction_authorized, message_status, token_estimate, compacted, created_at
		FROM (
			SELECT binding.ordinal AS run_ordinal, message.created_at AS sort_time,
				'message-' || message.id AS identity, binding.thread_id,
				binding.run_id, message.session_id, message.role, message.content,
				message.provenance_version, message.source_kind,
				message.source_ref, message.content_sha256,
				message.instruction_authorized, 'committed' AS message_status,
				message.token_estimate, message.compacted, message.created_at
			FROM thread_runs binding JOIN session_messages message
				ON message.session_id = binding.session_id
			WHERE binding.thread_id = ?`
	args := []any{strings.TrimSpace(threadID)}
	if !includeCompacted {
		query += ` AND message.compacted = 0`
	}
	query += ` UNION ALL
			SELECT binding.ordinal AS run_ordinal, steering.created_at AS sort_time,
				steering.id AS identity, binding.thread_id, binding.run_id,
				steering.session_id, 'user' AS role, steering.content,
				? AS provenance_version, ? AS source_kind, '' AS source_ref,
				steering.content_sha256, 1 AS instruction_authorized,
				steering.status AS message_status,
				(length(CAST(steering.content AS BLOB)) + 3) / 4 AS token_estimate,
				0 AS compacted, steering.created_at
			FROM thread_runs binding JOIN operator_steering_messages steering
				ON steering.run_id = binding.run_id AND steering.session_id = binding.session_id
			WHERE binding.thread_id = ? AND steering.status = 'pending'
		) ORDER BY run_ordinal, sort_time, identity`
	args = append(args, session.ContextProvenanceVersion, session.SourceOperatorMessage,
		strings.TrimSpace(threadID))
	if !all {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ThreadMessage
	for rows.Next() {
		var item domain.ThreadMessage
		var compacted, instructionAuthorized int
		var created string
		if err := rows.Scan(&item.ID, &item.ThreadID, &item.RunID, &item.SessionID,
			&item.Role, &item.Content, &item.ProvenanceVersion, &item.SourceKind,
			&item.SourceRef, &item.ContentSHA256, &instructionAuthorized, &item.Status,
			&item.TokenEstimate, &compacted, &created); err != nil {
			return nil, err
		}
		item.Compacted = compacted != 0
		item.InstructionAuthorized = instructionAuthorized != 0
		item.CreatedAt = parseTS(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListThreadEvents(ctx context.Context, threadID string,
	limit int,
) ([]domain.ThreadEvent, error) {
	if limit < 0 || limit > 1000 {
		return nil, errors.New("thread event limit must be between 0 and 1000")
	}
	query := `SELECT id, thread_id, COALESCE(run_id, ''),
		type, source, payload_json, created_at FROM thread_events
		WHERE thread_id = ? ORDER BY id`
	args := []any{strings.TrimSpace(threadID)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ThreadEvent
	for rows.Next() {
		var item domain.ThreadEvent
		var created string
		if err := rows.Scan(&item.ID, &item.ThreadID, &item.RunID, &item.Type,
			&item.Source, &item.PayloadJSON, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTS(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) listThreadRunAuditEvents(ctx context.Context,
	threadID string,
) ([]domain.ThreadRunAuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event.event_id, event.version,
		event.run_id, event.mission_id, event.sequence, event.type, event.source,
		COALESCE(event.subject_id, ''), event.payload_json, event.created_at
		FROM thread_runs binding JOIN run_events event ON event.run_id = binding.run_id
		WHERE binding.thread_id = ? ORDER BY binding.ordinal, event.sequence`,
		strings.TrimSpace(threadID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ThreadRunAuditEvent
	for rows.Next() {
		var item domain.ThreadRunAuditEvent
		var created string
		if err := rows.Scan(&item.EventID, &item.Version, &item.RunID, &item.MissionID,
			&item.Sequence, &item.Type, &item.Source, &item.SubjectID,
			&item.PayloadJSON, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTS(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// EnsureThreadSuccessor returns the current live Run when one already exists,
// otherwise it creates exactly one successor Run and a fresh Session inside the
// same immediate SQLite transaction. The new Run graph receives only initial,
// all-denied authority snapshots from createRunGraphTx.
func (s *SQLiteStore) EnsureThreadSuccessor(ctx context.Context, threadID,
	expectedLastRunID string, mission domain.Mission, candidate domain.Run,
	mode domain.RunModeSnapshot, linkedSession session.Session, initialEvents []events.Event,
) (domain.Thread, domain.Run, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	threadRecord, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`,
		strings.TrimSpace(threadID)))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if threadRecord.Status != domain.ThreadActive {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "Thread is not active")
	}
	if threadRecord.ActiveRunID != "" {
		active, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
			status, config_json, budget_json, started_at, finished_at, created_at, updated_at
			FROM runs WHERE id = ?`, threadRecord.ActiveRunID))
		if err != nil {
			return domain.Thread{}, domain.Run{}, false, err
		}
		if active.Terminal() {
			return domain.Thread{}, domain.Run{}, false, errors.New(
				"Thread active Run projection points at a terminal Run")
		}
		if err := tx.Commit(); err != nil {
			return domain.Thread{}, domain.Run{}, false, err
		}
		return threadRecord, active, false, nil
	}
	if threadRecord.LastRunID != strings.TrimSpace(expectedLastRunID) {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread last Run changed during continuation")
	}
	predecessor, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
		status, config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, threadRecord.LastRunID))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if !predecessor.Terminal() {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "Thread has no terminal Run to continue")
	}
	if candidate.MissionID != threadRecord.MissionID || mission.ID != threadRecord.MissionID ||
		mission.WorkspaceID != threadRecord.WorkspaceID {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread successor Mission scope changed")
	}
	if err := createRunGraphTx(ctx, tx, mission, candidate, mode, linkedSession, true,
		false, initialEvents); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal), 0) + 1
		FROM thread_runs WHERE thread_id = ?`, threadRecord.ID).Scan(&ordinal); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_runs
		(thread_id, run_id, session_id, ordinal, predecessor_run_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, threadRecord.ID, candidate.ID, linkedSession.ID,
		ordinal, predecessor.ID, ts(candidate.CreatedAt)); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{
		"predecessor_run_id": predecessor.ID, "successor_run_id": candidate.ID,
		"authority_inherited": false,
	})
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_events
		(thread_id, run_id, type, source, payload_json, created_at)
		VALUES (?, ?, 'thread.run_successor_created', 'thread_continuation', ?, ?)`,
		threadRecord.ID, candidate.ID, string(payload), ts(candidate.CreatedAt)); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	threadRecord, err = scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`,
		threadRecord.ID))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if threadRecord.ActiveRunID != candidate.ID || threadRecord.LastRunID != candidate.ID {
		return domain.Thread{}, domain.Run{}, false, errors.New(
			"Thread successor projection did not bind the candidate Run")
	}
	if err := tx.Commit(); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	return threadRecord, candidate, true, nil
}

func (s *SQLiteStore) TransitionThread(ctx context.Context, id string,
	action domain.ThreadLifecycleAction, expectedVersion int64, requestedBy string,
	at time.Time,
) (domain.Thread, error) {
	return s.transitionThread(ctx, id, action, expectedVersion, requestedBy, "", at)
}

func (s *SQLiteStore) TransitionThreadWithOperationKey(ctx context.Context, id string,
	action domain.ThreadLifecycleAction, expectedVersion int64, requestedBy, operationKey string,
	at time.Time,
) (domain.Thread, error) {
	operationKey, err := domain.NormalizeAgentOperationKey(operationKey)
	if err != nil {
		return domain.Thread{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread lifecycle idempotency key is invalid")
	}
	return s.transitionThread(ctx, id, action, expectedVersion, requestedBy, operationKey, at)
}

func (s *SQLiteStore) transitionThread(ctx context.Context, id string,
	action domain.ThreadLifecycleAction, expectedVersion int64, requestedBy, operationKey string,
	at time.Time,
) (domain.Thread, error) {
	if expectedVersion <= 0 {
		return domain.Thread{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread lifecycle expected version must be positive")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" {
		requestedBy = "thread_service"
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.Thread{}, err
	}
	defer func() { _ = tx.Rollback() }()
	keyDigest, requestFingerprint := "", ""
	if operationKey != "" {
		keyDigest = runmutation.Fingerprint("thread_lifecycle_operation.v1", operationKey)
		requestFingerprint = runmutation.Fingerprint("thread_lifecycle_request.v1",
			strings.TrimSpace(id), string(action), strconv.FormatInt(expectedVersion, 10),
			requestedBy)
		var storedFingerprint, storedThreadID, storedAction, resultJSON string
		err := tx.QueryRowContext(ctx, `SELECT request_fingerprint, thread_id, action,
			result_json FROM thread_lifecycle_operations WHERE key_digest = ?`, keyDigest).
			Scan(&storedFingerprint, &storedThreadID, &storedAction, &resultJSON)
		if err == nil {
			if storedFingerprint != requestFingerprint || storedThreadID != strings.TrimSpace(id) ||
				storedAction != string(action) {
				return domain.Thread{}, apperror.New(apperror.CodeConflict,
					"Thread lifecycle idempotency key was reused for another request")
			}
			var replay domain.Thread
			if err := json.Unmarshal([]byte(resultJSON), &replay); err != nil {
				return domain.Thread{}, err
			}
			if err := replay.Validate(); err != nil {
				return domain.Thread{}, err
			}
			if err := tx.Commit(); err != nil {
				return domain.Thread{}, err
			}
			return replay, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.Thread{}, err
		}
	}
	current, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`,
		strings.TrimSpace(id)))
	if err != nil {
		return domain.Thread{}, err
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return domain.Thread{}, apperror.New(apperror.CodeConflict,
			"Thread lifecycle version changed")
	}
	commitResult := func(value domain.Thread) (domain.Thread, error) {
		if keyDigest != "" {
			resultJSON, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return domain.Thread{}, marshalErr
			}
			if _, insertErr := tx.ExecContext(ctx, `INSERT INTO thread_lifecycle_operations
				(key_digest, request_fingerprint, thread_id, action, result_json, created_at)
				VALUES (?, ?, ?, ?, ?, ?)`, keyDigest, requestFingerprint, value.ID,
				string(action), string(resultJSON), ts(at)); insertErr != nil {
				return domain.Thread{}, insertErr
			}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return domain.Thread{}, commitErr
		}
		return value, nil
	}
	var target domain.ThreadStatus
	var archivedAt, deletedAt any
	switch action {
	case domain.ThreadArchive:
		if current.Status == domain.ThreadArchived {
			return commitResult(current)
		}
		if current.Status != domain.ThreadActive {
			return domain.Thread{}, apperror.New(apperror.CodeFailedPrecondition,
				"only an active Thread can be archived")
		}
		target, archivedAt = domain.ThreadArchived, ts(at)
	case domain.ThreadRestore:
		if current.Status == domain.ThreadActive {
			return commitResult(current)
		}
		if current.Status != domain.ThreadArchived {
			return domain.Thread{}, apperror.New(apperror.CodeFailedPrecondition,
				"only an archived Thread can be restored")
		}
		target = domain.ThreadActive
	case domain.ThreadDelete:
		if current.Status == domain.ThreadDeleted {
			return commitResult(current)
		}
		target, deletedAt = domain.ThreadDeleted, ts(at)
		if current.ArchivedAt != nil {
			archivedAt = ts(*current.ArchivedAt)
		} else {
			archivedAt = ts(at)
		}
	default:
		return domain.Thread{}, apperror.New(apperror.CodeInvalidArgument,
			"unsupported Thread lifecycle action")
	}
	if current.ActiveRunID != "" && action != domain.ThreadRestore {
		active, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
			status, config_json, budget_json, started_at, finished_at, created_at, updated_at
			FROM runs WHERE id = ?`, current.ActiveRunID))
		if err != nil {
			return domain.Thread{}, err
		}
		if action == domain.ThreadDelete {
			return domain.Thread{}, apperror.New(apperror.CodeFailedPrecondition,
				"a Thread with a live Run must be cancelled before deletion")
		}
		if active.Status == domain.RunPreparing || active.Status == domain.RunRunning {
			return domain.Thread{}, apperror.New(apperror.CodeFailedPrecondition,
				"a preparing or running Thread must be paused or cancelled first")
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE threads SET status = ?, version = version + 1,
		updated_at = ?, archived_at = ?, deleted_at = ? WHERE id = ? AND version = ?`,
		target, ts(at), archivedAt, deletedAt, current.ID, current.Version)
	if err != nil {
		return domain.Thread{}, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return domain.Thread{}, err
		}
		return domain.Thread{}, apperror.New(apperror.CodeConflict,
			"Thread lifecycle changed concurrently")
	}
	sessionStatus := session.StatusArchived
	if target == domain.ThreadActive {
		sessionStatus = session.StatusActive
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = ?, updated_at = ?
		WHERE id IN (SELECT session_id FROM thread_runs WHERE thread_id = ?)`,
		sessionStatus, ts(at), current.ID); err != nil {
		return domain.Thread{}, err
	}
	payload, _ := json.Marshal(map[string]any{"status": target, "requested_by": requestedBy})
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_events
		(thread_id, run_id, type, source, payload_json, created_at)
		VALUES (?, ?, ?, 'thread_lifecycle', ?, ?)`, current.ID,
		nullableString(current.ActiveRunID), "thread."+string(action), string(payload),
		ts(at)); err != nil {
		return domain.Thread{}, err
	}
	updated, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`, current.ID))
	if err != nil {
		return domain.Thread{}, err
	}
	return commitResult(updated)
}

func (s *SQLiteStore) ExportThread(ctx context.Context, threadID string) (domain.ThreadExport, error) {
	threadRecord, err := s.GetThread(ctx, threadID)
	if err != nil {
		return domain.ThreadExport{}, err
	}
	mission, err := s.GetMission(ctx, threadRecord.MissionID)
	if err != nil {
		return domain.ThreadExport{}, err
	}
	bindings, err := s.ListThreadRuns(ctx, threadRecord.ID)
	if err != nil {
		return domain.ThreadExport{}, err
	}
	runs := make([]domain.Run, 0, len(bindings))
	sessions := make([]domain.ThreadSession, 0, len(bindings))
	for _, binding := range bindings {
		run, err := s.GetRun(ctx, binding.RunID)
		if err != nil {
			return domain.ThreadExport{}, err
		}
		runs = append(runs, run)
		linkedSession, err := s.GetSession(ctx, binding.SessionID)
		if err != nil {
			return domain.ThreadExport{}, err
		}
		sessions = append(sessions, domain.ThreadSession{ID: linkedSession.ID,
			WorkspaceID: linkedSession.WorkspaceID, Title: linkedSession.Title,
			Route: linkedSession.Route, Status: linkedSession.Status,
			CreatedAt: linkedSession.CreatedAt, UpdatedAt: linkedSession.UpdatedAt})
	}
	messages, err := s.ListThreadMessagesPage(ctx, threadRecord.ID, true, 0, 0)
	if err != nil {
		return domain.ThreadExport{}, err
	}
	events, err := s.ListThreadEvents(ctx, threadRecord.ID, 0)
	if err != nil {
		return domain.ThreadExport{}, err
	}
	auditEvents, err := s.listThreadRunAuditEvents(ctx, threadRecord.ID)
	if err != nil {
		return domain.ThreadExport{}, err
	}
	return domain.ThreadExport{ProtocolVersion: domain.ThreadExportProtocolVersion,
		ExportedAt: time.Now().UTC(), Thread: threadRecord, Mission: mission,
		Bindings: bindings, Runs: runs, Sessions: sessions, Messages: messages, Events: events,
		AuditEvents: auditEvents}, nil
}

func scanThread(row scanner) (domain.Thread, error) {
	var item domain.Thread
	var status, created, updated string
	var archived, deleted sql.NullString
	if err := row.Scan(&item.ID, &item.ProtocolVersion, &item.WorkspaceID,
		&item.MissionID, &item.Title, &status, &item.ActiveRunID, &item.LastRunID,
		&item.Version, &created, &updated, &archived, &deleted); err != nil {
		return domain.Thread{}, err
	}
	item.Status = domain.ThreadStatus(status)
	item.CreatedAt = parseTS(created)
	item.UpdatedAt = parseTS(updated)
	item.ArchivedAt = parseNullableTS(archived)
	item.DeletedAt = parseNullableTS(deleted)
	return item, item.Validate()
}
