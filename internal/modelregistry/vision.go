package modelregistry

import (
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/llm"
)

// model_capabilities is exact local model metadata, not request_body. A
// supported declaration permits native image input but is not a vision test.
func parseVisionCapabilities(value any) (map[string]llm.VisionSupport, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) > maxProviderDefinitionModels {
		return nil, errors.New("model_capabilities must be a bounded model object")
	}
	out := make(map[string]llm.VisionSupport, len(object))
	for model, raw := range object {
		entry, ok := raw.(map[string]any)
		if !validAvailabilityIdentifier(model, maxPublicModelNameBytes) || !ok || len(entry) != 1 {
			return nil, errors.New("model_capabilities requires exact model identities and only vision")
		}
		state, ok := entry["vision"].(string)
		if !ok || (state != "supported" && state != "unsupported" && state != "unknown") {
			return nil, errors.New("model vision must be supported, unsupported, or unknown")
		}
		out[model] = llm.VisionSupport(state)
	}
	return out, nil
}

func validateDefinedVisionModels(raw json.RawMessage, models []string) error {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	value, found := root["model_capabilities"]
	if !found {
		return nil
	}
	declared, err := parseVisionCapabilities(value)
	if err != nil {
		return err
	}
	for model := range declared {
		found := false
		for _, configured := range models {
			if model == configured {
				found = true
				break
			}
		}
		if !found {
			return errors.New("vision declarations must name a configured local model")
		}
	}
	return nil
}

func (r *providerRequestRuntime) DescribeVision(model string) llm.VisionCapability {
	if r != nil {
		if state, ok := r.vision[model]; ok {
			return llm.VisionCapability{State: state, Source: "operator_declared"}
		}
	}
	return llm.VisionCapability{State: llm.VisionUnknown, Source: "unknown"}
}

func (r *Registry) DescribeVision(provider, model string) llm.VisionCapability {
	if r == nil || r.router == nil {
		return llm.VisionCapability{State: llm.VisionUnknown, Source: "unknown"}
	}
	return r.router.DescribeVision(llm.ModelRef{Provider: provider, Model: model})
}
