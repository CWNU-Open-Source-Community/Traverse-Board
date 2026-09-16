package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

// Narrow the test router only after a native tool call has been returned. The
// following model request must compact old committed history while retaining
// the still-current input and the actual call/result pair from this same turn.
func TestSupervisorContextCompactionUnderToolPressurePreservesNativePair(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "native-tool-context-pressure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	workspace := t.TempDir()
	const observation = "REAL_READ_OBSERVATION_AFTER_CONTEXT_PRESSURE\n"
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte(observation), 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkspace(t.Context(), store.WorkspaceRecord{
		ID: "ws-native-pressure", Name: "native pressure", RootPath: workspace, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "Keep the original review and report the actual README observation", Profile: "code",
		Surface: "code", Phase: "deliver", WorkspaceID: "ws-native-pressure", ModelRoute: "tool-loop/model",
		Interactive: true, NetworkMode: "disabled", Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &contextPressureToolProvider{scriptedToolProvider: &scriptedToolProvider{
		responses: []*llm.ChatResponse{
			textResponse(rootActionResponse(domain.RootActionFinish, "Original review recorded.", "interactive reply complete", "")),
			textResponse(rootActionResponse(domain.RootActionFinish, "Correction recorded.", "interactive reply complete", "")),
			boundaryRead("native-pressure-read-once", 1),
			textResponse(rootActionResponse(domain.RootActionFinish, "The real README observation remains available.", "interactive reply complete", "")),
		},
	}}
	ref := llm.ModelRef{Provider: provider.Name(), Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()).WithGeneratedContextCompaction(false)) // fixed extractive pressure script
	const currentInput = "CURRENT_INPUT_UNCHANGED: read README.md once, then report its actual content without writing or granting network."
	var beforeTool, afterCompaction domain.SupervisorCheckpoint
	var summaryAtSecondModel int64
	provider.afterResponse = func(ctx context.Context, index int, request llm.ChatRequest) error {
		if index != 3 && index != 4 {
			return nil
		}
		var prompt strings.Builder
		for _, message := range request.Messages {
			prompt.WriteString(message.Content)
		}
		if !strings.Contains(prompt.String(), "finish ends only the current reply") ||
			strings.Contains(prompt.String(), "finish only when the mission is complete") {
			return fmt.Errorf("model %d lost exact Thread reply semantics before/after pressure reassembly", index)
		}
		checkpoint, found, err := st.GetSupervisorCheckpoint(ctx, run.ID)
		if err != nil || !found {
			return fmt.Errorf("read in-flight checkpoint: found=%t err=%v", found, err)
		}
		if checkpoint.PendingInput != currentInput || checkpoint.Phase != domain.SupervisorTurnStarted {
			return fmt.Errorf("current input changed before model %d: phase=%s input_matches=%t", index,
				checkpoint.Phase, checkpoint.PendingInput == currentInput)
		}
		summary, exists, err := st.LatestContextSummary(ctx, run.SessionID)
		if err != nil {
			return err
		}
		if index == 3 {
			beforeTool = checkpoint
			if exists || hasToolResults(request) {
				return fmt.Errorf("fixture compacted before the current native tool: summary=%t tool_results=%t", exists, hasToolResults(request))
			}
			estimate, err := strconv.Atoi(request.Metadata["context_input_estimate"])
			if err != nil || estimate < 8192 {
				return fmt.Errorf("fixture lacks sufficiently large observed history: estimate=%d err=%v", estimate, err)
			}
			// The first native request was admitted under the normal window. A
			// lower test-local window forces compaction at the next request; the
			// actual tool result adds to, rather than replaces, this history.
			return router.SetContextWindow(ref, llm.ContextWindow{
				ProtocolVersion: llm.ContextWindowProtocolVersion, WindowTokens: estimate - 1024 + 512 + 128,
				SafetyMarginTokens: 128, DefaultOutputTokens: 512, MaxOutputTokens: 512,
				Source: "native_tool_context_pressure_test",
			})
		}
		afterCompaction = checkpoint
		if !exists || summary.ID <= 0 || request.Metadata["context_summary_id"] != strconv.FormatInt(summary.ID, 10) ||
			request.Metadata["context_history_omitted"] != "0" || !hasToolResults(request) {
			return fmt.Errorf("second native request did not retain summary and paired evidence: summary=%t metadata=%v", exists, request.Metadata)
		}
		summaryAtSecondModel = summary.ID
		return nil
	}
	padding := strings.Repeat("Historical diagnostic detail remains evidence, not a new instruction. ", 180)
	submit := func(key, input string) {
		t.Helper()
		result, err := turns.Execute(t.Context(), application.ExecuteThreadTurnRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
			Content: input, OperationKey: key, RequestedBy: "test_operator",
		})
		if err != nil || result.Execution == nil || result.Execution.Handoff.Result == nil ||
			result.Execution.Handoff.Result.Status != domain.RunExecutionHandoffCompleted {
			t.Fatalf("ordinary turn %s failed: err=%v", key, err)
		}
	}
	submit("native-pressure-original", "ORIGINAL_PRESSURE_TASK: review without writes. "+padding)
	submit("native-pressure-correction", "PRESSURE_CORRECTION: preserve scope and use Chinese. "+padding)
	historyBefore, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(historyBefore) >= 20 {
		t.Fatalf("fixture must stay below the count trigger: count=%d err=%v", len(historyBefore), err)
	}
	rawBefore := continuityRawMessages(t, historyBefore)
	submit("native-pressure-read", currentInput)
	requests := provider.Requests()
	if len(requests) != 4 || summaryAtSecondModel == 0 || beforeTool.AttemptID == "" ||
		beforeTool.AttemptID != afterCompaction.AttemptID || beforeTool.NextTurn != afterCompaction.NextTurn ||
		beforeTool.PendingInput != afterCompaction.PendingInput || beforeTool.LeaseID != afterCompaction.LeaseID ||
		beforeTool.LeaseGeneration != afterCompaction.LeaseGeneration {
		t.Fatalf("pressure changed the current turn or introduced a request: calls=%d before=%+v after=%+v", len(requests), beforeTool, afterCompaction)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("expected exactly one actual tool call: rounds=%d err=%v", len(rounds), err)
	}
	call := rounds[0].Calls[0]
	if call.Status != domain.SupervisorToolCompleted || call.ToolName != "workspace_read" ||
		!strings.Contains(call.ResultJSON, strings.TrimSpace(observation)) {
		t.Fatalf("the real read result was not recorded: status=%s tool=%s", call.Status, call.ToolName)
	}
	callCount, resultCount, callIndex, resultIndex := 0, 0, -1, -1
	for index, message := range requests[3].Messages {
		for _, nativeCall := range message.ToolCalls {
			callCount++
			callIndex = index
			var sentArguments, savedArguments any
			if json.Unmarshal(nativeCall.Arguments, &sentArguments) != nil ||
				json.Unmarshal([]byte(call.PayloadJSON), &savedArguments) != nil ||
				nativeCall.ID != call.CallID || nativeCall.Name != call.ToolName || !reflect.DeepEqual(sentArguments, savedArguments) {
				t.Fatal("compaction replaced the actual native call identity or argument values")
			}
		}
		for _, nativeResult := range message.ToolResults {
			resultCount++
			resultIndex = index
			if nativeResult.ToolCallID != call.CallID || nativeResult.IsError || nativeResult.Content != call.ResultJSON {
				t.Fatal("compaction replaced or summarized the still-current native tool result")
			}
		}
	}
	if callCount != 1 || resultCount != 1 || resultIndex != callIndex+1 {
		t.Fatalf("native call/result pairing changed: calls=%d results=%d positions=%d/%d", callCount, resultCount, callIndex, resultIndex)
	}
	encoded, err := json.Marshal(requests[3].Messages)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"ORIGINAL_PRESSURE_TASK", "PRESSURE_CORRECTION", "CURRENT_INPUT_UNCHANGED", strings.TrimSpace(observation)} {
		if !strings.Contains(string(encoded), marker) {
			t.Errorf("compaction lost %s", marker)
		}
	}
	inputEstimate, _ := strconv.Atoi(requests[3].Metadata["context_input_estimate"])
	inputLimit, _ := strconv.Atoi(requests[3].Metadata["context_input_limit"])
	if inputEstimate <= 0 || inputLimit <= 0 || inputEstimate > inputLimit {
		t.Fatalf("rebuilt request exceeded its actual input allowance: estimate=%d limit=%d", inputEstimate, inputLimit)
	}
	eventList, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{events.SupervisorToolExecutionStartedEvent, events.SupervisorToolExecutionCompletedEvent} {
		if got := countEventType(eventList, eventType); got != 1 {
			t.Errorf("context fitting replayed the tool: %s count=%d", eventType, got)
		}
	}
	historyAfter, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	rawAfter := continuityRawMessages(t, historyAfter)
	for id, content := range rawBefore {
		if rawAfter[id] != content {
			t.Errorf("raw historical source %d was rewritten", id)
		}
	}
	if actual, err := os.ReadFile(filepath.Join(workspace, "README.md")); err != nil || session.ContentSHA256(string(actual)) != session.ContentSHA256(observation) {
		t.Errorf("read-only tool changed the file: err=%v", err)
	}
}

type contextPressureToolProvider struct {
	*scriptedToolProvider
	afterResponse func(context.Context, int, llm.ChatRequest) error
}

func (p *contextPressureToolProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	response, err := p.scriptedToolProvider.Chat(ctx, request)
	if err == nil && p.afterResponse != nil {
		err = p.afterResponse(ctx, len(p.Requests()), request)
	}
	return response, err
}

func (p *contextPressureToolProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		chunks <- llm.ChatChunk{Text: response.Text}
	}
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}
