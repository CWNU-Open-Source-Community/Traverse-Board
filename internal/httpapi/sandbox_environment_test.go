package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/store"
)

type sandboxEnvironmentAPIFixture struct {
	api        *API
	settings   *application.FileSandboxEnvironmentSettingsStore
	probeCalls int
}

func newSandboxEnvironmentAPIFixture(t *testing.T) *sandboxEnvironmentAPIFixture {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	settings, err := application.NewFileSandboxEnvironmentSettingsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := &sandboxEnvironmentAPIFixture{settings: settings}
	probe := func(context.Context, application.SandboxEnvironmentSettings) (application.SandboxEnvironmentObservation, error) {
		fixture.probeCalls++
		return application.SandboxEnvironmentObservation{Installed: true, Ready: true}, nil
	}
	service, err := application.NewSandboxEnvironmentService(settings, application.DefaultSandboxEnvironmentSettings(),
		application.SandboxEnvironmentProbes{Local: probe, Docker: probe, SBX: probe})
	if err != nil {
		t.Fatal(err)
	}
	fixture.api, err = New(state, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		SandboxEnvironmentController: NewSandboxEnvironmentController(service), SandboxEnvironmentControlEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func sandboxEnvironmentHTTPRequest(api *API, method, path, token, contentType, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "127.0.0.1"
	request.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func sandboxEnvironmentControlTestBody() string {
	return `{"version":"sandbox_environment.v1","expected_revision":1,"settings":{"default_backend":"docker","docker_enabled":true,"docker_image_digest":"sha256:` + strings.Repeat("a", 64) + `","sbx_enabled":false,"sbx_template":""}}`
}

func readSandboxEnvironmentTestResponse(t *testing.T, response *httptest.ResponseRecorder) SandboxEnvironmentView {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope apiTestEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var view SandboxEnvironmentView
	if err := json.Unmarshal(envelope.Data, &view); err != nil {
		t.Fatal(err)
	}
	if response.Header().Get("Cache-Control") != "no-store" || view.CapabilityGrant || len(view.Backends) != 3 {
		t.Fatalf("invalid public capability projection: %#v", view)
	}
	return view
}

func TestSandboxEnvironmentHTTPUsesDistinctCapabilitiesAndBoundedBodies(t *testing.T) {
	fixture := newSandboxEnvironmentAPIFixture(t)
	valid := sandboxEnvironmentControlTestBody()
	for _, current := range []struct {
		name, method, path, token, contentType, body string
		status                                       int
	}{
		{"unauthorized read", http.MethodGet, SandboxEnvironmentPath, "", "", "", http.StatusUnauthorized},
		{"control token cannot read", http.MethodGet, SandboxEnvironmentPath, testControlToken, "", "", http.StatusUnauthorized},
		{"read token cannot write", http.MethodPut, SandboxEnvironmentPath, testAccessToken, "application/json", valid, http.StatusUnauthorized},
		{"unsupported method", http.MethodPost, SandboxEnvironmentPath, testAccessToken, "application/json", valid, http.StatusMethodNotAllowed},
		{"read query", http.MethodGet, SandboxEnvironmentPath + "?backend=sbx", testAccessToken, "", "", http.StatusBadRequest},
		{"malformed query", http.MethodGet, SandboxEnvironmentPath + "?backend=%", testAccessToken, "", "", http.StatusBadRequest},
		{"write query", http.MethodPut, SandboxEnvironmentPath + "?home=C:/private", testControlToken, "application/json", valid, http.StatusBadRequest},
		{"read body", http.MethodGet, SandboxEnvironmentPath, testAccessToken, "application/json", "{}", http.StatusBadRequest},
		{"wrong media type", http.MethodPut, SandboxEnvironmentPath, testControlToken, "text/plain", valid, http.StatusUnsupportedMediaType},
		{"oversized", http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", strings.Repeat(" ", MaxSandboxEnvironmentRequestBodyBytes+1), http.StatusRequestEntityTooLarge},
		{"missing body", http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", "", http.StatusBadRequest},
	} {
		t.Run(current.name, func(t *testing.T) {
			response := sandboxEnvironmentHTTPRequest(fixture.api, current.method, current.path, current.token, current.contentType, current.body)
			if response.Code != current.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if fixture.probeCalls != 0 {
		t.Fatalf("rejected request probed backend: %d", fixture.probeCalls)
	}
	current, err := fixture.settings.Load(context.Background())
	if err != nil || current.Revision != 1 || current.Settings != application.DefaultSandboxEnvironmentSettings() {
		t.Fatalf("rejected write changed settings: %#v %v", current, err)
	}
	read := readSandboxEnvironmentTestResponse(t, sandboxEnvironmentHTTPRequest(fixture.api, http.MethodGet, SandboxEnvironmentPath, testAccessToken, "", ""))
	if fixture.probeCalls != 3 || read.ProbeStatus != "checked" || read.Revision != 1 || read.Backends[0].Status != "ready" {
		t.Fatalf("read = %#v calls=%d", read, fixture.probeCalls)
	}
}

func TestSandboxEnvironmentHTTPStrictJSONRejectsAmbiguousSettings(t *testing.T) {
	fixture := newSandboxEnvironmentAPIFixture(t)
	valid := sandboxEnvironmentControlTestBody()
	for name, body := range map[string]string{
		"unknown top field":      strings.Replace(valid, `"version":`, `"owner_token":"secret","version":`, 1),
		"unknown setting":        strings.Replace(valid, `"default_backend":`, `"executable_path":"C:/evil","default_backend":`, 1),
		"duplicate top field":    strings.Replace(valid, `"expected_revision":1`, `"expected_revision":2,"expected_revision":1`, 1),
		"escaped duplicate":      strings.Replace(valid, `"version":`, `"vers\u0069on":"sandbox_environment.v1","version":`, 1),
		"duplicate nested field": strings.Replace(valid, `"docker_enabled":true`, `"docker_enabled":false,"docker_enabled":true`, 1),
		"missing bool":           strings.Replace(valid, `"sbx_enabled":false,`, "", 1),
		"missing pin":            strings.Replace(valid, `,"sbx_template":""`, "", 1),
		"missing revision":       strings.Replace(valid, `"expected_revision":1,`, "", 1),
		"null settings":          `{"version":"sandbox_environment.v1","expected_revision":1,"settings":null}`,
		"null bool":              strings.Replace(valid, `"sbx_enabled":false`, `"sbx_enabled":null`, 1),
		"null string":            strings.Replace(valid, `"sbx_template":""`, `"sbx_template":null`, 1),
		"case variant":           strings.Replace(valid, `"docker_enabled":`, `"Docker_Enabled":`, 1),
		"string bool":            strings.Replace(valid, `"docker_enabled":true`, `"docker_enabled":"true"`, 1),
		"fractional revision":    strings.Replace(valid, `"expected_revision":1`, `"expected_revision":1.5`, 1),
		"wrong version":          strings.Replace(valid, "sandbox_environment.v1", "sandbox_environment.v2", 1),
		"unpinned digest":        strings.Replace(valid, "sha256:"+strings.Repeat("a", 64), "latest", 1),
		"trailing data":          valid + `{}`,
		"array":                  "[" + valid + "]",
		"invalid UTF8":           string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			response := sandboxEnvironmentHTTPRequest(fixture.api, http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("ambiguous request accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
	current, err := fixture.settings.Load(context.Background())
	if err != nil || current.Revision != 1 || fixture.probeCalls != 0 {
		t.Fatalf("invalid JSON had side effects: %#v calls=%d err=%v", current, fixture.probeCalls, err)
	}
}

func TestSandboxEnvironmentHTTPSaveReplayConflictAndFreshRead(t *testing.T) {
	fixture := newSandboxEnvironmentAPIFixture(t)
	body := sandboxEnvironmentControlTestBody()
	first := readSandboxEnvironmentTestResponse(t, sandboxEnvironmentHTTPRequest(fixture.api, http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", body))
	if first.Revision != 2 || first.Replayed || first.ProbeStatus != "not_checked" || !first.RestartRequired || first.ActiveSettings.DefaultBackend != "local" || fixture.probeCalls != 0 {
		t.Fatalf("save = %#v calls=%d", first, fixture.probeCalls)
	}
	for _, row := range first.Backends {
		if row.Installed || row.Ready || row.Status != "not_checked" || row.Blockers[0].Code != "ENVIRONMENT_RECHECK_REQUIRED" {
			t.Fatalf("save returned installation claim: %#v", row)
		}
	}
	replay := readSandboxEnvironmentTestResponse(t, sandboxEnvironmentHTTPRequest(fixture.api, http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", body))
	if !replay.Replayed || replay.Revision != first.Revision || replay.Settings != first.Settings || fixture.probeCalls != 0 {
		t.Fatalf("exact unknown-outcome retry = %#v", replay)
	}
	response := sandboxEnvironmentHTTPRequest(fixture.api, http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", strings.Replace(body, `"default_backend":"docker"`, `"default_backend":"local"`, 1))
	if response.Code != http.StatusConflict {
		t.Fatalf("changed stale retry accepted: %d %s", response.Code, response.Body.String())
	}
	checked := readSandboxEnvironmentTestResponse(t, sandboxEnvironmentHTTPRequest(fixture.api, http.MethodGet, SandboxEnvironmentPath, testAccessToken, "", ""))
	if checked.ProbeStatus != "checked" || checked.Revision != 2 || checked.Backends[1].Status != "disabled" || checked.Backends[1].Ready || !checked.Backends[1].Installed || fixture.probeCalls != 3 {
		t.Fatalf("fresh read changed active gate: %#v", checked)
	}
	// A chunked request still obeys the same byte limit.
	request := httptest.NewRequest(http.MethodPut, SandboxEnvironmentPath, bytes.NewBufferString(strings.Repeat(" ", MaxSandboxEnvironmentRequestBodyBytes+1)))
	request.ContentLength = -1
	request.Host, request.RemoteAddr = "127.0.0.1", "127.0.0.1:12345"
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	fixture.api.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked limit bypassed: %d %s", response.Code, response.Body.String())
	}
}

func TestSandboxEnvironmentHTTPReadOnlyConfigurationDoesNotGrantControl(t *testing.T) {
	fixture := newSandboxEnvironmentAPIFixture(t)
	controller := fixture.api.sandboxEnvironmentController
	for _, configuration := range []Config{
		{AccessToken: testAccessToken, SandboxEnvironmentControlEnabled: true, SandboxEnvironmentController: controller},
		{AccessToken: testAccessToken, ControlToken: testControlToken, SandboxEnvironmentControlEnabled: true},
	} {
		if _, err := New(fixture.api.store, configuration); err == nil {
			t.Fatal("invalid control configuration accepted")
		}
	}
	readOnly, err := New(fixture.api.store, Config{AccessToken: testAccessToken, SandboxEnvironmentController: controller})
	if err != nil {
		t.Fatal(err)
	}
	readSandboxEnvironmentTestResponse(t, sandboxEnvironmentHTTPRequest(readOnly, http.MethodGet, SandboxEnvironmentPath, testAccessToken, "", ""))
	response := sandboxEnvironmentHTTPRequest(readOnly, http.MethodPut, SandboxEnvironmentPath, testControlToken, "application/json", sandboxEnvironmentControlTestBody())
	if response.Code != http.StatusNotFound {
		t.Fatalf("read-only configuration wrote: %d %s", response.Code, response.Body.String())
	}
	missing, err := New(fixture.api.store, Config{AccessToken: testAccessToken})
	if err != nil {
		t.Fatal(err)
	}
	response = sandboxEnvironmentHTTPRequest(missing, http.MethodGet, SandboxEnvironmentPath, testAccessToken, "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing controller exposed environment: %d %s", response.Code, response.Body.String())
	}
}
