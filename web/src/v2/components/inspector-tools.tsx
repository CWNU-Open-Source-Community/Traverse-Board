import { ArrowLeft, CalendarClock, Microscope, Settings } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import { RunWorkspace } from "../../components/run-workspace";
import { SessionWorkspace } from "../../components/session-workspace";
import { ScheduledTasksWorkspace } from "../../components/scheduled-tasks-workspace";
import { WorkbenchFrame } from "../../components/workbench-frame";
import { desktopBridgeAvailable } from "../../lib/desktop-bridge";
import { threadActivityLabel } from "../../lib/thread-activity-label";
import { useV2ThreadExecution } from "./thread-execution-control";
import type { V2SettingsSection } from "./sidebar";
import { InspectorRecordBrowser } from "./inspector-record-browser";
import "./inspector.css";

export function V2InspectorHome({ client, onOpenTool, onOpenSettings }: {
  client: CyberAgentClient;
  onOpenTool: (tool: "run" | "session" | "schedule", id?: string) => void;
  onOpenSettings: (section: V2SettingsSection) => void;
}) {
  return <main className="v2-inspector-home">
    <Microscope aria-hidden="true" size={28} /><h1>Inspector</h1>
    <p>从侧栏选择一个对话，查看它的执行记录、工具结果与诊断详情。</p>
    <p>顶部可随时切回对话，未发送的输入会保留。</p>
    <InspectorRecordBrowser client={client} onOpen={onOpenTool} />
    <details className="v2-inspector-home-utilities"><summary>其他检查工具</summary>
      <div><button onClick={() => onOpenTool("schedule")} type="button">
      <CalendarClock aria-hidden="true" size={16} />定时任务</button>
      <button onClick={() => onOpenSettings("about")} type="button">
        <Settings aria-hidden="true" size={16} />连接与诊断</button></div>
    </details>
  </main>;
}

// Advanced resource pages retain their exact scope and existing controls. They
// use the same application navigation/settings, and never infer a stale Thread.
export function V2InspectorTools({ client, tool, resourceID = "", threadID, onBack, onOpenSettings }: {
  client: CyberAgentClient;
  tool: "run" | "session" | "schedule";
  resourceID?: string;
  threadID: string;
  onBack: () => void;
  onOpenSettings: (section: V2SettingsSection) => void;
}) {
  const title = tool === "schedule" ? "定时任务" : tool === "run" ? "运行诊断" : "会话上下文";
  return <section className="v2-inspector-resource">
    <header><button onClick={onBack} type="button"><ArrowLeft aria-hidden="true" size={16} />返回 Inspector</button>
      <strong>{title}</strong></header>
    <div className="v2-resource-scope">
      {tool !== "schedule" && <p>此页的高级操作仅针对当前记录；{threadID ? "任务权限设置仍针对来源对话。" : "未绑定对话，任务权限设置不可用。"}</p>}
      {tool !== "schedule" && threadID && <SourceThreadActivity client={client} threadID={threadID} />}
      <details className="v2-inspector-resource-details"><summary>来源与设置</summary>
        {resourceID && <p>{tool === "schedule" ? "来源执行：" : "当前记录："}<code>{resourceID}</code></p>}
        {tool !== "schedule" && threadID && <p>上方显示来源对话的当前活动；下方是所选记录，可能来自较早的执行。</p>}
        <button onClick={() => onOpenSettings("general")} type="button">设置</button>
      </details>
    </div>
    <div className="v2-inspector-tool-body">
      {tool === "schedule" ? <ScheduledTasksWorkspace client={client} initialRunID={resourceID} />
        : !resourceID ? <p role="alert">地址缺少记录标识，请返回 Inspector 重新选择。</p>
          : <WorkbenchFrame client={client} desktop={desktopBridgeAvailable()} title={title}
            resourceKind={tool} runID={tool === "run" ? resourceID : ""}
            sessionID={tool === "session" ? resourceID : ""}>
            {tool === "run" ? <RunWorkspace client={client} key={`run:${resourceID}`} runID={resourceID}
              onOpenPlugins={() => onOpenSettings("extensions")} />
              : <SessionWorkspace client={client} key={`session:${resourceID}`} sessionID={resourceID}
                onOpenPlugins={() => onOpenSettings("extensions")} />}
          </WorkbenchFrame>}
    </div>
  </section>;
}

function SourceThreadActivity({ client, threadID }: { client: CyberAgentClient; threadID: string }) {
  const execution = useV2ThreadExecution(client, threadID);
  const label = threadActivityLabel({ threadID, execution: execution.data,
    readable: client.hasThreadExecutionRead === true, error: execution.isError });
  return <p role="status" aria-label="来源任务的 Agent 活动">来源任务的 Agent：{label}。
    {execution.isError && <button type="button" onClick={() => void execution.refetch()}>刷新执行状态</button>}
  </p>;
}
