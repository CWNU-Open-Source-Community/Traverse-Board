package application

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/buildinfo"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/redact"
)

const (
	DoctorSnapshotProtocolVersion   = "doctor-snapshot.v1"
	DebugQueryProtocolVersion       = "debug-query.v1"
	DiagnosticBundleProtocolVersion = "diagnostic-bundle.v1"
	maxDebugQueryEvents             = 100
	maxDebugQueryScan               = 500
	maxDebugQueryWindow             = 7 * 24 * time.Hour
	maxDiagnosticTypeSourceBytes    = 128
	maxDiagnosticSubjectBytes       = 256
)

type DiagnosticsStore interface {
	SchemaVersion(context.Context) (int, error)
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetRootAgent(context.Context, string) (domain.AgentNode, bool, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	ListRunEventsAfterSequence(context.Context, string, int64, int) ([]events.Event, error)
}

type ModelSnapshotSource interface {
	Snapshot() modelregistry.Snapshot
}

type DiagnosticsService struct {
	store  DiagnosticsStore
	models ModelSnapshotSource
	now    func() time.Time
}

type DiagnosticCheck struct {
	Component  string `json:"component"`
	Status     string `json:"status"`
	DetailCode string `json:"detail_code"`
	Evidence   string `json:"evidence"`
}

type DoctorRunSnapshot struct {
	RunID                     string                            `json:"run_id"`
	Status                    domain.RunStatus                  `json:"status"`
	Profile                   domain.Profile                    `json:"profile"`
	ModelRoute                string                            `json:"model_route"`
	WorkspaceID               string                            `json:"workspace_id,omitempty"`
	RootAgentID               string                            `json:"root_agent_id"`
	Surface                   domain.ExecutionSurface           `json:"surface"`
	Phase                     domain.ExecutionPhase             `json:"phase"`
	ExecutionPermission       domain.RunExecutionPermissionMode `json:"execution_permission"`
	NetworkMode               string                            `json:"network_mode"`
	AllowedNetworkTargetCount int                               `json:"allowed_network_target_count"`
	ReadOnlyToolsEligible     bool                              `json:"read_only_tools_eligible"`
	RepairToolsEligible       bool                              `json:"repair_tools_eligible"`
	ProcessCapabilityGranted  bool                              `json:"process_capability_granted"`
}

type DiagnosticRedaction struct {
	EventPayloads string `json:"event_payloads"`
	Prompts       string `json:"prompts"`
	TerminalInput string `json:"terminal_input"`
	CommandInput  string `json:"command_input"`
	Secrets       string `json:"secrets"`
}

type DoctorSnapshot struct {
	ProtocolVersion string                 `json:"protocol_version"`
	GeneratedAt     time.Time              `json:"generated_at"`
	Ready           bool                   `json:"ready"`
	SchemaVersion   int                    `json:"schema_version"`
	Build           buildinfo.Diagnostic   `json:"build"`
	Models          modelregistry.Snapshot `json:"models"`
	Run             *DoctorRunSnapshot     `json:"run,omitempty"`
	Checks          []DiagnosticCheck      `json:"checks"`
	Redaction       DiagnosticRedaction    `json:"redaction"`
}

type DebugQueryRequest struct {
	Version         string
	RunID           string
	AfterSequence   int64
	From            time.Time
	To              time.Time
	Limit           int
	CorrelationKind string
	CorrelationID   string
	TypePrefix      string
	SourcePrefix    string
}

type DebugTimelineItem struct {
	Sequence          int64     `json:"sequence"`
	Type              string    `json:"type"`
	Source            string    `json:"source"`
	SubjectID         string    `json:"subject_id"`
	Category          string    `json:"category"`
	OccurredAt        time.Time `json:"occurred_at"`
	ObservedAt        time.Time `json:"observed_at"`
	TimestampAdjusted bool      `json:"timestamp_adjusted"`
	Evidence          string    `json:"evidence"`
	PayloadState      string    `json:"payload_state"`
}

type DebugQueryResult struct {
	ProtocolVersion   string              `json:"protocol_version"`
	RunID             string              `json:"run_id"`
	From              time.Time           `json:"from"`
	To                time.Time           `json:"to"`
	AfterSequence     int64               `json:"after_sequence"`
	NextAfterSequence int64               `json:"next_after_sequence"`
	Limit             int                 `json:"limit"`
	Scanned           int                 `json:"scanned"`
	HasMore           bool                `json:"has_more"`
	Items             []DebugTimelineItem `json:"items"`
	Redaction         DiagnosticRedaction `json:"redaction"`
}

type DiagnosticBundle struct {
	ProtocolVersion string           `json:"protocol_version"`
	GeneratedAt     time.Time        `json:"generated_at"`
	Doctor          DoctorSnapshot   `json:"doctor"`
	Debug           DebugQueryResult `json:"debug"`
}

func NewDiagnosticsService(store DiagnosticsStore,
	models ModelSnapshotSource,
) *DiagnosticsService {
	return &DiagnosticsService{store: store, models: models,
		now: func() time.Time { return time.Now().UTC() }}
}

func (s *DiagnosticsService) Doctor(ctx context.Context,
	runID string,
) (DoctorSnapshot, error) {
	if s == nil || s.store == nil || s.models == nil || s.now == nil {
		return DoctorSnapshot{}, apperror.New(apperror.CodeFailedPrecondition,
			"structured doctor dependencies are required")
	}
	runID = strings.TrimSpace(runID)
	if runID != "" && !validControlIdentity(runID) {
		return DoctorSnapshot{}, apperror.New(apperror.CodeInvalidArgument,
			"doctor Run id is invalid")
	}
	schemaVersion, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return DoctorSnapshot{}, apperror.Normalize(err)
	}
	models := s.models.Snapshot()
	checks := []DiagnosticCheck{{Component: "store", Status: "ready",
		DetailCode: "schema_open", Evidence: "local_snapshot"}}
	providerReady := false
	for _, provider := range models.Providers {
		status := "degraded"
		if provider.Status == modelregistry.ProviderAvailable {
			status = "ready"
			providerReady = true
		} else if provider.Status == modelregistry.ProviderNotConfigured {
			status = "not_configured"
		}
		checks = append(checks, DiagnosticCheck{Component: "provider:" + provider.Name,
			Status: status, DetailCode: provider.Status, Evidence: "registry_snapshot"})
	}
	checks = append(checks,
		DiagnosticCheck{Component: "sandbox", Status: "not_probed",
			DetailCode: "runtime_probe_not_requested", Evidence: "configuration_only"},
		DiagnosticCheck{Component: "browser", Status: "not_probed",
			DetailCode: "runtime_probe_not_requested", Evidence: "configuration_only"},
		DiagnosticCheck{Component: "plugins", Status: "not_probed",
			DetailCode: "plugin_inventory_not_authoritative", Evidence: "none"})
	var runSnapshot *DoctorRunSnapshot
	routeReady := runID == ""
	if runID != "" {
		run, err := s.store.GetRun(ctx, runID)
		if err != nil {
			return DoctorSnapshot{}, apperror.Normalize(err)
		}
		mission, err := s.store.GetMission(ctx, run.MissionID)
		if err != nil {
			return DoctorSnapshot{}, apperror.Normalize(err)
		}
		root, found, err := s.store.GetRootAgent(ctx, run.ID)
		if err != nil || !found {
			if err == nil {
				err = errors.New("Run root Agent is unavailable")
			}
			return DoctorSnapshot{}, apperror.Normalize(err)
		}
		mode, err := s.store.GetRunMode(ctx, run.ID)
		if err != nil {
			return DoctorSnapshot{}, apperror.Normalize(err)
		}
		permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
		if err != nil {
			return DoctorSnapshot{}, apperror.Normalize(err)
		}
		for _, route := range models.Routes {
			if route.Name == run.Config.ModelRoute {
				routeReady = route.Available && route.HarnessReady
				break
			}
		}
		repairEligible := mode.Surface == domain.ExecutionSurfaceCode &&
			mode.Phase == domain.ExecutionPhaseDeliver &&
			(permission.Mode == domain.RunExecutionPermissionApproval ||
				permission.Mode == domain.RunExecutionPermissionFullAccess) &&
			permission.OperatorConfirmed
		runSnapshot = &DoctorRunSnapshot{
			RunID: run.ID, Status: run.Status, Profile: mission.Profile,
			ModelRoute: run.Config.ModelRoute, WorkspaceID: mission.WorkspaceID,
			RootAgentID: root.ID, Surface: mode.Surface, Phase: mode.Phase,
			ExecutionPermission: permission.Mode, NetworkMode: mission.Scope.NetworkMode,
			AllowedNetworkTargetCount: len(mission.Scope.AllowedTargets),
			ReadOnlyToolsEligible:     mode.Surface == domain.ExecutionSurfaceCode,
			RepairToolsEligible:       repairEligible,
			// Snapshot rows never carry a process-local capability grant.
			ProcessCapabilityGranted: false,
		}
		checks = append(checks,
			DiagnosticCheck{Component: "run", Status: "ready",
				DetailCode: string(run.Status), Evidence: "persisted_snapshot"},
			DiagnosticCheck{Component: "workspace", Status: workspaceDoctorStatus(mission.WorkspaceID),
				DetailCode: workspaceDoctorDetail(mission.WorkspaceID), Evidence: "run_binding"},
			DiagnosticCheck{Component: "network", Status: networkDoctorStatus(mission.Scope.NetworkMode),
				DetailCode: mission.Scope.NetworkMode, Evidence: "mission_scope"},
			DiagnosticCheck{Component: "tools", Status: doctorToolStatus(repairEligible),
				DetailCode: doctorToolDetail(repairEligible), Evidence: "policy_projection"})
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Component < checks[j].Component })
	redaction := closedDiagnosticRedaction()
	return DoctorSnapshot{ProtocolVersion: DoctorSnapshotProtocolVersion,
		GeneratedAt: s.now().UTC(), Ready: schemaVersion > 0 && providerReady && routeReady,
		SchemaVersion: schemaVersion, Build: buildinfo.PortableDiagnostic(), Models: models,
		Run: runSnapshot, Checks: checks, Redaction: redaction}, nil
}

func (s *DiagnosticsService) Debug(ctx context.Context,
	request DebugQueryRequest,
) (DebugQueryResult, error) {
	if s == nil || s.store == nil || s.now == nil {
		return DebugQueryResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"structured debug dependencies are required")
	}
	normalized, err := normalizeDebugQueryRequest(request, s.now().UTC())
	if err != nil {
		return DebugQueryResult{}, err
	}
	if _, err := s.store.GetRun(ctx, normalized.RunID); err != nil {
		return DebugQueryResult{}, apperror.Normalize(err)
	}
	items := make([]DebugTimelineItem, 0, normalized.Limit)
	cursor := normalized.AfterSequence
	scanned := 0
	hasMore := false
	lastObserved := time.Time{}
	for scanned < maxDebugQueryScan && len(items) < normalized.Limit {
		batchLimit := 100
		if remaining := maxDebugQueryScan - scanned; remaining < batchLimit {
			batchLimit = remaining
		}
		batch, err := s.store.ListRunEventsAfterSequence(ctx, normalized.RunID,
			cursor, batchLimit)
		if err != nil {
			return DebugQueryResult{}, apperror.Normalize(err)
		}
		if len(batch) == 0 {
			break
		}
		for index, event := range batch {
			scanned++
			cursor = event.Sequence
			if !debugEventMatches(event, normalized) {
				continue
			}
			observedAt := event.CreatedAt.UTC()
			adjusted := false
			if !lastObserved.IsZero() && observedAt.Before(lastObserved) {
				observedAt = lastObserved
				adjusted = true
			}
			lastObserved = observedAt
			items = append(items, DebugTimelineItem{
				Sequence: event.Sequence,
				Type: boundedDiagnosticMetadata(event.Type,
					maxDiagnosticTypeSourceBytes, false),
				Source: boundedDiagnosticMetadata(event.Source,
					maxDiagnosticTypeSourceBytes, false),
				SubjectID: boundedDiagnosticMetadata(event.SubjectID,
					maxDiagnosticSubjectBytes, true),
				Category:   classifyDiagnosticEvent(event.Type),
				OccurredAt: event.CreatedAt.UTC(), ObservedAt: observedAt,
				TimestampAdjusted: adjusted, Evidence: "persisted_event",
				PayloadState: "withheld",
			})
			if len(items) == normalized.Limit {
				hasMore = index < len(batch)-1 || len(batch) == batchLimit
				break
			}
		}
		if len(batch) < batchLimit || len(items) == normalized.Limit {
			break
		}
	}
	if scanned >= maxDebugQueryScan {
		hasMore = true
	}
	return DebugQueryResult{ProtocolVersion: DebugQueryProtocolVersion,
		RunID: normalized.RunID, From: normalized.From, To: normalized.To,
		AfterSequence: normalized.AfterSequence, NextAfterSequence: cursor,
		Limit: normalized.Limit, Scanned: scanned, HasMore: hasMore,
		Items: items, Redaction: closedDiagnosticRedaction()}, nil
}

func (s *DiagnosticsService) Bundle(ctx context.Context, runID string,
	debugRequest DebugQueryRequest,
) (DiagnosticBundle, error) {
	doctor, err := s.Doctor(ctx, runID)
	if err != nil {
		return DiagnosticBundle{}, err
	}
	debug, err := s.Debug(ctx, debugRequest)
	if err != nil {
		return DiagnosticBundle{}, err
	}
	return DiagnosticBundle{ProtocolVersion: DiagnosticBundleProtocolVersion,
		GeneratedAt: s.now().UTC(), Doctor: doctor, Debug: debug}, nil
}

func normalizeDebugQueryRequest(request DebugQueryRequest,
	now time.Time,
) (DebugQueryRequest, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.CorrelationKind = strings.ToLower(strings.TrimSpace(request.CorrelationKind))
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	request.TypePrefix = strings.TrimSpace(request.TypePrefix)
	request.SourcePrefix = strings.TrimSpace(request.SourcePrefix)
	if request.Version != DebugQueryProtocolVersion || !validControlIdentity(request.RunID) ||
		request.AfterSequence < 0 || request.Limit < 1 || request.Limit > maxDebugQueryEvents ||
		!validDiagnosticFilter(request.TypePrefix) ||
		!validDiagnosticFilter(request.SourcePrefix) {
		return DebugQueryRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"debug query version, Run, cursor, limit, or filter is invalid")
	}
	switch request.CorrelationKind {
	case "", "run":
		if request.CorrelationID != "" && request.CorrelationID != request.RunID {
			return DebugQueryRequest{}, apperror.New(apperror.CodeInvalidArgument,
				"run correlation must match the selected Run")
		}
	case "attempt", "tool", "process", "request":
		if !validControlIdentity(request.CorrelationID) {
			return DebugQueryRequest{}, apperror.New(apperror.CodeInvalidArgument,
				"debug correlation identity is invalid")
		}
	default:
		return DebugQueryRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"debug correlation kind is unsupported")
	}
	if request.To.IsZero() {
		request.To = now.UTC()
	} else {
		request.To = request.To.UTC()
	}
	if request.From.IsZero() {
		request.From = request.To.Add(-time.Hour)
	} else {
		request.From = request.From.UTC()
	}
	if request.From.After(request.To) || request.To.Sub(request.From) > maxDebugQueryWindow {
		return DebugQueryRequest{}, apperror.New(apperror.CodeInvalidArgument,
			"debug query time window is invalid or exceeds seven days")
	}
	return request, nil
}

func debugEventMatches(event events.Event, request DebugQueryRequest) bool {
	createdAt := event.CreatedAt.UTC()
	if createdAt.Before(request.From) || createdAt.After(request.To) ||
		(request.TypePrefix != "" && !strings.HasPrefix(event.Type, request.TypePrefix)) ||
		(request.SourcePrefix != "" && !strings.HasPrefix(event.Source, request.SourcePrefix)) {
		return false
	}
	if request.CorrelationKind != "" && request.CorrelationKind != "run" &&
		event.SubjectID != request.CorrelationID {
		return false
	}
	return true
}

func classifyDiagnosticEvent(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "model."), strings.HasPrefix(eventType, "provider."):
		return "model"
	case strings.HasPrefix(eventType, "tool."), strings.HasPrefix(eventType, "supervisor.tool_"),
		strings.HasPrefix(eventType, "controlled_command."),
		strings.HasPrefix(eventType, "host_command."), strings.HasPrefix(eventType, "terminal."),
		strings.HasPrefix(eventType, "sandbox."), strings.HasPrefix(eventType, "browser."):
		return "tool"
	case strings.HasPrefix(eventType, "policy."), strings.HasPrefix(eventType, "approval."),
		strings.Contains(eventType, "permission"), strings.Contains(eventType, "mode_selected"):
		return "policy"
	case strings.HasPrefix(eventType, "workspace."), strings.HasPrefix(eventType, "network."),
		strings.HasPrefix(eventType, "runtime."):
		return "infrastructure"
	default:
		return "application"
	}
}

func validDiagnosticFilter(value string) bool {
	return utf8.ValidString(value) && len(value) <= 128 && !strings.ContainsRune(value, 0)
}

func boundedDiagnosticMetadata(value string, maximumBytes int, allowEmpty bool) string {
	if !utf8.ValidString(value) || len(value) > maximumBytes || strings.ContainsRune(value, 0) {
		return "withheld"
	}
	if strings.TrimSpace(value) == "" {
		if allowEmpty {
			return ""
		}
		return "withheld"
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return "withheld"
		}
	}
	redacted := redact.String(value)
	if len(redacted) > maximumBytes {
		return "withheld"
	}
	return redacted
}

func closedDiagnosticRedaction() DiagnosticRedaction {
	return DiagnosticRedaction{EventPayloads: "withheld", Prompts: "withheld",
		TerminalInput: "withheld", CommandInput: "withheld", Secrets: "redacted"}
}

func workspaceDoctorStatus(workspaceID string) string {
	if workspaceID == "" {
		return "not_configured"
	}
	return "ready"
}

func workspaceDoctorDetail(workspaceID string) string {
	if workspaceID == "" {
		return "no_workspace_binding"
	}
	return "workspace_bound"
}

func networkDoctorStatus(mode string) string {
	if mode == "disabled" {
		return "ready"
	}
	return "degraded"
}

func doctorToolStatus(repairEligible bool) string {
	if repairEligible {
		return "not_probed"
	}
	return "ready"
}

func doctorToolDetail(repairEligible bool) string {
	if repairEligible {
		return "repair_policy_eligible_process_capability_unproven"
	}
	return "read_only_policy_eligible"
}
