package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesIncompleteFunctionArgumentsRemainRejectedWithUsefulReason(t *testing.T) {
	for _, tc := range []struct {
		name, arguments string
		incomplete      bool
	}{
		{"truncated file", `{"content":"unfinished`, true},
		{"invalid escape", `{"content":"\q"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := responsesStreamState{
				provider: "test", selectedModel: "model", responseID: "resp_test",
				started: true, publicItems: 1,
				items: map[string]*responsesStreamItem{},
			}
			item := responsesStreamItem{callID: "call_test", name: "workspace_change"}
			state.items["fc_test"] = &item
			state.itemOrder = []string{"fc_test"}
			chunk, done, err := state.completeTool("fc_test", &item, tc.arguments)
			if err != nil || chunk == nil || done || !item.callCompleted || !item.callInvalid || len(state.toolCalls) != 0 {
				t.Fatal("malformed native arguments did not remain pending until the terminal event")
			}
			item.completed = true
			item.final = &openAIResponsesOutputItem{ID: "fc_test", Type: "function_call", Status: "completed",
				CallID: item.callID, Name: item.name, Arguments: tc.arguments}
			usage, total := 1, 2
			terminal, terminalDone, terminalErr := state.consume(mustJSON(t, openAIResponsesStreamEvent{
				Type: "response.completed", Response: openAIResponsesResponse{
					ID: "resp_test", Object: "response", Status: "completed", Model: "model",
					Usage: &openAIResponsesUsage{InputTokens: &usage, OutputTokens: &usage, TotalTokens: &total},
				},
			}))
			if terminalErr == nil || ProviderErrorKind(terminalErr) != OutcomeInvalidResponse || ProviderErrorKind(terminalErr).Retryable() ||
				terminal != nil || terminalDone {
				t.Fatal("malformed native arguments became executable or retryable")
			}
			if strings.Contains(terminalErr.Error(), "check the model output-token limit") != tc.incomplete {
				t.Fatalf("wrong incomplete argument classification: %v", terminalErr)
			}
			if strings.Contains(terminalErr.Error(), tc.arguments) {
				t.Fatal("argument body exposed in error")
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
