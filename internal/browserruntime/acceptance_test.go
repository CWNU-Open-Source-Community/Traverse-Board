package browserruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBrowserAcceptanceBindsPublisherToSameOpenHandleWithoutStart(t *testing.T) {
	identity := browserIdentityFixture(t, BrowserProductChrome, BrowserChannelStable)
	probe := func(file *os.File, path string) (AuthenticodeEvidence, error) {
		if file == nil || path != identity.CanonicalPath {
			t.Fatal("publisher probe did not receive the exact open candidate")
		}
		return AuthenticodeEvidence{
			Source: AuthenticodeSourceWindows, Publisher: "Google LLC",
			CertificateSHA256: strings.Repeat("a", 64), SignatureVerified: true,
			SameOpenHandleVerified: true, CacheOnlyVerification: true,
		}, nil
	}
	candidate, err := buildBrowserAcceptanceCandidate(identity, probe)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Decision != BrowserAcceptanceAccepted ||
		candidate.ReasonCode != BrowserAcceptanceReasonPublisherVerified ||
		!candidate.ReviewEligible || !candidate.Evidence.PublisherPolicyMatched ||
		!candidate.Evidence.PublisherEvidenceComplete ||
		!candidate.SameHandleBytesRevalidated || !candidate.SameFilePathRevalidated ||
		!candidate.PERevalidated || !candidate.StartBlocked ||
		candidate.LaunchTrustComplete || candidate.ProcessStartEnabled ||
		candidate.ProductLaunchEnabled || candidate.Authority != (RuntimeAuthority{}) {
		t.Fatalf("browser acceptance widened authority: %#v", candidate)
	}
	if err := ValidateBrowserAcceptanceCandidate(candidate, identity); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserAcceptanceRefusesUnknownAndUnsupportedPublishers(t *testing.T) {
	tests := []struct {
		product   BrowserProduct
		publisher string
		reason    BrowserAcceptanceReason
	}{
		{BrowserProductEdge, "Example Corporation", BrowserAcceptanceReasonPublisherMismatch},
		{BrowserProductChromium, "Chromium", BrowserAcceptanceReasonPublisherUnsupported},
	}
	for _, test := range tests {
		t.Run(string(test.product), func(t *testing.T) {
			identity := browserIdentityFixture(t, test.product, BrowserChannelStable)
			candidate, err := buildBrowserAcceptanceCandidate(identity,
				func(*os.File, string) (AuthenticodeEvidence, error) {
					return AuthenticodeEvidence{
						Source: AuthenticodeSourceWindows, Publisher: test.publisher,
						CertificateSHA256: strings.Repeat("b", 64), SignatureVerified: true,
						SameOpenHandleVerified: true, CacheOnlyVerification: true,
					}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Decision != BrowserAcceptanceRefused ||
				candidate.ReasonCode != test.reason || candidate.ReviewEligible {
				t.Fatalf("publisher refusal was not preserved: %#v", candidate)
			}
		})
	}
}

func TestBrowserAcceptanceRejectsDriftAndTampering(t *testing.T) {
	identity := browserIdentityFixture(t, BrowserProductChrome, BrowserChannelStable)
	changed := false
	_, err := buildBrowserAcceptanceCandidate(identity,
		func(*os.File, string) (AuthenticodeEvidence, error) {
			raw, readErr := os.ReadFile(identity.CanonicalPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			raw[len(raw)-1] ^= 0xff
			if writeErr := os.WriteFile(identity.CanonicalPath, raw, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			changed = true
			return AuthenticodeEvidence{
				Source: AuthenticodeSourceWindows, Publisher: "Google LLC",
				CertificateSHA256: strings.Repeat("c", 64), SignatureVerified: true,
				SameOpenHandleVerified: true, CacheOnlyVerification: true,
			}, nil
		})
	if !changed || err == nil {
		t.Fatalf("same-handle byte drift unexpectedly passed: %v", err)
	}

	identity = browserIdentityFixture(t, BrowserProductChrome, BrowserChannelStable)
	candidate, err := buildBrowserAcceptanceCandidate(identity,
		func(*os.File, string) (AuthenticodeEvidence, error) {
			return AuthenticodeEvidence{
				Source: AuthenticodeSourceWindows, Publisher: "Google LLC",
				CertificateSHA256: strings.Repeat("d", 64), SignatureVerified: true,
				SameOpenHandleVerified: true, CacheOnlyVerification: true,
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	candidate.ProcessStartEnabled = true
	if err := ValidateBrowserAcceptanceCandidate(candidate, identity); err == nil {
		t.Fatal("authorizing browser acceptance mutation unexpectedly passed")
	}
}

func TestInstalledBrowserAcceptanceSmoke(t *testing.T) {
	if os.Getenv("CYBERAGENT_BROWSER_DISCOVERY_SMOKE") != "1" {
		t.Skip("set CYBERAGENT_BROWSER_DISCOVERY_SMOKE=1 for local read-only browser acceptance")
	}
	if runtime.GOOS != "windows" {
		t.Skip("Windows Authenticode acceptance is only available on Windows")
	}
	identities, err := DiscoverInstalledBrowsers()
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range identities {
		candidate, buildErr := BuildBrowserAcceptanceCandidate(identity)
		if buildErr != nil {
			t.Fatalf("acceptance %s: %v", identity.CanonicalPath, buildErr)
		}
		if err := ValidateBrowserAcceptanceCandidate(candidate, identity); err != nil {
			t.Fatalf("validate acceptance %s: %v", identity.CanonicalPath, err)
		}
		if !candidate.StartBlocked || candidate.ProcessStartEnabled ||
			candidate.ProductLaunchEnabled || candidate.Authority != (RuntimeAuthority{}) {
			t.Fatalf("local acceptance widened authority: %#v", candidate)
		}
		t.Logf("%s %s: decision=%s publisher=%q signature_verified=%t",
			identity.Product, identity.Channel, candidate.Decision,
			candidate.Evidence.Publisher,
			candidate.Evidence.SignatureVerified)
	}
	t.Logf("inspected %d browser executable candidate(s) without starting a process", len(identities))
}

func browserIdentityFixture(t *testing.T, product BrowserProduct,
	channel BrowserChannel,
) BrowserExecutableIdentity {
	t.Helper()
	root := t.TempDir()
	spec := knownSpec(t, DiscoveryRootProgramFiles, product, channel)
	path := filepath.Join(append([]string{root}, spec.Components...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, minimalPEImage(t, "amd64"), 0o600); err != nil {
		t.Fatal(err)
	}
	identities, err := discoverBrowserExecutables([]DiscoveryRoot{
		{ID: DiscoveryRootProgramFiles, Path: root},
	}, []browserExecutableSpec{spec}, browserExecutableVersion)
	if err != nil || len(identities) != 1 {
		t.Fatalf("discover browser fixture: count=%d err=%v", len(identities), err)
	}
	return identities[0]
}
