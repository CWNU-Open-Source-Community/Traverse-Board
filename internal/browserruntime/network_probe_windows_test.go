//go:build windows

package browserruntime

import (
	"os"
	"strings"
	"testing"
)

func TestBrowserNetworkProbeArgumentsNeverOpenCDPOrCallerNetworkOptions(t *testing.T) {
	arguments := fixedBrowserNetworkProbeArguments(`C:\probe-profile`,
		"http://127.0.0.1:18080/probe?token="+strings.Repeat("a", 24))
	joined := strings.Join(arguments, " ")
	for _, forbidden := range []string{"remote-debugging", "disable-web-security",
		"ignore-certificate-errors", "allow-running-insecure-content", "--proxy-server"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("browser network probe arguments contain %q: %v", forbidden, arguments)
		}
	}
	if strings.Count(joined, "http://") != 1 ||
		!strings.Contains(joined, "--host-resolver-rules=MAP * ~NOTFOUND") {
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
