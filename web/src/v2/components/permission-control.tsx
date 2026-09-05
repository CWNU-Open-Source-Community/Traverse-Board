import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bug, Check, ChevronDown, Container, ShieldCheck, ShieldOff, UserCheck } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type {
  ThreadExecutionPermissionControlRequestView,
  ThreadExecutionPermissionControlView,
  ThreadExecutionPermissionView,
} from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { browserCDPQueryKey, V2BrowserCDPControl } from "./browser-cdp-control";
import { V2ConfirmDialog } from "./dialog";
import { V2HighRiskActivationDialog } from "./high-risk-activation-dialog";
import { V2RunNetworkAuthorityControl } from "./run-network-authority-control";

type PermissionMode = ThreadExecutionPermissionView["mode"];

const options: Array<{
  mode: PermissionMode;
  label: string;
  detail: string;
  icon: typeof ShieldCheck;
}> = [
  { mode: "conservative", label: "保守模式", detail: "仅固定安全模板", icon: ShieldCheck },
  { mode: "workspace_access", label: "工作区访问", detail: "隔离读写，无网络", icon: Container },
  { mode: "approval", label: "逐次审批", detail: "宿主命令逐条确认", icon: UserCheck },
  { mode: "full_access", label: "完全访问", detail: "宿主文件、网络；完整 CDP 默认开启、可关闭", icon: ShieldOff },
  { mode: "debug", label: "调试模式", detail: "在完全访问上增加持久调试运行时", icon: Bug },
];

const riskRank: Record<PermissionMode, number> = {
  conservative: 0,
  workspace_access: 1,
  approval: 2,
  full_access: 3,
  debug: 4,
};

function modeAvailable(permission: ThreadExecutionPermissionView, mode: PermissionMode): boolean {
  if (mode === "conservative") return true;
  if (mode === "workspace_access") return permission.runtime.workspace_sandbox_enabled;
  if (mode === "approval") return permission.runtime.operator_approval_enabled;
  if (mode === "full_access") return permission.runtime.danger_full_access_enabled;
  return permission.runtime.debug_maximum_access_enabled;
}

function requestFor(mode: PermissionMode): ThreadExecutionPermissionControlRequestView {
  return {
    mode,
    reason: "v2 Thread permission selection",
    ...(mode === "workspace_access" ? { confirm_workspace_access: true } : {}),
    ...(mode === "approval" ? { confirm_user_approval: true } : {}),
    ...(mode === "full_access" ? { confirm_danger_full_access: true } : {}),
    ...(mode === "debug" ? { confirm_debug_access: true } : {}),
  };
}

function confirmationCopy(mode: PermissionMode): string {
  if (mode === "workspace_access") {
    return "当前对话及后续执行将获得受控工作区读写能力；网络、凭证和宿主进程仍会被拒绝。";
  }
  if (mode === "approval") return "当前对话的宿主命令将逐条等待你的明确批准。";
  if (mode === "full_access") return "当前对话将获得宿主文件、网络和未沙箱化命令；内置浏览器的完整 CDP 子权限默认开启，也可在权限页单独关闭。这可能造成数据丢失或泄露。";
  return "当前对话将在完全访问上增加持久终端、后台进程和限时输入能力，仅应在调试内核时使用。";
}

function effectCopy(result: ThreadExecutionPermissionControlView): string {
  if (String(result.current_run_effect) === "deferred") return "当前执行保持不变，将用于下一次执行";
  if (!result.execution_permission.runtime_gate_available) return "已选择 · 当前进程未授权";
  if (result.current_run_effect === "paused_and_applied") return "已安全暂停当前执行并应用";
  if (result.current_run_effect === "applied" || result.current_run_synchronized) return "已应用到当前和后续执行";
  return "将用于此对话的后续执行";
}

export function V2PermissionControl({ client, threadID, variant = "menu" }: {
  client: CyberAgentClient;
  threadID: string;
  variant?: "menu" | "settings";
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState<PermissionMode | null>(null);
  const shellRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const riskTriggerRef = useRef<HTMLButtonElement>(null);
  const query = useQuery({
    queryKey: v2QueryKeys.permission(threadID),
    queryFn: ({ signal }) => client.getThreadExecutionPermission(threadID, signal),
    enabled: Boolean(threadID),
    staleTime: 10_000,
  });
  const mutation = useMutation({
    mutationFn: (mode: PermissionMode) => client.changeThreadExecutionPermission(
      threadID, requestFor(mode), `v2-thread-permission-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      queryClient.setQueryData(v2QueryKeys.permission(threadID), result);
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(threadID) });
      if (result.current_run_id) {
        void queryClient.invalidateQueries({ queryKey: browserCDPQueryKey(result.current_run_id) });
      }
      setPending(null);
      if (variant === "menu") setOpen(false);
    },
  });
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!shellRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const permission = query.data?.execution_permission;
  const selected = useMemo(() => options.find(({ mode }) => mode === permission?.mode) ?? options[0],
    [permission?.mode]);
  const choose = (mode: PermissionMode, available: boolean, trigger: HTMLButtonElement) => {
    mutation.reset();
    if (!available) return;
    riskTriggerRef.current = trigger;
    const activatesColdFullAccess = mode === "full_access" &&
      permission?.runtime_gate_available === false;
    if (permission && riskRank[mode] < riskRank[permission.mode] &&
      !activatesColdFullAccess) {
      setPending(null);
      mutation.mutate(mode);
      return;
    }
    if (mode === "conservative") mutation.mutate(mode);
    else setPending(mode);
  };
  const downgrade = () => {
    mutation.reset();
    setPending(null);
    mutation.mutate("conservative");
  };
  const list = permission ? <>
    <div aria-label="对话执行权限" className="v2-permission-options" role="group">
      {options.map(({ mode, label, detail, icon: Icon }) => {
        const available = modeAvailable(permission, mode);
        const active = permission.mode === mode;
        const activeAndAuthorized = active && permission.runtime_gate_available;
        const optionDetail = active && !permission.runtime_gate_available && available
          ? "已保存 · 暂停且静止后确认激活"
          : available ? detail : "当前运行时未授权";
        return <button aria-label={label} aria-pressed={active} className={mode === "full_access" || mode === "debug"
          ? "is-risk" : ""}
          disabled={mutation.isPending || activeAndAuthorized || !available ||
            !client.hasExecutionPermissionControl}
          key={mode} onClick={(event) => choose(mode, available, event.currentTarget)} type="button">
          <Icon aria-hidden="true" size={17} /><span><strong>{label}</strong>
            <small>{optionDetail}</small></span>
          {activeAndAuthorized ? <Check aria-hidden="true" size={15} /> : null}
        </button>;
      })}
    </div>
    {(permission.mode === "full_access" || permission.mode === "debug") &&
      <button className="v2-permission-downgrade" disabled={mutation.isPending ||
        !client.hasExecutionPermissionControl} onClick={downgrade} type="button">
        <ShieldCheck aria-hidden="true" size={16} />
        <span><strong>立即降为保守模式</strong><small>新调用立即失效；既有进程在安全边界终止</small></span>
      </button>}
  </> : null;

  return <div className={`v2-permission-control is-${variant}`} ref={shellRef}>
    {variant === "menu" ? <>
      <button aria-expanded={open} aria-haspopup="menu" className="v2-composer-chip"
        disabled={!threadID} onClick={() => setOpen((value) => !value)} ref={triggerRef} type="button">
        <ShieldCheck aria-hidden="true" size={14} />{query.isLoading ? "读取权限…" : selected.label}
        <ChevronDown aria-hidden="true" size={13} />
      </button>
      {open && <div className="v2-permission-popover" role="menu">
        <header><strong>当前对话权限</strong><span>{query.data ? effectCopy(query.data) : "读取安全策略"}</span></header>
        {query.isError ? <p role="alert">无法读取权限设置</p> : list}
        {mutation.isError && <p role="alert">{mutation.error instanceof Error
          ? mutation.error.message : "权限更新失败"}</p>}
      </div>}
    </> : <section className="v2-settings-card v2-permission-settings-card">
      <header><div><h2>当前对话权限</h2><p>{query.data ? effectCopy(query.data) : "读取安全策略"}</p></div>
        <span className={`v2-risk-badge risk-${permission?.risk_tier ?? "minimal"}`}>{permission?.risk_tier ?? "—"}</span></header>
      {!threadID ? <p className="v2-settings-empty">先从侧栏打开一个对话。</p> : query.isLoading
        ? <p className="v2-settings-empty">正在读取权限…</p> : query.isError
          ? <p className="v2-settings-empty" role="alert">无法读取权限设置</p> : list}
      <V2BrowserCDPControl client={client} permissionMode={permission?.mode ?? null}
        executionRuntimeAvailable={permission?.runtime_gate_available ?? false}
        runID={query.data?.current_run_id ?? ""} />
      <V2RunNetworkAuthorityControl client={client} runID={query.data?.current_run_id ?? ""}
        threadID={threadID} />
      {mutation.isError && <p className="v2-inline-error" role="alert">{mutation.error instanceof Error
        ? mutation.error.message : "权限更新失败"}</p>}
    </section>}
    <V2ConfirmDialog busy={mutation.isPending} confirmLabel={`启用${options.find(({ mode }) => mode === pending)?.label ?? "权限"}`}
      danger={pending === "full_access" || pending === "debug"}
      description={pending ? confirmationCopy(pending) : ""} onCancel={() => setPending(null)}
      onConfirm={() => pending && mutation.mutate(pending)} open={pending !== null && pending !== "full_access"}
      returnFocusRef={variant === "menu" ? triggerRef : undefined}
      title="确认更改对话权限" />
    <V2HighRiskActivationDialog error={pending === "full_access" && mutation.isError
      ? mutation.error instanceof Error ? mutation.error.message : "权限更新失败" : undefined}
      onCancel={() => {
        if (mutation.isPending) return;
        mutation.reset();
        setPending(null);
      }} onConfirm={() => mutation.mutate("full_access")}
      open={pending === "full_access"} phase={mutation.isPending ? "applying" : "idle"}
      profile="full_access" returnFocusRef={variant === "menu" ? triggerRef : riskTriggerRef} />
  </div>;
}
