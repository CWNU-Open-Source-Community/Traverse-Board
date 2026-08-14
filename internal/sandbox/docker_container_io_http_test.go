package sandbox

import (
	"context"
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
