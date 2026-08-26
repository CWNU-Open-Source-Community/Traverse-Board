package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/standardcodedelivery"
)

type standardCodeDeliveryControllerStub struct {
	recordRequest application.StandardCodeDeliveryRecordRequest
	binding       standardcodedelivery.Binding
}

func (s *standardCodeDeliveryControllerStub) Current(_ context.Context,
	runID string,
) (standardcodedelivery.Report, bool, error) {
	binding := s.binding
	binding.RunID = runID
	return standardcodedelivery.Report{ID: "standard-code-delivery-http",
		ProtocolVersion: standardcodedelivery.ProtocolVersion,
		Status:          standardcodedelivery.StatusStale,
		ReceiptStatus:   standardcodedelivery.StatusPassed,
		Binding:         binding,
		ReceiptSHA256:   strings.Repeat("a", 64)}, true, nil
}

func (s *standardCodeDeliveryControllerStub) Record(_ context.Context,
	request application.StandardCodeDeliveryRecordRequest,
) (application.StandardCodeDeliveryRecordResult, error) {
	s.recordRequest = request
	report, _, _ := s.Current(context.Background(), request.RunID)
	return application.StandardCodeDeliveryRecordResult{Report: report}, nil
}

func TestStandardCodeDeliveryHTTPUsesOneProjectionAndBindsControlIntent(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &standardCodeDeliveryControllerStub{}
	fixture.api.standardCodeDeliveryController = controller
	path := "/api/v1/runs/" + fixture.run.ID + "/standard-code-delivery"

	current := fixture.get(t, path)
	if current.Code != http.StatusOK ||
		!strings.Contains(current.Body.String(), `"protocol_version":"standard_code_delivery.v1"`) ||
		!strings.Contains(current.Body.String(), `"status":"stale"`) ||
		!strings.Contains(current.Body.String(), `"receipt_status":"passed"`) {
		t.Fatalf("current status=%d body=%s", current.Code, current.Body.String())
	}

	recorded := performControlMethodPathRequest(t, fixture.api, http.MethodPost, path,
		"standard-code-delivery-http-record-0001", strings.NewReader(
			`{"operation_key":"delivery-op","declaration":"user_skipped",`+
				`"verification_job_ids":["job-2","job-1"],`+
				`"uncovered_items":["manual scenario"]}`))
	if recorded.Code != http.StatusOK ||
		controller.recordRequest.RunID != fixture.run.ID ||
		controller.recordRequest.OperationKey != "delivery-op" ||
		controller.recordRequest.RequestedBy != "api_operator" ||
		controller.recordRequest.Declaration != standardcodedelivery.DeclarationUserSkipped ||
		len(controller.recordRequest.VerificationJobIDs) != 2 ||
		!strings.Contains(recorded.Body.String(), `"report":{"id":"standard-code-delivery-http"`) {
		t.Fatalf("record status=%d request=%#v body=%s", recorded.Code,
			controller.recordRequest, recorded.Body.String())
	}
}

func TestStandardCodeDeliveryHTTPFailsClosed(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.standardCodeDeliveryController = &standardCodeDeliveryControllerStub{}
	path := "/api/v1/runs/" + fixture.run.ID + "/standard-code-delivery"

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path,
		strings.NewReader(`{"operation_key":"delivery-op"}`))
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	fixture.api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("read token authorized delivery record: status=%d body=%s",
			response.Code, response.Body.String())
	}

	duplicate := performControlMethodPathRequest(t, fixture.api, http.MethodPost, path,
		"standard-code-delivery-http-record-0002", strings.NewReader(
			`{"operation_key":"one","operation_key":"two"}`))
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("duplicate field status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}

	query := fixture.get(t, path+"?refresh=true")
	if query.Code != http.StatusBadRequest {
		t.Fatalf("unexpected query status=%d body=%s", query.Code, query.Body.String())
	}
}
