package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func removeSchemaV140ForTestStatements() []string {
	return append(removeSchemaV150ForTestStatements(), []string{
		// Historical downgrade fixtures start from the current schema. Remove
		// every newer migration record before v140 so reopening can replay the
		// now-current tail without manufacturing a migration-history gap.
		`DROP TRIGGER trg_web_fetch_authorizations_delete_immutable`,
		`DROP TRIGGER trg_web_fetch_authorizations_identity_immutable`,
		`DROP INDEX idx_web_fetch_authorizations_thread_target`,
		`DROP TABLE web_fetch_authorizations`,
		`DELETE FROM schema_migrations WHERE version = 149`,
		`DELETE FROM schema_migrations WHERE version = 148`,
		`DELETE FROM schema_migrations WHERE version = 147`,
		`DELETE FROM schema_migrations WHERE version = 146`,
		`DROP TRIGGER trg_run_mode_snapshot_insert`,
		`DROP TRIGGER trg_run_network_authority_operation_delete_immutable`,
		`DROP TRIGGER trg_run_network_authority_operation_update_immutable`,
		`DROP TABLE run_network_authority_operations`,
		requireMigrationTrigger("trg_run_mode_snapshot_insert", runModeStatements),
		`DELETE FROM schema_migrations WHERE version = 145`,
		`DELETE FROM schema_migrations WHERE version = 144`,
		`DELETE FROM schema_migrations WHERE version = 143`,
		`DELETE FROM schema_migrations WHERE version = 142`,
		`DELETE FROM schema_migrations WHERE version = 141`,
		`DROP TRIGGER trg_thread_run_insert_session_lifecycle`,
		`DROP TRIGGER trg_thread_status_projects_bound_sessions`,
		`DROP TRIGGER trg_thread_bound_session_status_guard`,
		`DELETE FROM schema_migrations WHERE version = 140`,
	}...)
}

func TestSchemaV140RepairsOnlyCanonicalThreadSessionProjection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "thread-session-v139.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	_, historical, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "preserve terminal Thread history", Profile: "review",
		Budget: domain.Budget{MaxTurns: 3},
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	historical, err = runs.Cancel(ctx, historical.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, historical.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion,
			ThreadID: threadRecord.ID, Content: "continue in the current Session",
			OperationKey: "v140-repair-successor-message-0001",
			RequestedBy:  "test_operator"})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	current, err := runs.Start(ctx, continued.Run.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}

	_, archivedRun, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "repair every archived binding", Profile: "review",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	archivedThread, err := state.GetThreadByRun(ctx, archivedRun.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV140ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v139 with %q: %v", statement, err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 139 {
		state.Close()
		t.Fatalf("restored schema version=%d want=139 err=%v", version, err)
	}

	threadRecord, err = state.GetThread(ctx, threadRecord.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	archived, err := state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
		threadRecord.Version, "test_operator", time.Now().UTC())
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	restored, err := state.TransitionThread(ctx, archived.ID, domain.ThreadRestore,
		archived.Version, "test_operator", time.Now().UTC().Add(time.Millisecond))
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	historicalBefore, err := state.GetSession(ctx, historical.SessionID)
	if err != nil || historicalBefore.Status != session.StatusArchived {
		state.Close()
		t.Fatalf("historical Session before migration=%+v err=%v", historicalBefore, err)
	}
	old := historical.CreatedAt.Add(-time.Hour)
	if _, err := state.db.ExecContext(ctx, `UPDATE sessions SET status = 'archived',
		updated_at = ? WHERE id = ?`, ts(old), current.SessionID); err != nil {
		state.Close()
		t.Fatal(err)
	}

	archivedThread, err = state.GetThread(ctx, archivedThread.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	archivedThread, err = state.TransitionThread(ctx, archivedThread.ID,
		domain.ThreadArchive, archivedThread.Version, "test_operator",
		time.Now().UTC().Add(2*time.Millisecond))
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE sessions SET status = 'active',
		updated_at = ? WHERE id IN (SELECT session_id FROM thread_runs WHERE thread_id = ?)`,
		ts(old), archivedThread.ID); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	repairedCurrent, err := upgraded.GetSession(ctx, current.SessionID)
	if err != nil || repairedCurrent.Status != session.StatusActive ||
		repairedCurrent.UpdatedAt.Before(restored.UpdatedAt) {
		t.Fatalf("current Session repair=%+v Thread=%+v err=%v",
			repairedCurrent, restored, err)
	}
	repairedHistorical, err := upgraded.GetSession(ctx, historical.SessionID)
	if err != nil || repairedHistorical.Status != session.StatusArchived ||
		!repairedHistorical.UpdatedAt.Equal(historicalBefore.UpdatedAt) {
		t.Fatalf("historical Session was changed: before=%+v after=%+v err=%v",
			historicalBefore, repairedHistorical, err)
	}
	repairedArchived, err := upgraded.GetSession(ctx, archivedRun.SessionID)
	if err != nil || repairedArchived.Status != session.StatusArchived ||
		repairedArchived.UpdatedAt.Before(archivedThread.UpdatedAt) {
		t.Fatalf("archived Session repair=%+v Thread=%+v err=%v",
			repairedArchived, archivedThread, err)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("upgraded version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func TestSchemaV140GuardsBoundSessionStatusAndAllowsCanonicalLifecycle(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "thread-session-guards.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runs := application.NewRunService(state)
	_, created, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "guard Thread-bound Session lifecycle", Profile: "review",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := runs.Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := state.GetSession(ctx, running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	linked.Status = session.StatusArchived
	linked.UpdatedAt = time.Now().UTC()
	if err := state.SaveSession(ctx, linked); err == nil ||
		!strings.Contains(err.Error(), "Thread-bound Session status") {
		t.Fatalf("direct active Session archive error=%v", err)
	}

	archived, err := state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
		threadRecord.Version, "test_operator", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	paused, err := state.GetRun(ctx, running.ID)
	if err != nil || paused.Status != domain.RunPaused {
		t.Fatalf("archived Run=%+v err=%v", paused, err)
	}
	linked, err = state.GetSession(ctx, running.SessionID)
	if err != nil || linked.Status != session.StatusArchived {
		t.Fatalf("archived Session=%+v err=%v", linked, err)
	}
	linked.Status = session.StatusActive
	linked.UpdatedAt = time.Now().UTC().Add(time.Millisecond)
	if err := state.SaveSession(ctx, linked); err == nil ||
		!strings.Contains(err.Error(), "Thread-bound Session status") {
		t.Fatalf("direct archived Session activation error=%v", err)
	}

	restored, err := state.TransitionThread(ctx, archived.ID, domain.ThreadRestore,
		archived.Version, "test_operator", time.Now().UTC().Add(2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	linked, err = state.GetSession(ctx, running.SessionID)
	if err != nil || linked.Status != session.StatusActive {
		t.Fatalf("restored current Session=%+v err=%v", linked, err)
	}
	if _, err := runs.Cancel(ctx, paused.ID); err != nil {
		t.Fatal(err)
	}
	linked.Status = session.StatusArchived
	linked.UpdatedAt = time.Now().UTC().Add(3 * time.Millisecond)
	if err := state.SaveSession(ctx, linked); err != nil {
		t.Fatalf("historical terminal Session archive: %v", err)
	}
	restored, err = state.GetThread(ctx, restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	archived, err = state.TransitionThread(ctx, restored.ID, domain.ThreadArchive,
		restored.Version, "test_operator", time.Now().UTC().Add(4*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO thread_runs
		(thread_id, run_id, session_id, ordinal, predecessor_run_id, created_at)
		VALUES (?, ?, ?, 2, ?, ?)`, archived.ID, running.ID, running.SessionID,
		running.ID, ts(time.Now().UTC())); err == nil ||
		!strings.Contains(err.Error(), "thread Run Session lifecycle") {
		t.Fatalf("archived Thread accepted a new Run binding: %v", err)
	}
	deleted, err := state.TransitionThread(ctx, archived.ID, domain.ThreadDelete,
		archived.Version, "test_operator", time.Now().UTC().Add(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	linked.Status = session.StatusActive
	linked.UpdatedAt = time.Now().UTC().Add(6 * time.Millisecond)
	if err := state.SaveSession(ctx, linked); err == nil ||
		!strings.Contains(err.Error(), "Thread-bound Session status") {
		t.Fatalf("deleted Thread Session activation error=%v Thread=%+v", err, deleted)
	}
	assertNoForeignKeyViolations(t, state.db)
}
