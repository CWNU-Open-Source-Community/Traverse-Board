package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

// KimiReasoningScope describes only the official K3 Chat Completions contract.
// The caller resolves the wire model through its existing runtime mapping.
func KimiReasoningScope(endpoint, wireModel string) bool {
	_, ok := kimiOpenAIEndpoint(endpoint)
	return ok && wireModel == "kimi-k3"
}

func kimiOpenAIEndpoint(endpoint string) (string, bool) {
	// Use the constructor's normalization, including whitespace/trailing slashes.
	endpoint, err := normalizeProviderBaseURL(endpoint, "kimi")
	if err != nil || strings.ContainsAny(endpoint, "?#") {
		return "", false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "api.moonshot.ai" && u.Hostname() != "api.moonshot.cn") ||
		(u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	switch u.Path {
	case "/v1":
		u.Path += "/chat/completions"
	case "/v1/chat/completions":
	default:
		return "", false
	}
	return u.String(), true
}

// Reasoning is nullable and presence-aware. Content itself is the accepted,
// sanitized text part; only its original absent/null/string shape is private.
type kimiReplayMetadata struct {
	WireModel        string          `json:"wire_model"`
	UpstreamModel    string          `json:"upstream_model"`
	ContentState     string          `json:"content_state"`
	ReasoningContent json.RawMessage `json:"reasoning_content,omitempty"`
}

type kimiReplayBuilder struct {
	responseID   string
	mode         string
	reasonState  string
	reasoning    strings.Builder
	contentState string
	text         strings.Builder
}

func (b *kimiReplayBuilder) observeID(id string) error {
	if id == "" {
		return nil
	}
	if !replayIdentity(id) || (b.responseID != "" && b.responseID != id) {
		return errors.New("Kimi response identity changed or is invalid")
	}
	b.responseID = id
	return nil
}

func (b *kimiReplayBuilder) observeContent(content *string) {
	if content != nil {
		b.contentState = "string"
	}
}

// Unlike a Gemini signature, reasoning_content is incremental text. Null and
// absent fragments never erase a string; an observed empty string remains a
// string. Complete responses retain all three presence states independently.
func (b *kimiReplayBuilder) capture(raw json.RawMessage, stream bool) error {
	mode := "complete"
	if stream {
		mode = "stream"
	}
	if b.mode != "" && (b.mode != mode || !stream) {
		return errors.New("Kimi reasoning capture mode changed")
	}
	b.mode = mode
	state, value, err := kimiNullableString(raw)
	if err != nil {
		return err
	}
	switch state {
	case "absent":
		return nil
	case "null":
		if b.reasonState == "" {
			b.reasonState = "null"
		}
	case "string":
		if len(value) > MaxProviderReplayBytes-b.reasoning.Len() {
			return errors.New("Kimi reasoning exceeds its aggregate bound")
		}
		b.reasonState = "string"
		b.reasoning.WriteString(value)
	}
	return nil
}

func kimiNullableString(raw json.RawMessage) (string, string, error) {
	if len(raw) == 0 {
		return "absent", "", nil
	}
	if validateKimiWireJSON(raw) != nil {
		return "", "", errors.New("Kimi native string exceeds its UTF-8 bound or is invalid")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null", "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) {
		return "", "", errors.New("Kimi native value is not a nullable string")
	}
	return "string", value, nil
}

func kimiReplayPartID(responseID, kind string, index int) string {
	return StableStreamID("kimi-native-replay", responseID, kind, strconv.Itoa(index))
}

func (p *OpenAICompatibleProvider) kimiReplayBinding(routeModel, wireModel string) string {
	endpoint, _ := kimiOpenAIEndpoint(p.baseURL)
	return providerHarnessBinding(p.runtime, p.name, endpoint, routeModel, wireModel,
		HarnessTransportOpenAIChatCompletions, "kimi-native-replay-v4")
}

func (p *OpenAICompatibleProvider) captureKimiResponse(routeModel, wireModel string, response openAIChatResponse, result *ChatResponse) error {
	if result == nil || len(response.Choices) != 1 ||
		(len(result.ToolCalls) == 0 && openAIFinishReason(response.Choices[0].FinishReason) != FinishReasonStop) ||
		(len(result.ToolCalls) != 0 && openAIFinishReason(response.Choices[0].FinishReason) != FinishReasonToolCalls) {
		return errors.New("Kimi replay requires a completed native response")
	}
	message := response.Choices[0].Message
	if len(message.ToolCalls) != len(result.ToolCalls) {
		return errors.New("Kimi native tool batch changed during acceptance")
	}
	for index, native := range message.ToolCalls {
		if !replayIdentity(native.ID) || native.ID != result.ToolCalls[index].ID || native.Function.Name != result.ToolCalls[index].Name {
			return errors.New("Kimi native tool identity changed during acceptance")
		}
	}
	b := &kimiReplayBuilder{}
	if b.observeID(response.ID) != nil || b.capture(message.ReasoningContent, false) != nil {
		return errors.New("Kimi response metadata is invalid")
	}
	contentRaw := message.ContentRaw
	if len(contentRaw) == 0 && message.Content != nil {
		contentRaw, _ = json.Marshal(*message.Content)
	}
	state, nativeText, err := kimiNullableString(contentRaw)
	if err != nil || redact.String(nativeText) != redact.String(result.Text) {
		return errors.New("Kimi accepted content does not match its native response")
	}
	b.contentState = state
	upstreamModel, err := normalizeOpenAIModel(response.Model)
	if err != nil {
		return errors.New("Kimi upstream model identity is invalid")
	}
	result.Replay, err = b.replay(p, routeModel, wireModel, upstreamModel, result.Text, result.ToolCalls)
	return err
}

// Called only after the adapter's existing finish/usage/stream-completion
// validation. Even a zero-tool answer carries a private provenance marker.
func (b *kimiReplayBuilder) replay(p *OpenAICompatibleProvider, routeModel, wireModel, upstreamModel, text string, calls []ToolCall) (*ProviderReplay, error) {
	if p == nil || !KimiReasoningScope(p.baseURL, wireModel) || !replayIdentity(b.responseID) || len(calls) > MaxProviderToolCalls ||
		!utf8.ValidString(text) || len(text) > MaxProviderReplayBytes {
		return nil, errors.New("Kimi response has no supported native source")
	}
	metadata := kimiReplayMetadata{WireModel: wireModel, UpstreamModel: upstreamModel, ContentState: b.contentState}
	if metadata.ContentState == "" {
		metadata.ContentState = "absent"
		if text != "" {
			metadata.ContentState = "string"
		}
	}
	switch b.reasonState {
	case "null":
		metadata.ReasoningContent = json.RawMessage("null")
	case "string":
		metadata.ReasoningContent, _ = json.Marshal(b.reasoning.String())
	}
	opaque, err := json.Marshal(metadata)
	if err != nil || len(opaque) > MaxProviderReplayBytes {
		return nil, errors.New("Kimi replay metadata exceeds its bound")
	}
	r := &ProviderReplay{version: 4, provider: p.name, model: routeModel,
		transport: HarnessTransportOpenAIChatCompletions, binding: p.kimiReplayBinding(routeModel, wireModel),
		responseID: b.responseID, calls: make([]providerReplayCall, len(calls)),
		parts: []providerReplayPart{{Kind: "kimi_metadata", ID: kimiReplayPartID(b.responseID, "metadata", 0), Opaque: opaque}}}
	text = redact.String(text)
	if text != "" {
		r.parts = append(r.parts, providerReplayPart{Kind: "text", ID: kimiReplayPartID(b.responseID, "text", 0), Text: text})
	}
	for index, call := range calls {
		digest, err := replayPayloadDigest(call.Arguments)
		if err != nil {
			return nil, err
		}
		r.calls[index] = providerReplayCall{WireID: call.ID, DurableID: call.ID, Name: call.Name, PayloadSHA256: digest}
		r.parts = append(r.parts, providerReplayPart{Kind: "kimi_tool", ID: kimiReplayPartID(b.responseID, "tool", index), CallIndex: index})
	}
	if _, err := r.EncodeForStore(); err != nil {
		return nil, err
	}
	return r, nil
}

func validateKimiProviderReplay(r *ProviderReplay) error {
	if r == nil || r.version != 4 || r.transport != HarnessTransportOpenAIChatCompletions ||
		!replayIdentity(r.provider) || !replayIdentity(r.model) || !replayIdentity(r.responseID) || !replayDigest(r.binding) ||
		len(r.calls) > MaxProviderToolCalls || len(r.parts) < 1 || len(r.parts) > len(r.calls)+2 ||
		r.parts[0].Kind != "kimi_metadata" {
		return errors.New("Kimi replay envelope is invalid")
	}
	wireIDs, durableIDs := map[string]bool{}, map[string]bool{}
	for _, call := range r.calls {
		if !replayIdentity(call.WireID) || !replayIdentity(call.DurableID) || validateToolName(call.Name) != nil ||
			!replayDigest(call.PayloadSHA256) || wireIDs[call.WireID] || durableIDs[call.DurableID] {
			return errors.New("Kimi replay call bindings are invalid")
		}
		wireIDs[call.WireID], durableIDs[call.DurableID] = true, true
	}
	var metadata kimiReplayMetadata
	toolIndex, size := 0, len(r.responseID)
	for index, part := range r.parts {
		size += len(part.Opaque) + len(part.Text) + len(part.ID)
		if size > MaxProviderReplayBytes || part.Phase != "" {
			return errors.New("Kimi replay exceeds its bound or contains an unsupported phase")
		}
		switch part.Kind {
		case "kimi_metadata":
			if index != 0 || part.ID != kimiReplayPartID(r.responseID, "metadata", 0) || part.Text != "" || part.CallIndex != 0 ||
				checkAnthropicReplayNullableFields(part.Opaque, []string{"wire_model", "upstream_model", "content_state"},
					[]string{"reasoning_content"}, []string{"reasoning_content"}) != nil || decodeKimiReplayJSON(part.Opaque, &metadata) != nil {
				return errors.New("Kimi replay metadata is invalid")
			}
			if metadata.WireModel != "kimi-k3" || (metadata.ContentState != "absent" && metadata.ContentState != "null" && metadata.ContentState != "string") {
				return errors.New("Kimi replay model or content state is invalid")
			}
			if model, err := normalizeOpenAIModel(metadata.UpstreamModel); err != nil || model != metadata.UpstreamModel {
				return errors.New("Kimi replay upstream model is invalid")
			}
			if _, _, err := kimiNullableString(metadata.ReasoningContent); err != nil {
				return err
			}
		case "text":
			if index != 1 || metadata.ContentState != "string" || part.ID != kimiReplayPartID(r.responseID, "text", 0) ||
				part.CallIndex != 0 || len(part.Opaque) != 0 || part.Text == "" || len(part.Text) > MaxModelOutputBytes ||
				!utf8.ValidString(part.Text) || redact.String(part.Text) != part.Text {
				return errors.New("Kimi replay public text is invalid")
			}
		case "kimi_tool":
			if part.Text != "" || len(part.Opaque) != 0 || part.CallIndex != toolIndex || toolIndex >= len(r.calls) ||
				part.ID != kimiReplayPartID(r.responseID, "tool", toolIndex) {
				return errors.New("Kimi replay tool position is invalid")
			}
			toolIndex++
		default:
			return errors.New("Kimi replay part is unsupported")
		}
	}
	if toolIndex != len(r.calls) {
		return errors.New("Kimi replay is missing tool positions")
	}
	// Count storage overhead as well as native strings, without calling Encode
	// recursively. This same bound applies to reconstructed request history.
	calls := r.calls
	if calls == nil {
		calls = []providerReplayCall{}
	}
	raw, err := json.Marshal(providerReplayStored{Version: r.version, Provider: r.provider, Model: r.model,
		Transport: r.transport, Binding: r.binding, Parts: r.parts, Calls: calls, ResponseID: &r.responseID})
	if err != nil || len(raw) > MaxProviderReplayBytes {
		return errors.New("Kimi replay exceeds its storage bound")
	}
	return nil
}

// The shared grammar rejects duplicate keys, invalid UTF-8/surrogates, trailing
// values and excessive nesting/nodes. No provider-specific fields pass through.
func validateKimiWireJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxProviderReplayBytes || !utf8.Valid(raw) || !anthropicReplayJSONUnicode(raw) {
		return errors.New("Kimi native JSON exceeds its UTF-8 byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if validateAnthropicReplayJSONValue(decoder, 0, &nodes) != nil || ensureModelJSONEOF(decoder) != nil {
		return errors.New("Kimi native JSON is ambiguous")
	}
	return nil
}

func decodeKimiReplayJSON(raw []byte, target any) error {
	if validateKimiWireJSON(raw) != nil {
		return errors.New("Kimi replay JSON exceeds its UTF-8 byte bound or is ambiguous")
	}
	if _, stored := target.(*providerReplayStored); stored && validateKimiReplayStoredJSON(raw) != nil {
		return errors.New("Kimi replay stored schema is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || ensureModelJSONEOF(decoder) != nil {
		return errors.New("Kimi replay JSON fields are invalid")
	}
	return nil
}

func validateKimiReplayStoredJSON(raw []byte) error {
	if checkAnthropicReplayFields(raw, []string{"version", "provider", "model", "transport", "binding", "parts", "calls", "response_id"}, nil) != nil {
		return errors.New("Kimi replay stored envelope fields are invalid")
	}
	var envelope struct {
		Parts []json.RawMessage `json:"parts"`
		Calls []json.RawMessage `json:"calls"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Kimi replay stored arrays are invalid")
	}
	for _, rawPart := range envelope.Parts {
		var part providerReplayPart
		if json.Unmarshal(rawPart, &part) != nil {
			return errors.New("Kimi replay stored part is invalid")
		}
		required, optional := []string{"kind", "id", "opaque"}, []string(nil)
		switch part.Kind {
		case "text":
			required = []string{"kind", "id", "text"}
		case "kimi_tool":
			required, optional = []string{"kind", "id"}, []string{"call_index"}
		case "kimi_metadata":
		default:
			return errors.New("Kimi replay stored part kind is invalid")
		}
		if checkAnthropicReplayFields(rawPart, required, optional) != nil {
			return errors.New("Kimi replay stored part fields are invalid")
		}
	}
	for _, call := range envelope.Calls {
		if checkAnthropicReplayFields(call, []string{"wire_id", "durable_id", "name", "payload_sha256"}, nil) != nil {
			return errors.New("Kimi replay stored call fields are invalid")
		}
	}
	return nil
}

func (p *OpenAICompatibleProvider) kimiMessages(messages []Message, routeModel, wireModel string) ([]openAIMessage, error) {
	if !KimiReasoningScope(p.baseURL, wireModel) {
		return nil, errors.New("Kimi replay request has an unsupported source")
	}
	var out []openAIMessage
	aliases, pending := map[string]string{}, map[string]bool{}
	historyBytes := 0
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "assistant" && message.Replay == nil {
			return nil, errors.New("Kimi assistant history has no native replay")
		}
		mapped, err := openAIMessages(message)
		if err != nil {
			return nil, errors.New("Kimi replay message is invalid")
		}
		if len(pending) != 0 && (role != "user" || len(message.ToolResults) == 0) {
			return nil, errors.New("Kimi replay inserted a message before completing its tool batch")
		}
		if message.Replay != nil {
			r := message.Replay
			if role != "assistant" || len(pending) != 0 || r.version != 4 ||
				!r.matchesSource(p.name, routeModel, HarnessTransportOpenAIChatCompletions, p.kimiReplayBinding(routeModel, wireModel)) ||
				r.ValidateToolCalls(message.ToolCalls) != nil || message.Content != r.AssistantText() || len(message.ToolResults) != 0 || len(message.Images) != 0 {
				return nil, errors.New("Kimi replay source or accepted response changed")
			}
			if len(mapped) == 0 {
				mapped = []openAIMessage{{Role: "assistant"}}
			}
			if len(mapped) != 1 {
				return nil, errors.New("Kimi replay assistant message is invalid")
			}
			raw, err := r.EncodeForStore()
			if err != nil || len(raw) > MaxProviderReplayBytes-historyBytes {
				return nil, errors.New("Kimi replay history exceeds its private bound")
			}
			historyBytes += len(raw)
			var metadata kimiReplayMetadata
			if decodeKimiReplayJSON(r.parts[0].Opaque, &metadata) != nil || metadata.WireModel != wireModel {
				return nil, errors.New("Kimi replay wire model changed")
			}
			mapped[0].Content = nil
			switch metadata.ContentState {
			case "null":
				mapped[0].ContentRaw = json.RawMessage("null")
			case "string":
				mapped[0].ContentRaw, _ = json.Marshal(r.AssistantText())
			}
			mapped[0].ReasoningContent = append(json.RawMessage(nil), metadata.ReasoningContent...)
			aliases = make(map[string]string, len(r.calls))
			for index, bound := range r.calls {
				mapped[0].ToolCalls[index].ID = bound.WireID
				aliases[bound.DurableID], pending[bound.DurableID] = bound.WireID, true
			}
		}
		for index, result := range message.ToolResults {
			if role != "user" || !pending[result.ToolCallID] || index >= len(mapped) {
				return nil, errors.New("Kimi replay tool result has no matching pending call")
			}
			mapped[index].ToolCallID = aliases[result.ToolCallID]
			delete(pending, result.ToolCallID)
		}
		if role == "user" && (strings.TrimSpace(message.Content) != "" || len(message.Images) != 0) && len(pending) != 0 {
			return nil, errors.New("Kimi replay started a new turn before completing tool results")
		}
		out = append(out, mapped...)
	}
	if len(pending) != 0 {
		return nil, errors.New("Kimi replay history is missing tool results")
	}
	return out, nil
}
