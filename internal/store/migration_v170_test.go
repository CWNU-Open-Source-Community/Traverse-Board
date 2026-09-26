package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestProviderReplaySchemaV170AddsPrivateLedgersWithoutChangingExistingRows(t *testing.T) {
	ctx := t.Context()
	st := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "v169.db"))
	defer st.Close()
	if err := applyMigrationPrefixForTest(ctx, st, migrationPlan(), 169); err != nil {
		t.Fatal(err)
	}
	f := providerReplayFixtureAtStore(t, st)
	f.start(t)
	f.attempt.Outcome = llm.OutcomeSuccess
	if _, err := st.RecordSupervisorModelCompleted(ctx, f.turn.Checkpoint, f.attempt, f.response); err != nil {
		t.Fatal(err)
	}
	callID := f.response.ToolCalls[0].ID
	if _, err := st.RecordSupervisorToolExecutionStarted(ctx, f.turn.Checkpoint, callID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RecordSupervisorToolResult(ctx, f.turn.Checkpoint, domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"ok":true}`, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	beforeRows := readV165SupervisorCallRows(t, ctx, st, f.turn.Run.ID)
	rows, err := st.db.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	beforeSchema := map[string]string{}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		beforeSchema[name] = ddl
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if err := st.applyMigration(ctx, migrationPlan()[169]); err != nil {
		t.Fatal(err)
	}
	if after := readV165SupervisorCallRows(t, ctx, st, f.turn.Run.ID); !reflect.DeepEqual(beforeRows, after) {
		t.Fatal("v170 changed old tool rows")
	}
	for name, before := range beforeSchema {
		var after string
		if err := st.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name=?`, name).Scan(&after); err != nil || before != after {
			t.Fatalf("v170 changed old schema object %s: %v", name, err)
		}
	}
	loaded, err := st.LoadSupervisorProviderReplay(ctx, f.turn.Checkpoint)
	if err != nil || len(loaded) != 0 {
		t.Fatalf("migration fabricated native replay: %d %v", len(loaded), err)
	}
	assertNoForeignKeyViolations(t, st.db)
	for _, table := range []string{"run_supervisor_provider_replay", "run_supervisor_context_recoveries"} {
		var count int
		if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("nonempty new ledger %s: %d %v", table, count, err)
		}
	}
}
