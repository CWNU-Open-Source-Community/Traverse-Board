import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, RefreshCw, RotateCw } from "lucide-react";
import { APIRequestError, type APIClient } from "../api/client";
import type { SandboxEnvironmentControlRequestView, SandboxEnvironmentSettingsView } from "../api/types";
import { sandboxEnvironmentQueryKey, sandboxSettingsMatch, validSandboxSettings } from "../api/sandbox-environment";
import { desktopErrorMessage, desktopSandboxRestartEnabled, restartDesktopWithSandboxSettings } from "../lib/desktop-bridge";
import { SandboxBackendSelector, sandboxBackendLabels } from "./sandbox-backend-selector";
import "./sandbox-environment-panel.css";

const localDefaults: SandboxEnvironmentSettingsView = {
  default_backend: "local", docker_enabled: false, docker_image_digest: "", sbx_enabled: false, sbx_template: "",
};
const saveIntentKey = ["sandbox", "environment-save-intent"] as const;
interface SaveIntent {
  body: SandboxEnvironmentControlRequestView;
  state: "pending" | "unknown" | "confirmed" | "conflict" | "rejected";
  error?: string;
  status?: number;
}

export function SandboxEnvironmentPanel({ client }: { client: APIClient }) {
  const queries = useQueryClient();
  const edited = useRef(false);
  const [draft, setDraft] = useState<SandboxEnvironmentSettingsView>(localDefaults);
  const [restarting, setRestarting] = useState(false);
  const environment = useQuery({ queryKey: sandboxEnvironmentQueryKey,
    queryFn: ({ signal }) => client.getSandboxEnvironment(signal), retry: false });
  const intent = useQuery<SaveIntent | null>({ queryKey: saveIntentKey, queryFn: () => null,
    enabled: false, initialData: null, gcTime: Infinity });
  const pending = intent.data?.state === "pending";
  const unknown = intent.data?.state === "unknown";
  const conflict = intent.data?.state === "conflict";
  const value = environment.data;
  useEffect(() => {
    if (intent.data && intent.data.state !== "confirmed") setDraft(intent.data.body.settings);
    else if (!edited.current && value) setDraft(value.settings);
  }, [intent.data, value]);
  const save = useMutation({ mutationFn: (body: SandboxEnvironmentControlRequestView) => client.saveSandboxEnvironment(body),
    onMutate: () => queries.cancelQueries({ queryKey: sandboxEnvironmentQueryKey }),
    onSuccess: (result, body) => {
      queries.setQueryData(saveIntentKey, { body, state: "confirmed" });
      queries.setQueryData(sandboxEnvironmentQueryKey, result);
      setDraft(result.settings); edited.current = false;
      void queries.invalidateQueries({ queryKey: sandboxEnvironmentQueryKey });
    },
    onError: (error, body) => {
      const conflict = error instanceof APIRequestError && error.status === 409;
      const rejected = error instanceof APIRequestError && [400, 401, 403, 404, 412, 413, 422].includes(error.status);
      queries.setQueryData(saveIntentKey, { body, state: conflict ? "conflict" : rejected ? "rejected" : "unknown", error: error.message,
        ...(error instanceof APIRequestError ? { status: error.status } : {}) });
    },
  });
  const restart = useMutation({ mutationFn: () => restartDesktopWithSandboxSettings(),
    onSuccess: (result) => setRestarting(result.status === "restarting") });
  const locked = pending || unknown || conflict || restarting || restart.isPending;
  const change = (next: SandboxEnvironmentSettingsView) => {
    const current = queries.getQueryData<SaveIntent | null>(saveIntentKey);
    if (locked || current?.state === "pending" || current?.state === "unknown" || current?.state === "conflict") return;
    edited.current = true; setDraft(next);
    if (current?.state === "confirmed" || current?.state === "rejected") queries.setQueryData(saveIntentKey, null);
  };
  const submit = (body: SandboxEnvironmentControlRequestView) => {
    const current = queries.getQueryData<SaveIntent | null>(saveIntentKey);
    if (!client.hasControl || current?.state === "pending" || restarting ||
      (current?.state === "unknown" && JSON.stringify(current.body) !== JSON.stringify(body))) return;
    queries.setQueryData(saveIntentKey, { body, state: "pending" });
    save.mutate(body);
  };
  const reread = async () => {
    const result = await environment.refetch();
    if (!result.data || result.isError) return;
    queries.setQueryData(saveIntentKey, null);
    edited.current = false; setDraft(result.data.settings);
  };
  const dirty = Boolean(value && !sandboxSettingsMatch(draft, value.settings));
  const valid = validSandboxSettings(draft);
  const canRestart = desktopSandboxRestartEnabled();
  return <section aria-label="执行环境设置" className="sandbox-environment-panel">
    <div className="section-heading"><div><h2>执行环境</h2><p>Local 默认在本机工作区执行。安装 Docker 或官方 sbx 后，在这里启用并配置需要的环境。</p></div>
      <button className="compact-command" disabled={environment.isFetching || pending || restarting} onClick={() => void environment.refetch()} type="button">
        <RefreshCw aria-hidden="true" size={14} />检测环境</button></div>
    {environment.isPending && <p role="status">正在检测本机执行环境…</p>}
    {environment.isError && <p role="alert">执行环境检测失败。点击“检测环境”重新读取。{environment.error.message}</p>}
    {value && <>
      <div aria-label="当前进程环境" className="sandbox-environment-observations">
        {value.backends.map((backend) => <article key={backend.backend}>
          <strong>{sandboxBackendLabels[backend.backend]}</strong>
          <span>{value.probe_status === "not_checked" ? "检测待完成" : backend.ready ? "当前进程已就绪"
            : backend.status === "disabled" ? "当前进程待启用" : backend.status === "configuration_required" ? "需要配置固定镜像" : "当前环境待准备"}</span>
          <p>{value.probe_status === "checked" ? backend.installed ? "已检测到本机安装" : "尚未检测到可用安装" : "设置已保存，重新检测当前环境。"}</p>
          {backend.blockers.map((blocker) => <p key={blocker.code}>{blocker.message}</p>)}
        </article>)}
      </div>
      <fieldset disabled={locked || !client.hasControl} className="sandbox-environment-form">
        <legend>保存下次启动设置</legend>
        <p>默认环境用于尚未配置编码环境的任务。已有任务继续使用当前配置，具体操作沿用任务权限和来源确认。</p>
        <SandboxBackendSelector value={draft.default_backend} disabled={locked || !client.hasControl}
          onChange={(backend) => change({ ...draft, default_backend: backend })} />
        <div className="sandbox-environment-fields">
          <label className="sandbox-environment-toggle"><input checked={draft.docker_enabled} onChange={(event) => change({ ...draft, docker_enabled: event.currentTarget.checked })} type="checkbox" />启用 Docker Engine</label>
          <label>Docker 固定镜像摘要<input autoComplete="off" maxLength={71} onChange={(event) => change({ ...draft, docker_image_digest: event.currentTarget.value })}
            placeholder="sha256:…" spellCheck={false} value={draft.docker_image_digest} /></label>
          <p>使用已审阅的 Linux 工具链镜像，填写完整 SHA-256 摘要。检测会检查本机 Engine 与镜像状态。</p>
          <label className="sandbox-environment-toggle"><input checked={draft.sbx_enabled} onChange={(event) => change({ ...draft, sbx_enabled: event.currentTarget.checked })} type="checkbox" />启用 Docker Sandboxes (sbx)</label>
          <label>sbx 固定模板<input autoComplete="off" maxLength={512} onChange={(event) => change({ ...draft, sbx_template: event.currentTarget.value })}
            placeholder="registry/repository@sha256:…" spellCheck={false} value={draft.sbx_template} /></label>
          <p>填写专用 traverse-runtime 命名空间可读取的固定 OCI 模板与 SHA-256 摘要。当前 sbx 的 MCP 隔离仍需验证；先检测并核对阻塞项，通过后再启用和重启。准备期间可选 Local 或 Docker Engine。</p>
        </div>
        {!valid && <p role="alert">请启用选作默认的后端，并核对固定镜像摘要或 sbx 模板格式。</p>}
        <button className="command-button primary" disabled={!dirty || !valid || locked || !client.hasControl}
          onClick={() => submit({ version: "sandbox_environment.v1", expected_revision: value.revision, settings: { ...draft } })} type="button">
          {pending && <LoaderCircle aria-hidden="true" className="spin" size={14} />}保存执行环境设置</button>
      </fieldset>
      {!client.hasControl && <p>当前连接可检测环境。连接桌面控制入口后可保存设置。</p>}
      {intent.data?.state === "confirmed" && <p role="status">执行环境设置已保存。{value.restart_required ? "重启应用后读取新配置。" : "当前进程配置已与保存设置一致。"}</p>}
      {pending && <p role="status">正在保存设置。原请求已保留，关闭后可回到这里查看结果。</p>}
      {unknown && <div role="alert"><p>保存结果待确认，使用原请求核对结果。{intent.data?.error}</p>
        <button disabled={!client.hasControl || pending} onClick={() => submit(intent.data!.body)} type="button">核对保存结果</button></div>}
      {conflict && <div role="alert"><p>执行环境设置已被修改。重新读取当前配置，核对后再保存。{intent.data?.error}</p>
        <button disabled={environment.isFetching} onClick={() => void reread()} type="button">重新读取配置</button></div>}
      {intent.data?.state === "rejected" && <p role="alert">设置未保存，请核对配置和连接权限后重试。{intent.data.error}</p>}
      {intent.data?.state === "rejected" && intent.data.status === 412 && <p>当前设置存储需要重新核对。请重新检测环境；仍无法保存时，关闭并重新打开应用。</p>}
      {value.restart_required && <div className="sandbox-environment-restart" role="status"><p>保存设置与当前进程配置不同。重启应用后再次检测环境，再回到任务配置编码环境。</p>
        {canRestart ? <button disabled={locked || dirty || environment.isFetching} onClick={() => restart.mutate()} type="button">
          <RotateCw aria-hidden="true" size={14} />{restarting ? "正在重启应用…" : "重启并读取执行环境设置"}</button>
          : <p>关闭并重新打开桌面应用，使保存设置生效。</p>}
        {restart.isError && <p role="alert">重启失败。{desktopErrorMessage(restart.error)}</p>}
      </div>}
      <details><summary>查看配置与检测记录</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>
    </>}
  </section>;
}
