import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bug,
  Check,
  Container,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
  UserCheck,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { RunExecutionPermissionView } from "../api/types";
import { shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { ErrorState, LoadingState, StatusBadge } from "./common";
import { PermissionConfirmation } from "./run-permission-settings";

type PermissionMode = RunExecutionPermissionView["mode"];

export type ThreadExecutionPermissionView =
  Omit<RunExecutionPermissionView, "protocol_version"> & {
    thread_id: string;
    protocol_version: "thread_execution_permission.v1";
    applies_to_current_run: boolean;
    applies_to_future_successor_runs: boolean;
  };

export type ThreadExecutionPermissionControlView = {
  execution_permission: ThreadExecutionPermissionView;
  current_run_id?: string;
  current_run_effect?: "applied" | "paused_and_applied" | "no_active_run" | "pending";
  current_run_mode?: PermissionMode;
  current_run_synchronized: boolean;
  replayed: boolean;
};

type PermissionOption = {
  id: PermissionMode;
  chinese: string;
  english: string;
  detailChinese: string;
  detailEnglish: string;
  icon: typeof ShieldCheck;
};

const permissionOptions: PermissionOption[] = [
  { id: "conservative", chinese: "保守模式", english: "Conservative",
    detailChinese: "固定安全模板", detailEnglish: "Fixed safe templates", icon: ShieldCheck },
  { id: "workspace_access", chinese: "工作区执行", english: "Workspace access",
    detailChinese: "隔离运行 · 无网络", detailEnglish: "Sandboxed · no network", icon: Container },
  { id: "approval", chinese: "用户审批", english: "User approval",
    detailChinese: "逐条人工确认", detailEnglish: "Per-command confirmation", icon: UserCheck },
  { id: "full_access", chinese: "完全访问", english: "Full access",
    detailChinese: "宿主机无沙箱", detailEnglish: "Unsandboxed host access", icon: ShieldOff },
  { id: "debug", chinese: "调试模式", english: "Debug",
    detailChinese: "持久交互终端", detailEnglish: "Persistent interactive terminal", icon: Bug },
];

function runtimeAvailable(permission: ThreadExecutionPermissionView,
  mode: PermissionMode): boolean {
  if (mode === "conservative") return true;
  if (mode === "workspace_access") return permission.runtime.workspace_sandbox_enabled;
  if (mode === "approval") return permission.runtime.operator_approval_enabled;
  if (mode === "full_access") return permission.runtime.danger_full_access_enabled;
  return permission.runtime.debug_maximum_access_enabled;
}

function confirmationDescription(mode: PermissionMode,
  t: (chinese: string, english: string) => string): string {
  if (mode === "workspace_access") {
    return t("该 Thread 当前及后续 Run 将允许受控工作区读写与沙箱命令；网络、凭证、主目录和宿主进程仍被拒绝。",
      "The current and future Runs in this Thread will allow controlled workspace access and sandboxed commands; network, credentials, home, and host processes remain denied.");
  }
  if (mode === "approval") {
    return t("该 Thread 当前及后续 Run 的每一条宿主机命令都需要人工批准。",
      "Every host command in the current and future Runs of this Thread will require approval.");
  }
  if (mode === "full_access") {
    return t("该 Thread 当前及后续 Run 将允许无沙箱宿主机文件与网络访问。",
      "The current and future Runs in this Thread will allow unsandboxed host filesystem and network access.");
  }
  return t("该 Thread 当前及后续 Run 将允许持久终端、后台进程和限时 Agent 输入。",
    "The current and future Runs in this Thread will allow persistent terminals, background processes, and time-limited Agent input.");
}

function effectLabel(result: ThreadExecutionPermissionControlView,
  t: (chinese: string, english: string) => string): string {
  if (result.current_run_effect === "paused_and_applied") {
    return t("当前 Run 已安全暂停并应用；后续 Run 会继承此设置。",
      "The current Run was safely paused and updated; future Runs will inherit this setting.");
  }
  if (result.current_run_effect === "applied" || result.current_run_synchronized) {
    return t("当前 Run 已应用；后续 Run 会继承此设置。",
      "Applied to the current Run; future Runs will inherit this setting.");
  }
  if (result.current_run_effect === "pending" && result.current_run_id) {
    return t("当前 Run 尚未同步。选择新档位时会先安全暂停，再把设置应用到整个 Thread。",
      "The current Run is not synchronized. Selecting a new level safely pauses it before applying the Thread setting.");
  }
  return t("当前没有活动 Run；此设置会用于该 Thread 的下一个及后续 Run。",
    "There is no active Run; this setting will apply to the next and future Runs in this Thread.");
}

function requestBody(mode: PermissionMode) {
  return {
    mode,
    reason: "settings Thread execution permission selection",
    ...(mode === "workspace_access" ? { confirm_workspace_access: true } : {}),
    ...(mode === "approval" ? { confirm_user_approval: true } : {}),
    ...(mode === "full_access" ? { confirm_danger_full_access: true } : {}),
    ...(mode === "debug" ? { confirm_debug_access: true } : {}),
  };
}

export function ThreadPermissionSettings({ client, threadID }: {
  client: CyberAgentClient;
  threadID: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [pendingMode, setPendingMode] = useState<PermissionMode | null>(null);
  const queryKey = ["thread", threadID, "execution-permission"] as const;
  const permissionQuery = useQuery({
    queryKey,
    queryFn: ({ signal }) => client.get<ThreadExecutionPermissionControlView>(
      `/threads/${encodeURIComponent(threadID)}/execution-permission`, {}, signal),
    enabled: threadID !== "",
  });
  const mutation = useMutation({
    mutationFn: (mode: PermissionMode) => client.postControl<ThreadExecutionPermissionControlView>(
      `/threads/${encodeURIComponent(threadID)}/execution-permission`,
      requestBody(mode),
      `settings-thread-execution-permission-${globalThis.crypto.randomUUID()}`,
    ),
    onSuccess: (result) => {
      setPendingMode(null);
      queryClient.setQueryData(queryKey, result);
      void queryClient.invalidateQueries({ queryKey: ["thread", threadID] });
      if (result.current_run_id) {
        void queryClient.invalidateQueries({ queryKey: ["run", result.current_run_id] });
      }
    },
  });

  if (!threadID) {
    return <section className="settings-page-section permission-settings-page">
      <h1>{t("权限", "Permissions")}</h1>
      <div className="permission-settings-empty">
        <ShieldCheck aria-hidden="true" size={24} />
        <strong>{t("从侧栏打开一个 Thread", "Open a Thread from the sidebar")}</strong>
        <span>{t("新 Thread 默认使用保守模式；打开后可一次设置整个 Thread。",
          "New Threads use Conservative by default. Open one to set its permission once.")}</span>
      </div>
    </section>;
  }
  if (permissionQuery.isPending) {
    return <LoadingState label={t("加载 Thread 权限", "Loading Thread permissions")} />;
  }
  if (permissionQuery.isError || !permissionQuery.data) {
    return <ErrorState error={permissionQuery.error} />;
  }

  const result = permissionQuery.data;
  const permission = result.execution_permission;
  const safeOptions = permissionOptions.filter(({ id }) => id !== "full_access" && id !== "debug");
  const advancedOptions = permissionOptions.filter(({ id }) => id === "full_access" || id === "debug");
  const advancedSelected = advancedOptions.some(({ id }) => permission.mode === id);
  const choose = (mode: PermissionMode) => {
    if (mode === "conservative") mutation.mutate(mode);
    else setPendingMode(mode);
  };
  const renderOption = ({ id, chinese, english, detailChinese, detailEnglish,
    icon: Icon }: PermissionOption) => {
    const available = runtimeAvailable(permission, id);
    const selected = permission.mode === id;
    const advanced = id === "full_access" || id === "debug";
    return <button aria-pressed={selected} className={advanced ? "danger" : ""}
      disabled={mutation.isPending || selected || !available || !client.hasExecutionPermissionControl}
      key={id} onClick={() => choose(id)} type="button">
      <Icon aria-hidden="true" size={17} />
      <span><strong>{t(chinese, english)}</strong>
        <em className={`capability-state ${available ? (advanced
          ? "capability-state-advanced_risk" : "capability-state-available")
          : "capability-state-startup_unavailable"}`}>
          {selected ? t("已选择", "Selected") : available
            ? advanced ? t("高级风险", "Advanced risk") : t("可用", "Available")
            : t("启动时不可用", "Unavailable at startup")}
        </em>
        <small>{available ? t(detailChinese, detailEnglish) :
          t("此应用启动时未开启所需运行时能力", "The required runtime capability was not enabled at startup")}</small>
      </span>
      {selected && <Check aria-hidden="true" size={15} />}
    </button>;
  };

  return <section className="settings-page-section permission-settings-page">
    <div className="permission-settings-title">
      <div><h1>{t("权限", "Permissions")}</h1>
        <span>Thread {shortID(threadID)} · {t("统一默认", "shared default")}</span></div>
      <StatusBadge status={permission.risk_tier} />
    </div>
    <section className="permission-thread-scope" aria-label={t("Thread 权限作用范围", "Thread permission scope")}>
      <ShieldCheck aria-hidden="true" size={18} />
      <div><strong>{t("设置一次，应用到整个 Thread", "Set once for the whole Thread")}</strong>
        <span>{t("当前 Run 与此 Thread 后续创建的所有 Run 都使用这个档位。若当前 Run 正在执行，系统会先安全暂停再应用。",
          "The current Run and every future Run in this Thread use this level. If the current Run is executing, it is safely paused before the change is applied.")}</span></div>
    </section>
    <section className="permission-control-card execution-permission-section">
      <div className="section-heading"><div>
        <h2><ShieldCheck aria-hidden="true" size={16} />{t("Thread 权限档位", "Thread permission level")}</h2>
        <span>{effectLabel(result, t)}{result.current_run_id
          ? ` · Run ${shortID(result.current_run_id)}` : ""}</span>
      </div></div>
      <div aria-label={t("Thread 执行权限", "Thread execution permission")}
        className="permission-option-grid permission-option-grid-three" role="group">
        {safeOptions.map(renderOption)}
      </div>
      <details className="permission-advanced-disclosure" open={advancedSelected || undefined}>
        <summary><ShieldAlert aria-hidden="true" size={15} />
          <span><strong>{t("高级风险权限", "Advanced risk permissions")}</strong>
            <small>{t("完全访问与调试只在明确需要时开启",
              "Enable Full access and Debug only when explicitly needed")}</small></span>
        </summary>
        <div aria-label={t("高级 Thread 执行权限", "Advanced Thread execution permissions")}
          className="permission-option-grid permission-option-grid-two" role="group">
          {advancedOptions.map(renderOption)}
        </div>
      </details>
      {pendingMode && <PermissionConfirmation
        description={confirmationDescription(pendingMode, t)}
        label={(() => { const option = permissionOptions.find(({ id }) => id === pendingMode);
          return option ? t(option.chinese, option.english) : pendingMode; })()}
        loading={mutation.isPending} onCancel={() => setPendingMode(null)}
        onConfirm={() => mutation.mutate(pendingMode)} />}
      <dl className="permission-facts">
        <div><dt>{t("作用域", "Scope")}</dt><dd>{t("当前与后续 Run", "Current + future Runs")}</dd></div>
        <div><dt>{t("命令", "Commands")}</dt><dd>{permission.command_scope.replaceAll("_", " ")}</dd></div>
        <div><dt>{t("网络", "Network")}</dt><dd>{permission.network_scope.replaceAll("_", " ")}</dd></div>
      </dl>
      {!client.hasExecutionPermissionControl && <p className="permission-closed-note">
        {t("当前连接没有修改权限；这里仍会显示已保存的 Thread 档位。",
          "This connection cannot change permissions; the saved Thread level remains visible.")}
      </p>}
      {mutation.isError && <div className="inline-warning" role="alert">
        {mutation.error instanceof Error ? mutation.error.message :
          t("Thread 权限档位切换失败", "Could not change the Thread permission level")}
      </div>}
    </section>
  </section>;
}
