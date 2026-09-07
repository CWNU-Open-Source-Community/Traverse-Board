package modelregistry

import (
	"context"
	"encoding/json"
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
