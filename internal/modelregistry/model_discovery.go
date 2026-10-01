package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/redact"
)

const ModelDiscoveryVersion = "provider_model_discovery.v1"

type DiscoveredModel struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name,omitempty"`
	InputTokenLimit  int    `json:"input_token_limit,omitempty"`
	OutputTokenLimit int    `json:"output_token_limit,omitempty"`
}

type ModelDiscoveryResult struct {
	Version   string            `json:"version"`
	Source    string            `json:"source"`
	Models    []DiscoveredModel `json:"models"`
	Truncated bool              `json:"truncated"`
}

type ModelDiscoveryOptions struct {
	ProviderID     string
	EndpointURL    string
	Transport      string
	AdvancedConfig json.RawMessage
	Secret         string
}

// ValidateModelDiscoveryDraft validates an empty-model draft independently of
// Provider definitions. Fetching a catalog neither saves nor qualifies a model.
func ValidateModelDiscoveryDraft(o ModelDiscoveryOptions) error {
	if !ValidCustomProviderID(o.ProviderID) || validateDefinitionURL(o.EndpointURL, false, maxProviderEndpointURLBytes) != nil {
		return errors.New("model discovery Provider identity or endpoint is invalid")
	}
	if o.Transport != llm.HarnessTransportOpenAIChatCompletions && o.Transport != llm.HarnessTransportOpenAIResponses && o.Transport != llm.HarnessTransportAnthropicMessages {
		return errors.New("model discovery transport is invalid")
	}
	_, err := ValidateAndNormalizeProviderAdvancedConfig(o.AdvancedConfig, o.ProviderID)
	return err
}

// DiscoverProviderModels sends only bounded, cancellable GET requests to the
// frozen draft origin. It never falls back to a preset after an upstream error.
func DiscoverProviderModels(ctx context.Context, o ModelDiscoveryOptions) (ModelDiscoveryResult, error) {
	result := ModelDiscoveryResult{Version: ModelDiscoveryVersion, Source: "provider_api", Models: []DiscoveredModel{}}
	if ctx == nil || ValidateModelDiscoveryDraft(o) != nil {
		return result, errors.New("model discovery draft is invalid")
	}
	normalized, _ := ValidateAndNormalizeProviderAdvancedConfig(o.AdvancedConfig, o.ProviderID)
	var advanced map[string]json.RawMessage
	_ = json.Unmarshal(normalized, &advanced)
	var headers map[string]any
	if raw, ok := advanced["request_headers"]; ok {
		_ = json.Unmarshal(raw, &headers)
	}
	policy := &providerRequestRuntime{providerID: o.ProviderID, endpoint: o.EndpointURL, headers: headers}
	if o.Secret == "" && !policy.AllowsKeylessEndpoint(o.EndpointURL) {
		return result, errors.New("model discovery requires an API key")
	}
	if o.Secret != "" && (!credential.ValidSecret(o.Secret) || len(o.Secret) < 8) {
		return result, errors.New("model discovery API key is invalid")
	}
	u, _ := url.Parse(o.EndpointURL)
	google := u.Scheme == "https" && strings.EqualFold(u.Hostname(), "generativelanguage.googleapis.com") && (u.Port() == "" || u.Port() == "443")
	deepseek := u.Scheme == "https" && strings.EqualFold(u.Hostname(), "api.deepseek.com") && (u.Port() == "" || u.Port() == "443")
	anthropicCatalog := o.Transport == llm.HarnessTransportAnthropicMessages && !google && !deepseek
	path := strings.TrimRight(u.Path, "/")
	if deepseek {
		u.Path = "/models"
	} else if google && (path == "/v1beta" || strings.HasPrefix(path, "/v1beta/")) {
		u.Path = "/v1beta/models"
	} else {
		for _, suffix := range []string{"/chat/completions", "/responses", "/messages"} {
			if strings.HasSuffix(path, suffix) {
				path = strings.TrimSuffix(path, suffix)
				break
			}
		}
		if path == "" && !strings.EqualFold(u.Hostname(), "api.deepseek.com") {
			path = "/v1"
		}
		u.Path = path + "/models"
	}
	u.RawPath = ""
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	seenModels, seenCursors := map[string]bool{}, map[string]bool{}
	cursor := ""
	for page := 0; page < 8; page++ {
		q := url.Values{}
		if google {
			q.Set("pageSize", "100")
			if cursor != "" {
				q.Set("pageToken", cursor)
			}
		}
		if anthropicCatalog {
			q.Set("limit", "100")
			if cursor != "" {
				q.Set("after_id", cursor)
			}
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return result, errors.New("model discovery request is invalid")
		}
		req.Header.Set("Accept", "application/json")
		if o.Secret != "" {
			if google {
				req.Header.Set("x-goog-api-key", o.Secret)
			} else if anthropicCatalog {
				req.Header.Set("x-api-key", o.Secret)
			} else {
				req.Header.Set("Authorization", "Bearer "+o.Secret)
			}
		}
		if anthropicCatalog {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
		if policy.Apply(o.Secret, req.Header, nil) != nil {
			return result, errors.New("model discovery headers are invalid")
		}
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, errors.New("model discovery could not connect to Provider")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return result, fmt.Errorf("model discovery Provider returned HTTP %d", response.StatusCode)
		}
		if readErr != nil || len(body) > 1<<20 {
			return result, errors.New("model discovery response is unavailable or too large")
		}
		models, next, more, err := parseModelDiscoveryPage(body, google, o.Secret)
		if err != nil {
			return result, err
		}
		for _, m := range models {
			if seenModels[m.ID] {
				continue
			}
			if len(result.Models) == 512 {
				result.Truncated = true
				return result, nil
			}
			seenModels[m.ID] = true
			result.Models = append(result.Models, m)
		}
		if !more {
			return result, nil
		}
		if !safeCatalogText(next, 2048, o.Secret, true) || next == "" || seenCursors[next] {
			return result, errors.New("model discovery pagination is invalid")
		}
		seenCursors[next], cursor = true, next
	}
	result.Truncated = true
	return result, nil
}

func parseModelDiscoveryPage(body []byte, google bool, secret string) ([]DiscoveredModel, string, bool, error) {
	var page struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			MaxInput    int    `json:"max_input_tokens"`
			MaxOutput   int    `json:"max_tokens"`
		} `json:"data"`
		Models []struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			Input       int      `json:"inputTokenLimit"`
			Output      int      `json:"outputTokenLimit"`
			Methods     []string `json:"supportedGenerationMethods"`
		} `json:"models"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
		Next    string `json:"nextPageToken"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &page) != nil || (google && page.Models == nil) || (!google && page.Data == nil) {
		return nil, "", false, errors.New("model discovery response has an invalid catalog")
	}
	models := []DiscoveredModel{}
	add := func(id, display string, input, output int) {
		if !validAvailabilityIdentifier(id, maxPublicModelNameBytes) || !safeCatalogText(id, maxPublicModelNameBytes, secret, false) {
			return
		}
		m := DiscoveredModel{ID: id}
		if safeCatalogText(display, 256, secret, false) {
			m.DisplayName = display
		}
		if input > 0 && input <= 2*1024*1024 {
			m.InputTokenLimit = input
		}
		if output > 0 && output <= 1_000_000 {
			m.OutputTokenLimit = output
		}
		models = append(models, m)
	}
	if google {
		for _, m := range page.Models {
			generation := false
			for _, method := range m.Methods {
				if method == "generateContent" {
					generation = true
				}
			}
			if generation {
				add(strings.TrimPrefix(m.Name, "models/"), m.DisplayName, m.Input, m.Output)
			}
		}
		return models, page.Next, page.Next != "", nil
	}
	for _, m := range page.Data {
		add(m.ID, m.DisplayName, m.MaxInput, m.MaxOutput)
	}
	return models, page.LastID, page.HasMore, nil
}

func safeCatalogText(value string, maximum int, secret string, cursor bool) bool {
	if !utf8.ValidString(value) || len(value) > maximum || strings.TrimSpace(value) != value || redact.String(value) != value || (secret != "" && strings.Contains(value, secret)) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || (cursor && unicode.IsSpace(r)) {
			return false
		}
	}
	return true
}
