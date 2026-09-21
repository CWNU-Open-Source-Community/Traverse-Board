package webevidence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
)

type fakeSourceConnector struct {
	name, version, endpoint string
	items                   []ConnectorSearchItem
	document                ConnectorDocument
	searchErr, readErr      error
	searchCalls, readCalls  int
}

func (c *fakeSourceConnector) Name() string           { return c.name }
func (c *fakeSourceConnector) Version() string        { return c.version }
func (c *fakeSourceConnector) SearchEndpoint() string { return c.endpoint }
func (c *fakeSourceConnector) MatchURL(rawURL string) bool {
	return rawURL == c.document.CanonicalURL
}
func (c *fakeSourceConnector) Search(context.Context, string, int,
	NetworkAuthority,
) ([]ConnectorSearchItem, error) {
	c.searchCalls++
	return append([]ConnectorSearchItem(nil), c.items...), c.searchErr
}
func (c *fakeSourceConnector) Read(context.Context, string, int,
	NetworkAuthority,
) (ConnectorDocument, error) {
	c.readCalls++
	return c.document, c.readErr
}

func TestSourceSearchFetchCitationAreDurableAndReplayWithoutNetwork(t *testing.T) {
	githubURL := "https://github.com/OWWZO/ai-agent/issues/1"
	github := &fakeSourceConnector{name: "github", version: "github-test.v1",
		endpoint: "https://api.github.com/search/issues",
		items: []ConnectorSearchItem{{URL: githubURL, Title: "Search issue",
			Snippet: "body and comments"}},
		document: ConnectorDocument{CanonicalURL: githubURL,
			RequestEndpoints: []string{"https://api.github.com/repos/OWWZO/ai-agent/issues/1"},
			HTTPStatus:       200, RawDigest: DigestBytes([]byte("raw GitHub API response")),
			Title: "Search issue", Byline: "owner", MIME: "text/markdown", Charset: "utf-8",
			Body:        "# Search issue\n\n## Body\nUseful detail\n\n## Comments\nMore detail",
			ContentKind: "github_issue_thread", Coverage: "body_and_comments",
			ItemsIncluded: 2, ItemsAvailable: 3, Truncated: true,
			TruncationReason: "comment_limit"}}
	hackerNews := &fakeSourceConnector{name: "hacker_news", version: "hn-test.v1",
		endpoint:  "https://hn.algolia.com/api/v1/search",
		searchErr: newConnectorError("rate_limited", errors.New("HTTP 429"))}
	store := newMemoryWebStore()
	service := NewService(store, nil, &fakeFetchBackend{}).
		WithSourceConnectors(github, hackerNews)
	now := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	scope := ExecutionScope{RunID: "run-source-search", MissionID: "mission-source-search",
		WorkspaceID: "workspace-source-search",
		Authority:   NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}
	scope.ConnectorFingerprint = service.SourceConnectorFingerprintFor(scope.Authority)
	if !validDigest(scope.ConnectorFingerprint) {
		t.Fatalf("connector fingerprint=%q", scope.ConnectorFingerprint)
	}

	request := SourceSearchRequest{Connectors: []string{"auto"}, Query: "agent search", Limit: 5}
	result, err := service.SourceSearch(t.Context(), scope, request, "source-search-operation")
	if err != nil || !result.Partial || result.Replayed || len(result.Sources) != 1 ||
		len(result.Failures) != 1 || result.Failures[0].Connector != "hacker_news" ||
		result.Failures[0].Code != "rate_limited" || result.Sources[0].CanonicalURL != githubURL ||
		result.Sources[0].Citeable || !result.Sources[0].Untrusted {
		t.Fatalf("source search=%#v err=%v", result, err)
	}
	if github.searchCalls != 1 || hackerNews.searchCalls != 1 {
		t.Fatalf("initial connector calls github=%d hn=%d", github.searchCalls, hackerNews.searchCalls)
	}
	replayed, err := service.SourceSearch(t.Context(), scope, request, "source-search-operation")
	if err != nil || !replayed.Replayed || replayed.SearchedAt != result.SearchedAt ||
		github.searchCalls != 1 || hackerNews.searchCalls != 1 {
		t.Fatalf("replayed source search=%#v calls=%d/%d err=%v",
			replayed, github.searchCalls, hackerNews.searchCalls, err)
	}
	changed := request
	changed.Query = "different query"
	if _, err := service.SourceSearch(t.Context(), scope, changed,
		"source-search-operation"); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation key conflict code=%s err=%v", apperror.CodeOf(err), err)
	}

	now = now.Add(time.Minute)
	fetched, err := service.Fetch(t.Context(), scope,
		FetchRequest{SourceID: result.Sources[0].SourceID, MaxItems: 2}, "source-fetch-operation")
	if err != nil || fetched.Replayed || fetched.Snapshot.State != SourcePartial ||
		fetched.Snapshot.Connector != "github" || fetched.Snapshot.Coverage != "body_and_comments" ||
		fetched.Snapshot.RawDigest != github.document.RawDigest ||
		fetched.Snapshot.TruncationReason != "comment_limit" ||
		!strings.Contains(fetched.Snapshot.Body, "More detail") || github.readCalls != 1 {
		t.Fatalf("connector fetch=%#v calls=%d err=%v", fetched, github.readCalls, err)
	}
	fetchReplay, err := service.Fetch(t.Context(), scope,
		FetchRequest{SourceID: result.Sources[0].SourceID, MaxItems: 2}, "source-fetch-operation")
	if err != nil || !fetchReplay.Replayed || github.readCalls != 1 {
		t.Fatalf("connector fetch replay=%#v calls=%d err=%v", fetchReplay, github.readCalls, err)
	}

	now = now.Add(time.Minute)
	citation, err := service.Cite(t.Context(), scope, CiteRequest{
		SourceID: fetched.Source.ID, SnapshotID: fetched.Snapshot.ID,
		Claim: "The issue body and comments contain useful detail."}, "source-cite-operation")
	if err != nil || citation.Replayed || !citation.Citation.Partial ||
		citation.Citation.URL != githubURL || citation.Citation.Digest != fetched.Snapshot.Digest {
		t.Fatalf("connector citation=%#v err=%v", citation, err)
	}
}

func TestSourceSearchRequiresExactConnectorCapabilityFingerprint(t *testing.T) {
	connector := &fakeSourceConnector{name: "github", version: "github-test.v1",
		endpoint: "https://api.github.com/search/issues"}
	service := NewService(newMemoryWebStore(), nil, &fakeFetchBackend{}).
		WithSourceConnectors(connector)
	scope := ExecutionScope{RunID: "run-source-fence", MissionID: "mission-source-fence",
		WorkspaceID:          "workspace-source-fence",
		Authority:            NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}},
		ConnectorFingerprint: strings.Repeat("0", 64)}
	_, err := service.SourceSearch(t.Context(), scope,
		SourceSearchRequest{Connectors: []string{"github"}, Query: "query", Limit: 1}, "operation")
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition || connector.searchCalls != 0 {
		t.Fatalf("stale fingerprint reached connector: calls=%d code=%s err=%v",
			connector.searchCalls, apperror.CodeOf(err), err)
	}
}

func TestSourceSearchContinuesAfterDuplicateOrUnauthorizedRank(t *testing.T) {
	first := "https://github.com/example/project/issues/1"
	last := "https://github.com/example/project/issues/2"
	connector := &fakeSourceConnector{name: "github", version: "github-test.v1",
		endpoint: "https://api.github.com/search/issues", items: []ConnectorSearchItem{
			{URL: first}, {URL: first}, {URL: "https://outside.example.com/result"}, {URL: last},
		}}
	service := NewService(newMemoryWebStore(), nil, &fakeFetchBackend{}).
		WithSourceConnectors(connector)
	scope := ExecutionScope{RunID: "run-ranked-sources", MissionID: "mission-ranked-sources",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"api.github.com", "github.com"}}}
	scope.ConnectorFingerprint = service.SourceConnectorFingerprintFor(scope.Authority)
	result, err := service.SourceSearch(t.Context(), scope,
		SourceSearchRequest{Connectors: []string{"github"}, Query: "query", Limit: 4}, "ranked-sources")
	if err != nil || len(result.Sources) != 2 || result.Sources[1].CanonicalURL != last {
		t.Fatalf("later valid source was lost: result=%+v err=%v", result, err)
	}
}
