package llm

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type boundaryTimeoutError struct{}

func (boundaryTimeoutError) Error() string   { return "fixture transport timeout" }
func (boundaryTimeoutError) Timeout() bool   { return true }
func (boundaryTimeoutError) Temporary() bool { return false }

type boundaryDataErrorReader struct{ data string }

func (r *boundaryDataErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, boundaryTimeoutError{}
	}
	return n, nil
}
func (*boundaryDataErrorReader) Close() error { return nil }

func TestProviderStreamParsesCompleteFramesBeforeBufferedReadError(t *testing.T) {
	for _, adapter := range []string{"anthropic", "openai", "responses", "ollama"} {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/complete=%t", adapter, complete), func(t *testing.T) {
				prefix := "data: "
				if adapter == "ollama" {
					prefix = ""
				}
				payload := boundaryPartialStream(adapter) + prefix + `{"unfinished":`
				want := OutcomeRetryable
				if complete {
					payload = boundaryPartialStream(adapter) + prefix + "{invalid}\n\n"
					want = OutcomeInvalidResponse
				}
				chunks := make(chan ChatChunk, 32)
				body := &boundaryDataErrorReader{data: payload}
				provider := boundaryProvider(t, adapter, "http://127.0.0.1:1", &http.Client{})
				switch p := provider.(type) {
				case *AnthropicCompatibleProvider:
					p.readStream(t.Context(), body, "model", chunks)
				case *OpenAICompatibleProvider:
					p.readStream(t.Context(), body, "model", chunks)
				case *OpenAIResponsesProvider:
					p.readStream(t.Context(), body, "model", chunks)
				case *OllamaProvider:
					p.readStream(t.Context(), body, "model", 0, chunks)
				}
				var failure *ProviderError
				var text strings.Builder
				for chunk := range chunks {
					text.WriteString(chunk.Text)
					if chunk.Err != nil {
						failure = NormalizeProviderError(provider.Name(), chunk.Err)
					}
				}
				if failure == nil || failure.Kind != want || text.String() != "partial" {
					t.Fatalf("read grouping changed the classification of complete frames: failure=%+v text=%q", failure, text.String())
				}
			})
		}
	}
	t.Run("complete_anthropic_error", func(t *testing.T) {
		provider := boundaryProvider(t, "anthropic", "http://127.0.0.1:1", &http.Client{}).(*AnthropicCompatibleProvider)
		body := &boundaryDataErrorReader{data: "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"private-error-canary\"}}\n\n"}
		chunks := make(chan ChatChunk, 32)
		provider.readStream(t.Context(), body, "model", chunks)
		var failure *ProviderError
		for chunk := range chunks {
			if chunk.Err != nil {
				failure = NormalizeProviderError(provider.Name(), chunk.Err)
			}
		}
		if failure == nil || failure.Kind != OutcomePermanent || strings.Contains(failure.Error(), "private-error-canary") {
			t.Fatalf("complete permanent error was hidden by later read error: %+v", failure)
		}
	})
	t.Run("complete_openai_terminal", func(t *testing.T) {
		provider := boundaryProvider(t, "openai", "http://127.0.0.1:1", &http.Client{}).(*OpenAICompatibleProvider)
		payload := boundaryPartialStream("openai") +
			"data: {\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"model\":\"model\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
			"data: [DONE]\n\n"
		chunks := make(chan ChatChunk, 32)
		provider.readStream(t.Context(), &boundaryDataErrorReader{data: payload}, "model", chunks)
		finals := 0
		for chunk := range chunks {
			if chunk.Err != nil {
				t.Fatal(chunk.Err)
			}
			if chunk.Done {
				finals++
			}
		}
		if finals != 1 {
			t.Fatalf("complete terminal lost before later read error: %d", finals)
		}
	})
}
