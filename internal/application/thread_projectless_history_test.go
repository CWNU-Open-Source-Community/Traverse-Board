package application_test

import (
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestProjectlessThreadHistorySettlesAndCannotReadAnotherProjectlessThread(t *testing.T) {
	first := toolResponse("search-current", "history_search", `{"query":"黎曼猜想","limit":20}`)
	first.ToolCalls = append(first.ToolCalls, llm.ToolCall{ID: "search-tools", Name: "history_search", Arguments: json.RawMessage(`{"query":"web_search","limit":20}`)})
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{first, historyRecallFinish()}}
	st, turns, request := threadControlFixture(t, provider)
	thread, err := st.GetThread(t.Context(), request.ThreadID)
	if err != nil || thread.WorkspaceID != "" {
		t.Fatal("fixture is not an actual projectless Thread")
	}
	request.Content = "联网搜索黎曼猜想的最新进展"
	result, err := turns.Execute(t.Context(), request)
	if err != nil || len(provider.Requests()) != 2 || result.Submission.Message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("projectless history left original input unresolved: calls=%d err=%v", len(provider.Requests()), err)
	}
	runID := result.Submission.Run.ID
	calls := historyRecallCalls(t, st, runID)
	if len(calls) != 2 {
		t.Fatalf("actual calls=%d", len(calls))
	}
	for _, call := range calls {
		var outer struct{ Stdout string }
		var page domain.HistorySearchResult
		if call.Status != domain.SupervisorToolCompleted || json.Unmarshal([]byte(call.ResultJSON), &outer) != nil || json.Unmarshal([]byte(outer.Stdout), &page) != nil || page.RunID != runID || page.ThreadID != request.ThreadID {
			t.Fatalf("history was not executed/sealed for this Thread: %+v", call)
		}
	}
	_, _ = turns.Execute(t.Context(), request)
	if len(provider.Requests()) != 2 {
		t.Fatal("original key replay repeated history or model work")
	}

	// Empty workspace is not a shared namespace: create a second ordinary
	// Thread through the same product services, then use its real opaque ID.
	const canary = "FOREIGN_PROJECTLESS_HISTORY_CANARY_20260913"
	_, foreign, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "another conversation", Profile: "review", ModelRoute: provider.Name() + "/model", Interactive: true, Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	provider.responses = append(provider.responses, historyRecallFinish())
	foreignRequest := request
	foreignRequest.ThreadID, foreignRequest.Content, foreignRequest.OperationKey = domain.InitialThreadID(foreign.ID), canary, "projectless-foreign-input"
	if _, err := turns.Execute(t.Context(), foreignRequest); err != nil {
		t.Fatal(err)
	}
	page, err := st.SearchThreadHistory(t.Context(), foreign.ID, domain.HistorySearchRequest{Query: canary})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("foreign source=%+v err=%v", page, err)
	}
	provider.responses = append(provider.responses, toolResponse("foreign-read", "history_read", historyRecallReadJSON(domain.HistoryReadRequest{SourceID: page.Records[0].SourceID, ExpectedSHA256: page.Records[0].ContentSHA256})), historyRecallFinish())
	request.OperationKey, request.Content = "projectless-denied-foreign-read", "Read the supplied source only if this conversation can access it."
	if _, err := turns.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	calls = historyRecallCalls(t, st, runID)
	denied := 0
	for _, call := range calls {
		if call.ToolName == "history_read" {
			if call.Status == domain.SupervisorToolCompleted || call.ErrorCode != string(apperror.CodeNotFound) {
				t.Fatalf("foreign projectless source accepted: %+v", call)
			}
			denied++
		}
	}
	if denied != 1 || len(provider.Requests()) != 5 {
		t.Fatal("foreign read did not settle exactly once")
	}
	for _, modelRequest := range provider.Requests()[3:] {
		encoded, _ := json.Marshal(modelRequest.Messages)
		if strings.Contains(string(encoded), canary) {
			t.Fatal("foreign empty-workspace source leaked into current model context")
		}
	}
}
