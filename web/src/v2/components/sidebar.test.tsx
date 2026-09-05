import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ThreadView, WorkspaceView } from "../../api/types";
import { V2SettingsSidebar, V2Sidebar } from "./sidebar";

const workspace: WorkspaceView = {
  id: "workspace-1", name: "Traverse Board", created_at: "2026-08-29T00:00:00Z",
};
const thread: ThreadView = {
  id: "thread-1", protocol_version: "thread.v1", workspace_id: workspace.id,
  mission_id: "mission-1", title: "Permission audit", status: "active",
  active_run_id: "run-1", last_run_id: "run-1", version: 3,
  composer_state: "ready", created_at: "2026-08-29T00:00:00Z",
  updated_at: "2026-08-29T00:00:00Z",
};

describe("V2Sidebar archive menu", () => {
  it("passes the exact selected Thread to the archive confirmation owner and closes the menu", async () => {
    const user = userEvent.setup();
    const onArchive = vi.fn();
    render(<V2Sidebar onArchive={onArchive} onNewConversation={vi.fn()}
      onOpenModels={vi.fn()} onOpenSettings={vi.fn()} onSearchOpen={vi.fn()} onSelectThread={vi.fn()}
      searchOpen={false} selectedThreadID={thread.id} threads={[thread]}
      workspaces={[workspace]} />);

    await user.click(screen.getByRole("button", { name: "Permission audit 的操作" }));
    const menu = screen.getByRole("menu");
    await user.click(within(menu).getByRole("menuitem", { name: "归档" }));

    expect(onArchive).toHaveBeenCalledTimes(1);
    expect(onArchive).toHaveBeenCalledWith(thread);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});

describe("V2 model navigation", () => {
  it("places a dedicated model entry immediately after new conversation", async () => {
    const user = userEvent.setup();
    const onOpenModels = vi.fn();
    render(<V2Sidebar onArchive={vi.fn()} onNewConversation={vi.fn()}
      onOpenModels={onOpenModels} onOpenSettings={vi.fn()} onSearchOpen={vi.fn()}
      onSelectThread={vi.fn()} searchOpen={false} selectedThreadID="" threads={[]}
      workspaces={[]} />);

    const navigation = screen.getByRole("navigation", { name: "对话导航" });
    const actions = within(navigation).getAllByRole("button");
    expect(actions.map((button) => button.textContent)).toEqual([
      expect.stringContaining("新对话"),
      "接入模型",
      "搜索对话",
    ]);
    await user.click(within(navigation).getByRole("button", { name: "接入模型" }));
    expect(onOpenModels).toHaveBeenCalledOnce();
  });

  it("places Models between General and Permissions and exposes page state", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<V2SettingsSidebar onBack={vi.fn()} onSelect={onSelect} section="models" />);

    const navigation = screen.getByRole("navigation", { name: "设置分类" });
    const labels = within(navigation).getAllByRole("button").map((button) => button.textContent);
    expect(labels.slice(0, 3)).toEqual(["常规", "模型", "权限"]);
    expect(labels).not.toContain("智能伙伴");
    const models = within(navigation).getByRole("button", { name: "模型" });
    expect(models).toHaveAttribute("aria-current", "page");
    await user.click(models);
    expect(onSelect).toHaveBeenCalledWith("models");
  });
});
