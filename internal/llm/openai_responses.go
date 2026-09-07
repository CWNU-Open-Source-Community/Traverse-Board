package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"
)

// OpenAIResponsesConfig configures an OpenAI Responses compatible endpoint.
// It intentionally installs no hosted web-search tool; native search is a
// separately qualified capability with its own execution authority.
type OpenAIResponsesConfig struct {
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
	HTTPClient   *http.Client
	Runtime      HTTPProviderRuntime
}

type OpenAIResponsesProvider struct {
	name         string
	baseURL      string
	apiKey       string
	defaultModel string
	client       *http.Client
	runtime      HTTPProviderRuntime
}

func NewOpenAIResponsesProvider(config OpenAIResponsesConfig) (*OpenAIResponsesProvider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "openai_responses"
	}
	baseURL, err := normalizeProviderBaseURL(config.BaseURL, name)
	if err != nil {
		return nil, err
	}
	if config.Runtime == nil {
		if err := validateProviderAPIKey(config.APIKey, name); err != nil {
			return nil, err
		}
	} else if err := validateHTTPProviderRuntime(config.Runtime); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(config.DefaultModel)
	if model == "" {
		model = defaultOpenAIModel
	}
	if _, err := normalizeOpenAIModel(model); err != nil {
		return nil, fmt.Errorf("default model for provider %s is invalid", name)
	}
	return &OpenAIResponsesProvider{
		name: name, baseURL: baseURL, apiKey: config.APIKey,
		defaultModel: model, client: providerHTTPClient(config.HTTPClient),
		runtime: config.Runtime,
	}, nil
}

func (p *OpenAIResponsesProvider) Name() string { return p.name }

func (p *OpenAIResponsesProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	secret, err := providerRequestCredential(ctx, p.name, p.apiKey, p.runtime)
	if err != nil {
		return nil, openAILocalError(p.name, "provider credential is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.modelsEndpoint(), nil)
	if err != nil {
		return nil, openAILocalError(p.name, "could not create model-list request")
	}
	if err := p.addHeaders(request, false, secret); err != nil {
		return nil, openAILocalError(p.name, "could not prepare model-list headers")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return nil, openAITransportError(ctx, p.name)
	}
	defer response.Body.Close()
	raw, err := readOpenAIBody(response.Body)
	if err != nil {
		return nil, openAIReadError(ctx, p.name, "could not read model-list response", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, openAIHTTPError(p.name, response.StatusCode,
			response.Header.Get("Retry-After"), raw)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Error *openAIError `json:"error,omitempty"`
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &payload) != nil || payload.Error != nil ||
		len(payload.Data) == 0 || len(payload.Data) > maxOpenAIModels {
		return nil, openAIProtocolError(p.name, "returned an invalid model list")
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, item := range payload.Data {
		id, err := normalizeOpenAIModel(item.ID)
		if err != nil {
			return nil, openAIProtocolError(p.name, "returned an invalid model identifier")
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, ModelInfo{ID: id, DisplayName: id, Provider: p.name})
	}
	if len(models) == 0 {
		return nil, openAIProtocolError(p.name, "returned an empty model list")
	}
	return models, nil
}

func (p *OpenAIResponsesProvider) Chat(ctx context.Context,
	request ChatRequest,
) (*ChatResponse, error) {
	selectedModel, wire, err := p.prepareRequest(request, false)
	if err != nil {
		return nil, openAILocalError(p.name, "could not prepare Responses request")
	}
	secret, err := providerRequestCredential(ctx, p.name, p.apiKey, p.runtime)
	if err != nil {
		return nil, openAILocalError(p.name, "provider credential is unavailable")
	}
	payload, err := providerRequestPayload(p.runtime, secret, wire)
	if err != nil {
		return nil, openAILocalError(p.name, "could not encode Responses request")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.responsesEndpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, openAILocalError(p.name, "could not create Responses request")
	}
	if err := p.addHeaders(httpRequest, false, secret); err != nil {
		return nil, openAILocalError(p.name, "could not prepare Responses request headers")
	}
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, openAITransportError(ctx, p.name)
	}
	defer response.Body.Close()
	raw, err := readOpenAIBody(response.Body)
	if err != nil {
		return nil, openAIReadError(ctx, p.name, "could not read Responses response", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, openAIHTTPError(p.name, response.StatusCode,
			response.Header.Get("Retry-After"), raw)
	}
	if !utf8.Valid(raw) {
		return nil, openAIProtocolError(p.name, "returned non-UTF-8 Responses JSON")
	}
	var parsed openAIResponsesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, openAIProtocolError(p.name, "returned malformed Responses JSON")
	}
	return p.normalizeResponse(selectedModel, parsed)
}

func (p *OpenAIResponsesProvider) StreamChat(ctx context.Context,
	request ChatRequest,
) (<-chan ChatChunk, error) {
	selectedModel, wire, err := p.prepareRequest(request, true)
	if err != nil {
		return nil, openAILocalError(p.name, "could not prepare streaming Responses request")
	}
	secret, err := providerRequestCredential(ctx, p.name, p.apiKey, p.runtime)
	if err != nil {
		return nil, openAILocalError(p.name, "provider credential is unavailable")
	}
	payload, err := providerRequestPayload(p.runtime, secret, wire)
	if err != nil {
		return nil, openAILocalError(p.name, "could not encode streaming Responses request")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.responsesEndpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, openAILocalError(p.name, "could not create streaming Responses request")
	}
	if err := p.addHeaders(httpRequest, true, secret); err != nil {
		return nil, openAILocalError(p.name, "could not prepare streaming Responses headers")
	}
	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, openAITransportError(ctx, p.name)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		raw, readErr := readOpenAIBody(response.Body)
		if readErr != nil {
			return nil, openAIReadError(ctx, p.name, "could not read Responses error", readErr)
		}
		return nil, openAIHTTPError(p.name, response.StatusCode,
			response.Header.Get("Retry-After"), raw)
	}
	chunks := make(chan ChatChunk, 8)
	go p.readStream(ctx, response.Body, selectedModel, chunks)
	return chunks, nil
}

func (p *OpenAIResponsesProvider) SupportsTools(model string) bool {
	_, err := normalizeOpenAIModel(strings.TrimSpace(model))
	return err == nil
}

func (*OpenAIResponsesProvider) SupportsVision(string) bool { return false }

func (p *OpenAIResponsesProvider) SupportsJSONMode(model string) bool {
	return p.SupportsTools(model)
}

func (p *OpenAIResponsesProvider) DescribeModelHarness(model string) ModelHarness {
	model = strings.TrimSpace(model)
	if model == "" {
		model = p.defaultModel
	}
	return ModelHarness{
		ProtocolVersion:   ModelHarnessProtocolVersion,
		TransportProtocol: HarnessTransportOpenAIResponses,
		ToolStrategy:      HarnessToolStrategyNative, JSONStrategy: HarnessJSONStrategyNative,
		QualificationStatus: HarnessQualificationRequired,
		BindingDigest: providerHarnessBinding(p.runtime, p.name, p.baseURL, model,
			HarnessTransportOpenAIResponses, HarnessToolStrategyNative,
			HarnessJSONStrategyNative),
	}
}

func (p *OpenAIResponsesProvider) prepareRequest(request ChatRequest,
	stream bool,
) (string, openAIResponsesRequest, error) {
	selectedModel := strings.TrimSpace(request.Model)
	if selectedModel == "" {
		selectedModel = p.defaultModel
	}
	var err error
	selectedModel, err = normalizeOpenAIModel(selectedModel)
	if err != nil {
		return "", openAIResponsesRequest{}, err
	}
	wireModel, err := providerRequestModel(p.runtime, selectedModel)
	if err != nil {
		return "", openAIResponsesRequest{}, err
	}
	wireModel, err = normalizeOpenAIModel(wireModel)
	if err != nil {
		return "", openAIResponsesRequest{}, err
	}
	if math.IsNaN(request.Temperature) || math.IsInf(request.Temperature, 0) ||
		request.Temperature < 0 || request.Temperature > 2 {
		return "", openAIResponsesRequest{}, errors.New("temperature is outside the supported range")
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	if maxTokens > 1_000_000 {
		return "", openAIResponsesRequest{}, errors.New("max output tokens exceeds the provider request limit")
	}
	wire := openAIResponsesRequest{Model: wireModel, MaxOutputTokens: maxTokens,
		Store: false, Stream: stream}
	if request.Temperature > 0 {
		wire.Temperature = &request.Temperature
	}
	if request.JSONMode {
		wire.Text = &openAIResponsesText{Format: openAIResponsesTextFormat{Type: "json_object"}}
	}
	for index, message := range request.Messages {
		items, err := openAIResponsesInput(message)
		if err != nil {
			return "", openAIResponsesRequest{}, fmt.Errorf("invalid message at index %d", index)
		}
		wire.Input = append(wire.Input, items...)
	}
	if len(wire.Input) == 0 {
		wire.Input = append(wire.Input, map[string]any{"role": "user", "content": "Hello"})
	}
	if len(request.Tools) > MaxProviderToolSpecs {
		return "", openAIResponsesRequest{}, errors.New("tool specification count exceeds the provider limit")
	}
	for index, spec := range request.Tools {
		name := strings.TrimSpace(spec.Name)
		parameters := append(json.RawMessage(nil), bytes.TrimSpace(spec.Parameters)...)
		if err := validateOpenAIToolName(name); err != nil || len(parameters) == 0 ||
			len(parameters) > MaxProviderToolPayloadSize || !utf8.Valid(parameters) ||
			!json.Valid(parameters) {
			return "", openAIResponsesRequest{}, fmt.Errorf("invalid tool specification at index %d", index)
		}
		wire.Tools = append(wire.Tools, openAIResponsesTool{Type: "function", Name: name,
			Description: strings.TrimSpace(spec.Description), Parameters: parameters})
	}
	return selectedModel, wire, nil
}

func openAIResponsesInput(message Message) ([]any, error) {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	content := strings.TrimSpace(message.Content)
	if content == "" && len(message.ToolCalls) == 0 && len(message.ToolResults) == 0 {
		return nil, nil
	}
	switch role {
	case "system":
		if content == "" || len(message.ToolCalls) != 0 || len(message.ToolResults) != 0 {
			return nil, errors.New("system message has invalid structured content")
		}
		return []any{map[string]any{"role": "system", "content": content}}, nil
	case "assistant":
		if len(message.ToolResults) != 0 {
			return nil, errors.New("assistant message cannot contain tool results")
		}
		calls, err := NormalizeToolCalls(message.ToolCalls)
		if err != nil {
			return nil, err
		}
		items := make([]any, 0, len(calls)+1)
		if content != "" {
			items = append(items, map[string]any{"role": "assistant", "content": content})
		}
		for _, call := range calls {
			items = append(items, map[string]any{"type": "function_call",
				"call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
		}
		return items, nil
	case "user":
		if len(message.ToolCalls) != 0 {
			return nil, errors.New("user message cannot contain tool calls")
		}
		items := make([]any, 0, len(message.ToolResults)+1)
		for _, result := range message.ToolResults {
			normalized, err := NormalizeToolResult(result)
			if err != nil {
				return nil, err
			}
			items = append(items, map[string]any{"type": "function_call_output",
				"call_id": normalized.ToolCallID, "output": normalized.Content})
		}
		if content != "" {
			items = append(items, map[string]any{"role": "user", "content": content})
		}
		if len(items) == 0 {
			return nil, errors.New("user message has no content")
		}
		return items, nil
	default:
		return nil, errors.New("message role is unsupported")
	}
}

func (p *OpenAIResponsesProvider) normalizeResponse(selectedModel string,
	response openAIResponsesResponse,
) (*ChatResponse, error) {
	if response.Object != "response" || response.Status != "completed" ||
		validateStreamIdentity(response.ID, "Responses response") != nil {
		return nil, openAIProtocolError(p.name, "returned an invalid Responses envelope")
	}
	if _, err := normalizeOpenAIModel(response.Model); err != nil {
		return nil, openAIProtocolError(p.name, "returned an invalid Responses model")
	}
	if response.Error != nil {
		return nil, openAIWireError(p.name, *response.Error)
	}
	if len(response.Output) == 0 || len(response.Output) > MaxProviderOutputItems {
		return nil, openAIProtocolError(p.name, "returned an invalid Responses output list")
	}
	var text strings.Builder
	calls := make([]ToolCall, 0)
	itemIDs := make(map[string]struct{}, len(response.Output))
	for _, item := range response.Output {
		if validateStreamIdentity(item.ID, "Responses output item") != nil {
			return nil, openAIProtocolError(p.name, "returned an invalid Responses output item identity")
		}
		if _, duplicate := itemIDs[item.ID]; duplicate {
			return nil, openAIProtocolError(p.name, "reused a Responses output item identity")
		}
		itemIDs[item.ID] = struct{}{}
		switch item.Type {
		case "message":
			if item.Status != "completed" || (item.Role != "" && item.Role != "assistant") {
				return nil, openAIProtocolError(p.name, "returned an invalid Responses message")
			}
			for _, part := range item.Content {
				if part.Type != "output_text" || !utf8.ValidString(part.Text) ||
					text.Len()+len(part.Text) > MaxModelOutputBytes {
					return nil, openAIProtocolError(p.name, "returned invalid Responses text")
				}
				_, _ = text.WriteString(part.Text)
			}
		case "function_call":
			if item.Status != "completed" {
				return nil, openAIProtocolError(p.name, "returned an incomplete Responses function call")
			}
			arguments := item.Arguments
			if strings.TrimSpace(arguments) == "" {
				arguments = `{}`
			}
			calls = append(calls, ToolCall{ID: item.CallID, Name: item.Name,
				Arguments: json.RawMessage(arguments)})
		case "reasoning", "compaction":
			// Private reasoning is deliberately not projected into the Harness.
		default:
			return nil, openAIProtocolError(p.name, "returned an unsupported Responses output item")
		}
	}
	normalizedCalls, err := NormalizeToolCalls(calls)
	if err != nil {
		return nil, openAIProtocolError(p.name, "returned invalid Responses function calls")
	}
	if text.Len() == 0 && len(normalizedCalls) == 0 {
		return nil, openAIProtocolError(p.name, "returned no usable Responses output")
	}
	usage, err := normalizeResponsesUsage(response.Usage)
	if err != nil {
		return nil, openAIProtocolError(p.name, "returned invalid Responses usage")
	}
	return &ChatResponse{ResponseID: response.ID, Text: text.String(),
		ToolCalls: normalizedCalls, Usage: usage, Model: selectedModel, Provider: p.name}, nil
}

func normalizeResponsesUsage(wire *openAIResponsesUsage) (Usage, error) {
	if wire == nil || wire.InputTokens == nil || wire.OutputTokens == nil ||
		wire.TotalTokens == nil {
		return Usage{}, errors.New("Responses token count is missing")
	}
	usage := Usage{InputTokens: *wire.InputTokens, OutputTokens: *wire.OutputTokens}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || *wire.TotalTokens < 0 {
		return Usage{}, errors.New("Responses token count is negative")
	}
	maxInt := int(^uint(0) >> 1)
	if usage.InputTokens > maxInt-usage.OutputTokens {
		return Usage{}, errors.New("Responses token count overflow")
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	if usage.TotalTokens != *wire.TotalTokens {
		return Usage{}, errors.New("Responses token count is inconsistent")
	}
	return usage, usage.Validate()
}

func (p *OpenAIResponsesProvider) responsesEndpoint() string {
	if strings.HasSuffix(p.baseURL, "/v1/responses") || strings.HasSuffix(p.baseURL, "/responses") {
		return p.baseURL
	}
	if strings.HasSuffix(p.baseURL, "/v1") {
		return p.baseURL + "/responses"
	}
	return p.baseURL + "/v1/responses"
}

func (p *OpenAIResponsesProvider) modelsEndpoint() string {
	if strings.HasSuffix(p.baseURL, "/v1/responses") {
		return strings.TrimSuffix(p.baseURL, "/responses") + "/models"
	}
	if strings.HasSuffix(p.baseURL, "/responses") {
		return strings.TrimSuffix(p.baseURL, "/responses") + "/models"
	}
	if strings.HasSuffix(p.baseURL, "/v1") {
		return p.baseURL + "/models"
	}
	return p.baseURL + "/v1/models"
}

func (p *OpenAIResponsesProvider) addHeaders(request *http.Request, stream bool,
	secret string,
) error {
	request.Header.Set("Content-Type", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	return applyProviderRequestHeaders(p.runtime, secret, request.Header)
}

type openAIResponsesRequest struct {
	Model           string                `json:"model"`
	Input           []any                 `json:"input"`
	Tools           []openAIResponsesTool `json:"tools,omitempty"`
	Text            *openAIResponsesText  `json:"text,omitempty"`
	Temperature     *float64              `json:"temperature,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens"`
	Store           bool                  `json:"store"`
	Stream          bool                  `json:"stream"`
}

type openAIResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIResponsesText struct {
	Format openAIResponsesTextFormat `json:"format"`
}

type openAIResponsesTextFormat struct {
	Type string `json:"type"`
}

type openAIResponsesUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
	TotalTokens  *int `json:"total_tokens"`
}

type openAIResponsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIResponsesOutputItem struct {
	ID        string                   `json:"id"`
	Type      string                   `json:"type"`
	Status    string                   `json:"status"`
	Role      string                   `json:"role"`
	Content   []openAIResponsesContent `json:"content"`
	CallID    string                   `json:"call_id"`
	Name      string                   `json:"name"`
	Arguments string                   `json:"arguments"`
}

type openAIResponsesResponse struct {
	ID     string                      `json:"id"`
	Object string                      `json:"object"`
	Status string                      `json:"status"`
	Model  string                      `json:"model"`
	Output []openAIResponsesOutputItem `json:"output"`
	Usage  *openAIResponsesUsage       `json:"usage"`
	Error  *openAIError                `json:"error"`
}

type openAIResponsesStreamEvent struct {
	Type      string                    `json:"type"`
	Response  openAIResponsesResponse   `json:"response"`
	Item      openAIResponsesOutputItem `json:"item"`
	ItemID    string                    `json:"item_id"`
	Delta     string                    `json:"delta"`
	Arguments string                    `json:"arguments"`
	Name      string                    `json:"name"`
	Error     *openAIError              `json:"error"`
}

type responsesStreamItem struct {
	typeName       StreamItemType
	wireType       string
	private        bool
	privateBytes   int
	callID         string
	name           string
	arguments      strings.Builder
	finalArguments string
	text           strings.Builder
	callCompleted  bool
	completed      bool
}

type responsesStreamState struct {
	provider      string
	selectedModel string
	responseID    string
	events        providerStreamEvents
	items         map[string]*responsesStreamItem
	toolCalls     []ToolCall
	started       bool
	terminal      bool
	publicItems   int
	wireEvents    int
}

func (s *responsesStreamState) consume(payload []byte) (*ChatChunk, bool, error) {
	s.wireEvents++
	if s.wireEvents > MaxItemStreamEvents || len(payload) > maxOpenAIStreamEventBytes {
		return nil, false, openAIProtocolError(s.provider, "Responses stream exceeded its event limit")
	}
	var event openAIResponsesStreamEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, false, openAIProtocolError(s.provider, "returned malformed Responses stream event")
	}
	switch event.Type {
	case "response.created":
		if s.started || event.Response.Object != "response" || event.Response.Status != "in_progress" ||
			validateStreamIdentity(event.Response.ID, "Responses stream response") != nil {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses stream start")
		}
		if _, err := normalizeOpenAIModel(event.Response.Model); err != nil {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses stream model")
		}
		s.started = true
		s.responseID = event.Response.ID
		s.events = newProviderStreamEvents(s.provider, s.selectedModel,
			s.responseID, StreamGranularityDelta)
		return &ChatChunk{Events: []StreamEvent{s.events.start()}}, false, nil
	case "response.in_progress", "response.content_part.added", "response.content_part.done",
		"response.output_text.done":
		if !s.started || s.terminal {
			return nil, false, openAIProtocolError(s.provider, "returned a Responses event outside an active response")
		}
		return nil, false, nil
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
		"response.reasoning_summary_text.done", "response.reasoning_text.done":
		item := s.items[event.ItemID]
		if !s.started || s.terminal || item == nil || !item.private || item.completed {
			return nil, false, openAIProtocolError(s.provider, "returned reasoning summary outside a private item")
		}
		return nil, false, nil
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		item := s.items[event.ItemID]
		if !s.started || s.terminal || item == nil || !item.private || item.completed ||
			event.Delta == "" || !utf8.ValidString(event.Delta) ||
			item.privateBytes+len(event.Delta) > MaxModelOutputBytes {
			return nil, false, openAIProtocolError(s.provider, "returned invalid private reasoning summary")
		}
		item.privateBytes += len(event.Delta)
		return nil, false, nil
	case "response.output_item.added":
		return s.startItem(event.Item)
	case "response.output_text.delta":
		item, err := s.activeItem(event.ItemID, StreamItemMessage)
		if err != nil || event.Delta == "" || !utf8.ValidString(event.Delta) ||
			item.text.Len()+len(event.Delta) > MaxModelOutputBytes {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses text delta")
		}
		_, _ = item.text.WriteString(event.Delta)
		return &ChatChunk{Text: event.Delta, Events: []StreamEvent{s.events.emit(StreamEvent{
			Type: StreamTextDelta, ItemID: event.ItemID, ItemType: StreamItemMessage,
			ItemStatus: StreamItemInProgress, TextDelta: event.Delta,
		})}}, false, nil
	case "response.function_call_arguments.delta":
		item, err := s.activeItem(event.ItemID, StreamItemToolCall)
		if err != nil || event.Delta == "" || !utf8.ValidString(event.Delta) ||
			item.arguments.Len()+len(event.Delta) > MaxProviderToolPayloadSize {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses function delta")
		}
		_, _ = item.arguments.WriteString(event.Delta)
		return &ChatChunk{Events: []StreamEvent{s.events.emit(StreamEvent{
			Type: StreamToolArgumentDelta, ItemID: event.ItemID, CallID: item.callID,
			ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
			ToolName: item.name, ArgumentDelta: event.Delta,
		})}}, false, nil
	case "response.function_call_arguments.done":
		item, err := s.activeItem(event.ItemID, StreamItemToolCall)
		if err != nil || item.callCompleted || (event.Name != "" && event.Name != item.name) {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses function completion")
		}
		return s.completeTool(event.ItemID, item, event.Arguments)
	case "response.output_item.done":
		return s.completeItem(event.Item)
	case "response.completed":
		if !s.started || s.terminal || event.Response.ID != s.responseID ||
			event.Response.Object != "response" || event.Response.Status != "completed" ||
			event.Response.Error != nil || len(s.items) == 0 || s.publicItems == 0 {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses completion")
		}
		if _, err := normalizeOpenAIModel(event.Response.Model); err != nil {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses completion model")
		}
		for _, item := range s.items {
			if !item.completed {
				return nil, false, openAIProtocolError(s.provider, "completed Responses stream with unfinished items")
			}
		}
		usage, err := normalizeResponsesUsage(event.Response.Usage)
		if err != nil {
			return nil, false, openAIProtocolError(s.provider, "returned invalid Responses stream usage")
		}
		calls, err := NormalizeToolCalls(s.toolCalls)
		if err != nil {
			return nil, false, openAIProtocolError(s.provider, "returned invalid Responses stream calls")
		}
		s.terminal = true
		return &ChatChunk{Done: true, ToolCalls: calls, Usage: &usage,
			Model: s.selectedModel, Provider: s.provider,
			Events: []StreamEvent{s.events.terminalEvent(OutcomeSuccess, &usage)}}, true, nil
	case "response.failed", "response.incomplete", "error":
		return nil, false, openAIProtocolError(s.provider, "Responses stream failed or was incomplete")
	default:
		return nil, false, openAIProtocolError(s.provider, "returned an unsupported Responses stream event")
	}
}

func (s *responsesStreamState) startItem(item openAIResponsesOutputItem) (*ChatChunk, bool, error) {
	if !s.started || s.terminal || len(s.items) >= MaxProviderOutputItems ||
		validateStreamIdentity(item.ID, "Responses output item") != nil || s.items[item.ID] != nil {
		return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses output item start")
	}
	if item.Status != "in_progress" {
		return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses output item status")
	}
	state := &responsesStreamItem{wireType: item.Type}
	events := make([]StreamEvent, 0, 2)
	switch item.Type {
	case "message":
		state.typeName = StreamItemMessage
		s.publicItems++
		events = append(events, s.events.emit(StreamEvent{Type: StreamOutputItemStarted,
			ItemID: item.ID, ItemType: StreamItemMessage, ItemStatus: StreamItemInProgress}))
	case "function_call":
		callID := strings.TrimSpace(item.CallID)
		name := strings.TrimSpace(item.Name)
		if validateStreamIdentity(callID, "Responses function call") != nil ||
			validateToolName(name) != nil || len(s.toolCalls) >= MaxProviderToolCalls {
			return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses function start")
		}
		for _, existing := range s.items {
			if existing.callID == callID {
				return nil, false, openAIProtocolError(s.provider, "reused a Responses function call id")
			}
		}
		state.typeName, state.callID, state.name = StreamItemToolCall, callID, name
		s.publicItems++
		events = append(events,
			s.events.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: item.ID,
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress}),
			s.events.emit(StreamEvent{Type: StreamToolCallStarted, ItemID: item.ID,
				CallID: callID, ItemType: StreamItemToolCall,
				ItemStatus: StreamItemInProgress, ToolName: name}))
	case "reasoning", "compaction":
		state.private = true
	default:
		return nil, false, openAIProtocolError(s.provider, "returned an unsupported Responses output item start")
	}
	s.items[item.ID] = state
	return &ChatChunk{Events: events}, false, nil
}

func (s *responsesStreamState) activeItem(id string,
	typeName StreamItemType,
) (*responsesStreamItem, error) {
	if !s.started || s.terminal {
		return nil, errors.New("Responses stream is not active")
	}
	item := s.items[id]
	if item == nil || item.typeName != typeName || item.completed {
		return nil, errors.New("Responses output item is not active")
	}
	return item, nil
}

func (s *responsesStreamState) completeTool(id string, item *responsesStreamItem,
	arguments string,
) (*ChatChunk, bool, error) {
	if item.arguments.Len() != 0 {
		if arguments == "" || strings.TrimSpace(arguments) != strings.TrimSpace(item.arguments.String()) {
			return nil, false, openAIProtocolError(s.provider, "Responses function arguments changed at completion")
		}
	} else if arguments == "" {
		arguments = `{}`
	}
	call, err := NormalizeToolCall(ToolCall{ID: item.callID, Name: item.name,
		Arguments: json.RawMessage(arguments)})
	if err != nil {
		return nil, false, openAIProtocolError(s.provider, "returned an invalid completed Responses function")
	}
	item.callCompleted = true
	item.finalArguments = string(call.Arguments)
	s.toolCalls = append(s.toolCalls, call)
	callCopy := call
	return &ChatChunk{Events: []StreamEvent{s.events.emit(StreamEvent{
		Type: StreamToolCallCompleted, ItemID: id, CallID: item.callID,
		ItemType: StreamItemToolCall, ItemStatus: StreamItemReadyForValidation,
		ToolName: item.name, CompletedCall: &callCopy,
	})}}, false, nil
}

func (s *responsesStreamState) completeItem(wire openAIResponsesOutputItem) (*ChatChunk, bool, error) {
	item := s.items[wire.ID]
	if item == nil || item.completed || wire.Type == "" || wire.Type != item.wireType ||
		wire.Status != "completed" {
		return nil, false, openAIProtocolError(s.provider, "returned an invalid Responses output item completion")
	}
	chunk := &ChatChunk{}
	if item.private {
		item.completed = true
		return chunk, false, nil
	}
	if item.typeName == StreamItemMessage {
		var completedText strings.Builder
		for _, part := range wire.Content {
			if part.Type != "output_text" || !utf8.ValidString(part.Text) ||
				completedText.Len()+len(part.Text) > MaxModelOutputBytes {
				return nil, false, openAIProtocolError(s.provider, "returned invalid completed Responses text")
			}
			_, _ = completedText.WriteString(part.Text)
		}
		if item.text.Len() == 0 && completedText.Len() > 0 {
			text := completedText.String()
			_, _ = item.text.WriteString(text)
			chunk.Text = text
			chunk.Events = append(chunk.Events, s.events.emit(StreamEvent{Type: StreamTextDelta,
				ItemID: wire.ID, ItemType: StreamItemMessage, ItemStatus: StreamItemInProgress,
				TextDelta: text}))
		} else if completedText.String() != item.text.String() {
			return nil, false, openAIProtocolError(s.provider, "Responses text changed at item completion")
		}
		chunk.Events = append(chunk.Events, s.events.emit(StreamEvent{Type: StreamOutputItemCompleted,
			ItemID: wire.ID, ItemType: StreamItemMessage, ItemStatus: StreamItemCompleted}))
	} else {
		if wire.CallID != item.callID || wire.Name != item.name {
			return nil, false, openAIProtocolError(s.provider, "Responses function identity changed at item completion")
		}
		if !item.callCompleted {
			completed, _, err := s.completeTool(wire.ID, item, wire.Arguments)
			if err != nil {
				return nil, false, err
			}
			chunk.Events = append(chunk.Events, completed.Events...)
		} else if strings.TrimSpace(wire.Arguments) != strings.TrimSpace(item.finalArguments) {
			return nil, false, openAIProtocolError(s.provider,
				"Responses function arguments changed at item completion")
		}
		chunk.Events = append(chunk.Events, s.events.emit(StreamEvent{Type: StreamOutputItemCompleted,
			ItemID: wire.ID, CallID: item.callID, ItemType: StreamItemToolCall,
			ItemStatus: StreamItemCompleted, ToolName: item.name}))
	}
	item.completed = true
	return chunk, false, nil
}

func (p *OpenAIResponsesProvider) readStream(ctx context.Context, body io.ReadCloser,
	selectedModel string, chunks chan<- ChatChunk,
) {
	defer close(chunks)
	defer body.Close()
	state := responsesStreamState{provider: p.name, selectedModel: selectedModel,
		items: make(map[string]*responsesStreamItem)}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxOpenAIStreamLineBytes)
	dataLines := make([]string, 0, 1)
	dataBytes := 0
	finished := false
	send := func(chunk ChatChunk) bool {
		select {
		case <-ctx.Done():
			return false
		case chunks <- chunk:
			return true
		}
	}
	sendFailure := func(err error) bool {
		if state.events.provider == "" {
			state.events = newProviderStreamEvents(p.name, selectedModel,
				"responses-unstarted", StreamGranularityDelta)
		}
		return send(state.events.failureChunk(err))
	}
	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		dataBytes = 0
		if payload == "[DONE]" {
			if state.terminal {
				finished = true
				return false
			}
			_ = sendFailure(openAIProtocolError(p.name, "Responses stream ended before completion"))
			return false
		}
		chunk, done, err := state.consume([]byte(payload))
		if err != nil {
			_ = sendFailure(err)
			return false
		}
		if chunk != nil && !send(*chunk) {
			return false
		}
		if done {
			finished = true
			return false
		}
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !flush() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if len(part) > maxOpenAIStreamEventBytes ||
				dataBytes > maxOpenAIStreamEventBytes-len(part) {
				_ = sendFailure(openAIProtocolError(p.name, "Responses stream event exceeds its limit"))
				return
			}
			dataBytes += len(part)
			dataLines = append(dataLines, part)
		}
	}
	if !flush() || finished || ctx.Err() != nil {
		return
	}
	if scanner.Err() != nil {
		_ = sendFailure(openAIProtocolError(p.name, "could not read Responses stream"))
		return
	}
	_ = sendFailure(openAIProtocolError(p.name, "Responses stream ended before completion"))
}
