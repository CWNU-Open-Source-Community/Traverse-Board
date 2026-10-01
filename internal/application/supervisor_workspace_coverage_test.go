package application

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/session"
)

// Exact sealed calls extracted from the frozen phase3-tools.json. The turn 8
// calls drove requests 013/014; the turn 3 reads provide real older full pages
// for the isolated boundary selection counterexample, not the full 016 lookup.
func coveragePhase3Calls(t *testing.T) []domain.SupervisorToolCall {
	return coverageReadCalls(t, "testdata/workspace-coverage-phase3.json")
}

func coverageReadCalls(t *testing.T, path string) []domain.SupervisorToolCall {
	t.Helper()
	raw, err := os.ReadFile(path)
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

func TestWorkspaceCoveragePhase3Request016ActualBoundaryLookup(t *testing.T) {
	calls := coverageReadCalls(t, "testdata/workspace-coverage-phase3-boundary.json")
	if len(calls) != 18 || calls[0].Turn != 8 || calls[len(calls)-1].Turn != 7 {
		t.Fatal("historical bounded lookup changed")
	}
	content, err := boundedToolBoundaryContext("boundary-phase3-016", calls)
	if err != nil {
		t.Fatal(err)
	}
	pages := coverageReceiptPages(content)
	for _, page := range pages {
		t.Logf("retained %s %v..%v / %v hash=%v", page["path"], page["start_line"], page["end_line"], page["total_lines"], page["content_sha256"])
	}
	coverageAssertWholeFile(t, pages, "styles.css", 266)
	message := toolBoundaryEvidenceMessage("boundary-budget", "boundary-phase3-016", content)
	tokens := estimateModelRequestTokens(llm.ChatRequest{Messages: []llm.Message{message}}) - 8
	t.Logf("actual 016 boundary tokens=%d calls=%d", tokens, len(calls))
	if tokens > 2048 {
		t.Fatal("boundary budget increased")
	}
}

func coverageTestCall(t *testing.T, id, path string, start, end, total, lineBytes int) domain.SupervisorToolCall {
	t.Helper()
	call := coveragePhase3Calls(t)[2]
	page, _, _ := supervisorWorkspaceReadPage(call)
	lines := make([]string, end-start+1)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d-%s", start+i, strings.Repeat("x", lineBytes))
	}
	page["path"], page["start_line"], page["end_line"], page["total_lines"] = path, float64(start), float64(end), float64(total)
	page["content"] = strings.Join(lines, "\n")
	page["content_sha256"] = session.ContentSHA256(path)
	page["total_bytes"], page["truncated"] = float64(total*(lineBytes+16)), start > 1 || end < total
	var envelope supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(call.ResultJSON), &envelope)
	envelope.Metadata = nil
	envelope.Stdout = stringMustMarshal(page)
	call.ResultJSON = stringMustMarshal(envelope)
	call.PayloadJSON = stringMustMarshal(map[string]any{"version": "agent-code-tools.v1", "path": path, "start_line": start, "end_line": end})
	call.CallID, call.Round, call.Position, call.ModelAttempt = id, 1, 1, 1
	return call
}

func coverageTestRound(t *testing.T, round int, calls ...domain.SupervisorToolCall) domain.SupervisorToolRound {
	t.Helper()
	first := calls[0]
	r := domain.SupervisorToolRound{RunID: first.RunID, Turn: first.Turn, AttemptID: first.AttemptID, Round: round, ModelAttempt: 1, CreatedAt: first.CreatedAt, CompletedAt: first.CompletedAt}
	for i, call := range calls {
		call.Round, call.Position, call.ModelAttempt = round, i+1, 1
		r.Calls = append(r.Calls, call)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func coverageMutatePage(t *testing.T, call domain.SupervisorToolCall, mutate func(map[string]any, *supervisorToolResultEnvelope)) domain.SupervisorToolCall {
	t.Helper()
	var e supervisorToolResultEnvelope
	_ = json.Unmarshal([]byte(call.ResultJSON), &e)
	var p map[string]any
	_ = json.Unmarshal([]byte(e.Stdout), &p)
	mutate(p, &e)
	e.Stdout = stringMustMarshal(p)
	call.ResultJSON = stringMustMarshal(e)
	return call
}

func TestWorkspaceCoverageRejectsContradictoryGroups(t *testing.T) {
	head := coverageTestCall(t, "head", "a.txt", 1, 3, 5, 5)
	tail := coverageTestCall(t, "tail", "a.txt", 3, 5, 5, 5)
	tail.Round = 2
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, *supervisorToolResultEnvelope)
	}{
		{"latest different hash", func(p map[string]any, e *supervisorToolResultEnvelope) { p["content_sha256"] = strings.Repeat("f", 64) }},
		{"contradictory overlap body", func(p map[string]any, e *supervisorToolResultEnvelope) {
			p["content"] = strings.Replace(p["content"].(string), "line-3", "wrong-3", 1)
		}},
		{"encoding conflict", func(p map[string]any, e *supervisorToolResultEnvelope) { p["encoding"] = "utf-8-bom" }},
		{"newline conflict", func(p map[string]any, e *supervisorToolResultEnvelope) { p["newline"] = "crlf" }},
		{"total bytes conflict", func(p map[string]any, e *supervisorToolResultEnvelope) { p["total_bytes"] = float64(999) }},
		{"short body huge range", func(p map[string]any, e *supervisorToolResultEnvelope) {
			p["end_line"], p["total_lines"] = float64(1e12), float64(1e12)
		}},
		{"redacted", func(p map[string]any, e *supervisorToolResultEnvelope) { p["redaction_count"] = float64(1) }},
		{"outer truncated", func(p map[string]any, e *supervisorToolResultEnvelope) { e.Truncated = true }},
		{"metadata conflict", func(p map[string]any, e *supervisorToolResultEnvelope) {
			e.Metadata = map[string]string{"total_bytes": "999"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := coverageMutatePage(t, tail, tc.mutate)
			groups, covered := supervisorWorkspaceCoverage([]domain.SupervisorToolCall{head, bad}, nil)
			if len(groups) != 0 || len(covered) != 0 {
				t.Fatal("contradictory or incomplete observations treated as complete coverage")
			}
		})
	}
	t.Run("authority scope conflict", func(t *testing.T) {
		bad := tail
		bad.AuthorityJSON = `{"workspace_id":"different"}`
		groups, _ := supervisorWorkspaceCoverage([]domain.SupervisorToolCall{head, bad}, nil)
		if len(groups) != 0 {
			t.Fatal("authority conflict formed a complete group")
		}
	})
	groups, _ := supervisorWorkspaceCoverage([]domain.SupervisorToolCall{head, tail}, nil)
	if len(groups) != 1 || len(groups[0].needed) != 2 {
		t.Fatal("matching paginated original bodies failed coverage")
	}
}

func TestWorkspaceCoverageNativeCompleteSkipsDuplicateAndReplansAfterEviction(t *testing.T) {
	head := coverageTestCall(t, "head", "a.txt", 1, 3, 5, 5)
	tail := coverageTestCall(t, "tail", "a.txt", 4, 5, 5, 5)
	full := coverageTestCall(t, "full", "a.txt", 1, 5, 5, 5)
	other := coverageTestCall(t, "other", "b.txt", 1, 1, 1, 5)
	rounds := []domain.SupervisorToolRound{coverageTestRound(t, 1, head, tail), coverageTestRound(t, 2, full), coverageTestRound(t, 3, other)}
	first, err := supervisorSegmentReceiptPlan(rounds, 1, 2048, head.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverageReceiptPages(first.ReceiptContent)) != 0 {
		t.Fatal("native-complete file bought duplicate receipt body")
	}
	second, err := supervisorSegmentReceiptPlan(rounds, 2, 2048, head.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	coverageAssertWholeFile(t, coveragePlanPages(t, second), "a.txt", 5)
	if len(coverageReceiptPages(second.ReceiptContent)) == 0 {
		t.Fatal("evicted native file stayed incorrectly excluded")
	}
	if !strings.Contains(second.ReceiptContent, full.CallID) {
		t.Fatal("evicted native identity disappeared")
	}
}

func TestWorkspaceCoverageOversizeGroupRollsBackBeforePageFallback(t *testing.T) {
	large := coverageTestCall(t, "large", "a.txt", 1, 2, 5, 8000)
	small := coverageTestCall(t, "small", "a.txt", 3, 4, 5, 5)
	native := coverageTestCall(t, "native", "a.txt", 5, 5, 5, 5)
	rounds := []domain.SupervisorToolRound{coverageTestRound(t, 1, large, small), coverageTestRound(t, 2, native)}
	plan, err := supervisorSegmentReceiptPlan(rounds, 1, 2048, large.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	pages := coverageReceiptPages(plan.ReceiptContent)
	if len(pages) != 1 || pages[0]["start_line"] != float64(3) {
		t.Fatal("oversize complete group did not fully roll back to original per-page fallback")
	}
	if plan.ReceiptTokens > 2048 || !strings.Contains(plan.ReceiptContent, large.CallID) || !strings.Contains(plan.ReceiptContent, small.CallID) {
		t.Fatal("rollback lost mandatory identity or budget")
	}
	for _, line := range strings.Split(plan.ReceiptContent, "\n") {
		var entry supervisorSegmentReceiptRound
		if json.Unmarshal([]byte(line), &entry) == nil && len(entry.Calls) > 0 {
			if !entry.Calls[0].ContentOmitted || !entry.Calls[0].ResultExcerpted {
				t.Fatal("failed group left a false whole-body flag")
			}
		}
	}
}

func TestWorkspaceCoverageShortGapPageBeatsLargePageAndRecentCompetitor(t *testing.T) {
	short := coverageTestCall(t, "short-gap", "a.txt", 101, 200, 1000, 5)
	long := coverageTestCall(t, "long-gap", "a.txt", 101, 1000, 1000, 5)
	competitor := coverageTestCall(t, "recent-competitor", "b.txt", 1, 240, 240, 5)
	head := coverageTestCall(t, "native-head", "a.txt", 1, 100, 1000, 5)
	tail := coverageTestCall(t, "native-tail", "a.txt", 201, 1000, 1000, 5)
	rounds := []domain.SupervisorToolRound{coverageTestRound(t, 1, short, long), coverageTestRound(t, 2, competitor), coverageTestRound(t, 3, head, tail)}
	plan, err := supervisorSegmentReceiptPlan(rounds, 2, 2048, short.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	coverageAssertWholeFile(t, coveragePlanPages(t, plan), "a.txt", 1000)
	retainedShort := false
	for _, page := range coverageReceiptPages(plan.ReceiptContent) {
		if page["path"] == "a.txt" {
			if page["end_line"] != float64(200) {
				t.Fatal("selected huge page instead of fitting native gap")
			}
			retainedShort = true
		}
	}
	if !retainedShort {
		t.Fatal("recent unrelated page displaced the fitting complete-file gap")
	}
	t.Logf("native-free gap receipt tokens=%d; short=%d bytes long=%d bytes", plan.ReceiptTokens, len(short.ResultJSON), len(long.ResultJSON))
}

func coveragePhase3Rounds(t *testing.T, count int) []domain.SupervisorToolRound {
	t.Helper()
	rounds := make([]domain.SupervisorToolRound, count)
	for _, call := range coveragePhase3Calls(t) {
		if call.Turn != 8 || call.Round > count {
			continue
		}
		r := &rounds[call.Round-1]
		if len(r.Calls) == 0 {
			*r = domain.SupervisorToolRound{RunID: call.RunID, Turn: call.Turn, AttemptID: call.AttemptID,
				Round: call.Round, ModelAttempt: call.ModelAttempt, CreatedAt: call.CreatedAt}
		}
		r.Calls = append(r.Calls, call)
		r.CompletedAt = call.CompletedAt
	}
	for _, round := range rounds {
		if err := round.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	return rounds
}

func coverageReceiptPages(content string) []map[string]any {
	var pages []map[string]any
	for _, line := range strings.Split(content, "\n") {
		var entry struct {
			Result map[string]any `json:"result"`
			Calls  []struct {
				Result map[string]any `json:"result"`
			} `json:"calls"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if _, ok := entry.Result["content"].(string); ok {
			pages = append(pages, entry.Result)
		}
		for _, call := range entry.Calls {
			if _, ok := call.Result["content"].(string); ok {
				pages = append(pages, call.Result)
			}
		}
	}
	return pages
}

func coveragePlanPages(t *testing.T, plan supervisorSegmentReceipt) []map[string]any {
	t.Helper()
	pages := coverageReceiptPages(plan.ReceiptContent)
	for _, round := range plan.NativeRounds {
		for _, call := range round.Calls {
			if page, _, ok := supervisorWorkspaceReadPage(call); ok {
				pages = append(pages, page)
			}
		}
	}
	return pages
}

func coverageAssertWholeFile(t *testing.T, pages []map[string]any, path string, total int) {
	t.Helper()
	seen := make(map[int]bool)
	var ranges []string
	for _, page := range pages {
		if page["path"] != path {
			continue
		}
		start, end := int(page["start_line"].(float64)), int(page["end_line"].(float64))
		ranges = append(ranges, stringMustMarshal([]int{start, end}))
		for line := start; line <= end; line++ {
			seen[line] = true
		}
	}
	for line := 1; line <= total; line++ {
		if !seen[line] {
			t.Fatalf("%s missing observed line %d; retained original ranges: %v", path, line, ranges)
		}
	}
}

func stringMustMarshal(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestWorkspaceCoveragePhase3Request013To014(t *testing.T) {
	rounds := coveragePhase3Rounds(t, 3)
	before, err := supervisorSegmentReceiptPlan(rounds[:2], 1, supervisorSegmentReceiptTokenBudget, rounds[0].AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	coverageAssertWholeFile(t, coveragePlanPages(t, before), "styles.css", 266)
	plan, err := supervisorSegmentReceiptPlan(rounds, 2, supervisorSegmentReceiptTokenBudget, rounds[0].AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("013 receipt tokens=%d, 014 receipt tokens=%d", before.ReceiptTokens, plan.ReceiptTokens)
	coverageAssertWholeFile(t, coveragePlanPages(t, plan), "app.js", 264)
	if plan.ReceiptTokens > 2048 {
		t.Fatal("receipt exceeded unchanged budget")
	}
	if !reflect.DeepEqual(plan.NativeRounds, rounds[2:]) {
		t.Fatal("native ledger changed")
	}
	request, err := supervisorRequestWithSegmentReceipt(llm.ChatRequest{}, plan, "coverage-session", rounds[0].AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Messages[1].ToolCalls[0].Arguments, json.RawMessage(rounds[2].Calls[0].PayloadJSON)) ||
		request.Messages[2].ToolResults[0].Content != rounds[2].Calls[0].ResultJSON {
		t.Fatal("original native call/result pairing or bytes changed")
	}
	entries, err := supervisorSegmentReceiptEntries(rounds[:2])
	if err != nil {
		t.Fatal(err)
	}
	for _, round := range entries {
		for _, call := range round.Calls {
			if !strings.Contains(plan.ReceiptContent, call.CallID) ||
				!strings.Contains(plan.ReceiptContent, call.ResultSHA256) ||
				!strings.Contains(plan.ReceiptContent, call.OriginalResult.SourceID) {
				t.Fatalf("lost exact status/identity/receipt for %s", call.CallID)
			}
		}
	}
	for _, retained := range coverageReceiptPages(plan.ReceiptContent) {
		found := false
		for _, round := range rounds[:2] {
			for _, call := range round.Calls {
				original, _, _ := supervisorWorkspaceReadPage(call)
				if reflect.DeepEqual(original, retained) {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("receipt body was sliced, joined or rewritten")
		}
	}
}

func TestWorkspaceCoverageBoundaryOriginalFullPageBeforeRepeatedSubpage(t *testing.T) {
	var latest, full domain.SupervisorToolCall
	for _, call := range coveragePhase3Calls(t) {
		page, _, _ := supervisorWorkspaceReadPage(call)
		if page["path"] == "styles.css" {
			if call.Turn == 8 && call.Round == 4 {
				latest = call
			}
			if call.Turn == 3 {
				full = call
			}
		}
	}
	content, err := boundedToolBoundaryContext("boundary-coverage", []domain.SupervisorToolCall{latest, full})
	if err != nil {
		t.Fatal(err)
	}
	coverageAssertWholeFile(t, coverageReceiptPages(content), "styles.css", 266)
	if !strings.Contains(content, full.CallID) || !strings.Contains(content, session.ContentSHA256(full.ResultJSON)) {
		t.Fatal("old full page lost its exact identity")
	}
	message := toolBoundaryEvidenceMessage("boundary-budget", "boundary-coverage", content)
	if estimateModelRequestTokens(llm.ChatRequest{Messages: []llm.Message{message}})-8 > 2048 {
		t.Fatal("boundary exceeded budget")
	}
}
