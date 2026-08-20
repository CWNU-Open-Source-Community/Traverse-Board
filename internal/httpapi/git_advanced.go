package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/gitadvanced"
)

const MaxGitAdvancedRequestBodyBytes = 128 * 1024

type GitAdvancedController interface {
	DiscoverHunks(context.Context, string, gitadvanced.Spec) (
		application.GitAdvancedReviewResult, error)
	Review(context.Context, application.GitAdvancedReviewRequest) (
		application.GitAdvancedReviewResult, error)
	Execute(context.Context, application.GitAdvancedExecuteRequest) (
		application.GitAdvancedExecuteResult, error)
	Projection(context.Context, string, int) (application.GitAdvancedProjection, error)
}

type gitAdvancedDiscoverView struct {
	Spec gitadvanced.Spec `json:"spec"`
}

type gitAdvancedReviewView struct {
	OperationKey string                       `json:"operation_key"`
	Scope        application.GitAdvancedScope `json:"scope"`
	Spec         gitadvanced.Spec             `json:"spec"`
}

type gitAdvancedExecuteView struct {
	OperationID string                       `json:"operation_id"`
	ApprovalID  string                       `json:"approval_id"`
	Scope       application.GitAdvancedScope `json:"scope"`
}

func matchGitAdvancedPath(value string) (string, string, bool) {
	segments := strings.Split(strings.TrimPrefix(value, "/api/v1/"), "/")
	if len(segments) == 3 && segments[0] == "runs" && segments[1] != "" &&
		segments[2] == "git-advanced" {
		return segments[1], "", true
	}
	if len(segments) == 4 && segments[0] == "runs" && segments[1] != "" &&
		segments[2] == "git-advanced" {
		switch segments[3] {
		case "discover-hunks", "review", "execute":
			return segments[1], segments[3], true
		}
	}
	return "", "", false
}

func (a *API) serveGitAdvanced(writer http.ResponseWriter, request *http.Request,
	requestID, runID, action string,
) {
	if a.gitAdvancedController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if action == "" {
		a.serveGitAdvancedProjection(writer, request, requestID, runID)
		return
	}
	if !a.gitAdvancedControlEnabled {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	authorization := a.tokenHash
	realm := `Bearer realm="CyberAgent API"`
	if action != "discover-hunks" {
		authorization = a.controlTokenHash
		realm = `Bearer realm="CyberAgent Control API"`
	}
	if !a.authorized(request, authorization) {
		writer.Header().Set("WWW-Authenticate", realm)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Git advanced action only supports POST"), http.StatusMethodNotAllowed)
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
	body, err := readBoundedRequestBody(request, MaxGitAdvancedRequestBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Git advanced"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	decode := func(destination any) error {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(destination); err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument,
				"Git advanced body must be one closed JSON object", err)
		}
		return ensureJSONEOF(decoder)
	}
	switch action {
	case "discover-hunks":
		var view gitAdvancedDiscoverView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.gitAdvancedController.DiscoverHunks(request.Context(), runID,
			view.Spec)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "review":
		var view gitAdvancedReviewView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.gitAdvancedController.Review(request.Context(),
			application.GitAdvancedReviewRequest{
				ProtocolVersion: application.GitAdvancedAPIProtocolVersion,
				RunID:           runID, OperationKey: view.OperationKey,
				RequestedBy: "api_operator", Scope: view.Scope, Spec: view.Spec})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		status := http.StatusOK
		if value.Operation != nil && !value.Replayed {
			status = http.StatusCreated
		}
		a.writeSuccessStatus(writer, requestID, value, nil, status)
	case "execute":
		var view gitAdvancedExecuteView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.gitAdvancedController.Execute(request.Context(),
			application.GitAdvancedExecuteRequest{
				ProtocolVersion: application.GitAdvancedAPIProtocolVersion,
				RunID:           runID, OperationID: view.OperationID, ApprovalID: view.ApprovalID,
				RequestedBy: "api_operator", Scope: view.Scope})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	}
}

func (a *API) serveGitAdvancedProjection(writer http.ResponseWriter,
	request *http.Request, requestID, runID string,
) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Git advanced projection only supports GET"), http.StatusMethodNotAllowed)
		return
	}
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"read-only HTTP API requests cannot contain a body"), 0)
		return
	}
	if err := validateSingleQueryValues(request.URL.Query(), "limit"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"Git advanced projection limit must be an integer"), 0)
			return
		}
		limit = parsed
	}
	value, err := a.gitAdvancedController.Projection(request.Context(), runID, limit)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, value, nil)
}
