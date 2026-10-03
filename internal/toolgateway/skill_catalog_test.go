package toolgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSkillCatalogPagingRequiresBoundedUnmixedRevision(t *testing.T) {
	for _, request := range []SkillReadRequest{{Catalog: true}, {Catalog: true, Offset: 32, CatalogRevision: strings.Repeat("a", 64)}} {
		raw, _ := json.Marshal(request)
		got, canonical, err := NormalizeSkillReadPayload(raw)
		if err != nil || got != request || !json.Valid(canonical) || got.Portable() {
			t.Fatal(got, err)
		}
	}
	for _, request := range []SkillReadRequest{
		{Catalog: true, Offset: -1}, {Catalog: true, Offset: 1048577}, {Catalog: true, Offset: 32},
		{Catalog: true, CatalogRevision: "latest"}, {Catalog: true, Name: "bundled"},
		{Catalog: true, InstallationID: "installed"}, {Catalog: true, Resource: "SKILL.md"},
		{Offset: 32}, {CatalogRevision: strings.Repeat("a", 64)},
	} {
		raw, _ := json.Marshal(request)
		if _, _, err := NormalizeSkillReadPayload(raw); err == nil {
			t.Fatalf("accepted invalid page query: %s", raw)
		}
	}
	for _, raw := range []string{`{"catalog":true,"unknown":1}`, `{"catalog":true} {}`, `{"catalog":true,"offset":1.2}`} {
		if _, _, err := NormalizeSkillReadPayload(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted malformed query", raw)
		}
	}
}
