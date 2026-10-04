package webevidence

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

type preflightWebStore struct {
	*memoryWebStore
	afterReplayRead func()
}

func (s *preflightWebStore) GetWebEvidenceOperation(ctx context.Context, runID, key string) (Operation, bool, error) {
	operation, found, err := s.memoryWebStore.GetWebEvidenceOperation(ctx, runID, key)
	if s.afterReplayRead != nil {
		s.afterReplayRead()
	}
	return operation, found, err
}

type preflightSearchProvider struct{ *fakeSearchProvider }

func (p *preflightSearchProvider) SearchFiltered(ctx context.Context, request SearchRequest, authority NetworkAuthority) ([]ProviderResult, error) {
	return p.Search(ctx, request.Query, request.Limit, authority)
}

func TestServiceNetworkPreflightGuardsDispatchAndPreservesReplay(t *testing.T) {
	for _, operation := range []string{"search", "filtered-search", "source-search", "fetch", "connector-fetch"} {
		t.Run(operation, func(t *testing.T) {
			state := &preflightWebStore{memoryWebStore: newMemoryWebStore()}
			provider := &preflightSearchProvider{fakeSearchProvider: &fakeSearchProvider{}}
			fetcher := &fakeFetchBackend{}
			url := "https://github.com/example/project/issues/1"
			github := &fakeSourceConnector{name: "github", version: "github-test.v1",
				endpoint: "https://api.github.com/search/issues",
				document: ConnectorDocument{CanonicalURL: url, RawDigest: DigestBytes([]byte("raw")),
					HTTPStatus: 200, MIME: "text/markdown", Body: "saved connector evidence"}}
			hackerNews := &fakeSourceConnector{name: "hacker_news", version: "hn-test.v1",
				endpoint: "https://hn.algolia.com/api/v1/search"}
			original := NewService(state, provider, fetcher).WithSourceConnectors(github, hackerNews)
			scope := bindSearchProvider(t, original, ExecutionScope{RunID: "run-preflight",
				MissionID: "mission-preflight", Authority: NetworkAuthority{Mode: "allowlist",
					AllowedTargets: []string{PublicHTTPSTarget}}})
			scope.ConnectorFingerprint = original.SourceConnectorFingerprintFor(scope.Authority)
			var live atomic.Bool
			var checks atomic.Int32
			live.Store(true)
			denied := apperror.New(apperror.CodePolicyDenied, "runtime permission was revoked")
			bound := original.WithNetworkPreflight(func(context.Context) error {
				checks.Add(1)
				if !live.Load() {
					return denied
				}
				return nil
			})
			invoke := func(service *Service, key string) (bool, error) {
				switch operation {
				case "search", "filtered-search":
					request := SearchRequest{Query: "evidence", Limit: 1}
					if operation == "filtered-search" {
						request.AllowedDomains = []string{"example.com"}
					}
					result, err := service.Search(t.Context(), scope, request, key)
					return result.Replayed, err
				case "source-search":
					result, err := service.SourceSearch(t.Context(), scope,
						SourceSearchRequest{Connectors: []string{"github", "hacker_news"}, Query: "evidence", Limit: 2}, key)
					return result.Replayed, err
				default:
					request := FetchRequest{URL: "https://docs.example.com/page"}
					if operation == "connector-fetch" {
						request.URL = url
					}
					result, err := service.Fetch(t.Context(), scope, request, key)
					return result.Replayed, err
				}
			}
			calls := func() int {
				return provider.calls + fetcher.calls + github.searchCalls + github.readCalls + hackerNews.searchCalls
			}
			wantCalls := 1
			if operation == "source-search" {
				wantCalls = 2
			}

			// Revoke during the service's durable lookup, after its caller has
			// already validated and projected authority.
			state.afterReplayRead = func() { live.Store(false) }
			if _, err := invoke(bound, "preflight-operation"); !errors.Is(err, denied) || calls() != 0 || len(state.operations) != 0 {
				t.Fatalf("revoked dispatch: calls=%d operations=%d err=%v", calls(), len(state.operations), err)
			}
			state.afterReplayRead = nil
			live.Store(true)
			checks.Store(0)
			if replayed, err := invoke(bound, "preflight-operation"); err != nil || replayed || calls() != wantCalls || checks.Load() != int32(wantCalls) {
				t.Fatalf("live dispatch: replay=%t calls=%d checks=%d err=%v", replayed, calls(), checks.Load(), err)
			}
			live.Store(false)
			if replayed, err := invoke(bound, "preflight-operation"); err != nil || !replayed || calls() != wantCalls || checks.Load() != int32(wantCalls) {
				t.Fatalf("saved replay: replay=%t calls=%d checks=%d err=%v", replayed, calls(), checks.Load(), err)
			}
			// Binding a request cannot install its callback on the shared service.
			// Nil retains the existing semantics for other permission paths.
			for _, service := range []*Service{original, bound.WithNetworkPreflight(nil)} {
				key := "original-operation"
				if service != original {
					key = "nil-preflight-operation"
				}
				before := calls()
				if replayed, err := invoke(service, key); err != nil || replayed || calls() != before+wantCalls || checks.Load() != int32(wantCalls) {
					t.Fatalf("original semantics: replay=%t calls=%d checks=%d err=%v", replayed, calls(), checks.Load(), err)
				}
			}
		})
	}
}

type preflightSearchResolver struct {
	*fakeSearchResolver
	afterResolve func()
}

func (r *preflightSearchResolver) ResolveSearch(ctx context.Context, route SearchRoute, authority NetworkAuthority) (SearchSelection, error) {
	selection, err := r.fakeSearchResolver.ResolveSearch(ctx, route, authority)
	if r.afterResolve != nil {
		r.afterResolve()
	}
	return selection, err
}

func TestServiceSearchPreflightRunsAfterFinalProviderResolution(t *testing.T) {
	provider := &fakeSearchProvider{}
	resolver := &preflightSearchResolver{fakeSearchResolver: &fakeSearchResolver{
		selection: SearchSelection{Policy: SearchPolicySearXNG, Backend: provider.Name(),
			SelectionReason: "configured_search_provider", Provider: provider}}}
	service := NewService(newMemoryWebStore(), nil, &fakeFetchBackend{}).WithSearchProviderResolver(resolver)
	scope := bindSearchProvider(t, service, ExecutionScope{RunID: "run-resolve-preflight", MissionID: "mission-resolve-preflight",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}})
	live := true
	resolver.afterResolve = func() { live = false }
	denied := apperror.New(apperror.CodePolicyDenied, "permission changed during provider resolution")
	bound := service.WithNetworkPreflight(func(context.Context) error {
		if !live {
			return denied
		}
		return nil
	})
	if _, err := bound.Search(t.Context(), scope, SearchRequest{Query: "evidence", Limit: 1}, "resolve-preflight"); !errors.Is(err, denied) || provider.calls != 0 {
		t.Fatalf("provider resolution escaped preflight: calls=%d err=%v", provider.calls, err)
	}
}
