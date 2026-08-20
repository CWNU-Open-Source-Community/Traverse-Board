//go:build !windows && !linux

package codeintel

import (
	"errors"
	"os/exec"
	"syscall"
)

type unixProcessTree struct{ pgid int }

func prepareOwnedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachOwnedProcessTree(cmd *exec.Cmd) (processTree, error) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil, errors.New("LSP process identity is unavailable")
	}
	return &unixProcessTree{pgid: cmd.Process.Pid}, nil
}

func (p *unixProcessTree) Kill() error {
	if p == nil || p.pgid <= 0 {
		return nil
	}
	err := syscall.Kill(-p.pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (*unixProcessTree) Close() error { return nil }
