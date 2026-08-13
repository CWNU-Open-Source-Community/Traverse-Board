package sandbox

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const (
	DockerContainerLifecycleRequestProtocolVersion = "sandbox_docker_container_lifecycle_probe_request.v1"
	DockerContainerLifecycleResultProtocolVersion  = "sandbox_docker_container_lifecycle_probe_result.v1"
	DockerContainerLifecycleTrust                  = "production_daemon_probe_non_authorizing"
	DockerContainerLifecycleConfirmation           = "RUN-LOCAL-DOCKER-LIFECYCLE-PROBE"

	DockerContainerLifecycleStatusExited    = "exited"
	DockerContainerLifecycleStatusTimedOut  = "timed_out"
	DockerContainerLifecycleStatusCancelled = "cancelled"

	DockerContainerLifecycleFailureDisabled        = "transport_disabled"
	DockerContainerLifecycleFailureUnsupported     = "transport_unsupported"
	DockerContainerLifecycleFailureConnection      = "connection_failed"
	DockerContainerLifecycleFailureInvalidResponse = "invalid_response"
	DockerContainerLifecycleFailureConfigMismatch  = "configuration_mismatch"
	DockerContainerLifecycleFailureUnsafeExisting  = "unsafe_existing_container"
	DockerContainerLifecycleFailureStart           = "start_failed"
	DockerContainerLifecycleFailureWait            = "wait_failed"
	DockerContainerLifecycleFailureTerminate       = "terminate_failed"
	DockerContainerLifecycleFailureCleanup         = "cleanup_failed"
)

type DockerContainerLifecycleError struct {
	code string
}

func (err *DockerContainerLifecycleError) Error() string {
	return "docker container lifecycle probe failed: " + err.code
}

func newDockerContainerLifecycleError(code string) error {
	return &DockerContainerLifecycleError{code: code}
}

func DockerContainerLifecycleErrorCode(err error) string {
	var lifecycleError *DockerContainerLifecycleError
	if errors.As(err, &lifecycleError) {
		return lifecycleError.code
	}
	return ""
}

type DockerContainerLifecycleRequest struct {
	ProtocolVersion     string
	AttemptID           string
	LeaseGeneration     int64
	WriteRequest        DockerContainerWriteRequest
	Stage               DockerContainerStageResult
	ProbeAuthorized     bool
	ProductEntryEnabled bool
	ExecutionAuthorized bool
	ArtifactAuthorized  bool
	RequestFingerprint  string
}

func NewDockerContainerLifecycleRequest(attemptID string, generation int64,
	writeRequest DockerContainerWriteRequest, stage DockerContainerStageResult,
	confirmation string,
) (DockerContainerLifecycleRequest, error) {
	request := DockerContainerLifecycleRequest{
		ProtocolVersion: DockerContainerLifecycleRequestProtocolVersion,
		AttemptID:       attemptID, LeaseGeneration: generation, WriteRequest: writeRequest,
		Stage: stage, ProbeAuthorized: confirmation == DockerContainerLifecycleConfirmation,
	}
	request.RequestFingerprint = dockerContainerLifecycleRequestFingerprint(request)
	if request.Validate() != nil {
		return DockerContainerLifecycleRequest{}, errors.New("docker container lifecycle request is invalid")
	}
	return request, nil
}

func (request DockerContainerLifecycleRequest) Validate() error {
	if request.ProtocolVersion != DockerContainerLifecycleRequestProtocolVersion ||
		validateStoredIdentity("Docker lifecycle attempt id", request.AttemptID) != nil ||
		request.LeaseGeneration < 1 || request.WriteRequest.Validate() != nil ||
		request.Stage.Validate() != nil || !request.ProbeAuthorized ||
		request.ProductEntryEnabled || request.ExecutionAuthorized ||
		request.ArtifactAuthorized ||
		request.Stage.RequestFingerprint != request.WriteRequest.RequestFingerprint ||
		request.Stage.SpecFingerprint != request.WriteRequest.Spec.SpecFingerprint ||
		request.Stage.ContainerStarted || request.Stage.ProcessExecuted ||
		request.Stage.ExecutionAuthorized || request.Stage.ArtifactCommitAuthorized ||
		request.RequestFingerprint != dockerContainerLifecycleRequestFingerprint(request) {
		return errors.New("docker container lifecycle request violates the probe boundary")
	}
	return nil
}

func dockerContainerLifecycleRequestFingerprint(request DockerContainerLifecycleRequest) string {
	return fingerprint(DockerContainerLifecycleRequestProtocolVersion, request.AttemptID,
		strconv.FormatInt(request.LeaseGeneration, 10), request.WriteRequest.RequestFingerprint,
		request.Stage.StageFingerprint, strconv.FormatBool(request.ProbeAuthorized),
		strconv.FormatBool(request.ProductEntryEnabled),
		strconv.FormatBool(request.ExecutionAuthorized),
		strconv.FormatBool(request.ArtifactAuthorized))
}

type DockerContainerLifecycleResult struct {
	ProtocolVersion            string
	Trust                      string
	Status                     string
	EndpointClass              string
	EndpointFingerprint        string
	RequestFingerprint         string
	StageFingerprint           string
	ContainerIDFingerprint     string
	ResultFingerprint          string
	ExitCode                   int
	DaemonReadCount            int
	DaemonWriteCount           int
	StartedAt                  time.Time
	CompletedAt                time.Time
	ConfigurationMatched       bool
	ContainerStarted           bool
	ProcessExecuted            bool
	WaitObserved               bool
	TimeoutObserved            bool
	CancellationObserved       bool
	GracefulSignalSent         bool
	ForcedSignalSent           bool
	FinalStateObserved         bool
	ContainerRemoved           bool
	TargetAbsenceConfirmed     bool
	CleanupConfirmed           bool
	OOMKilled                  bool
	OutputExported             bool
	ProbeExecutionAuthorized   bool
	ProductEntryEnabled        bool
	ProductExecutionAuthorized bool
	ArtifactCommitAuthorized   bool
}

func newDockerContainerLifecycleResult(request DockerContainerLifecycleRequest,
	endpoint DockerObservationEndpoint, status string, exitCode, reads, writes int,
	startedAt, completedAt time.Time, graceful, forced bool,
) (DockerContainerLifecycleResult, error) {
	result := DockerContainerLifecycleResult{
		ProtocolVersion: DockerContainerLifecycleResultProtocolVersion,
		Trust:           DockerContainerLifecycleTrust, Status: status,
		EndpointClass: endpoint.Class, EndpointFingerprint: endpoint.Fingerprint,
		RequestFingerprint:     request.RequestFingerprint,
		StageFingerprint:       request.Stage.StageFingerprint,
		ContainerIDFingerprint: request.Stage.ContainerIDFingerprint,
		ExitCode:               exitCode, DaemonReadCount: reads, DaemonWriteCount: writes,
		StartedAt: startedAt.UTC(), CompletedAt: completedAt.UTC(),
		ConfigurationMatched: true, ContainerStarted: true, ProcessExecuted: true,
		WaitObserved: true, GracefulSignalSent: graceful, ForcedSignalSent: forced,
		FinalStateObserved: true, ContainerRemoved: true, TargetAbsenceConfirmed: true,
		CleanupConfirmed: true, ProbeExecutionAuthorized: true,
	}
	result.TimeoutObserved = status == DockerContainerLifecycleStatusTimedOut
	result.CancellationObserved = status == DockerContainerLifecycleStatusCancelled
	result.ResultFingerprint = dockerContainerLifecycleResultFingerprint(result)
	if result.Validate() != nil {
		return DockerContainerLifecycleResult{}, errors.New("docker container lifecycle result is invalid")
	}
	return result, nil
}

func (result DockerContainerLifecycleResult) Validate() error {
	endpoint, endpointErr := NewDockerObservationEndpoint(result.EndpointClass)
	validStatus := result.Status == DockerContainerLifecycleStatusExited ||
		result.Status == DockerContainerLifecycleStatusTimedOut ||
		result.Status == DockerContainerLifecycleStatusCancelled
	if endpointErr != nil || !validDockerContainerLocalEndpoint(endpoint) ||
		result.ProtocolVersion != DockerContainerLifecycleResultProtocolVersion ||
		result.Trust != DockerContainerLifecycleTrust || !validStatus ||
		result.EndpointFingerprint != endpoint.Fingerprint ||
		!validDigest(result.RequestFingerprint) || !validDigest(result.StageFingerprint) ||
		!validDigest(result.ContainerIDFingerprint) || !validDigest(result.ResultFingerprint) ||
		result.ExitCode < 0 || result.ExitCode > 255 || result.DaemonReadCount < 3 ||
		result.DaemonReadCount > 32 || result.DaemonWriteCount < 2 ||
		result.DaemonWriteCount > 8 || result.StartedAt.IsZero() ||
		result.CompletedAt.Before(result.StartedAt) || !result.ConfigurationMatched ||
		!result.ContainerStarted || !result.ProcessExecuted || !result.WaitObserved ||
		!result.FinalStateObserved || !result.ContainerRemoved ||
		!result.TargetAbsenceConfirmed || !result.CleanupConfirmed || result.OOMKilled ||
		result.OutputExported || !result.ProbeExecutionAuthorized ||
		result.ProductEntryEnabled || result.ProductExecutionAuthorized ||
		result.ArtifactCommitAuthorized ||
		result.ResultFingerprint != dockerContainerLifecycleResultFingerprint(result) {
		return errors.New("docker container lifecycle result violates the non-authorizing boundary")
	}
	switch result.Status {
	case DockerContainerLifecycleStatusExited:
		if result.TimeoutObserved || result.CancellationObserved ||
			result.GracefulSignalSent || result.ForcedSignalSent {
			return errors.New("natural Docker lifecycle result contains cancellation evidence")
		}
	case DockerContainerLifecycleStatusTimedOut:
		if !result.TimeoutObserved || result.CancellationObserved || !result.GracefulSignalSent {
			return errors.New("timed-out Docker lifecycle result lacks termination evidence")
		}
	case DockerContainerLifecycleStatusCancelled:
		if result.TimeoutObserved || !result.CancellationObserved || !result.GracefulSignalSent {
			return errors.New("cancelled Docker lifecycle result lacks termination evidence")
		}
	}
	if result.ForcedSignalSent && !result.GracefulSignalSent {
		return errors.New("forced Docker termination must follow graceful termination")
	}
	return nil
}

func dockerContainerLifecycleResultFingerprint(result DockerContainerLifecycleResult) string {
	return fingerprint(DockerContainerLifecycleResultProtocolVersion, result.Trust, result.Status,
		result.EndpointClass, result.EndpointFingerprint, result.RequestFingerprint,
		result.StageFingerprint, result.ContainerIDFingerprint, strconv.Itoa(result.ExitCode),
		strconv.Itoa(result.DaemonReadCount), strconv.Itoa(result.DaemonWriteCount),
		result.StartedAt.Format(time.RFC3339Nano), result.CompletedAt.Format(time.RFC3339Nano),
		strconv.FormatBool(result.ConfigurationMatched), strconv.FormatBool(result.ContainerStarted),
		strconv.FormatBool(result.ProcessExecuted), strconv.FormatBool(result.WaitObserved),
		strconv.FormatBool(result.TimeoutObserved), strconv.FormatBool(result.CancellationObserved),
		strconv.FormatBool(result.GracefulSignalSent), strconv.FormatBool(result.ForcedSignalSent),
		strconv.FormatBool(result.FinalStateObserved), strconv.FormatBool(result.ContainerRemoved),
		strconv.FormatBool(result.TargetAbsenceConfirmed), strconv.FormatBool(result.CleanupConfirmed),
		strconv.FormatBool(result.OOMKilled), strconv.FormatBool(result.OutputExported),
		strconv.FormatBool(result.ProbeExecutionAuthorized), strconv.FormatBool(result.ProductEntryEnabled),
		strconv.FormatBool(result.ProductExecutionAuthorized),
		strconv.FormatBool(result.ArtifactCommitAuthorized))
}

type DockerContainerLifecycleTransport interface {
	Endpoint() DockerObservationEndpoint
	Stage(context.Context, DockerContainerWriteRequest) (DockerContainerStageResult, error)
	Run(context.Context, DockerContainerLifecycleRequest) (DockerContainerLifecycleResult, error)
}

type UnavailableDockerContainerLifecycleTransport struct {
	endpoint DockerObservationEndpoint
	reason   string
}

func NewUnavailableDockerContainerLifecycleTransport(endpointClass, reason string) DockerContainerLifecycleTransport {
	endpoint, err := NewDockerObservationEndpoint(endpointClass)
	if err != nil {
		endpoint, _ = NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	}
	return UnavailableDockerContainerLifecycleTransport{endpoint: endpoint, reason: reason}
}

func (transport UnavailableDockerContainerLifecycleTransport) Endpoint() DockerObservationEndpoint {
	return transport.endpoint
}

func (transport UnavailableDockerContainerLifecycleTransport) Stage(ctx context.Context,
	_ DockerContainerWriteRequest,
) (DockerContainerStageResult, error) {
	if err := ctx.Err(); err != nil {
		return DockerContainerStageResult{}, err
	}
	code := DockerContainerLifecycleFailureDisabled
	if transport.reason == DockerContainerLifecycleFailureUnsupported {
		code = DockerContainerLifecycleFailureUnsupported
	}
	return DockerContainerStageResult{}, newDockerContainerLifecycleError(code)
}

func (transport UnavailableDockerContainerLifecycleTransport) Run(ctx context.Context,
	_ DockerContainerLifecycleRequest,
) (DockerContainerLifecycleResult, error) {
	if err := ctx.Err(); err != nil {
		return DockerContainerLifecycleResult{}, err
	}
	code := DockerContainerLifecycleFailureDisabled
	if transport.reason == DockerContainerLifecycleFailureUnsupported {
		code = DockerContainerLifecycleFailureUnsupported
	}
	return DockerContainerLifecycleResult{}, newDockerContainerLifecycleError(code)
}
