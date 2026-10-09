import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useLayoutEffect, useState } from "react";
import type { APIClient } from "../../api/client";
import type { RunView, TaskBudgetSettings, TaskConfigurationView } from "../../api/types";
import { TaskConfiguration } from "./task-configuration";

const view = (workspaceID = "workspace-1"): TaskConfigurationView => ({ version: "task_configuration.v1", workspace_id: workspaceID, profile: "code", requested_budget: { max_turns: 30, max_tool_calls: 40 }, budget: { max_turns: 12, max_tool_calls: 8 }, sources: [{ field: "budget.max_turns", source: "project" }, { field: "budget.max_tool_calls", source: "project" }], project_disposition: "applied", project: { protocol: "project_config.v1", read_only: false, allowed_profiles: [], excluded_path_count: 2, skill_suggestion_count: 1 }, project_fingerprint: "a".repeat(64), fingerprint: "b".repeat(64), rejections: [], capability_grant: false });
const clientFixture = () => ({ previewTaskConfiguration: vi.fn().mockResolvedValue(view()), getRunTaskConfiguration: vi.fn().mockResolvedValue(view()), createThread: vi.fn() });

describe("TaskConfiguration presentation fixtures", () => {
  it("edits the bounded draft, resets defaults and displays Go-owned effective sources", async () => {
    const client = clientFixture(), changed = vi.fn();
    function Host() {
      const [budget, setBudget] = useState<TaskBudgetSettings | undefined>({ max_turns: 30, max_tool_calls: 40 });
      return <TaskConfiguration client={client as unknown as APIClient} workspaceID="workspace-1" budget={budget} onBudgetChange={(next) => { changed(next); setBudget(next); }} />;
    }
    render(<Host />);
    await screen.findByText("生效的执行上限");
    expect(screen.getByText(/并非精确账单/)).toBeInTheDocument();
    expect(screen.getAllByText("项目收窄").length).toBeGreaterThan(0);
    fireEvent.change(screen.getByRole("spinbutton", { name: "回合上限" }), { target: { value: "50" } });
    expect(changed).toHaveBeenLastCalledWith({ max_turns: 50, max_tool_calls: 40 });
    fireEvent.click(screen.getByRole("button", { name: "恢复默认预算" }));
    expect(changed).toHaveBeenLastCalledWith(undefined);
    expect(client.createThread).not.toHaveBeenCalled();
  });

  it("reports invalid numeric drafts and whole-project rejections without partial effective values", async () => {
    const client = clientFixture(), validity = vi.fn();
    const { rerender } = render(<TaskConfiguration client={client as unknown as APIClient} workspaceID="workspace-1" budget={{ max_turns: -1 }} onValidityChange={validity} />);
    expect(screen.getByRole("alert")).toHaveTextContent("预算超出支持范围");
    expect(validity).toHaveBeenLastCalledWith(false);
    expect(client.previewTaskConfiguration).not.toHaveBeenCalled();
    client.previewTaskConfiguration.mockResolvedValue({ ...view(), project_disposition: "rejected", project: undefined, fingerprint: undefined, project_fingerprint: undefined, sources: [], rejections: [{ field: "budget.max_turns", reason: "must narrow" }] });
    rerender(<TaskConfiguration client={client as unknown as APIClient} workspaceID="workspace-1" onValidityChange={validity} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("项目配置已拒绝");
    await waitFor(() => expect(validity).toHaveBeenLastCalledWith(false));
    expect(screen.queryByText("生效的执行上限")).not.toBeInTheDocument();
  });

  it("discards a late preview after the selected workspace changes", async () => {
    const client = clientFixture(); let finish!: (value: TaskConfigurationView) => void;
    client.previewTaskConfiguration.mockImplementationOnce(() => new Promise<TaskConfigurationView>((resolve) => { finish = resolve; })).mockResolvedValueOnce(view("workspace-2"));
    const { rerender } = render(<TaskConfiguration client={client as unknown as APIClient} workspaceID="workspace-1" />);
    await waitFor(() => expect(client.previewTaskConfiguration).toHaveBeenCalledTimes(1));
    const firstSignal = client.previewTaskConfiguration.mock.calls[0][1] as AbortSignal;
    rerender(<TaskConfiguration client={client as unknown as APIClient} workspaceID="workspace-2" />);
    expect(firstSignal.aborted).toBe(true);
    await screen.findByText("生效的执行上限");
    finish({ ...view(), project_disposition: "rejected", rejections: [{ field: "project_config", reason: "stale rejection" }] });
    await waitFor(() => expect(screen.queryByText(/stale rejection/)).not.toBeInTheDocument());
  });

  it("reads an existing Run's pinned snapshot without offering edits or loading the live project", async () => {
    const client = clientFixture();
    render(<TaskConfiguration client={client as unknown as APIClient} workspaceID="workspace-1" run={{ id: "run-pinned" } as RunView} onBudgetChange={vi.fn()} />);
    await screen.findByText("已保存的执行上限");
    expect(client.getRunTaskConfiguration).toHaveBeenCalledWith("run-pinned", expect.any(AbortSignal));
    expect(client.previewTaskConfiguration).not.toHaveBeenCalled();
    expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "恢复默认预算" })).not.toBeInTheDocument();
  });

  it("hides the previous connection snapshot on the first render with a different client", async () => {
    const first = clientFixture(), second = clientFixture(), displayed: boolean[] = [];
    second.getRunTaskConfiguration.mockImplementation(() => new Promise<TaskConfigurationView>(() => {}));
    function Host({ client }: { client: APIClient }) {
      useLayoutEffect(() => { displayed.push(Boolean(screen.queryByText("已保存的执行上限"))); }, [client]);
      return <TaskConfiguration client={client} workspaceID="workspace-1" run={{ id: "run-pinned" } as RunView} />;
    }
    const { rerender } = render(<Host client={first as unknown as APIClient} />);
    await screen.findByText("已保存的执行上限");
    rerender(<Host client={second as unknown as APIClient} />);
    expect(displayed.at(-1)).toBe(false);
    expect(screen.queryByText("已保存的执行上限")).not.toBeInTheDocument();
    await waitFor(() => expect(second.getRunTaskConfiguration).toHaveBeenCalledTimes(1));
  });
});
