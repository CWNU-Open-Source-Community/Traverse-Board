import { useId, useRef, useState, type MouseEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Bug, LoaderCircle, RotateCw, ShieldCheck } from "lucide-react";
import {
  desktopCurrentRiskProfile,
  desktopDebugRestartEnabled,
  desktopErrorMessage,
  restartDesktopInDebugMode,
  type DesktopRuntimeRiskProfile,
} from "../../lib/desktop-bridge";
import {
  V2HighRiskActivationDialog,
} from "./high-risk-activation-dialog";

const sessionCopy: Record<DesktopRuntimeRiskProfile, {
  label: string;
  detail: string;
}> = {
  safe: {
    label: "标准运行时",
    detail: "完全访问可在当前任务中即时确认；持久终端和后台调试运行时未开启。",
  },
  debug: {
    label: "调试已开启",
    detail: "本次应用会话已初始化持久终端、后台进程和限时终端输入运行时。",
  },
};

function actionState(current: DesktopRuntimeRiskProfile | null,
  restartEnabled: boolean): { disabled: boolean; label: string } {
  if (current === null) return { disabled: true, label: "桌面会话不可用" };
  if (current === "debug") return { disabled: true, label: "调试运行时已开启" };
  if (!restartEnabled) return { disabled: true, label: "重启入口不可用" };
  return { disabled: false, label: "启用调试模式并重启" };
}

export function V2RuntimeCapabilityControl() {
  const titleID = useId();
  const [open, setOpen] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const returnFocusRef = useRef<HTMLButtonElement>(null);
  const current = desktopCurrentRiskProfile();
  const restartEnabled = desktopDebugRestartEnabled();
  const mutation = useMutation({
    mutationFn: () => restartDesktopInDebugMode(),
    onSuccess: (result) => {
      if (result.status === "cancelled") {
        setOpen(false);
        return;
      }
      setRestarting(true);
    },
  });
  const beginActivation = (event: MouseEvent<HTMLButtonElement>) => {
    returnFocusRef.current = event.currentTarget;
    mutation.reset();
    setRestarting(false);
    setOpen(true);
  };
  const close = () => {
    if (mutation.isPending || restarting) return;
    mutation.reset();
    setOpen(false);
  };
  const phase = restarting ? "restarting" : mutation.isPending ? "confirming" : "idle";
  const status = current ? sessionCopy[current] : {
    label: "无法读取",
    detail: "当前页面没有可验证的桌面运行时能力信息。",
  };

  return <section aria-labelledby={titleID}
    className="v2-settings-card v2-runtime-capability-card">
    <header>
      <div><span className="v2-runtime-icon"><ShieldCheck aria-hidden="true" size={18} /></span>
        <div><h2 id={titleID}>调试运行时</h2><p>持久终端与后台进程</p></div></div>
      <span className={`v2-runtime-status is-${current ?? "unavailable"}`}>{status.label}</span>
    </header>
    <div className="v2-runtime-capability-body">
      <p className="v2-runtime-session-detail">{status.detail}</p>
      <div aria-label="调试运行时能力" className="v2-runtime-actions" role="group">
        {(() => {
          const action = actionState(current, restartEnabled);
          return <article>
            <span><Bug aria-hidden="true" size={18} /></span>
            <div><strong>调试模式</strong><p>
              继承完全访问（完整 CDP 子开关默认开启），并增加持久终端、后台进程和限时终端输入。
            </p></div>
            <button aria-label={action.label} disabled={action.disabled || mutation.isPending || restarting}
              onClick={beginActivation} type="button">
              {mutation.isPending
                ? <LoaderCircle aria-hidden="true" className="spin" size={14} />
                : !action.disabled ? <RotateCw aria-hidden="true" size={14} /> : null}
              {action.label}
            </button>
          </article>;
        })()}
      </div>
      <p className="v2-runtime-boundary">
        完全访问无需重启，但当前执行需暂停并处于静止边界后才能生效。只有调试模式因需要初始化
        长生命周期终端和后台运行时而重启；重启不会改写任务权限，普通启动会回到标准运行时。
      </p>
    </div>
    <V2HighRiskActivationDialog error={mutation.isError
      ? desktopErrorMessage(mutation.error) : undefined}
      onCancel={close} onConfirm={() => mutation.mutate()}
      open={open} phase={phase} profile="debug"
      returnFocusRef={returnFocusRef} />
  </section>;
}
