package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

// These are the only native assistant blocks supported by this replay format.
// Pointers preserve required empty strings; private strings never enter Text.
type anthropicReplayBlock struct {
	Type        string          `json:"type"`
	Text        *string         `json:"text,omitempty"`
	Thinking    *string         `json:"thinking,omitempty"`
	Signature   *string         `json:"signature,omitempty"`
	Data        *string         `json:"data,omitempty"`
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	Citations   json.RawMessage `json:"citations,omitempty"`
	Caller      json.RawMessage `json:"caller,omitempty"`
	ToolsetName json.RawMessage `json:"toolset_name,omitempty"`
}

type anthropicReplayDelta struct {
	Type        string          `json:"type"`
	Text        *string         `json:"text,omitempty"`
	Thinking    *string         `json:"thinking,omitempty"`
	Signature   *string         `json:"signature,omitempty"`
	PartialJSON *string         `json:"partial_json,omitempty"`
	Citation    json.RawMessage `json:"citation,omitempty"`
}

func (b *anthropicReplayBlock) UnmarshalJSON(raw []byte) error {
	type wire anthropicReplayBlock
	var value wire
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("Anthropic native block fields are invalid")
	}
	var required, optional, nullable []string
	switch value.Type {
	case "thinking":
		required, optional = []string{"type", "thinking"}, []string{"signature"}
	case "redacted_thinking":
		required = []string{"type", "data"}
	case "text":
		required = []string{"type", "text"}
		optional, nullable = []string{"citations"}, []string{"citations"}
	case "tool_use":
		required = []string{"type", "id", "name", "input"}
		optional, nullable = []string{"caller", "toolset_name"}, []string{"caller", "toolset_name"}
	default:
		return errors.New("Anthropic native block type is unsupported")
	}
	if checkAnthropicReplayNullableFields(raw, required, optional, nullable) != nil {
		return errors.New("Anthropic native block schema is invalid")
	}
	*b = anthropicReplayBlock(value)
	return nil
}

func (d *anthropicReplayDelta) UnmarshalJSON(raw []byte) error {
	type wire anthropicReplayDelta
	var value wire
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("Anthropic native delta fields are invalid")
	}
	field := ""
	switch value.Type {
	case "text_delta":
		field = "text"
	case "thinking_delta":
		field = "thinking"
	case "signature_delta":
		field = "signature"
	case "input_json_delta":
		field = "partial_json"
	case "citations_delta":
		field = "citation"
	default:
		return errors.New("Anthropic native delta type is unsupported")
	}
	if checkAnthropicReplayFields(raw, []string{"type", field}, nil) != nil {
		return errors.New("Anthropic native delta schema is invalid")
	}
	*d = anthropicReplayDelta(value)
	return nil
}

type anthropicReplayPendingBlock struct {
	block            anthropicReplayBlock
	closed           bool
	signatureSeen    bool
	text             strings.Builder
	thinking         strings.Builder
	signature        strings.Builder
	partial          strings.Builder
	hasPartial       bool
	hasCitationDelta bool
}

// A builder belongs to one upstream response, not the newest response in a
// conversation. Earlier responses retain their own identity during replay.
type anthropicReplayBuilder struct {
	responseID string
	blocks     []*anthropicReplayPendingBlock
	bytes      int
	textBytes  int
}

func (b *anthropicReplayBuilder) hasPrivate() bool {
	for _, pending := range b.blocks {
		if pending.block.Type == "thinking" || pending.block.Type == "redacted_thinking" {
			return true
		}
	}
	return false
}

func (p *AnthropicCompatibleProvider) replayBinding(model string) string {
	return providerHarnessBinding(nil, p.DescribeModelHarness(model).BindingDigest,
		"anthropic-native-replay-v2", strconv.FormatBool(p.disableThinking))
}

func (b *anthropicReplayBuilder) startBlock(index int, raw json.RawMessage) error {
	if b == nil || index != len(b.blocks) || index >= MaxProviderOutputItems ||
		len(raw) > MaxProviderReplayBytes-b.bytes {
		return errors.New("Anthropic replay block exceeds its position or byte bound")
	}
	var block anthropicReplayBlock
	if decodeAnthropicReplayJSON(raw, &block) != nil || validateAnthropicReplayBlock(block, false) != nil {
		return errors.New("Anthropic replay content block is unsupported or invalid")
	}
	if block.Text != nil && len(*block.Text) > MaxModelOutputBytes-b.textBytes {
		return errors.New("Anthropic replay text exceeds its byte bound")
	}
	pending := &anthropicReplayPendingBlock{block: block}
	if block.Text != nil {
		pending.text.WriteString(*block.Text)
		b.textBytes += len(*block.Text)
	}
	if block.Thinking != nil {
		pending.thinking.WriteString(*block.Thinking)
	}
	if block.Signature != nil {
		pending.signature.WriteString(*block.Signature)
		pending.signatureSeen = *block.Signature != ""
	}
	b.blocks = append(b.blocks, pending)
	b.bytes += len(raw)
	return nil
}

func (b *anthropicReplayBuilder) appendDelta(index int, raw json.RawMessage) error {
	if b == nil || index < 0 || index >= len(b.blocks) || b.blocks[index].closed ||
		len(raw) > MaxProviderReplayBytes-b.bytes {
		return errors.New("Anthropic replay delta has no bounded active block")
	}
	var delta anthropicReplayDelta
	if decodeAnthropicReplayJSON(raw, &delta) != nil {
		return errors.New("Anthropic replay delta is invalid")
	}
	pending := b.blocks[index]
	if delta.Type == "citations_delta" {
		var citation map[string]json.RawMessage
		if pending.block.Type != "text" || json.Unmarshal(delta.Citation, &citation) != nil || citation == nil {
			return errors.New("Anthropic citation delta does not match its text block")
		}
		// Citation metadata is inert for ordinary public text. Do not silently
		// omit it from a signed private tool replay that we cannot reconstruct.
		pending.hasCitationDelta = true
		b.bytes += len(raw)
		return nil
	}
	var target **string
	var accumulated *strings.Builder
	var value *string
	switch delta.Type {
	case "text_delta":
		if pending.block.Type != "text" || delta.Thinking != nil || delta.Signature != nil || delta.PartialJSON != nil {
			return errors.New("Anthropic replay text delta does not match its block")
		}
		target, value = &pending.block.Text, delta.Text
		accumulated = &pending.text
	case "thinking_delta":
		if pending.block.Type != "thinking" || pending.signatureSeen || delta.Text != nil || delta.Signature != nil || delta.PartialJSON != nil {
			return errors.New("Anthropic replay thinking delta does not match its block")
		}
		target, value = &pending.block.Thinking, delta.Thinking
		accumulated = &pending.thinking
	case "signature_delta":
		if pending.block.Type != "thinking" || delta.Text != nil || delta.Thinking != nil || delta.PartialJSON != nil {
			return errors.New("Anthropic replay signature delta does not match its block")
		}
		target, value = &pending.block.Signature, delta.Signature
		accumulated = &pending.signature
	case "input_json_delta":
		if pending.block.Type != "tool_use" || delta.Text != nil || delta.Thinking != nil || delta.Signature != nil {
			return errors.New("Anthropic replay tool delta does not match its block")
		}
		value = delta.PartialJSON
	default:
		return errors.New("Anthropic replay delta type is unsupported")
	}
	if value == nil || !utf8.ValidString(*value) || len(*value) > MaxProviderReplayBytes-b.bytes {
		return errors.New("Anthropic replay delta is missing or exceeds its byte bound")
	}
	if delta.Type == "input_json_delta" {
		if len(*value) > MaxProviderToolPayloadSize-pending.partial.Len() {
			return errors.New("Anthropic replay tool input exceeds its byte bound")
		}
		pending.hasPartial = true
		pending.partial.WriteString(*value)
	} else {
		if delta.Type == "text_delta" && len(*value) > MaxModelOutputBytes-b.textBytes {
			return errors.New("Anthropic replay text exceeds its byte bound")
		}
		accumulated.WriteString(*value)
		combined := accumulated.String()
		*target = &combined
		if delta.Type == "text_delta" {
			b.textBytes += len(*value)
		}
	}
	if delta.Type == "signature_delta" {
		pending.signatureSeen = true
	}
	// Counting received JSON is conservative and also bounds escaping overhead
	// before accumulating private strings or re-encoding complete blocks.
	b.bytes += len(raw)
	return nil
}

func (b *anthropicReplayBuilder) endBlock(index int) error {
	if b == nil || index < 0 || index >= len(b.blocks) || b.blocks[index].closed {
		return errors.New("Anthropic replay stop has no active block")
	}
	pending := b.blocks[index]
	if pending.hasPartial {
		pending.block.Input = json.RawMessage(pending.partial.String())
	}
	// A truncated or tool-free reply may omit a final signature. Complete
	// private blocks are required by finish before any tool replay is accepted.
	if validateAnthropicReplayBlock(pending.block, false) != nil {
		return errors.New("Anthropic replay content block is incomplete or invalid")
	}
	pending.closed = true
	return nil
}

// finish binds the already validated adapter calls. Native input is checked
// here, then discarded: the accepted tool payload remains the sole source of
// outbound arguments. Only successful tool responses should attach this replay.
func (b *anthropicReplayBuilder) finish(provider, model, binding string, calls []ToolCall) (*ProviderReplay, error) {
	if b == nil || !replayIdentity(b.responseID) || len(b.blocks) == 0 {
		return nil, errors.New("Anthropic replay has no native response identity or content")
	}
	normalized, err := NormalizeToolCalls(calls)
	if err != nil {
		return nil, errors.New("Anthropic replay tool batch is invalid")
	}
	r := &ProviderReplay{version: 2, provider: provider, model: model,
		transport: HarnessTransportAnthropicMessages, binding: binding, responseID: b.responseID,
		parts: make([]providerReplayPart, 0, len(b.blocks)), calls: make([]providerReplayCall, len(normalized))}
	toolIndex := 0
	for index, pending := range b.blocks {
		if !pending.closed {
			return nil, errors.New("Anthropic replay content has an unfinished block")
		}
		if pending.hasCitationDelta {
			return nil, errors.New("Anthropic citation-bearing private replay is unsupported")
		}
		block := pending.block
		if validateAnthropicReplayBlock(block, true) != nil {
			return nil, errors.New("Anthropic replay has an incomplete native block")
		}
		part := providerReplayPart{ID: anthropicReplayPartID(b.responseID, index)}
		switch block.Type {
		case "thinking", "redacted_thinking":
			part.Kind = block.Type
			part.Opaque, err = json.Marshal(block)
			if err != nil {
				return nil, errors.New("Anthropic replay private block cannot be encoded")
			}
		case "text":
			part.Kind, part.Text = "text", redact.String(*block.Text)
			part.Opaque, err = encodeAnthropicReplayMetadata(block)
		case "tool_use":
			if toolIndex >= len(normalized) {
				return nil, errors.New("Anthropic replay has an unmatched native tool")
			}
			call := normalized[toolIndex]
			nativeDigest, nativeErr := replayPayloadDigest(block.Input)
			digest, digestErr := replayPayloadDigest(call.Arguments)
			if nativeErr != nil || digestErr != nil || nativeDigest != digest || block.ID != call.ID || block.Name != call.Name {
				return nil, errors.New("Anthropic replay native tool does not match its received batch")
			}
			r.calls[toolIndex] = providerReplayCall{WireID: call.ID, DurableID: call.ID, Name: call.Name, PayloadSHA256: digest}
			part.Kind, part.CallIndex = "tool", toolIndex
			part.Opaque, err = encodeAnthropicReplayMetadata(block)
			toolIndex++
		default:
			return nil, errors.New("Anthropic replay content type is unsupported")
		}
		if err != nil {
			return nil, errors.New("Anthropic replay block metadata cannot be encoded")
		}
		r.parts = append(r.parts, part)
	}
	if toolIndex != len(normalized) {
		return nil, errors.New("Anthropic replay is missing a native tool")
	}
	if _, err := r.EncodeForStore(); err != nil {
		return nil, err
	}
	return r, nil
}

func anthropicReplayPartID(responseID string, index int) string {
	return StableStreamID("anthropic-native-block", responseID, strconv.Itoa(index))
}

func validateAnthropicProviderReplay(r *ProviderReplay) error {
	if r == nil || r.version != 2 || r.transport != HarnessTransportAnthropicMessages ||
		!replayIdentity(r.provider) || !replayIdentity(r.model) || !replayIdentity(r.responseID) || !replayDigest(r.binding) ||
		len(r.parts) == 0 || len(r.parts) > MaxProviderOutputItems || len(r.calls) > MaxProviderToolCalls {
		return errors.New("Anthropic replay envelope is invalid")
	}
	wireIDs, durableIDs := map[string]bool{}, map[string]bool{}
	for _, call := range r.calls {
		if !replayIdentity(call.WireID) || !replayIdentity(call.DurableID) || validateToolName(call.Name) != nil ||
			!replayDigest(call.PayloadSHA256) || wireIDs[call.WireID] || durableIDs[call.DurableID] {
			return errors.New("Anthropic replay call bindings are invalid")
		}
		wireIDs[call.WireID], durableIDs[call.DurableID] = true, true
	}
	size, textBytes, toolIndex := len(r.responseID), 0, 0
	for index, part := range r.parts {
		if part.ID != anthropicReplayPartID(r.responseID, index) || part.Phase != "" {
			return errors.New("Anthropic replay block does not match its response position")
		}
		size += len(part.ID) + len(part.Text) + len(part.Opaque)
		if size > MaxProviderReplayBytes {
			return errors.New("Anthropic replay exceeds its byte bound")
		}
		switch part.Kind {
		case "thinking", "redacted_thinking":
			var block anthropicReplayBlock
			if part.Text != "" || part.CallIndex != 0 || decodeAnthropicReplayJSON(part.Opaque, &block) != nil ||
				block.Type != part.Kind || validateAnthropicReplayBlock(block, true) != nil {
				return errors.New("Anthropic replay private block is invalid")
			}
		case "text":
			textBytes += len(part.Text)
			_, metadataErr := decodeAnthropicReplayMetadata(part.Kind, part.Opaque)
			if metadataErr != nil || part.CallIndex != 0 || !utf8.ValidString(part.Text) ||
				textBytes > MaxModelOutputBytes || redact.String(part.Text) != part.Text {
				return errors.New("Anthropic replay public block is invalid")
			}
		case "tool":
			_, metadataErr := decodeAnthropicReplayMetadata(part.Kind, part.Opaque)
			if part.Text != "" || metadataErr != nil || part.CallIndex != toolIndex || toolIndex >= len(r.calls) {
				return errors.New("Anthropic replay tool position is invalid")
			}
			toolIndex++
		default:
			return errors.New("Anthropic replay block type is unsupported")
		}
	}
	if toolIndex != len(r.calls) {
		return errors.New("Anthropic replay is missing a tool position")
	}
	if redact.String(r.AssistantText()) != r.AssistantText() {
		return errors.New("Anthropic replay public text crosses a redaction boundary")
	}
	return nil
}

// Reconstruct each assistant response independently. Never compare an earlier
// response's native identity with the response being requested next.
func anthropicReplayMessage(message Message, provider, model, binding string) ([]anthropicReplayBlock, map[string]string, error) {
	r := message.Replay
	if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || len(message.Images) != 0 || len(message.ToolResults) != 0 ||
		r == nil || !r.matchesSource(provider, model, HarnessTransportAnthropicMessages, binding) ||
		validateAnthropicProviderReplay(r) != nil || message.Content != r.AssistantText() {
		return nil, nil, errors.New("Anthropic replay does not match its assistant source")
	}
	calls, err := NormalizeToolCalls(message.ToolCalls)
	if err != nil || r.ValidateToolCalls(calls) != nil {
		return nil, nil, errors.New("Anthropic replay does not match its assistant tool batch")
	}
	blocks := make([]anthropicReplayBlock, 0, len(r.parts))
	aliases := make(map[string]string, len(r.calls))
	for _, part := range r.parts {
		switch part.Kind {
		case "thinking", "redacted_thinking":
			var block anthropicReplayBlock
			if decodeAnthropicReplayJSON(part.Opaque, &block) != nil {
				return nil, nil, errors.New("Anthropic replay private block cannot be reconstructed")
			}
			blocks = append(blocks, block)
		case "text":
			text := part.Text
			metadata, _ := decodeAnthropicReplayMetadata(part.Kind, part.Opaque)
			blocks = append(blocks, anthropicReplayBlock{Type: "text", Text: &text, Citations: metadata.Citations})
		case "tool":
			call, bound := calls[part.CallIndex], r.calls[part.CallIndex]
			metadata, _ := decodeAnthropicReplayMetadata(part.Kind, part.Opaque)
			blocks = append(blocks, anthropicReplayBlock{Type: "tool_use", ID: bound.WireID,
				Name: call.Name, Input: append(json.RawMessage(nil), call.Arguments...),
				Caller: metadata.Caller, ToolsetName: metadata.ToolsetName})
			aliases[bound.DurableID] = bound.WireID
		}
	}
	return blocks, aliases, nil
}

func anthropicReplayResults(results []ToolResult, aliases map[string]string) ([]ToolResult, error) {
	if len(results) != len(aliases) {
		return nil, errors.New("Anthropic replay tool results do not match the native batch")
	}
	out := make([]ToolResult, 0, len(results))
	seen := make(map[string]bool, len(results))
	for _, result := range results {
		normalized, err := NormalizeToolResult(result)
		wireID, found := aliases[normalized.ToolCallID]
		if err != nil || !found || !replayIdentity(wireID) || seen[normalized.ToolCallID] {
			return nil, errors.New("Anthropic replay tool result identity is invalid")
		}
		seen[normalized.ToolCallID] = true
		normalized.ToolCallID = wireID
		out = append(out, normalized)
	}
	return out, nil
}

func validateAnthropicReplayBlock(block anthropicReplayBlock, complete bool) error {
	if block.Type == "" {
		return errors.New("Anthropic replay block has no type")
	}
	for _, value := range []*string{block.Text, block.Thinking, block.Signature, block.Data} {
		if value != nil && (!utf8.ValidString(*value) || len(*value) > MaxProviderReplayBytes) {
			return errors.New("Anthropic replay string exceeds its UTF-8 byte bound")
		}
	}
	noToolFields := block.ID == "" && block.Name == "" && len(block.Input) == 0
	privateOnly := noToolFields && len(block.Citations) == 0 && len(block.Caller) == 0 && len(block.ToolsetName) == 0
	switch block.Type {
	case "thinking":
		if !privateOnly || block.Text != nil || block.Data != nil || block.Thinking == nil ||
			(complete && (block.Signature == nil || *block.Signature == "")) {
			return errors.New("Anthropic thinking block is invalid")
		}
	case "redacted_thinking":
		if !privateOnly || block.Text != nil || block.Thinking != nil || block.Signature != nil || block.Data == nil || *block.Data == "" {
			return errors.New("Anthropic redacted thinking block is invalid")
		}
	case "text":
		if !noToolFields || block.Text == nil || block.Thinking != nil || block.Signature != nil || block.Data != nil ||
			len(block.Caller) != 0 || len(block.ToolsetName) != 0 || len(*block.Text) > MaxModelOutputBytes ||
			validateAnthropicCitations(block.Citations, complete) != nil {
			return errors.New("Anthropic text block is invalid")
		}
	case "tool_use":
		var object map[string]json.RawMessage
		if block.Text != nil || block.Thinking != nil || block.Signature != nil || block.Data != nil ||
			len(block.Citations) != 0 || validateAnthropicDirectCaller(block.Caller) != nil ||
			validateAnthropicLocalToolset(block.ToolsetName) != nil ||
			!replayIdentity(block.ID) || validateToolName(block.Name) != nil || len(block.Input) > MaxProviderToolPayloadSize ||
			decodeAnthropicReplayJSON(block.Input, &object) != nil || object == nil {
			return errors.New("Anthropic tool block is invalid")
		}
	default:
		return errors.New("Anthropic native content block is unsupported")
	}
	return nil
}

// Strict native decoding rejects ambiguous keys and unknown replay fields.
// Unknown SSE event types are handled separately by the adapter, not here.
func decodeAnthropicReplayJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > MaxProviderReplayBytes || !utf8.Valid(raw) ||
		!anthropicReplayJSONUnicode(raw) {
		return errors.New("Anthropic replay JSON exceeds its UTF-8 byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if validateAnthropicReplayJSONValue(decoder, 0, &nodes) != nil || ensureModelJSONEOF(decoder) != nil {
		return errors.New("Anthropic replay JSON is ambiguous or invalid")
	}
	if _, stored := target.(*providerReplayStored); stored && validateAnthropicReplayStoredJSON(raw) != nil {
		return errors.New("Anthropic replay stored schema is invalid")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || ensureModelJSONEOF(decoder) != nil {
		return errors.New("Anthropic replay JSON fields are invalid")
	}
	return nil
}

func checkAnthropicReplayFields(raw []byte, required, optional []string) error {
	return checkAnthropicReplayNullableFields(raw, required, optional, nil)
}

func checkAnthropicReplayNullableFields(raw []byte, required, optional, nullable []string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("Anthropic replay object is invalid")
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, found := fields[key]; !found {
			return errors.New("Anthropic replay object has a missing field")
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	allowsNull := make(map[string]bool, len(nullable))
	for _, key := range nullable {
		allowsNull[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || (!allowsNull[key] && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return errors.New("Anthropic replay object has an unsupported field")
		}
	}
	return nil
}

func validateAnthropicReplayStoredJSON(raw []byte) error {
	if err := checkAnthropicReplayFields(raw,
		[]string{"version", "provider", "model", "transport", "binding", "parts", "calls", "response_id"}, nil); err != nil {
		return err
	}
	var envelope struct {
		Parts []json.RawMessage `json:"parts"`
		Calls []json.RawMessage `json:"calls"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Anthropic replay stored arrays are invalid")
	}
	for _, part := range envelope.Parts {
		if err := checkAnthropicReplayFields(part, []string{"kind", "id"},
			[]string{"phase", "text", "opaque", "call_index"}); err != nil {
			return err
		}
	}
	for _, call := range envelope.Calls {
		if err := checkAnthropicReplayFields(call,
			[]string{"wire_id", "durable_id", "name", "payload_sha256"}, nil); err != nil {
			return err
		}
	}
	return nil
}

// encoding/json replaces unpaired UTF-16 escapes with a replacement rune.
// Private replay must reject them instead of changing opaque provider bytes.
func anthropicReplayJSONUnicode(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}

func validateAnthropicReplayJSONValue(decoder *json.Decoder, depth int, nodes *int) error {
	(*nodes)++
	if depth > 64 || *nodes > 100000 {
		return errors.New("Anthropic replay JSON exceeds its structural bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("Anthropic replay JSON token is invalid")
	}
	delimiter, structured := token.(json.Delim)
	if !structured {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || keys[name] {
				return errors.New("Anthropic replay JSON object key is ambiguous")
			}
			keys[name] = true
			if validateAnthropicReplayJSONValue(decoder, depth+1, nodes) != nil {
				return errors.New("Anthropic replay JSON object value is invalid")
			}
		}
	case '[':
		for decoder.More() {
			if validateAnthropicReplayJSONValue(decoder, depth+1, nodes) != nil {
				return errors.New("Anthropic replay JSON array value is invalid")
			}
		}
	default:
		return errors.New("Anthropic replay JSON delimiter is invalid")
	}
	closing, err := decoder.Token()
	if err != nil || (delimiter == '{' && closing != json.Delim('}')) || (delimiter == '[' && closing != json.Delim(']')) {
		return errors.New("Anthropic replay JSON structure is incomplete")
	}
	return nil
}
