//go:build windows

package runner

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

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
			process, err := newPlatformCommandRuntimeStarter().Start(resolved)
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
