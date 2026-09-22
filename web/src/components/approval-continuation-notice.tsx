import type { ApprovalContinuationView } from "../api/types";
import { useLocale } from "../lib/locale";

// A completed continuation is a recorded model step, not proof of a file
// write, a successful command, or completion of the user's whole task.
export function ApprovalContinuationNotice({ continuation }: {
  continuation?: ApprovalContinuationView;
}) {
  const { t } = useLocale();
  if (!continuation) return null;
  const text = {
    not_started: t("审批已保存，本次未启动自动继续；可以在当前对话发送新消息。",
      "Review saved. This request did not start automatic continuation; you can send a message in this conversation."),
    queued: t("审批已保存，继续请求已排队；最新进度见对话执行记录。",
      "Review saved and continuation queued. See the conversation records for current progress."),
    waiting_approval: t("Agent 已继续处理，另一次操作需要批准。",
      "The Agent continued; another operation needs approval."),
    completed: t("审批已保存，后续工作轮已有结果；具体修改和命令结果见执行记录。",
      "Review saved. The subsequent turn has a recorded outcome; see execution records for file changes and command results."),
    failed: t("审批已保存，后续执行失败；请查看记录后在当前对话继续。",
      "Review saved, but subsequent execution failed. Check the records and continue in this conversation."),
  }[continuation.state];
  return <div className={continuation.state === "failed" ? "inline-warning" : undefined}
    role={continuation.state === "failed" ? "alert" : "status"}>
    <p>{text}{continuation.error_code && <> <code>{continuation.error_code}</code></>}</p>
  </div>;
}
