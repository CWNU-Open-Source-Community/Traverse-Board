package toolgateway

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentCodeRevertPayloadAcceptsOnlyExactSourceCoordinates(t *testing.T) {
	base := map[string]any{"version": AgentCodeRegistryVersion, "action": "propose_revert",
		"source_run_id": "run-source", "source_edit_id": "edit-source", "path": "target.txt",
		"expected_sha256": strings.Repeat("a", 64)}
	for _, hash := range []string{strings.Repeat("a", 64), "missing"} {
		base["expected_sha256"] = hash
		raw, _ := json.Marshal(base)
		canonical, err := NormalizeAgentCodePayload(WorkspaceChangeTool, raw)
		if err != nil {
			t.Fatal(err)
		}
		again, err := NormalizeAgentCodePayload(WorkspaceChangeTool, canonical)
		if err != nil || !bytes.Equal(again, canonical) {
			t.Fatalf("revert request is not stable: %s %s %v", canonical, again, err)
		}
	}
	for _, field := range []string{"content", "replacements", "destination_path", "destination_expected_sha256", "run_id", "apply"} {
		changed := map[string]any{}
		for key, value := range base {
			changed[key] = value
		}
		changed[field] = ""
		raw, _ := json.Marshal(changed)
		if _, err := NormalizeAgentCodePayload(WorkspaceChangeTool, raw); err == nil {
			t.Fatalf("revert accepted unrelated field %q", field)
		}
	}
	for _, field := range []string{"source_run_id", "source_edit_id", "path", "expected_sha256"} {
		changed := map[string]any{}
		for key, value := range base {
			if key != field {
				changed[key] = value
			}
		}
		raw, _ := json.Marshal(changed)
		if _, err := NormalizeAgentCodePayload(WorkspaceChangeTool, raw); err == nil {
			t.Fatalf("revert accepted missing %q", field)
		}
	}
	for _, field := range []string{"source_run_id", "source_edit_id"} {
		old := map[string]any{"version": AgentCodeRegistryVersion, "action": "create", "path": "target.txt", "expected_sha256": "missing", "content": "new", field: ""}
		raw, _ := json.Marshal(old)
		if _, err := NormalizeAgentCodePayload(WorkspaceChangeTool, raw); err == nil {
			t.Fatalf("non-revert accepted source field %q", field)
		}
	}
	definition, found := AgentCodeToolDefinition(WorkspaceChangeTool)
	if !found || !json.Valid(definition.InputSchema) || !bytes.Contains(definition.InputSchema, []byte(`"propose_revert"`)) {
		t.Fatal("revert is missing from the existing tool schema")
	}
}
