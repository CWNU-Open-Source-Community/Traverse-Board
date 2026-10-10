package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/sandbox"
)

// Executable discovery belongs to the native process. Settings select only a
// fixed backend and a pinned image, never a command line or daemon endpoint.
func NewDesktopSBXBackend(home string, settings application.SandboxEnvironmentSettings) (*sandbox.SBXBackend, error) {
	absolute, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(canonical, "sbx-owned")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	helper, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable := desktopSBXExecutable()
	return sandbox.NewSBXBackend(sandbox.SBXBackendConfig{Enabled: settings.SBXEnabled,
		ExecutablePath: executable, HelperExecutable: helper, TemplateReference: settings.SBXTemplate, JournalRoot: root})
}

func desktopSBXExecutable() string {
	if executable, err := exec.LookPath("sbx"); err == nil {
		return executable
	}
	if runtime.GOOS == "windows" {
		// The official per-user installer may have run after Desktop inherited
		// PATH. Resolve its fixed installation location without a renderer path.
		if home, err := os.UserHomeDir(); err == nil {
			candidate := filepath.Join(home, "AppData", "Local", "DockerSandboxes", "bin", "sbx.exe")
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate
			}
		}
	}
	return ""
}

func newDesktopSandboxEnvironmentController(home string, config ControlPlaneConfig,
	dockerInstalled bool,
) (httpapi.SandboxEnvironmentController, error) {
	if !config.SandboxEnvironmentControlEnabled || config.SandboxEnvironmentSettings == nil {
		return nil, nil
	}
	settingsStore, err := application.NewFileSandboxEnvironmentSettingsStore(home)
	if err != nil {
		return nil, err
	}
	probes := application.SandboxEnvironmentProbes{
		Local: func(ctx context.Context, _ application.SandboxEnvironmentSettings) (application.SandboxEnvironmentObservation, error) {
			// Local conformance can launch a contained test child. A settings
			// GET reports the native startup observation without rerunning it.
			proof := config.LocalSandboxReadiness
			if proof == nil || proof.Validate() != nil {
				return environmentObservation(false, false, "LOCAL_STARTUP_CHECK_REQUIRED", "重新启动应用以检测 Local 工作区隔离。"), nil
			}
			installed := proof.ReasonCode != sandbox.LocalReasonPlatformUnsupported && proof.ReasonCode != sandbox.LocalReasonArchitectureUnsupported
			if proof.Ready {
				return environmentObservation(true, true, "", ""), nil
			}
			return environmentObservation(installed, false, proof.ReasonCode, "按 Local 启动检测结果准备系统隔离组件，或选择 Docker Engine、Docker Sandboxes。"), nil
		},
		Docker: func(ctx context.Context, active application.SandboxEnvironmentSettings) (application.SandboxEnvironmentObservation, error) {
			_, commandErr := exec.LookPath("docker")
			transport := sandbox.NewLocalDockerReadOnlyTransport()
			pingErr := transport.Ping(ctx)
			installed := commandErr == nil || pingErr == nil
			if pingErr != nil {
				return environmentObservation(installed, false, "DOCKER_ENGINE_START_REQUIRED", "安装并启动 Docker Engine；Windows 上请在 Docker Desktop 中使用 Linux 容器。"), nil
			}
			proof, err := application.ProbeStandardCodeDockerReadiness(ctx, active.DockerEnabled, active.DockerImageDigest)
			if err != nil {
				return application.SandboxEnvironmentObservation{}, err
			}
			if proof.Ready && dockerInstalled {
				return environmentObservation(true, true, "", ""), nil
			}
			if proof.Ready {
				return environmentObservation(true, false, "DOCKER_ADAPTER_RESTART_REQUIRED", "Docker 已准备好，重新启动应用以接入执行后端。"), nil
			}
			return environmentObservation(true, false, proof.ReasonCode, "准备固定版本的 Standard Code 镜像并填写镜像摘要，然后保存设置、重启应用。"), nil
		},
		SBX: func(ctx context.Context, active application.SandboxEnvironmentSettings) (application.SandboxEnvironmentObservation, error) {
			var proof sandbox.SBXReadiness
			var err error
			if config.SBXBackend != nil {
				proof, err = config.SBXBackend.Readiness(ctx)
			} else {
				executable := desktopSBXExecutable()
				proof, err = sandbox.ProbeSBXReadiness(ctx, active.SBXEnabled, executable, active.SBXTemplate)
			}
			if err != nil {
				return application.SandboxEnvironmentObservation{}, err
			}
			if proof.Ready && config.SBXReadiness != nil && config.SBXReadiness.Ready {
				return environmentObservation(true, true, "", ""), nil
			}
			if proof.Ready {
				return environmentObservation(true, false, "SBX_ADAPTER_RESTART_REQUIRED", "Docker Sandboxes 已准备好，重新启动应用以接入执行后端。"), nil
			}
			message := "安装官方 sbx 0.47.0，在 traverse-runtime 环境完成本地虚拟化准备与登录，然后填写固定模板并重新检测。"
			switch proof.ReasonCode {
			case "cli_version_unsupported":
				message = "安装已验证的 sbx 0.47.0，然后重启应用并重新检测。"
			case "daemon_protocol_unavailable", "daemon_unavailable":
				message = "使用 sbx 0.47.0 启动 traverse-runtime 的 daemon 并完成登录，然后重新检测。"
			case "disabled":
				message = "启用 Docker Sandboxes，保存设置并重启应用。"
			case "template_missing":
				message = "准备固定 OCI 模板，填写完整 SHA-256 摘要后保存并重启应用。"
			case "ssh_forwarding_not_disabled":
				message = "在 traverse-runtime 环境关闭 ssh.agentForwardingEnabled，并重启对应 sandboxd 后重新检测。"
			case "mcp_helper_unavailable", "backend_closed":
				message = "重启应用以注册内置的沙箱辅助服务，然后重新检测。"
			case "mcp_local_gateway_required":
				message = "在 traverse-runtime 环境启用 mcp.forceLocalGateway，并重启对应 sandboxd 后重新检测。"
			}
			return environmentObservation(proof.CLIInstalled, false, proof.ReasonCode, message), nil
		},
	}
	service, err := application.NewSandboxEnvironmentService(settingsStore, *config.SandboxEnvironmentSettings, probes)
	if err != nil {
		return nil, err
	}
	return httpapi.NewSandboxEnvironmentController(service), nil
}

func environmentObservation(installed, ready bool, code, message string) application.SandboxEnvironmentObservation {
	observation := application.SandboxEnvironmentObservation{Installed: installed, Ready: ready,
		Blockers: []application.SandboxEnvironmentBlocker{}}
	if code != "" {
		observation.Blockers = append(observation.Blockers, application.SandboxEnvironmentBlocker{Code: code, Message: message})
	}
	return observation
}
