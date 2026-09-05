package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const AnthropicVersion = "2023-06-01"

const (
	maxProviderBaseURLBytes = 2048
	maxProviderAPIKeyBytes  = 16 * 1024
	defaultProviderTimeout  = 60 * time.Second
)

type AnthropicCompatibleConfig struct {
	Name            string
	BaseURL         string
	APIKey          string
	DefaultModel    string
	DisableThinking bool
	HTTPClient      *http.Client
	Runtime         HTTPProviderRuntime
}

type AnthropicCompatibleProvider struct {
	name            string
	baseURL         string
	apiKey          string
	defaultModel    string
	disableThinking bool
	client          *http.Client
	runtime         HTTPProviderRuntime
}

func NewAnthropicCompatibleProvider(config AnthropicCompatibleConfig) (*AnthropicCompatibleProvider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "anthropic_compatible"
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
	client := providerHTTPClient(config.HTTPClient)
	defaultModel := strings.TrimSpace(config.DefaultModel)
	if defaultModel == "" {
		defaultModel = "claude-3-5-sonnet-latest"
	}
	return &AnthropicCompatibleProvider{
		name:            name,
		baseURL:         baseURL,
		apiKey:          config.APIKey,
		defaultModel:    defaultModel,
		disableThinking: config.DisableThinking,
		client:          client,
		runtime:         config.Runtime,
	}, nil
}

func providerHTTPClient(source *http.Client) *http.Client {
	client := &http.Client{Timeout: defaultProviderTimeout}
	if source != nil {
		copy := *source
		client = &copy
		if client.Timeout <= 0 || client.Timeout > defaultProviderTimeout {
			client.Timeout = defaultProviderTimeout
		}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func normalizeProviderBaseURL(value string, provider string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", fmt.Errorf("base url is required for provider %s", provider)
	}
	if len([]byte(value)) > maxProviderBaseURLBytes {
		return "", fmt.Errorf("base url for provider %s exceeds %d bytes", provider,
			maxProviderBaseURLBytes)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("base url for provider %s must be an absolute HTTP(S) URL", provider)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("base url for provider %s cannot contain credentials, query, or fragment", provider)
	}
	if parsed.Scheme == "http" && !providerLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("base url for provider %s must use HTTPS outside loopback", provider)
	}
	return value, nil
}

func providerLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func validateProviderAPIKey(value string, provider string) error {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		len([]byte(value)) > maxProviderAPIKeyBytes {
		return fmt.Errorf("api key for provider %s must be normalized UTF-8 within %d bytes",
			provider, maxProviderAPIKeyBytes)
	}
	for _, current := range value {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return fmt.Errorf("api key for provider %s cannot contain whitespace or control characters",
				provider)
		}
	}
	return nil
}

func (p *AnthropicCompatibleProvider) Name() string {
	return p.name
}

func (p *AnthropicCompatibleProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	secret, err := providerRequestCredential(ctx, p.name, p.apiKey, p.runtime)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name,
			"provider credential is unavailable", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint("/v1/models"), nil)
	if err != nil {
		return nil, err
	}
	if err := p.addHeaders(req, secret, "application/json"); err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name,
			"could not prepare model-list headers", nil)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fallbackModelInfo(p.name, p.defaultModel), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallbackModelInfo(p.name, p.defaultModel), nil
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fallbackModelInfo(p.name, p.defaultModel), nil
	}
	if len(payload.Data) == 0 {
		return fallbackModelInfo(p.name, p.defaultModel), nil
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID == "" {
			continue
		}
		display := item.DisplayName
		if display == "" {
			display = item.ID
		}
		models = append(models, ModelInfo{
			ID:           item.ID,
			DisplayName:  display,
			Provider:     p.name,
			Capabilities: []string{"chat", "tools"},
		})
	}
	if len(models) == 0 {
		return fallbackModelInfo(p.name, p.defaultModel), nil
	}
	return models, nil
}

func (p *AnthropicCompatibleProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	selectedModel := strings.TrimSpace(req.Model)
	if selectedModel == "" {
		selectedModel = p.defaultModel
	}
	wireModel, err := providerRequestModel(p.runtime, selectedModel)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not map model", nil)
	}
	body, err := p.toRequest(wireModel, req)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not prepare request", err)
	}
	secret, err := providerRequestCredential(ctx, p.name, p.apiKey, p.runtime)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name,
			"provider credential is unavailable", nil)
	}
	payload, err := providerRequestPayload(p.runtime, secret, body)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not encode request", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/v1/messages"), bytes.NewReader(payload))
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not create request", err)
	}
	if err := p.addHeaders(httpReq, secret, "application/json"); err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name,
			"could not prepare request headers", nil)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, NormalizeProviderError(p.name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, NormalizeProviderError(p.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, anthropicHTTPError(p.name, resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	var parsed anthropicMessageResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, NewProviderError(OutcomeInvalidResponse, p.name, "returned malformed JSON", err)
	}
	text := parsed.Text()
	toolCalls, err := parsed.ToolCalls()
	if err != nil {
		return nil, NewProviderError(OutcomeInvalidResponse, p.name, "returned invalid tool calls", err)
	}
	if strings.TrimSpace(text) == "" && len(toolCalls) == 0 {
		return nil, NewProviderError(OutcomeInvalidResponse, p.name, "returned an empty text response", nil)
	}
	responseModel := parsed.ModelOrDefault(selectedModel)
	if p.runtime != nil {
		// A custom definition may map its stable local route identity to a
		// different upstream model identifier. Do not let that wire detail escape
		// back into the Router/Supervisor contract.
		responseModel = selectedModel
	}
	return &ChatResponse{
		Text:      text,
		ToolCalls: toolCalls,
		// Keep the upstream response inside the adapter. Raw model payloads are
		// not part of the Supervisor contract and must not cross persistence or
		// activity boundaries.
		Raw:      nil,
		Model:    responseModel,
		Provider: p.name,
		Usage: Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			TotalTokens:  parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	}, nil
}

func anthropicHTTPError(provider string, statusCode int, retryAfterHeader string, raw []byte) *ProviderError {
	// The response body is deliberately ignored. Provider error text may be
	// persisted by the Supervisor, and a remote endpoint can echo prompts,
	// tool arguments, credentials, or arbitrary private content in that body.
	_ = raw
	kind := OutcomePermanent
	reason := ProviderFailureProtocolIncompatible
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		reason = ProviderFailureAuthentication
	case http.StatusNotFound:
		// A bare 404 can also mean that the configured Anthropic-compatible
		// endpoint does not implement /v1/messages. Without an exact safe wire
		// code, keep the conservative protocol classification.
		reason = ProviderFailureProtocolIncompatible
	case http.StatusTooManyRequests:
		kind = OutcomeRateLimited
		reason = ProviderFailureRateLimit
	case http.StatusServiceUnavailable, 529:
		kind = OutcomeRetryable
		reason = ProviderFailureCapacity
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusGatewayTimeout:
		kind = OutcomeRetryable
		reason = ProviderFailureNetwork
	}
	message := fmt.Sprintf("returned HTTP %d", statusCode)
	err := NewProviderError(kind, provider, message, nil)
	err.StatusCode = statusCode
	err.RetryAfter = parseRetryAfter(retryAfterHeader, time.Now())
	err.Reason = reason
	return err
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds > int64(maxDuration/time.Second) {
			return maxDuration
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func (p *AnthropicCompatibleProvider) StreamChat(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	selectedModel := strings.TrimSpace(req.Model)
	if selectedModel == "" {
		selectedModel = p.defaultModel
	}
	wireModel, err := providerRequestModel(p.runtime, selectedModel)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not map model", nil)
	}
	body, err := p.toRequest(wireModel, req)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not prepare streaming request", err)
	}
	body.Stream = true
	secret, err := providerRequestCredential(ctx, p.name, p.apiKey, p.runtime)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name,
			"provider credential is unavailable", nil)
	}
	payload, err := providerRequestPayload(p.runtime, secret, body)
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not encode streaming request", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/v1/messages"), bytes.NewReader(payload))
	if err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name, "could not create streaming request", err)
	}
	if err := p.addHeaders(httpReq, secret, "text/event-stream"); err != nil {
		return nil, NewProviderError(OutcomePermanent, p.name,
			"could not prepare streaming request headers", nil)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, NormalizeProviderError(p.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if readErr != nil {
			return nil, NormalizeProviderError(p.name, readErr)
		}
		return nil, anthropicHTTPError(p.name, resp.StatusCode, resp.Header.Get("Retry-After"), raw)
	}
	ch := make(chan ChatChunk, 8)
	go p.readStream(ctx, resp.Body, selectedModel, ch)
	return ch, nil
}

func (p *AnthropicCompatibleProvider) readStream(ctx context.Context, body io.ReadCloser, defaultModel string, chunks chan<- ChatChunk) {
	defer close(chunks)
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	state := anthropicStreamState{model: defaultModel,
		preserveRequestedModel: p.runtime != nil,
		events:                 newProviderStreamEvents(p.name, defaultModel, "anthropic-response", StreamGranularityDelta)}
	dataLines := make([]string, 0, 1)
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
		if payload == "[DONE]" {
			_ = sendError(NewProviderError(OutcomeInvalidResponse, p.name,
				"stream ended without message_stop", io.ErrUnexpectedEOF))
			return false
		}
		chunk, done, err := state.consume([]byte(payload), p.name)
		if err != nil {
			_ = sendError(err)
			return false
		}
		if chunk != nil && !p.sendStreamChunk(ctx, chunks, *chunk) {
			return false
		}
		if done {
			stopped = true
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
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if !flush() || stopped || ctx.Err() != nil {
		return
	}
	if err := scanner.Err(); err != nil {
		_ = sendError(NewProviderError(OutcomeInvalidResponse, p.name, "stream read failed", err))
		return
	}
	_ = sendError(NewProviderError(OutcomeInvalidResponse, p.name,
		"stream ended before message_stop", io.ErrUnexpectedEOF))
}

func (p *AnthropicCompatibleProvider) sendStreamChunk(ctx context.Context, chunks chan<- ChatChunk, chunk ChatChunk) bool {
	if chunk.Done && strings.TrimSpace(chunk.Provider) == "" {
		chunk.Provider = p.name
	}
	select {
	case <-ctx.Done():
		return false
	case chunks <- chunk:
		return true
	}
}

func (p *AnthropicCompatibleProvider) SupportsTools(model string) bool {
	return true
}

func (p *AnthropicCompatibleProvider) SupportsVision(model string) bool {
	return false
}

func (p *AnthropicCompatibleProvider) SupportsJSONMode(model string) bool {
	return false
}

func (p *AnthropicCompatibleProvider) DescribeModelHarness(model string) ModelHarness {
	model = strings.TrimSpace(model)
	if model == "" {
		model = p.defaultModel
	}
	return ModelHarness{
		ProtocolVersion:     ModelHarnessProtocolVersion,
		TransportProtocol:   HarnessTransportAnthropicMessages,
		ToolStrategy:        HarnessToolStrategyNative,
		JSONStrategy:        HarnessJSONStrategyPrompt,
		QualificationStatus: HarnessQualificationRequired,
		BindingDigest: providerHarnessBinding(p.runtime, p.name, p.baseURL, model,
			HarnessTransportAnthropicMessages, HarnessToolStrategyNative,
			HarnessJSONStrategyPrompt),
	}
}

func (p *AnthropicCompatibleProvider) toRequest(model string, req ChatRequest) (anthropicMessageRequest, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	out := anthropicMessageRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
		Tools:     make([]anthropicTool, 0, len(req.Tools)),
	}
	if p.disableThinking {
		out.Thinking = &anthropicThinking{Type: "disabled"}
	}
	if req.Temperature > 0 {
		out.Temperature = &req.Temperature
	}
	var systemParts []string
	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		content := strings.TrimSpace(msg.Content)
		if content == "" && len(msg.ToolCalls) == 0 && len(msg.ToolResults) == 0 {
			continue
		}
		switch role {
		case "system":
			if len(msg.ToolCalls) > 0 || len(msg.ToolResults) > 0 {
				return anthropicMessageRequest{}, errors.New("system messages cannot contain tool blocks")
			}
			systemParts = append(systemParts, content)
		case "assistant":
			encoded, err := anthropicMessageContent(msg, true)
			if err != nil {
				return anthropicMessageRequest{}, err
			}
			out.Messages = append(out.Messages, anthropicMessage{Role: "assistant", Content: encoded})
		default:
			encoded, err := anthropicMessageContent(msg, false)
			if err != nil {
				return anthropicMessageRequest{}, err
			}
			out.Messages = append(out.Messages, anthropicMessage{Role: "user", Content: encoded})
		}
	}
	if len(req.Tools) > MaxProviderToolSpecs {
		return anthropicMessageRequest{}, errors.New("tool specification count exceeds the provider limit")
	}
	for index, spec := range req.Tools {
		name := strings.TrimSpace(spec.Name)
		description := strings.TrimSpace(spec.Description)
		parameters := append(json.RawMessage(nil), bytes.TrimSpace(spec.Parameters)...)
		if name == "" || len(parameters) == 0 || !json.Valid(parameters) {
			return anthropicMessageRequest{}, fmt.Errorf("invalid tool specification at index %d", index)
		}
		out.Tools = append(out.Tools, anthropicTool{Name: name, Description: description, InputSchema: parameters})
	}
	if len(systemParts) > 0 {
		out.System = strings.Join(systemParts, "\n\n")
	}
	if len(out.Messages) == 0 {
		out.Messages = append(out.Messages, anthropicMessage{Role: "user", Content: "Hello"})
	}
	return out, nil
}

func (p *AnthropicCompatibleProvider) endpoint(path string) string {
	if strings.HasSuffix(p.baseURL, path) {
		return p.baseURL
	}
	if strings.HasSuffix(p.baseURL, "/v1") && strings.HasPrefix(path, "/v1/") {
		return p.baseURL + strings.TrimPrefix(path, "/v1")
	}
	return p.baseURL + path
}

func (p *AnthropicCompatibleProvider) addHeaders(req *http.Request, secret string,
	accept string,
) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set("anthropic-version", AnthropicVersion)
	req.Header.Set("x-api-key", secret)
	req.Header.Set("Authorization", "Bearer "+secret)
	return applyProviderRequestHeaders(p.runtime, secret, req.Header)
}

type anthropicMessageRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

func anthropicMessageContent(message Message, assistant bool) (any, error) {
	content := strings.TrimSpace(message.Content)
	if assistant && len(message.ToolResults) > 0 {
		return nil, errors.New("assistant messages cannot contain tool results")
	}
	if !assistant && len(message.ToolCalls) > 0 {
		return nil, errors.New("user messages cannot contain tool calls")
	}
	if len(message.ToolCalls) == 0 && len(message.ToolResults) == 0 {
		return content, nil
	}
	blocks := make([]anthropicContentBlock, 0, 1+len(message.ToolCalls)+len(message.ToolResults))
	if assistant {
		if content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
		}
		calls, err := NormalizeToolCalls(message.ToolCalls)
		if err != nil {
			return nil, err
		}
		for _, call := range calls {
			blocks = append(blocks, anthropicContentBlock{
				Type: "tool_use", ID: call.ID, Name: call.Name,
				Input: append(json.RawMessage(nil), call.Arguments...),
			})
		}
	} else {
		for _, result := range message.ToolResults {
			normalized, err := NormalizeToolResult(result)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, anthropicContentBlock{
				Type: "tool_result", ToolUseID: normalized.ToolCallID,
				Content: normalized.Content, IsError: normalized.IsError,
			})
		}
		// Anthropic Messages requires every tool_result block to precede any
		// accompanying text in the same user message.
		if content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
		}
	}
	if len(blocks) == 0 {
		return nil, errors.New("structured Anthropic message has no content blocks")
	}
	return blocks, nil
}

type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicStreamState struct {
	model                  string
	inputTokens            int
	outputTokens           int
	toolBlocks             map[int]*anthropicStreamToolBlock
	textBlocks             map[int]bool
	privateBlocks          map[int]bool
	toolCalls              []ToolCall
	messageStarted         bool
	messageStopped         bool
	itemCount              int
	events                 providerStreamEvents
	preserveRequestedModel bool
}

type anthropicStreamToolBlock struct {
	id         string
	name       string
	input      json.RawMessage
	partial    strings.Builder
	hasPartial bool
}

func (s *anthropicStreamState) consume(payload []byte, provider string) (*ChatChunk, bool, error) {
	var event anthropicStreamEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "returned malformed stream event", err)
	}
	switch event.Type {
	case "message_start":
		if s.messageStarted || s.messageStopped {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned a duplicate message start", nil)
		}
		model := strings.TrimSpace(event.Message.Model)
		if model == "" || !utf8.ValidString(model) || len([]rune(model)) > MaxItemStreamIdentity ||
			strings.ContainsRune(model, 0) {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned an invalid model identity during the stream", nil)
		}
		if !s.preserveRequestedModel {
			s.model = model
			s.events.model = model
		}
		s.messageStarted = true
		s.inputTokens = event.Message.Usage.InputTokens
		s.outputTokens = event.Message.Usage.OutputTokens
		return &ChatChunk{Events: []StreamEvent{s.events.start()}}, false, nil
	case "content_block_start":
		if !s.messageStarted || s.messageStopped || event.Index < 0 || event.Index >= MaxProviderOutputItems ||
			s.blockExists(event.Index) {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned an invalid content block start", nil)
		}
		itemID := fmt.Sprintf("block/%d", event.Index)
		if event.ContentBlock.Type == "text" && event.ContentBlock.Text != "" {
			s.ensureBlockMaps()
			s.textBlocks[event.Index] = true
			s.itemCount++
			events := []StreamEvent{s.events.emit(StreamEvent{
				Type: StreamOutputItemStarted, ItemID: itemID, ItemType: StreamItemMessage,
				ItemStatus: StreamItemInProgress,
			})}
			events = append(events, s.events.emit(StreamEvent{
				Type: StreamTextDelta, ItemID: itemID, ItemType: StreamItemMessage,
				ItemStatus: StreamItemInProgress, TextDelta: event.ContentBlock.Text,
			}))
			return &ChatChunk{Text: event.ContentBlock.Text, Events: events}, false, nil
		}
		if event.ContentBlock.Type == "text" {
			s.ensureBlockMaps()
			s.textBlocks[event.Index] = true
			s.itemCount++
			return &ChatChunk{Events: []StreamEvent{s.events.emit(StreamEvent{
				Type: StreamOutputItemStarted, ItemID: itemID, ItemType: StreamItemMessage,
				ItemStatus: StreamItemInProgress,
			})}}, false, nil
		}
		if event.ContentBlock.Type == "tool_use" {
			if len(s.toolBlocks)+len(s.toolCalls) >= MaxProviderToolCalls {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
					"returned too many tool blocks", nil)
			}
			id := strings.TrimSpace(event.ContentBlock.ID)
			name := strings.TrimSpace(event.ContentBlock.Name)
			if err := validateStreamIdentity(id, "provider call"); err != nil {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
					"returned an invalid tool block identity", err)
			}
			if err := validateToolName(name); err != nil {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
					"returned an invalid tool block name", err)
			}
			s.ensureBlockMaps()
			s.toolBlocks[event.Index] = &anthropicStreamToolBlock{
				id: id, name: name,
				input: append(json.RawMessage(nil), event.ContentBlock.Input...),
			}
			s.itemCount++
			return &ChatChunk{Events: []StreamEvent{
				s.events.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: itemID,
					ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress}),
				s.events.emit(StreamEvent{Type: StreamToolCallStarted, ItemID: itemID, CallID: id,
					ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress, ToolName: name}),
			}}, false, nil
		}
		s.ensureBlockMaps()
		s.privateBlocks[event.Index] = true
	case "content_block_delta":
		if !s.messageStarted || s.messageStopped || event.Index < 0 ||
			event.Index >= MaxProviderOutputItems {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned a content delta outside an active message", nil)
		}
		itemID := fmt.Sprintf("block/%d", event.Index)
		if event.Delta.Type == "text_delta" {
			if event.Delta.Text == "" || !s.textBlocks[event.Index] ||
				s.toolBlocks[event.Index] != nil || s.privateBlocks[event.Index] {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
					"returned text for an unknown content block", nil)
			}
			events := []StreamEvent{s.events.emit(StreamEvent{
				Type: StreamTextDelta, ItemID: itemID, ItemType: StreamItemMessage,
				ItemStatus: StreamItemInProgress, TextDelta: event.Delta.Text,
			})}
			return &ChatChunk{Text: event.Delta.Text, Events: events}, false, nil
		}
		if event.Delta.Type == "input_json_delta" {
			block, exists := s.toolBlocks[event.Index]
			if !exists {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "returned tool JSON for an unknown block", nil)
			}
			if block.partial.Len()+len(event.Delta.PartialJSON) > MaxProviderToolPayloadSize {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "streamed tool arguments exceed the size limit", nil)
			}
			block.hasPartial = true
			_, _ = block.partial.WriteString(event.Delta.PartialJSON)
			if event.Delta.PartialJSON != "" {
				return &ChatChunk{Events: []StreamEvent{s.events.emit(StreamEvent{
					Type: StreamToolArgumentDelta, ItemID: itemID, CallID: block.id,
					ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
					ToolName: block.name, ArgumentDelta: event.Delta.PartialJSON,
				})}}, false, nil
			}
			return nil, false, nil
		}
		if !s.privateBlocks[event.Index] {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned an unsupported content block delta", nil)
		}
	case "content_block_stop":
		if !s.messageStarted || s.messageStopped || event.Index < 0 ||
			event.Index >= MaxProviderOutputItems {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned a content stop outside an active message", nil)
		}
		itemID := fmt.Sprintf("block/%d", event.Index)
		if s.textBlocks[event.Index] {
			delete(s.textBlocks, event.Index)
			return &ChatChunk{Events: []StreamEvent{s.events.emit(StreamEvent{
				Type: StreamOutputItemCompleted, ItemID: itemID, ItemType: StreamItemMessage,
				ItemStatus: StreamItemCompleted,
			})}}, false, nil
		}
		if block, exists := s.toolBlocks[event.Index]; exists {
			arguments := append(json.RawMessage(nil), block.input...)
			if block.hasPartial {
				arguments = json.RawMessage(block.partial.String())
			}
			if len(bytes.TrimSpace(arguments)) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			call, err := NormalizeToolCall(ToolCall{ID: block.id, Name: block.name, Arguments: arguments})
			if err != nil {
				return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "returned an invalid streamed tool call", err)
			}
			s.toolCalls = append(s.toolCalls, call)
			delete(s.toolBlocks, event.Index)
			callCopy := call
			return &ChatChunk{Events: []StreamEvent{
				s.events.emit(StreamEvent{Type: StreamToolCallCompleted, ItemID: itemID,
					CallID: block.id, ItemType: StreamItemToolCall,
					ItemStatus: StreamItemReadyForValidation, ToolName: block.name,
					CompletedCall: &callCopy}),
				s.events.emit(StreamEvent{Type: StreamOutputItemCompleted, ItemID: itemID,
					CallID: block.id, ItemType: StreamItemToolCall,
					ItemStatus: StreamItemCompleted, ToolName: block.name}),
			}}, false, nil
		}
		if s.privateBlocks[event.Index] {
			delete(s.privateBlocks, event.Index)
			return nil, false, nil
		}
		return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
			"returned a stop for an unknown content block", nil)
	case "message_delta":
		if !s.messageStarted || s.messageStopped {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider,
				"returned usage outside an active message", nil)
		}
		if event.Usage.InputTokens != 0 {
			s.inputTokens = event.Usage.InputTokens
		}
		s.outputTokens = event.Usage.OutputTokens
	case "message_stop":
		if !s.messageStarted || s.messageStopped || len(s.toolBlocks) != 0 ||
			len(s.textBlocks) != 0 || len(s.privateBlocks) != 0 || s.itemCount == 0 {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "message stopped with unfinished tool blocks", nil)
		}
		s.messageStopped = true
		calls, err := NormalizeToolCalls(s.toolCalls)
		if err != nil {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "returned invalid streamed tool calls", err)
		}
		chunk := s.finalChunk()
		chunk.ToolCalls = calls
		if err := chunk.Usage.Validate(); err != nil {
			return nil, false, NewProviderError(OutcomeInvalidResponse, provider, "returned invalid stream usage", err)
		}
		chunk.Events = []StreamEvent{s.events.terminalEvent(OutcomeSuccess, chunk.Usage)}
		return &chunk, true, nil
	case "error":
		kind := OutcomePermanent
		switch event.Error.Type {
		case "rate_limit_error":
			kind = OutcomeRateLimited
		case "overloaded_error", "api_error":
			kind = OutcomeRetryable
		}
		return nil, false, NewProviderError(kind, provider, event.Error.Message, nil)
	}
	return nil, false, nil
}

func (s *anthropicStreamState) ensureBlockMaps() {
	if s.toolBlocks == nil {
		s.toolBlocks = make(map[int]*anthropicStreamToolBlock)
	}
	if s.textBlocks == nil {
		s.textBlocks = make(map[int]bool)
	}
	if s.privateBlocks == nil {
		s.privateBlocks = make(map[int]bool)
	}
}

func (s *anthropicStreamState) blockExists(index int) bool {
	return s.toolBlocks[index] != nil || s.textBlocks[index] || s.privateBlocks[index]
}

func (s anthropicStreamState) finalChunk() ChatChunk {
	totalTokens := -1
	maxInt := int(^uint(0) >> 1)
	if s.inputTokens >= 0 && s.outputTokens >= 0 && s.inputTokens <= maxInt-s.outputTokens {
		totalTokens = s.inputTokens + s.outputTokens
	}
	usage := Usage{InputTokens: s.inputTokens, OutputTokens: s.outputTokens, TotalTokens: totalTokens}
	return ChatChunk{Done: true, ToolCalls: append([]ToolCall(nil), s.toolCalls...), Usage: &usage, Model: s.model}
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicMessageResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (r anthropicMessageResponse) Text() string {
	var parts []string
	for _, item := range r.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (r anthropicMessageResponse) ToolCalls() ([]ToolCall, error) {
	calls := make([]ToolCall, 0)
	for _, item := range r.Content {
		if item.Type != "tool_use" {
			continue
		}
		arguments := append(json.RawMessage(nil), item.Input...)
		if len(bytes.TrimSpace(arguments)) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		calls = append(calls, ToolCall{ID: item.ID, Name: item.Name, Arguments: arguments})
	}
	return NormalizeToolCalls(calls)
}

func (r anthropicMessageResponse) ModelOrDefault(model string) string {
	if strings.TrimSpace(r.Model) == "" {
		return model
	}
	return r.Model
}

func fallbackModelInfo(provider string, model string) []ModelInfo {
	if model == "" {
		model = "claude-3-5-sonnet-latest"
	}
	return []ModelInfo{{
		ID:           model,
		DisplayName:  model,
		Provider:     provider,
		Capabilities: []string{"chat", "tools"},
	}}
}
