package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const MaxProviderReplayBytes = 1024 * 1024

// ProviderReplay is adapter-owned protocol state, never public model output or
// tool authority. Ordinary JSON and diagnostic formatting cannot reveal it.
// Persistence must explicitly use EncodeForStore under the tool-round fence.
type ProviderReplay struct {
	version                             int
	provider, model, transport, binding string
	parts                               []providerReplayPart
	calls                               []providerReplayCall
}

type providerReplayPart struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	Phase     string          `json:"phase,omitempty"`
	Text      string          `json:"text,omitempty"`
	Opaque    json.RawMessage `json:"opaque,omitempty"`
	CallIndex int             `json:"call_index,omitempty"`
}

type providerReplayCall struct {
	WireID        string `json:"wire_id"`
	DurableID     string `json:"durable_id"`
	Name          string `json:"name"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type providerReplayStored struct {
	Version   int                  `json:"version"`
	Provider  string               `json:"provider"`
	Model     string               `json:"model"`
	Transport string               `json:"transport"`
	Binding   string               `json:"binding"`
	Parts     []providerReplayPart `json:"parts"`
	Calls     []providerReplayCall `json:"calls"`
}

func (*ProviderReplay) String() string   { return "<private provider replay>" }
func (*ProviderReplay) GoString() string { return "<private provider replay>" }

func newProviderReplay(provider, model, transport, binding string,
	parts []providerReplayPart, calls []ToolCall,
) (*ProviderReplay, error) {
	r := &ProviderReplay{version: 1, provider: provider, model: model, transport: transport, binding: binding,
		parts: append([]providerReplayPart(nil), parts...), calls: make([]providerReplayCall, len(calls))}
	for i := range r.parts {
		r.parts[i].Opaque = append(json.RawMessage(nil), r.parts[i].Opaque...)
		r.parts[i].Text = redact.String(r.parts[i].Text)
	}
	for i, call := range calls {
		digest, err := replayPayloadDigest(call.Arguments)
		if err != nil {
			return nil, err
		}
		r.calls[i] = providerReplayCall{WireID: call.ID, DurableID: call.ID, Name: call.Name, PayloadSHA256: digest}
	}
	if _, err := r.EncodeForStore(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ProviderReplay) Clone() *ProviderReplay {
	if r == nil {
		return nil
	}
	out := *r
	out.parts = append([]providerReplayPart(nil), r.parts...)
	out.calls = append([]providerReplayCall(nil), r.calls...)
	for i := range out.parts {
		out.parts[i].Opaque = append(json.RawMessage(nil), out.parts[i].Opaque...)
	}
	return &out
}

// BindToolCalls preserves the wire identities while binding exactly the batch
// accepted by the application. Hashing uses the router's existing JSON
// redactor; native arguments are never retained as a second source of truth.
func (r *ProviderReplay) BindToolCalls(prepared []ToolCall) (*ProviderReplay, error) {
	if r == nil {
		return nil, nil
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	if len(prepared) != len(r.calls) {
		return nil, errors.New("provider replay call count changed")
	}
	out := r.Clone()
	for i, call := range prepared {
		if call.Name != out.calls[i].Name {
			return nil, errors.New("provider replay call order changed")
		}
		digest, err := replayPayloadDigest(call.Arguments)
		if err != nil {
			return nil, err
		}
		out.calls[i].DurableID = call.ID
		out.calls[i].PayloadSHA256 = digest
	}
	if err := out.ValidateToolCalls(prepared); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ProviderReplay) ValidateSource(provider, model string) error {
	if r == nil {
		return nil
	}
	if err := r.validate(); err != nil {
		return err
	}
	if r.provider != provider || r.model != model {
		return errors.New("provider replay source changed")
	}
	return nil
}

func (r *ProviderReplay) matchesSource(provider, model, transport, binding string) bool {
	return r != nil && r.provider == provider && r.model == model && r.transport == transport && r.binding == binding
}

func (r *ProviderReplay) ValidateToolCalls(calls []ToolCall) error {
	if r == nil {
		return nil
	}
	if err := r.validate(); err != nil {
		return err
	}
	if len(calls) != len(r.calls) {
		return errors.New("provider replay call count does not match")
	}
	for i, call := range calls {
		digest, err := replayPayloadDigest(call.Arguments)
		if err != nil {
			return err
		}
		bound := r.calls[i]
		if call.ID != bound.DurableID || call.Name != bound.Name || digest != bound.PayloadSHA256 {
			return errors.New("provider replay does not match the accepted tool batch")
		}
	}
	return nil
}

func replayPayloadDigest(arguments json.RawMessage) (string, error) {
	safe, err := redactModelJSON(arguments)
	if err != nil {
		return "", errors.New("provider replay arguments are invalid")
	}
	digest := sha256.Sum256(safe)
	return hex.EncodeToString(digest[:]), nil
}

// AssistantText is the already-redacted public text from the same native
// response. Its pieces remain separately ordered inside adapter replay.
func (r *ProviderReplay) AssistantText() string {
	if r == nil {
		return ""
	}
	var out strings.Builder
	for _, part := range r.parts {
		if part.Kind == "text" {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

// ContextBytes conservatively accounts for private protocol state, without
// exposing its contents to a prompt, activity log or context summary.
func (r *ProviderReplay) ContextBytes() int {
	if r == nil {
		return 0
	}
	total := 0
	for _, part := range r.parts {
		total += len(part.Opaque) + len(part.ID) + len(part.Phase)
	}
	return total
}

func (r *ProviderReplay) EncodeForStore() ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(providerReplayStored{r.version, r.provider, r.model, r.transport, r.binding, r.parts, r.calls})
	if err != nil || len(raw) > MaxProviderReplayBytes {
		return nil, errors.New("provider replay exceeds its storage bound")
	}
	return raw, nil
}

func DecodeProviderReplay(raw []byte) (*ProviderReplay, error) {
	if len(raw) == 0 || len(raw) > MaxProviderReplayBytes || !utf8.Valid(raw) {
		return nil, errors.New("provider replay must be bounded UTF-8 JSON")
	}
	var value providerReplayStored
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || ensureModelJSONEOF(decoder) != nil {
		return nil, errors.New("provider replay encoding is invalid")
	}
	r := &ProviderReplay{value.Version, value.Provider, value.Model, value.Transport, value.Binding, value.Parts, value.Calls}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ProviderReplay) validate() error {
	if r == nil || r.version != 1 || r.transport != HarnessTransportOpenAIResponses ||
		!replayIdentity(r.provider) || !replayIdentity(r.model) || !replayDigest(r.binding) ||
		len(r.parts) == 0 || len(r.parts) > MaxProviderOutputItems || len(r.calls) > MaxProviderToolCalls {
		return errors.New("provider replay envelope is invalid")
	}
	wireIDs, durableIDs := map[string]bool{}, map[string]bool{}
	for _, call := range r.calls {
		if !replayIdentity(call.WireID) || !replayIdentity(call.DurableID) || validateToolName(call.Name) != nil ||
			!replayDigest(call.PayloadSHA256) || wireIDs[call.WireID] || durableIDs[call.DurableID] {
			return errors.New("provider replay call bindings are invalid")
		}
		wireIDs[call.WireID], durableIDs[call.DurableID] = true, true
	}
	ids, positions := map[string]bool{}, map[int]bool{}
	size, textBytes := 0, 0
	for _, part := range r.parts {
		if !replayIdentity(part.ID) || ids[part.ID] {
			return errors.New("provider replay item identity is invalid")
		}
		ids[part.ID] = true
		size += len(part.Opaque) + len(part.Text) + len(part.ID) + len(part.Phase)
		if size > MaxProviderReplayBytes {
			return errors.New("provider replay exceeds its byte bound")
		}
		switch part.Kind {
		case "reasoning", "compaction":
			if part.Text != "" || part.Phase != "" || part.CallIndex != 0 || validateReplayOpaque(part) != nil {
				return errors.New("provider replay opaque item is invalid")
			}
		case "text":
			textBytes += len(part.Text)
			if len(part.Opaque) != 0 || part.CallIndex != 0 || !utf8.ValidString(part.Text) || textBytes > MaxModelOutputBytes ||
				redact.String(part.Text) != part.Text || (part.Phase != "" && part.Phase != "commentary" && part.Phase != "final_answer") {
				return errors.New("provider replay public message is invalid")
			}
		case "tool":
			if len(part.Opaque) != 0 || part.Text != "" || part.Phase != "" || part.CallIndex < 0 || part.CallIndex >= len(r.calls) || positions[part.CallIndex] {
				return errors.New("provider replay tool position is invalid")
			}
			positions[part.CallIndex] = true
		default:
			return errors.New("provider replay item kind is unsupported")
		}
	}
	if len(positions) != len(r.calls) {
		return errors.New("provider replay is missing a tool position")
	}
	return nil
}

func validateReplayOpaque(part providerReplayPart) error {
	if len(part.Opaque) == 0 || !utf8.Valid(part.Opaque) {
		return errors.New("invalid opaque JSON")
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(part.Opaque, &value) != nil || value == nil {
		return errors.New("invalid opaque object")
	}
	for key := range value {
		if key != "id" && key != "type" && key != "status" && key != "encrypted_content" && !(part.Kind == "reasoning" && key == "summary") {
			return errors.New("unexpected opaque item field")
		}
	}
	var id, kind, status, encrypted string
	if json.Unmarshal(value["id"], &id) != nil || json.Unmarshal(value["type"], &kind) != nil || id != part.ID || kind != part.Kind {
		return errors.New("opaque item identity differs")
	}
	if raw, ok := value["status"]; ok && (json.Unmarshal(raw, &status) != nil || (status != "" && status != "completed")) {
		return errors.New("opaque item is incomplete")
	}
	if raw, ok := value["encrypted_content"]; ok && (json.Unmarshal(raw, &encrypted) != nil || !utf8.ValidString(encrypted)) {
		return errors.New("invalid opaque payload")
	}
	if part.Kind == "compaction" && encrypted == "" {
		return errors.New("missing opaque compaction payload")
	}
	if raw, ok := value["summary"]; ok {
		var summaries []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&summaries) != nil || ensureModelJSONEOF(decoder) != nil || len(summaries) > MaxProviderOutputItems {
			return errors.New("invalid reasoning summary")
		}
		for _, summary := range summaries {
			if summary.Type != "summary_text" || !utf8.ValidString(summary.Text) {
				return errors.New("invalid reasoning summary item")
			}
		}
	}
	return nil
}

func replayIdentity(value string) bool {
	return value != "" && len(value) <= MaxProviderToolIdentity*2 && strings.TrimSpace(value) == value && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func replayDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && strings.ToLower(value) == value
}
