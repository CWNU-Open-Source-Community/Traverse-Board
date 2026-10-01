package application

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

// A workspace_change result must remain directly usable after either receipt
// path. The live workspace_apply parser still owns authorization and CAS.
func TestSupervisorProposalReceiptPreservesCompleteApplyContract(t *testing.T) {
	for _, action := range []string{"replace", "propose_patch", "create", "move"} {
		for _, authorized := range []bool{false, true} {
			t.Run(action+"/authorized="+map[bool]string{false: "false", true: "true"}[authorized], func(t *testing.T) {
				original, proposed := strings.Repeat("a", 64), strings.Repeat("b", 64)
				if action == "create" {
					original = "missing"
				}
				if action == "move" {
					proposed = "missing"
				}
				arguments := toolgateway.WorkspaceApplyPayload{Version: toolgateway.AgentCodeRegistryVersion,
					EditID: "edit-proposal", ExpectedAction: action,
					ExpectedOriginalSHA256: original, ExpectedProposedSHA256: proposed}
				stdout, err := json.Marshal(map[string]any{"version": toolgateway.AgentCodeRegistryVersion,
					"edit_id": arguments.EditID, "path": "styles.css", "operation": action,
					"original_sha256": original, "proposed_sha256": proposed, "status": "approved",
					"apply_authorized": authorized, "review_required": !authorized, "apply_arguments": arguments})
				if err != nil {
					t.Fatal(err)
				}
				round := segmentReceiptRound(t, 1, 1, func(int) string { return string(stdout) })
				round.Calls[0].ToolName = "workspace_change"
				envelope, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
					Version: supervisorToolResultVersion, Tool: "workspace_change", Status: "completed", Stdout: string(stdout)})
				if err != nil {
					t.Fatal(err)
				}
				round.Calls[0].ResultJSON = string(envelope)
				projection, ok := supervisorSegmentReceiptProjection(round.Calls[0])
				if !ok {
					t.Fatal("proposal result cannot be projected")
				}
				assertReceiptApplyContract(t, projection, arguments, authorized)
				plan, err := supervisorSegmentReceiptPlan([]domain.SupervisorToolRound{round}, 1,
					supervisorSegmentReceiptTokenBudget, round.AttemptID)
				if err != nil {
					t.Fatal(err)
				}
				var receipt supervisorSegmentReceiptRound
				lines := strings.Split(strings.TrimPrefix(plan.ReceiptContent, supervisorSegmentReceiptPrefix), "\n")
				if err := json.Unmarshal([]byte(lines[0]), &receipt); err != nil {
					t.Fatal(err)
				}
				assertReceiptApplyContract(t, receipt.Calls[0].Result, arguments, authorized)
				// At the identity-only budget the entire result is omitted, rather
				// than advertising a partial executable argument object.
				minimal, err := supervisorSegmentReceiptEntries([]domain.SupervisorToolRound{round})
				if err != nil {
					t.Fatal(err)
				}
				minimalJSON, _ := json.Marshal(minimal[0])
				minimalContent := supervisorSegmentReceiptPrefix + string(minimalJSON) + "\nNo native tool call or result pairs of this segment remain in this request."
				minimumBudget := estimateModelRequestTokens(llm.ChatRequest{Messages: []llm.Message{
					supervisorSegmentReceiptMessage(segmentReceiptBudgetSessionID, round.AttemptID, minimalContent),
				}}) - 8
				tight, err := supervisorSegmentReceiptPlan([]domain.SupervisorToolRound{round}, 1, minimumBudget, round.AttemptID)
				if err != nil {
					t.Fatal(err)
				}
				var omitted supervisorSegmentReceiptRound
				if err := json.Unmarshal([]byte(strings.Split(strings.TrimPrefix(tight.ReceiptContent, supervisorSegmentReceiptPrefix), "\n")[0]), &omitted); err != nil {
					t.Fatal(err)
				}
				entry := omitted.Calls[0]
				if entry.Result != nil || !entry.DetailsOmitted || entry.OriginalResult.SourceID == "" ||
					entry.OriginalResult.ExpectedSHA256 != receipt.Calls[0].OriginalResult.ExpectedSHA256 || tight.ReceiptTokens > minimumBudget {
					t.Fatal("tight receipt lost its whole-result omission or exact recall contract")
				}
				boundary, err := boundedToolBoundaryContext(round.AttemptID, round.Calls)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, line := range strings.Split(boundary, "\n") {
					var entry struct {
						Result any `json:"result"`
					}
					if json.Unmarshal([]byte(line), &entry) == nil && entry.Result != nil {
						assertReceiptApplyContract(t, entry.Result, arguments, authorized)
						found = true
					}
				}
				if !found {
					t.Fatal("boundary omitted the small proposal result")
				}
			})
		}
	}
}

func assertReceiptApplyContract(t *testing.T, result any, want toolgateway.WorkspaceApplyPayload, authorized bool) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Arguments      json.RawMessage `json:"apply_arguments"`
		Authorized     bool            `json:"apply_authorized"`
		ReviewRequired bool            `json:"review_required"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	normalized, err := toolgateway.NormalizeAgentCodePayload(toolgateway.WorkspaceApplyTool, got.Arguments)
	if err != nil {
		t.Fatalf("receipt apply_arguments cannot pass the advertised parser: %s: %v", got.Arguments, err)
	}
	var actual toolgateway.WorkspaceApplyPayload
	if err := json.Unmarshal(normalized, &actual); err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatalf("receipt changed the exact apply contract: %+v want %+v err=%v", actual, want, err)
	}
	if got.Authorized != authorized || got.ReviewRequired != !authorized {
		t.Fatal("receipt changed authorization/review observations")
	}
}
