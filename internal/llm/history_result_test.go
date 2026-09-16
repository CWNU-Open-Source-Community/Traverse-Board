package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStoredHistorySurvivesRedactionWithoutAcceptingWireFlags(t *testing.T) {
	page, _ := json.Marshal(map[string]any{"version": "history_recall.v1", "instruction_authorized": false,
		"content": "password=[REDACTED:secret]\nkeep exact\ntoken=[REDACTE"})
	outer, _ := json.Marshal(map[string]any{"version": "supervisor_tool_result.v1", "tool": "history_read", "status": "completed", "stdout": string(page)})
	original := ToolResult{ToolCallID: "read-original", Content: string(outer)}
	protected := StoredHistoryToolResult(original)
	if !protected.preservesStoredHistory() {
		t.Fatal("stored history was not marked")
	}
	request, err := redactRequest(ChatRequest{Messages: []Message{{Role: "user", ToolResults: []ToolResult{protected}}}})
	if err != nil || request.Messages[0].ToolResults[0].Content != original.Content {
		t.Fatalf("exact source page changed: %v", err)
	}
	// Serialization never exports an instruction to skip redaction.
	wire, err := json.Marshal(protected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "Digest") || strings.Contains(string(wire), "storedHistory") {
		t.Fatal("internal marker reached JSON")
	}
	var unmarked ToolResult
	if err := json.Unmarshal(wire, &unmarked); err != nil {
		t.Fatal(err)
	}
	if unmarked.preservesStoredHistory() {
		t.Fatal("wire data restored the internal marker")
	}
	unsafe := ToolResult{ToolCallID: "other-result", Content: "password=verysecretvalue"}
	changed := protected
	changed.Content = unsafe.Content
	for _, result := range []ToolResult{unmarked, unsafe, changed} {
		request, err := redactRequest(ChatRequest{Messages: []Message{{Role: "user", ToolResults: []ToolResult{result}}}})
		if err != nil {
			t.Fatal(err)
		}
		if request.Messages[0].ToolResults[0].Content == result.Content {
			t.Fatal("unmarked or changed content bypassed normal redaction")
		}
	}
}
