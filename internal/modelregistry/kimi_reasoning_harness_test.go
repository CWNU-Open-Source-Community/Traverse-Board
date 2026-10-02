package modelregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/llm"
)

func TestHarnessQualificationKimiFourRequestsPreserveOrdinaryHistory(t *testing.T) {
	const endpoint = "https://api.moonshot.ai/v1/chat/completions"
	for _, invalid := range []string{"", "third_reason_type", "fourth_finish"} {
		t.Run(invalid, func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			var original []json.RawMessage
			var nonces []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests++
				var body struct {
					Model           string            `json:"model"`
					Messages        []json.RawMessage `json:"messages"`
					Tools           []json.RawMessage `json:"tools"`
					MaxTokens       int               `json:"max_tokens"`
					ResponseFormat  json.RawMessage   `json:"response_format"`
					Thinking        json.RawMessage   `json:"thinking"`
					ReasoningEffort json.RawMessage   `json:"reasoning_effort"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "kimi-k3" || body.MaxTokens != 256 ||
					len(body.Messages) != 2*requests || len(body.Thinking) != 0 || len(body.ReasoningEffort) != 0 {
					t.Error("K3 qualification changed its scope, per-request budget, thinking, or history")
					w.WriteHeader(400)
					return
				}
				if requests == 1 {
					original = append([]json.RawMessage(nil), body.Messages...)
					nonces = regexp.MustCompile(`[0-9a-f]{32}`).FindAllString(string(body.Messages[0]), -1)
					if len(nonces) != 3 || nonces[0] == nonces[1] || nonces[0] != nonces[2] {
						t.Error("invalid qualification nonces")
						w.WriteHeader(400)
						return
					}
				} else if !reflect.DeepEqual(original, body.Messages[:2]) {
					t.Error("original turn changed")
				}
				for round := 1; round < min(requests, 3); round++ {
					var assistant struct {
						Role      string `json:"role"`
						Reasoning string `json:"reasoning_content"`
						Calls     []struct {
							ID string `json:"id"`
						} `json:"tool_calls"`
					}
					var result struct {
						Role    string `json:"role"`
						CallID  string `json:"tool_call_id"`
						Content string `json:"content"`
					}
					if json.Unmarshal(body.Messages[2*round], &assistant) != nil || json.Unmarshal(body.Messages[2*round+1], &result) != nil ||
						assistant.Role != "assistant" || assistant.Reasoning != fmt.Sprintf(" private-K3-probe-%d \n", round) ||
						len(assistant.Calls) != 1 || assistant.Calls[0].ID != fmt.Sprintf("native-probe-%d", round) ||
						result.Role != "tool" || result.CallID != assistant.Calls[0].ID {
						t.Error("sequential qualification lost native reasoning or exact result pairing")
					}
					var value harnessProbeResponse
					if decodeExactJSON([]byte(result.Content), &value) != nil || value.Version != HarnessProbeProtocolVersion ||
						value.Status != "tool_result" || value.Nonce != nonces[round-1] {
						t.Error("synthetic result changed")
					}
				}
				if requests == 4 {
					var ordinary struct {
						Role      string `json:"role"`
						Reasoning string `json:"reasoning_content"`
						Content   string `json:"content"`
						Calls     []any  `json:"tool_calls"`
					}
					if json.Unmarshal(body.Messages[6], &ordinary) != nil || ordinary.Role != "assistant" || len(ordinary.Calls) != 0 || ordinary.Reasoning != " private-K3-probe-3 \n" {
						t.Error("ordinary answer lost its private history before follow-up")
					}
					var value harnessProbeResponse
					if decodeExactJSON([]byte(ordinary.Content), &value) != nil || value.Nonce != nonces[0] {
						t.Error("ordinary acknowledgement changed")
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(delta any, finish string) {
					event := map[string]any{"id": fmt.Sprintf("native-qualification-%d", requests), "model": "kimi-k3",
						"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
					if finish != "" {
						event["usage"] = map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}
					}
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				var reason any = fmt.Sprintf(" private-K3-probe-%d \n", requests)
				if requests == 3 && invalid == "third_reason_type" {
					reason = map[string]string{"unexpected": "private-K3-invalid"}
				}
				emit(map[string]any{"role": "assistant", "reasoning_content": reason}, "")
				if requests <= 2 {
					if len(body.Tools) != 1 || len(body.ResponseFormat) != 0 {
						t.Error("tool qualification forced JSON or lost its tool")
					}
					args, _ := json.Marshal(map[string]string{"nonce": nonces[requests-1]})
					emit(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("native-probe-%d", requests), "type": "function",
						"function": map[string]string{"name": "prayu_harness_echo", "arguments": string(args)}}}}, "tool_calls")
				} else {
					if len(body.Tools) != 0 || string(body.ResponseFormat) != `{"type":"json_object"}` {
						t.Error("ordinary qualification escaped native strict JSON scope")
					}
					final, _ := json.Marshal(harnessProbeResponse{Version: HarnessProbeProtocolVersion, Status: "ok", Nonce: nonces[requests-3]})
					finish := "stop"
					if requests == 4 && invalid == "fourth_finish" {
						finish = "unknown-terminal"
					}
					emit(map[string]any{"content": string(final)}, finish)
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			values := map[string]string{"CYBERAGENT_OPENAI_API_KEY": "fixture", "CYBERAGENT_OPENAI_BASE_URL": endpoint, "CYBERAGENT_OPENAI_MODEL": "kimi-k3"}
			registry := New(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
			ref := llm.ModelRef{Provider: "openai", Model: "kimi-k3"}
			if !registry.kimiHistoryProbes[ref] {
				t.Fatal("K3 plan was not frozen at construction")
			}
			values["CYBERAGENT_OPENAI_BASE_URL"] = "https://example.test/v1"
			target, _ := url.Parse(server.URL)
			provider, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: "openai", BaseURL: endpoint, APIKey: "fixture", DefaultModel: "kimi-k3",
				HTTPClient: &http.Client{Transport: geminiHarnessTransport(func(request *http.Request) (*http.Response, error) {
					deadline, ok := request.Context().Deadline()
					if !ok || time.Until(deadline) > HarnessQualificationTimeout || request.URL.String() != endpoint {
						t.Error("qualification escaped original deadline or origin")
					}
					copy := request.Clone(request.Context())
					copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
					return http.DefaultTransport.RoundTrip(copy)
				})}})
			if err != nil {
				t.Fatal(err)
			}
			registry.router.RegisterProvider(provider)
			result, err := registry.QualifyHarness(t.Context(), routeSettings{}, "openai", "kimi-k3")
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actual := requests
			mu.Unlock()
			if invalid != "" {
				want := 3
				if invalid == "fourth_finish" {
					want = 4
				}
				if actual != want || result.ModelCalls != want || result.Status != HarnessDiagnosticIncompatible || result.Harness.RootEligible {
					t.Fatal("incomplete native history qualified", result)
				}
			} else {
				if actual != 4 || result.ModelCalls != 4 || result.SyntheticToolCalls != 2 || result.Status != HarnessDiagnosticQualified || !result.Harness.RootEligible {
					t.Fatal("four-request native history did not qualify", result)
				}
				for _, workload := range []llm.HarnessWorkload{llm.HarnessWorkloadSpecialist, llm.HarnessWorkloadFanout} {
					request, _, err := registry.router.PrepareHarnessRequest(ref, workload, llm.ChatRequest{Tools: []llm.ToolSpec{{Name: "echo"}}})
					if err != nil || len(request.Tools) != 0 {
						t.Fatal("qualification expanded nonroot tools", err)
					}
				}
			}
			public, _ := json.Marshal(result)
			if strings.Contains(string(public), "private-K3") || result.ToolExecuted || result.ResponseContentReturned {
				t.Fatal("qualification leaked private content or claimed real tool work")
			}
		})
	}
}

func TestKimiFrozenCustomMappedScope(t *testing.T) {
	for _, endpoint := range []string{"https://api.moonshot.ai/v1", "https://api.moonshot.cn/v1", "https://api.kimi.com/coding/v1", "https://example.test/v1"} {
		registry := New(func(string) (string, bool) { return "", false })
		registry.credentials = func(context.Context, string) (string, bool, error) { return "fixture", true, nil }
		definition := validCustomDefinition(endpoint)
		definition.AdvancedConfig = json.RawMessage(`{"model_mapping":{"acme-code":"kimi-k3","acme-fast":"kimi-for-coding"}}`)
		if err := registry.registerCustomProvider(t.Context(), definition); err != nil {
			t.Fatal(err)
		}
		want := endpoint == "https://api.moonshot.ai/v1" || endpoint == "https://api.moonshot.cn/v1"
		if registry.kimiHistoryProbes[llm.ModelRef{Provider: definition.ID, Model: "acme-code"}] != want || registry.kimiHistoryProbes[llm.ModelRef{Provider: definition.ID, Model: "acme-fast"}] {
			t.Fatal("provider display name or alias changed K3 qualification scope")
		}
	}
}
