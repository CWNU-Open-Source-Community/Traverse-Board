package webevidence

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRequestTimeout = 15 * time.Second
	DefaultMaxResponse    = 2 * 1024 * 1024
	DefaultRedirectLimit  = 3
	DefaultAddressLimit   = 32
	WebEvidenceUserAgent  = "Traverse-Board-WebEvidence/1.0"
)

type DNSResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type TransportFactory func(host string, addresses []netip.Addr) http.RoundTripper

type SafeHTTPClient struct {
	Resolver         DNSResolver
	TransportFactory TransportFactory
	Timeout          time.Duration
	MaxRedirects     int
	MaxRetries       int
}

type HTTPDocument struct {
	RequestedURL string
	FinalURL     string
	StatusCode   int
	Header       http.Header
	Body         []byte
	Truncated    bool
	Redirects    int
}

func NewSafeHTTPClient() *SafeHTTPClient {
	return &SafeHTTPClient{Resolver: net.DefaultResolver, Timeout: DefaultRequestTimeout,
		MaxRedirects: DefaultRedirectLimit, MaxRetries: 1, TransportFactory: pinnedTransport}
}

func (c *SafeHTTPClient) Get(ctx context.Context, rawURL string, maxBytes int,
	accept string,
) (HTTPDocument, error) {
	return c.GetAuthorized(ctx, rawURL, maxBytes, accept, nil)
}

// GetAuthorized revalidates both public-address policy and the caller's Run
// authority before the initial request and every redirect hop.
func (c *SafeHTTPClient) GetAuthorized(ctx context.Context, rawURL string, maxBytes int,
	accept string, authorize func(string) error,
) (HTTPDocument, error) {
	if c == nil {
		return HTTPDocument{}, errors.New("safe web HTTP client is required")
	}
	if ctx == nil {
		return HTTPDocument{}, errors.New("safe web HTTP context is required")
	}
	if maxBytes <= 0 || maxBytes > DefaultMaxResponse {
		return HTTPDocument{}, errors.New("safe web response limit is invalid")
	}
	requested, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return HTTPDocument{}, err
	}
	current := requested
	redirectLimit := c.MaxRedirects
	if redirectLimit <= 0 || redirectLimit > DefaultRedirectLimit {
		redirectLimit = DefaultRedirectLimit
	}
	for redirects := 0; ; redirects++ {
		if authorize != nil {
			if err := authorize(current); err != nil {
				return HTTPDocument{}, fmt.Errorf("web request is outside Run authority: %w", err)
			}
		}
		maxRetries := c.MaxRetries
		if maxRetries < 0 || maxRetries > 1 {
			maxRetries = 1
		}
		var response *http.Response
		for attempt := 0; ; attempt++ {
			var requestErr error
			response, requestErr = c.getOnce(ctx, current, accept)
			if requestErr != nil {
				return HTTPDocument{}, requestErr
			}
			if !isRetryableHTTPStatus(response.StatusCode) || attempt >= maxRetries {
				break
			}
			if err := response.Body.Close(); err != nil {
				return HTTPDocument{}, fmt.Errorf("close web response before retry: %w", err)
			}
		}
		if isRedirect(response.StatusCode) {
			_ = response.Body.Close()
			if redirects >= redirectLimit {
				return HTTPDocument{}, errors.New("web redirect limit exceeded")
			}
			location := strings.TrimSpace(response.Header.Get("Location"))
			base, _ := url.Parse(current)
			next, parseErr := base.Parse(location)
			if parseErr != nil {
				return HTTPDocument{}, errors.New("web redirect location is invalid")
			}
			current, parseErr = CanonicalizePublicHTTPSURL(next.String())
			if parseErr != nil {
				return HTTPDocument{}, fmt.Errorf("web redirect escaped public HTTPS policy: %w", parseErr)
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return HTTPDocument{}, fmt.Errorf("read bounded web response: %w", readErr)
		}
		if closeErr != nil {
			return HTTPDocument{}, fmt.Errorf("close bounded web response: %w", closeErr)
		}
		truncated := len(body) > maxBytes
		if truncated {
			body = body[:maxBytes]
		}
		return HTTPDocument{RequestedURL: requested, FinalURL: current,
			StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: body,
			Truncated: truncated, Redirects: redirects}, nil
	}
}

func (c *SafeHTTPClient) getOnce(ctx context.Context, canonicalURL, accept string) (*http.Response, error) {
	parsed, _ := url.Parse(canonicalURL)
	host := parsed.Hostname()
	timeout := c.Timeout
	if timeout <= 0 || timeout > DefaultRequestTimeout {
		timeout = DefaultRequestTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	resolver := c.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupNetIP(requestCtx, "ip", host)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("resolve web target: %w", err)
	}
	if len(addresses) == 0 {
		cancel()
		return nil, errors.New("resolve web target returned no addresses")
	}
	if len(addresses) > DefaultAddressLimit {
		cancel()
		return nil, errors.New("resolve web target exceeded the address limit")
	}
	approved := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !IsPublicAddress(address) {
			cancel()
			return nil, errors.New("web target DNS resolved to a non-public address")
		}
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			approved = append(approved, address)
		}
	}
	factory := c.TransportFactory
	if factory == nil {
		factory = pinnedTransport
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, canonicalURL, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header.Set("Accept", strings.TrimSpace(accept))
	request.Header.Set("User-Agent", WebEvidenceUserAgent)
	request.Header.Set("Cache-Control", "no-cache")
	transport := factory(host, approved)
	if transport == nil {
		cancel()
		return nil, errors.New("safe web transport factory returned no pinned transport")
	}
	client := &http.Client{Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("request public web target: %w", err)
	}
	response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func pinnedTransport(host string, addresses []netip.Addr) http.RoundTripper {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	return &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: false,
		ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 64 * 1024,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12,
			ServerName: host},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil || port != "443" {
				return nil, errors.New("web transport attempted a non-HTTPS dial")
			}
			var last error
			for _, candidate := range addresses {
				conn, dialErr := dialer.DialContext(ctx, network,
					net.JoinHostPort(candidate.String(), strconv.Itoa(443)))
				if dialErr == nil {
					return conn, nil
				}
				last = dialErr
			}
			return nil, fmt.Errorf("dial pinned public web address: %w", last)
		},
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
