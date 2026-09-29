package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

type providerReplayFixture struct {
	store    *SQLiteStore
	path     string
	turn     domain.SupervisorTurn
	lease    domain.RunExecutionLease
	attempt  llm.ModelAttempt
	response llm.ChatResponse
}

func newProviderReplayFixture(t *testing.T) *providerReplayFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-replay.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := providerReplayFixtureAtStore(t, st)
	f.path = path
	t.Cleanup(func() { _ = f.store.Close() })
	return f
}

func providerReplayFixtureAtStore(t *testing.T, st *SQLiteStore) *providerReplayFixture {
	t.Helper()
	ctx := t.Context()
	_, run := createStructuredToolTestRun(t, ctx, st, "private provider replay")
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
	turn, err := st.BeginSupervisorTurn(ctx, lease, "continue tools")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool, json.RawMessage(`{"title":"evidence","content":"saved"}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := runmutation.SupervisorToolCallID(runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn, "note_create", string(payload)), 1)
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: id, Name: "note_create", Arguments: payload}
	attempt := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: 1, TransportAttempt: 1, MaxAttempts: 3, Provider: "test", Model: "model", InputEstimate: 1000}
	return &providerReplayFixture{store: st, turn: turn, lease: lease, attempt: attempt, response: llm.ChatResponse{Provider: "test", Model: "model", ToolCalls: []llm.ToolCall{call}, Usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}}
}

func testProviderReplay(t *testing.T, calls []llm.ToolCall, opaque string) *llm.ProviderReplay {
	t.Helper()
	var values any
	if err := json.Unmarshal(calls[0].Arguments, &values); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	raw, err := json.Marshal(map[string]any{
		"version": 1, "provider": "test", "model": "model", "transport": "openai_responses", "binding": strings.Repeat("a", 64),
		"parts": []any{map[string]any{"kind": "reasoning", "id": "rs_1", "opaque": map[string]any{"id": "rs_1", "type": "reasoning", "summary": []any{}, "encrypted_content": opaque}}, map[string]any{"kind": "tool", "id": "fc_1", "call_index": 0}},
		"calls": []any{map[string]any{"wire_id": "wire_1", "durable_id": calls[0].ID, "name": calls[0].Name, "payload_sha256": hex.EncodeToString(digest[:])}},
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := llm.DecodeProviderReplay(raw)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

func (f *providerReplayFixture) start(t *testing.T) {
	t.Helper()
	if _, err := f.store.RecordSupervisorModelStarted(t.Context(), f.turn.Checkpoint, f.attempt); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorProviderReplayAtomicPrivateAndDurable(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomeSuccess
	f.response.Replay = testProviderReplay(t, f.response.ToolCalls, "private-opaque-sentinel")
	if _, err := f.store.db.Exec(`CREATE TRIGGER inject_provider_replay_failure BEFORE INSERT ON run_supervisor_provider_replay BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, f.response); err == nil {
		t.Fatal("private write failure accepted")
	}
	rounds, err := f.store.ListSupervisorToolRounds(t.Context(), f.turn.Checkpoint)
	if err != nil || len(rounds) != 0 {
		t.Fatalf("private failure left tool work: %d %v", len(rounds), err)
	}
	eventList, err := f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil || countRunEventType(eventList, events.ModelCompletedEvent) != 0 {
		t.Fatal("private failure left successful terminal", err)
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER inject_provider_replay_failure`); err != nil {
		t.Fatal(err)
	}
	cp, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, f.response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, f.response); err != nil {
		t.Fatal("exact replay rejected", err)
	}
	changed := f.response
	changed.Replay = testProviderReplay(t, changed.ToolCalls, "changed-opaque")
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, f.attempt, changed); err == nil {
		t.Fatal("changed opaque terminal replay accepted")
	}
	changed.Replay = nil
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, f.attempt, changed); err == nil {
		t.Fatal("removed opaque terminal replay accepted")
	}
	rounds, err = f.store.ListRunSupervisorToolRoundsPage(t.Context(), f.turn.Run.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(rounds)
	eventList, err = f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventJSON, _ := json.Marshal(eventList)
	if strings.Contains(string(public)+string(eventJSON), "private-opaque-sentinel") || strings.Contains(string(public)+string(eventJSON), "wire_1") {
		t.Fatal("private replay leaked into public projection")
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := f.store.LoadSupervisorProviderReplay(t.Context(), cp)
	if err != nil || len(loaded) != 1 || loaded[1].ValidateToolCalls(f.response.ToolCalls) != nil {
		t.Fatalf("reopen lost replay: count=%d err=%v", len(loaded), err)
	}
	got, _ := loaded[1].EncodeForStore()
	want, _ := f.response.Replay.EncodeForStore()
	if string(got) != string(want) {
		t.Fatal("private replay bytes changed")
	}
	stale := cp
	stale.LeaseGeneration++
	if _, err := f.store.LoadSupervisorProviderReplay(t.Context(), stale); err == nil {
		t.Fatal("stale lease read private replay")
	}
	if _, err := f.store.db.Exec(`UPDATE run_supervisor_provider_replay SET model='other'`); err == nil {
		t.Fatal("private ledger mutation allowed")
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER trg_supervisor_provider_replay_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE run_supervisor_provider_replay SET replay_sha256=?`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LoadSupervisorProviderReplay(t.Context(), cp); err == nil {
		t.Fatal("corrupt replay loaded")
	}
}

func TestSupervisorProviderReplayRejectsWrongBatchAndSource(t *testing.T) {
	for _, mutation := range []string{"payload", "source", "no_tools"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProviderReplayFixture(t)
			f.start(t)
			f.attempt.Outcome = llm.OutcomeSuccess
			f.response.Replay = testProviderReplay(t, f.response.ToolCalls, "opaque")
			switch mutation {
			case "payload":
				calls := append([]llm.ToolCall(nil), f.response.ToolCalls...)
				calls[0].Arguments = json.RawMessage(`{"content":"different","title":"evidence"}`)
				f.response.Replay = testProviderReplay(t, calls, "opaque")
			case "source":
				raw, _ := f.response.Replay.EncodeForStore()
				raw = []byte(strings.Replace(string(raw), `"provider":"test"`, `"provider":"other"`, 1))
				var err error
				f.response.Replay, err = llm.DecodeProviderReplay(raw)
				if err != nil {
					t.Fatal(err)
				}
			case "no_tools":
				f.response.ToolCalls = nil
				f.response.Text = "done"
			}
			if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, f.response); err == nil {
				t.Fatal("invalid private replay accepted")
			}
			rounds, err := f.store.ListSupervisorToolRounds(t.Context(), f.turn.Checkpoint)
			if err != nil || len(rounds) != 0 {
				t.Fatal("invalid replay left executable tools", err)
			}
		})
	}
}

func TestSupervisorContextRecoveryClaimRequiresExactFailureAndPersists(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomePermanent
	f.attempt.FailureReason = llm.ProviderFailureReason("context_limit")
	f.attempt.ErrorText = "bounded context failure"
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), f.turn.Checkpoint, f.attempt); err == nil {
		t.Fatal("unrecorded failure claimed recovery")
	}
	cp, err := f.store.RecordSupervisorModelFailedWithUsage(t.Context(), f.turn.Checkpoint, f.attempt, llm.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`CREATE TRIGGER inject_context_claim_failure BEFORE INSERT ON run_supervisor_context_recoveries BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err == nil {
		t.Fatal("claim failure ignored")
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER inject_context_claim_failure`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err != nil || !claimed {
		t.Fatalf("valid recovery rejected: %t %v", claimed, err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err != nil || claimed {
		t.Fatalf("reopen reset recovery allowance: %t %v", claimed, err)
	}
	stale := cp
	stale.LeaseGeneration++
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), stale, f.attempt); err == nil {
		t.Fatal("stale owner claimed recovery")
	}
	wrong := f.attempt
	wrong.ToolRound++
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, wrong); err == nil {
		t.Fatal("another tool phase claimed old failure")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.store.ClaimSupervisorContextRecovery(cancelled, cp, f.attempt); err == nil {
		t.Fatal("cancelled context claimed recovery")
	}
}

func TestSupervisorContextRecoveryCannotUpgradeUntypedFailure(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome = llm.OutcomePermanent
	f.attempt.ErrorText = "context_limit context window exceeded"
	cp, err := f.store.RecordSupervisorModelFailed(t.Context(), f.turn.Checkpoint, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	f.attempt.FailureReason = llm.ProviderFailureReason("context_limit")
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err == nil {
		t.Fatal("untrusted error prose granted context recovery")
	}
}

func TestSupervisorContextRecoveryRestartsOnlyOneTransportGroup(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.attempt.MaxAttempts = 1
	f.start(t)
	f.attempt.Outcome = llm.OutcomePermanent
	f.attempt.FailureReason = llm.ProviderFailureReason("context_limit")
	f.attempt.ErrorText = "context is too large"
	cp, err := f.store.RecordSupervisorModelFailed(t.Context(), f.turn.Checkpoint, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	global, transport, err := f.store.NextSupervisorModelAttempt(t.Context(), cp, 0, 0)
	if err != nil || global != 2 || transport != 2 {
		t.Fatalf("failure without claim reset retries: %d %d %v", global, transport, err)
	}
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err != nil || !claimed {
		t.Fatal("could not claim recovery", err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	global, transport, err = f.store.NextSupervisorModelAttempt(t.Context(), cp, 0, 0)
	if err != nil || global != 2 || transport != 1 {
		t.Fatalf("claim transport group was not durable: %d %d %v", global, transport, err)
	}
	second := f.attempt
	second.Number, second.TransportAttempt, second.Outcome, second.FailureReason, second.ErrorText = global, transport, "", "", ""
	second.InputEstimate = f.attempt.InputEstimate - 1
	if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, second); err != nil {
		t.Fatal("recovered start rejected", err)
	}
	second.Outcome, second.FailureReason, second.ErrorText = llm.OutcomePermanent, llm.ProviderFailureReason("context_limit"), "still too large"
	cp, err = f.store.RecordSupervisorModelFailed(t.Context(), cp, second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, second); err != nil || claimed {
		t.Fatalf("second failure granted recovery: %t %v", claimed, err)
	}
	global, transport, err = f.store.NextSupervisorModelAttempt(t.Context(), cp, 0, 0)
	if err != nil || global != 3 || transport != 2 {
		t.Fatalf("second failure reset transport cap: %d %d %v", global, transport, err)
	}
}

func TestSupervisorContextRecoveryLateAccountingCannotClaimNewTurn(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	if _, err := f.store.FailSupervisorTurn(t.Context(), f.turn.Checkpoint, "owner no longer receives this attempt", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(f.store).Resume(t.Context(), f.turn.Run.ID); err != nil {
		t.Fatal(err)
	}
	next, err := f.store.BeginSupervisorTurn(t.Context(), f.lease, "new turn")
	if err != nil {
		t.Fatal(err)
	}
	f.attempt.Outcome, f.attempt.FailureReason, f.attempt.ErrorText = llm.OutcomePermanent, llm.ProviderFailureReason("context_limit"), "late context limit"
	if _, err := f.store.RecordSupervisorModelFailedWithUsage(t.Context(), f.turn.Checkpoint, f.attempt, llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, 0); err != nil {
		t.Fatal("late accounting was lost", err)
	}
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), next.Checkpoint, f.attempt); err == nil {
		t.Fatal("late accounting claimed new turn recovery")
	}
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), f.turn.Checkpoint, f.attempt); err == nil {
		t.Fatal("closed old turn claimed recovery")
	}
	var claims int
	if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM run_supervisor_context_recoveries`).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("late accounting inserted allowance: %d %v", claims, err)
	}
}

func TestSupervisorContextRecoveryCannotClaimAcrossLeaseTakeover(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.attempt.Outcome, f.attempt.FailureReason, f.attempt.ErrorText = llm.OutcomePermanent, llm.ProviderFailureReason("context_limit"), "context limit"
	if _, err := f.store.RecordSupervisorModelFailed(t.Context(), f.turn.Checkpoint, f.attempt); err != nil {
		t.Fatal(err)
	}
	expireTestRunExecutionLease(t, t.Context(), f.store, f.lease)
	lease := acquireTestRunExecutionLease(t, t.Context(), f.store, f.turn.Run.ID)
	recovered, err := f.store.BeginSupervisorTurn(t.Context(), lease, "continue tools")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Checkpoint.AttemptID != f.turn.Checkpoint.AttemptID || recovered.Checkpoint.LeaseGeneration == f.turn.Checkpoint.LeaseGeneration {
		t.Fatal("fixture did not take over same checkpoint")
	}
	if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), recovered.Checkpoint, f.attempt); err == nil {
		t.Fatal("new owner claimed old lease failure")
	}
	var claims int
	if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM run_supervisor_context_recoveries`).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("lease takeover created allowance: %d %v", claims, err)
	}
}
