package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
)

type fakeFullCDPProductionStore struct {
	mu                   sync.Mutex
	run                  domain.Run
	mission              domain.Mission
	browserPermission    domain.RunBrowserCDPPermissionSnapshot
	executionPermission  domain.RunExecutionPermissionSnapshot
	opened               int
	closed               int
	closeAuditAttempts   int
	closeAuditFailures   int
	closeAuditAlwaysFail bool
	closeAuditFailed     chan struct{}
	closeAuditRelease    <-chan struct{}
}

func (f *fakeFullCDPProductionStore) GetRun(context.Context, string) (domain.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.run, nil
}

func (f *fakeFullCDPProductionStore) GetMission(context.Context, string) (domain.Mission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mission, nil
}

func (f *fakeFullCDPProductionStore) GetRunBrowserCDPPermission(context.Context,
	string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.browserPermission, nil
}

func (f *fakeFullCDPProductionStore) GetRunExecutionPermission(context.Context,
	string,
) (domain.RunExecutionPermissionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.executionPermission, nil
}

func (f *fakeFullCDPProductionStore) PrepareBrowserLaunch(context.Context,
	browserruntime.SessionPlan, browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate, browserruntime.ProfileOwnershipPlan,
	string, string,
) (browserruntime.BrowserLaunchAttempt, browserruntime.BrowserLaunchLease, bool, error) {
	return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, nil
}

func (f *fakeFullCDPProductionStore) RecordBrowserLaunchReview(context.Context,
	browserruntime.SessionPlan, browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate, browserruntime.ProfileOwnershipPlan,
	browserruntime.BrowserLaunchAttempt, browserruntime.BrowserLaunchLease,
	browserruntime.BrowserLaunchReviewDecision, string, string,
) (browserruntime.BrowserLaunchReview, bool, error) {
	return browserruntime.BrowserLaunchReview{}, false, nil
}

func (f *fakeFullCDPProductionStore) RecordFullCDPSessionOpened(context.Context,
	string, string, string, string, string, string, string, time.Time, time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	return nil
}

func (f *fakeFullCDPProductionStore) RecordFullCDPSessionClosed(ctx context.Context,
	_, _, _, _, _ string, _ browserruntime.FullCDPRuntimeReceipt,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeAuditAttempts++
	if f.closeAuditFailures > 0 || f.closeAuditAlwaysFail {
		if f.closeAuditFailures > 0 {
			f.closeAuditFailures--
		}
		if f.closeAuditFailed != nil {
			close(f.closeAuditFailed)
			f.closeAuditFailed = nil
		}
		if f.closeAuditRelease != nil {
			select {
			case <-f.closeAuditRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return errors.New("injected transient Full CDP close audit failure")
	}
	f.closed++
	return nil
}

type fakeManagedFullCDPRuntime struct {
	mu         sync.Mutex
	runtimeID  string
	runID      string
	sessionID  string
	startedAt  time.Time
	expiresAt  time.Time
	done       chan struct{}
	closeOnce  sync.Once
	closes     int
	receipt    browserruntime.FullCDPRuntimeReceipt
	receiptErr error
}

func newFakeManagedFullCDPRuntime(request browserruntime.FullCDPManagedLaunchRequest,
) *fakeManagedFullCDPRuntime {
	startedAt := time.Now().UTC()
	return &fakeManagedFullCDPRuntime{
		runtimeID: request.RuntimeID, runID: request.Session.RunID,
		sessionID: request.Session.SessionID, startedAt: startedAt,
		expiresAt: startedAt.Add(time.Hour), done: make(chan struct{}),
	}
}

func (f *fakeManagedFullCDPRuntime) Close(_ context.Context, _ string) (
	browserruntime.FullCDPRuntimeReceipt, error,
) {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.closes++
		completedAt := time.Now().UTC()
		f.receipt, f.receiptErr = sealFakeFullCDPRuntimeReceipt(
			browserruntime.FullCDPRuntimeReceipt{
				ProtocolVersion: browserruntime.FullCDPRuntimeReceiptProtocolVersion,
				RuntimeID:       f.runtimeID, RunID: f.runID, SessionID: f.sessionID,
				CDPClosed: true, ProcessTreeQuiescent: true, ProfileReleased: true,
				ProfileCleaned: true, Succeeded: true, FullCDPUsed: true,
				StartedAt: f.startedAt, CompletedAt: completedAt,
			})
		close(f.done)
		f.mu.Unlock()
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.receipt, f.receiptErr
}

func (f *fakeManagedFullCDPRuntime) Done() <-chan struct{} { return f.done }
func (f *fakeManagedFullCDPRuntime) ExpiresAt() time.Time  { return f.expiresAt }

type malformedReceiptFullCDPRuntime struct {
	base *fakeManagedFullCDPRuntime
}

func (f *malformedReceiptFullCDPRuntime) Close(ctx context.Context, reason string) (
	browserruntime.FullCDPRuntimeReceipt, error,
) {
	receipt, err := f.base.Close(ctx, reason)
	receipt.Fingerprint = ""
	return receipt, err
}

func (f *malformedReceiptFullCDPRuntime) Done() <-chan struct{} {
	return f.base.Done()
}

func (f *malformedReceiptFullCDPRuntime) ExpiresAt() time.Time {
	return f.base.ExpiresAt()
}

type recoveringManagedFullCDPRuntime struct {
	mu             sync.Mutex
	runtimeID      string
	runID          string
	sessionID      string
	startedAt      time.Time
	expiresAt      time.Time
	done           chan struct{}
	failedOnce     chan struct{}
	allowRecovery  chan struct{}
	secondEntered  chan struct{}
	failedOnceSync sync.Once
	secondSync     sync.Once
	doneSync       sync.Once
	closes         int
}

func newRecoveringManagedFullCDPRuntime(
	request browserruntime.FullCDPManagedLaunchRequest,
) *recoveringManagedFullCDPRuntime {
	startedAt := time.Now().UTC()
	return &recoveringManagedFullCDPRuntime{
		runtimeID: request.RuntimeID, runID: request.Session.RunID,
		sessionID: request.Session.SessionID, startedAt: startedAt,
		expiresAt: startedAt.Add(time.Hour), done: make(chan struct{}),
		failedOnce: make(chan struct{}), allowRecovery: make(chan struct{}),
		secondEntered: make(chan struct{}),
	}
}

func (f *recoveringManagedFullCDPRuntime) Close(_ context.Context, _ string) (
	browserruntime.FullCDPRuntimeReceipt, error,
) {
	f.mu.Lock()
	f.closes++
	closeAttempt := f.closes
	f.mu.Unlock()
	completedAt := time.Now().UTC()
	if closeAttempt == 1 {
		f.failedOnceSync.Do(func() { close(f.failedOnce) })
		receipt, receiptErr := sealFakeFullCDPRuntimeReceipt(
			browserruntime.FullCDPRuntimeReceipt{
				ProtocolVersion: browserruntime.FullCDPRuntimeReceiptProtocolVersion,
				RuntimeID:       f.runtimeID, RunID: f.runID, SessionID: f.sessionID,
				CDPClosed: true, ProcessTreeQuiescent: false, ProfileReleased: false,
				ProfileCleaned: false, Succeeded: false, RecoveryRequired: true,
				FailureCode: "process_tree_not_quiescent", FullCDPUsed: true,
				StartedAt: f.startedAt, CompletedAt: completedAt,
			})
		return receipt, errors.Join(errors.New("injected process cleanup failure"),
			receiptErr)
	}
	f.secondSync.Do(func() { close(f.secondEntered) })
	<-f.allowRecovery
	f.doneSync.Do(func() { close(f.done) })
	receipt, receiptErr := sealFakeFullCDPRuntimeReceipt(
		browserruntime.FullCDPRuntimeReceipt{
			ProtocolVersion: browserruntime.FullCDPRuntimeReceiptProtocolVersion,
			RuntimeID:       f.runtimeID, RunID: f.runID, SessionID: f.sessionID,
			CDPClosed: true, ProcessTreeQuiescent: true, ProfileReleased: true,
			ProfileCleaned: true, Succeeded: true, FullCDPUsed: true,
			StartedAt: f.startedAt, CompletedAt: completedAt,
		})
	return receipt, receiptErr
}

func (f *recoveringManagedFullCDPRuntime) Done() <-chan struct{} { return f.done }
func (f *recoveringManagedFullCDPRuntime) ExpiresAt() time.Time  { return f.expiresAt }

func sealFakeFullCDPRuntimeReceipt(receipt browserruntime.FullCDPRuntimeReceipt) (
	browserruntime.FullCDPRuntimeReceipt, error,
) {
	if !receipt.CompletedAt.After(receipt.StartedAt) {
		receipt.CompletedAt = receipt.StartedAt.Add(time.Nanosecond)
	}
	receipt.AttemptFingerprint = strings.Repeat("1", 64)
	receipt.StartAuthorization = strings.Repeat("2", 64)
	receipt.SessionAuthorization = strings.Repeat("3", 64)
	if receipt.ProcessTreeQuiescent {
		receipt.ProcessExitFingerprint = strings.Repeat("4", 64)
	}
	if receipt.ProfileReleased {
		receipt.ReleasedProfileFingerprint = strings.Repeat("5", 64)
	}
	return browserruntime.SealFullCDPRuntimeReceipt(receipt)
}

func TestSealFakeFullCDPRuntimeReceipt(t *testing.T) {
	startedAt := time.Now().UTC()
	_, err := sealFakeFullCDPRuntimeReceipt(browserruntime.FullCDPRuntimeReceipt{
		ProtocolVersion: browserruntime.FullCDPRuntimeReceiptProtocolVersion,
		RuntimeID:       "runtime-test", RunID: "run-test", SessionID: "session-test",
		CDPClosed: true, ProcessTreeQuiescent: true, ProfileReleased: true,
		ProfileCleaned: true, Succeeded: true, FullCDPUsed: true,
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newFullCDPProductionServiceFixture(t *testing.T) (*FullCDPProductionService,
	*fakeFullCDPProductionStore, *int, **fakeManagedFullCDPRuntime,
) {
	t.Helper()
	now := time.Now().UTC().Round(time.Millisecond)
	run := domain.Run{ID: "run-full-cdp-production", MissionID: "mission-full-cdp-production",
		SessionID: "session-full-cdp-production", Status: domain.RunCreated, CreatedAt: now}
	mission := domain.Mission{ID: run.MissionID, WorkspaceID: "workspace-full-cdp-production",
		CreatedAt: now}
	browserInitial, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-production-initial", run, mission, "runtime-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	browserFull, err := browserInitial.Next("browser-cdp-production-full",
		domain.RunBrowserCDPPermissionFullDebug, true, "runtime-operator",
		"confirmed full CDP", now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	executionInitial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"execution-production-initial", run, mission, "runtime-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	executionFull, err := executionInitial.Next("execution-production-full",
		domain.RunExecutionPermissionFullAccess, true, "runtime-operator",
		"confirmed full access", now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	if _, err := authority.ActivateRunFullAccess(executionFull); err != nil {
		t.Fatal(err)
	}
	executionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	store := &fakeFullCDPProductionStore{run: run, mission: mission,
		browserPermission: browserFull, executionPermission: executionFull}
	identity, acceptance := fullCDPIdentityAcceptance(t)
	launches := 0
	var latest *fakeManagedFullCDPRuntime
	service := &FullCDPProductionService{
		store: store,
		runtimeCapabilities: browserruntime.FullCDPRuntimeCapabilities{
			StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true},
		permissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true},
		executionCapabilities: executionCapabilities,
		profileRoot:           filepath.Join(t.TempDir(), browserruntime.ProfileRuntimeRootName),
		discover: func() ([]browserruntime.BrowserExecutableIdentity, error) {
			return []browserruntime.BrowserExecutableIdentity{identity}, nil
		},
		accept: func(browserruntime.BrowserExecutableIdentity) (
			browserruntime.BrowserAcceptanceCandidate, error,
		) {
			return acceptance, nil
		},
		latestByRun:     make(map[string]*fullCDPSessionEntry),
		openOperations:  make(map[string]fullCDPOperationRecord),
		closeOperations: make(map[string]fullCDPOperationRecord),
	}
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		launches++
		latest = newFakeManagedFullCDPRuntime(request)
		return latest, nil
	}
	return service, store, &launches, &latest
}

func fullCDPOpenFixture(service *FullCDPProductionService,
	store *fakeFullCDPProductionStore, operationKey string,
) OpenFullCDPSessionRequest {
	return OpenFullCDPSessionRequest{
		RunID: store.run.ID, Target: "http://127.0.0.1:18080",
		Browser:                              FullCDPBrowserSelection{Product: "chrome", Channel: "stable"},
		ExpectedExecutionPermissionRevision:  store.executionPermission.Revision,
		ExpectedBrowserCDPPermissionRevision: store.browserPermission.Revision,
		ConfirmFullCDP:                       true, Reason: "operator confirmed local debugging",
		OperationKey: operationKey,
	}
}

func TestManagedFullCDPLaunchResultPreservesNilInterface(t *testing.T) {
	wantErr := errors.New("launch failed after safe cleanup")
	managed, gotErr := managedFullCDPLaunchResult(nil, wantErr)
	if managed != nil {
		t.Fatalf("nil concrete runtime became a non-nil interface: %#v", managed)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("launch error = %v, want %v", gotErr, wantErr)
	}
}

func TestFullCDPProductionServiceOpenReplayConflictAndClose(t *testing.T) {
	service, store, launches, latest := newFullCDPProductionServiceFixture(t)
	request := fullCDPOpenFixture(service, store, "open-full-cdp-production")
	opened, err := service.OpenFullCDPSession(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Session.State != FullCDPSessionReady || opened.Replayed || *launches != 1 {
		t.Fatalf("unexpected open result: %+v launches=%d", opened, *launches)
	}
	replayed, err := service.OpenFullCDPSession(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Session.SessionID != opened.Session.SessionID ||
		*launches != 1 {
		t.Fatalf("unexpected replay: %+v err=%v launches=%d", replayed, err, *launches)
	}
	conflict := request
	conflict.OperationKey = "different-full-cdp-open"
	if _, err := service.OpenFullCDPSession(t.Context(), conflict); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("second active session should conflict, got %v", err)
	}
	serialized, err := json.Marshal(opened.Session)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"devtools", "websocket", "profile_path", "pid",
		"authorization_fingerprint", "127.0.0.1:0"} {
		if strings.Contains(strings.ToLower(string(serialized)), forbidden) {
			t.Fatalf("redacted session leaked %q: %s", forbidden, serialized)
		}
	}
	closeRequest := CloseFullCDPSessionRequest{RunID: store.run.ID,
		ExpectedSessionID: opened.Session.SessionID,
		OperationKey:      "close-full-cdp-production", Reason: "operator closed"}
	closed, err := service.CloseFullCDPSession(t.Context(), closeRequest)
	if err != nil || closed.Session.State != FullCDPSessionClosed || closed.Replayed {
		t.Fatalf("unexpected close: %+v err=%v", closed, err)
	}
	closeReplay, err := service.CloseFullCDPSession(t.Context(), closeRequest)
	if err != nil || !closeReplay.Replayed || closeReplay.Session.State != FullCDPSessionClosed {
		t.Fatalf("unexpected close replay: %+v err=%v", closeReplay, err)
	}
	(*latest).mu.Lock()
	closeCount := (*latest).closes
	(*latest).mu.Unlock()
	if closeCount != 1 || store.opened != 1 || store.closed != 1 {
		t.Fatalf("lifecycle count mismatch: runtime=%d opened=%d closed=%d",
			closeCount, store.opened, store.closed)
	}
}

func TestFullCDPProductionServiceConcurrentOpenReplayUsesOneReservation(t *testing.T) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	service.cachePolicy = fullCDPCachePolicy{
		terminalRetention:   time.Hour,
		latestByRunLimit:    1,
		openOperationLimit:  1,
		closeOperationLimit: 1,
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var launchMu sync.Mutex
	launchCount := 0
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		launchMu.Lock()
		launchCount++
		if launchCount == 1 {
			close(entered)
		}
		launchMu.Unlock()
		<-release
		return newFakeManagedFullCDPRuntime(request), nil
	}

	const callers = 24
	type outcome struct {
		result FullCDPSessionResult
		err    error
	}
	outcomes := make(chan outcome, callers)
	request := fullCDPOpenFixture(service, store, "open-full-cdp-concurrent-replay")
	for range callers {
		go func() {
			result, err := service.OpenFullCDPSession(context.Background(), request)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent open did not reserve a launch")
	}
	close(release)

	initial := 0
	replayed := 0
	var sessionID string
	for range callers {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("concurrent replay failed: %v", outcome.err)
		}
		if sessionID == "" {
			sessionID = outcome.result.Session.SessionID
		}
		if outcome.result.Session.SessionID != sessionID ||
			outcome.result.Session.State != FullCDPSessionReady {
			t.Fatalf("concurrent replay returned a different session: %+v",
				outcome.result)
		}
		if outcome.result.Replayed {
			replayed++
		} else {
			initial++
		}
	}
	launchMu.Lock()
	launches := launchCount
	launchMu.Unlock()
	if launches != 1 || initial != 1 || replayed != callers-1 {
		t.Fatalf("concurrent reservation launches=%d initial=%d replayed=%d",
			launches, initial, replayed)
	}
	service.mu.Lock()
	openRecords := len(service.openOperations)
	service.mu.Unlock()
	if openRecords != 1 {
		t.Fatalf("concurrent replay retained %d open records", openRecords)
	}

	_, err := service.CloseFullCDPSession(t.Context(), CloseFullCDPSessionRequest{
		RunID: store.run.ID, ExpectedSessionID: sessionID,
		OperationKey: "close-full-cdp-concurrent-replay",
		Reason:       "test cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFullCDPProductionServiceBoundedReplayRetainsNewestExactSemantics(
	t *testing.T,
) {
	service, store, launches, _ := newFullCDPProductionServiceFixture(t)
	service.cachePolicy = fullCDPCachePolicy{
		terminalRetention:   time.Hour,
		latestByRunLimit:    1,
		openOperationLimit:  2,
		closeOperationLimit: 2,
	}

	openAndClose := func(openKey, closeKey string) FullCDPSessionResult {
		t.Helper()
		opened, err := service.OpenFullCDPSession(t.Context(),
			fullCDPOpenFixture(service, store, openKey))
		if err != nil {
			t.Fatalf("open %q: %v", openKey, err)
		}
		_, err = service.CloseFullCDPSession(t.Context(), CloseFullCDPSessionRequest{
			RunID: store.run.ID, ExpectedSessionID: opened.Session.SessionID,
			OperationKey: closeKey, Reason: "bounded replay cleanup",
		})
		if err != nil {
			t.Fatalf("close %q: %v", closeKey, err)
		}
		return opened
	}

	first := openAndClose("bounded-open-1", "bounded-close-1")
	second := openAndClose("bounded-open-2", "bounded-close-2")
	thirdRequest := fullCDPOpenFixture(service, store, "bounded-open-3")
	third, err := service.OpenFullCDPSession(t.Context(), thirdRequest)
	if err != nil {
		t.Fatal(err)
	}
	if third.Session.SessionID == first.Session.SessionID ||
		third.Session.SessionID == second.Session.SessionID {
		t.Fatal("new bounded-cache open reused a prior session identity")
	}

	secondReplay, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "bounded-open-2"))
	if err != nil || !secondReplay.Replayed ||
		secondReplay.Session.SessionID != second.Session.SessionID {
		service.mu.Lock()
		firstRecord, retainedFirst := service.openOperations[fullCDPOperationDigest("bounded-open-1")]
		secondRecord, retainedSecond := service.openOperations[fullCDPOperationDigest("bounded-open-2")]
		_, retainedThird := service.openOperations[fullCDPOperationDigest("bounded-open-3")]
		service.mu.Unlock()
		t.Fatalf("newest retained key lost exact replay: %+v err=%v retained=[%t %t %t] terminal=[%s %s] accepted=[%s %s]",
			secondReplay, err, retainedFirst, retainedSecond, retainedThird,
			fullCDPTerminalTime(firstRecord.entry), fullCDPTerminalTime(secondRecord.entry),
			firstRecord.acceptedAt, secondRecord.acceptedAt)
	}
	secondConflict := fullCDPOpenFixture(service, store, "bounded-open-2")
	secondConflict.Reason = "different retained intent"
	if _, err := service.OpenFullCDPSession(t.Context(), secondConflict); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("retained key lost conflict semantics: %v", err)
	}

	_, err = service.CloseFullCDPSession(t.Context(), CloseFullCDPSessionRequest{
		RunID: store.run.ID, ExpectedSessionID: third.Session.SessionID,
		OperationKey: "bounded-close-3", Reason: "bounded replay cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "bounded-open-1"))
	if err != nil {
		t.Fatalf("evicted oldest key was not reusable: %v", err)
	}
	if firstAgain.Replayed || firstAgain.Session.SessionID == first.Session.SessionID ||
		*launches != 4 {
		t.Fatalf("oldest key was not admitted as a fresh intent: %+v launches=%d",
			firstAgain, *launches)
	}
	_, err = service.CloseFullCDPSession(t.Context(), CloseFullCDPSessionRequest{
		RunID: store.run.ID, ExpectedSessionID: firstAgain.Session.SessionID,
		OperationKey: "bounded-close-4", Reason: "bounded replay cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	openRecords := len(service.openOperations)
	closeRecords := len(service.closeOperations)
	latestRecords := len(service.latestByRun)
	service.mu.Unlock()
	if openRecords > 2 || closeRecords > 2 || latestRecords > 1 {
		t.Fatalf("bounded caches grew past limits: open=%d close=%d latest=%d",
			openRecords, closeRecords, latestRecords)
	}
}

func TestFullCDPProductionServiceRejectsCapacityWithoutEvictingInflightOpen(
	t *testing.T,
) {
	for _, test := range []struct {
		name        string
		latestLimit int
		openLimit   int
	}{
		{name: "open operation", latestLimit: 2, openLimit: 1},
		{name: "latest Run", latestLimit: 1, openLimit: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, _, _ := newFullCDPProductionServiceFixture(t)
			service.cachePolicy = fullCDPCachePolicy{
				terminalRetention:   time.Hour,
				latestByRunLimit:    test.latestLimit,
				openOperationLimit:  test.openLimit,
				closeOperationLimit: 2,
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			service.launch = func(_ context.Context,
				request browserruntime.FullCDPManagedLaunchRequest,
			) (managedFullCDPRuntime, error) {
				close(entered)
				<-release
				return newFakeManagedFullCDPRuntime(request), nil
			}
			firstDone := make(chan FullCDPSessionResult, 1)
			firstErr := make(chan error, 1)
			go func() {
				result, err := service.OpenFullCDPSession(context.Background(),
					fullCDPOpenFixture(service, store, "inflight-capacity-open-1"))
				firstDone <- result
				firstErr <- err
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("first open did not reach launch")
			}
			second := fullCDPOpenFixture(service, store, "inflight-capacity-open-2")
			second.RunID = "run-full-cdp-capacity-second"
			if _, err := service.OpenFullCDPSession(t.Context(), second); err == nil ||
				apperror.CodeOf(err) != apperror.CodeResourceExhausted {
				t.Fatalf("in-flight cache saturation returned %v", err)
			}
			service.mu.Lock()
			retained := service.latestByRun[store.run.ID]
			openRecords := len(service.openOperations)
			service.mu.Unlock()
			if retained == nil || retained.view.State != FullCDPSessionStarting ||
				openRecords != 1 {
				t.Fatalf("in-flight owner was evicted: entry=%+v open=%d",
					retained, openRecords)
			}
			close(release)
			opened := <-firstDone
			if err := <-firstErr; err != nil {
				t.Fatal(err)
			}
			_, err := service.CloseFullCDPSession(t.Context(),
				CloseFullCDPSessionRequest{
					RunID: store.run.ID, ExpectedSessionID: opened.Session.SessionID,
					OperationKey: "inflight-capacity-close", Reason: "test cleanup",
				})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFullCDPProductionServiceRejectsCapacityWithoutEvictingInflightClose(
	t *testing.T,
) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	service.cachePolicy = fullCDPCachePolicy{
		terminalRetention:   time.Hour,
		latestByRunLimit:    1,
		openOperationLimit:  1,
		closeOperationLimit: 1,
	}
	var runtime *recoveringManagedFullCDPRuntime
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		runtime = newRecoveringManagedFullCDPRuntime(request)
		return runtime, nil
	}
	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "inflight-close-capacity-open"))
	if err != nil {
		t.Fatal(err)
	}
	firstClose := CloseFullCDPSessionRequest{
		RunID: store.run.ID, ExpectedSessionID: opened.Session.SessionID,
		OperationKey: "inflight-close-capacity-1", Reason: "test cleanup",
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.CloseFullCDPSession(cancelled, firstClose); err == nil ||
		apperror.CodeOf(err) != apperror.CodeCancelled {
		t.Fatalf("cancelled first close returned %v", err)
	}
	select {
	case <-runtime.secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not enter recovery")
	}
	secondClose := firstClose
	secondClose.OperationKey = "inflight-close-capacity-2"
	if _, err := service.CloseFullCDPSession(t.Context(), secondClose); err == nil ||
		apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("in-flight close cache saturation returned %v", err)
	}
	service.mu.Lock()
	entry := service.latestByRun[store.run.ID]
	closeRecords := len(service.closeOperations)
	service.mu.Unlock()
	if entry == nil || entry.runtime == nil || entry.closeFinished || closeRecords != 1 {
		t.Fatalf("in-flight close owner was evicted: entry=%+v close=%d",
			entry, closeRecords)
	}
	close(runtime.allowRecovery)
	select {
	case <-entry.closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("close recovery did not finish")
	}
}

func TestFullCDPProductionServicePrunesOnlyExpiredResourceSafeRecords(t *testing.T) {
	service, _, _, _ := newFullCDPProductionServiceFixture(t)
	service.cachePolicy = fullCDPCachePolicy{
		terminalRetention:   time.Hour,
		latestByRunLimit:    2,
		openOperationLimit:  2,
		closeOperationLimit: 2,
	}
	now := time.Now().UTC()
	completedAt := now.Add(-2 * time.Hour)
	safeDone := make(chan struct{})
	close(safeDone)
	safe := &fullCDPSessionEntry{
		view: FullCDPSessionView{
			RunID: "run-expired-safe", State: FullCDPSessionClosed,
			CompletedAt: &completedAt,
		},
		openFinished: true, closeDone: safeDone, closeFinished: true,
		terminalSequence: 1,
	}
	unsafeDone := make(chan struct{})
	unsafe := &fullCDPSessionEntry{
		view: FullCDPSessionView{
			RunID: "run-expired-inflight", State: FullCDPSessionClosing,
		},
		openFinished: true, closeDone: unsafeDone,
	}
	service.mu.Lock()
	service.openOperations = map[string]fullCDPOperationRecord{
		"safe-open": {entry: safe, acceptedAt: completedAt},
		"live-open": {entry: unsafe, acceptedAt: completedAt},
	}
	service.closeOperations = map[string]fullCDPOperationRecord{
		"safe-close": {entry: safe, acceptedAt: completedAt},
		"live-close": {entry: unsafe, acceptedAt: completedAt},
	}
	service.latestByRun = map[string]*fullCDPSessionEntry{
		safe.view.RunID: safe, unsafe.view.RunID: unsafe,
	}
	service.pruneTerminalCachesLocked(now)
	_, safeOpenRetained := service.openOperations["safe-open"]
	_, liveOpenRetained := service.openOperations["live-open"]
	_, safeCloseRetained := service.closeOperations["safe-close"]
	_, liveCloseRetained := service.closeOperations["live-close"]
	_, safeLatestRetained := service.latestByRun[safe.view.RunID]
	_, liveLatestRetained := service.latestByRun[unsafe.view.RunID]
	service.mu.Unlock()
	if safeOpenRetained || safeCloseRetained || safeLatestRetained ||
		!liveOpenRetained || !liveCloseRetained || !liveLatestRetained {
		t.Fatalf("TTL pruning crossed resource-safety boundary: safe=[%t %t %t] live=[%t %t %t]",
			safeOpenRetained, safeCloseRetained, safeLatestRetained,
			liveOpenRetained, liveCloseRetained, liveLatestRetained)
	}
}

func TestFullCDPProductionServiceRevocationAutomaticallyCloses(t *testing.T) {
	service, store, _, latest := newFullCDPProductionServiceFixture(t)
	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-revocation"))
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	initial, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-production-revoked", store.run, store.mission,
		"runtime-operator", time.Now().UTC())
	if err == nil {
		store.browserPermission = initial
	}
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, getErr := service.GetFullCDPSession(t.Context(), store.run.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if view.State == FullCDPSessionClosed {
			if view.SessionID != opened.Session.SessionID ||
				view.CloseReason != FullCDPClosePermissionRevoked {
				t.Fatalf("unexpected revoked state: %+v", view)
			}
			(*latest).mu.Lock()
			closeCount := (*latest).closes
			(*latest).mu.Unlock()
			if closeCount != 1 {
				t.Fatalf("revocation cleanup count = %d", closeCount)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("revoked Full CDP session did not close")
}

func TestFullCDPProductionServiceCloseIntentSurvivesClientCancellationDuringStart(t *testing.T) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		close(entered)
		<-release
		return newFakeManagedFullCDPRuntime(request), nil
	}
	openDone := make(chan error, 1)
	go func() {
		_, err := service.OpenFullCDPSession(context.Background(),
			fullCDPOpenFixture(service, store, "open-full-cdp-cancelled-close"))
		openDone <- err
	}()
	<-entered
	starting, err := service.GetFullCDPSession(t.Context(), store.run.ID)
	if err != nil || starting.State != FullCDPSessionStarting {
		t.Fatalf("expected starting Full CDP session, got %+v err=%v", starting, err)
	}
	request := CloseFullCDPSessionRequest{RunID: store.run.ID,
		ExpectedSessionID: starting.SessionID,
		OperationKey:      "close-full-cdp-cancelled-client",
		Reason:            "client disconnected after close intent"}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.CloseFullCDPSession(cancelled, request); err == nil ||
		apperror.CodeOf(err) != apperror.CodeCancelled {
		t.Fatalf("cancelled close caller should stop waiting, got %v", err)
	}
	close(release)
	if err := <-openDone; err != nil {
		t.Fatalf("open should finish under the accepted cleanup intent: %v", err)
	}
	retryContext, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer retryCancel()
	replayed, err := service.CloseFullCDPSession(retryContext, request)
	if err != nil || !replayed.Replayed || replayed.Session.State != FullCDPSessionClosed {
		t.Fatalf("same-key close retry did not observe completed cleanup: %+v err=%v",
			replayed, err)
	}
	if store.opened != 1 || store.closed != 1 {
		t.Fatalf("cancelled close lifecycle audit counts opened=%d closed=%d",
			store.opened, store.closed)
	}
}

func TestFullCDPProductionServiceRejectsNonLoopbackBeforeLaunchPreparation(t *testing.T) {
	service, store, launches, _ := newFullCDPProductionServiceFixture(t)
	request := fullCDPOpenFixture(service, store, "open-full-cdp-public-target")
	request.Target = "https://example.com/"
	if _, err := service.OpenFullCDPSession(t.Context(), request); err == nil ||
		apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("public Full CDP target should be rejected, got %v", err)
	}
	if *launches != 0 || store.opened != 0 {
		t.Fatalf("invalid target reached launch/audit: launches=%d opened=%d",
			*launches, store.opened)
	}
}

func TestFullCDPProductionServiceRetainsFailedLaunchOwnerUntilRecovery(t *testing.T) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	var runtime *recoveringManagedFullCDPRuntime
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		runtime = newRecoveringManagedFullCDPRuntime(request)
		return runtime, errors.New("injected Full CDP transport open failure")
	}

	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-recoverable-failure"))
	if err == nil || apperror.CodeOf(err) != apperror.CodeUnavailable ||
		opened.Session.State != FullCDPSessionClosing {
		t.Fatalf("recoverable failed launch was not exposed as Closing: %+v err=%v",
			opened, err)
	}
	select {
	case <-runtime.failedOnce:
	case <-time.After(2 * time.Second):
		t.Fatal("failed launch owner did not begin cleanup")
	}
	select {
	case <-runtime.secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("failed launch owner did not retry cleanup")
	}

	service.mu.Lock()
	active := service.active
	entry := service.latestByRun[store.run.ID]
	view := entry.view
	closeDone := entry.closeDone
	service.mu.Unlock()
	store.mu.Lock()
	openedAudits, closedAudits := store.opened, store.closed
	store.mu.Unlock()
	if active != 1 || view.State != FullCDPSessionClosing ||
		openedAudits != 0 || closedAudits != 0 {
		t.Fatalf("failed launch released ownership before recovery: active=%d view=%+v opened=%d closed=%d",
			active, view, openedAudits, closedAudits)
	}
	select {
	case <-closeDone:
		t.Fatal("failed launch cleanup completed before its resources recovered")
	default:
	}

	close(runtime.allowRecovery)
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("failed launch owner did not finish after recovery")
	}
	service.mu.Lock()
	active = service.active
	view = entry.view
	retainedRuntime := entry.runtime
	service.mu.Unlock()
	store.mu.Lock()
	openedAudits, closedAudits = store.opened, store.closed
	store.mu.Unlock()
	if active != 0 || view.State != FullCDPSessionFailed ||
		view.FailureCode == "" || retainedRuntime != nil ||
		openedAudits != 0 || closedAudits != 1 {
		t.Fatalf("failed launch recovery did not release exactly once: active=%d view=%+v runtime=%T opened=%d closed=%d",
			active, view, retainedRuntime, openedAudits, closedAudits)
	}
}

func TestFullCDPProductionServiceAuditsResourceSafeFailedLaunch(t *testing.T) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	var runtime *fakeManagedFullCDPRuntime
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		runtime = newFakeManagedFullCDPRuntime(request)
		return runtime, errors.New("injected launch failure after safe cleanup")
	}

	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-safe-failure"))
	if err == nil || apperror.CodeOf(err) != apperror.CodeUnavailable ||
		opened.Session.State != FullCDPSessionClosing {
		t.Fatalf("resource-safe failed launch was not retained for audit: %+v err=%v",
			opened, err)
	}
	service.mu.Lock()
	entry := service.latestByRun[store.run.ID]
	closeDone := entry.closeDone
	service.mu.Unlock()
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("resource-safe failed launch did not finish its close audit")
	}

	service.mu.Lock()
	active, view, retainedRuntime := service.active, entry.view, entry.runtime
	service.mu.Unlock()
	store.mu.Lock()
	openedAudits, closedAudits := store.opened, store.closed
	store.mu.Unlock()
	if active != 0 || view.State != FullCDPSessionFailed ||
		retainedRuntime != nil || openedAudits != 0 || closedAudits != 1 {
		t.Fatalf("resource-safe failed launch terminal state is incomplete: active=%d view=%+v runtime=%T opened=%d closed=%d",
			active, view, retainedRuntime, openedAudits, closedAudits)
	}
}

func TestFullCDPProductionServiceRetriesCloseAuditAfterReleasingResources(t *testing.T) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-audit-retry"))
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.closeAuditFailures = 1
	store.closeAuditFailed = make(chan struct{})
	firstAuditFailed := store.closeAuditFailed
	store.mu.Unlock()

	closeResult := make(chan FullCDPSessionResult, 1)
	closeErrors := make(chan error, 1)
	go func() {
		result, closeErr := service.CloseFullCDPSession(context.Background(),
			CloseFullCDPSessionRequest{RunID: store.run.ID,
				ExpectedSessionID: opened.Session.SessionID,
				OperationKey:      "close-full-cdp-audit-retry",
				Reason:            "verify durable close audit retry"})
		closeResult <- result
		closeErrors <- closeErr
	}()
	select {
	case <-firstAuditFailed:
	case <-time.After(2 * time.Second):
		t.Fatal("first Full CDP close audit attempt did not fail")
	}

	service.mu.Lock()
	entry := service.latestByRun[store.run.ID]
	active, view, runtime := service.active, entry.view, entry.runtime
	closeDone := entry.closeDone
	service.mu.Unlock()
	if active != 0 || view.State != FullCDPSessionClosing || runtime != nil ||
		!view.ProcessTreeQuiescent || !view.ProfileCleaned {
		t.Fatalf("audit-pending close retained safe resources: active=%d view=%+v runtime=%T",
			active, view, runtime)
	}
	select {
	case <-closeDone:
		t.Fatal("audit-pending close completed before its durable audit retry")
	default:
	}

	result := <-closeResult
	if closeErr := <-closeErrors; closeErr != nil ||
		result.Session.State != FullCDPSessionClosed ||
		result.Session.FailureCode != "" {
		t.Fatalf("Full CDP close audit retry result=%+v err=%v", result, closeErr)
	}
	store.mu.Lock()
	attempts, closed := store.closeAuditAttempts, store.closed
	store.mu.Unlock()
	if attempts != 2 || closed != 1 {
		t.Fatalf("Full CDP close audit attempts=%d durable=%d", attempts, closed)
	}
}

func TestFullCDPProductionServiceRejectsMalformedReceiptBeforeReleaseOrAudit(
	t *testing.T,
) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	var malformed *malformedReceiptFullCDPRuntime
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		malformed = &malformedReceiptFullCDPRuntime{
			base: newFakeManagedFullCDPRuntime(request),
		}
		return malformed, nil
	}
	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-malformed-receipt"))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := service.CloseFullCDPSession(t.Context(),
		CloseFullCDPSessionRequest{RunID: store.run.ID,
			ExpectedSessionID: opened.Session.SessionID,
			OperationKey:      "close-full-cdp-malformed-receipt",
			Reason:            "verify malformed receipt rejection"})
	if err == nil || apperror.CodeOf(err) != apperror.CodeInternal ||
		closed.Session.State != FullCDPSessionFailed ||
		closed.Session.FailureCode != "receipt_validation_failed" {
		t.Fatalf("malformed receipt result=%+v err=%v", closed, err)
	}
	service.mu.Lock()
	entry := service.latestByRun[store.run.ID]
	active, retainedRuntime, resourcesReleased := service.active, entry.runtime,
		entry.resourcesReleased
	service.mu.Unlock()
	store.mu.Lock()
	closeAuditAttempts := store.closeAuditAttempts
	store.mu.Unlock()
	if active != 1 || retainedRuntime != malformed || resourcesReleased ||
		closeAuditAttempts != 0 {
		t.Fatalf("malformed receipt released or audited ownership: active=%d runtime=%T released=%t audits=%d",
			active, retainedRuntime, resourcesReleased, closeAuditAttempts)
	}
	retry := fullCDPOpenFixture(service, store,
		"open-full-cdp-after-malformed-receipt")
	if _, retryErr := service.OpenFullCDPSession(t.Context(), retry); retryErr == nil ||
		apperror.CodeOf(retryErr) != apperror.CodeConflict {
		t.Fatalf("retained malformed-receipt owner was overwritten: %v", retryErr)
	}
}

func TestFullCDPProductionServiceShutdownDetachesCloseAuditBeforeReturn(
	t *testing.T,
) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	if _, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-detach-audit")); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.closeAuditAlwaysFail = true
	store.closeAuditFailed = make(chan struct{})
	releaseAudit := make(chan struct{})
	store.closeAuditRelease = releaseAudit
	firstAuditFailed := store.closeAuditFailed
	store.mu.Unlock()

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()
		shutdownDone <- service.Close(ctx)
	}()
	select {
	case <-firstAuditFailed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not reach its close audit")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before its in-flight store call drained: %v", err)
	case <-time.After(125 * time.Millisecond):
	}
	close(releaseAudit)
	if err := <-shutdownDone; err == nil ||
		apperror.CodeOf(err) != apperror.CodeDeadlineExceeded {
		t.Fatalf("timed shutdown returned %v", err)
	}
	store.mu.Lock()
	attemptsAfterReturn := store.closeAuditAttempts
	store.mu.Unlock()

	service.mu.Lock()
	entry := service.latestByRun[store.run.ID]
	closeDone := entry.closeDone
	service.mu.Unlock()
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("detached close audit did not finish its lifecycle operation")
	}
	time.Sleep(2 * fullCDPAuditRetryInterval)
	store.mu.Lock()
	attemptsLater := store.closeAuditAttempts
	store.mu.Unlock()
	if attemptsLater != attemptsAfterReturn {
		t.Fatalf("close audit touched the detached store after shutdown: before=%d after=%d",
			attemptsAfterReturn, attemptsLater)
	}
	service.mu.Lock()
	view := entry.view
	service.mu.Unlock()
	if view.State != FullCDPSessionFailed || view.ProcessTreeQuiescent == false ||
		view.ProfileCleaned == false {
		t.Fatalf("detached audit terminal state is incomplete: %+v", view)
	}
}

func TestFullCDPProductionServiceShutdownWinsStartingSession(t *testing.T) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var runtime *fakeManagedFullCDPRuntime
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		close(entered)
		<-release
		runtime = newFakeManagedFullCDPRuntime(request)
		return runtime, nil
	}
	openDone := make(chan error, 1)
	go func() {
		_, err := service.OpenFullCDPSession(context.Background(),
			fullCDPOpenFixture(service, store, "open-full-cdp-shutdown"))
		openDone <- err
	}()
	<-entered
	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		closeDone <- service.Close(ctx)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.Lock()
		shuttingDown := service.closed
		service.mu.Unlock()
		if shuttingDown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not enter shutdown")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-openDone; err == nil || apperror.CodeOf(err) != apperror.CodeUnavailable {
		t.Fatalf("shutdown should reject in-flight open, got %v", err)
	}
	if err := <-closeDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown cleanup failed: %v", err)
	}
	if runtime == nil {
		t.Fatal("test runtime was not created")
	}
	runtime.mu.Lock()
	closeCount := runtime.closes
	runtime.mu.Unlock()
	store.mu.Lock()
	openedAudits, closedAudits := store.opened, store.closed
	store.mu.Unlock()
	if closeCount != 1 || openedAudits != 0 || closedAudits != 1 {
		t.Fatalf("shutdown lifecycle runtime=%d opened=%d closed=%d, want 1/0/1",
			closeCount, openedAudits, closedAudits)
	}
}

func TestFullCDPProductionServiceShutdownKeepsCapacityUntilCleanupRecovers(
	t *testing.T,
) {
	service, store, _, _ := newFullCDPProductionServiceFixture(t)
	var runtime *recoveringManagedFullCDPRuntime
	service.launch = func(_ context.Context,
		request browserruntime.FullCDPManagedLaunchRequest,
	) (managedFullCDPRuntime, error) {
		runtime = newRecoveringManagedFullCDPRuntime(request)
		return runtime, nil
	}
	opened, err := service.OpenFullCDPSession(t.Context(),
		fullCDPOpenFixture(service, store, "open-full-cdp-recovering-shutdown"))
	if err != nil {
		t.Fatal(err)
	}

	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Close(shutdownContext) }()
	select {
	case <-runtime.failedOnce:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not publish the first recoverable failure")
	}

	service.mu.Lock()
	active := service.active
	view := service.latestByRun[store.run.ID].view
	closeDone := service.latestByRun[store.run.ID].closeDone
	service.mu.Unlock()
	store.mu.Lock()
	closedAudits := store.closed
	store.mu.Unlock()
	if active != 1 || view.State != FullCDPSessionClosing || closedAudits != 0 {
		t.Fatalf("recoverable cleanup released ownership early: active=%d view=%+v audits=%d",
			active, view, closedAudits)
	}
	select {
	case <-closeDone:
		t.Fatal("recoverable cleanup closed its completion channel")
	default:
	}

	// The shutdown context bounds the caller and detaches future store writes.
	// It must not cancel the background resource owner or release the occupied
	// session quota before the cleanup proof becomes safe.
	cancelShutdown()
	if err := <-shutdownDone; err == nil ||
		apperror.CodeOf(err) != apperror.CodeCancelled {
		t.Fatalf("cancelled shutdown wait returned %v", err)
	}
	select {
	case <-runtime.secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("background cleanup did not retry")
	}
	close(runtime.allowRecovery)
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("background cleanup did not finish after recovery")
	}

	service.mu.Lock()
	active = service.active
	view = service.latestByRun[store.run.ID].view
	service.mu.Unlock()
	store.mu.Lock()
	closedAudits = store.closed
	store.mu.Unlock()
	runtime.mu.Lock()
	closeAttempts := runtime.closes
	runtime.mu.Unlock()
	if active != 0 || view.State != FullCDPSessionFailed ||
		view.SessionID != opened.Session.SessionID || closedAudits != 0 ||
		closeAttempts != 2 {
		t.Fatalf("detached cleanup recovery did not finalize exactly once: active=%d view=%+v audits=%d attempts=%d",
			active, view, closedAudits, closeAttempts)
	}
}
