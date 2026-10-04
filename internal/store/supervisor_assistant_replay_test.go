package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
)

// The latest-schema fixture chain must remove this empty additive schema
// before C175. Refuse a downgrade carrying any native replay rather than
// deleting it or making a historical fixture with a gap in its ledger.
func removeSchemaV176ForTestStatements() []string {
	statements := []string{
		`CREATE TEMP TABLE legacy_fixture_empty_ordinary_replay(n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_ordinary_replay SELECT count(*) FROM run_supervisor_assistant_replay;`,
		`INSERT INTO legacy_fixture_empty_ordinary_replay SELECT count(*) FROM run_supervisor_assistant_replay_bindings;`,
		`DROP TABLE legacy_fixture_empty_ordinary_replay;`,
	}
	statements = append(statements, removeSchemaV177ForTestStatements()...)
	return append(statements, []string{
		`DROP TABLE run_supervisor_assistant_replay_bindings;`,
		`DROP TABLE run_supervisor_assistant_replay;`,
		`DELETE FROM schema_migrations WHERE version=176;`,
	}...)
}

func TestSchemaV176UpgradesV175WithoutInventingAssistantReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v175-assistant.db")
	st := openHistoricalTestDatabase(t, path, 177)

	f := providerReplayFixtureAtStore(t, st)
	legacy, err := st.SaveSessionMessage(t.Context(), session.NewMessage(f.turn.Run.SessionID, "assistant", "accepted old public answer"))
	if err != nil {
		t.Fatal(err)
	}
	var checksum string
	if err := st.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=175`).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV176ForTestStatements() {
		if _, err := st.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var actual string
	if err := st.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=175`).Scan(&actual); err != nil || actual != checksum {
		t.Fatal("migration changed C175 checksum", err)
	}
	for _, table := range []string{"run_supervisor_assistant_replay", "run_supervisor_assistant_replay_bindings"} {
		var count int
		if err := st.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("upgrade invented native assistant state", err)
		}
	}
	messages, err := st.ListSessionMessages(t.Context(), f.turn.Run.SessionID, true)
	if err != nil || len(messages) != 1 || messages[0].ID != legacy.ID || messages[0].Provenance != legacy.Provenance || messages[0].Content != legacy.Content {
		t.Fatal("upgrade rewrote old accepted history", err)
	}
	if _, err := st.LoadSupervisorAssistantHistory(t.Context(), f.turn.Checkpoint, []int64{legacy.ID}); err == nil {
		t.Fatal("upgraded old answer acquired native reasoning")
	}
}

func TestSchemaV176FixtureRefusesPrivateReplayBeforeMainSchemaMutation(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomeSuccess
	response, _ := ordinaryKimiStoreResponse(t, f)
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, response); err != nil {
		t.Fatal(err)
	}
	before, err := sqliteSchemaDigest(t.Context(), f.store.db)
	if err != nil {
		t.Fatal(err)
	}
	rejected := false
	for _, statement := range removeSchemaV176ForTestStatements() {
		if _, err := f.store.db.Exec(statement); err != nil {
			rejected = true
			break
		}
	}
	after, err := sqliteSchemaDigest(t.Context(), f.store.db)
	if err != nil || !rejected || before != after {
		t.Fatal("fixture downgrade deleted native evidence", err)
	}
	if version, err := f.store.SchemaVersion(t.Context()); err != nil || version != LatestSchemaVersion {
		t.Fatal("fixture downgrade changed the migration ledger", err)
	}
}

func testKimiStoreReplay(t *testing.T, response llm.ChatResponse, nativeID, reason string) *llm.ProviderReplay {
	t.Helper()
	parts := []any{map[string]any{"kind": "kimi_metadata", "id": llm.StableStreamID("kimi-native-replay", nativeID, "metadata", "0"),
		"opaque": map[string]any{"wire_model": "kimi-k3", "upstream_model": "upstream-k3-snapshot", "content_state": "string", "reasoning_content": reason}}}
	if response.Text != "" {
		parts = append(parts, map[string]any{"kind": "text", "id": llm.StableStreamID("kimi-native-replay", nativeID, "text", "0"), "text": response.Text})
	}
	calls := make([]any, 0, len(response.ToolCalls))
	for index, call := range response.ToolCalls {
		var arguments any
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			t.Fatal("invalid test call")
		}
		canonical, _ := json.Marshal(arguments)
		digest := sha256.Sum256(canonical)
		calls = append(calls, map[string]any{"wire_id": fmt.Sprintf("native-tool-%d", index), "durable_id": call.ID, "name": call.Name, "payload_sha256": hex.EncodeToString(digest[:])})
		parts = append(parts, map[string]any{"kind": "kimi_tool", "id": llm.StableStreamID("kimi-native-replay", nativeID, "tool", fmt.Sprint(index)), "call_index": index})
	}
	raw, _ := json.Marshal(map[string]any{"version": 4, "provider": response.Provider, "model": response.Model, "transport": "openai_chat_completions",
		"binding": strings.Repeat("a", 64), "response_id": nativeID, "parts": parts, "calls": calls})
	replay, err := llm.DecodeProviderReplay(raw)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

func ordinaryKimiStoreResponse(t *testing.T, f *providerReplayFixture) (llm.ChatResponse, domain.RootAction) {
	t.Helper()
	action := domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: "Accepted public reply"}
	raw, _ := json.Marshal(action)
	response := f.response
	response.Text, response.ToolCalls = string(raw), nil
	response.Replay = testKimiStoreReplay(t, response, "native-ordinary", "private-K3-store-sentinel \n雪")
	return response, action
}

func TestKimiOrdinaryAssistantReplayAtomicCandidateAndAcceptedBinding(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomeSuccess
	response, action := ordinaryKimiStoreResponse(t, f)
	if _, err := f.store.db.Exec(`CREATE TRIGGER inject_ordinary_candidate_failure BEFORE INSERT ON run_supervisor_assistant_replay BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, response); err == nil {
		t.Fatal("candidate write failure was accepted")
	}
	list, err := f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil || countRunEventType(list, events.ModelCompletedEvent) != 0 {
		t.Fatal("candidate failure left a model success", err)
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER inject_ordinary_candidate_failure`); err != nil {
		t.Fatal(err)
	}
	cp, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, f.attempt, response); err != nil {
		t.Fatal("exact model terminal replay failed", err)
	}
	for _, variant := range []string{"removed", "reason", "text"} {
		changed := response
		switch variant {
		case "removed":
			changed.Replay = nil
		case "reason":
			changed.Replay = testKimiStoreReplay(t, changed, "native-ordinary", "different-private-reason")
		case "text":
			changed.Text = "different accepted output"
		}
		if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, f.attempt, changed); err == nil {
			t.Fatal("changed terminal replay accepted", variant)
		}
	}
	if _, err := f.store.db.Exec(`CREATE TRIGGER inject_ordinary_binding_failure BEFORE INSERT ON run_supervisor_assistant_replay_bindings BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.store.CompleteSupervisorTurn(t.Context(), cp, response, action, policy.Decision{Allowed: true}, 0); err == nil {
		t.Fatal("binding write failure was accepted")
	}
	messages, err := f.store.ListSessionMessages(t.Context(), f.turn.Run.SessionID, false)
	if err != nil || len(messages) != 0 {
		t.Fatal("binding failure left public messages", err)
	}
	list, err = f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil || countRunEventType(list, events.AgentTurnCompletedEvent) != 0 {
		t.Fatal("binding failure completed the turn", err)
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER inject_ordinary_binding_failure`); err != nil {
		t.Fatal(err)
	}
	_, completed, saved, err := f.store.CompleteSupervisorTurn(t.Context(), cp, response, action, policy.Decision{Allowed: true}, 0)
	if err != nil || saved.Assistant.Content != action.Message || completed.NextTurn != cp.NextTurn+1 {
		t.Fatal("native/public binding failed", err)
	}
	if _, _, _, err := f.store.CompleteSupervisorTurn(t.Context(), cp, response, action, policy.Decision{Allowed: true}, 0); err != nil {
		t.Fatal("exact completion replay failed", err)
	}
	changed := response
	changed.Replay = nil
	if _, _, _, err := f.store.CompleteSupervisorTurn(t.Context(), cp, changed, action, policy.Decision{Allowed: true}, 0); err == nil {
		t.Fatal("completion replay removed private history")
	}
	changedAction := action
	changedAction.Message = "another public answer"
	if _, _, _, err := f.store.CompleteSupervisorTurn(t.Context(), cp, response, changedAction, policy.Decision{Allowed: true}, 0); err == nil {
		t.Fatal("completion replay changed the native projection")
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.store.BeginSupervisorTurn(t.Context(), f.lease, "fresh follow-up")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := f.store.LoadSupervisorAssistantHistory(t.Context(), next.Checkpoint, []int64{saved.Assistant.ID})
	if err != nil || len(loaded) != 1 || len(loaded[saved.Assistant.ID].Rounds) != 0 {
		t.Fatal("ordinary replay did not survive reopen/new user turn", err)
	}
	want, _ := response.Replay.EncodeForStore()
	got, _ := loaded[saved.Assistant.ID].Replay.EncodeForStore()
	if string(want) != string(got) || loaded[saved.Assistant.ID].Replay.AssistantText() != response.Text {
		t.Fatal("reopen changed native ordinary response")
	}
	publicMessages, _ := f.store.ListSessionMessages(t.Context(), f.turn.Run.SessionID, true)
	list, _ = f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	public, _ := json.Marshal([]any{publicMessages, list, loaded})
	if strings.Contains(string(public)+fmt.Sprintf("%+v", loaded), "private-K3-store-sentinel") || strings.Contains(string(public), "native-ordinary") {
		t.Fatal("ordinary private replay leaked through public history, events, or formatting")
	}
	stale := next.Checkpoint
	stale.LeaseGeneration++
	if _, err := f.store.LoadSupervisorAssistantHistory(t.Context(), stale, []int64{saved.Assistant.ID}); err == nil {
		t.Fatal("stale lease loaded historical private state")
	}
	if _, err := f.store.db.Exec(`UPDATE run_supervisor_assistant_replay SET model='changed'`); err == nil {
		t.Fatal("ordinary candidate was mutable")
	}
	if _, err := f.store.db.Exec(`DELETE FROM run_supervisor_assistant_replay_bindings`); err == nil {
		t.Fatal("ordinary binding could be removed")
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER trg_supervisor_assistant_replay_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE run_supervisor_assistant_replay SET replay_sha256=?`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LoadSupervisorAssistantHistory(t.Context(), next.Checkpoint, []int64{saved.Assistant.ID}); err == nil {
		t.Fatal("corrupt historical state was loaded")
	}
}

func TestKimiOrdinaryAssistantReplayRejectsUnacceptedLegacyAndStoppedState(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomeSuccess
	response, action := ordinaryKimiStoreResponse(t, f)
	cp, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, response)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := f.store.SaveSessionMessage(t.Context(), session.NewMessage(f.turn.Run.SessionID, "assistant", "unbound old answer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LoadSupervisorAssistantHistory(t.Context(), cp, []int64{legacy.ID}); err == nil {
		t.Fatal("unaccepted legacy projection acquired native state")
	}
	changed := response
	changed.Text = action.Message
	if _, _, _, err := f.store.CompleteSupervisorTurn(t.Context(), cp, changed, action, policy.Decision{Allowed: true}, 0); err == nil {
		t.Fatal("public text replaced the original native JSON")
	}
	if _, _, _, err := f.store.CompleteSupervisorTurn(t.Context(), cp, response, action, policy.Decision{Allowed: false}, 0); err == nil {
		t.Fatal("policy-denied answer bound private history")
	}
	if _, err := application.NewRunService(f.store).Cancel(t.Context(), f.turn.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LoadSupervisorAssistantHistory(t.Context(), cp, []int64{legacy.ID}); err == nil {
		t.Fatal("stopped run loaded private history")
	}
	var bindings int
	if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM run_supervisor_assistant_replay_bindings`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatal("unaccepted candidate became accepted history", err)
	}
}

func TestKimiOrdinaryAssistantReplaySQLiteRejectsMissingNativeEnvelope(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomeSuccess
	response, _ := ordinaryKimiStoreResponse(t, f)
	response.Replay = nil
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, response); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"version":4,"transport":"openai_chat_completions","provider":"test","model":"model","calls":null}`} {
		if _, err := f.store.db.Exec(`INSERT INTO run_supervisor_assistant_replay (run_id,turn,attempt_id,model_attempt,tool_round,provider,model,replay_blob,replay_sha256,created_at) VALUES (?,?,?,?,0,'test','model',?,?,?)`,
			f.turn.Run.ID, f.turn.Checkpoint.NextTurn, f.turn.Checkpoint.AttemptID, f.attempt.Number, []byte(raw), strings.Repeat("a", 64), ts(f.turn.Checkpoint.UpdatedAt)); err == nil {
			t.Fatal("missing native fields bypassed SQLite source guard")
		}
	}
}
