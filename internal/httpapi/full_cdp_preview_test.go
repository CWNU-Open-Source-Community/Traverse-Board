package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

type fullCDPPreviewStub struct {
	fullCDPSessionControllerStub
	capture  application.FullCDPPreviewCapture
	captures int
	reads    int
	actions  int
	request  application.FullCDPPreviewActionRequest
}

func (s *fullCDPPreviewStub) CaptureFullCDPPreview(_ context.Context, runID, sessionID string) (application.FullCDPPreviewView, error) {
	s.captures++
	return s.capture.View, nil
}
func (s *fullCDPPreviewStub) ReadFullCDPPreviewImage(_ context.Context, runID, sessionID, digest string) (application.FullCDPPreviewCapture, error) {
	s.reads++
	return s.capture, nil
}
func (s *fullCDPPreviewStub) ActFullCDPPreview(_ context.Context, request application.FullCDPPreviewActionRequest) (application.FullCDPPreviewView, error) {
	s.actions++
	s.request = request
	return s.capture.View, nil
}

func TestFullCDPPreviewHTTPControlCaptureAuthenticatedExactPNGAndAction(t *testing.T) {
	f := newAPIFixture(t)
	content := []byte("test image transport")
	hash := sha256.Sum256(content)
	digest := hex.EncodeToString(hash[:])
	view := application.FullCDPPreviewView{Version: application.FullCDPPreviewProtocolVersion, RunID: f.run.ID, SessionID: "full-cdp-preview-http", CanonicalURL: "http://127.0.0.1:18080/", CapturedAt: time.Now().UTC(),
		Page: application.FullCDPPreviewPage{SnapshotID: strings.Repeat("a", 64), UntrustedEvidence: true}, Image: application.FullCDPPreviewImage{MediaType: "image/png", Bytes: len(content), SHA256: digest, Width: 2, Height: 3}}
	stub := &fullCDPPreviewStub{capture: application.FullCDPPreviewCapture{View: view, PNG: content}}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		FullCDPSessionControlEnabled: true, ExecutionPermissionControlEnabled: true, BrowserCDPPermissionControlEnabled: true,
		BrowserCDPPermissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true, FullDebugEnabled: true},
		ExecutionPermissionCapabilities:  domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}, FullCDPSessionController: stub})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.ReplaceAll(FullCDPPreviewPathTemplate, "{run_id}", f.run.ID)
	body := `{"version":"full_cdp_preview.v1","expected_session_id":"` + view.SessionID + `"}`
	wrongToken := performRequest(t, api, http.MethodPost, path, testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", strings.NewReader(body))
	if wrongToken.Code != http.StatusUnauthorized || stub.captures != 0 {
		t.Fatal("read token captured browser")
	}
	response := performControlPathRequest(t, api, path, "unused-preview-key", strings.NewReader(body))
	var actual application.FullCDPPreviewView
	decodeDataStatus(t, response, http.StatusOK, &actual)
	if actual.Image.SHA256 != digest || stub.captures != 1 {
		t.Fatalf("capture=%+v", actual)
	}
	imagePath := strings.ReplaceAll(FullCDPPreviewImagePathTemplate, "{run_id}", f.run.ID) + "?session_id=" + view.SessionID + "&sha256=" + digest
	read := performRequest(t, api, http.MethodGet, imagePath, testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil)
	if read.Code != http.StatusOK || !bytes.Equal(read.Body.Bytes(), content) || read.Header().Get("ETag") != `"`+digest+`"` || read.Header().Get("X-Cyberagent-Content-SHA256") != digest || read.Header().Get("Content-Type") != "image/png" || read.Header().Get("Cache-Control") != "no-store" || stub.captures != 1 {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	wrongScope := performRequest(t, api, http.MethodGet, strings.ReplaceAll(imagePath, view.SessionID, "different-session"), testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil)
	if wrongScope.Code != http.StatusServiceUnavailable {
		t.Fatalf("wrong-scope result accepted: %d", wrongScope.Code)
	}
	actPath := strings.ReplaceAll(FullCDPPreviewActionPathTemplate, "{run_id}", f.run.ID)
	actBody, _ := json.Marshal(FullCDPPreviewActionRequestView{Version: application.FullCDPPreviewActionProtocolVersion, ExpectedSessionID: view.SessionID, ExpectedSnapshotID: view.Page.SnapshotID, Action: "click", Selector: "#press"})
	actResponse := performControlPathRequest(t, api, actPath, "unused-action-key", strings.NewReader(string(actBody)))
	decodeDataStatus(t, actResponse, http.StatusOK, &actual)
	if stub.actions != 1 || stub.request.RunID != f.run.ID || stub.request.SessionID != view.SessionID || stub.request.SnapshotID != view.Page.SnapshotID || stub.request.Selector != "#press" {
		t.Fatalf("action=%+v", stub.request)
	}
	for _, invalid := range []string{strings.Replace(body, `"expected_session_id"`, `"unknown"`, 1), strings.Replace(body, `"version":`, `"version":"duplicate","version":`, 1)} {
		invalidResponse := performControlPathRequest(t, api, path, "invalid-preview-body", strings.NewReader(invalid))
		if invalidResponse.Code != http.StatusBadRequest {
			t.Fatalf("invalid=%d %s", invalidResponse.Code, invalidResponse.Body.String())
		}
	}
	if stub.captures != 1 {
		t.Fatal("invalid request captured browser")
	}
}
