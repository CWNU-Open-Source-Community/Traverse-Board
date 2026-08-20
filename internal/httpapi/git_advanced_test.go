package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitadvanced"
)

type gitAdvancedControllerStub struct {
	discoverRunID string
	discoverSpec  gitadvanced.Spec
	review        application.GitAdvancedReviewRequest
	execute       application.GitAdvancedExecuteRequest
	projectionRun string
	projectionMax int
}

func (s *gitAdvancedControllerStub) DiscoverHunks(_ context.Context, runID string,
	spec gitadvanced.Spec,
) (application.GitAdvancedReviewResult, error) {
	s.discoverRunID, s.discoverSpec = runID, spec
	return application.GitAdvancedReviewResult{
		ProtocolVersion: application.GitAdvancedAPIProtocolVersion,
		RunID:           runID,
		Preview: gitadvanced.Preview{ProtocolVersion: gitadvanced.PreviewProtocolVersion,
			Operation: spec.Operation, Hunks: []gitadvanced.Hunk{{ID: strings.Repeat("a", 64),
				Path: "README.md"}}},
	}, nil
}

func (s *gitAdvancedControllerStub) Review(_ context.Context,
	request application.GitAdvancedReviewRequest,
) (application.GitAdvancedReviewResult, error) {
	s.review = request
	return application.GitAdvancedReviewResult{
		ProtocolVersion: application.GitAdvancedAPIProtocolVersion,
		RunID:           request.RunID,
		Preview: gitadvanced.Preview{ProtocolVersion: gitadvanced.PreviewProtocolVersion,
			Operation: request.Spec.Operation},
		Operation: &gitadvanced.OperationRecord{ID: "git-advanced-operation-test"},
	}, nil
}

func (s *gitAdvancedControllerStub) Execute(_ context.Context,
	request application.GitAdvancedExecuteRequest,
) (application.GitAdvancedExecuteResult, error) {
	s.execute = request
	return application.GitAdvancedExecuteResult{
		ProtocolVersion: application.GitAdvancedAPIProtocolVersion,
		Operation:       gitadvanced.OperationRecord{ID: request.OperationID},
		Receipt: gitadvanced.Receipt{ProtocolVersion: gitadvanced.ReceiptProtocolVersion,
			ID: "git-advanced-receipt-test", Status: gitadvanced.ReceiptSucceeded},
	}, nil
}

func (s *gitAdvancedControllerStub) Projection(_ context.Context, runID string,
	limit int,
) (application.GitAdvancedProjection, error) {
	s.projectionRun, s.projectionMax = runID, limit
	return application.GitAdvancedProjection{
		ProtocolVersion: application.GitAdvancedAPIProtocolVersion,
		RunID:           runID,
		Capability: gitadvanced.CapabilitySnapshot{
			ProtocolVersion: gitadvanced.CapabilityProtocolVersion,
			Enabled:         true,
		},
	}, nil
}

func TestGitAdvancedHTTPRoutesAuthenticateAndBindRunAuthority(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &gitAdvancedControllerStub{}
	fixture.api.gitAdvancedController = controller
	fixture.api.gitAdvancedControlEnabled = true
	basePath := "/api/v1/runs/" + fixture.run.ID + "/git-advanced"

	projection := fixture.get(t, basePath+"?limit=7")
	if projection.Code != http.StatusOK || controller.projectionRun != fixture.run.ID ||
		controller.projectionMax != 7 || !strings.Contains(projection.Body.String(),
		`"protocol_version":"git-advanced-api.v1"`) {
		t.Fatalf("projection status=%d run=%q limit=%d body=%s", projection.Code,
			controller.projectionRun, controller.projectionMax, projection.Body.String())
	}

	discoverRequest := httptest.NewRequest(http.MethodPost,
		"http://127.0.0.1"+basePath+"/discover-hunks", strings.NewReader(
			`{"spec":{"protocol_version":"git-advanced.v1",`+
				`"operation":"hunk_stage","paths":["README.md"]}}`))
	discoverRequest.Host = "127.0.0.1:8765"
	discoverRequest.RemoteAddr = "127.0.0.1:45000"
	discoverRequest.Header.Set("Authorization", "Bearer "+testAccessToken)
	discoverRequest.Header.Set("Content-Type", "application/json")
	discover := httptest.NewRecorder()
	fixture.api.ServeHTTP(discover, discoverRequest)
	if discover.Code != http.StatusOK || controller.discoverRunID != fixture.run.ID ||
		controller.discoverSpec.Operation != gitadvanced.HunkStage ||
		!strings.Contains(discover.Body.String(), strings.Repeat("a", 64)) {
		t.Fatalf("discover status=%d run=%q spec=%#v body=%s", discover.Code,
			controller.discoverRunID, controller.discoverSpec, discover.Body.String())
	}

	review := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		basePath+"/review", "git-advanced-review-http-0001", strings.NewReader(
			`{"operation_key":"stage-readme","scope":{"capability_generation":"`+
				strings.Repeat("b", 64)+`","lease_generation":3},`+
				`"spec":{"protocol_version":"git-advanced.v1",`+
				`"operation":"hunk_stage","hunk_ids":["`+strings.Repeat("a", 64)+`"]}}`))
	if review.Code != http.StatusCreated || controller.review.RunID != fixture.run.ID ||
		controller.review.RequestedBy != "api_operator" ||
		controller.review.ProtocolVersion != application.GitAdvancedAPIProtocolVersion ||
		controller.review.OperationKey != "stage-readme" {
		t.Fatalf("review status=%d request=%#v body=%s", review.Code,
			controller.review, review.Body.String())
	}

	execute := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		basePath+"/execute", "git-advanced-execute-http-0001", strings.NewReader(
			`{"operation_id":"git-advanced-operation-test",`+
				`"approval_id":"approval-test","scope":{"capability_generation":"`+
				strings.Repeat("b", 64)+`","lease_generation":3}}`))
	if execute.Code != http.StatusOK || controller.execute.RunID != fixture.run.ID ||
		controller.execute.RequestedBy != "api_operator" ||
		controller.execute.OperationID != "git-advanced-operation-test" ||
		controller.execute.ApprovalID != "approval-test" {
		t.Fatalf("execute status=%d request=%#v body=%s", execute.Code,
			controller.execute, execute.Body.String())
	}
}

func TestGitAdvancedHTTPRoutesFailClosed(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &gitAdvancedControllerStub{}
	fixture.api.gitAdvancedController = controller
	fixture.api.gitAdvancedControlEnabled = true
	basePath := "/api/v1/runs/" + fixture.run.ID + "/git-advanced"

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+basePath+"/review",
		strings.NewReader(`{"operation_key":"op"}`))
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	fixture.api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("read token authorized mutation: status=%d body=%s", response.Code,
			response.Body.String())
	}

	for name, body := range map[string]string{
		"duplicate field": `{"operation_key":"one","operation_key":"two"}`,
		"unknown field":   `{"operation_key":"one","force":true}`,
		"trailing value":  `{"operation_key":"one"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
				basePath+"/review", "git-advanced-invalid-"+strings.ReplaceAll(name, " ", "-"),
				strings.NewReader(body))
			if result.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}

	fixture.api.gitAdvancedControlEnabled = false
	disabled := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		basePath+"/review", "git-advanced-disabled-0001",
		strings.NewReader(`{"operation_key":"op"}`))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled route status=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestGitAdvancedHTTPConfigurationRequiresEveryAuthorityGate(t *testing.T) {
	fixture := newAPIFixture(t)
	valid := Config{
		AccessToken:                       testAccessToken,
		ControlToken:                      testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		},
		ApprovalControlEnabled:            true,
		ApprovalController:                application.NewApprovalControlService(fixture.store, nil, nil),
		WorkspaceCheckpointControlEnabled: true,
		WorkspaceCheckpointController:     &workspaceCheckpointControllerStub{},
		GitAdvancedControlEnabled:         true,
		GitAdvancedController:             &gitAdvancedControllerStub{},
	}
	if _, err := New(fixture.store, valid); err != nil {
		t.Fatalf("valid Git advanced configuration failed: %v", err)
	}

	tests := map[string]func(*Config){
		"permission control": func(config *Config) {
			config.ExecutionPermissionControlEnabled = false
		},
		"operator approval": func(config *Config) {
			config.ExecutionPermissionCapabilities.OperatorApprovalEnabled = false
		},
		"approval control": func(config *Config) {
			config.ApprovalControlEnabled = false
		},
		"checkpoint control": func(config *Config) {
			config.WorkspaceCheckpointControlEnabled = false
		},
	}
	for name, disable := range tests {
		t.Run(name, func(t *testing.T) {
			config := valid
			disable(&config)
			if _, err := New(fixture.store, config); err == nil ||
				!strings.Contains(err.Error(), "Git advanced control requires") {
				t.Fatalf("missing %s gate was accepted: %v", name, err)
			}
		})
	}
}
