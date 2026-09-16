package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/llm"
)

type capturedCustomProviderRequest struct {
	path          string
	authorization string
	region        string
	body          map[string]any
}

func TestCustomProviderAdvancedConfigEntersRequestsWithDynamicCredential(t *testing.T) {
	captured := make(chan capturedCustomProviderRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
		request *http.Request,
	) {
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		var body map[string]any
		if err := decoder.Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		captured <- capturedCustomProviderRequest{path: request.URL.Path,
			authorization: request.Header.Get("Authorization"),
			region:        request.Header.Get("X-Acme-Region"), body: body}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"model":"upstream-code","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()

	definition := validCustomDefinition(server.URL + "/v1/chat/completions")
	definition.AdvancedConfig = json.RawMessage(`{
		"request_headers":{
			"Authorization":{"$credential":"acme-models","template":"Custom ${secret}"},
			"X-Acme-Region":"west"
		},
		"request_body":{
			"reasoning_effort":"high",
			"provider_auth":{"api_key":{"$credential":"acme-models"}}
		},
		"model_mapping":{"acme-code":"upstream-code"},
		"extensions":{"ignored_at_runtime":true}
	}`)
	settings := routeSettings{
		ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1),
		"route.code":                  definition.ID + "/" + definition.DefaultModel,
	}
	var credentialValue atomic.Value
	credentialValue.Store("credential-one-0123456789")
	credentials := func(_ context.Context, provider string) (string, bool, error) {
		if provider != definition.ID {
			return "", false, nil
		}
		value := credentialValue.Load().(string)
		return value, value != "", nil
	}
	registry, err := newRegistry(func(string) (string, bool) { return "", false }, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	ref := llm.ModelRef{Provider: definition.ID, Model: definition.DefaultModel}
	profileBefore, err := registry.Router().HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}

	call := func() *llm.ChatResponse {
		response, callErr := registry.Router().ChatModelRef(t.Context(), ref, llm.ChatRequest{
			Messages: []llm.Message{{Role: "user", Content: "hello"}}, MaxTokens: 16,
		})
		if callErr != nil {
			t.Fatal(callErr)
		}
		return response
	}
	firstResponse := call()
	first := <-captured
	credentialValue.Store("credential-two-9876543210")
	secondResponse := call()
	second := <-captured

	for index, got := range []capturedCustomProviderRequest{first, second} {
		secret := []string{"credential-one-0123456789", "credential-two-9876543210"}[index]
		if got.path != "/v1/chat/completions" || got.authorization != "Custom "+secret ||
			got.region != "west" || got.body["model"] != "upstream-code" ||
			got.body["reasoning_effort"] != "high" || got.body["extensions"] != nil {
			t.Fatalf("advanced runtime request %d=%#v", index, got)
		}
		auth, ok := got.body["provider_auth"].(map[string]any)
		if !ok || auth["api_key"] != secret {
			t.Fatalf("credential reference was not resolved only at request time: %#v", got.body)
		}
	}
	if firstResponse.Model != definition.DefaultModel || secondResponse.Model != definition.DefaultModel {
		t.Fatalf("upstream model mapping escaped the local route identity: %#v %#v",
			firstResponse, secondResponse)
	}
	profileAfterCredentialChange, err := registry.Router().HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if profileBefore.BindingDigest != profileAfterCredentialChange.BindingDigest {
		t.Fatal("credential content entered the model Harness binding")
	}

	definition.Revision = 2
	encoded, err := EncodeProviderDefinitionCollection(ProviderDefinitionCollection{
		Version: ProviderDefinitionCollectionVersion, Revision: 2,
		Providers: []ProviderDefinition{definition},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings[ProviderDefinitionsSettingKey] = encoded
	if _, err := registry.Reload(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	profileAfterRevision, err := registry.Router().HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if profileBefore.BindingDigest == profileAfterRevision.BindingDigest {
		t.Fatal("Provider definition revision was omitted from the Harness binding")
	}

	credentialValue.Store("")
	_, err = registry.Router().ChatModelRef(t.Context(), ref, llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil || strings.Contains(err.Error(), "credential-one") ||
		strings.Contains(err.Error(), "credential-two") {
		t.Fatalf("missing dynamic credential did not fail closed safely: %v", err)
	}
}

func TestCustomResponsesTransportLoadsWithExactHarnessBinding(t *testing.T) {
	var capturedPath string
	var capturedAuthorization string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
		request *http.Request,
	) {
		capturedPath = request.URL.Path
		capturedAuthorization = request.Header.Get("Authorization")
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&capturedBody); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte(`{
			"id":"resp_custom","object":"response","status":"completed","model":"upstream-responses",
			"output":[{"id":"msg_custom","type":"message","status":"completed","role":"assistant",
			"content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()
	definition := validCustomDefinition(server.URL + "/v1/responses")
	definition.Transport = ProviderTransportOpenAIResponses
	definition.AdvancedConfig = json.RawMessage(`{
		"request_headers":{"Authorization":{"$credential":"acme-models","template":"Token ${secret}"}},
		"request_body":{"reasoning_effort":"high"},
		"model_mapping":{"acme-code":"upstream-responses"}
	}`)
	settings := routeSettings{ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, provider string) (string, bool, error) {
			if provider == definition.ID {
				return "responses-key-0123456789", true, nil
			}
			return "", false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	availability, found := providerByName(registry.Snapshot(), definition.ID)
	if !found || availability.Status != ProviderAvailable ||
		availability.Transport != ProviderTransportOpenAIResponses ||
		len(availability.Harnesses) != len(definition.Models) ||
		availability.Harnesses[0].TransportProtocol != llm.HarnessTransportOpenAIResponses {
		t.Fatalf("Responses Provider availability=%#v", availability)
	}
	profile, err := registry.Router().HarnessProfile(llm.ModelRef{
		Provider: definition.ID, Model: definition.DefaultModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.TransportProtocol != llm.HarnessTransportOpenAIResponses ||
		profile.QualificationStatus != llm.HarnessQualificationRequired ||
		profile.ToolCallsQualified || profile.StrictJSONQualified || profile.StreamingQualified {
		t.Fatalf("Responses Provider Harness was overstated: %#v", profile)
	}
	response, err := registry.Router().ChatModelRef(t.Context(), llm.ModelRef{
		Provider: definition.ID, Model: definition.DefaultModel,
	}, llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1/responses" ||
		capturedAuthorization != "Token responses-key-0123456789" ||
		capturedBody["model"] != "upstream-responses" ||
		capturedBody["reasoning_effort"] != "high" || capturedBody["store"] != false ||
		capturedBody["stream"] != false || capturedBody["include"] != nil ||
		response.Model != definition.DefaultModel || response.Text != "ok" {
		t.Fatalf("custom Responses runtime request=%#v auth=%q path=%q response=%#v",
			capturedBody, capturedAuthorization, capturedPath, response)
	}
}

func TestCustomKeylessLoopbackProvidersRecheckCredentialsAndOmitAuthHeaders(t *testing.T) {
	for _, transport := range []string{ProviderTransportOpenAIChatCompletions,
		ProviderTransportOpenAIResponses, ProviderTransportAnthropicMessages} {
		t.Run(transport, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if len(request.Header.Values("Authorization")) != 0 || len(request.Header.Values("x-api-key")) != 0 {
					t.Error("keyless request contained credential headers")
				}
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodGet {
					_, _ = writer.Write([]byte(`{"data":[{"id":"acme-code","type":"model","display_name":"Acme Code"}],"has_more":false}`))
					return
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				stream, _ := body["stream"].(bool)
				if stream {
					writer.Header().Set("Content-Type", "text/event-stream")
				}
				var response string
				switch transport {
				case ProviderTransportOpenAIChatCompletions:
					response = `{"model":"acme-code","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
					if stream {
						response = "data: " + `{"model":"acme-code","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}` + "\n\n" +
							"data: " + `{"model":"acme-code","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}` + "\n\ndata: [DONE]\n\n"
					}
				case ProviderTransportOpenAIResponses:
					response = `{"id":"resp_local","object":"response","status":"completed","model":"acme-code","output":[{"id":"msg_local","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`
					if stream {
						response = "data: " + `{"type":"response.created","response":{"id":"resp_local","object":"response","status":"in_progress","model":"acme-code"}}` + "\n\n" +
							"data: " + `{"type":"response.output_item.added","item":{"id":"msg_local","type":"message","status":"in_progress","role":"assistant"}}` + "\n\n" +
							"data: " + `{"type":"response.output_text.delta","item_id":"msg_local","delta":"ok"}` + "\n\n" +
							"data: " + `{"type":"response.output_item.done","item":{"id":"msg_local","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}` + "\n\n" +
							"data: " + `{"type":"response.completed","response":{"id":"resp_local","object":"response","status":"completed","model":"acme-code","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}` + "\n\n"
					}
				case ProviderTransportAnthropicMessages:
					response = `{"id":"msg_local","type":"message","role":"assistant","model":"acme-code","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
					if stream {
						response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_local\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"acme-code\",\"content\":[],\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
				}
				_, _ = writer.Write([]byte(response))
			}))
			defer server.Close()
			definition := validCustomDefinition(server.URL)
			definition.Transport = transport
			var credentialReads atomic.Int32
			var credentialFailure atomic.Bool
			registry, err := newRegistry(func(string) (string, bool) { return "", false },
				func(_ context.Context, provider string) (string, bool, error) {
					if provider == definition.ID {
						credentialReads.Add(1)
						if credentialFailure.Load() {
							return "", false, errors.New("credential store unavailable")
						}
					}
					return "", false, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.LoadRouteSettings(t.Context(), routeSettings{
				ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}); err != nil {
				t.Fatal(err)
			}
			availability, found := providerByName(registry.Snapshot(), definition.ID)
			if !found || availability.Status != ProviderAvailable || availability.CredentialSource != "none" ||
				!availability.NetworkRequired || availability.NativeWebSearchRuntimeEnabled || availability.Harnesses[0].RootEligible {
				t.Fatalf("keyless availability overstated authority or qualification: %#v", availability)
			}
			if _, err := registry.Router().ListModels(t.Context()); err != nil {
				t.Fatal(err)
			}
			ref := llm.ModelRef{Provider: definition.ID, Model: definition.DefaultModel}
			request := llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hello"}}}
			response, err := registry.Router().ChatModelRef(t.Context(), ref, request)
			if err != nil || response.Text != "ok" {
				t.Fatalf("keyless chat: %#v %v", response, err)
			}
			chunks, err := registry.Router().StreamChatModelRef(t.Context(), ref, request)
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for chunk := range chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				text.WriteString(chunk.Text)
			}
			if text.String() != "ok" || requests.Load() != 3 || credentialReads.Load() != 4 {
				t.Fatalf("keyless request did not resolve per call: text=%q calls=%d reads=%d", text.String(), requests.Load(), credentialReads.Load())
			}
			credentialFailure.Store(true)
			if _, err := registry.Router().ChatModelRef(t.Context(), ref, request); err == nil || requests.Load() != 3 {
				t.Fatalf("credential read failure became an anonymous request: err=%v calls=%d", err, requests.Load())
			}
		})
	}
}

func TestCustomKeylessRuntimeRequiresExactLoopbackAndNoEffectiveCredentialReferences(t *testing.T) {
	for _, current := range []struct {
		name, endpoint, config string
		allowed                bool
	}{
		{"local", "http://127.0.0.1:1234/v1", `{}`, true},
		{"localhost", "https://localhost:1234/v1", `{}`, true},
		{"ipv6", "http://[::1]:1234/v1", `{}`, true},
		{"mapped", "http://[::ffff:127.0.0.1]:1234/v1", `{}`, true},
		{"external", "https://api.example.com/v1", `{}`, false},
		{"ambiguous host", "https://127.1/v1", `{}`, false},
		{"header reference", "http://127.0.0.1:1234/v1", `{"request_headers":{"Authorization":{"$credential":"acme-models"}}}`, false},
		{"nested body reference", "http://127.0.0.1:1234/v1", `{"request_body":{"custom":[{"auth":{"$credential":"acme-models"}}]}}`, false},
		{"inert metadata", "http://127.0.0.1:1234/v1", `{"extensions":{"reference":{"$credential":"acme-models"}}}`, true},
	} {
		t.Run(current.name, func(t *testing.T) {
			definition := validCustomDefinition(current.endpoint)
			definition.AdvancedConfig = json.RawMessage(current.config)
			runtime, err := newProviderRequestRuntime(definition, nil)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.AllowsKeylessEndpoint(current.endpoint) != current.allowed ||
				runtime.AllowsKeylessEndpoint("http://127.0.0.1:4321/v1") {
				t.Fatal("keyless runtime did not bind the exact eligible endpoint")
			}
			_, err = runtime.ResolveCredential(t.Context())
			if (err == nil) != current.allowed {
				t.Fatalf("missing credential outcome=%v", err)
			}
			_, err = NewProviderRequestRuntime(definition, nil)
			if (err == nil) != current.allowed {
				t.Fatalf("public runtime constructor outcome=%v", err)
			}
			if !current.allowed && current.name != "external" && current.name != "ambiguous host" {
				if err := runtime.Apply("", http.Header{}, map[string]any{}); err == nil {
					t.Fatal("empty secret rendered a credential reference")
				}
			}
		})
	}
}
