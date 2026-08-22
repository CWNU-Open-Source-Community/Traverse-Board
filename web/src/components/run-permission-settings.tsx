import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bug,
  Check,
  Code2,
  Container,
  Eye,
  Globe2,
  LoaderCircle,
  MonitorUp,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
  Terminal,
  UserCheck,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  RunDetailView,
  RunBrowserCDPPermissionControlView,
  RunBrowserCDPPermissionView,
  RunExecutionInteractionControlRequestView,
  RunExecutionInteractionControlView,
  RunExecutionInteractionView,
  RunExecutionPermissionControlView,
  RunExecutionPermissionView,
  RunExecutionProfileControlView,
  RunExecutionProfileView,
} from "../api/types";
import { shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { ErrorState, LoadingState, StatusBadge } from "./common";

export function RunPermissionSettings({ client, runID }: {
  client: CyberAgentClient;
  runID: string;
}) {
  const { t } = useLocale();
  const detailQuery = useQuery({
    queryKey: ["run", runID],
    queryFn: ({ signal }) => client.get<RunDetailView>(
      `/runs/${encodeURIComponent(runID)}`, {}, signal),
    enabled: runID !== "",
  });
  if (!runID) {
    return <section className="settings-page-section permission-settings-page">
      <h1>{t("权限", "Permissions")}</h1>
      <div className="permission-settings-empty">
        <ShieldCheck aria-hidden="true" size={24} />
        <strong>{t("选择一个 Run", "Select a Run")}</strong>
        <span>{t("权限档位与执行边界按 Run 独立保存。", "Permission levels and execution boundaries are stored independently per Run.")}</span>
      </div>
    </section>;
  }
  if (detailQuery.isPending) {
    return <LoadingState label={t("加载 Run 权限", "Loading Run permissions")} />;
  }
  if (detailQuery.isError || !detailQuery.data) {
    return <ErrorState error={detailQuery.error} />;
  }
  const detail = detailQuery.data;
  return <section className="settings-page-section permission-settings-page">
    <div className="permission-settings-title">
      <div>
        <h1>{t("权限", "Permissions")}</h1>
        <span>{shortID(detail.run.id)} · {detail.run.status}</span>
      </div>
      <StatusBadge status={detail.execution_permission.risk_tier} />
    </div>
    <ExecutionPermissionPanel client={client} detail={detail}
      key={`permission-${detail.run.id}`} />
    <BrowserCDPPermissionPanel client={client} detail={detail}
      key={`browser-cdp-${detail.run.id}`} />
    <ExecutionInteractionPanel client={client} detail={detail}
      key={`interaction-${detail.run.id}`} />
    <ExecutionProfilePanel client={client} detail={detail}
      key={`profile-${detail.run.id}`} />
    <section className="permission-authority-boundary" aria-label={t("运行时授权边界", "Runtime authorization boundary")}>
      <ShieldAlert aria-hidden="true" size={17} />
      <div>
        <strong>{t("运行时授权仍为关闭", "Runtime authorization remains closed")}</strong>
        <span>{t("进程、网络和 Agent 终端输入继续由独立沙箱、审批与限时租约控制。", "Processes, network access, and Agent terminal input remain controlled by independent sandboxes, approvals, and time-limited leases.")}</span>
      </div>
    </section>
  </section>;
}

const executionProfiles: Array<{
  id: RunExecutionProfileView["profile"];
  chinese: string;
  english: string;
  detailChinese: string;
  detailEnglish: string;
  icon: typeof Eye;
}> = [
  { id: "preview", chinese: "预览", english: "Preview", detailChinese: "不启动进程", detailEnglish: "No process execution", icon: Eye },
  { id: "docker", chinese: "Docker", english: "Docker", detailChinese: "隔离容器", detailEnglish: "Isolated container", icon: Container },
  { id: "local", chinese: "本地工作区", english: "Local workspace", detailChinese: "系统沙箱", detailEnglish: "System sandbox", icon: Terminal },
];

export function ExecutionProfilePanel({ client, detail }: {
  client: CyberAgentClient;
  detail: RunDetailView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const profile = detail.execution_profile;
  const mutableStatus = detail.run.status === "created" || detail.run.status === "paused";
  const mutable = client.hasControl && mutableStatus && !detail.execution_lease?.active;
  const mutation = useMutation({
    mutationFn: (target: RunExecutionProfileView["profile"]) =>
      client.postControl<RunExecutionProfileControlView>(
        `/runs/${encodeURIComponent(detail.run.id)}/execution-profile`,
        { profile: target, reason: "settings execution profile selection" },
        `settings-execution-profile-${globalThis.crypto.randomUUID()}`,
      ),
    onSuccess: (result) => {
      queryClient.setQueryData<RunDetailView>(["run", detail.run.id], (current) => current
        ? { ...current, execution_profile: result.execution_profile }
        : current);
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id, "events"] });
    },
  });
  let boundary = t("操作员意图", "Operator intent");
  if (!client.hasControl) boundary = t("只读连接", "Read-only connection");
  else if (!mutableStatus) boundary = t("请先暂停 Run", "Pause the Run first");
  else if (detail.execution_lease?.active) boundary = t("执行租约占用中", "Execution lease is active");
  return (
    <section className="permission-control-card execution-profile-section">
      <div className="section-heading">
        <div>
          <h2><MonitorUp aria-hidden="true" size={16} />{t("执行环境", "Execution environment")}</h2>
          <span>{boundary}</span>
        </div>
        <StatusBadge status={profile.risk_tier} />
      </div>
      <div aria-label={t("Run 执行环境", "Run execution profile")}
        className="permission-option-grid permission-option-grid-three" role="group">
        {executionProfiles.map(({ id, chinese, english, detailChinese, detailEnglish, icon: Icon }) => (
          <button aria-pressed={profile.profile === id}
            disabled={!mutable || mutation.isPending || profile.profile === id}
            key={id} onClick={() => mutation.mutate(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{t(chinese, english)}</strong><small>{t(detailChinese, detailEnglish)}</small></span>
            {profile.profile === id && <Check aria-hidden="true" size={15} />}
          </button>
        ))}
      </div>
      <dl className="permission-facts">
        <div><dt>{t("后端", "Backend")}</dt><dd>{localizedProtocolValue(profile.backend, t)}</dd></div>
        <div><dt>{t("审批", "Approval")}</dt><dd>{localizedProtocolValue(profile.approval_policy, t)}</dd></div>
        <div><dt>{t("闸门", "Gate")}</dt><dd>{localizedProtocolValue(profile.required_gate, t)}</dd></div>
      </dl>
      {mutation.isError && <MutationError error={mutation.error}
        fallback={t("执行环境切换失败", "Execution environment switch failed")} />}
    </section>
  );
}

const executionPermissions: Array<{
  id: RunExecutionPermissionView["mode"];
  chinese: string;
  english: string;
  detailChinese: string;
  detailEnglish: string;
  icon: typeof ShieldCheck;
}> = [
  { id: "conservative", chinese: "保守模式", english: "Conservative", detailChinese: "固定安全模板", detailEnglish: "Fixed safe templates", icon: ShieldCheck },
  { id: "workspace_access", chinese: "工作区执行", english: "Workspace access", detailChinese: "隔离运行 · 无网络", detailEnglish: "Sandboxed · no network", icon: Container },
  { id: "approval", chinese: "用户审批", english: "User approval", detailChinese: "逐条人工确认", detailEnglish: "Per-command confirmation", icon: UserCheck },
  { id: "full_access", chinese: "完全访问", english: "Full access", detailChinese: "宿主机无沙箱", detailEnglish: "Unsandboxed host access", icon: ShieldOff },
  { id: "debug", chinese: "调试模式", english: "Debug", detailChinese: "持久交互终端", detailEnglish: "Persistent interactive terminal", icon: Bug },
];

function permissionRuntimeAvailable(
  permission: RunExecutionPermissionView,
  target: RunExecutionPermissionView["mode"],
) {
  switch (target) {
    case "conservative":
      return true;
    case "workspace_access":
      return permission.runtime.workspace_sandbox_enabled;
    case "approval":
      return permission.runtime.operator_approval_enabled;
    case "full_access":
      return permission.runtime.danger_full_access_enabled;
    case "debug":
      return permission.runtime.debug_maximum_access_enabled;
  }
}

export function ExecutionPermissionPanel({ client, detail }: {
  client: CyberAgentClient;
  detail: RunDetailView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const permission = detail.execution_permission;
  const [pendingMode, setPendingMode] =
    useState<RunExecutionPermissionView["mode"] | null>(null);
  const mutableStatus = detail.run.status === "created" || detail.run.status === "paused";
  const mutable = client.hasExecutionPermissionControl && mutableStatus;
  const mutation = useMutation({
    mutationFn: (target: RunExecutionPermissionView["mode"]) => {
      const body: {
        mode: RunExecutionPermissionView["mode"];
        reason: string;
        confirm_workspace_access?: boolean;
        confirm_user_approval?: boolean;
        confirm_danger_full_access?: boolean;
        confirm_debug_access?: boolean;
      } = { mode: target, reason: "settings execution permission selection" };
      if (target === "workspace_access") body.confirm_workspace_access = true;
      if (target === "approval") body.confirm_user_approval = true;
      if (target === "full_access") body.confirm_danger_full_access = true;
      if (target === "debug") body.confirm_debug_access = true;
      return client.postControl<RunExecutionPermissionControlView>(
        `/runs/${encodeURIComponent(detail.run.id)}/execution-permission`,
        body,
        `settings-execution-permission-${globalThis.crypto.randomUUID()}`,
      );
    },
    onSuccess: (result) => {
      setPendingMode(null);
      queryClient.setQueryData<RunDetailView>(["run", detail.run.id], (current) => current
        ? { ...current, execution_permission: result.execution_permission }
        : current);
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id, "events"] });
    },
  });
  const choose = (target: RunExecutionPermissionView["mode"]) => {
    if (target === "conservative") mutation.mutate(target);
    else setPendingMode(target);
  };
  let boundary = t("Run 策略快照", "Run policy snapshot");
  if (!client.hasExecutionPermissionControl) boundary = t("启动时未开放权限控制", "Permission control was not enabled at startup");
  else if (!mutableStatus) boundary = t("请先暂停 Run", "Pause the Run first");
  else if (detail.execution_lease?.active) boundary = t("切换将撤销当前执行租约", "Switching revokes the active execution lease");
  return (
    <section className="permission-control-card execution-permission-section">
      <div className="section-heading">
        <div>
          <h2><ShieldCheck aria-hidden="true" size={16} />{t("权限档位", "Permission level")}</h2>
          <span>{boundary}</span>
        </div>
        <StatusBadge status={permission.risk_tier} />
      </div>
      <div aria-label={t("Run 执行权限", "Run execution permission")}
        className="permission-option-grid permission-option-grid-five" role="group">
        {executionPermissions.map(({ id, chinese, english, detailChinese, detailEnglish, icon: Icon }) => {
          const available = permissionRuntimeAvailable(permission, id);
          return <button aria-pressed={permission.mode === id}
            className={id === "full_access" || id === "debug" ? "danger" : ""}
            disabled={!mutable || mutation.isPending || permission.mode === id || !available}
            key={id} onClick={() => choose(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{t(chinese, english)}</strong><small>{available
              ? t(detailChinese, detailEnglish)
              : id === "workspace_access"
                ? t("沙箱适配器未就绪", "Sandbox adapter is unavailable")
                : t("启动闸门未开启", "Startup gate is closed")}</small></span>
            {permission.mode === id && <Check aria-hidden="true" size={15} />}
          </button>;
        })}
      </div>
      {pendingMode && <PermissionConfirmation
        description={pendingMode === "workspace_access"
          ? t("只允许受控工作区读写与经过验证的沙箱命令；网络、凭证、主目录、宿主进程、持久终端和完整 CDP 均被拒绝。", "Allows controlled Workspace access and verified sandbox commands only; network, credentials, home, host processes, persistent terminals, and Full CDP are denied.")
          : pendingMode === "approval"
          ? t("每一条宿主机命令都需要用户批准。", "Every host command requires user approval.")
          : pendingMode === "full_access"
            ? t("将允许无沙箱宿主机文件与网络访问。", "Allows unsandboxed host filesystem and network access.")
            : t("将允许持久终端、后台进程和限时 Agent 输入。", "Allows a persistent terminal, background processes, and time-limited Agent input.")}
        label={(() => { const option = executionPermissions.find(({ id }) => id === pendingMode); return option ? t(option.chinese, option.english) : pendingMode; })()}
        loading={mutation.isPending} onCancel={() => setPendingMode(null)}
        onConfirm={() => mutation.mutate(pendingMode)} />}
      <dl className="permission-facts">
        <div><dt>{t("命令", "Commands")}</dt><dd>{localizedProtocolValue(permission.command_scope, t)}</dd></div>
        <div><dt>{t("文件系统", "Filesystem")}</dt><dd>{localizedProtocolValue(permission.filesystem_scope, t)}</dd></div>
        <div><dt>{t("网络", "Network")}</dt><dd>{localizedProtocolValue(permission.network_scope, t)}</dd></div>
      </dl>
      {permission.mode === "workspace_access" && <p className="permission-closed-note">
        {permission.runtime.workspace_sandbox_enabled
          ? t("该选择仍只是策略上限；每次启动都必须重新验证沙箱 adapter。", "This selection remains a policy ceiling; every start must revalidate the sandbox adapter.")
          : t("当前没有通过 readiness 的沙箱 adapter，因此此档不可执行命令，也不会回退到宿主进程。", "No sandbox adapter has passed readiness, so this level cannot execute commands and never falls back to a host process.")}
      </p>}
      {mutation.isError && <MutationError error={mutation.error}
        fallback={t("权限档位切换失败", "Permission level switch failed")} />}
    </section>
  );
}

const browserCDPPermissions: Array<{
  id: RunBrowserCDPPermissionView["mode"];
  chinese: string;
  english: string;
  detailChinese: string;
  detailEnglish: string;
  dangerous: boolean;
}> = [
  { id: "restricted", chinese: "受限 CDP", english: "Restricted CDP", detailChinese: "导航、DOM 与截图", detailEnglish: "Navigation, DOM, and screenshots", dangerous: false },
  { id: "full_debug", chinese: "完整 CDP（调试）", english: "Full CDP (debug)", detailChinese: "请求改写、Cookie 与任意方法", detailEnglish: "Request rewriting, cookies, and arbitrary methods", dangerous: true },
];

export function BrowserCDPPermissionPanel({ client, detail }: {
  client: CyberAgentClient;
  detail: RunDetailView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const permission = detail.browser_cdp_permission;
  const [confirmFull, setConfirmFull] = useState(false);
  const mutableStatus = detail.run.status === "created" || detail.run.status === "paused";
  const mutable = client.hasBrowserCDPPermissionControl && mutableStatus &&
    !detail.execution_lease?.active;
  const fullAvailable = client.hasFullCDPDebug && permission.runtime.full_debug_enabled &&
    permission.runtime.execution_debug_selected && detail.execution_permission.mode === "debug";
  const mutation = useMutation({
    mutationFn: (target: RunBrowserCDPPermissionView["mode"]) =>
      client.postControl<RunBrowserCDPPermissionControlView>(
        `/runs/${encodeURIComponent(detail.run.id)}/browser-cdp-permission`,
        {
          mode: target,
          reason: "settings browser CDP permission selection",
          ...(target === "full_debug" ? { confirm_full_cdp_debug: true } : {}),
        },
        `settings-browser-cdp-permission-${globalThis.crypto.randomUUID()}`,
      ),
    onSuccess: (result) => {
      setConfirmFull(false);
      queryClient.setQueryData<RunDetailView>(["run", detail.run.id], (current) => current
        ? { ...current, browser_cdp_permission: result.browser_cdp_permission }
        : current);
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id, "events"] });
    },
  });
  const choose = (target: RunBrowserCDPPermissionView["mode"]) => {
    if (target === "restricted") mutation.mutate(target);
    else setConfirmFull(true);
  };
  let boundary = t("独立 CDP 权限上限", "Independent CDP permission ceiling");
  if (!client.hasBrowserCDPPermissionControl) boundary = t("启动时未开放 CDP 权限控制", "CDP permission control was not enabled at startup");
  else if (!mutableStatus) boundary = t("请先暂停 Run", "Pause the Run first");
  else if (detail.execution_lease?.active) boundary = t("执行租约占用中", "Execution lease is active");
  return (
    <section className="permission-control-card browser-cdp-permission-section">
      <div className="section-heading">
        <div>
          <h2><Globe2 aria-hidden="true" size={16} />{t("浏览器 CDP", "Browser CDP")}</h2>
          <span>{boundary}</span>
        </div>
        <StatusBadge status={permission.risk_tier} />
      </div>
      <div aria-label={t("Run 浏览器 CDP 权限", "Run browser CDP permission")}
        className="permission-option-grid permission-option-grid-two" role="group">
        {browserCDPPermissions.map(({ id, chinese, english, detailChinese, detailEnglish, dangerous }) => {
          const available = id === "restricted" || fullAvailable;
          return <button aria-pressed={permission.mode === id}
            className={dangerous ? "danger" : ""}
            disabled={!mutable || mutation.isPending || permission.mode === id || !available}
            key={id} onClick={() => choose(id)} type="button">
            {dangerous
              ? <ShieldAlert aria-hidden="true" size={17} />
              : <ShieldCheck aria-hidden="true" size={17} />}
            <span>
              <strong>{t(chinese, english)}</strong>
              {dangerous && <em className="sensitive-permission-label">{t("高度敏感权限", "Highly sensitive permission")}</em>}
              <small>{available ? t(detailChinese, detailEnglish) : t("需要调试模式与专用启动闸门", "Requires Debug mode and its dedicated startup gate")}</small>
            </span>
            {permission.mode === id && <Check aria-hidden="true" size={15} />}
          </button>;
        })}
      </div>
      {confirmFull && <PermissionConfirmation
        description={t("允许请求捕获、改写与重放、Cookie 访问和任意 CDP 方法。选择不会自动启动浏览器。", "Allows request capture, rewriting and replay, cookie access, and arbitrary CDP methods. This selection does not launch a browser.")}
        label={t("完整 CDP（调试） · 高度敏感权限", "Full CDP (debug) · Highly sensitive permission")}
        loading={mutation.isPending} onCancel={() => setConfirmFull(false)}
        onConfirm={() => mutation.mutate("full_debug")} />}
      <dl className="permission-facts">
        <div><dt>{t("受限", "Restricted")}</dt><dd>{t("导航 · DOM · 截图", "navigate · DOM · screenshot")}</dd></div>
        <div><dt>{t("完整调试", "Full debug")}</dt><dd>{t("请求 · Cookie · 任意方法", "requests · cookies · arbitrary methods")}</dd></div>
        <div><dt>{t("传输", "Transport")}</dt><dd>{permission.transport_enabled ? t("启用", "enabled") : t("关闭", "closed")}</dd></div>
      </dl>
      <p className="permission-closed-note">
        {t("此处只保存能力上限；浏览器启动、CDP 传输与运行时授权仍保持关闭。", "This stores only the capability ceiling; browser launch, CDP transport, and runtime authorization remain closed.")}
      </p>
      {mutation.isError && <MutationError error={mutation.error}
        fallback={t("浏览器 CDP 权限切换失败", "Browser CDP permission switch failed")} />}
    </section>
  );
}

const interactionModes: Array<{
  id: RunExecutionInteractionView["mode"];
  chinese: string;
  english: string;
  detailChinese: string;
  detailEnglish: string;
  icon: typeof Eye;
}> = [
  { id: "preview", chinese: "预览", english: "Preview", detailChinese: "零进程权限", detailEnglish: "No process authority", icon: Eye },
  { id: "controlled", chinese: "Code", english: "Code", detailChinese: "受控无状态命令", detailEnglish: "Controlled stateless commands", icon: Code2 },
  { id: "debug", chinese: "Debug", english: "Debug", detailChinese: "用户终端优先", detailEnglish: "User terminal first", icon: Bug },
  { id: "cyber", chinese: "Cyber", english: "Cyber", detailChinese: "容器持久终端", detailEnglish: "Persistent container terminal", icon: Container },
];

function interactionCompatible(detail: RunDetailView,
  target: RunExecutionInteractionView["mode"]) {
  if (target === "preview") return true;
  if (target === "cyber") {
    return detail.mode.surface === "cyber" && detail.execution_profile.profile === "docker";
  }
  return detail.mode.surface === "code" && detail.execution_profile.profile === "local";
}

export function ExecutionInteractionPanel({ client, detail }: {
  client: CyberAgentClient;
  detail: RunDetailView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const interaction = detail.execution_interaction;
  const [pendingMode, setPendingMode] =
    useState<RunExecutionInteractionView["mode"] | null>(null);
  const mutableStatus = detail.run.status === "created" || detail.run.status === "paused";
  const mutable = client.hasControl && mutableStatus && !detail.execution_lease?.active;
  const mutation = useMutation({
    mutationFn: (target: RunExecutionInteractionView["mode"]) => {
      const body: RunExecutionInteractionControlRequestView = {
        mode: target,
        trust: target === "preview" ? "untrusted" : "trusted",
        reason: "settings execution interaction selection",
      };
      if (target !== "preview") body.confirm_workspace_trust = true;
      if (target === "debug") body.confirm_debug_boundary = true;
      if (target === "cyber") body.confirm_container_boundary = true;
      return client.postControl<RunExecutionInteractionControlView>(
        `/runs/${encodeURIComponent(detail.run.id)}/execution-interaction`,
        body,
        `settings-execution-interaction-${globalThis.crypto.randomUUID()}`,
      );
    },
    onSuccess: (result) => {
      setPendingMode(null);
      queryClient.setQueryData<RunDetailView>(["run", detail.run.id], (current) => current
        ? { ...current, execution_interaction: result.execution_interaction }
        : current);
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id, "events"] });
    },
  });
  const choose = (target: RunExecutionInteractionView["mode"]) => {
    if (target === "preview") mutation.mutate(target);
    else setPendingMode(target);
  };
  return (
    <section className="permission-control-card execution-interaction-section">
      <div className="section-heading">
        <div>
          <h2><Terminal aria-hidden="true" size={16} />{t("交互信任模型", "Interaction trust model")}</h2>
          <span>{detail.mode.surface} · {detail.execution_profile.profile}</span>
        </div>
        <StatusBadge status={interaction.workspace_trust} />
      </div>
      <div aria-label={t("Run 执行交互", "Run execution interaction")}
        className="permission-option-grid permission-option-grid-four" role="group">
        {interactionModes.map(({ id, chinese, english, detailChinese, detailEnglish, icon: Icon }) => {
          const compatible = interactionCompatible(detail, id);
          return <button aria-pressed={interaction.mode === id}
            disabled={!mutable || mutation.isPending || interaction.mode === id || !compatible}
            key={id} onClick={() => choose(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{t(chinese, english)}</strong><small>{compatible ? t(detailChinese, detailEnglish) : t("环境或工作面不匹配", "Environment or surface mismatch")}</small></span>
            {interaction.mode === id && <Check aria-hidden="true" size={15} />}
          </button>;
        })}
      </div>
      {pendingMode && <PermissionConfirmation
        description={pendingMode === "controlled"
          ? t("信任当前工作区并使用受控的一次性命令。", "Trust this workspace and use controlled one-shot commands.")
          : pendingMode === "debug"
            ? t("信任当前工作区并开放限时 Debug 交互边界。", "Trust this workspace and open a time-limited Debug interaction boundary.")
            : t("信任当前 Cyber 容器并开放持久容器终端边界。", "Trust this Cyber container and open a persistent container terminal boundary.")}
        label={(() => { const option = interactionModes.find(({ id }) => id === pendingMode); return option ? t(option.chinese, option.english) : pendingMode; })()}
        loading={mutation.isPending} onCancel={() => setPendingMode(null)}
        onConfirm={() => mutation.mutate(pendingMode)} />}
      <dl className="permission-facts">
        <div><dt>{t("命令", "Command")}</dt><dd>{localizedProtocolValue(interaction.command_form, t)}</dd></div>
        <div><dt>{t("终端", "Terminal")}</dt><dd>{interaction.persistent_terminal ? t("持久", "persistent") : t("无状态", "stateless")}</dd></div>
        <div><dt>{t("闸门", "Gate")}</dt><dd>{localizedProtocolValue(interaction.required_gate, t)}</dd></div>
      </dl>
      {mutation.isError && <MutationError error={mutation.error}
        fallback={t("交互信任模型切换失败", "Interaction trust model switch failed")} />}
    </section>
  );
}

function PermissionConfirmation({ description, label, loading, onCancel, onConfirm }: {
  description: string;
  label: string;
  loading: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useLocale();
  return <div className="permission-confirmation" role="alert">
    <ShieldAlert aria-hidden="true" size={17} />
    <span><strong>{label}</strong><small>{description}</small></span>
    <button className="secondary-button" onClick={onCancel} type="button">{t("取消", "Cancel")}</button>
    <button className="danger-button" disabled={loading} onClick={onConfirm} type="button">
      {loading
        ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
        : <Check aria-hidden="true" size={15} />}
      {t("确认", "Confirm")}
    </button>
  </div>;
}

function MutationError({ error, fallback }: { error: unknown; fallback: string }) {
  return <div className="inline-warning" role="alert">
    {error instanceof Error ? error.message : fallback}
  </div>;
}

function localizedProtocolValue(value: string,
  t: (chinese: string, english: string) => string): string {
  const labels: Record<string, [string, string]> = {
    none: ["无", "none"], disabled: ["禁用", "disabled"], required: ["必需", "required"],
    conservative: ["保守", "conservative"], approval: ["用户审批", "user approval"],
    workspace: ["工作区", "workspace"], unrestricted: ["不受限", "unrestricted"],
    closed: ["关闭", "closed"], full: ["完整", "full"], fixed: ["固定", "fixed"],
    stateless: ["无状态", "stateless"], persistent: ["持久", "persistent"],
  };
  const label = labels[value];
  return label ? t(label[0], label[1]) : value.replaceAll("_", " ");
}
