package webevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/redact"
)

const DefaultStaleAfter = 24 * time.Hour

type Store interface {
	GetWebEvidenceOperation(context.Context, string, string) (Operation, bool, error)
	SaveWebSearch(context.Context, []Source, Operation) (Operation, bool, error)
	SaveWebFetch(context.Context, Source, Snapshot, Operation) (Operation, bool, error)
	SaveWebCitation(context.Context, Citation, Operation) (Operation, bool, error)
	GetWebSource(context.Context, string, string) (Source, error)
	GetWebSnapshot(context.Context, string, string) (Snapshot, error)
	ListWebSources(context.Context, string, int) ([]Source, error)
	ListWebSnapshots(context.Context, string, int) ([]Snapshot, error)
	ListWebCitations(context.Context, string, int) ([]Citation, error)
}

type ExecutionScope struct {
	RunID       string
	MissionID   string
	WorkspaceID string
	Authority   NetworkAuthority
}

func (s ExecutionScope) Validate() error {
	if !validIdentity(s.RunID) || !validIdentity(s.MissionID) ||
		(s.WorkspaceID != "" && !validIdentity(s.WorkspaceID)) {
		return errors.New("web evidence execution scope identity is invalid")
	}
	return s.Authority.Validate()
}

type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type FetchRequest struct {
	SourceID string `json:"source_id,omitempty"`
	URL      string `json:"url,omitempty"`
}

type CiteRequest struct {
	SourceID   string `json:"source_id"`
	SnapshotID string `json:"snapshot_id"`
	Claim      string `json:"claim"`
	SpanStart  int    `json:"span_start,omitempty"`
	SpanEnd    int    `json:"span_end,omitempty"`
}

type Service struct {
	store      Store
	provider   SearchProvider
	fetcher    FetchBackend
	now        func() time.Time
	staleAfter time.Duration
}

func NewService(store Store, provider SearchProvider, fetcher FetchBackend) *Service {
	if fetcher == nil {
		fetcher = NewFetcher(nil)
	}
	return &Service{store: store, provider: provider, fetcher: fetcher,
		now: func() time.Time { return time.Now().UTC() }, staleAfter: DefaultStaleAfter}
}

func (s *Service) WithClock(now func() time.Time) *Service {
	if s != nil && now != nil {
		s.now = now
	}
	return s
}

func (s *Service) SearchAvailable() bool {
	return s != nil && s.provider != nil && strings.TrimSpace(s.provider.Endpoint()) != ""
}

func (s *Service) SearchAvailableFor(authority NetworkAuthority) bool {
	return s.SearchProviderFingerprintFor(authority) != ""
}

func (s *Service) SearchProviderFingerprintFor(authority NetworkAuthority) string {
	if !s.SearchAvailable() {
		return ""
	}
	endpoint, err := authority.Authorize(strings.TrimSpace(s.provider.Endpoint()))
	name := strings.TrimSpace(s.provider.Name())
	if err != nil || !validBoundedText(name, 256, false) || redact.String(name) != name {
		return ""
	}
	return DigestBytes([]byte(name + "\x00" + endpoint))
}

func (s *Service) Search(ctx context.Context, scope ExecutionScope, request SearchRequest,
	operationKey string,
) (SearchResult, error) {
	if err := s.ready(scope); err != nil {
		return SearchResult{}, err
	}
	query := boundedCleanText(request.Query, MaxQueryRunes)
	if query == "" || redact.String(query) != query {
		return SearchResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web search query is required and cannot contain credential material")
	}
	limit := request.Limit
	if limit == 0 {
		limit = 5
	}
	if limit < 1 || limit > MaxSources {
		return SearchResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web search limit must be between 1 and 10")
	}
	providerEndpoint := ""
	if s.provider != nil {
		providerEndpoint = strings.TrimSpace(s.provider.Endpoint())
	}
	if providerEndpoint == "" {
		return SearchResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"web_search_provider_unavailable: configure CYBERAGENT_WEB_SEARCH_ENDPOINT; no fallback provider was attempted")
	}
	providerName := strings.TrimSpace(s.provider.Name())
	if !validBoundedText(providerName, 256, false) || redact.String(providerName) != providerName {
		return SearchResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"web_search_provider_unavailable: configured provider identity is invalid; no fallback provider was attempted")
	}
	if _, err := scope.Authority.Authorize(providerEndpoint); err != nil {
		return SearchResult{}, apperror.Wrap(apperror.CodePolicyDenied,
			"web search provider is outside the Run network authority", err)
	}
	fingerprint, _ := RequestFingerprint(SearchRequest{Query: query, Limit: limit})
	keyDigest, err := ScopedOperationKeyDigest(scope.RunID, operationKey)
	if err != nil {
		return SearchResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"web search operation key is invalid", err)
	}
	if replay, found, replayErr := s.replaySearch(ctx, scope.RunID, keyDigest, fingerprint); found || replayErr != nil {
		return replay, replayErr
	}
	providerResults, err := s.provider.Search(ctx, query, limit, scope.Authority)
	if err != nil {
		return SearchResult{}, apperror.Wrap(apperror.CodeUnavailable,
			"web search provider request failed; no fallback provider was attempted", err)
	}
	now := s.now().UTC()
	result := SearchResult{ProtocolVersion: SearchProtocolVersion, Query: query,
		Provider: providerName, SearchedAt: now, Sources: []SearchStub{}}
	sources := make([]Source, 0, limit)
	for _, item := range providerResults {
		if len(result.Sources) == limit {
			break
		}
		canonical, canonicalErr := CanonicalizePublicHTTPSURL(item.URL)
		if canonicalErr != nil {
			continue
		}
		sourceID := StableSourceID(scope.RunID, canonical)
		source, lookupErr := s.store.GetWebSource(ctx, scope.RunID, sourceID)
		if lookupErr != nil && apperror.CodeOf(apperror.Normalize(lookupErr)) != apperror.CodeNotFound {
			return SearchResult{}, apperror.Normalize(lookupErr)
		}
		if lookupErr == nil && (source.RunID != scope.RunID ||
			source.MissionID != scope.MissionID || source.WorkspaceID != scope.WorkspaceID ||
			source.ID != sourceID || source.CanonicalURL != canonical) {
			return SearchResult{}, apperror.New(apperror.CodeInternal,
				"stored web search source escaped the active Run scope")
		}
		if lookupErr != nil {
			title := boundedCleanText(redact.String(item.Title), 1024)
			snippet := boundedSnippet(redact.String(item.Snippet))
			source, lookupErr = SealSource(Source{ID: sourceID,
				RunID: scope.RunID, MissionID: scope.MissionID, WorkspaceID: scope.WorkspaceID,
				CanonicalURL: canonical, Title: title,
				Snippet: snippet, Provider: providerName,
				State: SourceDiscovered, DiscoveredAt: now})
		}
		if lookupErr != nil {
			continue
		}
		sources = append(sources, source)
		result.Sources = append(result.Sources, SearchStub{SourceID: source.ID,
			CanonicalURL: source.CanonicalURL, Title: source.Title, Snippet: source.Snippet,
			Rank: len(result.Sources) + 1, Provider: source.Provider, Fetched: false})
	}
	response, err := marshalOperationResponse(result)
	if err != nil {
		return SearchResult{}, apperror.Wrap(apperror.CodeInternal,
			"encode web search operation", err)
	}
	operation := Operation{ProtocolVersion: OperationProtocolVersion, KeyDigest: keyDigest,
		RequestFingerprint: fingerprint, RunID: scope.RunID, ToolName: "web_search",
		Response: response, CreatedAt: now}
	stored, replayed, err := s.store.SaveWebSearch(ctx, sources, operation)
	if err != nil {
		return SearchResult{}, apperror.Normalize(err)
	}
	if replayed {
		return decodeSearchOperation(stored, fingerprint, true)
	}
	return result, nil
}

func (s *Service) Fetch(ctx context.Context, scope ExecutionScope, request FetchRequest,
	operationKey string,
) (FetchResult, error) {
	if err := s.ready(scope); err != nil {
		return FetchResult{}, err
	}
	request.SourceID = strings.TrimSpace(request.SourceID)
	request.URL = strings.TrimSpace(request.URL)
	if (request.SourceID == "") == (request.URL == "") {
		return FetchResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web fetch requires exactly one of source_id or url")
	}
	if request.SourceID != "" && redact.String(request.SourceID) != request.SourceID {
		return FetchResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web fetch source_id cannot contain credential material")
	}
	var source Source
	var err error
	now := s.now().UTC()
	if request.SourceID != "" {
		source, err = s.store.GetWebSource(ctx, scope.RunID, request.SourceID)
		if err != nil {
			return FetchResult{}, apperror.Normalize(err)
		}
	} else {
		canonical, authorizeErr := scope.Authority.Authorize(request.URL)
		if authorizeErr != nil {
			return FetchResult{}, apperror.Wrap(apperror.CodePolicyDenied,
				"web fetch target is outside the Run network authority", authorizeErr)
		}
		sourceID := StableSourceID(scope.RunID, canonical)
		source, err = s.store.GetWebSource(ctx, scope.RunID, sourceID)
		if err != nil && apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeNotFound {
			return FetchResult{}, apperror.Normalize(err)
		}
		if err != nil {
			source, err = SealSource(Source{ID: sourceID,
				RunID: scope.RunID, MissionID: scope.MissionID, WorkspaceID: scope.WorkspaceID,
				CanonicalURL: canonical, Provider: "direct", State: SourceDiscovered,
				DiscoveredAt: now})
		}
		if err != nil {
			return FetchResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
				"web fetch source is invalid", err)
		}
	}
	if source.RunID != scope.RunID || source.MissionID != scope.MissionID ||
		source.WorkspaceID != scope.WorkspaceID {
		return FetchResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"web fetch source does not match the active Run scope")
	}
	if _, err := scope.Authority.Authorize(source.CanonicalURL); err != nil {
		return FetchResult{}, apperror.Wrap(apperror.CodePolicyDenied,
			"web fetch source is outside the Run network authority", err)
	}
	canonicalRequest := FetchRequest{SourceID: source.ID}
	fingerprint, _ := RequestFingerprint(canonicalRequest)
	keyDigest, err := ScopedOperationKeyDigest(scope.RunID, operationKey)
	if err != nil {
		return FetchResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"web fetch operation key is invalid", err)
	}
	if replay, found, replayErr := s.replayFetch(ctx, scope.RunID, keyDigest, fingerprint); found || replayErr != nil {
		return replay, replayErr
	}
	fetched, fetchErr := s.fetcher.Fetch(ctx, source.CanonicalURL, scope.Authority)
	if fetchErr == nil {
		requested, requestedErr := scope.Authority.Authorize(fetched.RequestedURL)
		final, finalErr := scope.Authority.Authorize(fetched.FinalURL)
		if requestedErr != nil || finalErr != nil || requested != source.CanonicalURL ||
			final != fetched.FinalURL {
			fetchErr = errors.New(
				"web fetch backend URL escaped the source identity or Run authority")
		}
	}
	fetchedAt := s.now().UTC()
	snapshot := Snapshot{SourceID: source.ID, RunID: scope.RunID, MissionID: scope.MissionID,
		RequestedURL: source.CanonicalURL, FinalURL: source.CanonicalURL,
		FetchedAt: fetchedAt, StaleAt: fetchedAt.Add(s.staleAfter), Digest: DigestBytes(nil),
		MIME: "application/octet-stream", State: SourceFailed, Robots: "unknown",
		Provider: source.Provider}
	if fetchErr != nil {
		snapshot.ErrorCode = classifyFetchError(fetchErr)
		if blockedFetchErrorCode(snapshot.ErrorCode) {
			snapshot.State = SourceBlocked
			if snapshot.ErrorCode == "robots_blocked" {
				snapshot.Robots = "blocked"
			}
		}
	} else {
		body, redactionTruncated := boundUTF8(redact.String(fetched.Parsed.Body), MaxBodyBytes)
		snapshot.RequestedURL = fetched.RequestedURL
		snapshot.FinalURL = fetched.FinalURL
		snapshot.Title = boundedCleanText(redact.String(
			firstNonEmpty(fetched.Parsed.Title, source.Title)), 1024)
		snapshot.Byline = boundedCleanText(redact.String(fetched.Parsed.Byline), 512)
		snapshot.PublishedAt = boundedCleanText(redact.String(fetched.Parsed.PublishedAt), 128)
		snapshot.Digest = fetched.RawDigest
		snapshot.MIME = fetched.Parsed.MIME
		snapshot.Charset = fetched.Parsed.Charset
		snapshot.Body = body
		snapshot.Robots = fetched.Robots
		snapshot.Redirects = fetched.Redirects
		snapshot.Truncated = fetched.Truncated || redactionTruncated
		snapshot.State = SourceFetched
		if snapshot.Truncated || fetched.Parsed.Partial {
			snapshot.State = SourcePartial
		}
	}
	snapshot.ID = StableSnapshotID(source.ID, snapshot.Digest, fetchedAt)
	snapshot, err = SealSnapshot(snapshot)
	if err != nil {
		return FetchResult{}, apperror.Wrap(apperror.CodeInternal,
			"seal web fetch snapshot", err)
	}
	result := FetchResult{ProtocolVersion: FetchProtocolVersion, Source: source,
		Snapshot: snapshot}
	response, err := marshalOperationResponse(result)
	if err != nil {
		return FetchResult{}, apperror.Wrap(apperror.CodeInternal,
			"encode web fetch operation", err)
	}
	operation := Operation{ProtocolVersion: OperationProtocolVersion, KeyDigest: keyDigest,
		RequestFingerprint: fingerprint, RunID: scope.RunID, ToolName: "web_fetch",
		Response: response, CreatedAt: fetchedAt}
	stored, replayed, err := s.store.SaveWebFetch(ctx, source, snapshot, operation)
	if err != nil {
		return FetchResult{}, apperror.Normalize(err)
	}
	if replayed {
		return decodeFetchOperation(stored, fingerprint, true)
	}
	return result, nil
}

func (s *Service) Cite(ctx context.Context, scope ExecutionScope, request CiteRequest,
	operationKey string,
) (CitationResult, error) {
	if err := s.ready(scope); err != nil {
		return CitationResult{}, err
	}
	request.SourceID = strings.TrimSpace(request.SourceID)
	request.SnapshotID = strings.TrimSpace(request.SnapshotID)
	request.Claim = boundedCleanText(request.Claim, MaxClaimRunes)
	if request.SourceID == "" || request.SnapshotID == "" || request.Claim == "" ||
		redact.String(request.SourceID) != request.SourceID ||
		redact.String(request.SnapshotID) != request.SnapshotID ||
		redact.String(request.Claim) != request.Claim ||
		request.SpanStart < 0 || request.SpanEnd < 0 ||
		(request.SpanEnd == 0 && request.SpanStart != 0) ||
		(request.SpanEnd != 0 && request.SpanEnd <= request.SpanStart) {
		return CitationResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web citation source, snapshot, claim, or span is invalid")
	}
	fingerprint, _ := RequestFingerprint(request)
	keyDigest, err := ScopedOperationKeyDigest(scope.RunID, operationKey)
	if err != nil {
		return CitationResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"web citation operation key is invalid", err)
	}
	if replay, found, replayErr := s.replayCitation(ctx, scope.RunID, keyDigest, fingerprint); found || replayErr != nil {
		return replay, replayErr
	}
	source, err := s.store.GetWebSource(ctx, scope.RunID, request.SourceID)
	if err != nil {
		return CitationResult{}, apperror.Normalize(err)
	}
	snapshot, err := s.store.GetWebSnapshot(ctx, scope.RunID, request.SnapshotID)
	if err != nil {
		return CitationResult{}, apperror.Normalize(err)
	}
	if source.RunID != scope.RunID || source.MissionID != scope.MissionID ||
		source.WorkspaceID != scope.WorkspaceID || snapshot.RunID != scope.RunID ||
		snapshot.MissionID != scope.MissionID || snapshot.SourceID != source.ID ||
		(snapshot.State != SourceFetched &&
			snapshot.State != SourcePartial) {
		return CitationResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"web citation requires a fetched snapshot from the same Run and source")
	}
	if request.SpanEnd > utf8.RuneCountInString(snapshot.Body) {
		return CitationResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web citation span exceeds the fetched snapshot body")
	}
	now := s.now().UTC()
	citation, err := SealCitation(Citation{ID: StableCitationID(scope.RunID, keyDigest),
		RunID: scope.RunID, SourceID: source.ID, SnapshotID: snapshot.ID,
		Claim: request.Claim, SpanStart: request.SpanStart, SpanEnd: request.SpanEnd,
		URL: snapshot.FinalURL, Title: firstNonEmpty(snapshot.Title, source.Title),
		FetchedAt: snapshot.FetchedAt, StaleAt: snapshot.StaleAt, Digest: snapshot.Digest,
		Partial: snapshot.State == SourcePartial, Stale: snapshot.Stale(now), CreatedAt: now})
	if err != nil {
		return CitationResult{}, apperror.Wrap(apperror.CodeInternal,
			"seal web citation", err)
	}
	result := CitationResult{ProtocolVersion: CitationProtocolVersion, Citation: citation}
	response, err := marshalOperationResponse(result)
	if err != nil {
		return CitationResult{}, apperror.Wrap(apperror.CodeInternal,
			"encode web citation operation", err)
	}
	operation := Operation{ProtocolVersion: OperationProtocolVersion, KeyDigest: keyDigest,
		RequestFingerprint: fingerprint, RunID: scope.RunID, ToolName: "web_citation",
		Response: response, CreatedAt: now}
	stored, replayed, err := s.store.SaveWebCitation(ctx, citation, operation)
	if err != nil {
		return CitationResult{}, apperror.Normalize(err)
	}
	if replayed {
		return decodeCitationOperation(stored, fingerprint, true)
	}
	return result, nil
}

func (s *Service) ready(scope ExecutionScope) error {
	if s == nil || s.store == nil || s.fetcher == nil || s.now == nil {
		return apperror.New(apperror.CodeFailedPrecondition,
			"web evidence service is unavailable")
	}
	if scope.Authority.Mode == "disabled" {
		return apperror.New(apperror.CodeFailedPrecondition,
			"web_evidence_network_disabled: enable Run network_mode=allowlist and add an allowed target")
	}
	if err := scope.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"web evidence execution scope is invalid", err)
	}
	return nil
}

func (s *Service) replaySearch(ctx context.Context, runID, keyDigest,
	fingerprint string,
) (SearchResult, bool, error) {
	operation, found, err := s.store.GetWebEvidenceOperation(ctx, runID, keyDigest)
	if err != nil || !found {
		return SearchResult{}, found, apperror.Normalize(err)
	}
	result, decodeErr := decodeSearchOperation(operation, fingerprint, true)
	return result, true, decodeErr
}

func (s *Service) replayFetch(ctx context.Context, runID, keyDigest,
	fingerprint string,
) (FetchResult, bool, error) {
	operation, found, err := s.store.GetWebEvidenceOperation(ctx, runID, keyDigest)
	if err != nil || !found {
		return FetchResult{}, found, apperror.Normalize(err)
	}
	result, decodeErr := decodeFetchOperation(operation, fingerprint, true)
	return result, true, decodeErr
}

func (s *Service) replayCitation(ctx context.Context, runID, keyDigest,
	fingerprint string,
) (CitationResult, bool, error) {
	operation, found, err := s.store.GetWebEvidenceOperation(ctx, runID, keyDigest)
	if err != nil || !found {
		return CitationResult{}, found, apperror.Normalize(err)
	}
	result, decodeErr := decodeCitationOperation(operation, fingerprint, true)
	return result, true, decodeErr
}

func decodeSearchOperation(operation Operation, fingerprint string,
	replayed bool,
) (SearchResult, error) {
	if operation.Validate() != nil {
		return SearchResult{}, apperror.New(apperror.CodeInternal,
			"stored web search operation is invalid")
	}
	if operation.ToolName != "web_search" || operation.RequestFingerprint != fingerprint {
		return SearchResult{}, apperror.New(apperror.CodeConflict,
			"web evidence operation key was reused with different search input")
	}
	var result SearchResult
	if err := json.Unmarshal(operation.Response, &result); err != nil {
		return SearchResult{}, apperror.Wrap(apperror.CodeInternal,
			"decode stored web search operation", err)
	}
	if err := validateStoredSearchResult(operation.RunID, result); err != nil {
		return SearchResult{}, apperror.Wrap(apperror.CodeInternal,
			"validate stored web search operation", err)
	}
	result.Replayed = replayed
	return result, nil
}

func decodeFetchOperation(operation Operation, fingerprint string,
	replayed bool,
) (FetchResult, error) {
	if operation.Validate() != nil {
		return FetchResult{}, apperror.New(apperror.CodeInternal,
			"stored web fetch operation is invalid")
	}
	if operation.ToolName != "web_fetch" || operation.RequestFingerprint != fingerprint {
		return FetchResult{}, apperror.New(apperror.CodeConflict,
			"web evidence operation key was reused with different fetch input")
	}
	var result FetchResult
	if err := json.Unmarshal(operation.Response, &result); err != nil {
		return FetchResult{}, apperror.Wrap(apperror.CodeInternal,
			"decode stored web fetch operation", err)
	}
	if result.ProtocolVersion != FetchProtocolVersion || result.Source.Validate() != nil ||
		result.Snapshot.Validate() != nil || result.Replayed ||
		result.Source.RunID != operation.RunID ||
		result.Snapshot.RunID != operation.RunID ||
		result.Snapshot.SourceID != result.Source.ID ||
		result.Snapshot.MissionID != result.Source.MissionID {
		return FetchResult{}, apperror.New(apperror.CodeInternal,
			"stored web fetch result binding is invalid")
	}
	result.Replayed = replayed
	return result, nil
}

func decodeCitationOperation(operation Operation, fingerprint string,
	replayed bool,
) (CitationResult, error) {
	if operation.Validate() != nil {
		return CitationResult{}, apperror.New(apperror.CodeInternal,
			"stored web citation operation is invalid")
	}
	if operation.ToolName != "web_citation" || operation.RequestFingerprint != fingerprint {
		return CitationResult{}, apperror.New(apperror.CodeConflict,
			"web evidence operation key was reused with different citation input")
	}
	var result CitationResult
	if err := json.Unmarshal(operation.Response, &result); err != nil {
		return CitationResult{}, apperror.Wrap(apperror.CodeInternal,
			"decode stored web citation operation", err)
	}
	if result.ProtocolVersion != CitationProtocolVersion || result.Citation.Validate() != nil ||
		result.Replayed || result.Citation.RunID != operation.RunID {
		return CitationResult{}, apperror.New(apperror.CodeInternal,
			"stored web citation result binding is invalid")
	}
	result.Replayed = replayed
	return result, nil
}

func validateStoredSearchResult(runID string, result SearchResult) error {
	if result.ProtocolVersion != SearchProtocolVersion ||
		boundedCleanText(result.Query, MaxQueryRunes) != result.Query || result.Query == "" ||
		!validBoundedText(result.Provider, 256, false) || result.SearchedAt.IsZero() ||
		result.Replayed || len(result.Sources) > MaxSources {
		return errors.New("stored web search result metadata is invalid")
	}
	seenSources := make(map[string]struct{}, len(result.Sources))
	seenURLs := make(map[string]struct{}, len(result.Sources))
	for index, source := range result.Sources {
		canonical, err := CanonicalizePublicHTTPSURL(source.CanonicalURL)
		if err != nil || canonical != source.CanonicalURL || !validIdentity(source.SourceID) ||
			StableSourceID(runID, canonical) != source.SourceID ||
			!validBoundedText(source.Title, 1024, true) ||
			!validBoundedBytes(source.Snippet, MaxSnippetBytes) ||
			!validBoundedText(source.Provider, 256, false) ||
			source.Rank != index+1 || source.Fetched {
			return errors.New("stored web search source binding is invalid")
		}
		if _, duplicate := seenSources[source.SourceID]; duplicate {
			return errors.New("stored web search source is duplicated")
		}
		if _, duplicate := seenURLs[canonical]; duplicate {
			return errors.New("stored web search URL is duplicated")
		}
		seenSources[source.SourceID] = struct{}{}
		seenURLs[canonical] = struct{}{}
	}
	return nil
}

func classifyFetchError(err error) string {
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "robots"):
		return "robots_blocked"
	case strings.Contains(value, "http 401"), strings.Contains(value, "http 403"):
		return "access_blocked"
	case strings.Contains(value, "mime"), strings.Contains(value, "content-type"):
		return "unsupported_content"
	case strings.Contains(value, "redirect"):
		return "redirect_blocked"
	case strings.Contains(value, "dns"), strings.Contains(value, "resolve"):
		return "dns_blocked"
	case strings.Contains(value, "run authority"), strings.Contains(value, "not public"),
		strings.Contains(value, "non-public"), strings.Contains(value, "metadata endpoint"),
		strings.Contains(value, "credential"):
		return "target_blocked"
	case strings.Contains(value, "response limit"), strings.Contains(value, "exceeded"):
		return "response_too_large"
	default:
		return "fetch_failed"
	}
}

func blockedFetchErrorCode(code string) bool {
	switch code {
	case "robots_blocked", "access_blocked", "redirect_blocked", "dns_blocked",
		"target_blocked":
		return true
	default:
		return false
	}
}

func marshalOperationResponse(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	// The operation ledger is JSON, not HTML. Disabling HTML escaping keeps a
	// valid 128 KiB snapshot bounded even for adversarial '<', '>', and '&' text.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if !json.Valid(encoded) {
		return nil, errors.New("encoded Web evidence operation is invalid JSON")
	}
	return append(json.RawMessage(nil), encoded...), nil
}

func (s *Service) String() string {
	return fmt.Sprintf("web evidence service provider=%v", s.provider != nil)
}
