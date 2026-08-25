package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/session"
)

type StandardCodePresetStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetSession(context.Context, string) (session.Session, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionProfile(context.Context, string) (domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionProfileSnapshot(context.Context, string) (domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionInteraction(context.Context, string) (domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionInteractionSnapshot(context.Context, string) (domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionPermissionSnapshot(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
	GetRunBrowserCDPPermission(context.Context, string) (domain.RunBrowserCDPPermissionSnapshot, error)
	GetRunBrowserCDPPermissionSnapshot(context.Context, string) (domain.RunBrowserCDPPermissionSnapshot, error)
	GetRunModeSnapshot(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionLease(context.Context, string) (domain.RunExecutionLease, bool, error)
	GetStandardCodePresetOperation(context.Context, string) (domain.StandardCodePresetOperation, bool, error)
	BeginStandardCodePreset(context.Context, domain.StandardCodePresetOperation) (domain.StandardCodePresetOperation, bool, error)
	CreateMissionRunWithStandardCodePresetIntent(context.Context, domain.Mission,
		domain.Run, domain.RunModeSnapshot, session.Session, []events.Event,
		domain.StandardCodePresetOperation) (domain.StandardCodePresetOperation, bool, error)
	CommitStandardCodePreset(context.Context, domain.StandardCodePresetCommit) (
		domain.StandardCodePresetOperation, domain.Run, bool, error)
}

type StandardCodePresetDrydocks interface {
	InspectWorkspace(context.Context, string) (DrydockWorkspaceInspection, error)
	Projection(context.Context, string, int) (DrydockProjection, error)
	Create(context.Context, DrydockCreateRequest) (DrydockCreateResult, error)
}

type StandardCodePresetService struct {
	store    StandardCodePresetStore
	drydocks StandardCodePresetDrydocks
	runtime  CapabilityReadinessRuntime
	now      func() time.Time
}

type ConfigureStandardCodeRequest struct {
	Version               string
	RunID                 string
	WorkspaceID           string
	Goal                  string
	BackendIntent         string
	Action                string
	OperationKey          string
	RequestedBy           string
	ConfirmWorkspaceTrust bool
	ExpectedTrustDigest   string
}

type StandardCodePresetResultStatus string

const (
	StandardCodeResultBlocked         StandardCodePresetResultStatus = "blocked"
	StandardCodeResultWaitingForPause StandardCodePresetResultStatus = "waiting_for_pause"
	StandardCodeResultConfigured      StandardCodePresetResultStatus = "configured"
)

type StandardCodeNextStep string

const (
	StandardCodeNextConfirmWorkspaceTrust StandardCodeNextStep = "confirm_workspace_trust"
	StandardCodeNextPauseAndConfigure     StandardCodeNextStep = "pause_and_configure"
	StandardCodeNextWaitForQuiescence     StandardCodeNextStep = "wait_for_quiescence"
	StandardCodeNextSelectDocker          StandardCodeNextStep = "select_docker"
	StandardCodeNextSelectApproval        StandardCodeNextStep = "select_approval"
	StandardCodeNextRetryReadiness        StandardCodeNextStep = "retry_readiness"
	StandardCodeNextCreateNewRun          StandardCodeNextStep = "create_new_run"
)

type StandardCodeBackendReadiness struct {
	Backend     domain.StandardCodeBackend       `json:"backend"`
	Available   bool                             `json:"available"`
	BlockedBy   []CapabilityReadinessBlocker     `json:"blocked_by"`
	Remediation []CapabilityReadinessRemediation `json:"remediation"`
}

type StandardCodePresetResult struct {
	ProtocolVersion string                                  `json:"protocol_version"`
	Status          StandardCodePresetResultStatus          `json:"status"`
	Run             *domain.Run                             `json:"-"`
	RunID           string                                  `json:"run_id,omitempty"`
	WorkspaceID     string                                  `json:"workspace_id"`
	Action          domain.StandardCodePresetAction         `json:"action"`
	BackendIntent   domain.StandardCodeBackendIntent        `json:"backend_intent"`
	SelectedBackend domain.StandardCodeBackend              `json:"selected_backend,omitempty"`
	SelectionReason domain.StandardCodeSelectionReason      `json:"selection_reason,omitempty"`
	LocalReadiness  StandardCodeBackendReadiness            `json:"local_readiness"`
	DockerReadiness StandardCodeBackendReadiness            `json:"docker_readiness"`
	BlockedBy       []CapabilityReadinessBlocker            `json:"blocked_by"`
	NextSteps       []StandardCodeNextStep                  `json:"next_steps"`
	TrustRequired   bool                                    `json:"trust_required"`
	TrustDigest     string                                  `json:"trust_digest,omitempty"`
	Mode            *domain.RunModeSnapshot                 `json:"-"`
	Profile         *domain.RunExecutionProfileSnapshot     `json:"-"`
	Interaction     *domain.RunExecutionInteractionSnapshot `json:"-"`
	Permission      *domain.RunExecutionPermissionSnapshot  `json:"-"`
	BrowserCDP      *domain.RunBrowserCDPPermissionSnapshot `json:"-"`
	DrydockReady    bool                                    `json:"drydock_ready"`
	Network         string                                  `json:"network"`
	Credentials     string                                  `json:"credentials"`
	Replayed        bool                                    `json:"replayed"`
	CapabilityGrant bool                                    `json:"capability_grant"`
}

func NewStandardCodePresetService(store StandardCodePresetStore,
	drydocks StandardCodePresetDrydocks, runtime CapabilityReadinessRuntime,
) (*StandardCodePresetService, error) {
	if store == nil || drydocks == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code preset requires durable storage and Drydock control")
	}
	if err := runtime.Validate(); err != nil {
		return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
			"Standard Code preset runtime is invalid", err)
	}
	return &StandardCodePresetService{store: store, drydocks: drydocks,
		runtime: runtime, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *StandardCodePresetService) Configure(ctx context.Context,
	request ConfigureStandardCodeRequest,
) (StandardCodePresetResult, error) {
	if s == nil || s.store == nil || s.drydocks == nil || s.now == nil {
		return StandardCodePresetResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Standard Code preset service is unavailable")
	}
	normalized, intent, action, err := normalizeStandardCodePresetRequest(request)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	keyDigest := runmutation.Fingerprint("standard_code_preset_operation.v1",
		normalized.OperationKey)
	requestFingerprint := standardCodePresetRequestFingerprint(normalized, intent, action)
	localReadiness := s.backendReadiness(domain.StandardCodeSelectedLocal)
	dockerReadiness := s.backendReadiness(domain.StandardCodeSelectedDocker)
	base := StandardCodePresetResult{ProtocolVersion: domain.StandardCodePresetProtocolVersion,
		Status: StandardCodeResultBlocked, WorkspaceID: normalized.WorkspaceID,
		Action: action, BackendIntent: intent, LocalReadiness: localReadiness,
		DockerReadiness: dockerReadiness, Network: "disabled", Credentials: "none",
		CapabilityGrant: false}

	existing, found, err := s.store.GetStandardCodePresetOperation(ctx, keyDigest)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	if found {
		probe := domain.StandardCodePresetOperation{ProtocolVersion: domain.StandardCodePresetProtocolVersion,
			KeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
			RequestedRunID: normalized.RunID, RunID: existing.RunID,
			MissionID: existing.MissionID, WorkspaceID: existing.WorkspaceID,
			Action: action, BackendIntent: intent, SelectedBackend: existing.SelectedBackend,
			SelectionReason: existing.SelectionReason, RequestedBy: normalized.RequestedBy}
		if err := samePresetApplicationIntent(existing, probe); err != nil {
			return StandardCodePresetResult{}, err
		}
		if existing.Status == domain.StandardCodePresetConfigured {
			return s.loadConfiguredResult(ctx, existing, base, true)
		}
		base.RunID, base.WorkspaceID = existing.RunID, existing.WorkspaceID
		base.SelectedBackend, base.SelectionReason = existing.SelectedBackend,
			existing.SelectionReason
		return s.continuePrepared(ctx, normalized, existing, base, true)
	}
	if normalized.RunID != "" {
		run, runErr := s.store.GetRun(ctx, normalized.RunID)
		if runErr != nil {
			return StandardCodePresetResult{}, apperror.Normalize(runErr)
		}
		mission, missionErr := s.store.GetMission(ctx, run.MissionID)
		if missionErr != nil {
			return StandardCodePresetResult{}, apperror.Normalize(missionErr)
		}
		base.RunID, base.WorkspaceID = run.ID, mission.WorkspaceID
	}

	selected, reason, selectionBlocked := s.selectBackend(intent, localReadiness,
		dockerReadiness)
	if len(selectionBlocked) > 0 {
		base.BlockedBy = selectionBlocked
		base.NextSteps = s.backendAlternatives(intent, localReadiness, dockerReadiness)
		return base, nil
	}
	base.SelectedBackend, base.SelectionReason = selected, reason

	target, createNew, result, err := s.resolveTarget(ctx, normalized, action, base)
	if err != nil || result != nil {
		if result != nil {
			return *result, err
		}
		return StandardCodePresetResult{}, err
	}
	base.RunID, base.WorkspaceID = target.run.ID, target.mission.WorkspaceID

	readyDrydock, trusted, trustDigest, err := s.preflightDrydock(ctx, target.run.ID,
		target.mission.WorkspaceID, normalized)
	if err != nil {
		return StandardCodePresetResult{}, err
	}
	if !readyDrydock && !trusted {
		// A prepared new target has not been persisted yet. Do not expose a
		// generated identity that cannot be loaded or replayed; the confirmed
		// operation will create and return the actual Run atomically.
		if createNew {
			base.RunID = ""
		}
		base.TrustRequired, base.TrustDigest = true, trustDigest
		base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerWorkspaceUntrusted}
		base.NextSteps = []StandardCodeNextStep{StandardCodeNextConfirmWorkspaceTrust}
		return base, nil
	}

	now := s.now().UTC()
	status := domain.StandardCodePresetPreparing
	if !createNew && target.run.Status == domain.RunRunning {
		status = domain.StandardCodePresetWaitingForPause
	}
	operation := domain.StandardCodePresetOperation{
		ProtocolVersion: domain.StandardCodePresetProtocolVersion,
		KeyDigest:       keyDigest, RequestFingerprint: requestFingerprint,
		RequestedRunID: normalized.RunID, RunID: target.run.ID,
		MissionID: target.mission.ID, WorkspaceID: target.mission.WorkspaceID,
		Action: action, BackendIntent: intent, SelectedBackend: selected,
		SelectionReason: reason, Status: status, RequestedBy: normalized.RequestedBy,
		CreatedAt: now, UpdatedAt: now,
	}
	var replayed bool
	if createNew {
		operation, replayed, err = s.store.CreateMissionRunWithStandardCodePresetIntent(
			ctx, target.mission, target.run, target.mode, target.session,
			target.initialEvents, operation)
	} else {
		operation, replayed, err = s.store.BeginStandardCodePreset(ctx, operation)
	}
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	base.RunID, base.Replayed = operation.RunID, replayed
	return s.continuePrepared(ctx, normalized, operation, base, replayed)
}

type standardCodeTarget struct {
	mission       domain.Mission
	run           domain.Run
	mode          domain.RunModeSnapshot
	session       session.Session
	initialEvents []events.Event
}

func (s *StandardCodePresetService) resolveTarget(ctx context.Context,
	request ConfigureStandardCodeRequest, action domain.StandardCodePresetAction,
	base StandardCodePresetResult,
) (standardCodeTarget, bool, *StandardCodePresetResult, error) {
	if request.RunID == "" {
		if request.WorkspaceID == "" || request.Goal == "" {
			return standardCodeTarget{}, false, nil, apperror.New(
				apperror.CodeInvalidArgument,
				"new Standard Code Run requires a Workspace and goal")
		}
		return s.prepareNewTarget(ctx, request.WorkspaceID, request.Goal,
			request.RequestedBy)
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return standardCodeTarget{}, false, nil, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return standardCodeTarget{}, false, nil, apperror.Normalize(err)
	}
	mode, err := s.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return standardCodeTarget{}, false, nil, apperror.Normalize(err)
	}
	if request.WorkspaceID != "" && request.WorkspaceID != mission.WorkspaceID {
		return standardCodeTarget{}, false, nil, apperror.New(
			apperror.CodeConflict, "Standard Code Workspace does not match the Run")
	}
	if mission.WorkspaceID == "" || mission.Scope.NetworkMode != "disabled" {
		return standardCodeTarget{}, false, nil, apperror.New(
			apperror.CodeFailedPrecondition,
			"Standard Code requires a registered non-networked Workspace")
	}
	if mode.Surface != domain.ExecutionSurfaceCode {
		goal := request.Goal
		if goal == "" {
			goal = mission.Goal
		}
		return s.prepareNewTarget(ctx, mission.WorkspaceID, goal,
			request.RequestedBy)
	}
	base.RunID, base.WorkspaceID = run.ID, mission.WorkspaceID
	if run.Terminal() || run.Status == domain.RunPreparing ||
		run.Status == domain.RunWaitingApproval {
		base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
		base.NextSteps = []StandardCodeNextStep{StandardCodeNextCreateNewRun}
		return standardCodeTarget{}, false, &base, nil
	}
	lease, leaseFound, err := s.store.GetRunExecutionLease(ctx, run.ID)
	if err != nil {
		return standardCodeTarget{}, false, nil, apperror.Normalize(err)
	}
	activeLease := leaseFound && lease.ActiveAt(s.now())
	if (run.Status == domain.RunCreated || run.Status == domain.RunPaused) && activeLease {
		base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerExecutionLeaseActive}
		base.NextSteps = []StandardCodeNextStep{StandardCodeNextWaitForQuiescence}
		return standardCodeTarget{}, false, &base, nil
	}
	if run.Status == domain.RunRunning && action != domain.StandardCodePresetPauseAndConfigure {
		base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
		base.NextSteps = []StandardCodeNextStep{StandardCodeNextPauseAndConfigure}
		return standardCodeTarget{}, false, &base, nil
	}
	if run.Status != domain.RunCreated && run.Status != domain.RunPaused &&
		run.Status != domain.RunRunning {
		base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
		base.NextSteps = []StandardCodeNextStep{StandardCodeNextCreateNewRun}
		return standardCodeTarget{}, false, &base, nil
	}
	return standardCodeTarget{mission: mission, run: run, mode: mode}, false, nil, nil
}

func (s *StandardCodePresetService) prepareNewTarget(ctx context.Context,
	workspaceID, goal, requestedBy string,
) (standardCodeTarget, bool, *StandardCodePresetResult, error) {
	prepared, err := prepareRun(ctx, CreateRunRequest{Goal: goal,
		Profile: string(domain.ProfileCode), Surface: string(domain.ExecutionSurfaceCode),
		Phase: string(domain.ExecutionPhasePlan), WorkspaceID: workspaceID,
		Interactive: true, Budget: domain.DefaultBudget(), RequestedBy: requestedBy},
		s.store.GetSession)
	if err != nil {
		return standardCodeTarget{}, false, nil, apperror.Wrap(
			apperror.CodeInvalidArgument, "prepare Standard Code Run", err)
	}
	return standardCodeTarget{mission: prepared.Mission, run: prepared.Run,
		mode: prepared.Mode, session: prepared.Session,
		initialEvents: prepared.InitialEvents}, true, nil, nil
}

func (s *StandardCodePresetService) preflightDrydock(ctx context.Context, runID,
	workspaceID string, request ConfigureStandardCodeRequest,
) (ready, trusted bool, trustDigest string, err error) {
	if runID != "" {
		projection, projectionErr := s.drydocks.Projection(ctx, runID, 1)
		if projectionErr == nil && projection.Workspace != nil &&
			(projection.Workspace.State == drydock.StateReady ||
				projection.Workspace.State == drydock.StateDelivered) &&
			projection.Trust != nil {
			return true, true, "", nil
		}
		if projectionErr != nil && apperror.CodeOf(projectionErr) != apperror.CodeNotFound {
			return false, false, "", apperror.Normalize(projectionErr)
		}
	}
	inspection, err := s.drydocks.InspectWorkspace(ctx, workspaceID)
	if err != nil {
		return false, false, "", apperror.Normalize(err)
	}
	if !request.ConfirmWorkspaceTrust {
		return false, false, inspection.TrustDigest, nil
	}
	if request.ExpectedTrustDigest == "" ||
		request.ExpectedTrustDigest != inspection.TrustDigest {
		return false, false, inspection.TrustDigest, apperror.New(
			apperror.CodeConflict,
			"Standard Code Workspace Trust confirmation does not match the reviewed source")
	}
	return false, true, inspection.TrustDigest, nil
}

func (s *StandardCodePresetService) continuePrepared(ctx context.Context,
	request ConfigureStandardCodeRequest, operation domain.StandardCodePresetOperation,
	base StandardCodePresetResult, replayed bool,
) (StandardCodePresetResult, error) {
	base.RunID, base.WorkspaceID = operation.RunID, operation.WorkspaceID
	base.SelectedBackend, base.SelectionReason = operation.SelectedBackend,
		operation.SelectionReason
	projection, err := s.drydocks.Projection(ctx, operation.RunID, 1)
	if err != nil && apperror.CodeOf(err) != apperror.CodeNotFound {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	base.DrydockReady = projection.Workspace != nil && projection.Trust != nil &&
		(projection.Workspace.State == drydock.StateReady ||
			projection.Workspace.State == drydock.StateDelivered)
	run, runErr := s.store.GetRun(ctx, operation.RunID)
	if runErr != nil {
		return StandardCodePresetResult{}, apperror.Normalize(runErr)
	}
	lease, leaseFound, leaseErr := s.store.GetRunExecutionLease(ctx, run.ID)
	if leaseErr != nil {
		return StandardCodePresetResult{}, apperror.Normalize(leaseErr)
	}
	activeLease := leaseFound && lease.ActiveAt(s.now())
	if operation.Status == domain.StandardCodePresetWaitingForPause {
		if run.Status != domain.RunRunning && run.Status != domain.RunPaused {
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextCreateNewRun}
			base.Replayed = replayed
			return base, nil
		}
		if activeLease {
			base.Status = StandardCodeResultWaitingForPause
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerExecutionLeaseActive}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextWaitForQuiescence}
			base.Replayed = replayed
			return base, nil
		}
	} else {
		if run.Status == domain.RunRunning {
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextPauseAndConfigure}
			base.Replayed = replayed
			return base, nil
		}
		if !domain.CanChangeRunExecutionProfile(run.Status) {
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextCreateNewRun}
			base.Replayed = replayed
			return base, nil
		}
		if activeLease {
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerExecutionLeaseActive}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextWaitForQuiescence}
			base.Replayed = replayed
			return base, nil
		}
	}
	selectedReadiness := base.LocalReadiness
	if operation.SelectedBackend == domain.StandardCodeSelectedDocker {
		selectedReadiness = base.DockerReadiness
	}
	if !selectedReadiness.Available {
		if operation.Status == domain.StandardCodePresetWaitingForPause {
			base.Status = StandardCodeResultWaitingForPause
		}
		base.BlockedBy = append([]CapabilityReadinessBlocker(nil),
			selectedReadiness.BlockedBy...)
		base.NextSteps = s.backendAlternatives(operation.BackendIntent,
			base.LocalReadiness, base.DockerReadiness)
		base.Replayed = replayed
		return base, nil
	}
	if !base.DrydockReady {
		created, createErr := s.drydocks.Create(ctx, DrydockCreateRequest{
			RunID:                 operation.RunID,
			OperationKey:          "standard-code-drydock-" + operation.KeyDigest[:32],
			RequestedBy:           operation.RequestedBy,
			ConfirmWorkspaceTrust: request.ConfirmWorkspaceTrust,
			ExpectedTrustDigest:   request.ExpectedTrustDigest})
		if createErr != nil {
			return StandardCodePresetResult{}, apperror.Normalize(createErr)
		}
		if created.Workspace == nil {
			base.TrustRequired, base.TrustDigest = created.TrustRequired,
				created.TrustDigest
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerWorkspaceUntrusted}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextConfirmWorkspaceTrust}
			return base, nil
		}
		if created.Trust == nil {
			return StandardCodePresetResult{}, apperror.New(
				apperror.CodeFailedPrecondition,
				"Standard Code Drydock omitted its Trust binding")
		}
		projection.Workspace = created.Workspace
		projection.Trust = created.Trust
		base.DrydockReady = true
	}
	commit, err := s.prepareCommit(ctx, operation, *projection.Workspace)
	if err != nil {
		return StandardCodePresetResult{}, err
	}
	stored, _, commitReplayed, err := s.store.CommitStandardCodePreset(ctx, commit)
	if err != nil {
		if operation.Status == domain.StandardCodePresetWaitingForPause &&
			(apperror.CodeOf(err) == apperror.CodeConflict ||
				apperror.CodeOf(err) == apperror.CodeFailedPrecondition) {
			base.Status = StandardCodeResultWaitingForPause
			base.BlockedBy = []CapabilityReadinessBlocker{CapabilityBlockerRunNotQuiescent}
			base.NextSteps = []StandardCodeNextStep{StandardCodeNextWaitForQuiescence}
			base.Replayed = replayed
			return base, nil
		}
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	return s.loadConfiguredResult(ctx, stored, base,
		replayed || commitReplayed)
}

func (s *StandardCodePresetService) prepareCommit(ctx context.Context,
	operation domain.StandardCodePresetOperation, workspace drydock.Workspace,
) (domain.StandardCodePresetCommit, error) {
	mode, err := s.store.GetRunMode(ctx, operation.RunID)
	if err != nil {
		return domain.StandardCodePresetCommit{}, apperror.Normalize(err)
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, operation.RunID)
	if err != nil {
		return domain.StandardCodePresetCommit{}, apperror.Normalize(err)
	}
	interaction, err := s.store.GetRunExecutionInteraction(ctx, operation.RunID)
	if err != nil {
		return domain.StandardCodePresetCommit{}, apperror.Normalize(err)
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, operation.RunID)
	if err != nil {
		return domain.StandardCodePresetCommit{}, apperror.Normalize(err)
	}
	cdp, err := s.store.GetRunBrowserCDPPermission(ctx, operation.RunID)
	if err != nil {
		return domain.StandardCodePresetCommit{}, apperror.Normalize(err)
	}
	now := s.now().UTC()
	for _, at := range []time.Time{mode.CreatedAt, profile.CreatedAt,
		interaction.CreatedAt, permission.CreatedAt, cdp.CreatedAt} {
		if now.Before(at) {
			now = at
		}
	}
	if mode.Surface != domain.ExecutionSurfaceCode {
		return domain.StandardCodePresetCommit{}, apperror.New(
			apperror.CodeConflict, "Standard Code Run Surface changed")
	}
	if mode.Phase != domain.ExecutionPhasePlan {
		mode, err = mode.Next(idgen.New("run-mode"), domain.ExecutionPhasePlan,
			operation.RequestedBy, "Standard Code preset enters Plan", now)
		if err != nil {
			return domain.StandardCodePresetCommit{}, err
		}
	}
	targetProfile := operation.SelectedBackend.ExecutionProfile()
	if profile.Profile != targetProfile {
		profile, err = profile.Next(idgen.New("run-exec-profile"), targetProfile,
			operation.RequestedBy, "Standard Code preset selected sandbox backend", now)
		if err != nil {
			return domain.StandardCodePresetCommit{}, err
		}
	}
	if interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.Surface != domain.ExecutionSurfaceCode ||
		interaction.ExecutionProfile != profile.Profile ||
		interaction.ExecutionProfileRevision != profile.Revision ||
		interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted {
		interaction, err = interaction.Next(idgen.New("run-exec-interaction"),
			domain.RunExecutionInteractionControlled, mode, profile,
			domain.WorkspaceTrustTrusted, true, operation.RequestedBy,
			"Standard Code preset selected controlled interaction", now)
		if err != nil {
			return domain.StandardCodePresetCommit{}, err
		}
	}
	if permission.Mode != domain.RunExecutionPermissionWorkspaceAccess {
		permission, err = permission.Next(idgen.New("run-exec-permission"),
			domain.RunExecutionPermissionWorkspaceAccess, true,
			operation.RequestedBy, "Standard Code preset selected Workspace Access", now)
		if err != nil {
			return domain.StandardCodePresetCommit{}, err
		}
	}
	if cdp.Mode != domain.RunBrowserCDPPermissionRestricted {
		cdp, err = cdp.Next(idgen.New("run-browser-cdp-permission"),
			domain.RunBrowserCDPPermissionRestricted, false,
			operation.RequestedBy, "Standard Code preset disabled Full CDP", now)
		if err != nil {
			return domain.StandardCodePresetCommit{}, err
		}
	}
	return domain.StandardCodePresetCommit{Operation: operation, Mode: mode,
		Profile: profile, Interaction: interaction, Permission: permission,
		BrowserCDP: cdp, DrydockID: workspace.ID,
		DrydockGeneration:   workspace.Generation,
		DrydockCheckpointID: workspace.LastCheckpointID, CommittedAt: now}, nil
}

func (s *StandardCodePresetService) loadConfiguredResult(ctx context.Context,
	operation domain.StandardCodePresetOperation, base StandardCodePresetResult,
	replayed bool,
) (StandardCodePresetResult, error) {
	run, err := s.store.GetRun(ctx, operation.RunID)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	mode, err := s.store.GetRunModeSnapshot(ctx, operation.ModeSnapshotID)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	profile, err := s.store.GetRunExecutionProfileSnapshot(ctx,
		operation.ProfileSnapshotID)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	interaction, err := s.store.GetRunExecutionInteractionSnapshot(ctx,
		operation.InteractionSnapshotID)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	permission, err := s.store.GetRunExecutionPermissionSnapshot(ctx,
		operation.PermissionSnapshotID)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	cdp, err := s.store.GetRunBrowserCDPPermissionSnapshot(ctx,
		operation.BrowserCDPSnapshotID)
	if err != nil {
		return StandardCodePresetResult{}, apperror.Normalize(err)
	}
	base.Status, base.RunID, base.WorkspaceID = StandardCodeResultConfigured,
		operation.RunID, operation.WorkspaceID
	base.Action, base.BackendIntent = operation.Action, operation.BackendIntent
	base.SelectedBackend, base.SelectionReason = operation.SelectedBackend,
		operation.SelectionReason
	base.Run, base.Mode, base.Profile, base.Interaction, base.Permission,
		base.BrowserCDP = &run, &mode, &profile, &interaction, &permission, &cdp
	base.BlockedBy, base.NextSteps = []CapabilityReadinessBlocker{},
		[]StandardCodeNextStep{}
	base.DrydockReady, base.Replayed, base.CapabilityGrant = true, replayed, false
	return base, nil
}

func (s *StandardCodePresetService) backendReadiness(
	backend domain.StandardCodeBackend,
) StandardCodeBackendReadiness {
	result := StandardCodeBackendReadiness{Backend: backend,
		BlockedBy:   []CapabilityReadinessBlocker{},
		Remediation: []CapabilityReadinessRemediation{}}
	add := func(blocker CapabilityReadinessBlocker) {
		for _, existing := range result.BlockedBy {
			if existing == blocker {
				return
			}
		}
		result.BlockedBy = append(result.BlockedBy, blocker)
		result.Remediation = append(result.Remediation,
			capabilityRemediationsByBlocker[blocker]...)
	}
	if !s.runtime.StandardCodePresetEnabled {
		add(CapabilityBlockerCapabilityUnimplemented)
	}
	if !s.runtime.RunControlEnabled ||
		!s.runtime.ExecutionPermissionControlEnabled ||
		!s.runtime.ExecutionPermissionCapabilities.Allows(
			domain.RunExecutionPermissionWorkspaceAccess) {
		add(CapabilityBlockerStartupGateClosed)
	}
	adapterBackend := CommandRuntimeLocalSandboxBackend
	if backend == domain.StandardCodeSelectedDocker {
		adapterBackend = CommandRuntimeDockerSandboxBackend
	}
	installed := false
	for _, adapter := range s.runtime.CommandRuntimeAdapters {
		if adapter.Backend == adapterBackend && adapter.Executable() &&
			commandRuntimeExecutionProfile(adapter) == backend.ExecutionProfile() &&
			commandRuntimePermission(adapter) ==
				domain.RunExecutionPermissionWorkspaceAccess {
			installed = true
			break
		}
	}
	if !installed {
		add(CapabilityBlockerBackendNotReady)
	}
	if backend == domain.StandardCodeSelectedLocal {
		if !s.runtime.LocalSandboxInstalled || !s.runtime.LocalSandboxProven {
			add(CapabilityBlockerSandboxUnproven)
		} else if !s.runtime.LocalBackendReady {
			add(CapabilityBlockerBackendNotReady)
		}
	} else if s.runtime.DockerReadiness != nil {
		switch s.runtime.DockerReadiness.ReasonCode {
		case sandbox.DockerReadinessReasonNone:
		case sandbox.DockerReadinessReasonFeatureDisabled:
			add(CapabilityBlockerStartupGateClosed)
		case sandbox.DockerReadinessReasonDaemonUnreachable:
			add(CapabilityBlockerDockerUnavailable)
		default:
			add(CapabilityBlockerBackendNotReady)
		}
	} else {
		if !s.runtime.DockerStartupGateEnabled {
			add(CapabilityBlockerStartupGateClosed)
		}
		if !s.runtime.DockerAvailable {
			add(CapabilityBlockerDockerUnavailable)
		} else if !s.runtime.DockerBackendReady {
			add(CapabilityBlockerBackendNotReady)
		}
	}
	sort.Slice(result.BlockedBy, func(i, j int) bool {
		return capabilityBlockerOrder[result.BlockedBy[i]] <
			capabilityBlockerOrder[result.BlockedBy[j]]
	})
	result.Remediation = uniqueSortedStandardCodeRemediations(result.Remediation)
	result.Available = len(result.BlockedBy) == 0
	return result
}

func (s *StandardCodePresetService) selectBackend(intent domain.StandardCodeBackendIntent,
	local, docker StandardCodeBackendReadiness,
) (domain.StandardCodeBackend, domain.StandardCodeSelectionReason,
	[]CapabilityReadinessBlocker) {
	switch intent {
	case domain.StandardCodeBackendAuto:
		if local.Available {
			return domain.StandardCodeSelectedLocal,
				domain.StandardCodeReasonAutoLocalReady, nil
		}
		return "", "", append([]CapabilityReadinessBlocker(nil), local.BlockedBy...)
	case domain.StandardCodeBackendLocal:
		if local.Available {
			return domain.StandardCodeSelectedLocal,
				domain.StandardCodeReasonExplicitLocal, nil
		}
		return "", "", append([]CapabilityReadinessBlocker(nil), local.BlockedBy...)
	case domain.StandardCodeBackendDocker:
		if docker.Available {
			return domain.StandardCodeSelectedDocker,
				domain.StandardCodeReasonExplicitDocker, nil
		}
		return "", "", append([]CapabilityReadinessBlocker(nil), docker.BlockedBy...)
	default:
		return "", "", []CapabilityReadinessBlocker{
			CapabilityBlockerBackendNotReady}
	}
}

func (s *StandardCodePresetService) backendAlternatives(
	intent domain.StandardCodeBackendIntent, local, docker StandardCodeBackendReadiness,
) []StandardCodeNextStep {
	steps := make([]StandardCodeNextStep, 0, 3)
	if intent != domain.StandardCodeBackendDocker && docker.Available {
		steps = append(steps, StandardCodeNextSelectDocker)
	}
	if s.runtime.ExecutionPermissionCapabilities.Allows(
		domain.RunExecutionPermissionApproval) {
		steps = append(steps, StandardCodeNextSelectApproval)
	}
	if len(steps) == 0 {
		steps = append(steps, StandardCodeNextRetryReadiness)
	}
	return steps
}

func normalizeStandardCodePresetRequest(request ConfigureStandardCodeRequest) (
	ConfigureStandardCodeRequest, domain.StandardCodeBackendIntent,
	domain.StandardCodePresetAction, error,
) {
	if request.Version != domain.StandardCodePresetProtocolVersion {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("unsupported Standard Code preset version")
	}
	request.RunID = strings.TrimSpace(request.RunID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	if !utf8.ValidString(request.Goal) || strings.ContainsRune(request.Goal, 0) {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("Standard Code goal must be valid UTF-8 without NUL bytes")
	}
	request.Goal = strings.TrimSpace(redact.String(request.Goal))
	if len([]byte(request.Goal)) > domain.MaxRunCreationGoalBytes {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("Standard Code goal must contain at most 4096 bytes")
	}
	request.RequestedBy = strings.TrimSpace(redact.String(request.RequestedBy))
	request.ExpectedTrustDigest = strings.TrimSpace(request.ExpectedTrustDigest)
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	for _, value := range []string{request.RunID, request.WorkspaceID} {
		if value != "" && (!domain.ValidAgentID(value) || strings.ContainsRune(value, 0)) {
			return ConfigureStandardCodeRequest{}, "", "",
				errors.New("Standard Code Run and Workspace ids must be bounded")
		}
	}
	if !domain.ValidAgentID(request.RequestedBy) ||
		strings.ContainsRune(request.RequestedBy, 0) {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("Standard Code requester is invalid")
	}
	switch strings.ToLower(request.RequestedBy) {
	case "agent", "llm", "model", "repository", "repo", "skill", "config",
		"configuration", "project_config", "recovery", "recovery_data", "mcp",
		"plugin", "hook":
		return ConfigureStandardCodeRequest{}, "", "", errors.New(
			"models, agents, Skills, MCP, plugins, and repository configuration cannot invoke Standard Code")
	}
	originalKey := request.OperationKey
	request.OperationKey = strings.TrimSpace(request.OperationKey)
	if originalKey != request.OperationKey || !utf8.ValidString(request.OperationKey) {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("Standard Code operation key must be normalized UTF-8")
	}
	if _, err := domain.NormalizeAgentOperationKey(request.OperationKey); err != nil {
		return ConfigureStandardCodeRequest{}, "", "", err
	}
	for _, current := range request.OperationKey {
		if unicode.IsSpace(current) || unicode.IsControl(current) {
			return ConfigureStandardCodeRequest{}, "", "",
				errors.New("Standard Code operation key cannot contain whitespace")
		}
	}
	intent, err := domain.ParseStandardCodeBackendIntent(request.BackendIntent)
	if err != nil {
		return ConfigureStandardCodeRequest{}, "", "", err
	}
	action := domain.StandardCodePresetAction(strings.ToLower(strings.TrimSpace(request.Action)))
	if !action.Valid() {
		return ConfigureStandardCodeRequest{}, "", "",
			fmt.Errorf("unsupported Standard Code action %q", request.Action)
	}
	if action == domain.StandardCodePresetPauseAndConfigure && request.RunID == "" {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("pause-and-configure requires an existing Run")
	}
	request.BackendIntent, request.Action = string(intent), string(action)
	if request.ExpectedTrustDigest != "" &&
		(!validApplicationDigest(request.ExpectedTrustDigest) ||
			!request.ConfirmWorkspaceTrust) {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("Standard Code trust digest requires exact confirmation")
	}
	if request.ConfirmWorkspaceTrust && request.ExpectedTrustDigest == "" {
		return ConfigureStandardCodeRequest{}, "", "",
			errors.New("Standard Code trust confirmation requires its reviewed digest")
	}
	return request, intent, action, nil
}

func standardCodePresetRequestFingerprint(request ConfigureStandardCodeRequest,
	intent domain.StandardCodeBackendIntent, action domain.StandardCodePresetAction,
) string {
	return runmutation.Fingerprint("standard_code_preset_request.v1", request.RunID,
		request.WorkspaceID, request.Goal, string(intent), string(action),
		fmt.Sprintf("%t", request.ConfirmWorkspaceTrust), request.ExpectedTrustDigest,
		request.RequestedBy)
}

func samePresetApplicationIntent(existing,
	requested domain.StandardCodePresetOperation) error {
	if existing.ProtocolVersion != requested.ProtocolVersion ||
		existing.KeyDigest != requested.KeyDigest ||
		existing.RequestFingerprint != requested.RequestFingerprint ||
		existing.RequestedRunID != requested.RequestedRunID ||
		existing.Action != requested.Action ||
		existing.BackendIntent != requested.BackendIntent ||
		existing.RequestedBy != requested.RequestedBy {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset operation key was already used for different intent")
	}
	return nil
}

func uniqueSortedStandardCodeRemediations(
	values []CapabilityReadinessRemediation,
) []CapabilityReadinessRemediation {
	seen := make(map[CapabilityReadinessRemediation]struct{}, len(values))
	result := make([]CapabilityReadinessRemediation, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return capabilityRemediationOrder[result[i]] < capabilityRemediationOrder[result[j]]
	})
	return result
}

func validApplicationDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, current := range value {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}
