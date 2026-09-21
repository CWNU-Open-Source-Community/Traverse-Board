package webevidence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestResponsesSearchDiagnosticOneRequestAndHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		headers    http.Header
		body, code string
	}{
		{name: "unsupported", status: 400, code: "tool_unsupported", body: `{"error":{"message":"The tool web_search is not supported by this endpoint","type":"invalid_request_error","param":"tools[0].type","code":"unsupported_value"}}`},
		{name: "authentication", status: 401, code: "authentication", body: `{"error":"secret provider response must not escape"}`},
		{name: "rate limit", status: 429, code: "rate_limited", headers: http.Header{"Retry-After": {"120"}, "X-Ratelimit-Reset": {"1800000000"}}},
		{name: "rate limit 403", status: 403, code: "rate_limited", headers: http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1800000000"}}},
		{name: "denied 403", status: 403, code: "provider_rejected"},
		{name: "unperformed search", status: 200, code: "invalid_response", body: `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Plain answer"}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Header.Get("Authorization") != "Bearer diagnostic-credential" {
					t.Fatal("credential header was not preserved")
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				tools := body["tools"].([]any)
				if body["input"] != "Traverse Board" || body["model"] != "remote-search-model" || body["tool_choice"] != "required" ||
					body["max_tool_calls"] != float64(1) || body["store"] != false || tools[0].(map[string]any)["type"] != nativeSearchTool {
					t.Fatalf("protected request shape changed: %#v", body)
				}
				headers := tc.headers.Clone()
				if headers == nil {
					headers = make(http.Header)
				}
				headers.Set("Content-Type", "application/json")
				return webResponse(tc.status, headers, tc.body), nil
			})
			provider, err := NewOpenAIResponsesSearchProvider(client, "https://api.vendor.com/v1/responses", "vendor", "model", newResponsesSearchRuntimeStub("diagnostic-credential"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.CheckSearchConnection(t.Context(), "Traverse Board", 1, responsesSearchAuthority())
			var diagnostic *SearchDiagnosticError
			if requests != 1 || !errors.As(err, &diagnostic) || diagnostic.RequestNotAttempted || diagnostic.Code != tc.code || diagnostic.HTTPStatus != tc.status ||
				diagnostic.RetryAfter != tc.headers.Get("Retry-After") || diagnostic.RateLimitReset != tc.headers.Get("X-RateLimit-Reset") ||
				strings.Contains(err.Error(), "secret provider") {
				t.Fatalf("requests=%d diagnostic=%#v err=%v", requests, diagnostic, err)
			}
			if snapshot := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority()); snapshot.Status != SearchQualificationUnqualified {
				t.Fatalf("failed diagnostic changed qualification cache: %#v", snapshot)
			}
		})
	}
}

func TestResponsesSearchDiagnosticBypassesNegativeCacheAndUsesCachedVariant(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "standard tool after negative cache"
		if cached {
			name = "cached variant"
		}
		t.Run(name, func(t *testing.T) {
			requests := 0
			wantTool := nativeSearchTool
			if cached {
				wantTool = nativeSearchToolVariants[1]
			}
			client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
				requests++
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if tool := body["tools"].([]any)[0].(map[string]any)["type"]; tool != wantTool {
					t.Fatalf("tool=%v want=%s", tool, wantTool)
				}
				return webResponse(200, http.Header{"Content-Type": {"application/json"}}, successfulResponsesSearchBody()), nil
			})
			provider, err := NewOpenAIResponsesSearchProvider(client, "https://api.vendor.com/v1/responses", "vendor", "model", newResponsesSearchRuntimeStub("diagnostic-credential"))
			if err != nil {
				t.Fatal(err)
			}
			state, err := provider.resolveState(t.Context(), responsesSearchAuthority())
			if err != nil {
				t.Fatal(err)
			}
			if cached {
				provider.storeTool(state, wantTool)
			} else {
				provider.storeNegative(state, nativeSearchError(NativeSearchReasonProviderRejected), true)
				if _, err := provider.Search(t.Context(), "ordinary search", 1, responsesSearchAuthority()); err == nil || requests != 0 {
					t.Fatalf("ordinary Search bypassed negative cache: requests=%d err=%v", requests, err)
				}
			}
			results, err := provider.CheckSearchConnection(t.Context(), "Traverse Board", 1, responsesSearchAuthority())
			if err != nil || requests != 1 || len(results) != 1 {
				t.Fatalf("requests=%d results=%#v err=%v", requests, results, err)
			}
			if snapshot := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority()); snapshot.Status != SearchQualificationReady {
				t.Fatalf("positive hosted observation not cached: %#v", snapshot)
			}
		})
	}
}

func TestResponsesSearchDiagnosticRejectsUnauthorizedOrChangedShapeWithoutHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, code  string
		authority   NetworkAuthority
		changeShape bool
	}{
		{name: "unauthorized", code: "not_authorized", authority: NetworkAuthority{Mode: "disabled"}},
		{name: "changed shape", code: "not_configured", authority: responsesSearchAuthority(), changeShape: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := responsesSearchTestClient(t, func(*http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("unexpected network request")
			})
			runtime := newResponsesSearchRuntimeStub("diagnostic-credential")
			if tc.changeShape {
				runtime.apply = func(_ string, _ http.Header, body map[string]any) error { body["tool_choice"] = "auto"; return nil }
			}
			provider, err := NewOpenAIResponsesSearchProvider(client, "https://api.vendor.com/v1/responses", "vendor", "model", runtime)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.CheckSearchConnection(t.Context(), "Traverse Board", 1, tc.authority)
			var diagnostic *SearchDiagnosticError
			if requests != 0 || !errors.As(err, &diagnostic) || !diagnostic.RequestNotAttempted || diagnostic.Code != tc.code {
				t.Fatalf("requests=%d diagnostic=%#v err=%v", requests, diagnostic, err)
			}
		})
	}
}

func TestResponsesSearchDiagnosticTimeoutAndCredentialRotation(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		name, wantCode := "timeout", "timeout"
		if rotate {
			name, wantCode = "credential rotation", "configuration_changed"
		}
		t.Run(name, func(t *testing.T) {
			requests := 0
			runtime := newResponsesSearchRuntimeStub("diagnostic-credential")
			client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
				requests++
				if rotate {
					runtime.rotate("new-diagnostic-credential")
					return webResponse(200, http.Header{"Content-Type": {"application/json"}}, successfulResponsesSearchBody()), nil
				}
				<-request.Context().Done()
				return nil, request.Context().Err()
			})
			provider, err := NewOpenAIResponsesSearchProvider(client, "https://api.vendor.com/v1/responses", "vendor", "model", runtime)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			_, err = provider.CheckSearchConnection(ctx, "Traverse Board", 1, responsesSearchAuthority())
			var diagnostic *SearchDiagnosticError
			if requests != 1 || !errors.As(err, &diagnostic) || diagnostic.RequestNotAttempted || diagnostic.Code != wantCode {
				t.Fatalf("requests=%d diagnostic=%#v err=%v", requests, diagnostic, err)
			}
			if snapshot := provider.QualificationSnapshot(t.Context(), responsesSearchAuthority()); snapshot.Status != SearchQualificationUnqualified {
				t.Fatalf("failure qualified current credential: %#v", snapshot)
			}
		})
	}
}

func TestResponsesSearchDiagnosticCancelledBeforeSending(t *testing.T) {
	requests := 0
	client := responsesSearchTestClient(t, func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected network request")
	})
	runtime := newResponsesSearchRuntimeStub("diagnostic-credential")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runtime.apply = func(_ string, _ http.Header, _ map[string]any) error { cancel(); return nil }
	provider, err := NewOpenAIResponsesSearchProvider(client, "https://api.vendor.com/v1/responses", "vendor", "model", runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.CheckSearchConnection(ctx, "Traverse Board", 1, responsesSearchAuthority())
	var diagnostic *SearchDiagnosticError
	if requests != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "network" || !diagnostic.RequestNotAttempted {
		t.Fatalf("requests=%d diagnostic=%#v err=%v", requests, diagnostic, err)
	}
}
