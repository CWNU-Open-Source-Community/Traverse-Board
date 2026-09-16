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
    ? t("仅描述此上下文记录的状态，不决定对话是否可继续，也不表示 Agent 当前正在执行。", "Describes only this context record; it does not determine whether the conversation can continue or whether the Agent is currently executing.")
    : t("仅描述此执行记录的状态，不决定对话是否可继续，也不表示 Agent 当前正在执行。", "Describes only this execution record; it does not determine whether the conversation can continue or whether the Agent is currently executing.")}>
    {open ? kind === "session" ? t("未关闭", "Not closed") : t("未结束", "Not ended") : <StatusLabel status={status} />}
  </span>;
}

export function LifecycleStatusBadge({ status, kind = "run" }: Props) {
  const { t } = useLocale();
  const open = openLifecycle(status, kind);
  const label = open ? kind === "session" ? t("未关闭", "Not closed") : t("未结束", "Not ended") : undefined;
  return <span title={kind === "session"
    ? t("仅描述此上下文记录的状态，不决定对话是否可继续，也不表示 Agent 当前正在执行。", "Describes only this context record; it does not determine whether the conversation can continue or whether the Agent is currently executing.")
    : t("仅描述此执行记录的状态，不决定对话是否可继续，也不表示 Agent 当前正在执行。", "Describes only this execution record; it does not determine whether the conversation can continue or whether the Agent is currently executing.")}>
    <StatusBadge status={open ? "open" : status} label={label} />
  </span>;
}
