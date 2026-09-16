//go:build windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandRuntimePowerShellUTF8AndTopLevelExit(t *testing.T) {
	executable, err := resolveCommandRuntimeShell(CommandRuntimePowerShell)
	if err != nil || !strings.EqualFold(filepath.Base(executable), "pwsh.exe") {
		t.Skipf("configure CYBERAGENT_POWERSHELL_PATH with PowerShell 7 for the real Windows test: %v", err)
	}
	nativeFailure := "& '" + strings.ReplaceAll(executable, "'", "''") + "' -NoLogo -NoProfile -NonInteractive -Command 'exit 23'"
	for _, test := range []struct {
		name, script, stdout, stderr string
		exit                         int
		stderrContains               bool
		file                         bool
	}{
		{name: "unicode streams and nonzero", script: "Write-Output '附件 中文'\n[Console]::Error.WriteLine('错误 😀')\nexit 7", stdout: "附件 中文\r\n", stderr: "错误 😀\r\n", exit: 7},
		{name: "early return", script: "Write-Output '保留原文'\nreturn\nthrow 'must not run'", stdout: "保留原文\r\n"},
		{name: "native failure then return", script: nativeFailure + "\nreturn", exit: 1},
		{name: "native last failure", script: nativeFailure, exit: 1},
		{name: "shell failure then return", script: "Write-Error '中文错误'\nreturn", stderr: "中文错误", stderrContains: true, exit: 1},
		{name: "handled continuation", script: "Write-Error '先前错误'\nWrite-Output '继续'", stderr: "先前错误", stderrContains: true, stdout: "继续\r\n"},
		{name: "project script declarations", script: "& ./declared.ps1", stdout: "中文 utf-8\r\n", file: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.file {
				body := "using namespace System.Text\nparam([string]$Value = '中文')\n[Console]::Out.WriteLine($Value + ' ' + [UTF8Encoding]::new().WebName)\n"
				if err := os.WriteFile(filepath.Join(root, "declared.ps1"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			spec := commandRuntimeTestPowerShellSpec()
			spec.Script, spec.TimeoutMilliseconds = test.script, 10000
			resolved, err := NormalizeCommandRuntimeSpec(spec, root)
			if err != nil {
				t.Fatal(err)
			}
			store := newCommandRuntimeMemoryStore()
			manager, err := NewCommandRuntimeManager(store, newPlatformCommandRuntimeStarter(), "utf8-host-test")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := manager.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			request := commandRuntimeTestRequest(manager, 10000)
			request.Spec = resolved
			request.Scope.WorkspaceRootSHA256 = resolved.WorkspaceRootSHA256
			job, replayed, err := manager.Start(t.Context(), request)
			if err != nil || replayed {
				t.Fatalf("start: replayed=%t err=%v", replayed, err)
			}
			deadline := time.Now().Add(12 * time.Second)
			for !job.State.Terminal() && time.Now().Before(deadline) {
				job, _, err = manager.Wait(t.Context(), job.ID, 100*time.Millisecond, 0, MaxCommandRuntimeOutputRead)
				if err != nil {
					t.Fatal(err)
				}
			}
			record, err := store.GetCommandRuntimeJob(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			// The existing safe stream removes CR for presentation; observed byte
			// counters still describe the original UTF-8 pipes, including CRLF.
			stdout := strings.ReplaceAll(test.stdout, "\r", "")
			stderrMatches := record.Stderr == strings.ReplaceAll(test.stderr, "\r", "")
			if test.stderrContains {
				stderrMatches = strings.Contains(record.Stderr, test.stderr)
			}
			exit := -1
			if record.ExitCode != nil {
				exit = *record.ExitCode
			}
			t.Logf("exe=%s sha256=%s state=%s exit=%d stdout_hex=%x stderr_hex=%x observed=%d/%d reaped=%t", record.ExecutablePath,
				record.ExecutableSHA256, record.State, exit, []byte(record.Stdout), []byte(record.Stderr), record.StdoutObservedBytes, record.StderrObservedBytes, record.TreeReaped)
			if !record.State.Terminal() || record.ExitCode == nil || *record.ExitCode != test.exit || !record.TreeReaped ||
				record.Stdout != stdout || !stderrMatches || record.StdoutObservedBytes != int64(len(test.stdout)) ||
				(!test.stderrContains && record.StderrObservedBytes != int64(len(test.stderr))) {
				t.Fatalf("output/exit mismatch: exit=%v stdout=%q stderr=%q observed=%d/%d", record.ExitCode, record.Stdout, record.Stderr, record.StdoutObservedBytes, record.StderrObservedBytes)
			}
			before := record
			again, replayed, err := manager.Start(t.Context(), request)
			if err != nil || !replayed || again.ID != job.ID || again.PID != job.PID {
				t.Fatalf("same key did not replay original completed process: job=%+v replayed=%t err=%v", again, replayed, err)
			}
			after, err := store.GetCommandRuntimeJob(t.Context(), job.ID)
			if err != nil || after.Version != before.Version || after.IntentJSON != before.IntentJSON ||
				after.StdoutSHA256 != before.StdoutSHA256 || after.StderrSHA256 != before.StderrSHA256 {
				t.Fatal("replay changed the saved command or output")
			}
		})
	}
}
