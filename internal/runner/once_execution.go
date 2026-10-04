package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
	"time"
	"unicode/utf8"
)

// OnceOutputCapture is the bounded, redacted projection of one stream.
// Raw output bodies never leave the executor; only counts and a bounded
// redacted prefix are returned as untrusted evidence.
type OnceOutputCapture struct {
	ObservedBytes        int
	CapturedBytes        int
	CapturedPrefix       string
	CapturedPrefixSHA256 string
	ObservedSHA256       string
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

// NewPlatformOnceProcessStarter exposes the same whole-process-tree primitive
// used by OnceExecutor to other Go-owned fixed command families. Callers remain
// responsible for validating their closed executable/argv/cwd contract before
// invoking Start; the starter owns stdin closure, bounded output, cancellation,
// and descendant reaping.
func NewPlatformOnceProcessStarter() OnceStarter { return newPlatformOnceStarter() }

// OnceExecutor executes validated one-shot requests. It never inspects or
// persists raw output; the starter returns only bounded redacted projections.

// Execute validates the request again (defense in depth), then runs it under
// the per-call timeout. Cancellation and timeout must terminate the whole
// process tree, never leaving background children behind.

// boundedOnceBuffer captures at most limit bytes, records the true observed
// byte count, marks truncation, and enforces UTF-8 on the retained prefix.
type boundedOnceBuffer struct {
	captured    []byte
	observed    int
	truncated   bool
	invalidUTF8 bool
	digest      hash.Hash
}

func (b *boundedOnceBuffer) Write(value []byte) (int, error) {
	if b.digest == nil {
		b.digest = sha256.New()
	}
	_, _ = b.digest.Write(value)
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
	if b.digest == nil {
		empty := sha256.Sum256(nil)
		capture.ObservedSHA256 = hex.EncodeToString(empty[:])
	} else {
		capture.ObservedSHA256 = hex.EncodeToString(b.digest.Sum(nil))
	}
	if len(b.captured) > 0 {
		digest := sha256.Sum256(b.captured)
		capture.CapturedPrefixSHA256 = hex.EncodeToString(digest[:])
	}
	return capture
}

var _ = fmt.Sprintf
