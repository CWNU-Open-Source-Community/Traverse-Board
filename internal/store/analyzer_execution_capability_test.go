package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/analyzer"
	"cyberagent-workbench/internal/events"
)

func TestAnalyzerExecutionCapabilityIsAtomicAppendOnlyAndAudited(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "analyzer-execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	session, _, _, _ := browserLaunchStoreFixture(t, state)
	candidate := analyzerExecutionCandidateFixture(t, "analyzer-execution-request-1")
	token := bytes.Repeat([]byte{0x31}, analyzer.AnalyzerExecutionCapabilityTokenBytes)
	now := time.Now().UTC().Round(time.Millisecond)
	capability, code := analyzer.BuildAnalyzerExecutionCapability("analyzer-capability-store-1",
		session.RunID, session.WorkspaceID, candidate, token, now, now.Add(time.Minute))
	if code != "" {
		t.Fatal(code)
	}
	stored, replayed, err := state.RegisterAnalyzerExecutionCapability(ctx, capability)
	if err != nil || replayed || stored.Fingerprint != capability.Fingerprint {
		t.Fatalf("register capability: replay=%t stored=%+v err=%v", replayed, stored, err)
	}
	if _, replayed, err := state.RegisterAnalyzerExecutionCapability(ctx, capability); err != nil || !replayed {
		t.Fatalf("exact registration replay failed: replay=%t err=%v", replayed, err)
	}
	consumption, err := state.ConsumeAnalyzerExecutionCapability(ctx, capability.ID,
		"analyzer-consumption-store-1", token, candidate, now.Add(time.Second))
	if err != nil || !consumption.Atomic || !consumption.ReplayGuardEnforced {
		t.Fatalf("consume capability: %+v err=%v", consumption, err)
	}
	if _, err := state.ConsumeAnalyzerExecutionCapability(ctx, capability.ID,
		"analyzer-consumption-store-replay", token, candidate, now.Add(2*time.Second)); err == nil {
		t.Fatal("consumed capability replay unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE analyzer_execution_capabilities
		SET request_id = 'tampered' WHERE id = ?`, capability.ID); err == nil {
		t.Fatal("capability update unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM analyzer_execution_consumptions
		WHERE capability_id = ?`, capability.ID); err == nil {
		t.Fatal("consumption delete unexpectedly passed")
	}
	timeline, err := state.ListRunEvents(ctx, session.RunID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range timeline {
		counts[event.Type]++
		if (event.Type == events.AnalyzerExecutionCapabilityIssuedEvent ||
			event.Type == events.AnalyzerExecutionCapabilityConsumedEvent) &&
			(strings.Contains(event.PayloadJSON, base64.RawURLEncoding.EncodeToString(token)) ||
				strings.Contains(event.PayloadJSON, `"content_base64"`)) {
			t.Fatalf("analyzer audit event leaked bearer or input: %s", event.PayloadJSON)
		}
	}
	if counts[events.AnalyzerExecutionCapabilityIssuedEvent] != 1 ||
		counts[events.AnalyzerExecutionCapabilityConsumedEvent] != 1 {
		t.Fatalf("unexpected analyzer event counts: %+v", counts)
	}
}

func TestAnalyzerExecutionCapabilityConcurrentConsumeHasOneWinner(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "analyzer-execution-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	session, _, _, _ := browserLaunchStoreFixture(t, state)
	candidate := analyzerExecutionCandidateFixture(t, "analyzer-execution-request-race")
	token := bytes.Repeat([]byte{0x52}, analyzer.AnalyzerExecutionCapabilityTokenBytes)
	now := time.Now().UTC().Round(time.Millisecond)
	capability, _ := analyzer.BuildAnalyzerExecutionCapability("analyzer-capability-store-race",
		session.RunID, session.WorkspaceID, candidate, token, now, now.Add(time.Minute))
	if _, _, err := state.RegisterAnalyzerExecutionCapability(ctx, capability); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if _, err := state.ConsumeAnalyzerExecutionCapability(ctx, capability.ID,
				"analyzer-consumption-race-"+string(rune('a'+index)), token, candidate,
				now.Add(time.Second)); err == nil {
				winners.Add(1)
			}
		}(index)
	}
	wait.Wait()
	if winners.Load() != 1 {
		t.Fatalf("expected exactly one consumer, got %d", winners.Load())
	}
}

func TestSchemaV94UpgradesV93AnalyzerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "analyzer-v93-upgrade.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV94ForTestStatements() {
		if _, err := state.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("downgrade v94 with %q: %v", statement, err)
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

func removeSchemaV94ForTestStatements() []string {
	return append(removeSchemaV95ForTestStatements(), []string{
		`DROP TRIGGER trg_analyzer_execution_consumption_delete_immutable`,
		`DROP TRIGGER trg_analyzer_execution_consumption_update_immutable`,
		`DROP TRIGGER trg_analyzer_execution_capability_delete_immutable`,
		`DROP TRIGGER trg_analyzer_execution_capability_update_immutable`,
		`DROP TRIGGER trg_analyzer_execution_consumption_insert`,
		`DROP TRIGGER trg_analyzer_execution_capability_insert`,
		`DROP TABLE analyzer_execution_consumptions`,
		`DROP INDEX idx_analyzer_execution_capabilities_run_issued`,
		`DROP TABLE analyzer_execution_capabilities`,
		`DELETE FROM schema_migrations WHERE version = 94`,
	}...)
}

func analyzerExecutionCandidateFixture(t *testing.T, requestID string) analyzer.InvocationCandidate {
	t.Helper()
	request := analyzer.Request{
		ProtocolVersion: analyzer.RequestProtocolVersion, RequestID: requestID,
		Analyzer: analyzer.FixtureAnalyzerName,
		Input: analyzer.Input{MediaType: "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("Prayu analyzer fixture\n"))},
		Limits: analyzer.Limits{MaxInputBytes: analyzer.MaxDecodedInputBytes,
			MaxOutputBytes: 4096, TimeoutMilliseconds: 5000},
		MetadataOnly: true,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	candidate, code := analyzer.BuildInvocationCandidate(raw)
	if code != "" {
		t.Fatal(code)
	}
	return candidate
}
