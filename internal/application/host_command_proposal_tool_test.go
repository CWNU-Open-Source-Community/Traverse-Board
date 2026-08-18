package application

import (
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
