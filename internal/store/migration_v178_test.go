package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

// Seed the genuine immutable v177 schema directly. The current application
// writer deliberately cannot manufacture old five-mode records for a fixture.
func TestSchemaV178PreservesFiveModeHistoryAndOnlyAcceptsNewSelections(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "legacy-approval-preference.db")
	st := openHistoricalTestDatabase(t, path, 177)
	now := time.Now().UTC().Truncate(time.Millisecond)
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`INSERT INTO sessions(id,workspace_id,title,route,status,created_at,updated_at) VALUES('session-history','','history','code','active',?,?)`,
		`INSERT INTO missions(id,goal,profile,workspace_id,scope_json,created_at,updated_at) VALUES('mission-history','history','code','','{"network_mode":"disabled"}',?,?)`,
		`INSERT INTO runs(id,mission_id,session_id,status,config_json,budget_json,created_at,updated_at) VALUES('run-history','mission-history','session-history','created','{"model_route":"code"}','{"max_turns":2}',?,?)`,
		`INSERT INTO threads(id,protocol_version,workspace_id,mission_id,title,status,active_run_id,last_run_id,version,created_at,updated_at) VALUES('thread-history','thread.v1','','mission-history','history','active',NULL,NULL,0,?,?)`,
	} {
		if _, err := tx.ExecContext(ctx, query, ts(now), ts(now)); err != nil {
			t.Fatal(query, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_runs(thread_id,run_id,session_id,ordinal,predecessor_run_id,created_at) VALUES('thread-history','run-history','session-history',1,NULL,?)`, ts(now)); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "run-history", MissionID: "mission-history", CreatedAt: now}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot("unused-initial", run, domain.Mission{ID: run.MissionID}, "historical-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	threadInitial, err := domain.NewInitialThreadExecutionPermissionSnapshot("unused-thread", domain.Thread{ID: "thread-history", MissionID: run.MissionID}, "historical-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	modes := []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionConservative, domain.RunExecutionPermissionWorkspaceAccess,
		domain.RunExecutionPermissionApproval, domain.RunExecutionPermissionFullAccess, domain.RunExecutionPermissionDebug}
	var history []domain.RunExecutionPermissionSnapshot
	var threadHistory []domain.ThreadExecutionPermissionSnapshot
	for i, mode := range modes {
		value, err := initial.Next(fmt.Sprintf("legacy-permission-%d", i), mode, i != 0, "historical-operator", "old five-mode record", now)
		if err != nil {
			t.Fatal(err)
		}
		value.Revision = int64(i + 1)
		if err := insertRunExecutionPermissionSnapshotTx(ctx, tx, value); err != nil {
			t.Fatal(mode, err)
		}
		history = append(history, value)
		threadValue, err := threadInitial.Next(fmt.Sprintf("legacy-thread-%d", i), mode, i != 0, "historical-operator", "old five-mode Thread", now)
		if err != nil {
			t.Fatal(err)
		}
		threadValue.Revision = int64(i + 1)
		if err := insertThreadExecutionPermissionSnapshotTx(ctx, tx, threadValue); err != nil {
			t.Fatal(mode, err)
		}
		threadHistory = append(threadHistory, threadValue)
	}
	oldOperation := domain.RunExecutionPermissionOperation{KeyDigest: strings.Repeat("a", 64), RequestFingerprint: runExecutionPermissionRequestFingerprint(history[4]),
		SnapshotID: history[4].ID, RunID: run.ID, RequestedBy: "historical-operator", CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_execution_permission_operations(operation_key_digest,request_fingerprint,snapshot_id,run_id,requested_by,created_at) VALUES(?,?,?,?,?,?)`,
		oldOperation.KeyDigest, oldOperation.RequestFingerprint, oldOperation.SnapshotID, oldOperation.RunID, oldOperation.RequestedBy, ts(now)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := st.db.QueryRow(`SELECT group_concat(version || ':' || checksum, ',') FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i, expected := range history {
		actual, err := st.GetRunExecutionPermissionSnapshot(ctx, expected.ID)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatal("Run history changed", actual, expected, err)
		}
		actualThread, err := st.GetThreadExecutionPermissionSnapshot(ctx, threadHistory[i].ID)
		if err != nil || !reflect.DeepEqual(actualThread, threadHistory[i]) {
			t.Fatal("Thread history changed", actualThread, err)
		}
	}
	op, found, err := st.GetRunExecutionPermissionOperation(ctx, oldOperation.KeyDigest)
	if err != nil || !found || !reflect.DeepEqual(op, oldOperation) {
		t.Fatal("operation history changed", op, err)
	}
	var after string
	if err := st.db.QueryRow(`SELECT group_concat(version || ':' || checksum, ',') FROM schema_migrations WHERE version<=177`).Scan(&after); err != nil || after != before {
		t.Fatal("old migration checksums changed", err)
	}
	for _, kind := range []string{"run", "thread"} {
		table := kind + "_execution_permission_snapshots"
		// Even perfectly valid legacy control tuples are read-only after upgrade.
		if _, err := st.db.Exec(`INSERT INTO ` + table + ` SELECT id||'-forbidden', ` + kind + `_id, mission_id, revision+5, protocol_version,mode,approval_policy,command_scope,filesystem_scope,network_scope,persistent_terminal,background_process,agent_terminal_input,risk_tier,required_gate,policy_version,operator_confirmed,process_enabled,execution_authorized,capability_grant,requested_by,reason,created_at FROM ` + table + ` WHERE revision=1`); err == nil {
			t.Fatal("legacy SQL writer still available", kind)
		}
	}
	tx, err = st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	upgraded, err := history[4].Next("new-auto-after-legacy", domain.RunExecutionPermissionAuto, false, "operator", "explicit new preference", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertRunExecutionPermissionSnapshotTx(ctx, tx, upgraded); err != nil {
		t.Fatal("one-way Run transition rejected", err)
	}
	upgradedThread, err := threadHistory[4].Next("new-thread-auto-after-legacy", domain.RunExecutionPermissionAuto, false, "operator", "explicit new preference", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertThreadExecutionPermissionSnapshotTx(ctx, tx, upgradedThread); err != nil {
		t.Fatal("one-way Thread transition rejected", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE run_execution_permission_snapshots SET mode='full' WHERE id='new-auto-after-legacy'`,
		`DELETE FROM thread_execution_permission_snapshots WHERE id='legacy-thread-0'`,
		`DELETE FROM run_execution_permission_operations WHERE operation_key_digest='` + strings.Repeat("a", 64) + `'`,
	} {
		if _, err := st.db.Exec(query); err == nil {
			t.Fatal("immutable ledger trigger lost", query)
		}
	}
	assertNoForeignKeyViolations(t, st.db)
	assertLatestMigrationLedger(t, st, migrationPlan())
}
