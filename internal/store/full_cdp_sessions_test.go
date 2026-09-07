package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

func TestFullCDPSessionAuditEventsAreMetadataOnly(t *testing.T) {
	ctx := t.Context()
	state, run := fullCDPAuditStoreFixture(t)
	runtimeID := "full_cdp_runtime_audit_001"
	fullSessionID := "full_cdp_session_audit_001"
	startedAt := time.Date(2026, time.August, 30, 4, 0, 0, 0, time.UTC)
	expiresAt := startedAt.Add(5 * time.Minute)
	if err := state.RecordFullCDPSessionOpened(ctx, run.ID, runtimeID,
		fullSessionID, run.SessionID, "edge", "stable", "http://127.0.0.1:18080",
		startedAt, expiresAt); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordFullCDPSessionOpened(ctx, run.ID, runtimeID,
		fullSessionID, run.SessionID, "edge", "stable", "http://127.0.0.1:18080",
		startedAt, expiresAt); err != nil {
		t.Fatalf("exact Full CDP open audit replay failed: %v", err)
	}
	receipt := validFullCDPAuditReceipt(t, runtimeID, run.ID, run.SessionID,
		startedAt, startedAt.Add(time.Minute))
	if err := state.RecordFullCDPSessionClosed(ctx, run.ID, runtimeID,
		fullSessionID, run.SessionID, "operator_closed", receipt); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordFullCDPSessionClosed(ctx, run.ID, runtimeID,
		fullSessionID, run.SessionID, "operator_closed", receipt); err != nil {
		t.Fatalf("exact Full CDP close audit replay failed: %v", err)
	}

	timeline, err := state.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range timeline {
		if event.Type != events.FullCDPSessionOpenedEvent &&
			event.Type != events.FullCDPSessionClosedEvent {
			continue
		}
		counts[event.Type]++
		if event.Source != fullCDPAuditSource || event.SubjectID != runtimeID {
			t.Fatalf("Full CDP audit envelope is not runtime-bound: %#v", event)
		}
		for _, forbidden := range []string{
			receipt.AttemptFingerprint, receipt.StartAuthorization,
			receipt.SessionAuthorization, receipt.ProcessExitFingerprint,
			receipt.ReleasedProfileFingerprint, receipt.Fingerprint,
			`"endpoint"`, `"pid"`, `"profile_path"`, `"fence"`, `"token"`,
		} {
			if strings.Contains(event.PayloadJSON, forbidden) {
				t.Fatalf("Full CDP audit persisted forbidden runtime material %q: %s",
					forbidden, event.PayloadJSON)
			}
		}
	}
	if counts[events.FullCDPSessionOpenedEvent] != 1 ||
		counts[events.FullCDPSessionClosedEvent] != 1 {
		t.Fatalf("Full CDP audit event counts are invalid: %#v", counts)
	}
}

func TestFullCDPSessionAuditRejectsCrossSessionAndUnredactedReceipt(t *testing.T) {
	ctx := t.Context()
	state, run := fullCDPAuditStoreFixture(t)
	startedAt := time.Date(2026, time.August, 30, 5, 0, 0, 0, time.UTC)
	if err := state.RecordFullCDPSessionOpened(ctx, run.ID, "full_cdp_runtime_bad_001",
		"full_cdp_session_bad_001", "another_session", "chrome", "stable",
		"http://127.0.0.1:18080",
		startedAt, startedAt.Add(5*time.Minute)); err == nil {
		t.Fatal("Full CDP open audit accepted a cross-Session binding")
	}

	runtimeID := "full_cdp_runtime_bad_002"
	fullSessionID := "full_cdp_session_bad_002"
	if err := state.RecordFullCDPSessionOpened(ctx, run.ID, runtimeID, fullSessionID,
		run.SessionID, "chrome", "stable", "http://127.0.0.1:18080", startedAt,
		startedAt.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	receipt := validFullCDPAuditReceipt(t, runtimeID, run.ID, run.SessionID,
		startedAt, startedAt.Add(time.Minute))
	receipt.RawOutputIncluded = true
	receipt.Fingerprint = fullCDPAuditFingerprint(t, receipt)
	if err := state.RecordFullCDPSessionClosed(ctx, run.ID, runtimeID, fullSessionID,
		run.SessionID, "operator_closed", receipt); err == nil {
		t.Fatal("Full CDP close audit accepted an unredacted receipt")
	}

	timeline, err := state.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countRunEventType(timeline, events.FullCDPSessionOpenedEvent) != 1 ||
		countRunEventType(timeline, events.FullCDPSessionClosedEvent) != 0 {
		t.Fatalf("rejected Full CDP audit mutated the event log: %#v", timeline)
	}
}

func fullCDPAuditStoreFixture(t *testing.T) (*SQLiteStore, domain.Run) {
	t.Helper()
	state, err := Open(filepath.Join(t.TempDir(), "full-cdp-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	workspace := WorkspaceRecord{
		ID: "workspace-full-cdp-audit", Name: "full-cdp-audit",
		RootPath: t.TempDir(),
	}
	if err := state.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(t.Context(),
		application.CreateRunRequest{
			Goal: "audit a process-local Full CDP session", Profile: "code",
			WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	return state, run
}

func validFullCDPAuditReceipt(t *testing.T, runtimeID, runID, sessionID string,
	startedAt, completedAt time.Time,
) browserruntime.FullCDPRuntimeReceipt {
	t.Helper()
	receipt := browserruntime.FullCDPRuntimeReceipt{
		ProtocolVersion: browserruntime.FullCDPRuntimeReceiptProtocolVersion,
		RuntimeID:       runtimeID, RunID: runID, SessionID: sessionID,
		AttemptFingerprint:         strings.Repeat("a", 64),
		StartAuthorization:         strings.Repeat("b", 64),
		SessionAuthorization:       strings.Repeat("c", 64),
		ProcessExitFingerprint:     strings.Repeat("d", 64),
		ReleasedProfileFingerprint: strings.Repeat("e", 64),
		CDPClosed:                  true, ProcessTreeQuiescent: true,
		ProfileReleased: true, ProfileCleaned: true, Succeeded: true,
		FullCDPUsed: true, StartedAt: startedAt, CompletedAt: completedAt,
	}
	receipt.Fingerprint = fullCDPAuditFingerprint(t, receipt)
	if err := browserruntime.ValidateFullCDPRuntimeReceipt(receipt); err != nil {
		t.Fatalf("test Full CDP receipt is invalid: %v", err)
	}
	return receipt
}

func fullCDPAuditFingerprint(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		t.Fatal(err)
	}
	object, ok := canonical.(map[string]any)
	if !ok {
		t.Fatal("Full CDP receipt did not encode as an object")
	}
	delete(object, "fingerprint")
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
