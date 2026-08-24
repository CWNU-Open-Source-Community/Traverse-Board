package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestOpenAICompatibleProviderChatJSONAndModelList(t *testing.T) {
	var captured openAIChatRequest
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		auth = request.Header.Get("Authorization")
		switch request.URL.Path {
		case "/v1/models":
			_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"gpt-test"},{"id":"gpt-other"}]}`))
		case "/v1/chat/completions":
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Error(err)
				return
			}
			_, _ = writer.Write([]byte(`{
				"id":"chatcmpl-safe","model":"gpt-test",
				"choices":[{"index":0,"message":{"role":"assistant","content":"{\"status\":\"ok\"}","reasoning_content":"private trace"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":7,"completion_tokens":4,"total_tokens":11}
			}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	models, err := provider.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gpt-test" || models[0].Provider != "openai" ||
		len(models[0].Capabilities) != 0 {
		t.Fatalf("unexpected model list: %#v", models)
	}
	response, err := provider.Chat(t.Context(), ChatRequest{
		Messages: []Message{{Role: "system", Content: "Return JSON."}, {Role: "user", Content: "status"}},
		JSONMode: true, MaxTokens: 73, Metadata: map[string]string{"private": "must-not-cross"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer test-secret" {
		t.Fatalf("authorization = %q", auth)
	}
	if captured.Model != "gpt-test" || captured.MaxTokens != 73 || captured.ResponseFormat == nil ||
		captured.ResponseFormat.Type != "json_object" || len(captured.Messages) != 2 {
		t.Fatalf("unexpected request: %#v", captured)
	}
	encoded, _ := json.Marshal(captured)
	if strings.Contains(string(encoded), "must-not-cross") {
		t.Fatalf("internal metadata crossed the provider boundary: %s", encoded)
	}
	if response.Text != `{"status":"ok"}` || response.Model != "gpt-test" ||
		response.Provider != "openai" || response.Usage.TotalTokens != 11 || response.Raw != nil ||
		strings.Contains(response.Text, "private trace") {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestOpenAICompatibleProviderMapsToolsAndMultipleToolResults(t *testing.T) {
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Error(err)
			return
		}
		_, _ = writer.Write([]byte(`{
			"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":null,
			"tool_calls":[
				{"id":"call_c","type":"function","function":{"name":"run_command","arguments":"{\"command\":\"id\"}"}},
				{"id":"call_r","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}
			]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":9,"completion_tokens":5,"total_tokens":14}
		}`))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	response, err := provider.Chat(t.Context(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "old_a", Name: "run_command", Arguments: json.RawMessage(`{"command":"pwd"}`)},
				{ID: "old_b", Name: "read_file", Arguments: json.RawMessage(`{"path":"go.mod"}`)},
			}},
			{Role: "user", Content: "Continue after both results.", ToolResults: []ToolResult{
				{ToolCallID: "old_a", Content: `{"stdout":"repo"}`},
				{ToolCallID: "old_b", Content: `{"content":"module"}`},
			}},
		},
		Tools: []ToolSpec{
			{Name: "run_command", Description: "Run a command", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Messages) != 5 {
		t.Fatalf("message expansion = %#v", captured.Messages)
	}
	roles := []string{"user", "assistant", "tool", "tool", "user"}
	for index, role := range roles {
		if captured.Messages[index].Role != role {
			t.Fatalf("message %d role = %q, want %q", index, captured.Messages[index].Role, role)
		}
	}
	if captured.Messages[2].ToolCallID != "old_a" || captured.Messages[3].ToolCallID != "old_b" ||
		captured.Messages[4].Content == nil || *captured.Messages[4].Content != "Continue after both results." {
		t.Fatalf("tool-result mapping = %#v", captured.Messages)
	}
	if len(captured.Tools) != 2 || captured.Tools[0].Type != "function" ||
		len(captured.Messages[1].ToolCalls) != 2 {
		t.Fatalf("tool mapping = messages %#v tools %#v", captured.Messages, captured.Tools)
	}
	if len(response.ToolCalls) != 2 || response.ToolCalls[0].ID != "call_c" ||
		response.ToolCalls[1].Name != "read_file" || response.Text != "" {
		t.Fatalf("tool response = %#v", response)
	}
}

func TestOpenAICompatibleProviderStreamsInterleavedToolCallsDeterministically(t *testing.T) {
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Error(err)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		writeSSE := func(payload string) {
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", payload)
			flusher.Flush()
		}
		writeSSE(`{"id":"chunk","model":"gpt-test-2026-08-01","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}`)
		writeSSE(`{"id":"chunk","model":"gpt-test-2026-08-01","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read_file","arguments":"{\"pa"}},{"index":0,"id":"call_a","type":"function","function":{"name":"run_command","arguments":"{\"com"}}]},"finish_reason":null}]}`)
		writeSSE(`{"id":"chunk","model":"gpt-test-2026-08-01","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"mand\":\"id\"}"}},{"index":1,"function":{"arguments":"th\":\"README.md\"}"}}]},"finish_reason":null}]}`)
		writeSSE(`{"id":"chunk","model":"gpt-test-2026-08-01","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		writeSSE(`{"id":"chunk","model":"gpt-test-2026-08-01","choices":[],"usage":{"prompt_tokens":13,"completion_tokens":6,"total_tokens":19}}`)
		writeSSE(`[DONE]`)
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "inspect"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var all []ChatChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		all = append(all, chunk)
	}
	if !captured.Stream || captured.StreamOptions == nil || !captured.StreamOptions.IncludeUsage {
		t.Fatalf("stream usage option missing: %#v", captured)
	}
	var final *ChatChunk
	var eventTypes []StreamEventType
	for index := range all {
		for _, event := range all[index].Events {
			eventTypes = append(eventTypes, event.Type)
		}
		if all[index].Done {
			copy := all[index]
			final = &copy
		}
	}
	if final == nil || final.Model != "gpt-test" || final.Usage == nil ||
		final.Usage.TotalTokens != 19 || len(final.ToolCalls) != 2 {
		t.Fatalf("unexpected chunks: %#v", all)
	}
	wantEvents := []StreamEventType{StreamResponseStarted,
		StreamOutputItemStarted, StreamToolCallStarted, StreamToolArgumentDelta,
		StreamOutputItemStarted, StreamToolCallStarted, StreamToolArgumentDelta,
		StreamToolArgumentDelta, StreamToolArgumentDelta,
		StreamToolCallCompleted, StreamOutputItemCompleted,
		StreamToolCallCompleted, StreamOutputItemCompleted, StreamResponseCompleted}
	if !reflect.DeepEqual(eventTypes, wantEvents) {
		t.Fatalf("item event order = %v, want %v", eventTypes, wantEvents)
	}
	if first, second := final.ToolCalls[0], final.ToolCalls[1]; first.ID != "call_a" || first.Name != "run_command" || string(first.Arguments) != `{"command":"id"}` ||
		second.ID != "call_b" || second.Name != "read_file" || string(second.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("tool calls not deterministically aggregated: %#v", final.ToolCalls)
	}
}

func TestOpenAICompatibleProviderAcceptsUsageOnFinalChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test-snapshot\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "inspect"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var all []ChatChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		all = append(all, chunk)
	}
	var final *ChatChunk
	var eventTypes []StreamEventType
	for index := range all {
		for _, event := range all[index].Events {
			eventTypes = append(eventTypes, event.Type)
		}
		if all[index].Done {
			copy := all[index]
			final = &copy
		}
	}
	if final == nil || final.Usage == nil || final.Usage.TotalTokens != 12 ||
		len(final.ToolCalls) != 1 || final.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected final-choice usage stream: %#v", all)
	}
	if want := providerToolDeltaEventVector(1); !reflect.DeepEqual(eventTypes, want) {
		t.Fatalf("OpenAI tool event vector = %v, want %v", eventTypes, want)
	}
}

func TestOpenAICompatibleProviderAcceptsTextUsageOnFinalChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test-snapshot\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var final *ChatChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		text.WriteString(chunk.Text)
		if chunk.Done {
			copyChunk := chunk
			final = &copyChunk
		}
	}
	if text.String() != "ok" || final == nil || final.Usage == nil ||
		final.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected text final-choice usage: text=%q final=%#v", text.String(), final)
	}
}

func TestOpenAICompatibleProviderRejectsNonTerminalUsageEvents(t *testing.T) {
	tests := []struct {
		name   string
		events []string
	}{
		{name: "usage before finish with empty choices", events: []string{
			`{"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		}},
		{name: "duplicate usage after final choice", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
			`{"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				for _, event := range test.events {
					_, _ = fmt.Fprintf(writer, "data: %s\n\n", event)
				}
				_, _ = writer.Write([]byte("data: [DONE]\n\n"))
			}))
			defer server.Close()
			provider := newTestOpenAIProvider(t, server.URL)
			chunks, err := provider.StreamChat(t.Context(), ChatRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var got error
			for chunk := range chunks {
				if chunk.Err != nil {
					got = chunk.Err
				}
			}
			if ProviderErrorReason(got) != ProviderFailureProtocolIncompatible {
				t.Fatalf("usage event error = %#v", got)
			}
		})
	}
}

func TestOpenAICompatibleProviderRejectsUsageBeforeFinish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for chunk := range chunks {
		if chunk.Err != nil {
			got = chunk.Err
		}
	}
	if ProviderErrorReason(got) != ProviderFailureProtocolIncompatible {
		t.Fatalf("early usage error = %#v", got)
	}
}

func TestOpenAICompatibleProviderStreamsTextAcrossUTF8TransportSplits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		payload := []byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好🙂\"},\"finish_reason\":null}]}\n\n")
		for _, current := range payload {
			_, _ = writer.Write([]byte{current})
			flusher.Flush()
		}
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	finals := 0
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		if len(chunk.ToolCalls) != 0 && !chunk.Done {
			t.Fatalf("non-final tool calls: %#v", chunk)
		}
		text.WriteString(chunk.Text)
		if chunk.Done {
			finals++
		}
	}
	if text.String() != "你好🙂" || !utf8.ValidString(text.String()) || finals != 1 {
		t.Fatalf("text=%q finals=%d", text.String(), finals)
	}
}

func TestOpenAICompatibleProviderRejectsIncompleteStreams(t *testing.T) {
	tests := []struct {
		name   string
		events []string
	}{
		{name: "missing usage", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
			`[DONE]`,
		}},
		{name: "truncated finish", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"length"}]}`,
			`{"model":"gpt-test","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			`[DONE]`,
		}},
		{name: "missing done", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
			`{"model":"gpt-test","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				for _, event := range test.events {
					_, _ = fmt.Fprintf(writer, "data: %s\n\n", event)
				}
			}))
			defer server.Close()
			provider := newTestOpenAIProvider(t, server.URL)
			chunks, err := provider.StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			var got error
			for chunk := range chunks {
				if chunk.Err != nil {
					got = chunk.Err
				}
			}
			if got == nil || ProviderErrorKind(got) != OutcomeInvalidResponse ||
				ProviderErrorReason(got) != ProviderFailureProtocolIncompatible {
				t.Fatalf("error = %#v", got)
			}
		})
	}
}

func TestOpenAICompatibleProviderRejectsMalformedStreamedToolCalls(t *testing.T) {
	tests := []struct {
		name   string
		events []string
	}{
		{name: "sparse index", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		}},
		{name: "conflicting identity", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":"{"}}]},"finish_reason":null}]}`,
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_changed","function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
		}},
		{name: "invalid arguments", events: []string{
			`{"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":"not-json"}}]},"finish_reason":"tool_calls"}]}`,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				for _, event := range test.events {
					_, _ = fmt.Fprintf(writer, "data: %s\n\n", event)
				}
				_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
				_, _ = writer.Write([]byte("data: [DONE]\n\n"))
			}))
			defer server.Close()
			provider := newTestOpenAIProvider(t, server.URL)
			chunks, err := provider.StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			var got error
			for chunk := range chunks {
				if chunk.Err != nil {
					got = chunk.Err
				}
			}
			if got == nil || ProviderErrorReason(got) != ProviderFailureProtocolIncompatible {
				t.Fatalf("error = %#v", got)
			}
		})
	}
}

func TestOpenAICompatibleProviderRequiresExactUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{
			"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":99}
		}`))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	if _, err := provider.Chat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}); err == nil || ProviderErrorReason(err) != ProviderFailureProtocolIncompatible {
		t.Fatalf("inconsistent usage error = %#v", err)
	}
}

func TestOpenAICompatibleProviderAcceptsResolvedResponseModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{
			"model":"gpt-test-2026-08-01","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	response, err := provider.Chat(t.Context(), ChatRequest{Messages: []Message{{
		Role: "user", Content: "hello",
	}}})
	if err != nil || response.Model != "gpt-test" {
		t.Fatalf("resolved response = %#v error = %#v", response, err)
	}
}

func TestOpenAICompatibleProviderHTTPFailuresAreContentFree(t *testing.T) {
	secret := "sk-12345678901234567890"
	tests := []struct {
		status int
		kind   Outcome
		reason ProviderFailureReason
	}{
		{http.StatusUnauthorized, OutcomePermanent, ProviderFailureAuthentication},
		{http.StatusNotFound, OutcomePermanent, ProviderFailureProtocolIncompatible},
		{http.StatusTooManyRequests, OutcomeRateLimited, ProviderFailureRateLimit},
		{http.StatusServiceUnavailable, OutcomeRetryable, ProviderFailureCapacity},
	}
	for _, test := range tests {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Retry-After", "7")
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(fmt.Sprintf(`{"error":{"message":"raw %s prompt and args","type":"server_error","code":"ignored"}}`, secret)))
			}))
			defer server.Close()
			provider := newTestOpenAIProvider(t, server.URL)
			_, err := provider.Chat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
			var typed *ProviderError
			if !errors.As(err, &typed) {
				t.Fatalf("error = %#v", err)
			}
			if typed.Kind != test.kind || typed.Reason != test.reason || typed.StatusCode != test.status ||
				typed.RetryAfter != 7*time.Second || typed.Cause != nil {
				t.Fatalf("typed error = %#v", typed)
			}
			if strings.Contains(typed.Error(), secret) || strings.Contains(typed.Error(), "prompt") ||
				strings.Contains(typed.Error(), "args") || strings.Contains(typed.Error(), "server_error") {
				t.Fatalf("raw response leaked through error: %q", typed.Error())
			}
		})
	}
}

func TestOpenAICompatibleProviderClassifiesCanonicalModelNotFound(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		code     string
		want     ProviderFailureReason
	}{
		{name: "canonical code", typeName: "invalid_request_error", code: "model_not_found",
			want: ProviderFailureModelNotFound},
		{name: "near-match code", typeName: "invalid_request_error", code: "not_model_not_found_suffix",
			want: ProviderFailureProtocolIncompatible},
		{name: "near-match type", typeName: "model_not_foundish", code: "unknown",
			want: ProviderFailureProtocolIncompatible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprintf(writer,
					`{"error":{"message":"must remain private","type":%q,"code":%q}}`,
					test.typeName, test.code)
			}))
			defer server.Close()
			provider := newTestOpenAIProvider(t, server.URL)
			_, err := provider.Chat(t.Context(), ChatRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			})
			if ProviderErrorReason(err) != test.want ||
				strings.Contains(fmt.Sprint(err), "must remain private") {
				t.Fatalf("model-not-found classification = %#v", err)
			}
		})
	}
}

func TestClassifyOpenAIWireErrorRequiresExactSignals(t *testing.T) {
	tests := []struct {
		signal string
		want   ProviderFailureReason
	}{
		{signal: "rate_limit_exceeded", want: ProviderFailureRateLimit},
		{signal: "overloaded_error", want: ProviderFailureCapacity},
		{signal: "model_not_exists", want: ProviderFailureModelNotFound},
		{signal: "invalid_api_key", want: ProviderFailureAuthentication},
		{signal: "request_timeout", want: ProviderFailureNetwork},
		{signal: "rate_limit_exceeded_suffix", want: ProviderFailureNone},
		{signal: "prefix_invalid_api_key", want: ProviderFailureNone},
	}
	for _, test := range tests {
		encoded, err := json.Marshal(test.signal)
		if err != nil {
			t.Fatal(err)
		}
		if got := classifyOpenAIWireError(openAIError{Code: encoded}); got != test.want {
			t.Fatalf("signal %q classified as %q, want %q", test.signal, got, test.want)
		}
	}
}

func TestOpenAICompatibleProviderDistinguishesCancellationAndDeadline(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	tests := []struct {
		name       string
		context    func() (context.Context, context.CancelFunc)
		wantReason ProviderFailureReason
	}{
		{name: "cancelled", context: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}, wantReason: ProviderFailureNone},
		{name: "deadline", context: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, wantReason: ProviderFailureNetwork},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.context()
			defer cancel()
			_, err := provider.Chat(ctx, ChatRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			})
			if ProviderErrorKind(err) != OutcomeCancelled || ProviderErrorReason(err) != test.wantReason {
				t.Fatalf("transport error = %#v", err)
			}
		})
	}
}

func TestOpenAICompatibleProviderRejectsStreamModelChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test-2026-08-01\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"model\":\"gpt-test-2026-08-02\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
	}))
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for chunk := range chunks {
		if chunk.Err != nil {
			got = chunk.Err
		}
	}
	if ProviderErrorReason(got) != ProviderFailureProtocolIncompatible {
		t.Fatalf("stream model change error = %#v", got)
	}
}

func TestOpenAICompatibleProviderHarnessAndConfiguration(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	provider := newTestOpenAIProvider(t, server.URL+"/v1")
	profile := provider.DescribeModelHarness("")
	if profile.TransportProtocol != HarnessTransportOpenAIChatCompletions ||
		profile.ToolStrategy != HarnessToolStrategyNative ||
		profile.JSONStrategy != HarnessJSONStrategyNative ||
		profile.QualificationStatus != HarnessQualificationRequired || profile.Validate() != nil {
		t.Fatalf("invalid Harness profile: %#v", profile)
	}
	if !provider.SupportsTools("gpt-test") || !provider.SupportsJSONMode("gpt-test") ||
		provider.SupportsTools("unknown-model") || provider.SupportsJSONMode("unknown-model") {
		t.Fatal("OpenAI capability checks did not stay bound to the configured model")
	}
	if _, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: "http://example.com", APIKey: "secret", DefaultModel: "gpt-test",
	}); err == nil {
		t.Fatal("accepted non-loopback HTTP endpoint")
	}
	if _, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "secret with spaces", DefaultModel: "gpt-test",
	}); err == nil {
		t.Fatal("accepted unsafe API key")
	}
}

func newTestOpenAIProvider(t *testing.T, baseURL string) *OpenAICompatibleProvider {
	t.Helper()
	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		Name: "openai", BaseURL: baseURL, APIKey: "test-secret", DefaultModel: "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
