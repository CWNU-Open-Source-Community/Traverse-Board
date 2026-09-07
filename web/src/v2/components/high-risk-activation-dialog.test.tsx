import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { V2HighRiskActivationDialog } from "./high-risk-activation-dialog";

afterEach(cleanup);

describe("V2HighRiskActivationDialog", () => {
  it("describes full access, focuses the safe action, and confirms only on demand", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    render(<V2HighRiskActivationDialog onCancel={onCancel} onConfirm={onConfirm}
      open phase="idle" profile="full_access" />);

    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    expect(within(dialog).getByText("文件和文件夹")).toBeInTheDocument();
    expect(within(dialog).getByText("终端命令")).toBeInTheDocument();
    expect(within(dialog).getByText("互联网、浏览器和已连接的应用")).toBeInTheDocument();
    expect(within(dialog).getByText(/当前系统用户权限范围/u)).toBeInTheDocument();
    const scope = within(dialog).getByRole("note");
    expect(within(scope).getByText("影响范围")).toBeInTheDocument();
    expect(within(scope).getByText(/当前执行必须已暂停并处于静止边界/u)).toBeInTheDocument();
    expect(within(scope).getByText(/不会重启应用/u)).toBeInTheDocument();
    expect(within(dialog).getByText(/完整 CDP 作为其子开关默认开启/u)).toBeInTheDocument();

    const cancel = within(dialog).getByRole("button", { name: "取消" });
    await waitFor(() => expect(cancel).toHaveFocus());
    expect(onConfirm).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("cancels with Escape or the backdrop without confirming", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    const { container } = render(<V2HighRiskActivationDialog onCancel={onCancel}
      onConfirm={onConfirm} open profile="debug" />);

    expect(screen.getByText("完全访问的全部能力")).toBeInTheDocument();
    expect(screen.getByText("持久终端和后台进程")).toBeInTheDocument();
    expect(screen.getByText("终端输入与调试控制")).toBeInTheDocument();
    expect(screen.getByText(/完整 CDP 默认开启，可在权限页关闭/u)).toBeInTheDocument();
    expect(screen.getByText(/初始化持久终端、后台进程和限时终端输入运行时/u))
      .toBeInTheDocument();
    expect(screen.getByText(/重启不会改写任何任务的已保存权限/u)).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(onCancel).toHaveBeenCalledTimes(1);

    const overlay = document.body.querySelector<HTMLElement>(".v2-high-risk-overlay");
    expect(overlay).not.toBeNull();
    fireEvent.mouseDown(overlay!);
    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onConfirm).not.toHaveBeenCalled();
    expect(container).toBeEmptyDOMElement();
  });

  it("locks dismissal while the native confirmation or restart is in progress", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<V2HighRiskActivationDialog onCancel={onCancel} onConfirm={vi.fn()}
      open phase="restarting" profile="debug" />);

    const dialog = screen.getByRole("dialog", { name: "要开启调试模式吗？" });
    expect(dialog).toHaveAttribute("aria-busy", "true");
    expect(within(dialog).getByRole("status")).toHaveTextContent("正在重启…");
    expect(within(dialog).getByRole("button", { name: "取消" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("isolates the background tree and returns focus to an explicit activation control", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      const triggerRef = useRef<HTMLButtonElement>(null);
      return <><button onClick={() => setOpen(true)} ref={triggerRef} type="button">
        Activate full access
      </button><V2HighRiskActivationDialog onCancel={() => setOpen(false)}
        onConfirm={vi.fn()} open={open} profile="full_access" returnFocusRef={triggerRef} /></>;
    }
    const user = userEvent.setup();
    const { container } = render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Activate full access" });

    await user.click(trigger);

    expect(container).toHaveAttribute("inert");
    expect(container).toHaveAttribute("aria-hidden", "true");
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    await user.click(within(dialog).getByRole("button", { name: "取消" }));

    expect(container).not.toHaveAttribute("inert");
    expect(container).not.toHaveAttribute("aria-hidden");
    expect(trigger).toHaveFocus();
  });
});
