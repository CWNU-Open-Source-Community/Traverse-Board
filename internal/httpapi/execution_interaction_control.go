package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const RunExecutionInteractionControlPathTemplate = "/api/v1/runs/{run_id}/execution-interaction"

type RunExecutionInteractionControlRequestView struct {
	Mode                     string `json:"mode"`
	Trust                    string `json:"trust"`
	Reason                   string `json:"reason,omitempty"`
	ConfirmWorkspaceTrust    bool   `json:"confirm_workspace_trust,omitempty"`
	ConfirmDebugBoundary     bool   `json:"confirm_debug_boundary,omitempty"`
	ConfirmContainerBoundary bool   `json:"confirm_container_boundary,omitempty"`
}

type RunExecutionInteractionControlView struct {
	ExecutionInteraction RunExecutionInteractionView `json:"execution_interaction"`
	Replayed             bool                        `json:"replayed"`
}

func matchRunExecutionInteractionControlPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/runs/"
	const suffix = "/execution-interaction"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	runID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if runID == "" || strings.Contains(runID, "/") {
		return "", false
	}
	return runID, true
}

func (a *API) serveRunExecutionInteractionControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.controlEnabled {
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
				"Run execution interaction endpoint only supports POST"),
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
	var view RunExecutionInteractionControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution interaction body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	result, err := application.NewRunExecutionInteractionService(a.store).Change(
		request.Context(), application.ChangeRunExecutionInteractionRequest{
			RunID: runID, Mode: view.Mode, Trust: view.Trust,
			OperationKey: operationKey, RequestedBy: "http_control", Reason: view.Reason,
			ConfirmWorkspaceTrust:    view.ConfirmWorkspaceTrust,
			ConfirmDebugBoundary:     view.ConfirmDebugBoundary,
			ConfirmContainerBoundary: view.ConfirmContainerBoundary,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, RunExecutionInteractionControlView{
		ExecutionInteraction: runExecutionInteractionView(result.Interaction),
		Replayed:             result.Replayed,
	}, nil, http.StatusAccepted)
}
