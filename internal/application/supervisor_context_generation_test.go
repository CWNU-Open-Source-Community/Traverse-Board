package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestSummaryRequestPreservesSourcesOrFallsBackBeforeCallingModel(t *testing.T) {
	turn := domain.SupervisorTurn{Mission: domain.Mission{Goal: "原目标：保留接口"}}
	input := contextmgr.SummaryGenerationRequest{TaskID: "session", SourceSHA256: strings.Repeat("a", 64),
		Messages: []contextmgr.Message{{Content: strings.Repeat("旧历史", 20000)}}}
	_, err := supervisorSummaryRequest(turn, input, llm.ModelRef{Provider: "fixture", Model: "summary"}, llm.DefaultContextWindow(), true)
	if err == nil || !strings.Contains(err.Error(), "generation_input_window") {
		t.Fatalf("oversized originals must fall back before any truncated generation request: %v", err)
	}
	input.Messages[0].Content = "修改要求：保留 UTF-8；工具检查失败。"
	request, err := supervisorSummaryRequest(turn, input, llm.ModelRef{Provider: "fixture", Model: "summary"}, llm.DefaultContextWindow(), true)
	if err != nil || len(request.Tools) != 0 || request.Metadata["context_history_omitted"] != "0" ||
		!strings.Contains(request.Messages[1].Content, input.Messages[0].Content) || !strings.Contains(request.Messages[1].Content, turn.Mission.Goal) {
		t.Fatalf("complete source and original goal must survive without tools: request=%+v err=%v", request, err)
	}
	if !strings.Contains(request.Messages[0].Content, "targeting at most 1200 Unicode characters") ||
		!strings.Contains(request.Messages[0].Content, "hard acceptance limit is 1600 Unicode characters") ||
		request.MaxTokens != llm.DefaultContextWindow().OutputLimit(2048) || !request.JSONMode {
		t.Fatalf("summary target must leave character headroom without lowering the JSON token allowance: %+v", request)
	}
}

func TestSummaryRequestIncludesInputInRemainingRunBudget(t *testing.T) {
	turn := domain.SupervisorTurn{Run: domain.Run{Budget: domain.Budget{MaxTokens: 5000}}, Checkpoint: domain.SupervisorCheckpoint{TotalTokens: 4500}}
	_, err := supervisorSummaryRequest(turn, contextmgr.SummaryGenerationRequest{TaskID: "session"},
		llm.ModelRef{Provider: "fixture", Model: "summary"}, llm.DefaultContextWindow(), true)
	if err == nil || !strings.Contains(err.Error(), "generation_token_budget") {
		t.Fatalf("auxiliary call must reserve input and continuation budget: %v", err)
	}
}
