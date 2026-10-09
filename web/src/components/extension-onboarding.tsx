import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { APIClient } from "../api/client";
import type { ExtensionMCPServerView, ExtensionPluginInstallationView } from "../api/types";
import { useLocale } from "../lib/locale";
import { MCPCredentialControls } from "./mcp-credential-controls";
import "./extension-onboarding.css";

function Failure({ error }: { error: unknown }) {
  return error ? <p className="inline-warning" role="alert">{error instanceof Error ? error.message : "操作失败，请重试。"}</p> : null;
}

export function MCPRegistrationForm({ client, enabled, workspaceID, runID, capabilityKnown = true }: {
  client: APIClient; enabled: boolean; workspaceID: string; runID: string; capabilityKnown?: boolean;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [id, setID] = useState("");
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("streamable_http");
  const [target, setTarget] = useState("");
  const [args, setArgs] = useState("");
  const [credential, setCredential] = useState("");
  const [scope, setScope] = useState("workspace");
  const [capabilities, setCapabilities] = useState<string[]>(["tools"]);
  const registration = useMutation({
    mutationFn: () => client.registerMCPServer({ version: "extension-control.v1", descriptor: {
      protocol_version: "mcp-client.v1", id: id.trim(), name: name.trim(), transport,
      target: target.trim(), workspace_id: workspaceID, scope,
      ...(scope === "run" ? { run_id: runID } : {}),
      ...(transport === "stdio" && args ? { arguments: args.split(/\r?\n/u).filter(Boolean) } : {}),
      ...(transport === "streamable_http" && credential.trim() ? { credential_ref: credential.trim() } : {}),
      declared_capabilities: capabilities, call_timeout_ms: 30_000, max_result_bytes: 65_536,
    } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["extensions"] }),
  });
  const change = (update: () => void) => { update(); registration.reset(); };
  return <details className="extension-onboarding"><summary>{t("登记 MCP Server", "Register MCP server")}</summary>
    <p>{t("先登记描述符，再单独审查发现和能力。登记不会连接服务器或调用工具。来源记录为人工上传。",
      "Register a descriptor, then review discovery and capabilities separately. Registration does not connect or invoke tools. Source is recorded as a manual upload.")}</p>
    {!enabled && <p role="status">{!capabilityKnown ? t("MCP 接入能力尚未确认，请刷新状态。", "MCP onboarding capability is unconfirmed; refresh state.") :
      t("当前服务未开放 MCP 登记或当前连接没有控制权限。请检查服务版本与控制连接。", "MCP registration is unavailable. Check the service version and control connection.")}</p>}
    {!workspaceID && <p role="status">{t("先选择工作区，或打开一个任务。", "Select a workspace or open a task first.")}</p>}
    <form className="extension-form" onSubmit={(event) => { event.preventDefault(); registration.mutate(); }}>
      <fieldset disabled={!enabled || !workspaceID || registration.isPending}>
        <label>{t("Server ID", "Server ID")}<input required maxLength={256} value={id} onChange={(event) => change(() => setID(event.target.value))} /></label>
        <label>{t("显示名称", "Display name")}<input required maxLength={256} value={name} onChange={(event) => change(() => setName(event.target.value))} /></label>
        <label>{t("传输方式", "Transport")}<select value={transport} onChange={(event) => change(() => setTransport(event.target.value))}>
          <option value="streamable_http">HTTPS / Streamable HTTP</option><option value="stdio">本地程序 / stdio</option>
        </select></label>
        <label>{transport === "stdio" ? t("可执行文件绝对路径", "Absolute executable path") : t("HTTPS 地址", "HTTPS URL")}
          <input required maxLength={4096} type={transport === "stdio" ? "text" : "url"} value={target}
            placeholder={transport === "stdio" ? "C:\\tools\\mcp-server.exe" : "https://example.com/mcp"}
            onChange={(event) => change(() => setTarget(event.target.value))} /></label>
        <label>{t("登记范围", "Registration scope")}<select value={scope} onChange={(event) => change(() => setScope(event.target.value))}>
          <option value="workspace">工作区 / Workspace</option><option disabled={!runID} value="run">当前任务执行 / Current Run</option>
        </select></label>
        <p>{t("工作区", "Workspace")}: {workspaceID || "—"}{scope === "run" ? ` · Run: ${runID}` : ""}</p>
        <div className="extension-checks" role="group" aria-label={t("声明能力", "Declared capabilities")}>
          {["tools", "resources", "prompts"].map((capability) => <label key={capability}><input type="checkbox"
            checked={capabilities.includes(capability)} onChange={(event) => change(() => setCapabilities((current) =>
              event.target.checked ? [...current, capability] : current.filter((item) => item !== capability)))} />{capability}</label>)}
        </div>
        {transport === "stdio" ? <label>{t("程序参数（每行一项，可选）", "Arguments (one per line, optional)")}
          <textarea rows={3} maxLength={8192} value={args} onChange={(event) => change(() => setArgs(event.target.value))} /></label>
          : <label>{t("凭据名称（认证时填写，登记后输入令牌）", "Credential name (for authentication; enter the token after registration)")}
            <input maxLength={64} value={credential} placeholder="mcp-example" onChange={(event) => change(() => setCredential(event.target.value))} /></label>}
        <button className="settings-action" disabled={!id.trim() || !name.trim() || !target.trim() ||
          capabilities.length === 0 || registration.isSuccess} type="submit">
          {registration.isPending ? t("正在登记…", "Registering…") : t("提交登记", "Register descriptor")}</button>
      </fieldset>
    </form>
    {registration.data && <p role="status">{t("登记返回状态", "Registration returned state")}: {registration.data.server.state} ·
      {t("实际工具调用需读取任务记录。下一步见下方 Server 的审查操作。", "Inspect task records for actual invocation. Continue with the server review below.")}</p>}
    <Failure error={registration.error} />
  </details>;
}

export function PluginImportForm({ client, enabled, capabilityKnown = true }: { client: APIClient; enabled: boolean; capabilityKnown?: boolean }) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const imported = useMutation({
    mutationFn: async () => {
      if (!file || file.size === 0 || file.size > 4 * 1024 * 1024) throw new Error("Plugin ZIP 必须为 1 字节至 4 MiB。");
      const buffer = await file.arrayBuffer();
      const digest = await crypto.subtle.digest("SHA-256", buffer);
      const archive_sha256 = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
      let binary = "";
      const bytes = new Uint8Array(buffer);
      for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
      return client.importPluginPackage({ version: "extension-control.v1", archive_sha256, archive_base64: btoa(binary) });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["extensions"] }),
  });
  return <details className="extension-onboarding"><summary>{t("导入 Plugin ZIP", "Import Plugin ZIP")}</summary>
    <p>{t("支持 plugin.v1 包，最大 4 MiB。来源与 SHA-256 由服务端固定；导入仅暂存，之后逐项审查和启用。原生 Skill 包请在 Skill 包设置安装。",
      "Upload a plugin.v1 ZIP up to 4 MiB. The service pins its source and SHA-256. Import only stages the package; review and enable it separately. Install native Skill packages in Skill settings.")}</p>
    {!enabled && <p role="status">{!capabilityKnown ? t("Plugin 接入能力尚未确认，请刷新状态。", "Plugin onboarding capability is unconfirmed; refresh state.") :
      t("当前服务未开放 Plugin 导入或当前连接没有控制权限。请检查服务版本与控制连接。", "Plugin import is unavailable. Check the service version and control connection.")}</p>}
    <input hidden ref={input} type="file" accept="application/zip,.zip" aria-label={t("选择 Plugin ZIP", "Choose Plugin ZIP")}
      disabled={!enabled || imported.isPending} onChange={(event) => {
        const selected = event.target.files?.[0];
        if (selected) { setFile(selected); imported.reset(); }
        event.currentTarget.value = "";
      }} />
    <button className="settings-action" disabled={!enabled || imported.isPending} onClick={() => input.current?.click()} type="button">
      {t("选择 Plugin ZIP", "Choose Plugin ZIP")}</button>
    {file && <div className="extension-review">
      <p>{file.name} · {file.size} bytes</p>
      <p>{t("此文件将作为不受信任包暂存，不授予执行权；审查和启用是之后的独立操作。", "This file will be staged as untrusted without execution authority. Review and enable are separate steps.")}</p>
      <button className="settings-action" disabled={!enabled || imported.isPending || imported.isSuccess}
        onClick={() => imported.mutate()} type="button">{imported.isPending ? t("正在导入…", "Importing…") : t("暂存 Plugin 包", "Stage Plugin package")}</button>
    </div>}
    {imported.data && <p role="status">{t("导入返回状态", "Import returned state")}: {imported.data.installation.state} ·
      {t("此状态不代表任务已调用。下一步核对下方包指纹与审查状态。", "This does not prove task invocation. Inspect the package fingerprint and review state below.")}</p>}
    <Failure error={imported.error} />
  </details>;
}

export function MCPReviewControls({ client, server, onOpenTask, credentialCapability }: {
  client: APIClient; server: ExtensionMCPServerView; onOpenTask?: (workspaceID?: string) => void; credentialCapability?: boolean;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [confirmed, setConfirmed] = useState(false);
  const [draftOpen, setDraftOpen] = useState(false);
  const action = server.state === "capabilities_pending" ? "enable_capabilities" : "approve_discovery";
  const reviewable = ["staged", "disabled", "quarantined", "capabilities_pending"].includes(server.state);
  const review = useMutation({ mutationFn: () => client.reviewMCPServer(server.id, {
    version: "extension-control.v1", action, expected_descriptor_fingerprint: server.descriptor_fingerprint,
    ...(action === "enable_capabilities" ? { expected_capability_fingerprint: server.capabilities.fingerprint } : {}),
  }), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["extensions"] }) });
  const draft = `请在当前任务中通过现有 MCP 权限与审批路径调用 Server ${server.id} 的工具 ${server.capabilities.tools[0] ?? "（选择工具）"}，使用该工具需要的明确参数。请报告实际调用结果；不要把已登记或已启用视为调用成功。`;
  return <div className="extension-review">
    <MCPCredentialControls client={client} server={server} capability={credentialCapability} />
    <p>{server.state === "enabled" ? t("能力已启用；实际调用仍需当前任务的权限、审批与参数。", "Capabilities enabled; calls still require task permissions, approval, and arguments.")
      : server.state === "discovery_approved" ? t("发现审查已通过。点击重新发现以连接并读取能力；这不是工具调用。", "Discovery reviewed. Rediscover to connect and inspect capabilities; this does not invoke a tool.")
      : server.state === "capabilities_pending" ? t("发现已完成，请核对工具和能力指纹后启用。", "Discovery complete. Review tools and the capability fingerprint before enabling.")
      : t("先核对来源、目标、范围与描述符指纹，再明确批准发现。", "Inspect source, target, scope, and descriptor fingerprint before approving discovery.")}</p>
    {server.health_message && <p className="inline-warning">{server.health_message}</p>}
    <p>{t("已发现能力", "Discovered capabilities")}: {server.capabilities.negotiated.join(", ") || "—"}</p>
    {server.state === "enabled" && server.capabilities.tools.length === 0 && <p>{t("未发现可调用工具。当前任务的 MCP 调用入口需要 tools 能力；请重新发现，或登记提供工具的 Server。",
      "No callable tools were discovered. Task MCP calls require the tools capability; rediscover or register a server that provides tools.")}</p>}
    {server.capabilities.tools.length > 0 && <p className="extension-tool-list">{server.capabilities.tools.join(", ")}</p>}
    {server.capabilities.resources.length > 0 && <p className="extension-tool-list">Resources: {server.capabilities.resources.join(", ")}</p>}
    {server.capabilities.prompts.length > 0 && <p className="extension-tool-list">Prompts: {server.capabilities.prompts.join(", ")}</p>}
    {reviewable && <>
      <label><input checked={confirmed} disabled={review.isPending} onChange={(event) => setConfirmed(event.target.checked)} type="checkbox" />
        {action === "enable_capabilities" ? t("我已核对当前能力指纹与所有发现列表", "I reviewed this capability fingerprint and all discovered lists") : t("我已核对描述符，允许连接并发现能力", "I reviewed this descriptor and permit connection for discovery")}</label>
      <button className="settings-action" disabled={!client.hasExtensionControl || !confirmed || review.isPending}
        onClick={() => review.mutate()} type="button">{action === "enable_capabilities" ? t("审查并启用能力", "Review and enable capabilities") : t("批准能力发现", "Approve discovery")}</button>
    </>}
    {server.state === "enabled" && server.capabilities.tools.length > 0 && <>
      <button className="settings-action" onClick={() => setDraftOpen((value) => !value)} type="button">{t("生成首次调用任务草稿", "Prepare first-call task draft")}</button>
      {draftOpen && <label>{t("调用任务草稿（复制到任务中发送）", "Task draft (copy and send in your task)")}<textarea readOnly rows={4} value={draft} onFocus={(event) => event.currentTarget.select()} /></label>}
      {onOpenTask && <button className="settings-action" onClick={() => onOpenTask(server.workspace_id)} type="button">{t("打开任务输入区", "Open task composer")}</button>}
    </>}
    <Failure error={review.error} />
  </div>;
}

export function PluginReviewControls({ client, installation }: {
  client: APIClient; installation: ExtensionPluginInstallationView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [confirmed, setConfirmed] = useState(false);
  const [capabilities, setCapabilities] = useState<string[]>([]);
  const approve = ["staged", "quarantined"].includes(installation.state);
  const enable = ["approved", "disabled"].includes(installation.state);
  const review = useMutation({ mutationFn: () => client.reviewPluginInstallation(installation.id, {
    version: "extension-control.v1", action: approve ? "approve" : "enable",
    expected_package_fingerprint: installation.package_fingerprint, expected_generation: installation.generation,
    confirm_untrusted: confirmed, ...(approve ? {} : { capabilities }),
  }), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["extensions"] }) });
  return <div className="extension-review">
    <p>{approve ? t("包已暂存。核对来源、签名、声明能力和指纹后审查。", "Package staged. Review its source, signature, declared capabilities, and fingerprint.")
      : enable ? t("包已审查。明确选择要启用的能力；启用不代表当前任务已调用。", "Package reviewed. Select capabilities to enable; this does not confirm task invocation.")
      : t("包状态不代表贡献已被当前任务加载或调用；MCP 贡献仍需独立发现和审查。", "Package state does not prove task loading or invocation; MCP contributions require separate discovery and review.")}</p>
    <p>{t("声明能力", "Declared capabilities")}: {installation.manifest.capabilities.join(", ") || "—"}</p>
    {(approve || enable) && <>
      {enable && <div className="extension-checks" role="group" aria-label={t("启用 Plugin 能力", "Enable Plugin capabilities")}>
        {installation.manifest.capabilities.map((capability) => <label key={capability}><input type="checkbox" disabled={review.isPending}
          checked={capabilities.includes(capability)} onChange={(event) => setCapabilities((current) =>
            event.target.checked ? [...current, capability] : current.filter((item) => item !== capability))} />{capability}</label>)}
      </div>}
      <label><input type="checkbox" checked={confirmed} disabled={review.isPending} onChange={(event) => setConfirmed(event.target.checked)} />
        {t("我已核对包指纹，并明确允许此不受信任包的本次审查或启用", "I reviewed the package fingerprint and explicitly allow this untrusted review or enable action")}</label>
      <button className="settings-action" disabled={!client.hasExtensionControl || !confirmed || review.isPending || (enable && capabilities.length === 0)}
        onClick={() => review.mutate()} type="button">{approve ? t("审查 Plugin 包", "Review Plugin package") : t("启用所选能力", "Enable selected capabilities")}</button>
    </>}
    <Failure error={review.error} />
  </div>;
}
