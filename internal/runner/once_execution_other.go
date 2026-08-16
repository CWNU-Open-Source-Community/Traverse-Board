//go:build !windows

package runner

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// onceExecutableExtensionAllowed accepts native Unix executables; the shell
// interpreter denylist is the guard here.
func onceExecutableExtensionAllowed(string) bool { return true }

// unixOnceStarter runs the executable in its own process group so timeout and
// cancellation can signal the complete tree.
type unixOnceStarter struct{}

func newPlatformOnceStarter() OnceStarter { return unixOnceStarter{} }

func (unixOnceStarter) Name() string    { return "unix_once_process" }
func (unixOnceStarter) Available() bool { return true }

func (unixOnceStarter) Start(ctx context.Context, spec OnceStartSpec) (OnceStartResult, error) {
	started := OnceStartResult{StartedAt: time.Now().UTC(), StdinClosed: true}
	command := exec.CommandContext(ctx, spec.ExecutablePath, spec.Argv...)
	command.Dir = spec.WorkingDirectory
	command.Env = spec.Environment
	command.Stdin = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &boundedOnceBuffer{}
	stderr := &boundedOnceBuffer{}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return started, err
	}
	group := -command.Process.Pid
	waitErr := command.Wait()
	if ctx.Err() != nil && waitErr != nil {
		// Ensure the whole group is gone before reporting termination.
		_ = syscall.Kill(group, syscall.SIGKILL)
		_ = command.Process.Kill()
	}
	started.CompletedAt = time.Now().UTC()
	started.Stdout = stdout.Capture()
	started.Stderr = stderr.Capture()
	started.TreeReaped = true
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		started.TimedOut = true
	} else if ctx.Err() != nil {
		started.Cancelled = true
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			started.ExitCode = exitErr.ExitCode()
			if ctx.Err() == nil {
				// A plain non-zero exit is evidence, not an error.
				waitErr = nil
			}
			// When the context fired, the kill error must surface: timeout
			// and cancellation are hard failures with evidence attached.
		}
	}
	return started, waitErr
}

var _ = io.Discard
