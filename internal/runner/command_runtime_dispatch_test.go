package runner

import (
	"context"
	"errors"
	"testing"
)

func TestCommandRuntimeDispatchDeniedReceiptReplaysWithoutNewGrant(t *testing.T) {
	store := newCommandRuntimeMemoryStore()
	starter := &commandRuntimeFakeStarter{}
	manager, err := NewCommandRuntimeManager(store, starter, "dispatch-check-owner")
	if err != nil {
		t.Fatal(err)
	}
	request := commandRuntimeTestRequest(manager, 2000)
	denied := errors.New("authority revoked after Job preparation")
	checks := 0
	request.DispatchCheck = func(ctx context.Context, actual CommandRuntimeResolvedSpec) error {
		checks++
		_, jobID := CommandRuntimeOperationIdentity(request.Scope.RunID, request.Scope.OperationKey)
		if _, err := store.GetCommandRuntimeJob(ctx, jobID); err != nil {
			t.Fatal("guard ran before Job persistence", err)
		}
		return denied
	}
	job, replay, err := manager.Start(t.Context(), request)
	if !errors.Is(err, denied) || replay || checks != 1 || starter.starts != 0 || job.PID != 0 || job.State != CommandRuntimeJobFailed {
		t.Fatalf("denied launch: %+v replay=%v checks=%d starts=%d err=%v", job, replay, checks, starter.starts, err)
	}
	job, replay, err = manager.Start(t.Context(), request)
	if err != nil || !replay || checks != 1 || starter.starts != 0 || job.State != CommandRuntimeJobFailed {
		t.Fatalf("replay renewed dispatch: %+v replay=%v checks=%d err=%v", job, replay, checks, err)
	}
}

func TestCommandRuntimeDispatchRetainsCancellationAcrossDetachedLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	checks := 0
	detached := context.WithoutCancel(withCommandRuntimeDispatchCheck(ctx, func(context.Context, CommandRuntimeResolvedSpec) error { checks++; return nil }))
	cancel()
	if err := CheckCommandRuntimeDispatch(detached, CommandRuntimeResolvedSpec{}); !errors.Is(err, context.Canceled) || checks != 0 {
		t.Fatalf("detaching Job lifetime lost request cancellation: checks=%d err=%v", checks, err)
	}
}

func TestCommandRuntimeStdinGuardIsAfterReplayAndBeforeBytes(t *testing.T) {
	store := newCommandRuntimeMemoryStore()
	starter := &commandRuntimeFakeStarter{}
	manager, err := NewCommandRuntimeManager(store, starter, "stdin-guard-owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	job, _, err := manager.Start(t.Context(), commandRuntimeTestRequest(manager, 2000))
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("stdin authority revoked")
	deny := func(context.Context, string, []byte, bool) error { return denied }
	if _, n, replayed, err := manager.WriteStdinGuarded(t.Context(), job.ID, "denied-input", []byte("blocked"), false, deny); !errors.Is(err, denied) || n != 0 || replayed {
		t.Fatalf("stdin denial changed bytes: n=%d replay=%v err=%v", n, replayed, err)
	}
	process := starter.last()
	process.mu.Lock()
	input := process.input.String()
	process.mu.Unlock()
	if input != "" {
		t.Fatal("guard ran after input was written", input)
	}
	if _, n, _, err := manager.WriteStdinGuarded(t.Context(), job.ID, "approved-input", []byte("once"), false, func(context.Context, string, []byte, bool) error { return nil }); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if _, n, replayed, err := manager.WriteStdinGuarded(t.Context(), job.ID, "approved-input", []byte("once"), false, deny); err != nil || n != 4 || !replayed {
		t.Fatal(n, replayed, err)
	}
	process.mu.Lock()
	input = process.input.String()
	process.mu.Unlock()
	if input != "once" {
		t.Fatal("input replay dispatched twice", input)
	}
}
