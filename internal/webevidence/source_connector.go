package webevidence

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	SourceSearchProtocolVersion = "source_search.v1"
	DefaultConnectorItemLimit   = 20
	MaxConnectorItemLimit       = 50
)

// SourceConnector is a bounded, read-only adapter for public content whose
// useful evidence is not reliably represented by a generic HTML page. The
// connector performs transport and parsing only; Service still owns Run
// binding, immutable operations, snapshots, and citations.
type SourceConnector interface {
	Name() string
	Version() string
	SearchEndpoint() string
	MatchURL(string) bool
	Search(context.Context, string, int, NetworkAuthority) ([]ConnectorSearchItem, error)
	Read(context.Context, string, int, NetworkAuthority) (ConnectorDocument, error)
}

type ConnectorSearchItem struct {
	URL         string
	Title       string
	Snippet     string
	PublishedAt string
	ExternalID  string
}

type ConnectorDocument struct {
	CanonicalURL        string
	RequestEndpoints    []string
	HTTPStatus          int
	RawDigest           string
	Title               string
	Byline              string
	PublishedAt         string
	MIME                string
	Charset             string
	Body                string
	ContentKind         string
	Coverage            string
	ItemsIncluded       int
	ItemsAvailable      int // Zero means the platform does not report a total.
	Truncated           bool
	TruncationReason    string
	ContinuationFailure *ConnectorFailure
}

type ConnectorFailure struct {
	Connector       string `json:"connector"`
	Code            string `json:"code"`
	HTTPStatus      int    `json:"http_status,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	RetryAfter      string `json:"retry_after,omitempty"`
	RateLimitReset  string `json:"rate_limit_reset,omitempty"`
	RemoteRequestID string `json:"remote_request_id,omitempty"`
}

func (f ConnectorFailure) Validate() error {
	if !validConnectorIdentity(f.Connector) || !validBoundedText(f.Code, 128, false) ||
		(f.HTTPStatus != 0 && (f.HTTPStatus < 100 || f.HTTPStatus > 599)) ||
		!validBoundedText(f.RetryAfter, 128, true) || !validBoundedText(f.RateLimitReset, 128, true) ||
		!validBoundedText(f.RemoteRequestID, 256, true) {
		return errors.New("connector failure metadata is invalid")
	}
	if f.Endpoint != "" {
		canonical, err := CanonicalizePublicHTTPSURL(f.Endpoint)
		if err != nil || canonical != f.Endpoint {
			return errors.New("connector failure endpoint is invalid")
		}
	}
	return nil
}

func connectorFailure(name string, err error) *ConnectorFailure {
	status, endpoint, retryAfter, reset, requestID := connectorErrorObservation(err)
	return &ConnectorFailure{Connector: name, Code: connectorFailureCode(err), HTTPStatus: status,
		Endpoint: endpoint, RetryAfter: retryAfter, RateLimitReset: reset, RemoteRequestID: requestID}
}

type SourceSearchResult struct {
	ProtocolVersion string             `json:"protocol_version"`
	Query           string             `json:"query"`
	Connectors      []string           `json:"connectors"`
	Sources         []SearchStub       `json:"sources"`
	Failures        []ConnectorFailure `json:"failures"`
	SearchedAt      time.Time          `json:"searched_at"`
	Partial         bool               `json:"partial"`
	Replayed        bool               `json:"replayed"`
}

type SourceSearchRequest struct {
	Connectors []string `json:"connectors"`
	Query      string   `json:"query"`
	Limit      int      `json:"limit"`
}

type connectorError struct {
	code            string
	err             error
	httpStatus      int
	endpoint        string
	retryAfter      string
	rateLimitReset  string
	remoteRequestID string
}

func (e *connectorError) Error() string {
	if e == nil {
		return "source connector failed"
	}
	if e.err == nil {
		return e.code
	}
	return e.code + ": " + e.err.Error()
}

func (e *connectorError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newConnectorError(code string, err error) error {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "connector_failed"
	}
	return &connectorError{code: code, err: err}
}

func newConnectorHTTPError(code string, document HTTPDocument, err error) error {
	endpoint := document.FinalURL
	if endpoint == "" {
		endpoint = document.RequestedURL
	}
	return &connectorError{code: code, err: err, httpStatus: document.StatusCode,
		endpoint:       endpoint,
		retryAfter:     boundedConnectorText(document.Header.Get("Retry-After"), 128),
		rateLimitReset: boundedConnectorText(document.Header.Get("X-RateLimit-Reset"), 128),
		remoteRequestID: boundedConnectorText(firstNonEmpty(
			document.Header.Get("X-GitHub-Request-Id"),
			document.Header.Get("X-Request-Id")), 256)}
}

func connectorErrorObservation(err error) (status int, endpoint, retryAfter,
	rateLimitReset, remoteRequestID string,
) {
	var typed *connectorError
	if !errors.As(err, &typed) || typed == nil {
		return 0, "", "", "", ""
	}
	return typed.httpStatus, typed.endpoint, typed.retryAfter,
		typed.rateLimitReset, typed.remoteRequestID
}

func connectorFailureCode(err error) string {
	var typed *connectorError
	if errors.As(err, &typed) && typed != nil && typed.code != "" {
		return typed.code
	}
	value := strings.ToLower(fmt.Sprint(err))
	switch {
	case strings.Contains(value, "context deadline"), strings.Contains(value, "timeout"):
		return "timeout"
	case strings.Contains(value, "resolve"), strings.Contains(value, "dns"):
		return "network_unavailable"
	default:
		return "connector_failed"
	}
}

func normalizeConnectorName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validConnectorIdentity(value string) bool {
	value = normalizeConnectorName(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, current := range value {
		if (current < 'a' || current > 'z') && (current < '0' || current > '9') &&
			current != '_' && current != '-' {
			return false
		}
	}
	return true
}

func normalizeConnectorNames(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 3 {
		return nil, errors.New("source search requires between one and three connectors")
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		name := normalizeConnectorName(value)
		if name == "auto" {
			if len(values) != 1 {
				return nil, errors.New("source search auto connector cannot be combined")
			}
			return []string{"auto"}, nil
		}
		if !validConnectorIdentity(name) {
			return nil, errors.New("source search connector identity is invalid")
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	if len(normalized) == 0 {
		return nil, errors.New("source search requires a connector")
	}
	sort.Strings(normalized)
	return normalized, nil
}

func connectorEndpointAuthorized(connector SourceConnector, authority NetworkAuthority) (string, bool) {
	if connector == nil || !validConnectorIdentity(connector.Name()) ||
		strings.TrimSpace(connector.Version()) == "" {
		return "", false
	}
	endpoint := strings.TrimSpace(connector.SearchEndpoint())
	if endpoint == "" {
		return "", false
	}
	canonical, err := authority.Authorize(endpoint)
	return canonical, err == nil
}

func connectorForURL(connectors map[string]SourceConnector, rawURL, hint string) (SourceConnector, error) {
	hint = normalizeConnectorName(hint)
	if hint != "" && hint != "auto" {
		connector := connectors[hint]
		if connector == nil {
			return nil, errors.New("requested source connector is unavailable")
		}
		if hint != "rss" && !connector.MatchURL(rawURL) {
			return nil, errors.New("requested source connector does not support this URL")
		}
		return connector, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("source URL is invalid")
	}
	names := make([]string, 0, len(connectors))
	for name := range connectors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		connector := connectors[name]
		if connector != nil && connector.MatchURL(rawURL) {
			return connector, nil
		}
	}
	return nil, nil
}

func fetchSourceProvider(connector SourceConnector) string {
	if connector == nil {
		return "direct"
	}
	return "source:" + normalizeConnectorName(connector.Name())
}

func NewDefaultSourceConnectors(client *SafeHTTPClient) []SourceConnector {
	if client == nil {
		client = NewSafeHTTPClient()
	}
	return []SourceConnector{
		NewGitHubSourceConnector(client),
		NewHackerNewsSourceConnector(client),
		NewRSSSourceConnector(client),
	}
}
