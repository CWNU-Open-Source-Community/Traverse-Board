package webevidence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
)

type memoryWebStore struct {
	operations map[string]Operation
	sources    map[string]Source
	snapshots  map[string]Snapshot
	citations  map[string]Citation
}

func newMemoryWebStore() *memoryWebStore {
	return &memoryWebStore{operations: map[string]Operation{}, sources: map[string]Source{},
		snapshots: map[string]Snapshot{}, citations: map[string]Citation{}}
}

func scopedMemoryKey(runID, id string) string { return runID + "\x00" + id }

func (s *memoryWebStore) GetWebEvidenceOperation(_ context.Context, runID,
	keyDigest string,
) (Operation, bool, error) {
	value, found := s.operations[scopedMemoryKey(runID, keyDigest)]
	return value, found, nil
}

func (s *memoryWebStore) saveOperation(operation Operation) (Operation, bool, error) {
	key := scopedMemoryKey(operation.RunID, operation.KeyDigest)
	if existing, found := s.operations[key]; found {
		if existing.RequestFingerprint != operation.RequestFingerprint {
			return Operation{}, false, apperror.New(apperror.CodeConflict,
				"operation key conflict")
		}
		return existing, true, nil
	}
	s.operations[key] = operation
	return operation, false, nil
}

func (s *memoryWebStore) SaveWebSearch(_ context.Context, sources []Source,
	operation Operation,
) (Operation, bool, error) {
	if existing, replayed, err := s.saveOperation(operation); err != nil || replayed {
		return existing, replayed, err
	}
	for _, source := range sources {
		s.sources[scopedMemoryKey(source.RunID, source.ID)] = source
	}
	return operation, false, nil
}

func (s *memoryWebStore) SaveWebFetch(_ context.Context, source Source,
	snapshot Snapshot, operation Operation,
) (Operation, bool, error) {
	if existing, replayed, err := s.saveOperation(operation); err != nil || replayed {
		return existing, replayed, err
	}
	s.sources[scopedMemoryKey(source.RunID, source.ID)] = source
	s.snapshots[scopedMemoryKey(snapshot.RunID, snapshot.ID)] = snapshot
	return operation, false, nil
}

func (s *memoryWebStore) SaveWebCitation(_ context.Context, citation Citation,
	operation Operation,
) (Operation, bool, error) {
	if existing, replayed, err := s.saveOperation(operation); err != nil || replayed {
		return existing, replayed, err
	}
	s.citations[scopedMemoryKey(citation.RunID, citation.ID)] = citation
	return operation, false, nil
}

func (s *memoryWebStore) GetWebSource(_ context.Context, runID,
	sourceID string,
) (Source, error) {
	if value, found := s.sources[scopedMemoryKey(runID, sourceID)]; found {
		return value, nil
	}
	return Source{}, apperror.New(apperror.CodeNotFound, "source not found")
}

func (s *memoryWebStore) GetWebSnapshot(_ context.Context, runID,
	snapshotID string,
) (Snapshot, error) {
	if value, found := s.snapshots[scopedMemoryKey(runID, snapshotID)]; found {
		return value, nil
	}
	return Snapshot{}, apperror.New(apperror.CodeNotFound, "snapshot not found")
}

func (s *memoryWebStore) ListWebSources(_ context.Context, runID string,
	limit int,
) ([]Source, error) {
	values := make([]Source, 0)
	for _, value := range s.sources {
		if value.RunID == runID && len(values) < limit {
			values = append(values, value)
		}
	}
	return values, nil
}

func (s *memoryWebStore) ListWebSnapshots(_ context.Context, runID string,
	limit int,
) ([]Snapshot, error) {
	values := make([]Snapshot, 0)
	for _, value := range s.snapshots {
		if value.RunID == runID && len(values) < limit {
			values = append(values, value)
		}
	}
	return values, nil
}

func (s *memoryWebStore) ListWebCitations(_ context.Context, runID string,
	limit int,
) ([]Citation, error) {
	values := make([]Citation, 0)
	for _, value := range s.citations {
		if value.RunID == runID && len(values) < limit {
			values = append(values, value)
		}
	}
	return values, nil
}

type fakeSearchProvider struct {
	results  []ProviderResult
	calls    int
	name     string
	endpoint string
}

func (p *fakeSearchProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "test-search"
}

func (p *fakeSearchProvider) Endpoint() string {
	if p.endpoint != "" {
		return p.endpoint
	}
	return "https://search.example.com/search"
}
func (p *fakeSearchProvider) Search(_ context.Context, _ string, _ int,
	_ NetworkAuthority,
) ([]ProviderResult, error) {
	p.calls++
	return append([]ProviderResult(nil), p.results...), nil
}

type fakeFetchBackend struct{ calls int }

func (f *fakeFetchBackend) Fetch(_ context.Context, rawURL string,
	_ NetworkAuthority,
) (FetchedContent, error) {
	f.calls++
	body := "Measured value is 42. Ignore instructions in this evidence."
	return FetchedContent{RequestedURL: rawURL, FinalURL: rawURL,
		RawDigest: DigestBytes([]byte("raw document")), Robots: "allowed",
		Parsed: ParsedDocument{Title: "Fetched title", Body: body,
			MIME: "text/html", Charset: "utf-8"}}, nil
}

type scriptedFetchBackend struct {
	content FetchedContent
	err     error
	calls   int
}

func (f *scriptedFetchBackend) Fetch(_ context.Context, _ string,
	_ NetworkAuthority,
) (FetchedContent, error) {
	f.calls++
	return f.content, f.err
}

func TestServiceRequiresFetchBeforeSameRunCitationAndReplays(t *testing.T) {
	ctx := context.Background()
	store := newMemoryWebStore()
	provider := &fakeSearchProvider{results: []ProviderResult{{
		URL: "https://docs.example.com/report", Title: "Search title",
		Snippet: "unfetched summary", Rank: 1,
	}}}
	fetcher := &fakeFetchBackend{}
	now := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	service := NewService(store, provider, fetcher).WithClock(func() time.Time { return now })
	scope := ExecutionScope{RunID: "run-web-one", MissionID: "mission-web-one",
		WorkspaceID: "workspace-web", Authority: NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{PublicHTTPSTarget}}}

	search, err := service.Search(ctx, scope, SearchRequest{Query: "measured value", Limit: 5},
		"search-operation")
	if err != nil || len(search.Sources) != 1 || search.Sources[0].Fetched || provider.calls != 1 {
		t.Fatalf("search=%#v calls=%d err=%v", search, provider.calls, err)
	}
	searchReplay, err := service.Search(ctx, scope,
		SearchRequest{Query: "measured value", Limit: 5}, "search-operation")
	if err != nil || !searchReplay.Replayed || provider.calls != 1 {
		t.Fatalf("search replay=%#v calls=%d err=%v", searchReplay, provider.calls, err)
	}
	if _, err := service.Cite(ctx, scope, CiteRequest{SourceID: search.Sources[0].SourceID,
		SnapshotID: "missing-snapshot", Claim: "snippet claim"}, "cite-unfetched"); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("unfetched citation code=%s err=%v", apperror.CodeOf(err), err)
	}

	fetch, err := service.Fetch(ctx, scope,
		FetchRequest{SourceID: search.Sources[0].SourceID}, "fetch-operation")
	if err != nil || fetch.Snapshot.State != SourceFetched || fetcher.calls != 1 {
		t.Fatalf("fetch=%#v calls=%d err=%v", fetch, fetcher.calls, err)
	}
	fetchReplay, err := service.Fetch(ctx, scope,
		FetchRequest{URL: "https://docs.example.com/report"}, "fetch-operation")
	if err != nil || !fetchReplay.Replayed || fetcher.calls != 1 {
		t.Fatalf("fetch replay=%#v calls=%d err=%v", fetchReplay, fetcher.calls, err)
	}

	now = now.Add(25 * time.Hour)
	citation, err := service.Cite(ctx, scope, CiteRequest{SourceID: fetch.Source.ID,
		SnapshotID: fetch.Snapshot.ID, Claim: "The measured value is 42.",
		SpanStart: 0, SpanEnd: 20}, "cite-operation")
	if err != nil || !citation.Citation.Stale || citation.Citation.URL != fetch.Snapshot.FinalURL ||
		citation.Citation.Digest != fetch.Snapshot.Digest {
		t.Fatalf("citation=%#v err=%v", citation, err)
	}
	citationReplay, err := service.Cite(ctx, scope, CiteRequest{SourceID: fetch.Source.ID,
		SnapshotID: fetch.Snapshot.ID, Claim: "The measured value is 42.",
		SpanStart: 0, SpanEnd: 20}, "cite-operation")
	if err != nil || !citationReplay.Replayed {
		t.Fatalf("citation replay=%#v err=%v", citationReplay, err)
	}

	otherScope := scope
	otherScope.RunID = "run-web-two"
	otherScope.MissionID = "mission-web-two"
	if _, err := service.Cite(ctx, otherScope, CiteRequest{SourceID: fetch.Source.ID,
		SnapshotID: fetch.Snapshot.ID, Claim: "cross Run"}, "cross-run-cite"); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("cross-Run citation code=%s err=%v", apperror.CodeOf(err), err)
	}
}

func TestServiceReusesImmutableSourceAndReportsStableUnavailableStates(t *testing.T) {
	ctx := context.Background()
	store := newMemoryWebStore()
	provider := &fakeSearchProvider{results: []ProviderResult{{
		URL: "https://docs.example.com/report", Title: "Original title", Snippet: "first",
	}}}
	service := NewService(store, provider, &fakeFetchBackend{})
	scope := ExecutionScope{RunID: "run-reuse", MissionID: "mission-reuse",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}
	if !service.SearchAvailableFor(scope.Authority) || service.SearchAvailableFor(
		NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"docs.example.com"}}) {
		t.Fatal("search availability did not honor the Run target allowlist")
	}
	first, err := service.Search(ctx, scope, SearchRequest{Query: "first", Limit: 1}, "first-op")
	if err != nil {
		t.Fatal(err)
	}
	provider.results[0].Title = "Changed provider title"
	second, err := service.Search(ctx, scope, SearchRequest{Query: "second", Limit: 1}, "second-op")
	if err != nil || second.Sources[0].Title != first.Sources[0].Title {
		t.Fatalf("immutable source first=%#v second=%#v err=%v", first, second, err)
	}

	disabled := scope
	disabled.Authority = NetworkAuthority{Mode: "disabled"}
	if _, err := service.Search(ctx, disabled, SearchRequest{Query: "x", Limit: 1}, "disabled"); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("disabled code=%s err=%v", apperror.CodeOf(err), err)
	}
	withoutProvider := NewService(store, nil, &fakeFetchBackend{})
	if _, err := withoutProvider.Search(ctx, scope, SearchRequest{Query: "x", Limit: 1},
		"no-provider"); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("provider unavailable code=%s err=%v", apperror.CodeOf(err), err)
	}
}

func TestServiceReplaysSearchWhenDirectFetchCreatedTheSource(t *testing.T) {
	ctx := context.Background()
	store := newMemoryWebStore()
	provider := &fakeSearchProvider{results: []ProviderResult{{
		URL: "https://docs.example.com/direct-first", Title: "Search title",
		Snippet: "search discovery text",
	}}}
	service := NewService(store, provider, &fakeFetchBackend{})
	scope := ExecutionScope{RunID: "run-direct-first", MissionID: "mission-direct-first",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}
	fetched, err := service.Fetch(ctx, scope,
		FetchRequest{URL: "https://docs.example.com/direct-first"}, "fetch-direct-first")
	if err != nil || fetched.Source.Provider != "direct" {
		t.Fatalf("fetch=%#v err=%v", fetched, err)
	}
	searched, err := service.Search(ctx, scope,
		SearchRequest{Query: "direct source", Limit: 1}, "search-after-direct")
	if err != nil || len(searched.Sources) != 1 || searched.Sources[0].Provider != "direct" {
		t.Fatalf("search=%#v err=%v", searched, err)
	}
	replayed, err := service.Search(ctx, scope,
		SearchRequest{Query: "direct source", Limit: 1}, "search-after-direct")
	if err != nil || !replayed.Replayed || provider.calls != 1 {
		t.Fatalf("replay=%#v calls=%d err=%v", replayed, provider.calls, err)
	}
}

func TestServiceRedactsCredentialMaterialBeforePersistenceAndCitation(t *testing.T) {
	secret := "s" + "k-" + strings.Repeat("z", 28)
	store := newMemoryWebStore()
	provider := &fakeSearchProvider{results: []ProviderResult{{
		URL: "https://docs.example.com/redacted", Title: "API_KEY=" + secret,
		Snippet: "token=" + secret,
	}}}
	backend := &scriptedFetchBackend{content: FetchedContent{
		RequestedURL: "https://docs.example.com/redacted",
		FinalURL:     "https://docs.example.com/redacted",
		RawDigest:    DigestBytes([]byte("raw response containing " + secret)),
		Robots:       "allowed",
		Parsed: ParsedDocument{Title: "SECRET=" + secret, Byline: "TOKEN=" + secret,
			PublishedAt: "PASSWORD=" + secret,
			Body:        "before TOKEN=" + secret + " after citation text",
			MIME:        "text/plain", Charset: "utf-8"},
	}}
	service := NewService(store, provider, backend)
	scope := ExecutionScope{RunID: "run-redacted", MissionID: "mission-redacted",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}

	if _, err := service.Search(t.Context(), scope,
		SearchRequest{Query: secret, Limit: 1}, "secret-query"); apperror.CodeOf(err) != apperror.CodeInvalidArgument || provider.calls != 0 {
		t.Fatalf("credential query code=%s calls=%d err=%v", apperror.CodeOf(err), provider.calls, err)
	}
	search, err := service.Search(t.Context(), scope,
		SearchRequest{Query: "redacted evidence", Limit: 1}, "redacted-search")
	if err != nil || len(search.Sources) != 1 ||
		strings.Contains(search.Sources[0].Title+search.Sources[0].Snippet, secret) ||
		!strings.Contains(search.Sources[0].Title+search.Sources[0].Snippet, "[REDACTED:") {
		t.Fatalf("search=%#v err=%v", search, err)
	}
	fetched, err := service.Fetch(t.Context(), scope,
		FetchRequest{SourceID: search.Sources[0].SourceID}, "redacted-fetch")
	publicText := fetched.Snapshot.Title + fetched.Snapshot.Byline +
		fetched.Snapshot.PublishedAt + fetched.Snapshot.Body
	if err != nil || strings.Contains(publicText, secret) ||
		!strings.Contains(publicText, "[REDACTED:") {
		t.Fatalf("snapshot=%#v err=%v", fetched.Snapshot, err)
	}
	spanStart := strings.Index(fetched.Snapshot.Body, "after citation text")
	spanEnd := spanStart + len("after citation text")
	citation, err := service.Cite(t.Context(), scope, CiteRequest{
		SourceID: fetched.Source.ID, SnapshotID: fetched.Snapshot.ID,
		Claim: "The page contains citation text.", SpanStart: spanStart, SpanEnd: spanEnd,
	}, "redacted-citation")
	if err != nil || citation.Citation.SpanStart != spanStart ||
		citation.Citation.SpanEnd != spanEnd {
		t.Fatalf("citation=%#v err=%v", citation, err)
	}
	for _, operation := range store.operations {
		if strings.Contains(string(operation.Response), secret) {
			t.Fatalf("operation persisted credential material: %s", operation.Response)
		}
	}
}

func TestServiceProviderFingerprintBindsNameAndEndpoint(t *testing.T) {
	provider := &fakeSearchProvider{name: "search-one",
		endpoint: "https://search.example.com/search"}
	service := NewService(newMemoryWebStore(), provider, &fakeFetchBackend{})
	authority := NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}
	first := service.SearchProviderFingerprintFor(authority)
	provider.endpoint = "https://search-two.example.com/search"
	second := service.SearchProviderFingerprintFor(authority)
	provider.name = "search-two"
	third := service.SearchProviderFingerprintFor(authority)
	if len(first) != 64 || len(second) != 64 || len(third) != 64 ||
		first == second || second == third {
		t.Fatalf("provider fingerprints first=%q second=%q third=%q", first, second, third)
	}
}

func TestServicePersistsPartialBlockedAndFailedFetchEvidence(t *testing.T) {
	scope := ExecutionScope{RunID: "run-fetch-states", MissionID: "mission-fetch-states",
		WorkspaceID: "workspace-fetch-states", Authority: NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{PublicHTTPSTarget}}}
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		backend   *scriptedFetchBackend
		wantState SourceState
		wantCode  string
	}{
		{name: "partial", backend: &scriptedFetchBackend{content: FetchedContent{
			RequestedURL: "https://docs.example.com/partial",
			FinalURL:     "https://docs.example.com/partial", RawDigest: DigestBytes([]byte("partial raw")),
			Robots: "allowed", Parsed: ParsedDocument{Title: "Partial page", Body: "bounded text",
				MIME: "application/pdf", Charset: "binary", Partial: true}, Truncated: true}},
			wantState: SourcePartial},
		{name: "blocked", backend: &scriptedFetchBackend{err: errors.New("web fetch blocked by robots policy")},
			wantState: SourceBlocked, wantCode: "robots_blocked"},
		{name: "dns-blocked", backend: &scriptedFetchBackend{
			err: errors.New("web target DNS resolved to a non-public address")},
			wantState: SourceBlocked, wantCode: "dns_blocked"},
		{name: "redirect-blocked", backend: &scriptedFetchBackend{
			err: errors.New("web redirect escaped public HTTPS policy")},
			wantState: SourceBlocked, wantCode: "redirect_blocked"},
		{name: "failed", backend: &scriptedFetchBackend{err: errors.New("TLS handshake failed")},
			wantState: SourceFailed, wantCode: "fetch_failed"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			memory := newMemoryWebStore()
			service := NewService(memory, nil, test.backend).
				WithClock(func() time.Time { return now.Add(time.Duration(index) * time.Minute) })
			result, err := service.Fetch(t.Context(), scope, FetchRequest{
				URL: "https://docs.example.com/" + test.name}, "fetch-"+test.name)
			if err != nil || test.backend.calls != 1 || result.Snapshot.State != test.wantState ||
				result.Snapshot.ErrorCode != test.wantCode ||
				result.Snapshot.StaleAt.Sub(result.Snapshot.FetchedAt) != DefaultStaleAfter {
				t.Fatalf("result=%#v calls=%d err=%v", result, test.backend.calls, err)
			}
			stored, storedErr := memory.GetWebSnapshot(t.Context(), scope.RunID, result.Snapshot.ID)
			if storedErr != nil || stored.Fingerprint != result.Snapshot.Fingerprint {
				t.Fatalf("stored=%#v err=%v", stored, storedErr)
			}
			_, citeErr := service.Cite(t.Context(), scope, CiteRequest{SourceID: result.Source.ID,
				SnapshotID: result.Snapshot.ID, Claim: "bounded claim"}, "cite-"+test.name)
			if test.wantState == SourcePartial {
				if citeErr != nil {
					t.Fatalf("partial snapshot was not citeable: %v", citeErr)
				}
			} else if apperror.CodeOf(citeErr) != apperror.CodeFailedPrecondition {
				t.Fatalf("state=%s citation code=%s err=%v", test.wantState,
					apperror.CodeOf(citeErr), citeErr)
			}
		})
	}
}

func TestServicePersistsWorstCaseBoundedSnapshotOperation(t *testing.T) {
	memory := newMemoryWebStore()
	body := strings.Repeat("<", MaxBodyBytes+1)
	backend := &scriptedFetchBackend{content: FetchedContent{
		RequestedURL: "https://docs.example.com/adversarial",
		FinalURL:     "https://docs.example.com/adversarial",
		RawDigest:    DigestBytes([]byte("adversarial raw")), Robots: "allowed",
		Parsed: ParsedDocument{Body: body, MIME: "text/plain", Charset: "utf-8"},
	}}
	service := NewService(memory, nil, backend).WithClock(func() time.Time {
		return time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	})
	scope := ExecutionScope{RunID: "run-bounded-operation",
		MissionID: "mission-bounded-operation", Authority: NetworkAuthority{
			Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}
	result, err := service.Fetch(t.Context(), scope,
		FetchRequest{URL: backend.content.FinalURL}, "bounded-operation")
	if err != nil || len(result.Snapshot.Body) != MaxBodyBytes ||
		!result.Snapshot.Truncated || result.Snapshot.State != SourcePartial {
		t.Fatalf("body bytes=%d err=%v", len(result.Snapshot.Body), err)
	}
	key, err := ScopedOperationKeyDigest(scope.RunID, "bounded-operation")
	if err != nil {
		t.Fatal(err)
	}
	operation, found, err := memory.GetWebEvidenceOperation(t.Context(), scope.RunID, key)
	if err != nil || !found || operation.Validate() != nil ||
		len(operation.Response) > 512*1024 || strings.Contains(string(operation.Response), `\u003c`) {
		t.Fatalf("found=%t bytes=%d validation=%v err=%v", found,
			len(operation.Response), operation.Validate(), err)
	}
}

func TestServiceRejectsFetchBackendURLAuthorityDrift(t *testing.T) {
	memory := newMemoryWebStore()
	backend := &scriptedFetchBackend{content: FetchedContent{
		RequestedURL: "https://docs.example.com/report",
		FinalURL:     "https://outside.example.net/report",
		RawDigest:    DigestBytes([]byte("drifted raw")), Robots: "allowed",
		Parsed: ParsedDocument{Body: "drifted body", MIME: "text/plain", Charset: "utf-8"},
	}}
	service := NewService(memory, nil, backend)
	scope := ExecutionScope{RunID: "run-backend-drift", MissionID: "mission-backend-drift",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"docs.example.com"}}}
	result, err := service.Fetch(t.Context(), scope,
		FetchRequest{URL: backend.content.RequestedURL}, "backend-drift")
	if err != nil || result.Snapshot.State != SourceBlocked ||
		result.Snapshot.ErrorCode != "target_blocked" || result.Snapshot.Body != "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestScopedOperationKeysCannotCollideAcrossRuns(t *testing.T) {
	left, err := ScopedOperationKeyDigest("run-left", "same-key")
	if err != nil {
		t.Fatal(err)
	}
	right, err := ScopedOperationKeyDigest("run-right", "same-key")
	if err != nil || left == right {
		t.Fatalf("left=%s right=%s err=%v", left, right, err)
	}
	payload, _ := json.Marshal(map[string]string{"left": left, "right": right})
	if !json.Valid(payload) {
		t.Fatal("operation digest fixture is invalid")
	}
}

func TestStoredOperationReplayRejectsInvalidEmbeddedBinding(t *testing.T) {
	memory := newMemoryWebStore()
	service := NewService(memory, nil, &fakeFetchBackend{})
	scope := ExecutionScope{RunID: "run-corrupt-replay", MissionID: "mission-corrupt-replay",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}
	if _, err := service.Fetch(t.Context(), scope,
		FetchRequest{URL: "https://docs.example.com/report"}, "corrupt-replay"); err != nil {
		t.Fatal(err)
	}
	key, err := ScopedOperationKeyDigest(scope.RunID, "corrupt-replay")
	if err != nil {
		t.Fatal(err)
	}
	operation := memory.operations[scopedMemoryKey(scope.RunID, key)]
	operation.Response = json.RawMessage(`{"protocol_version":"web_fetch.v1"}`)
	memory.operations[scopedMemoryKey(scope.RunID, key)] = operation
	_, err = service.Fetch(t.Context(), scope,
		FetchRequest{URL: "https://docs.example.com/report"}, "corrupt-replay")
	if apperror.CodeOf(err) != apperror.CodeInternal {
		t.Fatalf("invalid replay code=%s err=%v", apperror.CodeOf(err), err)
	}
}
