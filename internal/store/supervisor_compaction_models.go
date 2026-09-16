package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/redact"
)

func supervisorCompactionHash(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	} // Only concrete, JSON-safe store value types reach this helper.
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// The complete persisted source set is stable across accounting and owner
// renewal/takeover. Changing a tail preference cannot buy a second model call
// for the same source snapshot. The generator separately binds its input window.
func supervisorCompactionSourceHash(snapshot supervisorCompactionSnapshot) string {
	return supervisorCompactionHash(struct {
		RunID, SessionID, WorkspaceID, PendingInput string
		RunConfigJSON, MissionGoal                  string
		OperatorInputSequence                       int64
		PendingImages, PendingAttachments           int
		HasSummary                                  bool
		Summary                                     contextmgr.Summary
		Messages                                    []contextmgr.Message
	}{snapshot.Checkpoint.RunID, snapshot.SessionID, snapshot.WorkspaceID, snapshot.Checkpoint.PendingInput,
		snapshot.RunConfigJSON, snapshot.MissionGoal,
		snapshot.OperatorInputSequence,
		snapshot.Checkpoint.PendingImageCount, snapshot.Checkpoint.PendingAttachmentCount,
		snapshot.HasSummary, snapshot.Summary, snapshot.contextMessages()})
}

func requireSupervisorCompactionAccountingTx(ctx context.Context, tx *sql.Tx, checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt) (domain.Run, domain.SupervisorCheckpoint, error) {
	var run domain.Run
	var current domain.SupervisorCheckpoint
	if attempt.Purpose != llm.ModelPurposeContextCompaction && !(attempt.Purpose == "" && attempt.SupervisorAttemptID != "") {
		return run, current, errors.New("late accounting requires a bound Supervisor monetary identity")
	}
	if err := attempt.ValidateStarted(); err != nil {
		return run, current, err
	}
	subject := supervisorModelSubject(checkpoint, attempt.Number)
	if err := requireSupervisorModelStartedMatchTx(ctx, tx, checkpoint.RunID, subject, attempt); err != nil {
		return run, current, err
	}
	var encoded string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE run_id=? AND source='model_gateway' AND type=? AND subject_id=?`, checkpoint.RunID, events.ModelStartedEvent, subject).Scan(&encoded); err != nil {
		return run, current, err
	}
	start, err := parseSupervisorModelStartedPayload(encoded)
	if err != nil {
		return run, current, err
	}
	if start.LeaseID != checkpoint.LeaseID || start.LeaseGeneration != checkpoint.LeaseGeneration || start.Turn != checkpoint.NextTurn || start.AttemptID != checkpoint.AttemptID {
		return run, current, apperror.New(apperror.CodeConflict, "late compaction receipt does not match its original owner")
	}
	current, err = scanSupervisorCheckpoint(tx.QueryRowContext(ctx, supervisorCheckpointSelect, checkpoint.RunID))
	if err != nil {
		return run, current, err
	}
	run, err = scanRun(tx.QueryRowContext(ctx, `SELECT id,mission_id,session_id,status,config_json,budget_json,started_at,finished_at,created_at,updated_at FROM runs WHERE id=?`, checkpoint.RunID))
	return run, current, err
}

func addSupervisorCompactionIdentity(payload map[string]any, attempt llm.ModelAttempt) {
	if attempt.Purpose == llm.ModelPurposeContextCompaction {
		payload["purpose"] = attempt.Purpose
		payload["compaction_source_sha256"] = attempt.CompactionSourceSHA256
		payload["monetary_attempt_number"] = attempt.MonetaryAttemptNumber()
	}
}

func (s *SQLiteStore) NextSupervisorCompactionAttempt(ctx context.Context, checkpoint domain.SupervisorCheckpoint) (int, error) {
	if err := checkpoint.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	_, current, err := requireActiveSupervisorAttemptTx(ctx, tx, checkpoint)
	if err != nil {
		return 0, err
	}
	if current.RepairPhase != domain.ProtocolRepairNone {
		return 0, apperror.New(apperror.CodeFailedPrecondition, "context compaction cannot run during protocol repair")
	}
	var count int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events WHERE run_id=? AND type=? AND source='model_gateway' AND subject_id LIKE ?`, checkpoint.RunID, events.ModelStartedEvent, supervisorModelSubjectPrefix(checkpoint)+"%").Scan(&count)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count + 1, nil
}

func requireNewSupervisorCompactionSourceTx(ctx context.Context, tx *sql.Tx, checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt) error {
	snapshot, err := readSupervisorCompactionSnapshot(ctx, tx, checkpoint)
	if err != nil {
		return err
	}
	if supervisorCompactionSourceHash(snapshot) != attempt.CompactionSourceSHA256 {
		return apperror.New(apperror.CodeConflict, "context compaction source changed before model start")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events WHERE run_id=? AND source='model_gateway' AND type=? AND json_extract(payload_json,'$.purpose')=? AND json_extract(payload_json,'$.compaction_source_sha256')=?`, checkpoint.RunID, events.ModelStartedEvent, llm.ModelPurposeContextCompaction, attempt.CompactionSourceSHA256).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return apperror.New(apperror.CodeConflict, "context compaction source already has a durable model attempt")
	}
	return nil
}

// The auxiliary response is a receipt, never an assistant item or tool round.
// Even an invalid model schema is recorded here before candidate validation.
func (s *SQLiteStore) RecordSupervisorCompactionCompleted(ctx context.Context, checkpoint domain.SupervisorCheckpoint,
	attempt llm.ModelAttempt, response llm.ChatResponse,
) (domain.SupervisorCheckpoint, int64, error) {
	attempt = sanitizeModelAttempt(attempt)
	if attempt.Purpose != llm.ModelPurposeContextCompaction {
		return domain.SupervisorCheckpoint{}, 0, apperror.New(apperror.CodeInvalidArgument, "compaction completion requires its source-bound purpose")
	}
	if err := attempt.ValidateCompleted(); err != nil {
		return domain.SupervisorCheckpoint{}, 0, err
	}
	invalidUsage := false
	if _, _, _, err := supervisorUsage(response.Usage); err != nil {
		invalidUsage = true
		response.Usage = llm.Usage{}
	}
	invalidOutput := len(response.ToolCalls) != 0 || len(response.Text) > llm.MaxModelOutputBytes
	for _, item := range response.Items {
		if item.Type == llm.StreamItemToolCall || item.CallID != "" || item.ToolName != "" {
			invalidOutput = true
		}
	}
	if len(response.Text) > llm.MaxModelOutputBytes {
		response.Text = ""
	}
	elapsedMillis, err := supervisorModelElapsedMillis(attempt.Elapsed)
	if err != nil {
		return domain.SupervisorCheckpoint{}, 0, err
	}
	// Valid JSON stays structural through event redaction; applying the secret
	// regexp to serialized JSON would turn escaped newlines into text tokens.
	var responseValue any = redact.String(response.Text)
	if json.Valid([]byte(response.Text)) {
		responseValue = json.RawMessage(response.Text)
	}
	payload := map[string]any{"turn": checkpoint.NextTurn, "attempt_id": checkpoint.AttemptID,
		"model_attempt": attempt.Number, "transport_attempt": attempt.TransportNumber(), "max_attempts": attempt.MaxAttempts,
		"protocol_repair": 0, "tool_round": 0, "provider": attempt.Provider, "model": attempt.Model,
		"outcome": attempt.Outcome, "elapsed_millis": elapsedMillis, "usage": response.Usage,
		"compaction_response": responseValue, "tool_call_count": 0,
		"compaction_output_rejected": invalidOutput, "compaction_usage_invalid": invalidUsage,
		"usage_unknown": (response.Usage.InputTokens == 0 && response.Usage.OutputTokens == 0 && response.Usage.TotalTokens == 0) || response.Usage.TotalTokens-response.Usage.InputTokens > response.Usage.OutputTokens}
	addSupervisorCompactionIdentity(payload, attempt)
	current, err := s.recordSupervisorModelTerminal(ctx, checkpoint, attempt, events.ModelCompletedEvent, payload, supervisorModelTerminalOptions{Usage: &response.Usage})
	if err != nil {
		return current, 0, err
	}
	var sequence int64
	err = s.db.QueryRowContext(ctx, `SELECT sequence FROM run_events WHERE run_id=? AND source='model_gateway' AND subject_id=? AND type=?`, checkpoint.RunID, supervisorModelSubject(checkpoint, attempt.Number), events.ModelCompletedEvent).Scan(&sequence)
	if err == nil && invalidUsage {
		err = fmt.Errorf("%w: compaction usage was invalid; receipt retained as unknown", contextmgr.ErrSummaryGenerationAborted)
	}
	return current, sequence, err
}

func requireSupervisorCompactionTerminalReplayTx(ctx context.Context, tx *sql.Tx, runID, subject, eventType string, expected map[string]any) error {
	var encoded string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE run_id=? AND source='model_gateway' AND subject_id=? AND type=?`, runID, subject, eventType).Scan(&encoded); err != nil {
		return err
	}
	var actual map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &actual); err != nil {
		return err
	}
	// The two checkpoint proofs are assigned only at the first atomic terminal.
	delete(actual, "checkpoint_before_sha256")
	delete(actual, "checkpoint_after_sha256")
	want, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	redactedWant, err := redactJSONPayload(string(want))
	if err != nil {
		return err
	}
	want = []byte(redactedWant)
	var comparable map[string]json.RawMessage
	if err := json.Unmarshal(want, &comparable); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, comparable) {
		return apperror.New(apperror.CodeConflict, "compaction completion differs from its durable receipt")
	}
	return nil
}

type supervisorCompactionReceipt struct {
	Sequence       int64
	Subject        string
	EventType      string
	Started        supervisorModelStartedPayload
	BeforeSHA      string
	AfterSHA       string
	Text           string
	UsageUnknown   bool
	OutputRejected bool
}

func readSupervisorCompactionReceipt(ctx context.Context, reader supervisorCompactionReader, runID, sourceSHA string) (supervisorCompactionReceipt, bool, error) {
	var receipt supervisorCompactionReceipt
	var startJSON string
	err := reader.QueryRowContext(ctx, `SELECT subject_id,payload_json FROM run_events WHERE run_id=? AND source='model_gateway' AND type=? AND json_extract(payload_json,'$.purpose')=? AND json_extract(payload_json,'$.compaction_source_sha256')=? ORDER BY sequence LIMIT 1`, runID, events.ModelStartedEvent, llm.ModelPurposeContextCompaction, sourceSHA).Scan(&receipt.Subject, &startJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt, false, nil
	}
	if err != nil {
		return receipt, false, err
	}
	receipt.Started, err = parseSupervisorModelStartedPayload(startJSON)
	if err != nil {
		return receipt, true, err
	}
	var terminalJSON string
	err = reader.QueryRowContext(ctx, `SELECT sequence,type,payload_json FROM run_events WHERE run_id=? AND source='model_gateway' AND subject_id=? AND type IN (?,?) ORDER BY sequence LIMIT 1`, runID, receipt.Subject, events.ModelCompletedEvent, events.ModelFailedEvent).Scan(&receipt.Sequence, &receipt.EventType, &terminalJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt, true, nil
	}
	if err != nil {
		return receipt, true, err
	}
	var terminal struct {
		Purpose        string          `json:"purpose"`
		SourceSHA      string          `json:"compaction_source_sha256"`
		Provider       string          `json:"provider"`
		Model          string          `json:"model"`
		Number         int             `json:"model_attempt"`
		BeforeSHA      string          `json:"checkpoint_before_sha256"`
		AfterSHA       string          `json:"checkpoint_after_sha256"`
		Text           json.RawMessage `json:"compaction_response"`
		UsageUnknown   bool            `json:"usage_unknown"`
		OutputRejected bool            `json:"compaction_output_rejected"`
	}
	if err := json.Unmarshal([]byte(terminalJSON), &terminal); err != nil {
		return receipt, true, err
	}
	if terminal.Purpose != llm.ModelPurposeContextCompaction || terminal.SourceSHA != sourceSHA || terminal.Provider != receipt.Started.Provider || terminal.Model != receipt.Started.Model || terminal.Number != receipt.Started.ModelAttempt || !validStoreDigest(terminal.BeforeSHA) || !validStoreDigest(terminal.AfterSHA) {
		return receipt, true, apperror.New(apperror.CodeFailedPrecondition, "compaction terminal is not bound to its source start")
	}
	receipt.BeforeSHA, receipt.AfterSHA, receipt.Text, receipt.UsageUnknown = terminal.BeforeSHA, terminal.AfterSHA, string(terminal.Text), terminal.UsageUnknown
	receipt.OutputRejected = terminal.OutputRejected
	return receipt, true, nil
}

func verifySupervisorGeneratedResponse(receipt supervisorCompactionReceipt, runID, attemptID, sourceSHA string, response contextmgr.SummaryGenerationResponse) error {
	want := contextmgr.SummaryGenerationReceipt{RunID: runID, AttemptID: attemptID, ModelAttempt: receipt.Started.ModelAttempt, CompletionSequence: receipt.Sequence, Provider: receipt.Started.Provider, Model: receipt.Started.Model, SourceSHA256: sourceSHA}
	if receipt.EventType != events.ModelCompletedEvent || response.Receipt != want || receipt.OutputRejected {
		return errors.New("generated handoff receipt does not match its durable completion")
	}
	text, err := contextmgr.ParseSummaryGenerationText(receipt.Text)
	if err != nil || strings.TrimSpace(redact.String(response.Text)) != strings.TrimSpace(text) {
		return fmt.Errorf("generated handoff text does not match its recorded model response: %w", err)
	}
	return nil
}
