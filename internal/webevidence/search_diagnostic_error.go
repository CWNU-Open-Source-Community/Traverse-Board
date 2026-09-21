package webevidence

// SearchDiagnosticError carries only bounded, public failure information.
// Provider bodies, credentials and transport error text are never projected.
type SearchDiagnosticError struct {
	RequestNotAttempted bool
	Code                string
	HTTPStatus          int
	RetryAfter          string
	RateLimitReset      string
	Message             string
}

func (e *SearchDiagnosticError) Error() string { return e.Message }

func searchHTTPDiagnostic(document HTTPDocument, message string) error {
	code := "provider_rejected"
	switch document.StatusCode {
	case 401:
		code = "authentication"
	case 429:
		code = "rate_limited"
	case 403:
		if document.Header.Get("X-RateLimit-Remaining") == "0" || document.Header.Get("Retry-After") != "" {
			code = "rate_limited"
		}
	}
	result := &SearchDiagnosticError{Code: code, HTTPStatus: document.StatusCode, Message: message}
	if code == "rate_limited" {
		result.RetryAfter = boundedConnectorText(document.Header.Get("Retry-After"), 128)
		result.RateLimitReset = boundedConnectorText(document.Header.Get("X-RateLimit-Reset"), 128)
	}
	return result
}
