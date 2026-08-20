package store_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/scheduler"
	"cyberagent-workbench/internal/store"
)

type storeScheduledClock struct{ now time.Time }

func (c *storeScheduledClock) Now() time.Time { return c.now }
func (c *storeScheduledClock) NewTimer(delay time.Duration) scheduler.Timer {
	return storeScheduledTimer{timer: time.NewTimer(delay)}
}

type storeScheduledTimer struct{ timer *time.Timer }

func (t storeScheduledTimer) C() <-chan time.Time            { return t.timer.C }
func (t storeScheduledTimer) Reset(delay time.Duration) bool { return t.timer.Reset(delay) }
func (t storeScheduledTimer) Stop() bool                     { return t.timer.Stop() }

func TestScheduledJobConcurrentCreateReplaysOneDurableIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scheduled-create-concurrency.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	run := createScheduledStoreRun(t, first)
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	request := scheduledStoreRequest(run.ID, anchor)
	clock := &storeScheduledClock{now: anchor}
	services := []*application.ScheduledJobService{
		application.NewScheduledJobService(first).WithClock(clock),
		application.NewScheduledJobService(second).WithClock(clock),
	}
	type createResult struct {
		value application.ScheduledJobControlResult
		err   error
	}
	start := make(chan struct{})
	results := make(chan createResult, len(services))
	var wait sync.WaitGroup
	for _, service := range services {
		wait.Add(1)
		go func(service *application.ScheduledJobService) {
			defer wait.Done()
			<-start
			value, err := service.Create(ctx, request)
			results <- createResult{value: value, err: err}
		}(service)
	}
	close(start)
	wait.Wait()
	close(results)
	jobID := ""
	replays := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.value.Replayed {
			replays++
		}
		if jobID == "" {
			jobID = result.value.Job.ID
		} else if result.value.Job.ID != jobID {
			t.Fatalf("concurrent create identities diverged: %s != %s",
				result.value.Job.ID, jobID)
		}
	}
	jobs, err := first.ListScheduledJobs(ctx, run.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != jobID || replays != 1 {
		t.Fatalf("jobs=%#v replays=%d err=%v", jobs, replays, err)
	}
}

func TestScheduledJobConcurrentClaimAndCrashFence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scheduled-concurrency.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	run := createScheduledStoreRun(t, first)
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &storeScheduledClock{now: anchor}
	service := application.NewScheduledJobService(first).WithClock(clock)
	created, err := service.Create(ctx, scheduledStoreRequest(run.ID, anchor))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	type claimResult struct {
		job      domain.ScheduledJob
		lease    domain.ScheduledJobLease
		acquired bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for index, current := range []*store.SQLiteStore{first, second} {
		wait.Add(1)
		go func(index int, current *store.SQLiteStore) {
			defer wait.Done()
			<-start
			job, lease, acquired, err := current.ClaimDueScheduledJob(ctx,
				"concurrent-owner-"+string(rune('a'+index)), anchor)
			results <- claimResult{job: job, lease: lease, acquired: acquired, err: err}
		}(index, current)
	}
	close(start)
	wait.Wait()
	close(results)
	var winner domain.ScheduledJobLease
	acquired := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent claim err=%v", result.err)
		}
		if result.acquired {
			acquired++
			winner = result.lease
		}
	}
	if acquired != 1 || winner.JobID != created.Job.ID {
		t.Fatalf("claim winners=%d lease=%#v", acquired, winner)
	}

	reclaimAt := winner.ExpiresAt.Add(time.Millisecond)
	if count, err := second.ReconcileScheduledJobs(ctx, reclaimAt, 10); err != nil || count != 1 {
		t.Fatalf("reconcile count=%d err=%v", count, err)
	}
	_, replacement, reacquired, err := second.ClaimDueScheduledJob(ctx,
		"recovery-owner", reclaimAt)
	if err != nil || !reacquired || replacement.Attempt != winner.Attempt+1 ||
		replacement.Generation <= winner.Generation ||
		replacement.OperationKey != winner.OperationKey {
		t.Fatalf("replacement=%#v reacquired=%t err=%v", replacement, reacquired, err)
	}
	sequence, err := first.LatestRunEventSequence(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	outcome := domain.ScheduledJobRoundOutcome{
		EventSequence:     sequence,
		ObservationSHA256: runmutation.Fingerprint("scheduled-store-observation.v1"),
		TargetStatus:      run.Status, TargetTerminal: false, Changed: false,
		Result: "No relevant target state changed",
	}
	if _, _, _, err := first.CompleteScheduledJobRound(ctx, winner, outcome,
		reclaimAt); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale fence code=%s err=%v", apperror.CodeOf(err), err)
	}
	completedAt := reclaimAt.Add(time.Second)
	job, round, _, err := second.CompleteScheduledJobRound(ctx, replacement,
		outcome, completedAt)
	if err != nil || round.Status != domain.ScheduledJobRoundUnchanged ||
		job.RoundsCompleted != 1 || job.ActiveLeaseGeneration != 0 {
		t.Fatalf("job=%#v round=%#v err=%v", job, round, err)
	}
	if _, _, _, err := second.CompleteScheduledJobRound(ctx, replacement,
		outcome, completedAt.Add(time.Millisecond)); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("duplicate completion code=%s err=%v", apperror.CodeOf(err), err)
	}
}

func createScheduledStoreRun(t *testing.T, state *store.SQLiteStore) domain.Run {
	t.Helper()
	ctx := context.Background()
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "scheduled claim fencing", Profile: "code",
			Surface: "code", Phase: "plan", Budget: domain.Budget{MaxTurns: 8},
			RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func scheduledStoreRequest(runID string, anchor time.Time) application.CreateScheduledJobRequest {
	return application.CreateScheduledJobRequest{
		Version: domain.ScheduledJobProtocolVersion, RunID: runID, TargetRunID: runID,
		Schedule: domain.ScheduledJobSchedule{Kind: domain.ScheduledJobPeriodic,
			Timezone: "UTC", AnchorAt: anchor, IntervalSeconds: 60,
			MisfirePolicy: domain.ScheduledJobMisfireRunOnce},
		DeadlineAt: anchor.Add(time.Hour), StopOnTargetTerminal: true,
		MaxRounds: 4, MaxModelCalls: 1, MaxElapsedSeconds: 3600,
		Retry: domain.ScheduledJobRetryPolicy{MaxAttempts: 3,
			InitialBackoffSeconds: 1, MaxBackoffSeconds: 10},
		Notification:  domain.ScheduledJobNotifyFailure,
		ExecutionMode: domain.ScheduledJobReadOnly,
		OperationKey:  "scheduled-store-create-operation-0001", RequestedBy: "operator",
	}
}
