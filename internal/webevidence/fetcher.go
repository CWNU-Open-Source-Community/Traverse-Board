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
	HTTPStatus   int
	RawDigest    string
	Parsed       ParsedDocument
	Robots       string
	Redirects    int
	Truncated    bool
}

// RobotsPolicy determines whether robots observations are an execution gate or
// an audit fact. An empty policy is treated as RobotsPolicyEnforce so callers
// compiled against the original fail-closed contract do not silently widen
// their authority.
type RobotsPolicy string

const (
	RobotsPolicyEnforce   RobotsPolicy = "enforce"
	RobotsPolicyAuditOnly RobotsPolicy = "audit_only"
)

func (p RobotsPolicy) Valid() bool {
	return p == RobotsPolicyEnforce || p == RobotsPolicyAuditOnly
}

func effectiveRobotsPolicy(policy RobotsPolicy) RobotsPolicy {
	if policy == "" {
		return RobotsPolicyEnforce
	}
	return policy
}

type FetchBackend interface {
	Fetch(context.Context, string, NetworkAuthority, RobotsPolicy) (FetchedContent, error)
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
	authority NetworkAuthority, robotsPolicy RobotsPolicy,
) (FetchedContent, error) {
	if f == nil || f.Client == nil {
		return FetchedContent{}, errors.New("web fetcher is unavailable")
	}
	robotsPolicy = effectiveRobotsPolicy(robotsPolicy)
	if !robotsPolicy.Valid() {
		return FetchedContent{}, errors.New("web fetch robots policy is invalid")
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
			// every redirect hop. Rechecking preserves an honest per-hop audit
			// even when the selected permission makes robots non-blocking.
			decision, robotsErr := CheckRobotsAuthorized(ctx, f.Client, raw, authority)
			robotsState = mergeRobotsAuditState(robotsState, decision.State,
				robotsErr, robotsPolicy)
			if robotsErr != nil {
				if robotsPolicy == RobotsPolicyAuditOnly {
					return nil
				}
				return fmt.Errorf("web fetch blocked because robots policy is unknown: %w",
					robotsErr)
			}
			if !decision.Allowed {
				if robotsPolicy == RobotsPolicyAuditOnly {
					return nil
				}
				return errors.New("web fetch blocked by robots policy")
			}
			return nil
		})
	if err != nil {
		return FetchedContent{RequestedURL: canonical, FinalURL: canonical,
			Robots: robotsState}, err
	}
	fetched := FetchedContent{RequestedURL: document.RequestedURL,
		FinalURL: document.FinalURL, HTTPStatus: document.StatusCode,
		Robots: robotsState, Redirects: document.Redirects,
		Truncated: document.Truncated}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		return fetched, fmt.Errorf("web fetch returned HTTP %d", document.StatusCode)
	}
	contentType := strings.TrimSpace(document.Header.Get("Content-Type"))
	if contentType == "" {
		return fetched, errors.New("web fetch response omitted Content-Type")
	}
	maxBody := f.MaxBodyBytes
	if maxBody <= 0 || maxBody > MaxBodyBytes {
		maxBody = MaxBodyBytes
	}
	parsed, err := ParseDocument(document.Body, contentType, maxBody)
	if err != nil {
		return fetched, err
	}
	fetched.RawDigest = DigestBytes(document.Body)
	fetched.Parsed = parsed
	fetched.Truncated = fetched.Truncated || parsed.Truncated || parsed.Partial
	return fetched, nil
}

func mergeRobotsAuditState(current, observed string, observedErr error,
	policy RobotsPolicy,
) string {
	if policy == RobotsPolicyAuditOnly {
		switch {
		case observedErr != nil || observed == "unknown":
			observed = "bypassed_unknown"
		case observed == "blocked":
			observed = "bypassed_disallow"
		}
	}
	priority := func(state string) int {
		switch state {
		case "bypassed_disallow":
			return 6
		case "bypassed_unknown":
			return 5
		case "blocked":
			return 4
		case "unknown":
			return 3
		case "allowed":
			return 2
		case "not_present":
			return 1
		default:
			return 0
		}
	}
	if priority(observed) >= priority(current) {
		return observed
	}
	return current
}
