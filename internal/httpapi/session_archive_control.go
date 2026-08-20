package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/session"
)

const (
	SessionArchiveControlPathTemplate = "/api/v1/sessions/{session_id}/archive"
	SessionArchiveProtocolVersion     = "session_archive.v1"
	MaxSessionArchiveBodyBytes        = 4 * 1024
)

type SessionArchiveControlRequestView struct {
	Version string `json:"version"`
	Confirm bool   `json:"confirm"`
}

type SessionArchiveControlView struct {
	Version   string `json:"version"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Replayed  bool   `json:"replayed"`
}

func matchSessionArchiveControlPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/sessions/"
	const suffix = "/archive"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	sessionID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if sessionID == "" || strings.Contains(sessionID, "/") {
		return "", false
	}
	return sessionID, true
}

func (a *API) serveSessionArchiveControl(writer http.ResponseWriter,
	request *http.Request, requestID string, sessionID string,
) {
	if !a.sessionMessageEnabled {
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
			"Session archive endpoint only supports POST"), http.StatusMethodNotAllowed)
		return
	}
	if err := validatePathIdentity(sessionID); err != nil {
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
	body, err := readBoundedRequestBody(request, MaxSessionArchiveBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if !utf8.Valid(body) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Session archive body must be valid UTF-8 JSON"), 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Session archive"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view SessionArchiveControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Session archive body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Version != SessionArchiveProtocolVersion || !view.Confirm {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Session archive requires its exact protocol version and confirmation"), 0)
		return
	}
	record, err := a.store.GetSession(request.Context(), sessionID)
	if err != nil {
		a.writeError(writer, requestID, apperror.Normalize(err), 0)
		return
	}
	replayed := record.Status == session.StatusArchived
	if !replayed {
		if _, hookErr := hooks.ExecuteBoundary(request.Context(), a.lifecycleHooks,
			hooks.Input{Event: hooks.SessionClosed, WorkspaceID: record.WorkspaceID},
			map[string]any{"session_id": record.ID, "source": "http_archive"}); hookErr != nil {
			var denied hooks.DeniedError
			code := apperror.CodeUnavailable
			message := "restricted lifecycle hooks are unavailable"
			if errors.As(hookErr, &denied) {
				code = apperror.CodePolicyDenied
				message = "restricted lifecycle hook denied Session archive"
			}
			a.writeError(writer, requestID, apperror.Wrap(code, message, hookErr), 0)
			return
		}
		record.Status = session.StatusArchived
		record.UpdatedAt = time.Now().UTC()
		if err := a.store.SaveSession(request.Context(), record); err != nil {
			a.writeError(writer, requestID, apperror.Normalize(err), 0)
			return
		}
	}
	a.writeSuccessStatus(writer, requestID, SessionArchiveControlView{
		Version: SessionArchiveProtocolVersion, SessionID: record.ID,
		Status: session.StatusArchived, Replayed: replayed,
	}, nil, http.StatusAccepted)
}
