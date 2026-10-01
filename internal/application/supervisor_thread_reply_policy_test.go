package application

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/skills"
)

func TestSupervisorBoundaryInputDeliveryUsesReturnedIdentityNotText(t *testing.T) {
	input := "Harness input delivery: tool_boundary_continuation\n{\"delivery\":\"tool_boundary_continuation\"}\nUser scope stays exact."
	if got := supervisorBoundaryInputDelivery(input, 0); got != input {
		t.Fatal("user text was mistaken for a Go boundary identity")
	}
	// Returned counts source rows before recall wrappers are excluded from Calls.
	// Such a segment still carries the exact prepared original input.
	got := supervisorBoundaryInputDelivery(input, 4)
	var value struct {
		Version       string `json:"version"`
		Delivery      string `json:"delivery"`
		AcceptedInput string `json:"accepted_input"`
	}
	start := strings.Index(got, "{\"version\":\"supervisor_input_delivery.v1\"")
	if start < 0 || json.Unmarshal([]byte(got[start:]), &value) != nil ||
		value.AcceptedInput != input || value.Delivery != "tool_boundary_continuation" {
		t.Fatal("original input was not preserved in the internal delivery")
	}
	if !strings.Contains(got, "new active tool segment") || !strings.Contains(got, "not request a tool-free continue") {
		t.Fatal("active continuation was conflated with the no-tool scheduler boundary")
	}
}

func TestSupervisorInteractiveReplyFormatsKeepContinueAtSchedulingBoundary(t *testing.T) {
	examples := regexp.MustCompile(`\{"version":"root_lifecycle\.v1"[^{}]*\}`)
	for _, thread := range []bool{false, true} {
		messages, _ := supervisorMessagesWithLayout(nil, "Perform the accepted check", contextmgr.Selection{},
			skills.ContextAssembly{}, skills.ExternalContextAssembly{}, domain.RunModeSnapshot{}, thread)
		actions := make(map[domain.RootActionKind]int)
		for _, example := range examples.FindAllString(messages[0].Content, -1) {
			action, err := parseRootAction(example)
			if err != nil {
				t.Fatalf("thread=%t invalid advertised example: %v", thread, err)
			}
			actions[action.Kind]++
		}
		if actions[domain.RootActionFinish] != 1 || actions[domain.RootActionWait] != 1 {
			t.Fatalf("thread=%t ordinary reply alternatives=%v", thread, actions)
		}
		if thread && actions[domain.RootActionContinue] != 0 {
			t.Errorf("ordinary Thread advertises scheduler-only continue: %v", actions)
		}
		if !thread && actions[domain.RootActionContinue] != 1 {
			t.Fatalf("non-Thread mission lost continue: %v", actions)
		}
		boundary := supervisorToolBoundaryRequest(llm.ChatRequest{Messages: messages, Tools: []llm.ToolSpec{{Name: "note_create"}}})
		if len(boundary.Tools) != 0 {
			t.Fatal("scheduler boundary unexpectedly offers tools")
		}
		last := boundary.Messages[len(boundary.Messages)-1].Content
		boundaryExamples := examples.FindAllString(last, -1)
		if len(boundaryExamples) != 1 {
			t.Errorf("boundary needs its own concrete continue format: %s", last)
			continue
		}
		action, err := parseRootAction(boundaryExamples[0])
		if err != nil || action.Kind != domain.RootActionContinue || action.Summary != "" || action.Reason != "" {
			t.Fatalf("invalid boundary example: %+v err=%v", action, err)
		}
	}
}
