package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	eventtypes "cyberagent-workbench/internal/events"
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
		argumentsFlow = make(chan struct{})
		releaseFinish = make(chan struct{})
		releaseOnce   sync.Once
	)
	releaseProvider := func() { releaseOnce.Do(func() { close(releaseFinish) }) }
	defer releaseProvider()
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
		if call == 0 {
			writer.Header().Set("Content-Type", "text/event-stream")
			encoded, encodeErr := json.Marshal(events[0])
			if encodeErr == nil {
				_, encodeErr = fmt.Fprintf(writer, "data: %s\n\n", encoded)
			}
			if encodeErr != nil {
				mu.Lock()
				handlerErrors = append(handlerErrors, encodeErr)
				mu.Unlock()
				return
			}
			writer.(http.Flusher).Flush()
			close(argumentsFlow)
			<-releaseFinish
			events = events[1:]
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
	supervisor := application.NewRunSupervisor(st, router, policy.NewDefaultChecker())
	type stepResult struct {
		result application.LifecycleResult
		err    error
	}
	done := make(chan stepResult, 1)
	go func() {
		result, err := supervisor.Step(context.Background(), run.ID)
		done <- stepResult{result: result, err: err}
	}()
	select {
	case <-argumentsFlow:
	case <-time.After(2 * time.Second):
		t.Fatal("OpenAI tool arguments did not begin streaming")
	}
	deadline := time.Now().Add(2 * time.Second)
	var live application.PublicModelStreamSnapshot
	for {
		var found bool
		live, found = supervisor.PublicModelStream(run.ID)
		if found && len(live.Items) == 1 && live.Items[0].ArgumentBytes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tool preparation did not reach the public item projection: %#v", live)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if item := live.Items[0]; item.Type != llm.StreamItemToolCall ||
		item.Status != llm.StreamItemInProgress || item.ToolName != "work_item_create" ||
		!item.Provisional || item.Durable || item.DurableCallID != "" {
		t.Fatalf("unsafe live tool preparation item: %#v", item)
	}
	before, err := st.ListWorkItems(context.Background(), domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(before) != 0 {
		t.Fatalf("tool executed before its complete call was validated: items=%#v err=%v", before, err)
	}
	releaseProvider()
	var completed stepResult
	select {
	case completed = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Supervisor did not finish the OpenAI tool round trip")
	}
	result, err := completed.result, completed.err
	if err != nil {
		mu.Lock()
		deferredErrors := append([]error(nil), handlerErrors...)
		requestCount := len(requests)
		mu.Unlock()
		var causes []error
		for current := err; current != nil; current = errors.Unwrap(current) {
			causes = append(causes, current)
		}
		t.Fatalf("Supervisor OpenAI tool round trip failed: %v (result: %#v; requests: %d; causes: %v; mock errors: %v)",
			err, result, requestCount, causes, deferredErrors)
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
	timeline, err := st.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := map[string]int{
		eventtypes.SupervisorToolExecutionStartedEvent:   1,
		eventtypes.SupervisorToolExecutionCompletedEvent: 1,
		eventtypes.SupervisorToolResultEvent:             1,
	}
	identities := map[string]string{}
	for _, event := range timeline {
		if _, tracked := wantEvents[event.Type]; !tracked {
			continue
		}
		wantEvents[event.Type]--
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"stream_response_id", "stream_item_id", "stream_call_id",
			"durable_call_id"} {
			value, _ := payload[field].(string)
			if value == "" {
				t.Fatalf("%s omitted %s: %s", event.Type, field, event.PayloadJSON)
			}
			if prior := identities[field]; prior != "" && prior != value {
				t.Fatalf("%s changed from %q to %q", field, prior, value)
			}
			identities[field] = value
		}
		if event.Type == eventtypes.SupervisorToolExecutionStartedEvent ||
			event.Type == eventtypes.SupervisorToolExecutionCompletedEvent {
			wantType := string(llm.StreamToolExecutionStarted)
			if event.Type == eventtypes.SupervisorToolExecutionCompletedEvent {
				wantType = string(llm.StreamToolExecutionCompleted)
			}
			if payload["item_stream_version"] != llm.ItemStreamProtocolVersion ||
				payload["item_event_type"] != wantType || payload["item_type"] != "tool_call" ||
				payload["durable"] != true || payload["provisional"] != false {
				t.Fatalf("execution event omitted its durable item projection: %s", event.PayloadJSON)
			}
		}
		if strings.Contains(event.PayloadJSON, "provider-call-1") ||
			strings.Contains(event.PayloadJSON, itemTitle) {
			t.Fatalf("provider identity or arguments leaked into execution event: %s", event.PayloadJSON)
		}
	}
	for eventType, remaining := range wantEvents {
		if remaining != 0 {
			t.Fatalf("event %s count mismatch: remaining=%d", eventType, remaining)
		}
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
