package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

type ThreadGitController interface {
	State(context.Context, string) (application.ThreadGitState, error)
	Preview(context.Context, string, application.ThreadGitPreviewRequest) (application.ThreadGitPreview, error)
	Execute(context.Context, string, application.ThreadGitExecuteRequest) (application.ThreadGitResult, error)
	Observe(context.Context, string, string) (application.ThreadGitResult, error)
}

func matchThreadGitPath(path string) (string, []string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) < 3 || parts[0] != "threads" || parts[1] == "" || parts[2] != "git" {
		return "", nil, false
	}
	if len(parts) == 3 || len(parts) == 4 && (parts[3] == "preview" || parts[3] == "execute") || len(parts) == 5 && parts[3] == "requests" && parts[4] != "" {
		return parts[1], parts[3:], true
	}
	return "", nil, false
}

func (a *API) routeThreadGit(writer http.ResponseWriter, request *http.Request, requestID, threadID string, remaining []string) {
	if a.threadGitController == nil {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "task Git is unavailable"), http.StatusNotFound)
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	read := len(remaining) == 0 || remaining[0] == "requests"
	token := a.controlTokenHash
	method := http.MethodPost
	if read {
		token = a.tokenHash
		method = http.MethodGet
	} else if !a.gitAdvancedControlEnabled || !a.executionPermissionControlEnabled {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "task Git control is unavailable"), http.StatusNotFound)
		return
	}
	if !a.authorized(request, token) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != method {
		writer.Header().Set("Allow", method)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "method is not supported for this task Git operation"), http.StatusMethodNotAllowed)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if read {
		if request.ContentLength > 0 || len(request.TransferEncoding) > 0 {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "read-only Git requests cannot contain a body"), 0)
			return
		}
		var value any
		var err error
		if len(remaining) == 0 {
			var state application.ThreadGitState
			state, err = a.threadGitController.State(request.Context(), threadID)
			if err == nil && (!a.gitAdvancedControlEnabled || !a.executionPermissionControlEnabled) {
				state.CanExecute = false
				state.BlockedReason = "当前服务未开启任务 Git 控制"
			}
			value = state
		} else {
			if err = validatePathIdentity(remaining[1]); err == nil {
				value, err = a.threadGitController.Observe(request.Context(), threadID, remaining[1])
			}
		}
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	body, err := readBoundedRequestBody(request, 64*1024)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err = rejectDuplicateJSONObjectFields(body, "task Git"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	decode := func(value any) error {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(value); err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument, "task Git request is not a closed object", err)
		}
		return ensureJSONEOF(decoder)
	}
	var value any
	if remaining[0] == "preview" {
		var input application.ThreadGitPreviewRequest
		err = decode(&input)
		if err == nil {
			value, err = a.threadGitController.Preview(request.Context(), threadID, input)
		}
	} else {
		var input application.ThreadGitExecuteRequest
		err = decode(&input)
		if err == nil {
			value, err = a.threadGitController.Execute(request.Context(), threadID, input)
		}
	}
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, value, nil)
}
