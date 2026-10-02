package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderSSEEventSizeCountsJoinedBytesAndRejectsOverflow(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("bytes_%+d", delta), func(t *testing.T) {
			size := providerSSEEventSize{}
			if !size.append(10) {
				t.Fatal("first line rejected")
			}
			before := size
			accepted := size.append(maxProviderSSEEventBytes - 11 + delta)
			if accepted != (delta <= 0) || accepted && size.bytes != maxProviderSSEEventBytes+delta || !accepted && size != before {
				t.Fatalf("separator or limit changed: accepted=%t size=%+v", accepted, size)
			}
		})
	}
	for _, count := range []int{maxProviderSSEDataLines - 1, maxProviderSSEDataLines, maxProviderSSEDataLines + 1} {
		t.Run(fmt.Sprintf("empty_lines_%d", count), func(t *testing.T) {
			size := providerSSEEventSize{}
			for line := 0; line < count; line++ {
				before := size
				accepted := size.append(0)
				if accepted != (line < maxProviderSSEDataLines) || !accepted && size != before {
					t.Fatalf("empty line %d was not bounded: %+v", line, size)
				}
			}
			want := min(count, maxProviderSSEDataLines)
			if size.lines != want || size.bytes != want-1 {
				t.Fatalf("empty separators lost: %+v", size)
			}
		})
	}
	maxInt := int(^uint(0) >> 1)
	for _, test := range []struct {
		name string
		size providerSSEEventSize
		part int
	}{
		{"oversized_part", providerSSEEventSize{}, maxInt},
		{"oversized_counter", providerSSEEventSize{bytes: maxInt, lines: 1}, 1},
		{"oversized_line_counter", providerSSEEventSize{lines: maxInt}, 0},
		{"negative_part", providerSSEEventSize{}, -1},
		{"negative_counter", providerSSEEventSize{bytes: -1}, 0},
		{"negative_line_counter", providerSSEEventSize{lines: -1}, 0},
		{"separator_at_byte_limit", providerSSEEventSize{bytes: maxProviderSSEEventBytes, lines: 1}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			size := test.size
			if size.append(test.part) || size != test.size {
				t.Fatalf("invalid arithmetic accepted or mutated: %+v", size)
			}
		})
	}
}

func sseBoundsProvider(t *testing.T, kind, endpoint string) Provider {
	t.Helper()
	if kind == "responses" {
		return newTestResponsesProvider(t, endpoint)
	}
	if kind == "chat" {
		return newTestOpenAIProvider(t, endpoint)
	}
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
		Name: "anthropic-bounds", BaseURL: endpoint, APIKey: "fixture-only", DefaultModel: "model-local"})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func sseBoundsFrames(kind string) (prefix, suffix, ignoredType string) {
	if kind == "anthropic" {
		return "data: {\"type\":\"message_start\",\"message\":{\"model\":\"model-local\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n",
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"bounded reply\"}}\n\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n", "ping"
	}
	return "data: {\"type\":\"response.created\",\"response\":{\"id\":\"response_1\",\"object\":\"response\",\"status\":\"in_progress\",\"model\":\"model-local\"}}\n\n",
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"message_1\",\"type\":\"message\",\"status\":\"in_progress\",\"role\":\"assistant\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"message_1\",\"delta\":\"bounded reply\"}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"message_1\",\"type\":\"message\",\"status\":\"completed\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"bounded reply\"}]}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"model-local\",\"output\":[{\"id\":\"message_1\",\"type\":\"message\",\"status\":\"completed\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"bounded reply\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n", "response.in_progress"
}

// Both data lines are individually legal. Their joined payload has exactly
// target bytes, including the newline, and remains valid JSON at each boundary.
func sseBoundsPaddedEvent(kind string, target int) string {
	_, _, eventType := sseBoundsFrames(kind)
	left := fmt.Sprintf(`{"type":%q,"secret":"sse-bounds-private-canary","pad1":"`, eventType)
	right := `"pad2":"`
	leftEnd, rightEnd := `",`, `","tail":true}`
	padding := target - len(left) - len(right) - len(leftEnd) - len(rightEnd) - 1
	first := left + strings.Repeat("x", padding/2) + leftEnd
	second := right + strings.Repeat("x", padding-padding/2) + rightEnd
	payload := first + "\n" + second
	if len(payload) != target || !json.Valid([]byte(payload)) ||
		len(first)+len("data: \n") >= maxOpenAIStreamLineBytes || len(second)+len("data: \n") >= maxOpenAIStreamLineBytes {
		panic("invalid bounded SSE boundary fixture")
	}
	return "data: " + first + "\n" + "data: " + second + "\n\n"
}

func verifySSEBoundsResult(t *testing.T, chunks <-chan ChatChunk, limited bool) {
	t.Helper()
	var failure error
	var text strings.Builder
	completed := 0
	var usage *Usage
	for chunk := range chunks {
		if len(chunk.ToolCalls) != 0 || chunk.Replay != nil {
			t.Fatal("framing fixture exposed accepted tools or replay")
		}
		text.WriteString(chunk.Text)
		if chunk.Done {
			completed++
			usage = chunk.Usage
		}
		if chunk.Err != nil {
			failure = chunk.Err
			if strings.Contains(failure.Error(), "sse-bounds-private-canary") {
				t.Fatal("untrusted SSE data leaked through error text")
			}
		}
	}
	if limited {
		if completed != 0 || ProviderErrorKind(failure) != OutcomeInvalidResponse ||
			ProviderErrorReason(failure) != ProviderFailureProtocolIncompatible || !strings.Contains(failure.Error(), "limit") {
			t.Fatalf("oversized event was parsed or accepted: completions=%d err=%v", completed, failure)
		}
	} else if failure != nil || completed != 1 || text.String() != "bounded reply" ||
		usage == nil || *usage != (Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}) {
		t.Fatalf("legal framing changed: completions=%d text=%q usage=%+v err=%v", completed, text.String(), usage, failure)
	}
}

func TestProviderSSEAggregateHTTPBoundariesAndReset(t *testing.T) {
	for _, kind := range []string{"anthropic", "responses"} {
		prefix, suffix, ignoredType := sseBoundsFrames(kind)
		for _, test := range []struct {
			name, middle string
			limited      bool
		}{
			{"bytes_below", sseBoundsPaddedEvent(kind, maxProviderSSEEventBytes-1), false},
			{"bytes_equal", sseBoundsPaddedEvent(kind, maxProviderSSEEventBytes), false},
			{"bytes_above", sseBoundsPaddedEvent(kind, maxProviderSSEEventBytes+1), true},
			{"bytes_reset", strings.Repeat(sseBoundsPaddedEvent(kind, maxProviderSSEEventBytes), 3), false},
			{"lines_below", strings.Repeat("data:\n", maxProviderSSEDataLines-2) + fmt.Sprintf("data: {\"type\":%q}\n\n", ignoredType), false},
			{"lines_equal", strings.Repeat("data: \t \n", maxProviderSSEDataLines-1) + fmt.Sprintf("data: {\"type\":%q}\n\n", ignoredType), false},
			{"lines_above", strings.Repeat("data:\n", maxProviderSSEDataLines) + fmt.Sprintf("data: {\"type\":%q}\n\n", ignoredType), true},
			{"empty_unterminated_above", strings.Repeat("data:\n", maxProviderSSEDataLines+1), true},
			{"mixed_reset_crlf_comments", strings.Repeat(": heartbeat\r\nevent: ignored\r\ndata: \t\r\ndata: {\"type\":\""+ignoredType+"\"}\r\n\r\n", 8), false},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, prefix+test.middle+suffix)
				}))
				defer server.Close()
				chunks, err := sseBoundsProvider(t, kind, server.URL).StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
				if err != nil {
					t.Fatal(err)
				}
				verifySSEBoundsResult(t, chunks, test.limited)
			})
		}
	}
}

func TestProviderSSERejectsBeforeEventDelimiterAndClosesHTTPBody(t *testing.T) {
	for _, kind := range []string{"anthropic", "responses", "chat"} {
		t.Run(kind, func(t *testing.T) {
			release := make(chan struct{})
			bodyClosed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				frame := sseBoundsPaddedEvent(kind, maxProviderSSEEventBytes+1)
				_, _ = io.WriteString(w, strings.TrimSuffix(frame, "\n"))
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					close(bodyClosed)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			chunks, err := sseBoundsProvider(t, kind, server.URL).StreamChat(ctx, ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
			if err != nil {
				t.Fatal(err)
			}
			verifySSEBoundsResult(t, chunks, true)
			select {
			case <-bodyClosed:
			case <-ctx.Done():
				t.Fatal("limit rejection waited for delimiter/EOF or kept the HTTP body open")
			}
		})
	}
}

func TestProviderSSEPartialAggregateCancellationAndTruncation(t *testing.T) {
	for _, kind := range []string{"anthropic", "responses"} {
		for _, mode := range []string{"cancel", "truncate"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ready := make(chan struct{})
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					prefix, _, _ := sseBoundsFrames(kind)
					_, _ = io.WriteString(w, prefix+"data:\ndata: {\"type\":\"unterminated-private-canary\"")
					w.(http.Flusher).Flush()
					close(ready)
					if mode == "cancel" {
						select {
						case <-r.Context().Done():
						case <-release:
						}
					}
				}))
				defer server.Close()
				defer close(release)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				chunks, err := sseBoundsProvider(t, kind, server.URL).StreamChat(ctx, ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
				if err != nil {
					t.Fatal(err)
				}
				<-ready
				if mode == "cancel" {
					cancel()
				}
				var failure error
				for chunk := range chunks {
					if chunk.Done || len(chunk.ToolCalls) != 0 || chunk.Replay != nil {
						t.Fatal("partial event accepted a successful result")
					}
					if chunk.Err != nil {
						failure = chunk.Err
						if strings.Contains(failure.Error(), "unterminated-private-canary") {
							t.Fatal("partial SSE body leaked through error text")
						}
					}
				}
				if mode == "truncate" && ProviderErrorKind(failure) != OutcomeInvalidResponse {
					t.Fatalf("truncated event was not rejected: %v", failure)
				}
			})
		}
	}
}
