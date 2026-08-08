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
    const composer = screen.getByLabelText("描述任务");
    fireEvent.change(composer, { target: { value: "检查当前项目" } });

    expect(fireEvent.keyDown(composer, { key: "Enter", shiftKey: true })).toBe(true);
    expect(fireEvent.keyDown(composer, { key: "Enter", isComposing: true })).toBe(true);
    expect(onCreateRun).not.toHaveBeenCalled();

    expect(fireEvent.keyDown(composer, { key: "Enter" })).toBe(false);
    expect(onCreateRun).toHaveBeenCalledWith({ goal: "检查当前项目", phase: "deliver" });
  });
});
