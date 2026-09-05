package webevidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ProviderResult struct {
	URL         string
	Title       string
	Snippet     string
	PublishedAt string
	Rank        int
}

type SearchProvider interface {
	Name() string
	Endpoint() string
	Search(context.Context, string, int, NetworkAuthority) ([]ProviderResult, error)
}

const (
	SearchPolicyDisabled       = "disabled"
	SearchPolicyAuto           = "auto"
	SearchPolicySearXNG        = "searxng"
	SearchPolicyProviderNative = "provider_native"
)

// SearchRoute binds Web search to the exact model route selected by the
// active Run. The resolver, not an endpoint suffix, decides whether that
// Provider has an eligible search backend.
type SearchRoute struct {
	ModelRoute string
}

// SearchSelection is a secret-free record of the backend selected for one
// Run/model route. Backend and SelectionReason are persisted with the search
// operation so an `auto` decision is never an invisible fallback.
type SearchSelection struct {
	Policy          string
	Backend         string
	SelectionReason string
	Binding         string
	Provider        SearchProvider
	// ProviderAuthority is an exact, Go-derived egress boundary for a hosted
	// model Provider. It is intentionally independent from the Run's direct Web
	// fetch authority. SearXNG selections leave this zero and use the Run
	// authority because that endpoint is part of the operator's Web boundary.
	ProviderAuthority            NetworkAuthority
	ProviderAuthorityIndependent bool
}

// SearchProviderResolver resolves and, when required by policy, qualifies the
// search backend for the current Run model route. Implementations must fail
// closed on configuration, credential, and authority checks. An exact
// provider_native policy may expose a locally validated candidate whose first
// Search call performs the bounded hosted-tool probe; auto-selection may still
// require prior observed qualification before choosing a backend.
type SearchProviderResolver interface {
	ResolveSearch(context.Context, SearchRoute, NetworkAuthority) (SearchSelection, error)
}

// QualifyingSearchProvider proves that a hosted search adapter and the exact
// configured model actually support the requested tool before web_search is
// advertised. The returned binding must be credential-free.
type QualifyingSearchProvider interface {
	SearchProvider
	Qualify(context.Context, NetworkAuthority) (string, error)
}

// ProviderGroundedSearchProvider marks a first-party hosted-search adapter whose
// returned source URLs are grounded by the selected Provider's search tool. The
// marker is intentionally separate from SearchProvider: aggregators such as
// SearXNG still return discovery hints which require web_fetch before they are
// citeable. Implementations remain untrusted evidence and grant no authority.
type ProviderGroundedSearchProvider interface {
	QualifyingSearchProvider
	ProviderGroundedSearch() bool
}

const (
	SearchQualificationUnqualified = "unqualified"
	SearchQualificationReady       = "ready"
	SearchQualificationUnavailable = "unavailable"
)

// SearchQualificationSnapshot is a credential-free, read-only view of an
// adapter's already-observed capability. Reading it must never perform a
// Provider request. In particular, an operator declaration is unqualified
// until a real bounded qualification call has populated the positive cache.
type SearchQualificationSnapshot struct {
	Status string
	Reason string
}

// SearXNGProvider consumes only the documented JSON search endpoint. It has no
// credentials, cookies, HTML scraping, or fallback provider. The endpoint is
// process configuration and must also be inside the Run network authority.
type SearXNGProvider struct {
	client   *SafeHTTPClient
	endpoint string
}

func NewSearXNGProvider(client *SafeHTTPClient, endpoint string) (*SearXNGProvider, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("SearXNG search endpoint is required")
	}
	canonical, err := CanonicalizePublicHTTPSURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("SearXNG search endpoint: %w", err)
	}
	parsed, _ := url.Parse(canonical)
	if parsed.Path == "/" {
		parsed.Path = "/search"
	}
	parsed.RawQuery = ""
	if client == nil {
		client = NewSafeHTTPClient()
	}
	return &SearXNGProvider{client: client, endpoint: parsed.String()}, nil
}

func (p *SearXNGProvider) Name() string { return "searxng" }

func (p *SearXNGProvider) Endpoint() string {
	if p == nil {
		return ""
	}
	return p.endpoint
}

func (p *SearXNGProvider) Search(ctx context.Context, query string,
	limit int, authority NetworkAuthority,
) ([]ProviderResult, error) {
	if p == nil || p.client == nil || p.endpoint == "" {
		return nil, errors.New("web search provider is unavailable")
	}
	if limit <= 0 || limit > MaxSources {
		return nil, errors.New("web search result limit is invalid")
	}
	endpoint, _ := url.Parse(p.endpoint)
	parameters := endpoint.Query()
	parameters.Set("q", query)
	parameters.Set("format", "json")
	parameters.Set("safesearch", "1")
	endpoint.RawQuery = parameters.Encode()
	document, err := p.client.GetAuthorized(ctx, endpoint.String(), DefaultMaxResponse,
		"application/json", func(raw string) error {
			_, authorizeErr := authority.Authorize(raw)
			return authorizeErr
		})
	if err != nil {
		return nil, err
	}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("web search provider returned HTTP %d", document.StatusCode)
	}
	if document.Truncated {
		return nil, errors.New("web search provider response exceeded the configured limit")
	}
	mediaType, _, contentTypeErr := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if contentTypeErr != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, errors.New("web search provider response MIME must be application/json")
	}
	var response struct {
		Results []struct {
			URL           string   `json:"url"`
			Title         string   `json:"title"`
			Content       string   `json:"content"`
			PublishedDate any      `json:"publishedDate"`
			Engines       []string `json:"engines"`
		} `json:"results"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(document.Body)))
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode web search provider response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("web search provider response contains trailing JSON")
	}
	results := make([]ProviderResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, item := range response.Results {
		canonical, err := CanonicalizePublicHTTPSURL(item.URL)
		if err != nil {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		published := ""
		switch value := item.PublishedDate.(type) {
		case string:
			published = boundedCleanText(value, 128)
		case float64:
			published = time.Unix(int64(value), 0).UTC().Format(time.RFC3339)
		}
		results = append(results, ProviderResult{URL: canonical,
			Title:   boundedCleanText(item.Title, 1024),
			Snippet: boundedSnippet(item.Content), PublishedAt: published,
			Rank: len(results) + 1})
		if len(results) == limit {
			break
		}
	}
	return results, nil
}
