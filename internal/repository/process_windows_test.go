//go:build windows

package repository

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestRepositoryCommandContextHidesWindowsConsole(t *testing.T) {
	command := repositoryCommandContext(t.Context(), "cmd.exe", "/c", "exit", "0")
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow ||
		command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("repository subprocess can create a visible console: %+v", command.SysProcAttr)
	}
}
