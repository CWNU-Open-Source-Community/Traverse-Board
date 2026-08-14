package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/analyzer"
	"cyberagent-workbench/internal/events"
)

func TestAnalyzerExecutionCommitIsAtomicAppendOnlyAndIdempotent(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "analyzer-commit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	session, _, _, _ := browserLaunchStoreFixture(t, state)
	rawRequest, candidate := analyzerExecutionRequestFixture(t, "analyzer-commit-request")
	token := bytes.Repeat([]byte{0x73}, analyzer.AnalyzerExecutionCapabilityTokenBytes)
	now := time.Now().UTC().Round(time.Millisecond)
	capability, code := analyzer.BuildAnalyzerExecutionCapability("analyzer-capability-commit",
		session.RunID, session.WorkspaceID, candidate, token, now, now.Add(time.Minute))
	if code != "" {
		t.Fatal(code)
	}
	if _, _, err := state.RegisterAnalyzerExecutionCapability(ctx, capability); err != nil {
		t.Fatal(err)
	}
	consumption, err := state.ConsumeAnalyzerExecutionCapability(ctx, capability.ID,
		"analyzer-consumption-commit", token, candidate, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	executed, code := analyzer.ExecuteEmbeddedWASI(ctx, rawRequest)
	if code != "" {
		t.Fatal(code)
	}
	request := analyzer.AnalyzerExecutionCommitRequest{
		ID: "analyzer-execution-commit", RunID: session.RunID, SessionID: session.SessionID,
		WorkspaceID: session.WorkspaceID, CapabilityID: capability.ID,
		ConsumptionID: consumption.ID, RequestedBy: "store-test", Candidate: candidate,
		Execution: executed.Execution, RawResult: executed.RawResult, CreatedAt: now.Add(2 * time.Second),
	}
	record, descriptor, replayed, err := state.CommitAnalyzerExecution(ctx, request)
	if err != nil || replayed || record.ArtifactID != descriptor.ID ||
		record.ResultSHA256 != descriptor.SHA256 || !record.ArtifactAtomic {
		t.Fatalf("commit result=%+v descriptor=%+v replay=%t err=%v", record, descriptor, replayed, err)
	}
	blob, err := state.GetRunArtifact(ctx, descriptor.ID)
	if err != nil || blob.Content != string(executed.RawResult) || blob.ToolName != embeddedAnalyzerToolName {
		t.Fatalf("artifact=%+v err=%v", blob, err)
	}
	replayedRecord, replayedDescriptor, replayed, err := state.CommitAnalyzerExecution(ctx, request)
	if err != nil || !replayed || !reflect.DeepEqual(record, replayedRecord) ||
		replayedDescriptor.ID != descriptor.ID {
		t.Fatalf("idempotent replay record=%+v descriptor=%+v replay=%t err=%v",
			replayedRecord, replayedDescriptor, replayed, err)
	}
	changed := request
	changed.RequestedBy = "changed-operator"
	if _, _, _, err := state.CommitAnalyzerExecution(ctx, changed); err == nil {
		t.Fatal("changed execution replay unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE analyzer_executions
		SET requested_by = 'tampered' WHERE id = ?`, record.ID); err == nil {
		t.Fatal("analyzer execution update unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM analyzer_executions WHERE id = ?`, record.ID); err == nil {
		t.Fatal("analyzer execution delete unexpectedly passed")
	}
	loaded, loadedArtifact, found, err := state.GetAnalyzerExecution(ctx, record.ID)
	if err != nil || !found || !reflect.DeepEqual(loaded, record) || loadedArtifact.ID != descriptor.ID {
		t.Fatalf("load record=%+v artifact=%+v found=%t err=%v", loaded, loadedArtifact, found, err)
	}
	timeline, err := state.ListRunEvents(ctx, session.RunID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range timeline {
		counts[event.Type]++
		if event.Type == events.AnalyzerExecutionCompletedEvent &&
			(strings.Contains(event.PayloadJSON, "content_base64") ||
				strings.Contains(event.PayloadJSON, base64.RawURLEncoding.EncodeToString(token))) {
			t.Fatalf("execution event leaked input or bearer: %s", event.PayloadJSON)
		}
	}
	if counts[events.AnalyzerExecutionCompletedEvent] != 1 || counts[events.ArtifactCreatedEvent] != 1 {
		t.Fatalf("unexpected event counts: %+v", counts)
	}
}

func TestSchemaV95UpgradesV94AnalyzerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "analyzer-v94-upgrade.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV95ForTestStatements() {
		if _, err := state.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("downgrade v95 with %q: %v", statement, err)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(t.Context()); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}

func removeSchemaV95ForTestStatements() []string {
	return append(removeSchemaV96ForTestStatements(), []string{
		`DROP TRIGGER trg_analyzer_execution_delete_immutable`,
		`DROP TRIGGER trg_analyzer_execution_update_immutable`,
		`DROP TRIGGER trg_analyzer_execution_insert`,
		`DROP INDEX idx_analyzer_executions_run_created`,
		`DROP TABLE analyzer_executions`,
		`DELETE FROM schema_migrations WHERE version = 95`,
	}...)
}

func removeSchemaV96ForTestStatements() []string {
	return append(removeSchemaV97ForTestStatements(), []string{
		`DROP TRIGGER trg_host_command_result_delete_immutable`,
		`DROP TRIGGER trg_host_command_result_update_immutable`,
		`DROP TRIGGER trg_host_command_intent_delete_immutable`,
		`DROP TRIGGER trg_host_command_intent_update_immutable`,
		`DROP TRIGGER trg_host_command_review_delete_immutable`,
		`DROP TRIGGER trg_host_command_review_update_immutable`,
		`DROP TRIGGER trg_host_command_proposal_operation_delete_immutable`,
		`DROP TRIGGER trg_host_command_proposal_operation_update_immutable`,
		`DROP TRIGGER trg_host_command_proposal_delete_immutable`,
		`DROP TRIGGER trg_host_command_proposal_update_immutable`,
		`DROP TRIGGER trg_host_command_result_insert_binding`,
		`DROP TRIGGER trg_host_command_intent_insert_binding`,
		`DROP TRIGGER trg_host_command_review_insert_binding`,
		`DROP TRIGGER trg_host_command_proposal_operation_insert_binding`,
		`DROP TRIGGER trg_host_command_proposal_insert_binding`,
		`DROP TABLE host_command_proposal_results`,
		`DROP TABLE host_command_proposal_execution_intents`,
		`DROP INDEX idx_host_command_reviews_run_created`,
		`DROP TABLE host_command_proposal_reviews`,
		`DROP TABLE host_command_proposal_operations`,
		`DROP INDEX idx_host_command_proposals_run_created`,
		`DROP TABLE host_command_proposals`,
		`DROP TRIGGER trg_supervisor_tool_call_model_attempt`,
		`DROP TRIGGER trg_supervisor_tool_round_completion`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v96`,
		`CREATE TABLE run_supervisor_tool_calls (
			run_id TEXT NOT NULL,
			turn INTEGER NOT NULL,
			attempt_id TEXT NOT NULL,
			round INTEGER NOT NULL,
			position INTEGER NOT NULL,
			model_attempt INTEGER NOT NULL,
			call_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL,
			result_json TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			completed_at TEXT,
			PRIMARY KEY(run_id, turn, attempt_id, round, position),
			UNIQUE(run_id, turn, attempt_id, call_id),
			FOREIGN KEY(run_id, turn, attempt_id, round)
				REFERENCES run_supervisor_tool_rounds(run_id, turn, attempt_id, round) ON DELETE CASCADE,
			CHECK(position BETWEEN 1 AND 4),
			CHECK(model_attempt > 0),
			CHECK(tool_name IN ('work_item_create', 'note_create',
				'specialist_delegation_propose', 'plan_delivery_propose')),
			CHECK(status IN ('pending', 'completed', 'denied', 'failed')),
			CHECK((status = 'pending' AND result_json = '' AND error_code = '' AND completed_at IS NULL)
				OR (status = 'completed' AND length(result_json) > 0 AND error_code = '' AND completed_at IS NOT NULL)
				OR (status IN ('denied', 'failed') AND length(result_json) > 0 AND length(error_code) > 0
					AND completed_at IS NOT NULL))
		)`,
		`INSERT INTO run_supervisor_tool_calls
			(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
			payload_json, status, result_json, error_code, created_at, completed_at)
			SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
			payload_json, status, result_json, error_code, created_at, completed_at
			FROM run_supervisor_tool_calls_v96`,
		`DROP TABLE run_supervisor_tool_calls_v96`,
		`CREATE INDEX idx_run_supervisor_tool_calls_pending
			ON run_supervisor_tool_calls(run_id, turn, attempt_id, status, round, position)`,
		`CREATE TRIGGER trg_supervisor_tool_call_model_attempt
			BEFORE INSERT ON run_supervisor_tool_calls
			WHEN NOT EXISTS (
				SELECT 1 FROM run_supervisor_tool_rounds
				WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
					AND round = NEW.round AND model_attempt = NEW.model_attempt
			)
			BEGIN SELECT RAISE(ABORT, 'supervisor tool call model attempt mismatch'); END`,
		`CREATE TRIGGER trg_supervisor_tool_round_completion
			BEFORE UPDATE OF completed_at ON run_supervisor_tool_rounds
			WHEN NEW.completed_at IS NOT NULL AND EXISTS (
				SELECT 1 FROM run_supervisor_tool_calls
				WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
					AND round = NEW.round AND status = 'pending'
			)
			BEGIN SELECT RAISE(ABORT, 'supervisor tool round still has pending calls'); END`,
		`DELETE FROM schema_migrations WHERE version = 96`,
	}...)
}

func removeSchemaV97ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_update_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_transition_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_transition_update_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_update_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_lease_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_intent_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_intent_update_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_transition_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_lease_update`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_lease_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_intent_insert`,
		`DROP TABLE sandbox_docker_lifecycle_cleanup_receipts`,
		`DROP INDEX idx_sandbox_docker_lifecycle_transitions_single_checkpoint`,
		`DROP INDEX idx_sandbox_docker_lifecycle_transitions_latest`,
		`DROP TABLE sandbox_docker_lifecycle_transitions`,
		`DROP TABLE sandbox_docker_lifecycle_actions`,
		`DROP INDEX idx_sandbox_docker_lifecycle_leases_status_expiry`,
		`DROP TABLE sandbox_docker_lifecycle_leases`,
		`DROP INDEX idx_sandbox_docker_lifecycle_intents_run_created`,
		`DROP TABLE sandbox_docker_lifecycle_intents`,
		`DELETE FROM schema_migrations WHERE version = 97`,
	}
}

func analyzerExecutionRequestFixture(t *testing.T, requestID string) ([]byte, analyzer.InvocationCandidate) {
	t.Helper()
	request := analyzer.Request{
		ProtocolVersion: analyzer.RequestProtocolVersion, RequestID: requestID,
		Analyzer: analyzer.FixtureAnalyzerName,
		Input: analyzer.Input{MediaType: "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("Prayu embedded analyzer\n"))},
		Limits: analyzer.Limits{MaxInputBytes: analyzer.MaxDecodedInputBytes,
			MaxOutputBytes: 4096, TimeoutMilliseconds: 5000}, MetadataOnly: true,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	candidate, code := analyzer.BuildInvocationCandidate(raw)
	if code != "" {
		t.Fatal(code)
	}
	return raw, candidate
}
