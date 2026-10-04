package runner

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ownershipDiagnosticBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *ownershipDiagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}
func (b *ownershipDiagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}
func TestFixedOwnershipDiagnosticsDistinguishesPhasesWithoutChangingKill(t *testing.T) {
	for _, phase := range []string{"authority", "owner_renew"} {
		t.Run(phase, func(t *testing.T) {
			state := newCommandRuntimeMemoryStore()
			starter := &commandRuntimeFakeStarter{}
			manager, err := NewCommandRuntimeManager(state, starter, "diagnostic-fixture")
			if err != nil {
				t.Fatal(err)
			}
			manager.ownerRenewEvery = 10 * time.Millisecond
			manager.ownerRenewTimeout = 100 * time.Millisecond
			output := &ownershipDiagnosticBuffer{}
			manager.ownershipDiagnostics = output
			var revoked atomic.Bool
			request := commandRuntimeTestRequest(manager, 2000)
			request.DispatchCheck = func(context.Context, CommandRuntimeResolvedSpec) error {
				if revoked.Load() {
					return apperror.New(apperror.CodePolicyDenied, "private-argv-path-and-secret")
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
			deadline := time.Now().Add(time.Second)
			for output.String() == "" && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			text := output.String()
			if !strings.Contains(text, "phase="+phase) || !strings.Contains(text, "context=none") || !strings.Contains(text, "shared_budget_ms=100") || strings.Contains(text, "private-") || strings.Contains(text, job.ID) {
				t.Fatalf("incorrect or sensitive diagnostic: %s", text)
			}
		})
	}
}
