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

type dockerEngineContainerIOTransport struct {
	doer     dockerContainerWriteHTTPDoer
	endpoint DockerObservationEndpoint
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
	return dockerEngineContainerIOTransport{doer: doer, endpoint: endpoint}, nil
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
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(plan.ContainerIDFingerprint) + "/attach"
	return transport.doStream(ctx, http.MethodPost, path,
		"logs=1&stderr=1&stdout=1&stream=0")
}

// ExportOutputs opens the tar archive of the dedicated output mount.
func (transport dockerEngineContainerIOTransport) ExportOutputs(ctx context.Context,
	plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(plan.ContainerIDFingerprint) + "/archive"
	return transport.doStream(ctx, http.MethodGet, path,
		"path="+url.QueryEscape(plan.OutputMountTarget))
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
