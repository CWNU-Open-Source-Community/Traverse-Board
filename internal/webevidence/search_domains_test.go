package webevidence

import "testing"

func TestNormalizeSearchDomainsAndExactSubdomainMatch(t *testing.T) {
	filter, err := NormalizeSearchDomains([]string{"Example.COM.", "docs.example.com", "example.com"}, nil)
	if err != nil || len(filter.AllowedDomains) != 2 || filter.AllowedDomains[0] != "docs.example.com" ||
		filter.AllowedDomains[1] != "example.com" || filter.Policy() != SearchFilterPolicyAllowed {
		t.Fatalf("normalized filter=%#v err=%v", filter, err)
	}
	for raw, want := range map[string]bool{
		"https://example.com/a": true, "https://a.example.com/a": true,
		"https://evil-example.com/a": false, "https://example.com.evil/a": false,
	} {
		got, err := filter.MatchURL(raw)
		if err != nil || got != want {
			t.Fatalf("match %s=%t want %t err=%v", raw, got, want, err)
		}
	}
}

func TestNormalizeSearchDomainsRejectsUnsafeShapes(t *testing.T) {
	for _, values := range [][]string{{"https://example.com"}, {"example.com/path"},
		{"user@example.com"}, {"*.example.com"}, {"127.0.0.1"}, {"exa_mple.com"},
		{"аmazon.com"}, {"example.com.."}} {
		if _, err := NormalizeSearchDomains(values, nil); err == nil {
			t.Fatalf("accepted invalid domain %#v", values)
		}
	}
	if _, err := NormalizeSearchDomains([]string{"example.com"}, []string{"evil.example"}); err == nil {
		t.Fatal("accepted mixed allow/block filters")
	}
}

func TestSearchResultValidateFiltersRejectsLegacyFilteredCountAndEscapingURL(t *testing.T) {
	if err := (SearchResult{FilteredOutCount: 1}).ValidateFilters(); err == nil {
		t.Fatal("accepted filtered count without a filter")
	}
	result := SearchResult{AllowedDomains: []string{"example.com"},
		FilterPolicy: SearchFilterPolicyAllowed,
		Sources:      []SearchStub{{CanonicalURL: "https://evil.example.net/"}}}
	if err := result.ValidateFilters(); err == nil {
		t.Fatal("accepted source outside stored allowed domains")
	}
	result.Sources[0].CanonicalURL = "https://docs.example.com/"
	if err := result.ValidateFilters(); err != nil {
		t.Fatalf("valid filter rejected: %v", err)
	}
}
