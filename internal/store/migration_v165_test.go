package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type v164SupervisorCallRow struct {
	RowID            int64
	RunID            string
	Turn             int
	AttemptID        string
	Round            int
	Position         int
	ModelAttempt     int
	CallID           string
	ToolName         string
	PayloadJSON      string
	AuthorityJSON    string
	Status           string
	ResultJSON       string
	ErrorCode        string
	CreatedAt        string
	CompletedAt      string
	StreamResponseID string
	StreamItemID     string
	StreamCallID     string
}

func TestSchemaV165PreservesWebEvidenceAndAdmitsReplayableSourceSearch(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "source-search-v165.db"))
	defer state.Close()
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 164); err != nil {
		t.Fatal(err)
	}
	mission, run := createStructuredToolTestRun(t, ctx, state, "source search migration")
	now := time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)
	canonical := "https://github.com/OWWZO/ai-agent/issues/1"
	source, err := webevidence.SealSource(webevidence.Source{
		ID: webevidence.StableSourceID(run.ID, canonical), RunID: run.ID,
		MissionID: mission.ID, WorkspaceID: mission.WorkspaceID,
		CanonicalURL: canonical, Title: "Public issue", Provider: "source:github",
		State: webevidence.SourceDiscovered, DiscoveredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := webOperation(t, run.ID, "web_search", "legacy-search",
		map[string]string{"protocol_version": webevidence.SearchProtocolVersion}, now)
	if _, replayed, err := state.SaveWebSearch(ctx, []webevidence.Source{source}, legacy); err != nil || replayed {
		t.Fatalf("save legacy operation replayed=%t err=%v", replayed, err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, state, run.ID), "preserve v164 Supervisor calls")
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint,
		attempt); err != nil || !inserted {
		t.Fatalf("record v164 model start: inserted=%t err=%v", inserted, err)
	}
	placeholderPayloads := make([]json.RawMessage, 2)
	placeholderCalls := make([]llm.ToolCall, 2)
	for index := range placeholderPayloads {
		placeholderPayloads[index], err = toolgateway.NormalizeStructuredMemoryPayload(
			toolgateway.NoteCreateTool, json.RawMessage(fmt.Sprintf(
				`{"title":"v164-%d","content":"migration placeholder"}`, index+1)))
		if err != nil {
			t.Fatal(err)
		}
		operationKey := runmutation.SupervisorToolOperationKey(run.ID,
			turn.Checkpoint.NextTurn, string(toolgateway.NoteCreateTool),
			string(placeholderPayloads[index]))
		callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
		if err != nil {
			t.Fatal(err)
		}
		placeholderCalls[index] = llm.ToolCall{ID: callID,
			Name: string(toolgateway.NoteCreateTool), Arguments: placeholderPayloads[index]}
	}
	attempt.Outcome = llm.OutcomeSuccess
	if _, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage:     llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: placeholderCalls}); err != nil {
		t.Fatalf("record v164 placeholder tool round: %v", err)
	}
	legacyCalls := []v164SupervisorCallRow{
		{RowID: 17, RunID: run.ID, Turn: turn.Checkpoint.NextTurn,
			AttemptID: turn.Checkpoint.AttemptID, Round: 1, Position: 1, ModelAttempt: 1,
			CallID: placeholderCalls[0].ID, ToolName: "web_fetch",
			PayloadJSON:   `{"version":"web_fetch.v1","url":"https://docs.example.com/v164"}`,
			AuthorityJSON: `{"version":"web-evidence-tools.v1","fixture":"completed-v164"}`,
			Status:        "completed",
			ResultJSON:    `{"version":"supervisor_tool_result.v1","tool":"web_fetch","status":"completed","content":"preserved 完整结果"}`,
			CreatedAt:     "2026-09-21T01:00:05Z", CompletedAt: "2026-09-21T01:00:06Z",
			StreamResponseID: "", StreamItemID: "", StreamCallID: ""},
		{RowID: 43, RunID: run.ID, Turn: turn.Checkpoint.NextTurn,
			AttemptID: turn.Checkpoint.AttemptID, Round: 1, Position: 2, ModelAttempt: 1,
			CallID: placeholderCalls[1].ID, ToolName: "web_search",
			PayloadJSON:   `{"version":"web_search.v1","query":"preserve migration","limit":2}`,
			AuthorityJSON: `{"version":"web-evidence-tools.v1","fixture":"failed-v164"}`,
			Status:        "failed",
			ResultJSON:    `{"version":"supervisor_tool_result.v1","tool":"web_search","status":"failed","message":"preserved failure"}`,
			ErrorCode:     "UNAVAILABLE",
			CreatedAt:     "2026-09-21T01:00:07Z", CompletedAt: "2026-09-21T01:00:08Z",
			StreamResponseID: "", StreamItemID: "", StreamCallID: ""},
	}
	for _, call := range legacyCalls {
		if _, err := state.db.ExecContext(ctx, `UPDATE run_supervisor_tool_calls
			SET rowid=?, tool_name=?, payload_json=?, authority_json=?, status=?,
				result_json=?, error_code=?, created_at=?, completed_at=?
			WHERE run_id=? AND call_id=?`, call.RowID, call.ToolName, call.PayloadJSON,
			call.AuthorityJSON, call.Status, call.ResultJSON, call.ErrorCode,
			call.CreatedAt, call.CompletedAt, call.RunID, call.CallID); err != nil {
			t.Fatalf("prepare v164 Supervisor call rowid=%d: %v", call.RowID, err)
		}
	}
	preparedCalls := readV165SupervisorCallRows(t, ctx, state, run.ID)
	if len(preparedCalls) != len(legacyCalls) {
		t.Fatalf("prepared v164 Supervisor call count=%d want=%d",
			len(preparedCalls), len(legacyCalls))
	}
	for index := range legacyCalls {
		if preparedCalls[index].RowID != legacyCalls[index].RowID ||
			preparedCalls[index].AuthorityJSON != legacyCalls[index].AuthorityJSON ||
			preparedCalls[index].ResultJSON != legacyCalls[index].ResultJSON ||
			preparedCalls[index].PayloadJSON != legacyCalls[index].PayloadJSON ||
			preparedCalls[index].Status != legacyCalls[index].Status ||
			preparedCalls[index].ErrorCode != legacyCalls[index].ErrorCode ||
			preparedCalls[index].StreamResponseID == "" ||
			preparedCalls[index].StreamItemID == "" ||
			preparedCalls[index].StreamCallID == "" {
			t.Fatalf("v164 Supervisor call fixture lost a preserved field: %#v",
				preparedCalls[index])
		}
	}
	legacyCalls = preparedCalls
	sourceSearch := webOperation(t, run.ID, "source_search", "source-search",
		map[string]string{"protocol_version": webevidence.SourceSearchProtocolVersion}, now.Add(time.Second))
	if _, _, err := state.SaveSourceSearch(ctx, []webevidence.Source{source}, sourceSearch); err == nil {
		t.Fatal("v164 unexpectedly admitted a source_search operation")
	}

	if err := state.applyMigration(ctx, migrationPlan()[164]); err != nil {
		t.Fatal(err)
	}
	if stored, found, err := state.GetWebEvidenceOperation(ctx, run.ID, legacy.KeyDigest); err != nil || !found ||
		!reflect.DeepEqual(stored, legacy) {
		t.Fatalf("legacy operation=%#v found=%t want=%#v err=%v", stored, found, legacy, err)
	}
	preservedCalls := readV165SupervisorCallRows(t, ctx, state, run.ID)
	if !reflect.DeepEqual(preservedCalls, legacyCalls) {
		t.Fatalf("v164 Supervisor calls changed across v165 migration:\n got: %#v\nwant: %#v",
			preservedCalls, legacyCalls)
	}
	if _, replayed, err := state.SaveSourceSearch(ctx, []webevidence.Source{source}, sourceSearch); err != nil || replayed {
		t.Fatalf("save source search replayed=%t err=%v", replayed, err)
	}
	if stored, replayed, err := state.SaveSourceSearch(ctx, []webevidence.Source{source}, sourceSearch); err != nil || !replayed || stored.KeyDigest != sourceSearch.KeyDigest {
		t.Fatalf("replay source search=%#v replayed=%t err=%v", stored, replayed, err)
	}

	var supervisorSchema, operationSchema string
	if err := state.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type='table' AND name='run_supervisor_tool_calls'`).Scan(&supervisorSchema); err != nil {
		t.Fatal(err)
	}
	if strings.Count(supervisorSchema, "'source_search'") != 3 {
		t.Fatalf("Supervisor schema does not bind source_search in all authority constraints:\n%s", supervisorSchema)
	}
	if err := state.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type='table' AND name='web_evidence_operations'`).Scan(&operationSchema); err != nil {
		t.Fatal(err)
	}
	if strings.Count(operationSchema, "'source_search'") != 1 {
		t.Fatalf("operation schema does not admit source_search exactly once:\n%s", operationSchema)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE web_evidence_operations
		SET response_json='{}' WHERE key_digest=?`, legacy.KeyDigest); err == nil ||
		!strings.Contains(err.Error(), "web evidence operation is immutable") {
		t.Fatalf("v165 allowed update of a legacy web evidence operation: %v", err)
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM web_evidence_operations
		WHERE key_digest=?`, legacy.KeyDigest); err == nil ||
		!strings.Contains(err.Error(), "web evidence operation cannot be deleted") {
		t.Fatalf("v165 allowed deletion of a legacy web evidence operation: %v", err)
	}
	if stored, found, err := state.GetWebEvidenceOperation(ctx, run.ID, legacy.KeyDigest); err != nil ||
		!found || !reflect.DeepEqual(stored, legacy) {
		t.Fatalf("immutable legacy operation changed after rejected writes: %#v found=%t err=%v",
			stored, found, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func readV165SupervisorCallRows(t *testing.T, ctx context.Context, state *SQLiteStore,
	runID string,
) []v164SupervisorCallRow {
	t.Helper()
	rows, err := state.db.QueryContext(ctx, `SELECT rowid, run_id, turn, attempt_id,
		round, position, model_attempt, call_id, tool_name, payload_json, authority_json,
		status, result_json, error_code, created_at, completed_at, stream_response_id,
		stream_item_id, stream_call_id
		FROM run_supervisor_tool_calls WHERE run_id=? ORDER BY rowid`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]v164SupervisorCallRow, 0, 2)
	for rows.Next() {
		var call v164SupervisorCallRow
		if err := rows.Scan(&call.RowID, &call.RunID, &call.Turn, &call.AttemptID,
			&call.Round, &call.Position, &call.ModelAttempt, &call.CallID, &call.ToolName,
			&call.PayloadJSON, &call.AuthorityJSON, &call.Status, &call.ResultJSON,
			&call.ErrorCode, &call.CreatedAt, &call.CompletedAt, &call.StreamResponseID,
			&call.StreamItemID, &call.StreamCallID); err != nil {
			t.Fatal(err)
		}
		result = append(result, call)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
