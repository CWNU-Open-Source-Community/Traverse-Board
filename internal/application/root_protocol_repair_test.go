package application

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestToolRequestRepairRetainsSchemasAndCompletedToolHistory(t *testing.T) {
	reason, err := domain.NewSupervisorToolRequestRepairReason(1, "expected_occurrences must be 1..1024")
	if err != nil {
		t.Fatal(err)
	}
	request := llm.ChatRequest{Tools: []llm.ToolSpec{{Name: "workspace_change"}}, Messages: []llm.Message{
		{Role: "system", Content: "original policy"},
		{Role: "user", Content: "edit the original entry only"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "read-1", Name: "workspace_read", Arguments: json.RawMessage(`{}`)}}},
		{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: "read-1", Content: `{"sha256":"verified source"}`}}},
	}}
	repair := supervisorProtocolRepairRequest(request, reason)
	if !reflect.DeepEqual(repair.Tools, request.Tools) || !reflect.DeepEqual(repair.Messages[2:len(repair.Messages)-1], request.Messages[1:]) || repair.Metadata["protocol_repair"] != "1" {
		t.Fatal("tool correction changed schemas or verified transcript")
	}
	if !strings.Contains(repair.Messages[1].Content, "before execution") || !strings.Contains(repair.Messages[1].Content, "expected_occurrences") {
		t.Fatal("tool correction omitted its no-effect boundary or actionable field diagnostic")
	}
}

func TestRootProtocolRepairPlacesFormatInstructionAfterIntactToolResults(t *testing.T) {
	request := llm.ChatRequest{JSONMode: true, Tools: []llm.ToolSpec{{Name: "web_search"}},
		Messages: []llm.Message{
			{Role: "system", Content: "original policy"},
			{Role: "user", Content: "Use saved sources only; do not search or write files."},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "search-call", Name: "web_search", Arguments: json.RawMessage(`{"query":"docs"}`)}}},
			{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: "search-call", Content: `{"source":"https://docs.example.com/"}`}}},
		}}
	repair := supervisorProtocolRepairRequest(request, "invalid JSON")
	if !repair.JSONMode || len(repair.Tools) != 0 || repair.Metadata["protocol_repair"] != "1" ||
		len(repair.Messages) != len(request.Messages)+2 || len(request.Tools) != 1 {
		t.Fatal("repair changed the qualified strategy, retained tools, or mutated the original request")
	}
	if !reflect.DeepEqual(repair.Messages[0], request.Messages[0]) ||
		!reflect.DeepEqual(repair.Messages[2:len(repair.Messages)-1], request.Messages[1:]) {
		t.Fatal("repair discarded or reordered policy, current input, or paired tool history")
	}
	last := repair.Messages[len(repair.Messages)-1]
	if last.Role != "user" || last.Content != rootProtocolRepairOutputInstruction ||
		len(last.ToolCalls) != 0 || len(last.ToolResults) != 0 {
		t.Fatal("repair did not end with the explicit non-authorizing JSON instruction")
	}
}

func TestRootProtocolRepairReasonIncludesSpecificValidationCause(t *testing.T) {
	for _, test := range []struct {
		name, response, want string
	}{
		{"unknown field", `{"version":"root_lifecycle.v1","action":"continue","message":"Ready","citation":[]}`, `unknown field "citation"`},
		{"wrong message type", `{"version":"root_lifecycle.v1","action":"continue","message":[]}`, "cannot unmarshal array"},
		{"finish without summary", `{"version":"root_lifecycle.v1","action":"finish","message":"Ready"}`, "finish action summary is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseRootAction(test.response)
			if err == nil {
				t.Fatal("invalid response was accepted")
			}
			reason := supervisorProtocolRepairReason(err)
			if !strings.Contains(reason, test.want) || strings.Contains(reason, test.response) {
				t.Fatalf("repair reason omits its cause or leaks the rejected body: %q", reason)
			}
		})
	}
}

func TestRootProtocolRepairReasonRemainsBoundedAndRedacted(t *testing.T) {
	secret := "sk-" + strings.Repeat("a", 48)
	err := apperror.Wrap(apperror.CodeFailedPrecondition, "invalid protocol",
		errors.New("api_key="+secret+" "+strings.Repeat("界", maxProtocolRepairReasonChars)))
	reason := supervisorProtocolRepairReason(err)
	if strings.Contains(reason, secret) || !utf8.ValidString(reason) ||
		utf8.RuneCountInString(reason) > maxProtocolRepairReasonChars {
		t.Fatalf("unsafe or unbounded repair diagnostic: %q", reason)
	}
	if supervisorProtocolRepairReason(nil) != "response did not conform to root_lifecycle.v1" {
		t.Fatal("nil repair diagnostic changed")
	}
}
