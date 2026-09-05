package httpapi

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/modelregistry"
)

const (
	ProviderDefinitionsPath               = "/api/v1/models/provider-definitions"
	ProviderDefinitionPathTemplate        = "/api/v1/models/provider-definitions/{provider}"
	ProviderDefinitionDeletePathTemplate  = "/api/v1/models/provider-definitions/{provider}/delete"
	maxProviderDefinitionControlBodyBytes = 128 * 1024
)

type ProviderDefinitionController interface {
	List(context.Context) (modelregistry.ProviderDefinitionCollection, error)
	Upsert(context.Context, application.ProviderDefinitionUpsertRequest) (
		application.ProviderDefinitionMutationResult, error)
	Delete(context.Context, application.ProviderDefinitionDeleteRequest) (
		application.ProviderDefinitionMutationResult, error)
}

type ProviderDefinitionCollectionView struct {
	Version   string                             `json:"version"`
	Revision  uint64                             `json:"revision"`
	Providers []modelregistry.ProviderDefinition `json:"providers"`
}

type ProviderDefinitionUpsertRequestView struct {
	Version                    string                           `json:"version"`
	ExpectedCollectionRevision uint64                           `json:"expected_collection_revision"`
	Definition                 modelregistry.ProviderDefinition `json:"definition"`
	Confirm                    bool                             `json:"confirm"`
}

type ProviderDefinitionDeleteRequestView struct {
	Version                    string `json:"version"`
	ExpectedCollectionRevision uint64 `json:"expected_collection_revision"`
	ExpectedDefinitionRevision uint64 `json:"expected_definition_revision"`
	Confirm                    bool   `json:"confirm"`
}

type ProviderDefinitionMutationView struct {
	ProtocolVersion    string                            `json:"protocol_version"`
	Collection         ProviderDefinitionCollectionView  `json:"collection"`
	Definition         *modelregistry.ProviderDefinition `json:"definition,omitempty"`
	DeletedID          string                            `json:"deleted_id,omitempty"`
	RegistryReloaded   bool                              `json:"registry_reloaded"`
	RegistryGeneration uint64                            `json:"registry_generation"`
}

func matchProviderDefinitionControlPath(requestPath string) (provider string,
	remove bool, matched bool,
) {
	const prefix = "/api/v1/models/provider-definitions/"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", false, false
	}
	segments := strings.Split(strings.TrimPrefix(requestPath, prefix), "/")
	if len(segments) == 1 && segments[0] != "" {
		return segments[0], false, true
	}
	if len(segments) == 2 && segments[0] != "" && segments[1] == "delete" {
		return segments[0], true, true
	}
	return "", false, false
}

func (a *API) providerDefinitions(request *http.Request) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	if !a.providerDefinitionEnabled || a.providerDefinitionController == nil {
		return nil, nil, apperror.New(apperror.CodeNotFound,
			"custom Provider definitions are unavailable")
	}
	collection, err := a.providerDefinitionController.List(request.Context())
	if err != nil {
		return nil, nil, err
	}
	return providerDefinitionCollectionView(collection), nil, nil
}

func (a *API) serveProviderDefinitionControl(writer http.ResponseWriter,
	request *http.Request, requestID string, provider string, remove bool,
) {
	const label = "Custom Provider definition control"
	if !a.authorizeRunOperation(writer, request, requestID,
		a.providerDefinitionEnabled, label) {
		return
	}
	if err := validatePathIdentity(provider); err != nil ||
		!modelregistry.ValidCustomProviderID(provider) {
		if err == nil {
			err = apperror.New(apperror.CodeInvalidArgument,
				"custom Provider identity is invalid or reserved")
		}
		a.writeError(writer, requestID, err, 0)
		return
	}
	body, err := readProviderDefinitionControlBody(request, label)
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	if remove {
		var view ProviderDefinitionDeleteRequestView
		if err := decodeStrictRunOperation(body, &view, label); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		result, err := a.providerDefinitionController.Delete(request.Context(),
			application.ProviderDefinitionDeleteRequest{Version: view.Version, ID: provider,
				ExpectedCollectionRevision: view.ExpectedCollectionRevision,
				ExpectedDefinitionRevision: view.ExpectedDefinitionRevision,
				Confirm:                    view.Confirm})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, providerDefinitionMutationView(result), nil,
			http.StatusAccepted)
		return
	}
	var view ProviderDefinitionUpsertRequestView
	if err := decodeStrictRunOperation(body, &view, label); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Definition.ID != provider {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"custom Provider path and definition identities must match"), 0)
		return
	}
	result, err := a.providerDefinitionController.Upsert(request.Context(),
		application.ProviderDefinitionUpsertRequest{Version: view.Version,
			ExpectedCollectionRevision: view.ExpectedCollectionRevision,
			Definition:                 view.Definition, Confirm: view.Confirm})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, providerDefinitionMutationView(result), nil,
		http.StatusAccepted)
}

func readProviderDefinitionControlBody(request *http.Request, label string) ([]byte, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, err
	}
	if err := validateJSONContentType(request.Header); err != nil {
		return nil, err
	}
	body, err := readBoundedRequestBody(request, maxProviderDefinitionControlBodyBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(body) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			label+" body must be valid UTF-8 JSON")
	}
	if err := rejectDuplicateJSONObjectFields(body, label); err != nil {
		return nil, err
	}
	return body, nil
}

func providerDefinitionCollectionView(
	collection modelregistry.ProviderDefinitionCollection,
) ProviderDefinitionCollectionView {
	providers := make([]modelregistry.ProviderDefinition, len(collection.Providers))
	copy(providers, collection.Providers)
	for index := range providers {
		providers[index].Models = append([]string(nil), collection.Providers[index].Models...)
		providers[index].AdvancedConfig = append([]byte(nil), collection.Providers[index].AdvancedConfig...)
	}
	return ProviderDefinitionCollectionView{Version: collection.Version,
		Revision: collection.Revision, Providers: providers}
}

func providerDefinitionMutationView(
	result application.ProviderDefinitionMutationResult,
) ProviderDefinitionMutationView {
	view := ProviderDefinitionMutationView{ProtocolVersion: result.ProtocolVersion,
		Collection: providerDefinitionCollectionView(result.Collection),
		DeletedID:  result.DeletedID, RegistryReloaded: result.RegistryReloaded,
		RegistryGeneration: result.RegistryGeneration}
	if result.DeletedID == "" {
		definition := result.Definition
		definition.Models = append([]string(nil), result.Definition.Models...)
		definition.AdvancedConfig = append([]byte(nil), result.Definition.AdvancedConfig...)
		view.Definition = &definition
	}
	return view
}
