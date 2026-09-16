import { CircleAlert } from "lucide-react";
import type { ThreadRunRecoveryView } from "../../api/types";

export function V2ThreadRunRecovery({ recovery, approvalSaved = false }: {
  recovery: ThreadRunRecoveryView;
  approvalSaved?: boolean;
}) {
  return <section aria-label="对话继续提示" aria-live="polite" className="v2-run-recovery">
    <span className="v2-run-recovery-icon"><CircleAlert aria-hidden="true" size={18} /></span>
    <div>
      <strong>{approvalSaved ? "审批已保存，后续执行已停止" : "本轮执行已停止，对话仍可继续"}</strong>
      <p>{recovery.detail}</p>
      <small>可以直接发送“继续”或补充要求。若上次提交的结果尚未确认，系统会先核对，再发送新要求。</small>
      {!recovery.quiescent && <small className="v2-run-recovery-waiting">
        上一次执行仍在释放资源；你可以先编辑消息，发送时系统会给出可重试提示。
      </small>}
    </div>
  </section>;
}
