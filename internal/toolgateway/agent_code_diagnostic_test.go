package toolgateway

import (
	"strings"
	"testing"
)

func TestPatchDiagnosticNamesInvalidFieldWithoutEchoingContent(t *testing.T) {
	value := WorkspaceChangePayload{Version: AgentCodeRegistryVersion, Action: "propose_patch", Path: "src/entry.mjs", ExpectedSHA256: strings.Repeat("a", 64), Replacements: []WorkspaceReplacement{{OldText: "private source", NewText: "private replacement", ExpectedOccurrences: 0}}}
	err := normalizeWorkspaceChangePayload(&value)
	if err == nil || !strings.Contains(err.Error(), "replacements[0].expected_occurrences") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unhelpful or content-leaking diagnostic: %v", err)
	}
	value.Replacements[0].ExpectedOccurrences = 1
	value.Replacements[0].OldText = ""
	err = normalizeWorkspaceChangePayload(&value)
	if err == nil || !strings.Contains(err.Error(), "replacements[0].old_text") {
		t.Fatalf("missing old_text diagnostic: %v", err)
	}
	value.Replacements[0].OldText = "old"
	value.Replacements[0].NewText = ""
	if err = normalizeWorkspaceChangePayload(&value); err != nil {
		t.Fatalf("valid deletion replacement rejected: %v", err)
	}
}
