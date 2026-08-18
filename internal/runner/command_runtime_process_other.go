//go:build !windows

package runner

import (
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"
)

type unixCommandRuntimeStarter struct{}

func newPlatformCommandRuntimeStarter() commandRuntimeStarter { return unixCommandRuntimeStarter{} }
func (unixCommandRuntimeStarter) Name() string                { return "posix-command-runtime-v2" }
func (unixCommandRuntimeStarter) Available() bool             { return true }

type unixCommandRuntimeProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	pid     int
	pgid    int
}

func (unixCommandRuntimeStarter) Start(spec CommandRuntimeResolvedSpec) (commandRuntimeProcess, error) {
	if commandRuntimeFileDigestMatches(spec.ExecutablePath, spec.ExecutableSHA256) != nil {
		return nil, ErrCommandRuntimeBoundary
	}
	command := exec.Command(spec.ExecutablePath, spec.CanonicalArgv...)
	command.Dir = spec.AbsoluteDirectory
	command.Env = append([]string(nil), spec.Environment...)
	command.SysProcAttr = commandRuntimeSysProcAttr()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	var stdin io.WriteCloser
	if spec.Spec.StdinPolicy == CommandRuntimeStdinPipe {
		stdin, err = command.StdinPipe()
		if err != nil {
			return nil, err
		}
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &unixCommandRuntimeProcess{command: command, stdin: stdin,
		stdout: stdout, stderr: stderr, pid: command.Process.Pid,
		pgid: command.Process.Pid}, nil
}

func (p *unixCommandRuntimeProcess) Ownership() CommandRuntimeProcessOwnership {
	return CommandRuntimeProcessOwnership{PID: p.pid, ProcessGroup: p.pgid,
		JobAssignedAtCreation: true, KillOnClose: true}
}
func (p *unixCommandRuntimeProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *unixCommandRuntimeProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *unixCommandRuntimeProcess) WriteStdin(data []byte) (int, error) {
	if p.stdin == nil {
		return 0, ErrCommandRuntimeJobClosed
	}
	return p.stdin.Write(data)
}
func (p *unixCommandRuntimeProcess) CloseStdin() error {
	if p.stdin == nil {
		return nil
	}
	err := p.stdin.Close()
	p.stdin = nil
	return err
}
func (p *unixCommandRuntimeProcess) Wait() (int, error) {
	err := p.command.Wait()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}
	_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
	return exitCode, err
}
func (p *unixCommandRuntimeProcess) Cancel(grace time.Duration) error {
	if err := syscall.Kill(-p.pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(grace)
	for grace > 0 && time.Now().Before(deadline) {
		if err := syscall.Kill(-p.pgid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return p.Kill()
}
func (p *unixCommandRuntimeProcess) Kill() error {
	err := syscall.Kill(-p.pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func (p *unixCommandRuntimeProcess) Close() error {
	return errors.Join(p.CloseStdin(), p.stdout.Close(), p.stderr.Close())
}

func commandRuntimeFileDigestMatches(path, expected string) error {
	actual, err := commandRuntimeFileSHA256(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return ErrCommandRuntimeBoundary
	}
	return nil
}

func cleanupCommandRuntimeOrphan(_, processGroup int) {
	if processGroup > 0 {
		_ = syscall.Kill(-processGroup, syscall.SIGKILL)
	}
}
