import { CircleAlert } from "lucide-react";
import type { ThreadRunRecoveryView } from "../../api/types";

export function V2ThreadRunRecovery({ recovery }: {
  recovery: ThreadRunRecoveryView;
}) {
  return <section aria-label="对话继续提示" aria-live="polite" className="v2-run-recovery">
    <span className="v2-run-recovery-icon"><CircleAlert aria-hidden="true" size={18} /></span>
    <div>
      <strong>上一次执行已停止，对话仍可继续</strong>
      <p>{recovery.detail}</p>
      <small>直接发送下一条消息即可。系统会在内部收束上一次执行、取消其中尚未执行的消息，并在新的执行上下文中应用待生效设置；旧消息不会被自动重发。</small>
      {!recovery.quiescent && <small className="v2-run-recovery-waiting">
        上一次执行仍在释放资源；你可以先编辑消息，发送时系统会给出可重试提示。
      </small>}
    </div>
  </section>;
}
