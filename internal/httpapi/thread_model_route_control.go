package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const (
	AvailableModelRoutesPath     = "/api/v1/models/routes/available"
	ThreadModelRoutePathTemplate = "/api/v1/threads/{thread_id}/model-route"
	maxThreadModelRouteBodyBytes = 8 * 1024
)

type ThreadModelRouteController interface {
	Catalog(context.Context) (application.ModelRouteCatalog, error)
	Get(context.Context, string) (application.ThreadModelRouteView, error)
	Change(context.Context, application.ChangeThreadModelRouteRequest) (
		application.ThreadModelRouteView, error)
}

type AvailableModelRouteView struct {
	ProviderID          string   `json:"provider_id"`
	ProviderName        string   `json:"provider_name"`
	Model               string   `json:"model"`
	Enabled             bool     `json:"enabled"`
	CredentialStatus    string   `json:"credential_status"`
	QualificationStatus string   `json:"qualification_status"`
	HarnessReady        bool     `json:"harness_ready"`
	Selectable          bool     `json:"selectable"`
	UnavailableReason   string   `json:"unavailable_reason"`
	DefaultForRoutes    []string `json:"default_for_routes"`
}

type AvailableModelRouteCollectionView struct {
	ProtocolVersion string                    `json:"protocol_version"`
	Generation      uint64                    `json:"generation"`
	Routes          []AvailableModelRouteView `json:"routes"`
}

type ThreadModelRouteView struct {
	ProtocolVersion    string `json:"protocol_version"`
	ThreadID           string `json:"thread_id"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	Source             string `json:"source"`
	EffectiveRunID     string `json:"effective_run_id,omitempty"`
	AppliesTo          string `json:"applies_to"`
	ActiveRunUnchanged bool   `json:"active_run_unchanged"`
	Replayed           bool   `json:"replayed"`
}

type ThreadModelRouteControlRequestView struct {
	Version      string                        `json:"version"`
	Action       domain.ThreadModelRouteAction `json:"action"`
	Provider     string                        `json:"provider,omitempty"`
	Model        string                        `json:"model,omitempty"`
	OperationKey string                        `json:"operation_key"`
	RequestedBy  string                        `json:"requested_by"`
}

func matchThreadModelRoutePath(requestPath string) (string, bool) {
	const prefix = "/api/v1/threads/"
	const suffix = "/model-route"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	threadID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if threadID == "" || strings.Contains(threadID, "/") {
		return "", false
	}
	return threadID, true
}

func (a *API) modelRouteCatalog(request *http.Request) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	if a.threadModelRouteController == nil {
		return nil, nil, apperror.New(apperror.CodeNotFound,
			"HTTP API endpoint was not found")
	}
	catalog, err := a.threadModelRouteController.Catalog(request.Context())
	if err != nil {
		return nil, nil, err
	}
	return availableModelRouteCollectionView(catalog), nil, nil
}

func (a *API) serveThreadModelRoute(writer http.ResponseWriter, request *http.Request,
	requestID string, threadID string,
) {
	if a.threadModelRouteController == nil {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
			"HTTP API endpoint was not found"), http.StatusNotFound)
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
	switch request.Method {
	case http.MethodGet:
		if !a.authorized(request, a.tokenHash) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
				"valid bearer authorization is required"), http.StatusUnauthorized)
			return
		}
		view, err := a.threadModelRouteController.Get(request.Context(), threadID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, threadModelRouteView(view), nil)
	case http.MethodPut:
		if !a.modelControlEnabled || !a.authorized(request, a.controlTokenHash) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
				"valid model control bearer authorization is required"),
				http.StatusUnauthorized)
			return
		}
		if err := validateJSONContentType(request.Header); err != nil {
			a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
			return
		}
		body, err := readBoundedRequestBody(request, maxThreadModelRouteBodyBytes)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		var view ThreadModelRouteControlRequestView
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&view); err != nil {
			a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
				"Thread model route body must be one JSON object", err), 0)
			return
		}
		if err := ensureJSONEOF(decoder); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		result, err := a.threadModelRouteController.Change(request.Context(),
			application.ChangeThreadModelRouteRequest{Version: view.Version,
				ThreadID: threadID, Action: view.Action, Provider: view.Provider,
				Model: view.Model, OperationKey: view.OperationKey,
				RequestedBy: view.RequestedBy})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, threadModelRouteView(result), nil)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Thread model route endpoint only supports GET and PUT"),
			http.StatusMethodNotAllowed)
	}
}

func availableModelRouteCollectionView(catalog application.ModelRouteCatalog) AvailableModelRouteCollectionView {
	routes := make([]AvailableModelRouteView, len(catalog.Routes))
	for index, route := range catalog.Routes {
		routes[index] = AvailableModelRouteView{ProviderID: route.ProviderID,
			ProviderName: route.ProviderName, Model: route.Model, Enabled: route.Enabled,
			CredentialStatus:    route.CredentialStatus,
			QualificationStatus: route.QualificationStatus,
			HarnessReady:        route.HarnessReady, Selectable: route.Selectable,
			UnavailableReason: route.UnavailableReason,
			DefaultForRoutes:  append([]string{}, route.DefaultForRoutes...)}
	}
	return AvailableModelRouteCollectionView{ProtocolVersion: catalog.ProtocolVersion,
		Generation: catalog.Generation, Routes: routes}
}

func threadModelRouteView(value application.ThreadModelRouteView) ThreadModelRouteView {
	return ThreadModelRouteView{ProtocolVersion: value.ProtocolVersion,
		ThreadID: value.ThreadID, Provider: value.Provider, Model: value.Model,
		Source: value.Source, EffectiveRunID: value.EffectiveRunID,
		AppliesTo: value.AppliesTo, ActiveRunUnchanged: value.ActiveRunUnchanged,
		Replayed: value.Replayed}
}
