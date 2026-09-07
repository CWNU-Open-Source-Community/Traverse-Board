package application

import (
	"context"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/runmutation"
)

const ModelRouteCatalogProtocolVersion = "model_route_catalog.v1"

type ModelRouteCatalogItem struct {
	ProviderID          string
	ProviderName        string
	Model               string
	Enabled             bool
	CredentialStatus    string
	QualificationStatus string
	HarnessReady        bool
	Selectable          bool
	UnavailableReason   string
	DefaultForRoutes    []string
	// DefinitionRevision is intentionally not projected by the HTTP catalog.
	// It is an internal CAS token used by Change to close the Registry-to-Store
	// race for custom Provider definitions.
	Custom             bool
	DefinitionRevision uint64
}

type ModelRouteCatalog struct {
	ProtocolVersion string
	Generation      uint64
	Routes          []ModelRouteCatalogItem
}

type ThreadModelRouteStore interface {
	GetThread(context.Context, string) (domain.Thread, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetRun(context.Context, string) (domain.Run, error)
	GetThreadModelRoutePreference(context.Context, string) (
		domain.ThreadModelRoutePreference, bool, error)
	ChangeThreadModelRoutePreference(context.Context,
		domain.ThreadModelRouteMutation) (domain.ThreadModelRouteMutationResult, error)
}

type ThreadModelRouteRegistry interface {
	Snapshot() modelregistry.Snapshot
	Router() *llm.Router
}

type ThreadModelRouteService struct {
	store    ThreadModelRouteStore
	registry ThreadModelRouteRegistry
}

type ThreadModelRouteView struct {
	ProtocolVersion    string
	ThreadID           string
	Provider           string
	Model              string
	Source             string
	EffectiveRunID     string
	AppliesTo          string
	ActiveRunUnchanged bool
	Replayed           bool
}

type ChangeThreadModelRouteRequest struct {
	Version      string
	ThreadID     string
	Action       domain.ThreadModelRouteAction
	Provider     string
	Model        string
	OperationKey string
	RequestedBy  string

	customProvider                     bool
	expectedProviderDefinitionRevision uint64
}

func NewThreadModelRouteService(store ThreadModelRouteStore,
	registry ThreadModelRouteRegistry,
) *ThreadModelRouteService {
	return &ThreadModelRouteService{store: store, registry: registry}
}

func (s *ThreadModelRouteService) Catalog(_ context.Context) (ModelRouteCatalog, error) {
	if s == nil || s.registry == nil {
		return ModelRouteCatalog{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread model route Registry is required")
	}
	snapshot := s.registry.Snapshot()
	defaults := make(map[string][]string)
	for _, route := range snapshot.Routes {
		key := route.Provider + "\x00" + route.Model
		defaults[key] = append(defaults[key], route.Name)
	}
	items := make([]ModelRouteCatalogItem, 0)
	for _, provider := range snapshot.Providers {
		// The deterministic mock Provider is an internal test fixture, not a
		// billable/configured route that should appear in the product picker.
		if provider.Name == "mock" && provider.Kind == modelregistry.ProviderKindLocal {
			continue
		}
		for _, model := range provider.Models {
			harness, found := catalogHarness(provider.Harnesses, model)
			credentialStatus := catalogCredentialStatus(provider)
			qualificationStatus := catalogQualificationStatus(harness, found)
			harnessReady := found && harness.RootEligible
			selectable := provider.Enabled &&
				provider.Status == modelregistry.ProviderAvailable && harnessReady
			if latest := strings.TrimSpace(harness.LatestQualificationStatus); latest != "" &&
				latest != modelregistry.QualificationStatusAvailable {
				selectable = false
			}
			item := ModelRouteCatalogItem{ProviderID: provider.Name,
				ProviderName: provider.DisplayName, Model: model, Enabled: provider.Enabled,
				CredentialStatus: credentialStatus, QualificationStatus: qualificationStatus,
				HarnessReady: harnessReady, Selectable: selectable,
				DefaultForRoutes: append([]string(nil), defaults[provider.Name+"\x00"+model]...),
				Custom:           provider.Custom, DefinitionRevision: provider.DefinitionRevision,
			}
			if item.ProviderName == "" {
				item.ProviderName = item.ProviderID
			}
			sort.Strings(item.DefaultForRoutes)
			item.UnavailableReason = catalogUnavailableReason(provider, item)
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ProviderName != items[j].ProviderName {
			return items[i].ProviderName < items[j].ProviderName
		}
		if items[i].ProviderID != items[j].ProviderID {
			return items[i].ProviderID < items[j].ProviderID
		}
		return items[i].Model < items[j].Model
	})
	return ModelRouteCatalog{ProtocolVersion: ModelRouteCatalogProtocolVersion,
		Generation: snapshot.Generation, Routes: items}, nil
}

func (s *ThreadModelRouteService) Get(ctx context.Context,
	threadID string,
) (ThreadModelRouteView, error) {
	if s == nil || s.store == nil || s.registry == nil {
		return ThreadModelRouteView{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread model route dependencies are required")
	}
	threadID = strings.TrimSpace(threadID)
	if !domain.ValidAgentID(threadID) {
		return ThreadModelRouteView{}, apperror.New(apperror.CodeInvalidArgument,
			"Thread model route Thread id is invalid")
	}
	threadRecord, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return ThreadModelRouteView{}, apperror.Normalize(err)
	}
	preference, found, err := s.store.GetThreadModelRoutePreference(ctx, threadID)
	if err != nil {
		return ThreadModelRouteView{}, apperror.Normalize(err)
	}
	var desired llm.ModelRef
	source := "default"
	if found && preference.Selected {
		desired = llm.ModelRef{Provider: preference.Provider, Model: preference.Model}
		source = "thread_preference"
	} else if found {
		mission, missionErr := s.store.GetMission(ctx, threadRecord.MissionID)
		if missionErr != nil {
			return ThreadModelRouteView{}, apperror.Normalize(missionErr)
		}
		desired, err = resolveConfiguredModelRef(s.registry.Router(), string(mission.Profile))
		if err != nil {
			return ThreadModelRouteView{}, apperror.Wrap(apperror.CodeFailedPrecondition,
				"Thread default model route is invalid", err)
		}
	} else {
		desired, err = s.inheritedRoute(ctx, threadRecord)
		if err != nil {
			return ThreadModelRouteView{}, err
		}
	}
	return s.routeView(ctx, threadRecord, desired, source)
}

func (s *ThreadModelRouteService) Change(ctx context.Context,
	request ChangeThreadModelRouteRequest,
) (ThreadModelRouteView, error) {
	if s == nil || s.store == nil || s.registry == nil {
		return ThreadModelRouteView{}, apperror.New(apperror.CodeFailedPrecondition,
			"Thread model route dependencies are required")
	}
	if err := normalizeThreadModelRouteRequest(&request); err != nil {
		return ThreadModelRouteView{}, err
	}
	if request.Action == domain.ThreadModelRouteSelect {
		catalog, err := s.Catalog(ctx)
		if err != nil {
			return ThreadModelRouteView{}, err
		}
		selectable := false
		customProvider := false
		var definitionRevision uint64
		for _, route := range catalog.Routes {
			if route.ProviderID == request.Provider && route.Model == request.Model &&
				route.Selectable {
				selectable = true
				customProvider = route.Custom
				definitionRevision = route.DefinitionRevision
				break
			}
		}
		if !selectable {
			return ThreadModelRouteView{}, apperror.New(apperror.CodeFailedPrecondition,
				"selected Provider model route is not currently eligible")
		}
		if customProvider && definitionRevision == 0 {
			return ThreadModelRouteView{}, apperror.New(apperror.CodeFailedPrecondition,
				"selected custom Provider model route has no durable definition revision")
		}
		request.customProvider = customProvider
		request.expectedProviderDefinitionRevision = definitionRevision
	}
	fingerprint := runmutation.Fingerprint("thread_model_route_request.v1",
		request.ThreadID, string(request.Action), request.Provider, request.Model,
		request.RequestedBy)
	result, err := s.store.ChangeThreadModelRoutePreference(ctx,
		domain.ThreadModelRouteMutation{Version: request.Version, ThreadID: request.ThreadID,
			Action: request.Action, Provider: request.Provider, Model: request.Model,
			CustomProvider:                     request.customProvider,
			ExpectedProviderDefinitionRevision: request.expectedProviderDefinitionRevision,
			OperationKey:                       request.OperationKey, RequestFingerprint: fingerprint,
			RequestedBy: request.RequestedBy, At: time.Now().UTC()})
	if err != nil {
		return ThreadModelRouteView{}, apperror.Normalize(err)
	}
	view, err := s.Get(ctx, request.ThreadID)
	if err != nil {
		return ThreadModelRouteView{}, err
	}
	view.Replayed = result.Replayed
	return view, nil
}

func (s *ThreadModelRouteService) inheritedRoute(ctx context.Context,
	threadRecord domain.Thread,
) (llm.ModelRef, error) {
	runID := threadRecord.ActiveRunID
	if runID == "" {
		runID = threadRecord.LastRunID
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return llm.ModelRef{}, apperror.Normalize(err)
	}
	ref, err := resolveConfiguredModelRef(s.registry.Router(), run.Config.ModelRoute)
	if err != nil {
		return llm.ModelRef{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"Thread default model route is invalid", err)
	}
	return ref, nil
}

func (s *ThreadModelRouteService) routeView(ctx context.Context, threadRecord domain.Thread,
	desired llm.ModelRef, source string,
) (ThreadModelRouteView, error) {
	view := ThreadModelRouteView{ProtocolVersion: domain.ThreadModelRouteProtocolVersion,
		ThreadID: threadRecord.ID, Provider: desired.Provider, Model: desired.Model,
		Source: source, AppliesTo: "next_run"}
	if threadRecord.ActiveRunID == "" {
		return view, nil
	}
	active, err := s.store.GetRun(ctx, threadRecord.ActiveRunID)
	if err != nil {
		return ThreadModelRouteView{}, apperror.Normalize(err)
	}
	activeRef, err := resolveConfiguredModelRef(s.registry.Router(), active.Config.ModelRoute)
	if err != nil {
		return ThreadModelRouteView{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"Thread active Run model route is invalid", err)
	}
	if activeRef == desired {
		view.AppliesTo = "current_and_next"
		view.EffectiveRunID = active.ID
		if source == "default" {
			view.Source = "active_run"
		}
		return view, nil
	}
	view.ActiveRunUnchanged = true
	return view, nil
}

func resolveConfiguredModelRef(router *llm.Router, route string) (llm.ModelRef, error) {
	if strings.Contains(route, "/") {
		return llm.ParseModelRef(route)
	}
	if router == nil {
		return llm.ModelRef{}, apperror.New(apperror.CodeFailedPrecondition,
			"model Router is unavailable")
	}
	return router.Resolve(route), nil
}

func normalizeThreadModelRouteRequest(request *ChangeThreadModelRouteRequest) error {
	if request == nil || request.Version != domain.ThreadModelRouteControlProtocolVersion ||
		!domain.ValidAgentID(request.ThreadID) || !domain.ValidAgentID(request.RequestedBy) ||
		request.OperationKey == "" || request.OperationKey != strings.TrimSpace(request.OperationKey) {
		return apperror.New(apperror.CodeInvalidArgument,
			"Thread model route control request is invalid")
	}
	switch request.Action {
	case domain.ThreadModelRouteSelect:
		if request.Provider == "" || request.Model == "" ||
			request.Provider != strings.TrimSpace(request.Provider) ||
			request.Model != strings.TrimSpace(request.Model) ||
			!domain.ValidAgentID(request.Provider) || !domain.ValidAgentID(request.Model) {
			return apperror.New(apperror.CodeInvalidArgument,
				"Thread model route selection is invalid")
		}
	case domain.ThreadModelRouteReset:
		if request.Provider != "" || request.Model != "" {
			return apperror.New(apperror.CodeInvalidArgument,
				"Thread model route reset cannot contain a Provider or model")
		}
	default:
		return apperror.New(apperror.CodeInvalidArgument,
			"Thread model route action is invalid")
	}
	return nil
}

func catalogHarness(values []modelregistry.HarnessAvailability,
	model string,
) (modelregistry.HarnessAvailability, bool) {
	for _, value := range values {
		if value.Model == model {
			return value, true
		}
	}
	return modelregistry.HarnessAvailability{}, false
}

func catalogCredentialStatus(provider modelregistry.ProviderAvailability) string {
	if provider.CredentialSource == "none" || !provider.NetworkRequired {
		return "not_required"
	}
	if !provider.Enabled {
		return "disabled"
	}
	switch provider.Status {
	case modelregistry.ProviderAvailable:
		return "configured"
	case modelregistry.ProviderNotConfigured:
		return "not_configured"
	case modelregistry.ProviderInvalidConfiguration:
		return "invalid_configuration"
	default:
		return "unavailable"
	}
}

func catalogQualificationStatus(harness modelregistry.HarnessAvailability,
	found bool,
) string {
	if !found {
		return "unavailable"
	}
	if status := strings.TrimSpace(harness.LatestQualificationStatus); status != "" {
		return status
	}
	if harness.RootEligible {
		return modelregistry.QualificationStatusAvailable
	}
	if status := strings.TrimSpace(harness.QualificationStatus); status != "" {
		return status
	}
	return modelregistry.QualificationStatusNotConfigured
}

func catalogUnavailableReason(provider modelregistry.ProviderAvailability,
	item ModelRouteCatalogItem,
) string {
	if item.Selectable {
		return ""
	}
	if !provider.Enabled {
		return "provider_disabled"
	}
	switch item.CredentialStatus {
	case "not_configured":
		return "credential_not_configured"
	case "invalid_configuration":
		return "invalid_configuration"
	case "unavailable":
		return "provider_unavailable"
	}
	if provider.Status != modelregistry.ProviderAvailable {
		return "provider_unavailable"
	}
	switch item.QualificationStatus {
	case modelregistry.QualificationStatusNotConfigured,
		modelregistry.QualificationStatusProtocolMismatch,
		modelregistry.QualificationStatusAuthFailed,
		modelregistry.QualificationStatusNetworkFailed,
		modelregistry.QualificationStatusRateLimit,
		modelregistry.QualificationStatusCapacity,
		modelregistry.QualificationStatusModelUnsupported:
		return item.QualificationStatus
	case "", modelregistry.QualificationStatusAvailable:
		// Harness readiness below determines the remaining reason.
	default:
		// Registry-internal Harness states such as qualification_required and
		// unavailable are intentionally folded onto the public API taxonomy.
		return "harness_qualification_required"
	}
	if !item.HarnessReady {
		return "harness_qualification_required"
	}
	return "provider_unavailable"
}
