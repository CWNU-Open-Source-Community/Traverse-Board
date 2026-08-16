package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

// OnceOutputCapture is the bounded, redacted projection of one stream.
// Raw output bodies never leave the executor; only counts and a bounded
// redacted prefix are returned as untrusted evidence.
type OnceOutputCapture struct {
	ObservedBytes        int
	CapturedBytes        int
	CapturedPrefix       string
	CapturedPrefixSHA256 string
	Truncated            bool
}

// OnceExecutionResult is untrusted evidence: exit code, bounded redacted
// prefixes, timing, and termination flags. It is deliberately not persisted
// with raw output anywhere.
type OnceExecutionResult struct {
	ProtocolVersion    string
	PolicyVersion      string
	RequestFingerprint string
	ExitCode           int
	Stdout             OnceOutputCapture
	Stderr             OnceOutputCapture
	StartedAt          time.Time
	CompletedAt        time.Time
	TimedOut           bool
	Cancelled          bool
	TreeReaped         bool
	StdinClosed        bool
}

// OnceStarter starts one process with its full environment replaced by the
// allowlisted entries. Implementations must arrange whole-process-tree
// termination when ctx is cancelled.
type OnceStarter interface {
	Name() string
	Available() bool
	Start(context.Context, OnceStartSpec) (OnceStartResult, error)
}

type OnceStartSpec struct {
	RequestFingerprint string
	ExecutablePath     string
	Argv               []string
	WorkingDirectory   string
	Environment        []string
}

type OnceStartResult struct {
	ExitCode    int
	Stdout      OnceOutputCapture
	Stderr      OnceOutputCapture
	StartedAt   time.Time
	CompletedAt time.Time
	TimedOut    bool
	Cancelled   bool
	TreeReaped  bool
	StdinClosed bool
}

// OnceExecutor executes validated one-shot requests. It never inspects or
// persists raw output; the starter returns only bounded redacted projections.
type OnceExecutor struct {
	starter OnceStarter
}

func NewOnceExecutor(starter OnceStarter) (*OnceExecutor, error) {
	if starter == nil || !validIdentity(starter.Name()) {
		return nil, ErrOnceCommandBoundary
	}
	return &OnceExecutor{starter: starter}, nil
}

func NewPlatformOnceExecutor() (*OnceExecutor, error) {
	return NewOnceExecutor(newPlatformOnceStarter())
}

func (e *OnceExecutor) Available() bool {
	return e != nil && e.starter != nil && e.starter.Available()
}

// Execute validates the request again (defense in depth), then runs it under
// the per-call timeout. Cancellation and timeout must terminate the whole
// process tree, never leaving background children behind.
func (e *OnceExecutor) Execute(ctx context.Context, request OnceCommandRequest) (OnceExecutionResult, error) {
	if e == nil || e.starter == nil || !e.starter.Available() {
		return OnceExecutionResult{}, errors.New("once command platform is unavailable")
	}
	if ctx == nil {
		return OnceExecutionResult{}, ErrOnceCommandBoundary
	}
	if err := ValidateOnceCommandSpec(request.Spec, request.WorkspaceRoot); err != nil {
		return OnceExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return OnceExecutionResult{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(request.Spec.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	started, err := e.starter.Start(callCtx, OnceStartSpec{
		RequestFingerprint: OnceCommandRequestFingerprint(request.RunID, request.WorkspaceID, request.Spec),
		ExecutablePath:     request.Spec.ExecutablePath,
		Argv:               request.Spec.Argv,
		WorkingDirectory:   resolveOnceWorkingDirectory(request.Spec.WorkingDirectory, request.WorkspaceRoot),
		Environment:        request.Spec.Environment,
	})
	if err != nil && started.StartedAt.IsZero() {
		return OnceExecutionResult{}, err
	}
	if err != nil {
		started.Cancelled = true
	}
	return OnceExecutionResult{
		ProtocolVersion:    OnceExecutionProtocolVersion,
		PolicyVersion:      OnceCommandPolicyVersion,
		RequestFingerprint: OnceCommandRequestFingerprint(request.RunID, request.WorkspaceID, request.Spec),
		ExitCode:           started.ExitCode,
		Stdout:             redactOnceCapture(started.Stdout),
		Stderr:             redactOnceCapture(started.Stderr),
		StartedAt:          started.StartedAt,
		CompletedAt:        started.CompletedAt,
		TimedOut:           started.TimedOut,
		Cancelled:          started.Cancelled,
		TreeReaped:         started.TreeReaped,
		StdinClosed:        started.StdinClosed,
	}, err
}

func resolveOnceWorkingDirectory(workingDirectory, workspaceRoot string) string {
	if filepath.IsAbs(workingDirectory) {
		return workingDirectory
	}
	return filepath.Join(workspaceRoot, workingDirectory)
}

func redactOnceCapture(capture OnceOutputCapture) OnceOutputCapture {
	capture.CapturedPrefix = redact.String(capture.CapturedPrefix)
	return capture
}

// boundedOnceBuffer captures at most limit bytes, records the true observed
// byte count, marks truncation, and enforces UTF-8 on the retained prefix.
type boundedOnceBuffer struct {
	captured    []byte
	observed    int
	truncated   bool
	invalidUTF8 bool
}

func (b *boundedOnceBuffer) Write(value []byte) (int, error) {
	b.observed += len(value)
	if len(b.captured) >= MaxOnceOutputBytes {
		b.truncated = true
		return len(value), nil
	}
	remaining := MaxOnceOutputBytes - len(b.captured)
	take := value
	if len(take) > remaining {
		take = take[:remaining]
		b.truncated = true
	}
	b.captured = append(b.captured, take...)
	if !utf8.Valid(b.captured) {
		b.invalidUTF8 = true
		b.captured = []byte(strings.ToValidUTF8(string(b.captured), "\uFFFD"))
	}
	return len(value), nil
}

func (b *boundedOnceBuffer) Capture() OnceOutputCapture {
	capture := OnceOutputCapture{
		ObservedBytes: b.observed, CapturedBytes: len(b.captured),
		CapturedPrefix: string(b.captured), Truncated: b.truncated || b.invalidUTF8,
	}
	if len(b.captured) > 0 {
		digest := sha256.Sum256(b.captured)
		capture.CapturedPrefixSHA256 = hex.EncodeToString(digest[:])
	}
	return capture
}

var _ = fmt.Sprintf
