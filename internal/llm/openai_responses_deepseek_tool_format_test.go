package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type deepSeekFormatCapture func(*http.Request) (*http.Response, error)

func (f deepSeekFormatCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDeepSeekResponsesToolPhaseChangesOnlyTextFormatOnChatAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "chat"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			request := ChatRequest{
				Model: "selected-model", JSONMode: true, MaxTokens: 4096, Temperature: 0.4,
				Messages: []Message{{Role: "system", Content: "Use native tools; final answer must be exact JSON."}, {Role: "user", Content: "Find current sources"}},
				Tools:    []ToolSpec{{Name: "web_search", Description: "Find sources", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)}},
			}
			calls := 0
			var captured map[string]any
			provider, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
				Name: "operator-selected-provider", BaseURL: "https://api.deepseek.com/v1", APIKey: "fixture-secret", DefaultModel: "selected-model",
				HTTPClient: &http.Client{Transport: deepSeekFormatCapture(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.String() != "https://api.deepseek.com/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-secret" {
						t.Error("endpoint or credential changed")
					}
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatal(err)
					}
					payload := `{"id":"response_actual","object":"response","status":"completed","model":"selected-model","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"current\"}"}],"usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}}`
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
						payload = "data: " + `{"type":"response.created","response":{"id":"response_actual","object":"response","status":"in_progress","model":"selected-model"}}` + "\n\n" +
							"data: " + `{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"web_search","arguments":""}}` + "\n\n" +
							"data: " + `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"query\":\"current\"}"}` + "\n\n" +
							"data: " + `{"type":"response.function_call_arguments.done","item_id":"fc_1","name":"web_search","arguments":"{\"query\":\"current\"}"}` + "\n\n" +
							"data: " + `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"current\"}"}}` + "\n\n" +
							"data: " + `{"type":"response.completed","response":{"id":"response_actual","object":"response","status":"completed","model":"selected-model","usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}}}` + "\n\n"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
				})},
			})
			if err != nil {
				t.Fatal(err)
			}
			var actual []ToolCall
			var usage int
			if stream {
				chunks, err := provider.StreamChat(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				for chunk := range chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					if chunk.Done {
						actual = chunk.ToolCalls
						usage = chunk.Usage.TotalTokens
					}
				}
			} else {
				response, err := provider.Chat(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				actual, usage = response.ToolCalls, response.Usage.TotalTokens
			}
			if calls != 1 || len(actual) != 1 || actual[0].ID != "call_1" || actual[0].Name != "web_search" || string(actual[0].Arguments) != `{"query":"current"}` || usage != 12 {
				t.Fatalf("native result/usage changed: calls=%d tools=%+v usage=%d", calls, actual, usage)
			}
			// The generic endpoint is the unchanged serialization baseline. Only
			// the official endpoint's text constraint may differ, not model,
			// input, function schema, output bound, store=false, or stream mode.
			baseline := *provider
			baseline.baseURL = "https://proxy.example/v1"
			_, wire, err := baseline.prepareRequest(request, stream)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(wire)
			var expected map[string]any
			_ = json.Unmarshal(encoded, &expected)
			if _, exists := expected["text"]; !exists {
				t.Fatal("baseline no longer requests native JSON")
			}
			delete(expected, "text")
			if !reflect.DeepEqual(captured, expected) {
				t.Fatalf("fields beyond text format changed: actual=%#v expected=%#v", captured, expected)
			}
			binding := providerHarnessBinding(provider.runtime, provider.name, provider.baseURL, request.Model, HarnessTransportOpenAIResponses, HarnessToolStrategyNative, HarnessJSONStrategyNative)
			if provider.DescribeModelHarness(request.Model).BindingDigest != binding || !request.JSONMode {
				t.Fatal("ability identity or caller request mutated")
			}
		})
	}
}

func TestDeepSeekResponsesToolFormatScopeAndToolFreeJSON(t *testing.T) {
	for _, test := range []struct {
		name, endpoint            string
		tools, jsonMode, wantJSON bool
	}{
		{"official tool-free final and format repair", "https://api.deepseek.com/v1", false, true, true},
		{"official explicit default port", "https://API.DEEPSEEK.COM.:443/v1", true, true, false},
		{"third-party proxy remains native JSON", "https://proxy.example/v1", true, true, true},
		{"lookalike suffix remains native JSON", "https://api.deepseek.com.example/v1", true, true, true},
		{"nondefault port remains native JSON", "https://api.deepseek.com:8443/v1", true, true, true},
		{"unrequested JSON is not introduced", "https://api.deepseek.com/v1", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := newTestResponsesProvider(t, test.endpoint)
			request := ChatRequest{JSONMode: test.jsonMode, Messages: []Message{{Role: "user", Content: "Respond exactly as requested"}}}
			if test.tools {
				request.Tools = []ToolSpec{{Name: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}}
			}
			_, wire, err := provider.prepareRequest(request, true)
			if err != nil || (wire.Text != nil) != test.wantJSON {
				t.Fatalf("JSON=%+v err=%v", wire.Text, err)
			}
			if wire.Text != nil && wire.Text.Format.Type != "json_object" {
				t.Fatal("tool-free native JSON changed")
			}
		})
	}
}
