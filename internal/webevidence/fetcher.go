package webevidence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type FetchedContent struct {
	RequestedURL string
	FinalURL     string
	RawDigest    string
	Parsed       ParsedDocument
	Robots       string
	Redirects    int
	Truncated    bool
}

type FetchBackend interface {
	Fetch(context.Context, string, NetworkAuthority) (FetchedContent, error)
}

type Fetcher struct {
	Client       *SafeHTTPClient
	MaxResponse  int
	MaxBodyBytes int
	CheckRobots  bool
}

func NewFetcher(client *SafeHTTPClient) *Fetcher {
	if client == nil {
		client = NewSafeHTTPClient()
	}
	return &Fetcher{Client: client, MaxResponse: DefaultMaxResponse,
		MaxBodyBytes: MaxBodyBytes, CheckRobots: true}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string,
	authority NetworkAuthority,
) (FetchedContent, error) {
	if f == nil || f.Client == nil {
		return FetchedContent{}, errors.New("web fetcher is unavailable")
	}
	canonical, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return FetchedContent{}, err
	}
	robotsState := "not_checked"
	maxResponse := f.MaxResponse
	if maxResponse <= 0 || maxResponse > DefaultMaxResponse {
		maxResponse = DefaultMaxResponse
	}
	document, err := f.Client.GetAuthorized(ctx, canonical, maxResponse,
		"text/html,application/xhtml+xml,text/plain,text/markdown,application/pdf,application/json;q=0.8",
		func(raw string) error {
			if _, authorizeErr := authority.Authorize(raw); authorizeErr != nil {
				return authorizeErr
			}
			if !f.CheckRobots {
				return nil
			}
			// SafeHTTPClient invokes this guard before the initial request and
			// every redirect hop. Rechecking the destination path prevents an
			// allowed origin from redirecting around another origin's robots
			// policy.
			decision, robotsErr := CheckRobotsAuthorized(ctx, f.Client, raw, authority)
			robotsState = decision.State
			if robotsErr != nil {
				return fmt.Errorf("web fetch blocked because robots policy is unknown: %w",
					robotsErr)
			}
			if !decision.Allowed {
				return errors.New("web fetch blocked by robots policy")
			}
			return nil
		})
	if err != nil {
		return FetchedContent{}, err
	}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		return FetchedContent{}, fmt.Errorf("web fetch returned HTTP %d", document.StatusCode)
	}
	contentType := strings.TrimSpace(document.Header.Get("Content-Type"))
	if contentType == "" {
		return FetchedContent{}, errors.New("web fetch response omitted Content-Type")
	}
	maxBody := f.MaxBodyBytes
	if maxBody <= 0 || maxBody > MaxBodyBytes {
		maxBody = MaxBodyBytes
	}
	parsed, err := ParseDocument(document.Body, contentType, maxBody)
	if err != nil {
		return FetchedContent{}, err
	}
	return FetchedContent{RequestedURL: document.RequestedURL, FinalURL: document.FinalURL,
		RawDigest: DigestBytes(document.Body), Parsed: parsed, Robots: robotsState,
		Redirects: document.Redirects,
		Truncated: document.Truncated || parsed.Truncated || parsed.Partial}, nil
}
