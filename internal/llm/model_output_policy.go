package llm

import (
	"net/url"
	"strings"
)

// ModelContextDescriber describes the exact immutable provider/model binding.
// Unknown remote models retain the conservative local planning window.
type ModelContextDescriber interface {
	ModelContextWindow(model string) ContextWindow
}

// HTTPModelContextRuntime exposes operator-owned limits for the actual wire
// model. These values are local planning data, never request-body extensions.
type HTTPModelContextRuntime interface {
	ModelContextWindow(wireModel string) (ContextWindow, bool)
}

// ModelOutputOptions reports immutable options that affect a remote default.
// It must never resolve credentials or expose arbitrary request-body data.
type HTTPModelOutputOptions interface {
	ModelOutputOptions() (thinking, reasoningEffort string)
}

func httpModelContextWindow(baseURL, model, transport string, runtime HTTPProviderRuntime, thinkingDisabled bool) ContextWindow {
	wireModel, err := providerRequestModel(runtime, model)
	if err != nil {
		return DefaultContextWindow()
	}
	if configured, ok := runtime.(HTTPModelContextRuntime); ok {
		if window, found := configured.ModelContextWindow(wireModel); found && window.Validate() == nil {
			return window
		}
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Scheme != "https" || !strings.EqualFold(endpoint.Hostname(), "api.deepseek.com") ||
		(endpoint.Port() != "" && endpoint.Port() != "443") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return DefaultContextWindow()
	}
	path := strings.TrimRight(endpoint.Path, "/")
	if transport == HarnessTransportAnthropicMessages {
		if path != "/anthropic" && path != "/anthropic/v1" && path != "/anthropic/v1/messages" {
			return DefaultContextWindow()
		}
	} else if transport != HarnessTransportOpenAIChatCompletions || (path != "" && path != "/v1" && path != "/chat/completions" && path != "/v1/chat/completions") {
		return DefaultContextWindow()
	}
	switch wireModel {
	case "deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro":
	default:
		return DefaultContextWindow()
	}
	// Official metadata checked 2026-09-29: api-docs.deepseek.com/quick_start/pricing/
	// and /api/create-chat-completion/. Legacy Flash IDs are documented aliases.
	// The default is not the advertised maximum, and neither changes history policy.
	output := 64 * 1024
	if options, ok := runtime.(HTTPModelOutputOptions); ok {
		thinking, effort := options.ModelOutputOptions()
		if thinking == "unknown" || effort == "unknown" {
			return DefaultContextWindow()
		}
		thinkingDisabled = thinkingDisabled || thinking == "disabled"
		// reasoning_effort also controls the thinking toggle. Reserve the
		// larger thinking allowance if existing options request it.
		if effort != "" {
			thinkingDisabled = effort == "none"
		}
		if effort == "max" {
			output = 128 * 1024
		}
	}
	if thinkingDisabled {
		output = 8 * 1024
	}
	return ContextWindow{ProtocolVersion: ContextWindowProtocolVersion, WindowTokens: 1_000_000,
		SafetyMarginTokens: DefaultContextSafetyTokens, DefaultOutputTokens: output,
		MaxOutputTokens: 393216, Source: "provider_model_metadata"}
}

func (p *AnthropicCompatibleProvider) ModelContextWindow(model string) ContextWindow {
	return httpModelContextWindow(p.baseURL, model, HarnessTransportAnthropicMessages, p.runtime, p.disableThinking)
}
func (p *OpenAICompatibleProvider) ModelContextWindow(model string) ContextWindow {
	return httpModelContextWindow(p.baseURL, model, HarnessTransportOpenAIChatCompletions, p.runtime, false)
}
func (p *OpenAIResponsesProvider) ModelContextWindow(model string) ContextWindow {
	return httpModelContextWindow(p.baseURL, model, HarnessTransportOpenAIResponses, p.runtime, false)
}

// PlannedOutputTokens is a local reservation, not necessarily a wire limit.
func (r ChatRequest) PlannedOutputTokens(window ContextWindow) int {
	if r.MaxTokens > 0 {
		return window.OutputLimit(r.MaxTokens)
	}
	if window.Source == "conservative_default" && len(r.Tools) > 0 {
		return window.MaxOutputTokens
	}
	return window.OutputLimit(0)
}

func (r ChatRequest) AllowsDefaultOutput() bool {
	return r.preparedModel != nil && r.preparedModel.optionalOutput && r.preparedModel.window.Source != "operator_model_policy"
}

func (r ChatRequest) PreparedContextWindow() (ContextWindow, bool) {
	if r.preparedModel == nil {
		return ContextWindow{}, false
	}
	return r.preparedModel.window, true
}

func (r ChatRequest) PreparedSupportsJSONMode() bool {
	return r.preparedModel != nil && r.preparedModel.jsonMode
}
