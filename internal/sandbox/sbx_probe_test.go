package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const sbxProbeGoodVersion = "sbx version v0.47.0\n"

type sbxProbeTransportFunc func(context.Context, SBXProcessRequest) (SBXProcessResult, error)

func (run sbxProbeTransportFunc) Run(ctx context.Context, request SBXProcessRequest) (SBXProcessResult, error) {
	if !slices.Equal(request.Arguments, []string{"--app-name", SBXAppName, "version"}) {
		return SBXProcessResult{ExitCode: -1}, ErrSBXBoundary
	}
	return run(ctx, request)
}

type sbxProbeVersionResult struct {
	output string
	err    error
}

// These fixtures have no journal, helper, namespace or daemon operations. The
// file supplies only bytes for the production version-cache identity check.
func sbxProbeBackend(t *testing.T, transport SBXProcessTransport) *SBXBackend {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "fake-sbx.exe")
	if err := os.WriteFile(executable, []byte("version identity fixture; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	backend, err := NewSBXBackend(SBXBackendConfig{ExecutablePath: executable}, WithSBXProcessTransport(transport))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func sbxProbeVersionAsync(backend *SBXBackend, ctx context.Context) <-chan sbxProbeVersionResult {
	result := make(chan sbxProbeVersionResult, 1)
	go func() {
		output, err := backend.compatibleCLI(ctx)
		result <- sbxProbeVersionResult{output, err}
	}()
	return result
}

// Done observation is a scheduling handshake, not a delay-based assertion.
// A waiter evaluating its gate select has reached the cancellation boundary.
type sbxProbeWaitContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (ctx *sbxProbeWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func sbxProbeAwait[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	// A generous hang guard is the only wall-clock dependency. Success and
	// exclusion assertions use completion channels and invocation counts.
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case result := <-channel:
		return result
	case <-timer.C:
		t.Fatal("probe test did not complete its scheduling handshake")
		var zero T
		return zero
	}
}

func TestSBXProbeVersionConcurrentChecksShareSuccessfulResult(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseOwner := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseOwner()
	backend := sbxProbeBackend(t, sbxProbeTransportFunc(func(ctx context.Context, _ SBXProcessRequest) (SBXProcessResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return SBXProcessResult{Stdout: []byte(sbxProbeGoodVersion)}, nil
		case <-ctx.Done():
			return SBXProcessResult{ExitCode: -1}, ctx.Err()
		}
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := sbxProbeVersionAsync(backend, ctx)
	sbxProbeAwait(t, entered)
	var waiters []<-chan sbxProbeVersionResult
	for range 12 {
		waiting := &sbxProbeWaitContext{Context: ctx, observed: make(chan struct{})}
		waiters = append(waiters, sbxProbeVersionAsync(backend, waiting))
		sbxProbeAwait(t, waiting.observed)
	}
	// The active fake cannot return until all callers have reached their gate.
	// A cache lacking single-flight protection would dispatch multiple fakes.
	releaseOwner()
	for _, result := range append([]<-chan sbxProbeVersionResult{first}, waiters...) {
		value := sbxProbeAwait(t, result)
		if value.err != nil || value.output != sbxProbeGoodVersion {
			t.Fatalf("concurrent compatibility proof failed: %+v", value)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("unchanged CLI was probed %d times for concurrent callers", calls.Load())
	}
}

func TestSBXProbeVersionCanceledWaiterDoesNotStartCLI(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	backend := sbxProbeBackend(t, sbxProbeTransportFunc(func(ctx context.Context, _ SBXProcessRequest) (SBXProcessResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return SBXProcessResult{Stdout: []byte(sbxProbeGoodVersion)}, nil
		case <-ctx.Done():
			return SBXProcessResult{ExitCode: -1}, ctx.Err()
		}
	}))
	ownerContext, cancelOwner := context.WithCancel(t.Context())
	defer cancelOwner()
	owner := sbxProbeVersionAsync(backend, ownerContext)
	sbxProbeAwait(t, entered)
	waitContext, cancelWait := context.WithCancel(t.Context())
	defer cancelWait()
	waiting := &sbxProbeWaitContext{Context: waitContext, observed: make(chan struct{})}
	waiter := sbxProbeVersionAsync(backend, waiting)
	sbxProbeAwait(t, waiting.observed)
	cancelWait()
	if value := sbxProbeAwait(t, waiter); !errors.Is(value.err, context.Canceled) || value.output != "" {
		t.Fatalf("waiting cancellation lost its result: %+v", value)
	}
	if calls.Load() != 1 {
		t.Fatal("canceled waiter started a second version process")
	}
	close(release)
	if value := sbxProbeAwait(t, owner); value.err != nil || value.output != sbxProbeGoodVersion {
		t.Fatalf("waiter cancellation affected active owner: %+v", value)
	}
	if output, err := backend.compatibleCLI(t.Context()); err != nil || output != sbxProbeGoodVersion || calls.Load() != 1 {
		t.Fatal("canceled waiter prevented reuse of the successful proof")
	}
}

func TestSBXProbeVersionFailuresDoNotCacheOrHoldGate(t *testing.T) {
	for _, failure := range []string{"process-error", "nonzero-exit", "unsupported", "canceled-owner"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			entered := make(chan struct{})
			backend := sbxProbeBackend(t, sbxProbeTransportFunc(func(ctx context.Context, _ SBXProcessRequest) (SBXProcessResult, error) {
				if calls.Add(1) != 1 {
					return SBXProcessResult{Stdout: []byte(sbxProbeGoodVersion)}, nil
				}
				switch failure {
				case "process-error":
					return SBXProcessResult{ExitCode: -1}, ErrSBXCLI
				case "nonzero-exit":
					return SBXProcessResult{ExitCode: 7, Stdout: []byte(sbxProbeGoodVersion)}, nil
				case "unsupported":
					return SBXProcessResult{Stdout: []byte("sbx version v0.48.0\n")}, nil
				default:
					close(entered)
					<-ctx.Done()
					return SBXProcessResult{ExitCode: -1}, ctx.Err()
				}
			}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := sbxProbeVersionAsync(backend, ctx)
			if failure == "canceled-owner" {
				sbxProbeAwait(t, entered)
				cancel()
			}
			value := sbxProbeAwait(t, first)
			if failure == "unsupported" {
				if value.err != nil || sbxCompatibleVersion(value.output) {
					t.Fatalf("unsupported version was accepted: %+v", value)
				}
			} else if value.err == nil {
				t.Fatal("failed owner produced a compatibility proof")
			}
			// A fresh caller must dispatch again, and only this successful retry
			// may satisfy later calls without another process.
			retry := sbxProbeAwait(t, sbxProbeVersionAsync(backend, t.Context()))
			if retry.err != nil || retry.output != sbxProbeGoodVersion || calls.Load() != 2 {
				t.Fatalf("failed probe cached output or retained its gate: %+v calls=%d", retry, calls.Load())
			}
			if output, err := backend.compatibleCLI(t.Context()); err != nil || output != sbxProbeGoodVersion || calls.Load() != 2 {
				t.Fatal("successful retry did not establish reusable compatibility")
			}
		})
	}
}

func sbxProbeInvalidCLIRequest(t *testing.T) SBXProcessRequest {
	t.Helper()
	return SBXProcessRequest{Executable: filepath.Join(t.TempDir(), "absent-sbx.exe"),
		Arguments: []string{"--app-name", SBXAppName, "version"}, OutputLimit: 4096}
}

func sbxProbeAssertShortGateFree(t *testing.T) {
	t.Helper()
	select {
	case sbxShortCLIGate <- struct{}{}:
		<-sbxShortCLIGate
	default:
		t.Fatal("short CLI operation retained the shared startup gate")
	}
}

func TestSBXProbeShortCLIGateExcludesTransportInstancesDuringCanceledWait(t *testing.T) {
	sbxProbeAssertShortGateFree(t)
	sbxShortCLIGate <- struct{}{} // stand in for another instance's active short command
	defer func() { <-sbxShortCLIGate }()
	var waiters []<-chan error
	var cancelWaiters []context.CancelFunc
	for range 2 {
		// Distinct transport instances must observe the same gate. An invalid
		// executable makes any premature dispatch return ErrSBXCLI, rather than
		// the cancellation expected from an admitted waiting request.
		transport := sbxProcessTransport{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		waiting := &sbxProbeWaitContext{Context: ctx, observed: make(chan struct{})}
		request := sbxProbeInvalidCLIRequest(t)
		result := make(chan error, 1)
		go func() {
			_, err := transport.Run(waiting, request)
			result <- err
		}()
		sbxProbeAwait(t, waiting.observed)
		select {
		case err := <-result:
			t.Fatalf("contending transport dispatched before cancellation: %v", err)
		default:
		}
		waiters = append(waiters, result)
		cancelWaiters = append(cancelWaiters, cancel)
	}
	for index, waiter := range waiters {
		cancelWaiters[index]()
		if err := sbxProbeAwait(t, waiter); !errors.Is(err, context.Canceled) {
			t.Fatalf("contending transport reached the invalid executable: %v", err)
		}
		if len(sbxShortCLIGate) != 1 {
			t.Fatal("canceled waiter released another instance's ownership")
		}
	}
}

func TestSBXProbeShortCLIGateDoesNotBlockLongSessions(t *testing.T) {
	sbxProbeAssertShortGateFree(t)
	sbxShortCLIGate <- struct{}{}
	defer func() { <-sbxShortCLIGate }()
	for _, verb := range []string{"create", "exec"} {
		request := sbxProbeInvalidCLIRequest(t)
		request.Arguments[2] = verb
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, err := (sbxProcessTransport{}).Run(ctx, request)
			result <- err
		}()
		if err := sbxProbeAwait(t, result); !errors.Is(err, ErrSBXCLI) {
			t.Fatalf("%s was held behind an unrelated short CLI: %v", verb, err)
		}
		if len(sbxShortCLIGate) != 1 {
			t.Fatalf("%s released another short CLI's gate", verb)
		}
	}
}

func TestSBXProbeShortCLIGateReleasedAfterProcessError(t *testing.T) {
	sbxProbeAssertShortGateFree(t)
	for range 2 {
		transport := sbxProcessTransport{}
		request := sbxProbeInvalidCLIRequest(t)
		result := make(chan error, 1)
		go func() {
			_, err := transport.Run(t.Context(), request)
			result <- err
		}()
		if err := sbxProbeAwait(t, result); !errors.Is(err, ErrSBXCLI) {
			t.Fatalf("expected invalid executable failure: %v", err)
		}
		sbxProbeAssertShortGateFree(t)
	}
}

// Cancel exactly after the gate admits a short command, before exec starts.
// This exercises the deferred release without launching any actual CLI.
type sbxProbeAdmissionCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	once     sync.Once
	admitted atomic.Bool
}

func (ctx *sbxProbeAdmissionCancelContext) Err() error {
	ctx.once.Do(func() {
		ctx.admitted.Store(len(sbxShortCLIGate) == 1)
		ctx.cancel()
	})
	return ctx.Context.Err()
}

func TestSBXProbeShortCLIGateReleasedAfterAdmissionCancellation(t *testing.T) {
	sbxProbeAssertShortGateFree(t)
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &sbxProbeAdmissionCancelContext{Context: parent, cancel: cancel}
	result, err := (sbxProcessTransport{}).Run(ctx, sbxProbeInvalidCLIRequest(t))
	if !ctx.admitted.Load() || !errors.Is(err, context.Canceled) || result.ExitCode != -1 {
		t.Fatalf("admission cancellation reached exec or lost its result: %+v %v", result, err)
	}
	sbxProbeAssertShortGateFree(t)
}
