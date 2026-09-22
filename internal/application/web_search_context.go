package application

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

const (
	webSearchContextTitleTokens   = 32
	webSearchContextSnippetTokens = 64
)

type webSearchContextSource struct {
	SourceID         string `json:"source_id"`
	URL              string `json:"canonical_url"`
	Rank             int    `json:"rank"`
	Title            string `json:"title,omitempty"`
	Snippet          string `json:"snippet,omitempty"`
	TitleTruncated   bool   `json:"title_truncated,omitempty"`
	SnippetTruncated bool   `json:"snippet_truncated,omitempty"`
}

type webSearchContextOutput struct {
	ProtocolVersion       string                         `json:"protocol_version"`
	Query                 string                         `json:"query"`
	Provider              string                         `json:"provider"`
	SearchPolicy          string                         `json:"search_policy"`
	AllowedDomains        []string                       `json:"allowed_domains,omitempty"`
	BlockedDomains        []string                       `json:"blocked_domains,omitempty"`
	FilterPolicy          string                         `json:"filter_policy,omitempty"`
	FilteredOutCount      int                            `json:"filtered_out_count,omitempty"`
	SearchedAt            time.Time                      `json:"searched_at"`
	Sources               []webSearchContextSource       `json:"sources"`
	SourceCount           int                            `json:"source_count"`
	Provenance            string                         `json:"provenance"`
	SourceStateAt         string                         `json:"source_state_at"`
	Fetched               bool                           `json:"fetched"`
	Citeable              bool                           `json:"citeable"`
	LocallyVerified       bool                           `json:"locally_verified"`
	Untrusted             bool                           `json:"untrusted"`
	InstructionAuthorized bool                           `json:"instruction_authorized"`
	ContextExcerpt        bool                           `json:"context_excerpt"`
	OriginalResult        domain.HistoryReadRequest      `json:"original_result"`
	Connectors            []string                       `json:"connectors,omitempty"`
	Partial               bool                           `json:"partial,omitempty"`
	Failures              []webevidence.ConnectorFailure `json:"failures,omitempty"`
}

// Only the model-bound discovery copy is compacted. Native call/result pairing,
// the sealed result, and the full search operation remain unchanged. In
// particular, provider-grounded receipts are never reduced to discovery stubs.
func supervisorWebSearchContextResult(call domain.SupervisorToolCall) (string, error) {
	if call.ToolName != string(toolgateway.WebSearchTool) || call.Status != domain.SupervisorToolCompleted {
		return call.ResultJSON, nil
	}
	var envelope supervisorToolResultEnvelope
	if !decodeKnownWebSearchContextJSON(call.ResultJSON, &envelope) {
		return call.ResultJSON, nil
	}
	if envelope.Version != supervisorToolResultVersion || envelope.Tool != call.ToolName || envelope.Status != string(call.Status) {
		return call.ResultJSON, nil
	}
	var original webevidence.SearchResult
	if !decodeKnownWebSearchContextJSON(envelope.Stdout, &original) ||
		original.ProtocolVersion != webevidence.SearchProtocolVersion || len(original.Sources) > 10 {
		return call.ResultJSON, nil
	}
	if err := original.ValidateFilters(); err != nil {
		return "", err
	}
	for _, source := range original.Sources {
		canonical, err := webevidence.CanonicalizePublicHTTPSURL(source.CanonicalURL)
		if source.SourceID == "" || source.Rank < 1 || err != nil || canonical != source.CanonicalURL || source.Provider != original.Provider ||
			source.ProviderGroundedCitation != nil || source.Provenance != "" || source.Citeable ||
			source.Fetched || source.LocallyVerified || !source.Untrusted || source.InstructionAuthorized {
			return call.ResultJSON, nil
		}
	}
	// This is the existing history_read opaque tool reference, not a new lookup
	// namespace. The reader independently checks the current Thread and source.
	ref, err := json.Marshal(struct {
		Run     string `json:"r"`
		Turn    int    `json:"t"`
		Attempt string `json:"a"`
		Call    string `json:"c"`
	}{call.RunID, call.Turn, call.AttemptID, call.CallID})
	if err != nil {
		return "", err
	}
	output := webSearchContextOutput{
		ProtocolVersion: original.ProtocolVersion, Query: original.Query,
		Provider: original.Provider, SearchPolicy: original.SearchPolicy, SearchedAt: original.SearchedAt,
		AllowedDomains: original.AllowedDomains, BlockedDomains: original.BlockedDomains,
		FilterPolicy: original.FilterPolicy, FilteredOutCount: original.FilteredOutCount,
		Sources: make([]webSearchContextSource, 0, len(original.Sources)), SourceCount: len(original.Sources),
		Provenance: "discovery_only", SourceStateAt: "this_search_operation; later web_fetch results may contain fetched snapshots",
		Untrusted: true, ContextExcerpt: true,
		OriginalResult: domain.HistoryReadRequest{SourceID: "tool:" + base64.RawURLEncoding.EncodeToString(ref),
			Part: "result", ExpectedSHA256: session.ContentSHA256(call.ResultJSON)},
	}
	for _, source := range original.Sources {
		title, titleTruncated := boundedWebContextText(source.Title, webSearchContextTitleTokens)
		snippet, snippetTruncated := boundedWebContextText(source.Snippet, webSearchContextSnippetTokens)
		item := webSearchContextSource{SourceID: source.SourceID, URL: source.CanonicalURL,
			Rank: source.Rank, Title: title, Snippet: snippet,
			TitleTruncated: titleTruncated, SnippetTruncated: snippetTruncated}
		output.Sources = append(output.Sources, item)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	envelope.Stdout = string(encoded)
	envelope.Truncated = true
	envelope.Metadata = map[string]string{
		"context_excerpt": "true", "result_sha256": output.OriginalResult.ExpectedSHA256,
		"arguments_sha256": session.ContentSHA256(call.PayloadJSON),
	}
	projected, err := marshalSupervisorToolResultEnvelope(envelope)
	return string(projected), err
}

func supervisorSourceSearchContextResult(call domain.SupervisorToolCall) (string, error) {
	if call.ToolName != string(toolgateway.SourceSearchTool) ||
		call.Status != domain.SupervisorToolCompleted {
		return call.ResultJSON, nil
	}
	var envelope supervisorToolResultEnvelope
	if !decodeKnownWebSearchContextJSON(call.ResultJSON, &envelope) ||
		envelope.Version != supervisorToolResultVersion || envelope.Tool != call.ToolName ||
		envelope.Status != string(call.Status) {
		return call.ResultJSON, nil
	}
	var original webevidence.SourceSearchResult
	if !decodeKnownWebSearchContextJSON(envelope.Stdout, &original) ||
		original.ProtocolVersion != webevidence.SourceSearchProtocolVersion ||
		len(original.Sources) > webevidence.MaxSources || len(original.Connectors) > 3 ||
		len(original.Failures) > 3 || original.Partial != (len(original.Failures) > 0) {
		return call.ResultJSON, nil
	}
	for _, failure := range original.Failures {
		if failure.Validate() != nil {
			return call.ResultJSON, nil
		}
	}
	for _, source := range original.Sources {
		canonical, err := webevidence.CanonicalizePublicHTTPSURL(source.CanonicalURL)
		if source.SourceID == "" || source.Rank < 1 || err != nil ||
			canonical != source.CanonicalURL || !strings.HasPrefix(source.Provider, "source:") ||
			source.ProviderGroundedCitation != nil || source.Provenance != "" ||
			source.Citeable || source.Fetched || source.LocallyVerified ||
			!source.Untrusted || source.InstructionAuthorized {
			return call.ResultJSON, nil
		}
	}
	ref, err := json.Marshal(struct {
		Run     string `json:"r"`
		Turn    int    `json:"t"`
		Attempt string `json:"a"`
		Call    string `json:"c"`
	}{call.RunID, call.Turn, call.AttemptID, call.CallID})
	if err != nil {
		return "", err
	}
	output := webSearchContextOutput{ProtocolVersion: original.ProtocolVersion,
		Query: original.Query, Provider: "source_connectors",
		Connectors: original.Connectors, Partial: original.Partial, Failures: original.Failures,
		SearchPolicy: "platform_connectors", SearchedAt: original.SearchedAt,
		Sources:     make([]webSearchContextSource, 0, len(original.Sources)),
		SourceCount: len(original.Sources), Provenance: "connector_discovery",
		SourceStateAt: "this_source_search_operation; later web_fetch results may contain connector snapshots",
		Untrusted:     true, ContextExcerpt: true,
		OriginalResult: domain.HistoryReadRequest{SourceID: "tool:" +
			base64.RawURLEncoding.EncodeToString(ref), Part: "result",
			ExpectedSHA256: session.ContentSHA256(call.ResultJSON)}}
	for _, source := range original.Sources {
		title, titleTruncated := boundedWebContextText(source.Title, webSearchContextTitleTokens)
		snippet, snippetTruncated := boundedWebContextText(source.Snippet, webSearchContextSnippetTokens)
		output.Sources = append(output.Sources, webSearchContextSource{
			SourceID: source.SourceID, URL: source.CanonicalURL, Rank: source.Rank,
			Title: title, Snippet: snippet, TitleTruncated: titleTruncated,
			SnippetTruncated: snippetTruncated})
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	envelope.Stdout = string(encoded)
	envelope.Truncated = true
	envelope.Metadata = map[string]string{
		"context_excerpt": "true", "result_sha256": output.OriginalResult.ExpectedSHA256,
		"arguments_sha256": session.ContentSHA256(call.PayloadJSON),
	}
	projected, err := marshalSupervisorToolResultEnvelope(envelope)
	return string(projected), err
}

func decodeKnownWebSearchContextJSON(content string, target any) bool {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target) == nil && decoder.Decode(new(any)) == io.EOF
}

// Use the same conservative multilingual estimator as the aggregate request
// gate. A Unicode character cap alone can triple the intended CJK allowance.
func boundedWebContextText(value string, budget int) (string, bool) {
	if contextmgr.EstimateTokens(value) <= budget {
		return value, false
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		if contextmgr.EstimateTokens(string(runes[:middle])) <= budget {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return string(runes[:low]), true
}
