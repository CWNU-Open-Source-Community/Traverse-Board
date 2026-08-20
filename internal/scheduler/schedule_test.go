package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestNextOccurrenceUsesStableUTCIdentityAcrossDSTFold(t *testing.T) {
	anchor := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC)
	schedule := domain.ScheduledJobSchedule{
		Kind: domain.ScheduledJobPeriodic, Timezone: "America/New_York",
		AnchorAt: anchor, IntervalSeconds: 3600,
		MisfirePolicy: domain.ScheduledJobMisfireRunOnce,
	}
	first, err := NextOccurrence(schedule, anchor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NextOccurrence(schedule, first)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(anchor.Add(time.Hour)) || !second.Equal(anchor.Add(2*time.Hour)) ||
		!second.After(first) {
		t.Fatalf("anchor=%s first=%s second=%s", anchor, first, second)
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	if first.In(location).Hour() != second.In(location).Hour() {
		t.Fatalf("fixture did not cross a repeated local hour: first=%s second=%s",
			first.In(location), second.In(location))
	}
}

func TestMisfireAndBackoffAreBounded(t *testing.T) {
	anchor := time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC)
	schedule := domain.ScheduledJobSchedule{Kind: domain.ScheduledJobPeriodic,
		Timezone: "America/New_York", AnchorAt: anchor, IntervalSeconds: 60,
		MisfirePolicy: domain.ScheduledJobMisfireSkip}
	if MissedPeriodicOccurrence(schedule, anchor, anchor.Add(59*time.Second)) {
		t.Fatal("sub-interval delay was classified as a misfire")
	}
	if !MissedPeriodicOccurrence(schedule, anchor, anchor.Add(time.Minute)) {
		t.Fatal("complete missed interval was not classified as a misfire")
	}
	retry := domain.ScheduledJobRetryPolicy{MaxAttempts: 8,
		InitialBackoffSeconds: 2, MaxBackoffSeconds: 5}
	if got := RetryBackoff(retry, 5); got != 5*time.Second {
		t.Fatalf("retry backoff=%s", got)
	}
	if got := IdleBackoff(time.Minute, 20); got != 15*time.Minute {
		t.Fatalf("idle backoff=%s", got)
	}
}

type schedulerFakeClock struct{ now time.Time }

func (c *schedulerFakeClock) Now() time.Time { return c.now }
func (c *schedulerFakeClock) NewTimer(delay time.Duration) Timer {
	return schedulerFakeTimer{timer: time.NewTimer(delay)}
}

type schedulerFakeTimer struct{ timer *time.Timer }

func (t schedulerFakeTimer) C() <-chan time.Time            { return t.timer.C }
func (t schedulerFakeTimer) Reset(delay time.Duration) bool { return t.timer.Reset(delay) }
func (t schedulerFakeTimer) Stop() bool                     { return t.timer.Stop() }

type schedulerRunnerStub struct {
	mu    sync.Mutex
	times []time.Time
}

func (r *schedulerRunnerStub) RunDue(_ context.Context, _ string,
	now time.Time,
) (bool, error) {
	r.mu.Lock()
	r.times = append(r.times, now)
	r.mu.Unlock()
	return true, nil
}

func TestWorkerRunOnceUsesInjectedClockAndSerialHealth(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	runner := &schedulerRunnerStub{}
	worker, err := NewWorker(runner, WorkerConfig{OwnerID: "scheduled-worker-test",
		PollInterval: time.Second, Clock: &schedulerFakeClock{now: now}})
	if err != nil {
		t.Fatal(err)
	}
	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.times) != 1 || !runner.times[0].Equal(now) {
		t.Fatalf("times=%v", runner.times)
	}
	health := worker.Health()
	if health.Active || health.Concurrency != 1 ||
		health.ProtocolVersion != WorkerHealthProtocolVersion {
		t.Fatalf("health=%#v", health)
	}
}
