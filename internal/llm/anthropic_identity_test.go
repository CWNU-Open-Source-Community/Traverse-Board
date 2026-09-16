package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type anthropicIdentityTransport struct {
	t         *testing.T
	wireName  string
	requested string
}

func (transport anthropicIdentityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body anthropicMessageRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		transport.t.Fatal(err)
	}
	if body.Model != transport.requested {
		transport.t.Fatalf("request model changed: %q", body.Model)
	}
	contentType := "application/json"
	content := fmt.Sprintf(`{"id":"identity-test","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"output_tokens":1}}`, transport.wireName)
	if body.Stream {
		contentType = "text/event-stream"
		content = fmt.Sprintf("data: {\"type\":\"message_start\",\"message\":{\"model\":%q,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n", transport.wireName) +
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"ok\"}}\n\n" +
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"data: {\"type\":\"message_stop\"}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(content))}, nil
}

func TestAnthropicDeepSeekResponseAliasPreservesOnlyExactRoute(t *testing.T) {
	for _, test := range []struct {
		name, provider, base, requested, returned, want string
	}{
		{"observed_official_flash_alias", "deepseek", "https://api.deepseek.com/anthropic", "deepseek-v4-flash", "deepseek-flash", "deepseek-v4-flash"},
		{"exact_model", "deepseek", "https://api.deepseek.com/anthropic", "deepseek-v4-flash", "deepseek-v4-flash", "deepseek-v4-flash"},
		{"different_model_stays_visible", "deepseek", "https://api.deepseek.com/anthropic", "deepseek-v4-flash", "deepseek-pro", "deepseek-pro"},
		{"different_request_stays_visible", "deepseek", "https://api.deepseek.com/anthropic", "deepseek-v4-pro", "deepseek-flash", "deepseek-flash"},
		{"different_host_stays_visible", "deepseek", "https://example.com/anthropic", "deepseek-v4-flash", "deepseek-flash", "deepseek-flash"},
		{"different_path_stays_visible", "deepseek", "https://api.deepseek.com/other", "deepseek-v4-flash", "deepseek-flash", "deepseek-flash"},
		{"different_provider_stays_visible", "other", "https://api.deepseek.com/anthropic", "deepseek-v4-flash", "deepseek-flash", "deepseek-flash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
				Name: test.provider, BaseURL: test.base, APIKey: "test-key", DefaultModel: test.requested,
				HTTPClient: &http.Client{Transport: anthropicIdentityTransport{t: t, requested: test.requested, wireName: test.returned}},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := ChatRequest{Model: test.requested, Messages: []Message{{Role: "user", Content: "hello"}}}
			response, err := provider.Chat(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if response.Model != test.want {
				t.Errorf("Chat model = %q, want %q", response.Model, test.want)
			}
			chunks, err := provider.StreamChat(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			completed := false
			for chunk := range chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				for _, event := range chunk.Events {
					if event.Model != test.want {
						t.Errorf("stream event model = %q, want %q", event.Model, test.want)
					}
				}
				if chunk.Done {
					completed = true
					if chunk.Model != test.want {
						t.Errorf("Stream model = %q, want %q", chunk.Model, test.want)
					}
				}
			}
			if !completed {
				t.Fatal("stream did not complete")
			}
		})
	}
}
