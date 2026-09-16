package application

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

func TestResolveProposalShellExecutableUsesRunnableWindowsDistributions(t *testing.T) {
	if runtime.GOOS != "windows" {
		if _, err := resolveProposalShellExecutable(
			toolgateway.HostCommandShellBash); !errors.Is(err, runner.ErrHostCommandPlatform) {
			t.Fatalf("non-Windows shell resolver error = %v", err)
		}
		return
	}
	powerShell, err := resolveProposalShellExecutable(
		toolgateway.HostCommandShellPowerShell)
	if err != nil {
		t.Fatalf("resolve PowerShell: %v", err)
	}
	if info, err := os.Lstat(powerShell); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("resolved PowerShell is not a regular file: %q (%v)", powerShell, err)
	}

	gitPath, err := filepath.Abs(findExecutableForTest(t, "git.exe"))
	if err != nil {
		t.Fatal(err)
	}
	gitBash := filepath.Clean(filepath.Join(filepath.Dir(gitPath), "..", "bin", "bash.exe"))
	if _, err := os.Lstat(gitBash); err != nil {
		t.Skipf("active Git distribution does not include Git Bash: %v", err)
	}
	bash, err := resolveProposalShellExecutable(toolgateway.HostCommandShellBash)
	if err != nil {
		t.Fatalf("resolve Git Bash: %v", err)
	}
	if !strings.EqualFold(filepath.Base(bash), "bash.exe") {
		t.Fatalf("resolved Bash executable = %q", bash)
	}
	if !strings.EqualFold(filepath.Clean(bash), gitBash) {
		t.Fatalf("resolved Bash %q is not from active Git distribution %q", bash, gitBash)
	}
	if systemRoot := strings.TrimSpace(os.Getenv("SystemRoot")); systemRoot != "" &&
		pathWithinRoot(bash, systemRoot) {
		t.Fatalf("legacy System32 WSL shim was accepted as Git Bash: %q", bash)
	}
	if !proposalExecutableRootAllowed(bash, t.TempDir()) {
		t.Fatalf("resolved Git Bash is outside the reviewed program roots: %q", bash)
	}
}

func TestResolveProposalPowerShellIgnoresPATHDecoy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell resolution")
	}
	decoyDirectory := t.TempDir()
	decoy := filepath.Join(decoyDirectory, "powershell.exe")
	if err := os.WriteFile(decoy, []byte("not PowerShell"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", decoyDirectory)
	resolved, err := resolveProposalShellExecutable(
		toolgateway.HostCommandShellPowerShell)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.Clean(resolved), filepath.Clean(decoy)) {
		t.Fatal("PowerShell resolver accepted a PATH-controlled executable")
	}
}

func TestHostProposalPowerShellUsesOnlyExactConfiguredRuntime(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell resolution")
	}
	// Copy a native PE image only to test resolution and identity. This fixture
	// is never started and does not claim PowerShell runtime compatibility.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	installation := t.TempDir()
	executable := filepath.Join(installation, "pwsh.exe")
	if err := os.WriteFile(executable, image, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
	resolved, err := resolveProposalShellExecutable(toolgateway.HostCommandShellPowerShell)
	if err != nil || !strings.EqualFold(resolved, executable) {
		t.Fatalf("configured PowerShell was not selected: %q (%v)", resolved, err)
	}
	path, digest, err := proposalExecutableIdentity(resolved, workspace)
	want := sha256.Sum256(image)
	if err != nil || path != resolved || digest != hex.EncodeToString(want[:]) {
		t.Fatalf("configured runtime identity was not pinned: %q %q (%v)", path, digest, err)
	}
	for _, untrusted := range []string{
		filepath.Join(installation, "other.exe"),
		filepath.Join(filepath.Dir(installation), "sibling", "pwsh.exe"),
	} {
		if proposalExecutableRootAllowed(untrusted, workspace) {
			t.Fatalf("configured runtime expanded trust to %q", untrusted)
		}
	}
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", "")
	if proposalExecutableRootAllowed(executable, workspace) {
		t.Fatal("removed host configuration kept trusting the portable executable")
	}
}

func TestHostProposalInvalidConfiguredPowerShellDoesNotFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell resolution")
	}
	textImage := filepath.Join(t.TempDir(), "pwsh.exe")
	if err := os.WriteFile(textImage, []byte(strings.Repeat("not a native image", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"pwsh.exe", "  ", `"C:\Tools\PowerShell\pwsh.exe"`,
		`\\server\share\pwsh.exe`, `\\?\C:\Tools\pwsh.exe`,
		filepath.Join(t.TempDir(), "cmd.exe"),
		filepath.Join(t.TempDir(), "pwsh.exe"), textImage,
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CYBERAGENT_POWERSHELL_PATH", value)
			resolved, err := resolveProposalShellExecutable(toolgateway.HostCommandShellPowerShell)
			if err == nil || resolved != "" {
				t.Fatalf("invalid explicit runtime silently fell back: %q (%v)", resolved, err)
			}
			if proposalExecutableRootAllowed(value, t.TempDir()) {
				t.Fatalf("invalid explicit runtime became a trusted executable: %q", value)
			}
		})
	}
}

func TestGitForWindowsDistributionRootRejectsParentExpansion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Git for Windows path semantics")
	}
	root := t.TempDir()
	direct := filepath.Join(root, "git.exe")
	if err := os.WriteFile(direct, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if value, ok := gitForWindowsDistributionRoot(direct); ok {
		t.Fatalf("unstructured git.exe expanded its parent trust root to %q", value)
	}
	commandDirectory := filepath.Join(root, "cmd")
	if err := os.MkdirAll(commandDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	structured := filepath.Join(commandDirectory, "git.exe")
	if err := os.WriteFile(structured, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, ok := gitForWindowsDistributionRoot(structured)
	if !ok || !strings.EqualFold(value, root) {
		t.Fatalf("verified Git for Windows layout root=%q ok=%t", value, ok)
	}
}

func TestPortableGitTrustsOnlyTheSelectedBashExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Git for Windows path semantics")
	}
	root := t.TempDir()
	commandDirectory := filepath.Join(root, "cmd")
	binDirectory := filepath.Join(root, "bin")
	otherDirectory := filepath.Join(root, "usr", "bin")
	for _, directory := range []string{commandDirectory, binDirectory, otherDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(commandDirectory, "git.exe"),
		filepath.Join(binDirectory, "bash.exe"),
		filepath.Join(otherDirectory, "unreviewed.exe"),
	} {
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", commandDirectory)
	if !proposalExecutableRootAllowed(filepath.Join(binDirectory, "bash.exe"), t.TempDir()) {
		t.Fatal("selected portable Git Bash was not trusted")
	}
	if proposalExecutableRootAllowed(filepath.Join(otherDirectory, "unreviewed.exe"), t.TempDir()) {
		t.Fatal("portable Git distribution root widened trust to another executable")
	}
}

func findExecutableForTest(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is unavailable: %v", name, err)
	}
	return path
}
