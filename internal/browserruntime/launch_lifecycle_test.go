package browserruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserLaunchAttemptLeaseAndFakeLifecycleRemainNonAuthorizing(t *testing.T) {
	session, identity, acceptance, ownership := browserLaunchFixture(t)
	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-001", now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BuildBrowserLaunchLease(attempt, "browser-lease-001",
		"worker-private-owner", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewFakeBrowserLifecycleAdapter(attempt, lease, FakeBrowserLifecyclePlan{
		State: BrowserLifecycleOrphaned, SyntheticProcessCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := NewBrowserLifecycleBridge(adapter)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := bridge.Observe(t.Context(), attempt, lease)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != BrowserLifecycleOrphaned || !observation.Synthetic ||
		observation.SyntheticProcessCount != 3 || observation.ActualProcessObserved ||
		observation.ProcessStarted || observation.NetworkUsed ||
		observation.ProductExecutionEnabled || observation.Authority != (RuntimeAuthority{}) {
		t.Fatalf("fake lifecycle widened authority: %#v", observation)
	}
	reconciliation, err := BuildBrowserLifecycleReconciliation(attempt, lease, observation)
	if err != nil {
		t.Fatal(err)
	}
	if reconciliation.Decision != BrowserLifecycleDecisionRecoverCandidate ||
		!reconciliation.RestartRecoveryCandidate ||
		!reconciliation.CancellationFanoutRequired ||
		!reconciliation.GracefulThenForcedTermination ||
		!reconciliation.StartBlocked || !reconciliation.ApplyBlocked ||
		reconciliation.ProcessTerminationAuthorized ||
		reconciliation.FilesystemCleanupAuthorized {
		t.Fatalf("orphan reconciliation widened authority: %#v", reconciliation)
	}
}

func TestBrowserLifecycleDisabledCancellationAndTampering(t *testing.T) {
	session, identity, acceptance, ownership := browserLaunchFixture(t)
	now := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-002", now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BuildBrowserLaunchLease(attempt, "browser-lease-002",
		"worker-owner-002", now, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := NewBrowserLifecycleBridge(DisabledBrowserLifecycleAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := disabled.Observe(t.Context(), attempt, lease)
	if err != nil || observation.State != BrowserLifecycleDisabled ||
		observation.Synthetic || !observation.ProcessTreeQuiescent {
		t.Fatalf("disabled lifecycle diverged: %#v err=%v", observation, err)
	}

	adapter, err := NewFakeBrowserLifecycleAdapter(attempt, lease, FakeBrowserLifecyclePlan{
		State: BrowserLifecycleRunning, SyntheticProcessCount: 1, Delay: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := NewBrowserLifecycleBridge(adapter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cancelled, err := bridge.Observe(ctx, attempt, lease)
	if err != nil || cancelled.State != BrowserLifecycleCancelled ||
		!cancelled.ProcessTreeQuiescent || cancelled.SyntheticProcessCount != 0 {
		t.Fatalf("cancelled lifecycle diverged: %#v err=%v", cancelled, err)
	}
	lease.ProcessExecutionAuthorized = true
	if err := ValidateBrowserLaunchLease(lease, attempt); err == nil {
		t.Fatal("authorizing browser lease mutation unexpectedly passed")
	}
}

func TestBrowserLifecycleRejectsUnsealedAdapterAndInvalidProcessState(t *testing.T) {
	session, identity, acceptance, ownership := browserLaunchFixture(t)
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-003", now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BuildBrowserLaunchLease(attempt, "browser-lease-003",
		"worker-owner-003", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFakeBrowserLifecycleAdapter(attempt, lease, FakeBrowserLifecyclePlan{
		State: BrowserLifecycleExited, SyntheticProcessCount: 1,
	}); err == nil {
		t.Fatal("quiescent fake state accepted a synthetic process")
	}
	if _, err := NewFakeBrowserLifecycleAdapter(attempt, lease, FakeBrowserLifecyclePlan{
		State: BrowserLifecycleRunning,
	}); err == nil {
		t.Fatal("running fake state accepted an empty synthetic process tree")
	}
}

func browserLaunchFixture(t *testing.T) (SessionPlan, BrowserExecutableIdentity,
	BrowserAcceptanceCandidate, ProfileOwnershipPlan,
) {
	t.Helper()
	session, err := BuildSessionPlan(NewSessionPlanRequest{
		SessionID: "session-browser-launch", RunID: "run-browser-launch",
		WorkspaceID: "workspace-browser-launch", ProfileID: ProfileSafeWeb,
		Targets: []string{"https://example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := directTestPath(t, t.TempDir())
	spec := knownSpec(t, DiscoveryRootProgramFiles, BrowserProductChrome,
		BrowserChannelStable)
	path := filepath.Join(append([]string{root}, spec.Components...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, minimalPEImage(t, "amd64"), 0o600); err != nil {
		t.Fatal(err)
	}
	identities, err := discoverBrowserExecutables([]DiscoveryRoot{
		{ID: DiscoveryRootProgramFiles, Path: root},
	}, []browserExecutableSpec{spec}, browserExecutableVersion)
	if err != nil || len(identities) != 1 {
		t.Fatalf("discover browser launch fixture: count=%d err=%v", len(identities), err)
	}
	identity := identities[0]
	acceptance, err := buildBrowserAcceptanceCandidate(identity,
		func(*os.File, string) (AuthenticodeEvidence, error) {
			return AuthenticodeEvidence{
				Source: AuthenticodeSourceWindows, Publisher: "Google LLC",
				CertificateSHA256: strings.Repeat("a", 64), SignatureVerified: true,
				SameOpenHandleVerified: true, CacheOnlyVerification: true,
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	profileRoot := filepath.Join(directTestPath(t, t.TempDir()), ProfileRuntimeRootName)
	ownership, err := BuildProfileOwnershipPlan(session, identity, profileRoot)
	if err != nil {
		t.Fatal(err)
	}
	return session, identity, acceptance, ownership
}
