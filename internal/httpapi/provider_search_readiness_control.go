package httpapi

import (
	"context"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const ProviderSearchReadinessPathTemplate = "/api/v1/threads/{thread_id}/search-readiness"

type ProviderSearchReadinessController interface {
	Get(context.Context, string) (application.ProviderSearchReadiness, error)
}

type ProviderSearchReadinessView struct {
	ProtocolVersion string `json:"protocol_version"`
	ThreadID        string `json:"thread_id,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	ModelRoute      string `json:"model_route,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	SearchPolicy    string `json:"search_policy,omitempty"`
	State           string `json:"state"`
	Reason          string `json:"reason"`
	Remediation     string `json:"remediation"`
	DetailCode      string `json:"detail_code,omitempty"`
	RequiredTarget  string `json:"required_target,omitempty"`
	NetworkMode     string `json:"network_mode"`
	ModeRevision    int64  `json:"mode_revision,omitempty"`
	RuntimeReady    bool   `json:"runtime_ready"`
	CapabilityGrant bool   `json:"capability_grant"`
}

func matchProviderSearchReadinessPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/threads/"
	const suffix = "/search-readiness"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	threadID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if threadID == "" || strings.Contains(threadID, "/") {
		return "", false
	}
	return threadID, true
}

func (a *API) serveProviderSearchReadiness(writer http.ResponseWriter,
	request *http.Request, requestID, threadID string,
) {
	if a.providerSearchReadinessController == nil {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
			"HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Provider search readiness endpoint only supports GET"),
			http.StatusMethodNotAllowed)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Provider search readiness request cannot contain a body"), 0)
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	value, err := a.providerSearchReadinessController.Get(request.Context(), threadID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, providerSearchReadinessView(value), nil)
}

func providerSearchReadinessView(value application.ProviderSearchReadiness) ProviderSearchReadinessView {
	return ProviderSearchReadinessView{ProtocolVersion: value.ProtocolVersion,
		ThreadID: value.ThreadID, RunID: value.RunID, ModelRoute: value.ModelRoute,
		Provider: value.Provider, Model: value.Model, SearchPolicy: value.SearchPolicy,
		State: value.State, Reason: value.Reason, Remediation: value.Remediation,
		DetailCode: value.DetailCode, RequiredTarget: value.RequiredTarget,
		NetworkMode: value.NetworkMode, ModeRevision: value.ModeRevision,
		RuntimeReady: value.RuntimeReady, CapabilityGrant: value.CapabilityGrant}
}
