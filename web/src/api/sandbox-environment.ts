import type { SandboxEnvironmentControlRequestView, SandboxEnvironmentSettingsView, SandboxEnvironmentView } from "./types";

export type SandboxBackend = "local" | "docker" | "sbx";
export const sandboxBackends = ["local", "docker", "sbx"] as const;
export const sandboxEnvironmentQueryKey = ["sandbox", "environment"] as const;

function exact(value: unknown, keys: string[]): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));
}

export function validSandboxSettings(value: unknown): value is SandboxEnvironmentSettingsView {
  if (!exact(value, ["default_backend", "docker_enabled", "docker_image_digest", "sbx_enabled", "sbx_template"]) ||
    !sandboxBackends.includes(value.default_backend as SandboxBackend) ||
    typeof value.docker_enabled !== "boolean" || typeof value.sbx_enabled !== "boolean" ||
    typeof value.docker_image_digest !== "string" || typeof value.sbx_template !== "string" ||
    (value.docker_image_digest !== "" && !/^sha256:[a-f0-9]{64}$/u.test(value.docker_image_digest)) ||
    value.sbx_template.length > 512 ||
    (value.sbx_template !== "" && (!/^[a-z0-9][a-z0-9._:/-]*@sha256:[a-f0-9]{64}$/u.test(value.sbx_template) ||
      value.sbx_template.includes("://") || value.sbx_template.includes("..") || value.sbx_template.includes("//"))) ||
    (value.default_backend === "docker" && !value.docker_enabled) ||
    (value.default_backend === "sbx" && !value.sbx_enabled)) return false;
  return true;
}

export function sandboxSettingsMatch(a: SandboxEnvironmentSettingsView, b: SandboxEnvironmentSettingsView): boolean {
  return a.default_backend === b.default_backend && a.docker_enabled === b.docker_enabled &&
    a.docker_image_digest === b.docker_image_digest && a.sbx_enabled === b.sbx_enabled && a.sbx_template === b.sbx_template;
}

export function validSandboxEnvironmentRequest(value: unknown): value is SandboxEnvironmentControlRequestView {
  return exact(value, ["version", "expected_revision", "settings"]) && value.version === "sandbox_environment.v1" &&
    Number.isSafeInteger(value.expected_revision) && Number(value.expected_revision) >= 1 && validSandboxSettings(value.settings);
}

export function parseSandboxEnvironment(value: unknown): SandboxEnvironmentView {
  if (!exact(value, ["protocol_version", "revision", "settings", "active_settings", "restart_required", "probe_status", "backends", "capability_grant", "replayed"]) ||
    value.protocol_version !== "sandbox_environment.v1" || !Number.isSafeInteger(value.revision) || Number(value.revision) < 1 ||
    !validSandboxSettings(value.settings) || !validSandboxSettings(value.active_settings) ||
    typeof value.restart_required !== "boolean" || value.restart_required === sandboxSettingsMatch(value.settings, value.active_settings) ||
    !["checked", "not_checked"].includes(String(value.probe_status)) || value.capability_grant !== false || typeof value.replayed !== "boolean" ||
    !Array.isArray(value.backends) || value.backends.length !== 3) throw new Error("Sandbox environment response is invalid");
  for (const [index, raw] of value.backends.entries()) {
    if (!exact(raw, ["backend", "enabled", "installed", "configured", "ready", "status", "blockers"]) || raw.backend !== sandboxBackends[index] ||
      [raw.enabled, raw.installed, raw.configured, raw.ready].some((entry) => typeof entry !== "boolean") ||
      !["ready", "disabled", "unavailable", "configuration_required", "not_checked"].includes(String(raw.status)) ||
      !Array.isArray(raw.blockers) || raw.blockers.length > 8 || raw.blockers.some((blocker) =>
        !exact(blocker, ["code", "message"]) || typeof blocker.code !== "string" || !/^[A-Za-z0-9_-]{1,80}$/u.test(blocker.code) ||
        typeof blocker.message !== "string" || blocker.message.length < 1 || Array.from(blocker.message).length > 240 || /[\r\n\t\u0000\p{Surrogate}]/u.test(blocker.message)) ||
      raw.ready !== (raw.status === "ready") ||
      (raw.ready && (!raw.enabled || !raw.installed || !raw.configured || raw.blockers.length !== 0)) ||
      (raw.status === "disabled" && raw.enabled) ||
      (value.probe_status === "not_checked" && (raw.status !== "not_checked" || raw.installed || raw.ready)) ||
      (value.probe_status === "checked" && raw.status === "not_checked")) throw new Error("Sandbox backend response is invalid");
    const expectedEnabled = raw.backend === "local" || (raw.backend === "docker" ? value.active_settings.docker_enabled : value.active_settings.sbx_enabled);
    const expectedConfigured = raw.backend === "local" || (raw.backend === "docker"
      ? value.active_settings.docker_image_digest !== "" : value.active_settings.sbx_template !== "");
    if (raw.enabled !== expectedEnabled || raw.configured !== expectedConfigured ||
      (value.probe_status === "checked" && !raw.enabled && raw.status !== "disabled") ||
      (raw.status === "configuration_required" && (!raw.enabled || raw.configured))) {
      throw new Error("Sandbox backend state changed its active settings");
    }
  }
  return value as unknown as SandboxEnvironmentView;
}
