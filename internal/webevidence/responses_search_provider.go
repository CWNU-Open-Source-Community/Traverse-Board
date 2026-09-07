package webevidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	nativeSearchTool               = "web_search"
	nativeSearchPreviewTool        = "web_search_preview"
	nativeSearchDatedTool          = "web_search_2025_08_26"
	nativeSearchResponseLimit      = 2 * 1024 * 1024
	nativeSearchCacheLimit         = 8
	nativeSearchMaxOutputItems     = 256
	nativeSearchMaxNestedItems     = 512
	nativeSearchMaxOutputTokens    = 512
	nativeSearchDeepSeekMaxTokens  = 2048
	nativeSearchMaxStructuredText  = 128 * 1024
	nativeSearchMaxJSONCandidates  = 256
	nativeSearchNegativeCacheLimit = 8
	// Qualification uses a concrete, stable public target. Vague capability
	// prose can make DeepSeek's hosted search spend its bounded continuation
	// rounds without producing a final structured result.
	nativeSearchQualificationQuery  = "OpenAI official website"
	nativeSearchDeepSeekInstruction = "You must use web_search at least once. After searching, return only JSON matching the requested schema. Include only source URLs grounded in the search results."
)

var nativeSearchToolVariants = [...]string{
	nativeSearchTool,
	nativeSearchPreviewTool,
	nativeSearchDatedTool,
}

const (
	nativeSearchTransportNegativeTTL = 5 * time.Second
	nativeSearchRejectedNegativeTTL  = 15 * time.Second
	nativeSearchInvalidNegativeTTL   = 30 * time.Second
	nativeSearchUnsupportedTTL       = 60 * time.Second
)

const (
	NativeSearchReasonInvalidConfiguration  = "invalid_configuration"
	NativeSearchReasonCredentialUnavailable = "credential_unavailable"
	NativeSearchReasonModelMappingInvalid   = "model_mapping_invalid"
	NativeSearchReasonRuntimeInvalid        = "runtime_customization_invalid"
	NativeSearchReasonEndpointUnauthorized  = "endpoint_unauthorized"
	NativeSearchReasonTransportUnavailable  = "transport_unavailable"
	NativeSearchReasonProviderRejected      = "provider_rejected"
	NativeSearchReasonToolUnsupported       = "tool_unsupported"
	NativeSearchReasonResponseInvalid       = "response_invalid"
)

// ResponsesSearchRuntime is the credential-safe request policy shared by the
// model Harness and hosted Web search. A modelregistry ProviderRequestRuntime
// satisfies this interface without webevidence importing either package.
// Implementations must be immutable and must not retain the secret passed to
// Apply.
type ResponsesSearchRuntime interface {
	ResolveCredential(context.Context) (string, error)
	MapModel(string) (string, error)
	Apply(string, http.Header, map[string]any) error
	BindingDigest() string
}

// NativeSearchQualificationError intentionally exposes only a stable reason
// code. Provider bodies, transport details and credentials never cross the
// qualification boundary or become a persisted selection_reason.
type NativeSearchQualificationError struct {
	Reason string
}

func (e *NativeSearchQualificationError) Error() string {
	reason := NativeSearchReasonInvalidConfiguration
	if e != nil && strings.TrimSpace(e.Reason) != "" {
		reason = strings.TrimSpace(e.Reason)
	}
	return "provider_native_search_unavailable: " + reason
}

type OpenAIResponsesSearchProvider struct {
	client     *SafeHTTPClient
	endpoint   string
	providerID string
	model      string
	runtime    ResponsesSearchRuntime
	deepSeek   bool

	cacheMu       sync.Mutex
	cache         map[[sha256.Size]byte]string
	cacheOrder    [][sha256.Size]byte
	negative      map[[sha256.Size]byte]responsesSearchNegative
	negativeOrder [][sha256.Size]byte
	flights       map[[sha256.Size]byte]*responsesSearchFlight
	now           func() time.Time
}

type responsesSearchState struct {
	secret      string
	mappedModel string
	binding     string
	baseKey     [sha256.Size]byte
}

type responsesSearchFlight struct {
	done chan struct{}
	tool string
	err  error
}

type responsesSearchNegative struct {
	reason    string
	expiresAt time.Time
}

type responsesSearchAttempt struct {
	results     []ProviderResult
	unsupported bool
	err         error
}

// NewOpenAIResponsesSearchProvider creates an OpenAI Responses-compatible
// hosted search adapter. The supplied URL is the complete Responses endpoint,
// not a base URL to which this package appends a path.
func NewOpenAIResponsesSearchProvider(client *SafeHTTPClient, endpoint,
	providerID, model string, runtime ResponsesSearchRuntime,
) (*OpenAIResponsesSearchProvider, error) {
	canonical, err := CanonicalizePublicHTTPSURL(endpoint)
	if err != nil {
		return nil, errors.New("provider-native Responses endpoint is invalid")
	}
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if !validBoundedText(providerID, 256, false) || redact.String(providerID) != providerID ||
		!validBoundedText(model, 512, false) || redact.String(model) != model || runtime == nil {
		return nil, errors.New("provider-native Responses configuration is invalid")
	}
	if !validResponsesRuntimeBinding(runtime.BindingDigest()) {
		return nil, errors.New("provider-native Responses runtime binding is invalid")
	}
	if client == nil {
		client = NewProviderSearchHTTPClient()
	}
	return &OpenAIResponsesSearchProvider{client: client, endpoint: canonical,
		providerID: providerID, model: model, runtime: runtime,
		deepSeek: deepSeekResponsesCompatibility(canonical),
		cache:    make(map[[sha256.Size]byte]string),
		negative: make(map[[sha256.Size]byte]responsesSearchNegative),
		flights:  make(map[[sha256.Size]byte]*responsesSearchFlight),
		now:      time.Now}, nil
}

func (p *OpenAIResponsesSearchProvider) Name() string {
	if p == nil {
		return ""
	}
	return p.providerID
}

func (p *OpenAIResponsesSearchProvider) Endpoint() string {
	if p == nil {
		return ""
	}
	return p.endpoint
}

// ProviderGroundedSearch declares that successful Search results were returned
// by a completed hosted web-search call for the exact qualified Responses route.
// It does not claim that Traverse fetched or independently verified the pages.
func (p *OpenAIResponsesSearchProvider) ProviderGroundedSearch() bool {
	return p != nil
}

// DeclaredCapabilityBinding validates the complete local Provider contract for
// an explicitly selected provider_native policy without issuing a Provider
// request. The resolver may use this credential-free binding to advertise the
// controlled Go web_search tool on a cold process; the first real Search call
// still performs bounded hosted-tool probing and records readiness.
//
// This is deliberately stricter than trusting the declaration alone: endpoint
// authority, credential presence, model mapping, runtime binding, advanced
// configuration, and the protected Responses request shape are all checked.
func (p *OpenAIResponsesSearchProvider) DeclaredCapabilityBinding(ctx context.Context,
	authority NetworkAuthority,
) (string, error) {
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return "", err
	}
	if _, _, err := p.prepareRequest(state, nativeSearchQualificationQuery,
		nativeSearchTool); err != nil {
		return "", err
	}
	return p.publicDeclaredBinding(state), nil
}

// Qualify observes a real hosted tool call before the resolver advertises
// web_search. A positive observation is cached by endpoint, Provider/model
// binding and a private hash of the current credential. The returned binding
// deliberately excludes that credential hash.
func (p *OpenAIResponsesSearchProvider) Qualify(ctx context.Context,
	authority NetworkAuthority,
) (string, error) {
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return "", err
	}
	// Evaluate Apply for every caller, including callers served by a positive
	// or short-lived negative cache entry.
	if _, _, err := p.prepareRequest(state, nativeSearchQualificationQuery,
		nativeSearchTool); err != nil {
		return "", err
	}
	if negative := p.cachedNegative(state); negative != nil {
		return "", negative
	}
	if tool, found := p.cachedTool(state); found {
		// If compatibility probing selected a hosted-tool variant, validate that
		// exact protected body too.
		if tool != nativeSearchTool {
			if _, _, err := p.prepareRequest(state, nativeSearchQualificationQuery,
				tool); err != nil {
				return "", err
			}
		}
		return p.publicBinding(state, tool), nil
	}
	tool, _, _, err := p.probe(ctx, state, authority,
		nativeSearchQualificationQuery, "")
	if err != nil {
		return "", err
	}
	return p.publicBinding(state, tool), nil
}

// QualificationSnapshot reports only already-observed qualification state.
// It resolves the current credential/configuration generation so a rotated
// credential cannot reuse an old observation, but it never performs network
// I/O and never returns credential material or a private cache key.
func (p *OpenAIResponsesSearchProvider) QualificationSnapshot(ctx context.Context,
	authority NetworkAuthority,
) SearchQualificationSnapshot {
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return SearchQualificationSnapshot{Status: SearchQualificationUnavailable,
			Reason: nativeSearchQualificationReason(err)}
	}
	// Match Qualify's protected request validation even when a cache entry is
	// present. Advanced configuration drift therefore invalidates readiness.
	if _, _, err := p.prepareRequest(state, nativeSearchQualificationQuery,
		nativeSearchTool); err != nil {
		return SearchQualificationSnapshot{Status: SearchQualificationUnavailable,
			Reason: nativeSearchQualificationReason(err)}
	}
	if negative := p.cachedNegative(state); negative != nil {
		return SearchQualificationSnapshot{Status: SearchQualificationUnavailable,
			Reason: nativeSearchQualificationReason(negative)}
	}
	if tool, found := p.cachedTool(state); found {
		if tool != nativeSearchTool {
			if _, _, err := p.prepareRequest(state, nativeSearchQualificationQuery,
				tool); err != nil {
				return SearchQualificationSnapshot{Status: SearchQualificationUnavailable,
					Reason: nativeSearchQualificationReason(err)}
			}
		}
		return SearchQualificationSnapshot{Status: SearchQualificationReady}
	}
	return SearchQualificationSnapshot{Status: SearchQualificationUnqualified}
}

func (p *OpenAIResponsesSearchProvider) Search(ctx context.Context, query string,
	limit int, authority NetworkAuthority,
) ([]ProviderResult, error) {
	if p == nil || ctx == nil || limit < 1 || limit > MaxSources {
		return nil, nativeSearchError(NativeSearchReasonInvalidConfiguration)
	}
	query = boundedCleanText(query, MaxQueryRunes)
	if query == "" || redact.String(query) != query {
		return nil, nativeSearchError(NativeSearchReasonInvalidConfiguration)
	}
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		return nil, err
	}
	if negative := p.cachedNegative(state); negative != nil {
		if _, _, prepareErr := p.prepareRequest(state, query,
			nativeSearchTool); prepareErr != nil {
			return nil, prepareErr
		}
		return nil, negative
	}
	if tool, found := p.cachedTool(state); found {
		attempt := p.execute(ctx, state, authority, query, limit, tool)
		if attempt.err == nil {
			return attempt.results, nil
		}
		if !attempt.unsupported {
			p.storeNegative(state, attempt.err, ctx.Err() == nil)
			return nil, attempt.err
		}
		p.invalidateTool(state, tool)
		_, results, performed, probeErr := p.probe(ctx, state, authority,
			query, tool)
		if probeErr != nil {
			return nil, probeErr
		}
		if performed {
			if len(results) > limit {
				results = results[:limit]
			}
			return results, nil
		}
		cached, found := p.cachedTool(state)
		if !found {
			return nil, nativeSearchError(NativeSearchReasonToolUnsupported)
		}
		final := p.execute(ctx, state, authority, query, limit, cached)
		if final.err != nil {
			p.storeNegative(state, final.err, ctx.Err() == nil)
		}
		return final.results, final.err
	}
	_, results, performed, err := p.probe(ctx, state, authority, query, "")
	if err != nil {
		return nil, err
	}
	if performed {
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}
	tool, found := p.cachedTool(state)
	if !found {
		return nil, nativeSearchError(NativeSearchReasonToolUnsupported)
	}
	attempt := p.execute(ctx, state, authority, query, limit, tool)
	if attempt.err != nil {
		p.storeNegative(state, attempt.err, ctx.Err() == nil)
	}
	return attempt.results, attempt.err
}

func (p *OpenAIResponsesSearchProvider) resolveState(ctx context.Context,
	authority NetworkAuthority,
) (responsesSearchState, error) {
	if p == nil || p.client == nil || p.runtime == nil || ctx == nil {
		return responsesSearchState{}, nativeSearchError(NativeSearchReasonInvalidConfiguration)
	}
	if _, err := authority.Authorize(p.endpoint); err != nil {
		return responsesSearchState{}, nativeSearchError(NativeSearchReasonEndpointUnauthorized)
	}
	secret, err := p.runtime.ResolveCredential(ctx)
	if err != nil || !validNativeSearchSecret(secret) {
		return responsesSearchState{}, nativeSearchError(NativeSearchReasonCredentialUnavailable)
	}
	mapped, err := p.runtime.MapModel(p.model)
	mapped = strings.TrimSpace(mapped)
	if err != nil || !validBoundedText(mapped, 512, false) || redact.String(mapped) != mapped {
		return responsesSearchState{}, nativeSearchError(NativeSearchReasonModelMappingInvalid)
	}
	binding := strings.TrimSpace(p.runtime.BindingDigest())
	if !validResponsesRuntimeBinding(binding) {
		return responsesSearchState{}, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	secretDigest := sha256.Sum256([]byte(secret))
	base := digestNativeSearchParts("provider_native_search_cache.v1", p.endpoint,
		p.providerID, p.model, mapped, binding, string(secretDigest[:]))
	return responsesSearchState{secret: secret, mappedModel: mapped,
		binding: binding, baseKey: base}, nil
}

func (p *OpenAIResponsesSearchProvider) probe(ctx context.Context,
	state responsesSearchState, authority NetworkAuthority, query string,
	startAfter string,
) (string, []ProviderResult, bool, error) {
	candidates, err := nativeSearchCandidatesAfter(startAfter)
	if err != nil {
		return "", nil, false, err
	}
	if tool, found := p.cachedTool(state); found {
		return tool, nil, false, nil
	}
	p.cacheMu.Lock()
	if tool, found := p.cachedToolLocked(state); found {
		p.cacheMu.Unlock()
		return tool, nil, false, nil
	}
	if flight := p.flights[state.baseKey]; flight != nil {
		done := flight.done
		p.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return "", nil, false,
				nativeSearchError(NativeSearchReasonTransportUnavailable)
		case <-done:
			if flight.err != nil {
				return "", nil, false, flight.err
			}
			return flight.tool, nil, false, nil
		}
	}
	flight := &responsesSearchFlight{done: make(chan struct{})}
	p.flights[state.baseKey] = flight
	p.cacheMu.Unlock()

	tool := ""
	attempt := responsesSearchAttempt{unsupported: true,
		err: nativeSearchError(NativeSearchReasonToolUnsupported)}
	for _, candidate := range candidates {
		tool = candidate
		attempt = p.execute(ctx, state, authority, query, MaxSources, tool)
		if !attempt.unsupported {
			break
		}
	}
	if attempt.unsupported {
		attempt.err = nativeSearchError(NativeSearchReasonToolUnsupported)
	}
	if attempt.err == nil {
		p.storeTool(state, tool)
	} else {
		p.storeNegative(state, attempt.err, ctx.Err() == nil)
	}
	p.cacheMu.Lock()
	delete(p.flights, state.baseKey)
	flight.tool = tool
	flight.err = attempt.err
	close(flight.done)
	p.cacheMu.Unlock()
	return tool, attempt.results, true, attempt.err
}

func nativeSearchCandidatesAfter(startAfter string) ([]string, error) {
	start := 0
	if startAfter != "" {
		found := false
		for index, tool := range nativeSearchToolVariants {
			if tool == startAfter {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return nil, nativeSearchError(NativeSearchReasonRuntimeInvalid)
		}
	}
	return nativeSearchToolVariants[start:], nil
}

func (p *OpenAIResponsesSearchProvider) execute(ctx context.Context,
	state responsesSearchState, authority NetworkAuthority, query string, limit int,
	tool string,
) responsesSearchAttempt {
	payload, headers, err := p.prepareRequest(state, query, tool)
	if err != nil {
		return responsesSearchAttempt{err: err}
	}
	document, err := p.client.PostJSONAuthorizedNoRedirect(ctx, p.endpoint, payload,
		nativeSearchResponseLimit, headers, func(raw string) error {
			_, authorizeErr := authority.Authorize(raw)
			return authorizeErr
		})
	if err != nil {
		return responsesSearchAttempt{err: nativeSearchError(NativeSearchReasonTransportUnavailable)}
	}
	if document.Truncated {
		return responsesSearchAttempt{err: nativeSearchError(NativeSearchReasonResponseInvalid)}
	}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		if explicitUnsupportedResponsesTool(document, tool) {
			return responsesSearchAttempt{unsupported: true,
				err: nativeSearchError(NativeSearchReasonToolUnsupported)}
		}
		return responsesSearchAttempt{err: nativeSearchError(NativeSearchReasonProviderRejected)}
	}
	mediaType, _, mimeErr := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if mimeErr != nil || !strings.EqualFold(mediaType, "application/json") {
		return responsesSearchAttempt{err: nativeSearchError(NativeSearchReasonResponseInvalid)}
	}
	results, observed, parseErr := parseResponsesSearchResults(document.Body, limit,
		p.deepSeek)
	if parseErr != nil || !observed {
		return responsesSearchAttempt{err: nativeSearchError(NativeSearchReasonResponseInvalid)}
	}
	return responsesSearchAttempt{results: results}
}

func (p *OpenAIResponsesSearchProvider) prepareRequest(state responsesSearchState,
	query, tool string,
) ([]byte, http.Header, error) {
	if !validNativeSearchTool(tool) {
		return nil, nil, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	body := map[string]any{
		"model":       state.mappedModel,
		"input":       query,
		"tools":       []any{map[string]any{"type": tool}},
		"tool_choice": "required",
		// DeepSeek currently ignores these three OpenAI-compatible fields.
		// They remain protected request-shape fields, but neither qualification
		// nor result validation relies on the Provider enforcing them.
		"max_tool_calls":      1,
		"max_output_tokens":   nativeSearchMaxOutputTokens,
		"parallel_tool_calls": false,
		"include":             []any{"web_search_call.action.sources"},
		"store":               false,
	}
	if p.deepSeek {
		body["input"] = "Search query: " + query
		body["instructions"] = nativeSearchDeepSeekInstruction
		body["tool_choice"] = "auto"
		body["max_output_tokens"] = nativeSearchDeepSeekMaxTokens
		body["text"] = deepSeekSearchTextFormat()
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+state.secret)
	if err := p.runtime.Apply(state.secret, headers, body); err != nil ||
		!validNativeSearchHeaders(headers) ||
		!protectedResponsesSearchBody(body, state.mappedModel, query, tool,
			p.deepSeek) {
		return nil, nil, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) == 0 || len(encoded) > DefaultMaxRequest {
		return nil, nil, nativeSearchError(NativeSearchReasonRuntimeInvalid)
	}
	return encoded, headers, nil
}

func validNativeSearchTool(tool string) bool {
	for _, candidate := range nativeSearchToolVariants {
		if tool == candidate {
			return true
		}
	}
	return false
}

func protectedResponsesSearchBody(body map[string]any, model, query, tool string,
	deepSeek bool,
) bool {
	expectedInput := query
	expectedChoice := any("required")
	expectedTokens := nativeSearchMaxOutputTokens
	if deepSeek {
		expectedInput = "Search query: " + query
		expectedChoice = "auto"
		expectedTokens = nativeSearchDeepSeekMaxTokens
	}
	if body == nil || body["model"] != model || body["input"] != expectedInput ||
		body["tool_choice"] != expectedChoice || body["max_tool_calls"] != 1 ||
		body["max_output_tokens"] != expectedTokens ||
		body["parallel_tool_calls"] != false || body["store"] != false {
		return false
	}
	if !reflect.DeepEqual(body["tools"], []any{map[string]any{"type": tool}}) ||
		!reflect.DeepEqual(body["include"], []any{"web_search_call.action.sources"}) {
		return false
	}
	if !deepSeek {
		return true
	}
	return body["instructions"] == nativeSearchDeepSeekInstruction &&
		reflect.DeepEqual(body["text"], deepSeekSearchTextFormat())
}

func deepSeekResponsesCompatibility(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "api.deepseek.com") {
		return false
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	return path == "/responses" || path == "/v1/responses"
}

func deepSeekSearchTextFormat() map[string]any {
	return map[string]any{"format": map[string]any{
		"type": "json_schema", "name": "web_search_results",
		"schema": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []any{"results"},
			"properties": map[string]any{
				"results": map[string]any{
					"type": "array", "maxItems": MaxSources,
					"items": map[string]any{
						"type": "object", "additionalProperties": false,
						"required": []any{"url", "title", "snippet"},
						"properties": map[string]any{
							"url":     map[string]any{"type": "string"},
							"title":   map[string]any{"type": "string"},
							"snippet": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}}
}

func validNativeSearchSecret(secret string) bool {
	if secret == "" || len(secret) > 16*1024 || strings.TrimSpace(secret) == "" ||
		!utf8.ValidString(secret) {
		return false
	}
	for _, current := range secret {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func validNativeSearchHeaders(headers http.Header) bool {
	if headers == nil || len(headers) == 0 || len(headers) > 64 {
		return false
	}
	total := 0
	for name, values := range headers {
		if name == "" || http.CanonicalHeaderKey(name) == "" || strings.ContainsRune(name, ':') ||
			len(values) == 0 || len(values) > 8 {
			return false
		}
		switch strings.ToLower(name) {
		case "connection", "content-length", "host", "proxy-authenticate",
			"proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
			return false
		}
		if strings.EqualFold(name, "Authorization") && len(values) != 1 {
			return false
		}
		total += len(name)
		for _, value := range values {
			if !utf8.ValidString(value) || len(value) > 16*1024 || strings.ContainsAny(value, "\r\n") {
				return false
			}
			for _, current := range value {
				if unicode.IsControl(current) && current != '\t' {
					return false
				}
			}
			total += len(value)
		}
	}
	return total <= 64*1024
}

func validResponsesRuntimeBinding(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}

func nativeSearchError(reason string) error {
	return &NativeSearchQualificationError{Reason: reason}
}

func nativeSearchQualificationReason(err error) string {
	var qualification *NativeSearchQualificationError
	if errors.As(err, &qualification) && qualification != nil &&
		strings.TrimSpace(qualification.Reason) != "" {
		return strings.TrimSpace(qualification.Reason)
	}
	return NativeSearchReasonInvalidConfiguration
}

func (p *OpenAIResponsesSearchProvider) cachedNegative(state responsesSearchState) error {
	key := nativeSearchNegativeCacheKey(state.baseKey)
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	entry, found := p.negative[key]
	if !found {
		return nil
	}
	if !p.nowUTC().Before(entry.expiresAt) {
		delete(p.negative, key)
		return nil
	}
	return nativeSearchError(entry.reason)
}

func (p *OpenAIResponsesSearchProvider) storeNegative(state responsesSearchState,
	err error, enabled bool,
) {
	if !enabled || err == nil {
		return
	}
	var qualification *NativeSearchQualificationError
	if !errors.As(err, &qualification) || qualification == nil {
		return
	}
	ttl := nativeSearchNegativeTTL(qualification.Reason)
	if ttl <= 0 {
		return
	}
	key := nativeSearchNegativeCacheKey(state.baseKey)
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	// A transient rejection invalidates the prior positive observation. Once
	// the bounded TTL expires, the next call must prove capability again.
	for _, tool := range nativeSearchToolVariants {
		delete(p.cache, nativeSearchToolCacheKey(state.baseKey, tool))
	}
	filtered := p.negativeOrder[:0]
	for _, existing := range p.negativeOrder {
		if existing != key {
			filtered = append(filtered, existing)
		}
	}
	p.negativeOrder = filtered
	p.negative[key] = responsesSearchNegative{reason: qualification.Reason,
		expiresAt: p.nowUTC().Add(ttl)}
	p.negativeOrder = append(p.negativeOrder, key)
	for len(p.negative) > nativeSearchNegativeCacheLimit && len(p.negativeOrder) > 0 {
		oldest := p.negativeOrder[0]
		p.negativeOrder = p.negativeOrder[1:]
		delete(p.negative, oldest)
	}
}

func (p *OpenAIResponsesSearchProvider) nowUTC() time.Time {
	if p != nil && p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}

func nativeSearchNegativeTTL(reason string) time.Duration {
	switch reason {
	case NativeSearchReasonTransportUnavailable:
		return nativeSearchTransportNegativeTTL
	case NativeSearchReasonProviderRejected:
		return nativeSearchRejectedNegativeTTL
	case NativeSearchReasonResponseInvalid, NativeSearchReasonRuntimeInvalid:
		return nativeSearchInvalidNegativeTTL
	case NativeSearchReasonToolUnsupported:
		return nativeSearchUnsupportedTTL
	default:
		return 0
	}
}

func (p *OpenAIResponsesSearchProvider) cachedTool(state responsesSearchState) (string, bool) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	return p.cachedToolLocked(state)
}

func (p *OpenAIResponsesSearchProvider) cachedToolLocked(state responsesSearchState) (string, bool) {
	for _, tool := range nativeSearchToolVariants {
		key := nativeSearchToolCacheKey(state.baseKey, tool)
		if cached, found := p.cache[key]; found && cached == tool {
			return tool, true
		}
	}
	return "", false
}

func (p *OpenAIResponsesSearchProvider) storeTool(state responsesSearchState, tool string) {
	key := nativeSearchToolCacheKey(state.baseKey, tool)
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	for _, other := range nativeSearchToolVariants {
		delete(p.cache, nativeSearchToolCacheKey(state.baseKey, other))
	}
	delete(p.negative, nativeSearchNegativeCacheKey(state.baseKey))
	filtered := p.cacheOrder[:0]
	for _, existing := range p.cacheOrder {
		if existing != key {
			filtered = append(filtered, existing)
		}
	}
	p.cacheOrder = filtered
	p.cache[key] = tool
	p.cacheOrder = append(p.cacheOrder, key)
	for len(p.cache) > nativeSearchCacheLimit && len(p.cacheOrder) > 0 {
		oldest := p.cacheOrder[0]
		p.cacheOrder = p.cacheOrder[1:]
		delete(p.cache, oldest)
	}
}

func (p *OpenAIResponsesSearchProvider) invalidateTool(state responsesSearchState, tool string) {
	p.cacheMu.Lock()
	delete(p.cache, nativeSearchToolCacheKey(state.baseKey, tool))
	p.cacheMu.Unlock()
}

func nativeSearchToolCacheKey(base [sha256.Size]byte, tool string) [sha256.Size]byte {
	return digestNativeSearchParts("provider_native_search_tool.v1", string(base[:]), tool)
}

func nativeSearchNegativeCacheKey(base [sha256.Size]byte) [sha256.Size]byte {
	return digestNativeSearchParts("provider_native_search_negative.v1", string(base[:]))
}

func digestNativeSearchParts(parts ...string) [sha256.Size]byte {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func (p *OpenAIResponsesSearchProvider) publicBinding(state responsesSearchState,
	tool string,
) string {
	digest := digestNativeSearchParts("provider_native_search_binding.v1", p.endpoint,
		p.providerID, p.model, state.mappedModel, state.binding, tool)
	return hex.EncodeToString(digest[:])
}

func (p *OpenAIResponsesSearchProvider) publicDeclaredBinding(
	state responsesSearchState,
) string {
	digest := digestNativeSearchParts("provider_native_search_declared_binding.v1",
		p.endpoint, p.providerID, p.model, state.mappedModel, state.binding)
	return hex.EncodeToString(digest[:])
}

func explicitUnsupportedResponsesTool(document HTTPDocument, tool string) bool {
	if (document.StatusCode != http.StatusBadRequest &&
		document.StatusCode != http.StatusUnprocessableEntity) || document.Truncated ||
		len(document.Body) == 0 {
		return false
	}
	var envelope struct {
		Error struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Param   string          `json:"param"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(document.Body)))
	if err := decoder.Decode(&envelope); err != nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return false
	}
	message := strings.ToLower(envelope.Error.Message)
	if len(message) == 0 || len(message) > 16*1024 || !strings.Contains(message,
		strings.ToLower(tool)) {
		return false
	}
	param := strings.ToLower(envelope.Error.Param)
	code := ""
	if len(envelope.Error.Code) != 0 && string(envelope.Error.Code) != "null" {
		_ = json.Unmarshal(envelope.Error.Code, &code)
		code = strings.ToLower(code)
	}
	explicitCode := code == "unsupported_tool" || code == "unsupported_value" ||
		code == "unknown_tool" || code == "invalid_tool" || code == "invalid_value"
	explicitParam := strings.Contains(param, "tool") &&
		(strings.Contains(param, "type") || strings.Contains(param, strings.ToLower(tool)))
	explicitMessage := strings.Contains(message, "unsupported") ||
		strings.Contains(message, "not supported") || strings.Contains(message, "unknown tool") ||
		strings.Contains(message, "unrecognized tool") ||
		strings.Contains(message, "invalid tool") || strings.Contains(message, "invalid value")
	return explicitMessage && (explicitCode || explicitParam)
}

func parseResponsesSearchResults(body []byte, limit int,
	deepSeek bool,
) ([]ProviderResult, bool, error) {
	if limit < 1 || limit > MaxSources {
		return nil, false, errors.New("invalid result limit")
	}
	var envelope struct {
		Status string            `json:"status"`
		Error  json.RawMessage   `json:"error"`
		Output []json.RawMessage `json:"output"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, false, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("Responses payload contains trailing JSON")
	}
	if len(envelope.Error) != 0 && string(envelope.Error) != "null" {
		return nil, false, errors.New("Responses payload contains an error")
	}
	if envelope.Status != "completed" {
		return nil, false, errors.New("Responses payload is not completed")
	}
	if len(envelope.Output) == 0 || len(envelope.Output) > nativeSearchMaxOutputItems {
		return nil, false, errors.New("Responses output is invalid")
	}
	collector := newResponsesResultCollector(limit)
	observed := false
	structured := make([]ProviderResult, 0, limit)
	for _, raw := range envelope.Output {
		var item struct {
			Type    string            `json:"type"`
			Status  string            `json:"status"`
			Action  json.RawMessage   `json:"action"`
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, false, err
		}
		switch item.Type {
		case "web_search_call":
			if deepSeek && item.Status == "failed" {
				// Hosted search may complete overall even when one auxiliary
				// open_page/find_in_page action fails. A failed action is not a
				// capability observation and contributes no evidence, but it does
				// not invalidate a separate completed hosted call with usable
				// results.
				continue
			}
			if item.Status != "completed" {
				return nil, false, errors.New("Responses web search call is not completed")
			}
			observed = true
			if len(item.Action) == 0 || string(item.Action) == "null" {
				continue
			}
			var action struct {
				Sources []struct {
					URL           string `json:"url"`
					Title         string `json:"title"`
					Snippet       string `json:"snippet"`
					PublishedAt   string `json:"published_at"`
					PublishedDate string `json:"published_date"`
				} `json:"sources"`
			}
			if err := json.Unmarshal(item.Action, &action); err != nil ||
				len(action.Sources) > nativeSearchMaxNestedItems {
				return nil, false, errors.New("Responses search sources are invalid")
			}
			for _, source := range action.Sources {
				published := source.PublishedAt
				if published == "" {
					published = source.PublishedDate
				}
				collector.add(source.URL, source.Title, source.Snippet, published)
			}
		case "message":
			if len(item.Content) > nativeSearchMaxNestedItems {
				return nil, false, errors.New("Responses message content is invalid")
			}
			for _, contentRaw := range item.Content {
				var content struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Annotations []struct {
						Type        string `json:"type"`
						URL         string `json:"url"`
						Title       string `json:"title"`
						URLCitation *struct {
							URL   string `json:"url"`
							Title string `json:"title"`
						} `json:"url_citation"`
					} `json:"annotations"`
				}
				if err := json.Unmarshal(contentRaw, &content); err != nil ||
					len(content.Annotations) > nativeSearchMaxNestedItems {
					return nil, false, errors.New("Responses annotations are invalid")
				}
				if deepSeek && observed && item.Status == "completed" &&
					content.Type == "output_text" {
					if parsed, ok := parseDeepSeekSearchResults(content.Text); ok {
						structured = parsed
					}
				}
				for _, annotation := range content.Annotations {
					if annotation.Type != "url_citation" {
						continue
					}
					citationURL, title := annotation.URL, annotation.Title
					if annotation.URLCitation != nil {
						citationURL, title = annotation.URLCitation.URL,
							annotation.URLCitation.Title
					}
					collector.add(citationURL, title, "", "")
				}
			}
		}
	}
	if deepSeek {
		if !observed || len(structured) == 0 {
			return nil, observed, errors.New(
				"DeepSeek Responses search returned no grounded structured results")
		}
		for _, result := range structured {
			collector.add(result.URL, result.Title, result.Snippet, "")
		}
		if len(collector.results) == 0 {
			return nil, observed, errors.New(
				"DeepSeek Responses search returned no grounded structured results")
		}
	}
	return collector.results, observed, nil
}

func parseDeepSeekSearchResults(text string) ([]ProviderResult, bool) {
	if text == "" || len(text) > nativeSearchMaxStructuredText || !utf8.ValidString(text) {
		return nil, false
	}
	var last []ProviderResult
	offset := 0
	candidates := 0
	for offset < len(text) {
		relative := strings.IndexByte(text[offset:], '{')
		if relative < 0 {
			break
		}
		start := offset + relative
		candidates++
		if candidates > nativeSearchMaxJSONCandidates {
			return nil, false
		}
		decoder := json.NewDecoder(strings.NewReader(text[start:]))
		decoder.UseNumber()
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err == nil {
			if parsed, ok := parseDeepSeekSearchResultObject(raw); ok {
				last = parsed
			}
		}
		offset = start + 1
	}
	return last, len(last) > 0
}

func parseDeepSeekSearchResultObject(raw json.RawMessage) ([]ProviderResult, bool) {
	if len(raw) == 0 || len(raw) > nativeSearchMaxStructuredText {
		return nil, false
	}
	var response struct {
		Results []struct {
			URL     *string `json:"url"`
			Title   *string `json:"title"`
			Snippet *string `json:"snippet"`
		} `json:"results"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) ||
		len(response.Results) == 0 || len(response.Results) > MaxSources {
		return nil, false
	}
	results := make([]ProviderResult, 0, len(response.Results))
	for _, item := range response.Results {
		if item.URL == nil || item.Title == nil || item.Snippet == nil {
			return nil, false
		}
		if _, err := CanonicalizePublicHTTPSURL(*item.URL); err != nil {
			continue
		}
		results = append(results, ProviderResult{URL: *item.URL,
			Title: *item.Title, Snippet: *item.Snippet})
	}
	return results, len(results) > 0
}

type responsesResultCollector struct {
	limit   int
	results []ProviderResult
	indexes map[string]int
}

func newResponsesResultCollector(limit int) *responsesResultCollector {
	return &responsesResultCollector{limit: limit,
		results: make([]ProviderResult, 0, limit), indexes: make(map[string]int, limit)}
}

func (c *responsesResultCollector) add(rawURL, title, snippet, published string) {
	canonical, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return
	}
	title = boundedCleanText(title, 1024)
	snippet = boundedSnippet(snippet)
	published = boundedCleanText(published, 128)
	if index, found := c.indexes[canonical]; found {
		if c.results[index].Title == "" {
			c.results[index].Title = title
		}
		if c.results[index].Snippet == "" {
			c.results[index].Snippet = snippet
		}
		if c.results[index].PublishedAt == "" {
			c.results[index].PublishedAt = published
		}
		return
	}
	if len(c.results) >= c.limit {
		return
	}
	c.indexes[canonical] = len(c.results)
	c.results = append(c.results, ProviderResult{URL: canonical, Title: title,
		Snippet: snippet, PublishedAt: published, Rank: len(c.results) + 1})
}

var _ QualifyingSearchProvider = (*OpenAIResponsesSearchProvider)(nil)
var _ ProviderGroundedSearchProvider = (*OpenAIResponsesSearchProvider)(nil)
