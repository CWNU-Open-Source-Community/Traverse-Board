package modelregistry

import (
	"bytes"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/llm"
)

// model_context_windows is operator configuration keyed by the wire model
// after model_mapping. It stays local and cannot override request fields.
func parseModelContextWindows(value any) (map[string]llm.ContextWindow, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 || len(object) > maxProviderDefinitionModels {
		return nil, errors.New("model_context_windows must contain named model policies")
	}
	result := make(map[string]llm.ContextWindow, len(object))
	for model, raw := range object {
		if !validAvailabilityIdentifier(model, maxPublicModelNameBytes) {
			return nil, errors.New("model context policy model is invalid")
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var limits struct {
			Window        int `json:"window_tokens"`
			DefaultOutput int `json:"default_output_tokens"`
			MaxOutput     int `json:"max_output_tokens"`
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&limits); err != nil {
			return nil, errors.New("model context policy fields are invalid")
		}
		window := llm.ContextWindow{ProtocolVersion: llm.ContextWindowProtocolVersion, WindowTokens: limits.Window,
			SafetyMarginTokens: llm.DefaultContextSafetyTokens, DefaultOutputTokens: limits.DefaultOutput,
			MaxOutputTokens: limits.MaxOutput, Source: "operator_model_policy"}
		if err := window.Validate(); err != nil {
			return nil, err
		}
		if window.MaxOutputTokens > 1_000_000 {
			return nil, errors.New("model output limit exceeds the adapter maximum of 1000000 tokens")
		}
		result[model] = window
	}
	return result, nil
}

func (r *providerRequestRuntime) ModelContextWindow(wireModel string) (llm.ContextWindow, bool) {
	window, found := r.contextWindows[wireModel]
	return window, found
}

func (r *providerRequestRuntime) ModelOutputOptions() (thinking, reasoningEffort string) {
	if raw, found := r.body["thinking"]; found {
		options, _ := raw.(map[string]any)
		thinking, _ = options["type"].(string)
		if thinking != "enabled" && thinking != "disabled" {
			thinking = "unknown"
		}
	}
	if raw, found := r.body["reasoning_effort"]; found {
		reasoningEffort, _ = raw.(string)
		switch reasoningEffort {
		case "none", "low", "minimal", "medium", "high", "xhigh", "max":
		default:
			reasoningEffort = "unknown"
		}
	}
	return thinking, reasoningEffort
}
