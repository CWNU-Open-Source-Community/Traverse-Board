//go:build windows

package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalSandboxPowerShellCommandBodyExitSemantics(t *testing.T) {
	executable, err := resolveCommandRuntimeShell(CommandRuntimePowerShell)
	if err != nil || !strings.EqualFold(filepath.Base(executable), "pwsh.exe") {
		t.Skipf("trusted PowerShell 7 is unavailable; configure CYBERAGENT_POWERSHELL_PATH to run the real shell regression: %v", err)
	}
	windowsRoot, err := controlledWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(windowsRoot, "System32", "cmd.exe")
	nativeFailure := "& '" + strings.ReplaceAll(cmdPath, "'", "''") + "' /d /c 'exit 7'"
	for _, test := range []struct {
		name, script, stdout, stderr string
		exit                         int
		file                         bool
		rejected                     bool
	}{
		{name: "last Write-Error", script: "Write-Error 'synthetic failure'", stderr: "synthetic failure", exit: 1},
		{name: "Write-Error then return", script: "Write-Error 'synthetic failure'; return", stderr: "synthetic failure", exit: 1},
		{name: "native failure then return", script: nativeFailure + "; return", exit: 1},
		{name: "handled continuation", script: "Write-Error 'synthetic failure'; Write-Output 'continued'", stdout: "continued", stderr: "synthetic failure", exit: 0},
		{name: "explicit exit", script: "exit 23", exit: 23},
		{name: "inline param rejected", script: "param([string]$Value = 'must-not-execute'); Write-Output $Value", rejected: true},
		{name: "file using and param", script: "& ./declared.ps1", stdout: "declared utf-8", exit: 0, file: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.file {
				body := "using namespace System.Text\nparam([string]$Value = 'declared')\n[Console]::Out.WriteLine($Value + ' ' + [UTF8Encoding]::new().WebName)\n"
				if err := os.WriteFile(filepath.Join(root, "declared.ps1"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			spec := commandRuntimeTestPowerShellSpec()
			spec.Script = test.script
			resolved, err := NormalizeLocalSandboxCommandRuntimeSpec(spec, root)
			if test.rejected {
				if !errors.Is(err, ErrCommandRuntimeBoundary) || !strings.Contains(err.Error(), "inline PowerShell param/using declarations") || len(resolved.CanonicalArgv) != 0 {
					t.Fatalf("inline declaration was not rejected before launch: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Execute the production canonical argv using fixed synthetic input.
			// This host test checks language/exit behavior, not LPAC authority.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, resolved.ExecutablePath, resolved.CanonicalArgv...)
			command.Dir = resolved.AbsoluteDirectory
			command.Env = resolved.Environment
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err = command.Run()
			if ctx.Err() != nil {
				t.Fatalf("synthetic PowerShell process timed out: %v", ctx.Err())
			}
			var exitError *exec.ExitError
			if err != nil && !errors.As(err, &exitError) {
				t.Fatal(err)
			}
			if command.ProcessState == nil || command.ProcessState.ExitCode() != test.exit {
				t.Fatalf("exit=%v want=%d stdout=%q stderr=%q", command.ProcessState, test.exit, stdout.String(), stderr.String())
			}
			if strings.TrimSpace(stdout.String()) != test.stdout ||
				!strings.Contains(stderr.String(), test.stderr) {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestLocalSandboxRejectsSystemPowerShellBeforeLaunchSpec(t *testing.T) {
	windowsRoot, err := controlledWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(windowsRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(executable); err != nil {
		t.Skipf("system Windows PowerShell is unavailable: %v", err)
	}
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
	resolved, err := NormalizeLocalSandboxCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), t.TempDir())
	if !errors.Is(err, ErrCommandRuntimeLocalPowerShell) ||
		resolved.ExecutablePath != "" || len(resolved.CanonicalArgv) != 0 {
		t.Fatalf("system PS5 produced a Local launch specification: executable=%q argv=%q error=%v", resolved.ExecutablePath, resolved.CanonicalArgv, err)
	}
}
