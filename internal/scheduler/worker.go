package scheduler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
)

const (
	WorkerProtocolVersion       = "scheduled-job-worker.v1"
	WorkerHealthProtocolVersion = "scheduled-job-worker-health.v1"
	WorkerConcurrency           = 1
	MinPollInterval             = 250 * time.Millisecond
	MaxPollInterval             = 60 * time.Second
	DefaultPollInterval         = 2 * time.Second
)

type DueRunner interface {
	RunDue(context.Context, string, time.Time) (bool, error)
}

type WorkerState string

const (
	WorkerReady    WorkerState = "ready"
	WorkerRunning  WorkerState = "running"
	WorkerDraining WorkerState = "draining"
	WorkerStopped  WorkerState = "stopped"
)

type WorkerHealth struct {
	ProtocolVersion    string
	State              WorkerState
	Active             bool
	PollIntervalMillis int64
	Concurrency        int
}

type WorkerConfig struct {
	PollInterval time.Duration
	OwnerID      string
	Clock        Clock
	OnError      func(error)
}

// Worker is process-local and serial. Cross-process exclusivity is enforced by
// the durable claim/fencing transaction; this mutex only prevents overlapping
// ticks in one process.
type Worker struct {
	runner       DueRunner
	clock        Clock
	pollInterval time.Duration
	ownerID      string
	onError      func(error)
	runMu        sync.Mutex
	healthMu     sync.RWMutex
	state        WorkerState
	active       bool
	started      bool
}

func NewWorker(runner DueRunner, config WorkerConfig) (*Worker, error) {
	if runner == nil {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"scheduled job worker runner is required")
	}
	interval := config.PollInterval
	if interval == 0 {
		interval = DefaultPollInterval
	}
	if interval < MinPollInterval || interval > MaxPollInterval {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"scheduled job worker poll interval is outside its hard bounds")
	}
	clock := config.Clock
	if clock == nil {
		clock = RealClock{}
	}
	ownerID := config.OwnerID
	if ownerID == "" {
		ownerID = idgen.New("scheduled-worker")
	}
	if ownerID != strings.TrimSpace(ownerID) || !domain.ValidAgentID(ownerID) ||
		strings.ContainsRune(ownerID, 0) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"scheduled job worker owner identity is invalid")
	}
	return &Worker{runner: runner, clock: clock, pollInterval: interval,
		ownerID: ownerID, onError: config.OnError, state: WorkerReady}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.runner == nil || w.clock == nil || ctx == nil {
		return apperror.New(apperror.CodeFailedPrecondition,
			"scheduled job worker is unavailable")
	}
	w.healthMu.Lock()
	if w.started {
		w.healthMu.Unlock()
		return apperror.New(apperror.CodeFailedPrecondition,
			"scheduled job worker cannot be restarted")
	}
	w.started = true
	w.state = WorkerRunning
	w.healthMu.Unlock()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			w.healthMu.Lock()
			if w.state == WorkerRunning {
				w.state = WorkerDraining
			}
			w.healthMu.Unlock()
		case <-done:
		}
	}()
	defer func() {
		close(done)
		w.healthMu.Lock()
		w.active = false
		w.state = WorkerStopped
		w.healthMu.Unlock()
	}()
	timer := w.clock.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C():
			_, err := w.RunOnce(ctx)
			if err != nil && !errors.Is(err, context.Canceled) && w.onError != nil {
				w.onError(apperror.Normalize(err))
			}
			timer.Reset(w.pollInterval)
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.runner == nil || w.clock == nil || ctx == nil {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"scheduled job worker is unavailable")
	}
	w.runMu.Lock()
	defer w.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	w.healthMu.Lock()
	w.active = true
	w.healthMu.Unlock()
	defer func() {
		w.healthMu.Lock()
		w.active = false
		w.healthMu.Unlock()
	}()
	return w.runner.RunDue(ctx, w.ownerID, w.clock.Now().UTC())
}

func (w *Worker) Health() WorkerHealth {
	if w == nil {
		return WorkerHealth{ProtocolVersion: WorkerHealthProtocolVersion,
			State: WorkerStopped, Concurrency: WorkerConcurrency}
	}
	w.healthMu.RLock()
	defer w.healthMu.RUnlock()
	return WorkerHealth{ProtocolVersion: WorkerHealthProtocolVersion,
		State: w.state, Active: w.active,
		PollIntervalMillis: w.pollInterval.Milliseconds(), Concurrency: WorkerConcurrency}
}
