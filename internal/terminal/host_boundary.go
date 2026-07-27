package terminal

import (
	"context"
	"errors"
	"sync"
)

type HostBoundaryEvent string

const (
	HostBoundarySessionLocked       HostBoundaryEvent = "session_locked"
	HostBoundarySessionDisconnected HostBoundaryEvent = "session_disconnected"
	HostBoundarySystemSuspending    HostBoundaryEvent = "system_suspending"
	HostBoundarySystemResumed       HostBoundaryEvent = "system_resumed"
)

var ErrHostBoundaryMonitor = errors.New("host boundary monitor is unavailable")

type HostBoundarySource interface {
	Run(context.Context, func(HostBoundaryEvent), func(error)) error
}

// HostBoundaryMonitor converts native trust-boundary events into a complete
// process-local Agent terminal-input lease revocation. User-owned terminals
// remain open; only the temporary Agent input authority is removed.
type HostBoundaryMonitor struct {
	manager *Manager
	source  HostBoundarySource

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
	runErr  error
}

func NewHostBoundaryMonitor(manager *Manager,
	source HostBoundarySource,
) (*HostBoundaryMonitor, error) {
	if manager == nil || source == nil {
		return nil, ErrHostBoundaryMonitor
	}
	return &HostBoundaryMonitor{manager: manager, source: source}, nil
}

func NewPlatformHostBoundaryMonitor(manager *Manager) (*HostBoundaryMonitor, error) {
	return NewHostBoundaryMonitor(manager, newPlatformHostBoundarySource())
}

func (m *HostBoundaryMonitor) Start(parent context.Context) error {
	if m == nil || parent == nil || parent.Err() != nil {
		return ErrHostBoundaryMonitor
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return ErrHostBoundaryMonitor
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.done = make(chan struct{})
	m.started = true
	started := make(chan error, 1)
	go func(done chan struct{}) {
		defer close(done)
		var startOnce sync.Once
		signalStarted := func(err error) {
			startOnce.Do(func() {
				started <- err
			})
		}
		err := m.source.Run(ctx, func(event HostBoundaryEvent) {
			if validHostBoundaryEvent(event) {
				m.manager.RevokeForLockOrSleep()
			}
		}, signalStarted)
		signalStarted(err)
		if err != nil && !errors.Is(err, context.Canceled) {
			m.mu.Lock()
			m.runErr = err
			m.mu.Unlock()
		}
	}(m.done)
	if err := <-started; err != nil {
		cancel()
		done := m.done
		m.cancel = nil
		m.done = nil
		m.stopped = true
		m.mu.Unlock()
		<-done
		m.mu.Lock()
		return errors.Join(ErrHostBoundaryMonitor, err)
	}
	return nil
}

func (m *HostBoundaryMonitor) Stop() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.stopped {
		err := m.runErr
		m.mu.Unlock()
		return err
	}
	m.stopped = true
	cancel, done := m.cancel, m.done
	m.cancel = nil
	m.done = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	m.mu.Lock()
	err := m.runErr
	m.mu.Unlock()
	return err
}

func validHostBoundaryEvent(event HostBoundaryEvent) bool {
	switch event {
	case HostBoundarySessionLocked, HostBoundarySessionDisconnected,
		HostBoundarySystemSuspending, HostBoundarySystemResumed:
		return true
	default:
		return false
	}
}
