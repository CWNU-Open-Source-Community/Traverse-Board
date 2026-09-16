import { useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Cpu, LoaderCircle, PackageSearch, PlugZap, RefreshCw } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { CodeIntelQualificationView, CodeIntelServerView, ExtensionMCPServerView,
  ExtensionPluginInstallationView, HealthView } from "../api/types";
import { useLocale } from "../lib/locale";
import { PrayuBrand } from "./prayu-brand";

export type Density = "comfortable" | "compact";

const densityStorageKey = "prayu.ui-density";

export function readDensity(): Density {
  if (typeof window === "undefined") return "comfortable";
  try {
    return window.localStorage.getItem(densityStorageKey) === "compact"
      ? "compact" : "comfortable";
  } catch {
    return "comfortable";
  }
}

export function persistDensity(density: Density) {
  try {
    window.localStorage.setItem(densityStorageKey, density);
  } catch {
    // Display preferences must never block the workbench.
  }
}

export function WebSkillInstall({ client }: { client: CyberAgentClient }) {
  const { t } = useLocale();
  const inputRef = useRef<HTMLInputElement>(null);
  const operationKey = useRef("");
  const [selected, setSelected] = useState<File | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [message, setMessage] = useState("");
  const install = useMutation({
    mutationFn: (file: File) => file.arrayBuffer().then((buffer) => {
      let binary = "";
      const bytes = new Uint8Array(buffer);
      const chunk = 0x8000;
      for (let i = 0; i < bytes.length; i += chunk) {
        binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
      }
      return client.installSkillPackage({
        version: "skill_package_installation.v1", archive_base64: btoa(binary),
        surface: "code", confirm_untrusted: true,
      }, operationKey.current);
    }),
    onSuccess: () => setMessage(t("Skill 包已安装", "Skill package installed")),
  });
  return <div className="settings-web-skill">
    <input accept="application/zip,.zip" aria-label="选择 Skill ZIP 包" disabled={install.isPending}
      hidden ref={inputRef} type="file"
      onChange={(event) => {
        const file = event.target.files?.[0];
        if (file) {
          setSelected(file);
          setConfirmed(false);
          setMessage("");
          install.reset();
          operationKey.current = `web-skill-install-${globalThis.crypto.randomUUID()}`;
        }
        event.currentTarget.value = "";
      }} />
    <button className="settings-action" disabled={install.isPending}
      onClick={() => inputRef.current?.click()} type="button">
      {install.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={15} /> : <PackageSearch aria-hidden="true" size={15} />}
      {t("选择 Skill ZIP 包", "Choose Skill ZIP package")}
    </button>
    {selected && <div className="shared-skill-confirmation">
      <p>{selected.name}</p>
      <label><input checked={confirmed} disabled={install.isPending || install.isSuccess}
        onChange={(event) => setConfirmed(event.target.checked)} type="checkbox" />
        {t("确认按不受信任包登记到 Code，不授予执行权", "Register as an untrusted Code package without execution authority")}</label>
      <button className="settings-action" disabled={!client.hasSkillInstallation || !confirmed ||
        install.isPending || install.isSuccess} onClick={() => install.mutate(selected)} type="button">
        {t("安装 Skill 包", "Install Skill package")}</button>
    </div>}
    {message && <span className="projection-placeholder">{message}</span>}
    {install.error && <span className="inline-warning">{install.error instanceof Error ? install.error.message : t("安装失败", "Install failed")}</span>}
  </div>;
}

export function ShortcutSettings() {
  const { t } = useLocale();
  return <section className="settings-page-section">
    <h1>{t("键盘快捷键", "Keyboard shortcuts")}</h1>
    <dl className="shortcut-list">
      <div><dt>{t("Inspector 命令面板", "Inspector command palette")}</dt><dd><kbd>Ctrl</kbd><kbd>K</kbd></dd></div>
      <div><dt>{t("关闭对话框或预览", "Close dialog or preview")}</dt><dd><kbd>Esc</kbd></dd></div>
      <div><dt>{t("选择上一项", "Select previous item")}</dt><dd><kbd>↑</kbd></dd></div>
      <div><dt>{t("选择下一项", "Select next item")}</dt><dd><kbd>↓</kbd></dd></div>
      <div><dt>{t("确认当前操作", "Confirm current action")}</dt><dd><kbd>Enter</kbd></dd></div>
    </dl>
  </section>;
}

export function AboutSettings({ desktop, health }: { desktop: boolean; health: HealthView | null }) {
  const { t } = useLocale();
  return <section className="settings-page-section about-prayu">
    <PrayuBrand className="about-mark" variant="icon" />
    <h1>Traverse Board · 针路簿</h1>
    <p>{t("本地优先的 AI Agent 工作台", "Local-first AI Agent Workbench")}</p>
    <dl className="settings-row-list">
      <div><dt>{t("应用版本", "Application version")}</dt><dd>{health?.app_version ?? "dev"}</dd></div>
      <div><dt>API 协议</dt><dd>{health?.api_version ?? "api.v1"}</dd></div>
      <div><dt>{t("数据库", "Database")}</dt><dd>schema v{health?.schema_version ?? "-"}</dd></div>
      <div><dt>{t("运行界面", "Surface")}</dt><dd>{desktop ? t("桌面端", "Desktop") : t("网页端", "Web")}</dd></div>
    </dl>
  </section>;
}

type ExtensionAction =
  | { kind: "refresh-mcp"; server: ExtensionMCPServerView }
  | { kind: "disable-mcp"; server: ExtensionMCPServerView }
  | { kind: "disable-plugin"; installation: ExtensionPluginInstallationView };

export function ExtensionSettings({ client, selectedRunID }: {
  client: CyberAgentClient;
  selectedRunID: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const inventory = useQuery({
    queryKey: ["extensions", selectedRunID],
    queryFn: ({ signal }) => client.extensionInventory(selectedRunID, signal),
  });
  const codeIntelWorkspaceID = selectedRunID ? (inventory.data?.workspace_id ?? "") : "";
  const codeIntel = useQuery({
    queryKey: ["code-intel", codeIntelWorkspaceID],
    queryFn: ({ signal }) => client.codeIntelInventory(codeIntelWorkspaceID, signal),
    enabled: selectedRunID === "" || codeIntelWorkspaceID !== "",
  });
  const action = useMutation<unknown, Error, ExtensionAction>({
    mutationFn: (value: ExtensionAction) => {
      if (value.kind === "refresh-mcp") {
        return client.refreshMCPServer(value.server.id);
      }
      if (value.kind === "disable-mcp") {
        return client.reviewMCPServer(value.server.id, {
          version: "extension-control.v1", action: "disable",
          expected_descriptor_fingerprint: value.server.descriptor_fingerprint,
        });
      }
      return client.reviewPluginInstallation(value.installation.id, {
        version: "extension-control.v1", action: "disable",
        expected_package_fingerprint: value.installation.package_fingerprint,
        expected_generation: value.installation.generation,
        confirm_untrusted: false,
      });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["extensions"] }),
  });
  const qualificationOnly = codeIntel.data?.qualifications.filter((qualification) =>
    !codeIntel.data.servers.some((server) => server.workspace_id === qualification.workspace_id &&
      server.server_id === qualification.server_id)) ?? [];
  const codeIntelCount = (codeIntel.data?.servers.length ?? 0) + qualificationOnly.length;
  return <section className="settings-page-section extension-settings">
    <header className="extension-heading">
      <div>
        <h1>{t("Code Intel、MCP 与 Plugin", "Code Intel, MCP and Plugins")}</h1>
        <p>{t("查看语言服务器与扩展的真实运行状态和固定能力指纹。不会显示语言服务器命令、环境或凭据。",
          "Inspect live language-server and extension state with pinned capability fingerprints. Language-server commands, environment, and credentials are never shown.")}</p>
      </div>
      <button className="settings-action" disabled={inventory.isFetching || codeIntel.isFetching}
        onClick={() => { void inventory.refetch(); void codeIntel.refetch(); }} type="button">
        <RefreshCw aria-hidden="true"
          className={inventory.isFetching || codeIntel.isFetching ? "spin" : ""} size={15} />
        {t("刷新", "Refresh")}
      </button>
    </header>
    {inventory.error && <p className="inline-warning">{inventory.error instanceof Error ?
      inventory.error.message : t("扩展状态读取失败", "Failed to read extension state")}</p>}
    {action.error && <p className="inline-warning">{action.error instanceof Error ?
      action.error.message : t("扩展操作失败", "Extension action failed")}</p>}
    {codeIntel.error && <p className="inline-warning">{codeIntel.error instanceof Error ?
      codeIntel.error.message : t("语言服务器状态读取失败", "Failed to read language-server state")}</p>}
    <ExtensionCollection title="Code Intel / LSP" count={codeIntelCount}>
      {codeIntel.data?.servers.map((server) => <CodeIntelServerCard
        key={`${server.workspace_id}/${server.server_id}`} server={server}
        qualification={codeIntel.data.qualifications.find((item) =>
          item.workspace_id === server.workspace_id && item.server_id === server.server_id)} />)}
      {qualificationOnly.map((qualification) => <CodeIntelQualificationCard
        key={`${qualification.workspace_id}/${qualification.server_id}/qualification`}
        qualification={qualification} />)}
      {codeIntel.data && codeIntel.data.servers.length === 0 &&
        <ExtensionEmpty>{t("尚未配置经审查的本地语言服务器。",
          "No reviewed local language server is configured.")}</ExtensionEmpty>}
    </ExtensionCollection>
    <ExtensionCollection title="MCP Client" count={inventory.data?.mcp_servers.length ?? 0}>
      {inventory.data?.mcp_servers.map((server) => <MCPServerCard action={action}
        client={client} key={server.id} server={server} />)}
      {inventory.data && inventory.data.mcp_servers.length === 0 &&
        <ExtensionEmpty>{selectedRunID ?
          t("当前 Run / Workspace 没有 MCP Server。", "No MCP server is scoped to this Run / Workspace.") :
          t("选择一个 Run 以查看其 MCP Server。", "Select a Run to inspect its MCP servers.")}</ExtensionEmpty>}
    </ExtensionCollection>
    <ExtensionCollection title="Plugin" count={inventory.data?.plugins.length ?? 0}>
      {inventory.data?.plugins.map((installation) => <PluginCard action={action}
        client={client} installation={installation} key={installation.id} />)}
      {inventory.data && inventory.data.plugins.length === 0 &&
        <ExtensionEmpty>{t("尚未安装 Plugin。", "No Plugin is installed.")}</ExtensionEmpty>}
    </ExtensionCollection>
  </section>;
}

function CodeIntelQualificationCard({ qualification }: {
  qualification: CodeIntelQualificationView;
}) {
  const { t } = useLocale();
  return <article className="extension-card code-intel-card">
    <header><div><Cpu aria-hidden="true" size={17} />
      <div><strong>{qualification.server_id}</strong>
        <span>{qualification.workspace_id}</span></div></div>
      <ExtensionState state={qualification.health} /></header>
    <dl className="extension-facts">
      <div><dt>{t("资格", "Qualification")}</dt><dd>{qualification.eligible ?
        t("已通过", "Eligible") : t("未通过", "Ineligible")}</dd></div>
      <div><dt>{t("人工审查", "Human review")}</dt><dd>{qualification.reviewed ?
        t("已完成", "Reviewed") : t("未完成", "Pending")}</dd></div>
      <div><dt>{t("可执行文件哈希", "Executable hash")}</dt>
        <dd>{qualification.executable_hash_matched ?
          t("匹配", "Matched") : t("不匹配", "Mismatch")}</dd></div>
      <div><dt>{t("最小环境", "Minimal environment")}</dt>
        <dd>{qualification.minimal_environment ? t("是", "Yes") : t("否", "No")}</dd></div>
    </dl>
    <Fingerprint label={t("描述符指纹", "Descriptor fingerprint")}
      value={qualification.descriptor_fingerprint} />
    {qualification.reason && <p className="inline-warning code-intel-error">
      {qualification.reason}</p>}
  </article>;
}

function CodeIntelServerCard({ qualification, server }: {
  qualification?: CodeIntelQualificationView;
  server: CodeIntelServerView;
}) {
  const { t } = useLocale();
  const enabledCapabilities = Object.values(server.capabilities)
    .filter((enabled) => enabled).length;
  return <article className="extension-card code-intel-card">
    <header><div><Cpu aria-hidden="true" size={17} />
      <div><strong>{server.server_name}</strong>
        <span>{server.workspace_id} · {server.server_id}</span></div></div>
      <ExtensionState state={server.health} /></header>
    <dl className="extension-facts">
      <div><dt>{t("语言", "Languages")}</dt><dd>{server.languages.join(", ")}</dd></div>
      <div><dt>{t("能力", "Capabilities")}</dt><dd>{enabledCapabilities} / 10</dd></div>
      <div><dt>{t("来源", "Source")}</dt><dd>{server.source_kind}</dd></div>
      <div><dt>{t("版本", "Version")}</dt><dd>{server.server_version || "—"}</dd></div>
      <div><dt>{t("代次", "Generation")}</dt>
        <dd>{server.generation ? server.generation.slice(0, 12) : "—"}</dd></div>
      <div><dt>{t("模型工具", "Model tools")}</dt>
        <dd>{server.model_visible_tools.length}</dd></div>
      <div><dt>{t("资格", "Qualification")}</dt><dd>{qualification ?
        (qualification.eligible ? t("已通过", "Eligible") : t("未通过", "Ineligible")) :
        t("未检查", "Not checked")}</dd></div>
      {qualification && <div><dt>{t("审查与哈希", "Review and hash")}</dt>
        <dd>{qualification.reviewed && qualification.executable_hash_matched ?
          t("已固定", "Pinned") : t("不完整", "Incomplete")}</dd></div>}
    </dl>
    <p className="extension-target" title={server.source_label}>{server.source_label}</p>
    <Fingerprint label={t("能力指纹", "Capability fingerprint")}
      value={server.capability_fingerprint || server.descriptor_fingerprint} />
    {server.model_visible_tools.length > 0 && <p className="code-intel-tools"
      title={server.model_visible_tools.join(", ")}>{server.model_visible_tools.join(", ")}</p>}
    {server.last_error && <p className="inline-warning code-intel-error">{server.last_error}</p>}
    {qualification?.reason && <p className="inline-warning code-intel-error">
      {qualification.reason}</p>}
  </article>;
}

function ExtensionCollection({ title, count, children }: {
  title: string; count: number; children: ReactNode;
}) {
  return <section className="extension-collection">
    <header><h2>{title}</h2><span>{count}</span></header>
    <div className="extension-card-list">{children}</div>
  </section>;
}

function MCPServerCard({ action, client, server }: {
  action: { isPending: boolean; mutate: (value: ExtensionAction) => void };
  client: CyberAgentClient;
  server: ExtensionMCPServerView;
}) {
  const { t } = useLocale();
  const refreshable = ["discovery_approved", "capabilities_pending", "enabled",
    "quarantined"].includes(server.state);
  const disableable = !["disabled", "revoked"].includes(server.state);
  return <article className="extension-card">
    <header><div><PlugZap aria-hidden="true" size={17} />
      <div><strong>{server.name}</strong><span>{server.id}</span></div></div>
      <ExtensionState state={server.state} /></header>
    <dl className="extension-facts">
      <div><dt>{t("传输", "Transport")}</dt><dd>{server.transport}</dd></div>
      <div><dt>{t("健康", "Health")}</dt><dd>{server.health}</dd></div>
      <div><dt>{t("范围", "Scope")}</dt><dd>{server.scope}</dd></div>
      <div><dt>{t("工具", "Tools")}</dt><dd>{server.capabilities.tools.length}</dd></div>
      <div><dt>{t("凭据引用", "Credential ref")}</dt><dd>{server.credential_ref || "—"}</dd></div>
      <div><dt>{t("来源", "Source")}</dt><dd>{server.source.kind}</dd></div>
    </dl>
    <p className="extension-target" title={server.target}>{server.target}</p>
    <Fingerprint label={t("能力指纹", "Capability fingerprint")}
      value={server.capabilities.fingerprint || server.descriptor_fingerprint} />
    <div className="extension-actions">
      <button className="settings-action" disabled={!client.hasExtensionControl ||
        !refreshable || action.isPending}
        onClick={() => action.mutate({ kind: "refresh-mcp", server })} type="button">
        <RefreshCw aria-hidden="true" size={14} />{t("重新发现", "Rediscover")}
      </button>
      <button className="settings-action danger" disabled={!client.hasExtensionControl ||
        !disableable || action.isPending}
        onClick={() => action.mutate({ kind: "disable-mcp", server })} type="button">
        <Ban aria-hidden="true" size={14} />{t("立即关闭", "Disable now")}
      </button>
    </div>
  </article>;
}

function PluginCard({ action, client, installation }: {
  action: { isPending: boolean; mutate: (value: ExtensionAction) => void };
  client: CyberAgentClient;
  installation: ExtensionPluginInstallationView;
}) {
  const { t } = useLocale();
  const disableable = !["disabled", "revoked", "rolled_back"].includes(installation.state);
  return <article className="extension-card">
    <header><div><PackageSearch aria-hidden="true" size={17} />
      <div><strong>{installation.manifest.name}</strong>
        <span>{installation.manifest.publisher} · v{installation.manifest.version}</span></div></div>
      <ExtensionState state={installation.state} /></header>
    <dl className="extension-facts">
      <div><dt>{t("签名", "Signature")}</dt><dd>{installation.signature_valid ?
        t("有效", "Valid") : installation.signature_present ? t("无效", "Invalid") :
          t("未签名", "Unsigned")}</dd></div>
      <div><dt>{t("来源", "Source")}</dt><dd>{installation.source.kind}</dd></div>
      <div><dt>{t("已启用", "Enabled")}</dt>
        <dd>{installation.enabled_capabilities.join(", ") || "—"}</dd></div>
      <div><dt>{t("代次", "Generation")}</dt><dd>{installation.generation}</dd></div>
    </dl>
    <p className="extension-target" title={installation.source.uri}>{installation.source.uri}</p>
    <Fingerprint label={t("包指纹", "Package fingerprint")}
      value={installation.package_fingerprint} />
    <div className="extension-actions">
      <button className="settings-action danger" disabled={!client.hasExtensionControl ||
        !disableable || action.isPending}
        onClick={() => action.mutate({ kind: "disable-plugin", installation })} type="button">
        <Ban aria-hidden="true" size={14} />{t("立即关闭", "Disable now")}
      </button>
    </div>
  </article>;
}

function ExtensionState({ state }: { state: string }) {
  return <span className={`extension-state state-${state}`}>{state.replaceAll("_", " ")}</span>;
}

function Fingerprint({ label, value }: { label: string; value: string }) {
  return <div className="extension-fingerprint"><span>{label}</span>
    <code title={value}>{value ? `${value.slice(0, 12)}…${value.slice(-8)}` : "—"}</code></div>;
}

function ExtensionEmpty({ children }: { children: ReactNode }) {
  return <p className="extension-empty">{children}</p>;
}
