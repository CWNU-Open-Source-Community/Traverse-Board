package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	dockerLifecycleExitNatural = "natural"
	dockerLifecycleExitTerm    = "term"
	dockerLifecycleExitKill    = "kill"
)

type dockerLifecycleTestDaemon struct {
	mu       sync.Mutex
	base     dockerWriteTestDaemon
	mode     string
	state    string
	running  bool
	pid      int
	exitCode int
	starts   int
	terms    int
	kills    int
	waits    int
	requests []string
	unsafe   bool
}

func dockerContainerLifecycleWaitBody(exitCode int) []byte {
	return []byte(`{"StatusCode":` + strconv.Itoa(exitCode) + `}`)
}

func (daemon *dockerLifecycleTestDaemon) Do(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	daemon.mu.Lock()
	daemon.requests = append(daemon.requests, request.Method+" "+request.URL.RequestURI())
	path := request.URL.Path
	containerPrefix := "/v" + DockerContainerWriteAPIVersion + "/containers/"
	if request.Method == http.MethodPost && strings.HasPrefix(path, containerPrefix) {
		switch {
		case strings.HasSuffix(path, "/start"):
			daemon.starts++
			daemon.state, daemon.running, daemon.pid = "running", true, 42
			if daemon.mode == dockerLifecycleExitNatural {
				daemon.state, daemon.running, daemon.pid, daemon.exitCode = "exited", false, 0, 0
			}
			daemon.mu.Unlock()
			return dockerWriteTestResponse(request, http.StatusNoContent, nil), nil
		case strings.HasSuffix(path, "/wait"):
			daemon.waits++
			if !daemon.running && daemon.state == "exited" {
				exitCode := daemon.exitCode
				daemon.mu.Unlock()
				return dockerWriteTestResponse(request, http.StatusOK,
					dockerContainerLifecycleWaitBody(exitCode)), nil
			}
			daemon.mu.Unlock()
			<-request.Context().Done()
			return nil, request.Context().Err()
		case strings.HasSuffix(path, "/kill"):
			signal := request.URL.Query().Get("signal")
			if signal == DockerTerminationSignalGraceful {
				daemon.terms++
				if daemon.mode == dockerLifecycleExitTerm {
					daemon.state, daemon.running, daemon.pid, daemon.exitCode = "exited", false, 0, 143
				}
			} else if signal == DockerTerminationSignalForced {
				daemon.kills++
				daemon.state, daemon.running, daemon.pid, daemon.exitCode = "exited", false, 0, 137
			}
			daemon.mu.Unlock()
			return dockerWriteTestResponse(request, http.StatusNoContent, nil), nil
		}
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, containerPrefix) &&
		strings.HasSuffix(path, "/json") {
		payload, found := daemon.inspectPayloadLocked(request)
		daemon.mu.Unlock()
		if !found {
			return dockerWriteTestResponse(request, http.StatusNotFound, nil), nil
		}
		return dockerWriteTestResponse(request, http.StatusOK, payload), nil
	}
	daemon.mu.Unlock()
	response, err := daemon.base.Do(request)
	if err == nil && request.Method == http.MethodDelete && response.StatusCode == http.StatusNoContent {
		daemon.mu.Lock()
		daemon.state, daemon.running, daemon.pid = "", false, 0
		daemon.mu.Unlock()
	}
	return response, err
}

func (daemon *dockerLifecycleTestDaemon) inspectPayloadLocked(request *http.Request) ([]byte, bool) {
	daemon.base.mu.Lock()
	defer daemon.base.mu.Unlock()
	if daemon.base.containerID == "" {
		return nil, false
	}
	reference := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path,
		"/v"+DockerContainerWriteAPIVersion+"/containers/"), "/json")
	reference, _ = url.PathUnescape(reference)
	if reference != daemon.base.containerID && reference != daemon.base.name {
		return nil, false
	}
	payload := daemon.base.inspectPayload()
	var document map[string]any
	_ = json.Unmarshal(payload, &document)
	state := daemon.state
	if state == "" {
		state = "created"
	}
	document["State"] = map[string]any{
		"Status": state, "Running": daemon.running, "Paused": false,
		"Restarting": false, "OOMKilled": false, "Dead": false, "Pid": daemon.pid,
		"ExitCode": daemon.exitCode, "StartedAt": "2026-08-13T00:00:00Z",
		"FinishedAt": "2026-08-13T00:00:01Z",
	}
	if daemon.unsafe {
		config := document["Config"].(map[string]any)
		config["Labels"] = map[string]string{"io.cyberagent.managed": "false"}
	}
	data, _ := json.Marshal(document)
	return data, true
}

func newDockerLifecycleTestTransport(t *testing.T, mode string,
) (*dockerLifecycleTestDaemon, dockerEngineContainerLifecycleTransport,
	DockerContainerLifecycleRequest) {
	t.Helper()
	writeRequest := newDockerContainerWriteTestRequest(t)
	endpoint, _ := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	daemon := &dockerLifecycleTestDaemon{mode: mode}
	transport, err := newDockerEngineContainerLifecycleTransport(daemon, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := transport.Stage(context.Background(), writeRequest)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewDockerContainerLifecycleRequest("docker-lifecycle-test-attempt", 1,
		writeRequest, stage, DockerContainerLifecycleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	return daemon, transport, request
}

func TestDockerContainerLifecycleNaturalExitRemovesExactContainer(t *testing.T) {
	daemon, transport, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitNatural)
	result, err := transport.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Validate() != nil || result.Status != DockerContainerLifecycleStatusExited ||
		result.ExitCode != 0 || result.GracefulSignalSent || result.ForcedSignalSent ||
		daemon.starts != 1 || daemon.terms != 0 || daemon.kills != 0 ||
		daemon.base.containerID != "" {
		t.Fatalf("natural Docker lifecycle is invalid: result=%#v daemon=%#v", result, daemon)
	}
}

func TestDockerContainerLifecycleTimeoutEscalatesAndCleansUp(t *testing.T) {
	daemon, transport, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitKill)
	transport.timeoutOverride = 10 * time.Millisecond
	transport.graceOverride = 10 * time.Millisecond
	result, err := transport.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("timeout probe should return durable lifecycle evidence: %v", err)
	}
	if result.Validate() != nil || result.Status != DockerContainerLifecycleStatusTimedOut ||
		!result.TimeoutObserved || !result.GracefulSignalSent || !result.ForcedSignalSent ||
		result.ExitCode != 137 || daemon.starts != 1 || daemon.terms != 1 ||
		daemon.kills != 1 || daemon.base.containerID != "" {
		t.Fatalf("timed-out Docker lifecycle did not clean up: result=%#v daemon=%#v",
			result, daemon)
	}
}

func TestDockerContainerLifecycleCancellationFansOutAndCleansUp(t *testing.T) {
	daemon, transport, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitTerm)
	transport.timeoutOverride = time.Second
	transport.graceOverride = 25 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	result, err := transport.Run(ctx, request)
	if err != context.Canceled || result.Validate() != nil ||
		result.Status != DockerContainerLifecycleStatusCancelled ||
		!result.CancellationObserved || !result.GracefulSignalSent || result.ForcedSignalSent ||
		result.ExitCode != 143 || daemon.terms != 1 || daemon.kills != 0 ||
		daemon.base.containerID != "" {
		t.Fatalf("cancelled Docker lifecycle did not fan out safely: result=%#v daemon=%#v err=%v",
			result, daemon, err)
	}
}

func TestDockerContainerLifecycleRejectsChangedContainerWithoutMutation(t *testing.T) {
	daemon, transport, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitNatural)
	daemon.unsafe = true
	_, err := transport.Run(context.Background(), request)
	if DockerContainerLifecycleErrorCode(err) != DockerContainerLifecycleFailureUnsafeExisting ||
		daemon.starts != 0 || daemon.base.deletes != 0 || daemon.base.containerID == "" {
		t.Fatalf("unsafe Docker container was mutated: daemon=%#v err=%v", daemon, err)
	}
}

func TestDockerContainerLifecycleContractsRejectTamperingAndMissingConfirmation(t *testing.T) {
	_, _, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitNatural)
	if _, err := NewDockerContainerLifecycleRequest("docker-lifecycle-test-attempt", 1,
		request.WriteRequest, request.Stage, "wrong-confirmation"); err == nil {
		t.Fatal("Docker lifecycle request accepted missing exact confirmation")
	}
	request.ProductEntryEnabled = true
	request.RequestFingerprint = dockerContainerLifecycleRequestFingerprint(request)
	if request.Validate() == nil {
		t.Fatal("Docker lifecycle request enabled the product entry")
	}
}

func TestDockerContainerLifecycleResultCannotAuthorizeProductExecution(t *testing.T) {
	_, transport, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitNatural)
	result, err := transport.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result.ProductExecutionAuthorized = true
	result.ResultFingerprint = dockerContainerLifecycleResultFingerprint(result)
	if result.Validate() == nil {
		t.Fatal("Docker lifecycle result authorized product execution")
	}
}

type failDockerLifecycleStartDoer struct {
	delegate dockerContainerWriteHTTPDoer
}

func (doer failDockerLifecycleStartDoer) Do(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start") {
		return dockerWriteTestResponse(request, http.StatusInternalServerError,
			[]byte(`{"message":"start rejected"}`)), nil
	}
	return doer.delegate.Do(request)
}

func TestDockerContainerLifecycleStartFailureRemovesUnstartedContainer(t *testing.T) {
	daemon, transport, request := newDockerLifecycleTestTransport(t, dockerLifecycleExitNatural)
	transport.doer = failDockerLifecycleStartDoer{delegate: daemon}
	transport.write.doer = transport.doer
	_, err := transport.Run(context.Background(), request)
	if DockerContainerLifecycleErrorCode(err) != DockerContainerLifecycleFailureStart ||
		daemon.starts != 0 || daemon.base.deletes != 1 || daemon.base.containerID != "" {
		t.Fatalf("failed Docker start did not clean up its exact container: daemon=%#v err=%v",
			daemon, err)
	}
}

func TestDockerContainerLifecycleHTTPAllowlistIsClosed(t *testing.T) {
	id := strings.Repeat("b", 64)
	name := "cyberagent-" + strings.Repeat("a", 24)
	allowed := []struct{ method, path, query string }{
		{http.MethodGet, "/v1.40/containers/" + id + "/json", ""},
		{http.MethodGet, "/v1.40/containers/" + name + "/json", ""},
		{http.MethodPost, "/v1.40/containers/" + id + "/start", ""},
		{http.MethodPost, "/v1.40/containers/" + id + "/wait", "condition=not-running"},
		{http.MethodPost, "/v1.40/containers/" + id + "/kill", "signal=SIGTERM"},
		{http.MethodPost, "/v1.40/containers/" + id + "/kill", "signal=SIGKILL"},
		{http.MethodDelete, "/v1.40/containers/" + id, "v=1"},
	}
	for _, test := range allowed {
		if !validDockerContainerLifecycleOperation(test.method, test.path, test.query, nil) {
			t.Fatalf("Docker lifecycle allowlist rejected %s %s?%s", test.method, test.path, test.query)
		}
	}
	for _, test := range []struct {
		method, path, query string
		body                []byte
	}{
		{http.MethodPost, "/v1.40/containers/" + name + "/start", "", nil},
		{http.MethodPost, "/v1.40/containers/" + id + "/exec", "", nil},
		{http.MethodGet, "/v1.40/containers/" + id + "/logs", "", nil},
		{http.MethodPost, "/v1.40/containers/" + id + "/kill", "signal=9", nil},
		{http.MethodPost, "/v1.40/containers/" + id + "/wait", "", nil},
		{http.MethodDelete, "/v1.40/containers/" + id, "force=1", nil},
		{http.MethodPost, "/v1.40/containers/" + id + "/start", "", []byte(`{}`)},
		{http.MethodPost, "/v1.40/images/create", "fromImage=alpine", nil},
	} {
		if validDockerContainerLifecycleOperation(test.method, test.path, test.query, test.body) {
			t.Fatalf("Docker lifecycle allowlist accepted %s %s?%s",
				test.method, test.path, test.query)
		}
	}
}

func TestDockerContainerLifecycleRejectsRedirectedOrDuplicateJSON(t *testing.T) {
	id := strings.Repeat("b", 64)
	request, _ := http.NewRequest(http.MethodPost,
		"http://docker/v1.40/containers/"+id+"/wait?condition=not-running", nil)
	response := dockerWriteTestResponse(request, http.StatusOK,
		[]byte(`{"StatusCode":0,"StatusCode":1}`))
	response.Request.URL.Host = "redirected"
	transport, _ := newDockerEngineContainerLifecycleTransport(
		dockerLifecycleHTTPDoerFunc(func(*http.Request) (*http.Response, error) {
			return response, nil
		}), mustDockerLifecycleEndpoint(t))
	if _, err := transport.wait(context.Background(), id, &dockerLifecycleCounters{}); err == nil {
		t.Fatal("Docker lifecycle accepted a redirected duplicate response")
	}
}

type dockerLifecycleHTTPDoerFunc func(*http.Request) (*http.Response, error)

func (doer dockerLifecycleHTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return doer(request)
}

type dockerLifecycleBlockingBody struct {
	ctx context.Context
}

func (body dockerLifecycleBlockingBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (dockerLifecycleBlockingBody) Close() error { return nil }

func TestDockerContainerLifecycleRecognizesCancellationWhileReadingWaitBody(t *testing.T) {
	id := strings.Repeat("b", 64)
	transport, err := newDockerEngineContainerLifecycleTransport(
		dockerLifecycleHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
			response := dockerWriteTestResponse(request, http.StatusOK, nil)
			response.Header.Set("Content-Type", "application/json")
			response.Body = dockerLifecycleBlockingBody{ctx: request.Context()}
			return response, nil
		}), mustDockerLifecycleEndpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := transport.wait(ctx, id, &dockerLifecycleCounters{}); err != context.DeadlineExceeded {
		t.Fatalf("wait body cancellation was not preserved: %v", err)
	}
}

func mustDockerLifecycleEndpoint(t *testing.T) DockerObservationEndpoint {
	t.Helper()
	endpoint, err := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}
