import type { SandboxEnvironmentSettingsView, SandboxEnvironmentView } from "../api/types";

export const sandboxSettingsFixture: SandboxEnvironmentSettingsView = {
  default_backend: "local", docker_enabled: false, docker_image_digest: "", sbx_enabled: false, sbx_template: "",
};

export function sandboxEnvironmentFixture(settings: Partial<SandboxEnvironmentSettingsView> = {},
  active: Partial<SandboxEnvironmentSettingsView> = {}, revision = 1, checked = true): SandboxEnvironmentView {
  const saved = { ...sandboxSettingsFixture, ...settings };
  const running = { ...sandboxSettingsFixture, ...active };
  return { protocol_version: "sandbox_environment.v1", revision, settings: saved, active_settings: running,
    restart_required: JSON.stringify(saved) !== JSON.stringify(running), probe_status: checked ? "checked" : "not_checked",
    capability_grant: false, replayed: false,
    backends: (["local", "docker", "sbx"] as const).map((backend) => {
      const enabled = backend === "local" || (backend === "docker" ? running.docker_enabled : running.sbx_enabled);
      const configured = backend === "local" || Boolean(backend === "docker" ? running.docker_image_digest : running.sbx_template);
      const ready = checked && backend === "local";
      return { backend, enabled, installed: ready, configured, ready,
        status: !checked ? "not_checked" : ready ? "ready" : !enabled ? "disabled" : !configured ? "configuration_required" : "unavailable",
        blockers: !checked ? [{ code: "ENVIRONMENT_RECHECK_REQUIRED", message: "设置已保存，重新检测当前环境。" }]
          : ready ? [] : [{ code: "INSTALLATION_REQUIRED", message: "安装后重新检测环境。" }] };
    }) };
}
