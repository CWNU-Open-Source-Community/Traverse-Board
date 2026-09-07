package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const (
	FullCDPSessionControlPathTemplate      = "/api/v1/runs/{run_id}/full-cdp-session"
	FullCDPSessionCloseControlPathTemplate = "/api/v1/runs/{run_id}/full-cdp-session/close"
)

type FullCDPSessionOpenRequestView struct {
	Version                              string                      `json:"version"`
	Target                               string                      `json:"target"`
	Browser                              FullCDPBrowserSelectionView `json:"browser"`
	ExpectedExecutionPermissionRevision  int64                       `json:"expected_execution_permission_revision"`
	ExpectedBrowserCDPPermissionRevision int64                       `json:"expected_browser_cdp_permission_revision"`
	ConfirmFullCDP                       bool                        `json:"confirm_full_cdp"`
	Reason                               string                      `json:"reason,omitempty"`
}

type FullCDPBrowserSelectionView struct {
	Product string `json:"product"`
	Channel string `json:"channel"`
}

type FullCDPSessionCloseRequestView struct {
	Version           string `json:"version"`
	ExpectedSessionID string `json:"expected_session_id"`
	Reason            string `json:"reason,omitempty"`
}

// FullCDPSessionView is an explicit metadata-only HTTP projection. It must not
// grow runtime handles, process identities, filesystem paths, DevTools
// endpoints, permission snapshot identities, fences, or authorization data.
type FullCDPSessionView struct {
	Version              string                       `json:"version"`
	SessionID            string                       `json:"session_id,omitempty"`
	RunID                string                       `json:"run_id"`
	State                string                       `json:"state"`
	Browser              *FullCDPBrowserSelectionView `json:"browser,omitempty"`
	TargetOrigin         string                       `json:"target_origin,omitempty"`
	RuntimeAvailable     bool                         `json:"runtime_available"`
	StartedAt            *time.Time                   `json:"started_at,omitempty"`
	ExpiresAt            *time.Time                   `json:"expires_at,omitempty"`
	CompletedAt          *time.Time                   `json:"completed_at,omitempty"`
	CloseReason          string                       `json:"close_reason,omitempty"`
	CDPClosed            bool                         `json:"cdp_closed"`
	ProcessTreeQuiescent bool                         `json:"process_tree_quiescent"`
	ProfileReleased      bool                         `json:"profile_released"`
	ProfileCleaned       bool                         `json:"profile_cleaned"`
	FailureCode          string                       `json:"failure_code,omitempty"`
}

type FullCDPSessionControlView struct {
	Session  FullCDPSessionView `json:"session"`
	Replayed bool               `json:"replayed"`
}

func matchFullCDPSessionControlPath(requestPath string) (runID string,
	closeSession bool, matched bool,
) {
	const prefix = "/api/v1/runs/"
	const sessionSuffix = "/full-cdp-session"
	const closeSuffix = sessionSuffix + "/close"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", false, false
	}
	suffix := sessionSuffix
	if strings.HasSuffix(requestPath, closeSuffix) {
		suffix = closeSuffix
		closeSession = true
	} else if !strings.HasSuffix(requestPath, sessionSuffix) {
		return "", false, false
	}
	runID = strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if runID == "" || strings.Contains(runID, "/") {
		return "", false, false
	}
	return runID, closeSession, true
}

func (a *API) serveFullCDPSessionControl(writer http.ResponseWriter,
	request *http.Request, requestID, runID string, closeSession bool,
) {
	if !a.fullCDPSessionControlEnabled || a.fullCDPSessionController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if closeSession {
		a.serveFullCDPSessionClose(writer, request, requestID, runID)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Full CDP session endpoint only supports GET and POST"),
			http.StatusMethodNotAllowed)
		return
	}
	if request.Method == http.MethodGet {
		if !a.authorized(request, a.tokenHash) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
				"valid bearer authorization is required"), http.StatusUnauthorized)
			return
		}
		if err := validateFullCDPSessionBoundary(request, runID, true); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.fullCDPSessionController.GetFullCDPSession(
			request.Context(), runID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, FullCDPSessionControlView{
			Session: fullCDPSessionView(value),
		}, nil, http.StatusOK)
		return
	}
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid control bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if err := validateFullCDPSessionBoundary(request, runID, false); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, err := sessionControlIdempotencyKey(request.Header,
		"Full CDP session open")
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view FullCDPSessionOpenRequestView
	if err := decodeFullCDPSessionRequest(request, "Full CDP session open", &view); err != nil {
		a.writeFullCDPSessionRequestError(writer, requestID, err)
		return
	}
	if view.Version != application.FullCDPSessionProtocolVersion {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Full CDP session open version is invalid"), 0)
		return
	}
	result, err := a.fullCDPSessionController.OpenFullCDPSession(request.Context(),
		application.OpenFullCDPSessionRequest{RunID: runID, Target: view.Target,
			Browser: application.FullCDPBrowserSelection{Product: view.Browser.Product,
				Channel: view.Browser.Channel},
			ExpectedExecutionPermissionRevision:  view.ExpectedExecutionPermissionRevision,
			ExpectedBrowserCDPPermissionRevision: view.ExpectedBrowserCDPPermissionRevision,
			ConfirmFullCDP:                       view.ConfirmFullCDP, Reason: view.Reason,
			OperationKey: operationKey})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, FullCDPSessionControlView{
		Session: fullCDPSessionView(result.Session), Replayed: result.Replayed,
	}, nil, http.StatusCreated)
}

func (a *API) serveFullCDPSessionClose(writer http.ResponseWriter,
	request *http.Request, requestID, runID string,
) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Full CDP session close endpoint only supports POST"),
			http.StatusMethodNotAllowed)
		return
	}
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid control bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if err := validateFullCDPSessionBoundary(request, runID, false); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, err := sessionControlIdempotencyKey(request.Header,
		"Full CDP session close")
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view FullCDPSessionCloseRequestView
	if err := decodeFullCDPSessionRequest(request, "Full CDP session close", &view); err != nil {
		a.writeFullCDPSessionRequestError(writer, requestID, err)
		return
	}
	if view.Version != application.FullCDPSessionCloseProtocolVersion {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Full CDP session close version is invalid"), 0)
		return
	}
	result, err := a.fullCDPSessionController.CloseFullCDPSession(request.Context(),
		application.CloseFullCDPSessionRequest{RunID: runID,
			ExpectedSessionID: view.ExpectedSessionID,
			OperationKey:      operationKey, Reason: view.Reason})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, FullCDPSessionControlView{
		Session: fullCDPSessionView(result.Session), Replayed: result.Replayed,
	}, nil, http.StatusOK)
}

func validateFullCDPSessionBoundary(request *http.Request, runID string,
	readOnly bool,
) error {
	if err := validatePathIdentity(runID); err != nil {
		return err
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		return err
	}
	if readOnly {
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			return apperror.New(apperror.CodeInvalidArgument,
				"Full CDP session GET cannot contain a body")
		}
		return nil
	}
	return nil
}

func decodeFullCDPSessionRequest(request *http.Request, label string,
	target any,
) error {
	body, err := readBoundedControlBody(request)
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONObjectFields(body, label); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			label+" body must be one strict JSON object", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func (a *API) writeFullCDPSessionRequestError(writer http.ResponseWriter,
	requestID string, err error,
) {
	status := 0
	if apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeResourceExhausted {
		status = http.StatusRequestEntityTooLarge
	}
	a.writeError(writer, requestID, err, status)
}

func fullCDPSessionView(value application.FullCDPSessionView) FullCDPSessionView {
	view := FullCDPSessionView{Version: value.Version, SessionID: value.SessionID,
		RunID: value.RunID, State: string(value.State),
		TargetOrigin: value.TargetOrigin, RuntimeAvailable: value.RuntimeAvailable,
		StartedAt: value.StartedAt, ExpiresAt: value.ExpiresAt,
		CompletedAt: value.CompletedAt, CloseReason: value.CloseReason,
		CDPClosed: value.CDPClosed, ProcessTreeQuiescent: value.ProcessTreeQuiescent,
		ProfileReleased: value.ProfileReleased, ProfileCleaned: value.ProfileCleaned,
		FailureCode: value.FailureCode}
	if value.Browser.Product != "" || value.Browser.Channel != "" {
		view.Browser = &FullCDPBrowserSelectionView{Product: value.Browser.Product,
			Channel: value.Browser.Channel}
	}
	return view
}
