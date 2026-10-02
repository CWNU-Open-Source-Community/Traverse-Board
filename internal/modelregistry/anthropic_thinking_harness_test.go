package modelregistry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/llm"
)

const thinkingHarnessModel = "claude-sonnet-5"

func TestHarnessQualificationReplaysAnthropicThinkingInActualContinuation(t *testing.T) {
	for _, signed := range []bool{true, false} {
		t.Run(fmt.Sprintf("signed=%t", signed), func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			var nonce, system string
			var thinking json.RawMessage
			var expected []json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests++
				var body struct {
					Model     string          `json:"model"`
					MaxTokens int             `json:"max_tokens"`
					Stream    bool            `json:"stream"`
					System    string          `json:"system"`
					Thinking  json.RawMessage `json:"thinking"`
					Tools     []struct {
						Name string `json:"name"`
					} `json:"tools"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != thinkingHarnessModel ||
					body.MaxTokens != harnessProbeMaxTokens || !body.Stream || len(body.Tools) != 1 ||
					body.Tools[0].Name != "prayu_harness_echo" {
					t.Error("qualification changed its provider, tool or attempt budget")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(event any) {
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				start := func(index int, block json.RawMessage) {
					emit(map[string]any{"type": "content_block_start", "index": index, "content_block": block})
				}
				delta := func(index int, kind, field, value string) {
					emit(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]string{"type": kind, field: value}})
				}
				stop := func(index int) { emit(map[string]any{"type": "content_block_stop", "index": index}) }
				emit(map[string]any{"type": "message_start", "message": map[string]any{
					"id": fmt.Sprintf("msg_qualification_%d", requests), "model": thinkingHarnessModel,
					"usage": map[string]int{"input_tokens": 4, "output_tokens": 0}}})
				finish := "tool_use"
				if requests == 1 {
					if len(body.Messages) != 1 || body.Messages[0].Role != "user" ||
						!strings.Contains(body.System, "version "+HarnessProbeProtocolVersion+", status ok, and the same nonce") {
						t.Error("acknowledgement instructions were not in the original turn")
						return
					}
					var prompt string
					if json.Unmarshal(body.Messages[0].Content, &prompt) != nil {
						t.Error("initial user message is not text")
						return
					}
					words := strings.Fields(prompt)
					nonce = strings.TrimSuffix(words[len(words)-1], ".")
					system, thinking = body.System, append(json.RawMessage(nil), body.Thinking...)
					expected = []json.RawMessage{
						json.RawMessage(`{"type":"thinking","thinking":"","signature":"private-qualification signature \n"}`),
						json.RawMessage(`{"type":"redacted_thinking","data":"private-qualification-redacted \n"}`),
						json.RawMessage(`{"type":"text","text":"I will call the tool.","citations":null}`),
						json.RawMessage(`{"type":"tool_use","id":"native-qualification-tool","name":"prayu_harness_echo","input":{"nonce":"` + nonce + `"},"caller":{"type":"direct"},"toolset_name":null}`),
					}
					start(0, json.RawMessage(`{"type":"thinking","thinking":""}`))
					if signed {
						delta(0, "signature_delta", "signature", "private-qualification ")
						delta(0, "signature_delta", "signature", "signature \n")
					}
					stop(0)
					start(1, expected[1])
					stop(1)
					start(2, json.RawMessage(`{"type":"text","text":"","citations":null}`))
					delta(2, "text_delta", "text", "I will call the tool.")
					stop(2)
					start(3, json.RawMessage(`{"type":"tool_use","id":"native-qualification-tool","name":"prayu_harness_echo","input":{},"caller":{"type":"direct"},"toolset_name":null}`))
					delta(3, "input_json_delta", "partial_json", `{"nonce":"`+nonce+`"}`)
					stop(3)
				} else {
					if requests != 2 || len(body.Messages) != 3 || body.Messages[1].Role != "assistant" || body.Messages[2].Role != "user" ||
						body.System != system || !reflect.DeepEqual(body.Thinking, thinking) {
						t.Error("continuation changed the original turn or thinking configuration")
						return
					}
					var actualBlocks, expectedBlocks any
					_ = json.Unmarshal(body.Messages[1].Content, &actualBlocks)
					raw, _ := json.Marshal(expected)
					_ = json.Unmarshal(raw, &expectedBlocks)
					if !reflect.DeepEqual(actualBlocks, expectedBlocks) {
						t.Error("native thinking, signature, redacted data or tool metadata was lost in the actual next request")
					}
					var results []struct {
						Type    string `json:"type"`
						ID      string `json:"tool_use_id"`
						Content string `json:"content"`
					}
					if json.Unmarshal(body.Messages[2].Content, &results) != nil || len(results) != 1 ||
						results[0].Type != "tool_result" || results[0].ID != "native-qualification-tool" {
						t.Error("continuation added new user text or lost native tool-result pairing")
						return
					}
					var toolResult harnessProbeResponse
					if json.Unmarshal([]byte(results[0].Content), &toolResult) != nil || toolResult.Nonce != nonce ||
						toolResult.Version != HarnessProbeProtocolVersion || toolResult.Status != "tool_result" {
						t.Error("qualification result lost its exact nonce exchange")
						return
					}
					final, _ := json.Marshal(harnessProbeResponse{Version: HarnessProbeProtocolVersion, Status: "ok", Nonce: nonce})
					start(0, json.RawMessage(`{"type":"text","text":"","citations":null}`))
					delta(0, "text_delta", "text", string(final))
					stop(0)
					finish = "end_turn"
				}
				emit(map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": finish}, "usage": map[string]int{"output_tokens": 3}})
				emit(map[string]string{"type": "message_stop"})
			}))
			defer server.Close()
			values := map[string]string{"CYBERAGENT_ANTHROPIC_API_KEY": "fixture-secret", "CYBERAGENT_ANTHROPIC_BASE_URL": server.URL,
				"CYBERAGENT_ANTHROPIC_MODEL": thinkingHarnessModel}
			registry := New(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
			settings := routeSettings{}
			result, err := registry.QualifyHarness(t.Context(), settings, "anthropic", thinkingHarnessModel)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actualRequests := requests
			mu.Unlock()
			if signed {
				if actualRequests != 2 || result.Status != HarnessDiagnosticQualified || result.ModelCalls != 2 || result.SyntheticToolCalls != 1 ||
					!result.Harness.RootEligible || !result.Harness.ToolCallsQualified || !result.Harness.ToolResultsQualified ||
					!result.Harness.StreamingQualified || !result.Harness.StrictJSONQualified {
					t.Fatalf("native thinking exchange was not qualified: %+v", result)
				}
				ref := llm.ModelRef{Provider: "anthropic", Model: thinkingHarnessModel}
				for _, workload := range []llm.HarnessWorkload{llm.HarnessWorkloadSpecialist, llm.HarnessWorkloadFanout} {
					prepared, _, err := registry.Router().PrepareHarnessRequest(ref, workload, llm.ChatRequest{Tools: []llm.ToolSpec{{Name: "echo"}}})
					if err != nil || len(prepared.Tools) != 0 {
						t.Fatalf("qualification expanded nonroot tool permissions: %v", err)
					}
				}
			} else if actualRequests != 1 || result.Status != HarnessDiagnosticIncompatible || result.ModelCalls != 1 || result.Harness.RootEligible ||
				result.Outcome != string(llm.OutcomeInvalidResponse) {
				t.Fatalf("unsigned thinking qualified or reached continuation: %+v", result)
			}
			if result.ToolExecuted || result.ResponseContentReturned {
				t.Fatal("qualification claimed execution or exposed model content")
			}
			encoded, _ := json.Marshal(struct {
				Result   HarnessQualificationResult
				Settings routeSettings
			}{result, settings})
			if strings.Contains(string(encoded), "private-qualification") || strings.Contains(string(encoded), "native-qualification-tool") {
				t.Fatal("private replay reached public qualification records")
			}
		})
	}
}
