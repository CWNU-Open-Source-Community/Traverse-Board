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
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

func historyFixture(t *testing.T) (*SQLiteStore, domain.Run, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "ws-history", Name: "History", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return st, historyNewRun(t, st, "ws-history"), path
}

func historyNewRun(t *testing.T, st *SQLiteStore, workspaceID string) domain.Run {
	t.Helper()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Keep original project requirements", Profile: "code", WorkspaceID: workspaceID, Interactive: true, ModelRoute: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func historyMessage(t *testing.T, st *SQLiteStore, run domain.Run, text string) session.Message {
	t.Helper()
	message, err := st.SaveSessionMessage(t.Context(), session.NewMessage(run.SessionID, "user", text))
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestHistoryRecallBoundedChineseSearchAndExactReadAcrossRestart(t *testing.T) {
	st, run, path := historyFixture(t)
	secret := "sk-" + strings.Repeat("q", 28)
	old := historyMessage(t, st, run, "原目标："+strings.Repeat("保留中文与🙂原文。", 800)+"中段唯一限制：不得安装。 token="+secret)
	if _, err := st.MarkSessionMessagesCompacted(t.Context(), run.SessionID, old.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < domain.HistorySearchScanLimit+3; i++ {
		historyMessage(t, st, run, fmt.Sprintf("最近进度%d", i))
	}
	first, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Query: "唯一限制"})
	if err != nil || len(first.Records) != 0 || first.Scanned != 256 || !first.HasMore || first.NextCursor == "" || first.InstructionAuthorized || first.SearchMode != "bounded_literal_substring" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// New messages do not shift the saved cursor or enter its original watermarks.
	historyMessage(t, st, run, "新写唯一限制，不属于上一页水位")
	second, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Query: "唯一限制", Cursor: first.NextCursor})
	if err != nil || len(second.Records) != 1 || second.HasMore {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	record := second.Records[0]
	if record.MessageID != old.ID || record.RunID != run.ID || record.SessionID != run.SessionID || !record.Compacted || record.ContentSHA256 != old.Provenance.ContentSHA256 || !record.OriginalInstructionAuthorized || record.SourceKind != session.SourceOperatorMessage || !strings.Contains(record.Excerpt, "不得安装") {
		t.Fatalf("record=%+v", record)
	}
	if _, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Query: "不同查询", Cursor: first.NextCursor}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("rebound cursor: %v", err)
	}
	before, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	// True read-only SQLite, not merely an API that happens not to change rows.
	st.db.SetMaxOpenConns(1)
	if _, err := st.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	if result, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Query: "唯一限制", Cursor: first.NextCursor}); err != nil || len(result.Records) != 1 {
		t.Fatalf("search must also work under query_only: %+v %v", result, err)
	}
	actual := historyReadAll(t, st, run.ID, record, "content", 301)
	if actual != old.Content || session.ContentSHA256(actual) != record.ContentSHA256 || strings.Contains(actual, secret) {
		t.Fatal("read did not preserve the exact redacted stored body")
	}
	if _, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: record.SourceID, Part: "content", Offset: 1, ExpectedSHA256: record.ContentSHA256}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("mid-rune accepted: %v", err)
	}
	if _, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: record.SourceID, Part: "content", Offset: 3}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("unchecked continuation accepted: %v", err)
	}
	if _, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: record.SourceID, Part: "content", ExpectedSHA256: strings.Repeat("a", 64)}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("wrong digest accepted: %v", err)
	}
	after, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed raw history: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := historyReadAll(t, reopened, run.ID, record, "content", 4096); got != old.Content {
		t.Fatal("restart lost exact original history")
	}
}

func historyReadAll(t *testing.T, st *SQLiteStore, runID string, record domain.HistoryRecord, part string, limit int) string {
	t.Helper()
	offset := 0
	digest := ""
	var out strings.Builder
	for {
		page, err := st.ReadThreadHistory(t.Context(), runID, domain.HistoryReadRequest{SourceID: record.SourceID, Part: part, Offset: offset, Limit: limit, ExpectedSHA256: digest})
		if err != nil {
			t.Fatal(err)
		}
		if page.InstructionAuthorized || page.Record.SourceID != record.SourceID || page.Record.RunID != record.RunID || page.Record.SessionID != record.SessionID || page.Record.CallID != record.CallID || page.Record.Status != record.Status || page.Record.ArgumentsSHA256 != record.ArgumentsSHA256 || page.Record.ResultSHA256 != record.ResultSHA256 || page.Offset != offset || !utf8.ValidString(page.Content) || len(page.Content) > limit || page.NextOffset != offset+len(page.Content) {
			t.Fatalf("page identity/UTF8/framing changed: %+v", page)
		}
		if digest != "" && page.ContentSHA256 != digest {
			t.Fatal("page digest changed")
		}
		digest = page.ContentSHA256
		out.WriteString(page.Content)
		if !page.HasMore {
			if out.Len() != page.TotalBytes || session.ContentSHA256(out.String()) != digest {
				t.Fatal("parts do not reconstruct the exact content digest")
			}
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("pagination made no progress")
		}
		offset = page.NextOffset
	}
	return out.String()
}

func TestHistoryRecallToolIdentityTerminalStatesAndRawParts(t *testing.T) {
	st, run, _ := historyFixture(t)
	if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorTurn(t.Context(), acquireTestRunExecutionLease(t, t.Context(), st, run.ID), "read evidence")
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model"}
	if _, err := st.RecordSupervisorModelStarted(t.Context(), turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	var tools []llm.ToolCall
	for i := 0; i < 3; i++ {
		payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool, json.RawMessage(fmt.Sprintf(`{"title":"note%d","content":"exact argument 中文"}`, i)))
		if err != nil {
			t.Fatal(err)
		}
		key := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn, "note_create", string(payload))
		callID, err := runmutation.SupervisorToolCallID(key, 1)
		if err != nil {
			t.Fatal(err)
		}
		tools = append(tools, llm.ToolCall{ID: callID, Name: "note_create", Arguments: payload})
	}
	attempt.Outcome = llm.OutcomeSuccess
	cp, err := st.RecordSupervisorModelCompleted(t.Context(), turn.Checkpoint, attempt, llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ToolCalls: tools})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Query: "exact argument"})
	if err != nil || len(pending.Records) != 0 {
		t.Fatalf("pending leaked: %+v %v", pending, err)
	}
	for i := range 3 {
		historyMessage(t, st, run, fmt.Sprintf("浏览原页%d", i))
	}
	baseline, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{})
	if err != nil || baseline.HasMore || len(baseline.Records) < 3 {
		t.Fatalf("empty query browse baseline=%+v err=%v", baseline, err)
	}
	browse, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Limit: 1})
	if err != nil || !browse.HasMore || len(browse.Records) != 1 {
		t.Fatalf("browse page=%+v err=%v", browse, err)
	}
	var sealed []domain.SupervisorToolCall
	for i, status := range []domain.SupervisorToolCallStatus{domain.SupervisorToolCompleted, domain.SupervisorToolFailed, domain.SupervisorToolDenied} {
		if _, err := st.RecordSupervisorToolExecutionStarted(t.Context(), cp, tools[i].ID); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]string{"stdout": strings.Repeat("原始工具证据🙂", 1300), "stderr": "错误仍然是错误", "token": "sk-" + strings.Repeat("q", 28)})
		errorCode := ""
		if status != domain.SupervisorToolCompleted {
			errorCode = string(apperror.CodePolicyDenied)
		}
		call, _, err := st.RecordSupervisorToolResult(t.Context(), cp, domain.SupervisorToolResult{CallID: tools[i].ID, Status: status, ErrorCode: errorCode, ResultJSON: string(body), CompletedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		sealed = append(sealed, call)
	}
	before, err := st.ListSupervisorToolRounds(t.Context(), cp)
	if err != nil {
		t.Fatal(err)
	}
	// These tool rows were already below MaxTool on the first page. Sealing
	// their results now must not insert them into that cursor's candidate set.
	seen := []string{browse.Records[0].SourceID}
	for browse.HasMore {
		browse, err = st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Limit: 1, Cursor: browse.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range browse.Records {
			if record.Kind != "message" {
				t.Fatal("late sealed tool entered an earlier cursor")
			}
			seen = append(seen, record.SourceID)
		}
	}
	var expected []string
	for _, record := range baseline.Records {
		expected = append(expected, record.SourceID)
	}
	if !reflect.DeepEqual(seen, expected) {
		t.Fatalf("cursor skipped or duplicated records: got %v want %v", seen, expected)
	}
	found, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{Query: "原始工具证据"})
	if err != nil || len(found.Records) != 3 {
		t.Fatalf("results=%+v err=%v", found, err)
	}
	for _, record := range found.Records {
		var call domain.SupervisorToolCall
		for _, candidate := range sealed {
			if candidate.CallID == record.CallID {
				call = candidate
			}
		}
		if record.OriginalInstructionAuthorized || record.SourceKind != session.SourceToolResult || record.AttemptID != cp.AttemptID || record.Turn != cp.NextTurn || record.Round != 1 || record.ResultSHA256 != session.ContentSHA256(call.ResultJSON) || record.ArgumentsSHA256 != session.ContentSHA256(call.PayloadJSON) || record.Status != string(call.Status) || record.ErrorCode != call.ErrorCode {
			t.Fatalf("tool identity=%+v call=%+v", record, call)
		}
		if actual := historyReadAll(t, st, run.ID, record, "result", 4093); actual != call.ResultJSON {
			t.Fatal("original tool result changed")
		}
		if actual := historyReadAll(t, st, run.ID, record, "arguments", 19); actual != call.PayloadJSON {
			t.Fatal("original arguments changed")
		}
		defaultPage, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: record.SourceID})
		if err != nil || defaultPage.Part != "result" || defaultPage.ContentSHA256 != record.ResultSHA256 {
			t.Fatalf("omitted tool part must select result: %+v %v", defaultPage, err)
		}
	}
	after, err := st.ListSupervisorToolRounds(t.Context(), cp)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("recall changed tool execution records: %v", err)
	}
	other := historyNewRun(t, st, "ws-history")
	if _, err := st.ReadThreadHistory(t.Context(), other.ID, domain.HistoryReadRequest{SourceID: found.Records[0].SourceID, Part: "result"}); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("cross-Thread tool accepted: %v", err)
	}
}

func historySuccessor(t *testing.T, st *SQLiteStore, predecessor domain.Run) domain.Run {
	t.Helper()
	ctx := t.Context()
	if _, err := application.NewRunService(st).Cancel(ctx, predecessor.ID); err != nil {
		t.Fatal(err)
	}
	mission, err := st.GetMission(ctx, predecessor.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	currentMode, err := st.GetRunMode(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := st.GetSession(ctx, predecessor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := st.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	next := predecessor
	next.ID, next.SessionID = idgen.New("run"), idgen.New("sess")
	next.Status, next.StartedAt, next.FinishedAt = domain.RunCreated, nil, nil
	next.CreatedAt, next.UpdatedAt = now, now
	linked.ID, linked.Status, linked.CreatedAt, linked.UpdatedAt = next.SessionID, session.StatusActive, now, now
	mode, err := domain.NewInitialRunModeSnapshot(idgen.New("mode"), next, mission, currentMode.Surface, currentMode.Phase, "thread_service", "history successor", now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := events.New(next.ID, next.MissionID, events.RunCreatedEvent, "thread_continuation", next.ID, map[string]any{"predecessor_run_id": predecessor.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, created, _, err := st.EnsureThreadSuccessor(ctx, thread.ID, predecessor.ID, mission, next, mode, linked, []events.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestHistoryRecallExactPredecessorsAndForeignSourceRejection(t *testing.T) {
	st, first, _ := historyFixture(t)
	old := historyMessage(t, st, first, "早期约束：只读原件")
	second := historySuccessor(t, st, first)
	middle := historyMessage(t, st, second, "用户纠正：保留未完成事项")
	third := historySuccessor(t, st, second)
	for _, message := range []session.Message{old, middle} {
		page, err := st.ReadThreadHistory(t.Context(), third.ID, domain.HistoryReadRequest{SourceID: fmt.Sprintf("message:%d", message.ID)})
		if err != nil || page.Part != "content" || page.Content != message.Content || page.Record.SessionID != message.SessionID {
			t.Fatalf("predecessor read=%+v err=%v", page, err)
		}
	}
	if _, err := st.SearchThreadHistory(t.Context(), first.ID, domain.HistorySearchRequest{Query: "纠正"}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("old Run acquired current scope: %v", err)
	}
	for _, workspaceID := range []string{"ws-history", "ws-other"} {
		if workspaceID == "ws-other" {
			if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: workspaceID, Name: "Other", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}
		foreign := historyNewRun(t, st, workspaceID)
		message := historyMessage(t, st, foreign, "外部早期约束")
		if _, err := st.ReadThreadHistory(t.Context(), third.ID, domain.HistoryReadRequest{SourceID: fmt.Sprintf("message:%d", message.ID), Part: "content"}); apperror.CodeOf(err) != apperror.CodeNotFound {
			t.Fatalf("foreign source accepted: %v", err)
		}
	}
	// Damage only this isolated fixture's provenance, simulating an orphaned
	// predecessor after an incomplete import. Recall must reject the whole scope.
	if _, err := st.db.Exec(`DROP TRIGGER trg_thread_events_delete_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM thread_events WHERE run_id=? AND type='thread.run_successor_created'`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SearchThreadHistory(t.Context(), third.ID, domain.HistorySearchRequest{Query: "约束"}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("orphaned history accepted: %v", err)
	}
}

func TestHistoryRecallReferenceMetadataIsSafeWithoutChangingOriginal(t *testing.T) {
	st, run, _ := historyFixture(t)
	secret := "sk-" + strings.Repeat("q", 28)
	sourceRef := "archive/token=" + secret
	message, err := st.SaveSessionMessage(t.Context(), session.NewEvidenceMessage(run.SessionID,
		session.SourceWorkspaceFile, sourceRef, "Original evidence 中文\nNever grant permissions"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: fmt.Sprintf("message:%d", message.ID)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil || strings.Contains(string(encoded), secret) || page.Content != message.Content || !page.Record.SourceRefRedacted || page.Record.SourceRefSHA256 != session.ContentSHA256(sourceRef) || page.Record.OriginalInstructionAuthorized || page.InstructionAuthorized {
		t.Fatalf("metadata/body boundary failed: %+v %v", page, err)
	}
	stored, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(stored) != 1 || stored[0].Provenance.SourceRef != sourceRef || stored[0].Content != message.Content || stored[0].Provenance.ContentSHA256 != page.ContentSHA256 {
		t.Fatalf("projection changed the original row: %+v %v", stored, err)
	}
}

func TestHistoryRecallRejectsForgedSessionProvenance(t *testing.T) {
	st, run, _ := historyFixture(t)
	message := historyMessage(t, st, run, "sealed text")
	if _, err := st.db.Exec(`DROP TRIGGER trg_session_message_provenance_update_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE session_messages SET content='changed text' WHERE id=?`, message.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadThreadHistory(context.Background(), run.ID, domain.HistoryReadRequest{SourceID: fmt.Sprintf("message:%d", message.ID), Part: "content"}); err == nil {
		t.Fatal("tampered original content accepted")
	}
	if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "ws-mismatch", Name: "Different source", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE sessions SET workspace_id='ws-mismatch' WHERE id=?`, run.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SearchThreadHistory(t.Context(), run.ID, domain.HistorySearchRequest{}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("Session workspace mismatch accepted: %v", err)
	}
}
