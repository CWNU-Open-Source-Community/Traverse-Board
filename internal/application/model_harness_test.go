package application

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/llm"
)

func TestPrepareReadOnlyFanoutShardRequestsWritesBackProtocolPolicy(t *testing.T) {
	provider, err := llm.NewAnthropicCompatibleProvider(llm.AnthropicCompatibleConfig{
		Name: "test", BaseURL: "https://example.invalid/anthropic",
		APIKey: "test-secret", DefaultModel: "model-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: "test", Model: "model-a"})
	router.RegisterProvider(provider)
	ref := llm.ModelRef{Provider: "test", Model: "model-a"}
	base, err := router.HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := router.SetHarnessQualification(ref, llm.HarnessQualification{
		ProtocolVersion:    llm.ModelHarnessProtocolVersion,
		BindingDigest:      base.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true,
		StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	requests := map[int]*readOnlyFanoutShardRequest{
		1: {Request: llm.ChatRequest{
			Tools:    []llm.ToolSpec{{Name: "not-for-fanout"}},
			JSONMode: true,
		}},
		2: {Request: llm.ChatRequest{
			Tools:    []llm.ToolSpec{{Name: "also-not-for-fanout"}},
			JSONMode: true,
		}},
	}
	if err := prepareReadOnlyFanoutShardRequests(router, ref, requests); err != nil {
		t.Fatal(err)
	}
	for index, request := range requests {
		if len(request.Request.Tools) != 0 || request.Request.JSONMode ||
			request.Request.Metadata["harness_workload"] != string(llm.HarnessWorkloadFanout) ||
			request.Request.Metadata["harness_json_strategy"] != llm.HarnessJSONStrategyPrompt {
			t.Fatalf("shard %d did not receive the prepared Harness request: %#v",
				index, request.Request)
		}
	}
}
