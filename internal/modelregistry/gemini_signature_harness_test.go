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

type geminiHarnessTransport func(*http.Request) (*http.Response, error)

func (f geminiHarnessTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHarnessQualificationGeminiTwoNativeRoundsActualHTTP(t *testing.T) {
	const endpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	const model = "gemini-3.7-flash"
	for _, omitSecondSignature := range []bool{false, true} {
		t.Run(fmt.Sprintf("omit_second_signature=%t", omitSecondSignature), func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			var original []json.RawMessage
			var nonces []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests++
				var body struct {
					Model          string            `json:"model"`
					Messages       []json.RawMessage `json:"messages"`
					Tools          []json.RawMessage `json:"tools"`
					MaxTokens      int               `json:"max_tokens"`
					ResponseFormat json.RawMessage   `json:"response_format"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != model || body.MaxTokens != 256 || len(body.Messages) != requests*2 {
					t.Error("scoped probe changed the model, per-call budget or number of tool rounds")
					w.WriteHeader(400)
					return
				}
				if requests == 1 {
					original = append([]json.RawMessage(nil), body.Messages...)
					nonces = regexp.MustCompile(`[0-9a-f]{32}`).FindAllString(string(body.Messages[0]), -1)
					if len(nonces) != 3 || nonces[0] == nonces[1] || nonces[2] != nonces[0] {
						t.Fatal("sequential rounds did not receive distinct original-turn nonces")
					}
				} else {
					if !reflect.DeepEqual(original, body.Messages[:2]) {
						t.Error("continuation replaced the original system or user instructions")
					}
					for round := 1; round < requests; round++ {
						var assistant struct {
							Role         string          `json:"role"`
							ExtraContent json.RawMessage `json:"extra_content"`
							ToolCalls    []struct {
								ID           string `json:"id"`
								ExtraContent struct {
									Google struct {
										Signature string `json:"thought_signature"`
									} `json:"google"`
								} `json:"extra_content"`
							} `json:"tool_calls"`
						}
						var result struct {
							Role       string `json:"role"`
							ToolCallID string `json:"tool_call_id"`
							Content    string `json:"content"`
						}
						if json.Unmarshal(body.Messages[2*round], &assistant) != nil || json.Unmarshal(body.Messages[2*round+1], &result) != nil ||
							assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 ||
							assistant.ToolCalls[0].ID != fmt.Sprintf("qualification-native-%d", round) ||
							assistant.ToolCalls[0].ExtraContent.Google.Signature != fmt.Sprintf("private-Gemini-probe-%d \n", round) ||
							len(assistant.ExtraContent) == 0 || result.Role != "tool" || result.ToolCallID != assistant.ToolCalls[0].ID {
							t.Error("sequential probe lost native signatures or results-only tool pairing")
						}
						var value harnessProbeResponse
						if decodeExactJSON([]byte(result.Content), &value) != nil || value.Version != HarnessProbeProtocolVersion || value.Status != "tool_result" || value.Nonce != nonces[round-1] {
							t.Error("synthetic tool result was not the exact round nonce")
						}
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(delta any, finish string) {
					event := map[string]any{"id": fmt.Sprintf("qualification-response-%d", requests), "model": model,
						"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
					if finish != "" {
						event["usage"] = map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}
					}
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				if requests <= 2 {
					if len(body.Tools) != 1 || len(body.ResponseFormat) != 0 {
						t.Error("native tool round lost its tool or forced JSON")
					}
					arguments := `{"nonce":"` + nonces[requests-1] + `"}`
					emit(map[string]any{"role": "assistant", "content": "I will echo the nonce.", "tool_calls": []any{map[string]any{
						"index": 0, "id": fmt.Sprintf("qualification-native-%d", requests), "type": "function", "function": map[string]string{"name": "prayu_harness_echo", "arguments": arguments[:10]}}}}, "")
					emit(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]string{"arguments": arguments[10:]}}}}, "")
					tool := map[string]any{"index": 0}
					if !omitSecondSignature || requests != 2 {
						tool["extra_content"] = map[string]any{"google": map[string]string{"thought_signature": fmt.Sprintf("private-Gemini-probe-%d \n", requests)}}
					}
					emit(map[string]any{"content": "", "extra_content": map[string]any{"google": map[string]string{"thought_signature": fmt.Sprintf("private-Gemini-message-%d", requests)}}, "tool_calls": []any{tool}}, "tool_calls")
				} else {
					if requests != 3 || len(body.Tools) != 0 || string(body.ResponseFormat) != `{"type":"json_object"}` {
						t.Error("final scoped probe did not exercise native JSON without offering a third tool")
					}
					final, _ := json.Marshal(harnessProbeResponse{Version: HarnessProbeProtocolVersion, Status: "ok", Nonce: nonces[0]})
					emit(map[string]any{"role": "assistant", "content": string(final)}, "stop")
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			values := map[string]string{"CYBERAGENT_OPENAI_API_KEY": "fixture", "CYBERAGENT_OPENAI_BASE_URL": endpoint, "CYBERAGENT_OPENAI_MODEL": model}
			registry := New(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
			ref := llm.ModelRef{Provider: "openai", Model: model}
			if !registry.geminiSequentialProbes[ref] {
				t.Fatal("configured Google endpoint did not freeze its sequential probe plan")
			}
			values["CYBERAGENT_OPENAI_BASE_URL"] = "https://example.test/v1"
			target, _ := url.Parse(server.URL)
			provider, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: "openai", BaseURL: endpoint, DefaultModel: model, APIKey: "fixture",
				HTTPClient: &http.Client{Transport: geminiHarnessTransport(func(request *http.Request) (*http.Response, error) {
					deadline, ok := request.Context().Deadline()
					if !ok || time.Until(deadline) > HarnessQualificationTimeout || request.URL.String() != endpoint {
						t.Error("probe escaped the shared deadline or complete endpoint")
					}
					copy := request.Clone(request.Context())
					copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
					return http.DefaultTransport.RoundTrip(copy)
				})}})
			if err != nil {
				t.Fatal(err)
			}
			registry.router.RegisterProvider(provider)
			result, err := registry.QualifyHarness(t.Context(), routeSettings{}, "openai", model)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actual := requests
			mu.Unlock()
			if omitSecondSignature {
				if actual != 2 || result.ModelCalls != 2 || result.Status != HarnessDiagnosticIncompatible || result.Harness.RootEligible {
					t.Fatalf("unsigned second round qualified: %+v", result)
				}
			} else {
				if actual != 3 || result.ModelCalls != 3 || result.SyntheticToolCalls != 2 || result.Status != HarnessDiagnosticQualified || !result.Harness.RootEligible || !result.Harness.StrictJSONQualified {
					t.Fatalf("sequential native replay did not qualify: %+v", result)
				}
				for _, workload := range []llm.HarnessWorkload{llm.HarnessWorkloadSpecialist, llm.HarnessWorkloadFanout} {
					request, _, err := registry.router.PrepareHarnessRequest(ref, workload, llm.ChatRequest{Tools: []llm.ToolSpec{{Name: "echo"}}})
					if err != nil || len(request.Tools) != 0 {
						t.Fatal("qualification expanded nonroot tool authority", err)
					}
				}
			}
			public, _ := json.Marshal(result)
			if strings.Contains(string(public), "private-Gemini") || result.ToolExecuted || result.ResponseContentReturned {
				t.Fatal("probe leaked native state or claimed tool execution")
			}
		})
	}
}

func TestGeminiFrozenCustomMappingScope(t *testing.T) {
	registry := New(func(string) (string, bool) { return "", false })
	registry.credentials = func(_ context.Context, _ string) (string, bool, error) { return "fixture", true, nil }
	definition := validCustomDefinition("https://generativelanguage.googleapis.com/v1beta/openai/chat/completions")
	definition.AdvancedConfig = json.RawMessage(`{"model_mapping":{"acme-code":"gemini-3.7-flash","acme-fast":"other-model"}}`)
	if err := registry.registerCustomProvider(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	if !registry.geminiSequentialProbes[llm.ModelRef{Provider: definition.ID, Model: "acme-code"}] || registry.geminiSequentialProbes[llm.ModelRef{Provider: definition.ID, Model: "acme-fast"}] {
		t.Fatal("custom registration inferred scope from route names instead of actual wire model")
	}
}
