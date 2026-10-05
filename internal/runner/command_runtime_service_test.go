package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type serviceCancelProcess struct {
	*commandRuntimeFakeProcess
	cancels, kills atomic.Int32
}

func (p *serviceCancelProcess) Cancel(_ time.Duration) error { p.cancels.Add(1); return nil }
func (p *serviceCancelProcess) Kill() error {
	p.kills.Add(1)
	return p.commandRuntimeFakeProcess.Kill()
}

type serviceCancelStarter struct{ process *serviceCancelProcess }

func (*serviceCancelStarter) Name() string    { return "service-cancel-fixture" }
func (*serviceCancelStarter) Available() bool { return true }
func (s *serviceCancelStarter) Start(context.Context, CommandRuntimeScope, CommandRuntimeResolvedSpec) (commandRuntimeProcess, error) {
	return s.process, nil
}

func TestThreadApplicationServiceCancellationNeverEscalatesOrAdoptsColdOwner(t *testing.T) {
	st := newCommandRuntimeMemoryStore()
	process := &serviceCancelProcess{commandRuntimeFakeProcess: newCommandRuntimeFakeProcess()}
	starter := &serviceCancelStarter{process: process}
	manager, err := NewCommandRuntimeManager(st, starter, "service-owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	snapshot, _, err := manager.Start(t.Context(), commandRuntimeTestRequest(manager, 60_000))
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.GetCommandRuntimeJob(t.Context(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	wrong := job
	wrong.SessionID = "another-session"
	if _, _, err := manager.CancelOwnedCommandRuntimeServiceJob(t.Context(), wrong); err == nil {
		t.Fatal("wrong durable tuple can cancel")
	}
	cold, err := NewCommandRuntimeManager(st, starter, "cold-service-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cold.CancelOwnedCommandRuntimeServiceJob(t.Context(), job); err == nil {
		t.Fatal("cold owner adopted durable process")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, _, err := manager.CancelOwnedCommandRuntimeServiceJob(t.Context(), job)
			if err != nil || value.State != CommandRuntimeJobStopping {
				t.Errorf("concurrent cancel=%+v %v", value, err)
			}
		}()
	}
	wg.Wait()
	if process.cancels.Load() != 1 || process.kills.Load() != 0 {
		t.Fatalf("repeated cancel escalated: cancels=%d kills=%d", process.cancels.Load(), process.kills.Load())
	}
	process.finish(125)
	terminal, _ := waitCommandRuntimeTerminal(t, manager, job.ID)
	job, err = st.GetCommandRuntimeJob(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != CommandRuntimeJobCancelled || !terminal.TreeReaped {
		t.Fatalf("terminal cleanup=%+v", terminal)
	}
	if replay, replayed, err := cold.CancelOwnedCommandRuntimeServiceJob(t.Context(), job); err != nil || !replayed || replay.State != CommandRuntimeJobCancelled {
		t.Fatalf("cold terminal replay=%+v %t %v", replay, replayed, err)
	}
	unfinishedRecord := job
	unfinishedRecord.TreeReaped = false
	unfinishedOwner := &CommandRuntimeManager{
		ownerID: job.OwnerID, ownerGeneration: job.OwnerGeneration, adapter: job.Adapter,
		entries: map[string]*commandRuntimeEntry{job.ID: {record: unfinishedRecord, process: process}},
	}
	if snapshot, replayed, err := unfinishedOwner.CancelOwnedCommandRuntimeServiceJob(t.Context(), job); !errors.Is(err, ErrCommandRuntimeUncertain) || replayed || snapshot.TreeReaped {
		t.Fatalf("terminal owner without reaping was replayed: %+v %t %v", snapshot, replayed, err)
	}
	if process.cancels.Load() != 1 || process.kills.Load() != 0 {
		t.Fatal("terminal replay signalled a process")
	}
}
