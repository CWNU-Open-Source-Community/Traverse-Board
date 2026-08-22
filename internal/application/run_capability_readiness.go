package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

const RunCapabilityReadinessProtocolVersion = "run_capability_readiness.v1"

type CapabilityReadinessBlocker string

const (
	CapabilityBlockerRunNotQuiescent         CapabilityReadinessBlocker = "run_not_quiescent"
	CapabilityBlockerExecutionLeaseActive    CapabilityReadinessBlocker = "execution_lease_active"
	CapabilityBlockerStartupGateClosed       CapabilityReadinessBlocker = "startup_gate_closed"
	CapabilityBlockerCapabilityUnimplemented CapabilityReadinessBlocker = "capability_not_implemented"
	CapabilityBlockerBackendNotReady         CapabilityReadinessBlocker = "backend_not_ready"
	CapabilityBlockerSurfaceMismatch         CapabilityReadinessBlocker = "surface_mismatch"
	CapabilityBlockerProfileMismatch         CapabilityReadinessBlocker = "profile_mismatch"
	CapabilityBlockerPermissionMismatch      CapabilityReadinessBlocker = "permission_mismatch"
	CapabilityBlockerWorkspaceUntrusted      CapabilityReadinessBlocker = "workspace_untrusted"
	CapabilityBlockerSandboxUnproven         CapabilityReadinessBlocker = "sandbox_unproven"
	CapabilityBlockerDockerUnavailable       CapabilityReadinessBlocker = "docker_unavailable"
)

type CapabilityReadinessRemediation string

const (
	CapabilityRemediationPauseRun                 CapabilityReadinessRemediation = "pause_run"
	CapabilityRemediationCreateNewRun             CapabilityReadinessRemediation = "create_new_run"
	CapabilityRemediationWaitForExecutionLease    CapabilityReadinessRemediation = "wait_for_execution_lease"
	CapabilityRemediationRestartWithStartupGate   CapabilityReadinessRemediation = "restart_with_startup_gate"
	CapabilityRemediationUpgradeApplication       CapabilityReadinessRemediation = "upgrade_application"
	CapabilityRemediationRetryBackendReadiness    CapabilityReadinessRemediation = "retry_backend_readiness"
	CapabilityRemediationSelectRequiredSurface    CapabilityReadinessRemediation = "select_required_surface"
	CapabilityRemediationSelectRequiredProfile    CapabilityReadinessRemediation = "select_required_profile"
	CapabilityRemediationSelectRequiredPermission CapabilityReadinessRemediation = "select_required_permission"
	CapabilityRemediationTrustWorkspace           CapabilityReadinessRemediation = "trust_workspace"
	CapabilityRemediationVerifySandbox            CapabilityReadinessRemediation = "verify_sandbox"
	CapabilityRemediationInstallOrStartDocker     CapabilityReadinessRemediation = "install_or_start_docker"
)

const StandardCodePresetValue = "standard_code"

var capabilityBlockerOrder = map[CapabilityReadinessBlocker]int{
	CapabilityBlockerRunNotQuiescent: 0, CapabilityBlockerExecutionLeaseActive: 1,
	CapabilityBlockerStartupGateClosed: 2, CapabilityBlockerCapabilityUnimplemented: 3,
	CapabilityBlockerSurfaceMismatch: 4, CapabilityBlockerProfileMismatch: 5,
	CapabilityBlockerPermissionMismatch: 6, CapabilityBlockerWorkspaceUntrusted: 7,
	CapabilityBlockerSandboxUnproven: 8, CapabilityBlockerDockerUnavailable: 9,
	CapabilityBlockerBackendNotReady: 10,
}

var capabilityRemediationOrder = map[CapabilityReadinessRemediation]int{
	CapabilityRemediationPauseRun: 0, CapabilityRemediationCreateNewRun: 1,
	CapabilityRemediationWaitForExecutionLease:    2,
	CapabilityRemediationRestartWithStartupGate:   3,
	CapabilityRemediationUpgradeApplication:       4,
	CapabilityRemediationSelectRequiredSurface:    5,
	CapabilityRemediationSelectRequiredProfile:    6,
	CapabilityRemediationSelectRequiredPermission: 7,
	CapabilityRemediationTrustWorkspace:           8, CapabilityRemediationVerifySandbox: 9,
	CapabilityRemediationInstallOrStartDocker:  10,
	CapabilityRemediationRetryBackendReadiness: 11,
}

var capabilityRemediationsByBlocker = map[CapabilityReadinessBlocker][]CapabilityReadinessRemediation{
	CapabilityBlockerRunNotQuiescent:         {CapabilityRemediationPauseRun, CapabilityRemediationCreateNewRun},
	CapabilityBlockerExecutionLeaseActive:    {CapabilityRemediationWaitForExecutionLease},
	CapabilityBlockerStartupGateClosed:       {CapabilityRemediationRestartWithStartupGate},
	CapabilityBlockerCapabilityUnimplemented: {CapabilityRemediationUpgradeApplication},
	CapabilityBlockerSurfaceMismatch:         {CapabilityRemediationSelectRequiredSurface},
	CapabilityBlockerProfileMismatch:         {CapabilityRemediationSelectRequiredProfile},
	CapabilityBlockerPermissionMismatch:      {CapabilityRemediationSelectRequiredPermission},
	CapabilityBlockerWorkspaceUntrusted:      {CapabilityRemediationTrustWorkspace},
	CapabilityBlockerSandboxUnproven:         {CapabilityRemediationVerifySandbox},
	CapabilityBlockerDockerUnavailable:       {CapabilityRemediationInstallOrStartDocker},
	CapabilityBlockerBackendNotReady:         {CapabilityRemediationRetryBackendReadiness},
}

var capabilityRuntimeFailureBlockers = map[CapabilityReadinessBlocker]struct{}{
	CapabilityBlockerCapabilityUnimplemented: {}, CapabilityBlockerSurfaceMismatch: {},
	CapabilityBlockerProfileMismatch: {}, CapabilityBlockerPermissionMismatch: {},
	CapabilityBlockerWorkspaceUntrusted: {}, CapabilityBlockerSandboxUnproven: {},
	CapabilityBlockerDockerUnavailable: {}, CapabilityBlockerBackendNotReady: {},
}

type CapabilityReadinessOption struct {
	Value            string                           `json:"value"`
	Selected         bool                             `json:"selected"`
	Selectable       bool                             `json:"selectable"`
	RuntimeAvailable bool                             `json:"runtime_available"`
	BlockedBy        []CapabilityReadinessBlocker     `json:"blocked_by"`
	Remediation      []CapabilityReadinessRemediation `json:"remediation"`
	RestartRequired  bool                             `json:"restart_required"`
}

type RunCapabilityReadiness struct {
	ProtocolVersion       string                      `json:"protocol_version"`
	RunID                 string                      `json:"run_id"`
	Permissions           []CapabilityReadinessOption `json:"permissions"`
	Profiles              []CapabilityReadinessOption `json:"profiles"`
	Interactions          []CapabilityReadinessOption `json:"interactions"`
	BrowserCDPPermissions []CapabilityReadinessOption `json:"browser_cdp_permissions"`
	Presets               []CapabilityReadinessOption `json:"presets"`
	CapabilityGrant       bool                        `json:"capability_grant"`
}

type CapabilityReadinessRuntime struct {
	RunControlEnabled                  bool
	ExecutionPermissionControlEnabled  bool
	BrowserCDPPermissionControlEnabled bool
	StandardCodePresetEnabled          bool
	ExecutionPermissionCapabilities    domain.ExecutionPermissionRuntimeCapabilities
	BrowserCDPPermissionCapabilities   domain.BrowserCDPPermissionRuntimeCapabilities
	LocalSandboxInstalled              bool
	LocalSandboxProven                 bool
	LocalBackendReady                  bool
	DockerStartupGateEnabled           bool
	DockerAvailable                    bool
	DockerBackendReady                 bool
	DockerReadiness                    *sandbox.DockerReadiness
	BrowserBackendReady                bool
}

func (r CapabilityReadinessRuntime) Validate() error {
	if err := r.ExecutionPermissionCapabilities.Validate(); err != nil {
		return fmt.Errorf("execution permission capabilities: %w", err)
	}
	if err := r.BrowserCDPPermissionCapabilities.Validate(); err != nil {
		return fmt.Errorf("browser CDP capabilities: %w", err)
	}
	if r.BrowserCDPPermissionCapabilities.ControlEnabled !=
		r.BrowserCDPPermissionControlEnabled {
		return errors.New("browser CDP control and runtime capability must match")
	}
	if r.LocalSandboxProven && !r.LocalSandboxInstalled {
		return errors.New("a proven local sandbox must be installed")
	}
	if r.LocalBackendReady && (!r.LocalSandboxInstalled || !r.LocalSandboxProven ||
		!r.ExecutionPermissionCapabilities.WorkspaceSandboxEnabled) {
		return errors.New("a ready local backend requires the Workspace sandbox gate and proof")
	}
	if r.DockerBackendReady && (!r.DockerStartupGateEnabled || !r.DockerAvailable) {
		return errors.New("a ready Docker backend requires its startup gate and installation")
	}
	if r.DockerReadiness != nil {
		if r.DockerReadiness.Validate() != nil ||
			r.DockerReadiness.FeatureEnabled != r.DockerStartupGateEnabled ||
			r.DockerAvailable != r.DockerReadiness.DaemonReachable ||
			r.DockerBackendReady != r.DockerReadiness.Ready {
			return errors.New("detailed Docker readiness does not match runtime facts")
		}
	}
	if r.BrowserBackendReady && !r.BrowserCDPPermissionControlEnabled {
		return errors.New("a ready browser backend requires restricted CDP control")
	}
	if r.StandardCodePresetEnabled && (!r.RunControlEnabled ||
		!r.ExecutionPermissionControlEnabled || !r.BrowserCDPPermissionControlEnabled) {
		return errors.New("Standard Code preset control requires all component controls")
	}
	return nil
}

type RunCapabilityReadinessStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionProfile(context.Context, string) (domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
	GetRunBrowserCDPPermission(context.Context, string) (domain.RunBrowserCDPPermissionSnapshot, error)
	GetRunExecutionInteraction(context.Context, string) (domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionLease(context.Context, string) (domain.RunExecutionLease, bool, error)
}

type RunCapabilityReadinessService struct {
	store   RunCapabilityReadinessStore
	runtime CapabilityReadinessRuntime
	now     func() time.Time
}

func NewRunCapabilityReadinessService(store RunCapabilityReadinessStore,
	runtime CapabilityReadinessRuntime,
) *RunCapabilityReadinessService {
	return &RunCapabilityReadinessService{store: store, runtime: runtime,
		now: func() time.Time { return time.Now().UTC() }}
}

func (s *RunCapabilityReadinessService) Project(ctx context.Context,
	runID string,
) (RunCapabilityReadiness, error) {
	if s == nil || s.store == nil || s.now == nil {
		return RunCapabilityReadiness{}, apperror.New(apperror.CodeFailedPrecondition,
			"Run capability readiness service is unavailable")
	}
	if err := s.runtime.Validate(); err != nil {
		return RunCapabilityReadiness{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"Run capability readiness runtime is invalid", err)
	}
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return RunCapabilityReadiness{}, apperror.New(apperror.CodeInvalidArgument,
			"Run capability readiness Run id is invalid")
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	mode, err := s.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	cdp, err := s.store.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	interaction, err := s.store.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	lease, found, err := s.store.GetRunExecutionLease(ctx, run.ID)
	if err != nil {
		return RunCapabilityReadiness{}, apperror.Normalize(err)
	}
	if err := validateCapabilityReadinessInputs(run, mode, profile, permission, cdp,
		interaction, lease, found); err != nil {
		return RunCapabilityReadiness{}, apperror.Wrap(apperror.CodeInternal,
			"Run capability readiness source facts are invalid", err)
	}
	activeLease := found && lease.ActiveAt(s.now())
	value := capabilityReadinessProjection{run: run, mode: mode, profile: profile,
		permission: permission, cdp: cdp, interaction: interaction,
		activeLease: activeLease, runtime: s.runtime}.project()
	if err := value.Validate(); err != nil {
		return RunCapabilityReadiness{}, apperror.Wrap(apperror.CodeInternal,
			"Run capability readiness projection is invalid", err)
	}
	return value, nil
}

func validateCapabilityReadinessInputs(run domain.Run, mode domain.RunModeSnapshot,
	profile domain.RunExecutionProfileSnapshot,
	permission domain.RunExecutionPermissionSnapshot,
	cdp domain.RunBrowserCDPPermissionSnapshot,
	interaction domain.RunExecutionInteractionSnapshot,
	lease domain.RunExecutionLease, leaseFound bool,
) error {
	if err := run.Validate(); err != nil {
		return err
	}
	for _, value := range []struct {
		runID, missionID string
		err              error
	}{
		{mode.RunID, mode.MissionID, mode.Validate()},
		{profile.RunID, profile.MissionID, profile.Validate()},
		{permission.RunID, permission.MissionID, permission.Validate()},
		{cdp.RunID, cdp.MissionID, cdp.Validate()},
		{interaction.RunID, interaction.MissionID, interaction.Validate()},
	} {
		if value.err != nil || value.runID != run.ID || value.missionID != run.MissionID {
			return errors.New("Run capability readiness snapshots do not match the Run")
		}
	}
	if leaseFound && (lease.Validate() != nil || lease.RunID != run.ID) {
		return errors.New("Run capability readiness lease does not match the Run")
	}
	return nil
}

type capabilityReadinessProjection struct {
	run         domain.Run
	mode        domain.RunModeSnapshot
	profile     domain.RunExecutionProfileSnapshot
	permission  domain.RunExecutionPermissionSnapshot
	cdp         domain.RunBrowserCDPPermissionSnapshot
	interaction domain.RunExecutionInteractionSnapshot
	activeLease bool
	runtime     CapabilityReadinessRuntime
}

func (p capabilityReadinessProjection) project() RunCapabilityReadiness {
	return RunCapabilityReadiness{
		ProtocolVersion: RunCapabilityReadinessProtocolVersion, RunID: p.run.ID,
		Permissions: p.permissionOptions(), Profiles: p.profileOptions(),
		Interactions:          p.interactionOptions(),
		BrowserCDPPermissions: p.browserCDPOptions(), Presets: p.presetOptions(),
		CapabilityGrant: false,
	}
}

func (p capabilityReadinessProjection) permissionOptions() []CapabilityReadinessOption {
	modes := []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionConservative,
		domain.RunExecutionPermissionWorkspaceAccess,
		domain.RunExecutionPermissionApproval,
		domain.RunExecutionPermissionFullAccess,
		domain.RunExecutionPermissionDebug,
	}
	options := make([]CapabilityReadinessOption, 0, len(modes))
	for _, target := range modes {
		builder := newReadinessOption(string(target), p.permission.Mode == target)
		selectable := p.runQuiescent()
		p.addRunStateBlocker(builder)
		gateAvailable := p.runtime.ExecutionPermissionCapabilities.Allows(target)
		if !p.runtime.ExecutionPermissionControlEnabled || !gateAvailable {
			selectable = false
			builder.add(CapabilityBlockerStartupGateClosed,
				CapabilityRemediationRestartWithStartupGate)
		}
		runtimeAvailable := gateAvailable
		if target == domain.RunExecutionPermissionConservative {
			runtimeAvailable = true
		}
		if target == domain.RunExecutionPermissionWorkspaceAccess &&
			!p.anyWorkspaceSandboxReady() {
			runtimeAvailable = false
			builder.add(CapabilityBlockerSandboxUnproven,
				CapabilityRemediationVerifySandbox)
		}
		options = append(options, builder.finish(selectable, runtimeAvailable))
	}
	return options
}

func (p capabilityReadinessProjection) profileOptions() []CapabilityReadinessOption {
	profiles := []domain.RunExecutionProfile{domain.RunExecutionProfilePreview,
		domain.RunExecutionProfileDocker, domain.RunExecutionProfileLocal}
	options := make([]CapabilityReadinessOption, 0, len(profiles))
	for _, target := range profiles {
		builder := newReadinessOption(string(target), p.profile.Profile == target)
		selectable := p.runQuiescent() && !p.activeLease && p.runtime.RunControlEnabled
		p.addRunStateBlocker(builder)
		if p.activeLease {
			builder.add(CapabilityBlockerExecutionLeaseActive,
				CapabilityRemediationWaitForExecutionLease)
		}
		if !p.runtime.RunControlEnabled {
			builder.add(CapabilityBlockerStartupGateClosed,
				CapabilityRemediationRestartWithStartupGate)
		}
		runtimeAvailable := true
		switch target {
		case domain.RunExecutionProfileLocal:
			runtimeAvailable = p.addLocalBackendBlockers(builder)
		case domain.RunExecutionProfileDocker:
			runtimeAvailable = p.addDockerBackendBlockers(builder)
		}
		if target != domain.RunExecutionProfilePreview &&
			p.interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted {
			runtimeAvailable = false
			builder.add(CapabilityBlockerWorkspaceUntrusted,
				CapabilityRemediationTrustWorkspace)
		}
		options = append(options, builder.finish(selectable, runtimeAvailable))
	}
	return options
}

func (p capabilityReadinessProjection) interactionOptions() []CapabilityReadinessOption {
	modes := []domain.RunExecutionInteractionMode{domain.RunExecutionInteractionPreview,
		domain.RunExecutionInteractionControlled, domain.RunExecutionInteractionDebug,
		domain.RunExecutionInteractionCyber}
	options := make([]CapabilityReadinessOption, 0, len(modes))
	for _, target := range modes {
		builder := newReadinessOption(string(target), p.interaction.Mode == target)
		selectable := p.runQuiescent() && !p.activeLease && p.runtime.RunControlEnabled
		p.addRunStateBlocker(builder)
		if p.activeLease {
			builder.add(CapabilityBlockerExecutionLeaseActive,
				CapabilityRemediationWaitForExecutionLease)
		}
		if !p.runtime.RunControlEnabled {
			builder.add(CapabilityBlockerStartupGateClosed,
				CapabilityRemediationRestartWithStartupGate)
		}
		runtimeAvailable := true
		if target != domain.RunExecutionInteractionPreview {
			expectedSurface, expectedProfile := domain.ExecutionSurfaceCode,
				domain.RunExecutionProfileLocal
			if target == domain.RunExecutionInteractionCyber {
				expectedSurface, expectedProfile = domain.ExecutionSurfaceCyber,
					domain.RunExecutionProfileDocker
			}
			if p.mode.Surface != expectedSurface {
				selectable, runtimeAvailable = false, false
				builder.add(CapabilityBlockerSurfaceMismatch,
					CapabilityRemediationSelectRequiredSurface)
			}
			if p.profile.Profile != expectedProfile {
				selectable, runtimeAvailable = false, false
				builder.add(CapabilityBlockerProfileMismatch,
					CapabilityRemediationSelectRequiredProfile)
			}
			if p.interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted {
				runtimeAvailable = false
				builder.add(CapabilityBlockerWorkspaceUntrusted,
					CapabilityRemediationTrustWorkspace)
			}
			if target == domain.RunExecutionInteractionCyber {
				if !p.addDockerBackendBlockers(builder) {
					runtimeAvailable = false
				}
			} else if !p.addLocalBackendBlockers(builder) {
				runtimeAvailable = false
			}
		}
		if target == domain.RunExecutionInteractionDebug {
			if p.permission.Mode != domain.RunExecutionPermissionDebug {
				runtimeAvailable = false
				builder.add(CapabilityBlockerPermissionMismatch,
					CapabilityRemediationSelectRequiredPermission)
			}
			if !p.runtime.ExecutionPermissionCapabilities.DebugMaximumAccessEnabled {
				runtimeAvailable = false
				builder.add(CapabilityBlockerStartupGateClosed,
					CapabilityRemediationRestartWithStartupGate)
			}
		}
		options = append(options, builder.finish(selectable, runtimeAvailable))
	}
	return options
}

func (p capabilityReadinessProjection) browserCDPOptions() []CapabilityReadinessOption {
	modes := []domain.RunBrowserCDPPermissionMode{
		domain.RunBrowserCDPPermissionRestricted, domain.RunBrowserCDPPermissionFullDebug}
	options := make([]CapabilityReadinessOption, 0, len(modes))
	for _, target := range modes {
		builder := newReadinessOption(string(target), p.cdp.Mode == target)
		selectable := p.runQuiescent() && !p.activeLease &&
			p.runtime.BrowserCDPPermissionCapabilities.Allows(target)
		p.addRunStateBlocker(builder)
		if p.activeLease {
			builder.add(CapabilityBlockerExecutionLeaseActive,
				CapabilityRemediationWaitForExecutionLease)
		}
		gateAvailable := p.runtime.BrowserCDPPermissionControlEnabled &&
			p.runtime.BrowserCDPPermissionCapabilities.Allows(target)
		if !gateAvailable {
			selectable = false
			builder.add(CapabilityBlockerStartupGateClosed,
				CapabilityRemediationRestartWithStartupGate)
		}
		runtimeAvailable := gateAvailable && p.runtime.BrowserBackendReady
		if gateAvailable && !p.runtime.BrowserBackendReady {
			builder.add(CapabilityBlockerBackendNotReady,
				CapabilityRemediationRetryBackendReadiness)
		}
		if target == domain.RunBrowserCDPPermissionFullDebug &&
			p.permission.Mode != domain.RunExecutionPermissionDebug {
			selectable, runtimeAvailable = false, false
			builder.add(CapabilityBlockerPermissionMismatch,
				CapabilityRemediationSelectRequiredPermission)
		}
		options = append(options, builder.finish(selectable, runtimeAvailable))
	}
	return options
}

func (p capabilityReadinessProjection) presetOptions() []CapabilityReadinessOption {
	selected := p.mode.Surface == domain.ExecutionSurfaceCode &&
		p.profile.Profile == domain.RunExecutionProfileLocal &&
		p.interaction.Mode == domain.RunExecutionInteractionControlled &&
		p.permission.Mode == domain.RunExecutionPermissionWorkspaceAccess &&
		p.cdp.Mode == domain.RunBrowserCDPPermissionRestricted
	builder := newReadinessOption(StandardCodePresetValue, selected)
	selectable := p.runtime.StandardCodePresetEnabled && p.runQuiescent() && !p.activeLease
	p.addRunStateBlocker(builder)
	if p.activeLease {
		builder.add(CapabilityBlockerExecutionLeaseActive,
			CapabilityRemediationWaitForExecutionLease)
	}
	if !p.runtime.StandardCodePresetEnabled {
		builder.add(CapabilityBlockerCapabilityUnimplemented,
			CapabilityRemediationUpgradeApplication)
	}
	runtimeAvailable := p.runtime.StandardCodePresetEnabled
	if !p.addLocalBackendBlockers(builder) {
		runtimeAvailable = false
	}
	if p.interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted {
		runtimeAvailable = false
		builder.add(CapabilityBlockerWorkspaceUntrusted,
			CapabilityRemediationTrustWorkspace)
	}
	if !p.runtime.BrowserCDPPermissionCapabilities.ControlEnabled ||
		!p.runtime.BrowserBackendReady {
		runtimeAvailable = false
		if !p.runtime.BrowserCDPPermissionCapabilities.ControlEnabled {
			builder.add(CapabilityBlockerStartupGateClosed,
				CapabilityRemediationRestartWithStartupGate)
		} else {
			builder.add(CapabilityBlockerBackendNotReady,
				CapabilityRemediationRetryBackendReadiness)
		}
	}
	return []CapabilityReadinessOption{builder.finish(selectable, runtimeAvailable)}
}

func (p capabilityReadinessProjection) addLocalBackendBlockers(
	builder *capabilityReadinessOptionBuilder,
) bool {
	ready := true
	if !p.runtime.ExecutionPermissionCapabilities.WorkspaceSandboxEnabled {
		ready = false
		builder.add(CapabilityBlockerStartupGateClosed,
			CapabilityRemediationRestartWithStartupGate)
	}
	if !p.runtime.LocalSandboxInstalled || !p.runtime.LocalSandboxProven {
		ready = false
		builder.add(CapabilityBlockerSandboxUnproven,
			CapabilityRemediationVerifySandbox)
	} else if !p.runtime.LocalBackendReady {
		ready = false
		builder.add(CapabilityBlockerBackendNotReady,
			CapabilityRemediationRetryBackendReadiness)
	}
	return ready
}

func (p capabilityReadinessProjection) addDockerBackendBlockers(
	builder *capabilityReadinessOptionBuilder,
) bool {
	if p.runtime.DockerReadiness != nil {
		switch p.runtime.DockerReadiness.ReasonCode {
		case sandbox.DockerReadinessReasonNone:
			return true
		case sandbox.DockerReadinessReasonFeatureDisabled:
			builder.add(CapabilityBlockerStartupGateClosed,
				CapabilityRemediationRestartWithStartupGate)
		case sandbox.DockerReadinessReasonDaemonUnreachable:
			builder.add(CapabilityBlockerDockerUnavailable,
				CapabilityRemediationInstallOrStartDocker)
		default:
			builder.add(CapabilityBlockerBackendNotReady,
				CapabilityRemediationRetryBackendReadiness)
		}
		return false
	}
	ready := true
	if !p.runtime.DockerStartupGateEnabled {
		ready = false
		builder.add(CapabilityBlockerStartupGateClosed,
			CapabilityRemediationRestartWithStartupGate)
	}
	if !p.runtime.DockerAvailable {
		ready = false
		builder.add(CapabilityBlockerDockerUnavailable,
			CapabilityRemediationInstallOrStartDocker)
	} else if !p.runtime.DockerBackendReady {
		ready = false
		builder.add(CapabilityBlockerBackendNotReady,
			CapabilityRemediationRetryBackendReadiness)
	}
	return ready
}

func (p capabilityReadinessProjection) anyWorkspaceSandboxReady() bool {
	return p.runtime.LocalBackendReady || p.runtime.DockerBackendReady
}

func (p capabilityReadinessProjection) runQuiescent() bool {
	return p.run.Status == domain.RunCreated || p.run.Status == domain.RunPaused
}

func (p capabilityReadinessProjection) addRunStateBlocker(
	builder *capabilityReadinessOptionBuilder,
) {
	if p.runQuiescent() {
		return
	}
	remediation := CapabilityRemediationPauseRun
	if p.run.Terminal() || p.run.Status == domain.RunPreparing {
		remediation = CapabilityRemediationCreateNewRun
	}
	builder.add(CapabilityBlockerRunNotQuiescent, remediation)
}

type capabilityReadinessOptionBuilder struct {
	value       string
	selected    bool
	blockedBy   map[CapabilityReadinessBlocker]struct{}
	remediation map[CapabilityReadinessRemediation]struct{}
}

func newReadinessOption(value string, selected bool) *capabilityReadinessOptionBuilder {
	return &capabilityReadinessOptionBuilder{value: value, selected: selected,
		blockedBy:   make(map[CapabilityReadinessBlocker]struct{}),
		remediation: make(map[CapabilityReadinessRemediation]struct{})}
}

func (b *capabilityReadinessOptionBuilder) add(blocker CapabilityReadinessBlocker,
	remediation CapabilityReadinessRemediation,
) {
	b.blockedBy[blocker] = struct{}{}
	b.remediation[remediation] = struct{}{}
}

func (b *capabilityReadinessOptionBuilder) finish(selectable, runtimeAvailable bool) CapabilityReadinessOption {
	blockedBy := make([]CapabilityReadinessBlocker, 0, len(b.blockedBy))
	for value := range b.blockedBy {
		blockedBy = append(blockedBy, value)
	}
	sort.Slice(blockedBy, func(i, j int) bool {
		return capabilityBlockerOrder[blockedBy[i]] < capabilityBlockerOrder[blockedBy[j]]
	})
	remediation := make([]CapabilityReadinessRemediation, 0, len(b.remediation))
	for value := range b.remediation {
		remediation = append(remediation, value)
	}
	sort.Slice(remediation, func(i, j int) bool {
		return capabilityRemediationOrder[remediation[i]] <
			capabilityRemediationOrder[remediation[j]]
	})
	restartRequired := false
	for _, blocker := range blockedBy {
		if blocker == CapabilityBlockerStartupGateClosed {
			restartRequired = true
			break
		}
	}
	return CapabilityReadinessOption{Value: b.value, Selected: b.selected,
		Selectable: selectable, RuntimeAvailable: runtimeAvailable,
		BlockedBy: blockedBy, Remediation: remediation,
		RestartRequired: restartRequired}
}

func (r RunCapabilityReadiness) Validate() error {
	if r.ProtocolVersion != RunCapabilityReadinessProtocolVersion ||
		!domain.ValidAgentID(r.RunID) || r.CapabilityGrant {
		return errors.New("Run capability readiness envelope is invalid")
	}
	groups := []struct {
		name        string
		options     []CapabilityReadinessOption
		expected    []string
		maxSelected int
	}{
		{"permissions", r.Permissions, []string{"conservative", "workspace_access", "approval", "full_access", "debug"}, 1},
		{"profiles", r.Profiles, []string{"preview", "docker", "local"}, 1},
		{"interactions", r.Interactions, []string{"preview", "controlled", "debug", "cyber"}, 1},
		{"browser CDP permissions", r.BrowserCDPPermissions, []string{"restricted", "full_debug"}, 1},
		{"presets", r.Presets, []string{StandardCodePresetValue}, 1},
	}
	for _, group := range groups {
		if len(group.options) != len(group.expected) {
			return fmt.Errorf("Run capability readiness %s are incomplete", group.name)
		}
		selected := 0
		for index, option := range group.options {
			if option.Value != group.expected[index] || option.Validate() != nil {
				return fmt.Errorf("Run capability readiness %s option is invalid", group.name)
			}
			if option.Selected {
				selected++
			}
		}
		if selected > group.maxSelected || group.name != "presets" && selected != 1 {
			return fmt.Errorf("Run capability readiness %s selection is invalid", group.name)
		}
	}
	return nil
}

func (o CapabilityReadinessOption) Validate() error {
	if strings.TrimSpace(o.Value) == "" || len(o.BlockedBy) > len(capabilityBlockerOrder) ||
		len(o.Remediation) > len(capabilityRemediationOrder) {
		return errors.New("capability readiness option is invalid")
	}
	last := -1
	for _, value := range o.BlockedBy {
		order, ok := capabilityBlockerOrder[value]
		if !ok || order <= last {
			return errors.New("capability readiness blockers are invalid")
		}
		last = order
	}
	last = -1
	for _, value := range o.Remediation {
		order, ok := capabilityRemediationOrder[value]
		if !ok || order <= last {
			return errors.New("capability readiness remediation is invalid")
		}
		last = order
	}
	hasStartupBlocker := false
	for _, value := range o.BlockedBy {
		hasStartupBlocker = hasStartupBlocker || value == CapabilityBlockerStartupGateClosed
		if o.RuntimeAvailable {
			if _, runtimeFailure := capabilityRuntimeFailureBlockers[value]; runtimeFailure {
				return errors.New("capability readiness runtime disposition is invalid")
			}
		}
		if !containsReadinessRemediation(o.Remediation,
			capabilityRemediationsByBlocker[value]...) {
			return errors.New("capability readiness remediation is incomplete")
		}
	}
	for _, remediation := range o.Remediation {
		matched := false
		for _, blocker := range o.BlockedBy {
			if containsReadinessRemediation([]CapabilityReadinessRemediation{remediation},
				capabilityRemediationsByBlocker[blocker]...) {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("capability readiness remediation is unrelated")
		}
	}
	if o.RestartRequired != hasStartupBlocker || len(o.BlockedBy) == 0 != (len(o.Remediation) == 0) {
		return errors.New("capability readiness disposition is invalid")
	}
	return nil
}

func containsReadinessRemediation(values []CapabilityReadinessRemediation,
	want ...CapabilityReadinessRemediation,
) bool {
	for _, value := range values {
		for _, candidate := range want {
			if value == candidate {
				return true
			}
		}
	}
	return false
}
