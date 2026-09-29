package store

import (
	"cyberagent-workbench/internal/domain"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// Query regression: round numbers restart across attempts. A small relational
// fixture isolates ordering; the application journey tests use the real schema
// and writers to verify failure closure, compaction, restart and provenance.
func TestSupervisorFileEffectQueryKeepsNewestAttemptBeforeOlderRounds(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE run_supervisor_tool_calls(run_id TEXT,turn INTEGER,attempt_id TEXT,round INTEGER,position INTEGER,
		model_attempt INTEGER,call_id TEXT,stream_response_id TEXT,stream_item_id TEXT,stream_call_id TEXT,
		tool_name TEXT,payload_json TEXT,authority_json TEXT,status TEXT,result_json TEXT,error_code TEXT,created_at TEXT,completed_at TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(run string, turn int, attempt string, round int, tool, status, id string) {
		t.Helper()
		at := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = db.Exec(`INSERT INTO run_supervisor_tool_calls VALUES(?,?,?,?,1,1,?,'response','item','call',?,'{}','{}',?,'{}','',?,?)`, run, turn, attempt, round, id, tool, status, at, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 9; i++ {
		insert("run-effects", 1, "old-attempt", 4, "workspace_change", "completed", fmt.Sprintf("old-%d", i))
	}
	insert("run-effects", 1, "new-attempt", 1, "workspace_apply", "completed", "latest-apply")
	insert("other-run", 1, "foreign", 4, "workspace_apply", "completed", "foreign-apply")
	insert("run-effects", 2, "current", 4, "workspace_apply", "completed", "current-apply")
	insert("run-effects", 1, "pending", 4, "workspace_apply", "pending", "pending-apply")
	st := &SQLiteStore{db: db}
	calls, err := st.SupervisorFileEffectCalls(t.Context(), domain.SupervisorCheckpoint{RunID: "run-effects", NextTurn: 2, Phase: domain.SupervisorIdle, UpdatedAt: time.Now().UTC()})
	if err != nil || len(calls) != 7 || calls[0].CallID != "latest-apply" {
		t.Fatalf("old rounds displaced the new effect: %+v %v", calls, err)
	}
	for _, call := range calls {
		if call.RunID != "run-effects" || call.Turn != 1 || call.Status == domain.SupervisorToolPending {
			t.Fatal("query crossed scope or admitted unfinished effects", call)
		}
	}
}
