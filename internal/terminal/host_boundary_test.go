package terminal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type hostBoundarySourceStub struct {
	ready  chan struct{}
	events chan HostBoundaryEvent
}

func (s *hostBoundarySourceStub) Run(ctx context.Context,
	emit func(HostBoundaryEvent),
	ready func(error),
) error {
	ready(nil)
	close(s.ready)
	for {
		select {
		case event := <-s.events:
			emit(event)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type failingHostBoundarySourceStub struct{}

func (failingHostBoundarySourceStub) Run(context.Context,
	func(HostBoundaryEvent), func(error),
) error {
	return errors.New("native subscription failed")
}

type hostBoundaryRevokerStub struct {
	mu        sync.Mutex
	revokeAll int
}

func (*hostBoundaryRevokerStub) RevokeTerminal(string) int  { return 0 }
func (*hostBoundaryRevokerStub) RevokeRun(string) int       { return 0 }
func (*hostBoundaryRevokerStub) RevokeWorkspace(string) int { return 0 }
func (r *hostBoundaryRevokerStub) RevokeAll() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokeAll++
	return 1
}

func (r *hostBoundaryRevokerStub) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revokeAll
}

func TestHostBoundaryMonitorRevokesEveryNativeTrustLoss(t *testing.T) {
	revoker := &hostBoundaryRevokerStub{}
	manager, err := NewManager(&terminalBackendStub{}, revoker)
	if err != nil {
		t.Fatal(err)
	}
	source := &hostBoundarySourceStub{
		ready: make(chan struct{}), events: make(chan HostBoundaryEvent),
	}
	monitor, err := NewHostBoundaryMonitor(manager, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-source.ready
	events := []HostBoundaryEvent{
		HostBoundarySessionLocked,
		HostBoundarySessionDisconnected,
		HostBoundarySystemSuspending,
		HostBoundarySystemResumed,
	}
	for _, event := range events {
		source.events <- event
	}
	deadline := time.After(time.Second)
	for revoker.count() != len(events) {
		select {
		case <-deadline:
			t.Fatalf("native events revoked %d leases, want %d",
				revoker.count(), len(events))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := monitor.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Start(context.Background()); !errors.Is(err, ErrHostBoundaryMonitor) {
		t.Fatalf("stopped monitor restarted: %v", err)
	}
}

func TestHostBoundaryMonitorIgnoresUnknownEvents(t *testing.T) {
	revoker := &hostBoundaryRevokerStub{}
	manager, _ := NewManager(&terminalBackendStub{}, revoker)
	source := &hostBoundarySourceStub{
		ready: make(chan struct{}), events: make(chan HostBoundaryEvent),
	}
	monitor, _ := NewHostBoundaryMonitor(manager, source)
	if err := monitor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-source.ready
	source.events <- HostBoundaryEvent("unknown")
	if err := monitor.Stop(); err != nil {
		t.Fatal(err)
	}
	if revoker.count() != 0 {
		t.Fatal("unknown host event revoked terminal leases")
	}
}

func TestHostBoundaryMonitorFailsClosedBeforeStartup(t *testing.T) {
	manager, _ := NewManager(&terminalBackendStub{}, &hostBoundaryRevokerStub{})
	monitor, _ := NewHostBoundaryMonitor(manager, failingHostBoundarySourceStub{})
	if err := monitor.Start(context.Background()); !errors.Is(err,
		ErrHostBoundaryMonitor) {
		t.Fatalf("native subscription failure was not returned: %v", err)
	}
}
