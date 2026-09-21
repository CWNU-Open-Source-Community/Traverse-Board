package webevidence

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/redact"
)

type sourceSearchStore interface {
	SaveSourceSearch(context.Context, []Source, Operation) (Operation, bool, error)
}

type sourceConnectorBinding struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Endpoint string `json:"endpoint"`
}

func (s *Service) SourceConnectorFingerprintFor(authority NetworkAuthority) string {
	if s == nil || len(s.connectors) == 0 || authority.Validate() != nil {
		return ""
	}
	bindings := make([]sourceConnectorBinding, 0, len(s.connectors))
	for _, connector := range s.connectors {
		endpoint, available := connectorEndpointAuthorized(connector, authority)
		if !available {
			continue
		}
		bindings = append(bindings, sourceConnectorBinding{Name: connector.Name(),
			Version: connector.Version(), Endpoint: endpoint})
	}
	if len(bindings) == 0 {
		return ""
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Name < bindings[j].Name })
	fingerprint, err := RequestFingerprint(bindings)
	if err != nil {
		return ""
	}
	return fingerprint
}

func (s *Service) SourceSearch(ctx context.Context, scope ExecutionScope,
	request SourceSearchRequest, operationKey string,
) (SourceSearchResult, error) {
	if err := s.ready(scope); err != nil {
		return SourceSearchResult{}, err
	}
	persistence, ok := s.store.(sourceSearchStore)
	if !ok {
		return SourceSearchResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"source search persistence is unavailable")
	}
	query := boundedCleanText(request.Query, MaxQueryRunes)
	if query == "" || redact.String(query) != query {
		return SourceSearchResult{}, apperror.New(apperror.CodeInvalidArgument,
			"source search query is required and cannot contain credential material")
	}
	limit := request.Limit
	if limit < 1 || limit > MaxSources {
		return SourceSearchResult{}, apperror.New(apperror.CodeInvalidArgument,
			"source search limit must be between 1 and 10")
	}
	requested, err := normalizeConnectorNames(request.Connectors)
	if err != nil {
		return SourceSearchResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"source search connectors are invalid", err)
	}
	expectedFingerprint := strings.TrimSpace(scope.ConnectorFingerprint)
	currentFingerprint := s.SourceConnectorFingerprintFor(scope.Authority)
	if !validDigest(expectedFingerprint) || currentFingerprint != expectedFingerprint {
		return SourceSearchResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"source connector authority changed after capability advertisement; request a fresh capability snapshot")
	}
	selected, err := s.selectSearchConnectors(requested, scope.Authority)
	if err != nil {
		return SourceSearchResult{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"requested source connector is unavailable for this Run", err)
	}
	names := make([]string, 0, len(selected))
	for _, connector := range selected {
		names = append(names, connector.Name())
	}
	canonicalRequest := struct {
		Connectors           []string `json:"connectors"`
		Query                string   `json:"query"`
		Limit                int      `json:"limit"`
		ConnectorFingerprint string   `json:"connector_fingerprint"`
	}{Connectors: names, Query: query, Limit: limit, ConnectorFingerprint: expectedFingerprint}
	fingerprint, _ := RequestFingerprint(canonicalRequest)
	keyDigest, err := ScopedOperationKeyDigest(scope.RunID, operationKey)
	if err != nil {
		return SourceSearchResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"source search operation key is invalid", err)
	}
	if replay, found, replayErr := s.replaySourceSearch(ctx, scope.RunID, keyDigest,
		fingerprint); found || replayErr != nil {
		return replay, replayErr
	}

	type connectorOutcome struct {
		connector SourceConnector
		items     []ConnectorSearchItem
		err       error
	}
	outcomes := make([]connectorOutcome, len(selected))
	var wait sync.WaitGroup
	for index, connector := range selected {
		wait.Add(1)
		go func(index int, connector SourceConnector) {
			defer wait.Done()
			items, searchErr := connector.Search(ctx, query, limit, scope.Authority)
			outcomes[index] = connectorOutcome{connector: connector, items: items, err: searchErr}
		}(index, connector)
	}
	wait.Wait()

	now := s.now().UTC()
	result := SourceSearchResult{ProtocolVersion: SourceSearchProtocolVersion,
		Query: query, Connectors: names, Sources: []SearchStub{}, Failures: []ConnectorFailure{},
		SearchedAt: now}
	sources := make([]Source, 0, limit)
	seenURLs := make(map[string]struct{}, limit)
	for _, outcome := range outcomes {
		if outcome.err != nil {
			result.Failures = append(result.Failures, *connectorFailure(outcome.connector.Name(), outcome.err))
		}
	}
	for rank := 0; len(result.Sources) < limit; rank++ {
		hasCandidate := false
		for _, outcome := range outcomes {
			if outcome.err != nil || rank >= len(outcome.items) {
				continue
			}
			hasCandidate = true
			item := outcome.items[rank]
			canonical, canonicalErr := scope.Authority.Authorize(item.URL)
			if canonicalErr != nil {
				continue
			}
			if _, exists := seenURLs[canonical]; exists {
				continue
			}
			seenURLs[canonical] = struct{}{}
			provider := "source:" + outcome.connector.Name()
			sourceID := StableSourceID(scope.RunID, canonical)
			source, lookupErr := s.store.GetWebSource(ctx, scope.RunID, sourceID)
			if lookupErr != nil && apperror.CodeOf(apperror.Normalize(lookupErr)) != apperror.CodeNotFound {
				return SourceSearchResult{}, apperror.Normalize(lookupErr)
			}
			if lookupErr != nil {
				source, lookupErr = SealSource(Source{ID: sourceID, RunID: scope.RunID,
					MissionID: scope.MissionID, WorkspaceID: scope.WorkspaceID,
					CanonicalURL: canonical, Title: boundedCleanText(redact.String(item.Title), 1024),
					Snippet: boundedSnippet(redact.String(item.Snippet)), Provider: provider,
					State: SourceDiscovered, DiscoveredAt: now})
			}
			if lookupErr != nil || source.RunID != scope.RunID ||
				source.MissionID != scope.MissionID || source.WorkspaceID != scope.WorkspaceID ||
				source.CanonicalURL != canonical {
				continue
			}
			sources = append(sources, source)
			result.Sources = append(result.Sources, SearchStub{SourceID: source.ID,
				CanonicalURL: source.CanonicalURL,
				Title:        boundedCleanText(redact.String(item.Title), 1024),
				Snippet:      boundedSnippet(redact.String(item.Snippet)),
				Rank:         len(result.Sources) + 1, Provider: provider,
				Fetched: false, Citeable: false, Untrusted: true})
			if len(result.Sources) == limit {
				break
			}
		}
		if !hasCandidate {
			break
		}
	}
	result.Partial = len(result.Failures) > 0
	response, err := marshalOperationResponse(result)
	if err != nil {
		return SourceSearchResult{}, apperror.Wrap(apperror.CodeInternal,
			"encode source search operation", err)
	}
	operation := Operation{ProtocolVersion: OperationProtocolVersion, KeyDigest: keyDigest,
		RequestFingerprint: fingerprint, RunID: scope.RunID, ToolName: "source_search",
		Response: response, CreatedAt: now}
	stored, replayed, err := persistence.SaveSourceSearch(ctx, sources, operation)
	if err != nil {
		return SourceSearchResult{}, apperror.Normalize(err)
	}
	if replayed {
		return decodeSourceSearchOperation(stored, fingerprint, true)
	}
	return result, nil
}

func (s *Service) selectSearchConnectors(requested []string,
	authority NetworkAuthority,
) ([]SourceConnector, error) {
	if s == nil || len(s.connectors) == 0 {
		return nil, errors.New("source connectors are unavailable")
	}
	names := requested
	if len(requested) == 1 && requested[0] == "auto" {
		names = names[:0]
		for name, connector := range s.connectors {
			if _, available := connectorEndpointAuthorized(connector, authority); available {
				names = append(names, name)
			}
		}
		sort.Strings(names)
	}
	selected := make([]SourceConnector, 0, len(names))
	for _, name := range names {
		connector := s.connectors[name]
		if _, available := connectorEndpointAuthorized(connector, authority); !available {
			return nil, errors.New("connector endpoint is outside the Run authority")
		}
		selected = append(selected, connector)
	}
	if len(selected) == 0 {
		return nil, errors.New("no searchable source connector is available")
	}
	return selected, nil
}

func (s *Service) replaySourceSearch(ctx context.Context, runID, keyDigest,
	fingerprint string,
) (SourceSearchResult, bool, error) {
	operation, found, err := s.store.GetWebEvidenceOperation(ctx, runID, keyDigest)
	if err != nil || !found {
		return SourceSearchResult{}, found, apperror.Normalize(err)
	}
	result, decodeErr := decodeSourceSearchOperation(operation, fingerprint, true)
	return result, true, decodeErr
}

func decodeSourceSearchOperation(operation Operation, fingerprint string,
	replayed bool,
) (SourceSearchResult, error) {
	if operation.Validate() != nil || operation.ToolName != "source_search" ||
		operation.RequestFingerprint != fingerprint {
		return SourceSearchResult{}, apperror.New(apperror.CodeConflict,
			"web evidence operation key was reused with different source search input")
	}
	var result SourceSearchResult
	decoder := json.NewDecoder(strings.NewReader(string(operation.Response)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return SourceSearchResult{}, apperror.Wrap(apperror.CodeInternal,
			"decode stored source search operation", err)
	}
	if result.ProtocolVersion != SourceSearchProtocolVersion || result.Replayed ||
		result.SearchedAt.IsZero() || len(result.Connectors) == 0 ||
		len(result.Sources) > MaxSources || result.Query == "" {
		return SourceSearchResult{}, apperror.New(apperror.CodeInternal,
			"stored source search result binding is invalid")
	}
	for index, source := range result.Sources {
		canonical, err := CanonicalizePublicHTTPSURL(source.CanonicalURL)
		if err != nil || canonical != source.CanonicalURL || source.Rank != index+1 ||
			source.SourceID != StableSourceID(operation.RunID, canonical) ||
			!strings.HasPrefix(source.Provider, "source:") || source.Fetched ||
			source.Citeable || source.LocallyVerified || !source.Untrusted ||
			source.InstructionAuthorized || source.ProviderGroundedCitation != nil {
			return SourceSearchResult{}, apperror.New(apperror.CodeInternal,
				"stored source search source is invalid")
		}
	}
	for _, failure := range result.Failures {
		if failure.Validate() != nil {
			return SourceSearchResult{}, apperror.New(apperror.CodeInternal,
				"stored source search failure is invalid")
		}
	}
	result.Replayed = replayed
	return result, nil
}

func connectorDocumentAsFetched(document ConnectorDocument,
	connector SourceConnector,
) FetchedContent {
	return FetchedContent{RequestedURL: document.CanonicalURL, FinalURL: document.CanonicalURL,
		HTTPStatus: document.HTTPStatus, RawDigest: DigestBytes([]byte(document.Body)),
		Parsed: ParsedDocument{Title: document.Title, Byline: document.Byline,
			PublishedAt: document.PublishedAt, MIME: document.MIME,
			Charset: document.Charset, Body: document.Body, Partial: document.Truncated},
		Robots: "api_not_applicable", Truncated: document.Truncated,
		Connector: connector.Name(), ConnectorVersion: connector.Version(),
		ContentKind:        document.ContentKind,
		RequestEndpoints:   append([]string(nil), document.RequestEndpoints...),
		ConnectorRawDigest: document.RawDigest, Coverage: document.Coverage,
		ItemsIncluded: document.ItemsIncluded, ItemsAvailable: document.ItemsAvailable,
		TruncationReason: document.TruncationReason, ContinuationFailure: document.ContinuationFailure}
}
