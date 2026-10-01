package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/projectconfig"
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

func TestSummaryRequestProjectInstructionDeliveryRefusesOverflowWithoutDroppingRulesOrHistory(t *testing.T) {
	root := t.TempDir()
	content := "SUMMARY_REQUIRED_BEGIN\n" + strings.Repeat("r", 12*1024) + "\nSUMMARY_REQUIRED_END"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := projectconfig.DiscoverInstructions(t.Context(), root, ".")
	if err != nil {
		t.Fatal(err)
	}
	turn := domain.SupervisorTurn{Mission: domain.Mission{Goal: "Preserve the original goal and required project constraints"}}
	turn.Run.Config.ProjectInstructions, _ = json.Marshal(legacy)
	turn.Run.Config.ProjectInstructionsFingerprint = legacy.Fingerprint
	input := contextmgr.SummaryGenerationRequest{TaskID: "session", SourceSHA256: strings.Repeat("a", 64),
		Messages: []contextmgr.Message{{Content: "HISTORICAL_SOURCE_BEGIN " + strings.Repeat("h", 2048) + " HISTORICAL_SOURCE_END"}}}
	ref := llm.ModelRef{Provider: "fixture", Model: "summary"}
	window := llm.ContextWindow{ProtocolVersion: llm.ContextWindowProtocolVersion, WindowTokens: 4096,
		SafetyMarginTokens: 128, DefaultOutputTokens: 512, MaxOutputTokens: 512, Source: "summary_required_test"}
	request, audit, err := supervisorSummaryRequestAndAudit(turn, input, ref, window, true, llm.ChatRequest{})
	if err != nil || len(request.Messages) != 2 || audit != nil {
		t.Fatalf("unclassified summary compatibility changed: err=%v messages=%d audit=%+v", err, len(request.Messages), audit)
	}
	pinned, err := projectconfig.ClassifyInstructionSnapshot(legacy, []projectconfig.InstructionSourceDelivery{{
		Path: legacy.Sources[0].Path, ContentSHA256: legacy.Sources[0].ContentSHA256, Requirement: projectconfig.InstructionMandatory,
	}})
	if err != nil {
		t.Fatal(err)
	}
	turn.Run.Config.ProjectInstructions, _ = json.Marshal(pinned)
	turn.Run.Config.ProjectInstructionsFingerprint = pinned.Fingerprint
	request, audit, err = supervisorSummaryRequestAndAudit(turn, input, ref, window, true, llm.ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "generation_input_window") || len(request.Messages) != 0 || audit != nil {
		t.Fatalf("overflow must return no dispatchable request or claimed delivery: err=%v messages=%d audit=%+v", err, len(request.Messages), audit)
	}
	request, audit, err = supervisorSummaryRequestAndAudit(turn, input, ref, llm.DefaultContextWindow(), true, llm.ChatRequest{})
	if err != nil || audit == nil || len(audit.Included) != 1 || audit.Included[0].SourceID != pinned.DeliverySourceID(0) ||
		len(request.Messages) != 3 || !strings.Contains(request.Messages[1].Content, input.Messages[0].Content) {
		t.Fatalf("fitting request lost original history or required-source audit: err=%v audit=%+v", err, audit)
	}
	var envelope projectInstructionDeliveryEnvelope
	if json.Unmarshal([]byte(request.Messages[2].Content), &envelope) != nil || request.Messages[2].Role != "user" ||
		envelope.Content != pinned.Sources[0].Content || envelope.Source.Snapshot != pinned.Fingerprint || envelope.Authority != pinned.Sources[0].Authority {
		t.Fatal("summary request changed the complete mandatory source, pin or workflow-only authority")
	}
}
