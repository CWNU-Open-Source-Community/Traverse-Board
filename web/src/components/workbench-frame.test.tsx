import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import type { CyberAgentClient } from "../api/client";
import { EmptyConversation, SidebarResizeHandle } from "./workbench-frame";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

describe("SidebarResizeHandle", () => {
  it("supports bounded keyboard resizing and double-click reset", () => {
    const onChange = vi.fn();
    render(<SidebarResizeHandle onChange={onChange} value={286} />);
    const separator = screen.getByRole("separator", { name: "调整侧栏宽度" });

    fireEvent.keyDown(separator, { key: "ArrowLeft" });
    fireEvent.keyDown(separator, { key: "ArrowRight" });
    fireEvent.keyDown(separator, { key: "Home" });
    fireEvent.keyDown(separator, { key: "End" });
    fireEvent.doubleClick(separator);

    expect(onChange.mock.calls.map((call) => call[0])).toEqual([274, 298, 232, 420, 286]);
  });
});

describe("EmptyConversation", () => {
  it("creates with Enter but leaves Shift+Enter and IME composition to the editor", () => {
    const onCreateRun = vi.fn();
    render(<QueryClientProvider client={new QueryClient()}>
      <EmptyConversation client={{} as CyberAgentClient} creationEnabled
        onCreateRun={onCreateRun} />
    </QueryClientProvider>);
    const composer = screen.getByLabelText("描述 Thread 目标");
    fireEvent.change(composer, { target: { value: "检查当前项目" } });

    expect(fireEvent.keyDown(composer, { key: "Enter", shiftKey: true })).toBe(true);
    expect(fireEvent.keyDown(composer, { key: "Enter", isComposing: true })).toBe(true);
    expect(onCreateRun).not.toHaveBeenCalled();

    expect(fireEvent.keyDown(composer, { key: "Enter" })).toBe(false);
    expect(onCreateRun).toHaveBeenCalledWith({ goal: "检查当前项目", phase: "deliver" });
  });

  it("keeps the new Thread composer visible beside setup and continue actions", () => {
    const onCreateRun = vi.fn();
    const onStartCoding = vi.fn();
    const first = render(<QueryClientProvider client={new QueryClient()}>
      <EmptyConversation client={{} as CyberAgentClient} creationEnabled
        onCreateRun={onCreateRun} onStartCoding={onStartCoding} />
    </QueryClientProvider>);
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(onStartCoding).toHaveBeenCalledOnce();
    const setupComposer = screen.getByLabelText("描述 Thread 目标");
    expect(setupComposer).toBeInTheDocument();
    fireEvent.change(setupComposer, { target: { value: "不要让设置挡住任务输入" } });
    fireEvent.keyDown(setupComposer, { key: "Enter" });
    expect(onCreateRun).toHaveBeenCalledWith({ goal: "不要让设置挡住任务输入",
      phase: "deliver" });
    first.unmount();

    const onContinueRun = vi.fn();
    render(<QueryClientProvider client={new QueryClient()}>
      <EmptyConversation client={{} as CyberAgentClient} creationEnabled
        onContinueRun={onContinueRun} onCreateRun={onCreateRun} />
    </QueryClientProvider>);
    fireEvent.click(screen.getByRole("button", { name: "继续" }));
    expect(onContinueRun).toHaveBeenCalledOnce();
    const continueComposer = screen.getByLabelText("描述 Thread 目标");
    expect(continueComposer).toBeInTheDocument();
    fireEvent.change(continueComposer, { target: { value: "开始另一个任务" } });
    fireEvent.click(screen.getByRole("button", { name: "创建 Thread" }));
    expect(onCreateRun).toHaveBeenCalledWith({ goal: "开始另一个任务", phase: "deliver" });
  });
});
