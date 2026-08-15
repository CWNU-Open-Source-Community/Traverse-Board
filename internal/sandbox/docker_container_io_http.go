package sandbox

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const (
	DockerContainerIOFailureInvalidResponse = "invalid_response"
	DockerContainerIOFailureConnection      = "connection_failed"
	DockerContainerIOFailureUnavailable     = "unavailable"
	DockerContainerIOFailureConfigMismatch  = "configuration_mismatch"
)

// DockerContainerIOTransport is the closed daemon surface for the container
// I/O contract: exactly one bounded attach for logs and one archive export of
// the dedicated output mount. Both return raw streams that the domain layer
// bounds and validates; no other verb is reachable.
type DockerContainerIOTransport interface {
	Endpoint() DockerObservationEndpoint
	AttachLogs(ctx context.Context, plan DockerLogCapturePlan) (io.ReadCloser, error)
	ExportOutputs(ctx context.Context, plan DockerOutputExportPlan) (io.ReadCloser, error)
}

// DockerContainerOwnedIOTransport is the product-safe I/O surface. Unlike the
// compatibility methods above, it never treats a persisted container-ID
// fingerprint as a Docker reference. The exact lifecycle request is inspected
// by its deterministic name and rebound to the daemon's raw container ID before
// either stream is opened.
type DockerContainerOwnedIOTransport interface {
	DockerContainerIOTransport
	AttachOwnedLogs(ctx context.Context, request DockerContainerLifecycleRequest,
		plan DockerLogCapturePlan) (io.ReadCloser, error)
	ExportOwnedOutputs(ctx context.Context, request DockerContainerLifecycleRequest,
		plan DockerOutputExportPlan) (io.ReadCloser, error)
}

type dockerEngineContainerIOTransport struct {
	doer     dockerContainerWriteHTTPDoer
	endpoint DockerObservationEndpoint
	write    dockerEngineContainerWriteTransport
}

func newDockerEngineContainerIOTransport(doer dockerContainerWriteHTTPDoer,
	endpoint DockerObservationEndpoint,
) (dockerEngineContainerIOTransport, error) {
	if doer == nil {
		return dockerEngineContainerIOTransport{}, errors.New("docker I/O HTTP client is required")
	}
	if !validDockerContainerLocalEndpoint(endpoint) {
		return dockerEngineContainerIOTransport{}, errors.New("docker I/O transport requires a fixed local endpoint")
	}
	write, err := newDockerEngineContainerWriteTransport(doer, endpoint)
	if err != nil {
		return dockerEngineContainerIOTransport{}, err
	}
	return dockerEngineContainerIOTransport{doer: doer, endpoint: endpoint, write: write}, nil
}

func (transport dockerEngineContainerIOTransport) Endpoint() DockerObservationEndpoint {
	return transport.endpoint
}

// AttachLogs opens the bounded historical log stream. stream=0 means the
// daemon returns the collected logs and closes; live streaming is never used.
func (transport dockerEngineContainerIOTransport) AttachLogs(ctx context.Context,
	plan DockerLogCapturePlan,
) (io.ReadCloser, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return transport.attachLogs(ctx, plan.ContainerIDFingerprint)
}

// ExportOutputs opens the tar archive of the dedicated output mount.
func (transport dockerEngineContainerIOTransport) ExportOutputs(ctx context.Context,
	plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return transport.exportOutputs(ctx, plan.ContainerIDFingerprint, plan.OutputMountTarget)
}

// AttachOwnedLogs verifies the exact durable lifecycle ownership before using
// the daemon-returned raw container ID for the bounded historical attach.
func (transport dockerEngineContainerIOTransport) AttachOwnedLogs(ctx context.Context,
	request DockerContainerLifecycleRequest, plan DockerLogCapturePlan,
) (io.ReadCloser, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	containerID, err := transport.resolveOwnedContainer(ctx, request, plan.AttemptID,
		plan.Generation, plan.RunID, plan.ContainerIDFingerprint)
	if err != nil {
		return nil, err
	}
	return transport.attachLogs(ctx, containerID)
}

// ExportOwnedOutputs verifies the exact durable lifecycle ownership before
// using the daemon-returned raw container ID for the dedicated output archive.
func (transport dockerEngineContainerIOTransport) ExportOwnedOutputs(ctx context.Context,
	request DockerContainerLifecycleRequest, plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	outputTargetMatched := false
	for _, mount := range request.WriteRequest.Spec.Mounts {
		if mount.DedicatedOutput && mount.Target == plan.OutputMountTarget {
			outputTargetMatched = true
			break
		}
	}
	if !outputTargetMatched {
		return nil, newDockerContainerIOError(DockerContainerIOFailureConfigMismatch)
	}
	containerID, err := transport.resolveOwnedContainer(ctx, request, plan.AttemptID,
		plan.Generation, plan.RunID, plan.ContainerIDFingerprint)
	if err != nil {
		return nil, err
	}
	return transport.exportOutputs(ctx, containerID, plan.OutputMountTarget)
}

func (transport dockerEngineContainerIOTransport) resolveOwnedContainer(ctx context.Context,
	request DockerContainerLifecycleRequest, attemptID string, generation int64, runID,
	containerIDFingerprint string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if request.Validate() != nil || request.Ownership.isZero() ||
		request.Ownership.Validate() != nil || request.AttemptID != attemptID ||
		request.WriteRequest.Spec.RunID != runID ||
		request.Ownership.ResourceGeneration != generation ||
		request.Ownership.BaseLabelPlanFingerprint != request.WriteRequest.Spec.LabelPlanFingerprint ||
		request.ResourceIDFingerprint == "" ||
		request.ResourceIDFingerprint != containerIDFingerprint ||
		(request.Stage.ProtocolVersion != "" &&
			request.Stage.EndpointFingerprint != transport.endpoint.Fingerprint) {
		return "", newDockerContainerIOError(DockerContainerIOFailureConfigMismatch)
	}
	expectedLabels, err := dockerContainerLifecycleOwnedLabels(
		request.WriteRequest.Spec.Labels, request.Ownership)
	if err != nil || len(expectedLabels) != 9 {
		return "", newDockerContainerIOError(DockerContainerIOFailureConfigMismatch)
	}
	inspection, found, err := transport.write.inspect(ctx,
		request.WriteRequest.Spec.ContainerName)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", mapDockerContainerWriteIOError(err)
	}
	if !found {
		return "", newDockerContainerIOError(DockerContainerIOFailureUnavailable)
	}
	if verifyDockerContainerConfigurationWithLabels(inspection, request.WriteRequest,
		expectedLabels) != nil ||
		fingerprint("sandbox_docker_container_id.v1", inspection.ID) !=
			request.ResourceIDFingerprint {
		return "", newDockerContainerIOError(DockerContainerIOFailureConfigMismatch)
	}
	return inspection.ID, nil
}

func mapDockerContainerWriteIOError(err error) error {
	if err == nil {
		return nil
	}
	if DockerContainerWriteErrorCode(err) == DockerContainerWriteFailureConnection {
		return newDockerContainerIOError(DockerContainerIOFailureConnection)
	}
	return newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
}

func (transport dockerEngineContainerIOTransport) attachLogs(ctx context.Context,
	containerID string,
) (io.ReadCloser, error) {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(containerID) + "/attach"
	return transport.doStream(ctx, http.MethodPost, path,
		"logs=1&stderr=1&stdout=1&stream=0")
}

func (transport dockerEngineContainerIOTransport) exportOutputs(ctx context.Context,
	containerID, outputMountTarget string,
) (io.ReadCloser, error) {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(containerID) + "/archive"
	return transport.doStream(ctx, http.MethodGet, path,
		"path="+url.QueryEscape(outputMountTarget))
}

func (transport dockerEngineContainerIOTransport) doStream(ctx context.Context,
	method, path, rawQuery string,
) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validDockerContainerIOOperation(method, path, rawQuery) {
		return nil, newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
	}
	requestURL := "http://docker" + path
	if rawQuery != "" {
		requestURL += "?" + rawQuery
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
	}
	request.Header.Set("User-Agent", "cyberagent-workbench/docker-container-io-v1")
	if method == http.MethodGet {
		request.Header.Set("Accept", "application/x-tar")
	}
	response, err := transport.doer.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, newDockerContainerIOError(DockerContainerIOFailureConnection)
	}
	if response == nil || response.Body == nil {
		return nil, newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
	}
	if response.Request == nil || response.Request.URL == nil ||
		response.Request.Method != method || response.Request.URL.Scheme != "http" ||
		response.Request.URL.Host != "docker" || response.Request.URL.Path != path ||
		response.Request.URL.RawQuery != rawQuery {
		_ = response.Body.Close()
		return nil, newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		return nil, newDockerContainerIOError(DockerContainerIOFailureUnavailable)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
	}
	if method == http.MethodGet {
		if contentType := response.Header.Get("Content-Type"); contentType != "" {
			mediaType, _, parseErr := mime.ParseMediaType(contentType)
			if parseErr != nil || !strings.EqualFold(mediaType, "application/x-tar") {
				_ = response.Body.Close()
				return nil, newDockerContainerIOError(DockerContainerIOFailureInvalidResponse)
			}
		}
	}
	return response.Body, nil
}

// validDockerContainerIOOperation pins the exact attach/archive surface.
// Anything else, including every other query shape, fails closed.
func validDockerContainerIOOperation(method, path, rawQuery string) bool {
	prefix := "/v" + DockerContainerWriteAPIVersion + "/containers/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	tail := strings.TrimPrefix(path, prefix)
	switch method {
	case http.MethodPost:
		if !strings.HasSuffix(tail, "/attach") {
			return false
		}
		reference, err := url.PathUnescape(strings.TrimSuffix(tail, "/attach"))
		if err != nil || !validDockerContainerID(reference) ||
			tail != url.PathEscape(reference)+"/attach" {
			return false
		}
		return rawQuery == "logs=1&stderr=1&stdout=1&stream=0"
	case http.MethodGet:
		if !strings.HasSuffix(tail, "/archive") {
			return false
		}
		reference, err := url.PathUnescape(strings.TrimSuffix(tail, "/archive"))
		if err != nil || !validDockerContainerID(reference) ||
			tail != url.PathEscape(reference)+"/archive" {
			return false
		}
		values, err := url.ParseQuery(rawQuery)
		if err != nil || len(values) != 1 || len(values["path"]) != 1 {
			return false
		}
		target := values.Get("path")
		return rawQuery == "path="+url.QueryEscape(target) &&
			validateVirtualPath("docker archive target", target) == nil
	default:
		return false
	}
}

type unavailableDockerContainerIOTransport struct {
	endpoint DockerObservationEndpoint
	reason   string
}

// NewUnavailableDockerContainerIOTransport returns the fixed fail-closed
// transport for environments without the local Docker endpoint. It is the
// only constructor exported from this package for the I/O surface.
func NewUnavailableDockerContainerIOTransport(endpointClass,
	reason string,
) DockerContainerIOTransport {
	endpoint, err := NewDockerObservationEndpoint(endpointClass)
	if err != nil {
		endpoint, _ = NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	}
	return unavailableDockerContainerIOTransport{endpoint: endpoint, reason: reason}
}

func (transport unavailableDockerContainerIOTransport) Endpoint() DockerObservationEndpoint {
	return transport.endpoint
}

func (transport unavailableDockerContainerIOTransport) AttachLogs(ctx context.Context,
	plan DockerLogCapturePlan,
) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, newDockerContainerIOError(DockerContainerIOFailureUnavailable)
}

func (transport unavailableDockerContainerIOTransport) ExportOutputs(ctx context.Context,
	plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, newDockerContainerIOError(DockerContainerIOFailureUnavailable)
}

func (transport unavailableDockerContainerIOTransport) AttachOwnedLogs(ctx context.Context,
	request DockerContainerLifecycleRequest, plan DockerLogCapturePlan,
) (io.ReadCloser, error) {
	return transport.AttachLogs(ctx, plan)
}

func (transport unavailableDockerContainerIOTransport) ExportOwnedOutputs(ctx context.Context,
	request DockerContainerLifecycleRequest, plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	return transport.ExportOutputs(ctx, plan)
}

type DockerContainerIOError struct {
	code string
}

func (err *DockerContainerIOError) Error() string {
	if err == nil {
		return "docker container I/O error"
	}
	return "docker container I/O " + err.code
}

func newDockerContainerIOError(code string) error {
	return &DockerContainerIOError{code: code}
}

func DockerContainerIOErrorCode(err error) string {
	var target *DockerContainerIOError
	if errors.As(err, &target) && target != nil {
		return target.code
	}
	return ""
}
