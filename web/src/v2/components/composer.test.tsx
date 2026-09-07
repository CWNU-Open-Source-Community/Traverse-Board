import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { WorkspaceView } from "../../api/types";
import { V2Composer } from "./composer";

const client = {} as CyberAgentClient;
const workspaces: WorkspaceView[] = [
  { id: "workspace-1", name: "Traverse Board", created_at: "2026-08-29T00:00:00Z" },
];

function renderComposer(onSubmit = vi.fn(async () => undefined), workspaceID = "workspace-1") {
  const onWorkspaceChange = vi.fn();
  render(<V2Composer client={client} onSubmit={onSubmit}
    onWorkspaceChange={onWorkspaceChange} threadID="" workspaceID={workspaceID}
    workspaces={workspaces} />);
  return { onSubmit, onWorkspaceChange };
}

describe("V2Composer", () => {
  it("trims and submits a ready message with Enter, then clears and refocuses", async () => {
    const user = userEvent.setup();
    const controls = renderComposer();
    const textarea = screen.getByRole("textbox", { name: "开始新对话" });

    await user.type(textarea, "  检查归档与权限流程  ");
    await user.keyboard("{Enter}");

    await waitFor(() => expect(controls.onSubmit)
      .toHaveBeenCalledWith("检查归档与权限流程"));
    expect(textarea).toHaveValue("");
    expect(textarea).toHaveFocus();
  });

  it("keeps failed input visible, reports the error, and clears it on the next edit", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockRejectedValue(new Error("控制平面暂时不可用"));
    renderComposer(onSubmit);
    const textarea = screen.getByRole("textbox", { name: "开始新对话" });

    await user.type(textarea, "保留这条消息");
    await user.click(screen.getByRole("button", { name: "发送消息" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("控制平面暂时不可用");
    expect(textarea).toHaveValue("保留这条消息");
    await user.type(textarea, "。");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("requires a workspace before enabling submission", () => {
    const missing = renderComposer(vi.fn(async () => undefined), "");
    fireEvent.change(screen.getByRole("textbox", { name: "开始新对话" }),
      { target: { value: "有效文字" } });
    expect(screen.getByRole("button", { name: "发送消息" })).toBeDisabled();
    expect(missing.onSubmit).not.toHaveBeenCalled();
  });

  it("rejects content whose UTF-8 payload exceeds 16 KiB", () => {
    renderComposer();
    const oversized = "界".repeat(5_500);
    fireEvent.change(screen.getByRole("textbox", { name: "开始新对话" }),
      { target: { value: oversized } });
    expect(screen.getByText("消息不能超过 16 KiB")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发送消息" })).toBeDisabled();
  });
});
