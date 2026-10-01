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

const maxGeminiSignatureBytes = 256 * 1024

// GeminiThoughtSignatureScope identifies the Google AI Studio Gemini 3 Chat
// protocol. Model mapping is resolved by the caller; route aliases, Vertex,
// proxies and unrelated OpenAI-compatible providers do not imply this scope.
func GeminiThoughtSignatureScope(endpoint, wireModel string) bool {
	_, ok := geminiOpenAIEndpoint(endpoint)
	model, err := normalizeOpenAIModel(wireModel)
	return ok && err == nil && model == wireModel && (strings.HasPrefix(wireModel, "gemini-3-") || strings.HasPrefix(wireModel, "gemini-3."))
}

func geminiOpenAIEndpoint(endpoint string) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() != "generativelanguage.googleapis.com" ||
		(u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	switch u.Path {
	case "/v1beta/openai":
		u.Path += "/chat/completions"
	case "/v1beta/openai/chat/completions":
	default:
		return "", false
	}
	return u.String(), true
}

type geminiReplayMetadata struct {
	WireModel     string `json:"wire_model"`
	UpstreamModel string `json:"upstream_model"`
	Signature     string `json:"signature,omitempty"`
}

type geminiReplayTool struct {
	Signature string `json:"signature,omitempty"`
}

type geminiReplayBuilder struct {
	responseID string
	signature  string
	tools      map[int]string
	bytes      int
	text       strings.Builder
}

// Signatures are complete opaque values, unlike Anthropic signature_delta.
// Identical metadata may repeat in a stream; conflicting values are ambiguous.
func (b *geminiReplayBuilder) capture(raw json.RawMessage, toolIndex *int) error {
	signature, err := geminiSignature(raw)
	if err != nil || signature == "" {
		return err
	}
	previous := b.signature
	if toolIndex != nil {
		if *toolIndex < 0 || *toolIndex >= MaxProviderToolCalls {
			return errors.New("Gemini signature has an invalid tool position")
		}
		previous = b.tools[*toolIndex]
	}
	if previous != "" {
		if signature != previous {
			return errors.New("Gemini signature metadata changed")
		}
		return nil
	}
	if len(signature) > MaxProviderReplayBytes-b.bytes {
		return errors.New("Gemini signatures exceed their aggregate bound")
	}
	if toolIndex == nil {
		b.signature = signature
	} else {
		if b.tools == nil {
			b.tools = make(map[int]string)
		}
		b.tools[*toolIndex] = signature
	}
	b.bytes += len(signature)
	return nil
}

func (b *geminiReplayBuilder) observeID(id string) error {
	if id == "" {
		return nil
	}
	if !replayIdentity(id) || (b.responseID != "" && b.responseID != id) {
		return errors.New("Gemini response identity changed or is invalid")
	}
	b.responseID = id
	return nil
}

// Only the recognized Google field is retained. Extension siblings are inert
// and never copied into replay or the next request.
func geminiSignature(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var extra map[string]json.RawMessage
	if decodeGeminiReplayJSON(raw, &extra) != nil || extra == nil {
		return "", errors.New("Gemini extension metadata is invalid")
	}
	googleRaw, exists := extra["google"]
	if !exists {
		return "", nil
	}
	var google map[string]json.RawMessage
	if decodeGeminiReplayJSON(googleRaw, &google) != nil || google == nil {
		return "", errors.New("Gemini Google metadata is invalid")
	}
	signatureRaw, exists := google["thought_signature"]
	if !exists {
		return "", nil
	}
	var signature string
	if bytes.Equal(bytes.TrimSpace(signatureRaw), []byte("null")) ||
		json.Unmarshal(signatureRaw, &signature) != nil || signature == "" ||
		!utf8.ValidString(signature) || len(signature) > maxGeminiSignatureBytes {
		return "", errors.New("Gemini signature exceeds its UTF-8 byte bound or is invalid")
	}
	return signature, nil
}

func geminiExtraContent(signature string) json.RawMessage {
	if signature == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"google": map[string]string{"thought_signature": signature}})
	return raw
}

func geminiReplayPartID(responseID, kind string, index int) string {
	return StableStreamID("gemini-native-replay", responseID, kind, strconv.Itoa(index))
}

func (p *OpenAICompatibleProvider) geminiReplayBinding(routeModel, wireModel string) string {
	return providerHarnessBinding(p.runtime, p.name, p.baseURL, routeModel, wireModel,
		HarnessTransportOpenAIChatCompletions, "gemini-native-replay-v3")
}

func (p *OpenAICompatibleProvider) captureGeminiResponse(routeModel, wireModel string, response openAIChatResponse, result *ChatResponse) error {
	b := &geminiReplayBuilder{}
	if b.observeID(response.ID) != nil || b.capture(response.Choices[0].Message.ExtraContent, nil) != nil {
		return errors.New("Gemini response metadata is invalid")
	}
	for index, call := range response.Choices[0].Message.ToolCalls {
		if err := b.capture(call.ExtraContent, &index); err != nil {
			return err
		}
	}
	var err error
	upstreamModel, err := normalizeOpenAIModel(response.Model)
	if err != nil {
		return err
	}
	result.Replay, err = b.replay(p, routeModel, wireModel, upstreamModel, result.Text, result.ToolCalls)
	return err
}

func (b *geminiReplayBuilder) replay(p *OpenAICompatibleProvider, routeModel, wireModel, upstreamModel, text string, calls []ToolCall) (*ProviderReplay, error) {
	// The durable tool-round fence does not store optional text-only signatures
	// across later ordinary user turns. Do not claim that wider history scope.
	if len(calls) == 0 {
		return nil, nil
	}
	if !replayIdentity(b.responseID) || b.tools[0] == "" {
		return nil, errors.New("Gemini tool response omitted its native identity or first signature")
	}
	metadata, _ := json.Marshal(geminiReplayMetadata{WireModel: wireModel, UpstreamModel: upstreamModel, Signature: b.signature})
	r := &ProviderReplay{version: 3, provider: p.name, model: routeModel,
		transport: HarnessTransportOpenAIChatCompletions, binding: p.geminiReplayBinding(routeModel, wireModel),
		responseID: b.responseID, calls: make([]providerReplayCall, len(calls)),
		parts: []providerReplayPart{{Kind: "gemini_metadata", ID: geminiReplayPartID(b.responseID, "metadata", 0), Opaque: metadata}}}
	if text != "" {
		r.parts = append(r.parts, providerReplayPart{Kind: "text", ID: geminiReplayPartID(b.responseID, "text", 0), Text: redact.String(text)})
	}
	for index, call := range calls {
		digest, err := replayPayloadDigest(call.Arguments)
		if err != nil {
			return nil, err
		}
		r.calls[index] = providerReplayCall{WireID: call.ID, DurableID: call.ID, Name: call.Name, PayloadSHA256: digest}
		opaque, _ := json.Marshal(geminiReplayTool{Signature: b.tools[index]})
		r.parts = append(r.parts, providerReplayPart{Kind: "gemini_tool", ID: geminiReplayPartID(b.responseID, "tool", index), CallIndex: index, Opaque: opaque})
	}
	if _, err := r.EncodeForStore(); err != nil {
		return nil, err
	}
	return r, nil
}

func validateGeminiProviderReplay(r *ProviderReplay) error {
	if r == nil || r.version != 3 || r.transport != HarnessTransportOpenAIChatCompletions ||
		!replayIdentity(r.provider) || !replayIdentity(r.model) || !replayIdentity(r.responseID) || !replayDigest(r.binding) ||
		len(r.calls) == 0 || len(r.calls) > MaxProviderToolCalls || len(r.parts) < 2 || len(r.parts) > len(r.calls)+2 {
		return errors.New("Gemini replay envelope is invalid")
	}
	wireIDs, durableIDs := map[string]bool{}, map[string]bool{}
	for _, call := range r.calls {
		if !replayIdentity(call.WireID) || !replayIdentity(call.DurableID) || validateToolName(call.Name) != nil ||
			!replayDigest(call.PayloadSHA256) || wireIDs[call.WireID] || durableIDs[call.DurableID] {
			return errors.New("Gemini replay call bindings are invalid")
		}
		wireIDs[call.WireID], durableIDs[call.DurableID] = true, true
	}
	toolIndex, signatureBytes, size := 0, 0, len(r.responseID)
	for index, part := range r.parts {
		size += len(part.Opaque) + len(part.Text) + len(part.ID)
		if size > MaxProviderReplayBytes || part.Phase != "" {
			return errors.New("Gemini replay exceeds its bound or contains an unsupported phase")
		}
		signature := ""
		switch part.Kind {
		case "gemini_metadata":
			var metadata geminiReplayMetadata
			if index != 0 || part.ID != geminiReplayPartID(r.responseID, "metadata", 0) || part.Text != "" || part.CallIndex != 0 ||
				checkAnthropicReplayFields(part.Opaque, []string{"wire_model", "upstream_model"}, []string{"signature"}) != nil ||
				decodeGeminiReplayJSON(part.Opaque, &metadata) != nil {
				return errors.New("Gemini replay metadata is invalid")
			}
			if !geminiStoredSignatureValid(part.Opaque, metadata.Signature) {
				return errors.New("Gemini replay message signature is invalid")
			}
			if model, err := normalizeOpenAIModel(metadata.WireModel); err != nil || model != metadata.WireModel ||
				!GeminiThoughtSignatureScope("https://generativelanguage.googleapis.com/v1beta/openai", model) {
				return errors.New("Gemini replay wire model is invalid")
			}
			if model, err := normalizeOpenAIModel(metadata.UpstreamModel); err != nil || model != metadata.UpstreamModel {
				return errors.New("Gemini replay upstream model is invalid")
			}
			signature = metadata.Signature
		case "text":
			if index != 1 || part.ID != geminiReplayPartID(r.responseID, "text", 0) || part.CallIndex != 0 || len(part.Opaque) != 0 ||
				part.Text == "" || len(part.Text) > MaxModelOutputBytes || !utf8.ValidString(part.Text) || redact.String(part.Text) != part.Text {
				return errors.New("Gemini replay public text is invalid")
			}
		case "gemini_tool":
			var tool geminiReplayTool
			if part.Text != "" || part.CallIndex != toolIndex || toolIndex >= len(r.calls) ||
				part.ID != geminiReplayPartID(r.responseID, "tool", toolIndex) ||
				checkAnthropicReplayFields(part.Opaque, nil, []string{"signature"}) != nil || decodeGeminiReplayJSON(part.Opaque, &tool) != nil ||
				(toolIndex == 0 && tool.Signature == "") {
				return errors.New("Gemini replay tool position or signature is invalid")
			}
			if !geminiStoredSignatureValid(part.Opaque, tool.Signature) {
				return errors.New("Gemini replay tool signature is invalid")
			}
			signature = tool.Signature
			toolIndex++
		default:
			return errors.New("Gemini replay part is unsupported")
		}
		if len(signature) > maxGeminiSignatureBytes || !utf8.ValidString(signature) || len(signature) > MaxProviderReplayBytes-signatureBytes {
			return errors.New("Gemini replay signature exceeds its bound")
		}
		signatureBytes += len(signature)
	}
	if toolIndex != len(r.calls) {
		return errors.New("Gemini replay is missing tool positions")
	}
	return nil
}

// Reuse the bounded private JSON grammar, not Anthropic's envelope schema.
func validateGeminiWireJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxOpenAIResponseBytes || !utf8.Valid(raw) || !anthropicReplayJSONUnicode(raw) {
		return errors.New("Gemini native JSON exceeds its UTF-8 byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if validateAnthropicReplayJSONValue(decoder, 0, &nodes) != nil || ensureModelJSONEOF(decoder) != nil {
		return errors.New("Gemini native JSON is ambiguous")
	}
	return nil
}

func decodeGeminiReplayJSON(raw []byte, target any) error {
	if len(raw) > MaxProviderReplayBytes || validateGeminiWireJSON(raw) != nil {
		return errors.New("Gemini replay JSON exceeds its UTF-8 byte bound")
	}
	if _, stored := target.(*providerReplayStored); stored && validateGeminiReplayStoredJSON(raw) != nil {
		return errors.New("Gemini replay stored schema is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || ensureModelJSONEOF(decoder) != nil {
		return errors.New("Gemini replay JSON fields are invalid")
	}
	return nil
}

func geminiStoredSignatureValid(raw json.RawMessage, signature string) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	_, present := fields["signature"]
	return !present || signature != ""
}

func validateGeminiReplayStoredJSON(raw []byte) error {
	if err := checkAnthropicReplayFields(raw,
		[]string{"version", "provider", "model", "transport", "binding", "parts", "calls", "response_id"}, nil); err != nil {
		return err
	}
	var envelope struct {
		Parts []json.RawMessage `json:"parts"`
		Calls []json.RawMessage `json:"calls"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Gemini replay stored arrays are invalid")
	}
	for _, rawPart := range envelope.Parts {
		var part providerReplayPart
		if json.Unmarshal(rawPart, &part) != nil {
			return errors.New("Gemini replay stored part is invalid")
		}
		required, optional := []string{"kind", "id", "opaque"}, []string(nil)
		if part.Kind == "text" {
			required = []string{"kind", "id", "text"}
		} else if part.Kind == "gemini_tool" {
			optional = []string{"call_index"}
		}
		if err := checkAnthropicReplayFields(rawPart, required, optional); err != nil {
			return err
		}
	}
	for _, call := range envelope.Calls {
		if err := checkAnthropicReplayFields(call, []string{"wire_id", "durable_id", "name", "payload_sha256"}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *OpenAICompatibleProvider) geminiMessages(messages []Message, routeModel, wireModel string) ([]openAIMessage, error) {
	var out []openAIMessage
	aliases, pending := map[string]string{}, map[string]bool{}
	signatureBytes, currentTurn := 0, 0
	for index, message := range messages {
		if strings.ToLower(strings.TrimSpace(message.Role)) == "user" && (strings.TrimSpace(message.Content) != "" || len(message.Images) != 0) {
			currentTurn = index
		}
	}
	for index, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		mapped, err := openAIMessages(message)
		if err != nil {
			return nil, errors.New("Gemini replay message is invalid")
		}
		if message.Replay != nil {
			r := message.Replay
			if role != "assistant" || len(mapped) != 1 || len(pending) != 0 ||
				!r.matchesSource(p.name, routeModel, HarnessTransportOpenAIChatCompletions, p.geminiReplayBinding(routeModel, wireModel)) ||
				r.version != 3 || r.ValidateToolCalls(message.ToolCalls) != nil ||
				message.Content != r.AssistantText() {
				return nil, errors.New("Gemini replay source or accepted response changed")
			}
			var metadata geminiReplayMetadata
			_ = decodeGeminiReplayJSON(r.parts[0].Opaque, &metadata)
			if metadata.WireModel != wireModel {
				return nil, errors.New("Gemini replay wire model changed")
			}
			if len(metadata.Signature) > MaxProviderReplayBytes-signatureBytes {
				return nil, errors.New("Gemini replay history exceeds its signature bound")
			}
			if text := r.AssistantText(); text != "" {
				mapped[0].Content = &text
			}
			mapped[0].ExtraContent = geminiExtraContent(metadata.Signature)
			signatureBytes += len(metadata.Signature)
			aliases = make(map[string]string, len(r.calls))
			for _, part := range r.parts {
				if part.Kind != "gemini_tool" {
					continue
				}
				var tool geminiReplayTool
				_ = decodeGeminiReplayJSON(part.Opaque, &tool)
				if len(tool.Signature) > MaxProviderReplayBytes-signatureBytes {
					return nil, errors.New("Gemini replay history exceeds its signature bound")
				}
				signatureBytes += len(tool.Signature)
				bound := r.calls[part.CallIndex]
				mapped[0].ToolCalls[part.CallIndex].ID = bound.WireID
				mapped[0].ToolCalls[part.CallIndex].ExtraContent = geminiExtraContent(tool.Signature)
				aliases[bound.DurableID], pending[bound.DurableID] = bound.WireID, true
			}
		} else if role == "assistant" && len(message.ToolCalls) != 0 {
			if index >= currentTurn || len(pending) != 0 {
				return nil, errors.New("Gemini current tool turn has no native replay")
			}
			aliases = make(map[string]string, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				aliases[call.ID], pending[call.ID] = call.ID, true
			}
		}
		if len(message.ToolResults) != 0 {
			for resultIndex, result := range message.ToolResults {
				if !pending[result.ToolCallID] {
					return nil, errors.New("Gemini replay tool result has no matching pending call")
				}
				mapped[resultIndex].ToolCallID = aliases[result.ToolCallID]
				delete(pending, result.ToolCallID)
			}
		}
		if role == "user" && (strings.TrimSpace(message.Content) != "" || len(message.Images) != 0) && len(pending) != 0 {
			return nil, errors.New("Gemini replay started a new turn before completing tool results")
		}
		out = append(out, mapped...)
	}
	if len(pending) != 0 {
		return nil, errors.New("Gemini replay history is missing tool results")
	}
	return out, nil
}
