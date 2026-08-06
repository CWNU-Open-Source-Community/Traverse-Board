package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/analyzer"
	"cyberagent-workbench/internal/events"
)

func TestAnalyzerStartControlPersistsReplayGuardFakeLifecycleAndAudit(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "analyzer-start-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	session, _, _, _ := browserLaunchStoreFixture(t, state)
	now := time.Now().UTC().Round(time.Millisecond)
	request := analyzerStartRequestStoreFixture(t, session.RunID, session.WorkspaceID,
		"analyzer-start-fake", analyzer.AnalyzerStartAdapterFake, strings.Repeat("1", 64), now,
		now.Add(time.Hour))
	stored, replayed, err := state.RegisterAnalyzerStartRequest(ctx, request)
	if err != nil || replayed || stored.Fingerprint != request.Fingerprint {
		t.Fatalf("register request: replayed=%v stored=%+v err=%v", replayed, stored, err)
	}
	if _, replayed, err := state.RegisterAnalyzerStartRequest(ctx, request); err != nil || !replayed {
		t.Fatalf("exact request replay: replayed=%v err=%v", replayed, err)
	}
	reusedNonce := request
	reusedNonce.ID = "analyzer-start-reused-nonce"
	reusedNonce.Fingerprint = analyzerStartStoreFingerprint(t, reusedNonce)
	if _, _, err := state.RegisterAnalyzerStartRequest(ctx, reusedNonce); err == nil {
		t.Fatal("nonce reuse with another request unexpectedly passed")
	}
	wrongWorkspace := analyzerStartRequestStoreFixture(t, session.RunID, "workspace-not-bound",
		"analyzer-start-wrong-workspace", analyzer.AnalyzerStartAdapterFake,
		strings.Repeat("5", 64), now, now.Add(time.Hour))
	if _, _, err := state.RegisterAnalyzerStartRequest(ctx, wrongWorkspace); err == nil {
		t.Fatal("request with a mismatched run/workspace binding unexpectedly passed")
	}
	prepared, err := state.PrepareAnalyzerStartIntent(ctx, request.ID, now.Add(time.Second))
	if err != nil || prepared.State != analyzer.AnalyzerStartIntentPrepared {
		t.Fatalf("prepare fake intent: %+v err=%v", prepared, err)
	}
	if replay, err := state.PrepareAnalyzerStartIntent(ctx, request.ID,
		now.Add(2*time.Second)); err != nil || replay.Fingerprint != prepared.Fingerprint {
		t.Fatalf("prepare replay: %+v err=%v", replay, err)
	}
	consumed, err := state.ConsumeAnalyzerStartIntent(ctx, request.ID,
		prepared.Fingerprint, now.Add(2*time.Second))
	if err != nil || consumed.State != analyzer.AnalyzerStartIntentConsumed {
		t.Fatalf("consume fake intent: %+v err=%v", consumed, err)
	}
	if replay, err := state.ConsumeAnalyzerStartIntent(ctx, request.ID,
		prepared.Fingerprint, now.Add(3*time.Second)); err != nil ||
		replay.Fingerprint != consumed.Fingerprint {
		t.Fatalf("consume replay: %+v err=%v", replay, err)
	}
	completed, err := state.CompleteFakeAnalyzerStartIntent(ctx, request.ID,
		consumed.Fingerprint, now.Add(3*time.Second))
	if err != nil || completed.State != analyzer.AnalyzerStartIntentFakeSucceeded ||
		completed.ProcessObserved || completed.ProcessStartAuthorized {
		t.Fatalf("complete fake intent: %+v err=%v", completed, err)
	}
	receipts, err := state.ListAnalyzerStartLifecycleReceipts(ctx, request.ID)
	if err != nil || len(receipts) != 3 ||
		receipts[2].PreviousReceiptFingerprint != receipts[1].Fingerprint {
		t.Fatalf("receipt chain=%+v err=%v", receipts, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE analyzer_start_requests
		SET adapter = 'disabled' WHERE id = ?`, request.ID); err == nil {
		t.Fatal("durable request update unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM analyzer_start_intents
		WHERE id = ?`, completed.ID); err == nil {
		t.Fatal("intent delete unexpectedly passed")
	}
	eventList, err := state.ListRunEvents(ctx, session.RunID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range eventList {
		counts[event.Type]++
		if event.Type == events.AnalyzerStartRequestRegisteredEvent ||
			event.Type == events.AnalyzerStartIntentRecordedEvent ||
			event.Type == events.AnalyzerStartLifecycleReceiptRecordedEvent {
			if !strings.Contains(event.PayloadJSON, `"redacted":true`) ||
				strings.Contains(event.PayloadJSON, request.NonceSHA256) ||
				strings.Contains(event.PayloadJSON, "command") {
				t.Fatalf("analyzer event leaked detail: %s", event.PayloadJSON)
			}
		}
	}
	if counts[events.AnalyzerStartRequestRegisteredEvent] != 1 ||
		counts[events.AnalyzerStartIntentRecordedEvent] != 3 ||
		counts[events.AnalyzerStartLifecycleReceiptRecordedEvent] != 3 {
		t.Fatalf("unexpected analyzer event counts: %+v", counts)
	}
}

func TestAnalyzerStartControlCompetingGenerationAndRestartRecovery(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "analyzer-start-recovery.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session, _, _, _ := browserLaunchStoreFixture(t, state)
	now := time.Now().UTC().Round(time.Millisecond)
	request := analyzerStartRequestStoreFixture(t, session.RunID, session.WorkspaceID,
		"analyzer-start-race", analyzer.AnalyzerStartAdapterFake, strings.Repeat("2", 64), now,
		now.Add(time.Hour))
	if _, _, err := state.RegisterAnalyzerStartRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	prepared, err := state.PrepareAnalyzerStartIntent(ctx, request.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := state.ConsumeAnalyzerStartIntent(ctx, request.ID, prepared.Fingerprint,
			now.Add(2*time.Second))
		results <- err
	}()
	go func() {
		defer wg.Done()
		_, err := state.CancelAnalyzerStartIntent(ctx, request.ID, prepared.Fingerprint,
			now.Add(2*time.Second))
		results <- err
	}()
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("competing generation successes=%d, want 1", succeeded)
	}
	latest, found, err := state.LoadLatestAnalyzerStartIntent(ctx, request.ID)
	if err != nil || !found {
		t.Fatalf("load race winner: found=%v err=%v", found, err)
	}
	if latest.State == analyzer.AnalyzerStartIntentConsumed {
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
		state, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		reconciled, err := state.ReconcileAnalyzerStartIntents(ctx, now.Add(3*time.Second), 10)
		if err != nil || len(reconciled) != 1 ||
			reconciled[0].State != analyzer.AnalyzerStartIntentRecoveryRequired ||
			reconciled[0].ProcessObserved {
			t.Fatalf("restart recovery=%+v err=%v", reconciled, err)
		}
	}
	defer state.Close()

	expiring := analyzerStartRequestStoreFixture(t, session.RunID, session.WorkspaceID,
		"analyzer-start-expiring", analyzer.AnalyzerStartAdapterFake, strings.Repeat("3", 64),
		now, now.Add(10*time.Second))
	if _, _, err := state.RegisterAnalyzerStartRequest(ctx, expiring); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PrepareAnalyzerStartIntent(ctx, expiring.ID,
		now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reconciled, err := state.ReconcileAnalyzerStartIntents(ctx, expiring.ExpiresAt, 10)
	if err != nil || len(reconciled) != 1 ||
		reconciled[0].State != analyzer.AnalyzerStartIntentExpired ||
		reconciled[0].RequestConsumed {
		t.Fatalf("expiry reconciliation=%+v err=%v", reconciled, err)
	}
}

func TestAnalyzerStartControlDisabledAndV93Migration(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "analyzer-start-v93.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session, _, _, _ := browserLaunchStoreFixture(t, state)
	now := time.Now().UTC().Round(time.Millisecond)
	request := analyzerStartRequestStoreFixture(t, session.RunID, session.WorkspaceID,
		"analyzer-start-disabled", analyzer.AnalyzerStartAdapterDisabled,
		strings.Repeat("4", 64), now, now.Add(time.Hour))
	if _, _, err := state.RegisterAnalyzerStartRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	disabled, err := state.PrepareAnalyzerStartIntent(ctx, request.ID, now.Add(time.Second))
	if err != nil || disabled.State != analyzer.AnalyzerStartIntentDisabled || !disabled.Terminal {
		t.Fatalf("disabled intent=%+v err=%v", disabled, err)
	}
	if _, err := state.ConsumeAnalyzerStartIntent(ctx, request.ID, disabled.Fingerprint,
		now.Add(2*time.Second)); err == nil {
		t.Fatal("disabled adapter was consumed")
	}
	unknown := analyzerStartRequestStoreFixture(t, session.RunID, session.WorkspaceID,
		"analyzer-start-unknown-json", analyzer.AnalyzerStartAdapterFake,
		strings.Repeat("6", 64), now, now.Add(time.Hour))
	if err := directAnalyzerStartRequestInsert(t, state, unknown, func(payload map[string]any) {
		payload["unknown"] = false
	}); err == nil {
		t.Fatal("direct SQL request with an unknown field unexpectedly passed")
	}
	widened := analyzerStartRequestStoreFixture(t, session.RunID, session.WorkspaceID,
		"analyzer-start-widened-json", analyzer.AnalyzerStartAdapterFake,
		strings.Repeat("7", 64), now, now.Add(time.Hour))
	if err := directAnalyzerStartRequestInsert(t, state, widened, func(payload map[string]any) {
		payload["authority"].(map[string]any)["process_start"] = true
	}); err == nil {
		t.Fatal("direct SQL request with process authority unexpectedly passed")
	}
	for _, statement := range removeSchemaV93ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v93 fixture with %q: %v", statement, err)
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
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}

func analyzerStartRequestStoreFixture(t *testing.T, runID, workspaceID, id string,
	adapter analyzer.AnalyzerStartAdapter, nonce string, registeredAt, expiresAt time.Time,
) analyzer.AnalyzerDurableStartRequest {
	t.Helper()
	request := analyzer.AnalyzerDurableStartRequest{
		ProtocolVersion: analyzer.AnalyzerDurableStartRequestProtocolVersion,
		ID:              id, RunID: runID, WorkspaceID: workspaceID,
		SignedRequestID: id + "-signed", Analyzer: "rust-fixture",
		TargetGOOS: "windows", TargetGOARCH: "amd64",
		ExecutableSHA256:         strings.Repeat("a", 64),
		OperatorIdentitySHA256:   strings.Repeat("b", 64),
		AdmissionMatrixSHA256:    strings.Repeat("c", 64),
		ScopeApprovalSHA256:      strings.Repeat("d", 64),
		CapabilityRequestSHA256:  strings.Repeat("e", 64),
		CapabilityContractSHA256: strings.Repeat("f", 64), NonceSHA256: nonce,
		Adapter: adapter, IssuedAt: registeredAt.Add(-time.Minute),
		ExpiresAt: expiresAt, RegisteredAt: registeredAt,
		ExactRunWorkspaceBound: true, SignatureVerified: true,
		ClockValidityVerified: true, DurableReplayGuardPresent: true,
		StartBlocked: true, MetadataOnly: true,
	}
	request.Fingerprint = analyzerStartStoreFingerprint(t, request)
	if err := analyzer.ValidateStoredAnalyzerDurableStartRequest(request); err != nil {
		t.Fatal(err)
	}
	return request
}

func analyzerStartStoreFingerprint(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var canonical map[string]any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		t.Fatal(err)
	}
	delete(canonical, "fingerprint")
	raw, err = json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func directAnalyzerStartRequestInsert(t *testing.T, state *SQLiteStore,
	request analyzer.AnalyzerDurableStartRequest, mutate func(map[string]any),
) error {
	t.Helper()
	ctx := t.Context()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	mutate(payload)
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	missionID, _, _, err := analyzerStartRunBindingTx(ctx, tx, request.RunID)
	if err != nil {
		t.Fatal(err)
	}
	event, err := events.New(request.RunID, missionID,
		events.AnalyzerStartRequestRegisteredEvent, "analyzer_start_control", request.ID,
		map[string]any{"request_fingerprint": request.Fingerprint,
			"adapter": request.Adapter, "start_blocked": true, "redacted": true})
	if err != nil {
		t.Fatal(err)
	}
	event.CreatedAt = request.RegisteredAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO analyzer_start_requests
		(id, run_id, mission_id, workspace_id, signed_request_id, nonce_sha256,
		fingerprint, adapter, event_sequence, payload_json, registered_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, request.ID, request.RunID,
		missionID, request.WorkspaceID, request.SignedRequestID, request.NonceSHA256,
		request.Fingerprint, request.Adapter, event.Sequence, string(raw),
		ts(request.RegisteredAt), ts(request.ExpiresAt))
	return err
}

func removeSchemaV93ForTestStatements() []string {
	return append(removeSchemaV94ForTestStatements(), []string{
		`DROP TRIGGER trg_analyzer_start_receipt_delete_immutable`,
		`DROP TRIGGER trg_analyzer_start_receipt_update_immutable`,
		`DROP TRIGGER trg_analyzer_start_intent_delete_immutable`,
		`DROP TRIGGER trg_analyzer_start_intent_update_immutable`,
		`DROP TRIGGER trg_analyzer_start_request_delete_immutable`,
		`DROP TRIGGER trg_analyzer_start_request_update_immutable`,
		`DROP TRIGGER trg_analyzer_start_receipt_insert`,
		`DROP TRIGGER trg_analyzer_start_intent_insert`,
		`DROP TRIGGER trg_analyzer_start_request_insert`,
		`DROP INDEX idx_analyzer_start_receipts_request_generation`,
		`DROP TABLE analyzer_start_lifecycle_receipts`,
		`DROP INDEX idx_analyzer_start_intents_recovery`,
		`DROP INDEX idx_analyzer_start_intents_request_generation`,
		`DROP TABLE analyzer_start_intents`,
		`DROP INDEX idx_analyzer_start_requests_run_registered`,
		`DROP TABLE analyzer_start_requests`,
		`DELETE FROM schema_migrations WHERE version = 93`,
	}...)
}
