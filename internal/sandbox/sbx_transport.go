package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SBXProcessRequest is an internal, already-compiled local CLI invocation.
// Applications must use SBXBackend rather than assembling these themselves.
type SBXProcessRequest struct {
	Executable  string
	Arguments   []string
	Directory   string
	Environment []string
	Stdin       io.Reader
	OutputLimit int
}

type SBXProcessResult struct {
	ExitCode       int
	Stdout, Stderr []byte
}

// SBXProcessTransport permits deterministic lifecycle tests without running sbx.
type SBXProcessTransport interface {
	Run(context.Context, SBXProcessRequest) (SBXProcessResult, error)
}

type sbxProcessTransport struct{}

type sbxOutputBudget struct {
	mu        sync.Mutex
	remaining int
	overflow  bool
	cancel    context.CancelFunc
}
type sbxOutputWriter struct {
	budget *sbxOutputBudget
	buffer bytes.Buffer
}

func (w *sbxOutputWriter) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := len(p)
	if n > w.budget.remaining {
		_, _ = w.buffer.Write(p[:w.budget.remaining])
		w.budget.remaining = 0
		w.budget.overflow = true
		w.budget.cancel()
		return n, nil
	}
	w.budget.remaining -= n
	return w.buffer.Write(p)
}

func (sbxProcessTransport) Run(ctx context.Context, r SBXProcessRequest) (SBXProcessResult, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	budget := &sbxOutputBudget{remaining: r.OutputLimit, cancel: cancel}
	stdout, stderr := &sbxOutputWriter{budget: budget}, &sbxOutputWriter{budget: budget}
	cmd := exec.CommandContext(child, r.Executable, r.Arguments...)
	cmd.Dir, cmd.Env, cmd.Stdin = r.Directory, r.Environment, r.Stdin
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = stdout, stderr, 2*time.Second
	err := cmd.Run()
	result := SBXProcessResult{ExitCode: -1, Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if budget.overflow {
		return result, ErrSBXOutputLimit
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && result.ExitCode >= 0 {
			return result, nil
		}
		// Raw daemon errors can include host credentials or sensitive paths.
		return result, ErrSBXCLI
	}
	return result, nil
}

func sbxHostEnvironment() []string {
	allowed := map[string]bool{"HOME": true, "USERPROFILE": true, "APPDATA": true,
		"LOCALAPPDATA": true, "SYSTEMROOT": true, "WINDIR": true, "SYSTEMDRIVE": true,
		"PATH": true, "PATHEXT": true, "TEMP": true, "TMP": true, "TMPDIR": true,
		"XDG_CONFIG_HOME": true, "XDG_RUNTIME_DIR": true, "DBUS_SESSION_BUS_ADDRESS": true}
	result := []string{"DOCKER_CLI_PLUGIN_ORIGINAL_CLI_COMMAND=", "NO_COLOR=1"}
	for _, binding := range os.Environ() {
		name, _, _ := strings.Cut(binding, "=")
		if allowed[strings.ToUpper(name)] {
			result = append(result, binding)
		}
	}
	return result
}
