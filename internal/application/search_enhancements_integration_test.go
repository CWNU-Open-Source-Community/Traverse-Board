package application_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/webevidence"
)

type enhancedSearchSettings map[string]string

func (s enhancedSearchSettings) GetProviderSetting(_ context.Context, key string) (string, bool, error) {
	value, ok := s[key]
	return value, ok, nil
}

type enhancedSearchDNS struct{}

func (enhancedSearchDNS) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type enhancedSearchTransport func(*http.Request) (*http.Response, error)

func (f enhancedSearchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type enhancedQuestionBackend struct {
	calls int
	body  string
}

func (f *enhancedQuestionBackend) Fetch(_ context.Context, rawURL string, authority webevidence.NetworkAuthority, _ webevidence.RobotsPolicy) (webevidence.FetchedContent, error) {
	f.calls++
	return webevidence.FetchedContent{RequestedURL: rawURL, FinalURL: rawURL, HTTPStatus: 200, Robots: "allowed",
		RawDigest: webevidence.DigestBytes([]byte("raw report")), Parsed: webevidence.ParsedDocument{Title: "Original report", Body: f.body, MIME: "text/html", Charset: "utf-8"}}, nil
}

func TestThreadAnthropicSearchAndQuestionReachActualModelRequest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "enhanced-search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("search-model-id", "web_search", `{"version":"web_search.v1","query":"release date","limit":2,"allowed_domains":["docs.example.com"]}`),
		toolResponse("fetch-model-id", "web_fetch", `{"version":"web_fetch.v1","url":"https://docs.example.com/report","question":"发射日期"}`),
		textResponse(rootActionResponse(domain.RootActionFinish, "已取得原文。", "本轮结束。", "")),
	}}
	definition := modelregistry.ProviderDefinition{Version: modelregistry.ProviderDefinitionVersion, ID: provider.Name(), DisplayName: "Native search fixture",
		EndpointURL: "https://api.example.com/v1", DefaultModel: "model", Models: []string{"model"}, Transport: modelregistry.ProviderTransportAnthropicMessages,
		SearchMode: modelregistry.ProviderSearchModeProviderNative, NativeWebSearchCapability: modelregistry.NativeWebSearchDeclaredUnverified,
		AdvancedConfig: json.RawMessage(`{}`), Enabled: true, Revision: 1}
	encoded, err := modelregistry.EncodeProviderDefinitionCollection(modelregistry.ProviderDefinitionCollection{Version: modelregistry.ProviderDefinitionCollectionVersion, Revision: 1, Providers: []modelregistry.ProviderDefinition{definition}})
	if err != nil {
		t.Fatal(err)
	}
	settings := enhancedSearchSettings{modelregistry.ProviderDefinitionsSettingKey: encoded, "route.code": provider.Name() + "/model"}
	credentials := credential.NewMemoryStore()
	if err := credentials.Put(t.Context(), provider.Name(), "test-only-native-search-secret"); err != nil {
		t.Fatal(err)
	}
	registry, err := modelregistry.NewFromEnvironmentWithCredentials(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	wireCalls := 0
	client := &webevidence.SafeHTTPClient{Resolver: enhancedSearchDNS{}, TransportFactory: func(host string, _ []netip.Addr) http.RoundTripper {
		return enhancedSearchTransport(func(request *http.Request) (*http.Response, error) {
			wireCalls++
			var body struct {
				Tools []struct {
					Type           string
					AllowedDomains []string `json:"allowed_domains"`
				}
			}
			if host != "api.example.com" || request.URL.Path != "/v1/messages" || json.NewDecoder(request.Body).Decode(&body) != nil ||
				len(body.Tools) != 1 || body.Tools[0].Type != "web_search_20250305" || len(body.Tools[0].AllowedDomains) != 1 || body.Tools[0].AllowedDomains[0] != "docs.example.com" {
				t.Fatal("ordinary tool request did not reach native provider with domains")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"server_tool_use","id":"srv-search","name":"web_search","input":{"query":"release date"}},{"type":"web_search_tool_result","tool_use_id":"srv-search","content":[{"type":"web_search_result","url":"https://docs.example.com/report","title":"Original report","encrypted_content":"opaque"},{"type":"web_search_result","url":"https://outside.example.net/report","title":"Other report","encrypted_content":"opaque-2"}]}]}`))}, nil
		})
	}}
	resolver, err := application.NewProviderSearchResolver(registry, settings, credentials, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	const answer = "发射日期为九月二十二日🙂"
	fetcher := &enhancedQuestionBackend{body: strings.Repeat("无关材料。", 1600) + answer + strings.Repeat("其他记录。", 500)}
	service := webevidence.NewService(st, nil, fetcher).WithSearchProviderResolver(resolver)
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Search official report and read the original date", Profile: "review", Interactive: true,
		ModelRoute: provider.Name() + "/model", NetworkMode: "allowlist", AllowedTargets: []string{"docs.example.com"}, Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()).WithWebEvidence(service))
	input := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), Content: "找到报告中的发射日期并给出原文依据。", OperationKey: "native-search-question-input", RequestedBy: "test_operator"}
	result, err := turns.Execute(t.Context(), input)
	if err != nil || result.Submission.Message.Status != domain.OperatorSteeringCommitted || wireCalls != 1 || fetcher.calls != 1 || len(provider.Requests()) != 3 {
		t.Fatalf("ordinary chain: wire=%d fetch=%d models=%d err=%v", wireCalls, fetcher.calls, len(provider.Requests()), err)
	}
	definitions, _ := json.Marshal(provider.Requests()[0].Tools)
	if !strings.Contains(string(definitions), "allowed_domains") || !strings.Contains(string(definitions), "blocked_domains") || !strings.Contains(string(definitions), "question") {
		t.Fatal("model did not receive enhanced tool schemas")
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil || len(rounds) != 2 {
		t.Fatal("missing durable search and fetch rounds", err)
	}
	for _, round := range rounds {
		for _, call := range round.Calls {
			var projected string
			for _, message := range provider.Requests()[2].Messages {
				for _, tr := range message.ToolResults {
					if tr.ToolCallID == call.CallID {
						projected = tr.Content
					}
				}
			}
			var outer struct{ Status, Stdout string }
			if json.Unmarshal([]byte(projected), &outer) != nil || outer.Status != "completed" {
				t.Fatal("actual model result not paired with durable call")
			}
			if call.ToolName == "web_search" {
				var search webevidence.SearchResult
				if json.Unmarshal([]byte(outer.Stdout), &search) != nil || search.ValidateFilters() != nil || len(search.Sources) != 1 || search.FilteredOutCount != 1 ||
					!search.HasProviderGroundedCitations() || search.Sources[0].CanonicalURL != "https://docs.example.com/report" {
					t.Fatalf("model search provenance lost: %+v", search)
				}
			} else if call.ToolName == "web_fetch" {
				var output struct {
					Extraction *webevidence.Extraction
					Snapshot   struct {
						SnapshotID string `json:"snapshot_id"`
						Body       string
						BodyOffset int `json:"body_offset"`
					}
				}
				if json.Unmarshal([]byte(outer.Stdout), &output) != nil || output.Extraction == nil || !strings.Contains(output.Snapshot.Body, answer) || output.Snapshot.BodyOffset <= 0 {
					t.Fatal("model lost relevant original passage")
				}
				snapshot, err := st.GetWebSnapshot(t.Context(), run.ID, output.Snapshot.SnapshotID)
				if err != nil || snapshot.Body != fetcher.body || output.Extraction.Validate(snapshot) != nil || output.Snapshot.Body != string([]rune(snapshot.Body)[output.Extraction.SpanStart:output.Extraction.SpanEnd]) {
					t.Fatal("model excerpt cannot recover exact snapshot", err)
				}
			}
		}
	}
	if _, err := turns.Execute(t.Context(), input); err != nil || wireCalls != 1 || fetcher.calls != 1 || len(provider.Requests()) != 3 {
		t.Fatal("input replay executed search/fetch/model again", err)
	}
	after, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil || !reflect.DeepEqual(after, rounds) {
		t.Fatal("projection or replay mutated durable tool evidence", err)
	}
}
