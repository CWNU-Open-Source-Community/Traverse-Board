package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandRuntimeWindowsPowerShell5BindsWorkspaceProfileBeforeLaunch(t *testing.T) {
	windowsRoot, err := controlledWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(windowsRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
	t.Setenv("USERPROFILE", t.TempDir())
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := commandRuntimeTestPowerShellSpec()
	spec.WorkingDirectory = "work"
	spec.Script = "[Console]::Out.WriteLine('powershell-5-profile-ready'); [Console]::Out.WriteLine($env:USERPROFILE)"
	resolved, err := NormalizeCommandRuntimeSpec(spec, filepath.Join(root, "work", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(resolved.CanonicalArgv, "|"), "|-NoProfile|") ||
		resolved.ProfileStartupFiles || resolved.EnvironmentInherited || len(resolved.Spec.Environment) != 0 {
		t.Fatalf("profile binding changed the startup or caller environment contract: %+v", resolved)
	}
	profile := ""
	for _, entry := range resolved.Environment {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "USERPROFILE") {
			profile = value
		}
		if strings.EqualFold(name, "HOME") && value != "" {
			t.Fatalf("HOME boundary changed: %q", entry)
		}
	}
	if profile != resolved.WorkspaceRoot || commandRuntimePathEqual(profile, resolved.AbsoluteDirectory) ||
		commandRuntimePathEqual(profile, os.Getenv("USERPROFILE")) {
		t.Fatalf("profile did not bind the canonical workspace root: %q root=%q cwd=%q", profile, resolved.WorkspaceRoot, resolved.AbsoluteDirectory)
	}
	encoded, _ := json.Marshal(resolved.Environment)
	digest := sha256.Sum256(encoded)
	if resolved.EnvironmentSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("stored environment digest does not describe the launched environment")
	}
	beforeBinding := resolved
	beforeBinding.Environment = replaceCommandRuntimeEnvironment(append([]string(nil), resolved.Environment...), "USERPROFILE", "")
	encoded, _ = json.Marshal(beforeBinding.Environment)
	digest = sha256.Sum256(encoded)
	beforeBinding.EnvironmentSHA256 = hex.EncodeToString(digest[:])
	if CommandRuntimeSpecFingerprint(beforeBinding) == CommandRuntimeSpecFingerprint(resolved) {
		t.Fatal("profile binding was omitted from the replay fingerprint")
	}
	spec.Environment = []CommandRuntimeEnvironment{{Name: "UserProfile", Value: t.TempDir()}}
	if _, err := NormalizeCommandRuntimeSpec(spec, root); !errors.Is(err, ErrCommandRuntimeBoundary) {
		t.Fatalf("caller could override the bound profile: %v", err)
	}
	process, err := newPlatformCommandRuntimeStarter().Start(context.Background(), CommandRuntimeScope{}, resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	timer := time.AfterFunc(5*time.Second, func() { _ = process.Kill() })
	defer timer.Stop()
	stdoutDone, stderrDone := make(chan []byte, 1), make(chan []byte, 1)
	go func() { value, _ := io.ReadAll(process.Stdout()); stdoutDone <- value }()
	go func() { value, _ := io.ReadAll(process.Stderr()); stderrDone <- value }()
	exitCode, waitErr := process.Wait()
	stdout, stderr := <-stdoutDone, <-stderrDone
	if waitErr != nil || exitCode != 0 ||
		!strings.Contains(string(stdout), "powershell-5-profile-ready") || !strings.Contains(string(stdout), profile) {
		t.Fatalf("real Windows PowerShell 5 startup failed: stdout=%q stderr=%q exit=%d err=%v", stdout, stderr, exitCode, waitErr)
	}
}

func TestCommandRuntimeWindowsPowerShell7KeepsExistingProfileEnvironment(t *testing.T) {
	// Pinning-only fixture: the separate PS5 regression launches the real shell.
	executable := commandRuntimeTestPowerShellImage(t, t.TempDir(), "pwsh.exe")
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
	resolved, err := NormalizeCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+strings.Join(resolved.Environment, "\n")+"\n", "\nUSERPROFILE=\n") {
		t.Fatal("PowerShell 7's existing environment was changed by the PS5 compatibility fix")
	}
}
