package webevidence

import (
	"net/netip"
	"strings"
	"testing"
)

func TestCanonicalizePublicHTTPSURL(t *testing.T) {
	canonical, err := CanonicalizePublicHTTPSURL(
		" HTTPS://WWW.Example.COM:443/a/../b/?z=2&a=3&a=1#fragment ")
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "https://www.example.com/b/?a=1&a=3&z=2" {
		t.Fatalf("canonical URL=%q", canonical)
	}
	encodedPath, err := CanonicalizePublicHTTPSURL(
		"https://www.example.com/%70rivate/%2e%2e/public")
	if err != nil || encodedPath != "https://www.example.com/public" {
		t.Fatalf("encoded path canonical URL=%q err=%v", encodedPath, err)
	}
	unicodeHost, err := CanonicalizePublicHTTPSURL("https://BÜCHER.de/über")
	if err != nil || unicodeHost != "https://xn--bcher-kva.de/%C3%BCber" {
		t.Fatalf("unicode canonical URL=%q err=%v", unicodeHost, err)
	}
}

func TestCanonicalizePublicHTTPSURLRejectsSSRFTargets(t *testing.T) {
	for _, raw := range []string{
		"http://www.example.com/", "https://user:pass@www.example.com/",
		"https://localhost/", "https://service.internal/", "https://printer/",
		"https://service。internal/",
		"https://metadata.google.internal/computeMetadata/v1/",
		"https://169.254.169.254/latest/meta-data/", "https://127.0.0.1/",
		"https://10.0.0.1/", "https://100.64.0.1/", "https://198.18.0.1/",
		"https://[::1]/", "https://[fe80::1]/", "https://www.example.com:8443/",
		"https://www.example.com\\@127.0.0.1/",
		"https://www.example.com/private%5c..%5cpublic",
		"https://www.example.com/report?access_token=secret",
		"https://www.example.com/report?X-Amz-Signature=secret",
		"https://www.example.com/sk-abcdefghijklmnopqrstuvwxyz123456",
		"https://www.example.com/%73%6b%2dabcdefghijklmnopqrstuvwxyz123456",
		"https://www.example.com/?q=" + strings.Repeat("界", 1000),
	} {
		t.Run(strings.ReplaceAll(raw, "/", "_"), func(t *testing.T) {
			if canonical, err := CanonicalizePublicHTTPSURL(raw); err == nil {
				t.Fatalf("accepted forbidden URL as %q", canonical)
			}
		})
	}
}

func TestNetworkAuthorityIsExplicitAndFailClosed(t *testing.T) {
	disabled := NetworkAuthority{Mode: "disabled"}
	if _, err := disabled.Authorize("https://www.example.com/"); err == nil ||
		!strings.Contains(err.Error(), "web_evidence_network_disabled") {
		t.Fatalf("disabled authority err=%v", err)
	}
	authority := NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{
		"search.example.com", "*.docs.example.com",
	}}
	for _, allowed := range []string{
		"https://search.example.com/", "https://api.docs.example.com/guide",
	} {
		if _, err := authority.Authorize(allowed); err != nil {
			t.Fatalf("authorize %s: %v", allowed, err)
		}
	}
	for _, denied := range []string{
		"https://docs.example.com/", "https://evil-example.com/",
		"https://api.docs.example.com.evil.com/",
	} {
		if _, err := authority.Authorize(denied); err == nil {
			t.Fatalf("authorized denied URL %s", denied)
		}
	}
	public := NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}
	if _, err := public.Authorize("https://www.example.net/"); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeExactAuthorityTargetsCanonicalizesAndRejectsBroadGrants(t *testing.T) {
	targets, err := NormalizeExactAuthorityTargets([]string{
		" HTTPS://Docs.Example.COM:443/ ", "api.example.com", "docs.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(targets, ","); got != "api.example.com,docs.example.com" {
		t.Fatalf("canonical targets=%q", got)
	}
	for _, raw := range [][]string{
		nil,
		{""},
		{"*.example.com"},
		{PublicHTTPSTarget},
		{"http://docs.example.com"},
		{"https://docs.example.com/path"},
		{"localhost"},
	} {
		if normalized, err := NormalizeExactAuthorityTargets(raw); err == nil {
			t.Fatalf("accepted broad or invalid exact targets %#v as %#v", raw, normalized)
		}
	}
}

func TestIsPublicAddressRejectsSpecialRanges(t *testing.T) {
	for _, raw := range []string{"0.0.0.1", "100.64.0.1", "169.254.170.2",
		"192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"::1", "::192.168.1.1", "::ffff:0:192.168.1.1", "64:ff9b::a00:1",
		"64:ff9b:1::1", "100::1", "2001::1",
		"2001:db8::1", "2002::1", "3fff::1", "5f00::1", "fc00::1", "fec0::1",
		"ff02::1"} {
		if IsPublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("special address %s was public", raw)
		}
	}
	if !IsPublicAddress(netip.MustParseAddr("93.184.216.34")) ||
		!IsPublicAddress(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Fatal("expected public test addresses to be accepted")
	}
}
