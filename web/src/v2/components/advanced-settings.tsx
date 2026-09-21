import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { CyberAgentClient } from "../../api/client";
import type { ThreadDetailView } from "../../api/types";
import { DesktopSkillPreviewDialog } from "../../components/desktop-skill-preview";
import { SafeWebReadinessPanel } from "../../components/safe-web-readiness";
import { AboutSettings, ExtensionSettings, WebSkillInstall, persistDensity, readDensity,
  type Density } from "../../components/shared-settings-panels";
import { applyRunNavigationMode, readRunNavigationMode, type RunNavigationMode } from "../../lib/run-navigation";
import { v2QueryKeys } from "../query-keys";
import { useConnectionStore } from "../../state/connection";
import "./advanced-settings.css";

export function V2ExtensionSettings({ client, threadID }: { client: CyberAgentClient; threadID: string }) {
  const thread = useQuery({
    queryKey: v2QueryKeys.thread(threadID),
    queryFn: ({ signal }) => client.get<ThreadDetailView>(`/threads/${encodeURIComponent(threadID)}`, {}, signal),
    enabled: Boolean(threadID),
  });
  const runID = (thread.data?.active_run ?? thread.data?.last_run)?.id;
  if (threadID && (thread.isPending || thread.isError || !runID)) return <>
    <h1>扩展与代码智能</h1>
    {thread.isPending ? <p role="status">正在读取当前任务的扩展范围…</p> : <p role="alert">
      无法确定当前任务的扩展范围。<button className="v2-setting-link" onClick={() => void thread.refetch()}
        type="button">重试</button></p>}
  </>;
  return <div className="v2-shared-settings">
    <p className="v2-settings-lead">{threadID ? `当前任务：${thread.data?.thread.title}。代码智能状态按其工作区读取。`
      : "未选择任务，显示已登记的扩展与代码智能状态。"}扩展的实际范围见各条记录；关闭操作作用于该扩展或安装，不只是隐藏当前任务中的显示。</p>
    <ExtensionSettings client={client} key={runID ?? "global"} selectedRunID={runID ?? ""} />
  </div>;
}

export function V2SkillSettings({ client, desktop }: { client: CyberAgentClient; desktop: boolean }) {
  const [previewOpen, setPreviewOpen] = useState(false);
  return <><h1>Skill 包</h1><p className="v2-settings-lead">
    将已有技能包登记到本地技能库。安装不授予执行权限，也不代表当前任务已经加载或执行它。
  </p><section className="v2-settings-section v2-shared-settings"><div className="v2-settings-card v2-skill-settings-card">
    {desktop ? <>
      <p>先预览 ZIP 包的结构、来源与文件，再确认安装到 Code 或 Cyber 工作面。</p>
      <button className="settings-action" onClick={() => setPreviewOpen(true)} type="button">预览 Skill 包</button>
      {!client.hasSkillInstallation && <p>当前连接只允许预览，未开放安装。</p>}
      <DesktopSkillPreviewDialog installationEnabled={client.hasSkillInstallation}
        onClose={() => setPreviewOpen(false)} open={previewOpen} />
    </> : client.hasSkillInstallation ? <>
      <p>网页端可上传 ZIP 包并登记到 Code。结构校验由服务端执行；本机文件选择预览仅在桌面端提供。</p>
      <WebSkillInstall client={client} />
    </> : <p role="status">当前连接未开放 Skill 安装。已有技能不会因此被删除或修改。</p>}
  </div></section></>;
}

export function V2InspectorPreferences({ onOpenInspector }: {
  onOpenInspector: (returnFocus?: HTMLElement | null) => void;
}) {
  const [density, setDensity] = useState(readDensity);
  const [navigation, setNavigation] = useState(readRunNavigationMode);
  const changeDensity = (value: Density) => {
    setDensity(value);
    document.documentElement.dataset.prayuDensity = value;
    persistDensity(value);
  };
  const changeNavigation = (value: RunNavigationMode) => {
    setNavigation(value);
    applyRunNavigationMode(value);
  };
  return <><h1>Inspector 偏好与诊断</h1><p className="v2-settings-lead">
    这些显示偏好保存在本机，仅调整 Inspector 的列表间距与高级导航，不改变任务权限或执行状态。
  </p><section className="v2-settings-section v2-shared-settings"><div className="v2-settings-card v2-inspector-preferences">
    <div><strong>Inspector 内容间距</strong><div className="v2-setting-segmented" role="group" aria-label="Inspector 内容间距">
      {(["comfortable", "compact"] as const).map((value) => <button aria-pressed={density === value}
        key={value} onClick={() => changeDensity(value)} type="button">{value === "compact" ? "紧凑" : "舒展"}</button>)}
    </div></div>
    <div><strong>运行记录导航</strong><div className="v2-setting-segmented" role="group" aria-label="运行记录导航">
      {(["compact", "diagnostic"] as const).map((value) => <button aria-pressed={navigation === value}
        key={value} onClick={() => changeNavigation(value)} type="button">{value === "compact" ? "精简" : "完整诊断"}</button>)}
    </div></div>
    <button className="settings-action" onClick={(event) => onOpenInspector(event.currentTarget)} type="button">打开 Inspector</button>
  </div></section></>;
}

export function V2AboutSettings({ client, desktop }: { client: CyberAgentClient; desktop: boolean }) {
  const queryClient = useQueryClient();
  const disconnect = useConnectionStore((state) => state.disconnect);
  const health = useQuery({ queryKey: ["health"], queryFn: ({ signal }) => client.health(signal), retry: false });
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  return <div className="v2-shared-settings">
    {health.isPending ? <><h1>关于</h1><p role="status">正在读取应用版本…</p></> : health.isError ? <>
      <h1>关于</h1><p role="alert">应用信息读取失败，版本未知。
        <button className="v2-setting-link" onClick={() => void health.refetch()} type="button">重试</button></p>
    </> : <AboutSettings desktop={desktop} health={health.data} />}
    <details className="v2-settings-diagnostics" onToggle={(event) => setDiagnosticsOpen(event.currentTarget.open)}>
      <summary>连接与网页诊断</summary>
      <p>这些结果来自当前连接的服务与运行环境。</p>
      {health.data && <p>服务状态：{health.data.status}</p>}
      {diagnosticsOpen && <SafeWebReadinessPanel client={client} />}
    </details>
    {!desktop && <section className="v2-settings-section"><h2>当前界面连接</h2>
      <p>断开后可重新填写连接信息。此操作只断开当前界面连接，不会停止任务或撤销已受理的操作。</p>
      <button className="settings-action" onClick={() => { queryClient.clear(); disconnect(); }} type="button">断开连接</button>
    </section>}
  </div>;
}
