package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/store"
)

func TestScheduledJobHTTPControlDiagnosticsAndRedaction(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "scheduled-job-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspace := store.WorkspaceRecord{ID: "workspace-scheduled-job-http", Name: "schedule",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := st.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(st).Create(t.Context(),
		application.CreateRunRequest{Goal: "observe one explicit target", Profile: string(domain.ProfileCode),
			Surface: string(domain.ExecutionSurfaceCode), Phase: string(domain.ExecutionPhasePlan),
			WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 4}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(st).Start(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret := "-----BEGIN PRIVATE KEY-----scheduled-http-secret"
	if _, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: secret,
		OperationKey: "scheduled-http-secret-event-0001", RequestedBy: "http_test",
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewScheduledJobService(st)
	api, err := New(st, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		ScheduledJobControlEnabled: true, ScheduledJobController: service,
		ModelRegistry: modelregistry.New(nil), AppVersion: "scheduled-job-http-test"})
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	deadline := anchor.Add(time.Hour)
	body := `{"version":"scheduled-job.v1","schedule":{"kind":"once",` +
		`"timezone":"UTC","anchor_at":"` + anchor.Format(time.RFC3339) + `",` +
		`"misfire_policy":"run_once"},"deadline_at":"` + deadline.Format(time.RFC3339) + `",` +
		`"stop_on_target_terminal":true,"max_rounds":1,"max_model_calls":0,` +
		`"max_elapsed_seconds":3600,"retry":{"max_attempts":2,` +
		`"initial_backoff_seconds":1,"max_backoff_seconds":10},` +
		`"notification":"all","execution_mode":"read_only","confirm_repair":false}`
	path := "/api/v1/runs/" + run.ID + "/scheduled-jobs"
	createdResponse := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "scheduled-http-create-0001", "application/json", strings.NewReader(body))
	if createdResponse.Code != http.StatusAccepted ||
		!strings.Contains(createdResponse.Body.String(), `"execution_started":false`) ||
		!strings.Contains(createdResponse.Body.String(), `"authority_bypass":false`) ||
		strings.Contains(createdResponse.Body.String(), "fence_token") ||
		strings.Contains(createdResponse.Body.String(), "operation_key") {
		t.Fatalf("scheduled job create status=%d body=%s",
			createdResponse.Code, createdResponse.Body.String())
	}
	var envelope struct {
		Data ScheduledJobControlView `json:"data"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	job := envelope.Data.Job
	if job.ID == "" || job.OwnerRunID != run.ID || job.Spec.ExecutionMode != domain.ScheduledJobReadOnly {
		t.Fatalf("unexpected scheduled job: %#v", job)
	}

	replay := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "scheduled-http-create-0001", "application/json", strings.NewReader(body))
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("scheduled job replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	readAsControl := performSessionMessageRequest(t, api, http.MethodGet,
		ScheduledJobPathTemplate, testControlToken, "", "", nil)
	assertAPIError(t, readAsControl, http.StatusUnauthorized, "POLICY_DENIED")
	createAsRead := performSessionMessageRequest(t, api, http.MethodPost, path,
		testAccessToken, "scheduled-http-create-0002", "application/json", strings.NewReader(body))
	assertAPIError(t, createAsRead, http.StatusUnauthorized, "POLICY_DENIED")

	list := performSessionMessageRequest(t, api, http.MethodGet,
		path+"?limit=10", testAccessToken, "", "", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), job.ID) {
		t.Fatalf("scheduled job list status=%d body=%s", list.Code, list.Body.String())
	}
	detail := performSessionMessageRequest(t, api, http.MethodGet,
		"/api/v1/scheduled-jobs/"+job.ID, testAccessToken, "", "", nil)
	if detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), "fence_token") {
		t.Fatalf("scheduled job detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	pause := performSessionMessageRequest(t, api, http.MethodPost,
		path+"/"+job.ID+"/pause", testControlToken, "scheduled-http-pause-0001",
		"application/json", strings.NewReader(`{"version":"scheduled-job-control.v1",`+
			`"expected_revision":`+jsonInt(job.Revision)+`}`))
	if pause.Code != http.StatusAccepted || !strings.Contains(pause.Body.String(), `"status":"paused"`) {
		t.Fatalf("scheduled job pause status=%d body=%s", pause.Code, pause.Body.String())
	}
	unknown := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "scheduled-http-unknown-0001", "application/json",
		strings.NewReader(strings.TrimSuffix(body, "}")+`,"unknown":true}`))
	assertAPIError(t, unknown, http.StatusBadRequest, "INVALID_ARGUMENT")

	doctor := performSessionMessageRequest(t, api, http.MethodGet,
		DoctorSnapshotPath+"?run_id="+run.ID, testAccessToken, "", "", nil)
	if doctor.Code != http.StatusOK || !strings.Contains(doctor.Body.String(),
		`"protocol_version":"doctor-snapshot.v1"`) || strings.Contains(doctor.Body.String(), secret) {
		t.Fatalf("doctor status=%d body=%s", doctor.Code, doctor.Body.String())
	}
	debug := performSessionMessageRequest(t, api, http.MethodGet,
		DebugQueryPath+"?run_id="+run.ID+"&limit=100", testAccessToken, "", "", nil)
	if debug.Code != http.StatusOK || !strings.Contains(debug.Body.String(),
		`"payload_state":"withheld"`) || strings.Contains(debug.Body.String(), secret) ||
		strings.Contains(debug.Body.String(), "payload_json") {
		t.Fatalf("debug status=%d body=%s", debug.Code, debug.Body.String())
	}
	bundle := performSessionMessageRequest(t, api, http.MethodGet,
		DiagnosticBundlePath+"?run_id="+run.ID+"&limit=10", testAccessToken, "", "", nil)
	if bundle.Code != http.StatusOK || strings.Contains(bundle.Body.String(), secret) ||
		!strings.Contains(bundle.Body.String(), `"protocol_version":"diagnostic-bundle.v1"`) {
		t.Fatalf("bundle status=%d body=%s", bundle.Code, bundle.Body.String())
	}
}

func jsonInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
