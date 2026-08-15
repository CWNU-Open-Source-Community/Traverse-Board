package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/events"
)

func TestBrowserRuntimeLifecycleRecordsAreAppendOnlyRecoverableAndAudited(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "browser-runtime-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	session, identity, acceptance, ownership := browserLaunchStoreFixture(t, state)
	attempt, _, _, err := state.PrepareBrowserLaunch(ctx, session, identity, acceptance,
		ownership, "browser-runtime-lifecycle-operation", "browser-runtime-worker")
	if err != nil {
		t.Fatal(err)
	}

	checkpoints := successfulBrowserRuntimeCheckpointFixture(t, attempt,
		ownership, "browser-runtime-durable", time.Now().UTC().Round(time.Millisecond))
	for _, checkpoint := range checkpoints {
		if err := state.RecordBrowserRuntimeCheckpoint(ctx, checkpoint); err != nil {
			t.Fatalf("record checkpoint generation %d: %v", checkpoint.Generation, err)
		}
	}
	if err := state.RecordBrowserRuntimeCheckpoint(ctx, checkpoints[len(checkpoints)-1]); err != nil {
		t.Fatalf("exact checkpoint replay failed: %v", err)
	}
	receipt := successfulBrowserRuntimeReceiptFixture(t, checkpoints[len(checkpoints)-1])
	if err := state.RecordBrowserRuntimeReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordBrowserRuntimeReceipt(ctx, receipt); err != nil {
		t.Fatalf("exact receipt replay failed: %v", err)
	}

	latest, found, err := state.LoadLatestBrowserRuntimeCheckpoint(ctx, receipt.RuntimeID)
	if err != nil || !found || latest.Fingerprint != receipt.FinalCheckpointFingerprint {
		t.Fatalf("latest checkpoint mismatch: found=%v checkpoint=%+v err=%v", found, latest, err)
	}
	loadedReceipt, found, err := state.LoadBrowserRuntimeReceipt(ctx, receipt.RuntimeID)
	if err != nil || !found || !reflect.DeepEqual(loadedReceipt, receipt) {
		t.Fatalf("receipt mismatch: found=%v receipt=%+v err=%v", found, loadedReceipt, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE browser_runtime_checkpoints
		SET stage = 'failed' WHERE id = ?`, latest.ID); err == nil {
		t.Fatal("browser runtime checkpoint update unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM browser_runtime_receipts
		WHERE id = ?`, receipt.ID); err == nil {
		t.Fatal("browser runtime receipt delete unexpectedly passed")
	}

	recoveryInitial := initialBrowserRuntimeCheckpointFixture(t, attempt,
		ownership, "browser-runtime-recovery", latest.RecordedAt.Add(time.Second))
	if err := state.RecordBrowserRuntimeCheckpoint(ctx, recoveryInitial); err != nil {
		t.Fatal(err)
	}
	recoveryFailed := recoveryInitial
	recoveryFailed.ID = recoveryInitial.RuntimeID + "-checkpoint-2"
	recoveryFailed.PreviousCheckpointFingerprint = recoveryInitial.Fingerprint
	recoveryFailed.Generation = 2
	recoveryFailed.Stage = browserruntime.BrowserRuntimeStageFailed
	recoveryFailed.RecoveryRequired = true
	recoveryFailed.FailureCode = "network_cleanup_unverified"
	recoveryFailed.RecordedAt = recoveryInitial.RecordedAt.Add(time.Millisecond)
	recoveryFailed.Fingerprint = browserRuntimeLifecycleStoreFixtureFingerprint(t, recoveryFailed)
	if err := browserruntime.ValidateStoredBrowserRuntimeCheckpointSuccessor(
		recoveryFailed, recoveryInitial); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordBrowserRuntimeCheckpoint(ctx, recoveryFailed); err != nil {
		t.Fatal(err)
	}
	recoveryReceipt := browserruntime.BrowserRuntimeReceipt{
		ProtocolVersion: browserruntime.BrowserRuntimeReceiptProtocolVersion,
		ID:              recoveryFailed.RuntimeID + "-receipt", RuntimeID: recoveryFailed.RuntimeID,
		RunID: recoveryFailed.RunID, AttemptFingerprint: recoveryFailed.AttemptFingerprint,
		AuthorizationFingerprint:   recoveryFailed.AuthorizationFingerprint,
		FinalCheckpointFingerprint: recoveryFailed.Fingerprint,
		RestrictedCDPClosed:        true, Succeeded: false, RecoveryRequired: true,
		FailureCode: recoveryFailed.FailureCode, StartedAt: recoveryInitial.RecordedAt,
		CompletedAt: recoveryFailed.RecordedAt.Add(time.Millisecond),
	}
	recoveryReceipt.Fingerprint = browserRuntimeLifecycleStoreFixtureFingerprint(t, recoveryReceipt)
	if err := state.RecordBrowserRuntimeReceipt(ctx, recoveryReceipt); err != nil {
		t.Fatal(err)
	}
	recoverable, err := state.ListRecoverableBrowserRuntimeCheckpoints(ctx, 10)
	if err != nil || len(recoverable) != 1 ||
		recoverable[0].Fingerprint != recoveryFailed.Fingerprint {
		t.Fatalf("unexpected recovery projection: %+v err=%v", recoverable, err)
	}

	timeline, err := state.ListRunEvents(ctx, session.RunID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range timeline {
		counts[event.Type]++
		if event.Type == events.BrowserRuntimeCheckpointRecordedEvent ||
			event.Type == events.BrowserRuntimeReceiptRecordedEvent {
			if strings.Contains(event.PayloadJSON, "authorization_fingerprint") ||
				strings.Contains(event.PayloadJSON, "process_exit_fingerprint") ||
				!strings.Contains(event.PayloadJSON, `"redacted":true`) {
				t.Fatalf("browser lifecycle event leaked runtime detail: %s", event.PayloadJSON)
			}
		}
	}
	if counts[events.BrowserRuntimeCheckpointRecordedEvent] != len(checkpoints)+2 ||
		counts[events.BrowserRuntimeReceiptRecordedEvent] != 2 {
		t.Fatalf("unexpected lifecycle event counts: %+v", counts)
	}
}

func TestBrowserRuntimeLifecycleRejectsBrokenAncestryAndV92Migrates(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "browser-runtime-v92.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session, identity, acceptance, ownership := browserLaunchStoreFixture(t, state)
	attempt, _, _, err := state.PrepareBrowserLaunch(ctx, session, identity, acceptance,
		ownership, "browser-runtime-v92-operation", "browser-runtime-worker")
	if err != nil {
		t.Fatal(err)
	}
	initial := initialBrowserRuntimeCheckpointFixture(t, attempt, ownership,
		"browser-runtime-ancestry", time.Now().UTC().Round(time.Millisecond))
	broken := initial
	broken.ID = broken.RuntimeID + "-checkpoint-2"
	broken.Generation = 2
	broken.Stage = browserruntime.BrowserRuntimeStageCDPClosed
	broken.PreviousCheckpointFingerprint = strings.Repeat("9", 64)
	broken.RecordedAt = broken.RecordedAt.Add(time.Millisecond)
	broken.Fingerprint = browserRuntimeLifecycleStoreFixtureFingerprint(t, broken)
	if err := state.RecordBrowserRuntimeCheckpoint(ctx, broken); err == nil {
		t.Fatal("checkpoint with missing predecessor unexpectedly passed")
	}
	for _, statement := range removeSchemaV92ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v92 fixture with %q: %v", statement, err)
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

func initialBrowserRuntimeCheckpointFixture(t *testing.T,
	attempt browserruntime.BrowserLaunchAttempt, ownership browserruntime.ProfileOwnershipPlan,
	runtimeID string, recordedAt time.Time,
) browserruntime.BrowserRuntimeCheckpoint {
	t.Helper()
	checkpoint := browserruntime.BrowserRuntimeCheckpoint{
		ProtocolVersion: browserruntime.BrowserRuntimeCheckpointProtocolVersion,
		ID:              runtimeID + "-checkpoint-1", RuntimeID: runtimeID, RunID: attempt.RunID,
		AttemptID: attempt.ID, AttemptFingerprint: attempt.Fingerprint,
		AuthorizationFingerprint:    strings.Repeat("c", 64),
		ProcessStartSpecFingerprint: strings.Repeat("d", 64),
		ProfileOwnershipFingerprint: ownership.Fingerprint,
		ProfileLeaseFingerprint:     strings.Repeat("e", 64), Generation: 1,
		Stage:                 browserruntime.BrowserRuntimeStageRunning,
		RestrictedCDPExpected: false, RestrictedCDPClosed: true, RecordedAt: recordedAt,
	}
	checkpoint.Fingerprint = browserRuntimeLifecycleStoreFixtureFingerprint(t, checkpoint)
	if err := browserruntime.ValidateStoredBrowserRuntimeCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func successfulBrowserRuntimeCheckpointFixture(t *testing.T,
	attempt browserruntime.BrowserLaunchAttempt, ownership browserruntime.ProfileOwnershipPlan,
	runtimeID string, recordedAt time.Time,
) []browserruntime.BrowserRuntimeCheckpoint {
	t.Helper()
	initial := initialBrowserRuntimeCheckpointFixture(t, attempt, ownership, runtimeID, recordedAt)
	stages := []browserruntime.BrowserRuntimeLifecycleStage{
		browserruntime.BrowserRuntimeStageCDPClosed,
		browserruntime.BrowserRuntimeStageProcessQuiescent,
		browserruntime.BrowserRuntimeStageNetworkReleased,
		browserruntime.BrowserRuntimeStageProfileReleased,
		browserruntime.BrowserRuntimeStageCompleted,
	}
	checkpoints := []browserruntime.BrowserRuntimeCheckpoint{initial}
	for index, stage := range stages {
		previous := checkpoints[len(checkpoints)-1]
		next := previous
		next.ID = runtimeID + "-checkpoint-" + strconv.Itoa(index+2)
		next.Generation = previous.Generation + 1
		next.PreviousCheckpointFingerprint = previous.Fingerprint
		next.Stage = stage
		next.RecordedAt = previous.RecordedAt.Add(time.Millisecond)
		next.Fingerprint = ""
		switch stage {
		case browserruntime.BrowserRuntimeStageProcessQuiescent:
			next.ProcessTerminationRequested = true
			next.ProcessTreeQuiescent = true
		case browserruntime.BrowserRuntimeStageNetworkReleased:
			next.NetworkCleanupVerified = true
		case browserruntime.BrowserRuntimeStageProfileReleased:
			next.ProfileReleased = true
			next.ReleasedProfileFingerprint = strings.Repeat("f", 64)
		case browserruntime.BrowserRuntimeStageCompleted:
			next.ProfileCleaned = true
		}
		next.Fingerprint = browserRuntimeLifecycleStoreFixtureFingerprint(t, next)
		if err := browserruntime.ValidateStoredBrowserRuntimeCheckpointSuccessor(next, previous); err != nil {
			t.Fatal(err)
		}
		checkpoints = append(checkpoints, next)
	}
	return checkpoints
}

func successfulBrowserRuntimeReceiptFixture(t *testing.T,
	checkpoint browserruntime.BrowserRuntimeCheckpoint,
) browserruntime.BrowserRuntimeReceipt {
	t.Helper()
	receipt := browserruntime.BrowserRuntimeReceipt{
		ProtocolVersion: browserruntime.BrowserRuntimeReceiptProtocolVersion,
		ID:              checkpoint.RuntimeID + "-receipt", RuntimeID: checkpoint.RuntimeID,
		RunID: checkpoint.RunID, AttemptFingerprint: checkpoint.AttemptFingerprint,
		AuthorizationFingerprint:   checkpoint.AuthorizationFingerprint,
		FinalCheckpointFingerprint: checkpoint.Fingerprint,
		ProcessExitFingerprint:     strings.Repeat("1", 64),
		ReleasedProfileFingerprint: checkpoint.ReleasedProfileFingerprint,
		RestrictedCDPClosed:        true, ProcessTreeQuiescent: true,
		NetworkCleanupVerified: true, ProfileReleased: true, ProfileCleaned: true,
		Succeeded: true, RecoveryRequired: false,
		StartedAt:   checkpoint.RecordedAt.Add(-time.Second),
		CompletedAt: checkpoint.RecordedAt.Add(time.Millisecond),
	}
	receipt.Fingerprint = browserRuntimeLifecycleStoreFixtureFingerprint(t, receipt)
	if err := browserruntime.ValidateStoredBrowserRuntimeReceiptForCheckpoint(
		receipt, checkpoint); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func browserRuntimeLifecycleStoreFixtureFingerprint(t *testing.T, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case browserruntime.BrowserRuntimeCheckpoint:
		typed.Fingerprint = ""
		value = typed
	case browserruntime.BrowserRuntimeReceipt:
		typed.Fingerprint = ""
		value = typed
	default:
		t.Fatalf("unsupported browser runtime lifecycle fixture type %T", value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		t.Fatal(err)
	}
	if object, ok := canonical.(map[string]any); ok {
		delete(object, "fingerprint")
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func removeSchemaV92ForTestStatements() []string {
	return append(removeSchemaV93ForTestStatements(), []string{
		`DROP TRIGGER trg_browser_runtime_receipt_delete_immutable`,
		`DROP TRIGGER trg_browser_runtime_receipt_update_immutable`,
		`DROP TRIGGER trg_browser_runtime_checkpoint_delete_immutable`,
		`DROP TRIGGER trg_browser_runtime_checkpoint_update_immutable`,
		`DROP TRIGGER trg_browser_runtime_receipt_insert`,
		`DROP TRIGGER trg_browser_runtime_checkpoint_insert`,
		`DROP INDEX idx_browser_runtime_receipts_run_completed`,
		`DROP TABLE browser_runtime_receipts`,
		`DROP INDEX idx_browser_runtime_checkpoints_run_recorded`,
		`DROP INDEX idx_browser_runtime_checkpoints_runtime_generation`,
		`DROP TABLE browser_runtime_checkpoints`,
		`DELETE FROM schema_migrations WHERE version = 92`,
	}...)
}
