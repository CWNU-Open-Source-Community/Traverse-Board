package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/llm"
)

func TestSupervisorInheritedContextIsRequiredAndLargerWindowsCanCarryIt(t *testing.T) {
	goal := []contextmgr.Section{{Kind: "task_goal", SourceID: "mission", Content: "original goal", Priority: 1000}}
	inherited := []contextmgr.Section{{Kind: "continuity_context", SourceID: "inherited", Content: strings.Repeat("保留", 1600), Priority: 1000}}
	notes := []contextmgr.Section{{Kind: "note", SourceID: "optional", Content: strings.Repeat("note ", 25000), Priority: 500}}
	small, err := supervisorMemoryContext(contextmgr.Summary{}, false, nil, nil, nil, goal, inherited, notes)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireSupervisorContinuityContext(small, contextmgr.Summary{}, false, "mission", "inherited"); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("inherited history was silently omitted: %v", err)
	}
	window := llm.DefaultContextWindow()
	large, err := supervisorMemoryContextWithinBudget(supervisorMemoryBudget(window),
		false, contextmgr.Summary{}, false, nil, nil, nil, goal, inherited, notes)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireSupervisorContinuityContext(large, contextmgr.Summary{}, false, "mission", "inherited"); err != nil {
		t.Fatal(err)
	}
	if containsContextSource(large.IncludedSources, "note", "optional") || !containsContextSource(large.IncludedSources, "continuity_context", "inherited") {
		t.Fatal("optional notes displaced required predecessor context")
	}
	// The allocation alone is not permission to overflow the model: final
	// request fitting still counts tool schemas, current input and output.
	inputLimit, _ := window.InputLimit(window.MaxOutputTokens)
	if large.EstimatedTokens > inputLimit/2 {
		t.Fatal("memory exceeded its larger-model allocation")
	}
}
