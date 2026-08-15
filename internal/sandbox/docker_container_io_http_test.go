package sandbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/idgen"
)

type scriptedDockerIOHTTPDoer struct {
	status      int
	contentType string
	body        string
	breakEcho   bool
	requests    []*http.Request
}

type ownedDockerIOTestDoer struct {
	lifecycle *dockerLifecycleTestDaemon
	fail      error
	requests  []*http.Request
}

func (doer *ownedDockerIOTestDoer) Do(request *http.Request) (*http.Response, error) {
	doer.requests = append(doer.requests, request)
	if doer.fail != nil {
		return nil, doer.fail
	}
	switch {
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/attach"):
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader("raw")), Request: request}, nil
	case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/archive"):
		return &http.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": []string{"application/x-tar"}},
			Body:   io.NopCloser(strings.NewReader("archive")), Request: request}, nil
	default:
		return doer.lifecycle.Do(request)
	}
}

func testOwnedDockerIOTransport(t *testing.T) (*dockerLifecycleTestDaemon,
	*ownedDockerIOTestDoer, dockerEngineContainerIOTransport,
	DockerContainerLifecycleRequest) {
	t.Helper()
	daemon, _, request := newOwnedDockerLifecycleTestTransport(t,
		dockerLifecycleExitNatural, allowDockerLifecycleFence)
	daemon.mu.Lock()
	daemon.requests = nil
	daemon.mu.Unlock()
	daemon.base.mu.Lock()
	daemon.base.requests = nil
	daemon.base.mu.Unlock()
	doer := &ownedDockerIOTestDoer{lifecycle: daemon}
	endpoint, _ := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	transport, err := newDockerEngineContainerIOTransport(doer, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return daemon, doer, transport, request
}

func testOwnedDockerLogPlan(t *testing.T,
	request DockerContainerLifecycleRequest,
) DockerLogCapturePlan {
	t.Helper()
	plan, err := NewDockerLogCapturePlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, 1024, 64, 60)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testOwnedDockerOutputPlan(t *testing.T,
	request DockerContainerLifecycleRequest,
) DockerOutputExportPlan {
	t.Helper()
	outputTarget := ""
	for _, mount := range request.WriteRequest.Spec.Mounts {
		if mount.DedicatedOutput {
			outputTarget = mount.Target
			break
		}
	}
	plan, err := NewDockerOutputExportPlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, outputTarget, MaxDockerOutputFiles,
		MaxDockerOutputFileBytes, MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func (doer *scriptedDockerIOHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	doer.requests = append(doer.requests, request)
	response := &http.Response{
		StatusCode: doer.status,
		Body:       io.NopCloser(strings.NewReader(doer.body)),
		Header:     http.Header{},
	}
	if doer.contentType != "" {
		response.Header.Set("Content-Type", doer.contentType)
	}
	if !doer.breakEcho {
		response.Request = request
	}
	return response, nil
}

func testDockerIOTransport(t *testing.T, doer *scriptedDockerIOHTTPDoer) dockerEngineContainerIOTransport {
	t.Helper()
	endpoint, err := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newDockerEngineContainerIOTransport(doer, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func TestDockerContainerIOTransportAttachAndArchiveAllowlist(t *testing.T) {
	doer := &scriptedDockerIOHTTPDoer{status: http.StatusOK, body: "raw"}
	transport := testDockerIOTransport(t, doer)
	plan, err := NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"), 1,
		idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(), 1024, 64, 60)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := transport.AttachLogs(context.Background(), plan)
	if err != nil {
		t.Fatalf("attach rejected: %v", err)
	}
	_ = stream.Close()
	if len(doer.requests) != 1 || doer.requests[0].Method != http.MethodPost ||
		doer.requests[0].URL.RawQuery != "logs=1&stderr=1&stdout=1&stream=0" ||
		doer.requests[0].URL.Host != "docker" {
		t.Fatalf("unexpected attach request: %#v", doer.requests[0])
	}
	exportPlan, err := NewDockerOutputExportPlan(idgen.New("sandbox-docker-attempt"), 1,
		idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(),
		"/run/cyberagent/outputs", MaxDockerOutputFiles, MaxDockerOutputFileBytes,
		MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	doer.contentType = "application/x-tar"
	archive, err := transport.ExportOutputs(context.Background(), exportPlan)
	if err != nil {
		t.Fatalf("archive rejected: %v", err)
	}
	_ = archive.Close()
	if len(doer.requests) != 2 || doer.requests[1].Method != http.MethodGet ||
		doer.requests[1].URL.RawQuery != "path=%2Frun%2Fcyberagent%2Foutputs" {
		t.Fatalf("unexpected archive request: %#v", doer.requests[1])
	}
}

func TestDockerContainerOwnedIOInspectsNameThenUsesRawContainerID(t *testing.T) {
	_, doer, transport, request := testOwnedDockerIOTransport(t)
	stream, err := transport.AttachOwnedLogs(context.Background(), request,
		testOwnedDockerLogPlan(t, request))
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	wantInspect := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		request.WriteRequest.Spec.ContainerName + "/json"
	wantAttach := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		dockerWriteTestContainerID + "/attach"
	if len(doer.requests) != 2 || doer.requests[0].URL.Path != wantInspect ||
		doer.requests[1].URL.Path != wantAttach ||
		strings.Contains(doer.requests[1].URL.Path, request.ResourceIDFingerprint) {
		t.Fatalf("owned attach did not rebind to raw ID: %#v", doer.requests)
	}

	doer.requests = nil
	archive, err := transport.ExportOwnedOutputs(context.Background(), request,
		testOwnedDockerOutputPlan(t, request))
	if err != nil {
		t.Fatal(err)
	}
	_ = archive.Close()
	wantArchive := "/v" + DockerContainerWriteAPIVersion + "/containers/" +
		dockerWriteTestContainerID + "/archive"
	if len(doer.requests) != 2 || doer.requests[0].URL.Path != wantInspect ||
		doer.requests[1].URL.Path != wantArchive ||
		strings.Contains(doer.requests[1].URL.Path, request.ResourceIDFingerprint) {
		t.Fatalf("owned export did not rebind to raw ID: %#v", doer.requests)
	}
}

func TestDockerContainerOwnedIORejectsMismatchBeforeAttachOrExport(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*dockerLifecycleTestDaemon)
	}{
		{name: "container identity", mutate: func(daemon *dockerLifecycleTestDaemon) {
			daemon.base.containerID = strings.Repeat("c", 64)
		}},
		{name: "ownership labels", mutate: func(daemon *dockerLifecycleTestDaemon) {
			daemon.unsafe = true
		}},
		{name: "container config", mutate: func(daemon *dockerLifecycleTestDaemon) {
			daemon.base.payload.HostConfig.NetworkMode = "bridge"
		}},
	}
	operations := []struct {
		name string
		run  func(context.Context, dockerEngineContainerIOTransport,
			DockerContainerLifecycleRequest) (io.ReadCloser, error)
	}{
		{name: "attach", run: func(ctx context.Context,
			transport dockerEngineContainerIOTransport,
			request DockerContainerLifecycleRequest) (io.ReadCloser, error) {
			return transport.AttachOwnedLogs(ctx, request,
				testOwnedDockerLogPlan(t, request))
		}},
		{name: "export", run: func(ctx context.Context,
			transport dockerEngineContainerIOTransport,
			request DockerContainerLifecycleRequest) (io.ReadCloser, error) {
			return transport.ExportOwnedOutputs(ctx, request,
				testOwnedDockerOutputPlan(t, request))
		}},
	}
	for _, mutation := range mutations {
		for _, operation := range operations {
			t.Run(mutation.name+"/"+operation.name, func(t *testing.T) {
				daemon, doer, transport, request := testOwnedDockerIOTransport(t)
				mutation.mutate(daemon)
				_, err := operation.run(context.Background(), transport, request)
				if DockerContainerIOErrorCode(err) != DockerContainerIOFailureConfigMismatch {
					t.Fatalf("mismatch error = %v", err)
				}
				if len(doer.requests) != 1 ||
					!strings.HasSuffix(doer.requests[0].URL.Path, "/json") {
					t.Fatalf("mismatch escaped inspect-only boundary: %#v", doer.requests)
				}
			})
		}
	}
}

func TestDockerContainerOwnedIOConnectionFailureHasStableCode(t *testing.T) {
	_, _, _, request := testOwnedDockerIOTransport(t)
	endpoint, _ := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	doer := &ownedDockerIOTestDoer{fail: errors.New("dial failed")}
	transport, err := newDockerEngineContainerIOTransport(doer, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.AttachOwnedLogs(context.Background(), request,
		testOwnedDockerLogPlan(t, request))
	if DockerContainerIOErrorCode(err) != DockerContainerIOFailureConnection ||
		len(doer.requests) != 1 {
		t.Fatalf("connection failure = %v requests=%d", err, len(doer.requests))
	}
}

func TestDockerContainerOwnedIORejectsUnownedOutputTargetBeforeInspect(t *testing.T) {
	_, doer, transport, request := testOwnedDockerIOTransport(t)
	plan, err := NewDockerOutputExportPlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, "/run/cyberagent/not-outputs",
		MaxDockerOutputFiles, MaxDockerOutputFileBytes, MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.ExportOwnedOutputs(context.Background(), request, plan)
	if DockerContainerIOErrorCode(err) != DockerContainerIOFailureConfigMismatch ||
		len(doer.requests) != 0 {
		t.Fatalf("unowned output target escaped pre-inspect rejection: err=%v requests=%#v",
			err, doer.requests)
	}
}

func TestDockerContainerIOTransportRejectsEverythingElse(t *testing.T) {
	base := "/v" + DockerContainerWriteAPIVersion + "/containers/" + testDockerContainerFingerprint()
	tests := []struct {
		name     string
		method   string
		path     string
		rawQuery string
	}{
		{name: "attach live stream", method: http.MethodPost, path: base + "/attach",
			rawQuery: "logs=1&stderr=1&stdout=1&stream=1"},
		{name: "attach missing logs", method: http.MethodPost, path: base + "/attach",
			rawQuery: "stderr=1&stdout=1&stream=0"},
		{name: "attach extra parameter", method: http.MethodPost, path: base + "/attach",
			rawQuery: "logs=1&stderr=1&stdout=1&stream=0&timestamps=1"},
		{name: "archive root target", method: http.MethodGet, path: base + "/archive",
			rawQuery: "path=%2F"},
		{name: "archive unclean target", method: http.MethodGet, path: base + "/archive",
			rawQuery: "path=%2Frun%2Fcyberagent%2Foutputs%2F"},
		{name: "archive extra parameter", method: http.MethodGet, path: base + "/archive",
			rawQuery: "path=%2Frun%2Fcyberagent%2Foutputs&force=1"},
		{name: "archive relative target", method: http.MethodGet, path: base + "/archive",
			rawQuery: "path=outputs"},
		{name: "wrong verb", method: http.MethodDelete, path: base, rawQuery: "v=1"},
		{name: "wrong path", method: http.MethodPost, path: base + "/exec", rawQuery: ""},
		{name: "bad reference", method: http.MethodGet, path: "/v" + DockerContainerWriteAPIVersion + "/containers/nope/archive",
			rawQuery: "path=%2Frun"},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			if validDockerContainerIOOperation(current.method, current.path, current.rawQuery) {
				t.Fatal("non-allowlisted operation was accepted")
			}
		})
	}
}

func TestDockerContainerIOTransportResponseValidation(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		breakEcho   bool
		wantCode    string
	}{
		{name: "not found", status: http.StatusNotFound, wantCode: DockerContainerIOFailureUnavailable},
		{name: "server error", status: http.StatusInternalServerError, wantCode: DockerContainerIOFailureInvalidResponse},
		{name: "bad archive content type", status: http.StatusOK, contentType: "text/html",
			wantCode: DockerContainerIOFailureInvalidResponse},
		{name: "echo broken", status: http.StatusOK, contentType: "application/x-tar",
			breakEcho: true, wantCode: DockerContainerIOFailureInvalidResponse},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			doer := &scriptedDockerIOHTTPDoer{status: current.status,
				contentType: current.contentType, breakEcho: current.breakEcho, body: "x"}
			transport := testDockerIOTransport(t, doer)
			plan, err := NewDockerOutputExportPlan(idgen.New("sandbox-docker-attempt"), 1,
				idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(),
				"/run/cyberagent/outputs", MaxDockerOutputFiles, MaxDockerOutputFileBytes,
				MaxDockerOutputTotalBytes)
			if err != nil {
				t.Fatal(err)
			}
			_, err = transport.ExportOutputs(context.Background(), plan)
			if err == nil || DockerContainerIOErrorCode(err) != current.wantCode {
				t.Fatalf("export error = %v, want code %q", err, current.wantCode)
			}
		})
	}
}

func TestDockerContainerIOTransportRequiresFixedEndpoint(t *testing.T) {
	doer := &scriptedDockerIOHTTPDoer{status: http.StatusOK}
	if _, err := newDockerEngineContainerIOTransport(doer, DockerObservationEndpoint{}); err == nil {
		t.Fatal("invalid endpoint was accepted")
	}
	if _, err := newDockerEngineContainerIOTransport(nil, DockerObservationEndpoint{}); err == nil {
		t.Fatal("missing doer was accepted")
	}
	endpoint, err := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newDockerEngineContainerIOTransport(doer, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if transport.Endpoint() != endpoint {
		t.Fatal("endpoint was not preserved")
	}
}

func TestDockerContainerIOTransportCancelPropagates(t *testing.T) {
	doer := &scriptedDockerIOHTTPDoer{status: http.StatusOK}
	transport := testDockerIOTransport(t, doer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan, err := NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"), 1,
		idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(), 1024, 64, 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.AttachLogs(ctx, plan); err == nil {
		t.Fatal("cancelled attach succeeded")
	}
	if len(doer.requests) != 0 {
		t.Fatal("cancelled context still sent a request")
	}
}
