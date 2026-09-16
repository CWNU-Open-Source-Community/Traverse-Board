package releasegate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Run the actual finally cleanup block without starting a Desktop or WebView.
// Windows file sharing reproduces the child-process database lock that can
// briefly outlive the Desktop parent process.
func TestDesktopSmokeCleanupWaitsForReleasedFilesAndFailsClosed(t *testing.T) {
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Fatal("Windows Desktop release checks require PowerShell 7: ", err)
	}
	smokeScript, err := filepath.Abs(filepath.Join("..", "..", "scripts", "smoke-desktop-operator-preview.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"released_lock", "persistent_lock", "keep_data", "outside_root"} {
		t.Run(scenario, func(t *testing.T) {
			fixtureRoot := t.TempDir()
			temporaryRoot := filepath.Join(fixtureRoot, ".tmp")
			home := filepath.Join(temporaryRoot, "desktop-direct-launch-smoke-fixture")
			if scenario == "outside_root" {
				home = filepath.Join(fixtureRoot, "unowned-home")
			}
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			lockedFile := filepath.Join(home, "declarative_performance_observer.db")
			if err := os.WriteFile(lockedFile, []byte("owned smoke fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			var fileHandle windows.Handle
			if strings.HasSuffix(scenario, "lock") {
				name, err := windows.UTF16PtrFromString(lockedFile)
				if err != nil {
					t.Fatal(err)
				}
				fileHandle, err = windows.CreateFile(name, windows.GENERIC_READ,
					windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
					windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if fileHandle != 0 {
						windows.CloseHandle(fileHandle)
					}
				}()
			}
			signal := filepath.Join(fixtureRoot, "cleanup-started")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference = 'Stop'
$tokens = $null
$parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile(
    $env:SMOKE_TEST_SCRIPT, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw 'Smoke script has parse errors' }
$cleanup = @($ast.FindAll({ param($node)
    $node -is [System.Management.Automation.Language.IfStatementAst] -and
    $node.Extent.Text.StartsWith('if (-not $KeepData')
}, $true))
if ($cleanup.Count -ne 1) { throw 'Expected exactly one smoke home cleanup block' }
$temporaryRoot = $env:SMOKE_TEST_TEMP_ROOT
$isolatedHome = $env:SMOKE_TEST_HOME
$KeepData = $env:SMOKE_TEST_SCENARIO -eq 'keep_data'
[System.IO.File]::WriteAllText($env:SMOKE_TEST_SIGNAL, 'ready')
& ([scriptblock]::Create($cleanup[0].Extent.Text))
`)
			command.Env = append(os.Environ(),
				"SMOKE_TEST_SCRIPT="+smokeScript,
				"SMOKE_TEST_TEMP_ROOT="+temporaryRoot,
				"SMOKE_TEST_HOME="+home,
				"SMOKE_TEST_SCENARIO="+scenario,
				"SMOKE_TEST_SIGNAL="+signal)
			released := make(chan struct{})
			if scenario == "released_lock" {
				go func() {
					defer close(released)
					for ctx.Err() == nil {
						if _, err := os.Stat(signal); err == nil {
							time.Sleep(750 * time.Millisecond)
							windows.CloseHandle(fileHandle)
							fileHandle = 0
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()
			} else {
				close(released)
			}
			output, runErr := command.CombinedOutput()
			timedOut := ctx.Err() != nil
			cancel()
			<-released
			if timedOut {
				t.Fatalf("cleanup did not finish within its bounded retry period: %s", output)
			}
			expectError := scenario == "persistent_lock" || scenario == "outside_root"
			if (runErr != nil) != expectError {
				t.Fatalf("cleanup error = %v, want error %v; output:\n%s", runErr, expectError, output)
			}
			if scenario == "outside_root" && !strings.Contains(string(output), "Refusing to clean") {
				t.Fatalf("cleanup failed without rejecting the outside path: %s", output)
			}
			_, statErr := os.Stat(lockedFile)
			if scenario == "released_lock" {
				if !os.IsNotExist(statErr) {
					t.Fatalf("released smoke database was not removed: %v", statErr)
				}
				if _, err := os.Stat(home); !os.IsNotExist(err) {
					t.Fatalf("released smoke home was not removed: %v", err)
				}
			} else if statErr != nil {
				t.Fatalf("cleanup removed data that must be retained: %v", statErr)
			}
		})
	}
}
