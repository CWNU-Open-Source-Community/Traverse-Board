package application

import (
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func actualBoundaryCalls(t *testing.T) []domain.SupervisorToolCall {
	t.Helper()
	raw, err := os.ReadFile("testdata/boundary_pending_apply.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls []domain.SupervisorToolCall
	if err = json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if err = c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	return calls
}

func boundaryCompleteContracts(t *testing.T, content string, calls []domain.SupervisorToolCall) int {
	t.Helper()
	entries := boundaryBudgetEntries(t, content)
	n := 0
	for _, c := range calls {
		if c.ToolName != "workspace_change" {
			continue
		}
		var env supervisorToolResultEnvelope
		_ = json.Unmarshal([]byte(c.ResultJSON), &env)
		var raw map[string]any
		_ = json.Unmarshal([]byte(env.Stdout), &raw)
		entry := entries[c.CallID]
		if entry == nil {
			continue
		}
		result, _ := entry["result"].(map[string]any)
		a, _ := json.Marshal(result["apply_arguments"])
		b, _ := json.Marshal(raw["apply_arguments"])
		if string(a) == string(b) {
			n++
		}
	}
	return n
}

func TestActualBoundaryPendingApplyCapacity(t *testing.T) {
	calls := actualBoundaryCalls(t)
	content, err := boundedToolBoundaryContext("boundary-actual", calls)
	if err != nil {
		t.Fatal(err)
	}
	tokens := estimateModelRequestTokens(llm.ChatRequest{Messages: []llm.Message{toolBoundaryEvidenceMessage("actual-session", "boundary-actual", content)}}) - 8
	n := boundaryCompleteContracts(t, content, calls)
	t.Logf("actual boundary: %d/3 complete contracts, %d wrapped tokens", n, tokens)
	if n != 0 {
		t.Fatalf("fixed-2048 counterfactual changed: %d/3", n)
	}
	plan, err := minimalSupervisorBoundaryReceipt(calls, "actual-session", "boundary-actual")
	if err != nil {
		t.Fatal(err)
	}
	request := llm.ChatRequest{MaxTokens: 4096, Messages: []llm.Message{{Role: "system", Content: strings.Repeat("policy ", 500)}, toolBoundaryEvidenceMessage(plan.SessionID, plan.AttemptID, plan.Content)}, Tools: []llm.ToolSpec{{Name: "workspace_apply", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	limit, err := supervisorReceiptInputLimit(request, llm.DefaultContextWindow(), domain.Budget{}, domain.SupervisorCheckpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(calls)
	fit, err := fitSupervisorReceiptViews(request, supervisorSegmentReceipt{}, plan, plan.SessionID, plan.AttemptID, limit, nil)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Content string `json:"content"`
	}
	if err = json.Unmarshal([]byte(strings.SplitN(fit.Messages[1].Content, "\n", 2)[1]), &record); err != nil {
		t.Fatal(err)
	}
	if n = boundaryCompleteContracts(t, record.Content, calls); n != 3 {
		t.Fatalf("dynamic boundary lost actual contracts: %d/3", n)
	}
	if !reflect.DeepEqual(retainedBoundaryFileEffects(plan.Content), retainedBoundaryFileEffects(record.Content)) {
		t.Fatal("mandatory effects changed during fit")
	}
	if estimateModelRequestTokens(fit) > limit || fit.MaxTokens != request.MaxTokens || !reflect.DeepEqual(fit.Tools, request.Tools) || len(fit.Messages) != len(request.Messages) {
		t.Fatal("request budget/shape changed")
	}
	if _, ok := fit.Metadata["context_boundary_receipt_content"]; ok {
		t.Fatal("internal content leaked into dispatch metadata")
	}
	after, _ := json.Marshal(calls)
	if string(before) != string(after) {
		t.Fatal("sealed calls modified")
	}
	t.Logf("dynamic boundary: %d/3 exact contracts, %s receipt tokens, %s allocated capacity, request=%d inputLimit=%d", n, fit.Metadata["context_boundary_receipt_tokens"], fit.Metadata["context_boundary_receipt_budget"], estimateModelRequestTokens(fit), limit)
}

func TestBoundaryPriorityUsesExactIdentityAndSettledCurrentEffects(t *testing.T) {
	calls := actualBoundaryCalls(t)
	old, other := calls[0], calls[1]
	other.Turn = 2
	other.AttemptID = "another-completed-attempt"
	other.CallID = old.CallID
	failed := old
	failed.Turn = 3
	failed.AttemptID = "current-attempt"
	failed.Round = 1
	failed.Status = domain.SupervisorToolFailed
	failed.ErrorCode = "CONFLICT"
	var env supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(old.ResultJSON), &env)
	var result map[string]any
	_ = json.Unmarshal([]byte(env.Stdout), &result)
	failed.PayloadJSON = stringMustMarshal(map[string]any{"path": result["path"]})
	failed.ResultJSON = stringMustMarshal(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: old.ToolName, Status: "failed", Code: "CONFLICT"})
	plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{old, other}, "exact-identity-session", "current-attempt")
	if err != nil {
		t.Fatal(err)
	}
	content, err := supervisorBoundaryReceiptContent(plan, 20000, []domain.SupervisorToolCall{failed}, false)
	if err != nil {
		t.Fatal(err)
	}
	var retained = map[string]map[string]any{}
	for _, line := range strings.Split(content, "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil {
			ref, _ := entry["original_result"].(map[string]any)
			retained[ref["source_id"].(string)] = entry
		}
	}
	if retained[supervisorReceiptCallKey(old)]["result"] != nil || retained[supervisorReceiptCallKey(other)]["result"] == nil {
		t.Fatal("same provider call ID contaminated another exact receipt")
	}
	// An old failed attempt's round4 preceded this turn's completed round1.
	old.Round = 1
	failed.Turn = old.Turn
	failed.AttemptID = "old-failed-attempt"
	failed.Round = 4
	plan, err = minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{old}, "s", "a")
	if err != nil {
		t.Fatal(err)
	}
	content, err = supervisorBoundaryReceiptContent(plan, 20000, []domain.SupervisorToolCall{failed}, true)
	if err != nil || boundaryCompleteContracts(t, content, []domain.SupervisorToolCall{old}) != 1 {
		t.Fatal("older failed attempt shadowed completed boundary", err)
	}
	// Position ASC from SQL is not chronological newest-first within a round.
	newer := old
	newer.CallID = "later-in-same-batch"
	newer.Position = old.Position + 1
	if newer.Position > 4 {
		old.Position = 1
		newer.Position = 2
	}
	env.Metadata = nil
	result["review_required"] = true
	result["apply_authorized"] = false
	result["status"] = "proposed"
	env.Stdout = stringMustMarshal(result)
	newer.ResultJSON = stringMustMarshal(env)
	plan, err = minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{old, newer}, "s", "a")
	if err != nil {
		t.Fatal(err)
	}
	content, err = supervisorBoundaryReceiptContent(plan, 20000, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if boundaryBudgetEntries(t, content)[old.CallID]["result"] != nil {
		t.Fatal("SQL position order resurrected superseded old contract")
	}
	// A failed later turn may already have written an earlier proposal. Its
	// receipt must still block that earlier contract after the retry only reads.
	old.Turn = 1
	old.Round = 1
	var read domain.SupervisorToolCall
	for _, c := range calls {
		if c.ToolName == "workspace_read" {
			read = c
			break
		}
	}
	read.Turn = 2
	read.AttemptID = "completed-retry-attempt"
	applied := old
	applied.Turn = 2
	applied.AttemptID = "failed-earlier-attempt"
	applied.ToolName = "workspace_apply"
	applied.CallID = "already-written"
	applied.Round = 4
	var original supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(old.ResultJSON), &original)
	var proposal map[string]any
	_ = json.Unmarshal([]byte(original.Stdout), &proposal)
	applied.ResultJSON = stringMustMarshal(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: "workspace_apply", Status: "completed", Stdout: stringMustMarshal(map[string]any{"version": "agent-code-tools.v1", "edit_id": proposal["edit_id"], "path": proposal["path"], "status": "applied", "file_written": true, "replayed": false, "proposed_sha256": proposal["proposed_sha256"]})})
	if effect := domain.ObservedSupervisorToolEffect(applied); effect == nil || effect.Effect != "recorded_applied" {
		t.Fatal("fixture did not record a real apply observation")
	}
	plan, err = minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{old, read}, "s", "a")
	if err != nil {
		t.Fatal(err)
	}
	content, err = supervisorBoundaryReceiptContent(plan, 20000, []domain.SupervisorToolCall{applied}, false)
	if err != nil || boundaryBudgetEntries(t, content)[old.CallID]["result"] != nil {
		t.Fatal("a write in a failed attempt was forgotten after its read-only retry", err)
	}
}

func TestCombinedReceiptsReserveContractsBeforeLargeFileBodies(t *testing.T) {
	calls := actualBoundaryCalls(t)
	boundary, err := minimalSupervisorBoundaryReceipt(calls, "combined-session", "combined-attempt")
	if err != nil {
		t.Fatal(err)
	}
	var read domain.SupervisorToolCall
	for _, c := range calls {
		if c.ToolName == "workspace_read" {
			read = c
			break
		}
	}
	read.Turn = 2
	read.AttemptID = boundary.AttemptID
	read.Round = 1
	read.Position = 1
	read.CallID = "huge-current-read"
	var env supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(read.ResultJSON), &env)
	var page map[string]any
	_ = json.Unmarshal([]byte(env.Stdout), &page)
	page["content"] = strings.Repeat("r", 24000)
	page["start_line"] = 1
	page["end_line"] = 1
	page["total_lines"] = 1
	page["total_bytes"] = 24000
	env.Stdout = stringMustMarshal(page)
	read.ResultJSON = stringMustMarshal(env)
	round := domain.SupervisorToolRound{RunID: read.RunID, Turn: read.Turn, AttemptID: read.AttemptID, Round: 1, ModelAttempt: read.ModelAttempt, CreatedAt: read.CreatedAt, CompletedAt: read.CompletedAt, Calls: []domain.SupervisorToolCall{read}}
	segment, err := minimalSupervisorSegmentReceiptPlan([]domain.SupervisorToolRound{round}, 1, boundary.SessionID, boundary.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	base := llm.ChatRequest{MaxTokens: 4096, Messages: []llm.Message{{Role: "system", Content: "prepared mandatory policy"}, toolBoundaryEvidenceMessage(boundary.SessionID, boundary.AttemptID, boundary.Content)}}
	request, err := supervisorRequestWithSegmentReceipt(base, segment, boundary.SessionID, boundary.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := supervisorBoundaryReceiptContent(boundary, 20000, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	limit := estimateModelRequestTokens(request) + supervisorBoundaryReceiptTokens(boundary, contracts) - supervisorBoundaryReceiptTokens(boundary, boundary.Content) + 180
	fit, err := fitSupervisorReceiptViews(request, segment, boundary, boundary.SessionID, boundary.AttemptID, limit, []domain.SupervisorToolCall{read})
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal([]byte(strings.SplitN(fit.Messages[1].Content, "\n", 2)[1]), &record)
	if boundaryCompleteContracts(t, record.Content, calls) != 3 || strings.Contains(fit.Messages[2].Content, strings.Repeat("r", 1000)) || estimateModelRequestTokens(fit) > limit {
		t.Fatal("current optional page crowded out boundary contracts")
	}
	if !reflect.DeepEqual(retainedBoundaryFileEffects(boundary.Content), retainedBoundaryFileEffects(record.Content)) {
		t.Fatal("optional allocation changed file-effect dedup membership")
	}
}

func TestDynamicBoundaryFailsClosedOnIdentityOverflowAndOwnedMessageConflict(t *testing.T) {
	calls := actualBoundaryCalls(t)
	plan, err := minimalSupervisorBoundaryReceipt(calls, "tight-boundary", "tight-attempt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = supervisorBoundaryReceiptContent(plan, 2048, nil, false); err == nil {
		t.Fatal("mandatory identities silently discarded under insufficient capacity")
	}
	request := llm.ChatRequest{Messages: []llm.Message{toolBoundaryEvidenceMessage(plan.SessionID, plan.AttemptID, plan.Content)}}
	request.Messages = append(request.Messages, request.Messages[0])
	if _, err = fitSupervisorReceiptViews(request, supervisorSegmentReceipt{}, plan, plan.SessionID, plan.AttemptID, 20000, nil); err == nil {
		t.Fatal("duplicate owned evidence was accepted")
	}
	conflicting := calls[0]
	conflicting.AttemptID = "another-attempt-same-turn"
	if _, err = minimalSupervisorBoundaryReceipt(append(calls, conflicting), "s", "a"); err == nil {
		t.Fatal("ambiguous completed boundary attempts were ordered by ID")
	}
}

func TestDynamicBoundaryPreservesPagesClaimsFailuresAndRecallScope(t *testing.T) {
	calls := actualBoundaryCalls(t)
	makeCall := func(id, tool string, result map[string]any) domain.SupervisorToolCall {
		call := calls[0]
		call.CallID = id
		call.ToolName = tool
		call.AuthorityJSON = "{}"
		if tool == "history_read" || tool == "history_search" {
			call.AuthorityJSON = ""
		}
		call.ResultJSON = stringMustMarshal(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: tool, Status: "completed", Stdout: stringMustMarshal(result)})
		return call
	}
	t.Run("pages keep identities but deduplicate body", func(t *testing.T) {
		var read domain.SupervisorToolCall
		for _, c := range calls {
			if c.ToolName == "workspace_read" {
				read = c
				break
			}
		}
		newer := read
		newer.Turn = 2
		newer.AttemptID = "read-later-attempt"
		newer.CallID = "read-later"
		plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{read, newer}, "s", "a")
		if err != nil {
			t.Fatal(err)
		}
		content, err := supervisorBoundaryReceiptContent(plan, 20000, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		page, _, ok := supervisorWorkspaceReadPage(read)
		if !ok {
			t.Fatal("fixture lacks a whole page")
		}
		encoded, _ := json.Marshal(page["content"])
		if len(boundaryBudgetEntries(t, content)) != 2 || strings.Count(content, string(encoded)) != 1 {
			t.Fatal("dedup removed identity or duplicated original body")
		}
		tight, err := supervisorBoundaryReceiptContent(plan, supervisorBoundaryReceiptTokens(plan, plan.Content)+400, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(tight, string(encoded)) {
			t.Fatal("oversized page partly/wholly exceeded its allocation")
		}
		if !strings.Contains(tight, `"content_omitted":true`) || !strings.Contains(tight, `"content_sha256"`) {
			t.Fatal("omitted page lost exact cursor/hash projection")
		}
	})
	t.Run("complete multi-page file remains original", func(t *testing.T) {
		a := coverageTestCall(t, "page-one", "README.md", 1, 2, 4, 10)
		b := coverageTestCall(t, "page-two", "README.md", 3, 4, 4, 10)
		a.Position = 1
		b.Position = 2
		plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{a, b}, "s", "a")
		if err != nil {
			t.Fatal(err)
		}
		content, err := supervisorBoundaryReceiptContent(plan, 20000, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		coverageAssertWholeFile(t, coverageReceiptPages(content), "README.md", 4)
	})
	t.Run("oversize claim is wholly omitted with recall", func(t *testing.T) {
		cite := makeCall("huge-claim", "web_citation", map[string]any{"citation": map[string]any{"citation_id": "exact-cite", "snapshot_id": "exact-snapshot", "claim": strings.Repeat("完整主张", 1500), "partial": true, "instruction_authorized": false}})
		plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{cite}, "s", "a")
		if err != nil {
			t.Fatal(err)
		}
		content, err := supervisorBoundaryReceiptContent(plan, 2048, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		entry := boundaryBudgetEntries(t, content)[cite.CallID]
		result := entry["result"].(map[string]any)["citation"].(map[string]any)
		if result["claim"] != nil || result["claim_omitted"] != true || result["partial"] != true || entry["original_result"] == nil {
			t.Fatal("claim was shortened or lost exact readback")
		}
	})
	t.Run("inner failure remains failed", func(t *testing.T) {
		call := makeCall("job-failed", "command_runtime", map[string]any{"jobs": []any{map[string]any{"id": "job-exact", "state": "failed", "exit_code": 7, "output_cursor": 91, "stderr": "compiler failure"}}})
		plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{call}, "s", "a")
		if err != nil {
			t.Fatal(err)
		}
		content, err := supervisorBoundaryReceiptContent(plan, 2048, nil, false)
		if err != nil || !strings.Contains(content, `"state":"failed"`) || !strings.Contains(content, `"exit_code":7`) {
			t.Fatal("inner failure upgraded/lost", err)
		}
	})
	t.Run("recall wrappers remain excluded", func(t *testing.T) {
		for _, tool := range []string{"history_read", "history_search"} {
			call := makeCall("wrapper", tool, map[string]any{"content": "prior result"})
			plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{call}, "s", "a")
			if err != nil {
				t.Fatal(err)
			}
			content, err := supervisorBoundaryReceiptContent(plan, 2048, nil, false)
			if err != nil || len(boundaryBudgetEntries(t, content)) != 0 || !strings.Contains(content, "Selected 0 of 1") {
				t.Fatal("manufactured unreadable memory of memory", err)
			}
		}
	})
}
