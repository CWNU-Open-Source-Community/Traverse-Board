package llm

import (
	"errors"
	"fmt"
	"time"
)

// ErrPreparedRequestChanged is a local rejection before any provider call.
// Callers must release its exact reservation rather than charge unknown usage.
var ErrPreparedRequestChanged = errors.New("model configuration changed before dispatch; prepare the request again")

type routerGeneration struct{ marker byte }

type preparedModelRequest struct {
	router         *Router
	generation     *routerGeneration
	ref            ModelRef
	window         ContextWindow
	profile        ModelHarness
	qualification  HarnessQualification
	workload       HarnessWorkload
	optionalOutput bool
	jsonMode       bool
}

func (r *Router) contextWindowLocked(ref ModelRef, provider Provider) ContextWindow {
	key, valid := contextWindowKey(ref)
	if valid {
		if window, found := r.contextWindows[key]; found && window.Validate() == nil {
			return window
		}
		if described, ok := provider.(ModelContextDescriber); ok {
			if window := described.ModelContextWindow(ref.Model); window.Validate() == nil {
				return window
			}
		}
	}
	return DefaultContextWindow()
}

func (r *Router) harnessProfileLocked(ref ModelRef, provider Provider) (ModelHarness, error) {
	profile := providerContractHarness(provider, ref.Model)
	if described, ok := provider.(ModelHarnessDescriber); ok {
		profile = described.DescribeModelHarness(ref.Model)
	}
	if err := profile.Validate(); err != nil {
		return ModelHarness{}, err
	}
	return applyHarnessQualification(profile, r.qualifications[ref.Provider+"\x00"+ref.Model], time.Now().UTC()), nil
}

// PrepareModelRequest captures planning and provider identity together without
// requiring root Harness qualification (e.g. auxiliary context summaries).
func (r *Router) PrepareModelRequest(ref ModelRef, request ChatRequest) (ChatRequest, error) {
	if r == nil {
		return ChatRequest{}, errors.New("router is required")
	}
	if _, valid := contextWindowKey(ref); !valid || (request.Model != "" && request.Model != ref.Model) {
		return ChatRequest{}, errors.New("model request identity differs from its route")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, found := r.providers[ref.Provider]
	if !found {
		return ChatRequest{}, fmt.Errorf("provider %q is not registered", ref.Provider)
	}
	profile, err := r.harnessProfileLocked(ref, provider)
	if err != nil {
		return ChatRequest{}, err
	}
	optional := profile.TransportProtocol == HarnessTransportOpenAIChatCompletions ||
		profile.TransportProtocol == HarnessTransportOpenAIResponses || profile.TransportProtocol == HarnessTransportOllamaChat
	request.Model = ref.Model
	request.preparedModel = &preparedModelRequest{router: r, generation: r.generation, ref: ref,
		window: r.contextWindowLocked(ref, provider), profile: profile,
		qualification: r.qualifications[ref.Provider+"\x00"+ref.Model], optionalOutput: optional,
		jsonMode: provider.SupportsJSONMode(ref.Model)}
	return request, nil
}

// The final check and capture share one lock. The captured immutable provider
// is used directly after unlock, as for calls already in flight during reload.
func (r *Router) providerForRequestLocked(ref ModelRef, request ChatRequest) (Provider, bool, error) {
	provider, found := r.providers[ref.Provider]
	binding := request.preparedModel
	if binding == nil {
		return provider, found, nil
	}
	if !found || binding.router != r || binding.generation != r.generation || binding.ref != ref || request.Model != ref.Model {
		return nil, false, ErrPreparedRequestChanged
	}
	profile, err := r.harnessProfileLocked(ref, provider)
	if err != nil || profile.BindingDigest != binding.profile.BindingDigest ||
		r.qualifications[ref.Provider+"\x00"+ref.Model] != binding.qualification {
		return nil, false, ErrPreparedRequestChanged
	}
	if binding.workload != "" {
		if !profile.StrictJSONQualified || (len(request.Tools) > 0 &&
			(profile.ToolStrategy != HarnessToolStrategyNative || !profile.ToolCallsQualified || !profile.ToolResultsQualified || !profile.StreamingQualified)) {
			return nil, false, ErrPreparedRequestChanged
		}
	}
	return provider, true, nil
}

func (r *Router) ValidatePreparedRequest(ref ModelRef, request ChatRequest) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, _, err := r.providerForRequestLocked(ref, request)
	return err
}
