//go:build windows

package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsHostPowerShellUTF8OptIn(t *testing.T) {
	if os.Getenv("CYBERAGENT_TEST_WINDOWS_HOST_EXECUTION") != "1" {
		t.Skip("set CYBERAGENT_TEST_WINDOWS_HOST_EXECUTION=1 for the real PowerShell output check")
	}
	executable, err := ResolveHostPowerShellExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.EqualFold([]byte(filepath.Base(executable)), []byte("pwsh.exe")) {
		t.Skip("this regression requires an explicitly installed PowerShell 7")
	}
	digest, err := hostTestFileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{"NO_COLOR=1", "PATHEXT=.COM;.EXE;.BAT;.CMD"}
	for _, key := range []string{"SystemRoot", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	nativeFailure := "& '" + strings.ReplaceAll(executable, "'", "''") + "' -NoLogo -NoProfile -NonInteractive -Command 'exit 23'"
	for _, test := range []struct {
		name, command, stdout, stderr string
		exit                          int
		exactStderr                   bool
		legacy                        bool
		file                          bool
	}{
		{name: "unicode nonzero", command: "Write-Output 'UX_Q_HOST_ACTUAL_OUTPUT 中文'; [Console]::Error.WriteLine('错误 😀'); exit 7", stdout: "UX_Q_HOST_ACTUAL_OUTPUT 中文\r\n", stderr: "错误 😀\r\n", exit: 7, exactStderr: true},
		{name: "early return", command: "Write-Output '中文'; return; throw 'must not run'", stdout: "中文\r\n", exactStderr: true},
		{name: "native failure then return", command: nativeFailure + "; return", exit: 1, exactStderr: true},
		{name: "legacy native failure then return", command: nativeFailure + "; return", exit: 1, exactStderr: true, legacy: true},
		{name: "native last failure", command: nativeFailure, exit: 1, exactStderr: true},
		{name: "shell failure then return", command: "Write-Error '错误'; return", stderr: "错误", exit: 1},
		{name: "file declarations", command: "& ./declared.ps1", stdout: "中文 utf-8\r\n", exactStderr: true, file: true},
		{name: "function param", command: "function Show-Value { param([string]$Value) Write-Output $Value }; Show-Value '中文'", stdout: "中文\r\n", exactStderr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			argv, err := CanonicalHostShellArguments("powershell", test.command)
			if err != nil {
				t.Fatal(err)
			}
			if test.legacy {
				argv = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", test.command}
			}
			workspace := filepath.Clean(t.TempDir())
			if test.file {
				body := "using namespace System.Text\nparam([string]$Value = '中文')\n[Console]::Out.WriteLine($Value + ' ' + [UTF8Encoding]::new().WebName)\n"
				if err := os.WriteFile(filepath.Join(workspace, "declared.ps1"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			spec, err := NewHostCommandSpec(HostCommandSpecRequest{
				ExecutablePath: executable, ExecutableSHA256: digest, Argv: argv,
				WorkingDirectory: workspace, Environment: environment,
				NetworkIntent: HostNetworkIntentHost, TimeoutMilliseconds: (30 * time.Second).Milliseconds(),
				Purpose: "read-only PowerShell UTF-8 stdout/stderr and nonzero exit regression",
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (windowsHostStarter{}).Start(context.Background(), HostStartSpec{
				RequestID: "host-exec-powershell-utf8", Command: spec, Environment: environment,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := result.Validate(); err != nil {
				t.Fatal(err)
			}
			t.Logf("PowerShell=%s sha256=%s exit=%d stdout_hex=%x stderr_hex=%x reaped=%t", executable, digest,
				result.ExitCode, result.Stdout.Data, result.Stderr.Data, result.TreeReaped)
			stderrMatches := bytes.Contains(result.Stderr.Data, []byte(test.stderr))
			if test.exactStderr {
				stderrMatches = bytes.Equal(result.Stderr.Data, []byte(test.stderr))
			}
			if result.ExitCode != test.exit || !result.TreeReaped ||
				!bytes.Equal(result.Stdout.Data, []byte(test.stdout)) || !stderrMatches ||
				result.Stdout.Truncated || result.Stderr.Truncated {
				t.Fatalf("real Host PowerShell output/exit mismatch: exit=%d stdout=%q stderr=%q reaped=%t",
					result.ExitCode, result.Stdout.Data, result.Stderr.Data, result.TreeReaped)
			}
		})
	}
}
