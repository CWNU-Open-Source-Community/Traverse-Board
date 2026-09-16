package httpapi

import (
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/llm"
)

func TestThreadModelVisionHTTPProjectionRetainsStateAndSource(t *testing.T) {
	for _, capability := range []llm.VisionCapability{
		{State: llm.VisionSupported, Source: "operator_declared"},
		{State: llm.VisionUnsupported, Source: "provider_metadata"},
		{State: llm.VisionUnknown, Source: "unknown"},
	} {
		catalog := availableModelRouteCollectionView(application.ModelRouteCatalog{Routes: []application.ModelRouteCatalogItem{{ProviderID: "provider", Model: "model", VisionCapability: capability}}})
		view := threadModelRouteView(application.ThreadModelRouteView{Provider: "provider", Model: "model", VisionCapability: capability})
		for _, value := range []any{catalog.Routes[0], view} {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Vision llm.VisionCapability `json:"vision_capability"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Vision != capability {
				t.Fatalf("vision lost: %s %v", raw, err)
			}
		}
	}
}
