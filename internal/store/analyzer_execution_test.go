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
	if version, err := upgraded.SchemaVersion(t.Context()); err != nil || version != 95 {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}

func removeSchemaV95ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_analyzer_execution_delete_immutable`,
		`DROP TRIGGER trg_analyzer_execution_update_immutable`,
		`DROP TRIGGER trg_analyzer_execution_insert`,
		`DROP INDEX idx_analyzer_executions_run_created`,
		`DROP TABLE analyzer_executions`,
		`DELETE FROM schema_migrations WHERE version = 95`,
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
