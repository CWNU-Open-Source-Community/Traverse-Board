package browserruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

type browserRuntimeFacts struct {
	session         SessionPlan
	identity        BrowserExecutableIdentity
	acceptance      BrowserAcceptanceCandidate
	ownership       ProfileOwnershipPlan
	attempt         BrowserLaunchAttempt
	launchLease     BrowserLaunchLease
	review          BrowserLaunchReview
	networkEvidence BrowserNetworkContainmentEvidence
	networkReview   BrowserNetworkContainmentReview
	networkPlan     BrowserNetworkContainmentPlan
	permission      domain.RunBrowserCDPPermissionSnapshot
	authorization   BrowserStartAuthorization
	now             time.Time
}

func newLoopbackBrowserRuntimeFacts(t *testing.T) browserRuntimeFacts {
	t.Helper()
	_, identity, acceptance, _ := browserLaunchFixture(t)
	session, err := BuildSessionPlan(NewSessionPlanRequest{
		SessionID: "session-production-browser", RunID: "run-production-browser",
		WorkspaceID: "workspace-production-browser", ProfileID: ProfileSafeWeb,
		Targets: []string{"http://127.0.0.1:18080"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := BuildProfileOwnershipPlan(session, identity,
		filepath.Join(directTestPath(t, t.TempDir()), ProfileRuntimeRootName))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Round(time.Millisecond)
	permission, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-production", domain.Run{ID: session.RunID,
			MissionID: "mission-production-browser", Status: domain.RunCreated, CreatedAt: now},
		domain.Mission{ID: "mission-production-browser", CreatedAt: now},
		"runtime-test-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	return buildBrowserRuntimeFacts(t, session, identity, acceptance, ownership,
		permission, now)
}

func buildBrowserRuntimeFacts(t *testing.T, session SessionPlan,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
	ownership ProfileOwnershipPlan, permission domain.RunBrowserCDPPermissionSnapshot,
	now time.Time,
) browserRuntimeFacts {
	t.Helper()
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-production", now)
	if err != nil {
		t.Fatal(err)
	}
	launchLease, err := BuildBrowserLaunchLease(attempt, "browser-lease-production",
		"browser-runtime-worker", now, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	review, err := BuildBrowserLaunchReview(session, identity, acceptance, ownership,
		attempt, launchLease, "browser-review-production",
		BrowserLaunchReviewAcceptCandidate, "independent-runtime-operator",
		"browser-review-operation-production", "", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	networkEvidence, err := BuildBrowserNetworkContainmentEvidence(identity, acceptance,
		BrowserNetworkProbeReport{
			ID: "browser-network-evidence-production", CollectorIdentity: "network-probe-operator",
			Adapter:                WindowsWFPBrowserContainmentAdapterName,
			DynamicSessionObserved: true, AtomicInstallObserved: true,
			ExactTargetObserved: true, WrongPortDenied: true,
			WrongLoopbackAddressDenied: true, NonLoopbackAddressDenied: true,
			IPv6Denied: true, RuleCleanupObserved: true, Production: true,
			StartedAt: now.Add(2 * time.Second), CompletedAt: now.Add(3 * time.Second),
		})
	if err != nil {
		t.Fatal(err)
	}
	networkReview, err := BuildBrowserNetworkContainmentReview(networkEvidence,
		identity, acceptance, "browser-network-review-production",
		"independent-network-reviewer", true, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	networkPlan, err := BuildBrowserNetworkContainmentPlan(session, identity,
		acceptance, networkEvidence, networkReview, now.Add(5*time.Second),
		networkEvidence.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := AuthorizeSafeWebStart(session, identity, acceptance,
		ownership, attempt, launchLease, review, networkEvidence, networkReview,
		networkPlan, permission,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true},
		ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
			DisposableProfileEnabled: true, NetworkContainmentEnabled: true,
			RestrictedCDPEnabled: true}, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return browserRuntimeFacts{session: session, identity: identity,
		acceptance: acceptance, ownership: ownership, attempt: attempt,
		launchLease: launchLease, review: review, networkEvidence: networkEvidence,
		networkReview: networkReview, networkPlan: networkPlan, permission: permission,
		authorization: authorization, now: now.Add(6 * time.Second)}
}

func (facts browserRuntimeFacts) materialize(t *testing.T) ProfileRuntimeLease {
	t.Helper()
	lease, err := MaterializeDisposableProfile(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestSafeWebAuthorizationRequiresRestrictedLiteralLoopbackAndRuntimeGates(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	if err := ValidateBrowserStartAuthorization(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission); err != nil {
		t.Fatal(err)
	}

	publicSession, err := BuildSessionPlan(NewSessionPlanRequest{
		SessionID: "session-public-browser", RunID: facts.session.RunID,
		WorkspaceID: facts.session.WorkspaceID, ProfileID: ProfileSafeWeb,
		Targets: []string{"https://example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	publicOwnership, err := BuildProfileOwnershipPlan(publicSession, facts.identity,
		filepath.Join(directTestPath(t, t.TempDir()), ProfileRuntimeRootName))
	if err != nil {
		t.Fatal(err)
	}
	publicAttempt, err := BuildBrowserLaunchAttempt(publicSession, facts.identity,
		facts.acceptance, publicOwnership, "browser-attempt-public", facts.now)
	if err != nil {
		t.Fatal(err)
	}
	publicLease, err := BuildBrowserLaunchLease(publicAttempt, "browser-lease-public",
		"browser-public-worker", facts.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publicReview, err := BuildBrowserLaunchReview(publicSession, facts.identity,
		facts.acceptance, publicOwnership, publicAttempt, publicLease,
		"browser-review-public", BrowserLaunchReviewAcceptCandidate,
		"browser-public-operator", "browser-public-operation", "",
		facts.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AuthorizeSafeWebStart(publicSession, facts.identity, facts.acceptance,
		publicOwnership, publicAttempt, publicLease, publicReview, facts.networkEvidence,
		facts.networkReview, facts.networkPlan, facts.permission,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true},
		ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
			DisposableProfileEnabled: true, NetworkContainmentEnabled: true},
		facts.now.Add(2*time.Second)); err == nil {
		t.Fatal("public browser target unexpectedly received runtime authority")
	}

	full, err := facts.permission.Next("browser-cdp-production-full",
		domain.RunBrowserCDPPermissionFullDebug, true, "runtime-test-operator",
		"test full debug boundary", facts.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AuthorizeSafeWebStart(facts.session, facts.identity, facts.acceptance,
		facts.ownership, facts.attempt, facts.launchLease, facts.review,
		facts.networkEvidence, facts.networkReview, facts.networkPlan, full,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true,
			FullDebugEnabled: true}, ProductionRuntimeCapabilities{
			SafeWebStartEnabled: true, DisposableProfileEnabled: true,
			NetworkContainmentEnabled: true, RestrictedCDPEnabled: true}, facts.now); err == nil {
		t.Fatal("full-debug CDP permission unexpectedly entered the restricted runtime")
	}
	if _, err := AuthorizeSafeWebStart(facts.session, facts.identity, facts.acceptance,
		facts.ownership, facts.attempt, facts.launchLease, facts.review,
		facts.networkEvidence, facts.networkReview, facts.networkPlan, facts.permission,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true},
		ProductionRuntimeCapabilities{}, facts.now); err == nil {
		t.Fatal("disabled process-local runtime gates unexpectedly authorized a start")
	}
}

func TestDisposableProfileMaterializeReleaseCleanupAndRecovery(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	for _, name := range profileEnvironmentDirectoryNames {
		if info, err := os.Lstat(filepath.Join(lease.DirectoryPath, name)); err != nil ||
			!info.IsDir() {
			t.Fatalf("Profile environment directory %s missing: %v", name, err)
		}
	}
	active, err := ObserveDisposableProfile(facts.ownership, true)
	if err != nil || active.State != ProfileDirectoryOwnedActive {
		t.Fatalf("active Profile observation=%#v err=%v", active, err)
	}
	stale, err := ObserveDisposableProfile(facts.ownership, false)
	if err != nil || stale.State != ProfileDirectoryOwnedStale {
		t.Fatalf("stale Profile observation=%#v err=%v", stale, err)
	}
	if _, err := ReleaseDisposableProfile(facts.authorization, lease,
		facts.ownership, false, facts.now.Add(time.Second)); err == nil {
		t.Fatal("active process tree unexpectedly allowed Profile release")
	}
	if err := os.WriteFile(filepath.Join(lease.DirectoryPath, "browser-state"),
		[]byte("untrusted browser bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	released, err := ReleaseDisposableProfile(facts.authorization, lease,
		facts.ownership, true, facts.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveDisposableProfile(facts.ownership, false)
	if err != nil || observation.State != ProfileDirectoryOwnedReleased {
		t.Fatalf("released Profile observation=%#v err=%v", observation, err)
	}
	if err := CleanupReleasedProfile(facts.authorization, released,
		facts.ownership, false); err == nil {
		t.Fatal("non-quiescent process tree unexpectedly allowed Profile cleanup")
	}
	if err := CleanupReleasedProfile(facts.authorization, released,
		facts.ownership, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(facts.ownership.DirectoryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact disposable Profile still exists: %v", err)
	}
	if _, err := os.Lstat(facts.ownership.RootPath); err != nil {
		t.Fatalf("Profile runtime root was removed: %v", err)
	}
	if err := CleanupReleasedProfile(facts.authorization, released,
		facts.ownership, true); err == nil {
		t.Fatal("replayed Profile cleanup unexpectedly succeeded")
	}

	recoveryFacts := newLoopbackBrowserRuntimeFacts(t)
	oldLease := recoveryFacts.materialize(t)
	stale, err = ObserveDisposableProfile(recoveryFacts.ownership, false)
	if err != nil {
		t.Fatal(err)
	}
	recoveredOwnership, err := BuildRecoveredProfileOwnershipPlan(
		recoveryFacts.ownership, stale, recoveryFacts.session, recoveryFacts.identity)
	if err != nil {
		t.Fatal(err)
	}
	recoveredFacts := buildBrowserRuntimeFacts(t, recoveryFacts.session,
		recoveryFacts.identity, recoveryFacts.acceptance, recoveredOwnership,
		recoveryFacts.permission, recoveryFacts.now.Add(10*time.Second))
	recoveredLease := recoveredFacts.materialize(t)
	if recoveredLease.Generation != oldLease.Generation+1 ||
		recoveredLease.MarkerFingerprint == oldLease.MarkerFingerprint {
		t.Fatalf("Profile recovery did not fence the old generation: %#v", recoveredLease)
	}
	if err := ValidateProfileRuntimeLease(oldLease, recoveredFacts.authorization,
		recoveredOwnership); err == nil {
		t.Fatal("old Profile lease remained valid after recovery")
	}
}

func TestRemoveProfileTreeBoundedRetriesTransientWindowsStyleSharingFailure(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "owned-profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := func(path string) error {
		calls++
		if calls == 1 {
			return &os.PathError{Op: "unlinkat", Path: path, Err: os.ErrPermission}
		}
		return os.RemoveAll(path)
	}
	if err := removeProfileTreeBounded(profile, time.Second, remove); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("bounded Profile cleanup calls=%d, want 2", calls)
	}
	if _, err := os.Lstat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bounded Profile cleanup left its exact target: %v", err)
	}
}

func TestDisposableProfileRefusesForeignMarker(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	foreign := facts.ownership
	foreign.OwnerToken = strings.Repeat("f", 64)
	foreign.MarkerPayloadSHA256 = strings.Repeat("e", 64)
	foreign.Fingerprint = ""
	foreign.Fingerprint, _ = profileOwnershipFingerprint(foreign)
	marker := newProfileOwnerMarker(foreign, facts.now)
	if err := writeProfileOwnerMarkerAtomic(lease.DirectoryPath, marker); err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveDisposableProfile(facts.ownership, false)
	if err != nil || observation.State != ProfileDirectoryForeign {
		t.Fatalf("foreign Profile observation=%#v err=%v", observation, err)
	}
	if _, err := ReleaseDisposableProfile(facts.authorization, lease,
		facts.ownership, true, facts.now.Add(time.Second)); err == nil {
		t.Fatal("foreign Profile marker unexpectedly allowed release")
	}
}

type fakeBrowserProcessStarter struct {
	mu      sync.Mutex
	started []BrowserStartSpec
	process *fakeBrowserPlatformProcess
}

type fakeBrowserNetworkContainmentFactory struct {
	available bool
}

func (factory *fakeBrowserNetworkContainmentFactory) Name() string {
	return FakeBrowserContainmentAdapterName
}

func (factory *fakeBrowserNetworkContainmentFactory) Available() bool {
	return factory != nil && factory.available
}

func (factory *fakeBrowserNetworkContainmentFactory) Prepare(
	plan BrowserNetworkContainmentPlan,
) (browserNetworkContainmentGuard, error) {
	if !factory.Available() || plan.Adapter != WindowsWFPBrowserContainmentAdapterName {
		return nil, ErrBrowserRuntimeUnavailable
	}
	return &fakeBrowserNetworkContainmentGuard{
		fingerprint: strings.Repeat("a", 64),
	}, nil
}

type fakeBrowserNetworkContainmentGuard struct {
	fingerprint string
	closed      bool
}

func (guard *fakeBrowserNetworkContainmentGuard) Adapter() string {
	return WindowsWFPBrowserContainmentAdapterName
}

func (guard *fakeBrowserNetworkContainmentGuard) Fingerprint() string {
	if guard == nil {
		return ""
	}
	return guard.fingerprint
}

func (guard *fakeBrowserNetworkContainmentGuard) Close() error {
	guard.closed = true
	return nil
}

func (guard *fakeBrowserNetworkContainmentGuard) CleanupVerified() bool {
	return guard != nil && guard.closed
}

func (starter *fakeBrowserProcessStarter) Name() string    { return "fake-browser-process.v1" }
func (starter *fakeBrowserProcessStarter) Available() bool { return true }
func (starter *fakeBrowserProcessStarter) Start(_ context.Context,
	spec BrowserStartSpec,
) (browserPlatformProcess, error) {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	starter.started = append(starter.started, spec)
	starter.process = &fakeBrowserPlatformProcess{pid: 4242, spec: spec,
		done: make(chan struct{})}
	return starter.process, nil
}

type fakeBrowserPlatformProcess struct {
	mu      sync.Mutex
	pid     int
	spec    BrowserStartSpec
	done    chan struct{}
	exit    BrowserProcessExit
	hasExit bool
	once    sync.Once
}

func (process *fakeBrowserPlatformProcess) PID() int              { return process.pid }
func (process *fakeBrowserPlatformProcess) Done() <-chan struct{} { return process.done }
func (process *fakeBrowserPlatformProcess) Exit() (BrowserProcessExit, bool) {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.exit, process.hasExit
}
func (process *fakeBrowserPlatformProcess) Stop(_ context.Context, timedOut bool) error {
	process.once.Do(func() {
		process.mu.Lock()
		process.exit = newBrowserProcessExit("fake-browser-process.v1", process.spec,
			0, true, timedOut, !timedOut, process.spec.CreatedAt,
			process.spec.CreatedAt.Add(time.Millisecond))
		process.hasExit = true
		process.mu.Unlock()
		close(process.done)
	})
	return nil
}

func TestBrowserProcessControllerUsesFixedArgumentsAndBroadcastExit(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	profileLease := facts.materialize(t)
	starter := &fakeBrowserProcessStarter{}
	revalidated := 0
	controller, err := newBrowserProcessController(starter,
		func(identity BrowserExecutableIdentity,
			acceptance BrowserAcceptanceCandidate,
		) error {
			revalidated++
			if !reflect.DeepEqual(identity, facts.identity) ||
				!reflect.DeepEqual(acceptance, facts.acceptance) {
				return errors.New("revalidation received different evidence")
			}
			return nil
		}, &fakeBrowserNetworkContainmentFactory{available: true})
	if err != nil {
		t.Fatal(err)
	}
	process, err := controller.Start(t.Context(), facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission, profileLease, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() != 4242 || revalidated != 1 {
		t.Fatalf("unexpected process pid=%d revalidations=%d", process.PID(), revalidated)
	}
	spec := process.StartSpec()
	if !reflect.DeepEqual(spec.Arguments,
		fixedRestrictedBrowserArguments(facts.ownership.DirectoryPath)) ||
		spec.ShellUsed || spec.PersonalProfileUsed || spec.InitialURL != "about:blank" ||
		strings.Contains(strings.Join(spec.Arguments, " "), facts.session.Scope.Origins[0].String()) {
		t.Fatalf("browser process escaped fixed start contract: %#v", spec)
	}
	if _, ok := process.Exit(); ok {
		t.Fatal("browser process published an exit before completion")
	}
	firstWaiter, secondWaiter := process.Done(), process.Done()
	if err := process.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-firstWaiter
	<-secondWaiter
	containmentVerified, containmentErr := process.WaitForContainmentCleanup(t.Context())
	if containmentErr != nil || !containmentVerified {
		t.Fatalf("browser network containment cleanup verified=%t err=%v",
			containmentVerified, containmentErr)
	}
	exit, ok := process.Exit()
	if !ok || !exit.TreeReaped || !exit.Cancelled || exit.TimedOut ||
		exit.StartSpecFingerprint != spec.Fingerprint ||
		exit.Fingerprint != browserRuntimeFingerprint(exit) {
		t.Fatalf("unexpected browser process exit: %#v", exit)
	}
}

func TestBrowserProcessControllerRejectsMarkerTampering(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	profileLease := facts.materialize(t)
	markerPath := filepath.Join(profileLease.DirectoryPath, ProfileOwnerMarkerName)
	if err := os.WriteFile(markerPath, []byte(`{"owner":"attacker"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	starter := &fakeBrowserProcessStarter{}
	controller, err := newBrowserProcessController(starter,
		func(BrowserExecutableIdentity, BrowserAcceptanceCandidate) error { return nil },
		&fakeBrowserNetworkContainmentFactory{available: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(t.Context(), facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission, profileLease, facts.now); err == nil {
		t.Fatal("tampered Profile marker unexpectedly started a browser")
	}
	if len(starter.started) != 0 {
		t.Fatal("platform starter was called after Profile marker tampering")
	}
}
