package webevidence

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/redact"
)

// CheckSearchConnection performs at most one HTTP request to the configured
// Responses endpoint. An explicit check bypasses negative qualification cache
// entries, but never probes alternate tools or another search backend.
func (p *OpenAIResponsesSearchProvider) CheckSearchConnection(ctx context.Context,
	query string, limit int, authority NetworkAuthority,
) ([]ProviderResult, error) {
	if p == nil || ctx == nil || limit < 1 || limit > MaxSources {
		return nil, nativeSearchUnsentDiagnostic("not_configured")
	}
	if err := ctx.Err(); err != nil {
		diagnostic := nativeSearchTransportDiagnostic(ctx, err)
		diagnostic.RequestNotAttempted = true
		return nil, diagnostic
	}
	query = boundedCleanText(query, MaxQueryRunes)
	if query == "" || redact.String(query) != query {
		return nil, nativeSearchUnsentDiagnostic("not_configured")
	}
	state, err := p.resolveState(ctx, authority)
	if err != nil {
		var failure *NativeSearchQualificationError
		if errors.As(err, &failure) {
			switch failure.Reason {
			case NativeSearchReasonEndpointUnauthorized:
				return nil, nativeSearchUnsentDiagnostic("not_authorized")
			case NativeSearchReasonCredentialUnavailable:
				return nil, nativeSearchUnsentDiagnostic("authentication")
			}
		}
		return nil, nativeSearchUnsentDiagnostic("not_configured")
	}
	tool, found := p.cachedTool(state)
	if !found {
		tool = nativeSearchTool
	}
	payload, headers, err := p.prepareRequest(state, query, tool)
	if err != nil {
		return nil, nativeSearchUnsentDiagnostic("not_configured")
	}
	if err := ctx.Err(); err != nil {
		diagnostic := nativeSearchTransportDiagnostic(ctx, err)
		diagnostic.RequestNotAttempted = true
		return nil, diagnostic
	}
	document, err := p.client.PostJSONAuthorizedNoRedirect(ctx, p.endpoint, payload,
		nativeSearchResponseLimit, headers, func(raw string) error {
			_, err := authority.Authorize(raw)
			return err
		})
	if err != nil {
		return nil, nativeSearchTransportDiagnostic(ctx, err)
	}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		// Authentication and rate-limit statuses take precedence over text in an
		// untrusted error body that happens to mention an unsupported tool.
		failure := searchHTTPDiagnostic(document, "Provider search connection check failed")
		var diagnostic *SearchDiagnosticError
		if errors.As(failure, &diagnostic) && diagnostic.Code == "provider_rejected" &&
			!document.Truncated && explicitUnsupportedResponsesTool(document, tool) {
			diagnostic.Code = "tool_unsupported"
		}
		return nil, failure
	}
	if document.Truncated {
		return nil, nativeSearchDiagnostic("invalid_response", document.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, nativeSearchDiagnostic("invalid_response", document.StatusCode)
	}
	results, observed, err := parseResponsesSearchResults(document.Body, limit, p.deepSeek)
	if err != nil || !observed {
		return nil, nativeSearchDiagnostic("invalid_response", document.StatusCode)
	}
	// Qualification belongs to the exact credential/model/runtime generation
	// that sent the request; a concurrent rotation cannot publish it as current.
	current, err := p.resolveState(ctx, authority)
	if err != nil || current.baseKey != state.baseKey {
		return nil, nativeSearchDiagnostic("configuration_changed", document.StatusCode)
	}
	p.storeTool(state, tool)
	return results, nil
}

func nativeSearchDiagnostic(code string, status int) *SearchDiagnosticError {
	return &SearchDiagnosticError{Code: code, HTTPStatus: status, Message: "Provider search connection check failed"}
}

func nativeSearchUnsentDiagnostic(code string) *SearchDiagnosticError {
	failure := nativeSearchDiagnostic(code, 0)
	failure.RequestNotAttempted = true
	return failure
}

func nativeSearchTransportDiagnostic(ctx context.Context, err error) *SearchDiagnosticError {
	var timeout net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &timeout) && timeout.Timeout()) {
		return nativeSearchDiagnostic("timeout", 0)
	}
	return nativeSearchDiagnostic("network", 0)
}
