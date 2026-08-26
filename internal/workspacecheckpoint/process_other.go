//go:build !windows

package workspacecheckpoint

import (
	"context"
	"os/exec"
)

func checkpointCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
