package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

const standardCodeSupervisorLedgerSelect = `SELECT id, operation_key_digest,
	request_fingerprint, kind, decision, tool_call_id, tool_name, tool_action,
	tool_kind, intent_fingerprint, evidence_fingerprint, result_status, error_code,
	from_state, to_state, reason_code, snapshot_json, event_sequence, created_at
	FROM standard_code_supervisor_ledger`

func (s *SQLiteStore) GetStandardCodeSupervisorSnapshot(ctx context.Context,
	runID string,
) (domain.StandardCodeSupervisorSnapshot, bool, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) {
		return domain.StandardCodeSupervisorSnapshot{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code Supervisor Run id is invalid")
	}
	entry, err := scanStandardCodeSupervisorLedgerEntry(s.db.QueryRowContext(ctx,
		standardCodeSupervisorLedgerSelect+` WHERE run_id = ?
			ORDER BY snapshot_version DESC LIMIT 1`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StandardCodeSupervisorSnapshot{}, false, nil
	}
	if err != nil {
		return domain.StandardCodeSupervisorSnapshot{}, false, err
	}
	return entry.Snapshot, true, nil
}

func (s *SQLiteStore) ListStandardCodeSupervisorLedger(ctx context.Context,
	runID string, limit int,
) ([]domain.StandardCodeSupervisorLedgerEntry, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || limit <= 0 ||
		limit > domain.StandardCodeSupervisorMaximumLedgerEntries {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code Supervisor ledger query is invalid")
	}
	rows, err := s.db.QueryContext(ctx, standardCodeSupervisorLedgerSelect+
		` WHERE run_id = ? ORDER BY snapshot_version ASC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]domain.StandardCodeSupervisorLedgerEntry, 0)
	for rows.Next() {
		entry, scanErr := scanStandardCodeSupervisorLedgerEntry(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) AppendStandardCodeSupervisorLedger(ctx context.Context,
	expectedVersion int64, entry domain.StandardCodeSupervisorLedgerEntry,
) (domain.StandardCodeSupervisorLedgerEntry, bool, error) {
	if expectedVersion < 0 || entry.Snapshot.Version != expectedVersion+1 ||
		entry.EventSequence != 0 {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code Supervisor ledger version is invalid")
	}
	probe := entry
	probe.EventSequence = 1
	if err := probe.Validate(); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "Standard Code Supervisor ledger entry is invalid", err)
	}
	if entry.LeaseID == "" || entry.LeaseGeneration <= 0 {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "Standard Code Supervisor ledger requires an active execution lease")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, getErr := getStandardCodeSupervisorLedgerByOperation(ctx, tx,
		entry.OperationKeyDigest); getErr != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, getErr
	} else if found {
		if existing.RequestFingerprint != entry.RequestFingerprint ||
			existing.Kind != entry.Kind || existing.Snapshot.RunID != entry.Snapshot.RunID {
			return domain.StandardCodeSupervisorLedgerEntry{}, false, apperror.New(
				apperror.CodeConflict, "Standard Code Supervisor operation key was used for different intent")
		}
		if err := tx.Commit(); err != nil {
			return domain.StandardCodeSupervisorLedgerEntry{}, false, err
		}
		return existing, true, nil
	}
	var latestVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(snapshot_version), 0)
		FROM standard_code_supervisor_ledger WHERE run_id = ?`, entry.Snapshot.RunID).
		Scan(&latestVersion); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	if latestVersion != expectedVersion {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, apperror.New(
			apperror.CodeConflict, "Standard Code Supervisor state changed concurrently")
	}
	if err := requireStandardCodeSupervisorBindingTx(ctx, tx, entry); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	eventType := standardCodeSupervisorEventType(entry)
	event, err := events.New(entry.Snapshot.RunID, entry.Snapshot.MissionID, eventType,
		"standard_code_supervisor", entry.ID, map[string]any{
			"protocol_version": entry.Snapshot.ProtocolVersion,
			"kind":             entry.Kind, "decision": entry.Decision,
			"from_state": entry.FromState, "to_state": entry.ToState,
			"reason_code": entry.ReasonCode, "tool_name": entry.ToolName,
			"tool_kind": entry.ToolKind, "turn": entry.Snapshot.Turn,
			"snapshot_version":           entry.Snapshot.Version,
			"tool_rounds":                entry.Snapshot.TotalToolRounds,
			"commands_used":              entry.Snapshot.CommandsUsed,
			"jobs_started":               entry.Snapshot.JobsStarted,
			"fix_rounds":                 entry.Snapshot.FixRounds,
			"mutation_epoch":             entry.Snapshot.MutationEpoch,
			"verified_mutation_epoch":    entry.Snapshot.VerifiedMutationEpoch,
			"capability_refresh_pending": entry.Snapshot.ExpectedCapabilityGeneration != "",
			"output_bytes":               entry.Snapshot.OutputBytes,
			"stop_reason":                entry.Snapshot.StopReason,
			"instruction_authorized":     false, "capability_grant": false,
		})
	if err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	event.CreatedAt = entry.CreatedAt.UTC()
	inserted, err := insertRunEventTx(ctx, tx, event)
	if err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	entry.EventSequence = inserted.Sequence
	if err := entry.Validate(); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	snapshotJSON, err := json.Marshal(entry.Snapshot)
	if err != nil || len(snapshotJSON) > domain.StandardCodeSupervisorMaximumSnapshotBytes {
		if err == nil {
			err = errors.New("Standard Code Supervisor snapshot exceeded its durable bound")
		}
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO standard_code_supervisor_ledger
		(id, operation_key_digest, request_fingerprint, protocol_version, run_id,
		 mission_id, workspace_id, root_agent_id, preset_operation_key_digest, turn,
		 attempt_id, kind, decision, tool_call_id, tool_name, tool_action, tool_kind,
		 intent_fingerprint, evidence_fingerprint, result_status, error_code,
		 from_state, to_state, reason_code, snapshot_version, snapshot_json,
		 event_sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.OperationKeyDigest, entry.RequestFingerprint,
		entry.Snapshot.ProtocolVersion, entry.Snapshot.RunID, entry.Snapshot.MissionID,
		entry.Snapshot.WorkspaceID, entry.Snapshot.RootAgentID,
		entry.Snapshot.PresetOperationKeyDigest, entry.Snapshot.Turn,
		entry.Snapshot.AttemptID, entry.Kind, entry.Decision, entry.ToolCallID,
		entry.ToolName, entry.ToolAction, entry.ToolKind, entry.IntentFingerprint,
		entry.EvidenceFingerprint, entry.ResultStatus, entry.ErrorCode,
		entry.FromState, entry.ToState, entry.ReasonCode, entry.Snapshot.Version,
		string(snapshotJSON), entry.EventSequence, ts(entry.CreatedAt))
	if err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	return entry, false, nil
}

func requireStandardCodeSupervisorBindingTx(ctx context.Context, tx *sql.Tx,
	entry domain.StandardCodeSupervisorLedgerEntry,
) error {
	snapshot := entry.Snapshot
	preset, err := scanStandardCodePresetOperation(tx.QueryRowContext(ctx,
		standardCodePresetOperationSelect+` WHERE operation_key_digest = ? AND status = 'configured'`,
		snapshot.PresetOperationKeyDigest))
	if err != nil {
		return err
	}
	if preset.RunID != snapshot.RunID || preset.MissionID != snapshot.MissionID ||
		preset.WorkspaceID != snapshot.WorkspaceID {
		return apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code Supervisor preset binding changed")
	}
	if err := requireRunExecutionLeaseTx(ctx, tx, snapshot.RunID, entry.LeaseID,
		entry.LeaseGeneration); err != nil {
		return err
	}
	checkpoint, found, err := getSupervisorCheckpointTx(ctx, tx, snapshot.RunID)
	if err != nil || !found {
		return errors.Join(err, apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code Supervisor checkpoint is missing"))
	}
	if checkpoint.Phase != domain.SupervisorTurnStarted ||
		checkpoint.NextTurn != snapshot.Turn || checkpoint.AttemptID != snapshot.AttemptID ||
		checkpoint.LeaseID != entry.LeaseID ||
		checkpoint.LeaseGeneration != entry.LeaseGeneration {
		return apperror.New(apperror.CodeConflict,
			"Standard Code Supervisor turn generation changed")
	}
	var modeID, profileID, interactionID, permissionID, browserCDPID string
	var modeRevision, profileRevision, interactionRevision, permissionRevision,
		browserCDPRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT id, revision FROM run_mode_snapshots
		WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, snapshot.RunID).
		Scan(&modeID, &modeRevision); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id, revision FROM run_execution_profile_snapshots
		WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, snapshot.RunID).
		Scan(&profileID, &profileRevision); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id, revision FROM run_execution_interaction_snapshots
		WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, snapshot.RunID).
		Scan(&interactionID, &interactionRevision); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id, revision FROM run_execution_permission_snapshots
		WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, snapshot.RunID).
		Scan(&permissionID, &permissionRevision); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id, revision FROM run_browser_cdp_permission_snapshots
		WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, snapshot.RunID).
		Scan(&browserCDPID, &browserCDPRevision); err != nil {
		return err
	}
	if modeID != snapshot.ModeSnapshotID || modeRevision != snapshot.ModeRevision ||
		profileID != snapshot.ProfileSnapshotID || profileRevision != snapshot.ProfileRevision ||
		interactionID != snapshot.InteractionSnapshotID ||
		interactionRevision != snapshot.InteractionRevision ||
		permissionID != snapshot.PermissionSnapshotID ||
		permissionRevision != snapshot.PermissionRevision ||
		browserCDPID != snapshot.BrowserCDPSnapshotID ||
		browserCDPRevision != snapshot.BrowserCDPRevision {
		return apperror.New(apperror.CodeConflict,
			"Standard Code Supervisor authority snapshot changed")
	}
	budget, _, err := loadRunBudgetTx(ctx, tx, snapshot.RunID)
	if err != nil {
		return err
	}
	if budget.MaxTokens != snapshot.RunTokenLimit ||
		budget.TimeoutSeconds*1000 != snapshot.RunTimeoutMillis ||
		budget.MaxToolCalls != snapshot.RunToolCallLimit {
		return apperror.New(apperror.CodeConflict,
			"Standard Code Supervisor Run budget changed")
	}
	return nil
}

func standardCodeSupervisorEventType(entry domain.StandardCodeSupervisorLedgerEntry) string {
	if entry.ToState == domain.StandardCodeSupervisorStopped ||
		entry.Kind == domain.StandardCodeSupervisorStoppedRecord {
		return events.StandardCodeSupervisorStoppedEvent
	}
	switch entry.Kind {
	case domain.StandardCodeSupervisorCallAuthorized:
		return events.StandardCodeSupervisorAuthorizedEvent
	case domain.StandardCodeSupervisorCallDenied:
		return events.StandardCodeSupervisorDeniedEvent
	case domain.StandardCodeSupervisorCallReplayed:
		return events.StandardCodeSupervisorReplayedEvent
	case domain.StandardCodeSupervisorCallObserved,
		domain.StandardCodeSupervisorRoundObserved,
		domain.StandardCodeSupervisorActionRecorded:
		return events.StandardCodeSupervisorObservedEvent
	default:
		return events.StandardCodeSupervisorPreparedEvent
	}
}

func getStandardCodeSupervisorLedgerByOperation(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, digest string) (domain.StandardCodeSupervisorLedgerEntry, bool, error) {
	entry, err := scanStandardCodeSupervisorLedgerEntry(queryer.QueryRowContext(ctx,
		standardCodeSupervisorLedgerSelect+` WHERE operation_key_digest = ?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, nil
	}
	if err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, false, err
	}
	return entry, true, nil
}

func scanStandardCodeSupervisorLedgerEntry(row scanner) (
	domain.StandardCodeSupervisorLedgerEntry, error,
) {
	var entry domain.StandardCodeSupervisorLedgerEntry
	var snapshotJSON, createdAt string
	if err := row.Scan(&entry.ID, &entry.OperationKeyDigest, &entry.RequestFingerprint,
		&entry.Kind, &entry.Decision, &entry.ToolCallID, &entry.ToolName,
		&entry.ToolAction, &entry.ToolKind, &entry.IntentFingerprint,
		&entry.EvidenceFingerprint, &entry.ResultStatus, &entry.ErrorCode,
		&entry.FromState, &entry.ToState, &entry.ReasonCode, &snapshotJSON,
		&entry.EventSequence, &createdAt); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{}, err
	}
	if len(snapshotJSON) == 0 || len(snapshotJSON) >
		domain.StandardCodeSupervisorMaximumSnapshotBytes ||
		json.Unmarshal([]byte(snapshotJSON), &entry.Snapshot) != nil {
		return domain.StandardCodeSupervisorLedgerEntry{},
			errors.New("stored Standard Code Supervisor snapshot is invalid")
	}
	entry.CreatedAt = parseTS(createdAt)
	if err := entry.Validate(); err != nil {
		return domain.StandardCodeSupervisorLedgerEntry{},
			fmt.Errorf("validate stored Standard Code Supervisor ledger entry: %w", err)
	}
	return entry, nil
}
