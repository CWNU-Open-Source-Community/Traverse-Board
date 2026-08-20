package application_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/scheduler"
	"cyberagent-workbench/internal/store"
)

type scheduledStaticClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *scheduledStaticClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *scheduledStaticClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now.UTC()
	c.mu.Unlock()
}

func (c *scheduledStaticClock) NewTimer(delay time.Duration) scheduler.Timer {
	return scheduledTestTimer{timer: time.NewTimer(delay)}
}

type scheduledTestTimer struct{ timer *time.Timer }

func (t scheduledTestTimer) C() <-chan time.Time            { return t.timer.C }
func (t scheduledTestTimer) Reset(delay time.Duration) bool { return t.timer.Reset(delay) }
func (t scheduledTestTimer) Stop() bool                     { return t.timer.Stop() }

type scheduledExecutorStub struct {
	mu        sync.Mutex
	requests  []application.ScheduledJobExecutionRequest
	result    application.ScheduledJobExecutionResult
	onExecute func(context.Context, application.ScheduledJobExecutionRequest) error
}

type scheduledPermissionDriftStore struct {
	*store.SQLiteStore
	reportDrift bool
}

func (s *scheduledPermissionDriftStore) GetRunExecutionPermission(ctx context.Context,
	runID string,
) (domain.RunExecutionPermissionSnapshot, error) {
	value, err := s.SQLiteStore.GetRunExecutionPermission(ctx, runID)
	if err == nil && s.reportDrift {
		value.Revision++
	}
	return value, err
}

func (s *scheduledExecutorStub) ExecuteScheduledJobRound(ctx context.Context,
	request application.ScheduledJobExecutionRequest,
) (application.ScheduledJobExecutionResult, error) {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	result := s.result
	onExecute := s.onExecute
	s.mu.Unlock()
	if onExecute != nil {
		if err := onExecute(ctx, request); err != nil {
			return result, err
		}
	}
	if result == (application.ScheduledJobExecutionResult{}) {
		result.ModelCalled = true
	}
	return result, nil
}

func (s *scheduledExecutorStub) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *scheduledExecutorStub) Last() application.ScheduledJobExecutionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[len(s.requests)-1]
}

func TestScheduledJobControlIsIdempotentAndExplicitlyScoped(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledJobApplicationFixture(t)
	defer state.Close()
	now := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: now}
	service := application.NewScheduledJobService(state).WithClock(clock)
	request := scheduledReadOnlyRequest(run.ID, now,
		"scheduled-create-operation-0001")
	created, err := service.Create(ctx, request)
	if err != nil || created.Replayed || created.Job.OwnerRunID != run.ID ||
		created.Job.Spec.TargetRunID != run.ID ||
		created.Job.Spec.ExecutionMode != domain.ScheduledJobReadOnly {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	replayed, err := service.Create(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Job.ID != created.Job.ID {
		t.Fatalf("replayed=%#v err=%v", replayed, err)
	}
	changed := request
	changed.MaxRounds++
	if _, err := service.Create(ctx, changed); err == nil {
		t.Fatal("same idempotency key accepted changed schedule")
	}
	foreign := request
	foreign.OperationKey = "scheduled-foreign-target-operation-0001"
	foreign.TargetRunID = "different-run"
	if _, err := service.Create(ctx, foreign); err == nil {
		t.Fatal("scheduled job accepted a non-owner target")
	}

	paused, err := service.Transition(ctx, application.TransitionScheduledJobRequest{
		Version: domain.ScheduledJobControlProtocolVersion, RunID: run.ID,
		JobID: created.Job.ID, Action: domain.ScheduledJobPause,
		ExpectedRevision: created.Job.Revision,
		OperationKey:     "scheduled-pause-operation-0001", RequestedBy: "operator",
	})
	if err != nil || paused.Job.Status != domain.ScheduledJobPaused ||
		paused.Job.NextWakeAt != nil {
		t.Fatalf("paused=%#v err=%v", paused, err)
	}
	resumed, err := service.Transition(ctx, application.TransitionScheduledJobRequest{
		Version: domain.ScheduledJobControlProtocolVersion, RunID: run.ID,
		JobID: created.Job.ID, Action: domain.ScheduledJobResume,
		ExpectedRevision: paused.Job.Revision,
		OperationKey:     "scheduled-resume-operation-0001", RequestedBy: "operator",
	})
	if err != nil || resumed.Job.Status != domain.ScheduledJobActive ||
		resumed.Job.NextWakeAt == nil {
		t.Fatalf("resumed=%#v err=%v", resumed, err)
	}
	cancelled, err := service.Transition(ctx, application.TransitionScheduledJobRequest{
		Version: domain.ScheduledJobControlProtocolVersion, RunID: run.ID,
		JobID: created.Job.ID, Action: domain.ScheduledJobCancel,
		ExpectedRevision: resumed.Job.Revision,
		OperationKey:     "scheduled-cancel-operation-0001", RequestedBy: "operator",
	})
	if err != nil || cancelled.Job.Status != domain.ScheduledJobCancelled ||
		cancelled.Job.StopReason != domain.ScheduledJobStopCancelled {
		t.Fatalf("cancelled=%#v err=%v", cancelled, err)
	}
	snapshot, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || snapshot.Job.Status != domain.ScheduledJobCancelled ||
		snapshot.Authorization != nil {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func TestScheduledReadOnlyPlanSupportsExplicitCyberTarget(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "scheduled-cyber-plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, createdRun, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "bounded cyber monitor", Profile: "learn",
			Surface: "cyber", Phase: "plan", Budget: domain.Budget{MaxTurns: 8},
			RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, createdRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	result, err := application.NewScheduledJobService(state).WithClock(clock).Create(ctx,
		scheduledReadOnlyRequest(run.ID, anchor,
			"scheduled-cyber-plan-operation-0001"))
	if err != nil || result.Job.OwnerRunID != run.ID ||
		result.Job.Spec.ExecutionMode != domain.ScheduledJobReadOnly {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestScheduledJobWorkerSkipsModelWhenOnlyItsOwnStateChanged(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledJobApplicationFixture(t)
	defer state.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	executor := &scheduledExecutorStub{}
	service := application.NewScheduledJobService(state).WithClock(clock).
		WithRoundExecutor(executor)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-worker-create-operation-0001")
	request.MaxRounds = 3
	request.MaxModelCalls = 2
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := service.RunDue(ctx, "scheduled-worker-a", anchor)
	if err != nil || !handled || executor.Count() != 1 {
		t.Fatalf("first handled=%t calls=%d err=%v", handled, executor.Count(), err)
	}
	first, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || first.Job.RoundsCompleted != 1 || first.Job.ModelCalls != 1 ||
		len(first.Rounds) != 1 || !first.Rounds[0].Changed ||
		!first.Rounds[0].ModelCalled || first.Rounds[0].ToolCalled {
		t.Fatalf("first snapshot=%#v err=%v", first, err)
	}
	secondAt := anchor.Add(time.Minute)
	clock.Set(secondAt)
	handled, err = service.RunDue(ctx, "scheduled-worker-a", secondAt)
	if err != nil || !handled || executor.Count() != 1 {
		t.Fatalf("second handled=%t calls=%d err=%v", handled, executor.Count(), err)
	}
	second, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || second.Job.RoundsCompleted != 2 || second.Job.ModelCalls != 1 ||
		len(second.Rounds) != 2 || second.Rounds[0].Status != domain.ScheduledJobRoundUnchanged ||
		second.Rounds[0].Changed || second.Rounds[0].ModelCalled ||
		second.Rounds[0].ToolCalled || len(second.Notifications) != 2 ||
		second.Notifications[0].Kind != "completed" {
		t.Fatalf("second snapshot=%#v err=%v", second, err)
	}
}

func TestScheduledJobRetainsEventsAppendedDuringExecutorForNextRound(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledJobApplicationFixture(t)
	defer state.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	runs := application.NewRunService(state)
	executor := &scheduledExecutorStub{}
	executor.onExecute = func(_ context.Context,
		_ application.ScheduledJobExecutionRequest,
	) error {
		if executor.Count() != 1 {
			return nil
		}
		if _, err := runs.Pause(ctx, run.ID); err != nil {
			return err
		}
		_, err := runs.Resume(ctx, run.ID)
		return err
	}
	service := application.NewScheduledJobService(state).WithClock(clock).
		WithRoundExecutor(executor)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-executor-watermark-operation-0001")
	request.MaxRounds = 3
	request.MaxModelCalls = 3
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := service.RunDue(ctx, "scheduled-watermark-worker", anchor); err != nil || !handled || executor.Count() != 1 {
		t.Fatalf("first handled=%t calls=%d err=%v", handled, executor.Count(), err)
	}
	first, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || first.Job.NextWakeAt == nil {
		t.Fatalf("first snapshot=%#v err=%v", first, err)
	}
	secondAt := *first.Job.NextWakeAt
	clock.Set(secondAt)
	if handled, err := service.RunDue(ctx, "scheduled-watermark-worker", secondAt); err != nil || !handled || executor.Count() != 2 {
		t.Fatalf("events appended during executor were skipped: handled=%t calls=%d err=%v",
			handled, executor.Count(), err)
	}
	second, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || second.Job.RoundsCompleted != 2 || len(second.Rounds) != 2 ||
		!second.Rounds[0].Changed || !second.Rounds[0].ModelCalled {
		t.Fatalf("second snapshot=%#v err=%v", second, err)
	}
}

func TestScheduledApprovedRepairFailsClosedWithoutExecutor(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledRepairApplicationFixture(t)
	defer state.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	service := application.NewScheduledJobService(state).WithClock(clock)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-repair-no-executor-operation-0001")
	request.ExecutionMode = domain.ScheduledJobApprovedRepair
	request.ConfirmRepair = true
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := service.RunDue(ctx, "scheduled-repair-worker-a", anchor)
	if !handled || apperror.CodeOf(err) != apperror.CodeUnavailable {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	snapshot, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || snapshot.Authorization == nil ||
		snapshot.Rounds[0].Status != domain.ScheduledJobRoundRetryWait ||
		snapshot.Rounds[0].ModelCalled || snapshot.Rounds[0].ToolCalled {
		t.Fatalf("repair failure did not stay fenced: %#v err=%v", snapshot, err)
	}
	retryAt := *snapshot.Job.NextWakeAt
	clock.Set(retryAt)
	handled, err = service.RunDue(ctx, "scheduled-repair-worker-a", retryAt)
	if !handled || apperror.CodeOf(err) != apperror.CodeUnavailable {
		t.Fatalf("retry handled=%t err=%v", handled, err)
	}
	snapshot, err = service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || snapshot.Job.Status != domain.ScheduledJobFailed ||
		snapshot.Job.StopReason != domain.ScheduledJobStopRetryExhausted ||
		snapshot.Job.RoundsCompleted != 1 || snapshot.Rounds[0].Attempt != 2 ||
		snapshot.Rounds[0].Status != domain.ScheduledJobRoundFailed ||
		snapshot.Rounds[0].ModelCalled || snapshot.Rounds[0].ToolCalled {
		t.Fatalf("repair retry did not exhaust closed: %#v err=%v", snapshot, err)
	}
}

func TestScheduledJobStopAndMisfireMatrix(t *testing.T) {
	t.Run("deadline before claim", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		base := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: base}
		service := application.NewScheduledJobService(state).WithClock(clock)
		request := scheduledReadOnlyRequest(run.ID, base.Add(time.Minute),
			"scheduled-deadline-stop-operation-0001")
		request.DeadlineAt = request.Schedule.AnchorAt.Add(time.Minute)
		request.MaxModelCalls = 0
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(request.DeadlineAt)
		handled, err := service.RunDue(t.Context(), "scheduled-deadline-worker",
			request.DeadlineAt)
		if err != nil || !handled {
			t.Fatalf("handled=%t err=%v", handled, err)
		}
		assertScheduledStop(t, service, created.Job.ID, domain.ScheduledJobExhausted,
			domain.ScheduledJobStopDeadline, 0)
	})

	t.Run("elapsed budget before claim", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		base := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: base}
		service := application.NewScheduledJobService(state).WithClock(clock)
		request := scheduledReadOnlyRequest(run.ID, base.Add(time.Minute),
			"scheduled-elapsed-stop-operation-0001")
		request.MaxElapsedSeconds = 30
		request.MaxModelCalls = 0
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(request.Schedule.AnchorAt)
		handled, err := service.RunDue(t.Context(), "scheduled-elapsed-worker",
			request.Schedule.AnchorAt)
		if err != nil || !handled {
			t.Fatalf("handled=%t err=%v", handled, err)
		}
		assertScheduledStop(t, service, created.Job.ID, domain.ScheduledJobExhausted,
			domain.ScheduledJobStopElapsedBudget, 0)
	})

	t.Run("round budget after authoritative completion", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: anchor}
		service := application.NewScheduledJobService(state).WithClock(clock)
		request := scheduledReadOnlyRequest(run.ID, anchor,
			"scheduled-round-stop-operation-0001")
		request.MaxRounds = 1
		request.MaxModelCalls = 0
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		handled, err := service.RunDue(t.Context(), "scheduled-round-worker", anchor)
		if err != nil || !handled {
			t.Fatalf("handled=%t err=%v", handled, err)
		}
		assertScheduledStop(t, service, created.Job.ID, domain.ScheduledJobExhausted,
			domain.ScheduledJobStopRoundBudget, 1)
	})

	t.Run("failed attempt charges and reports the final model budget", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: anchor}
		executor := &scheduledExecutorStub{
			result: application.ScheduledJobExecutionResult{ModelCalled: true},
			onExecute: func(context.Context,
				application.ScheduledJobExecutionRequest,
			) error {
				return apperror.New(apperror.CodeUnavailable, "synthetic model failure")
			},
		}
		service := application.NewScheduledJobService(state).WithClock(clock).
			WithRoundExecutor(executor)
		request := scheduledReadOnlyRequest(run.ID, anchor,
			"scheduled-model-budget-failure-operation-0001")
		request.MaxModelCalls = 1
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		handled, err := service.RunDue(t.Context(), "scheduled-model-budget-worker", anchor)
		if !handled || apperror.CodeOf(err) != apperror.CodeUnavailable {
			t.Fatalf("handled=%t err=%v", handled, err)
		}
		assertScheduledStop(t, service, created.Job.ID, domain.ScheduledJobExhausted,
			domain.ScheduledJobStopModelBudget, 1)
		snapshot, err := service.Get(t.Context(), created.Job.ID, 10, 10)
		if err != nil || snapshot.Job.ModelCalls != 1 || len(snapshot.Rounds) != 1 ||
			!snapshot.Rounds[0].ModelCalled || snapshot.Rounds[0].Status != domain.ScheduledJobRoundFailed {
			t.Fatalf("model budget failure snapshot=%#v err=%v", snapshot, err)
		}
	})

	t.Run("terminal target before claim", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: anchor}
		service := application.NewScheduledJobService(state).WithClock(clock)
		request := scheduledReadOnlyRequest(run.ID, anchor,
			"scheduled-target-stop-operation-0001")
		request.MaxModelCalls = 0
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := application.NewRunService(state).Cancel(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
		handled, err := service.RunDue(t.Context(), "scheduled-target-worker", anchor)
		if err != nil || !handled {
			t.Fatalf("handled=%t err=%v", handled, err)
		}
		assertScheduledStop(t, service, created.Job.ID, domain.ScheduledJobCompleted,
			domain.ScheduledJobStopTargetTerminal, 0)
	})

	t.Run("skip collapses long sleep without execution", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: anchor}
		executor := &scheduledExecutorStub{}
		service := application.NewScheduledJobService(state).WithClock(clock).
			WithRoundExecutor(executor)
		request := scheduledReadOnlyRequest(run.ID, anchor,
			"scheduled-misfire-skip-operation-0001")
		request.Schedule.MisfirePolicy = domain.ScheduledJobMisfireSkip
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		wake := anchor.Add(10 * time.Minute)
		clock.Set(wake)
		handled, err := service.RunDue(t.Context(), "scheduled-skip-worker", wake)
		if err != nil || !handled || executor.Count() != 0 {
			t.Fatalf("handled=%t calls=%d err=%v", handled, executor.Count(), err)
		}
		snapshot, err := service.Get(t.Context(), created.Job.ID, 10, 10)
		if err != nil || snapshot.Job.Status != domain.ScheduledJobActive ||
			snapshot.Job.RoundsCompleted != 1 || len(snapshot.Rounds) != 1 ||
			snapshot.Rounds[0].Status != domain.ScheduledJobRoundSkipped ||
			snapshot.Rounds[0].ModelCalled || snapshot.Rounds[0].ToolCalled ||
			snapshot.Job.NextWakeAt == nil || !snapshot.Job.NextWakeAt.After(wake) {
			t.Fatalf("skip snapshot=%#v err=%v", snapshot, err)
		}
	})

	t.Run("run once catches up one occurrence after long sleep", func(t *testing.T) {
		state, run := newScheduledJobApplicationFixture(t)
		defer state.Close()
		anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
		clock := &scheduledStaticClock{now: anchor}
		service := application.NewScheduledJobService(state).WithClock(clock)
		request := scheduledReadOnlyRequest(run.ID, anchor,
			"scheduled-misfire-run-once-operation-0001")
		request.MaxModelCalls = 0
		created, err := service.Create(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		wake := anchor.Add(10 * time.Minute)
		clock.Set(wake)
		handled, err := service.RunDue(t.Context(), "scheduled-catchup-worker", wake)
		if err != nil || !handled {
			t.Fatalf("handled=%t err=%v", handled, err)
		}
		snapshot, err := service.Get(t.Context(), created.Job.ID, 10, 10)
		if err != nil || snapshot.Job.Status != domain.ScheduledJobActive ||
			snapshot.Job.RoundsCompleted != 1 || len(snapshot.Rounds) != 1 ||
			!snapshot.Rounds[0].OccurrenceAt.Equal(anchor) ||
			snapshot.Job.NextWakeAt == nil || !snapshot.Job.NextWakeAt.After(wake) {
			t.Fatalf("catch-up snapshot=%#v err=%v", snapshot, err)
		}
		handled, err = service.RunDue(t.Context(), "scheduled-catchup-worker", wake)
		if err != nil || handled {
			t.Fatalf("same wake replayed an occurrence: handled=%t err=%v", handled, err)
		}
	})
}

func assertScheduledStop(t *testing.T, service *application.ScheduledJobService,
	jobID string, status domain.ScheduledJobStatus, reason domain.ScheduledJobStopReason,
	rounds int,
) {
	t.Helper()
	snapshot, err := service.Get(t.Context(), jobID, 10, 10)
	if err != nil || snapshot.Job.Status != status || snapshot.Job.StopReason != reason ||
		snapshot.Job.RoundsCompleted != rounds || len(snapshot.Rounds) != rounds {
		t.Fatalf("stop snapshot=%#v err=%v", snapshot, err)
	}
}

func TestScheduledApprovedRepairBindsExactAuthorizationToExecutor(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledRepairApplicationFixture(t)
	defer state.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	executor := &scheduledExecutorStub{result: application.ScheduledJobExecutionResult{
		ModelCalled: true, ToolCalled: true,
	}}
	service := application.NewScheduledJobService(state).WithClock(clock).
		WithRoundExecutor(executor)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-repair-executor-operation-0001")
	request.ExecutionMode = domain.ScheduledJobApprovedRepair
	request.ConfirmRepair = true
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := service.RunDue(ctx, "scheduled-repair-worker-b", anchor)
	if err != nil || !handled || executor.Count() != 1 {
		t.Fatalf("handled=%t calls=%d err=%v", handled, executor.Count(), err)
	}
	execution := executor.Last()
	if execution.Authorization == nil || execution.Authorization.JobID != created.Job.ID ||
		execution.Authorization.RunID != run.ID || execution.Authorization.ExecutionBypass ||
		execution.Authorization.NetworkBypass || execution.Authorization.ApprovalBypass {
		t.Fatalf("repair executor authorization=%#v", execution.Authorization)
	}
	snapshot, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || snapshot.Job.ModelCalls != 1 ||
		snapshot.Job.StopReason != domain.ScheduledJobStopModelBudget ||
		!snapshot.Rounds[0].ModelCalled || !snapshot.Rounds[0].ToolCalled {
		t.Fatalf("repair result=%#v err=%v", snapshot, err)
	}
}

func TestScheduledApprovedRepairStopsBeforeExecutorAfterPermissionDrift(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledRepairApplicationFixture(t)
	defer state.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	executor := &scheduledExecutorStub{result: application.ScheduledJobExecutionResult{
		ModelCalled: true, ToolCalled: true,
	}}
	service := application.NewScheduledJobService(state).WithClock(clock).
		WithRoundExecutor(executor)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-repair-drift-operation-0001")
	request.ExecutionMode = domain.ScheduledJobApprovedRepair
	request.ConfirmRepair = true
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	if _, err := runs.Pause(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{RunID: run.ID,
			Mode:         string(domain.RunExecutionPermissionConservative),
			OperationKey: "scheduled-repair-permission-revoke-0001",
			RequestedBy:  "operator", Reason: "revoke scheduled repair authority"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	handled, err := service.RunDue(ctx, "scheduled-repair-worker-c", anchor)
	if err != nil || !handled || executor.Count() != 0 {
		t.Fatalf("handled=%t executor=%d err=%v", handled, executor.Count(), err)
	}
	snapshot, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || snapshot.Job.Status != domain.ScheduledJobFailed ||
		snapshot.Job.StopReason != domain.ScheduledJobStopAuthorization ||
		len(snapshot.Rounds) != 0 {
		t.Fatalf("stale repair authorization did not stop closed: %#v err=%v", snapshot, err)
	}
}

func TestScheduledApprovedRepairRechecksPermissionAfterClaimBeforeExecutor(t *testing.T) {
	ctx := context.Background()
	state, run := newScheduledRepairApplicationFixture(t)
	defer state.Close()
	anchor := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &scheduledStaticClock{now: anchor}
	executor := &scheduledExecutorStub{result: application.ScheduledJobExecutionResult{
		ModelCalled: true, ToolCalled: true,
	}}
	driftStore := &scheduledPermissionDriftStore{SQLiteStore: state}
	service := application.NewScheduledJobService(driftStore).WithClock(clock).
		WithRoundExecutor(executor)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-repair-post-claim-drift-operation-0001")
	request.ExecutionMode = domain.ScheduledJobApprovedRepair
	request.ConfirmRepair = true
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	// The durable claim still observes the real current row. The Application
	// handoff then sees a changed revision and must stop before the executor.
	driftStore.reportDrift = true
	handled, err := service.RunDue(ctx, "scheduled-repair-worker-d", anchor)
	if !handled || apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		executor.Count() != 0 {
		t.Fatalf("handled=%t executor=%d err=%v", handled, executor.Count(), err)
	}
	snapshot, err := service.Get(ctx, created.Job.ID, 10, 10)
	if err != nil || len(snapshot.Rounds) != 1 ||
		snapshot.Rounds[0].Status != domain.ScheduledJobRoundRetryWait ||
		snapshot.Rounds[0].ModelCalled || snapshot.Rounds[0].ToolCalled {
		t.Fatalf("post-claim drift did not fail closed: %#v err=%v", snapshot, err)
	}
}

func TestScheduledJobRealClockShortOnceSmoke(t *testing.T) {
	state, run := newScheduledJobApplicationFixture(t)
	defer state.Close()
	service := application.NewScheduledJobService(state)
	anchor := time.Now().UTC().Add(300 * time.Millisecond).Truncate(time.Millisecond)
	request := scheduledReadOnlyRequest(run.ID, anchor,
		"scheduled-real-clock-smoke-operation-0001")
	request.Schedule.Kind = domain.ScheduledJobOnce
	request.Schedule.IntervalSeconds = 0
	request.DeadlineAt = anchor.Add(5 * time.Second)
	request.MaxRounds = 1
	request.MaxModelCalls = 0
	request.MaxElapsedSeconds = 5
	created, err := service.Create(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := scheduler.NewWorker(service, scheduler.WorkerConfig{
		PollInterval: scheduler.MinPollInterval, OwnerID: "scheduled-smoke-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, lookupErr := service.Get(t.Context(), created.Job.ID, 10, 10)
		if lookupErr != nil {
			cancel()
			t.Fatal(lookupErr)
		}
		if snapshot.Job.Status.Terminal() {
			cancel()
			if snapshot.Job.Status != domain.ScheduledJobCompleted ||
				snapshot.Job.StopReason != domain.ScheduledJobStopOnceCompleted ||
				snapshot.Job.RoundsCompleted != 1 || snapshot.Job.ModelCalls != 0 ||
				len(snapshot.Rounds) != 1 || snapshot.Rounds[0].ModelCalled ||
				snapshot.Rounds[0].ToolCalled {
				t.Fatalf("unexpected real-clock smoke result: %#v", snapshot)
			}
			break
		}
		select {
		case <-workerCtx.Done():
			t.Fatalf("scheduled smoke did not complete: %v", workerCtx.Err())
		case <-ticker.C:
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if health := worker.Health(); health.State != scheduler.WorkerStopped || health.Active {
		t.Fatalf("scheduled smoke worker did not stop: %#v", health)
	}
}

func newScheduledJobApplicationFixture(t *testing.T) (*store.SQLiteStore, domain.Run) {
	t.Helper()
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "scheduled-jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "bounded scheduled monitor", Profile: "code",
			Surface: "code", Phase: "plan", Budget: domain.Budget{MaxTurns: 8},
			RequestedBy: "operator"})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state, run
}

func newScheduledRepairApplicationFixture(t *testing.T) (*store.SQLiteStore, domain.Run) {
	t.Helper()
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "scheduled-repair.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "bounded approved repair", Profile: "code",
			Surface: "code", Phase: "deliver", Budget: domain.Budget{MaxTurns: 8},
			RequestedBy: "operator"})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{RunID: created.ID,
			Mode:         string(domain.RunExecutionPermissionApproval),
			OperationKey: "scheduled-repair-permission-operation-0001",
			RequestedBy:  "operator", Reason: "authorize one exact bounded repair schedule",
			ConfirmUserApproval: true}); err != nil {
		state.Close()
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state, run
}

func scheduledReadOnlyRequest(runID string, anchor time.Time,
	operationKey string,
) application.CreateScheduledJobRequest {
	return application.CreateScheduledJobRequest{
		Version: domain.ScheduledJobProtocolVersion, RunID: runID, TargetRunID: runID,
		Schedule: domain.ScheduledJobSchedule{
			Kind: domain.ScheduledJobPeriodic, Timezone: "UTC", AnchorAt: anchor,
			IntervalSeconds: 60, MisfirePolicy: domain.ScheduledJobMisfireRunOnce,
		},
		DeadlineAt: anchor.Add(time.Hour), StopOnTargetTerminal: true,
		MaxRounds: 4, MaxModelCalls: 1, MaxElapsedSeconds: 3600,
		Retry: domain.ScheduledJobRetryPolicy{MaxAttempts: 2,
			InitialBackoffSeconds: 1, MaxBackoffSeconds: 10},
		Notification:  domain.ScheduledJobNotifyAll,
		ExecutionMode: domain.ScheduledJobReadOnly,
		OperationKey:  operationKey, RequestedBy: "operator",
	}
}
