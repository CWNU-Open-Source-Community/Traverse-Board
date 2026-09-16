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

type ThreadPullRequestController interface {
	Discover(context.Context, string, string, string) (application.ThreadPullRequestDiscovery, error)
	Preview(context.Context, string, application.ThreadPullRequestPreviewRequest) (application.ThreadPullRequestPreviewResult, error)
	Create(context.Context, string, application.ThreadPullRequestCreateRequest) (application.ThreadPullRequestResult, error)
	Observe(context.Context, string, string) (application.ThreadPullRequestResult, error)
	Refresh(context.Context, string, application.ThreadPullRequestRefreshRequest) (application.ThreadPullRequestRefreshResult, error)
	ImportCredential(context.Context, string, application.ThreadPullRequestCredentialRequest) (application.GitHubReviewCredentialView, error)
}

type threadPullRequestRoute struct{ threadID, action string }

func matchThreadPullRequestPath(path string) (threadPullRequestRoute, bool) {
	p := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(p) < 3 || len(p) > 4 || p[0] != "threads" || p[1] == "" || p[2] != "pull-request" {
		return threadPullRequestRoute{}, false
	}
	r := threadPullRequestRoute{threadID: p[1]}
	if len(p) == 3 {
		return r, true
	}
	r.action = p[3]
	switch r.action {
	case "preview", "create", "request", "refresh", "credential":
		return r, true
	}
	return threadPullRequestRoute{}, false
}

func (a *API) serveThreadPullRequest(w http.ResponseWriter, r *http.Request, requestID string, route threadPullRequestRoute) {
	if a.threadPullRequestController == nil || !a.githubReviewControlEnabled {
		a.writeError(w, requestID, apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	if err := validatePathIdentity(route.threadID); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	read := route.action == "" || route.action == "request"
	method := http.MethodPost
	token := a.controlTokenHash
	if read {
		method = http.MethodGet
		token = a.tokenHash
	}
	if !a.authorized(r, token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(w, requestID, apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "unsupported pull request method"), http.StatusMethodNotAllowed)
		return
	}
	var result any
	var err error
	if read {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "read-only pull request requests cannot contain a body"), 0)
			return
		}
		query := r.URL.Query()
		for key, values := range query {
			if len(values) != 1 || (route.action == "request" && key != "operation_key") || (route.action == "" && key != "connection_id" && key != "base_branch") {
				a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "unexpected or repeated pull request query"), 0)
				return
			}
		}
		if route.action == "request" {
			result, err = a.threadPullRequestController.Observe(r.Context(), route.threadID, query.Get("operation_key"))
		} else {
			result, err = a.threadPullRequestController.Discover(r.Context(), route.threadID, query.Get("connection_id"), query.Get("base_branch"))
		}
	} else {
		if err = rejectQuery(r.URL.Query()); err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		if err = validateJSONContentType(r.Header); err != nil {
			a.writeError(w, requestID, err, http.StatusUnsupportedMediaType)
			return
		}
		bound := MaxGitHubReviewRequestBodyBytes
		if route.action == "credential" {
			bound = 4096
		}
		body, readErr := readBoundedRequestBody(r, int64(bound))
		if readErr != nil {
			a.writeError(w, requestID, readErr, 0)
			return
		}
		if err = rejectDuplicateJSONObjectFields(body, "task pull request"); err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		decode := func(v any) error {
			d := json.NewDecoder(bytes.NewReader(body))
			d.DisallowUnknownFields()
			if e := d.Decode(v); e != nil {
				return apperror.New(apperror.CodeInvalidArgument, "pull request body must be a closed JSON object")
			}
			return ensureJSONEOF(d)
		}
		switch route.action {
		case "preview":
			var v application.ThreadPullRequestPreviewRequest
			if err = decode(&v); err == nil {
				result, err = a.threadPullRequestController.Preview(r.Context(), route.threadID, v)
			}
		case "create":
			var v application.ThreadPullRequestCreateRequest
			if err = decode(&v); err == nil {
				result, err = a.threadPullRequestController.Create(r.Context(), route.threadID, v)
			}
		case "refresh":
			var v application.ThreadPullRequestRefreshRequest
			if err = decode(&v); err == nil {
				result, err = a.threadPullRequestController.Refresh(r.Context(), route.threadID, v)
			}
		case "credential":
			var v application.ThreadPullRequestCredentialRequest
			if err = decode(&v); err == nil {
				result, err = a.threadPullRequestController.ImportCredential(r.Context(), route.threadID, v)
			}
		}
	}
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	a.writeSuccess(w, requestID, result, nil)
}
