package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	result := waitWindowsProfileTestProcess(process, 5*time.Second)
	t.Logf("PowerShell 5 profile startup: %s", result)
	if result.watchdog || result.waitErr != nil || result.killErr != nil ||
		result.stdout.err != nil || result.stderr.err != nil || result.exitCode != 0 ||
		!strings.Contains(string(result.stdout.value), "powershell-5-profile-ready") ||
		!strings.Contains(string(result.stdout.value), profile) {
		t.Fatalf("real Windows PowerShell 5 startup failed: %s", result)
	}
}

func TestCommandRuntimeWindowsPowerShell5ProfileWatchdogReapsProcess(t *testing.T) {
	windowsRoot, err := controlledWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", filepath.Join(windowsRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
	spec := commandRuntimeTestPowerShellSpec()
	spec.Script = "Start-Sleep -Seconds 30"
	resolved, err := NormalizeCommandRuntimeSpec(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	process, err := newPlatformCommandRuntimeStarter().Start(t.Context(), CommandRuntimeScope{}, resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	result := waitWindowsProfileTestProcess(process, 100*time.Millisecond)
	// Wait verifies that the Job Object has no surviving child processes.
	if !result.watchdog || result.exitCode != commandRuntimeWindowsExitCode ||
		result.waitErr != nil || result.killErr != nil || result.stdout.err != nil || result.stderr.err != nil {
		t.Fatalf("test watchdog did not terminate and reap the owned process: %s", result)
	}
	t.Logf("test watchdog termination: %s", result)
}

type windowsProfileTestOutput struct {
	value []byte
	err   error
}

type windowsProfileTestResult struct {
	stdout, stderr windowsProfileTestOutput
	exitCode       int
	waitErr        error
	killErr        error
	watchdog       bool
	elapsed        time.Duration
}

func (r windowsProfileTestResult) String() string {
	return fmt.Sprintf("elapsed=%s watchdog_triggered=%t exit=%d wait=%v kill=%v stdout=%q stdout_read=%v stderr=%q stderr_read=%v",
		r.elapsed, r.watchdog, r.exitCode, r.waitErr, r.killErr,
		r.stdout.value, r.stdout.err, r.stderr.value, r.stderr.err)
}

// Keep the existing safety budget and report test termination separately from a
// shell exit, including the termination and output collection errors.
func waitWindowsProfileTestProcess(process commandRuntimeProcess, budget time.Duration) windowsProfileTestResult {
	started := time.Now()
	stdoutDone, stderrDone := make(chan windowsProfileTestOutput, 1), make(chan windowsProfileTestOutput, 1)
	go func() { value, err := io.ReadAll(process.Stdout()); stdoutDone <- windowsProfileTestOutput{value, err} }()
	go func() { value, err := io.ReadAll(process.Stderr()); stderrDone <- windowsProfileTestOutput{value, err} }()
	waitDone := make(chan windowsProfileTestResult, 1)
	go func() {
		code, err := process.Wait()
		waitDone <- windowsProfileTestResult{exitCode: code, waitErr: err}
	}()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	var result windowsProfileTestResult
	select {
	case result = <-waitDone:
	case <-timer.C:
		// A completed Wait takes precedence if scheduler delay made both
		// channels ready before this goroutine could receive either one.
		select {
		case result = <-waitDone:
		default:
			killErr := process.Kill()
			if killErr != nil {
				_ = process.Close()
			}
			result = <-waitDone
			result.watchdog, result.killErr = true, killErr
		}
	}
	result.stdout, result.stderr = <-stdoutDone, <-stderrDone
	result.elapsed = time.Since(started)
	return result
}

func TestCommandRuntimeWindowsPowerShell7KeepsExistingProfileEnvironment(t *testing.T) {
	// Pinning-only fixture: the separate PS5 regression launches the real shell.
	// Windows TEMP can use an ancestor alias or an 8.3 spelling. Create the
	// trusted fixture at its canonical path, as the production resolver requires.
	runtimeRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable := commandRuntimeTestPowerShellImage(t, runtimeRoot, "pwsh.exe")
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
	resolved, err := NormalizeCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+strings.Join(resolved.Environment, "\n")+"\n", "\nUSERPROFILE=\n") {
		t.Fatal("PowerShell 7's existing environment was changed by the PS5 compatibility fix")
	}
}
