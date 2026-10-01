package httpapi

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/modelregistry"
	"net/http"
)

const ProviderModelDiscoveryPath = "/api/v1/models/model-discovery"

type providerModelDiscoverer interface {
	DiscoverModels(context.Context, application.ProviderModelDiscoveryRequest) (modelregistry.ModelDiscoveryResult, error)
}

func (a *API) serveProviderModelDiscovery(w http.ResponseWriter, r *http.Request, requestID string) {
	const label = "Provider model discovery"
	if !a.authorizeRunOperation(w, r, requestID, a.providerDefinitionEnabled, label) {
		return
	}
	c, ok := a.providerDefinitionController.(providerModelDiscoverer)
	if !ok {
		a.writeError(w, requestID, apperror.New(apperror.CodeNotFound, "Provider model discovery is unavailable"), 0)
		return
	}
	body, err := readProviderDefinitionControlBody(r, label)
	if err != nil {
		a.writeError(w, requestID, err, runOperationErrorStatus(err))
		return
	}
	var request application.ProviderModelDiscoveryRequest
	if err := decodeStrictRunOperation(body, &request, label); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	result, err := c.DiscoverModels(r.Context(), request)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(w, requestID, result, nil, http.StatusOK)
}
