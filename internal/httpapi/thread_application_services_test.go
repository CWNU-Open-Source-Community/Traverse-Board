package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

type threadApplicationServiceControllerStub struct {
	stops   int
	request application.ThreadApplicationServiceStopRequest
}

func (s *threadApplicationServiceControllerStub) List(_ context.Context, thread string, limit int) (application.ThreadApplicationServicesView, error) {
	if limit < 1 || limit > 50 {
		return application.ThreadApplicationServicesView{}, apperror.New(apperror.CodeInvalidArgument, "invalid limit")
	}
	return application.ThreadApplicationServicesView{Version: application.ThreadApplicationServicesProtocolVersion, ThreadID: thread, Services: []application.ThreadApplicationServiceView{serviceControlTestView(thread)}}, nil
}
func (*threadApplicationServiceControllerStub) Get(_ context.Context, thread, job string) (application.ThreadApplicationServiceDetailView, error) {
	view := serviceControlTestView(thread)
	view.JobID = job
	return application.ThreadApplicationServiceDetailView{Version: application.ThreadApplicationServicesProtocolVersion, Service: view, Output: application.ThreadApplicationServiceOutputView{}, CandidateURLs: []application.ThreadApplicationServiceURLView{}}, nil
}
func (s *threadApplicationServiceControllerStub) Stop(_ context.Context, thread, job string, input application.ThreadApplicationServiceStopRequest) (application.ThreadApplicationServiceStopView, error) {
	s.stops++
	s.request = input
	view := serviceControlTestView(thread)
	view.JobID = job
	view.State = "cancelled"
	view.CanStop = false
	return application.ThreadApplicationServiceStopView{Version: application.ThreadApplicationServicesProtocolVersion, Service: view}, nil
}
func serviceControlTestView(thread string) application.ThreadApplicationServiceView {
	return application.ThreadApplicationServiceView{ThreadID: thread, RunID: "original-run", JobID: "exact-job", State: "running", CreatedAt: time.Now().UTC(), CanStop: true}
}

func TestThreadApplicationServicesHTTPControlGateAndClosedStopContract(t *testing.T) {
	f := newAPIFixture(t)
	controller := &threadApplicationServiceControllerStub{}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, RunExecutionEnabled: true, RunExecutionController: &runExecutionControllerFake{}, ThreadApplicationServiceController: controller})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/threads/exact-thread/application-services/exact-job/stop"
	valid := `{"version":"thread_application_services.v1","expected_run_id":"original-run"}`
	for _, test := range []struct {
		name, token, key, body, query string
		status                        int
	}{
		{"read token", testAccessToken, "application-stop-exact-job", valid, "", 401},
		{"missing key", testControlToken, "", valid, "", 400},
		{"wrong Job key", testControlToken, "application-stop-another-job", valid, "", 400},
		{"key in body", testControlToken, "application-stop-exact-job", `{"version":"thread_application_services.v1","expected_run_id":"original-run","operation_key":"application-stop-exact-job"}`, "", 400},
		{"unknown body", testControlToken, "application-stop-exact-job", `{"version":"thread_application_services.v1","expected_run_id":"original-run","pid":42}`, "", 400},
		{"duplicate body", testControlToken, "application-stop-exact-job", `{"version":"thread_application_services.v1","expected_run_id":"original-run","expected_run_id":"another-run"}`, "", 400},
		{"extra query", testControlToken, "application-stop-exact-job", valid, "?run_id=another-run", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765"+path+test.query, strings.NewReader(test.body))
			r.RemoteAddr = "127.0.0.1:45000"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+test.token)
			if test.key != "" {
				r.Header.Set("Idempotency-Key", test.key)
			}
			w := httptest.NewRecorder()
			api.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	if controller.stops != 0 {
		t.Fatal("rejected HTTP request reached cleanup")
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765"+path, strings.NewReader(valid))
	r.RemoteAddr = "127.0.0.1:45000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+testControlToken)
	r.Header.Set("Idempotency-Key", "application-stop-exact-job")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 200 || controller.stops != 1 || controller.request.ExpectedRunID != "original-run" || controller.request.OperationKey != "application-stop-exact-job" {
		t.Fatalf("stop dispatch status=%d input=%+v body=%s", w.Code, controller.request, w.Body)
	}
	readonly, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, ThreadApplicationServiceController: controller})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	readonly.ServeHTTP(w, r)
	if w.Code != 404 || controller.stops != 1 {
		t.Fatalf("disabled gate reached stop: %d calls=%d", w.Code, controller.stops)
	}
	get := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/v1/threads/exact-thread/application-services", nil)
	get.RemoteAddr = "127.0.0.1:45000"
	get.Header.Set("Authorization", "Bearer "+testAccessToken)
	w = httptest.NewRecorder()
	readonly.ServeHTTP(w, get)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"can_stop":true`) {
		t.Fatalf("read-only metadata projected cleanup: %d %s", w.Code, w.Body)
	}
	for _, limit := range []string{"0", "-1", "51", "invalid"} {
		get.URL.RawQuery = "limit=" + limit
		w = httptest.NewRecorder()
		readonly.ServeHTTP(w, get)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid metadata limit %q: %d %s", limit, w.Code, w.Body)
		}
	}
}
