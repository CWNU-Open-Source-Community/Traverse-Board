package webevidence

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
)

func (r SearchResult) ValidateFilters() error {
	filter, err := NormalizeSearchDomains(r.AllowedDomains, r.BlockedDomains)
	if err != nil || !slices.Equal(filter.AllowedDomains, r.AllowedDomains) ||
		!slices.Equal(filter.BlockedDomains, r.BlockedDomains) ||
		filter.Policy() != r.FilterPolicy || r.FilteredOutCount < 0 ||
		(filter.Policy() == "" && r.FilteredOutCount != 0) {
		return errors.New("web search domain filter is invalid")
	}
	for _, source := range r.Sources {
		matched, matchErr := filter.MatchURL(source.CanonicalURL)
		if matchErr != nil || !matched {
			return errors.New("web search source violates its domain filter")
		}
	}
	return nil
}

const MaxSearchDomains = 32

const (
	SearchFilterPolicyAllowed = "allowed_domains"
	SearchFilterPolicyBlocked = "blocked_domains"
)

type SearchDomainFilter struct {
	AllowedDomains []string
	BlockedDomains []string
}

func NormalizeSearchDomains(allowed, blocked []string) (SearchDomainFilter, error) {
	if len(allowed) > 0 && len(blocked) > 0 {
		return SearchDomainFilter{}, errors.New("web search allowed_domains and blocked_domains are mutually exclusive")
	}
	normalize := func(values []string) ([]string, error) {
		if len(values) > MaxSearchDomains {
			return nil, errors.New("web search domain filter exceeds the 32-domain limit")
		}
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			domain, err := normalizeSearchDomain(value)
			if err != nil {
				return nil, err
			}
			seen[domain] = struct{}{}
		}
		result := make([]string, 0, len(seen))
		for domain := range seen {
			result = append(result, domain)
		}
		sort.Strings(result)
		return result, nil
	}
	normalizedAllowed, err := normalize(allowed)
	if err != nil {
		return SearchDomainFilter{}, err
	}
	normalizedBlocked, err := normalize(blocked)
	if err != nil {
		return SearchDomainFilter{}, err
	}
	return SearchDomainFilter{AllowedDomains: normalizedAllowed,
		BlockedDomains: normalizedBlocked}, nil
}

func normalizeSearchDomain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasSuffix(value, ".") {
		value = strings.TrimSuffix(value, ".")
	}
	if value == "" || len(value) > 253 || !isASCII(value) || net.ParseIP(value) != nil ||
		strings.ContainsAny(value, "/:@?#[]*%") || strings.HasSuffix(value, ".") {
		return "", errors.New("web search domain must be a bare ASCII DNS name")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("web search domain contains an invalid DNS label")
		}
		for _, current := range label {
			if (current < 'a' || current > 'z') && (current < '0' || current > '9') && current != '-' {
				return "", errors.New("web search domain contains an invalid DNS label")
			}
		}
	}
	return value, nil
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}

func (f SearchDomainFilter) Policy() string {
	if len(f.AllowedDomains) > 0 {
		return SearchFilterPolicyAllowed
	}
	if len(f.BlockedDomains) > 0 {
		return SearchFilterPolicyBlocked
	}
	return ""
}

func (f SearchDomainFilter) MatchURL(rawURL string) (bool, error) {
	canonical, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return false, err
	}
	parsed, err := url.Parse(canonical)
	if err != nil {
		return false, err
	}
	host := strings.ToLower(parsed.Hostname())
	matches := func(domains []string) bool {
		for _, domain := range domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		}
		return false
	}
	if len(f.AllowedDomains) > 0 {
		return matches(f.AllowedDomains), nil
	}
	if len(f.BlockedDomains) > 0 {
		return !matches(f.BlockedDomains), nil
	}
	return true, nil
}
