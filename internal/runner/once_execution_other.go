//go:build !windows

package runner

import (
	"context"
	"errors"
	"io"
	"os"
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
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return started, err
	}
	defer stdoutRead.Close()
	defer stdoutWrite.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return started, err
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()
	command.Stdout = stdoutWrite
	command.Stderr = stderrWrite
	if err := command.Start(); err != nil {
		return started, err
	}
	// The parent must not retain writer handles: pipe EOF must reflect only the
	// controlled process group. Because Cmd receives *os.File writers, Wait does
	// not wait on output-copy goroutines before we can terminate descendants.
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	stdoutChannel := captureOncePipe(stdoutRead)
	stderrChannel := captureOncePipe(stderrRead)
	group := -command.Process.Pid
	waitErr := command.Wait()
	// A successful direct process may still have background descendants. Reap
	// the complete group on every return, not only after timeout/cancellation.
	// ESRCH simply means the group already exited.
	_ = syscall.Kill(group, syscall.SIGKILL)
	_ = command.Process.Kill()
	started.CompletedAt = time.Now().UTC()
	started.Stdout = waitOncePipeCapture(stdoutRead, stdoutChannel)
	started.Stderr = waitOncePipeCapture(stderrRead, stderrChannel)
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

func captureOncePipe(reader *os.File) <-chan OnceOutputCapture {
	result := make(chan OnceOutputCapture, 1)
	go func() {
		buffer := &boundedOnceBuffer{}
		_, _ = io.Copy(buffer, reader)
		result <- buffer.Capture()
	}()
	return result
}

func waitOncePipeCapture(reader *os.File,
	result <-chan OnceOutputCapture,
) OnceOutputCapture {
	select {
	case capture := <-result:
		return capture
	case <-time.After(2 * time.Second):
		// A descendant that deliberately escaped the process group must never
		// hold the caller hostage through an inherited output handle.
		_ = reader.Close()
		capture := <-result
		capture.Truncated = true
		return capture
	}
}
