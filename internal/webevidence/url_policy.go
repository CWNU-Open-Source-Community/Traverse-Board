package webevidence

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"

	"golang.org/x/net/idna"
)

var blockedMetadataNames = map[string]struct{}{
	"metadata.google.internal": {}, "metadata.azure.internal": {},
	"instance-data.ec2.internal": {},
}

// CanonicalizePublicHTTPSURL normalizes identity without performing DNS. The
// caller must still resolve and pin a public address immediately before I/O.
func CanonicalizePublicHTTPSURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len([]byte(raw)) > 4096 || !utf8.ValidString(raw) ||
		strings.Contains(raw, "\\") || containsControl(raw) {
		return "", errors.New("web URL is empty, oversized, or malformed")
	}
	if redact.String(raw) != raw {
		return "", errors.New("web URL cannot contain credential material")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("web URL must be absolute and cannot contain credentials")
	}
	if strings.ToLower(parsed.Scheme) != "https" {
		return "", errors.New("web URL must use HTTPS")
	}
	if strings.Contains(parsed.Path, "\\") {
		return "", errors.New("web URL path cannot contain an encoded backslash")
	}
	host, err := normalizePublicHost(parsed.Hostname())
	if err != nil {
		return "", err
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", errors.New("web URL may only use the HTTPS default port")
	}
	parsed.Scheme = "https"
	parsed.Host = host
	if strings.Contains(host, ":") {
		parsed.Host = net.JoinHostPort(host, "443")
	}
	parsed.User = nil
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	} else {
		trailing := strings.HasSuffix(parsed.Path, "/")
		parsed.Path = path.Clean(parsed.Path)
		if !strings.HasPrefix(parsed.Path, "/") {
			parsed.Path = "/" + parsed.Path
		}
		if trailing && parsed.Path != "/" {
			parsed.Path += "/"
		}
	}
	// Re-escape from the normalized decoded path. Retaining RawPath would let
	// alternate encodings such as /%70rivate evade canonical identity and
	// robots path matching while the origin interprets them as /private.
	parsed.RawPath = ""
	parsed.RawFragment = ""
	query := parsed.Query()
	for key := range query {
		if credentialQueryKey(key) {
			return "", errors.New("web URL cannot contain credential-bearing query parameters")
		}
	}
	for key, values := range query {
		sort.Strings(values)
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	parsed.ForceQuery = false
	canonical := parsed.String()
	if len([]byte(canonical)) > 4096 {
		return "", errors.New("canonical web URL exceeds the configured limit")
	}
	if redact.String(canonical) != canonical {
		return "", errors.New("web URL cannot contain encoded credential material")
	}
	return canonical, nil
}

func credentialQueryKey(value string) bool {
	value = strings.NewReplacer("-", "_", ".", "_").Replace(
		strings.ToLower(strings.TrimSpace(value)))
	switch value {
	case "access_token", "api_key", "apikey", "auth_token", "authorization",
		"client_secret", "credential", "jwt", "oauth_token", "password", "passwd",
		"secret", "signature", "sig", "token", "x_amz_credential", "x_amz_signature",
		"x_goog_credential", "x_goog_signature":
		return true
	default:
		return false
	}
}

func normalizePublicHost(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "%*[]/\\") ||
		containsControl(host) {
		return "", errors.New("web URL host is malformed")
	}
	if forbiddenPublicHostName(host) {
		return "", errors.New("web URL host is local or metadata-only and forbidden")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if !IsPublicAddress(address) {
			return "", errors.New("web URL address is not public")
		}
		return address.String(), nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" || len(ascii) > 253 || strings.Contains(ascii, "..") {
		return "", errors.New("web URL host is not a valid DNS name")
	}
	ascii = strings.ToLower(ascii)
	// IDNA maps several Unicode dot and compatibility characters. Reapply the
	// local/metadata policy after that mapping so an alternate spelling cannot
	// bypass the name-level denylist before DNS validation.
	if forbiddenPublicHostName(ascii) {
		return "", errors.New("web URL host is local or metadata-only and forbidden")
	}
	if !strings.Contains(ascii, ".") {
		return "", errors.New("web URL host must be a fully qualified public DNS name")
	}
	return ascii, nil
}

func forbiddenPublicHostName(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".intranet") || strings.HasSuffix(host, ".corp") ||
		strings.HasSuffix(host, ".home") || strings.HasSuffix(host, ".home.arpa") ||
		strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".localdomain") ||
		strings.HasSuffix(host, ".test") || strings.HasSuffix(host, ".invalid") ||
		strings.HasSuffix(host, ".example") {
		return true
	}
	if _, blocked := blockedMetadataNames[host]; blocked {
		return true
	}
	return false
}

func IsPublicAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() ||
		address.IsMulticast() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() {
		return false
	}
	for _, raw := range []string{
		"169.254.169.254", "169.254.170.2", "100.100.100.200", "168.63.129.16",
		"fd00:ec2::254",
	} {
		if address == netip.MustParseAddr(raw) {
			return false
		}
	}
	for _, raw := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4",
		"::/96", "::ffff:0:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48",
		"100::/64", "2001::/32", "2001:2::/48",
		"2001:10::/28", "2001:20::/28", "2001:db8::/32", "2002::/16",
		"3fff::/20", "5f00::/16", "fec0::/10",
	} {
		if netip.MustParsePrefix(raw).Contains(address) {
			return false
		}
	}
	return address.IsGlobalUnicast()
}

type NetworkAuthority struct {
	Mode           string   `json:"mode"`
	AllowedTargets []string `json:"allowed_targets"`
}

func (a NetworkAuthority) Validate() error {
	if a.Mode == "disabled" {
		if len(a.AllowedTargets) != 0 {
			return errors.New("disabled web network authority cannot retain targets")
		}
		return nil
	}
	if a.Mode != "allowlist" || len(a.AllowedTargets) == 0 || len(a.AllowedTargets) > 256 {
		return errors.New("web network authority requires a bounded allowlist")
	}
	for _, target := range a.AllowedTargets {
		if _, err := normalizeAuthorityTarget(target); err != nil {
			return err
		}
	}
	return nil
}

func (a NetworkAuthority) Authorize(rawURL string) (string, error) {
	if a.Mode == "disabled" {
		return "", errors.New("web_evidence_network_disabled: enable Run network_mode=allowlist and add an allowed target")
	}
	if err := a.Validate(); err != nil {
		return "", fmt.Errorf("invalid web network authority: %w", err)
	}
	canonical, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(canonical)
	host := parsed.Hostname()
	for _, rawTarget := range a.AllowedTargets {
		target, _ := normalizeAuthorityTarget(rawTarget)
		switch {
		case target == PublicHTTPSTarget:
			return canonical, nil
		case strings.HasPrefix(target, "*.") && strings.HasSuffix(host, target[1:]) &&
			host != strings.TrimPrefix(target, "*."):
			return canonical, nil
		case host == target:
			return canonical, nil
		}
	}
	return "", errors.New("web_evidence_target_denied: add the public host to the Run allowed targets")
}

func normalizeAuthorityTarget(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == PublicHTTPSTarget {
		return raw, nil
	}
	if strings.HasPrefix(raw, "https://") {
		canonical, err := CanonicalizePublicHTTPSURL(raw)
		if err != nil {
			return "", err
		}
		parsed, _ := url.Parse(canonical)
		if parsed.Path != "/" || parsed.RawQuery != "" {
			return "", errors.New("web allowlist URL must identify an HTTPS origin")
		}
		return parsed.Hostname(), nil
	}
	if strings.HasPrefix(raw, "*.") {
		host, err := normalizePublicHost(strings.TrimPrefix(raw, "*."))
		if err != nil || net.ParseIP(host) != nil {
			return "", errors.New("web wildcard target must be a public DNS suffix")
		}
		return "*." + host, nil
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if port != "443" {
			return "", errors.New("web allowlist target may only use port 443")
		}
		raw = host
	}
	return normalizePublicHost(raw)
}

func containsControl(value string) bool {
	for _, current := range value {
		if unicode.IsControl(current) {
			return true
		}
	}
	return false
}
