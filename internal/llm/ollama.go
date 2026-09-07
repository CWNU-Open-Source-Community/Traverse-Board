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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// OllamaDefaultBaseURL is the standard loopback endpoint. The provider is
	// only enabled when the operator configures the endpoint explicitly; this
	// constant is the documented fallback, never an implicit enablement.
	OllamaDefaultBaseURL = "http://127.0.0.1:11434"
	// OllamaDefaultName is the stable provider identity used by the registry,
	// routes, CLI, and diagnostics.
	OllamaDefaultName = "ollama"

	maxOllamaResponseBytes    = 2 << 20
	maxOllamaStreamLineBytes  = 1 << 20
	maxOllamaStreamEventBytes = 1 << 20
	maxOllamaModels           = 4096
	maxOllamaModelBytes       = 512
	maxOllamaContextLength    = 4 << 20
	maxOllamaShowModels       = 64
)

const (
	ollamaCapabilityCompletion = "completion"
	ollamaCapabilityTools      = "tools"
	ollamaCapabilityVision     = "vision"
)

// OllamaConfig configures the native loopback-only Ollama provider. There is
// deliberately no API key field: the local daemon is unauthenticated and the
// only authorization is the explicitly configured loopback endpoint.
type OllamaConfig struct {
	Name       string
	BaseURL    string
	HTTPClient *http.Client
}

// OllamaModelProbe is the bounded, content-free capability observation for one
// exact model. Every capability defaults to unsupported until the daemon
// explicitly reports it.
type OllamaModelProbe struct {
	Model         string
	Known         bool
	Tools         bool
	Vision        bool
	ContextLength int
}

// OllamaProvider implements the protocol-neutral Provider contract on top of
// the native Ollama HTTP API (tags/chat/show) for one explicitly configured
// loopback endpoint. Models are never trusted: capability claims come only
// from probed daemon metadata and stay unknown (therefore unsupported) until
// observed.
type OllamaProvider struct {
	name    string
	baseURL string
	client  *http.Client

	mu     sync.RWMutex
	models map[string]ollamaModelState
}

type ollamaModelState struct {
	known         bool
	tools         bool
	vision        bool
	contextLength int
}

// NewOllamaProvider builds a loopback-only Ollama provider. Non-loopback
// hosts, HTTPS, URL credentials, queries, fragments, path prefixes, and
// proxy-bearing transports are rejected up front.
func NewOllamaProvider(config OllamaConfig) (*OllamaProvider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = OllamaDefaultName
	}
	baseURL, err := normalizeOllamaBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	client, err := ollamaHTTPClient(config.HTTPClient)
	if err != nil {
		return nil, fmt.Errorf("HTTP client for provider %s is invalid: %w", name, err)
	}
	return &OllamaProvider{
		name: name, baseURL: baseURL, client: client,
		models: make(map[string]ollamaModelState),
	}, nil
}

func (p *OllamaProvider) Name() string { return p.name }

// ListModels reads the daemon tag list. The per-model capabilities field marks
// a model as probed: models whose capabilities are absent stay unknown and
// therefore report no capability until ProbeModel observes /api/show.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint("/api/tags"), nil)
	if err != nil {
		return nil, ollamaLocalError(p.name, "could not create model-list request")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, ollamaTransportError(ctx, p.name)
	}
	defer resp.Body.Close()
	raw, err := readOllamaBody(resp.Body)
	if err != nil {
		return nil, ollamaReadError(ctx, p.name, "could not read model-list response", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, ollamaHTTPError(p.name, resp.StatusCode, raw)
	}
	if !utf8.Valid(raw) {
		return nil, ollamaProtocolError(p.name, "returned a non-UTF-8 model list")
	}
	var payload struct {
		Models []struct {
			Name         string    "json:\"name\""
			Model        string    "json:\"model\""
			Capabilities *[]string "json:\"capabilities\""
		} "json:\"models\""
		Error string "json:\"error\""
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, ollamaProtocolError(p.name, "returned a malformed model list")
	}
	if payload.Error != "" {
		return nil, ollamaWireError(p.name, payload.Error, http.StatusOK)
	}
	if len(payload.Models) == 0 || len(payload.Models) > maxOllamaModels {
		return nil, ollamaProtocolError(p.name, "returned an invalid model list")
	}
	models := make([]ModelInfo, 0, len(payload.Models))
	seen := make(map[string]struct{}, len(payload.Models))
	p.mu.Lock()
	for _, item := range payload.Models {
		id, err := normalizeOllamaModel(item.Name)
		if err != nil {
			p.mu.Unlock()
			return nil, ollamaProtocolError(p.name, "returned an invalid model identifier")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		state := ollamaModelState{known: item.Capabilities != nil}
		if item.Capabilities != nil {
			capabilities := normalizeOllamaCapabilities(*item.Capabilities)
			state.tools = capabilities.tools
			state.vision = capabilities.vision
		}
		p.models[id] = state
		models = append(models, ModelInfo{
			ID: id, DisplayName: id, Provider: p.name,
			Capabilities: ollamaModelCapabilityList(state),
		})
	}
	p.mu.Unlock()
	if len(models) == 0 {
		return nil, ollamaProtocolError(p.name, "returned an empty model list")
	}
	return models, nil
}

// ProbeModel observes /api/show for one exact model and records the daemon's
// capability claims plus the reported context length. Absent fields leave the
// corresponding capability unknown and unsupported.
func (p *OllamaProvider) ProbeModel(ctx context.Context, model string) (OllamaModelProbe, error) {
	model, err := normalizeOllamaModel(model)
	if err != nil {
		return OllamaModelProbe{}, ollamaLocalError(p.name, "could not normalize the probed model")
	}
	body, err := json.Marshal(map[string]any{"model": model, "verbose": false})
	if err != nil {
		return OllamaModelProbe{}, ollamaLocalError(p.name, "could not encode the probe request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/api/show"), bytes.NewReader(body))
	if err != nil {
		return OllamaModelProbe{}, ollamaLocalError(p.name, "could not create the probe request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return OllamaModelProbe{}, ollamaTransportError(ctx, p.name)
	}
	defer resp.Body.Close()
	raw, err := readOllamaBody(resp.Body)
	if err != nil {
		return OllamaModelProbe{}, ollamaReadError(ctx, p.name, "could not read the probe response", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OllamaModelProbe{}, ollamaHTTPError(p.name, resp.StatusCode, raw)
	}
	if !utf8.Valid(raw) {
		return OllamaModelProbe{}, ollamaProtocolError(p.name, "returned a non-UTF-8 probe response")
	}
	var payload struct {
		Capabilities []string       "json:\"capabilities\""
		ModelInfo    map[string]any "json:\"model_info\""
		Error        string         "json:\"error\""
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return OllamaModelProbe{}, ollamaProtocolError(p.name, "returned a malformed probe response")
	}
	if payload.Error != "" {
		return OllamaModelProbe{}, ollamaWireError(p.name, payload.Error, http.StatusOK)
	}
	capabilities := normalizeOllamaCapabilities(payload.Capabilities)
	contextLength := ollamaContextLength(payload.ModelInfo)
	probe := OllamaModelProbe{
		Model: model, Known: true, Tools: capabilities.tools, Vision: capabilities.vision,
		ContextLength: contextLength,
	}
	p.mu.Lock()
	p.models[model] = ollamaModelState{
		known: true, tools: capabilities.tools, vision: capabilities.vision,
		contextLength: contextLength,
	}
	p.mu.Unlock()
	return probe, nil
}

// ProbeAll observes every currently listed model. Failures are bounded and
// reported; a probe failure never invalidates already-observed facts.
func (p *OllamaProvider) ProbeAll(ctx context.Context, limit int) ([]OllamaModelProbe, error) {
	if limit <= 0 || limit > maxOllamaShowModels {
		return nil, ollamaLocalError(p.name, "probe limit is out of range")
	}
	models, err := p.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]OllamaModelProbe, 0, min(len(models), limit))
	for index, model := range models {
		if index >= limit {
			break
		}
		probe, probeErr := p.ProbeModel(ctx, model.ID)
		if probeErr != nil {
			return results, probeErr
		}
		results = append(results, probe)
	}
	return results, nil
}

// ContextLength returns the probed context length for one model. Zero means
// the daemon reported none, which callers must treat as unavailable.
func (p *OllamaProvider) ContextLength(model string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state, ok := p.models[strings.TrimSpace(model)]
	if !ok {
		return 0
	}
	return state.contextLength
}

func (p *OllamaProvider) SupportsTools(model string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state, ok := p.models[strings.TrimSpace(model)]
	return ok && state.known && state.tools
}

func (p *OllamaProvider) SupportsVision(model string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state, ok := p.models[strings.TrimSpace(model)]
	return ok && state.known && state.vision
}

// SupportsJSONMode is true for any probed model: the JSON format is an
// Ollama wire-level grammar feature, while strict output validity remains a
// per-model Harness qualification.
func (p *OllamaProvider) SupportsJSONMode(model string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state, ok := p.models[strings.TrimSpace(model)]
	return ok && state.known
}

func (p *OllamaProvider) DescribeModelHarness(model string) ModelHarness {
	model = strings.TrimSpace(model)
	toolStrategy := HarnessToolStrategyNone
	jsonStrategy := HarnessJSONStrategyNone
	if p.SupportsTools(model) {
		toolStrategy = HarnessToolStrategyNative
	}
	if p.SupportsJSONMode(model) {
		jsonStrategy = HarnessJSONStrategyNative
	}
	return ModelHarness{
		ProtocolVersion:     ModelHarnessProtocolVersion,
		TransportProtocol:   HarnessTransportOllamaChat,
		ToolStrategy:        toolStrategy,
		JSONStrategy:        jsonStrategy,
		QualificationStatus: HarnessQualificationRequired,
		BindingDigest: harnessBindingDigest(p.name, p.baseURL, model,
			HarnessTransportOllamaChat, toolStrategy, jsonStrategy),
	}
}

func (p *OllamaProvider) Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error) {
	model, wire, messagesBytes, err := p.prepareRequest(request)
	if err != nil {
		return nil, ollamaLocalError(p.name, err.Error())
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, ollamaLocalError(p.name, "could not encode request")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint("/api/chat"), bytes.NewReader(payload))
	if err != nil {
		return nil, ollamaLocalError(p.name, "could not create request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, ollamaTransportError(ctx, p.name)
	}
	defer resp.Body.Close()
	raw, err := readOllamaBody(resp.Body)
	if err != nil {
		return nil, ollamaReadError(ctx, p.name, "could not read response", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, ollamaHTTPError(p.name, resp.StatusCode, raw)
	}
	if !utf8.Valid(raw) {
		return nil, ollamaProtocolError(p.name, "returned non-UTF-8 JSON")
	}
	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, ollamaProtocolError(p.name, "returned malformed JSON")
	}
	if parsed.Error != "" {
		return nil, ollamaWireError(p.name, parsed.Error, http.StatusOK)
	}
	return p.normalizeResponse(model, parsed, messagesBytes)
}

func (p *OllamaProvider) StreamChat(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	model, wire, messagesBytes, err := p.prepareRequest(request)
	if err != nil {
		return nil, ollamaLocalError(p.name, err.Error())
	}
	wire.Stream = true
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, ollamaLocalError(p.name, "could not encode streaming request")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint("/api/chat"), bytes.NewReader(payload))
	if err != nil {
		return nil, ollamaLocalError(p.name, "could not create streaming request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, ollamaTransportError(ctx, p.name)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		raw, readErr := readOllamaBody(resp.Body)
		if readErr != nil {
			return nil, ollamaReadError(ctx, p.name, "could not read streaming error", readErr)
		}
		return nil, ollamaHTTPError(p.name, resp.StatusCode, raw)
	}
	chunks := make(chan ChatChunk, 8)
	go p.readStream(ctx, resp.Body, model, messagesBytes, chunks)
	return chunks, nil
}

func (p *OllamaProvider) prepareRequest(request ChatRequest) (
	string, ollamaChatRequest, int, error,
) {
	model := strings.TrimSpace(request.Model)
	model, err := normalizeOllamaModel(model)
	if err != nil {
		return "", ollamaChatRequest{}, 0, errors.New("model is required and must be a normalized Ollama model name")
	}
	if math.IsNaN(request.Temperature) || math.IsInf(request.Temperature, 0) ||
		request.Temperature < 0 || request.Temperature > 2 {
		return "", ollamaChatRequest{}, 0, errors.New("temperature is outside the supported range")
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	if maxTokens > 1_000_000 {
		return "", ollamaChatRequest{}, 0, errors.New("max tokens exceeds the provider request limit")
	}
	wire := ollamaChatRequest{Model: model, Messages: make([]ollamaMessage, 0, len(request.Messages)+1)}
	wire.Options = &ollamaOptions{NumPredict: maxTokens}
	if request.Temperature > 0 {
		wire.Options.Temperature = &request.Temperature
	}
	if request.JSONMode {
		if !p.SupportsJSONMode(model) {
			return "", ollamaChatRequest{}, 0, errors.New("model JSON capability is not established")
		}
		wire.Format = "json"
	}
	messagesBytes := 0
	for index, message := range request.Messages {
		mapped, mappedBytes, mapErr := ollamaMessages(message)
		if mapErr != nil {
			return "", ollamaChatRequest{}, 0, fmt.Errorf("invalid message at index %d", index)
		}
		messagesBytes += mappedBytes
		wire.Messages = append(wire.Messages, mapped...)
	}
	if len(wire.Messages) == 0 {
		wire.Messages = append(wire.Messages, ollamaMessage{Role: "user", Content: "Hello"})
		messagesBytes += len("Hello")
	}
	if len(request.Tools) > MaxProviderToolSpecs {
		return "", ollamaChatRequest{}, 0, errors.New("tool specification count exceeds the provider limit")
	}
	if len(request.Tools) > 0 {
		// The no-tool safe path: an unprobed or tool-less model must never
		// receive tool schemas and must never fake a tool call.
		if !p.SupportsTools(model) {
			return "", ollamaChatRequest{}, 0, errors.New("model does not support tool calling")
		}
		for index, spec := range request.Tools {
			name := strings.TrimSpace(spec.Name)
			parameters := append(json.RawMessage(nil), bytes.TrimSpace(spec.Parameters)...)
			if err := validateOpenAIToolName(name); err != nil || len(parameters) == 0 ||
				len(parameters) > MaxProviderToolPayloadSize || !utf8.Valid(parameters) ||
				!json.Valid(parameters) {
				return "", ollamaChatRequest{}, 0,
					fmt.Errorf("invalid tool specification at index %d", index)
			}
			wire.Tools = append(wire.Tools, ollamaTool{Type: "function", Function: ollamaFunctionSpec{
				Name: name, Description: strings.TrimSpace(spec.Description), Parameters: parameters,
			}})
		}
	}
	return model, wire, messagesBytes, nil
}

func (p *OllamaProvider) normalizeResponse(model string, parsed ollamaChatResponse,
	messagesBytes int,
) (*ChatResponse, error) {
	if !parsed.Done {
		return nil, ollamaProtocolError(p.name, "returned an incomplete response")
	}
	if parsed.Message == nil {
		return nil, ollamaProtocolError(p.name, "returned a response without a message")
	}
	if role := strings.TrimSpace(parsed.Message.Role); role != "" && role != "assistant" {
		return nil, ollamaProtocolError(p.name, "returned an invalid message role")
	}
	text := parsed.Message.Content
	if !utf8.ValidString(text) || len(text) > MaxModelOutputBytes {
		return nil, ollamaProtocolError(p.name, "returned invalid message content")
	}
	toolCalls, err := normalizeOllamaToolCalls(parsed.Message.ToolCalls)
	if err != nil {
		return nil, ollamaProtocolError(p.name, "returned invalid tool calls")
	}
	usage := ollamaUsage(parsed.PromptEvalCount, parsed.EvalCount, text, messagesBytes)
	return &ChatResponse{
		Text: text, ToolCalls: toolCalls, Usage: usage, Model: model, Provider: p.name,
	}, nil
}

// ollamaUsage uses the daemon token counts when present and otherwise falls
// back to a conservative characters/4 estimate so budget accounting still has
// a bound on older servers that omit counts.
func ollamaUsage(promptEval, eval int, text string, messagesBytes int) Usage {
	if promptEval > 0 && eval > 0 {
		return Usage{InputTokens: promptEval, OutputTokens: eval,
			TotalTokens: promptEval + eval}
	}
	input := (messagesBytes + 3) / 4
	output := (len(text) + 3) / 4
	if input < 1 {
		input = 1
	}
	if output < 1 {
		output = 1
	}
	return Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output}
}

type ollamaChatRequest struct {
	Model    string          "json:\"model\""
	Messages []ollamaMessage "json:\"messages\""
	Stream   bool            "json:\"stream\""
	Format   string          "json:\"format,omitempty\""
	Tools    []ollamaTool    "json:\"tools,omitempty\""
	Options  *ollamaOptions  "json:\"options,omitempty\""
}

type ollamaOptions struct {
	Temperature *float64 "json:\"temperature,omitempty\""
	NumPredict  int      "json:\"num_predict,omitempty\""
}

type ollamaMessage struct {
	Role      string               "json:\"role\""
	Content   string               "json:\"content\""
	ToolCalls []ollamaWireToolCall "json:\"tool_calls,omitempty\""
}

type ollamaTool struct {
	Type     string             "json:\"type\""
	Function ollamaFunctionSpec "json:\"function\""
}

type ollamaFunctionSpec struct {
	Name        string          "json:\"name\""
	Description string          "json:\"description\""
	Parameters  json.RawMessage "json:\"parameters\""
}

type ollamaWireToolCall struct {
	Function ollamaWireFunction "json:\"function\""
}

type ollamaWireFunction struct {
	Name      string          "json:\"name\""
	Arguments json.RawMessage "json:\"arguments\""
}

type ollamaChatResponse struct {
	Model           string                 "json:\"model\""
	Message         *ollamaResponseMessage "json:\"message\""
	Done            bool                   "json:\"done\""
	DoneReason      string                 "json:\"done_reason\""
	PromptEvalCount int                    "json:\"prompt_eval_count\""
	EvalCount       int                    "json:\"eval_count\""
	Error           string                 "json:\"error\""
}

type ollamaResponseMessage struct {
	Role      string               "json:\"role\""
	Content   string               "json:\"content\""
	ToolCalls []ollamaWireToolCall "json:\"tool_calls\""
}

func ollamaMessages(message Message) ([]ollamaMessage, int, error) {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	content := strings.TrimSpace(message.Content)
	if content == "" && len(message.ToolCalls) == 0 && len(message.ToolResults) == 0 {
		return nil, 0, nil
	}
	bytesTotal := 0
	switch role {
	case "system":
		if len(message.ToolCalls) != 0 || len(message.ToolResults) != 0 || content == "" {
			return nil, 0, errors.New("system message has invalid structured content")
		}
		return []ollamaMessage{{Role: "system", Content: content}}, len(content), nil
	case "assistant":
		if len(message.ToolResults) != 0 {
			return nil, 0, errors.New("assistant message cannot contain tool results")
		}
		calls, err := NormalizeToolCalls(message.ToolCalls)
		if err != nil {
			return nil, 0, err
		}
		mapped := ollamaMessage{Role: "assistant", Content: content}
		bytesTotal += len(content)
		for _, call := range calls {
			arguments, argErr := normalizeOllamaArguments(call.Arguments)
			if argErr != nil {
				return nil, 0, argErr
			}
			mapped.ToolCalls = append(mapped.ToolCalls, ollamaWireToolCall{
				Function: ollamaWireFunction{Name: call.Name, Arguments: arguments},
			})
			bytesTotal += len(call.Name) + len(arguments)
		}
		return []ollamaMessage{mapped}, bytesTotal, nil
	case "user":
		if len(message.ToolCalls) != 0 {
			return nil, 0, errors.New("user message cannot contain tool calls")
		}
		mapped := make([]ollamaMessage, 0, len(message.ToolResults)+1)
		for _, result := range message.ToolResults {
			normalized, err := NormalizeToolResult(result)
			if err != nil {
				return nil, 0, err
			}
			mapped = append(mapped, ollamaMessage{Role: "tool", Content: normalized.Content})
			bytesTotal += len(normalized.Content)
		}
		if content != "" {
			mapped = append(mapped, ollamaMessage{Role: "user", Content: content})
			bytesTotal += len(content)
		}
		if len(mapped) == 0 {
			return nil, 0, errors.New("user message has no content")
		}
		return mapped, bytesTotal, nil
	default:
		return nil, 0, errors.New("message role is unsupported")
	}
}

func normalizeOllamaArguments(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := append(json.RawMessage(nil), bytes.TrimSpace(raw)...)
	if len(trimmed) == 0 || len(trimmed) > MaxProviderToolPayloadSize ||
		!utf8.Valid(trimmed) || !json.Valid(trimmed) {
		return nil, errors.New("tool call arguments are invalid")
	}
	// Ollama occasionally serializes arguments as a JSON string. Unwrap it so
	// the downstream schema decoder receives the object itself.
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, errors.New("tool call arguments string is invalid")
		}
		return append(json.RawMessage(nil), text...), nil
	}
	if trimmed[0] != '{' {
		return nil, errors.New("tool call arguments must be a JSON object")
	}
	return trimmed, nil
}

func normalizeOllamaToolCalls(wire []ollamaWireToolCall) ([]ToolCall, error) {
	if len(wire) == 0 {
		return nil, nil
	}
	if len(wire) > MaxProviderToolCalls {
		return nil, fmt.Errorf("tool call list exceeds %d items", MaxProviderToolCalls)
	}
	out := make([]ToolCall, len(wire))
	seen := make(map[string]struct{}, len(wire))
	for index, item := range wire {
		name := strings.TrimSpace(item.Function.Name)
		if err := validateOpenAIToolName(name); err != nil {
			return nil, fmt.Errorf("invalid tool call name at index %d", index)
		}
		arguments, err := normalizeOllamaArguments(item.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("invalid tool call arguments at index %d", index)
		}
		id := "ollama_call_" + strconv.Itoa(index)
		if _, exists := seen[id]; exists {
			return nil, errors.New("tool call ids must be unique")
		}
		seen[id] = struct{}{}
		out[index] = ToolCall{ID: id, Name: name, Arguments: arguments}
	}
	return out, nil
}

func normalizeOllamaModel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]byte(value)) > maxOllamaModelBytes {
		return "", errors.New("Ollama model name must be bounded UTF-8")
	}
	if strings.ContainsAny(value, " \t\r\n") || strings.ContainsRune(value, 0) ||
		strings.HasPrefix(value, ".") || strings.Contains(value, "..") {
		return "", errors.New("Ollama model name is invalid")
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "//") || strings.HasPrefix(value, ":") ||
		strings.Contains(value, "::") {
		return "", errors.New("Ollama model name is invalid")
	}
	return value, nil
}

func normalizeOllamaBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", errors.New("Ollama base url is required")
	}
	if len([]byte(value)) > maxProviderBaseURLBytes {
		return "", fmt.Errorf("Ollama base url exceeds %d bytes", maxProviderBaseURLBytes)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" {
		return "", errors.New("Ollama base url must be an absolute loopback HTTP URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Ollama base url cannot contain credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("Ollama base url cannot carry a path prefix")
	}
	if !providerLoopbackHost(parsed.Hostname()) {
		return "", errors.New("Ollama base url must use an explicit loopback host")
	}
	return value, nil
}

// ollamaHTTPClient forces a proxy-free transport: even when the process
// environment defines HTTP_PROXY, loopback Ollama requests must never be
// routed through a proxy, and non-loopback hosts are rejected before any
// request is built.
func ollamaHTTPClient(source *http.Client) (*http.Client, error) {
	client := &http.Client{Timeout: defaultProviderTimeout}
	if source != nil {
		copy := *source
		client = &copy
		if client.Timeout <= 0 || client.Timeout > defaultProviderTimeout {
			client.Timeout = defaultProviderTimeout
		}
	}
	switch transport := client.Transport.(type) {
	case nil:
		client.Transport = &http.Transport{Proxy: nil}
	case *http.Transport:
		clone := transport.Clone()
		clone.Proxy = nil
		client.Transport = clone
	default:
		return nil, errors.New("Ollama requires a proxy-free standard HTTP transport")
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, nil
}

func (p *OllamaProvider) endpoint(path string) string {
	return p.baseURL + path
}

func readOllamaBody(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxOllamaResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxOllamaResponseBytes {
		return nil, errors.New("response exceeds size limit")
	}
	return raw, nil
}

func ollamaLocalError(provider string, message string) *ProviderError {
	err := NewProviderError(OutcomePermanent, provider, message, nil)
	err.Reason = ProviderFailureProtocolIncompatible
	return err
}

func ollamaProtocolError(provider string, message string) *ProviderError {
	err := NewProviderError(OutcomeInvalidResponse, provider, message, nil)
	err.Reason = ProviderFailureProtocolIncompatible
	return err
}

func ollamaTransportError(ctx context.Context, provider string) *ProviderError {
	if ctx.Err() != nil {
		err := NewProviderError(OutcomeCancelled, provider, "request was cancelled", nil)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err.Reason = ProviderFailureNetwork
		}
		return err
	}
	// The loopback daemon is simply not running. This stable sentence is the
	// explainable "service not started" diagnostic; it never embeds a host,
	// path, or wire payload.
	err := NewProviderError(OutcomeRetryable, provider,
		"Ollama service is unreachable on the configured loopback endpoint", nil)
	err.Reason = ProviderFailureNetwork
	return err
}

func ollamaReadError(ctx context.Context, provider string, message string, source error) *ProviderError {
	if ctx.Err() != nil {
		return ollamaTransportError(ctx, provider)
	}
	var network net.Error
	if errors.As(source, &network) {
		err := NewProviderError(OutcomeRetryable, provider, message, nil)
		err.Reason = ProviderFailureNetwork
		return err
	}
	return ollamaProtocolError(provider, message)
}

func ollamaHTTPError(provider string, statusCode int, raw []byte) *ProviderError {
	kind := OutcomePermanent
	reason := ProviderFailureProtocolIncompatible
	switch statusCode {
	case http.StatusTooManyRequests:
		kind, reason = OutcomeRateLimited, ProviderFailureRateLimit
	case http.StatusServiceUnavailable, 529:
		kind, reason = OutcomeRetryable, ProviderFailureCapacity
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusGatewayTimeout:
		kind, reason = OutcomeRetryable, ProviderFailureNetwork
	}
	if statusCode == http.StatusNotFound {
		var envelope struct {
			Error string "json:\"error\""
		}
		if utf8.Valid(raw) && json.Unmarshal(raw, &envelope) == nil &&
			classifyOllamaWireError(envelope.Error) == ProviderFailureModelNotFound {
			reason = ProviderFailureModelNotFound
		}
	} else if utf8.Valid(raw) {
		var envelope struct {
			Error string "json:\"error\""
		}
		if json.Unmarshal(raw, &envelope) == nil {
			wireReason := classifyOllamaWireError(envelope.Error)
			if wireReason != ProviderFailureNone {
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
		}
	}
	err := NewProviderError(kind, provider, fmt.Sprintf("returned HTTP %d", statusCode), nil)
	err.StatusCode = statusCode
	err.Reason = reason
	return err
}

func ollamaWireError(provider string, message string, statusCode int) *ProviderError {
	reason := classifyOllamaWireError(message)
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
	err.StatusCode = statusCode
	err.Reason = reason
	return err
}

// classifyOllamaWireError maps untrusted wire error text onto a closed set of
// canonical categories using exact, content-free substrings. It never copies
// the upstream message out.
func classifyOllamaWireError(message string) ProviderFailureReason {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lower, "not found"):
		return ProviderFailureModelNotFound
	case strings.Contains(lower, "out of memory"),
		strings.Contains(lower, "insufficient memory"),
		strings.Contains(lower, "no memory"):
		return ProviderFailureCapacity
	case strings.Contains(lower, "rate limit"):
		return ProviderFailureRateLimit
	case strings.Contains(lower, "connection"), strings.Contains(lower, "unreachable"),
		strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"):
		return ProviderFailureNetwork
	default:
		return ProviderFailureNone
	}
}

func normalizeOllamaCapabilities(values []string) ollamaCapabilities {
	var out ollamaCapabilities
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case ollamaCapabilityTools:
			out.tools = true
		case ollamaCapabilityVision:
			out.vision = true
		case ollamaCapabilityCompletion:
			out.completion = true
		}
	}
	return out
}

func ollamaContextLength(modelInfo map[string]any) int {
	best := 0
	for key, raw := range modelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		value, ok := raw.(float64)
		if !ok || value <= 0 || value != math.Trunc(value) ||
			value > maxOllamaContextLength {
			continue
		}
		length := int(value)
		if length > best {
			best = length
		}
	}
	return best
}

func ollamaModelCapabilityList(state ollamaModelState) []string {
	if !state.known {
		return []string{}
	}
	list := make([]string, 0, 3)
	list = append(list, ollamaCapabilityCompletion)
	if state.tools {
		list = append(list, ollamaCapabilityTools)
	}
	if state.vision {
		list = append(list, ollamaCapabilityVision)
	}
	return list
}

type ollamaCapabilities struct {
	completion bool
	tools      bool
	vision     bool
}

// ollamaStreamState consumes the Ollama NDJSON chat stream. Each line is one
// JSON event; the terminal event carries done:true and the token counts.
type ollamaStreamState struct {
	provider      string
	model         string
	messagesBytes int
	upstreamModel string
	responseModel bool
	finished      bool
	textBytes     int
	hasText       bool
	textItemSeen  bool
	toolCount     int
	tools         []ToolCall
	usage         *Usage
	events        providerStreamEvents
}

func (s *ollamaStreamState) consume(line []byte) (*ChatChunk, error) {
	if len(line) == 0 {
		return nil, nil
	}
	if len(line) > maxOllamaStreamLineBytes {
		return nil, ollamaProtocolError(s.provider, "stream line exceeds its size limit")
	}
	if !utf8.Valid(line) {
		return nil, ollamaProtocolError(s.provider, "returned a non-UTF-8 stream event")
	}
	var event ollamaChatResponse
	if err := json.Unmarshal(line, &event); err != nil {
		return nil, ollamaProtocolError(s.provider, "returned a malformed stream event")
	}
	if event.Error != "" {
		return nil, ollamaWireError(s.provider, event.Error, http.StatusOK)
	}
	if strings.TrimSpace(event.Model) != "" {
		model, err := normalizeOllamaModel(event.Model)
		if err != nil {
			return nil, ollamaProtocolError(s.provider, "returned an invalid stream model")
		}
		if s.responseModel && model != s.upstreamModel {
			return nil, ollamaProtocolError(s.provider, "changed model identity during the stream")
		}
		s.upstreamModel, s.responseModel = model, true
	}
	if s.finished {
		return nil, ollamaProtocolError(s.provider, "received an event after the stream finished")
	}
	streamEvents := s.ensureStreamStarted()
	chunk := &ChatChunk{}
	if event.Message != nil {
		if role := strings.TrimSpace(event.Message.Role); role != "" && role != "assistant" {
			return nil, ollamaProtocolError(s.provider, "returned an invalid stream role")
		}
		if event.Message.Content != "" {
			text := event.Message.Content
			if !utf8.ValidString(text) || s.textBytes > MaxModelOutputBytes-len(text) {
				return nil, ollamaProtocolError(s.provider, "streamed text exceeds the output limit")
			}
			s.textBytes += len(text)
			s.hasText = true
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
		if len(event.Message.ToolCalls) != 0 {
			if s.toolCount != 0 {
				return nil, ollamaProtocolError(s.provider, "received duplicate tool calls during the stream")
			}
			calls, err := normalizeOllamaToolCalls(event.Message.ToolCalls)
			if err != nil {
				return nil, ollamaProtocolError(s.provider, "returned invalid tool calls")
			}
			s.tools = calls
			s.toolCount = len(calls)
			for index := range calls {
				itemID := fmt.Sprintf("tool/%d", index)
				call := calls[index]
				streamEvents = append(streamEvents,
					s.events.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: itemID,
						ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress}),
					s.events.emit(StreamEvent{Type: StreamToolCallStarted, ItemID: itemID,
						CallID: call.ID, ItemType: StreamItemToolCall,
						ItemStatus: StreamItemInProgress, ToolName: call.Name}),
					s.events.emit(StreamEvent{Type: StreamToolCallCompleted, ItemID: itemID,
						CallID: call.ID, ItemType: StreamItemToolCall,
						ItemStatus: StreamItemReadyForValidation, ToolName: call.Name,
						CompletedCall: &call}),
					s.events.emit(StreamEvent{Type: StreamOutputItemCompleted, ItemID: itemID,
						CallID: call.ID, ItemType: StreamItemToolCall,
						ItemStatus: StreamItemCompleted, ToolName: call.Name}),
				)
			}
		}
	}
	if event.Done {
		s.finished = true
		if s.toolCount == 0 && !s.hasText {
			return nil, ollamaProtocolError(s.provider, "stream produced no content")
		}
		if reason := strings.TrimSpace(event.DoneReason); reason != "" &&
			reason != "stop" && reason != "length" {
			return nil, ollamaProtocolError(s.provider, "returned an invalid done reason")
		}
		usage := ollamaUsage(event.PromptEvalCount, event.EvalCount, "", s.messagesBytes)
		s.usage = &usage
		if s.textItemSeen {
			streamEvents = append(streamEvents, s.events.emit(StreamEvent{
				Type: StreamOutputItemCompleted, ItemID: "message", ItemType: StreamItemMessage,
				ItemStatus: StreamItemCompleted,
			}))
		}
		streamEvents = append(streamEvents, s.events.terminalEvent(OutcomeSuccess, s.usage))
		chunk.Done = true
		chunk.ToolCalls = s.tools
		chunk.Events = streamEvents
		chunk.Usage = s.usage
		chunk.Model, chunk.Provider = s.model, s.provider
		return chunk, nil
	}
	chunk.Events = streamEvents
	if chunk.Text == "" && len(chunk.Events) == 0 {
		return nil, nil
	}
	chunk.Model, chunk.Provider = s.model, s.provider
	return chunk, nil
}

func (s *ollamaStreamState) ensureStreamStarted() []StreamEvent {
	if s.events.started {
		return nil
	}
	return []StreamEvent{s.events.start()}
}

func (s *ollamaStreamState) finalChunk() (ChatChunk, error) {
	if !s.finished {
		return ChatChunk{}, ollamaProtocolError(s.provider, "stream ended before the terminal event")
	}
	if s.toolCount == 0 && !s.hasText {
		return ChatChunk{}, ollamaProtocolError(s.provider, "stream produced no content")
	}
	usage := *s.usage
	return ChatChunk{Done: true, ToolCalls: s.tools, Usage: &usage,
		Model: s.model, Provider: s.provider}, nil
}

func (p *OllamaProvider) readStream(ctx context.Context, body io.ReadCloser,
	model string, messagesBytes int, chunks chan<- ChatChunk,
) {
	defer close(chunks)
	defer body.Close()
	state := ollamaStreamState{provider: p.name, model: model, messagesBytes: messagesBytes,
		events: newProviderStreamEvents(p.name, model, "ollama-response", StreamGranularityComplete)}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxOllamaStreamLineBytes)
	sendError := func(err error) bool {
		return p.sendStreamChunk(ctx, chunks, state.events.failureChunk(err))
	}
	// The reader keeps consuming until EOF: Ollama closes the connection after
	// the terminal event, and any non-empty trailing event must surface as a
	// protocol violation instead of being silently ignored.
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		chunk, err := state.consume([]byte(line))
		if err != nil {
			_ = sendError(err)
			return
		}
		if chunk != nil && !p.sendStreamChunk(ctx, chunks, *chunk) {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	if scanner.Err() != nil {
		_ = sendError(ollamaReadError(ctx, p.name, "stream read failed", scanner.Err()))
		return
	}
	if !state.finished {
		_ = sendError(ollamaProtocolError(p.name, "stream ended before the terminal event"))
	}
}

func (p *OllamaProvider) sendStreamChunk(ctx context.Context,
	chunks chan<- ChatChunk, chunk ChatChunk,
) bool {
	select {
	case <-ctx.Done():
		return false
	case chunks <- chunk:
		return true
	}
}
