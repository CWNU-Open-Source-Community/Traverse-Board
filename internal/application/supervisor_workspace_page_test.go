package application

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

// These three sealed, synthetic workspace reads are the pages present in real
// request 023 and lost in request 024 of the 2026-09-29 long-task acceptance.
func recordedWorkspacePages(t *testing.T) []domain.SupervisorToolCall {
	t.Helper()
	raw, err := os.ReadFile("testdata/workspace-read-pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls []domain.SupervisorToolCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if err := call.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	return calls
}

func TestWorkspacePageIdentityIncludesScopeVersionRangeAndRedaction(t *testing.T) {
	original := recordedWorkspacePages(t)[1]
	_, key, ok := supervisorWorkspaceReadPage(original)
	if !ok {
		t.Fatal("real page did not decode")
	}
	for name, value := range map[string]any{"workspace_id": "other-workspace", "root_fingerprint": strings.Repeat("a", 64),
		"content_sha256": strings.Repeat("b", 64), "start_line": float64(2), "redaction_count": float64(1), "content": "different observed body"} {
		t.Run(name, func(t *testing.T) {
			call := original
			var e supervisorToolResultEnvelope
			_ = json.Unmarshal([]byte(call.ResultJSON), &e)
			var page map[string]any
			_ = json.Unmarshal([]byte(e.Stdout), &page)
			page[name] = value
			if s, yes := value.(string); yes {
				if _, exists := e.Metadata[name]; exists {
					e.Metadata[name] = s
				}
			}
			raw, _ := json.Marshal(page)
			e.Stdout = string(raw)
			encoded, _ := marshalSupervisorToolResultEnvelope(e)
			call.ResultJSON = string(encoded)
			_, changed, valid := supervisorWorkspaceReadPage(call)
			if !valid || changed == key {
				t.Fatal("different scope/version/page/redaction was treated as the same page")
			}
		})
	}
	rejected := original
	rejected.Status = domain.SupervisorToolFailed
	if _, _, ok := supervisorWorkspaceReadPage(rejected); ok {
		t.Fatal("failed read was retained as a complete page")
	}
	var e supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(original.ResultJSON), &e)
	e.Metadata["content_sha256"] = strings.Repeat("e", 64)
	encoded, _ := marshalSupervisorToolResultEnvelope(e)
	rejected = original
	rejected.ResultJSON = string(encoded)
	if _, _, ok := supervisorWorkspaceReadPage(rejected); ok {
		t.Fatal("conflicting file identities were merged")
	}
}

func TestFileEffectsDeduplicateOnlyFinalExactBoundaryObservation(t *testing.T) {
	call := boundaryBudgetCall(t, "dedup-proposal", "workspace_change", map[string]any{
		"version": "agent-code-tools.v1", "edit_id": "edit-dedup", "path": "app.js", "operation": "create",
		"status": "proposed", "proposed_sha256": strings.Repeat("a", 64), "apply_authorized": false, "review_required": true})
	cp := domain.SupervisorCheckpoint{RunID: call.RunID, NextTurn: call.Turn + 1, AttemptID: "next-attempt"}
	boundary, err := boundedToolBoundaryContext(cp.AttemptID, []domain.SupervisorToolCall{call})
	if err != nil {
		t.Fatal(err)
	}
	exact, err := boundedSupervisorFileEffectContext(cp, []domain.SupervisorToolCall{call}, boundary)
	if err != nil || exact != "" {
		t.Fatal("exact duplicate was not removed", err, exact)
	}
	for _, other := range []string{"candidate omitted from final boundary", strings.ReplaceAll(boundary, "proposal_only", "unknown"), strings.ReplaceAll(boundary, "\"part\":\"result\"", "\"part\":\"other\"")} {
		kept, err := boundedSupervisorFileEffectContext(cp, []domain.SupervisorToolCall{call}, other)
		if err != nil || !strings.Contains(kept, "proposal_only") {
			t.Fatal("nonidentical or omitted observation erased independent evidence", err)
		}
	}
}

func TestRecordedWorkspacePagesSurviveReceiptWithinOriginalBudget(t *testing.T) {
	calls := recordedWorkspacePages(t)
	round := domain.SupervisorToolRound{RunID: calls[0].RunID, Turn: calls[0].Turn, AttemptID: calls[0].AttemptID,
		Round: calls[0].Round, ModelAttempt: calls[0].ModelAttempt, CreatedAt: calls[0].CreatedAt, CompletedAt: calls[2].CompletedAt, Calls: calls}
	plan, err := supervisorSegmentReceiptPlan([]domain.SupervisorToolRound{round}, 1, supervisorSegmentReceiptTokenBudget, "next-attempt")
	if err != nil {
		t.Fatal(err)
	}
	// The live repro lost all three bodies. At least one selected complete page
	// must survive without expanding the 2048 budget; never a sliced body.
	whole := 0
	for _, call := range calls {
		var envelope supervisorToolResultEnvelope
		_ = json.Unmarshal([]byte(call.ResultJSON), &envelope)
		var page map[string]any
		_ = json.Unmarshal([]byte(envelope.Stdout), &page)
		encoded, _ := json.Marshal(page["content"])
		if strings.Contains(plan.ReceiptContent, string(encoded)) {
			whole++
		}
		if !strings.Contains(plan.ReceiptContent, domain.SupervisorToolResultReference(call).ExpectedSHA256) {
			t.Fatal("receipt lost exact readback")
		}
	}
	if whole == 0 {
		t.Fatal("receipt discarded every complete observed page")
	}
	var envelope supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(calls[1].ResultJSON), &envelope)
	var page map[string]any
	_ = json.Unmarshal([]byte(envelope.Stdout), &page)
	encodedContent, _ := json.Marshal(page["content"])
	if plan.ReceiptTokens > supervisorSegmentReceiptTokenBudget {
		t.Fatal("receipt increased budget")
	}
	if !strings.Contains(plan.ReceiptContent, `"content_omitted":true`) {
		t.Fatal("other omitted pages are not explicit")
	}

	// Test repeated-page dedup independently of complete-file prioritization.
	// Every observation has a distinct valid round coordinate, as in the Store;
	// multiple calls cannot share a round's position or the same call ID.
	var repeated []domain.SupervisorToolCall
	for i := 8; i >= 0; i-- {
		repeat := calls[1]
		repeat.Turn += i / domain.MaxSupervisorToolRounds
		repeat.Round = i%domain.MaxSupervisorToolRounds + 1
		repeat.Position = 1
		repeat.AttemptID = fmt.Sprintf("page-repeat-attempt-%d", repeat.Turn)
		repeat.CallID = fmt.Sprintf("page-repeat-call-%d", i)
		if err := repeat.Validate(); err != nil {
			t.Fatal(err)
		}
		repeated = append(repeated, repeat)
	}
	content, err := boundedToolBoundaryContext("next-attempt", repeated)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(content, string(encodedContent)) != 1 {
		t.Fatal("boundary lost or duplicated the observed page")
	}
	if !strings.Contains(content, domain.SupervisorToolResultReference(repeated[0]).SourceID) ||
		!strings.Contains(content, domain.SupervisorToolResultReference(repeated[0]).ExpectedSHA256) {
		t.Fatal("deduplicated page lost its exact latest readback reference")
	}
	message := toolBoundaryEvidenceMessage("dedup-budget", "next-attempt", content)
	if estimateModelRequestTokens(llm.ChatRequest{Messages: []llm.Message{message}})-8 > toolBoundaryContextTokens {
		t.Fatal("deduplicated page increased the boundary budget")
	}
}
