package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	reasoningAddedOpaque = "unfinished-added-opaque-canary"
	reasoningDoneOpaque  = "completed-done-opaque-canary"
	reasoningSummary     = "private-summary-canary"
)

func reasoningStatusItem(status any, omit bool, completed bool) map[string]any {
	item := map[string]any{"id": "reasoning_1", "type": "reasoning", "summary": []any{}}
	if !omit {
		item["status"] = status
	}
	if completed {
		item["encrypted_content"] = reasoningDoneOpaque
		item["summary"] = []any{map[string]any{"type": "summary_text", "text": reasoningSummary}}
	} else {
		item["encrypted_content"] = reasoningAddedOpaque
	}
	return item
}

func reasoningStatusSequence(mask int, tools bool) []map[string]any {
	added := reasoningStatusItem("in_progress", mask&1 != 0, false)
	done := reasoningStatusItem("completed", mask&2 != 0, true)
	terminal := reasoningStatusItem("completed", mask&4 != 0, true)
	publicAdded := map[string]any{"id": "message_1", "type": "message", "status": "in_progress", "role": "assistant"}
	publicDone := map[string]any{"id": "message_1", "type": "message", "status": "completed", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": "public reply"}}}
	if tools {
		publicAdded = map[string]any{"id": "function_1", "type": "function_call", "status": "in_progress",
			"call_id": "wire_call_1", "name": "read_file", "arguments": ""}
		publicDone = map[string]any{"id": "function_1", "type": "function_call", "status": "completed",
			"call_id": "wire_call_1", "name": "read_file", "arguments": "{\"path\":\"README.md\"}"}
	}
	return []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "response_1", "object": "response", "status": "in_progress", "model": "model-local"}},
		{"type": "response.output_item.added", "item": added},
		{"type": "response.output_item.done", "item": done},
		{"type": "response.output_item.added", "item": publicAdded},
		{"type": "response.output_item.done", "item": publicDone},
		{"type": "response.completed", "response": map[string]any{"id": "response_1", "object": "response", "status": "completed", "model": "model-local",
			"output": []any{terminal, publicDone}, "usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}},
	}
}

func writeReasoningStatusSequence(t *testing.T, w http.ResponseWriter, sequence []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range sequence {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
}

func TestResponsesReasoningOptionalStatusLifecycleAndReplay(t *testing.T) {
	for _, tools := range []bool{false, true} {
		for mask := 0; mask < 9; mask++ {
			t.Run(fmt.Sprintf("omitted_%03b/tools_%t", mask, tools), func(t *testing.T) {
				sequence := reasoningStatusSequence(mask, tools)
				if mask == 8 {
					sequence[1]["item"].(map[string]any)["status"] = nil
					sequence[2]["item"].(map[string]any)["status"] = nil
					sequence[5]["response"].(map[string]any)["output"].([]any)[0].(map[string]any)["status"] = nil
				}
				var requests atomic.Int32
				continuations := make(chan []map[string]any, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if requests.Add(1) == 1 {
						writeReasoningStatusSequence(t, w, sequence)
						return
					}
					var continuation []map[string]any
					if err := json.Unmarshal(body["input"], &continuation); err != nil {
						t.Error(err)
						return
					}
					continuations <- continuation
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "response_2", "object": "response", "status": "completed", "model": "model-local",
						"output": []any{map[string]any{"id": "message_2", "type": "message", "status": "completed", "role": "assistant",
							"content": []any{map[string]any{"type": "output_text", "text": "continued"}}}},
						"usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}})
				}))
				defer server.Close()
				provider := newTestResponsesProvider(t, server.URL)
				chunks, err := provider.StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
				if err != nil {
					t.Fatal(err)
				}
				accumulator, _ := NewItemStreamAccumulator("optional-status-fixture", provider.Name(), "model-local")
				var terminal *ChatChunk
				var text strings.Builder
				completions, toolCompletions := 0, 0
				for chunk := range chunks {
					if chunk.Err != nil {
						t.Fatalf("legal optional status failed: %v", chunk.Err)
					}
					if _, _, err := accumulator.Consume(chunk); err != nil {
						t.Fatalf("legal optional status broke the public item stream: %v", err)
					}
					text.WriteString(chunk.Text)
					raw, _ := json.Marshal(chunk.Events)
					for _, marker := range []string{reasoningAddedOpaque, reasoningDoneOpaque, reasoningSummary} {
						if strings.Contains(string(raw), marker) || strings.Contains(chunk.Text, marker) {
							t.Fatal("private reasoning escaped through public output")
						}
					}
					for _, event := range chunk.Events {
						if event.Type == StreamResponseCompleted {
							completions++
						}
						if event.Type == StreamToolCallCompleted {
							toolCompletions++
						}
					}
					if chunk.Done {
						copy := chunk
						terminal = &copy
					}
				}
				wantTools := 0
				if tools {
					wantTools = 1
				}
				if terminal == nil || terminal.Usage == nil || *terminal.Usage != (Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}) ||
					completions != 1 || toolCompletions != wantTools || len(terminal.ToolCalls) != wantTools {
					t.Fatalf("completion, calls or usage changed: terminal=%+v completions=%d tool_completions=%d", terminal, completions, toolCompletions)
				}
				if !tools {
					if text.String() != "public reply" || terminal.Replay != nil || terminal.FinishReason != FinishReasonStop {
						t.Fatalf("tool-free response retained private state: text=%q terminal=%+v", text.String(), terminal)
					}
					return
				}
				if terminal.Replay == nil || terminal.FinishReason != FinishReasonToolCalls {
					t.Fatalf("tool continuation lost private state: %+v", terminal)
				}
				prepared := append([]ToolCall(nil), terminal.ToolCalls...)
				prepared[0].ID = "durable_fixture_call"
				bound, err := terminal.Replay.BindToolCalls(prepared)
				if err != nil {
					t.Fatal(err)
				}
				stored, err := bound.EncodeForStore()
				if err != nil {
					t.Fatal(err)
				}
				reopened, err := DecodeProviderReplay(stored)
				if err != nil {
					t.Fatalf("optional status failed existing store replay decoding: %v", err)
				}
				response, err := provider.Chat(t.Context(), ChatRequest{Messages: []Message{
					{Role: "assistant", ToolCalls: prepared, Replay: reopened},
					{Role: "user", ToolResults: []ToolResult{{ToolCallID: prepared[0].ID, Content: "{}"}}},
				}})
				if err != nil || response == nil || response.Text != "continued" || requests.Load() != 2 {
					t.Fatalf("native continuation failed: response=%+v err=%v requests=%d", response, err, requests.Load())
				}
				continuation := <-continuations
				if len(continuation) != 3 || continuation[0]["type"] != "reasoning" ||
					continuation[0]["encrypted_content"] != reasoningDoneOpaque ||
					continuation[1]["type"] != "function_call" || continuation[1]["call_id"] != "wire_call_1" ||
					continuation[2]["type"] != "function_call_output" || continuation[2]["call_id"] != "wire_call_1" {
					t.Fatal("replay lost completed reasoning order or original call pairing")
				}
				_, statusPresent := continuation[0]["status"]
				if statusPresent != (mask&4 == 0 && mask != 8) {
					t.Fatalf("internal lifecycle rewrote the wire status: present=%t mask=%d", statusPresent, mask)
				}
				raw, _ := json.Marshal(continuation)
				if strings.Contains(string(raw), reasoningAddedOpaque) || strings.Contains(string(raw), prepared[0].ID) {
					t.Fatal("unfinished reasoning or durable call identity crossed the native boundary")
				}
			})
		}
	}
}

func TestResponsesReasoningOptionalStatusStillRejectsInvalidLifecycle(t *testing.T) {
	type invalidCase struct {
		name   string
		mutate func([]map[string]any) []map[string]any
		tail   string
	}
	var cases []invalidCase
	for _, phase := range []int{1, 2, 5} {
		for _, status := range []any{"", "invalid-status-canary", "in_progress", "incomplete", "completed", true, 7} {
			if phase == 1 && status == "in_progress" || phase != 1 && status == "completed" {
				continue
			}
			cases = append(cases, invalidCase{fmt.Sprintf("phase_%d_status_%v", phase, status),
				func(sequence []map[string]any) []map[string]any {
					if phase == 5 {
						sequence[5]["response"].(map[string]any)["output"].([]any)[0].(map[string]any)["status"] = status
					} else {
						sequence[phase]["item"].(map[string]any)["status"] = status
					}
					return sequence
				}, ""})
		}
	}
	cases = append(cases,
		invalidCase{"unknown_done_id", func(s []map[string]any) []map[string]any { s[2]["item"].(map[string]any)["id"] = "unknown"; return s }, ""},
		invalidCase{"changed_done_type", func(s []map[string]any) []map[string]any {
			s[2]["item"].(map[string]any)["type"] = "compaction"
			return s
		}, ""},
		invalidCase{"duplicate_done", func(s []map[string]any) []map[string]any {
			return append(append(append([]map[string]any{}, s[:3]...), s[2]), s[3:]...)
		}, ""},
		invalidCase{"unfinished_reasoning", func(s []map[string]any) []map[string]any { return append(s[:2:2], s[3:]...) }, ""},
		invalidCase{"missing_response_terminal", func(s []map[string]any) []map[string]any { return s[:5] }, ""},
		invalidCase{"truncated_response_terminal", func(s []map[string]any) []map[string]any { return s[:5] }, "data: {\"type\":\"response.completed\"\n\n"},
		invalidCase{"truncated_reasoning_done", func(s []map[string]any) []map[string]any { return s[:2] }, "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"reasoning_1\",\"type\":\"reasoning\",\"encrypted_content\":\"truncated-private-canary\"\n\n"},
		invalidCase{"terminal_changed_encrypted_content", func(s []map[string]any) []map[string]any {
			s[5]["response"].(map[string]any)["output"].([]any)[0].(map[string]any)["encrypted_content"] = "changed-private-canary"
			return s
		}, ""},
		invalidCase{"terminal_changed_order", func(s []map[string]any) []map[string]any {
			output := s[5]["response"].(map[string]any)["output"].([]any)
			output[0], output[1] = output[1], output[0]
			return s
		}, ""},
		invalidCase{"public_item_missing_added_status", func(s []map[string]any) []map[string]any { delete(s[3]["item"].(map[string]any), "status"); return s }, ""},
		invalidCase{"public_item_missing_done_status", func(s []map[string]any) []map[string]any { delete(s[4]["item"].(map[string]any), "status"); return s }, ""},
	)
	for _, tools := range []bool{false, true} {
		for _, test := range cases {
			t.Run(fmt.Sprintf("%s/tools_%t", test.name, tools), func(t *testing.T) {
				sequence := test.mutate(reasoningStatusSequence(7, tools))
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					writeReasoningStatusSequence(t, w, sequence)
					_, _ = fmt.Fprint(w, test.tail)
				}))
				defer server.Close()
				chunks, err := newTestResponsesProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
				if err != nil {
					t.Fatal(err)
				}
				var failure error
				for chunk := range chunks {
					if chunk.Done || len(chunk.ToolCalls) != 0 || chunk.Replay != nil {
						t.Fatal("invalid lifecycle exposed an accepted result")
					}
					if chunk.Err != nil {
						failure = chunk.Err
					}
				}
				if ProviderErrorKind(failure) != OutcomeInvalidResponse || ProviderErrorReason(failure) != ProviderFailureProtocolIncompatible {
					t.Fatalf("invalid lifecycle did not fail closed: %v", failure)
				}
			})
		}
	}
}

func TestResponsesReasoningOptionalStatusPreservesFailedTerminals(t *testing.T) {
	for _, mode := range []string{"failed", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			sequence := reasoningStatusSequence(7, true)
			sequence = append(sequence[:2:2], sequence[3:5]...)
			response := map[string]any{"id": "response_1", "object": "response", "status": mode, "model": "model-local",
				"usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}
			wantKind, wantReason := OutcomeRetryable, ProviderFailureCapacity
			if mode == "failed" {
				response["error"] = map[string]any{"type": "server_error", "code": "server_error", "message": "private-error-canary"}
			} else {
				response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
				wantKind, wantReason = OutcomePermanent, ProviderFailureOutputLimit
			}
			sequence = append(sequence, map[string]any{"type": "response." + mode, "response": response})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeReasoningStatusSequence(t, w, sequence) }))
			defer server.Close()
			chunks, err := newTestResponsesProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
			if err != nil {
				t.Fatal(err)
			}
			var terminal *ChatChunk
			accumulator, _ := NewItemStreamAccumulator("failed-status-fixture", "responses-test", "model-local")
			for chunk := range chunks {
				if _, _, err := accumulator.Consume(chunk); err != nil {
					t.Fatal(err)
				}
				if chunk.Done || len(chunk.ToolCalls) != 0 || chunk.Replay != nil {
					t.Fatal("failed terminal accepted partial tools")
				}
				if chunk.Err != nil {
					copy := chunk
					terminal = &copy
				}
			}
			if terminal == nil || terminal.Usage == nil || terminal.Usage.TotalTokens != 5 ||
				ProviderErrorKind(terminal.Err) != wantKind || ProviderErrorReason(terminal.Err) != wantReason {
				t.Fatalf("failed terminal lost usage or classification: %+v", terminal)
			}
		})
	}
}
