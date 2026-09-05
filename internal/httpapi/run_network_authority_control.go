package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const (
	RunNetworkAuthorityControlPathTemplate = "/api/v1/runs/{run_id}/network-authority"
	MaxRunNetworkAuthorityControlBodyBytes = 160 * 1024
)

type RunNetworkAuthorityControlRequestView struct {
	Version              string   `json:"version"`
	ExpectedModeRevision int64    `json:"expected_mode_revision"`
	AddAllowedTargets    []string `json:"add_allowed_targets"`
	Reason               string   `json:"reason,omitempty"`
}

type RunNetworkAuthorityControlView struct {
	Version         string      `json:"version"`
	RunID           string      `json:"run_id"`
	Mode            RunModeView `json:"mode"`
	AddedTargets    []string    `json:"added_targets"`
	Replayed        bool        `json:"replayed"`
	CapabilityGrant bool        `json:"capability_grant"`
}

func matchRunNetworkAuthorityControlPath(requestPath string) (string, bool) {
	return matchRunOperationControlPath(requestPath, "/network-authority")
}

func (a *API) serveRunNetworkAuthorityControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.authorizeRunOperation(writer, request, requestID,
		a.controlEnabled, "Run network authority") {
		return
	}
	if err := validatePathIdentity(runID); err != nil {
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
	operationKey, err := sessionControlIdempotencyKey(request.Header,
		"Run network authority")
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	body, err := readBoundedRequestBody(request, MaxRunNetworkAuthorityControlBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	if !utf8.Valid(body) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Run network authority body must be valid UTF-8 JSON"), 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Run network authority"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view RunNetworkAuthorityControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Run network authority body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	service := application.NewRunNetworkAuthorityService(a.store).
		WithRuntimeAuthority(a.executionPermissionCapabilities.RuntimeAuthority)
	result, err := service.Expand(request.Context(),
		application.ExpandRunNetworkAuthorityRequest{
			Version: view.Version, RunID: strings.TrimSpace(runID),
			ExpectedModeRevision: view.ExpectedModeRevision,
			AddAllowedTargets:    append([]string(nil), view.AddAllowedTargets...),
			OperationKey:         operationKey, RequestedBy: "http_control", Reason: view.Reason,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, RunNetworkAuthorityControlView{
		Version: application.RunNetworkAuthorityControlProtocolVersion,
		RunID:   result.Mode.RunID, Mode: runModeView(result.Mode),
		AddedTargets: append([]string(nil), result.AddedTargets...),
		Replayed:     result.Replayed, CapabilityGrant: true,
	}, nil, http.StatusAccepted)
}
