package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/llm"
)

// An existing path is a failed tool result the model can correct, not unknown
// mutation work that prevents the accepted Thread input from ever settling.
func TestThreadWorkspaceCreateConflictAllowsCorrectedProposal(t *testing.T) {
	st, run, root, input := toolBoundaryFixture(t, domain.Budget{MaxTurns: 8, MaxToolCalls: 20})
	p := &boundaryJourneyProvider{}
	p.respond = func(ctx context.Context, request llm.ChatRequest, index int) (*llm.ChatResponse, error) {
		switch index {
		case 1:
			return toolResponse("existing-create", "workspace_change", `{"version":"agent-code-tools.v1","action":"create","path":"README.md","expected_sha256":"missing","content":"unreviewed overwrite\n"}`), nil
		case 2:
			if !hasErrorToolResult(request, "CONFLICT") {
				t.Fatal("the model did not receive the actual file conflict")
			}
			edits, err := st.ListFileEdits(ctx, fileedit.ListFilter{SessionID: run.SessionID})
			if err != nil || len(edits) != 0 {
				t.Fatalf("rejected create saved a proposal: %#v %v", edits, err)
			}
			return boundaryPropose("corrected-patch"), nil
		case 3:
			return textResponse(rootActionResponse(domain.RootActionFinish, "The existing file was preserved; a replacement proposal awaits review", "proposal ready", "")), nil
		default:
			return nil, fmt.Errorf("unexpected model request %d", index)
		}
	}
	service := toolBoundaryService(st, st, p)
	result, err := service.Execute(t.Context(), input)
	if err != nil || result.Execution == nil || result.Submission.Message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("conflict stranded the accepted input: %#v %v", result, err)
	}
	if len(p.Requests()) != 3 {
		t.Fatalf("model requests=%d want=3", len(p.Requests()))
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 10)
	if err != nil || len(rounds) != 2 || len(rounds[0].Calls) != 1 || len(rounds[1].Calls) != 1 {
		t.Fatalf("unexpected durable tool rounds: %#v %v", rounds, err)
	}
	seen := make(map[string]bool)
	for _, round := range rounds {
		call := round.Calls[0]
		var payload struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(call.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		seen[payload.Action] = true
		switch payload.Action {
		case "create":
			if call.Status != domain.SupervisorToolFailed || call.ErrorCode != "CONFLICT" {
				t.Fatalf("create conflict is still unresolved: %#v", call)
			}
		case "propose_patch":
			if call.Status != domain.SupervisorToolCompleted {
				t.Fatalf("corrected proposal did not complete: %#v", call)
			}
		default:
			t.Fatalf("unexpected proposal action: %q", payload.Action)
		}
	}
	if !seen["create"] || !seen["propose_patch"] {
		t.Fatalf("missing conflict or corrected proposal: %v", seen)
	}
	edits, err := st.ListFileEdits(t.Context(), fileedit.ListFilter{SessionID: run.SessionID})
	if err != nil || len(edits) != 1 || edits[0].Status != fileedit.StatusProposed || edits[0].Operation != fileedit.OperationReplace {
		t.Fatalf("corrected proposal=%#v %v", edits, err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "README.md")); err != nil || string(content) != "original text\n" {
		t.Fatalf("proposal unexpectedly wrote the file: %q %v", content, err)
	}
	if _, err := service.Execute(t.Context(), input); err != nil || len(p.Requests()) != 3 {
		t.Fatalf("confirming the original input repeated model work: %v", err)
	}
	assertOneBoundaryTranscriptInput(t, st, input.ThreadID, input.Content)
	// A new ordinary input must work in the same Thread without a recovery UI.
	next := input
	next.Content, next.OperationKey = "Keep the proposal for later review", "after-workspace-create-conflict"
	followup := &scriptedToolProvider{responses: []*llm.ChatResponse{
		textResponse(rootActionResponse(domain.RootActionFinish, "Proposal retained without applying it", "unchanged", "")),
	}}
	continued, err := toolBoundaryService(st, st, followup).Execute(t.Context(), next)
	if err != nil || continued.Submission.Message.Status != domain.OperatorSteeringCommitted || len(followup.Requests()) != 1 {
		t.Fatalf("ordinary follow-up remained blocked: %#v %v", continued, err)
	}
}
