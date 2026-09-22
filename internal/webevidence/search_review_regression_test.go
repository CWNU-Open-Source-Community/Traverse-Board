package webevidence

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAnthropicReviewRejectsNullResultsAndLateCredentialRotation(t *testing.T) {
	for _, test := range []struct {
		name, content, reason string
		rotate                bool
	}{
		{"null", `null`, NativeSearchReasonResponseInvalid, false},
		{"empty", `[]`, "", false},
		{"rotation", `[]`, NativeSearchReasonConfigurationChanged, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newResponsesSearchRuntimeStub("initial-test-credential")
			calls := 0
			client := responsesSearchTestClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				if test.rotate {
					runtime.rotate("changed-test-credential")
				}
				return webResponse(200, http.Header{"Content-Type": {"application/json"}}, `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"release"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":`+test.content+`}]}`), nil
			})
			provider, err := NewAnthropicSearchProvider(client, "https://api.vendor.com/v1", "anthropic", "claude", runtime)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Search(t.Context(), "release", 1, responsesSearchAuthority())
			var native *NativeSearchQualificationError
			if test.reason == "" {
				if err != nil || provider.QualificationSnapshot(t.Context(), responsesSearchAuthority()).Status != SearchQualificationReady {
					t.Fatal("legitimate zero results failed", err)
				}
			} else if !errors.As(err, &native) || native.Reason != test.reason || provider.QualificationSnapshot(t.Context(), responsesSearchAuthority()).Status == SearchQualificationReady {
				t.Fatalf("accepted invalid or obsolete result: err=%v qualification=%+v", err, provider.QualificationSnapshot(t.Context(), responsesSearchAuthority()))
			}
			if calls != 1 {
				t.Fatalf("unexpected calls=%d", calls)
			}
		})
	}
}

func TestNativeReviewReplaysCurrentTitleWithoutChangingOlderSource(t *testing.T) {
	state := newMemoryWebStore()
	const canonical = "https://docs.example.net/grounded"
	provider := &fakeGroundedSearchProvider{fakeSearchProvider: &fakeSearchProvider{name: "native-test", endpoint: "https://api.provider.com/search",
		results: []ProviderResult{{URL: canonical, Title: "Current title", Snippet: "Current result"}}}}
	resolver := &fakeSearchResolver{selection: SearchSelection{Policy: SearchPolicyProviderNative, Backend: provider.Name(), SelectionReason: "declared_provider_native",
		Binding: strings.Repeat("8", 64), Provider: provider, ProviderAuthority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"api.provider.com"}}, ProviderAuthorityIndependent: true}}
	service := NewService(state, nil, &fakeFetchBackend{}).WithSearchProviderResolver(resolver)
	scope := bindSearchProvider(t, service, ExecutionScope{RunID: "run-older-native", MissionID: "mission-older-native", WorkspaceID: "workspace-older-native", ModelRoute: "provider/model", Authority: NetworkAuthority{Mode: "disabled"}})
	older, err := SealSource(Source{ID: StableSourceID(scope.RunID, canonical), RunID: scope.RunID, MissionID: scope.MissionID, WorkspaceID: scope.WorkspaceID,
		CanonicalURL: canonical, Title: "Older immutable title", Provider: "direct", State: SourceDiscovered, DiscoveredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	state.sources[scopedMemoryKey(scope.RunID, older.ID)] = older
	request := SearchRequest{Query: "grounded report", Limit: 1}
	first, err := service.Search(t.Context(), scope, request, "native-current-title")
	if err != nil || len(first.Sources) != 1 || first.Sources[0].Title != "Current title" {
		t.Fatal("initial native search", err)
	}
	replayed, err := service.Search(t.Context(), scope, request, "native-current-title")
	if err != nil || !replayed.Replayed || provider.calls != 1 || replayed.Sources[0].ProviderGroundedCitation.Title != "Current title" {
		t.Fatal("native replay lost current citation title", err)
	}
	if state.sources[scopedMemoryKey(scope.RunID, older.ID)] != older {
		t.Fatal("native search changed older Source")
	}
}
