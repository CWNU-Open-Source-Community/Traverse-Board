//go:build !windows

package repository

import (
	"context"
	"os/exec"
)

func repositoryCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
