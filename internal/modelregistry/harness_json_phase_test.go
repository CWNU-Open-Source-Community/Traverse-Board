package modelregistry

import (
	"context"
	"errors"
	"testing"

	"cyberagent-workbench/internal/llm"
)

type jsonPhaseQualificationProvider struct {
	qualificationProvider
	transport string
	strategy  string
}

func (p *jsonPhaseQualificationProvider) DescribeModelHarness(model string) llm.ModelHarness {
	profile := p.qualificationProvider.DescribeModelHarness(model)
	profile.TransportProtocol, profile.JSONStrategy = p.transport, p.strategy
	return profile
}

func (p *jsonPhaseQualificationProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	switch request.Metadata["phase"] {
	case "tool_call":
		if len(request.Tools) != 1 || request.JSONMode {
			return nil, errors.New("native tool phase lost its sole tool or forced JSON")
		}
	case "tool_result_and_json":
		wantTools := 1
		if p.transport == llm.HarnessTransportOpenAIResponses && p.strategy == llm.HarnessJSONStrategyNative {
			wantTools = 0
		}
		if len(request.Tools) != wantTools || request.JSONMode != (p.strategy == llm.HarnessJSONStrategyNative) {
			return nil, errors.New("acknowledgement did not exercise the declared JSON strategy")
		}
		calls, results := 0, 0
		for _, message := range request.Messages {
			for _, call := range message.ToolCalls {
				if call.ID == "probe-call" && call.Name == "prayu_harness_echo" {
					calls++
				}
			}
			for _, result := range message.ToolResults {
				if result.ToolCallID == "probe-call" {
					results++
				}
			}
		}
		if calls != 1 || results != 1 {
			return nil, errors.New("acknowledgement lost the real paired tool exchange")
		}
	}
	return p.qualificationProvider.StreamChat(ctx, request)
}

func TestHarnessQualificationVerifiesResponsesNativeJSONAfterToolPhase(t *testing.T) {
	for _, test := range []struct{ name, transport, strategy string }{
		{"Responses native JSON", llm.HarnessTransportOpenAIResponses, llm.HarnessJSONStrategyNative},
		{"Chat Completions unchanged", llm.HarnessTransportOpenAIChatCompletions, llm.HarnessJSONStrategyNative},
		{"prompt JSON unchanged", llm.HarnessTransportAnthropicMessages, llm.HarnessJSONStrategyPrompt},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := registryWithQualificationProvider("a")
			registry.router.RegisterProvider(&jsonPhaseQualificationProvider{
				qualificationProvider: qualificationProvider{binding: "a", responseModel: "model"},
				transport:             test.transport, strategy: test.strategy,
			})
			result, err := registry.QualifyHarness(t.Context(), routeSettings{}, "qualification-test", "model")
			if err != nil || result.Status != HarnessDiagnosticQualified || !result.Harness.RootEligible ||
				!result.Harness.StrictJSONQualified || result.ModelCalls != 2 || result.SyntheticToolCalls != 1 {
				t.Fatalf("native exchange was not independently qualified: result=%+v err=%v", result, err)
			}
		})
	}
}
