package runner

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"cyberagent-workbench/internal/commandruntimeadapter"
)

// CommandRuntimeSandboxResult is the small terminal boundary a sandbox backend
// supplies to the shared Job manager. The backend owns isolation, cancellation,
// and complete tree reaping; the manager continues to own cursor framing,
// sanitization, durable Job state, Artifact capture, and restart fencing.
type CommandRuntimeSandboxResult struct {
	ExitCode   int
	Stdout     []byte
	Stderr     []byte
	TreeReaped bool
}

func (r CommandRuntimeSandboxResult) Validate(maximum int) error {
	if r.ExitCode < 0 || !r.TreeReaped || maximum < 1 ||
		len(r.Stdout) > maximum || len(r.Stderr) > maximum ||
		len(r.Stdout)+len(r.Stderr) > maximum {
		return ErrCommandRuntimeBoundary
	}
	return nil
}

// CommandRuntimeSandboxExecutor adapts an already-isolated backend without
// exposing backend choice to the model-facing command-runtime.v2 schema.
// Execute must return only after the complete owned process/container tree has
// stopped; cancellation is delivered through ctx.
type CommandRuntimeSandboxExecutor interface {
	Identity() commandruntimeadapter.Identity
	Available() bool
	ExecuteSandboxCommand(context.Context, CommandRuntimeScope,
		CommandRuntimeResolvedSpec) (CommandRuntimeSandboxResult, error)
}

// NewSandboxCommandRuntimeManager reuses the mature Run-owned Job state
// machine for a sandbox backend. A sandbox Job intentionally persists no host
// PID/process-group identifier because neither is safe cross-backend authority.
func NewSandboxCommandRuntimeManager(store CommandRuntimeStore,
	executor CommandRuntimeSandboxExecutor, ownerID string,
) (*CommandRuntimeManager, error) {
	if executor == nil || !executor.Available() {
		return nil, ErrCommandRuntimeUnavailable
	}
	identity := executor.Identity()
	if identity.Kind != commandruntimeadapter.KindSandboxedWorkspace ||
		!identity.Executable() {
		return nil, ErrCommandRuntimeUnavailable
	}
	generation := time.Now().UTC().UnixNano()
	return newCommandRuntimeManagerWithAdapter(store,
		commandRuntimeSandboxStarter{executor: executor}, identity, ownerID, generation)
}

type commandRuntimeSandboxStarter struct {
	executor CommandRuntimeSandboxExecutor
}

func (s commandRuntimeSandboxStarter) Name() string {
	if s.executor == nil {
		return ""
	}
	return s.executor.Identity().BackendIdentity
}

func (s commandRuntimeSandboxStarter) Available() bool {
	return s.executor != nil && s.executor.Available() &&
		s.executor.Identity().Kind == commandruntimeadapter.KindSandboxedWorkspace &&
		s.executor.Identity().Executable()
}

func (s commandRuntimeSandboxStarter) Start(_ context.Context,
	scope CommandRuntimeScope, spec CommandRuntimeResolvedSpec,
) (commandRuntimeProcess, error) {
	if !s.Available() || !scope.Adapter.SameBackend(s.executor.Identity()) ||
		spec.Spec.StdinPolicy != CommandRuntimeStdinClosed ||
		spec.Spec.InitialStdin != "" || !spec.Spec.CloseInitialStdin {
		return nil, ErrCommandRuntimeBoundary
	}
	return newCommandRuntimeSandboxProcess(s.executor, scope, spec), nil
}

type commandRuntimeSandboxProcess struct {
	cancel       context.CancelFunc
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter
	done         chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
	exitCode     int
	waitErr      error
}

func newCommandRuntimeSandboxProcess(executor CommandRuntimeSandboxExecutor,
	scope CommandRuntimeScope, spec CommandRuntimeResolvedSpec,
) *commandRuntimeSandboxProcess {
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	executionContext, cancel := context.WithCancel(context.Background())
	process := &commandRuntimeSandboxProcess{cancel: cancel,
		stdoutReader: stdoutReader, stdoutWriter: stdoutWriter,
		stderrReader: stderrReader, stderrWriter: stderrWriter,
		done: make(chan struct{}), exitCode: 125}
	go process.execute(executionContext, executor, scope, spec)
	return process
}

func (p *commandRuntimeSandboxProcess) execute(ctx context.Context,
	executor CommandRuntimeSandboxExecutor, scope CommandRuntimeScope,
	spec CommandRuntimeResolvedSpec,
) {
	result, err := executor.ExecuteSandboxCommand(ctx, scope, spec)
	if validationErr := result.Validate(spec.Spec.Output.ArtifactBytes); validationErr != nil {
		err = errors.Join(err, validationErr)
		result.ExitCode = 125
		result.Stdout = nil
		if err != nil {
			result.Stderr = []byte(err.Error())
			if len(result.Stderr) > spec.Spec.Output.ArtifactBytes {
				result.Stderr = result.Stderr[:spec.Spec.Output.ArtifactBytes]
			}
		}
	}
	if len(result.Stdout) > 0 {
		_, _ = p.stdoutWriter.Write(result.Stdout)
	}
	if len(result.Stderr) > 0 {
		_, _ = p.stderrWriter.Write(result.Stderr)
	}
	_ = p.stdoutWriter.Close()
	_ = p.stderrWriter.Close()
	p.mu.Lock()
	p.exitCode = result.ExitCode
	p.waitErr = err
	p.mu.Unlock()
	close(p.done)
}

func (*commandRuntimeSandboxProcess) Ownership() CommandRuntimeProcessOwnership {
	return CommandRuntimeProcessOwnership{JobAssignedAtCreation: true, KillOnClose: true}
}

func (p *commandRuntimeSandboxProcess) Stdout() io.ReadCloser { return p.stdoutReader }
func (p *commandRuntimeSandboxProcess) Stderr() io.ReadCloser { return p.stderrReader }

func (*commandRuntimeSandboxProcess) WriteStdin([]byte) (int, error) {
	return 0, ErrCommandRuntimeBoundary
}

func (*commandRuntimeSandboxProcess) CloseStdin() error { return nil }

func (p *commandRuntimeSandboxProcess) Wait() (int, error) {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode, p.waitErr
}

func (p *commandRuntimeSandboxProcess) Cancel(grace time.Duration) error {
	p.cancel()
	if grace <= 0 {
		return nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		return ErrCommandRuntimeUnavailable
	}
}

func (p *commandRuntimeSandboxProcess) Kill() error {
	p.cancel()
	return nil
}

func (p *commandRuntimeSandboxProcess) Close() error {
	p.closeOnce.Do(func() {
		p.cancel()
		_ = p.stdoutReader.Close()
		_ = p.stderrReader.Close()
	})
	return nil
}

var _ commandRuntimeProcess = (*commandRuntimeSandboxProcess)(nil)
