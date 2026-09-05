import { render, screen } from "@testing-library/react";
import type { ThreadRunRecoveryView } from "../../api/types";
import { V2ThreadRunRecovery } from "./thread-run-recovery";

const recovery: ThreadRunRecoveryView = {
  version: "thread_run_recovery.v1",
  run_id: "run-old",
  handoff_operation_id: "run-handoff-failed",
  error_code: "failed_precondition",
  stop_reason: "failed_precondition",
  detail: "旧 Run 的固定模型已不满足执行条件。",
  quiescent: true,
  failed_at: "2026-09-01T00:43:30Z",
};

describe("V2ThreadRunRecovery", () => {
  it("explains that the next explicit message continues automatically", () => {
    render(<V2ThreadRunRecovery recovery={recovery} />);

    expect(screen.getByText("上一次执行已停止，对话仍可继续")).toBeInTheDocument();
    expect(screen.getByText(/直接发送下一条消息即可/)).toBeInTheDocument();
    expect(screen.getByText(/旧消息不会被自动重发/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("keeps the composer guidance non-destructive while resources are releasing", () => {
    render(<V2ThreadRunRecovery recovery={{ ...recovery, quiescent: false }} />);
    expect(screen.getByText(/你可以先编辑消息/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
