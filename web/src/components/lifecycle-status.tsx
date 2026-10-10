import { useLocale } from "../lib/locale";
import { StatusBadge, StatusLabel } from "./common";

type LifecycleKind = "run" | "session";
type Props = { status: string; kind?: LifecycleKind };

function openLifecycle(status: string, kind: LifecycleKind) {
  return kind === "run" ? status === "running" : status === "active";
}

// Run/Session lifecycle describes a record, not conversation continuation or live work.
// Keep generic Job, tool and model status labels separate from this projection.
export function LifecycleStatusLabel({ status, kind = "run" }: Props) {
  const { t } = useLocale();
  const open = openLifecycle(status, kind);
  return <span title={kind === "session"
    ? t("上下文记录状态。继续交流请返回对话，查看当前工作进度请打开执行活动。", "Context record state. Return to the conversation to continue chatting; open execution activity for current progress.")
    : t("执行记录状态。继续交流请返回对话，查看当前工作进度请打开执行活动。", "Execution record state. Return to the conversation to continue chatting; open execution activity for current progress.")}>
    {open ? kind === "session" ? t("未关闭", "Not closed") : t("未结束", "Not ended") : <StatusLabel status={status} />}
  </span>;
}

export function LifecycleStatusBadge({ status, kind = "run" }: Props) {
  const { t } = useLocale();
  const open = openLifecycle(status, kind);
  const label = open ? kind === "session" ? t("未关闭", "Not closed") : t("未结束", "Not ended") : undefined;
  return <span title={kind === "session"
    ? t("上下文记录状态。继续交流请返回对话，查看当前工作进度请打开执行活动。", "Context record state. Return to the conversation to continue chatting; open execution activity for current progress.")
    : t("执行记录状态。继续交流请返回对话，查看当前工作进度请打开执行活动。", "Execution record state. Return to the conversation to continue chatting; open execution activity for current progress.")}>
    <StatusBadge status={open ? "open" : status} label={label} />
  </span>;
}
