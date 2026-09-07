package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const (
	StandardCodePresetCreatePath              = "/api/v1/standard-code/preset"
	StandardCodePresetRunPathTemplate         = "/api/v1/runs/{run_id}/standard-code/preset"
	StandardCodePauseAndConfigurePathTemplate = "/api/v1/runs/{run_id}/standard-code/pause-and-configure"
)

type StandardCodePresetController interface {
	Configure(context.Context, application.ConfigureStandardCodeRequest) (
		application.StandardCodePresetResult, error)
}

type StandardCodePresetControlRequestView struct {
	Version               string `json:"version"`
	WorkspaceID           string `json:"workspace_id,omitempty"`
	Goal                  string `json:"goal,omitempty"`
	BackendIntent         string `json:"backend_intent"`
	ConfirmWorkspaceTrust bool   `json:"confirm_workspace_trust"`
	ExpectedTrustDigest   string `json:"expected_trust_digest,omitempty"`
}

type StandardCodeBackendReadinessView struct {
	Backend     string                                       `json:"backend"`
	Available   bool                                         `json:"available"`
	BlockedBy   []application.CapabilityReadinessBlocker     `json:"blocked_by"`
	Remediation []application.CapabilityReadinessRemediation `json:"remediation"`
}

type StandardCodePresetControlView struct {
	ProtocolVersion      string                                     `json:"protocol_version"`
	Status               application.StandardCodePresetResultStatus `json:"status"`
	RunID                string                                     `json:"run_id,omitempty"`
	WorkspaceID          string                                     `json:"workspace_id"`
	Action               domain.StandardCodePresetAction            `json:"action"`
	BackendIntent        domain.StandardCodeBackendIntent           `json:"backend_intent"`
	SelectedBackend      domain.StandardCodeBackend                 `json:"selected_backend,omitempty"`
	SelectionReason      domain.StandardCodeSelectionReason         `json:"selection_reason,omitempty"`
	LocalReadiness       StandardCodeBackendReadinessView           `json:"local_readiness"`
	DockerReadiness      StandardCodeBackendReadinessView           `json:"docker_readiness"`
	BlockedBy            []application.CapabilityReadinessBlocker   `json:"blocked_by"`
	NextSteps            []application.StandardCodeNextStep         `json:"next_steps"`
	TrustRequired        bool                                       `json:"trust_required"`
	TrustDigest          string                                     `json:"trust_digest,omitempty"`
	Run                  *RunView                                   `json:"run,omitempty"`
	Mode                 *RunModeView                               `json:"mode,omitempty"`
	ExecutionProfile     *RunExecutionProfileView                   `json:"execution_profile,omitempty"`
	ExecutionInteraction *RunExecutionInteractionView               `json:"execution_interaction,omitempty"`
	ExecutionPermission  *RunExecutionPermissionView                `json:"execution_permission,omitempty"`
	BrowserCDPPermission *RunBrowserCDPPermissionView               `json:"browser_cdp_permission,omitempty"`
	DrydockReady         bool                                       `json:"drydock_ready"`
	Network              string                                     `json:"network"`
	Credentials          string                                     `json:"credentials"`
	Replayed             bool                                       `json:"replayed"`
	CapabilityGrant      bool                                       `json:"capability_grant"`
}

func matchStandardCodePresetControlPath(path string) (runID string,
	action domain.StandardCodePresetAction, matched bool) {
	if path == StandardCodePresetCreatePath {
		return "", domain.StandardCodePresetConfigure, true
	}
	if runID, ok := matchRunOperationControlPath(path, "/standard-code/preset"); ok {
		return runID, domain.StandardCodePresetConfigure, true
	}
	if runID, ok := matchRunOperationControlPath(path,
		"/standard-code/pause-and-configure"); ok {
		return runID, domain.StandardCodePresetPauseAndConfigure, true
	}
	return "", "", false
}

func (a *API) serveStandardCodePresetControl(writer http.ResponseWriter,
	request *http.Request, requestID, runID string,
	action domain.StandardCodePresetAction) {
	if !a.standardCodePresetEnabled || a.standardCodePresetController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid control bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code preset endpoint only supports POST"),
			http.StatusMethodNotAllowed)
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
	operationKey, err := runCreationIdempotencyKey(request.Header)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	body, err := readBoundedRequestBody(request, MaxRunOperationControlBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if !utf8.Valid(body) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code preset body must be valid UTF-8 JSON"), 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Standard Code preset"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view StandardCodePresetControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Standard Code preset body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	result, err := a.standardCodePresetController.Configure(request.Context(),
		application.ConfigureStandardCodeRequest{Version: view.Version, RunID: runID,
			WorkspaceID: view.WorkspaceID, Goal: view.Goal,
			BackendIntent: view.BackendIntent, Action: string(action),
			OperationKey: operationKey, RequestedBy: "http_control",
			ConfirmWorkspaceTrust: view.ConfirmWorkspaceTrust,
			ExpectedTrustDigest:   view.ExpectedTrustDigest})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, a.standardCodePresetControlView(result),
		nil, http.StatusAccepted)
}

func (a *API) standardCodePresetControlView(
	result application.StandardCodePresetResult) StandardCodePresetControlView {
	view := StandardCodePresetControlView{ProtocolVersion: result.ProtocolVersion,
		Status: result.Status, RunID: result.RunID, WorkspaceID: result.WorkspaceID,
		Action: result.Action, BackendIntent: result.BackendIntent,
		SelectedBackend: result.SelectedBackend, SelectionReason: result.SelectionReason,
		LocalReadiness:  standardCodeBackendReadinessView(result.LocalReadiness),
		DockerReadiness: standardCodeBackendReadinessView(result.DockerReadiness),
		BlockedBy:       append([]application.CapabilityReadinessBlocker(nil), result.BlockedBy...),
		NextSteps:       append([]application.StandardCodeNextStep(nil), result.NextSteps...),
		TrustRequired:   result.TrustRequired, TrustDigest: result.TrustDigest,
		DrydockReady: result.DrydockReady, Network: result.Network,
		Credentials: result.Credentials, Replayed: result.Replayed,
		CapabilityGrant: result.CapabilityGrant}
	if result.Run != nil {
		value := runView(*result.Run)
		view.Run = &value
	}
	if result.Mode != nil {
		value := runModeView(*result.Mode)
		view.Mode = &value
	}
	if result.Profile != nil {
		value := runExecutionProfileView(*result.Profile)
		view.ExecutionProfile = &value
	}
	if result.Interaction != nil {
		value := runExecutionInteractionView(*result.Interaction)
		view.ExecutionInteraction = &value
	}
	if result.Permission != nil {
		value := runExecutionPermissionView(*result.Permission,
			a.executionPermissionCapabilities)
		view.ExecutionPermission = &value
	}
	if result.BrowserCDP != nil && result.Permission != nil {
		value := runBrowserCDPPermissionView(*result.BrowserCDP,
			a.browserCDPPermissionCapabilities, *result.Permission,
			a.executionPermissionCapabilities)
		view.BrowserCDPPermission = &value
	}
	return view
}

func standardCodeBackendReadinessView(
	value application.StandardCodeBackendReadiness) StandardCodeBackendReadinessView {
	return StandardCodeBackendReadinessView{Backend: string(value.Backend),
		Available: value.Available,
		BlockedBy: append([]application.CapabilityReadinessBlocker(nil), value.BlockedBy...),
		Remediation: append([]application.CapabilityReadinessRemediation(nil),
			value.Remediation...)}
}
