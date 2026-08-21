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
	"cyberagent-workbench/internal/githubreview"
)

const MaxGitHubReviewRequestBodyBytes = 128 * 1024

type GitHubReviewController interface {
	Configure(context.Context, application.GitHubReviewConfigureRequest) (
		application.GitHubReviewConfigureResult, error)
	ListConnections(context.Context, bool) ([]application.GitHubReviewCredentialView, error)
	CredentialStatus(context.Context, string) (application.GitHubReviewCredentialView, error)
	BeginDeviceFlow(context.Context, string) (githubreview.DeviceAuthorization, error)
	PollDeviceFlow(context.Context, string, string) (githubreview.DevicePollResult, error)
	Disconnect(context.Context, string) (application.GitHubReviewCredentialView, error)
	Qualify(context.Context, string, int64) (application.GitHubReviewQualificationResult, error)
	Fetch(context.Context, application.GitHubReviewFetchRequest) (
		application.GitHubReviewFetchResult, error)
	BuildEvidence(context.Context, application.GitHubReviewEvidenceRequest) (
		application.GitHubReviewEvidenceResult, error)
	ReviewWrite(context.Context, application.GitHubReviewWriteReviewRequest) (
		application.GitHubReviewWriteReviewResult, error)
	ExecuteWrite(context.Context, application.GitHubReviewWriteExecuteRequest) (
		application.GitHubReviewWriteExecuteResult, error)
	Projection(context.Context, string, string, int64, int) (
		application.GitHubReviewProjection, error)
}

type githubReviewRoute struct {
	kind, connectionID, runID, action string
}

type githubReviewEmptyView struct{}

type githubReviewConfigureView struct {
	ConnectionID       string                           `json:"connection_id,omitempty"`
	Repository         githubreview.RepositoryIdentity  `json:"repository"`
	Credential         githubreview.CredentialReference `json:"credential"`
	ClientID           string                           `json:"client_id,omitempty"`
	AllowedLogHosts    []string                         `json:"allowed_log_hosts"`
	WriteEnabled       bool                             `json:"write_enabled"`
	Enabled            bool                             `json:"enabled"`
	ExpectedGeneration int64                            `json:"expected_generation"`
}

type githubReviewDevicePollView struct {
	SessionID string `json:"session_id"`
}

type githubReviewPullRequestView struct {
	PullRequest int64 `json:"pull_request"`
}

type githubReviewEvidenceView struct {
	SnapshotID string `json:"snapshot_id"`
}

type githubReviewWriteReviewView struct {
	ConnectionID string                 `json:"connection_id"`
	SnapshotID   string                 `json:"snapshot_id"`
	OperationKey string                 `json:"operation_key"`
	Spec         githubreview.WriteSpec `json:"spec"`
}

type githubReviewWriteExecuteView struct {
	OperationID string `json:"operation_id"`
	ApprovalID  string `json:"approval_id"`
}

func matchGitHubReviewPath(value string) (githubReviewRoute, bool) {
	segments := strings.Split(strings.TrimPrefix(value, "/api/v1/"), "/")
	if len(segments) == 2 && segments[0] == "github-review" &&
		segments[1] == "connections" {
		return githubReviewRoute{kind: "connections"}, true
	}
	if len(segments) == 3 && segments[0] == "github-review" &&
		segments[1] == "connections" && segments[2] != "" {
		return githubReviewRoute{kind: "connection", connectionID: segments[2]}, true
	}
	if len(segments) == 4 && segments[0] == "github-review" &&
		segments[1] == "connections" && segments[2] != "" {
		switch segments[3] {
		case "device", "device-poll", "disconnect", "qualify", "fetch":
			return githubReviewRoute{kind: "connection", connectionID: segments[2],
				action: segments[3]}, true
		}
	}
	if len(segments) == 3 && segments[0] == "runs" && segments[1] != "" &&
		segments[2] == "github-review" {
		return githubReviewRoute{kind: "run", runID: segments[1]}, true
	}
	if len(segments) == 4 && segments[0] == "runs" && segments[1] != "" &&
		segments[2] == "github-review" {
		switch segments[3] {
		case "evidence", "review", "execute":
			return githubReviewRoute{kind: "run", runID: segments[1], action: segments[3]}, true
		}
	}
	return githubReviewRoute{}, false
}

func (a *API) serveGitHubReview(writer http.ResponseWriter, request *http.Request,
	requestID string, route githubReviewRoute,
) {
	if a.githubReviewController == nil || !a.githubReviewControlEnabled {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if route.connectionID != "" {
		if err := validatePathIdentity(route.connectionID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	if route.runID != "" {
		if err := validatePathIdentity(route.runID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	readOnly := request.Method == http.MethodGet && route.action == ""
	token := a.controlTokenHash
	realm := `Bearer realm="CyberAgent Control API"`
	if readOnly {
		token = a.tokenHash
		realm = `Bearer realm="CyberAgent API"`
	}
	if !a.authorized(request, token) {
		writer.Header().Set("WWW-Authenticate", realm)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if readOnly {
		a.serveGitHubReviewRead(writer, request, requestID, route)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review control only supports POST"), http.StatusMethodNotAllowed)
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
	body, err := readBoundedRequestBody(request, MaxGitHubReviewRequestBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "GitHub review"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	decode := func(target any) error {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument,
				"GitHub review body must be one closed JSON object", err)
		}
		return ensureJSONEOF(decoder)
	}
	switch {
	case route.kind == "connections" && route.action == "":
		var view githubReviewConfigureView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.Configure(request.Context(),
			application.GitHubReviewConfigureRequest{
				ProtocolVersion: application.GitHubReviewAPIProtocolVersion,
				ConnectionID:    view.ConnectionID, Repository: view.Repository,
				Credential: view.Credential, ClientID: view.ClientID,
				AllowedLogHosts: view.AllowedLogHosts, WriteEnabled: view.WriteEnabled,
				Enabled:            view.Enabled,
				ExpectedGeneration: view.ExpectedGeneration, RequestedBy: "api_operator"})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		status := http.StatusOK
		if view.ExpectedGeneration == 0 && !value.Replayed {
			status = http.StatusCreated
		}
		a.writeSuccessStatus(writer, requestID, value, nil, status)
	case route.kind == "connection" && route.action == "device":
		if err := decode(&struct{}{}); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.BeginDeviceFlow(request.Context(),
			route.connectionID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, value, nil, http.StatusCreated)
	case route.kind == "connection" && route.action == "device-poll":
		var view githubReviewDevicePollView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.PollDeviceFlow(request.Context(),
			route.connectionID, view.SessionID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case route.kind == "connection" && route.action == "disconnect":
		if err := decode(&struct{}{}); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.Disconnect(request.Context(),
			route.connectionID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case route.kind == "connection" && route.action == "qualify":
		var view githubReviewPullRequestView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.Qualify(request.Context(),
			route.connectionID, view.PullRequest)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case route.kind == "connection" && route.action == "fetch":
		var view githubReviewPullRequestView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.Fetch(request.Context(),
			application.GitHubReviewFetchRequest{
				ProtocolVersion: application.GitHubReviewAPIProtocolVersion,
				ConnectionID:    route.connectionID, PullRequest: view.PullRequest})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, value, nil, http.StatusCreated)
	case route.kind == "run" && route.action == "evidence":
		var view githubReviewEvidenceView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.BuildEvidence(request.Context(),
			application.GitHubReviewEvidenceRequest{
				ProtocolVersion: application.GitHubReviewAPIProtocolVersion,
				RunID:           route.runID, SnapshotID: view.SnapshotID})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, value, nil, http.StatusCreated)
	case route.kind == "run" && route.action == "review":
		var view githubReviewWriteReviewView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.ReviewWrite(request.Context(),
			application.GitHubReviewWriteReviewRequest{
				ProtocolVersion: application.GitHubReviewAPIProtocolVersion,
				RunID:           route.runID, ConnectionID: view.ConnectionID,
				SnapshotID: view.SnapshotID, OperationKey: view.OperationKey,
				RequestedBy: "api_operator", Spec: view.Spec})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		status := http.StatusOK
		if !value.Replayed {
			status = http.StatusCreated
		}
		a.writeSuccessStatus(writer, requestID, value, nil, status)
	case route.kind == "run" && route.action == "execute":
		var view githubReviewWriteExecuteView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.ExecuteWrite(request.Context(),
			application.GitHubReviewWriteExecuteRequest{
				ProtocolVersion: application.GitHubReviewAPIProtocolVersion,
				RunID:           route.runID, OperationID: view.OperationID,
				ApprovalID: view.ApprovalID, RequestedBy: "api_operator"})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	default:
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
	}
}

func (a *API) serveGitHubReviewRead(writer http.ResponseWriter,
	request *http.Request, requestID string, route githubReviewRoute,
) {
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"read-only HTTP API requests cannot contain a body"), 0)
		return
	}
	switch route.kind {
	case "connections":
		if err := validateSingleQueryValues(request.URL.Query(), "enabled_only"); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		enabledOnly := false
		if raw := request.URL.Query().Get("enabled_only"); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil || (raw != "true" && raw != "false") {
				a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
					"enabled_only must be true or false"), 0)
				return
			}
			enabledOnly = parsed
		}
		value, err := a.githubReviewController.ListConnections(request.Context(), enabledOnly)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "connection":
		if err := rejectQuery(request.URL.Query()); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.githubReviewController.CredentialStatus(request.Context(),
			route.connectionID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "run":
		if err := validateSingleQueryValues(request.URL.Query(), "connection_id",
			"pull_request", "limit"); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		connectionID := request.URL.Query().Get("connection_id")
		pullRequest, parseErr := parseGitHubReviewInt64(request.URL.Query().Get("pull_request"), 0)
		if parseErr != nil {
			a.writeError(writer, requestID, parseErr, 0)
			return
		}
		limitValue, parseErr := parseGitHubReviewInt64(request.URL.Query().Get("limit"), 100)
		if parseErr != nil {
			a.writeError(writer, requestID, parseErr, 0)
			return
		}
		value, err := a.githubReviewController.Projection(request.Context(), route.runID,
			connectionID, pullRequest, int(limitValue))
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	default:
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
	}
}

func parseGitHubReviewInt64(value string, fallback int64) (int64, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review numeric query value is invalid")
	}
	return parsed, nil
}
