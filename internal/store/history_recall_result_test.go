package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

func prepareHistoryResultCall(t *testing.T, st *SQLiteStore, run domain.Run, name string,
	request any,
) (domain.SupervisorCheckpoint, string) {
	t.Helper()
	ctx := t.Context()
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorTurn(ctx, acquireTestRunExecutionLease(t, ctx, st, run.ID), "recall exact prior evidence")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if name == "note_create" {
		payload, err = toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool, payload)
	} else {
		payload, err = toolgateway.NormalizeHistoryRecallPayload(toolgateway.ToolName(name), payload)
	}
	if err != nil {
		t.Fatal(err)
	}
	key := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn, name, string(payload))
	callID, err := runmutation.SupervisorToolCallID(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model"}
	if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt, llm.ChatResponse{
		Provider: "test", Model: "model", ToolCalls: []llm.ToolCall{{ID: callID, Name: name, Arguments: payload}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSupervisorToolExecutionStarted(ctx, checkpoint, callID); err != nil {
		t.Fatal(err)
	}
	return checkpoint, callID
}

func historyResultEnvelope(t *testing.T, name string, value any) string {
	t.Helper()
	stdout, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]any{
		"version": "supervisor_tool_result.v1", "tool": name, "status": "completed", "stdout": string(stdout),
		"metadata": map[string]string{"diagnostic": "password=separate-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestSupervisorHistoryResultPreservesExactPageAndSealedReplay(t *testing.T) {
	st, run, _ := historyFixture(t)
	message := historyMessage(t, st, run, "password=abcdefgh\nkeep exact 中文🙂 after newline")
	request := domain.HistoryReadRequest{SourceID: fmt.Sprintf("message:%d", message.ID), Part: "content"}
	checkpoint, callID := prepareHistoryResultCall(t, st, run, "history_read", request)
	page, err := st.ReadThreadHistory(t.Context(), run.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := json.Marshal(page)
	if redact.String(string(stdout)) == string(stdout) {
		t.Fatal("fixture does not reproduce nested JSON redaction corruption")
	}
	result := domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted,
		ResultJSON: historyResultEnvelope(t, "history_read", page), CompletedAt: time.Now().UTC()}
	stored, replayed, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, result)
	if err != nil || replayed {
		t.Fatalf("exact history page rejected: %v", err)
	}
	var envelope struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(stored.ResultJSON), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Stdout != string(stdout) || strings.Contains(stored.ResultJSON, "separate-secret") {
		t.Fatalf("page changed or unrelated metadata escaped redaction: %s", stored.ResultJSON)
	}
	var observed domain.HistoryReadResult
	if err := json.Unmarshal([]byte(envelope.Stdout), &observed); err != nil || observed.Content != message.Content ||
		observed.ContentSHA256 != session.ContentSHA256(observed.Content) {
		t.Fatalf("saved page/hash mismatch: %#v %v", observed, err)
	}
	if _, err := st.MarkSessionMessagesCompacted(t.Context(), run.SessionID, message.ID); err != nil {
		t.Fatal(err)
	}
	again, replayed, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, result)
	if err != nil || !replayed || again.ResultJSON != stored.ResultJSON {
		t.Fatalf("same sealed result was rewritten after compaction: %v", err)
	}
}

func TestSupervisorHistoryResultVerifiesSearchAtOriginalSnapshot(t *testing.T) {
	st, run, _ := historyFixture(t)
	historyMessage(t, st, run, "older password=abcdefgh\nkeep prior observation")
	historyMessage(t, st, run, "newer password=abcdefgh\nkeep latest observation")
	request := domain.HistorySearchRequest{Query: "password", Limit: 1}
	checkpoint, callID := prepareHistoryResultCall(t, st, run, "history_search", request)
	page, err := st.SearchThreadHistory(t.Context(), run.ID, request)
	if err != nil || !page.HasMore || page.SnapshotCursor == "" {
		t.Fatalf("fixture search not paged: %#v %v", page, err)
	}
	// Valid later activity must not change the page being committed.
	historyMessage(t, st, run, "later password=abcdefgh excludes original snapshot")
	stored, _, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, domain.SupervisorToolResult{
		CallID: callID, Status: domain.SupervisorToolCompleted, CompletedAt: time.Now().UTC(),
		ResultJSON: historyResultEnvelope(t, "history_search", page),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(page)
	var outer map[string]json.RawMessage
	var actual string
	if json.Unmarshal([]byte(stored.ResultJSON), &outer) != nil || json.Unmarshal(outer["stdout"], &actual) != nil || actual != string(expected) {
		t.Fatal("search projection was not preserved at its original watermark")
	}
}

func TestSupervisorHistoryResultRejectsFabricatedSourcePages(t *testing.T) {
	st, run, _ := historyFixture(t)
	message := historyMessage(t, st, run, "original exact source")
	request := domain.HistoryReadRequest{SourceID: fmt.Sprintf("message:%d", message.ID), Part: "content"}
	checkpoint, callID := prepareHistoryResultCall(t, st, run, "history_read", request)
	page, err := st.ReadThreadHistory(t.Context(), run.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"content", "hash", "source", "offset", "authority", "unknown_field"} {
		t.Run(change, func(t *testing.T) {
			forged := page
			switch change {
			case "content":
				forged.Content = "password=fabricated-secret"
				forged.ContentSHA256 = session.ContentSHA256(forged.Content)
			case "hash":
				forged.ContentSHA256 = strings.Repeat("f", 64)
			case "source":
				forged.Record.SourceID = "message:999999"
			case "offset":
				forged.Offset++
			case "authority":
				forged.InstructionAuthorized = true
			}
			var value any = forged
			if change == "unknown_field" {
				encoded, _ := json.Marshal(forged)
				var object map[string]any
				_ = json.Unmarshal(encoded, &object)
				object["extra_text"] = "unverified secret"
				value = object
			}
			_, _, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, domain.SupervisorToolResult{
				CallID: callID, Status: domain.SupervisorToolCompleted, CompletedAt: time.Now().UTC(),
				ResultJSON: historyResultEnvelope(t, "history_read", value),
			})
			if apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("forged projection accepted: %v", err)
			}
		})
	}
	rounds, err := st.ListSupervisorToolRounds(t.Context(), checkpoint)
	if err != nil || len(rounds) != 1 || rounds[0].Calls[0].Status != domain.SupervisorToolPending || rounds[0].Calls[0].ResultJSON != "" {
		t.Fatalf("rejected page changed its ledger: %#v %v", rounds, err)
	}
}

func TestSupervisorHistoryResultDoesNotExemptOtherToolsOrErrors(t *testing.T) {
	for _, name := range []string{"note_create", "history_read"} {
		t.Run(name, func(t *testing.T) {
			st, run, _ := historyFixture(t)
			var request any = map[string]string{"title": "fact", "content": "fact"}
			if name == "history_read" {
				request = domain.HistoryReadRequest{SourceID: "message:1", Part: "content"}
			}
			checkpoint, callID := prepareHistoryResultCall(t, st, run, name, request)
			status, code := domain.SupervisorToolCompleted, ""
			if name == "history_read" {
				status, code = domain.SupervisorToolFailed, string(apperror.CodeNotFound)
			}
			// A self-described history version cannot opt another original call,
			// or a failed recall, out of ordinary secret redaction.
			stored, _, err := st.RecordSupervisorToolResult(context.Background(), checkpoint, domain.SupervisorToolResult{
				CallID: callID, Status: status, ErrorCode: code, CompletedAt: time.Now().UTC(),
				ResultJSON: historyResultEnvelope(t, "history_read", map[string]string{"version": domain.HistoryRecallVersion, "content": "password=unverified-secret"}),
			})
			if err != nil || strings.Contains(stored.ResultJSON, "unverified-secret") || !strings.Contains(stored.ResultJSON, "REDACTED") {
				t.Fatalf("ordinary redaction was bypassed: %s %v", stored.ResultJSON, err)
			}
		})
	}
}
