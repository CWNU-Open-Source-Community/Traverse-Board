import { useRef, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import type { CyberAgentClient } from "../api/client";
import { useLocale } from "../lib/locale";
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
  sessionID, threadID = "" }: {
  title: string;
  children: ReactNode;
  client: CyberAgentClient;
  desktop: boolean;
  resourceKind: WorkbenchResourceKind;
  runID: string;
  sessionID: string;
  threadID?: string;
}) {
  return <WorkbenchDock client={client} desktop={desktop} resourceKind={resourceKind}
    runID={runID} sessionID={sessionID} threadID={threadID} title={title}>
    {children}
  </WorkbenchDock>;
}
