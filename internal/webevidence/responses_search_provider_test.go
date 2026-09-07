package webevidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type responsesSearchRuntimeStub struct {
	mu      sync.Mutex
	secret  string
	mapped  string
	binding string
	apply   func(string, http.Header, map[string]any) error
	resolve int
	mapCall int
	applied int
}

func newResponsesSearchRuntimeStub(secret string) *responsesSearchRuntimeStub {
	digest := sha256.Sum256([]byte("responses-search-runtime"))
	return &responsesSearchRuntimeStub{secret: secret, mapped: "remote-search-model",
		binding: hex.EncodeToString(digest[:])}
}

func (r *responsesSearchRuntimeStub) ResolveCredential(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolve++
	return r.secret, nil
}

func (r *responsesSearchRuntimeStub) MapModel(string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mapCall++
	return r.mapped, nil
}

func (r *responsesSearchRuntimeStub) Apply(secret string, headers http.Header,
	body map[string]any,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied++
	if r.apply != nil {
		return r.apply(secret, headers, body)
	}
	return nil
}

func (r *responsesSearchRuntimeStub) BindingDigest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.binding
}

func (r *responsesSearchRuntimeStub) rotate(secret string) {
	r.mu.Lock()
	r.secret = secret
	r.mu.Unlock()
}

func responsesSearchTestClient(t *testing.T,
	handler func(*http.Request) (*http.Response, error),
) *SafeHTTPClient {
	return responsesSearchTestClientForHost(t, "api.vendor.com", handler)
}

func responsesSearchTestClientForHost(t *testing.T, expectedHost string,
	handler func(*http.Request) (*http.Response, error),
) *SafeHTTPClient {
	t.Helper()
	return &SafeHTTPClient{
		Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		TransportFactory: func(host string, _ []netip.Addr) http.RoundTripper {
			if host != expectedHost {
				t.Fatalf("unexpected Provider host %q", host)
			}
			return roundTripFunc(handler)
		},
	}
}

func TestOpenAIResponsesSearchProviderUsesBoundedDeepSeekStructuredContract(t *testing.T) {
	const query = "OpenAI official website"
	runtime := newResponsesSearchRuntimeStub("deepseek-credential")
	requests := 0
	client := responsesSearchTestClientForHost(t, "api.deepseek.com",
		func(request *http.Request) (*http.Response, error) {
			requests++
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			text, _ := body["text"].(map[string]any)
			format, _ := text["format"].(map[string]any)
			schema, _ := format["schema"].(map[string]any)
			properties, _ := schema["properties"].(map[string]any)
			results, _ := properties["results"].(map[string]any)
			if body["input"] != "Search query: "+query ||
				body["instructions"] != nativeSearchDeepSeekInstruction ||
				body["tool_choice"] != "auto" ||
				body["max_output_tokens"] != float64(nativeSearchDeepSeekMaxTokens) ||
				format["type"] != "json_schema" || format["name"] != "web_search_results" ||
				schema["additionalProperties"] != false ||
				results["maxItems"] != float64(MaxSources) {
				t.Fatalf("DeepSeek hosted-search request shape is invalid: %#v", body)
			}
			response := `{"status":"completed","output":[` +
				`{"type":"message","status":"completed","content":[{"type":"output_text","text":"searching","annotations":[]}]},` +
				`{"type":"web_search_call","status":"completed","action":{"type":"search","queries":["OpenAI"]}},` +
				`{"type":"web_search_call","status":"failed","action":{"type":"open_page","url":"https://failed.example.net"}},` +
				`{"type":"message","status":"completed","content":[{"type":"output_text","text":"Search complete: {\"results\":[{\"url\":\"https://openai.com/\",\"title\":\"OpenAI\",\"snippet\":\"Official site\"},{\"url\":\"http://insecure.example.net/\",\"title\":\"Rejected\",\"snippet\":\"Not HTTPS\"}]} (grounded)","annotations":[]}]}` +
				`]}`
			return webResponse(http.StatusOK,
				http.Header{"Content-Type": {"application/json"}}, response), nil
		})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.deepseek.com/v1/responses", "official-deepseek", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	results, err := provider.Search(t.Context(), query, 2, NetworkAuthority{
		Mode: "allowlist", AllowedTargets: []string{"api.deepseek.com"}})
	if err != nil || requests != 1 || len(results) != 1 ||
		results[0].URL != "https://openai.com/" || results[0].Title != "OpenAI" ||
		results[0].Snippet != "Official site" {
		t.Fatalf("requests=%d results=%#v err=%v", requests, results, err)
	}
}

func TestOpenAIResponsesSearchProviderRejectsUngroundedDeepSeekStructuredOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "no completed hosted call", output: `{"type":"web_search_call","status":"failed","action":{"type":"search"}},` +
			`{"type":"message","status":"completed","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://example.com/\",\"title\":\"Example\",\"snippet\":\"Result\"}]}"}]}`},
		{name: "empty results", output: `{"type":"web_search_call","status":"completed","action":{"type":"search"}},` +
			`{"type":"message","status":"completed","content":[{"type":"output_text","text":"{\"results\":[]}"}]}`},
		{name: "missing required result field", output: `{"type":"web_search_call","status":"completed","action":{"type":"search"}},` +
			`{"type":"message","status":"completed","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://example.com/\",\"title\":\"Example\"}]}"}]}`},
		{name: "non structured message", output: `{"type":"web_search_call","status":"completed","action":{"type":"search"}},` +
			`{"type":"message","status":"completed","content":[{"type":"output_text","text":"not json"}]}`},
		{name: "citation without structured result", output: `{"type":"web_search_call","status":"completed","action":{"type":"search"}},` +
			`{"type":"message","status":"completed","content":[{"type":"output_text","text":"not json","annotations":[{"type":"url_citation","url":"https://example.com/","title":"Example"}]}]}`},
		{name: "structured message precedes hosted call", output: `{"type":"message","status":"completed","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://example.com/\",\"title\":\"Example\",\"snippet\":\"Result\"}]}"}]},` +
			`{"type":"web_search_call","status":"completed","action":{"type":"search"}},` +
			`{"type":"message","status":"completed","content":[{"type":"output_text","text":"not json"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := responsesSearchTestClientForHost(t, "api.deepseek.com",
				func(*http.Request) (*http.Response, error) {
					requests++
					return webResponse(http.StatusOK,
						http.Header{"Content-Type": {"application/json"}},
						`{"status":"completed","output":[`+test.output+`]}`), nil
				})
			provider, err := NewOpenAIResponsesSearchProvider(client,
				"https://api.deepseek.com/responses", "deepseek-invalid", "model",
				newResponsesSearchRuntimeStub("deepseek-invalid-credential"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Search(t.Context(), "grounded query", 1,
				NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"api.deepseek.com"}})
			var qualification *NativeSearchQualificationError
			if !errors.As(err, &qualification) ||
				qualification.Reason != NativeSearchReasonResponseInvalid || requests != 1 {
				t.Fatalf("qualification=%#v requests=%d err=%v", qualification,
					requests, err)
			}
		})
	}
}

func responsesSearchAuthority() NetworkAuthority {
	return NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"api.vendor.com"}}
}

func successfulResponsesSearchBody() string {
	return `{"status":"completed","output":[` +
		`{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[` +
		`{"type":"url","url":"https://docs.example.com/a/../report","title":" Primary source ","snippet":" bounded\tsnippet ","published_at":"2026-08-31"},` +
		`{"type":"url","url":"http://docs.example.com/insecure","title":"insecure"},` +
		`{"type":"url","url":"https://127.0.0.1/private","title":"private"},` +
		`{"type":"url","url":"https://docs.example.com/report","title":"duplicate"}` +
		`]}},` +
		`{"type":"message","content":[{"type":"output_text","text":"answer","annotations":[` +
		`{"type":"url_citation","url":"https://docs.example.com/report","title":"citation title"},` +
		`{"type":"url_citation","url_citation":{"url":"https://news.example.org/item","title":"News item"}}` +
		`]}]}` +
		`]}`
}

func TestOpenAIResponsesSearchProviderRequiresCompletedResponseAndSearchCall(t *testing.T) {
	completed := successfulResponsesSearchBody()
	tests := []struct {
		name string
		body string
	}{
		{name: "missing response status", body: strings.Replace(completed,
			`"status":"completed",`, "", 1)},
		{name: "incomplete response", body: strings.Replace(completed,
			`"status":"completed"`, `"status":"incomplete"`, 1)},
		{name: "in progress response", body: strings.Replace(completed,
			`"status":"completed"`, `"status":"in_progress"`, 1)},
		{name: "failed response", body: strings.Replace(completed,
			`"status":"completed"`, `"status":"failed"`, 1)},
		{name: "missing search call status", body: strings.Replace(completed,
			`"type":"web_search_call","status":"completed",`,
			`"type":"web_search_call",`, 1)},
		{name: "search call in progress", body: strings.Replace(completed,
			`"type":"web_search_call","status":"completed"`,
			`"type":"web_search_call","status":"in_progress"`, 1)},
		{name: "search call failed", body: strings.Replace(completed,
			`"type":"web_search_call","status":"completed"`,
			`"type":"web_search_call","status":"failed"`, 1)},
		{name: "mixed completed and failed search calls", body: strings.Replace(completed,
			`{"type":"message"`,
			`{"type":"web_search_call","status":"failed","action":{"type":"open_page"}},`+
				`{"type":"message"`, 1)},
		{name: "citations without hosted call", body: `{"status":"completed","output":[` +
			`{"type":"message","status":"completed","content":[{"type":"output_text",` +
			`"text":"answer","annotations":[{"type":"url_citation",` +
			`"url":"https://docs.example.com/report","title":"citation"}]}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := responsesSearchTestClient(t,
				func(*http.Request) (*http.Response, error) {
					requests++
					return webResponse(http.StatusOK,
						http.Header{"Content-Type": {"application/json"}}, test.body), nil
				})
			provider, err := NewOpenAIResponsesSearchProvider(client,
				"https://api.vendor.com/v1/responses", "completion-state-provider",
				"model", newResponsesSearchRuntimeStub("completion-state-credential"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
			var qualification *NativeSearchQualificationError
			if !errors.As(err, &qualification) ||
				qualification.Reason != NativeSearchReasonResponseInvalid || requests != 1 {
				t.Fatalf("qualification=%#v requests=%d err=%v", qualification,
					requests, err)
			}
			snapshot := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority())
			if snapshot.Status != SearchQualificationUnavailable ||
				snapshot.Reason != NativeSearchReasonResponseInvalid || requests != 1 {
				t.Fatalf("snapshot=%+v requests=%d", snapshot, requests)
			}
		})
	}
}

func TestOpenAIResponsesSearchProviderQualifiesCachesAndInvalidatesOnCredentialRotation(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("credential-one")
	runtime.apply = func(secret string, headers http.Header, body map[string]any) error {
		headers.Set("X-Provider-Policy", "active")
		body["metadata"] = map[string]any{"tenant": "local"}
		return nil
	}
	var requests atomic.Int32
	client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
		requestNumber := requests.Add(1)
		wantSecret := "credential-one"
		if requestNumber >= 3 {
			wantSecret = "credential-two"
		}
		if request.Method != http.MethodPost || request.URL.String() !=
			"https://api.vendor.com/v1/responses" ||
			request.Header.Get("Authorization") != "Bearer "+wantSecret ||
			request.Header.Get("X-Provider-Policy") != "active" ||
			request.Header.Get("Content-Type") != "application/json" ||
			request.Header.Get("Cookie") != "" {
			t.Fatalf("unsafe native search request %s headers=%v", request.URL, request.Header)
		}
		var body map[string]any
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools, _ := body["tools"].([]any)
		tool, _ := tools[0].(map[string]any)
		include, _ := body["include"].([]any)
		if body["model"] != "remote-search-model" || body["store"] != false ||
			body["max_tool_calls"] != float64(1) || body["tool_choice"] != "required" ||
			body["max_output_tokens"] != float64(nativeSearchMaxOutputTokens) ||
			body["parallel_tool_calls"] != false ||
			len(tools) != 1 || tool["type"] != nativeSearchTool || len(include) != 1 ||
			include[0] != "web_search_call.action.sources" || body["metadata"] == nil {
			t.Fatalf("native search request body=%#v", body)
		}
		return webResponse(http.StatusOK,
			http.Header{"Content-Type": {"application/json; charset=utf-8"}},
			successfulResponsesSearchBody()), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "custom-openai", "local-model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	firstBinding, err := provider.Qualify(t.Context(), responsesSearchAuthority())
	if err != nil || requests.Load() != 1 || len(firstBinding) != sha256.Size*2 {
		t.Fatalf("first binding=%q requests=%d err=%v", firstBinding, requests.Load(), err)
	}
	secondBinding, err := provider.Qualify(t.Context(), responsesSearchAuthority())
	if err != nil || requests.Load() != 1 || secondBinding != firstBinding {
		t.Fatalf("cached binding=%q requests=%d err=%v", secondBinding, requests.Load(), err)
	}
	results, err := provider.Search(t.Context(), "public evidence", 2,
		responsesSearchAuthority())
	if err != nil || requests.Load() != 2 || len(results) != 2 ||
		results[0].URL != "https://docs.example.com/report" ||
		results[0].Title != "Primary source" || results[0].Snippet != "bounded snippet" ||
		results[0].Rank != 1 || results[1].URL != "https://news.example.org/item" ||
		results[1].Title != "News item" || results[1].Rank != 2 {
		t.Fatalf("results=%#v requests=%d err=%v", results, requests.Load(), err)
	}
	// The result hosts are intentionally absent from the Run authority. Search
	// discovery accepts them; a later fetch must obtain explicit authority.
	runtime.rotate("credential-two")
	rotatedBinding, err := provider.Qualify(t.Context(), responsesSearchAuthority())
	if err != nil || requests.Load() != 3 || rotatedBinding != firstBinding {
		t.Fatalf("rotated binding=%q requests=%d err=%v", rotatedBinding, requests.Load(), err)
	}
	runtime.mu.Lock()
	if runtime.resolve != 4 || runtime.mapCall != 4 || runtime.applied < 5 {
		t.Fatalf("runtime calls resolve=%d map=%d apply=%d", runtime.resolve,
			runtime.mapCall, runtime.applied)
	}
	runtime.mu.Unlock()
}

func TestOpenAIResponsesSearchProviderQualificationSnapshotNeverProbes(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("snapshot-credential")
	var requests atomic.Int32
	client := responsesSearchTestClient(t, func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return webResponse(http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			successfulResponsesSearchBody()), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "snapshot-provider", "local-model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	before := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority())
	if before.Status != SearchQualificationUnqualified || before.Reason != "" ||
		requests.Load() != 0 {
		t.Fatalf("before=%+v requests=%d", before, requests.Load())
	}
	if _, err := provider.Qualify(t.Context(), responsesSearchAuthority()); err != nil {
		t.Fatal(err)
	}
	after := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority())
	if after.Status != SearchQualificationReady || after.Reason != "" ||
		requests.Load() != 1 {
		t.Fatalf("after=%+v requests=%d", after, requests.Load())
	}
	runtime.rotate("snapshot-credential-rotated")
	rotated := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority())
	if rotated.Status != SearchQualificationUnqualified || rotated.Reason != "" ||
		requests.Load() != 1 {
		t.Fatalf("rotated=%+v requests=%d", rotated, requests.Load())
	}
	denied := provider.QualificationSnapshot(t.Context(), NetworkAuthority{
		Mode: "allowlist", AllowedTargets: []string{"docs.example.com"}})
	if denied.Status != SearchQualificationUnavailable ||
		denied.Reason != NativeSearchReasonEndpointUnauthorized || requests.Load() != 1 {
		t.Fatalf("denied=%+v requests=%d", denied, requests.Load())
	}
}

func TestOpenAIResponsesSearchProviderFallsBackOnlyForExplicitUnsupportedTool(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("fallback-credential")
	var requests atomic.Int32
	client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
		var body struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Tools) != 1 {
			t.Fatalf("request body=%#v err=%v", body, err)
		}
		current := requests.Add(1)
		if current == 1 {
			if body.Tools[0].Type != nativeSearchTool {
				t.Fatalf("first tool=%q", body.Tools[0].Type)
			}
			return webResponse(http.StatusBadRequest,
				http.Header{"Content-Type": {"application/json"}},
				`{"error":{"message":"The tool web_search is not supported by this endpoint","type":"invalid_request_error","param":"tools[0].type","code":"unsupported_value"}}`), nil
		}
		if body.Tools[0].Type != nativeSearchPreviewTool {
			t.Fatalf("fallback tool=%q", body.Tools[0].Type)
		}
		return webResponse(http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			successfulResponsesSearchBody()), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "fallback-provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := provider.Qualify(t.Context(), responsesSearchAuthority())
	if err != nil || requests.Load() != 2 || len(binding) != sha256.Size*2 {
		t.Fatalf("binding=%q requests=%d err=%v", binding, requests.Load(), err)
	}
	if _, err := provider.Search(t.Context(), "cached preview", 1,
		responsesSearchAuthority()); err != nil || requests.Load() != 3 {
		t.Fatalf("cached preview requests=%d err=%v", requests.Load(), err)
	}

	unrelatedRequests := 0
	unrelatedClient := responsesSearchTestClient(t,
		func(*http.Request) (*http.Response, error) {
			unrelatedRequests++
			return webResponse(http.StatusBadRequest,
				http.Header{"Content-Type": {"application/json"}},
				`{"error":{"message":"invalid input for web_search","param":"input","code":"invalid_value"}}`), nil
		})
	unrelated, err := NewOpenAIResponsesSearchProvider(unrelatedClient,
		"https://api.vendor.com/v1/responses", "unrelated-provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = unrelated.Qualify(t.Context(), responsesSearchAuthority())
	var qualification *NativeSearchQualificationError
	if !errors.As(err, &qualification) ||
		qualification.Reason != NativeSearchReasonProviderRejected || unrelatedRequests != 1 {
		t.Fatalf("qualification=%#v requests=%d err=%v", qualification,
			unrelatedRequests, err)
	}
}

func TestOpenAIResponsesSearchProviderFallsBackToDatedHostedToolAndBindsIt(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("dated-fallback-credential")
	var requestedTools []string
	client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
		var body struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Tools) != 1 {
			t.Fatalf("request body=%#v err=%v", body, err)
		}
		tool := body.Tools[0].Type
		requestedTools = append(requestedTools, tool)
		if tool != nativeSearchDatedTool {
			return webResponse(http.StatusUnprocessableEntity,
				http.Header{"Content-Type": {"application/json"}},
				`{"error":{"message":"The tool `+tool+` is not supported by this endpoint","type":"invalid_request_error","param":"tools[0].type","code":"unsupported_tool"}}`), nil
		}
		return webResponse(http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			successfulResponsesSearchBody()), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "dated-fallback-provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := provider.Qualify(t.Context(), responsesSearchAuthority())
	if err != nil || strings.Join(requestedTools, ",") !=
		strings.Join(nativeSearchToolVariants[:], ",") {
		t.Fatalf("tools=%v binding=%q err=%v", requestedTools, binding, err)
	}
	state, err := provider.resolveState(t.Context(), responsesSearchAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if want := provider.publicBinding(state, nativeSearchDatedTool); binding != want {
		t.Fatalf("binding=%q want exact dated binding=%q", binding, want)
	}
	if _, err := provider.Search(t.Context(), "cached dated hosted search", 1,
		responsesSearchAuthority()); err != nil || len(requestedTools) != 4 ||
		requestedTools[3] != nativeSearchDatedTool {
		t.Fatalf("cached tools=%v err=%v", requestedTools, err)
	}
}

func TestOpenAIResponsesSearchProviderBoundsUnsupportedVariantProbing(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("unsupported-variants-credential")
	var requestedTools []string
	client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
		var body struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Tools) != 1 {
			t.Fatalf("request body=%#v err=%v", body, err)
		}
		tool := body.Tools[0].Type
		requestedTools = append(requestedTools, tool)
		return webResponse(http.StatusBadRequest,
			http.Header{"Content-Type": {"application/json"}},
			`{"error":{"message":"The tool `+tool+` is not supported by this endpoint","param":"tools[0].type","code":"unsupported_tool"}}`), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "unsupported-variants-provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupported := func(err error) {
		t.Helper()
		var qualification *NativeSearchQualificationError
		if !errors.As(err, &qualification) ||
			qualification.Reason != NativeSearchReasonToolUnsupported {
			t.Fatalf("qualification=%#v err=%v", qualification, err)
		}
	}
	_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
	assertUnsupported(err)
	if strings.Join(requestedTools, ",") != strings.Join(nativeSearchToolVariants[:], ",") {
		t.Fatalf("bounded variant trace=%v", requestedTools)
	}
	_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
	assertUnsupported(err)
	if len(requestedTools) != len(nativeSearchToolVariants) {
		t.Fatalf("negative cache repeated variant probes: %v", requestedTools)
	}
}

func TestOpenAIResponsesSearchProviderAdvancesFromCachedPreviewToDatedTool(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("cached-preview-credential")
	var requestedTools []string
	previewAccepted := true
	client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
		var body struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Tools) != 1 {
			t.Fatalf("request body=%#v err=%v", body, err)
		}
		tool := body.Tools[0].Type
		requestedTools = append(requestedTools, tool)
		if tool == nativeSearchPreviewTool && previewAccepted || tool == nativeSearchDatedTool {
			return webResponse(http.StatusOK,
				http.Header{"Content-Type": {"application/json"}},
				successfulResponsesSearchBody()), nil
		}
		return webResponse(http.StatusBadRequest,
			http.Header{"Content-Type": {"application/json"}},
			`{"error":{"message":"The tool `+tool+` is not supported by this endpoint","param":"tools[0].type","code":"unsupported_tool"}}`), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "cached-preview-provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Qualify(t.Context(), responsesSearchAuthority()); err != nil {
		t.Fatal(err)
	}
	previewAccepted = false
	if _, err := provider.Search(t.Context(), "advance cached variant", 1,
		responsesSearchAuthority()); err != nil {
		t.Fatal(err)
	}
	want := []string{nativeSearchTool, nativeSearchPreviewTool,
		nativeSearchPreviewTool, nativeSearchDatedTool}
	if strings.Join(requestedTools, ",") != strings.Join(want, ",") {
		t.Fatalf("variant trace=%v want=%v", requestedTools, want)
	}
}

func TestOpenAIResponsesSearchProviderRejectsRedirectMalformedResponseAndSecretEcho(t *testing.T) {
	const secret = "credential-that-must-never-leak"
	runtime := newResponsesSearchRuntimeStub(secret)
	tests := []struct {
		name       string
		response   *http.Response
		wantReason string
	}{
		{name: "redirect", response: webResponse(http.StatusTemporaryRedirect,
			http.Header{"Location": {"https://other.example.net/v1/responses"}}, ""),
			wantReason: NativeSearchReasonTransportUnavailable},
		{name: "trailing JSON", response: webResponse(http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			successfulResponsesSearchBody()+` {}`),
			wantReason: NativeSearchReasonResponseInvalid},
		{name: "secret echo", response: webResponse(http.StatusUnauthorized,
			http.Header{"Content-Type": {"application/json"}},
			`{"error":{"message":"rejected `+secret+`"}}`),
			wantReason: NativeSearchReasonProviderRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := responsesSearchTestClient(t,
				func(*http.Request) (*http.Response, error) {
					requests++
					return test.response, nil
				})
			provider, err := NewOpenAIResponsesSearchProvider(client,
				"https://api.vendor.com/v1/responses", "safe-errors", "model", runtime)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
			var qualification *NativeSearchQualificationError
			if !errors.As(err, &qualification) || qualification.Reason != test.wantReason ||
				requests != 1 || strings.Contains(err.Error(), secret) {
				t.Fatalf("qualification=%#v requests=%d err=%v", qualification,
					requests, err)
			}
		})
	}
}

func TestOpenAIResponsesSearchProviderSerializesConcurrentQualificationProbe(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("concurrent-credential")
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	client := responsesSearchTestClient(t, func(*http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		return webResponse(http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			successfulResponsesSearchBody()), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "concurrent-provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 12
	errorsOut := make(chan error, goroutines)
	bindings := make(chan string, goroutines)
	for range goroutines {
		go func() {
			binding, qualifyErr := provider.Qualify(t.Context(), responsesSearchAuthority())
			bindings <- binding
			errorsOut <- qualifyErr
		}()
	}
	<-started
	close(release)
	var binding string
	for range goroutines {
		if err := <-errorsOut; err != nil {
			t.Fatal(err)
		}
		current := <-bindings
		if binding == "" {
			binding = current
		} else if current != binding {
			t.Fatalf("credential-free bindings differ %q != %q", current, binding)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent qualification sent %d requests", requests.Load())
	}
	runtime.mu.Lock()
	if runtime.resolve != goroutines || runtime.mapCall != goroutines ||
		runtime.applied < goroutines {
		t.Fatalf("runtime calls resolve=%d map=%d apply=%d", runtime.resolve,
			runtime.mapCall, runtime.applied)
	}
	runtime.mu.Unlock()
}

func TestOpenAIResponsesSearchProviderBoundsNegativeQualificationCache(t *testing.T) {
	tests := []struct {
		name       string
		response   func() (*http.Response, error)
		wantReason string
	}{
		{name: "unauthorized", response: func() (*http.Response, error) {
			return webResponse(http.StatusUnauthorized,
				http.Header{"Content-Type": {"application/json"}},
				`{"error":{"message":"unauthorized"}}`), nil
		}, wantReason: NativeSearchReasonProviderRejected},
		{name: "rate limited", response: func() (*http.Response, error) {
			return webResponse(http.StatusTooManyRequests,
				http.Header{"Content-Type": {"application/json"}},
				`{"error":{"message":"rate limited"}}`), nil
		}, wantReason: NativeSearchReasonProviderRejected},
		{name: "transport timeout", response: func() (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}, wantReason: NativeSearchReasonTransportUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newResponsesSearchRuntimeStub("negative-one")
			requests := 0
			client := responsesSearchTestClient(t,
				func(*http.Request) (*http.Response, error) {
					requests++
					return test.response()
				})
			provider, err := NewOpenAIResponsesSearchProvider(client,
				"https://api.vendor.com/v1/responses", "negative-provider", "model", runtime)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
			provider.now = func() time.Time { return now }
			assertReason := func(err error) {
				t.Helper()
				var qualification *NativeSearchQualificationError
				if !errors.As(err, &qualification) || qualification.Reason != test.wantReason {
					t.Fatalf("qualification=%#v err=%v", qualification, err)
				}
			}
			_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
			assertReason(err)
			_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
			assertReason(err)
			if requests != 1 {
				t.Fatalf("negative cache sent %d requests", requests)
			}
			// A rotated credential gets a different private cache key and may be
			// retried immediately; the credential or its digest is never returned.
			runtime.rotate("negative-two")
			_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
			assertReason(err)
			if requests != 2 {
				t.Fatalf("credential rotation did not invalidate negative cache: %d", requests)
			}
			now = now.Add(nativeSearchNegativeTTL(test.wantReason) + time.Second)
			_, err = provider.Qualify(t.Context(), responsesSearchAuthority())
			assertReason(err)
			if requests != 3 {
				t.Fatalf("expired negative cache sent %d requests", requests)
			}
		})
	}
}

func TestOpenAIResponsesSearchProviderSharesNegativeProbeFlight(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("negative-concurrent")
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	client := responsesSearchTestClient(t, func(*http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		return webResponse(http.StatusTooManyRequests,
			http.Header{"Content-Type": {"application/json"}},
			`{"error":{"message":"rate limited"}}`), nil
	})
	provider, err := NewOpenAIResponsesSearchProvider(client,
		"https://api.vendor.com/v1/responses", "negative-concurrent", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 10
	errorsOut := make(chan error, goroutines)
	for range goroutines {
		go func() {
			_, qualifyErr := provider.Qualify(t.Context(), responsesSearchAuthority())
			errorsOut <- qualifyErr
		}()
	}
	<-started
	close(release)
	for range goroutines {
		var qualification *NativeSearchQualificationError
		if err := <-errorsOut; !errors.As(err, &qualification) ||
			qualification.Reason != NativeSearchReasonProviderRejected {
			t.Fatalf("qualification=%#v err=%v", qualification, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("negative flight sent %d requests", requests.Load())
	}
	if _, err := provider.Qualify(t.Context(), responsesSearchAuthority()); err == nil ||
		requests.Load() != 1 {
		t.Fatalf("negative cache requests=%d err=%v", requests.Load(), err)
	}
}

func TestOpenAIResponsesSearchProviderRequiresPublicAuthorizedEndpointAndValidRuntime(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("credential")
	for _, endpoint := range []string{
		"http://api.vendor.com/v1/responses",
		"https://127.0.0.1/v1/responses",
		"https://user:secret@api.vendor.com/v1/responses",
	} {
		if _, err := NewOpenAIResponsesSearchProvider(nil, endpoint,
			"provider", "model", runtime); err == nil {
			t.Fatalf("unsafe endpoint %q was accepted", endpoint)
		}
	}
	provider, err := NewOpenAIResponsesSearchProvider(responsesSearchTestClient(t,
		func(*http.Request) (*http.Response, error) {
			return nil, io.EOF
		}), "https://api.vendor.com/v1/responses", "provider", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Qualify(t.Context(), NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"other.example.net"}})
	var qualification *NativeSearchQualificationError
	if !errors.As(err, &qualification) ||
		qualification.Reason != NativeSearchReasonEndpointUnauthorized {
		t.Fatalf("qualification=%#v err=%v", qualification, err)
	}
	badRuntime := newResponsesSearchRuntimeStub("credential")
	badRuntime.binding = "not-a-digest"
	if _, err := NewOpenAIResponsesSearchProvider(nil,
		"https://api.vendor.com/v1/responses", "provider", "model", badRuntime); err == nil {
		t.Fatal("invalid runtime binding was accepted")
	}
	mutatingRuntime := newResponsesSearchRuntimeStub("credential")
	mutatingRuntime.apply = func(_ string, _ http.Header, body map[string]any) error {
		body["max_output_tokens"] = 8192
		body["parallel_tool_calls"] = true
		return nil
	}
	transportCalled := false
	mutating, err := NewOpenAIResponsesSearchProvider(responsesSearchTestClient(t,
		func(*http.Request) (*http.Response, error) {
			transportCalled = true
			return nil, io.EOF
		}), "https://api.vendor.com/v1/responses", "provider", "model", mutatingRuntime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutating.Qualify(t.Context(), responsesSearchAuthority())
	if !errors.As(err, &qualification) ||
		qualification.Reason != NativeSearchReasonRuntimeInvalid || transportCalled {
		t.Fatalf("protected body mutation qualification=%#v transport=%t err=%v",
			qualification, transportCalled, err)
	}
}
