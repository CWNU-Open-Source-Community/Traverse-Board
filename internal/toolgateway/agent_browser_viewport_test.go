package toolgateway

import (
	"encoding/json"
	"testing"
)

func TestAgentBrowserViewportStrictDimensionsAndScope(t *testing.T) {
	for _, raw := range []string{
		`{"version":"browser_navigate.v2","url":"https://example.org","viewport":{"width":390,"height":844}}`,
		`{"version":"browser_navigate.v2","url":"https://example.org","viewport":{"width":240,"height":240}}`,
		`{"version":"browser_navigate.v2","url":"https://example.org","viewport":{"width":3840,"height":2160}}`,
	} {
		p, err := NormalizeAgentBrowserPayload(BrowserNavigateTool, json.RawMessage(raw))
		var decoded AgentBrowserPayload
		if err != nil || json.Unmarshal(p, &decoded) != nil || decoded.Viewport == nil {
			t.Fatalf("viewport lost during canonical dispatch: %s %v", p, err)
		}
	}
	for _, raw := range []string{
		`{"width":239,"height":844}`, `{"width":390,"height":2161}`, `{"width":3841,"height":844}`,
		`{"width":390.5,"height":844}`, `{"width":390}`, `{"width":390,"height":844,"mobile":true}`,
	} {
		p := `{"version":"browser_navigate.v2","url":"https://example.org","viewport":` + raw + `}`
		if _, err := NormalizeAgentBrowserPayload(BrowserNavigateTool, json.RawMessage(p)); err == nil {
			t.Fatalf("invalid viewport accepted: %s", p)
		}
	}
	if _, err := NormalizeAgentBrowserPayload(BrowserSnapshotTool, json.RawMessage(`{"version":"browser_snapshot.v2","viewport":{"width":390,"height":844}}`)); err == nil {
		t.Fatal("snapshot gained resize dispatch")
	}
}
