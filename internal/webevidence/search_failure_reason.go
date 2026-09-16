package webevidence

import (
	"context"
	"errors"
	"net"
	"strings"
)

// Durable web_search failures separate two provider failure categories so an
// operator can tell a transport outage from a search service that answered
// without usable results. The apperror code stays UNAVAILABLE; only the
// bounded durable message gains one stable reason. Provider bodies, request
// headers, credentials, and raw transport details never enter the message.
const (
	// SearchFailureReasonUnreachable covers connection, proxy, TLS, DNS, and
	// timeout failures where no usable provider response existed.
	SearchFailureReasonUnreachable = "provider unreachable (network/proxy)"
	// SearchFailureReasonNoUsableResults covers a received provider response
	// that produced no results: HTTP non-200, access challenge pages,
	// unsupported or unparseable pages, and empty result sets.
	SearchFailureReasonNoUsableResults = "no usable results"
)

// Markers stay intentionally narrow and describe only errors produced by this
// package's own search providers and safe HTTP client. Provider-controlled
// response text never becomes an error string, so a marker is never derived
// from page content. Typed transport errors are checked before any marker.
var searchUnreachableMarkers = []string{
	"resolve web target returned no addresses",
	"connect configured web proxy failed",
	"dial pinned public web address",
	"connection refused",
	"connection reset",
	"broken pipe",
	"no such host",
	"i/o timeout",
	"network is unreachable",
	"network is down",
	"tls:",
	"x509",
	"proxy connect",
}

var searchNoUsableResultMarkers = []string{
	"access challenge",
	"no usable public https results",
	"returned an unsupported page",
	"returned an invalid page",
	"page was unavailable or exceeded the response limit",
	"response exceeded the configured limit",
	"response mime must be application/json",
	"decode web search provider response",
	"response contains trailing json",
	"returned http ",
	"redirects are forbidden",
}

// ClassifySearchProviderFailure maps a plain search provider error into one of
// the two stable categories, or returns "" when the failure fits neither and
// the caller must keep the generic message. The error itself is never echoed:
// only the category crosses into the durable message.
func ClassifySearchProviderFailure(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return SearchFailureReasonUnreachable
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		// The safe HTTP client wraps dial, proxy CONNECT, TLS, and deadline
		// failures in *url.Error and *net.OpError, which all implement
		// net.Error. This typed check precedes every text marker.
		return SearchFailureReasonUnreachable
	}
	value := strings.ToLower(err.Error())
	for _, marker := range searchUnreachableMarkers {
		if strings.Contains(value, marker) {
			return SearchFailureReasonUnreachable
		}
	}
	for _, marker := range searchNoUsableResultMarkers {
		if strings.Contains(value, marker) {
			return SearchFailureReasonNoUsableResults
		}
	}
	return ""
}
