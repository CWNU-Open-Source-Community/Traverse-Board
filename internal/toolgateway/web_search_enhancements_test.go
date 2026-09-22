package toolgateway

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSearchEnhancementPayloadsPreserveRetrievalConditions(t *testing.T) {
	raw, err := NormalizeWebEvidencePayload(WebSearchTool, json.RawMessage(`{"version":"web_search.v1","query":"original query","limit":2,"allowed_domains":["Docs.Example.com","docs.example.com"]}`))
	var search WebSearchPayload
	if err != nil || json.Unmarshal(raw, &search) != nil || !reflect.DeepEqual(search.AllowedDomains, []string{"docs.example.com"}) || search.Query != "original query" {
		t.Fatalf("search conditions changed: %s %v", raw, err)
	}
	for _, fields := range []string{
		`"allowed_domains":null`, `"allowed_domains":["https://example.com"]`,
		`"allowed_domains":["127.0.0.1"]`, `"allowed_domains":["example.com"],"blocked_domains":["other.example.com"]`,
		`"allowed_domains":[],"blocked_domains":[]`, `"blocked_domains":["*.example.com"]`,
	} {
		if _, err := NormalizeWebEvidencePayload(WebSearchTool, json.RawMessage(`{"version":"web_search.v1","query":"query","limit":1,`+fields+`}`)); err == nil {
			t.Errorf("accepted invalid search conditions: %s", fields)
		}
	}
	for _, identity := range []string{`"url":"https://docs.example.com/report"`, `"source_id":"source-a"`, `"source_id":"source-a","snapshot_id":"snapshot-a"`} {
		raw, err := NormalizeWebEvidencePayload(WebFetchTool, json.RawMessage(`{"version":"web_fetch.v1",`+identity+`,"question":"  release\n date  "}`))
		var fetch WebFetchPayload
		if err != nil || json.Unmarshal(raw, &fetch) != nil || fetch.Question != "release date" {
			t.Errorf("question retrieval failed: %s %v", raw, err)
		}
	}
	for _, fields := range []string{
		`"source_id":"source-a","snapshot_id":"snapshot-a","offset":0,"limit":20,"question":"date"`,
		`"source_id":"source-a","snapshot_id":"snapshot-a","question":"date","connector":"github"`,
		`"url":"https://docs.example.com/report","question":null`,
		`"url":"https://docs.example.com/report","question":"   "`,
	} {
		if _, err := NormalizeWebEvidencePayload(WebFetchTool, json.RawMessage(`{"version":"web_fetch.v1",`+fields+`}`)); err == nil {
			t.Errorf("accepted ambiguous question read: %s", fields)
		}
	}
}
