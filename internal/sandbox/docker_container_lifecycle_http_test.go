package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
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

func newOwnedDockerLifecycleTestTransport(t *testing.T, mode string,
	fence DockerContainerLifecycleFence,
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
	ownership, err := NewDockerContainerLifecycleOwnership(
		"docker-lifecycle-owned-test-attempt", 1,
		fingerprint("docker-lifecycle-owned-test-intent"),
		writeRequest.Spec.LabelPlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := transport.StageOwned(context.Background(), writeRequest, ownership, fence)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewOwnedDockerContainerLifecycleRequest(ownership.AttemptID, 1,
		writeRequest, stage, ownership, DockerContainerLifecycleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	return daemon, transport, request
}

func allowDockerLifecycleFence(context.Context, DockerContainerLifecycleActionKind) error {
	return nil
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

func TestDockerContainerLifecycleStageOwnedRequiresCreateFence(t *testing.T) {
	writeRequest := newDockerContainerWriteTestRequest(t)
	endpoint := mustDockerLifecycleEndpoint(t)
	daemon := &dockerLifecycleTestDaemon{}
	transport, err := newDockerEngineContainerLifecycleTransport(daemon, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := NewDockerContainerLifecycleOwnership("stale-create-attempt", 1,
		fingerprint("stale-create-intent"), writeRequest.Spec.LabelPlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	stale := errors.New("stale lifecycle lease")
	fence := func(_ context.Context, action DockerContainerLifecycleActionKind) error {
		if action != DockerContainerLifecycleActionCreate {
			t.Fatalf("unexpected action before create: %q", action)
		}
		return stale
	}
	_, err = transport.StageOwned(context.Background(), writeRequest, ownership, fence)
	if !errors.Is(err, stale) || daemon.base.creates != 0 || daemon.base.containerID != "" {
		t.Fatalf("stale create fence allowed mutation: creates=%d id=%q err=%v",
			daemon.base.creates, daemon.base.containerID, err)
	}
}

func TestDockerContainerLifecycleStageOwnedAdoptsUncertainCreateWithoutRefencing(t *testing.T) {
	writeRequest := newDockerContainerWriteTestRequest(t)
	endpoint := mustDockerLifecycleEndpoint(t)
	daemon := &dockerLifecycleTestDaemon{}
	transport, err := newDockerEngineContainerLifecycleTransport(daemon, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := NewDockerContainerLifecycleOwnership("uncertain-create-attempt", 1,
		fingerprint("uncertain-create-intent"), writeRequest.Spec.LabelPlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	actions := []DockerContainerLifecycleActionKind{}
	fence := func(_ context.Context, action DockerContainerLifecycleActionKind) error {
		actions = append(actions, action)
		return nil
	}
	first, err := transport.StageOwned(context.Background(), writeRequest, ownership, fence)
	if err != nil {
		t.Fatal(err)
	}
	second, err := transport.StageOwned(context.Background(), writeRequest, ownership,
		func(context.Context, DockerContainerLifecycleActionKind) error {
			t.Fatal("exact post-create recovery attempted another create fence")
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0] != DockerContainerLifecycleActionCreate ||
		daemon.base.creates != 1 || first.ContainerIDFingerprint != second.ContainerIDFingerprint ||
		!second.ExistingContainerAdopted || second.ContainerCreatedNow {
		t.Fatalf("uncertain create did not converge exactly: actions=%v creates=%d first=%#v second=%#v",
			actions, daemon.base.creates, first, second)
	}
}

func TestDockerContainerLifecycleOwnedRecoveryDoesNotRestartRunningOrExited(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    string
		running  bool
		pid      int
		exitCode int
		expected string
	}{
		{name: "running", state: "running", running: true, pid: 42,
			expected: DockerContainerLifecycleStateRunning},
		{name: "exited", state: "exited", exitCode: 19,
			expected: DockerContainerLifecycleStateExited},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
				dockerLifecycleExitNatural, allowDockerLifecycleFence)
			daemon.mu.Lock()
			daemon.state, daemon.running, daemon.pid, daemon.exitCode =
				test.state, test.running, test.pid, test.exitCode
			daemon.mu.Unlock()
			observation, err := transport.Observe(context.Background(), request)
			if err != nil || observation.Validate() != nil || observation.State != test.expected {
				t.Fatalf("recovery observation is invalid: %#v err=%v", observation, err)
			}
			beforeStarts := daemon.starts
			_, started, err := transport.Start(context.Background(), request,
				func(context.Context, DockerContainerLifecycleActionKind) error {
					t.Fatal("idempotent recovery attempted to fence a duplicate start")
					return nil
				})
			if err != nil || started || daemon.starts != beforeStarts {
				t.Fatalf("recovery restarted %s container: started=%v starts=%d err=%v",
					test.name, started, daemon.starts, err)
			}
		})
	}
}

func TestDockerContainerLifecycleRecoveredRequestRequiresDurableResourceIdentity(t *testing.T) {
	_, _, staged := newOwnedDockerLifecycleTestTransport(t,
		dockerLifecycleExitNatural, allowDockerLifecycleFence)
	if _, err := NewRecoveredDockerContainerLifecycleRequest(staged.AttemptID,
		staged.LeaseGeneration, staged.WriteRequest, "", staged.Ownership,
		DockerContainerLifecycleConfirmation); err == nil {
		t.Fatal("recovery request accepted an unbound container identity")
	}
	recovered, err := NewRecoveredDockerContainerLifecycleRequest(staged.AttemptID,
		staged.LeaseGeneration, staged.WriteRequest, staged.ResourceIDFingerprint,
		staged.Ownership, DockerContainerLifecycleConfirmation)
	if err != nil || recovered.ResourceIDFingerprint != staged.ResourceIDFingerprint ||
		recovered.Stage.ProtocolVersion != "" || recovered.Validate() != nil {
		t.Fatalf("durably-bound recovery request is invalid: %#v err=%v", recovered, err)
	}
}

func TestDockerContainerLifecycleOwnedAbsentAndExitedOperationsAreIdempotent(t *testing.T) {
	t.Run("absent observe and cleanup", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitNatural, allowDockerLifecycleFence)
		daemon.base.mu.Lock()
		daemon.base.containerID = ""
		daemon.base.mu.Unlock()
		observation, err := transport.Observe(context.Background(), request)
		if err != nil || observation.Validate() != nil ||
			observation.State != DockerContainerLifecycleStateAbsent {
			t.Fatalf("absent container was not safely observed: %#v err=%v", observation, err)
		}
		result, err := transport.Cleanup(context.Background(), request,
			func(context.Context, DockerContainerLifecycleActionKind) error {
				t.Fatal("idempotent absent cleanup attempted a DELETE")
				return nil
			})
		if err != nil || !result.AlreadyAbsent || !result.AbsenceConfirmed ||
			result.ContainerRemoved || result.DaemonWriteCount != 0 {
			t.Fatalf("absent cleanup is not idempotent: %#v err=%v", result, err)
		}
	})
	t.Run("exited terminate", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitNatural, allowDockerLifecycleFence)
		daemon.mu.Lock()
		daemon.state, daemon.exitCode = "exited", 23
		daemon.mu.Unlock()
		result, err := transport.Terminate(context.Background(), request,
			func(context.Context, DockerContainerLifecycleActionKind) error {
				t.Fatal("idempotent exited termination attempted a signal or wait")
				return nil
			})
		if err != nil || !result.AlreadyStopped || result.ExitCode != 23 ||
			result.GracefulSignalSent || result.ForcedSignalSent ||
			daemon.terms != 0 || daemon.kills != 0 || daemon.waits != 0 {
			t.Fatalf("exited termination is not idempotent: %#v daemon=%#v err=%v",
				result, daemon, err)
		}
	})
}

func TestDockerContainerLifecycleOwnedObserveRejectsLegacyLabels(t *testing.T) {
	daemon, transport, legacy := newDockerLifecycleTestTransport(t, dockerLifecycleExitNatural)
	ownership, err := NewDockerContainerLifecycleOwnership(legacy.AttemptID, 1,
		fingerprint("owned-intent"), legacy.WriteRequest.Spec.LabelPlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewOwnedDockerContainerLifecycleRequest(legacy.AttemptID, 1,
		legacy.WriteRequest, legacy.Stage, ownership, DockerContainerLifecycleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.Observe(context.Background(), request)
	if DockerContainerLifecycleErrorCode(err) != DockerContainerLifecycleFailureUnsafeExisting ||
		daemon.starts != 0 || daemon.base.deletes != 0 {
		t.Fatalf("owned recovery accepted legacy six-label container: daemon=%#v err=%v",
			daemon, err)
	}
}

func TestDockerContainerLifecycleOwnedObserveRejectsPartialAndExtraLabelsWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "missing attempt", mutate: func(labels map[string]string) {
			delete(labels, DockerContainerLifecycleLabelAttempt)
		}},
		{name: "missing generation", mutate: func(labels map[string]string) {
			delete(labels, DockerContainerLifecycleLabelResourceGeneration)
		}},
		{name: "missing intent", mutate: func(labels map[string]string) {
			delete(labels, DockerContainerLifecycleLabelIntent)
		}},
		{name: "extra label", mutate: func(labels map[string]string) {
			labels["io.cyberagent.lifecycle.extra"] = "unexpected"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
				dockerLifecycleExitNatural, allowDockerLifecycleFence)
			daemon.base.mu.Lock()
			test.mutate(daemon.base.payload.Labels)
			daemon.base.mu.Unlock()
			_, err := transport.Observe(context.Background(), request)
			if DockerContainerLifecycleErrorCode(err) != DockerContainerLifecycleFailureUnsafeExisting ||
				daemon.starts != 0 || daemon.terms != 0 || daemon.kills != 0 ||
				daemon.base.deletes != 0 || daemon.base.containerID == "" {
				t.Fatalf("changed ownership labels reached mutation: daemon=%#v err=%v", daemon, err)
			}
		})
	}
}

func dockerContainerLifecycleFailClosedOperations() []struct {
	name    string
	prepare func(*dockerLifecycleTestDaemon)
	run     func(dockerEngineContainerLifecycleTransport, DockerContainerLifecycleRequest) error
} {
	return []struct {
		name    string
		prepare func(*dockerLifecycleTestDaemon)
		run     func(dockerEngineContainerLifecycleTransport, DockerContainerLifecycleRequest) error
	}{
		{name: "start", run: func(transport dockerEngineContainerLifecycleTransport,
			request DockerContainerLifecycleRequest,
		) error {
			_, _, err := transport.Start(context.Background(), request,
				allowDockerLifecycleFence)
			return err
		}},
		{name: "terminate", prepare: func(daemon *dockerLifecycleTestDaemon) {
			daemon.mu.Lock()
			defer daemon.mu.Unlock()
			daemon.state, daemon.running, daemon.pid = "running", true, 42
		}, run: func(transport dockerEngineContainerLifecycleTransport,
			request DockerContainerLifecycleRequest,
		) error {
			transport.graceOverride = time.Millisecond
			_, err := transport.Terminate(context.Background(), request,
				allowDockerLifecycleFence)
			return err
		}},
		{name: "cleanup", prepare: func(daemon *dockerLifecycleTestDaemon) {
			daemon.mu.Lock()
			defer daemon.mu.Unlock()
			daemon.state, daemon.running, daemon.pid, daemon.exitCode = "exited", false, 0, 0
		}, run: func(transport dockerEngineContainerLifecycleTransport,
			request DockerContainerLifecycleRequest,
		) error {
			_, err := transport.Cleanup(context.Background(), request,
				allowDockerLifecycleFence)
			return err
		}},
	}
}

func assertDockerContainerLifecycleDidNotMutate(t *testing.T,
	daemon *dockerLifecycleTestDaemon,
) {
	t.Helper()
	daemon.mu.Lock()
	starts, terms, kills := daemon.starts, daemon.terms, daemon.kills
	daemon.mu.Unlock()
	daemon.base.mu.Lock()
	deletes, containerID := daemon.base.deletes, daemon.base.containerID
	daemon.base.mu.Unlock()
	if starts != 0 || terms != 0 || kills != 0 || deletes != 0 || containerID == "" {
		t.Fatalf("failed-closed inspection reached a Docker mutation: starts=%d terms=%d kills=%d deletes=%d id=%q",
			starts, terms, kills, deletes, containerID)
	}
}

func TestDockerContainerLifecycleDaemonConnectionFailureIsFailClosed(t *testing.T) {
	for _, operation := range dockerContainerLifecycleFailClosedOperations() {
		t.Run(operation.name, func(t *testing.T) {
			daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
				dockerLifecycleExitKill, allowDockerLifecycleFence)
			if operation.prepare != nil {
				operation.prepare(daemon)
			}
			var requests []string
			unreachable := dockerLifecycleHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.Method+" "+request.URL.RequestURI())
				return nil, errors.New("Docker daemon unreachable")
			})
			transport.doer, transport.write.doer = unreachable, unreachable

			err := operation.run(transport, request)
			if DockerContainerLifecycleErrorCode(err) != DockerContainerLifecycleFailureConnection {
				t.Fatalf("daemon connection failure code=%q err=%v",
					DockerContainerLifecycleErrorCode(err), err)
			}
			if len(requests) != 1 || !strings.HasPrefix(requests[0], http.MethodGet+" ") ||
				!strings.HasSuffix(requests[0], "/json") {
				t.Fatalf("daemon connection failure issued requests after inspect: %v", requests)
			}
			assertDockerContainerLifecycleDidNotMutate(t, daemon)
		})
	}
}

func TestDockerContainerLifecycleConfigurationMismatchIsFailClosed(t *testing.T) {
	for _, operation := range dockerContainerLifecycleFailClosedOperations() {
		t.Run(operation.name, func(t *testing.T) {
			daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
				dockerLifecycleExitKill, allowDockerLifecycleFence)
			if operation.prepare != nil {
				operation.prepare(daemon)
			}
			daemon.base.mu.Lock()
			daemon.base.payload.WorkingDir = "/tampered-workdir"
			daemon.base.mu.Unlock()
			daemon.mu.Lock()
			requestCount := len(daemon.requests)
			daemon.mu.Unlock()

			err := operation.run(transport, request)
			if DockerContainerLifecycleErrorCode(err) != DockerContainerLifecycleFailureConfigMismatch {
				t.Fatalf("configuration mismatch code=%q err=%v",
					DockerContainerLifecycleErrorCode(err), err)
			}
			daemon.mu.Lock()
			requests := append([]string(nil), daemon.requests[requestCount:]...)
			daemon.mu.Unlock()
			if len(requests) != 1 || !strings.HasPrefix(requests[0], http.MethodGet+" ") ||
				!strings.HasSuffix(requests[0], "/json") {
				t.Fatalf("configuration mismatch issued requests after inspect: %v", requests)
			}
			assertDockerContainerLifecycleDidNotMutate(t, daemon)
		})
	}
}

func TestDockerContainerLifecycleStaleFencePreventsEachPostAndDelete(t *testing.T) {
	stale := errors.New("stale lifecycle lease")
	deny := func(expected DockerContainerLifecycleActionKind) DockerContainerLifecycleFence {
		return func(_ context.Context, actual DockerContainerLifecycleActionKind) error {
			if actual != expected {
				t.Fatalf("fenced action=%q, want %q", actual, expected)
			}
			return stale
		}
	}
	t.Run("start", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitNatural, allowDockerLifecycleFence)
		_, _, err := transport.Start(context.Background(), request,
			deny(DockerContainerLifecycleActionStart))
		if !errors.Is(err, stale) || daemon.starts != 0 {
			t.Fatalf("stale start fence allowed mutation: starts=%d err=%v", daemon.starts, err)
		}
	})
	t.Run("wait", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitKill, allowDockerLifecycleFence)
		if _, started, err := transport.Start(context.Background(), request,
			allowDockerLifecycleFence); err != nil || !started {
			t.Fatal(err)
		}
		_, err := transport.Wait(context.Background(), request,
			deny(DockerContainerLifecycleActionWait))
		if !errors.Is(err, stale) || daemon.waits != 0 {
			t.Fatalf("stale wait fence allowed POST: waits=%d err=%v", daemon.waits, err)
		}
	})
	t.Run("term", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitKill, allowDockerLifecycleFence)
		_, _, _ = transport.Start(context.Background(), request, allowDockerLifecycleFence)
		_, err := transport.Terminate(context.Background(), request,
			deny(DockerContainerLifecycleActionTERM))
		if !errors.Is(err, stale) || daemon.terms != 0 || daemon.kills != 0 {
			t.Fatalf("stale TERM fence allowed signal: terms=%d kills=%d err=%v",
				daemon.terms, daemon.kills, err)
		}
	})
	t.Run("kill", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitKill, allowDockerLifecycleFence)
		transport.graceOverride = 5 * time.Millisecond
		_, _, _ = transport.Start(context.Background(), request, allowDockerLifecycleFence)
		fence := func(_ context.Context, action DockerContainerLifecycleActionKind) error {
			if action == DockerContainerLifecycleActionKILL {
				return stale
			}
			return nil
		}
		_, err := transport.Terminate(context.Background(), request, fence)
		if !errors.Is(err, stale) || daemon.terms != 1 || daemon.kills != 0 {
			t.Fatalf("stale KILL fence allowed signal: terms=%d kills=%d err=%v",
				daemon.terms, daemon.kills, err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
			dockerLifecycleExitNatural, allowDockerLifecycleFence)
		_, err := transport.Cleanup(context.Background(), request,
			deny(DockerContainerLifecycleActionDelete))
		if !errors.Is(err, stale) || daemon.base.deletes != 0 || daemon.base.containerID == "" {
			t.Fatalf("stale delete fence allowed mutation: deletes=%d id=%q err=%v",
				daemon.base.deletes, daemon.base.containerID, err)
		}
	})
}

func TestDockerContainerLifecycleTerminationFenceSequenceMatchesDaemonRequests(t *testing.T) {
	daemon, transport, request := newOwnedDockerLifecycleTestTransport(t,
		dockerLifecycleExitKill, allowDockerLifecycleFence)
	transport.graceOverride = 5 * time.Millisecond
	if _, started, err := transport.Start(context.Background(), request,
		allowDockerLifecycleFence); err != nil || !started {
		t.Fatalf("start failed: started=%v err=%v", started, err)
	}
	var actions []DockerContainerLifecycleActionKind
	fence := func(_ context.Context, action DockerContainerLifecycleActionKind) error {
		actions = append(actions, action)
		return nil
	}
	result, err := transport.Terminate(context.Background(), request, fence)
	if err != nil {
		t.Fatal(err)
	}
	want := []DockerContainerLifecycleActionKind{
		DockerContainerLifecycleActionTERM,
		DockerContainerLifecycleActionWait,
		DockerContainerLifecycleActionKILL,
		DockerContainerLifecycleActionWait,
	}
	if !slices.Equal(actions, want) || !result.GracefulSignalSent ||
		!result.ForcedSignalSent || result.ExitCode != 137 || daemon.terms != 1 ||
		daemon.kills != 1 || daemon.waits != 2 {
		t.Fatalf("termination fence order differs from side effects: actions=%v want=%v result=%#v daemon=%#v",
			actions, want, result, daemon)
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
	if _, err := transport.wait(context.Background(), id, &dockerLifecycleCounters{}, nil); err == nil {
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
	if _, err := transport.wait(ctx, id, &dockerLifecycleCounters{}, nil); err != context.DeadlineExceeded {
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
