//go:build windows

package runner

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func TestCommandRuntimeWindowsExplicitPowerShellPinsHostSelection(t *testing.T) {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		t.Run(name, func(t *testing.T) {
			executable := commandRuntimeTestPowerShellImage(t, t.TempDir(), name)
			t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
			resolved, err := NormalizeCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			wantSHA, err := commandRuntimeFileSHA256(executable)
			if err != nil {
				t.Fatal(err)
			}
			if !commandRuntimePathEqual(resolved.ExecutablePath, executable) ||
				resolved.ExecutableSHA256 != wantSHA || !resolved.ExecutablePinned ||
				len(resolved.CanonicalArgv) != 8 ||
				strings.Join(resolved.CanonicalArgv[:6], "|") != "-NoLogo|-NoProfile|-NonInteractive|-OutputFormat|Text|-Command" ||
				resolved.CanonicalArgv[6] != hostPowerShellUTF8Bootstrap ||
				resolved.CanonicalArgv[7] != "Write-Output runtime-selection" {
				t.Fatalf("explicit host runtime was not pinned: path=%q sha=%q argv=%q",
					resolved.ExecutablePath, resolved.ExecutableSHA256, resolved.CanonicalArgv)
			}
			for _, entry := range resolved.Environment {
				if strings.HasPrefix(strings.ToUpper(entry), "CYBERAGENT_POWERSHELL_PATH=") {
					t.Fatal("host runtime selection leaked into the child environment")
				}
			}
			spec := commandRuntimeTestPowerShellSpec()
			spec.Environment = []CommandRuntimeEnvironment{{Name: "CYBERAGENT_POWERSHELL_PATH", Value: executable}}
			if _, err := NormalizeCommandRuntimeSpec(spec, t.TempDir()); !errors.Is(err, ErrCommandRuntimeBoundary) {
				t.Fatalf("per-command host runtime configuration was accepted: %v", err)
			}
		})
	}
}

func TestCommandRuntimeWindowsExplicitPowerShellInvalidDoesNotFallback(t *testing.T) {
	root := t.TempDir()
	textImage := filepath.Join(t.TempDir(), "pwsh.exe")
	if err := os.WriteFile(textImage, []byte("not a native image"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path string
		want       error
	}{
		{"relative", `pwsh.exe`, ErrCommandRuntimeBoundary},
		{"blank", "  ", ErrCommandRuntimeBoundary},
		{"quoted", `"C:\Tools\PowerShell\pwsh.exe"`, ErrCommandRuntimeBoundary},
		{"wrong name", filepath.Join(t.TempDir(), "cmd.exe"), ErrCommandRuntimeBoundary},
		{"network", `\\server\share\pwsh.exe`, ErrCommandRuntimeBoundary},
		{"device", `\\?\C:\Tools\pwsh.exe`, ErrCommandRuntimeBoundary},
		{"missing", filepath.Join(t.TempDir(), "pwsh.exe"), ErrCommandRuntimeUnavailable},
		{"not native", textImage, ErrCommandRuntimeBoundary},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CYBERAGENT_POWERSHELL_PATH", test.path)
			if _, err := NormalizeCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), root); !errors.Is(err, test.want) {
				t.Fatalf("invalid explicit runtime fell back or returned wrong error: %v", err)
			}
		})
	}
}

func TestCommandRuntimeWindowsExplicitPowerShellRejectsWorkspaceAndAlias(t *testing.T) {
	root := t.TempDir()
	executable := commandRuntimeTestPowerShellImage(t, root, "pwsh.exe")
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", executable)
	if _, err := NormalizeCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), root); !errors.Is(err, ErrCommandRuntimeBoundary) {
		t.Fatalf("project executable selected as runtime: %v", err)
	}
	t.Run("ancestor alias", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "runtime-link")
		if err := os.Symlink(root, link); err != nil {
			windowsRoot, rootErr := controlledWindowsDirectory()
			if rootErr != nil {
				t.Fatal(rootErr)
			}
			command := exec.Command(filepath.Join(windowsRoot, "System32", "cmd.exe"),
				"/d", "/c", "mklink", "/J", link, root)
			if output, junctionErr := command.CombinedOutput(); junctionErr != nil {
				t.Skipf("host cannot create an ancestor alias: symlink=%v junction=%v output=%s", err, junctionErr, output)
			}
		}
		if _, err := os.Stat(filepath.Join(link, "pwsh.exe")); err != nil {
			t.Fatalf("ancestor alias does not reach the test executable: %v", err)
		}
		t.Setenv("CYBERAGENT_POWERSHELL_PATH", filepath.Join(link, "pwsh.exe"))
		if _, err := NormalizeCommandRuntimeSpec(commandRuntimeTestPowerShellSpec(), root); !errors.Is(err, ErrCommandRuntimeBoundary) && !errors.Is(err, ErrCommandRuntimeUnavailable) {
			t.Fatalf("runtime alias bypassed the project boundary: %v", err)
		}
	})
}

func TestCommandRuntimeWindowsPowerShellDefaultDoesNotUsePath(t *testing.T) {
	t.Setenv("CYBERAGENT_POWERSHELL_PATH", "")
	before, beforeErr := resolveCommandRuntimeShell(CommandRuntimePowerShell)
	pathRoot := t.TempDir()
	commandRuntimeTestPowerShellImage(t, pathRoot, "pwsh.exe")
	t.Setenv("PATH", pathRoot)
	after, afterErr := resolveCommandRuntimeShell(CommandRuntimePowerShell)
	if !commandRuntimePathEqual(before, after) || !errors.Is(afterErr, beforeErr) {
		t.Fatalf("PATH changed default PowerShell: before=%q %v after=%q %v", before, beforeErr, after, afterErr)
	}
}

func commandRuntimeTestPowerShellSpec() CommandRuntimeSpec {
	return CommandRuntimeSpec{
		Version: CommandRuntimeProtocolVersion, Profile: CommandRuntimePowerShell,
		Script: "Write-Output runtime-selection", WorkingDirectory: ".",
		Environment: []CommandRuntimeEnvironment{},
		StdinPolicy: CommandRuntimeStdinClosed, CloseInitialStdin: true,
		TimeoutMilliseconds: 1000,
		Output: CommandRuntimeOutputPolicy{InlineBytes: MinCommandRuntimeInlineBytes,
			ArtifactBytes: MinCommandRuntimeInlineBytes},
		Network: CommandRuntimeNetworkDisabled, Credentials: CommandRuntimeCredentialsNone,
		Purpose: "validate trusted PowerShell selection without starting a process",
	}
}

func commandRuntimeTestPowerShellImage(t *testing.T, root, name string) string {
	t.Helper()
	// This PE fixture exercises resolution and pinning only; it is never launched
	// as PowerShell and is not evidence of shell or LPAC compatibility.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, value, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandRuntimeProcessExecutableOnAnotherVolumeIsOutsideWorkspace(t *testing.T) {
	outside, err := commandRuntimeExecutableOutsideWorkspace(
		`C:\Program Files\Go\bin\go.exe`, `D:\standard-code-attack-181\runtime`)
	if err != nil || !outside {
		t.Fatalf("cross-volume executable outside=%t err=%v", outside, err)
	}
	outside, err = commandRuntimeExecutableOutsideWorkspace(
		`D:\standard-code-attack-181\runtime\tool.exe`,
		`D:\standard-code-attack-181\runtime`)
	if err != nil || outside {
		t.Fatalf("workspace executable outside=%t err=%v", outside, err)
	}
}

func TestCommandRuntimeWindowsPowerShell5PowerShell7AndGitBashSmoke(t *testing.T) {
	root := t.TempDir()
	var powershell5 string
	if windowsRoot, err := controlledWindowsDirectory(); err == nil {
		powershell5 = filepath.Join(windowsRoot, "System32", "WindowsPowerShell", "v1.0",
			"powershell.exe")
	}
	var powershell7 string
	for _, programFiles := range controlledKnownFolders(windows.FOLDERID_ProgramFiles,
		windows.FOLDERID_ProgramFilesX64, windows.FOLDERID_ProgramFilesX86) {
		candidate := filepath.Join(programFiles, "PowerShell", "7", "pwsh.exe")
		if commandRuntimeRegularFile(candidate) {
			powershell7 = candidate
			break
		}
	}
	gitBash, _ := resolveCommandRuntimeShell(CommandRuntimeBash)
	for _, test := range []struct {
		name       string
		profile    CommandRuntimeProfile
		executable string
		script     string
	}{
		{name: "Windows PowerShell 5", profile: CommandRuntimePowerShell,
			executable: powershell5, script: "[Console]::Out.WriteLine('powershell-5-smoke')"},
		{name: "PowerShell 7", profile: CommandRuntimePowerShell,
			executable: powershell7, script: "[Console]::Out.WriteLine('powershell-7-smoke')"},
		{name: "Git Bash", profile: CommandRuntimeBash,
			executable: gitBash, script: "printf 'git-bash-smoke\\n'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !commandRuntimeRegularFile(test.executable) {
				t.Skipf("%s is unavailable", test.name)
			}
			resolved, err := NormalizeCommandRuntimeSpec(CommandRuntimeSpec{
				Version: CommandRuntimeProtocolVersion, Profile: test.profile,
				Script: test.script, WorkingDirectory: ".",
				Environment: []CommandRuntimeEnvironment{},
				StdinPolicy: CommandRuntimeStdinClosed, CloseInitialStdin: true,
				TimeoutMilliseconds: 5000,
				Output: CommandRuntimeOutputPolicy{InlineBytes: MinCommandRuntimeInlineBytes,
					ArtifactBytes: MinCommandRuntimeInlineBytes},
				Network:     CommandRuntimeNetworkDisabled,
				Credentials: CommandRuntimeCredentialsNone, Purpose: test.name + " smoke",
			}, root)
			if err != nil {
				t.Fatal(err)
			}
			resolved.ExecutablePath = filepath.Clean(test.executable)
			resolved.ExecutableSHA256, err = commandRuntimeFileSHA256(resolved.ExecutablePath)
			if err != nil {
				t.Fatal(err)
			}
			process, err := newPlatformCommandRuntimeStarter().Start(
				context.Background(), CommandRuntimeScope{}, resolved)
			if err != nil {
				t.Fatal(err)
			}
			stdoutDone := make(chan []byte, 1)
			stderrDone := make(chan []byte, 1)
			go func() { value, _ := io.ReadAll(process.Stdout()); stdoutDone <- value }()
			go func() { value, _ := io.ReadAll(process.Stderr()); stderrDone <- value }()
			exitCode, waitErr := process.Wait()
			stdout, stderr := <-stdoutDone, <-stderrDone
			_ = process.Close()
			decodedStdout := commandRuntimeWindowsTestOutput(stdout)
			decodedStderr := commandRuntimeWindowsTestOutput(stderr)
			if test.name == "Windows PowerShell 5" &&
				strings.EqualFold(strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")), "true") &&
				uint32(exitCode) == uint32(0xffff0000) &&
				strings.Contains(decodedStderr, "System.Management.Automation.Utils") &&
				strings.Contains(strings.ToLower(decodedStderr), "type initializer") {
				t.Skipf("GitHub Windows service session rejected Windows PowerShell 5 before script initialization; product authority remains closed")
			}
			if waitErr != nil || exitCode != 0 || !strings.Contains(decodedStdout, "smoke") {
				t.Fatalf("stdout=%q stderr=%q exit=%d err=%v",
					decodedStdout, decodedStderr, exitCode, waitErr)
			}
		})
	}
}

func commandRuntimeWindowsTestOutput(value []byte) string {
	if len(value) < 2 || len(value)%2 != 0 {
		return string(value)
	}
	zeroHighBytes := 0
	for index := 1; index < len(value); index += 2 {
		if value[index] == 0 {
			zeroHighBytes++
		}
	}
	if zeroHighBytes*4 < (len(value)/2)*3 {
		return string(value)
	}
	units := make([]uint16, len(value)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(value[index*2:])
	}
	return string(utf16.Decode(units))
}

func TestCommandRuntimeWindowsTestOutput(t *testing.T) {
	utf16LE := []byte{'s', 0, 'm', 0, 'o', 0, 'k', 0, 'e', 0}
	if got := commandRuntimeWindowsTestOutput(utf16LE); got != "smoke" {
		t.Fatalf("UTF-16LE output = %q", got)
	}
	if got := commandRuntimeWindowsTestOutput([]byte("smoke")); got != "smoke" {
		t.Fatalf("UTF-8 output = %q", got)
	}
}

func commandRuntimeRegularFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
