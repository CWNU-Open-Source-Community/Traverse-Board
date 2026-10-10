package application

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/sandbox"
)

const SandboxEnvironmentVersion = "sandbox_environment.v1"

type SandboxEnvironmentSettings struct {
	DefaultBackend    string `json:"default_backend"`
	DockerEnabled     bool   `json:"docker_enabled"`
	DockerImageDigest string `json:"docker_image_digest"`
	SBXEnabled        bool   `json:"sbx_enabled"`
	SBXTemplate       string `json:"sbx_template"`
}

func DefaultSandboxEnvironmentSettings() SandboxEnvironmentSettings {
	return SandboxEnvironmentSettings{DefaultBackend: "local"}
}

func (settings SandboxEnvironmentSettings) Validate() error {
	if settings.DefaultBackend != "local" && settings.DefaultBackend != "docker" && settings.DefaultBackend != "sbx" {
		return apperror.New(apperror.CodeInvalidArgument, "choose local, docker or sbx as the default sandbox")
	}
	if settings.DefaultBackend == "docker" && !settings.DockerEnabled || settings.DefaultBackend == "sbx" && !settings.SBXEnabled {
		return apperror.New(apperror.CodeInvalidArgument, "enable the selected sandbox before saving it as the default")
	}
	if settings.DockerImageDigest != "" && !sandbox.ValidOCIImageDigest(settings.DockerImageDigest) {
		return apperror.New(apperror.CodeInvalidArgument, "Docker image must use sha256 followed by its exact 64-character lowercase digest")
	}
	if len(settings.SBXTemplate) > 512 || !utf8.ValidString(settings.SBXTemplate) ||
		(settings.SBXTemplate != "" && !sandbox.ValidSBXTemplateReference(settings.SBXTemplate)) {
		return apperror.New(apperror.CodeInvalidArgument, "SBX template must use a complete OCI reference pinned to a sha256 digest")
	}
	if redact.String(settings.DockerImageDigest) != settings.DockerImageDigest || redact.String(settings.SBXTemplate) != settings.SBXTemplate {
		return apperror.New(apperror.CodeInvalidArgument, "use an image or template reference without credentials")
	}
	return nil
}

type SandboxEnvironmentSettingsSnapshot struct {
	Revision int64
	Settings SandboxEnvironmentSettings
}

type SandboxEnvironmentSettingsStore interface {
	Load(context.Context) (SandboxEnvironmentSettingsSnapshot, error)
	Save(context.Context, int64, SandboxEnvironmentSettings) (SandboxEnvironmentSettingsSnapshot, bool, error)
}

type SandboxEnvironmentBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Probes observe the startup configuration. They must honor cancellation and
// remain read-only: no installs, pulls, logins, creation or execution grants.
type SandboxEnvironmentProbe func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error)

type SandboxEnvironmentObservation struct {
	Installed bool
	Ready     bool
	Blockers  []SandboxEnvironmentBlocker
}

type SandboxEnvironmentProbes struct {
	Local  SandboxEnvironmentProbe
	Docker SandboxEnvironmentProbe
	SBX    SandboxEnvironmentProbe
}

type SandboxEnvironmentBackend struct {
	Backend    string                      `json:"backend"`
	Enabled    bool                        `json:"enabled"`
	Installed  bool                        `json:"installed"`
	Configured bool                        `json:"configured"`
	Ready      bool                        `json:"ready"`
	Status     string                      `json:"status"`
	Blockers   []SandboxEnvironmentBlocker `json:"blockers"`
}

type SandboxEnvironment struct {
	ProtocolVersion string                      `json:"protocol_version"`
	Revision        int64                       `json:"revision"`
	Settings        SandboxEnvironmentSettings  `json:"settings"`
	ActiveSettings  SandboxEnvironmentSettings  `json:"active_settings"`
	RestartRequired bool                        `json:"restart_required"`
	ProbeStatus     string                      `json:"probe_status"`
	Backends        []SandboxEnvironmentBackend `json:"backends"`
	CapabilityGrant bool                        `json:"capability_grant"`
	Replayed        bool                        `json:"replayed"`
}

type SaveSandboxEnvironmentRequest struct {
	Version          string
	ExpectedRevision int64
	Settings         SandboxEnvironmentSettings
}

type SandboxEnvironmentService struct {
	store  SandboxEnvironmentSettingsStore
	active SandboxEnvironmentSettings
	probes SandboxEnvironmentProbes
}

func NewSandboxEnvironmentService(store SandboxEnvironmentSettingsStore, active SandboxEnvironmentSettings,
	probes SandboxEnvironmentProbes,
) (*SandboxEnvironmentService, error) {
	if store == nil {
		return nil, apperror.New(apperror.CodeInvalidArgument, "sandbox settings store is required")
	}
	if err := active.Validate(); err != nil {
		return nil, err
	}
	return &SandboxEnvironmentService{store: store, active: active, probes: probes}, nil
}

func (service *SandboxEnvironmentService) SandboxEnvironment(ctx context.Context) (SandboxEnvironment, error) {
	if ctx == nil {
		return SandboxEnvironment{}, apperror.New(apperror.CodeInvalidArgument, "sandbox environment context is required")
	}
	snapshot, err := service.store.Load(ctx)
	if err != nil {
		return SandboxEnvironment{}, err
	}
	if snapshot.Revision < 1 || snapshot.Settings.Validate() != nil {
		return SandboxEnvironment{}, apperror.New(apperror.CodeFailedPrecondition, "saved sandbox settings are invalid")
	}
	view := service.view(snapshot, false)
	view.ProbeStatus = "checked"
	probes := []SandboxEnvironmentProbe{service.probes.Local, service.probes.Docker, service.probes.SBX}
	for index, probe := range probes {
		if err := ctx.Err(); err != nil {
			return SandboxEnvironment{}, err
		}
		backend := &view.Backends[index]
		if probe == nil {
			backend.Status = "unavailable"
			backend.Blockers = []SandboxEnvironmentBlocker{{Code: "PROBE_UNAVAILABLE", Message: "重新启动应用，接入当前环境的检测。"}}
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		observation, probeErr := probe(bounded, service.active)
		cancel()
		if err := ctx.Err(); err != nil {
			return SandboxEnvironment{}, err
		}
		backend.Installed = observation.Installed
		if probeErr != nil || !validSandboxEnvironmentBlockers(observation.Blockers) || observation.Ready && (!observation.Installed || len(observation.Blockers) != 0) {
			backend.Status = "unavailable"
			backend.Blockers = []SandboxEnvironmentBlocker{{Code: "PROBE_FAILED", Message: "环境检测暂未完成，重新检测当前环境。"}}
			continue
		}
		backend.Blockers = append([]SandboxEnvironmentBlocker{}, observation.Blockers...)
		switch {
		case !backend.Enabled:
			backend.Status = "disabled"
			backend.Blockers = append(backend.Blockers, SandboxEnvironmentBlocker{Code: "BACKEND_DISABLED", Message: "在环境设置中启用此后端，保存后重新启动应用。"})
		case !backend.Configured:
			backend.Status = "configuration_required"
			backend.Blockers = append(backend.Blockers, SandboxEnvironmentBlocker{Code: "PINNED_REFERENCE_REQUIRED", Message: "填写固定版本的镜像或模板，保存后重新启动应用。"})
		case observation.Ready:
			backend.Status, backend.Ready = "ready", true
		default:
			backend.Status = "unavailable"
			if len(backend.Blockers) == 0 {
				backend.Blockers = []SandboxEnvironmentBlocker{{Code: "BACKEND_NOT_READY", Message: "检查后端安装和本机连接，再重新检测环境。"}}
			}
		}
	}
	return view, nil
}

// Saving preferences never probes a backend or mutates this process's startup
// gates. An explicit restart and the normal Run approval remain separate.
func (service *SandboxEnvironmentService) SaveSandboxEnvironment(ctx context.Context, request SaveSandboxEnvironmentRequest) (SandboxEnvironment, error) {
	if ctx == nil || request.ExpectedRevision < 1 || request.Version != SandboxEnvironmentVersion {
		return SandboxEnvironment{}, apperror.New(apperror.CodeInvalidArgument, "sandbox environment settings version is unsupported")
	}
	if err := request.Settings.Validate(); err != nil {
		return SandboxEnvironment{}, err
	}
	snapshot, replayed, err := service.store.Save(ctx, request.ExpectedRevision, request.Settings)
	if err != nil {
		return SandboxEnvironment{}, err
	}
	return service.view(snapshot, replayed), nil
}

func (service *SandboxEnvironmentService) view(snapshot SandboxEnvironmentSettingsSnapshot, replayed bool) SandboxEnvironment {
	return SandboxEnvironment{ProtocolVersion: SandboxEnvironmentVersion, Revision: snapshot.Revision, Settings: snapshot.Settings,
		ActiveSettings: service.active, RestartRequired: snapshot.Settings != service.active, ProbeStatus: "not_checked", Replayed: replayed,
		Backends: []SandboxEnvironmentBackend{
			{Backend: "local", Enabled: true, Configured: true, Status: "not_checked", Blockers: environmentRecheckBlockers()},
			{Backend: "docker", Enabled: service.active.DockerEnabled, Configured: sandbox.ValidOCIImageDigest(service.active.DockerImageDigest), Status: "not_checked", Blockers: environmentRecheckBlockers()},
			{Backend: "sbx", Enabled: service.active.SBXEnabled, Configured: service.active.SBXTemplate != "" && sandbox.ValidSBXTemplateReference(service.active.SBXTemplate), Status: "not_checked", Blockers: environmentRecheckBlockers()},
		}}
}

func environmentRecheckBlockers() []SandboxEnvironmentBlocker {
	return []SandboxEnvironmentBlocker{{Code: "ENVIRONMENT_RECHECK_REQUIRED", Message: "设置已保存，重新检测当前环境。"}}
}

func validSandboxEnvironmentBlockers(blockers []SandboxEnvironmentBlocker) bool {
	// Reserve one of the eight public slots for the active configuration gate.
	if len(blockers) > 7 {
		return false
	}
	for _, blocker := range blockers {
		if len(blocker.Code) == 0 || len(blocker.Code) > 80 || strings.TrimSpace(blocker.Code) != blocker.Code || redact.String(blocker.Code) != blocker.Code ||
			len(blocker.Message) == 0 || utf8.RuneCountInString(blocker.Message) > 240 || !utf8.ValidString(blocker.Message) ||
			strings.TrimSpace(blocker.Message) != blocker.Message || strings.ContainsAny(blocker.Message, "\r\n\t\x00") || redact.String(blocker.Message) != blocker.Message {
			return false
		}
		for _, char := range blocker.Message {
			if unicode.IsControl(char) {
				return false
			}
		}
		for _, char := range blocker.Code {
			if char != '_' && char != '-' && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}
