package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCommandRuntimeCommittedInitialStdinHasJobLifetime(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		denial error
	}{
		{"request completion", nil},
		{"authority revoked", errors.New("runtime authority revoked")},
		{"lease lost", errors.New("execution lease lost")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := newCommandRuntimeMemoryStore()
			starter := &commandRuntimeFakeStarter{}
			manager, err := NewCommandRuntimeManager(store, starter, "initial-input-owner")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
			request := commandRuntimeTestRequest(manager, 5000)
			request.Spec.Spec.StdinPolicy = CommandRuntimeStdinPipe
			request.Spec.Spec.InitialStdin = "committed input\n"
			request.Spec.Spec.CloseInitialStdin = true
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var checks atomic.Int32
			request.DispatchCheck = func(ctx context.Context, _ CommandRuntimeResolvedSpec) error {
				if checks.Add(1) == 1 {
					return ctx.Err()
				}
				close(entered)
				<-release
				if scenario.denial != nil {
					return scenario.denial
				}
				return ctx.Err()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			job, replay, err := manager.Start(ctx, request)
			if err != nil || replay || job.State != CommandRuntimeJobRunning {
				t.Fatalf("start: %+v replay=%v err=%v", job, replay, err)
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("initial input guard did not start")
			}
			// Deterministically finish the originating HTTP/request lifetime only
			// AFTER Start returned a durably owned running Job, with input pending.
			cancel()
			unblock()
			entry := manager.entry(job.ID)
			select {
			case <-entry.inputGate:
				entry.unlockInput()
			case <-time.After(2 * time.Second):
				t.Fatal("initial input guard did not complete")
			}
			process := starter.last()
			process.mu.Lock()
			input, closed := process.input.String(), process.stdinClosed
			process.mu.Unlock()
			if scenario.denial == nil {
				if input != request.Spec.Spec.InitialStdin || !closed || entry.snapshot().State != CommandRuntimeJobRunning {
					t.Fatalf("response completion interrupted committed background startup: input=%q closed=%v state=%s", input, closed, entry.snapshot().State)
				}
			} else {
				if input != "" || !closed {
					t.Fatalf("revoked authority wrote input: %q closed=%v", input, closed)
				}
				finished, _, err := manager.Wait(t.Context(), job.ID, time.Second, 0, 4096)
				if err != nil || finished.State != CommandRuntimeJobFailed {
					t.Fatal("denied initial input did not stop owned Job", finished.State, err)
				}
			}
			if checks.Load() != 2 {
				t.Fatal("initial input bypassed fresh authority check", checks.Load())
			}
		})
	}
}
