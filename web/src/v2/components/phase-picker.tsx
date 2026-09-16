import type { Ref } from "react";
import { ListChecks } from "lucide-react";
import "./thread-plan.css";

export type V2WorkPhase = "plan" | "deliver";

export function V2PhasePicker({ phase, onChange, disabled = false, planAvailable = true, buttonRef }: {
  phase: V2WorkPhase;
  onChange: (phase: V2WorkPhase) => void;
  disabled?: boolean;
  planAvailable?: boolean;
  buttonRef?: Ref<HTMLButtonElement>;
}) {
  const planning = phase === "plan";
  return <button type="button" ref={buttonRef} className={`v2-phase-picker v2-phase-toggle${planning ? " is-active" : ""}`}
    aria-pressed={planning} disabled={disabled || !planning && !planAvailable}
    title={planning ? "关闭计划模式，恢复默认处理方式；不会发送草稿或改变权限"
      : !planAvailable ? "当前连接未启用计划模式" : "开启后先分析并形成计划，确认后执行；切换不会发送草稿或改变权限"}
    onClick={() => onChange(planning ? "deliver" : "plan")}>
    <ListChecks aria-hidden="true" size={14} />计划模式
  </button>;
}
