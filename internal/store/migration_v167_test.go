package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/scheduler"
)

func createV167ScheduledRun(t *testing.T, state *SQLiteStore) domain.Run {
	t.Helper()
	ctx := context.Background()
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "v167 observation consent", Profile: "code",
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

func v167CreateRequest(runID, operationKey string, now time.Time) application.CreateScheduledJobRequest {
	return application.CreateScheduledJobRequest{
		Version: domain.ScheduledJobProtocolVersion, RunID: runID, TargetRunID: runID,
		Schedule: domain.ScheduledJobSchedule{Kind: domain.ScheduledJobPeriodic,
			Timezone: "UTC", AnchorAt: now.Add(time.Second), IntervalSeconds: 60,
			MisfirePolicy: domain.ScheduledJobMisfireRunOnce},
		DeadlineAt: now.Add(time.Hour), StopOnTargetTerminal: true, MaxRounds: 4,
		MaxModelCalls: 0, MaxElapsedSeconds: 3600,
		Retry: domain.ScheduledJobRetryPolicy{MaxAttempts: 2,
			InitialBackoffSeconds: 1, MaxBackoffSeconds: 2},
		Notification:  domain.ScheduledJobNotifyChange,
		ExecutionMode: domain.ScheduledJobReadOnly,
		OperationKey:  operationKey, RequestedBy: "operator",
	}
}

func TestSchemaV167DoesNotBackfillAndConsentIsImmutable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v167-upgrade.db")
	// Seed at exactly v167 before removing its empty consent table. Opening the
	// latest schema would leave later ledger entries and create a false gap.
	state := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 167); err != nil {
		t.Fatal(err)
	}
	run := createV167ScheduledRun(t, state)
	now := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	service := application.NewScheduledJobService(state).WithClock(&v167Clock{now: now})
	legacy, err := service.Create(ctx, v167CreateRequest(run.ID,
		"v167-legacy-create", now))
	if err != nil {
		t.Fatal(err)
	}
	var originalConsents int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_job_observation_consents`).Scan(&originalConsents); err != nil || originalConsents != 0 {
		t.Fatalf("legacy seed unexpectedly created consent: count=%d err=%v", originalConsents, err)
	}
	for _, statement := range []string{
		`DROP TRIGGER trg_scheduled_job_observation_consent_insert`,
		`DROP TRIGGER trg_scheduled_job_observation_consent_update_immutable`,
		`DROP TRIGGER trg_scheduled_job_observation_consent_delete_immutable`,
		`DROP INDEX idx_scheduled_job_observation_consents_run`,
		`DROP TABLE scheduled_job_observation_consents`,
		`DELETE FROM schema_migrations WHERE version=167`,
	} {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	applied, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationPlan(migrationPlan(), applied); err != nil {
		t.Fatal(err)
	}
	assertNoForeignKeyViolations(t, state.db)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	service = application.NewScheduledJobService(state).WithClock(&v167Clock{now: now})
	var count int
	if err := state.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scheduled_job_observation_consents`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("migration backfilled consent: count=%d err=%v", count, err)
	}
	stored, err := state.GetScheduledJob(ctx, legacy.Job.ID)
	if err != nil || stored.ObservationConsentVersion != 0 {
		t.Fatalf("legacy projection=%#v err=%v", stored, err)
	}
	enabled, err := service.EnableObservation(ctx,
		application.EnableScheduledJobObservationRequest{RunID: run.ID,
			JobID: legacy.Job.ID, ExpectedRevision: legacy.Job.Revision,
			ObservationConsentVersion: domain.ScheduledJobObservationConsentVersion,
			OperationKey:              "v167-enable-legacy", RequestedBy: "operator"})
	if err != nil || enabled.Job.ObservationConsentVersion != 1 {
		t.Fatalf("enabled=%#v err=%v", enabled, err)
	}
	if _, err := state.db.ExecContext(ctx,
		`UPDATE scheduled_job_observation_consents SET confirmed_by='rewritten' WHERE job_id=?`,
		legacy.Job.ID); err == nil {
		t.Fatal("observation consent was mutable")
	}
	if _, err := state.db.ExecContext(ctx,
		`DELETE FROM scheduled_job_observation_consents WHERE job_id=?`, legacy.Job.ID); err == nil {
		t.Fatal("observation consent was deletable")
	}
}

type v167Clock struct{ now time.Time }

func (c *v167Clock) Now() time.Time { return c.now }
func (c *v167Clock) NewTimer(delay time.Duration) scheduler.Timer {
	return v167Timer{timer: time.NewTimer(delay)}
}

type v167Timer struct{ timer *time.Timer }

func (t v167Timer) C() <-chan time.Time            { return t.timer.C }
func (t v167Timer) Reset(delay time.Duration) bool { return t.timer.Reset(delay) }
func (t v167Timer) Stop() bool                     { return t.timer.Stop() }

func TestScheduledObservationCreateFailureRollsBackWholeTransaction(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "v167-atomic-create.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	run := createV167ScheduledRun(t, state)
	now := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	clock := &v167Clock{now: now}
	service := application.NewScheduledJobService(state).WithClock(clock)
	request := v167CreateRequest(run.ID, "v167-atomic-create", now)
	request.ObservationConsentVersion = domain.ScheduledJobObservationConsentVersion
	if _, err := state.db.ExecContext(ctx, `CREATE TRIGGER force_v167_consent_failure
		BEFORE INSERT ON scheduled_job_observation_consents
		BEGIN SELECT RAISE(ABORT, 'forced consent failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, request); err == nil {
		t.Fatal("forced consent failure unexpectedly created a job")
	}
	jobs, err := state.ListScheduledJobs(ctx, run.ID, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("partial jobs=%#v err=%v", jobs, err)
	}
	keyDigest := runmutation.ScheduledJobOperationDigest(run.ID, request.OperationKey)
	if _, found, err := state.GetScheduledJobOperation(ctx, keyDigest); err != nil || found {
		t.Fatalf("partial operation found=%t err=%v", found, err)
	}
	if _, err := state.db.ExecContext(ctx, `DROP TRIGGER force_v167_consent_failure`); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, request)
	if err != nil || created.Replayed || created.Job.ObservationConsentVersion != 1 {
		t.Fatalf("retry after rollback=%#v err=%v", created, err)
	}
}
