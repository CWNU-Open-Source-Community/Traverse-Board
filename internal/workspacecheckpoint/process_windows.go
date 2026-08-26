//go:build windows

package workspacecheckpoint

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func checkpointCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	return command
}
