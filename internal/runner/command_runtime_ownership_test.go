package runner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
)

func TestCommandRuntimeOwnershipFailureInterruptsProcess(t *testing.T) {
	for _, phase := range []string{"authority", "owner_renew"} {
		t.Run(phase, func(t *testing.T) {
			state := newCommandRuntimeMemoryStore()
			starter := &commandRuntimeFakeStarter{}
			manager, err := NewCommandRuntimeManager(state, starter, "ownership-failure-fixture")
			if err != nil {
				t.Fatal(err)
			}
			manager.ownerRenewEvery = 10 * time.Millisecond
			manager.ownerRenewTimeout = 100 * time.Millisecond
			var revoked atomic.Bool
			request := commandRuntimeTestRequest(manager, 2000)
			request.DispatchCheck = func(context.Context, CommandRuntimeResolvedSpec) error {
				if revoked.Load() {
					return apperror.New(apperror.CodePolicyDenied, "authorization revoked")
				}
				return nil
			}
			request.OwnershipCheck = func(ctx context.Context) error { return request.DispatchCheck(ctx, request.Spec) }
			job, _, err := manager.Start(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "authority" {
				revoked.Store(true)
			} else {
				state.mu.Lock()
				state.failActive = true
				state.mu.Unlock()
			}
			terminal, _ := waitCommandRuntimeTerminal(t, manager, job.ID)
			if terminal.State != CommandRuntimeJobInterrupted || terminal.ExitCode == nil || *terminal.ExitCode != 125 {
				t.Fatalf("did not stop revoked process: %+v", terminal)
			}
		})
	}
}
