package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/store"
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

func TestStandardCodeDeliveryUnavailableDoesNotReportInvalidRun(t *testing.T) {
	fixture := newAPIFixture(t)
	path := "/api/v1/runs/" + fixture.run.ID + "/standard-code-delivery"
	fixture.api.standardCodeDeliveryController = nil
	assertAPIError(t, fixture.get(t, path), http.StatusNotFound, "NOT_FOUND")
	// A misconfigured caller can still pass a typed nil through the interface.
	// Keep that diagnostic distinct from an invalid user-supplied identity.
	var unavailable *application.StandardCodeDeliveryService
	fixture.api.standardCodeDeliveryController = unavailable
	response := fixture.get(t, path)
	assertAPIError(t, response, http.StatusPreconditionFailed, "FAILED_PRECONDITION")
	if strings.Contains(response.Body.String(), "Run id is invalid") {
		t.Fatal("missing delivery service blamed a valid Run identity")
	}
}

type deliveryPresetProjectionStore struct {
	*store.SQLiteStore
	operation domain.StandardCodePresetOperation
	found     bool
	reads     int
}

func (s *deliveryPresetProjectionStore) GetConfiguredStandardCodePresetOperation(context.Context, string) (domain.StandardCodePresetOperation, bool, error) {
	s.reads++
	return s.operation, s.found, nil
}

func TestStandardCodeDeliveryPresetFactIsExplicitAndBoundToDetailRun(t *testing.T) {
	fixture := newAPIFixture(t)
	reader := &deliveryPresetProjectionStore{SQLiteStore: fixture.store}
	fixture.api.store = reader
	thread, err := fixture.store.GetThreadByRun(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		found  bool
		status domain.StandardCodePresetStatus
		runID  string
		want   bool
	}{
		{"no preset", false, "", fixture.run.ID, false},
		{"preparing", true, domain.StandardCodePresetPreparing, fixture.run.ID, false},
		{"different run", true, domain.StandardCodePresetConfigured, "other-run", false},
		{"configured", true, domain.StandardCodePresetConfigured, fixture.run.ID, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader.found = test.found
			reader.operation = domain.StandardCodePresetOperation{RunID: test.runID,
				MissionID: fixture.run.MissionID, Status: test.status}
			reader.reads = 0
			var detail ThreadDetailView
			decodeDataStatus(t, fixture.get(t, "/api/v1/threads/"+thread.ID), http.StatusOK, &detail)
			if reader.reads != 1 || detail.ActiveRun == nil ||
				detail.ActiveRun.StandardCodePresetConfigured == nil ||
				*detail.ActiveRun.StandardCodePresetConfigured != test.want ||
				detail.LastRun.StandardCodePresetConfigured == nil ||
				*detail.LastRun.StandardCodePresetConfigured != test.want ||
				len(detail.Runs) != 1 || detail.Runs[0].Run.StandardCodePresetConfigured == nil ||
				*detail.Runs[0].Run.StandardCodePresetConfigured != test.want {
				t.Fatalf("inconsistent detail preset fact: %+v reads=%d", detail, reader.reads)
			}
			var runDetail RunDetailView
			decodeDataStatus(t, fixture.get(t, "/api/v1/runs/"+fixture.run.ID), http.StatusOK, &runDetail)
			if reader.reads != 2 || runDetail.Run.StandardCodePresetConfigured == nil ||
				*runDetail.Run.StandardCodePresetConfigured != test.want {
				t.Fatalf("run detail preset fact=%+v reads=%d", runDetail.Run, reader.reads)
			}
			before := reader.reads
			response := fixture.get(t, "/api/v1/runs")
			if response.Code != http.StatusOK || reader.reads != before ||
				strings.Contains(response.Body.String(), "standard_code_preset_configured") {
				t.Fatal("run list added per-row preset queries or asserted an unknown fact")
			}
		})
	}
}
