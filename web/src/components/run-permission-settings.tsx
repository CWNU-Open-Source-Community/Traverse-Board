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
  ShieldCheck,
  ShieldOff,
  Terminal,
  UserCheck,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  RunDetailView,
  RunExecutionInteractionControlRequestView,
  RunExecutionInteractionControlView,
  RunExecutionInteractionView,
  RunExecutionPermissionControlView,
  RunExecutionPermissionView,
  RunExecutionProfileControlView,
  RunExecutionProfileView,
} from "../api/types";
import { shortID } from "../lib/format";
import { ErrorState, LoadingState, StatusBadge } from "./common";

export function RunPermissionSettings({ client, runID }: {
  client: CyberAgentClient;
  runID: string;
}) {
  const detailQuery = useQuery({
    queryKey: ["run", runID],
    queryFn: ({ signal }) => client.get<RunDetailView>(
      `/runs/${encodeURIComponent(runID)}`, {}, signal),
    enabled: runID !== "",
  });
  if (!runID) {
    return <section className="settings-page-section permission-settings-page">
      <h1>权限</h1>
      <div className="permission-settings-empty">
        <ShieldCheck aria-hidden="true" size={24} />
        <strong>选择一个 Run</strong>
        <span>权限档位与执行边界按 Run 独立保存。</span>
      </div>
    </section>;
  }
  if (detailQuery.isPending) {
    return <LoadingState label="加载 Run 权限" />;
  }
  if (detailQuery.isError || !detailQuery.data) {
    return <ErrorState error={detailQuery.error} />;
  }
  const detail = detailQuery.data;
  return <section className="settings-page-section permission-settings-page">
    <div className="permission-settings-title">
      <div>
        <h1>权限</h1>
        <span>{shortID(detail.run.id)} · {detail.run.status}</span>
      </div>
      <StatusBadge status={detail.execution_permission.risk_tier} />
    </div>
    <ExecutionPermissionPanel client={client} detail={detail}
      key={`permission-${detail.run.id}`} />
    <ExecutionInteractionPanel client={client} detail={detail}
      key={`interaction-${detail.run.id}`} />
    <ExecutionProfilePanel client={client} detail={detail}
      key={`profile-${detail.run.id}`} />
    <section className="permission-authority-boundary" aria-label="运行时授权边界">
      <ShieldAlert aria-hidden="true" size={17} />
      <div>
        <strong>运行时授权仍为关闭</strong>
        <span>进程、网络和 Agent 终端输入继续由独立沙箱、审批与限时租约控制。</span>
      </div>
    </section>
  </section>;
}

const executionProfiles: Array<{
  id: RunExecutionProfileView["profile"];
  label: string;
  detail: string;
  icon: typeof Eye;
}> = [
  { id: "preview", label: "Preview", detail: "不启动进程", icon: Eye },
  { id: "docker", label: "Docker", detail: "隔离容器", icon: Container },
  { id: "local", label: "本地工作区", detail: "系统沙箱", icon: Terminal },
];

export function ExecutionProfilePanel({ client, detail }: {
  client: CyberAgentClient;
  detail: RunDetailView;
}) {
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
  let boundary = "操作员意图";
  if (!client.hasControl) boundary = "只读连接";
  else if (!mutableStatus) boundary = "请先暂停 Run";
  else if (detail.execution_lease?.active) boundary = "执行租约占用中";
  return (
    <section className="permission-control-card execution-profile-section">
      <div className="section-heading">
        <div>
          <h2><MonitorUp aria-hidden="true" size={16} />执行环境</h2>
          <span>{boundary}</span>
        </div>
        <StatusBadge status={profile.risk_tier} />
      </div>
      <div aria-label="Run execution profile"
        className="permission-option-grid permission-option-grid-three" role="group">
        {executionProfiles.map(({ id, label, detail: optionDetail, icon: Icon }) => (
          <button aria-pressed={profile.profile === id}
            disabled={!mutable || mutation.isPending || profile.profile === id}
            key={id} onClick={() => mutation.mutate(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{label}</strong><small>{optionDetail}</small></span>
            {profile.profile === id && <Check aria-hidden="true" size={15} />}
          </button>
        ))}
      </div>
      <dl className="permission-facts">
        <div><dt>Backend</dt><dd>{profile.backend}</dd></div>
        <div><dt>Approval</dt><dd>{profile.approval_policy}</dd></div>
        <div><dt>Gate</dt><dd>{profile.required_gate}</dd></div>
      </dl>
      {mutation.isError && <MutationError error={mutation.error}
        fallback="执行环境切换失败" />}
    </section>
  );
}

const executionPermissions: Array<{
  id: RunExecutionPermissionView["mode"];
  label: string;
  detail: string;
  icon: typeof ShieldCheck;
}> = [
  { id: "conservative", label: "保守模式", detail: "固定安全模板", icon: ShieldCheck },
  { id: "approval", label: "用户审批", detail: "逐条人工确认", icon: UserCheck },
  { id: "full_access", label: "完全访问", detail: "宿主机无沙箱", icon: ShieldOff },
  { id: "debug", label: "调试模式", detail: "持久交互终端", icon: Bug },
];

function permissionRuntimeAvailable(
  permission: RunExecutionPermissionView,
  target: RunExecutionPermissionView["mode"],
) {
  switch (target) {
    case "conservative":
      return true;
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
  const queryClient = useQueryClient();
  const permission = detail.execution_permission;
  const [pendingMode, setPendingMode] =
    useState<RunExecutionPermissionView["mode"] | null>(null);
  const mutableStatus = detail.run.status === "created" || detail.run.status === "paused";
  const mutable = client.hasExecutionPermissionControl && mutableStatus &&
    !detail.execution_lease?.active;
  const mutation = useMutation({
    mutationFn: (target: RunExecutionPermissionView["mode"]) => {
      const body: {
        mode: RunExecutionPermissionView["mode"];
        reason: string;
        confirm_user_approval?: boolean;
        confirm_danger_full_access?: boolean;
        confirm_debug_access?: boolean;
      } = { mode: target, reason: "settings execution permission selection" };
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
  let boundary = "Run 策略快照";
  if (!client.hasExecutionPermissionControl) boundary = "启动时未开放权限控制";
  else if (!mutableStatus) boundary = "请先暂停 Run";
  else if (detail.execution_lease?.active) boundary = "执行租约占用中";
  return (
    <section className="permission-control-card execution-permission-section">
      <div className="section-heading">
        <div>
          <h2><ShieldCheck aria-hidden="true" size={16} />权限档位</h2>
          <span>{boundary}</span>
        </div>
        <StatusBadge status={permission.risk_tier} />
      </div>
      <div aria-label="Run execution permission"
        className="permission-option-grid permission-option-grid-four" role="group">
        {executionPermissions.map(({ id, label, detail: optionDetail, icon: Icon }) => {
          const available = permissionRuntimeAvailable(permission, id);
          return <button aria-pressed={permission.mode === id}
            className={id === "full_access" || id === "debug" ? "danger" : ""}
            disabled={!mutable || mutation.isPending || permission.mode === id || !available}
            key={id} onClick={() => choose(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{label}</strong><small>{available ? optionDetail : "启动闸门未开启"}</small></span>
            {permission.mode === id && <Check aria-hidden="true" size={15} />}
          </button>;
        })}
      </div>
      {pendingMode && <PermissionConfirmation
        description={pendingMode === "approval"
          ? "每一条宿主机命令都需要用户批准。"
          : pendingMode === "full_access"
            ? "将允许无沙箱宿主机文件与网络访问。"
            : "将允许持久终端、后台进程和限时 Agent 输入。"}
        label={executionPermissions.find(({ id }) => id === pendingMode)?.label ?? pendingMode}
        loading={mutation.isPending} onCancel={() => setPendingMode(null)}
        onConfirm={() => mutation.mutate(pendingMode)} />}
      <dl className="permission-facts">
        <div><dt>Commands</dt><dd>{permission.command_scope}</dd></div>
        <div><dt>Filesystem</dt><dd>{permission.filesystem_scope}</dd></div>
        <div><dt>Network</dt><dd>{permission.network_scope}</dd></div>
      </dl>
      {mutation.isError && <MutationError error={mutation.error}
        fallback="权限档位切换失败" />}
    </section>
  );
}

const interactionModes: Array<{
  id: RunExecutionInteractionView["mode"];
  label: string;
  detail: string;
  icon: typeof Eye;
}> = [
  { id: "preview", label: "预览", detail: "零进程权限", icon: Eye },
  { id: "controlled", label: "Code", detail: "受控无状态命令", icon: Code2 },
  { id: "debug", label: "Debug", detail: "用户终端优先", icon: Bug },
  { id: "cyber", label: "Cyber", detail: "容器持久终端", icon: Container },
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
          <h2><Terminal aria-hidden="true" size={16} />交互信任模型</h2>
          <span>{detail.mode.surface} · {detail.execution_profile.profile}</span>
        </div>
        <StatusBadge status={interaction.workspace_trust} />
      </div>
      <div aria-label="Run execution interaction"
        className="permission-option-grid permission-option-grid-four" role="group">
        {interactionModes.map(({ id, label, detail: optionDetail, icon: Icon }) => {
          const compatible = interactionCompatible(detail, id);
          return <button aria-pressed={interaction.mode === id}
            disabled={!mutable || mutation.isPending || interaction.mode === id || !compatible}
            key={id} onClick={() => choose(id)} type="button">
            <Icon aria-hidden="true" size={17} />
            <span><strong>{label}</strong><small>{compatible ? optionDetail : "环境或工作面不匹配"}</small></span>
            {interaction.mode === id && <Check aria-hidden="true" size={15} />}
          </button>;
        })}
      </div>
      {pendingMode && <PermissionConfirmation
        description={pendingMode === "controlled"
          ? "信任当前工作区并使用受控的一次性命令。"
          : pendingMode === "debug"
            ? "信任当前工作区并开放限时 Debug 交互边界。"
            : "信任当前 Cyber 容器并开放持久容器终端边界。"}
        label={interactionModes.find(({ id }) => id === pendingMode)?.label ?? pendingMode}
        loading={mutation.isPending} onCancel={() => setPendingMode(null)}
        onConfirm={() => mutation.mutate(pendingMode)} />}
      <dl className="permission-facts">
        <div><dt>Command</dt><dd>{interaction.command_form}</dd></div>
        <div><dt>Terminal</dt><dd>{interaction.persistent_terminal ? "persistent" : "stateless"}</dd></div>
        <div><dt>Gate</dt><dd>{interaction.required_gate}</dd></div>
      </dl>
      {mutation.isError && <MutationError error={mutation.error}
        fallback="交互信任模型切换失败" />}
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
  return <div className="permission-confirmation" role="alert">
    <ShieldAlert aria-hidden="true" size={17} />
    <span><strong>{label}</strong><small>{description}</small></span>
    <button className="secondary-button" onClick={onCancel} type="button">取消</button>
    <button className="danger-button" disabled={loading} onClick={onConfirm} type="button">
      {loading
        ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
        : <Check aria-hidden="true" size={15} />}
      确认
    </button>
  </div>;
}

function MutationError({ error, fallback }: { error: unknown; fallback: string }) {
  return <div className="inline-warning" role="alert">
    {error instanceof Error ? error.message : fallback}
  </div>;
}
