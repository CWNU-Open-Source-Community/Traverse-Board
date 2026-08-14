package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func TestOpenAICompatibleProviderRunSupervisorToolRoundTrip(t *testing.T) {
	const (
		providerName = "openai-supervisor-test"
		modelName    = "model"
		itemTitle    = "OpenAI integration item"
		finalMessage = "tool transcript accepted"
	)

	var (
		mu            sync.Mutex
		requests      []openAISupervisorWireRequest
		handlerErrors []error
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer test-secret" {
			http.Error(writer, "unexpected authorization header", http.StatusBadRequest)
			return
		}
		var wire openAISupervisorWireRequest
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			http.Error(writer, "invalid request JSON", http.StatusBadRequest)
			return
		}

		mu.Lock()
		call := len(requests)
		requests = append(requests, wire)
		mu.Unlock()

		var validationErr error
		switch call {
		case 0:
			validationErr = validateOpenAISupervisorFirstRequest(wire)
		case 1:
			validationErr = validateOpenAISupervisorToolResultRequest(wire, itemTitle)
		default:
			validationErr = fmt.Errorf("unexpected provider call %d", call+1)
		}
		if validationErr != nil {
			mu.Lock()
			handlerErrors = append(handlerErrors, validationErr)
			mu.Unlock()
			http.Error(writer, "wire protocol validation failed", http.StatusBadRequest)
			return
		}

		var events []openAISupervisorStreamEvent
		if call == 0 {
			finish := "tool_calls"
			events = []openAISupervisorStreamEvent{
				{
					Model: modelName,
					Choices: []openAISupervisorStreamChoice{{
						Index: 0,
						Delta: openAISupervisorStreamDelta{
							Role: "assistant",
							ToolCalls: []openAISupervisorStreamToolCall{{
								Index: 0, ID: "provider-call-1", Type: "function",
								Function: openAISupervisorWireFunction{
									Name:      "work_item_create",
									Arguments: `{"title":"OpenAI integration item","priority":"high"}`,
								},
							}},
						},
					}},
				},
				{Model: modelName, Choices: []openAISupervisorStreamChoice{{
					Index: 0, FinishReason: &finish,
				}}},
				{Model: modelName, Usage: &openAISupervisorWireUsage{
					PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
				}},
			}
		} else {
			finish := "stop"
			content := rootActionResponse(domain.RootActionContinue, finalMessage, "", "")
			events = []openAISupervisorStreamEvent{
				{Model: modelName, Choices: []openAISupervisorStreamChoice{{
					Index: 0, Delta: openAISupervisorStreamDelta{Role: "assistant", Content: &content},
				}}},
				{Model: modelName, Choices: []openAISupervisorStreamChoice{{
					Index: 0, FinishReason: &finish,
				}}},
				{Model: modelName, Usage: &openAISupervisorWireUsage{
					PromptTokens: 12, CompletionTokens: 6, TotalTokens: 18,
				}},
			}
		}
		if err := writeOpenAISupervisorStream(writer, events); err != nil {
			mu.Lock()
			handlerErrors = append(handlerErrors, err)
			mu.Unlock()
		}
	}))
	defer server.Close()

	provider, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{
		Name: providerName, BaseURL: server.URL, APIKey: "test-secret", DefaultModel: modelName,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: providerName, Model: modelName})
	router.RegisterProvider(provider)
	ref := llm.ModelRef{Provider: providerName, Model: modelName}
	profile, err := router.HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := router.SetHarnessQualification(ref, llm.HarnessQualification{
		ProtocolVersion:      llm.ModelHarnessProtocolVersion,
		BindingDigest:        profile.BindingDigest,
		ToolCallsQualified:   true,
		ToolResultsQualified: true,
		StrictJSONQualified:  true,
		StreamingQualified:   true,
		QualifiedAt:          now,
		ExpiresAt:            now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "openai-supervisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := newStartedRunForProvider(t, st, providerName,
		domain.Budget{MaxTurns: 3, MaxToolCalls: 5})
	result, err := application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).
		Step(context.Background(), run.ID)
	if err != nil {
		mu.Lock()
		deferredErrors := append([]error(nil), handlerErrors...)
		mu.Unlock()
		t.Fatalf("Supervisor OpenAI tool round trip failed: %v (mock errors: %v)", err, deferredErrors)
	}
	if result.Status != application.LifecycleTurnCompleted || result.ToolRounds != 1 ||
		result.ToolCalls != 1 || result.ModelAttempts != 2 || result.Text != finalMessage {
		t.Fatalf("unexpected Supervisor result: %#v", result)
	}

	mu.Lock()
	captured := append([]openAISupervisorWireRequest(nil), requests...)
	capturedErrors := append([]error(nil), handlerErrors...)
	mu.Unlock()
	if len(capturedErrors) != 0 {
		t.Fatalf("mock server rejected OpenAI wire requests: %v", capturedErrors)
	}
	if len(captured) != 2 {
		t.Fatalf("provider call count = %d, want 2", len(captured))
	}

	items, err := st.ListWorkItems(context.Background(), domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 || items[0].Title != itemTitle {
		t.Fatalf("tool mutation was not executed exactly once: items=%#v err=%v", items, err)
	}
	messages, err := st.ListSessionMessages(context.Background(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" ||
		messages[1].Content != finalMessage {
		t.Fatalf("Session did not commit exactly one user/assistant pair: %#v", messages)
	}
}

type openAISupervisorWireRequest struct {
	Model          string                        `json:"model"`
	Messages       []openAISupervisorWireMessage `json:"messages"`
	Tools          []openAISupervisorWireTool    `json:"tools"`
	Stream         bool                          `json:"stream"`
	StreamOptions  *openAISupervisorStreamOption `json:"stream_options"`
	ResponseFormat *openAISupervisorFormat       `json:"response_format"`
}

type openAISupervisorWireMessage struct {
	Role       string                     `json:"role"`
	Content    *string                    `json:"content"`
	ToolCalls  []openAISupervisorWireCall `json:"tool_calls"`
	ToolCallID string                     `json:"tool_call_id"`
}

type openAISupervisorWireTool struct {
	Type     string                       `json:"type"`
	Function openAISupervisorWireFunction `json:"function"`
}

type openAISupervisorWireCall struct {
	ID       string                       `json:"id"`
	Type     string                       `json:"type"`
	Function openAISupervisorWireFunction `json:"function"`
}

type openAISupervisorWireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAISupervisorStreamOption struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAISupervisorFormat struct {
	Type string `json:"type"`
}

type openAISupervisorWireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAISupervisorStreamEvent struct {
	Model   string                         `json:"model"`
	Choices []openAISupervisorStreamChoice `json:"choices"`
	Usage   *openAISupervisorWireUsage     `json:"usage,omitempty"`
}

type openAISupervisorStreamChoice struct {
	Index        int                         `json:"index"`
	Delta        openAISupervisorStreamDelta `json:"delta"`
	FinishReason *string                     `json:"finish_reason,omitempty"`
}

type openAISupervisorStreamDelta struct {
	Role      string                           `json:"role,omitempty"`
	Content   *string                          `json:"content,omitempty"`
	ToolCalls []openAISupervisorStreamToolCall `json:"tool_calls,omitempty"`
}

type openAISupervisorStreamToolCall struct {
	Index    int                          `json:"index"`
	ID       string                       `json:"id"`
	Type     string                       `json:"type"`
	Function openAISupervisorWireFunction `json:"function"`
}

func validateOpenAISupervisorFirstRequest(request openAISupervisorWireRequest) error {
	if request.Model != "model" || !request.Stream || request.StreamOptions == nil ||
		!request.StreamOptions.IncludeUsage || request.ResponseFormat == nil ||
		request.ResponseFormat.Type != "json_object" {
		return errors.New("initial request omitted the qualified streaming JSON contract")
	}
	toolFound := false
	for _, tool := range request.Tools {
		if tool.Type == "function" && tool.Function.Name == "work_item_create" {
			toolFound = true
		}
	}
	if !toolFound {
		return errors.New("initial request omitted the allowlisted work_item_create tool")
	}
	for _, message := range request.Messages {
		if message.Role == "tool" || len(message.ToolCalls) != 0 {
			return errors.New("initial request unexpectedly contained a prior tool transcript")
		}
	}
	return nil
}

func validateOpenAISupervisorToolResultRequest(request openAISupervisorWireRequest,
	wantTitle string,
) error {
	if request.Model != "model" || !request.Stream || request.StreamOptions == nil ||
		!request.StreamOptions.IncludeUsage || request.ResponseFormat == nil ||
		request.ResponseFormat.Type != "json_object" {
		return errors.New("follow-up request omitted the qualified streaming JSON contract")
	}
	for index := 0; index+1 < len(request.Messages); index++ {
		assistant := request.Messages[index]
		toolResult := request.Messages[index+1]
		if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 || toolResult.Role != "tool" {
			continue
		}
		call := assistant.ToolCalls[0]
		if call.ID == "" || call.Type != "function" || call.Function.Name != "work_item_create" ||
			toolResult.ToolCallID != call.ID || toolResult.Content == nil {
			return errors.New("assistant tool call and role=tool result were not paired")
		}
		var arguments struct {
			Title    string `json:"title"`
			Priority string `json:"priority"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil ||
			arguments.Title != wantTitle || arguments.Priority != "high" {
			return errors.New("assistant tool-call arguments were not preserved")
		}
		var result struct {
			Version  string            `json:"version"`
			Tool     string            `json:"tool"`
			Status   string            `json:"status"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(*toolResult.Content), &result); err != nil ||
			result.Version != "supervisor_tool_result.v1" || result.Tool != "work_item_create" ||
			result.Status != "completed" || result.Metadata["entity_id"] == "" {
			return errors.New("role=tool message did not contain the successful tool result")
		}
		return nil
	}
	return errors.New("follow-up request did not contain adjacent assistant tool_calls and role=tool messages")
}

func writeOpenAISupervisorStream(writer http.ResponseWriter,
	events []openAISupervisorStreamEvent,
) error {
	writer.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "data: %s\n\n", encoded); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(writer, "data: [DONE]\n\n")
	return err
}
