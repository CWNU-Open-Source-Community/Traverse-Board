//go:build windows

package repository

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func repositoryCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	return command
}
