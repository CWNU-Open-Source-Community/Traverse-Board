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
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	defaultOpenAIModel        = "gpt-4.1-mini"
	maxOpenAIResponseBytes    = 2 << 20
	maxOpenAIStreamLineBytes  = 1 << 20
	maxOpenAIStreamEventBytes = 1 << 20
	maxOpenAIModels           = 4096
	maxOpenAIModelBytes       = 512
)

// OpenAICompatibleConfig configures an OpenAI Chat Completions compatible
// endpoint. Credentials remain in memory and are never copied into requests,
// responses, errors, or Harness metadata.
type OpenAICompatibleConfig struct {
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
	HTTPClient   *http.Client
}

// OpenAICompatibleProvider implements the protocol-neutral Provider contract
// using the OpenAI Chat Completions wire protocol.
type OpenAICompatibleProvider struct {
	name         string
	baseURL      string
	apiKey       string
	defaultModel string
	client       *http.Client
}

func NewOpenAICompatibleProvider(config OpenAICompatibleConfig) (*OpenAICompatibleProvider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "openai_compatible"
	}
	baseURL, err := normalizeProviderBaseURL(config.BaseURL, name)
	if err != nil {
		return nil, err
	}
	if err := validateProviderAPIKey(config.APIKey, name); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(config.DefaultModel)
	if model == "" {
		model = defaultOpenAIModel
	}
	model, err = normalizeOpenAIModel(model)
	if err != nil {
		return nil, fmt.Errorf("default model for provider %s is invalid", name)
	}
	return &OpenAICompatibleProvider{
		name: name, baseURL: baseURL, apiKey: config.APIKey,
		defaultModel: model, client: providerHTTPClient(config.HTTPClient),
	}, nil
}

func (p *OpenAICompatibleProvider) Name() string { return p.name }

func (p *OpenAICompatibleProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint("/v1/models"), nil)
	if err != nil {
		return nil, openAILocalError(p.name, "could not create model-list request")
	}
	p.addHeaders(req, false)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, openAITransportError(ctx, p.name)
	}
	defer resp.Body.Close()
	raw, err := readOpenAIBody(resp.Body)
	if err != nil {
		return nil, openAIReadError(ctx, p.name, "could not read model-list response", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, openAIHTTPError(p.name, resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	if !utf8.Valid(raw) {
		return nil, openAIProtocolError(p.name, "returned a non-UTF-8 model list")
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Error *openAIError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, openAIProtocolError(p.name, "returned a malformed model list")
	}
	if payload.Error != nil {
		return nil, openAIWireError(p.name, *payload.Error)
	}
	if len(payload.Data) == 0 || len(payload.Data) > maxOpenAIModels {
		return nil, openAIProtocolError(p.name, "returned an invalid model list")
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, item := range payload.Data {
		id, err := normalizeOpenAIModel(item.ID)
		if err != nil {
			return nil, openAIProtocolError(p.name, "returned an invalid model identifier")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, ModelInfo{
			ID: id, DisplayName: id, Provider: p.name,
			// The Models endpoint exposes identity and ownership, not a reliable
			// per-model Chat/Tool/JSON capability contract. Those capabilities
			// are established by the configured model's Harness qualification.
			Capabilities: []string{},
		})
	}
	if len(models) == 0 {
		return nil, openAIProtocolError(p.name, "returned an empty model list")
	}
	return models, nil
}

func (p *OpenAICompatibleProvider) Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error) {
	model, wire, err := p.prepareRequest(request, false)
	if err != nil {
		return nil, openAILocalError(p.name, "could not prepare request")
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, openAILocalError(p.name, "could not encode request")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint("/v1/chat/completions"), bytes.NewReader(payload))
	if err != nil {
		return nil, openAILocalError(p.name, "could not create request")
	}
	p.addHeaders(httpReq, false)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, openAITransportError(ctx, p.name)
	}
	defer resp.Body.Close()
	raw, err := readOpenAIBody(resp.Body)
	if err != nil {
		return nil, openAIReadError(ctx, p.name, "could not read response", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, openAIHTTPError(p.name, resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	if !utf8.Valid(raw) {
		return nil, openAIProtocolError(p.name, "returned non-UTF-8 JSON")
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, openAIProtocolError(p.name, "returned malformed JSON")
	}
	if parsed.Error != nil {
		return nil, openAIWireError(p.name, *parsed.Error)
	}
	return p.normalizeResponse(model, parsed)
}

func (p *OpenAICompatibleProvider) StreamChat(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	model, wire, err := p.prepareRequest(request, true)
	if err != nil {
		return nil, openAILocalError(p.name, "could not prepare streaming request")
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, openAILocalError(p.name, "could not encode streaming request")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint("/v1/chat/completions"), bytes.NewReader(payload))
	if err != nil {
		return nil, openAILocalError(p.name, "could not create streaming request")
	}
	p.addHeaders(httpReq, true)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, openAITransportError(ctx, p.name)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		raw, readErr := readOpenAIBody(resp.Body)
		if readErr != nil {
			return nil, openAIReadError(ctx, p.name, "could not read error response", readErr)
		}
		return nil, openAIHTTPError(p.name, resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	chunks := make(chan ChatChunk, 8)
	go p.readStream(ctx, resp.Body, model, chunks)
	return chunks, nil
}

func (p *OpenAICompatibleProvider) SupportsTools(model string) bool {
	return strings.TrimSpace(model) == p.defaultModel
}

func (p *OpenAICompatibleProvider) SupportsVision(string) bool { return false }

func (p *OpenAICompatibleProvider) SupportsJSONMode(model string) bool {
	return strings.TrimSpace(model) == p.defaultModel
}

func (p *OpenAICompatibleProvider) DescribeModelHarness(model string) ModelHarness {
	model = strings.TrimSpace(model)
	if model == "" {
		model = p.defaultModel
	}
	return ModelHarness{
		ProtocolVersion:   ModelHarnessProtocolVersion,
		TransportProtocol: HarnessTransportOpenAIChatCompletions,
		ToolStrategy:      HarnessToolStrategyNative, JSONStrategy: HarnessJSONStrategyNative,
		QualificationStatus: HarnessQualificationRequired,
		BindingDigest: harnessBindingDigest(p.name, p.baseURL, model,
			HarnessTransportOpenAIChatCompletions, HarnessToolStrategyNative,
			HarnessJSONStrategyNative),
	}
}

func (p *OpenAICompatibleProvider) prepareRequest(request ChatRequest, stream bool) (string, openAIChatRequest, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = p.defaultModel
	}
	var err error
	model, err = normalizeOpenAIModel(model)
	if err != nil {
		return "", openAIChatRequest{}, err
	}
	if math.IsNaN(request.Temperature) || math.IsInf(request.Temperature, 0) ||
		request.Temperature < 0 || request.Temperature > 2 {
		return "", openAIChatRequest{}, errors.New("temperature is outside the supported range")
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	if maxTokens > 1_000_000 {
		return "", openAIChatRequest{}, errors.New("max tokens exceeds the provider request limit")
	}
	wire := openAIChatRequest{Model: model, MaxTokens: maxTokens, Stream: stream}
	if request.Temperature > 0 {
		wire.Temperature = &request.Temperature
	}
	if stream {
		wire.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	}
	if request.JSONMode {
		wire.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
	}
	for index, message := range request.Messages {
		mapped, err := openAIMessages(message)
		if err != nil {
			return "", openAIChatRequest{}, fmt.Errorf("invalid message at index %d", index)
		}
		wire.Messages = append(wire.Messages, mapped...)
	}
	if len(wire.Messages) == 0 {
		content := "Hello"
		wire.Messages = append(wire.Messages, openAIMessage{Role: "user", Content: &content})
	}
	if len(request.Tools) > MaxProviderToolCalls {
		return "", openAIChatRequest{}, errors.New("tool specification count exceeds the provider limit")
	}
	for index, spec := range request.Tools {
		name := strings.TrimSpace(spec.Name)
		parameters := append(json.RawMessage(nil), bytes.TrimSpace(spec.Parameters)...)
		if err := validateOpenAIToolName(name); err != nil || len(parameters) == 0 ||
			len(parameters) > MaxProviderToolPayloadSize || !utf8.Valid(parameters) || !json.Valid(parameters) {
			return "", openAIChatRequest{}, fmt.Errorf("invalid tool specification at index %d", index)
		}
		wire.Tools = append(wire.Tools, openAITool{Type: "function", Function: openAIFunctionSpec{
			Name: name, Description: strings.TrimSpace(spec.Description), Parameters: parameters,
		}})
	}
	return model, wire, nil
}

func openAIMessages(message Message) ([]openAIMessage, error) {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	content := strings.TrimSpace(message.Content)
	if content == "" && len(message.ToolCalls) == 0 && len(message.ToolResults) == 0 {
		return nil, nil
	}
	switch role {
	case "system":
		if len(message.ToolCalls) != 0 || len(message.ToolResults) != 0 || content == "" {
			return nil, errors.New("system message has invalid structured content")
		}
		return []openAIMessage{{Role: "system", Content: &content}}, nil
	case "assistant":
		if len(message.ToolResults) != 0 {
			return nil, errors.New("assistant message cannot contain tool results")
		}
		calls, err := NormalizeToolCalls(message.ToolCalls)
		if err != nil {
			return nil, err
		}
		mapped := openAIMessage{Role: "assistant"}
		if content != "" {
			mapped.Content = &content
		}
		for _, call := range calls {
			mapped.ToolCalls = append(mapped.ToolCalls, openAIToolCall{
				ID: call.ID, Type: "function", Function: openAIFunctionCall{
					Name: call.Name, Arguments: string(call.Arguments),
				},
			})
		}
		return []openAIMessage{mapped}, nil
	case "user":
		if len(message.ToolCalls) != 0 {
			return nil, errors.New("user message cannot contain tool calls")
		}
		mapped := make([]openAIMessage, 0, len(message.ToolResults)+1)
		for _, result := range message.ToolResults {
			normalized, err := NormalizeToolResult(result)
			if err != nil {
				return nil, err
			}
			resultContent := normalized.Content
			mapped = append(mapped, openAIMessage{
				Role: "tool", Content: &resultContent, ToolCallID: normalized.ToolCallID,
			})
		}
		if content != "" {
			mapped = append(mapped, openAIMessage{Role: "user", Content: &content})
		}
		if len(mapped) == 0 {
			return nil, errors.New("user message has no content")
		}
		return mapped, nil
	default:
		return nil, errors.New("message role is unsupported")
	}
}

func (p *OpenAICompatibleProvider) normalizeResponse(defaultModel string, response openAIChatResponse) (*ChatResponse, error) {
	if len(response.Choices) != 1 || response.Choices[0].Index == nil || *response.Choices[0].Index != 0 {
		return nil, openAIProtocolError(p.name, "returned an invalid choice list")
	}
	choice := response.Choices[0]
	if choice.Message.Role != "" && choice.Message.Role != "assistant" {
		return nil, openAIProtocolError(p.name, "returned an invalid message role")
	}
	text := ""
	if choice.Message.Content != nil {
		text = *choice.Message.Content
	}
	if !utf8.ValidString(text) || len(text) > MaxModelOutputBytes {
		return nil, openAIProtocolError(p.name, "returned invalid response text")
	}
	calls, err := normalizeOpenAIToolCalls(choice.Message.ToolCalls)
	if err != nil {
		return nil, openAIProtocolError(p.name, "returned invalid tool calls")
	}
	if err := validateOpenAIFinishReason(choice.FinishReason, text != "", len(calls)); err != nil {
		return nil, openAIProtocolError(p.name, "returned an incompatible finish reason")
	}
	if response.Usage == nil {
		return nil, openAIProtocolError(p.name, "omitted token usage")
	}
	usage, err := normalizeOpenAIUsage(*response.Usage)
	if err != nil {
		return nil, openAIProtocolError(p.name, "returned invalid token usage")
	}
	if _, err := normalizeOpenAIModel(response.Model); err != nil {
		return nil, openAIProtocolError(p.name, "returned an invalid model identity")
	}
	return &ChatResponse{
		Text: text, ToolCalls: calls, Usage: usage, Raw: nil,
		// OpenAI may resolve a stable alias to a dated snapshot in the wire
		// response. Keep the selected Router identity stable while still
		// requiring a valid upstream identity above.
		Model: defaultModel, Provider: p.name,
	}, nil
}

func normalizeOpenAIToolCalls(wire []openAIToolCall) ([]ToolCall, error) {
	calls := make([]ToolCall, 0, len(wire))
	for _, item := range wire {
		if item.Type != "" && item.Type != "function" {
			return nil, errors.New("tool call type is unsupported")
		}
		arguments := item.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = `{}`
		}
		calls = append(calls, ToolCall{ID: item.ID, Name: item.Function.Name,
			Arguments: json.RawMessage(arguments)})
	}
	return NormalizeToolCalls(calls)
}

func normalizeOpenAIUsage(wire openAIUsage) (Usage, error) {
	if wire.PromptTokens == nil || wire.CompletionTokens == nil || wire.TotalTokens == nil {
		return Usage{}, errors.New("token count is missing")
	}
	usage := Usage{InputTokens: *wire.PromptTokens, OutputTokens: *wire.CompletionTokens}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || *wire.TotalTokens < 0 {
		return Usage{}, errors.New("negative token count")
	}
	maxInt := int(^uint(0) >> 1)
	if usage.InputTokens > maxInt-usage.OutputTokens {
		return Usage{}, errors.New("token count overflow")
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	if *wire.TotalTokens != usage.TotalTokens {
		return Usage{}, errors.New("inconsistent total token count")
	}
	return usage, usage.Validate()
}

func validateOpenAIFinishReason(reason string, hasText bool, toolCount int) error {
	switch strings.TrimSpace(reason) {
	case "stop":
		if toolCount != 0 || !hasText {
			return errors.New("stop finish did not contain text")
		}
	case "tool_calls":
		if toolCount == 0 {
			return errors.New("tool finish did not contain calls")
		}
	default:
		return errors.New("finish reason is unsupported or incomplete")
	}
	return nil
}

func normalizeOpenAIModel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]byte(value)) > maxOpenAIModelBytes ||
		strings.ContainsRune(value, 0) || redact.String(value) != value {
		return "", errors.New("model identifier is invalid")
	}
	for _, current := range value {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return "", errors.New("model identifier is invalid")
		}
	}
	return value, nil
}

func validateOpenAIToolName(name string) error {
	_, err := NormalizeToolCall(ToolCall{ID: "spec", Name: name, Arguments: json.RawMessage(`{}`)})
	return err
}

func (p *OpenAICompatibleProvider) endpoint(path string) string {
	if strings.HasSuffix(p.baseURL, path) {
		return p.baseURL
	}
	if strings.HasSuffix(p.baseURL, "/v1") && strings.HasPrefix(path, "/v1/") {
		return p.baseURL + strings.TrimPrefix(path, "/v1")
	}
	return p.baseURL + path
}

func (p *OpenAICompatibleProvider) addHeaders(request *http.Request, stream bool) {
	request.Header.Set("Content-Type", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
}

func readOpenAIBody(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxOpenAIResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxOpenAIResponseBytes {
		return nil, errors.New("response exceeds size limit")
	}
	return raw, nil
}

func openAILocalError(provider string, message string) *ProviderError {
	err := NewProviderError(OutcomePermanent, provider, message, nil)
	err.Reason = ProviderFailureProtocolIncompatible
	return err
}

func openAIProtocolError(provider string, message string) *ProviderError {
	err := NewProviderError(OutcomeInvalidResponse, provider, message, nil)
	err.Reason = ProviderFailureProtocolIncompatible
	return err
}

func openAITransportError(ctx context.Context, provider string) *ProviderError {
	if ctx.Err() != nil {
		err := NewProviderError(OutcomeCancelled, provider, "request was cancelled", nil)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err.Reason = ProviderFailureNetwork
		}
		return err
	}
	err := NewProviderError(OutcomeRetryable, provider, "request failed", nil)
	err.Reason = ProviderFailureNetwork
	return err
}

func openAIReadError(ctx context.Context, provider string, message string, source error) *ProviderError {
	if ctx.Err() != nil {
		return openAITransportError(ctx, provider)
	}
	var network net.Error
	if errors.As(source, &network) {
		err := NewProviderError(OutcomeRetryable, provider, message, nil)
		err.Reason = ProviderFailureNetwork
		return err
	}
	return openAIProtocolError(provider, message)
}

func openAIHTTPError(provider string, statusCode int, retryAfter string, raw []byte) *ProviderError {
	kind := OutcomePermanent
	reason := ProviderFailureProtocolIncompatible
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		reason = ProviderFailureAuthentication
	case http.StatusTooManyRequests:
		kind, reason = OutcomeRateLimited, ProviderFailureRateLimit
	case http.StatusServiceUnavailable, 529:
		kind, reason = OutcomeRetryable, ProviderFailureCapacity
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusGatewayTimeout:
		kind, reason = OutcomeRetryable, ProviderFailureNetwork
	}
	var envelope struct {
		Error *openAIError `json:"error"`
	}
	// The HTTP status is authoritative for standard status classes. Some
	// compatible endpoints reuse broad error types such as server_error even
	// for authentication and throttling responses.
	wireReason := ProviderFailureNone
	if utf8.Valid(raw) && json.Unmarshal(raw, &envelope) == nil && envelope.Error != nil {
		wireReason = classifyOpenAIWireError(*envelope.Error)
	}
	if statusCode == http.StatusNotFound {
		// A bare 404 normally means the configured Chat Completions path is
		// incompatible. Only a canonical, content-free error code/type can
		// prove that the requested model itself was not found.
		if wireReason == ProviderFailureModelNotFound {
			reason = wireReason
		}
	} else if reason == ProviderFailureProtocolIncompatible && wireReason != ProviderFailureNone {
		reason = wireReason
		switch wireReason {
		case ProviderFailureRateLimit:
			kind = OutcomeRateLimited
		case ProviderFailureCapacity, ProviderFailureNetwork:
			kind = OutcomeRetryable
		default:
			kind = OutcomePermanent
		}
	}
	err := NewProviderError(kind, provider, fmt.Sprintf("returned HTTP %d", statusCode), nil)
	err.StatusCode = statusCode
	err.RetryAfter = parseRetryAfter(retryAfter, time.Now())
	err.Reason = reason
	return err
}

func openAIWireError(provider string, wire openAIError) *ProviderError {
	reason := classifyOpenAIWireError(wire)
	kind := OutcomePermanent
	switch reason {
	case ProviderFailureRateLimit:
		kind = OutcomeRateLimited
	case ProviderFailureCapacity, ProviderFailureNetwork:
		kind = OutcomeRetryable
	case ProviderFailureNone:
		reason = ProviderFailureProtocolIncompatible
	}
	err := NewProviderError(kind, provider, "returned a provider error", nil)
	err.Reason = reason
	return err
}

func classifyOpenAIWireError(wire openAIError) ProviderFailureReason {
	code := ""
	if len(wire.Code) > 0 {
		_ = json.Unmarshal(wire.Code, &code)
	}
	// Prefer the specific code over the broader error type. Both are treated as
	// untrusted wire values and must match a closed canonical allowlist; prefix
	// or substring matches could turn attacker-controlled near-misses into false
	// authentication, capacity, or model-not-found facts.
	for _, signal := range []string{code, wire.Type} {
		switch strings.ToLower(strings.TrimSpace(signal)) {
		case "rate_limit", "rate_limit_error", "rate_limit_exceeded",
			"insufficient_quota", "quota_exceeded":
			return ProviderFailureRateLimit
		case "server_error", "overloaded", "overloaded_error",
			"capacity_exceeded":
			return ProviderFailureCapacity
		case "model_not_found", "model_not_exist", "model_not_exists":
			return ProviderFailureModelNotFound
		case "authentication_error", "invalid_api_key", "incorrect_api_key",
			"api_key_invalid", "permission_denied", "insufficient_permissions":
			return ProviderFailureAuthentication
		case "request_timeout", "timeout_error":
			return ProviderFailureNetwork
		}
	}
	return ProviderFailureNone
}

type openAIChatRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	Tools          []openAITool          `json:"tools,omitempty"`
	Temperature    *float64              `json:"temperature,omitempty"`
	MaxTokens      int                   `json:"max_tokens"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
	Stream         bool                  `json:"stream,omitempty"`
	StreamOptions  *openAIStreamOptions  `json:"stream_options,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIFunctionSpec `json:"function"`
}

type openAIFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIUsage struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
	TotalTokens      *int `json:"total_tokens"`
}

type openAIError struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
}

type openAIChatResponse struct {
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage"`
	Error   *openAIError   `json:"error,omitempty"`
}

type openAIChoice struct {
	Index        *int              `json:"index"`
	Message      openAIMessage     `json:"message"`
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason"`
}

type openAIStreamDelta struct {
	Role      string                 `json:"role"`
	Content   *string                `json:"content"`
	ToolCalls []openAIStreamToolCall `json:"tool_calls"`
}

type openAIStreamToolCall struct {
	Index    *int               `json:"index"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIStreamResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage"`
	Error   *openAIError   `json:"error,omitempty"`
}

type openAIStreamToolState struct {
	id        string
	name      string
	idSeen    bool
	nameSeen  bool
	itemID    string
	itemSeen  bool
	callSeen  bool
	arguments strings.Builder
	pending   []string
}

type openAIStreamState struct {
	provider      string
	model         string
	upstreamModel string
	responseModel bool
	finished      bool
	finishReason  string
	usage         *Usage
	textBytes     int
	hasTextDelta  bool
	textItemSeen  bool
	tools         map[int]*openAIStreamToolState
	events        providerStreamEvents
}

func (s *openAIStreamState) consume(payload []byte) (*ChatChunk, error) {
	if !utf8.Valid(payload) {
		return nil, openAIProtocolError(s.provider, "returned a non-UTF-8 stream event")
	}
	var event openAIStreamResponse
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, openAIProtocolError(s.provider, "returned a malformed stream event")
	}
	if event.Error != nil {
		return nil, openAIWireError(s.provider, *event.Error)
	}
	if strings.TrimSpace(event.Model) != "" {
		model, err := normalizeOpenAIModel(event.Model)
		if err != nil {
			return nil, openAIProtocolError(s.provider, "returned an invalid stream model")
		}
		if s.responseModel && model != s.upstreamModel {
			return nil, openAIProtocolError(s.provider, "changed model identity during the stream")
		}
		s.upstreamModel, s.responseModel = model, true
	}
	if len(event.Choices) == 0 {
		if event.Usage == nil || s.usage != nil || !s.finished || !s.responseModel ||
			strings.TrimSpace(event.Model) == "" {
			return nil, openAIProtocolError(s.provider, "returned an invalid stream choice")
		}
		usage, err := normalizeOpenAIUsage(*event.Usage)
		if err != nil {
			return nil, openAIProtocolError(s.provider, "returned invalid stream usage")
		}
		s.usage = &usage
		events := s.ensureStreamStarted()
		if len(events) == 0 {
			return nil, nil
		}
		return &ChatChunk{Events: events}, nil
	}
	if len(event.Choices) != 1 || event.Choices[0].Index == nil || *event.Choices[0].Index != 0 || s.finished {
		return nil, openAIProtocolError(s.provider, "returned an invalid stream choice")
	}
	choice := event.Choices[0]
	if choice.Delta.Role != "" && choice.Delta.Role != "assistant" {
		return nil, openAIProtocolError(s.provider, "returned an invalid stream role")
	}
	streamEvents := s.ensureStreamStarted()
	chunk := &ChatChunk{}
	if choice.Delta.Content != nil && *choice.Delta.Content != "" {
		text := *choice.Delta.Content
		if s.textBytes > MaxModelOutputBytes-len(text) {
			return nil, openAIProtocolError(s.provider, "streamed text exceeds the output limit")
		}
		s.textBytes += len(text)
		s.hasTextDelta = true
		if !s.textItemSeen {
			s.textItemSeen = true
			streamEvents = append(streamEvents, s.events.emit(StreamEvent{
				Type: StreamOutputItemStarted, ItemID: "message", ItemType: StreamItemMessage,
				ItemStatus: StreamItemInProgress,
			}))
		}
		streamEvents = append(streamEvents, s.events.emit(StreamEvent{
			Type: StreamTextDelta, ItemID: "message", ItemType: StreamItemMessage,
			ItemStatus: StreamItemInProgress, TextDelta: text,
		}))
		chunk.Text = text
	}
	for _, delta := range choice.Delta.ToolCalls {
		events, err := s.consumeToolDelta(delta)
		if err != nil {
			return nil, openAIProtocolError(s.provider, "returned an invalid tool-call delta")
		}
		streamEvents = append(streamEvents, events...)
	}
	if choice.FinishReason != "" {
		s.finished = true
		s.finishReason = strings.TrimSpace(choice.FinishReason)
	}
	if event.Usage != nil {
		// OpenAI emits a final empty-choices usage event when include_usage is
		// enabled. Some compatible endpoints attach the same exact usage object
		// to the choice that carries finish_reason. Accept both terminal forms,
		// while rejecting early, duplicate, or structurally incomplete usage.
		if s.usage != nil || !s.finished || !s.responseModel ||
			strings.TrimSpace(event.Model) == "" {
			return nil, openAIProtocolError(s.provider, "returned usage before the final stream event")
		}
		usage, err := normalizeOpenAIUsage(*event.Usage)
		if err != nil {
			return nil, openAIProtocolError(s.provider, "returned invalid stream usage")
		}
		s.usage = &usage
	}
	chunk.Events = streamEvents
	if chunk.Text == "" && len(chunk.Events) == 0 {
		return nil, nil
	}
	return chunk, nil
}

func (s *openAIStreamState) consumeToolDelta(delta openAIStreamToolCall) ([]StreamEvent, error) {
	events := make([]StreamEvent, 0, 4)
	if delta.Index == nil || *delta.Index < 0 || *delta.Index >= MaxProviderToolCalls {
		return nil, errors.New("tool-call index is invalid")
	}
	if delta.Type != "" && delta.Type != "function" {
		return nil, errors.New("tool-call type is unsupported")
	}
	if s.tools == nil {
		s.tools = make(map[int]*openAIStreamToolState)
	}
	state, exists := s.tools[*delta.Index]
	if !exists {
		if len(s.tools) >= MaxProviderToolCalls {
			return nil, errors.New("too many tool calls")
		}
		state = &openAIStreamToolState{itemID: fmt.Sprintf("tool/%d", *delta.Index)}
		s.tools[*delta.Index] = state
	}
	if !state.itemSeen {
		state.itemSeen = true
		events = append(events, s.events.emit(StreamEvent{
			Type: StreamOutputItemStarted, ItemID: state.itemID, ItemType: StreamItemToolCall,
			ItemStatus: StreamItemInProgress,
		}))
	}
	if delta.ID != "" {
		if state.idSeen && delta.ID != state.id {
			return nil, errors.New("tool-call id changed")
		}
		state.id, state.idSeen = delta.ID, true
	}
	if delta.Function.Name != "" {
		if state.nameSeen && delta.Function.Name != state.name {
			return nil, errors.New("tool-call name changed")
		}
		state.name, state.nameSeen = delta.Function.Name, true
	}
	if !utf8.ValidString(state.id) || !utf8.ValidString(state.name) ||
		len([]rune(state.id)) > MaxProviderToolIdentity || len([]rune(state.name)) > MaxProviderToolIdentity {
		return nil, errors.New("tool-call identity exceeds the limit")
	}
	if state.arguments.Len() > MaxProviderToolPayloadSize-len(delta.Function.Arguments) {
		return nil, errors.New("tool-call arguments exceed the limit")
	}
	_, _ = state.arguments.WriteString(delta.Function.Arguments)
	if delta.Function.Arguments != "" {
		state.pending = append(state.pending, delta.Function.Arguments)
	}
	if !state.callSeen && state.idSeen && state.nameSeen {
		if err := validateToolName(state.name); err != nil {
			return nil, err
		}
		state.callSeen = true
		events = append(events, s.events.emit(StreamEvent{
			Type: StreamToolCallStarted, ItemID: state.itemID, CallID: state.id,
			ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress, ToolName: state.name,
		}))
		for _, pending := range state.pending {
			events = append(events, s.events.emit(StreamEvent{
				Type: StreamToolArgumentDelta, ItemID: state.itemID, CallID: state.id,
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
				ToolName: state.name, ArgumentDelta: pending,
			}))
		}
		state.pending = nil
	} else if state.callSeen && delta.Function.Arguments != "" {
		pending := state.pending[len(state.pending)-1]
		state.pending = state.pending[:len(state.pending)-1]
		events = append(events, s.events.emit(StreamEvent{
			Type: StreamToolArgumentDelta, ItemID: state.itemID, CallID: state.id,
			ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
			ToolName: state.name, ArgumentDelta: pending,
		}))
	}
	return events, nil
}

func (s *openAIStreamState) finalChunk() (ChatChunk, error) {
	if !s.finished {
		return ChatChunk{}, openAIProtocolError(s.provider, "stream ended without a finish reason")
	}
	if s.usage == nil {
		return ChatChunk{}, openAIProtocolError(s.provider, "stream omitted token usage")
	}
	if !s.responseModel {
		return ChatChunk{}, openAIProtocolError(s.provider, "stream omitted its model identity")
	}
	indices := make([]int, 0, len(s.tools))
	for index := range s.tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	calls := make([]ToolCall, 0, len(indices))
	for expected, index := range indices {
		if index != expected {
			return ChatChunk{}, openAIProtocolError(s.provider, "stream returned sparse tool-call indices")
		}
		state := s.tools[index]
		arguments := state.arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = `{}`
		}
		calls = append(calls, ToolCall{ID: state.id, Name: state.name,
			Arguments: json.RawMessage(arguments)})
	}
	var err error
	calls, err = NormalizeToolCalls(calls)
	if err != nil {
		return ChatChunk{}, openAIProtocolError(s.provider, "stream returned invalid tool calls")
	}
	if err := validateOpenAIFinishReason(s.finishReason, s.hasTextDelta, len(calls)); err != nil {
		return ChatChunk{}, openAIProtocolError(s.provider, "stream returned an incompatible finish reason")
	}
	usage := *s.usage
	events := make([]StreamEvent, 0, 3+len(calls)*2)
	if s.textItemSeen {
		events = append(events, s.events.emit(StreamEvent{
			Type: StreamOutputItemCompleted, ItemID: "message", ItemType: StreamItemMessage,
			ItemStatus: StreamItemCompleted,
		}))
	}
	for _, index := range indices {
		state := s.tools[index]
		if !state.callSeen || len(state.pending) != 0 {
			return ChatChunk{}, openAIProtocolError(s.provider, "stream returned an unfinished tool call")
		}
		var call ToolCall
		for _, current := range calls {
			if current.ID == state.id {
				call = current
				break
			}
		}
		events = append(events, s.events.emit(StreamEvent{
			Type: StreamToolCallCompleted, ItemID: state.itemID, CallID: state.id,
			ItemType: StreamItemToolCall, ItemStatus: StreamItemReadyForValidation,
			ToolName: state.name, CompletedCall: &call,
		}))
		events = append(events, s.events.emit(StreamEvent{
			Type: StreamOutputItemCompleted, ItemID: state.itemID, CallID: state.id,
			ItemType: StreamItemToolCall, ItemStatus: StreamItemCompleted, ToolName: state.name,
		}))
	}
	events = append(events, s.events.terminalEvent(OutcomeSuccess, &usage))
	return ChatChunk{Done: true, ToolCalls: calls, Events: events, Usage: &usage,
		Model: s.model, Provider: s.provider}, nil
}

func (s *openAIStreamState) ensureStreamStarted() []StreamEvent {
	if s.events.started {
		return nil
	}
	return []StreamEvent{s.events.start()}
}

func (p *OpenAICompatibleProvider) readStream(ctx context.Context, body io.ReadCloser,
	model string, chunks chan<- ChatChunk,
) {
	defer close(chunks)
	defer body.Close()
	state := openAIStreamState{provider: p.name, model: model,
		events: newProviderStreamEvents(p.name, model, "openai-response", StreamGranularityDelta)}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxOpenAIStreamLineBytes)
	dataLines := make([]string, 0, 1)
	eventBytes := 0
	stopped := false
	sendError := func(err error) bool {
		return p.sendStreamChunk(ctx, chunks, state.events.failureChunk(err))
	}
	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		eventBytes = 0
		if strings.TrimSpace(payload) == "[DONE]" {
			final, err := state.finalChunk()
			if err != nil {
				_ = sendError(err)
				return false
			}
			stopped = true
			return p.sendStreamChunk(ctx, chunks, final)
		}
		chunk, err := state.consume([]byte(payload))
		if err != nil {
			_ = sendError(err)
			return false
		}
		return chunk == nil || p.sendStreamChunk(ctx, chunks, *chunk)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !flush() || stopped {
				return
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found || field != "data" {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		if !utf8.ValidString(value) || eventBytes > maxOpenAIStreamEventBytes-len(value)-1 {
			_ = sendError(openAIProtocolError(p.name, "stream event exceeds its UTF-8 size limit"))
			return
		}
		eventBytes += len(value) + 1
		dataLines = append(dataLines, value)
	}
	if len(dataLines) != 0 && !flush() {
		return
	}
	if stopped || ctx.Err() != nil {
		return
	}
	if scanner.Err() != nil {
		_ = sendError(openAIReadError(ctx, p.name, "stream read failed", scanner.Err()))
		return
	}
	_ = sendError(openAIProtocolError(p.name, "stream ended before [DONE]"))
}

func (p *OpenAICompatibleProvider) sendStreamChunk(ctx context.Context,
	chunks chan<- ChatChunk, chunk ChatChunk,
) bool {
	select {
	case <-ctx.Done():
		return false
	case chunks <- chunk:
		return true
	}
}
