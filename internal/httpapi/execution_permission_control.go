package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const RunExecutionPermissionControlPathTemplate = "/api/v1/runs/{run_id}/execution-permission"

type RunExecutionPermissionControlRequestView struct {
	Mode                    string `json:"mode"`
	Reason                  string `json:"reason,omitempty"`
	ConfirmUserApproval     bool   `json:"confirm_user_approval,omitempty"`
	ConfirmDangerFullAccess bool   `json:"confirm_danger_full_access,omitempty"`
	ConfirmDebugAccess      bool   `json:"confirm_debug_access,omitempty"`
}

type RunExecutionPermissionControlView struct {
	ExecutionPermission RunExecutionPermissionView `json:"execution_permission"`
	Replayed            bool                       `json:"replayed"`
}

func matchRunExecutionPermissionControlPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/runs/"
	const suffix = "/execution-permission"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	runID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if runID == "" || strings.Contains(runID, "/") {
		return "", false
	}
	return runID, true
}

func (a *API) serveRunExecutionPermissionControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.executionPermissionControlEnabled {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodePolicyDenied,
				"valid control bearer authorization is required"),
			http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInvalidArgument,
				"Run execution permission endpoint only supports POST"),
			http.StatusMethodNotAllowed)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, err := runExecutionProfileIdempotencyKey(request.Header)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	body, err := readBoundedControlBody(request)
	if err != nil {
		status := 0
		if apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeResourceExhausted {
			status = http.StatusRequestEntityTooLarge
		}
		a.writeError(writer, requestID, err, status)
		return
	}
	var view RunExecutionPermissionControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution permission body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	service := application.NewRunExecutionPermissionService(
		a.store, a.executionPermissionCapabilities)
	result, err := service.Change(request.Context(),
		application.ChangeRunExecutionPermissionRequest{
			RunID: runID, Mode: view.Mode, OperationKey: operationKey,
			RequestedBy: "http_control", Reason: view.Reason,
			ConfirmUserApproval:     view.ConfirmUserApproval,
			ConfirmDangerFullAccess: view.ConfirmDangerFullAccess,
			ConfirmDebugAccess:      view.ConfirmDebugAccess,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, RunExecutionPermissionControlView{
		ExecutionPermission: runExecutionPermissionView(
			result.Permission, a.executionPermissionCapabilities),
		Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}

func executionPermissionRuntimeView(
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) ExecutionPermissionRuntimeView {
	return ExecutionPermissionRuntimeView{
		OperatorApprovalEnabled:   capabilities.OperatorApprovalEnabled,
		DangerFullAccessEnabled:   capabilities.DangerFullAccessEnabled,
		DebugMaximumAccessEnabled: capabilities.DebugMaximumAccessEnabled,
	}
}
