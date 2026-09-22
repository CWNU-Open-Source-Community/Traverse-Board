package webevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/redact"
)

const (
	anthropicSearchToolType     = "web_search_20250305"
	anthropicSearchToolName     = "web_search"
	anthropicSearchMaxUses      = 3
	anthropicSearchMaxTokens    = 1024
	anthropicSearchMaxRequests  = 3
	anthropicSearchVersion      = "2023-06-01"
	anthropicSearchResponseSize = 2 * 1024 * 1024
)

type AnthropicSearchProvider struct {
	client     *SafeHTTPClient
	endpoint   string
	providerID string
	model      string
	runtime    ResponsesSearchRuntime

	cacheMu  sync.Mutex
	ready    map[[sha256.Size]byte]bool
	negative map[[sha256.Size]byte]anthropicSearchNegative
	now      func() time.Time
}

type anthropicSearchNegative struct {
	reason    string
	expiresAt time.Time
}

type anthropicSearchState struct {
	secret      string
	mappedModel string
	binding     string
	baseKey     [sha256.Size]byte
}

func NewAnthropicSearchProvider(client *SafeHTTPClient, endpoint, providerID,
	model string, runtime ResponsesSearchRuntime,
) (*AnthropicSearchProvider, error) {
	canonical, err := anthropicMessagesEndpoint(endpoint)
	providerID, model = strings.TrimSpace(providerID), strings.TrimSpace(model)
	if err != nil || !validBoundedText(providerID, 256, false) ||
		redact.String(providerID) != providerID || !validBoundedText(model, 512, false) ||
		redact.String(model) != model || runtime == nil ||
		!validResponsesRuntimeBinding(runtime.BindingDigest()) {
		return nil, errors.New("provider-native Anthropic search configuration is invalid")
	}
	if client == nil {
		client = NewProviderSearchHTTPClient()
	}
	return &AnthropicSearchProvider{client: client, endpoint: canonical,
		providerID: providerID, model: model, runtime: runtime,
		ready:    make(map[[sha256.Size]byte]bool),
		negative: make(map[[sha256.Size]byte]anthropicSearchNegative), now: time.Now}, nil
}

func anthropicMessagesEndpoint(endpoint string) (string, error) {
	canonical, err := CanonicalizePublicHTTPSURL(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(canonical)
	if err != nil || parsed.RawQuery != "" {
		return "", errors.New("Anthropic Messages endpoint is invalid")
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/v1/messages"):
	case strings.HasSuffix(path, "/v1"):
		path += "/messages"
	default:
		path += "/v1/messages"
	}
	parsed.Path, parsed.RawPath = path, ""
	return CanonicalizePublicHTTPSURL(parsed.String())
}

func (p *AnthropicSearchProvider) Name() string {
	if p == nil {
		return ""
	}
	return p.providerID
}

func (p *AnthropicSearchProvider) Endpoint() string {
	if p == nil {
		return ""
	}
	return p.endpoint
}

func (p *AnthropicSearchProvider) ProviderGroundedSearch() bool { return p != nil }

func (p *AnthropicSearchProvider) DeclaredCapabilityBinding(ctx context.Context,
	authority NetworkAuthority,
) (string, error) {
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return "", err
	}
	if _, _, err := p.prepareRequest(state, SearchRequest{Query: nativeSearchQualificationQuery,
		Limit: MaxSources}, nil); err != nil {
		return "", err
	}
	return p.publicBinding(state), nil
}

func (p *AnthropicSearchProvider) Qualify(ctx context.Context,
	authority NetworkAuthority,
) (string, error) {
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return "", err
	}
	key := anthropicSearchRequestKey(state.baseKey, SearchDomainFilter{}, nativeSearchQualificationQuery)
	if reason := p.cachedNegative(key); reason != "" {
		return "", nativeSearchError(reason)
	}
	if p.isReady(state.baseKey) {
		return p.publicBinding(state), nil
	}
	if _, err := p.search(ctx, state, authority, SearchRequest{
		Query: nativeSearchQualificationQuery, Limit: MaxSources}, true); err != nil {
		p.storeNegative(key, err, ctx.Err() == nil)
		return "", err
	}
	p.storeReady(state.baseKey)
	return p.publicBinding(state), nil
}

func (p *AnthropicSearchProvider) QualificationSnapshot(ctx context.Context,
	authority NetworkAuthority,
) SearchQualificationSnapshot {
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return SearchQualificationSnapshot{Status: SearchQualificationUnavailable,
			Reason: nativeSearchQualificationReason(err)}
	}
	if _, _, err := p.prepareRequest(state, SearchRequest{Query: nativeSearchQualificationQuery,
		Limit: MaxSources}, nil); err != nil {
		return SearchQualificationSnapshot{Status: SearchQualificationUnavailable,
			Reason: nativeSearchQualificationReason(err)}
	}
	if reason := p.cachedNegative(anthropicSearchRequestKey(state.baseKey, SearchDomainFilter{}, nativeSearchQualificationQuery)); reason != "" {
		return SearchQualificationSnapshot{Status: SearchQualificationUnavailable, Reason: reason}
	}
	if p.isReady(state.baseKey) {
		return SearchQualificationSnapshot{Status: SearchQualificationReady}
	}
	return SearchQualificationSnapshot{Status: SearchQualificationUnqualified}
}

func (p *AnthropicSearchProvider) Search(ctx context.Context, query string,
	limit int, authority NetworkAuthority,
) ([]ProviderResult, error) {
	return p.SearchFiltered(ctx, SearchRequest{Query: query, Limit: limit}, authority)
}

func (p *AnthropicSearchProvider) SearchFiltered(ctx context.Context,
	request SearchRequest, authority NetworkAuthority,
) ([]ProviderResult, error) {
	request.Query = boundedCleanText(request.Query, MaxQueryRunes)
	filter, err := NormalizeSearchDomains(request.AllowedDomains, request.BlockedDomains)
	if p == nil || ctx == nil || err != nil || request.Query == "" ||
		redact.String(request.Query) != request.Query || request.Limit < 1 || request.Limit > MaxSources {
		return nil, nativeSearchError(NativeSearchReasonInvalidConfiguration)
	}
	request.AllowedDomains, request.BlockedDomains = filter.AllowedDomains, filter.BlockedDomains
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return nil, err
	}
	key := anthropicSearchRequestKey(state.baseKey, filter, request.Query)
	if reason := p.cachedNegative(key); reason != "" {
		return nil, nativeSearchError(reason)
	}
	results, err := p.search(ctx, state, authority, request, false)
	if err != nil {
		p.storeNegative(key, err, ctx.Err() == nil)
		return nil, err
	}
	p.storeReady(state.baseKey)
	return results, nil
}

// CheckSearchConnection deliberately performs one request. A pause proves the
// transport answered but does not prove a completed search.
func (p *AnthropicSearchProvider) CheckSearchConnection(ctx context.Context,
	query string, limit int, authority NetworkAuthority,
) ([]ProviderResult, error) {
	query = boundedCleanText(query, MaxQueryRunes)
	if p == nil || ctx == nil || query == "" || redact.String(query) != query ||
		limit < 1 || limit > MaxSources {
		return nil, nativeSearchUnsentDiagnostic("not_configured")
	}
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return nil, nativeSearchUnsentDiagnostic("not_configured")
	}
	ledger := newAnthropicSearchLedger(limit)
	response, err := p.exchange(ctx, state, authority,
		SearchRequest{Query: query, Limit: limit}, nil)
	if err != nil {
		return nil, nativeSearchDiagnostic(nativeSearchQualificationReason(err), 0)
	}
	if err := ledger.consume(response); err != nil {
		return nil, nativeSearchDiagnostic(nativeSearchQualificationReason(err), http.StatusOK)
	}
	if response.StopReason == "pause_turn" {
		return nil, nativeSearchDiagnostic(NativeSearchReasonResponseIncomplete, http.StatusOK)
	}
	results, err := ledger.finish(response.StopReason)
	if err != nil {
		return nil, nativeSearchDiagnostic(nativeSearchQualificationReason(err), http.StatusOK)
	}
	current, err := p.resolveState(ctx, authority)
	if err != nil || current.baseKey != state.baseKey {
		return nil, nativeSearchDiagnostic(NativeSearchReasonConfigurationChanged, http.StatusOK)
	}
	p.storeReady(state.baseKey)
	return results, nil
}

func (p *AnthropicSearchProvider) search(ctx context.Context, state anthropicSearchState,
	authority NetworkAuthority, request SearchRequest, diagnostic bool,
) ([]ProviderResult, error) {
	ledger := newAnthropicSearchLedger(request.Limit)
	var messages []json.RawMessage
	for attempt := 0; attempt < anthropicSearchMaxRequests; attempt++ {
		response, err := p.exchange(ctx, state, authority, request, messages)
		if err != nil {
			return nil, err
		}
		if err := ledger.consume(response); err != nil {
			return nil, err
		}
		if response.StopReason != "pause_turn" {
			results, err := ledger.finish(response.StopReason)
			if err != nil {
				return nil, err
			}
			current, err := p.resolveState(ctx, authority)
			if err != nil || current.baseKey != state.baseKey {
				return nil, nativeSearchError(NativeSearchReasonConfigurationChanged)
			}
			return results, nil
		}
		if diagnostic || attempt+1 == anthropicSearchMaxRequests {
			return nil, nativeSearchError(NativeSearchReasonResponseIncomplete)
		}
		current, err := p.resolveState(ctx, authority)
		if err != nil || current.baseKey != state.baseKey {
			return nil, nativeSearchError(NativeSearchReasonConfigurationChanged)
		}
		messages = append(messages, append(json.RawMessage(nil), response.RawContent...))
	}
	return nil, nativeSearchError(NativeSearchReasonResponseIncomplete)
}

func (p *AnthropicSearchProvider) exchange(ctx context.Context, state anthropicSearchState,
	authority NetworkAuthority, request SearchRequest, paused []json.RawMessage,
) (anthropicSearchResponse, error) {
	payload, headers, err := p.prepareRequest(state, request, paused)
	if err != nil {
		return anthropicSearchResponse{}, err
	}
	document, err := p.client.PostJSONAuthorizedNoRedirect(ctx, p.endpoint, payload,
		anthropicSearchResponseSize, headers, func(raw string) error {
			_, authorizeErr := authority.Authorize(raw)
			return authorizeErr
		})
	if err != nil {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonTransportUnavailable)
	}
	if document.Truncated {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		if document.StatusCode == http.StatusTooManyRequests {
			return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonRateLimited)
		}
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonProviderRejected)
	}
	mediaType, _, err := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	return decodeAnthropicSearchResponse(document.Body)
}

func (p *AnthropicSearchProvider) prepareRequest(state anthropicSearchState,
	request SearchRequest, paused []json.RawMessage,
) ([]byte, http.Header, error) {
	filter, err := NormalizeSearchDomains(request.AllowedDomains, request.BlockedDomains)
	if err != nil {
		return nil, nil, nativeSearchError(NativeSearchReasonInvalidConfiguration)
	}
	tool := map[string]any{"type": anthropicSearchToolType,
		"name": anthropicSearchToolName, "max_uses": anthropicSearchMaxUses}
	if len(filter.AllowedDomains) > 0 {
		tool["allowed_domains"] = filter.AllowedDomains
	} else if len(filter.BlockedDomains) > 0 {
		tool["blocked_domains"] = filter.BlockedDomains
	}
	messages := []any{map[string]any{"role": "user", "content": request.Query}}
	for _, content := range paused {
		if len(content) == 0 || !json.Valid(content) || len(content) > DefaultMaxRequest {
			return nil, nil, nativeSearchError(NativeSearchReasonRequestSizeBudget)
		}
		messages = append(messages, map[string]any{"role": "assistant", "content": content})
	}
	body := map[string]any{"model": state.mappedModel, "stream": false,
		"max_tokens": anthropicSearchMaxTokens, "messages": messages, "tools": []any{tool}}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	headers.Set("x-api-key", state.secret)
	headers.Set("Authorization", "Bearer "+state.secret)
	headers.Set("anthropic-version", anthropicSearchVersion)
	if err := p.runtime.Apply(state.secret, headers, body); err != nil ||
		!validNativeSearchHeaders(headers) ||
		headers.Get("x-api-key") != state.secret ||
		headers.Get("Authorization") != "Bearer "+state.secret ||
		headers.Get("anthropic-version") != anthropicSearchVersion ||
		!protectedAnthropicSearchBody(body, state.mappedModel, request.Query, tool, messages) {
		return nil, nil, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 {
		return nil, nil, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	if len(encoded) > DefaultMaxRequest {
		return nil, nil, nativeSearchError(NativeSearchReasonRequestSizeBudget)
	}
	return encoded, headers, nil
}

func protectedAnthropicSearchBody(body map[string]any, model, query string,
	tool map[string]any, messages []any,
) bool {
	return body["model"] == model && body["stream"] == false &&
		body["max_tokens"] == anthropicSearchMaxTokens &&
		reflect.DeepEqual(body["messages"], messages) &&
		reflect.DeepEqual(body["tools"], []any{tool}) && query != ""
}

func (p *AnthropicSearchProvider) resolveState(ctx context.Context,
	authority NetworkAuthority,
) (anthropicSearchState, error) {
	if p == nil || p.client == nil || p.runtime == nil || ctx == nil {
		return anthropicSearchState{}, nativeSearchError(NativeSearchReasonInvalidConfiguration)
	}
	if _, err := authority.Authorize(p.endpoint); err != nil {
		return anthropicSearchState{}, nativeSearchError(NativeSearchReasonEndpointUnauthorized)
	}
	secret, err := p.runtime.ResolveCredential(ctx)
	if err != nil || !validNativeSearchSecret(secret) {
		return anthropicSearchState{}, nativeSearchError(NativeSearchReasonCredentialUnavailable)
	}
	mapped, err := p.runtime.MapModel(p.model)
	mapped, binding := strings.TrimSpace(mapped), strings.TrimSpace(p.runtime.BindingDigest())
	if err != nil || !validBoundedText(mapped, 512, false) || redact.String(mapped) != mapped {
		return anthropicSearchState{}, nativeSearchError(NativeSearchReasonModelMappingInvalid)
	}
	if !validResponsesRuntimeBinding(binding) {
		return anthropicSearchState{}, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	secretDigest := sha256.Sum256([]byte(secret))
	baseKey := digestNativeSearchParts("anthropic_native_search_cache.v1", p.endpoint,
		p.providerID, p.model, mapped, binding, string(secretDigest[:]))
	return anthropicSearchState{secret: secret, mappedModel: mapped,
		binding: binding, baseKey: baseKey}, nil
}

func (p *AnthropicSearchProvider) publicBinding(state anthropicSearchState) string {
	return DigestBytes([]byte(strings.Join([]string{"anthropic_native_search.v1",
		p.endpoint, p.providerID, p.model, state.mappedModel, state.binding,
		anthropicSearchToolType}, "\x00")))
}

func anthropicSearchRequestKey(base [sha256.Size]byte, filter SearchDomainFilter, query string) [sha256.Size]byte {
	return digestNativeSearchParts("anthropic_native_search_negative.v1", string(base[:]),
		filter.Policy(), strings.Join(filter.AllowedDomains, "\x00"),
		strings.Join(filter.BlockedDomains, "\x00"), query)
}

func (p *AnthropicSearchProvider) isReady(key [sha256.Size]byte) bool {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	return p.ready[key]
}

func (p *AnthropicSearchProvider) storeReady(key [sha256.Size]byte) {
	p.cacheMu.Lock()
	p.ready[key] = true
	delete(p.negative, anthropicSearchRequestKey(key, SearchDomainFilter{}, nativeSearchQualificationQuery))
	p.cacheMu.Unlock()
}

func (p *AnthropicSearchProvider) cachedNegative(key [sha256.Size]byte) string {
	p.cacheMu.Lock()
	if len(p.negative) >= 16 {
		for oldest := range p.negative {
			delete(p.negative, oldest)
			break
		}
	}
	defer p.cacheMu.Unlock()
	negative, ok := p.negative[key]
	if !ok {
		return ""
	}
	if !p.now().Before(negative.expiresAt) {
		delete(p.negative, key)
		return ""
	}
	return negative.reason
}

func (p *AnthropicSearchProvider) storeNegative(key [sha256.Size]byte, err error, eligible bool) {
	if !eligible {
		return
	}
	reason := nativeSearchQualificationReason(err)
	if reason == NativeSearchReasonInvalidToolInput || reason == NativeSearchReasonQueryTooLong ||
		reason == NativeSearchReasonRequestTooLarge || reason == NativeSearchReasonRequestSizeBudget {
		// These are request/filter specific; key already includes the filter.
	}
	p.cacheMu.Lock()
	p.negative[key] = anthropicSearchNegative{reason: reason, expiresAt: p.now().Add(15 * time.Second)}
	p.cacheMu.Unlock()
}

type anthropicSearchResponse struct {
	StopReason string
	Content    []json.RawMessage
	RawContent json.RawMessage
}

func decodeAnthropicSearchResponse(raw []byte) (anthropicSearchResponse, error) {
	var envelope struct {
		Type       string            `json:"type"`
		Role       string            `json:"role"`
		StopReason string            `json:"stop_reason"`
		Content    []json.RawMessage `json:"content"`
		Error      json.RawMessage   `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&envelope); err != nil {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	if envelope.Type == "error" || len(envelope.Error) > 0 {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonProviderRejected)
	}
	if envelope.Type != "message" || envelope.Role != "assistant" ||
		len(envelope.Content) == 0 || len(envelope.Content) > nativeSearchMaxOutputItems {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	content, err := json.Marshal(envelope.Content)
	if err != nil || len(content) > anthropicSearchResponseSize {
		return anthropicSearchResponse{}, nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	return anthropicSearchResponse{StopReason: envelope.StopReason,
		Content: envelope.Content, RawContent: content}, nil
}

type anthropicSearchLedger struct {
	limit     int
	calls     map[string]bool
	results   []ProviderResult
	byURL     map[string]int
	citations map[string]anthropicSearchCitation
	observed  bool
}

type anthropicSearchCitation struct {
	Title string
	Text  string
}

func newAnthropicSearchLedger(limit int) *anthropicSearchLedger {
	return &anthropicSearchLedger{limit: limit, calls: make(map[string]bool),
		byURL: make(map[string]int), citations: make(map[string]anthropicSearchCitation)}
}

func (l *anthropicSearchLedger) consume(response anthropicSearchResponse) error {
	for _, raw := range response.Content {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		}
		switch header.Type {
		case "server_tool_use":
			var block struct {
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(raw, &block) != nil || !validIdentity(block.ID) ||
				block.Name != anthropicSearchToolName || !json.Valid(block.Input) {
				return nativeSearchError(NativeSearchReasonResponseInvalid)
			}
			if _, duplicate := l.calls[block.ID]; duplicate {
				return nativeSearchError(NativeSearchReasonResponseInvalid)
			}
			l.calls[block.ID] = false
			l.observed = true
		case "web_search_tool_result":
			if err := l.consumeResult(raw); err != nil {
				return err
			}
		case "text":
			if err := l.consumeText(raw); err != nil {
				return err
			}
		case "tool_use":
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		default:
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		}
	}
	return nil
}

func (l *anthropicSearchLedger) consumeResult(raw json.RawMessage) error {
	var block struct {
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &block) != nil || !validIdentity(block.ToolUseID) {
		return nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	settled, exists := l.calls[block.ToolUseID]
	if !exists || settled {
		return nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	var failure struct {
		Type      string `json:"type"`
		ErrorCode string `json:"error_code"`
	}
	if len(block.Content) == 0 || !json.Valid(block.Content) {
		return nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	if block.Content[0] == '{' {
		if json.Unmarshal(block.Content, &failure) != nil || failure.Type != "web_search_tool_result_error" {
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		}
		return nativeSearchError(anthropicSearchErrorReason(failure.ErrorCode))
	}
	if block.Content[0] != '[' {
		return nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	var items []struct {
		Type             string `json:"type"`
		URL              string `json:"url"`
		Title            string `json:"title"`
		PageAge          string `json:"page_age"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if json.Unmarshal(block.Content, &items) != nil || len(items) > nativeSearchMaxNestedItems {
		return nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	for _, item := range items {
		if item.Type != "web_search_result" {
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		}
		canonical, err := CanonicalizePublicHTTPSURL(item.URL)
		if err != nil {
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		}
		if _, exists := l.byURL[canonical]; exists {
			continue
		}
		l.byURL[canonical] = len(l.results)
		l.results = append(l.results, ProviderResult{URL: canonical,
			Title:       boundedCleanText(item.Title, 1024),
			PublishedAt: boundedCleanText(item.PageAge, 128), Rank: len(l.results) + 1})
	}
	l.calls[block.ToolUseID] = true
	return nil
}

func (l *anthropicSearchLedger) consumeText(raw json.RawMessage) error {
	var block struct {
		Text      string `json:"text"`
		Citations []struct {
			Type      string `json:"type"`
			URL       string `json:"url"`
			Title     string `json:"title"`
			CitedText string `json:"cited_text"`
		} `json:"citations"`
	}
	if json.Unmarshal(raw, &block) != nil {
		return nativeSearchError(NativeSearchReasonResponseInvalid)
	}
	for _, citation := range block.Citations {
		if citation.Type != "web_search_result_location" {
			return nativeSearchError(NativeSearchReasonResponseInvalid)
		}
		canonical, err := CanonicalizePublicHTTPSURL(citation.URL)
		if err != nil {
			continue
		}
		if _, exists := l.byURL[canonical]; !exists {
			continue
		}
		l.citations[canonical] = anthropicSearchCitation{Title: boundedCleanText(citation.Title, 1024),
			Text: boundedSnippet(citation.CitedText)}
	}
	return nil
}

func (l *anthropicSearchLedger) finish(stopReason string) ([]ProviderResult, error) {
	if stopReason == "max_tokens" || stopReason == "pause_turn" {
		return nil, nativeSearchError(NativeSearchReasonResponseIncomplete)
	}
	if stopReason != "end_turn" || !l.observed {
		return nil, nativeSearchError(NativeSearchReasonSearchNotPerformed)
	}
	for _, settled := range l.calls {
		if !settled {
			return nil, nativeSearchError(NativeSearchReasonResponseIncomplete)
		}
	}
	for index := range l.results {
		if citation, ok := l.citations[l.results[index].URL]; ok {
			if citation.Title != "" {
				l.results[index].Title = citation.Title
			}
			l.results[index].Snippet = citation.Text
		}
	}
	if len(l.results) > l.limit {
		l.results = l.results[:l.limit]
	}
	for index := range l.results {
		l.results[index].Rank = index + 1
	}
	return append([]ProviderResult(nil), l.results...), nil
}

func anthropicSearchErrorReason(code string) string {
	switch strings.TrimSpace(code) {
	case "too_many_requests":
		return NativeSearchReasonRateLimited
	case "unavailable":
		return NativeSearchReasonServiceUnavailable
	case "invalid_tool_input":
		return NativeSearchReasonInvalidToolInput
	case "query_too_long":
		return NativeSearchReasonQueryTooLong
	case "request_too_large":
		return NativeSearchReasonRequestTooLarge
	case "max_uses_exceeded":
		return NativeSearchReasonMaxUsesExceeded
	default:
		return NativeSearchReasonResponseInvalid
	}
}
