package llm

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Recognized public block metadata has a separate policy from signed thinking.
// Citation-bearing ordinary text is still projected as text, as before. Native
// private replay supports only absent/null/empty citations until citation
// reconstruction (including citations_delta) has its own protocol contract.
type anthropicReplayMetadata struct {
	Citations   json.RawMessage `json:"citations,omitempty"`
	Caller      json.RawMessage `json:"caller,omitempty"`
	ToolsetName json.RawMessage `json:"toolset_name,omitempty"`
}

func validateAnthropicCitations(raw json.RawMessage, replay bool) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var citations []map[string]json.RawMessage
	if json.Unmarshal(raw, &citations) != nil || citations == nil {
		return errors.New("Anthropic text citations are invalid")
	}
	for _, citation := range citations {
		if citation == nil {
			return errors.New("Anthropic text citation is invalid")
		}
	}
	if replay && len(citations) != 0 {
		return errors.New("Anthropic citation-bearing private replay is unsupported")
	}
	return nil
}

func validateAnthropicDirectCaller(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var caller struct {
		Type string `json:"type"`
	}
	if checkAnthropicReplayFields(raw, []string{"type"}, nil) != nil ||
		decodeAnthropicReplayJSON(raw, &caller) != nil || caller.Type != "direct" {
		return errors.New("Anthropic caller is not a direct local tool invocation")
	}
	return nil
}

func validateAnthropicLocalToolset(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var family string
	if json.Unmarshal(raw, &family) != nil || family != "" {
		return errors.New("Anthropic toolset invocation requires an explicit toolset contract")
	}
	return nil
}

func encodeAnthropicReplayMetadata(block anthropicReplayBlock) (json.RawMessage, error) {
	metadata := anthropicReplayMetadata{Citations: block.Citations, Caller: block.Caller, ToolsetName: block.ToolsetName}
	if len(metadata.Citations)+len(metadata.Caller)+len(metadata.ToolsetName) == 0 {
		return nil, nil
	}
	return json.Marshal(metadata)
}

func decodeAnthropicReplayMetadata(kind string, raw json.RawMessage) (anthropicReplayMetadata, error) {
	var metadata anthropicReplayMetadata
	if len(raw) == 0 {
		return metadata, nil
	}
	allowed := []string{"citations"}
	if kind == "tool" {
		allowed = []string{"caller", "toolset_name"}
	}
	if checkAnthropicReplayNullableFields(raw, nil, allowed, allowed) != nil ||
		decodeAnthropicReplayJSON(raw, &metadata) != nil ||
		validateAnthropicCitations(metadata.Citations, true) != nil ||
		validateAnthropicDirectCaller(metadata.Caller) != nil ||
		validateAnthropicLocalToolset(metadata.ToolsetName) != nil {
		return anthropicReplayMetadata{}, errors.New("Anthropic replay block metadata is invalid")
	}
	return metadata, nil
}
