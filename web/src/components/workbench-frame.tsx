import { useRef, useState, type FormEvent, type KeyboardEvent, type PointerEvent,
  type ReactNode } from "react";
import {
  ArrowUp,
  CalendarClock,
  GitPullRequest,
  MessagesSquare,
  PackageSearch,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { RunCreationControlRequestView } from "../api/types";
import { useLocale } from "../lib/locale";
import { submitComposerOnEnter } from "../lib/composer-keyboard";
import { AgentComposerControls } from "./agent-composer-controls";
import { WorkbenchDock, type WorkbenchResourceKind } from "./workbench-dock";

export const minimumSidebarWidth = 232;
export const maximumSidebarWidth = 420;
export const defaultSidebarWidth = 286;

export function clampSidebarWidth(width: number): number {
  return Math.min(maximumSidebarWidth, Math.max(minimumSidebarWidth, Math.round(width)));
}

export function SidebarResizeHandle({ value, onChange }: {
  value: number;
  onChange: (value: number) => void;
}) {
  const { t } = useLocale();
  const drag = useRef<{ pointerID: number; originX: number; originWidth: number } | null>(null);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      onChange(clampSidebarWidth(value + (event.key === "ArrowLeft" ? -12 : 12)));
    } else if (event.key === "Home") {
      event.preventDefault();
      onChange(minimumSidebarWidth);
    } else if (event.key === "End") {
      event.preventDefault();
      onChange(maximumSidebarWidth);
    }
  };
  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    drag.current = { pointerID: event.pointerId, originX: event.clientX, originWidth: value };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (!drag.current || drag.current.pointerID !== event.pointerId) return;
    onChange(clampSidebarWidth(drag.current.originWidth + event.clientX - drag.current.originX));
  };
  const finishDrag = (event: PointerEvent<HTMLDivElement>) => {
    if (!drag.current || drag.current.pointerID !== event.pointerId) return;
    drag.current = null;
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };
  return <div aria-label={t("调整侧栏宽度", "Resize sidebar")} aria-orientation="vertical"
    aria-valuemax={maximumSidebarWidth} aria-valuemin={minimumSidebarWidth}
    aria-valuenow={value} className="sidebar-resize-handle"
    onDoubleClick={() => onChange(defaultSidebarWidth)} onKeyDown={onKeyDown}
    onPointerCancel={finishDrag} onPointerDown={onPointerDown}
    onPointerMove={onPointerMove} onPointerUp={finishDrag} role="separator" tabIndex={0} />;
}

export function WorkbenchFrame({ title, children, client, desktop, resourceKind, runID,
  sessionID }: {
  title: string;
  children: ReactNode;
  client: CyberAgentClient;
  desktop: boolean;
  resourceKind: WorkbenchResourceKind;
  runID: string;
  sessionID: string;
}) {
  return <WorkbenchDock client={client} desktop={desktop} resourceKind={resourceKind}
    runID={runID} sessionID={sessionID} title={title}>
    {children}
  </WorkbenchDock>;
}

export interface NewRunDraft {
  goal: string;
  phase: NonNullable<RunCreationControlRequestView["phase"]>;
}

export function EmptyConversation({ client, onCreateRun, creationEnabled, onOpenPlugins }: {
  client: CyberAgentClient;
  onCreateRun: (draft: NewRunDraft) => void;
  creationEnabled: boolean;
  onOpenPlugins?: () => void;
}) {
  const { t } = useLocale();
  const [goal, setGoal] = useState("");
  const [planMode, setPlanMode] = useState(false);
  const [targetMode, setTargetMode] = useState(false);
  const normalized = goal.trim();
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!creationEnabled || !normalized) return;
    onCreateRun({ goal: normalized, phase: planMode ? "plan" : "deliver" });
  };
  return (
    <div className="prayu-empty-conversation">
      <div className="prayu-empty-heading">
        <MessagesSquare aria-hidden="true" size={24} />
        <h1>{t("开始一项任务", "Start a task")}</h1>
      </div>
      <form className="prayu-starter-composer" onSubmit={submit}>
        <textarea aria-label={t("描述任务", "Describe task")} disabled={!creationEnabled}
          onChange={(event) => setGoal(event.target.value)} onKeyDown={submitComposerOnEnter}
          placeholder={t("描述你想完成的工作", "Describe the work you want to complete")}
          rows={2} value={goal} />
        <AgentComposerControls client={client} onOpenPlugins={onOpenPlugins}
          onPlanModeChange={setPlanMode} onTargetModeChange={setTargetMode}
          planMode={planMode} route="code" targetMode={targetMode}
          trailing={<button aria-label={t("创建任务", "Create task")} className="composer-send-button"
            disabled={!creationEnabled || !normalized} title={t("创建任务", "Create task")} type="submit">
            <ArrowUp aria-hidden="true" size={16} />
          </button>} />
      </form>
    </div>
  );
}

type UtilityKind = "pull-requests" | "schedule" | "plugins";

const utilityViews: Record<UtilityKind, {
  title: [string, string];
  empty: [string, string];
  icon: typeof GitPullRequest;
}> = {
  "pull-requests": { title: ["拉取请求", "Pull requests"], empty: ["暂无拉取请求", "No pull requests"], icon: GitPullRequest },
  schedule: { title: ["自动定时", "Scheduled tasks"], empty: ["暂无自动任务", "No scheduled tasks"], icon: CalendarClock },
  plugins: { title: ["插件", "Plugins"], empty: ["暂无已打开的插件", "No open plugins"], icon: PackageSearch },
};

export function UtilityWorkspace({ kind, onOpenPlugins }: {
  kind: UtilityKind;
  onOpenPlugins?: () => void;
}) {
  const { t } = useLocale();
  const view = utilityViews[kind];
  const Icon = view.icon;
  return (
    <section className="utility-workspace">
      <header><Icon aria-hidden="true" size={18} /><h1>{t(...view.title)}</h1></header>
      <div className="utility-empty-state">
        <Icon aria-hidden="true" size={25} />
        <strong>{t(...view.empty)}</strong>
        {kind === "plugins" && onOpenPlugins &&
          <button className="command-button" onClick={onOpenPlugins} type="button">{t("打开插件管理", "Open plugin manager")}</button>}
      </div>
    </section>
  );
}
