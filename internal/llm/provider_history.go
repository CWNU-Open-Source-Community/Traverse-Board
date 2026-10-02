package llm

// RequiresPrivateAssistantHistory identifies adapter-owned state whose native
// assistant history cannot be replaced by ordinary text or a summary.
func (r *ProviderReplay) RequiresPrivateAssistantHistory() bool {
	return r != nil && r.version == 4
}

type privateAssistantHistoryProvider interface {
	RequiresPrivateAssistantHistory(string) bool
}

func (p *OpenAICompatibleProvider) RequiresPrivateAssistantHistory(model string) bool {
	if model == "" {
		model = p.defaultModel
	}
	wireModel, err := providerRequestModel(p.runtime, model)
	if err != nil {
		return false
	}
	wireModel, err = normalizeOpenAIModel(wireModel)
	return err == nil && KimiReasoningScope(p.baseURL, wireModel)
}

// This is a local protocol requirement, never a grant of tools or execution.
func (r *Router) RequiresPrivateAssistantHistory(ref ModelRef) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[ref.Provider].(privateAssistantHistoryProvider)
	return ok && p.RequiresPrivateAssistantHistory(ref.Model)
}
