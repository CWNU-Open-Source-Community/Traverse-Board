//go:build windows

package browserruntime

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestBrowserNetworkProbeRunFailureCodesAreSpecificAndBounded(t *testing.T) {
	tests := []struct {
		phase string
		err   error
		want  string
	}{
		{"baseline", errBrowserNetworkProbeProcessExited,
			"baseline_browser_exited_before_canaries"},
		{"baseline", context.DeadlineExceeded, "baseline_canary_timeout"},
		{"restricted", context.Canceled, "restricted_probe_cancelled"},
		{"restricted", errors.New("fixture"), "restricted_runtime_failed"},
	}
	for _, test := range tests {
		if got := browserNetworkProbeRunFailureCode(test.phase, test.err); got != test.want {
			t.Fatalf("failure code = %q, want %q", got, test.want)
		}
	}
}

func TestBrowserNetworkProbeArgumentsNeverOpenCDPOrCallerNetworkOptions(t *testing.T) {
	arguments := fixedBrowserNetworkProbeArguments(`C:\probe-profile`,
		"http://127.0.0.1:18080/probe?token="+strings.Repeat("a", 24),
		[]windowsWFPRemoteTarget{
			{Address: netip.MustParseAddr("127.0.0.1"), Port: 18080},
			{Address: netip.MustParseAddr("127.0.0.2"), Port: 18081},
			{Address: netip.MustParseAddr("::1"), Port: 18082},
		})
	joined := strings.Join(arguments, " ")
	for _, forbidden := range []string{"remote-debugging", "disable-web-security",
		"ignore-certificate-errors", "allow-running-insecure-content", "--proxy-server"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("browser network probe arguments contain %q: %v", forbidden, arguments)
		}
	}
	if strings.Count(joined, "http://") != 1 ||
		!strings.Contains(joined, "--host-resolver-rules=MAP * ~NOTFOUND, "+
			"EXCLUDE 127.0.0.1, EXCLUDE 127.0.0.2, EXCLUDE ::1") {
		t.Fatalf("browser network probe arguments widened: %v", arguments)
	}
}

func TestBrowserNetworkProbeCleanupOnlyRemovesItsExactTemporaryProfile(t *testing.T) {
	profilePath, err := os.MkdirTemp("", browserNetworkProbeProfilePrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath+`\marker`, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupBrowserNetworkProbeProfile(profilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(profilePath); !os.IsNotExist(err) {
		t.Fatalf("exact browser network probe Profile still exists: %v", err)
	}
	foreign := t.TempDir()
	if err := removeBrowserNetworkProbeProfile(foreign); err == nil {
		t.Fatal("foreign temporary directory was accepted as a browser probe Profile")
	}
}

func TestInstalledEdgeBrowserNetworkProbeBaselineSmoke(t *testing.T) {
	if os.Getenv("CYBERAGENT_BROWSER_PROBE_SMOKE") != "1" {
		t.Skip("set CYBERAGENT_BROWSER_PROBE_SMOKE=1 to exercise the installed Edge runtime")
	}
	identities, err := DiscoverInstalledBrowsers()
	if err != nil {
		t.Fatal(err)
	}
	var identity BrowserExecutableIdentity
	for _, candidate := range identities {
		if candidate.Product == BrowserProductEdge &&
			candidate.Channel == BrowserChannelStable {
			identity = candidate
			break
		}
	}
	if identity.Fingerprint == "" {
		t.Skip("stable Edge is not installed")
	}
	harness, err := newBrowserNetworkProbeHarness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer harness.Close()
	token := strings.Repeat("a", 24)
	err = runBrowserNetworkProbePhase(context.Background(), identity, harness, token, true)
	observation := harness.Observe(token)
	if err != nil {
		t.Fatalf("installed Edge baseline failed: %v; observation=%+v", err, observation)
	}
	if !observation.All() {
		t.Fatalf("installed Edge baseline incomplete: %+v", observation)
	}
}
