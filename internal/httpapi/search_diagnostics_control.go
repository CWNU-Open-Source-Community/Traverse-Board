package httpapi

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

type searchDiagnosticsController interface {
	Check(context.Context, string) (application.SearchDiagnostics, error)
}

type searchDiagnosticsRequest struct {
	Version string `json:"version"`
	Confirm bool   `json:"confirm"`
}

func matchSearchDiagnosticsPath(path string) (string, bool) {
	const prefix, suffix = "/api/v1/threads/", "/search-diagnostics"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, id != "" && !strings.Contains(id, "/")
}

func (a *API) serveSearchDiagnostics(writer http.ResponseWriter, request *http.Request, requestID, threadID string) {
	controller, available := a.providerSearchReadinessController.(searchDiagnosticsController)
	if !a.authorizeRunOperation(writer, request, requestID, available, "Search diagnostics") {
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	body, err := readBoundedRequestBody(request, 1024)
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	if !utf8.Valid(body) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "Search diagnostics requires UTF-8 JSON"), 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Search diagnostics"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view searchDiagnosticsRequest
	if err := decodeStrictRunOperation(body, &view, "Search diagnostics"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Version != application.SearchDiagnosticsProtocolVersion || !view.Confirm {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "An explicit search connection check is required"), 0)
		return
	}
	value, err := controller.Check(request.Context(), threadID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, value, nil)
}
