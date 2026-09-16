package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cyberagent-workbench/internal/llm"
)

const qualificationPreamble = "I will call the synthetic tool."

type preambleQualificationProvider struct {
	qualificationProvider
	failure string
}

func (p *preambleQualificationProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	if request.Metadata["phase"] == "tool_result_and_json" {
		foundAssistant := false
		for _, message := range request.Messages {
			if message.Role == "system" && strings.Contains(message.Content, "Do not answer with text") {
				return nil, errors.New("probe system instruction contradicts final JSON request")
			}
			if message.Role == "assistant" && len(message.ToolCalls) == 1 && message.Content == qualificationPreamble {
				foundAssistant = true
			}
		}
		if !foundAssistant {
			return nil, errors.New("second probe lost the actual assistant tool message")
		}
	}
	base, err := p.qualificationProvider.StreamChat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 4)
	if request.Metadata["phase"] == "tool_call" {
		chunks <- llm.ChatChunk{Text: qualificationPreamble}
	}
	for chunk := range base {
		if request.Metadata["phase"] == "tool_call" && chunk.Done {
			switch p.failure {
			case "missing tool":
				chunk.ToolCalls = nil
			case "wrong nonce":
				chunk.ToolCalls[0].Arguments = json.RawMessage(`{"nonce":"wrong"}`)
			case "extra tool":
				extra := chunk.ToolCalls[0]
				extra.ID = "another-call"
				chunk.ToolCalls = append(chunk.ToolCalls, extra)
			}
		}
		if request.Metadata["phase"] == "tool_result_and_json" && chunk.Text != "" && p.failure == "final prose" {
			chunk.Text = "The result is " + chunk.Text
		}
		if request.Metadata["phase"] == "tool_result_and_json" && chunk.Done && p.failure == "second tool" {
			chunk.ToolCalls = []llm.ToolCall{{ID: "another-call", Name: "prayu_harness_echo", Arguments: json.RawMessage(`{"nonce":"wrong"}`)}}
		}
		chunks <- chunk
	}
	close(chunks)
	return chunks, nil
}

func TestHarnessQualificationAllowsToolPreambleWithoutRelaxingExactExchange(t *testing.T) {
	for _, failure := range []string{"", "missing tool", "wrong nonce", "extra tool", "final prose", "second tool"} {
		name := failure
		if name == "" {
			name = "valid tool with preamble"
		}
		t.Run(name, func(t *testing.T) {
			registry := registryWithQualificationProvider("a")
			registry.router.RegisterProvider(&preambleQualificationProvider{
				qualificationProvider: qualificationProvider{binding: "a", responseModel: "model"}, failure: failure,
			})
			settings := routeSettings{}
			result, err := registry.QualifyHarness(t.Context(), settings, "qualification-test", "model")
			if err != nil {
				t.Fatal(err)
			}
			if result.ToolExecuted || result.ResponseContentReturned {
				t.Fatalf("probe exposed content or claimed execution: %+v", result)
			}
			if failure == "" {
				if result.Status != HarnessDiagnosticQualified || !result.Harness.RootEligible || result.ModelCalls != 2 || result.SyntheticToolCalls != 1 {
					t.Fatalf("valid native tool exchange rejected: %+v", result)
				}
			} else if result.Status != HarnessDiagnosticIncompatible || result.Harness.RootEligible || result.Outcome != string(llm.OutcomeInvalidResponse) {
				t.Fatalf("malformed exchange qualified: %+v", result)
			}
			for _, value := range settings {
				if strings.Contains(value, qualificationPreamble) {
					t.Fatal("model preamble entered the qualification record")
				}
			}
		})
	}
}
