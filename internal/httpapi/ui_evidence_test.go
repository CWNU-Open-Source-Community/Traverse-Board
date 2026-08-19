package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/uievidence"
)

type uiEvidenceControllerStub struct {
	attempt      uievidence.Attempt
	bundle       application.UIEvidenceBundle
	artifact     uievidence.Artifact
	startRequest application.UIEvidenceStartRequest
	listFilter   uievidence.ListFilter
	cancelledID  string
}

func (stub *uiEvidenceControllerStub) Start(_ context.Context,
	request application.UIEvidenceStartRequest,
) (uievidence.Attempt, error) {
	stub.startRequest = request
	return stub.attempt, nil
}

func (stub *uiEvidenceControllerStub) Cancel(_ context.Context, attemptID string) (
	uievidence.Attempt, error,
) {
	stub.cancelledID = attemptID
	return stub.attempt, nil
}

func (stub *uiEvidenceControllerStub) Get(context.Context, string) (
	application.UIEvidenceBundle, error,
) {
	return stub.bundle, nil
}

func (stub *uiEvidenceControllerStub) List(_ context.Context,
	filter uievidence.ListFilter,
) ([]uievidence.Attempt, error) {
	stub.listFilter = filter
	return []uievidence.Attempt{stub.attempt}, nil
}

func (stub *uiEvidenceControllerStub) Artifact(context.Context, string, string) (
	uievidence.Artifact, error,
) {
	return stub.artifact, nil
}

func TestUIEvidenceHTTPPreservesNotRunAndRequiresSeparateControlAuthority(t *testing.T) {
	fixture := newAPIFixture(t)
	stub := &uiEvidenceControllerStub{attempt: uievidence.Attempt{
		ProtocolVersion: uievidence.AttemptProtocolVersion,
		Manifest: uievidence.Manifest{AttemptID: "ui-attempt-http-0001",
			RunID: fixture.run.ID},
		Status: uievidence.StatusNotRun, FailureStage: uievidence.FailureNone,
	}}
	stub.bundle = application.UIEvidenceBundle{Attempt: stub.attempt,
		Steps: []uievidence.StepReceipt{}, Artifacts: []uievidence.ArtifactMetadata{}}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, UIEvidenceControlEnabled: true,
		UIEvidenceController: stub, AppVersion: "ui-evidence-http-test"})
	if err != nil {
		t.Fatal(err)
	}
	listPath := "/api/v1/runs/" + fixture.run.ID + "/ui-evidence?status=not_run&limit=7"
	unauthorized := performRequest(t, api, http.MethodGet, listPath, "",
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "POLICY_DENIED")

	response := performRequest(t, api, http.MethodGet, listPath, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	var attempts []uievidence.Attempt
	decodeData(t, response, &attempts)
	if len(attempts) != 1 || attempts[0].Status != uievidence.StatusNotRun ||
		attempts[0].Status.Passed() || stub.listFilter.RunID != fixture.run.ID ||
		stub.listFilter.Status != uievidence.StatusNotRun || stub.listFilter.Limit != 7 {
		t.Fatalf("not-run UI evidence projection changed meaning: attempts=%#v filter=%#v",
			attempts, stub.listFilter)
	}

	startPath := "/api/v1/runs/" + fixture.run.ID + "/ui-evidence"
	readToken := uiEvidenceJSONRequest(t, api, startPath, testAccessToken, `{}`)
	assertAPIError(t, readToken, http.StatusUnauthorized, "POLICY_DENIED")
	unknown := uiEvidenceJSONRequest(t, api, startPath, testControlToken,
		`{"operation_key":"http-ui-evidence-0001","unexpected":true}`)
	assertAPIError(t, unknown, http.StatusBadRequest, "INVALID_ARGUMENT")
}

func TestUIEvidenceHTTPDownloadsExactUntrustedArtifact(t *testing.T) {
	fixture := newAPIFixture(t)
	content := []byte("synthetic evidence")
	artifact, err := uievidence.SealArtifact(uievidence.ArtifactMetadata{
		ID: "ui-artifact-http-0001", AttemptID: "ui-attempt-http-0001",
		RunID: fixture.run.ID, StepID: "capture-final", Kind: uievidence.ArtifactDOM,
		MIME: "application/json", Viewport: uievidence.Viewport{Width: 1280, Height: 720, DPR: 1},
		SourceCommit:    strings.Repeat("1", 40),
		RetentionPolicy: uievidence.ArtifactRetentionRunHistory,
		Redacted:        true, Untrusted: true,
		CreatedAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
	}, content)
	if err != nil {
		t.Fatal(err)
	}
	stub := &uiEvidenceControllerStub{artifact: artifact}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		UIEvidenceController: stub, AppVersion: "ui-evidence-artifact-test"})
	if err != nil {
		t.Fatal(err)
	}
	requestPath := "/api/v1/ui-evidence/ui-attempt-http-0001/artifacts/ui-artifact-http-0001"
	response := performRequest(t, api, http.MethodGet, requestPath, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	if response.Code != http.StatusOK || response.Body.String() != string(content) ||
		response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("ETag") != `"`+artifact.Metadata.SHA256+`"` ||
		response.Header().Get("X-CyberAgent-Evidence-Untrusted") != "true" ||
		response.Header().Get("X-CyberAgent-Content-SHA256") != artifact.Metadata.SHA256 {
		t.Fatalf("artifact response lost integrity metadata: status=%d headers=%#v body=%q",
			response.Code, response.Header(), response.Body.String())
	}
}

func TestUIEvidenceHTTPRejectsCorruptArtifactContent(t *testing.T) {
	fixture := newAPIFixture(t)
	artifact, err := uievidence.SealArtifact(uievidence.ArtifactMetadata{
		ID: "ui-artifact-http-corrupt-0001", AttemptID: "ui-attempt-http-corrupt-0001",
		RunID: fixture.run.ID, StepID: "capture-final", Kind: uievidence.ArtifactDOM,
		MIME: "application/json", Viewport: uievidence.Viewport{Width: 1280, Height: 720, DPR: 1},
		SourceCommit:    strings.Repeat("1", 40),
		RetentionPolicy: uievidence.ArtifactRetentionRunHistory,
		Redacted:        true, Untrusted: true,
		CreatedAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
	}, []byte("sealed evidence"))
	if err != nil {
		t.Fatal(err)
	}
	artifact.Content[0] ^= 0xff
	stub := &uiEvidenceControllerStub{artifact: artifact}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		UIEvidenceController: stub, AppVersion: "ui-evidence-artifact-corrupt-test"})
	if err != nil {
		t.Fatal(err)
	}
	requestPath := "/api/v1/ui-evidence/ui-attempt-http-corrupt-0001/artifacts/" +
		"ui-artifact-http-corrupt-0001"
	response := performRequest(t, api, http.MethodGet, requestPath, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	assertAPIError(t, response, http.StatusServiceUnavailable, "UNAVAILABLE")
}

func TestUIEvidenceHTTPRejectsInvalidEnablement(t *testing.T) {
	fixture := newAPIFixture(t)
	if _, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, UIEvidenceControlEnabled: true}); err == nil {
		t.Fatal("enabled UI evidence without its controller was accepted")
	}
	if _, err := New(fixture.store, Config{AccessToken: testAccessToken,
		UIEvidenceControlEnabled: true,
		UIEvidenceController:     &uiEvidenceControllerStub{}}); err == nil {
		t.Fatal("enabled UI evidence without a control token was accepted")
	}
}

func uiEvidenceJSONRequest(t *testing.T, api *API, requestPath, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+requestPath,
		strings.NewReader(body))
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
