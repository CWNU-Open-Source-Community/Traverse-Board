package analyzer

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	AnalyzerEmbeddedWASIExecutionProtocolVersion = "analyzer_embedded_wasi_execution.v1"
	AnalyzerEmbeddedWASIExecutionBackend         = "embedded_wazero_wasip1.v1"
	MaxAnalyzerEmbeddedWASIStderrBytes           = 4 * 1024
)

//go:embed embedded/cyberagent-analyzer-fixture.wasm
var embeddedAnalyzerFixtureWASM []byte

// AnalyzerEmbeddedWASIExecution is a redacted receipt for one in-process guest
// invocation. It deliberately excludes the request body and result body.
type AnalyzerEmbeddedWASIExecution struct {
	ProtocolVersion      string           `json:"protocol_version"`
	Backend              string           `json:"backend"`
	ModuleSHA256         string           `json:"module_sha256"`
	ProfileSHA256        string           `json:"profile_sha256"`
	CandidateSHA256      string           `json:"candidate_sha256"`
	RequestSHA256        string           `json:"request_sha256"`
	RequestID            string           `json:"request_id"`
	Analyzer             string           `json:"analyzer"`
	Status               InvocationStatus `json:"status"`
	FailureCode          ErrorCode        `json:"failure_code"`
	ExitCode             int              `json:"exit_code"`
	ResultProtocol       string           `json:"result_protocol"`
	StdoutBytes          int              `json:"stdout_bytes"`
	StdoutSHA256         string           `json:"stdout_sha256"`
	StderrBytes          int              `json:"stderr_bytes"`
	StderrSHA256         string           `json:"stderr_sha256"`
	DeadlineMilliseconds int              `json:"deadline_ms"`
	RuntimePerInvocation bool             `json:"runtime_per_invocation"`
	RuntimeClosed        bool             `json:"runtime_closed"`
	GuestInstantiated    bool             `json:"guest_instantiated"`
	GuestExecuted        bool             `json:"guest_executed"`
	ResultValidated      bool             `json:"result_validated"`
	DeterministicMatch   bool             `json:"deterministic_match"`
	BoundedMemoryStdio   bool             `json:"bounded_memory_stdio"`
	FilesystemMounted    bool             `json:"filesystem_mounted"`
	EnvironmentInherited bool             `json:"environment_inherited"`
	NetworkEnabled       bool             `json:"network_enabled"`
	SubprocessEnabled    bool             `json:"subprocess_enabled"`
	RawRequestIncluded   bool             `json:"raw_request_included"`
	RawResultIncluded    bool             `json:"raw_result_included"`
	Fingerprint          string           `json:"fingerprint"`
}

// AnalyzerEmbeddedWASIResult keeps the validated metadata-only result in
// memory for the immediate caller. RawResult is never part of JSON or a
// durable receipt and must be committed only by the later product gate.
type AnalyzerEmbeddedWASIResult struct {
	Execution AnalyzerEmbeddedWASIExecution `json:"execution"`
	RawResult []byte                        `json:"-"`
}

// ExecuteEmbeddedWASI runs only the build-embedded, provenance-pinned fixture.
// Callers cannot supply module bytes, paths, argv, environment, mounts, or host
// functions.
func ExecuteEmbeddedWASI(ctx context.Context, rawRequest []byte) (AnalyzerEmbeddedWASIResult, ErrorCode) {
	return executeEmbeddedWASIModule(ctx, rawRequest, embeddedAnalyzerFixtureWASM)
}

func executeEmbeddedWASIModule(ctx context.Context, rawRequest, module []byte) (
	AnalyzerEmbeddedWASIResult, ErrorCode,
) {
	if ctx == nil {
		return AnalyzerEmbeddedWASIResult{}, CodeInvalidRequest
	}
	candidate, code := BuildInvocationCandidate(rawRequest)
	if code != "" {
		return AnalyzerEmbeddedWASIResult{}, code
	}
	request, code := DecodeRequest(rawRequest)
	if code != "" {
		return AnalyzerEmbeddedWASIResult{}, code
	}
	canonicalRequest, err := json.Marshal(request)
	if err != nil {
		return AnalyzerEmbeddedWASIResult{}, CodeInternal
	}
	profile := BuildAnalyzerEmbeddedWASIProfile()
	execution, ok := newAnalyzerEmbeddedWASIExecution(candidate, profile, module)
	if !ok {
		return AnalyzerEmbeddedWASIResult{}, CodeInternal
	}

	deadlineCtx, cancel := context.WithTimeout(ctx,
		millisecondsDuration(candidate.Limits.TimeoutMilliseconds))
	defer cancel()
	if err := deadlineCtx.Err(); err != nil {
		return finalizedEmbeddedWASIContextFailure(execution, err)
	}
	assessment, assessmentCode := AssessAnalyzerEmbeddedWASIModule(deadlineCtx, module, profile)
	if assessmentCode != "" || !assessment.Passed {
		if err := deadlineCtx.Err(); err != nil {
			return finalizedEmbeddedWASIContextFailure(execution, err)
		}
		execution.Status = InvocationFailed
		execution.FailureCode = assessmentCode
		if execution.FailureCode == "" {
			execution.FailureCode = CodeCapabilityDenied
		}
		return finalizeEmbeddedWASIExecution(execution, nil, nil), execution.FailureCode
	}

	runtimeConfig := wazero.NewRuntimeConfigInterpreter().
		WithCoreFeatures(api.CoreFeaturesV2).
		WithMemoryLimitPages(profile.MemoryLimitPages).
		WithCloseOnContextDone(true).
		WithDebugInfoEnabled(false).
		WithCustomSections(false)
	runtime := wazero.NewRuntimeWithConfig(deadlineCtx, runtimeConfig)
	runtimeClosed := false
	closeRuntime := func() {
		if !runtimeClosed {
			runtimeClosed = runtime.Close(context.Background()) == nil
		}
	}
	defer closeRuntime()

	if _, err = wasi_snapshot_preview1.Instantiate(deadlineCtx, runtime); err != nil {
		closeRuntime()
		return failedEmbeddedWASIExecution(execution, deadlineCtx, err, runtimeClosed, nil, nil)
	}
	compiled, err := runtime.CompileModule(deadlineCtx, module)
	if err != nil {
		closeRuntime()
		return failedEmbeddedWASIExecution(execution, deadlineCtx, err, runtimeClosed, nil, nil)
	}
	defer compiled.Close(context.Background())

	stdout := newBoundedAnalyzerWriter(candidate.Limits.MaxOutputBytes)
	stderr := newBoundedAnalyzerWriter(MaxAnalyzerEmbeddedWASIStderrBytes)
	config := wazero.NewModuleConfig().
		WithName("").
		WithArgs("cyberagent-analyzer-fixture").
		WithStdin(bytes.NewReader(canonicalRequest)).
		WithStdout(stdout).
		WithStderr(stderr).
		WithRandSource(zeroReader{})
	execution.GuestExecuted = true
	instance, instantiateErr := runtime.InstantiateModule(deadlineCtx, compiled, config)
	if instance != nil {
		execution.GuestInstantiated = true
		_ = instance.Close(context.Background())
	} else if instantiateErr == nil || analyzerWASIExitCode(instantiateErr) >= 0 {
		// WASI _start commonly closes the module via proc_exit while instantiating.
		execution.GuestInstantiated = true
	}
	closeRuntime()
	if instantiateErr != nil {
		if exitCode := analyzerWASIExitCode(instantiateErr); exitCode >= 0 {
			execution.ExitCode = exitCode
		} else {
			return failedEmbeddedWASIExecution(execution, deadlineCtx, instantiateErr,
				runtimeClosed, stdout, stderr)
		}
	} else {
		execution.ExitCode = ExitSuccess
	}
	execution.RuntimeClosed = runtimeClosed

	if stdout.overflow || stderr.overflow {
		execution.Status = InvocationFailed
		execution.FailureCode = CodeOutputLimitExceeded
		return finalizeEmbeddedWASIExecution(execution, stdout.bytes(), stderr.bytes()),
			CodeOutputLimitExceeded
	}
	rawResult := stdout.bytes()
	if !validEmbeddedWASIResult(candidate, canonicalRequest, rawResult, execution.ExitCode) {
		execution.Status = InvocationFailed
		execution.FailureCode = CodeInvalidResult
		return finalizeEmbeddedWASIExecution(execution, rawResult, stderr.bytes()), CodeInvalidResult
	}
	execution.Status = InvocationSucceeded
	execution.ResultProtocol = candidate.Descriptor.ResultProtocol
	execution.ResultValidated = true
	execution.DeterministicMatch = true
	final := finalizeEmbeddedWASIExecution(execution, rawResult, stderr.bytes())
	if code := ValidateAnalyzerEmbeddedWASIExecution(final.Execution, candidate); code != "" {
		return AnalyzerEmbeddedWASIResult{}, CodeInternal
	}
	final.RawResult = append([]byte(nil), rawResult...)
	return final, ""
}

func ValidateAnalyzerEmbeddedWASIExecution(value AnalyzerEmbeddedWASIExecution,
	candidate InvocationCandidate,
) ErrorCode {
	candidateDigest, ok := invocationCandidateSHA256(candidate)
	if !ok || value.ProtocolVersion != AnalyzerEmbeddedWASIExecutionProtocolVersion ||
		value.Backend != AnalyzerEmbeddedWASIExecutionBackend ||
		!validDigest(value.ModuleSHA256) || !validDigest(value.ProfileSHA256) ||
		value.CandidateSHA256 != candidateDigest || value.RequestSHA256 != candidate.RequestSHA256 ||
		value.RequestID != candidate.RequestID || value.Analyzer != candidate.Analyzer ||
		value.Status != InvocationSucceeded || value.FailureCode != "" ||
		value.ExitCode != ExitSuccess || value.ResultProtocol != candidate.Descriptor.ResultProtocol ||
		value.StdoutBytes <= 0 || value.StdoutBytes > candidate.Limits.MaxOutputBytes ||
		!validDigest(value.StdoutSHA256) || value.StderrBytes != 0 || value.StderrSHA256 != "" ||
		value.DeadlineMilliseconds != candidate.Limits.TimeoutMilliseconds ||
		!value.RuntimePerInvocation || !value.RuntimeClosed || !value.GuestInstantiated ||
		!value.GuestExecuted || !value.ResultValidated || !value.DeterministicMatch ||
		!value.BoundedMemoryStdio || value.FilesystemMounted || value.EnvironmentInherited ||
		value.NetworkEnabled || value.SubprocessEnabled || value.RawRequestIncluded ||
		value.RawResultIncluded || !validDigest(value.Fingerprint) {
		return CodeInvalidResult
	}
	expected := value
	expected.Fingerprint = ""
	if analyzerStartFingerprint(expected) != value.Fingerprint {
		return CodeInvalidResult
	}
	return ""
}

func newAnalyzerEmbeddedWASIExecution(candidate InvocationCandidate,
	profile AnalyzerEmbeddedWASIProfile, module []byte,
) (AnalyzerEmbeddedWASIExecution, bool) {
	profileDigest, profileOK := canonicalSHA256(profile)
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	if !profileOK || !candidateOK || len(module) == 0 {
		return AnalyzerEmbeddedWASIExecution{}, false
	}
	moduleDigest := sha256.Sum256(module)
	return AnalyzerEmbeddedWASIExecution{
		ProtocolVersion: AnalyzerEmbeddedWASIExecutionProtocolVersion,
		Backend:         AnalyzerEmbeddedWASIExecutionBackend,
		ModuleSHA256:    hex.EncodeToString(moduleDigest[:]), ProfileSHA256: profileDigest,
		CandidateSHA256: candidateDigest, RequestSHA256: candidate.RequestSHA256,
		RequestID: candidate.RequestID, Analyzer: candidate.Analyzer,
		ExitCode: -1, DeadlineMilliseconds: candidate.Limits.TimeoutMilliseconds,
		RuntimePerInvocation: true, BoundedMemoryStdio: true,
	}, true
}

func finalizeEmbeddedWASIExecution(value AnalyzerEmbeddedWASIExecution, stdout, stderr []byte) AnalyzerEmbeddedWASIResult {
	value.StdoutBytes = len(stdout)
	if len(stdout) > 0 {
		digest := sha256.Sum256(stdout)
		value.StdoutSHA256 = hex.EncodeToString(digest[:])
	}
	value.StderrBytes = len(stderr)
	if len(stderr) > 0 {
		digest := sha256.Sum256(stderr)
		value.StderrSHA256 = hex.EncodeToString(digest[:])
	}
	value.Fingerprint = ""
	value.Fingerprint = analyzerStartFingerprint(value)
	return AnalyzerEmbeddedWASIResult{Execution: value}
}

func finalizedEmbeddedWASIContextFailure(value AnalyzerEmbeddedWASIExecution, err error) (
	AnalyzerEmbeddedWASIResult, ErrorCode,
) {
	value.RuntimeClosed = true
	if errors.Is(err, context.DeadlineExceeded) {
		value.Status = InvocationTimedOut
		value.FailureCode = CodeDeadlineExceeded
	} else {
		value.Status = InvocationCancelled
		value.FailureCode = CodeProcessFailed
	}
	return finalizeEmbeddedWASIExecution(value, nil, nil), value.FailureCode
}

func failedEmbeddedWASIExecution(value AnalyzerEmbeddedWASIExecution, ctx context.Context,
	err error, runtimeClosed bool, stdout, stderr *boundedAnalyzerWriter,
) (AnalyzerEmbeddedWASIResult, ErrorCode) {
	value.RuntimeClosed = runtimeClosed
	if contextErr := ctx.Err(); contextErr != nil {
		return finalizedEmbeddedWASIContextFailure(value, contextErr)
	}
	value.Status = InvocationFailed
	value.FailureCode = CodeProcessFailed
	var out, diagnostic []byte
	if stdout != nil {
		out = stdout.bytes()
	}
	if stderr != nil {
		diagnostic = stderr.bytes()
	}
	_ = err
	return finalizeEmbeddedWASIExecution(value, out, diagnostic), CodeProcessFailed
}

func validEmbeddedWASIResult(candidate InvocationCandidate, rawRequest, rawResult []byte,
	exitCode int,
) bool {
	if exitCode != ExitSuccess || len(rawResult) == 0 ||
		len(rawResult) > candidate.Limits.MaxOutputBytes {
		return false
	}
	return validSuccessfulAnalyzerResult(candidate, rawRequest, rawResult)
}

func analyzerWASIExitCode(err error) int {
	var exitErr *sys.ExitError
	if !errors.As(err, &exitErr) {
		return -1
	}
	if exitErr.ExitCode() > 255 {
		return -1
	}
	return int(exitErr.ExitCode())
}

type boundedAnalyzerWriter struct {
	buffer   bytes.Buffer
	maximum  int
	overflow bool
}

func newBoundedAnalyzerWriter(maximum int) *boundedAnalyzerWriter {
	return &boundedAnalyzerWriter{maximum: maximum}
}

func (writer *boundedAnalyzerWriter) Write(value []byte) (int, error) {
	if writer == nil || writer.maximum < 0 {
		return 0, errors.New("invalid analyzer output writer")
	}
	remaining := writer.maximum - writer.buffer.Len()
	if remaining <= 0 {
		writer.overflow = len(value) > 0
		return 0, io.ErrShortWrite
	}
	if len(value) > remaining {
		_, _ = writer.buffer.Write(value[:remaining])
		writer.overflow = true
		return remaining, io.ErrShortWrite
	}
	return writer.buffer.Write(value)
}

func (writer *boundedAnalyzerWriter) bytes() []byte {
	if writer == nil {
		return nil
	}
	return append([]byte(nil), writer.buffer.Bytes()...)
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	clear(value)
	return len(value), nil
}

func millisecondsDuration(value int) time.Duration {
	return time.Duration(value) * time.Millisecond
}

var _ io.Writer = (*boundedAnalyzerWriter)(nil)
var _ io.Reader = zeroReader{}
