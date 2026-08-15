package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

func removeSchemaV85ForTestStatements() []string {
	return append(removeSchemaV86ForTestStatements(), []string{
		`DROP TRIGGER trg_browser_launch_review_operation_delete_immutable`,
		`DROP TRIGGER trg_browser_launch_review_operation_update_immutable`,
		`DROP TRIGGER trg_browser_launch_review_delete_immutable`,
		`DROP TRIGGER trg_browser_launch_review_update_immutable`,
		`DROP TRIGGER trg_browser_launch_preparation_operation_delete_immutable`,
		`DROP TRIGGER trg_browser_launch_preparation_operation_update_immutable`,
		`DROP TRIGGER trg_browser_launch_lease_delete_immutable`,
		`DROP TRIGGER trg_browser_launch_lease_update_immutable`,
		`DROP TRIGGER trg_browser_launch_attempt_delete_immutable`,
		`DROP TRIGGER trg_browser_launch_attempt_update_immutable`,
		`DROP TRIGGER trg_browser_launch_review_operation_insert`,
		`DROP TRIGGER trg_browser_launch_review_insert`,
		`DROP TRIGGER trg_browser_launch_preparation_operation_insert`,
		`DROP TRIGGER trg_browser_launch_lease_insert`,
		`DROP TRIGGER trg_browser_launch_attempt_insert`,
		`DROP INDEX idx_browser_launch_reviews_run_event`,
		`DROP TABLE browser_launch_review_operations`,
		`DROP TABLE browser_launch_reviews`,
		`DROP TABLE browser_launch_preparation_operations`,
		`DROP TABLE browser_launch_leases`,
		`DROP INDEX idx_browser_launch_attempts_run_created`,
		`DROP TABLE browser_launch_attempts`,
		`DELETE FROM schema_migrations WHERE version = 85`,
	}...)
}

func TestBrowserLaunchPreparationAndReviewAreDurableImmutableAndNonAuthorizing(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "browser-launch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	sessionPlan, identity, acceptance, ownership := browserLaunchStoreFixture(t, state)

	attempt, lease, preparedReplayed, err := state.PrepareBrowserLaunch(ctx, sessionPlan,
		identity, acceptance, ownership, "browser-launch-preparation-operation-001",
		"browser-worker-001")
	if err != nil {
		t.Fatal(err)
	}
	if preparedReplayed || attempt.ProcessStartAuthorized ||
		lease.ProcessExecutionAuthorized || !attempt.StartBlocked ||
		!lease.StartBlocked {
		t.Fatalf("durable browser launch preparation widened authority: attempt=%#v lease=%#v",
			attempt, lease)
	}
	replayAttempt, replayLease, replayReplayed, err := state.PrepareBrowserLaunch(ctx,
		sessionPlan, identity, acceptance, ownership,
		"browser-launch-preparation-operation-001", "browser-worker-001")
	if err != nil || !replayReplayed ||
		replayAttempt.ID != attempt.ID || replayLease.ID != lease.ID {
		t.Fatalf("browser launch preparation replay diverged: %#v err=%v", replayAttempt, err)
	}
	if _, _, _, err := state.PrepareBrowserLaunch(ctx, sessionPlan, identity, acceptance,
		ownership, "browser-launch-preparation-operation-001", "another-worker"); err == nil {
		t.Fatal("browser launch preparation key accepted changed ownership")
	}

	review, reviewedReplayed, err := state.RecordBrowserLaunchReview(ctx, sessionPlan,
		identity, acceptance, ownership, attempt, lease,
		browserruntime.BrowserLaunchReviewAcceptCandidate,
		"browser-launch-review-operation-001", "independent-operator")
	if err != nil {
		t.Fatal(err)
	}
	if reviewedReplayed || !review.AcceptedForFutureAdapter || review.StartAuthorized ||
		review.ProcessExecutionAuthorized || review.NetworkAuthorized ||
		review.ProcessTerminationAuthorized || review.FilesystemCleanupAuthorized ||
		review.ArtifactCommitAuthorized {
		t.Fatalf("durable browser launch review widened authority: %#v", review)
	}
	reviewReplayReview, reviewReplayReplayed, err := state.RecordBrowserLaunchReview(ctx,
		sessionPlan, identity, acceptance, ownership, attempt, lease,
		browserruntime.BrowserLaunchReviewAcceptCandidate,
		"browser-launch-review-operation-001", "independent-operator")
	if err != nil || !reviewReplayReplayed ||
		reviewReplayReview.ID != review.ID {
		t.Fatalf("browser launch review replay diverged: %#v err=%v", reviewReplayReview, err)
	}
	if _, _, err := state.RecordBrowserLaunchReview(ctx, sessionPlan, identity, acceptance,
		ownership, attempt, lease, browserruntime.BrowserLaunchReviewRejectCandidate,
		"browser-launch-review-operation-001", "independent-operator"); err == nil {
		t.Fatal("browser launch review key accepted a changed decision")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE browser_launch_attempts
		SET generation = 2 WHERE id = ?`, attempt.ID); err == nil {
		t.Fatal("browser launch attempt update unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM browser_launch_leases
		WHERE id = ?`, lease.ID); err == nil {
		t.Fatal("browser launch lease delete unexpectedly passed")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE browser_launch_reviews
		SET decision = 'reject_candidate' WHERE id = ?`, review.ID); err == nil {
		t.Fatal("browser launch review update unexpectedly passed")
	}

	timeline, err := state.ListRunEvents(ctx, sessionPlan.RunID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range timeline {
		counts[event.Type]++
		if event.Type == events.BrowserLaunchAttemptPreparedEvent ||
			event.Type == events.BrowserLaunchLeaseRecordedEvent ||
			event.Type == events.BrowserLaunchReviewedEvent {
			if strings.Contains(event.PayloadJSON, "browser-worker-001") ||
				strings.Contains(event.PayloadJSON, "independent-operator") ||
				strings.Contains(event.PayloadJSON, "reviewer_sha256") ||
				strings.Contains(event.PayloadJSON, "operation_key") ||
				strings.Contains(event.PayloadJSON, identity.CanonicalPath) ||
				!strings.Contains(event.PayloadJSON, `"start_authorized":false`) &&
					event.Type == events.BrowserLaunchReviewedEvent {
				t.Fatalf("browser launch event leaked private identity/path or authority: %s",
					event.PayloadJSON)
			}
		}
	}
	if counts[events.BrowserLaunchAttemptPreparedEvent] != 1 ||
		counts[events.BrowserLaunchLeaseRecordedEvent] != 1 ||
		counts[events.BrowserLaunchReviewedEvent] != 1 {
		t.Fatalf("browser launch audit event counts are invalid: %#v", counts)
	}
}

func TestBrowserLaunchReviewRequiresIndependentLiveLease(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "browser-launch-independent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	sessionPlan, identity, acceptance, ownership := browserLaunchStoreFixture(t, state)
	attempt, lease, _, err := state.PrepareBrowserLaunch(ctx, sessionPlan, identity,
		acceptance, ownership, "browser-launch-preparation-operation-002", "same-operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.RecordBrowserLaunchReview(ctx, sessionPlan, identity, acceptance,
		ownership, attempt, lease, browserruntime.BrowserLaunchReviewAcceptCandidate,
		"browser-launch-review-operation-002", "same-operator"); err == nil {
		t.Fatal("browser worker unexpectedly reviewed its own attempt")
	}
	tampered := lease
	tampered.ProcessExecutionAuthorized = true
	if _, _, err := state.RecordBrowserLaunchReview(ctx, sessionPlan, identity, acceptance,
		ownership, attempt, tampered, browserruntime.BrowserLaunchReviewAcceptCandidate,
		"browser-launch-review-operation-003", "independent-operator"); err == nil {
		t.Fatal("authorizing browser launch lease unexpectedly reached review")
	}
}

func browserLaunchStoreFixture(t *testing.T, state *SQLiteStore) (
	browserruntime.SessionPlan, browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate, browserruntime.ProfileOwnershipPlan,
) {
	t.Helper()
	ctx := t.Context()
	workspace := WorkspaceRecord{
		ID: "workspace-browser-launch-store", Name: "browser-launch-store",
		RootPath: t.TempDir(),
	}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{
			Goal: "prepare a non-starting browser launch gate", Profile: "code",
			WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	sessionPlan, err := browserruntime.BuildSessionPlan(browserruntime.NewSessionPlanRequest{
		SessionID: run.SessionID, RunID: run.ID, WorkspaceID: workspace.ID,
		ProfileID: browserruntime.ProfileSafeWeb,
		Targets:   []string{"https://example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(filepath.Join("Google", "Chrome", "Application", "chrome.exe"))
	identity := browserruntime.BrowserExecutableIdentity{
		ProtocolVersion: browserruntime.BrowserExecutableIdentityProtocolVersion,
		Product:         browserruntime.BrowserProductChrome, Channel: browserruntime.BrowserChannelStable,
		Vendor: "Google", RootID: browserruntime.DiscoveryRootProgramFiles,
		CanonicalPath: filepath.Join(t.TempDir(), filepath.FromSlash(relative)),
		RelativePath:  relative, HostGOOS: runtime.GOOS, HostGOARCH: runtime.GOARCH,
		TargetGOARCH: "amd64", ExecutableBytes: 1024,
		ExecutableSHA256: strings.Repeat("a", 64),
		VersionSource:    browserruntime.VersionSourceUnavailable,
		PEFormatVerified: true, RegularFileVerified: true, SymlinkRejected: true,
		MetadataOnly: true,
	}
	identity.Fingerprint = browserLaunchFixtureFingerprint(t, identity)
	if err := browserruntime.ValidateBrowserExecutableIdentity(identity); err != nil {
		t.Fatal(err)
	}
	acceptance := browserruntime.BrowserAcceptanceCandidate{
		ProtocolVersion:               browserruntime.BrowserAcceptanceProtocolVersion,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		Product:                       identity.Product, Channel: identity.Channel, RootID: identity.RootID,
		ExecutableSHA256: identity.ExecutableSHA256, ExecutableBytes: identity.ExecutableBytes,
		TargetGOARCH: identity.TargetGOARCH,
		Decision:     browserruntime.BrowserAcceptanceAccepted,
		ReasonCode:   browserruntime.BrowserAcceptanceReasonPublisherVerified,
		Evidence: browserruntime.AuthenticodeEvidence{
			Source: browserruntime.AuthenticodeSourceWindows, Publisher: "Google LLC",
			CertificateSHA256: strings.Repeat("b", 64), SignatureVerified: true,
			SameOpenHandleVerified: true, CacheOnlyVerification: true,
			PublisherPolicyMatched:    true,
			PublisherPolicyVersion:    browserruntime.BrowserPublisherPolicyVersion,
			PublisherEvidenceComplete: true,
		},
		SameHandleBytesRevalidated: true, SameFilePathRevalidated: true,
		PERevalidated: true, ReviewEligible: true, StartBlocked: true, MetadataOnly: true,
	}
	acceptance.Fingerprint = browserLaunchFixtureFingerprint(t, acceptance)
	if err := browserruntime.ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		t.Fatal(err)
	}
	ownership, err := browserruntime.BuildProfileOwnershipPlan(sessionPlan, identity,
		filepath.Join(t.TempDir(), browserruntime.ProfileRuntimeRootName))
	if err != nil {
		t.Fatal(err)
	}
	return sessionPlan, identity, acceptance, ownership
}

func browserLaunchFixtureFingerprint(t *testing.T, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case browserruntime.BrowserExecutableIdentity:
		typed.Fingerprint = ""
		value = typed
	case browserruntime.BrowserAcceptanceCandidate:
		typed.Fingerprint = ""
		value = typed
	default:
		t.Fatalf("unsupported browser launch fixture fingerprint type %T", value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
