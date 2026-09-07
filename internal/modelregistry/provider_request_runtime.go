package modelregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/llm"
)

// providerRequestRuntime is created from an already validated and normalized
// Provider definition. It deliberately interprets only the documented
// request_headers, request_body, and model_mapping containers. Every other
// advanced JSON field remains durable operator-owned extension data but has no
// runtime authority.
type providerRequestRuntime struct {
	providerID string
	credential credentialLookup
	headers    map[string]any
	body       map[string]any
	models     map[string]string
	binding    string
}

var _ llm.HTTPProviderRuntime = (*providerRequestRuntime)(nil)

// NewProviderRequestRuntime exposes the same credential-safe runtime policy to
// adjacent production adapters (for example, a separately qualified hosted
// search adapter) without duplicating advanced-config parsing rules.
func NewProviderRequestRuntime(definition ProviderDefinition,
	reader CredentialReader,
) (llm.HTTPProviderRuntime, error) {
	if reader == nil {
		return nil, errors.New("custom Provider credential reader is required")
	}
	return newProviderRequestRuntime(definition,
		func(ctx context.Context, provider string) (string, bool, error) {
			return reader.Get(ctx, provider)
		})
}

func newProviderRequestRuntime(definition ProviderDefinition,
	credentials credentialLookup,
) (*providerRequestRuntime, error) {
	normalized, err := ValidateAndNormalizeProviderAdvancedConfig(
		definition.AdvancedConfig, definition.ID)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(normalized)))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return nil, errors.New("custom Provider advanced config runtime is invalid")
	}
	runtime := &providerRequestRuntime{
		providerID: definition.ID,
		credential: credentials,
		headers:    map[string]any{},
		body:       map[string]any{},
		models:     map[string]string{},
	}
	if value, found := root["request_headers"]; found {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("custom Provider request headers runtime is invalid")
		}
		runtime.headers = object
	}
	if value, found := root["request_body"]; found {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("custom Provider request body runtime is invalid")
		}
		runtime.body = object
	}
	if value, found := root["model_mapping"]; found {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("custom Provider model mapping runtime is invalid")
		}
		for local, rawRemote := range object {
			remote, ok := rawRemote.(string)
			if !ok || !validAvailabilityIdentifier(local, maxPublicModelNameBytes) ||
				!validAvailabilityIdentifier(remote, maxPublicModelNameBytes) {
				return nil, errors.New("custom Provider model mapping runtime is invalid")
			}
			runtime.models[local] = remote
		}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("provider_request_runtime.v1\x00"))
	_, _ = hash.Write([]byte(definition.ID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatUint(definition.Revision, 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(normalized)
	runtime.binding = hex.EncodeToString(hash.Sum(nil))
	return runtime, nil
}

func (r *providerRequestRuntime) ResolveCredential(ctx context.Context) (string, error) {
	if r == nil || r.credential == nil || ctx == nil {
		return "", errors.New("custom Provider credential is unavailable")
	}
	secret, found, err := r.credential(ctx, r.providerID)
	if err != nil || !found || secret == "" {
		return "", errors.New("custom Provider credential is unavailable")
	}
	return secret, nil
}

func (r *providerRequestRuntime) MapModel(model string) (string, error) {
	if r == nil {
		return "", errors.New("custom Provider request runtime is unavailable")
	}
	if mapped, found := r.models[model]; found {
		return mapped, nil
	}
	return model, nil
}

func (r *providerRequestRuntime) Apply(secret string, headers http.Header,
	body map[string]any,
) error {
	if r == nil {
		return errors.New("custom Provider request runtime is unavailable")
	}
	if headers != nil {
		for name, raw := range r.headers {
			value, err := resolveAdvancedRuntimeValue(raw, secret)
			if err != nil {
				return errors.New("custom Provider request header is invalid")
			}
			text, ok := value.(string)
			if !ok {
				return errors.New("custom Provider request header is invalid")
			}
			headers.Set(name, text)
		}
	}
	if body != nil {
		for name, raw := range r.body {
			if _, exists := body[name]; exists {
				return errors.New("custom Provider request body conflicts with a Harness field")
			}
			value, err := resolveAdvancedRuntimeValue(raw, secret)
			if err != nil {
				return errors.New("custom Provider request body is invalid")
			}
			body[name] = value
		}
	}
	return nil
}

func (r *providerRequestRuntime) BindingDigest() string {
	if r == nil {
		return ""
	}
	return r.binding
}

func resolveAdvancedRuntimeValue(value any, secret string) (any, error) {
	switch current := value.(type) {
	case map[string]any:
		if reference, found := current["$credential"]; found {
			provider, ok := reference.(string)
			if !ok || provider == "" || len(current) > 2 {
				return nil, errors.New("credential reference is invalid")
			}
			result := secret
			if rawTemplate, templated := current["template"]; templated {
				template, ok := rawTemplate.(string)
				if !ok || strings.Count(template, "${secret}") != 1 {
					return nil, errors.New("credential reference template is invalid")
				}
				result = strings.Replace(template, "${secret}", secret, 1)
			}
			return result, nil
		}
		out := make(map[string]any, len(current))
		for key, child := range current {
			resolved, err := resolveAdvancedRuntimeValue(child, secret)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(current))
		for index, child := range current {
			resolved, err := resolveAdvancedRuntimeValue(child, secret)
			if err != nil {
				return nil, err
			}
			out[index] = resolved
		}
		return out, nil
	case nil, bool, json.Number, string:
		return current, nil
	default:
		return nil, errors.New("advanced runtime value is invalid")
	}
}
