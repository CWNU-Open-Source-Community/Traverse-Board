//go:build windows

package terminal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsTerminalShellIgnoresRedirectedSystemRoot(t *testing.T) {
	redirected := filepath.Clean(t.TempDir())
	t.Setenv("SystemRoot", redirected)
	path, err := terminalShellPath()
	if err != nil {
		t.Fatal(err)
	}
	windowsDirectory, err := windows.GetWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.ToLower(path),
		strings.ToLower(redirected)+string(filepath.Separator)) {
		t.Fatalf("terminal shell followed redirected SystemRoot: %s", path)
	}
	if !strings.EqualFold(path, filepath.Join(windowsDirectory, "System32",
		"WindowsPowerShell", "v1.0", "powershell.exe")) {
		t.Fatalf("terminal shell path=%q Windows directory=%q",
			path, windowsDirectory)
	}
}

func TestWindowsConPTYOptIn(t *testing.T) {
	if os.Getenv("CYBERAGENT_TEST_WINDOWS_CONPTY") != "1" {
		t.Skip("set CYBERAGENT_TEST_WINDOWS_CONPTY=1 to run the real ConPTY smoke test")
	}
	manager, err := NewPlatformManager(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = manager.Shutdown()
	})
	session, err := manager.Start(context.Background(),
		terminalStartTestRequestFor(t, "windows-conpty-smoke"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "PRAYU_CONPTY_OK"
	if _, err := manager.WriteUser(context.Background(), UserInputRequest{
		SessionID:   session.ID,
		Data:        []byte("Write-Output '" + marker + "'; exit\r"),
		RequestedBy: "test_operator", UserConfirmed: true,
	}); err != nil {
		t.Fatal(err)
	}
	var output []byte
	cursor := uint64(0)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		page, readErr := manager.Read(session.ID, cursor,
			MaxTerminalOutputReadBytes)
		if readErr != nil {
			t.Fatal(readErr)
		}
		output = append(output, page.Data...)
		cursor = page.NextCursor
		current, getErr := manager.Get(session.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if bytes.Contains(output, []byte(marker)) &&
			current.State == SessionExited {
			if current.ExitCode != 0 {
				t.Fatalf("PowerShell exit code=%d output=%q",
					current.ExitCode, output)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ConPTY smoke timed out; output=%q", output)
}
