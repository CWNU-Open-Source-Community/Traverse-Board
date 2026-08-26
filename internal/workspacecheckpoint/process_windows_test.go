//go:build windows

package workspacecheckpoint

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestCheckpointCommandContextHidesWindowsConsole(t *testing.T) {
	command := checkpointCommandContext(t.Context(), "cmd.exe", "/c", "exit", "0")
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow ||
		command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("checkpoint subprocess can create a visible console: %+v", command.SysProcAttr)
	}
}
