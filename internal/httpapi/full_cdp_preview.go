package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const (
	FullCDPPreviewPathTemplate       = "/api/v1/runs/{run_id}/full-cdp-session/preview"
	FullCDPPreviewImagePathTemplate  = "/api/v1/runs/{run_id}/full-cdp-session/preview-image"
	FullCDPPreviewActionPathTemplate = "/api/v1/runs/{run_id}/full-cdp-session/preview-action"
)

type FullCDPPreviewRequestView struct {
	Version           string `json:"version"`
	ExpectedSessionID string `json:"expected_session_id"`
}

type FullCDPPreviewActionRequestView struct {
	Version            string `json:"version"`
	ExpectedSessionID  string `json:"expected_session_id"`
	ExpectedSnapshotID string `json:"expected_snapshot_id"`
	Action             string `json:"action"`
	Selector           string `json:"selector"`
	Value              string `json:"value,omitempty"`
}

func matchFullCDPPreviewPath(path string) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) != 4 || parts[0] != "runs" || parts[1] == "" || parts[2] != "full-cdp-session" ||
		(parts[3] != "preview" && parts[3] != "preview-image" && parts[3] != "preview-action") {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func (a *API) serveFullCDPPreview(w http.ResponseWriter, r *http.Request, requestID, runID, action string) {
	image := action == "preview-image"
	controller, ok := a.fullCDPSessionController.(application.FullCDPPreviewController)
	if !a.fullCDPSessionControlEnabled || !ok {
		a.writeError(w, requestID, apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	token, method := a.controlTokenHash, http.MethodPost
	if image {
		token, method = a.tokenHash, http.MethodGet
	}
	if !a.authorized(r, token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(w, requestID, apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "browser preview method is invalid"), http.StatusMethodNotAllowed)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if image {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "browser preview GET cannot contain a body"), 0)
			return
		}
		if err := validateSingleQueryValues(r.URL.Query(), "session_id", "sha256"); err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		sessionID, digest := r.URL.Query().Get("session_id"), r.URL.Query().Get("sha256")
		if validatePathIdentity(sessionID) != nil || !isLowerHexSHA256(digest) {
			a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "browser preview image identity is invalid"), 0)
			return
		}
		value, err := controller.ReadFullCDPPreviewImage(r.Context(), runID, sessionID, digest)
		if err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		actual := sha256.Sum256(value.PNG)
		if value.View.RunID != runID || value.View.SessionID != sessionID || value.View.Image.MediaType != "image/png" ||
			value.View.Image.SHA256 != digest || value.View.Image.Bytes != len(value.PNG) || hex.EncodeToString(actual[:]) != digest {
			a.writeError(w, requestID, apperror.New(apperror.CodeUnavailable, "browser preview image failed identity verification"), 0)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(value.PNG)))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", `"`+digest+`"`)
		w.Header().Set("X-Cyberagent-Content-SHA256", digest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(value.PNG)
		return
	}
	if err := rejectQuery(r.URL.Query()); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(r.Header); err != nil {
		a.writeError(w, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	if action == "preview-action" {
		var body FullCDPPreviewActionRequestView
		if err := decodeFullCDPSessionRequest(r, "Full CDP preview action", &body); err != nil {
			a.writeFullCDPSessionRequestError(w, requestID, err)
			return
		}
		if body.Version != application.FullCDPPreviewActionProtocolVersion || validatePathIdentity(body.ExpectedSessionID) != nil || !isLowerHexSHA256(body.ExpectedSnapshotID) {
			a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "browser preview action identity is invalid"), 0)
			return
		}
		value, err := controller.ActFullCDPPreview(r.Context(), application.FullCDPPreviewActionRequest{
			RunID: runID, SessionID: body.ExpectedSessionID, SnapshotID: body.ExpectedSnapshotID, Action: body.Action, Selector: body.Selector, Value: body.Value})
		if err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		if value.Version != application.FullCDPPreviewProtocolVersion || value.RunID != runID || value.SessionID != body.ExpectedSessionID {
			a.writeError(w, requestID, apperror.New(apperror.CodeUnavailable, "browser preview result identity changed"), 0)
			return
		}
		a.writeSuccess(w, requestID, value, nil)
		return
	}
	var body FullCDPPreviewRequestView
	if err := decodeFullCDPSessionRequest(r, "Full CDP preview", &body); err != nil {
		a.writeFullCDPSessionRequestError(w, requestID, err)
		return
	}
	if body.Version != application.FullCDPPreviewProtocolVersion || validatePathIdentity(body.ExpectedSessionID) != nil {
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "browser preview request is invalid"), 0)
		return
	}
	value, err := controller.CaptureFullCDPPreview(r.Context(), runID, body.ExpectedSessionID)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if value.Version != application.FullCDPPreviewProtocolVersion || value.RunID != runID || value.SessionID != body.ExpectedSessionID {
		a.writeError(w, requestID, apperror.New(apperror.CodeUnavailable, "browser preview result identity changed"), 0)
		return
	}
	a.writeSuccess(w, requestID, value, nil)
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
