package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const RunBrowserCDPPermissionControlPathTemplate = "/api/v1/runs/{run_id}/browser-cdp-permission"

type RunBrowserCDPPermissionControlRequestView struct {
	Mode                string `json:"mode"`
	Reason              string `json:"reason,omitempty"`
	ConfirmFullCDPDebug bool   `json:"confirm_full_cdp_debug,omitempty"`
}

type RunBrowserCDPPermissionControlView struct {
	BrowserCDPPermission RunBrowserCDPPermissionView `json:"browser_cdp_permission"`
	Replayed             bool                        `json:"replayed"`
}

func matchRunBrowserCDPPermissionControlPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/runs/"
	const suffix = "/browser-cdp-permission"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	runID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if runID == "" || strings.Contains(runID, "/") {
		return "", false
	}
	return runID, true
}

func (a *API) serveRunBrowserCDPPermissionControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.browserCDPPermissionControlEnabled {
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
				"Run browser CDP permission endpoint only supports POST"),
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
	var view RunBrowserCDPPermissionControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Run browser CDP permission body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	service := application.NewRunBrowserCDPPermissionServiceWithExecutionCapabilities(
		a.store, a.browserCDPPermissionCapabilities,
		a.executionPermissionCapabilities)
	result, err := service.Change(request.Context(),
		application.ChangeRunBrowserCDPPermissionRequest{
			RunID: runID, Mode: view.Mode, OperationKey: operationKey,
			RequestedBy: "http_control", Reason: view.Reason,
			ConfirmFullCDPDebug: view.ConfirmFullCDPDebug,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	executionPermission, err := a.store.GetRunExecutionPermission(request.Context(), runID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, RunBrowserCDPPermissionControlView{
		BrowserCDPPermission: runBrowserCDPPermissionView(result.Permission,
			a.browserCDPPermissionCapabilities, executionPermission,
			a.executionPermissionCapabilities),
		Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}
