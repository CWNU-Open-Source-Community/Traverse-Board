package application

import (
	"bytes"
	"encoding/json"

	"cyberagent-workbench/internal/toolgateway"
)

// supervisorAcceptedToolExecutionPayload is only for an already persisted call,
// after invokeSupervisorTool verifies its Agent and current execution authority.
// The old writer omitted content:"" from a normalized create. Preserve that
// accepted intent without changing its durable JSON, semantic key or wire IDs.
// Incoming provider calls and direct Gateway calls still require explicit body.
func supervisorAcceptedToolExecutionPayload(name toolgateway.ToolName, durableJSON string) json.RawMessage {
	raw := json.RawMessage(durableJSON)
	if name != toolgateway.WorkspaceChangeTool {
		return raw
	}
	var old struct {
		Version        string `json:"version"`
		Action         string `json:"action"`
		Path           string `json:"path"`
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if json.Unmarshal(raw, &old) != nil || old.Version != toolgateway.AgentCodeRegistryVersion ||
		old.Action != "create" || old.ExpectedSHA256 != "missing" {
		return raw
	}
	// Exact old canonical bytes exclude extra/duplicate/null fields, alternate
	// actions, and noncanonical spellings that the historic writer never emitted.
	canonical, err := json.Marshal(old)
	if err != nil || !bytes.Equal(canonical, raw) {
		return raw
	}
	upgraded, err := json.Marshal(toolgateway.WorkspaceChangePayload{
		Version: old.Version, Action: old.Action, Path: old.Path,
		ExpectedSHA256: old.ExpectedSHA256, Content: "",
	})
	if err != nil {
		return raw
	}
	validated, err := toolgateway.NormalizeAgentCodePayload(name, upgraded)
	if err != nil {
		return raw
	}
	return validated
}
