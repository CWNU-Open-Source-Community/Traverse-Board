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

func (r *Registry) freezeKimiProbe(provider, endpoint string, models []string, runtime llm.HTTPProviderRuntime) {
	for _, model := range models {
		wireModel := model
		if runtime != nil {
			var err error
			wireModel, err = runtime.MapModel(model)
			if err != nil {
				continue
			}
		}
		if llm.KimiReasoningScope(endpoint, strings.TrimSpace(wireModel)) {
			r.kimiHistoryProbes[llm.ModelRef{Provider: provider, Model: model}] = true
		}
	}
}

// The scoped K3 plan keeps the existing overall timeout and per-call
// bounds. Four logical calls observe two tools, ordinary completion, and a new
// user turn; earlier two-call records cannot qualify this preserved history.
func (r *Registry) probeKimiHarness(ctx context.Context, ref llm.ModelRef, base llm.ModelHarness) (llm.HarnessQualification, int, int, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return llm.HarnessQualification{}, 0, 0, err
	}
	nonces := []string{hex.EncodeToString(random[:16]), hex.EncodeToString(random[16:])}
	tool := llm.ToolSpec{Name: "prayu_harness_echo", Description: "Return the supplied qualification nonce without side effects.",
		Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["nonce"],"properties":{"nonce":{"type":"string"}}}`)}
	request := llm.ChatRequest{Messages: []llm.Message{
		{Role: "system", Content: "Universal Code K3 model Harness qualification. Call only prayu_harness_echo exactly once with nonce " + nonces[0] +
			". After its result, call the same tool exactly once with nonce " + nonces[1] +
			". After that result, return exactly one JSON object with version " + HarnessProbeProtocolVersion + ", status ok, and nonce " + nonces[0] +
			". When the next user asks for follow-up, return the same strict JSON with the second nonce. Do not call further tools. No external work is performed."},
		{Role: "user", Content: "Begin the two-step synthetic qualification."},
	}, Tools: []llm.ToolSpec{tool}, MaxTokens: harnessProbeMaxTokens,
		Metadata: map[string]string{"purpose": "model_harness_qualification", "phase": "kimi_tool_1"}}
	for round := 0; round < 2; round++ {
		response, err := collectHarnessProbeStream(ctx, r.router, ref, request)
		if err != nil {
			return llm.HarnessQualification{}, round + 1, round, err
		}
		if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != tool.Name || !response.Replay.RequiresPrivateAssistantHistory() {
			return llm.HarnessQualification{}, round + 1, round + len(response.ToolCalls), llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "K3 Harness omitted its native tool history", nil)
		}
		var args struct {
			Nonce string `json:"nonce"`
		}
		if decodeExactJSON(response.ToolCalls[0].Arguments, &args) != nil || args.Nonce != nonces[round] {
			return llm.HarnessQualification{}, round + 1, round + 1, llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "K3 Harness returned invalid tool arguments", nil)
		}
		result, _ := json.Marshal(harnessProbeResponse{Version: HarnessProbeProtocolVersion, Status: "tool_result", Nonce: nonces[round]})
		request.Messages = append(request.Messages,
			llm.Message{Role: "assistant", Content: response.Text, ToolCalls: response.ToolCalls, Replay: response.Replay.Clone()},
			llm.Message{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: response.ToolCalls[0].ID, Content: string(result)}}})
		request.Metadata["phase"] = "kimi_tool_2"
	}
	request.Tools = nil
	request.JSONMode = base.JSONStrategy == llm.HarnessJSONStrategyNative
	for phase := 0; phase < 2; phase++ {
		request.Metadata["phase"] = "kimi_completion"
		if phase == 1 {
			request.Metadata["phase"] = "kimi_ordinary_followup"
		}
		response, err := collectHarnessProbeStream(ctx, r.router, ref, request)
		if err != nil {
			return llm.HarnessQualification{}, phase + 3, 2, err
		}
		var final harnessProbeResponse
		if len(response.ToolCalls) != 0 || !response.Replay.RequiresPrivateAssistantHistory() ||
			decodeExactJSON([]byte(response.Text), &final) != nil || final.Version != HarnessProbeProtocolVersion || final.Status != "ok" || final.Nonce != nonces[phase] {
			return llm.HarnessQualification{}, phase + 3, 2 + len(response.ToolCalls), llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider, "K3 Harness omitted its strict native acknowledgement", nil)
		}
		if phase == 0 {
			request.Messages = append(request.Messages, llm.Message{Role: "assistant", Content: response.Text, Replay: response.Replay.Clone()},
				llm.Message{Role: "user", Content: "Follow-up: acknowledge the second nonce " + nonces[1] + " as instructed."})
		}
	}
	now := time.Now().UTC()
	return llm.HarnessQualification{ProtocolVersion: llm.ModelHarnessProtocolVersion, BindingDigest: base.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true, StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now, ExpiresAt: now.Add(HarnessQualificationTTL)}, 4, 2, nil
}
