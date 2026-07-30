package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	terminalruntime "cyberagent-workbench/internal/terminal"
)

func (s *SQLiteStore) RecordDebugTerminalAgentInputAudit(
	ctx context.Context,
	record terminalruntime.AgentInputAuditRecord,
) error {
	if s == nil || s.db == nil {
		return apperror.New(apperror.CodeFailedPrecondition,
			"debug terminal Agent-input audit store is unavailable")
	}
	if err := record.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"debug terminal Agent-input audit record is invalid", err)
	}
	eventType, err := debugTerminalAgentInputEventType(record.Kind)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	run, mission, err := getCoordinatorRunTx(ctx, tx, record.RunID)
	if err != nil {
		return err
	}
	if run.MissionID != record.MissionID ||
		run.SessionID != record.SessionID ||
		mission.WorkspaceID != record.WorkspaceID {
		return apperror.New(apperror.CodeConflict,
			"debug terminal Agent-input audit identities are stale")
	}
	if err := validateDebugTerminalAgentInputSnapshotBinding(
		ctx, tx, record); err != nil {
		return err
	}
	payload := map[string]any{
		"protocol":                   record.ProtocolVersion,
		"kind":                       record.Kind,
		"workspace_id":               record.WorkspaceID,
		"terminal_session_id":        record.TerminalSessionID,
		"interaction_snapshot_id":    record.InteractionSnapshotID,
		"interaction_revision":       record.InteractionRevision,
		"execution_profile_revision": record.ExecutionProfileRevision,
		"permission_snapshot_id":     record.PermissionSnapshotID,
		"permission_revision":        record.PermissionRevision,
		"permission_mode":            record.PermissionMode,
		"requested_by":               record.RequestedBy,
		"operation_digest":           record.OperationDigest,
		"data_sha256":                record.DataSHA256,
		"data_bytes":                 record.DataBytes,
		"bytes_written":              record.BytesWritten,
		"process_local":              record.ProcessLocal,
		"token_persisted":            record.TokenPersisted,
		"token_exposed":              record.TokenExposed,
		"raw_input_persisted":        record.RawInputPersisted,
		"automatic_retry_allowed":    record.AutomaticRetryAllowed,
	}
	event, err := events.New(record.RunID, record.MissionID, eventType,
		"debug_terminal_agent_input", record.BindingID, payload)
	if err != nil {
		return err
	}
	event.EventID = "evt-" + record.ID
	event.CreatedAt = record.CreatedAt
	if existing, found, err := getRunEventByEventID(ctx, tx, event.EventID); err != nil {
		return err
	} else if found {
		if !sameDebugTerminalAgentInputEvent(existing, event) {
			return apperror.New(apperror.CodeConflict,
				"debug terminal Agent-input audit id was reused")
		}
		return tx.Commit()
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func validateDebugTerminalAgentInputSnapshotBinding(
	ctx context.Context,
	tx *sql.Tx,
	record terminalruntime.AgentInputAuditRecord,
) error {
	interaction, err := getRunExecutionInteractionSnapshot(
		ctx, tx, record.InteractionSnapshotID)
	if err != nil {
		return apperror.Wrap(apperror.CodeConflict,
			"debug terminal Agent-input interaction binding is stale", err)
	}
	permission, err := getRunExecutionPermissionSnapshot(
		ctx, tx, record.PermissionSnapshotID)
	if err != nil {
		return apperror.Wrap(apperror.CodeConflict,
			"debug terminal Agent-input permission binding is stale", err)
	}
	profile, err := scanRunExecutionProfileSnapshot(tx.QueryRowContext(ctx,
		runExecutionProfileSnapshotSelect+
			` WHERE run_id = ? AND revision = ?`,
		record.RunID, record.ExecutionProfileRevision))
	if err != nil {
		return apperror.Wrap(apperror.CodeConflict,
			"debug terminal Agent-input profile binding is stale", err)
	}
	if interaction.RunID != record.RunID ||
		interaction.MissionID != record.MissionID ||
		interaction.Revision != record.InteractionRevision ||
		interaction.ExecutionProfileRevision !=
			record.ExecutionProfileRevision ||
		interaction.Mode != domain.RunExecutionInteractionDebug ||
		interaction.Surface != domain.ExecutionSurfaceCode ||
		interaction.ExecutionProfile != domain.RunExecutionProfileLocal ||
		interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted ||
		interaction.CommandForm != domain.ExecutionCommandUserConPTY ||
		!interaction.PersistentTerminal || interaction.AgentInputDefault ||
		profile.RunID != record.RunID ||
		profile.MissionID != record.MissionID ||
		profile.Revision != record.ExecutionProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		permission.RunID != record.RunID ||
		permission.MissionID != record.MissionID ||
		permission.Revision != record.PermissionRevision ||
		permission.Mode != record.PermissionMode ||
		permission.Mode != domain.RunExecutionPermissionDebug ||
		!permission.PersistentTerminal || !permission.BackgroundProcess ||
		!permission.AgentTerminalInput {
		return apperror.New(apperror.CodeConflict,
			"debug terminal Agent-input snapshot binding is inconsistent")
	}
	return nil
}

func debugTerminalAgentInputEventType(
	kind terminalruntime.AgentInputAuditKind,
) (string, error) {
	switch kind {
	case terminalruntime.AgentInputAuditGranted:
		return events.DebugTerminalAgentInputGrantedEvent, nil
	case terminalruntime.AgentInputAuditPrepared:
		return events.DebugTerminalAgentInputPreparedEvent, nil
	case terminalruntime.AgentInputAuditCompleted:
		return events.DebugTerminalAgentInputCompletedEvent, nil
	case terminalruntime.AgentInputAuditRevoked:
		return events.DebugTerminalAgentInputRevokedEvent, nil
	default:
		return "", apperror.New(apperror.CodeInvalidArgument,
			"debug terminal Agent-input audit kind is unsupported")
	}
}

func getRunEventByEventID(ctx context.Context, tx *sql.Tx,
	eventID string,
) (events.Event, bool, error) {
	event, err := scanRunEvent(tx.QueryRowContext(ctx, `SELECT id, event_id,
		version, run_id, mission_id, sequence, type, source, subject_id,
		payload_json, created_at FROM run_events WHERE event_id = ?`,
		strings.TrimSpace(eventID)))
	if errors.Is(err, sql.ErrNoRows) {
		return events.Event{}, false, nil
	}
	return event, err == nil, err
}

func sameDebugTerminalAgentInputEvent(left events.Event,
	right events.Event,
) bool {
	var leftPayload, rightPayload any
	if json.Unmarshal([]byte(left.PayloadJSON), &leftPayload) != nil ||
		json.Unmarshal([]byte(right.PayloadJSON), &rightPayload) != nil {
		return false
	}
	leftEncoded, leftErr := json.Marshal(leftPayload)
	rightEncoded, rightErr := json.Marshal(rightPayload)
	return leftErr == nil && rightErr == nil &&
		left.EventID == right.EventID &&
		left.Version == right.Version &&
		left.RunID == right.RunID &&
		left.MissionID == right.MissionID &&
		left.Type == right.Type &&
		left.Source == right.Source &&
		left.SubjectID == right.SubjectID &&
		string(leftEncoded) == string(rightEncoded) &&
		left.CreatedAt.Equal(right.CreatedAt)
}
