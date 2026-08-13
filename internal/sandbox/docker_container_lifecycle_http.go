package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxDockerContainerLifecycleResponseBytes = 2 * 1024 * 1024
	dockerContainerLifecycleCleanupTimeout   = 15 * time.Second
	dockerContainerLifecycleForcedWait       = 5 * time.Second
)

type dockerEngineContainerLifecycleTransport struct {
	doer            dockerContainerWriteHTTPDoer
	endpoint        DockerObservationEndpoint
	write           dockerEngineContainerWriteTransport
	timeoutOverride time.Duration
	graceOverride   time.Duration
}

func newDockerEngineContainerLifecycleTransport(doer dockerContainerWriteHTTPDoer,
	endpoint DockerObservationEndpoint,
) (dockerEngineContainerLifecycleTransport, error) {
	if doer == nil || !validDockerContainerLocalEndpoint(endpoint) {
		return dockerEngineContainerLifecycleTransport{},
			errors.New("docker lifecycle transport requires a fixed local endpoint")
	}
	write, err := newDockerEngineContainerWriteTransport(doer, endpoint)
	if err != nil {
		return dockerEngineContainerLifecycleTransport{}, err
	}
	return dockerEngineContainerLifecycleTransport{doer: doer, endpoint: endpoint, write: write}, nil
}

func (transport dockerEngineContainerLifecycleTransport) Endpoint() DockerObservationEndpoint {
	return transport.endpoint
}

func (transport dockerEngineContainerLifecycleTransport) Stage(ctx context.Context,
	request DockerContainerWriteRequest,
) (DockerContainerStageResult, error) {
	return transport.write.Stage(ctx, request)
}

type dockerLifecycleCounters struct {
	reads  int
	writes int
}

func (transport dockerEngineContainerLifecycleTransport) Run(ctx context.Context,
	request DockerContainerLifecycleRequest,
) (result DockerContainerLifecycleResult, returnedErr error) {
	if err := ctx.Err(); err != nil {
		return DockerContainerLifecycleResult{}, err
	}
	if request.Validate() != nil || transport.doer == nil ||
		!validDockerContainerLocalEndpoint(transport.endpoint) ||
		request.Stage.EndpointFingerprint != transport.endpoint.Fingerprint {
		return DockerContainerLifecycleResult{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureConfigMismatch)
	}
	counters := &dockerLifecycleCounters{}
	inspection, found, err := transport.inspect(ctx, request.WriteRequest.Spec.ContainerName, counters)
	if err != nil {
		return DockerContainerLifecycleResult{}, err
	}
	if !found || verifyDockerContainerInspection(inspection, request.WriteRequest) != nil ||
		fingerprint("sandbox_docker_container_id.v1", inspection.ID) !=
			request.Stage.ContainerIDFingerprint {
		return DockerContainerLifecycleResult{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureUnsafeExisting)
	}
	containerID := inspection.ID
	startedAt := time.Now().UTC()
	if err := transport.start(ctx, containerID, counters); err != nil {
		cleanupErr := transport.reconcileAfterFailure(request, containerID, counters)
		if cleanupErr != nil {
			return DockerContainerLifecycleResult{}, errors.Join(err, cleanupErr)
		}
		return DockerContainerLifecycleResult{}, err
	}

	timeout := time.Duration(request.WriteRequest.Spec.Termination.TimeoutSeconds) * time.Second
	if transport.timeoutOverride > 0 {
		timeout = transport.timeoutOverride
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	exitCode, waitErr := transport.wait(waitCtx, containerID, counters)
	waitCancel()
	if waitErr == nil {
		completedAt, cleanupErr := transport.verifyRemoveAndConfirm(
			context.Background(), request, containerID, exitCode, counters)
		if cleanupErr != nil {
			return DockerContainerLifecycleResult{}, cleanupErr
		}
		return newDockerContainerLifecycleResult(request, transport.endpoint,
			DockerContainerLifecycleStatusExited, exitCode, counters.reads, counters.writes,
			startedAt, completedAt, false, false)
	}
	if !errors.Is(waitErr, context.Canceled) && !errors.Is(waitErr, context.DeadlineExceeded) {
		waitErr = errors.Join(newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureWait), waitErr)
		cleanupErr := transport.reconcileAfterFailure(request, containerID, counters)
		if cleanupErr != nil {
			return DockerContainerLifecycleResult{}, errors.Join(waitErr, cleanupErr)
		}
		return DockerContainerLifecycleResult{}, waitErr
	}

	status := DockerContainerLifecycleStatusTimedOut
	contextErr := error(nil)
	if ctx.Err() != nil {
		status = DockerContainerLifecycleStatusCancelled
		contextErr = ctx.Err()
	}
	exitCode, graceful, forced, completedAt, cleanupErr := transport.terminateRemoveAndConfirm(
		request, containerID, counters)
	if cleanupErr != nil {
		return DockerContainerLifecycleResult{}, cleanupErr
	}
	result, resultErr := newDockerContainerLifecycleResult(request, transport.endpoint, status,
		exitCode, counters.reads, counters.writes, startedAt, completedAt, graceful, forced)
	if resultErr != nil {
		return DockerContainerLifecycleResult{}, resultErr
	}
	return result, contextErr
}

func (transport dockerEngineContainerLifecycleTransport) reconcileAfterFailure(
	request DockerContainerLifecycleRequest, containerID string, counters *dockerLifecycleCounters,
) error {
	_, _, _, _, err := transport.terminateRemoveAndConfirm(request, containerID, counters)
	if err != nil {
		return errors.Join(newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureCleanup), err)
	}
	return nil
}

func (transport dockerEngineContainerLifecycleTransport) terminateRemoveAndConfirm(
	request DockerContainerLifecycleRequest, containerID string, counters *dockerLifecycleCounters,
) (exitCode int, graceful, forced bool, completedAt time.Time, returnedErr error) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(),
		dockerContainerLifecycleCleanupTimeout)
	defer cleanupCancel()
	inspection, found, err := transport.inspect(cleanupCtx, containerID, counters)
	if err != nil {
		return 0, false, false, time.Time{}, err
	}
	if !found || inspection.ID != containerID ||
		fingerprint("sandbox_docker_container_id.v1", inspection.ID) !=
			request.Stage.ContainerIDFingerprint ||
		verifyDockerContainerConfiguration(inspection, request.WriteRequest) != nil {
		return 0, false, false, time.Time{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureUnsafeExisting)
	}
	if inspection.State.Running || inspection.State.Paused || inspection.State.Restarting {
		if err := transport.kill(cleanupCtx, containerID, DockerTerminationSignalGraceful,
			counters); err != nil {
			return 0, false, false, time.Time{}, err
		}
		graceful = true
		grace := time.Duration(request.WriteRequest.Spec.Termination.GracePeriodMillis) * time.Millisecond
		if transport.graceOverride > 0 {
			grace = transport.graceOverride
		}
		if grace <= 0 {
			grace = time.Millisecond
		}
		graceCtx, graceCancel := context.WithTimeout(cleanupCtx, grace)
		exitCode, err = transport.wait(graceCtx, containerID, counters)
		graceCancel()
		if err != nil {
			if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				return 0, graceful, false, time.Time{}, err
			}
			if err := transport.kill(cleanupCtx, containerID, DockerTerminationSignalForced,
				counters); err != nil {
				return 0, graceful, false, time.Time{}, err
			}
			forced = true
			forceCtx, forceCancel := context.WithTimeout(cleanupCtx,
				dockerContainerLifecycleForcedWait)
			exitCode, err = transport.wait(forceCtx, containerID, counters)
			forceCancel()
			if err != nil {
				return 0, graceful, forced, time.Time{}, err
			}
		}
	} else if inspection.State.Status == "created" && inspection.State.Pid == 0 {
		if err := transport.remove(cleanupCtx, containerID, counters); err != nil {
			return 0, false, false, time.Time{}, err
		}
		_, found, err := transport.inspect(cleanupCtx,
			request.WriteRequest.Spec.ContainerName, counters)
		if err != nil {
			return 0, false, false, time.Time{}, err
		}
		if found {
			return 0, false, false, time.Time{},
				newDockerContainerLifecycleError(DockerContainerLifecycleFailureCleanup)
		}
		return 0, false, false, time.Now().UTC(), nil
	} else {
		exitCode = inspection.State.ExitCode
	}
	completedAt, err = transport.verifyRemoveAndConfirm(cleanupCtx, request, containerID,
		exitCode, counters)
	return exitCode, graceful, forced, completedAt, err
}

func (transport dockerEngineContainerLifecycleTransport) verifyRemoveAndConfirm(ctx context.Context,
	request DockerContainerLifecycleRequest, containerID string, exitCode int,
	counters *dockerLifecycleCounters,
) (time.Time, error) {
	cleanupCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		cleanupCtx, cancel = context.WithTimeout(ctx, dockerContainerLifecycleCleanupTimeout)
		defer cancel()
	}
	inspection, found, err := transport.inspect(cleanupCtx, containerID, counters)
	if err != nil {
		return time.Time{}, err
	}
	if !found || inspection.ID != containerID ||
		fingerprint("sandbox_docker_container_id.v1", inspection.ID) !=
			request.Stage.ContainerIDFingerprint ||
		verifyDockerContainerFinalInspection(inspection, request.WriteRequest, exitCode) != nil {
		return time.Time{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureConfigMismatch)
	}
	if err := transport.remove(cleanupCtx, containerID, counters); err != nil {
		return time.Time{}, err
	}
	remaining, found, err := transport.inspect(cleanupCtx,
		request.WriteRequest.Spec.ContainerName, counters)
	if err != nil {
		return time.Time{}, err
	}
	if found {
		_ = remaining
		return time.Time{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureCleanup)
	}
	return time.Now().UTC(), nil
}

func verifyDockerContainerFinalInspection(inspection dockerContainerInspection,
	request DockerContainerWriteRequest, exitCode int,
) error {
	if verifyDockerContainerConfiguration(inspection, request) != nil ||
		inspection.State.Status != "exited" || inspection.State.Running ||
		inspection.State.Paused || inspection.State.Restarting || inspection.State.Dead ||
		inspection.State.OOMKilled || inspection.State.Pid != 0 ||
		inspection.State.ExitCode != exitCode || exitCode < 0 || exitCode > 255 {
		return newDockerContainerLifecycleError(DockerContainerLifecycleFailureConfigMismatch)
	}
	return nil
}

type dockerContainerLifecycleWaitResponse struct {
	StatusCode int `json:"StatusCode"`
	Error      *struct {
		Message string `json:"Message"`
	} `json:"Error"`
}

func (transport dockerEngineContainerLifecycleTransport) inspect(ctx context.Context,
	reference string, counters *dockerLifecycleCounters,
) (dockerContainerInspection, bool, error) {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(reference) + "/json"
	response, err := transport.do(ctx, http.MethodGet, path, "", nil, true)
	if err != nil {
		return dockerContainerInspection{}, false, err
	}
	counters.reads++
	if response.status == http.StatusNotFound {
		return dockerContainerInspection{}, false, nil
	}
	var inspection dockerContainerInspection
	if err := decodeDockerContainerLifecycleJSON(response.body, &inspection); err != nil {
		return dockerContainerInspection{}, false, err
	}
	return inspection, true, nil
}

func (transport dockerEngineContainerLifecycleTransport) start(ctx context.Context,
	containerID string, counters *dockerLifecycleCounters,
) error {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(containerID) + "/start"
	if _, err := transport.do(ctx, http.MethodPost, path, "", nil, false); err != nil {
		return errors.Join(newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureStart), err)
	}
	counters.writes++
	return nil
}

func (transport dockerEngineContainerLifecycleTransport) wait(ctx context.Context,
	containerID string, counters *dockerLifecycleCounters,
) (int, error) {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(containerID) + "/wait"
	response, err := transport.do(ctx, http.MethodPost, path, "condition=not-running", nil, true)
	if err != nil {
		return 0, err
	}
	counters.reads++
	var payload dockerContainerLifecycleWaitResponse
	if decodeDockerContainerLifecycleJSON(response.body, &payload) != nil ||
		payload.StatusCode < 0 || payload.StatusCode > 255 ||
		(payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "") {
		return 0, newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureInvalidResponse)
	}
	return payload.StatusCode, nil
}

func (transport dockerEngineContainerLifecycleTransport) kill(ctx context.Context,
	containerID, signal string, counters *dockerLifecycleCounters,
) error {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(containerID) + "/kill"
	if _, err := transport.do(ctx, http.MethodPost, path,
		"signal="+url.QueryEscape(signal), nil, false); err != nil {
		return errors.Join(newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureTerminate), err)
	}
	counters.writes++
	return nil
}

func (transport dockerEngineContainerLifecycleTransport) remove(ctx context.Context,
	containerID string, counters *dockerLifecycleCounters,
) error {
	path := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		url.PathEscape(containerID)
	if _, err := transport.do(ctx, http.MethodDelete, path, "v=1", nil, false); err != nil {
		return errors.Join(newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureCleanup), err)
	}
	counters.writes++
	return nil
}

type dockerContainerLifecycleHTTPResponse struct {
	status int
	body   []byte
}

func (transport dockerEngineContainerLifecycleTransport) do(ctx context.Context,
	method, path, rawQuery string, body []byte, wantJSON bool,
) (dockerContainerLifecycleHTTPResponse, error) {
	if err := ctx.Err(); err != nil {
		return dockerContainerLifecycleHTTPResponse{}, err
	}
	if !validDockerContainerLifecycleOperation(method, path, rawQuery, body) {
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	requestURL := "http://docker" + path
	if rawQuery != "" {
		requestURL += "?" + rawQuery
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "prayu/docker-lifecycle-probe-v1")
	response, err := transport.doer.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return dockerContainerLifecycleHTTPResponse{}, ctx.Err()
		}
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureConnection)
	}
	if response == nil || response.Body == nil {
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil ||
		response.Request.Method != method || response.Request.URL.Scheme != "http" ||
		response.Request.URL.Host != "docker" || response.Request.URL.Path != path ||
		response.Request.URL.RawQuery != rawQuery {
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	allowed := false
	switch {
	case method == http.MethodGet:
		allowed = response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNotFound
	case method == http.MethodPost && strings.HasSuffix(path, "/wait"):
		allowed = response.StatusCode == http.StatusOK
	case method == http.MethodPost:
		allowed = response.StatusCode == http.StatusNoContent
	case method == http.MethodDelete:
		allowed = response.StatusCode == http.StatusNoContent
	}
	if !allowed {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return dockerContainerLifecycleHTTPResponse{status: response.StatusCode}, nil
	}
	if wantJSON {
		contentType := response.Header.Get("Content-Type")
		if contentType != "" {
			mediaType, _, parseErr := mime.ParseMediaType(contentType)
			if parseErr != nil || !strings.EqualFold(mediaType, "application/json") {
				return dockerContainerLifecycleHTTPResponse{},
					newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
			}
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body,
		maxDockerContainerLifecycleResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return dockerContainerLifecycleHTTPResponse{}, ctx.Err()
		}
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	if len(data) == 0 || len(data) > maxDockerContainerLifecycleResponseBytes {
		return dockerContainerLifecycleHTTPResponse{},
			newDockerContainerLifecycleError(DockerContainerLifecycleFailureInvalidResponse)
	}
	return dockerContainerLifecycleHTTPResponse{status: response.StatusCode, body: data}, nil
}

func validDockerContainerLifecycleOperation(method, path, rawQuery string, body []byte) bool {
	if len(body) != 0 {
		return false
	}
	prefix := "/v" + DockerContainerWriteAPIVersion + "/containers/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	tail := strings.TrimPrefix(path, prefix)
	switch method {
	case http.MethodGet:
		if rawQuery != "" || !strings.HasSuffix(tail, "/json") {
			return false
		}
		reference, err := url.PathUnescape(strings.TrimSuffix(tail, "/json"))
		return err == nil && tail == url.PathEscape(reference)+"/json" &&
			(validDockerContainerID(reference) || validDockerContainerName(reference))
	case http.MethodPost:
		for _, operation := range []string{"start", "wait", "kill"} {
			suffix := "/" + operation
			if !strings.HasSuffix(tail, suffix) {
				continue
			}
			containerID, err := url.PathUnescape(strings.TrimSuffix(tail, suffix))
			if err != nil || !validDockerContainerID(containerID) ||
				tail != url.PathEscape(containerID)+suffix {
				return false
			}
			switch operation {
			case "start":
				return rawQuery == ""
			case "wait":
				return rawQuery == "condition=not-running"
			case "kill":
				return rawQuery == "signal=SIGTERM" || rawQuery == "signal=SIGKILL"
			}
		}
		return false
	case http.MethodDelete:
		containerID, err := url.PathUnescape(tail)
		return err == nil && validDockerContainerID(containerID) &&
			tail == url.PathEscape(containerID) && rawQuery == "v=1"
	default:
		return false
	}
}

func decodeDockerContainerLifecycleJSON(data []byte, target any) error {
	if !json.Valid(data) || rejectDuplicateDockerObservationJSON(data) != nil {
		return newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureInvalidResponse)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureInvalidResponse)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return newDockerContainerLifecycleError(
			DockerContainerLifecycleFailureInvalidResponse)
	}
	return nil
}
