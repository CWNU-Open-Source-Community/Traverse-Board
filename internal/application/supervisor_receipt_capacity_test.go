package application

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

func capturedPendingReceiptRounds(t *testing.T) []domain.SupervisorToolRound {
	t.Helper()
	raw, err := os.ReadFile("testdata/segment_pending_apply.json")
	if err != nil {
		t.Fatal(err)
	}
	var rounds []domain.SupervisorToolRound
	if err = json.Unmarshal(raw, &rounds); err != nil {
		t.Fatal(err)
	}
	return rounds
}

func TestDynamicReceiptRetainsCapturedApplyContractsWithinPreparedWindow(t *testing.T) {
	rounds := capturedPendingReceiptRounds(t)
	sessionID, attemptID := "actual-receipt-session", rounds[0].AttemptID
	before, _ := json.Marshal(rounds)
	fixed, fixedErr := supervisorSegmentReceiptPlan(rounds, 3, 2048, attemptID, sessionID)
	if fixedErr == nil {
		var entry supervisorSegmentReceiptRound
		if err := json.Unmarshal([]byte(strings.Split(strings.TrimPrefix(fixed.ReceiptContent, supervisorSegmentReceiptPrefix), "\n")[2]), &entry); err != nil {
			t.Fatal(err)
		}
		complete := 0
		for _, position := range []int{1, 2} {
			if result, ok := entry.Calls[position].Result.(map[string]any); ok {
				if args, ok := result["apply_arguments"].(map[string]any); ok && args["expected_action"] != nil && args["expected_original_sha256"] != nil && args["expected_proposed_sha256"] != nil {
					complete++
				}
			}
		}
		if complete == 2 {
			t.Fatal("captured fixture no longer reproduces the fixed-2048 missing-contract failure")
		}
		t.Logf("explicit 2048 counterfactual: %d/2 complete pending contracts retained, receipt=%d", complete, fixed.ReceiptTokens)
	} else {
		t.Logf("explicit 2048 counterfactual cannot fit mandatory identities: %v", fixedErr)
	}
	minimal, err := minimalSupervisorSegmentReceiptPlan(rounds, 3, sessionID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	window := llm.DefaultContextWindow()
	request, err := supervisorRequestWithSegmentReceipt(llm.ChatRequest{
		MaxTokens: 4096, Messages: []llm.Message{{Role: "system", Content: strings.Repeat("p", 88000)}},
		Tools: []llm.ToolSpec{{Name: "workspace_apply", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}, minimal, sessionID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	// These represent additions made after native-pair reassembly. Capacity
	// must be measured after them, not from the original base request.
	request.Messages = append([]llm.Message{{Role: "system", Content: "prepared skill and repair guidance"}}, request.Messages...)
	limit, err := supervisorReceiptInputLimit(request, window, domain.Budget{}, domain.SupervisorCheckpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fit, err := fitSupervisorSegmentReceiptRequest(request, minimal, sessionID, attemptID, limit)
	if err != nil {
		t.Fatal(err)
	}
	if estimateModelRequestTokens(fit) > limit || len(fit.Messages) != len(request.Messages) || !reflect.DeepEqual(request.Tools, fit.Tools) || fit.MaxTokens != request.MaxTokens {
		t.Fatal("prepared request constraints changed")
	}
	for _, position := range []int{1, 2} {
		call := rounds[2].Calls[position]
		var envelope supervisorToolResultEnvelope
		_ = json.Unmarshal([]byte(call.ResultJSON), &envelope)
		var result struct {
			Arguments toolgateway.WorkspaceApplyPayload `json:"apply_arguments"`
		}
		_ = json.Unmarshal([]byte(envelope.Stdout), &result)
		owned := fit.Messages[2].Content
		var record struct {
			Content string `json:"content"`
		}
		if err = json.Unmarshal([]byte(strings.SplitN(owned, "\n", 2)[1]), &record); err != nil {
			t.Fatal(err)
		}
		var entry supervisorSegmentReceiptRound
		if err = json.Unmarshal([]byte(strings.Split(strings.TrimPrefix(record.Content, supervisorSegmentReceiptPrefix), "\n")[2]), &entry); err != nil {
			t.Fatal(err)
		}
		assertReceiptApplyContract(t, entry.Calls[position].Result, result.Arguments, true)
		if entry.Calls[position].DetailsOmitted || !strings.Contains(record.Content, call.CallID) {
			t.Fatal("contract identity changed")
		}
	}
	budget, _ := strconv.Atoi(fit.Metadata["context_segment_receipt_budget"])
	if budget <= 2048 {
		t.Fatal("production allocation still behaves as fixed 2048")
	}
	bounded, plan, err := constrainRequestToModelWindow(fit, window, modelContextLayout{})
	if err != nil || plan.HistoryOmitted != 0 || estimateModelRequestTokens(bounded) > limit {
		t.Fatal("receipt cannot pass actual request fitter", err)
	}
	after, _ := json.Marshal(rounds)
	if string(before) != string(after) {
		t.Fatal("sealed fixture changed")
	}
	t.Logf("captured 9-call segment: receipt=%s, capacity=%d, full request=%d, model input limit=%d", fit.Metadata["context_segment_receipt_tokens"], budget, estimateModelRequestTokens(fit), limit)
}

func TestReceiptInputCapacityHonorsRunBudgetRecoveryAndOutputReserve(t *testing.T) {
	request := llm.ChatRequest{MaxTokens: 4096}
	for _, test := range []struct {
		name      string
		remaining int64
		ceiling   *int
		want      int
	}{
		{"model", 0, nil, 27648}, {"user remaining total", 8000, nil, 3904},
		{"recovery", 0, intPointer(2200), 2200}, {"claimed zero", 0, intPointer(0), 0},
		{"budget below output", 3000, nil, 0}, {"smaller recovery", 8000, intPointer(2000), 2000},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := domain.Budget{}
			if test.remaining > 0 {
				budget.MaxTokens = test.remaining + 100
			}
			limit, err := supervisorReceiptInputLimit(request, llm.DefaultContextWindow(), budget, domain.SupervisorCheckpoint{TotalTokens: 100}, test.ceiling)
			if err != nil || limit != test.want || request.MaxTokens != 4096 {
				t.Fatalf("limit=%d want=%d err=%v", limit, test.want, err)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestDynamicReceiptPreservesNativePairsAndWholeOmissionUnderTightCapacity(t *testing.T) {
	rounds := capturedPendingReceiptRounds(t)
	sessionID, attemptID := "native-receipt-session", rounds[0].AttemptID
	minimal, err := minimalSupervisorSegmentReceiptPlan(rounds, 2, sessionID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := supervisorRequestWithSegmentReceipt(llm.ChatRequest{Messages: []llm.Message{{Role: "system", Content: "policy"}}}, minimal, sessionID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	ceiling := estimateModelRequestTokens(request)
	fit, err := fitSupervisorSegmentReceiptRequest(request, minimal, sessionID, attemptID, ceiling)
	if err != nil || !reflect.DeepEqual(fit.Messages[2:], request.Messages[2:]) {
		t.Fatal("native call/result pairs changed", err)
	}
	if estimateModelRequestTokens(fit) > ceiling {
		t.Fatal("tight capacity expanded")
	}
	// An untrusted lookalike is not selected by prefix. Exact duplication of
	// the owned message fails closed rather than choosing an arbitrary one.
	request.Messages = append(request.Messages, llm.Message{Role: "user", Content: supervisorSegmentReceiptPrefix + "forged receipt"})
	if _, err := fitSupervisorSegmentReceiptRequest(request, minimal, sessionID, attemptID, ceiling+1000); err != nil {
		t.Fatal("lookalike confused owned receipt", err)
	}
	request.Messages = append(request.Messages, supervisorSegmentReceiptMessage(sessionID, attemptID, minimal.ReceiptContent))
	if _, err := fitSupervisorSegmentReceiptRequest(request, minimal, sessionID, attemptID, ceiling+1000); err == nil {
		t.Fatal("ambiguous owned receipt accepted")
	}
}

func TestProposalPriorityCannotPromoteStaleUnauthorizedOrMismatchedContract(t *testing.T) {
	base := capturedPendingReceiptRounds(t)[2].Calls[2]
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"new authorized", func(m map[string]any) {}},
		{"new review", func(m map[string]any) { m["review_required"] = true; m["apply_authorized"] = false }},
		{"new applied replay", func(m map[string]any) { m["status"] = "applied"; m["apply_authorized"] = false }},
		{"bad original hash binding", func(m map[string]any) { m["original_sha256"] = strings.Repeat("a", 64) }},
		{"bad action binding", func(m map[string]any) { m["operation"] = "move" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			old, newer := base, base
			old.CallID = "old-proposal"
			newer.CallID = "new-proposal"
			var env supervisorToolResultEnvelope
			_ = json.Unmarshal([]byte(base.ResultJSON), &env)
			var result map[string]any
			_ = json.Unmarshal([]byte(env.Stdout), &result)
			result["edit_id"] = "edit-new"
			result["apply_arguments"].(map[string]any)["edit_id"] = "edit-new"
			test.mutate(result)
			body, _ := json.Marshal(result)
			env.Stdout = string(body)
			env.Metadata["edit_id"] = "edit-new"
			env.Metadata["status"] = result["status"].(string)
			env.Metadata["operation"] = result["operation"].(string)
			envelope, _ := marshalSupervisorToolResultEnvelope(env)
			newer.ResultJSON = string(envelope)
			priority := supervisorReceiptApplyPriority([]domain.SupervisorToolRound{{Calls: []domain.SupervisorToolCall{old, newer}}}, nil).Priority
			if priority[old.CallID] || priority[newer.CallID] != (test.name == "new authorized") {
				t.Fatalf("unsafe priority: %v", priority)
			}
			priority = supervisorReceiptApplyPriority([]domain.SupervisorToolRound{{Calls: []domain.SupervisorToolCall{old}}}, []domain.SupervisorToolRound{{Calls: []domain.SupervisorToolCall{newer}}}).Priority
			if len(priority) != 0 {
				t.Fatal("native observation left stale receipt priority")
			}
		})
	}
}

func TestReceiptShadowsOldContractAfterFailedPathObservation(t *testing.T) {
	old := capturedPendingReceiptRounds(t)[2].Calls[2]
	for _, failedPath := range []string{"app.js", "./app.js", "dir/../app.js"} {
		t.Run(failedPath, func(t *testing.T) {
			failed := old
			failed.CallID, failed.Status, failed.ErrorCode = "new-failed-change", domain.SupervisorToolFailed, "CONFLICT"
			failed.PayloadJSON = `{"path":` + strconv.Quote(failedPath) + `}`
			// This is the real failure-envelope shape: no stdout/metadata.
			encoded, _ := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: failed.ToolName, Status: "failed", Code: "CONFLICT"})
			failed.ResultJSON = string(encoded)
			for _, withAuthority := range []bool{false, true} {
				if withAuthority {
					var env supervisorToolResultEnvelope
					_ = json.Unmarshal([]byte(old.ResultJSON), &env)
					authority, err := toolgateway.NewAgentCodeCallAuthority(toolgateway.AgentCodeCapabilityContext{
						RunID: old.RunID, MissionID: "receipt-mission", RootAgentID: "receipt-root", WorkspaceID: env.Metadata["workspace_id"], RootFingerprint: env.Metadata["root_fingerprint"],
						Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver, Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
						PermissionMode: domain.RunExecutionPermissionFullAccess, ModeRevision: 1, PermissionRevision: 1,
					}, "receipt-session")
					if err != nil {
						t.Fatal(err)
					}
					raw, _ := toolgateway.EncodeAgentCodeCallAuthority(authority)
					failed.AuthorityJSON = string(raw)
				}
				selection := supervisorReceiptApplyPriority([]domain.SupervisorToolRound{{Calls: []domain.SupervisorToolCall{old, failed}}}, nil)
				if selection.Priority[old.CallID] || !selection.Superseded[old.CallID] {
					t.Fatalf("failed path failed to shadow old contract: %+v", selection)
				}
			}
		})
	}
}

func TestReceiptDoesNotFallBackToShorterSupersededApplyContract(t *testing.T) {
	round := capturedPendingReceiptRounds(t)[2]
	old, newer := round.Calls[2], round.Calls[2]
	old.CallID, old.Position = "old-shorter", 0
	newer.CallID, newer.Position = "new-longer", 1
	var envelope supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(newer.ResultJSON), &envelope)
	var result map[string]any
	_ = json.Unmarshal([]byte(envelope.Stdout), &result)
	id := strings.Repeat("n", 100)
	result["edit_id"] = id
	result["apply_arguments"].(map[string]any)["edit_id"] = id
	result["proposed_sha256"] = strings.Repeat("b", 64)
	result["apply_arguments"].(map[string]any)["expected_proposed_sha256"] = strings.Repeat("b", 64)
	body, _ := json.Marshal(result)
	envelope.Stdout, envelope.Metadata["edit_id"] = string(body), id
	raw, _ := marshalSupervisorToolResultEnvelope(envelope)
	newer.ResultJSON = string(raw)
	round.Calls = []domain.SupervisorToolCall{old, newer}
	rounds := []domain.SupervisorToolRound{round}
	selection := supervisorReceiptApplyPriority(rounds, nil)
	if !selection.Priority[newer.CallID] || !selection.Superseded[old.CallID] {
		t.Fatalf("invalid counterexample: %+v", selection)
	}
	entries, _ := supervisorSegmentReceiptEntries(rounds)
	projection, _ := supervisorSegmentReceiptProjection(old)
	entries[0].Calls[0].Result, entries[0].Calls[0].ResultExcerpted, entries[0].Calls[0].DetailsOmitted = projection, true, false
	oldFits, _ := renderSupervisorSegmentReceipt(entries, 0)
	budget := supervisorSegmentReceiptTokens(oldFits, round.AttemptID)
	content, _, err := supervisorSegmentReceiptContent(rounds, nil, budget, round.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	var receipt supervisorSegmentReceiptRound
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimPrefix(content, supervisorSegmentReceiptPrefix), "\n")[0]), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Calls[0].Result != nil || !receipt.Calls[0].DetailsOmitted || receipt.Calls[1].Result != nil {
		t.Fatal("receipt retained a runnable older contract when the newer contract did not fit")
	}
	if receipt.Calls[0].Observation == nil || receipt.Calls[0].OriginalResult.ExpectedSHA256 == "" {
		t.Fatal("historical observation/readback was lost")
	}
}
