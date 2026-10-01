package modelregistry

import (
	"cyberagent-workbench/internal/credential"
	"os"
	"testing"
)

// Opt-in read-only real catalog check. It never logs or writes the OS key and
// makes no inference about Harness eligibility from catalog membership.
func TestLiveDeepSeekModelCatalogFromExistingAnthropicEndpoint(t *testing.T) {
	if os.Getenv("TRAVERSE_MODEL_CATALOG_LIVE") != "1" {
		t.Skip("explicit live catalog check not enabled")
	}
	store := credential.NewSystemStore()
	secret, found, err := store.Get(t.Context(), "deepseek")
	if err != nil || !found || secret == "" {
		t.Skip("existing DeepSeek key unavailable")
	}
	result, err := DiscoverProviderModels(t.Context(), ModelDiscoveryOptions{ProviderID: "model-discovery-draft", EndpointURL: "https://api.deepseek.com/anthropic/v1/messages", Transport: ProviderTransportAnthropicMessages, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) == 0 {
		t.Fatal("real Provider catalog was empty")
	}
	t.Logf("real_provider_api=true; models=%d; truncated=%v; credential_persisted=false; model_inference_calls=0", len(result.Models), result.Truncated)
}
