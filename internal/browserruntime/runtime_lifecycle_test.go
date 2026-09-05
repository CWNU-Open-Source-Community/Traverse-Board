package browserruntime

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingBrowserRuntimeLifecycleSink struct {
	mu                   sync.Mutex
	checkpointCalls      int
	failCheckpointAtCall int
	checkpoints          []BrowserRuntimeCheckpoint
	receipts             []BrowserRuntimeReceipt
}

func (sink *recordingBrowserRuntimeLifecycleSink) RecordBrowserRuntimeCheckpoint(
	_ context.Context, checkpoint BrowserRuntimeCheckpoint,
) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.checkpointCalls++
	if sink.checkpointCalls == sink.failCheckpointAtCall {
		return errors.New("injected checkpoint failure")
	}
	sink.checkpoints = append(sink.checkpoints, checkpoint)
	return nil
}

func (sink *recordingBrowserRuntimeLifecycleSink) RecordBrowserRuntimeReceipt(
	_ context.Context, receipt BrowserRuntimeReceipt,
) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.receipts = append(sink.receipts, receipt)
	return nil
}

type failingRestrictedBrowserSessionCloser struct {
	called bool
	err    error
}

func (closer *failingRestrictedBrowserSessionCloser) Close(context.Context) error {
	closer.called = true
	return closer.err
}

type lifecycleTestNetworkGuard struct {
	mu       sync.Mutex
	closed   bool
	verified bool
}

func (*lifecycleTestNetworkGuard) Adapter() string {
	return WindowsWFPBrowserContainmentAdapterName
}

func (*lifecycleTestNetworkGuard) Fingerprint() string {
	return strings.Repeat("a", 64)
}

func (guard *lifecycleTestNetworkGuard) Close() error {
	guard.mu.Lock()
	guard.closed = true
	guard.mu.Unlock()
	return nil
}

func (guard *lifecycleTestNetworkGuard) CleanupVerified() bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.closed && guard.verified
}

func newLifecycleTestProcess(t *testing.T, facts browserRuntimeFacts,
	lease ProfileRuntimeLease, guard *lifecycleTestNetworkGuard,
) (*BrowserProcess, *fakeBrowserPlatformProcess) {
	t.Helper()
	spec, err := buildBrowserStartSpec(facts.authorization, facts.identity,
		facts.ownership, lease, facts.networkPlan, guard.Fingerprint(), facts.now)
	if err != nil {
		t.Fatal(err)
	}
	platform := &fakeBrowserPlatformProcess{
		pid: 4243, spec: spec, done: make(chan struct{}),
	}
	process := &BrowserProcess{
		spec: spec, platform: platform, guard: guard, containmentDone: make(chan struct{}),
	}
	go process.releaseContainmentWhenDone()
	return process, platform
}

func TestBrowserRuntimeLifecycleFinalizesInStrictCleanupOrder(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	guard := &lifecycleTestNetworkGuard{verified: true}
	process, _ := newLifecycleTestProcess(t, facts, lease, guard)
	sink := &recordingBrowserRuntimeLifecycleSink{}
	coordinator, err := newBrowserRuntimeLifecycleCoordinator(
		"browser-runtime-success", facts.attempt, facts.authorization,
		facts.ownership, lease, process, nil, sink, facts.now)
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := coordinator.Finalize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Succeeded || receipt.RecoveryRequired || !receipt.ProfileCleaned ||
		!receipt.NetworkCleanupVerified || !receipt.ProcessTreeQuiescent {
		t.Fatalf("unexpected successful receipt: %+v", receipt)
	}
	wantStages := []BrowserRuntimeLifecycleStage{
		BrowserRuntimeStageRunning,
		BrowserRuntimeStageCDPClosed,
		BrowserRuntimeStageProcessQuiescent,
		BrowserRuntimeStageNetworkReleased,
		BrowserRuntimeStageProfileReleased,
		BrowserRuntimeStageCompleted,
	}
	gotStages := make([]BrowserRuntimeLifecycleStage, 0, len(sink.checkpoints))
	for _, checkpoint := range sink.checkpoints {
		gotStages = append(gotStages, checkpoint.Stage)
	}
	if !reflect.DeepEqual(gotStages, wantStages) || len(sink.receipts) != 1 {
		t.Fatalf("unexpected lifecycle records: stages=%v receipts=%d", gotStages, len(sink.receipts))
	}
	if _, err := os.Lstat(facts.ownership.DirectoryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released Profile still exists: %v", err)
	}
}

func TestBrowserRuntimeLifecycleFinalizeCanOnlyBeClaimedOnceConcurrently(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	guard := &lifecycleTestNetworkGuard{verified: true}
	process, _ := newLifecycleTestProcess(t, facts, lease, guard)
	sink := &recordingBrowserRuntimeLifecycleSink{}
	coordinator, err := newBrowserRuntimeLifecycleCoordinator(
		"browser-runtime-concurrent-finalize", facts.attempt, facts.authorization,
		facts.ownership, lease, process, nil, sink, facts.now)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, finalizeErr := coordinator.Finalize(t.Context())
			results <- finalizeErr
		}()
	}
	close(start)

	var succeeded, rejected int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(err.Error(), "already finalized"):
			rejected++
		default:
			t.Fatalf("unexpected concurrent finalize result: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent finalize claims were not exclusive: succeeded=%d rejected=%d",
			succeeded, rejected)
	}
	sink.mu.Lock()
	receiptCount := len(sink.receipts)
	sink.mu.Unlock()
	if receiptCount != 1 {
		t.Fatalf("concurrent finalize wrote %d receipts, want 1", receiptCount)
	}
}

func TestBrowserRuntimeLifecycleContinuesCleanupAfterCDPCloseFailure(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	guard := &lifecycleTestNetworkGuard{verified: true}
	process, _ := newLifecycleTestProcess(t, facts, lease, guard)
	closer := &failingRestrictedBrowserSessionCloser{err: errors.New("injected CDP close failure")}
	sink := &recordingBrowserRuntimeLifecycleSink{}
	coordinator, err := newBrowserRuntimeLifecycleCoordinator(
		"browser-runtime-cdp-failure", facts.attempt, facts.authorization,
		facts.ownership, lease, process, closer, sink, facts.now)
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := coordinator.Finalize(t.Context())
	if err == nil || !strings.Contains(err.Error(), "restricted_cdp_close_failed") {
		t.Fatalf("expected CDP close failure, got %v", err)
	}
	if !closer.called || receipt.Succeeded || receipt.RecoveryRequired ||
		!receipt.ProcessTreeQuiescent || !receipt.NetworkCleanupVerified ||
		!receipt.ProfileCleaned || receipt.FailureCode != "restricted_cdp_close_failed" {
		t.Fatalf("cleanup did not complete after CDP failure: %+v", receipt)
	}
	if _, err := os.Lstat(facts.ownership.DirectoryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Profile was not cleaned after CDP failure: %v", err)
	}
	if len(sink.checkpoints) != 2 ||
		sink.checkpoints[len(sink.checkpoints)-1].Stage != BrowserRuntimeStageFailed ||
		len(sink.receipts) != 1 {
		t.Fatalf("failure lifecycle was not durably closed: checkpoints=%d receipts=%d",
			len(sink.checkpoints), len(sink.receipts))
	}
}

func TestBrowserRuntimeLifecycleContinuesCleanupAfterCheckpointFailure(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	guard := &lifecycleTestNetworkGuard{verified: true}
	process, _ := newLifecycleTestProcess(t, facts, lease, guard)
	sink := &recordingBrowserRuntimeLifecycleSink{failCheckpointAtCall: 1}
	coordinator, err := newBrowserRuntimeLifecycleCoordinator(
		"browser-runtime-persistence-failure", facts.attempt, facts.authorization,
		facts.ownership, lease, process, nil, sink, facts.now)
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := coordinator.Finalize(t.Context())
	if err == nil || !strings.Contains(err.Error(), "checkpoint_persistence_failed") {
		t.Fatalf("expected checkpoint persistence failure, got %v", err)
	}
	if receipt.Succeeded || receipt.RecoveryRequired || !receipt.ProfileCleaned ||
		receipt.FailureCode != "checkpoint_persistence_failed" {
		t.Fatalf("unexpected persistence failure receipt: %+v", receipt)
	}
	if len(sink.checkpoints) != 0 || len(sink.receipts) != 0 {
		t.Fatalf("broken ancestry must not be extended: checkpoints=%d receipts=%d",
			len(sink.checkpoints), len(sink.receipts))
	}
	if _, err := os.Lstat(facts.ownership.DirectoryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Profile was not cleaned after persistence failure: %v", err)
	}
}

func TestBrowserRuntimeLifecycleRetainsProfileWithoutNetworkCleanupProof(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	lease := facts.materialize(t)
	guard := &lifecycleTestNetworkGuard{verified: false}
	process, _ := newLifecycleTestProcess(t, facts, lease, guard)
	sink := &recordingBrowserRuntimeLifecycleSink{}
	coordinator, err := newBrowserRuntimeLifecycleCoordinator(
		"browser-runtime-network-failure", facts.attempt, facts.authorization,
		facts.ownership, lease, process, nil, sink, facts.now)
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := coordinator.Finalize(t.Context())
	if err == nil || !strings.Contains(err.Error(), "network_cleanup_unverified") {
		t.Fatalf("expected network cleanup failure, got %v", err)
	}
	if receipt.Succeeded || !receipt.RecoveryRequired || receipt.ProfileReleased ||
		receipt.ProfileCleaned || receipt.NetworkCleanupVerified {
		t.Fatalf("unverified network cleanup removed Profile: %+v", receipt)
	}
	if _, err := os.Lstat(facts.ownership.DirectoryPath); err != nil {
		t.Fatalf("recoverable Profile evidence was not retained: %v", err)
	}
	if len(sink.receipts) != 1 || sink.receipts[0].CompletedAt.Sub(sink.receipts[0].StartedAt) <= 0 {
		t.Fatalf("failed lifecycle receipt missing or invalid: %+v", sink.receipts)
	}
}

func TestBrowserProcessStopRetriesAfterTerminationFailure(t *testing.T) {
	spec := BrowserStartSpec{CreatedAt: time.Now().UTC()}
	platform := &stopErrorBrowserPlatformProcess{
		done: make(chan struct{}), err: errors.New("injected stop error"),
	}
	process := &BrowserProcess{spec: spec, platform: platform}
	first := process.Stop(t.Context())
	second := process.Stop(t.Context())
	if !errors.Is(first, platform.err) || !errors.Is(second, platform.err) || platform.calls != 2 {
		t.Fatalf("failed stop was not retried: first=%v second=%v calls=%d", first, second, platform.calls)
	}
}

type stopErrorBrowserPlatformProcess struct {
	done  chan struct{}
	err   error
	calls int
}

func (*stopErrorBrowserPlatformProcess) PID() int { return 991 }
func (process *stopErrorBrowserPlatformProcess) Done() <-chan struct{} {
	return process.done
}
func (*stopErrorBrowserPlatformProcess) Exit() (BrowserProcessExit, bool) {
	return BrowserProcessExit{}, false
}
func (process *stopErrorBrowserPlatformProcess) Stop(context.Context, bool) error {
	process.calls++
	return process.err
}
