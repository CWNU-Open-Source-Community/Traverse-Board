import { CalendarClock, Microscope, Settings } from "lucide-react";
import type { APIClient } from "../../api/client";
import type { V2SettingsSection } from "./sidebar";
import { InspectorRecordBrowser } from "./inspector-record-browser";
import "./inspector.css";

export function V2InspectorHome({ client, onOpenTool, onOpenSettings }: {
  client: APIClient;
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
      <CalendarClock aria-hidden="true" size={16} />定时观察</button>
      <button onClick={() => onOpenSettings("about")} type="button">
        <Settings aria-hidden="true" size={16} />连接与诊断</button></div>
    </details>
  </main>;
}
