package toolgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Validate what a provider sees, independently of the runtime parser. The old
// shared action branch advertised propose_patch+content as valid, although the
// execution path can only patch via replacements.
func TestWorkspaceChangePublishedActionContract(t *testing.T) {
	definition, _ := AgentCodeToolDefinition(WorkspaceChangeTool)
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	compiler.LoadURL = func(string) (io.ReadCloser, error) { return nil, errors.New("no external schema") }
	if err := compiler.AddResource("workspace-change.json", bytes.NewReader(definition.InputSchema)); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("workspace-change.json")
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, action, digest, fields string
		valid                        bool
	}{
		{"patch", "propose_patch", hash, `,"replacements":[{"old_text":"old","new_text":"new","expected_occurrences":1}]`, true},
		{"patch_missing_replacements", "propose_patch", hash, "", false},
		{"patch_content_is_not_replacement", "propose_patch", hash, `,"content":"new"`, false},
		{"patch_missing_file", "propose_patch", "missing", `,"replacements":[{"old_text":"old","new_text":"new","expected_occurrences":1}]`, false},
		{"patch_mixed_actions", "propose_patch", hash, `,"content":"new","replacements":[{"old_text":"old","new_text":"new","expected_occurrences":1}]`, false},
		{"create", "create", "missing", `,"content":"new"`, true},
		{"empty_create", "create", "missing", `,"content":""`, true},
		{"create_wrong_hash", "create", hash, `,"content":"new"`, false},
		{"replace", "replace", hash, `,"content":"new"`, true},
		{"empty_replace", "replace", hash, `,"content":""`, true},
		{"replace_missing_content", "replace", hash, "", false},
		{"replace_null_content", "replace", hash, `,"content":null`, false},
		{"replace_missing_file", "replace", "missing", `,"content":"new"`, false},
		{"replace_mixed_actions", "replace", hash, `,"content":"new","replacements":[{"old_text":"old","new_text":"new","expected_occurrences":1}]`, false},
		{"move", "move", hash, `,"destination_path":"other.txt","destination_expected_sha256":"missing"`, true},
		{"move_missing_destination", "move", hash, "", false},
		{"move_overwrite", "move", hash, `,"destination_path":"other.txt","destination_expected_sha256":"` + hash + `"`, false},
		{"revert", "propose_revert", hash, `,"source_run_id":"run-source","source_edit_id":"edit-source"`, true},
		{"revert_missing_source", "propose_revert", hash, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"version":"agent-code-tools.v1","action":"` + tc.action + `","path":"test.txt","expected_sha256":"` + tc.digest + `"` + tc.fields + `}`)
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			schemaErr := schema.Validate(value)
			_, parserErr := NormalizeAgentCodePayload(WorkspaceChangeTool, raw)
			if (schemaErr == nil) != tc.valid || (parserErr == nil) != tc.valid {
				t.Fatalf("valid=%t schema=%v parser=%v", tc.valid, schemaErr, parserErr)
			}
			if tc.valid && (tc.action == "create" || tc.action == "replace") {
				canonical, _ := NormalizeAgentCodePayload(WorkspaceChangeTool, raw)
				var normalized any
				if err := json.Unmarshal(canonical, &normalized); err != nil || schema.Validate(normalized) != nil {
					t.Fatalf("normalization lost required content: %s err=%v", canonical, err)
				}
			}
		})
	}
}

func TestWorkspaceApplyPublishedReplaceAlias(t *testing.T) {
	definition, _ := AgentCodeToolDefinition(WorkspaceApplyTool)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("workspace-apply.json", bytes.NewReader(definition.InputSchema)); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("workspace-apply.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"replace", "propose_patch"} {
		raw, _ := json.Marshal(WorkspaceApplyPayload{Version: AgentCodeRegistryVersion, EditID: "edit-alias",
			ExpectedAction: action, ExpectedOriginalSHA256: strings.Repeat("a", 64), ExpectedProposedSHA256: strings.Repeat("b", 64)})
		var value any
		_ = json.Unmarshal(raw, &value)
		if err := schema.Validate(value); err != nil {
			t.Fatalf("apply %s schema: %v", action, err)
		}
		if _, err := NormalizeAgentCodePayload(WorkspaceApplyTool, raw); err != nil {
			t.Fatalf("apply %s parser: %v", action, err)
		}
	}
}
