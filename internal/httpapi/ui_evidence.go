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
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/uievidence"
)

const MaxUIEvidenceRequestBodyBytes = 512 * 1024

type UIEvidenceController interface {
	Start(context.Context, application.UIEvidenceStartRequest) (uievidence.Attempt, error)
	Cancel(context.Context, string) (uievidence.Attempt, error)
	Get(context.Context, string) (application.UIEvidenceBundle, error)
	List(context.Context, uievidence.ListFilter) ([]uievidence.Attempt, error)
	Artifact(context.Context, string, string) (uievidence.Artifact, error)
}

type uiEvidenceStartView struct {
	OperationKey  string                                 `json:"operation_key"`
	Build         *runner.CommandRuntimeSpec             `json:"build,omitempty"`
	Start         runner.CommandRuntimeSpec              `json:"start"`
	Readiness     uievidence.Readiness                   `json:"readiness"`
	URL           string                                 `json:"url"`
	Route         string                                 `json:"route"`
	Browser       application.UIEvidenceBrowserSelection `json:"browser"`
	Environment   uievidence.Environment                 `json:"environment"`
	Fixture       uievidence.Fixture                     `json:"fixture"`
	Steps         []application.UIEvidenceRuntimeStep    `json:"steps"`
	Capture       uievidence.CapturePolicy               `json:"capture"`
	FailurePolicy uievidence.FailurePolicy               `json:"failure_policy"`
}

type uiEvidenceCancelView struct {
	Confirm *bool `json:"confirm"`
}

type uiEvidenceRoute struct {
	runID      string
	attemptID  string
	artifactID string
	action     string
}

func matchUIEvidencePath(value string) (uiEvidenceRoute, bool) {
	segments := strings.Split(strings.TrimPrefix(value, "/api/v1/"), "/")
	if len(segments) == 3 && segments[0] == "runs" && segments[1] != "" &&
		segments[2] == "ui-evidence" {
		return uiEvidenceRoute{runID: segments[1], action: "collection"}, true
	}
	if len(segments) == 2 && segments[0] == "ui-evidence" && segments[1] != "" {
		return uiEvidenceRoute{attemptID: segments[1], action: "attempt"}, true
	}
	if len(segments) == 3 && segments[0] == "ui-evidence" && segments[1] != "" &&
		segments[2] == "cancel" {
		return uiEvidenceRoute{attemptID: segments[1], action: "cancel"}, true
	}
	if len(segments) == 4 && segments[0] == "ui-evidence" && segments[1] != "" &&
		segments[2] == "artifacts" && segments[3] != "" {
		return uiEvidenceRoute{attemptID: segments[1], artifactID: segments[3],
			action: "artifact"}, true
	}
	return uiEvidenceRoute{}, false
}

func (a *API) serveUIEvidence(writer http.ResponseWriter, request *http.Request,
	requestID string, route uiEvidenceRoute,
) {
	if a.uiEvidenceController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if route.runID != "" {
		if err := validatePathIdentity(route.runID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	if route.attemptID != "" {
		if err := validatePathIdentity(route.attemptID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	if route.artifactID != "" {
		if err := validatePathIdentity(route.artifactID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	if (route.action == "collection" && request.Method == http.MethodPost) ||
		route.action == "cancel" {
		a.serveUIEvidenceMutation(writer, request, requestID, route)
		return
	}
	a.serveUIEvidenceRead(writer, request, requestID, route)
}

func (a *API) serveUIEvidenceRead(writer http.ResponseWriter, request *http.Request,
	requestID string, route uiEvidenceRoute,
) {
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence read only supports GET"), http.StatusMethodNotAllowed)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"read-only HTTP API requests cannot contain a body"), 0)
		return
	}
	switch route.action {
	case "collection":
		a.serveUIEvidenceList(writer, request, requestID, route.runID)
	case "attempt":
		if err := rejectQuery(request.URL.Query()); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.uiEvidenceController.Get(request.Context(), route.attemptID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "artifact":
		if err := rejectQuery(request.URL.Query()); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.uiEvidenceController.Artifact(request.Context(),
			route.attemptID, route.artifactID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err := value.Validate(); err != nil ||
			value.Metadata.AttemptID != route.attemptID || value.Metadata.ID != route.artifactID {
			a.writeError(writer, requestID, apperror.Wrap(apperror.CodeUnavailable,
				"UI evidence artifact failed integrity verification", err), 0)
			return
		}
		writeUIEvidenceArtifact(writer, value)
	default:
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
	}
}

func (a *API) serveUIEvidenceList(writer http.ResponseWriter, request *http.Request,
	requestID, runID string,
) {
	if err := validateSingleQueryValues(request.URL.Query(), "status", "limit"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	filter := uievidence.ListFilter{RunID: runID, Limit: 100}
	if raw := request.URL.Query().Get("status"); raw != "" {
		filter.Status = uievidence.Status(raw)
	}
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"UI evidence limit must be an integer"), 0)
			return
		}
		filter.Limit = parsed
	}
	if err := filter.Validate(); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"UI evidence list filter is invalid", err), 0)
		return
	}
	values, err := a.uiEvidenceController.List(request.Context(), filter)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if values == nil {
		values = []uievidence.Attempt{}
	}
	a.writeSuccess(writer, requestID, values, nil)
}

func (a *API) serveUIEvidenceMutation(writer http.ResponseWriter, request *http.Request,
	requestID string, route uiEvidenceRoute,
) {
	if !a.uiEvidenceControlEnabled {
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
			"UI evidence mutation only supports POST"), http.StatusMethodNotAllowed)
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
	body, err := readBoundedRequestBody(request, MaxUIEvidenceRequestBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "UI evidence"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	decode := func(destination any) error {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(destination); err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument,
				"UI evidence body must be one JSON object", err)
		}
		return ensureJSONEOF(decoder)
	}
	if route.action == "collection" {
		var view uiEvidenceStartView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.uiEvidenceController.Start(request.Context(),
			application.UIEvidenceStartRequest{RunID: route.runID,
				OperationKey: view.OperationKey, Build: view.Build, Start: view.Start,
				Readiness: view.Readiness, URL: view.URL, Route: view.Route,
				Browser: view.Browser, Environment: view.Environment, Fixture: view.Fixture,
				Steps: view.Steps, Capture: view.Capture, FailurePolicy: view.FailurePolicy})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, value, nil, http.StatusAccepted)
		return
	}
	var view uiEvidenceCancelView
	if err := decode(&view); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Confirm == nil || !*view.Confirm {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence cancellation requires confirm=true"), 0)
		return
	}
	value, err := a.uiEvidenceController.Cancel(request.Context(), route.attemptID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, value, nil)
}

func writeUIEvidenceArtifact(writer http.ResponseWriter, artifact uievidence.Artifact) {
	metadata := artifact.Metadata
	writer.Header().Set("Content-Type", metadata.MIME)
	writer.Header().Set("Content-Length", strconv.FormatInt(metadata.Bytes, 10))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("ETag", `"`+metadata.SHA256+`"`)
	writer.Header().Set("X-CyberAgent-Content-SHA256", metadata.SHA256)
	writer.Header().Set("X-CyberAgent-Evidence-Untrusted", "true")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(artifact.Content)
}
