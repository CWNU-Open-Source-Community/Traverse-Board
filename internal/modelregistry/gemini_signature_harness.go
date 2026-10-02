package modelregistry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"cyberagent-workbench/internal/llm"
)

// This private plan is frozen while constructing a Registry. Qualification
// does not infer a provider protocol from public names or mutable environment.
func (r *Registry) freezeGeminiProbe(provider, endpoint string, models []string, runtime llm.HTTPProviderRuntime) {
	for _, model := range models {
		wireModel := model
		if runtime != nil {
			var err error
			wireModel, err = runtime.MapModel(model)
			if err != nil {
				continue
			}
		}
		if llm.GeminiThoughtSignatureScope(endpoint, strings.TrimSpace(wireModel)) {
			r.geminiSequentialProbes[llm.ModelRef{Provider: provider, Model: model}] = true
		}
	}
}

func (r *Registry) probeGeminiHarness(ctx context.Context, ref llm.ModelRef, base llm.ModelHarness) (llm.HarnessQualification, int, int, error) {
	var nonceBytes [32]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return llm.HarnessQualification{}, 0, 0, err
	}
	nonces := []string{hex.EncodeToString(nonceBytes[:16]), hex.EncodeToString(nonceBytes[16:])}
	tool := llm.ToolSpec{Name: "prayu_harness_echo",
		Description: "Return the supplied qualification nonce without side effects.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["nonce"],"properties":{"nonce":{"type":"string"}}}`)}
	request := llm.ChatRequest{Messages: []llm.Message{
		{Role: "system", Content: "Traverse Board Gemini model Harness qualification. Call only prayu_harness_echo exactly once with nonce " + nonces[0] +
			". After its result, call the same tool exactly once with nonce " + nonces[1] +
			". After the second result, return exactly one JSON object with version " + HarnessProbeProtocolVersion + ", status ok, and nonce " + nonces[0] +
			". Do not call further tools. No external work is performed by this probe."},
		{Role: "user", Content: "Begin the two-step synthetic qualification."},
	}, Tools: []llm.ToolSpec{tool}, MaxTokens: harnessProbeMaxTokens,
		Metadata: map[string]string{"purpose": "model_harness_qualification", "phase": "gemini_tool_call_1"}}
	for round := 0; round < 2; round++ {
		response, err := collectHarnessProbeStream(ctx, r.router, ref, request)
		if err != nil {
			return llm.HarnessQualification{}, round + 1, round, err
		}
		if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != tool.Name || response.Replay == nil {
			return llm.HarnessQualification{}, round + 1, round + len(response.ToolCalls),
				llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "Gemini Harness probe omitted a signed sequential tool response", nil)
		}
		var arguments struct {
			Nonce string `json:"nonce"`
		}
		if err := decodeExactJSON(response.ToolCalls[0].Arguments, &arguments); err != nil || arguments.Nonce != nonces[round] {
			return llm.HarnessQualification{}, round + 1, round + 1,
				llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "Gemini Harness probe returned invalid sequential tool arguments", err)
		}
		result, err := json.Marshal(harnessProbeResponse{Version: HarnessProbeProtocolVersion, Status: "tool_result", Nonce: nonces[round]})
		if err != nil {
			return llm.HarnessQualification{}, round + 1, round + 1, err
		}
		request.Messages = append(request.Messages,
			llm.Message{Role: "assistant", Content: response.Text, ToolCalls: response.ToolCalls, Replay: response.Replay.Clone()},
			llm.Message{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: response.ToolCalls[0].ID, Content: string(result)}}})
		request.Metadata["phase"] = "gemini_tool_call_2"
	}
	request.Tools = nil
	request.JSONMode = base.JSONStrategy == llm.HarnessJSONStrategyNative
	request.Metadata["phase"] = "gemini_tool_results_and_json"
	response, err := collectHarnessProbeStream(ctx, r.router, ref, request)
	if err != nil {
		return llm.HarnessQualification{}, 3, 2, err
	}
	if len(response.ToolCalls) != 0 {
		return llm.HarnessQualification{}, 3, 2 + len(response.ToolCalls),
			llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "Gemini Harness probe called a third tool", nil)
	}
	var final harnessProbeResponse
	if err := decodeExactJSON([]byte(response.Text), &final); err != nil || final.Version != HarnessProbeProtocolVersion || final.Status != "ok" || final.Nonce != nonces[0] {
		return llm.HarnessQualification{}, 3, 2,
			llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "Gemini Harness probe omitted its strict JSON acknowledgement", err)
	}
	now := time.Now().UTC()
	return llm.HarnessQualification{ProtocolVersion: llm.ModelHarnessProtocolVersion, BindingDigest: base.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true, StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now, ExpiresAt: now.Add(HarnessQualificationTTL)}, 3, 2, nil
}
