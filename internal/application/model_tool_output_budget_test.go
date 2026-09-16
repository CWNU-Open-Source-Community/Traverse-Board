package application

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func fileGeneratingModelRequest() llm.ChatRequest {
	return llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: "Propose a test file."}},
		Tools:    []llm.ToolSpec{{Name: "workspace_change", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

func TestModelToolOutputBudgetReservesExistingCapAndHonorsRemainingBudget(t *testing.T) {
	window := llm.DefaultContextWindow()
	for _, tc := range []struct {
		name   string
		budget domain.Budget
		used   int64
		want   int
	}{
		{name: "no aggregate token limit", want: 4096},
		{name: "small remaining budget", budget: domain.Budget{MaxTokens: 600}, used: 400, want: 200},
		{name: "large remaining budget remains capped", budget: domain.Budget{MaxTokens: 100000}, want: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := supervisorRequestWithinBudget(fileGeneratingModelRequest(), tc.budget,
				domain.SupervisorCheckpoint{TotalTokens: tc.used})
			if err != nil {
				t.Fatal(err)
			}
			request, plan, err := constrainRequestToModelWindow(request, window, modelContextLayout{})
			if err != nil {
				t.Fatal(err)
			}
			if request.MaxTokens != tc.want || plan.OutputLimitTokens != tc.want ||
				plan.InputLimitTokens+plan.OutputLimitTokens+plan.SafetyMarginTokens != window.WindowTokens ||
				plan.EstimatedInput > plan.InputLimitTokens {
				t.Fatalf("unsafe output allocation: request=%d plan=%#v", request.MaxTokens, plan)
			}
		})
	}
	request := fileGeneratingModelRequest()
	request.MaxTokens = 128
	bounded, _, err := constrainRequestToModelWindow(request, window, modelContextLayout{})
	if err != nil || bounded.MaxTokens != 128 {
		t.Fatalf("explicit output allowance changed: %d, %v", bounded.MaxTokens, err)
	}
	request.Tools, request.MaxTokens = nil, 0
	bounded, _, err = constrainRequestToModelWindow(request, window, modelContextLayout{})
	if err != nil || bounded.MaxTokens != 1024 {
		t.Fatalf("plain reply default changed: %d, %v", bounded.MaxTokens, err)
	}
	if _, err := supervisorRequestWithinBudget(fileGeneratingModelRequest(), domain.Budget{MaxTokens: 400},
		domain.SupervisorCheckpoint{TotalTokens: 400}); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("exhausted budget accepted: %v", err)
	}
}

func TestModelToolOutputReserveStillRejectsMandatoryContextOverflow(t *testing.T) {
	window := llm.DefaultContextWindow()
	request := fileGeneratingModelRequest()
	// This input fits the old short-reply allocation but cannot safely coexist
	// with the output space reserved for an actual file proposal.
	request.Messages[0].Content = strings.Repeat("x", 29000*4)
	if _, _, err := constrainRequestToModelWindow(request, window, modelContextLayout{}); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("mandatory context consumed reserved output capacity: %v", err)
	}
}

func TestModelToolOutputBudgetReachesResponsesWireAndPreservesLargeFileArguments(t *testing.T) {
	content := strings.Repeat("// generated test line\n", 300)
	arguments, _ := json.Marshal(map[string]any{"version": "agent-code-tools.v1", "action": "create", "path": "test/cli.test.mjs", "expected_sha256": "missing", "content": content})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.MaxOutputTokens != 4096 {
			t.Errorf("file-generation wire allowance=%d, want 4096", body.MaxOutputTokens)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(v any) { b, _ := json.Marshal(v); _, _ = fmt.Fprintf(w, "data: %s\n\n", b) }
		write(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_file", "object": "response", "status": "in_progress", "model": "test-model"}})
		item := map[string]any{"id": "fc_file", "type": "function_call", "status": "in_progress", "call_id": "call_file", "name": "workspace_change", "arguments": ""}
		write(map[string]any{"type": "response.output_item.added", "item": item})
		write(map[string]any{"type": "response.function_call_arguments.delta", "item_id": "fc_file", "delta": string(arguments)})
		write(map[string]any{"type": "response.function_call_arguments.done", "item_id": "fc_file", "name": "workspace_change", "arguments": string(arguments)})
		item["status"], item["arguments"] = "completed", string(arguments)
		write(map[string]any{"type": "response.output_item.done", "item": item})
		write(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_file", "object": "response", "status": "completed", "model": "test-model", "usage": map[string]any{"input_tokens": 100, "output_tokens": 1800, "total_tokens": 1900}}})
	}))
	defer server.Close()
	provider, err := llm.NewOpenAIResponsesProvider(llm.OpenAIResponsesConfig{Name: "test", BaseURL: server.URL, APIKey: "synthetic", DefaultModel: "test-model", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	request, err := supervisorRequestWithinBudget(fileGeneratingModelRequest(), domain.Budget{}, domain.SupervisorCheckpoint{})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err = constrainRequestToModelWindow(request, llm.DefaultContextWindow(), modelContextLayout{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.StreamChat(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var final *llm.ChatChunk
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		if chunk.Done {
			copy := chunk
			final = &copy
		}
	}
	if final == nil || final.Usage.OutputTokens != 1800 || len(final.ToolCalls) != 1 || string(final.ToolCalls[0].Arguments) != string(arguments) {
		t.Fatal("large function arguments were truncated or terminal usage was lost")
	}
}
