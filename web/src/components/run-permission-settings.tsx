import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bug,
  Check,
  Code2,
  Container,
  Eye,
  LoaderCircle,
  MonitorUp,
  ShieldAlert,
  Terminal,
} from "lucide-react";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type {
  CapabilityReadinessOptionView,
  RunDetailView,
  RunCapabilityReadinessView,
  RunExecutionInteractionControlRequestView,
  RunExecutionInteractionControlView,
  RunExecutionInteractionView,
  RunExecutionProfileControlView,
  RunExecutionProfileView,
  StandardCodePresetControlRequestView,
  StandardCodePresetControlView,
} from "../api/types";
import { shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { ErrorState, LoadingState, StatusBadge } from "./common";
import { v2QueryKeys } from "../v2/query-keys";

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
  { id: "local", chinese: "本地工作区", english: "Local workspace", detailChinese: "按当前权限执行本地命令", detailEnglish: "Local commands under the current permission", icon: Terminal },
];

export function ExecutionProfilePanel({ client, detail, readiness }: {
  client: CyberAgentClient;
  detail: RunDetailView;
  readiness: RunCapabilityReadinessView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const profile = detail.execution_profile;
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
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id], exact: true });
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id, "events"] });
      void queryClient.invalidateQueries({
        queryKey: ["run", detail.run.id, "capability-readiness"],
      });
    },
  });
  const selectedReadiness = selectedCapabilityReadiness(readiness.profiles);
  const boundary = capabilityReadinessSummary(selectedReadiness,
    t("操作员意图", "Operator intent"), t);
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
        {executionProfiles.map(({ id, chinese, english, detailChinese, detailEnglish, icon: Icon }) => {
          const option = capabilityReadinessOption(readiness.profiles, id);
          return <button aria-pressed={option.selected}
            disabled={mutation.isPending || option.selected || !option.selectable}
            key={id} onClick={() => mutation.mutate(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{t(chinese, english)}</strong><CapabilityState option={option} />
              <small>{capabilityReadinessDetail(
              option, t(detailChinese, detailEnglish), t)}</small></span>
            {option.selected && <Check aria-hidden="true" size={15} />}
          </button>
        })}
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

export function ExecutionInteractionPanel({ client, detail, readiness }: {
  client: CyberAgentClient;
  detail: RunDetailView;
  readiness: RunCapabilityReadinessView;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const interaction = detail.execution_interaction;
  const [pendingMode, setPendingMode] =
    useState<RunExecutionInteractionView["mode"] | null>(null);
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
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id], exact: true });
      void queryClient.invalidateQueries({ queryKey: ["run", detail.run.id, "events"] });
      void queryClient.invalidateQueries({
        queryKey: ["run", detail.run.id, "capability-readiness"],
      });
    },
  });
  const choose = (target: RunExecutionInteractionView["mode"]) => {
    if (target === "preview") mutation.mutate(target);
    else setPendingMode(target);
  };
  const selectedReadiness = selectedCapabilityReadiness(readiness.interactions);
  const renderOption = ({ id, chinese, english, detailChinese, detailEnglish,
    icon: Icon }: typeof interactionModes[number]) => {
    const option = capabilityReadinessOption(readiness.interactions, id);
    const advancedRisk = id === "debug" || id === "cyber";
    return <button aria-pressed={option.selected}
      className={advancedRisk ? "danger" : ""}
      disabled={mutation.isPending || option.selected || !option.selectable}
      key={id} onClick={() => choose(id)} type="button">
      <Icon aria-hidden="true" size={17} />
      <span><strong>{t(chinese, english)}</strong>
        <CapabilityState advancedRisk={advancedRisk} option={option} />
        <small>{capabilityReadinessDetail(option,
          t(detailChinese, detailEnglish), t)}</small></span>
      {option.selected && <Check aria-hidden="true" size={15} />}
    </button>;
  };
  const safeInteractions = interactionModes.filter(({ id }) =>
    id === "preview" || id === "controlled");
  const advancedInteractions = interactionModes.filter(({ id }) =>
    id === "debug" || id === "cyber");
  const advancedSelected = advancedInteractions.some(({ id }) =>
    capabilityReadinessOption(readiness.interactions, id).selected);
  return (
    <section className="permission-control-card execution-interaction-section">
      <div className="section-heading">
        <div>
          <h2><Terminal aria-hidden="true" size={16} />{t("交互信任模型", "Interaction trust model")}</h2>
          <span>{capabilityReadinessSummary(selectedReadiness,
            `${detail.mode.surface} · ${detail.execution_profile.profile}`, t)}</span>
        </div>
        <StatusBadge status={interaction.workspace_trust} />
      </div>
      <div aria-label={t("Run 执行交互", "Run execution interaction")}
        className="permission-option-grid permission-option-grid-two" role="group">
        {safeInteractions.map(renderOption)}
      </div>
      <details className="permission-advanced-disclosure" open={advancedSelected || undefined}>
        <summary><ShieldAlert aria-hidden="true" size={15} />
          <span><strong>{t("高级交互模式", "Advanced interaction modes")}</strong>
            <small>{t("Debug 与 Cyber 会扩大终端持续时间或容器边界",
              "Debug and Cyber widen terminal lifetime or container boundaries")}</small></span>
        </summary>
        <div aria-label={t("高级 Run 执行交互", "Advanced Run execution interaction")}
          className="permission-option-grid permission-option-grid-two" role="group">
          {advancedInteractions.map(renderOption)}
        </div>
      </details>
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

type PresetAction = "configure" | "pause_and_configure";
type PresetBackend = "auto" | "docker";
interface PresetAttempt {
  runID: string;
  threadID?: string;
  action: PresetAction;
  body: StandardCodePresetControlRequestView;
  operationKey: string;
}
interface PresetInteraction {
  attempt: PresetAttempt;
  state: "pending" | "unknown" | "invalidated" | "confirmed";
  result?: StandardCodePresetControlView;
  error?: string;
}
const presetIntentKey = (runID: string) => ["run", runID, "standard-code-preset-intent"] as const;

export function StandardCodeReadinessPanel({ client, detail, readiness, threadID, configureDisabledReason }: {
  client: CyberAgentClient;
  detail: RunDetailView;
  readiness: RunCapabilityReadinessView;
  threadID?: string;
  configureDisabledReason?: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const option = capabilityReadinessOption(readiness.presets, "standard_code");
  const runtime = readiness.command_runtime;
  const configuredDelivery = Boolean(threadID) && detail.run.standard_code_preset_configured === true &&
    detail.mode.phase === "deliver";
  const intent = useQuery<PresetInteraction | null>({ queryKey: presetIntentKey(detail.run.id),
    queryFn: () => null, enabled: false, initialData: null, gcTime: Infinity });
  const result = intent.data?.result;
  const pending = intent.data?.state === "pending";
  const unknown = intent.data?.state === "unknown";
  const invalidated = intent.data?.state === "invalidated";
  const waiting = intent.data?.state === "confirmed" && result?.status === "waiting_for_pause";
  const pendingTrust = intent.data?.state === "confirmed" && result?.trust_required && result.trust_digest ? result : null;
  const action: PresetAction = detail.run.status === "running"
    ? "pause_and_configure" : "configure";
  const mutation = useMutation({
    mutationFn: (attempt: PresetAttempt) => client.configureStandardCode(attempt.runID, attempt.action,
      attempt.body, attempt.operationKey),
    onSuccess: (next, attempt) => {
      queryClient.setQueryData<PresetInteraction | null>(presetIntentKey(attempt.runID), (current) =>
        current?.attempt.operationKey === attempt.operationKey ? { attempt, state: "confirmed", result: next } : current);
      if (next.status === "configured" && next.run && next.mode &&
        next.execution_profile && next.execution_interaction &&
        next.execution_permission && next.browser_cdp_permission) {
        if (next.run.id === attempt.runID) {
          queryClient.setQueryData<RunDetailView>(["run", attempt.runID], (current) => current
            ? { ...current, run: { ...next.run!, standard_code_preset_configured: true }, mode: next.mode!,
                execution_profile: next.execution_profile!,
                execution_interaction: next.execution_interaction!,
                execution_permission: next.execution_permission!,
                browser_cdp_permission: next.browser_cdp_permission! }
            : current);
        } else {
          void queryClient.invalidateQueries({ queryKey: ["runs"] });
          void queryClient.invalidateQueries({ queryKey: ["run", next.run.id] });
        }
      }
      if (next.run_id && next.run_id !== attempt.runID) void queryClient.invalidateQueries({ queryKey: ["run", next.run_id] });
    },
    onError: (error, attempt) => {
      queryClient.setQueryData<PresetInteraction | null>(presetIntentKey(attempt.runID), (current) =>
        current?.attempt.operationKey === attempt.operationKey
          ? error instanceof APIRequestError && error.operationKeyInvalidated === true
            ? { attempt, state: "invalidated", error: error.message }
            : { ...current, state: "unknown", error: error.message } : current);
    },
    onSettled: (_next, _error, attempt) => {
      void queryClient.invalidateQueries({ queryKey: ["run", attempt.runID] });
      if (attempt.threadID) {
        void queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(attempt.threadID) });
        void queryClient.invalidateQueries({ queryKey: v2QueryKeys.permission(attempt.threadID) });
      }
    },
  });
  const submit = (attempt: PresetAttempt) => {
    const current = queryClient.getQueryData<PresetInteraction | null>(presetIntentKey(attempt.runID));
    if (!client.hasStandardCodePreset || current?.state === "pending" ||
      (current?.state === "unknown" && current.attempt.operationKey !== attempt.operationKey)) return;
    queryClient.setQueryData<PresetInteraction>(presetIntentKey(attempt.runID), { attempt, state: "pending", result: current?.result });
    mutation.mutate(attempt);
  };
  const invoke = (backend: PresetBackend, selectedAction = action, confirm = false, digest?: string) => {
    if (configuredDelivery) return;
    const previous = intent.data?.attempt;
    // A repeated response asking for the same confirmed digest is still the same
    // attempt. A newly reviewed digest or first trust confirmation is a new intent.
    const sameTrust = confirm && previous?.body.confirm_workspace_trust &&
      previous.body.expected_trust_digest === digest && previous.body.backend_intent === backend && previous.action === selectedAction;
    submit(sameTrust ? previous : { runID: detail.run.id, threadID, action: selectedAction,
      body: { version: "standard_code_preset.v1", backend_intent: backend, confirm_workspace_trust: confirm,
        ...(digest ? { expected_trust_digest: digest } : {}) },
      operationKey: `settings-standard-code-${globalThis.crypto.randomUUID()}` });
  };
  const recheck = () => {
    const current = queryClient.getQueryData<PresetInteraction | null>(presetIntentKey(detail.run.id));
    if (current?.state !== "invalidated" || current.attempt.operationKey !== intent.data?.attempt.operationKey ||
      configureDisabledReason || configuredDelivery || !client.hasStandardCodePreset) return;
    queryClient.setQueryData(presetIntentKey(detail.run.id), null);
    invoke(current.attempt.body.backend_intent as PresetBackend);
  };
  const statusLabel = configuredDelivery ? t("已配置，交付中", "Configured, in Deliver") : result
    ? result.status === "configured" ? t("已配置", "configured")
      : result.status === "waiting_for_pause" ? t("等待静止", "waiting for quiescence")
        : t("被阻止", "blocked")
    : option.runtime_available ? t("就绪", "ready") : t("未就绪", "not ready");
  const pauseCanResolve = detail.run.status === "running" &&
    option.blocked_by.length > 0 && option.blocked_by.every((blocker) =>
      blocker === "run_not_quiescent" || blocker === "execution_lease_active");
  const runtimeFacts = <>
    <dl className="permission-facts">
      <div><dt>{t("已选择", "Selected")}</dt><dd>{option.selected ? t("是", "yes") : t("否", "no")}</dd></div>
      <div><dt>{t("可选择", "Selectable")}</dt><dd>{option.selectable ? t("是", "yes") : t("否", "no")}</dd></div>
      <div><dt>{t("运行时", "Runtime")}</dt><dd>{option.runtime_available ? t("可用", "available") : t("不可用", "unavailable")}</dd></div>
      <div><dt>{t("协议", "Protocol")}</dt><dd>{runtime.protocol_available ? t("存在", "available") : t("缺失", "missing")}</dd></div>
      <div><dt>{t("Adapter", "Adapter")}</dt><dd>{runtime.adapter_installed ? t("已安装", "installed") : t("未安装", "not installed")}</dd></div>
      <div><dt>{t("后端", "Backend")}</dt><dd>{runtime.adapter_ready ? t("就绪", "ready") : t("未就绪", "not ready")}</dd></div>
      <div><dt>{t("当前 Run", "Current Run")}</dt><dd>{runtime.current_run_granted ? t("已授予", "granted") : t("未授予", "not granted")}</dd></div>
      <div><dt>{t("预设状态", "Preset status")}</dt><dd>{statusLabel}</dd></div>
      <div><dt>{t("网络", "Network")}</dt><dd>{result?.network ?? "disabled"}</dd></div>
      <div><dt>{t("凭证", "Credentials")}</dt><dd>{result?.credentials ?? "none"}</dd></div>
    </dl>
    {runtime.current_run_granted && <p className="permission-closed-note">{runtime.adapter_kind} · {runtime.backend}</p>}
  </>;
  return <section className="permission-control-card standard-code-readiness-section">
    <div className="section-heading">
      <div>
        <h2><Code2 aria-hidden="true" size={16} />Standard Code</h2>
        <span>{configuredDelivery ? t("编码环境已配置，继续交付已选计划。", "The coding environment is configured. Continue delivering the selected plan.")
          : capabilityReadinessSummary(option,
            threadID ? t("编码环境", "Coding environment") : t("原子预设 readiness", "Atomic preset readiness"), t)}</span>
      </div>
      <StatusBadge status={configuredDelivery ? "configured" : result?.status === "configured" || option.selected
        ? "ready" : "blocked"} />
    </div>
    <div aria-label={t("Standard Code 预设", "Standard Code preset")}
      className="permission-option-grid permission-option-grid-two" role="group">
      <button aria-pressed={configuredDelivery || option.selected}
        disabled={configuredDelivery || !client.hasStandardCodePreset || Boolean(configureDisabledReason) || pending || unknown || invalidated || waiting || Boolean(pendingTrust) || option.selected ||
          (!option.selectable && !pauseCanResolve)}
        onClick={() => invoke("auto")} type="button">
        <Code2 aria-hidden="true" size={17} />
        <span>
          <strong>{configuredDelivery ? t("交付中", "In Deliver") : detail.run.status === "running"
            ? t("暂停并开始编码", "Pause and start coding")
            : t("开始编码", "Start coding")}</strong>
          {configuredDelivery ? <em className="capability-state capability-state-selected">{t("已配置", "Configured")}</em>
            : <CapabilityState option={option} />}
          <small>{configuredDelivery
            ? t("沿用已选计划继续交付；执行权限仍按每次操作检查。", "Continue with the selected plan; execution authority is checked for each operation.")
            : capabilityReadinessDetail(option,
              t("工作区执行与受控沙箱", "Workspace access with controlled sandbox"), t)}</small>
        </span>
        {(configuredDelivery || option.selected) && <Check aria-hidden="true" size={15} />}
      </button>
      {result?.docker_readiness.available && result.next_steps.includes("select_docker") &&
        <button disabled={configuredDelivery || pending || unknown || waiting || Boolean(configureDisabledReason)} onClick={() => invoke("docker")} type="button">
          <Container aria-hidden="true" size={17} />
          <span><strong>{t("显式使用 Docker", "Use Docker explicitly")}</strong>
            <small>{t("固定 network=none 与无凭证", "Fixed network=none and no credentials")}</small>
          </span>
        </button>}
    </div>
    {configureDisabledReason && <p>{configureDisabledReason}</p>}
    {pendingTrust && !configureDisabledReason && !configuredDelivery && <PermissionConfirmation
      description={`${t("确认当前工作区来源摘要后，创建或复用受信任 Drydock 并一次性提交完整预设。",
        "Confirm the reviewed Workspace source digest, then create or reuse the trusted Drydock and commit the complete preset once.")} ${pendingTrust.trust_digest}`}
      label={t("确认工作区来源", "Confirm Workspace source")}
      loading={pending} onCancel={() => queryClient.setQueryData(presetIntentKey(detail.run.id), null)}
      onConfirm={() => invoke(pendingTrust.backend_intent as PresetBackend, pendingTrust.action as PresetAction, true,
        pendingTrust.trust_digest)} />}
    {threadID ? <><p>{t("此编码预设不启用网络，也不注入凭证。", "This coding preset enables no network access and injects no credentials.")}</p>
      <details><summary>{t("查看运行环境详情", "View runtime environment details")}</summary>{runtimeFacts}</details></> : runtimeFacts}
    {result && result.status !== "configured" && result.next_steps.length > 0 &&
      <p className="permission-closed-note">
        {t("下一步", "Next")}: {result.next_steps.map((step) =>
          localizedStandardCodeNextStep(step, t)).join(" · ")}
      </p>}
    {result?.status === "waiting_for_pause" && <div className="permission-readiness-explanation"
      role="status">
      <strong>{t("暂停尚未完成", "Pause is not complete")}</strong>
      <span>{t("控制面已请求暂停，但在 Run 完全静止且执行租约释放前不会提交 Standard Code 配置；这不是配置成功。",
        "The control plane requested a pause, but Standard Code is not committed until the Run is fully quiescent and its execution lease is released. This is not a successful configuration.")}</span>
      {result.blocked_by.length > 0 && <small>{result.blocked_by.map((blocker) =>
        localizedReadinessValue(readinessBlockerLabels, blocker, t)).join(" · ")}</small>}
    </div>}
    {result?.status === "configured" && result.run_id && result.run_id !== detail.run.id &&
      <p className="permission-closed-note">
        {t("已创建新的 Code Run", "Created a new Code Run")}: {shortID(result.run_id)}
      </p>}
    {result?.status === "waiting_for_pause" && intent.data?.state === "confirmed" &&
      <button className="command-button" disabled={pending}
        onClick={() => submit(intent.data!.attempt)} type="button">
        {pending ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
          : t("重新检查静止状态", "Check quiescence again")}
      </button>}
    {pending && <p role="status">{t("正在确认编码配置；关闭后可回到此执行查看结果。",
      "Checking the coding configuration; return to this execution after closing to see the result.")}</p>}
    {unknown && <div role="alert"><p>{t("编码配置结果尚未确认，请使用原请求确认。", "The coding configuration result is unknown. Confirm the original request.")} {intent.data?.error}</p>
      <button className="command-button" disabled={!client.hasStandardCodePreset} onClick={() => submit(intent.data!.attempt)} type="button">
        {t("确认上次编码配置", "Confirm previous coding configuration")}</button></div>}
    {invalidated && <div role="alert"><p>{t("任务配置已变化，之前的编码配置请求已失效，不会再应用。请重新核对环境与来源，确认后再配置。",
      "The task configuration changed and the previous coding request can no longer be applied. Recheck the environment and source before configuring again.")}</p>
      <button className="command-button" disabled={configuredDelivery || !client.hasStandardCodePreset || Boolean(configureDisabledReason)} onClick={recheck} type="button">
        {t("重新核对编码配置", "Recheck coding configuration")}</button></div>}
    {!client.hasStandardCodePreset && <p className="permission-closed-note">
      {t("此进程未启用 Standard Code 原子预设控制。",
        "This process has not enabled Standard Code atomic preset control.")}
    </p>}
  </section>;
}

function localizedStandardCodeNextStep(value: string, t: ReadinessTranslator): string {
  const labels: Record<string, [string, string]> = {
    confirm_workspace_trust: ["确认工作区来源", "Confirm Workspace source"],
    pause_and_configure: ["显式暂停并配置", "Pause and configure explicitly"],
    wait_for_quiescence: ["等待执行静止", "Wait for quiescence"],
    select_docker: ["显式选择 Docker", "Select Docker explicitly"],
    select_ask: ["改用 Ask 审批", "Use Ask approval"],
    retry_readiness: ["修复后重试 readiness", "Repair and retry readiness"],
    create_new_run: ["创建新的 Code Run", "Create a new Code Run"],
  };
  const label = labels[value];
  return label ? t(label[0], label[1]) : value.replaceAll("_", " ");
}

type ReadinessTranslator = (chinese: string, english: string) => string;

function capabilityReadinessOption(options: CapabilityReadinessOptionView[],
  value: string): CapabilityReadinessOptionView {
  const option = options.find((candidate) => candidate.value === value);
  if (!option) {
    throw new Error(`Capability readiness option ${value} is missing`);
  }
  return option;
}

function selectedCapabilityReadiness(
  options: CapabilityReadinessOptionView[],
): CapabilityReadinessOptionView {
  const selected = options.find((option) => option.selected);
  if (!selected) {
    throw new Error("Capability readiness selection is missing");
  }
  return selected;
}

const readinessBlockerLabels: Record<string, [string, string]> = {
  run_not_quiescent: ["Run 尚未暂停", "Run is not quiescent"],
  execution_lease_active: ["执行租约占用中", "Execution lease is active"],
  startup_gate_closed: ["启动闸门未开启", "Startup gate is closed"],
  capability_not_implemented: ["能力尚未实现", "Capability is not implemented"],
  backend_not_ready: ["后端 readiness 未通过", "Backend readiness has not passed"],
  surface_mismatch: ["工作面不匹配", "Surface mismatch"],
  profile_mismatch: ["执行环境不匹配", "Profile mismatch"],
  permission_mismatch: ["权限档位不匹配", "Permission mismatch"],
  workspace_untrusted: ["工作区尚未信任", "Workspace is untrusted"],
  sandbox_unproven: ["沙箱隔离尚未证明", "Sandbox isolation is unproven"],
  docker_unavailable: ["Docker 不可用", "Docker is unavailable"],
};

const readinessRemediationLabels: Record<string, [string, string]> = {
  pause_run: ["暂停 Run", "Pause the Run"],
  create_new_run: ["创建新 Run", "Create a new Run"],
  wait_for_execution_lease: ["等待租约释放", "Wait for the lease to release"],
  restart_with_startup_gate: ["开启闸门并重启", "Restart with the startup gate"],
  upgrade_application: ["升级到实现该能力的版本", "Upgrade to a version that implements it"],
  retry_backend_readiness: ["修复后端并重新检查", "Repair the backend and retry readiness"],
  select_required_surface: ["选择所需工作面", "Select the required surface"],
  select_required_profile: ["选择所需执行环境", "Select the required profile"],
  select_required_permission: ["选择所需权限档位", "Select the required permission"],
  trust_workspace: ["确认工作区信任", "Confirm Workspace trust"],
  verify_sandbox: ["安装并验证沙箱", "Install and verify the sandbox"],
  install_or_start_docker: ["安装或启动 Docker", "Install or start Docker"],
};

function capabilityReadinessSummary(option: CapabilityReadinessOptionView,
  fallback: string, t: ReadinessTranslator): string {
  if (option.blocked_by.length === 0) {
    return option.runtime_available ? fallback : t("运行时不可用", "Runtime unavailable");
  }
  return localizedReadinessValue(readinessBlockerLabels, option.blocked_by[0]!, t);
}

function capabilityReadinessDetail(option: CapabilityReadinessOptionView,
  fallback: string, t: ReadinessTranslator): string {
  if (option.blocked_by.length === 0) {
    return option.runtime_available ? fallback : t("运行时不可用", "Runtime unavailable");
  }
  const blockers = option.blocked_by.map((value) =>
    localizedReadinessValue(readinessBlockerLabels, value, t)).join(" · ");
  const remediation = option.remediation.map((value) =>
    localizedReadinessValue(readinessRemediationLabels, value, t)).join(" · ");
  const restart = option.restart_required ? t(" · 需重启", " · restart required") : "";
  return `${blockers} → ${remediation}${restart}`;
}

type CapabilityPresentationState = "selected" | "temporarily_locked" |
  "startup_unavailable" | "backend_unavailable" | "incompatible" |
  "advanced_risk" | "action_required" | "available" | "unavailable";

function capabilityPresentationState(option: CapabilityReadinessOptionView,
  advancedRisk = false): CapabilityPresentationState {
  if (option.selected) return "selected";
  const blockers = new Set(option.blocked_by);
  if (blockers.has("run_not_quiescent") || blockers.has("execution_lease_active")) {
    return "temporarily_locked";
  }
  if (blockers.has("startup_gate_closed") || blockers.has("capability_not_implemented")) {
    return "startup_unavailable";
  }
  if (blockers.has("backend_not_ready") || blockers.has("sandbox_unproven") ||
    blockers.has("docker_unavailable")) {
    return "backend_unavailable";
  }
  if (blockers.has("surface_mismatch") || blockers.has("profile_mismatch") ||
    blockers.has("permission_mismatch")) {
    return "incompatible";
  }
  if (blockers.has("workspace_untrusted")) return "action_required";
  if (advancedRisk) return "advanced_risk";
  if (!option.selectable || !option.runtime_available) return "unavailable";
  return "available";
}

function CapabilityState({ option, advancedRisk = false }: {
  option: CapabilityReadinessOptionView;
  advancedRisk?: boolean;
}) {
  const { t } = useLocale();
  const state = capabilityPresentationState(option, advancedRisk);
  const labels: Record<CapabilityPresentationState, [string, string]> = {
    selected: ["已选择", "Selected"],
    temporarily_locked: ["暂时锁定", "Temporarily locked"],
    startup_unavailable: ["启动时不可用", "Unavailable at startup"],
    backend_unavailable: ["后端不可用", "Backend unavailable"],
    incompatible: ["不兼容", "Incompatible"],
    advanced_risk: ["高级风险", "Advanced risk"],
    action_required: ["需要操作", "Action required"],
    available: ["可用", "Available"],
    unavailable: ["不可用", "Unavailable"],
  };
  return <em className={`capability-state capability-state-${state}`}
    data-readiness-state={state}>{t(...labels[state])}</em>;
}

function localizedReadinessValue(labels: Record<string, [string, string]>,
  value: string, t: ReadinessTranslator): string {
  const label = labels[value];
  return label ? t(label[0], label[1]) : value.replaceAll("_", " ");
}

export function PermissionConfirmation({ description, label, loading, onCancel, onConfirm }: {
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
