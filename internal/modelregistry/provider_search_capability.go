package modelregistry

import (
	"net/url"
	"strings"
)

// ProviderNativeWebSearchKnownUnsupported is a read-only compatibility fact,
// not a rewrite of an operator's saved search policy. DeepSeek's current public
// Responses contract ignores built-in web_search tools. Old web_search_call
// input may still be restored as history; that does not execute a new search.
// See https://api-docs.deepseek.com/guides/responses_api/ (checked 2026-09-13).
//
// Only the exact official API host is recognized. A provider/model name or a
// proxy endpoint is not evidence of this contract. False means no known denial;
// the existing transport, declaration and runtime qualification still apply.
func ProviderNativeWebSearchKnownUnsupported(definition ProviderDefinition) bool {
	endpoint, err := url.Parse(strings.TrimSpace(definition.EndpointURL))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(endpoint.Hostname(), "."), "api.deepseek.com")
}
