package llm

import (
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
			state := responsesStreamState{provider: "test"}
			item := responsesStreamItem{callID: "call_test", name: "workspace_change"}
			chunk, done, err := state.completeTool("fc_test", &item, tc.arguments)
			if err == nil || ProviderErrorKind(err) != OutcomeInvalidResponse || ProviderErrorKind(err).Retryable() ||
				chunk != nil || done || item.callCompleted || len(state.toolCalls) != 0 {
				t.Fatal("malformed native arguments became executable or retryable")
			}
			if strings.Contains(err.Error(), "check the model output-token limit") != tc.incomplete {
				t.Fatalf("wrong incomplete argument classification: %v", err)
			}
			if strings.Contains(err.Error(), tc.arguments) {
				t.Fatal("argument body exposed in error")
			}
		})
	}
}
