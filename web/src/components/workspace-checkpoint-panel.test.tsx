import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type {
  WorkspaceCheckpointRestoreView,
  WorkspaceCheckpointTimelineView,
  WorkspaceCheckpointView,
} from "../api/types";
import { WorkspaceCheckpointPanel } from "./workspace-checkpoint-panel";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ t: (chinese: string) => chinese }),
}));

function checkpoint(id: string, title: string, createdAt: string,
  recovery = "complete"): WorkspaceCheckpointView {
  return {
    id,
    protocol_version: "workspace-checkpoint.v1",
    run_id: "run-1",
    mission_id: "mission-1",
    session_id: "session-1",
    workspace_id: "workspace-1",
    trigger: "command_batch",
    phase: "after",
    trigger_receipt_id: `receipt-${id}`,
    root_fingerprint: "a".repeat(64),
    root_path_sha256: "b".repeat(64),
    base_commit: "c".repeat(40),
    branch: "codex/test",
    index_sha256: "d".repeat(64),
    manifest_sha256: "e".repeat(64),
    recovery_level: recovery,
    incomplete_reasons: recovery === "partial" ? ["filesystem watcher unavailable"] : [],
    entry_count: 2,
    stored_bytes: 24,
    created_at: createdAt,
    title,
  };
}

function timeline(): WorkspaceCheckpointTimelineView {
  const before = checkpoint("checkpoint-before", "Before shell", "2026-08-18T00:00:00Z", "partial");
  const current = checkpoint("checkpoint-current", "After shell", "2026-08-18T00:01:00Z");
  return {
    protocol_version: "workspace-checkpoint-api.v1",
    run_id: "run-1",
    workspace_id: "workspace-1",
    current: {
      run_id: "run-1",
      workspace_id: "workspace-1",
      current_checkpoint_id: current.id,
      last_transaction_id: "transaction-shell",
      updated_at: "2026-08-18T00:01:00Z",
    },
    checkpoints: [current, before],
    transactions: [{
      id: "transaction-shell",
      protocol_version: "workspace-checkpoint.v1",
      operation_key_digest: "1".repeat(64),
      request_fingerprint: "2".repeat(64),
      run_id: "run-1",
      workspace_id: "workspace-1",
      kind: "command_batch",
      trigger_receipt_id: "receipt-shell",
      before_checkpoint_id: before.id,
      after_checkpoint_id: current.id,
      status: "completed",
      recovery_level: "partial",
      created_at: "2026-08-18T00:00:00Z",
      updated_at: "2026-08-18T00:01:00Z",
      completed_at: "2026-08-18T00:01:00Z",
    }],
    storage_usage: { blob_bytes: 24, blob_count: 2, checkpoint_count: 2 },
  };
}

function preview(): WorkspaceCheckpointRestoreView {
  return {
    protocol_version: "workspace-checkpoint-api.v1",
    confirmed: false,
    replayed: false,
    before: timeline().checkpoints[0]!,
    preview: {
      protocol_version: "workspace-checkpoint.v1",
      expected_current_checkpoint_id: "checkpoint-current",
      target_checkpoint_id: "checkpoint-before",
      observed_checkpoint_id: "checkpoint-observed",
      recovery_level: "partial",
      index_changed: false,
      truncated: false,
      conflicts: [],
      changes: [{
        kind: "modify",
        path: "src/parser.go",
        from_sha256: "a".repeat(64),
        to_sha256: "b".repeat(64),
        recoverable: true,
        binary: false,
      }],
    },
  };
}

function renderPanel(client: CyberAgentClient, runStatus = "paused", queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } })) {
  return render(<QueryClientProvider client={queryClient}>
    <WorkspaceCheckpointPanel client={client} runID="run-1" runStatus={runStatus} />
  </QueryClientProvider>);
}

function restored(status: "completed" | "failed" = "completed", kind = "undo"): WorkspaceCheckpointRestoreView {
  return { ...preview(), confirmed: true, replayed: true, transaction: {
    ...timeline().transactions[0]!, id: "transaction-restore", status, kind,
    target_checkpoint_id: "checkpoint-before", expected_current_checkpoint_id: "checkpoint-current",
  } };
}

describe("WorkspaceCheckpointPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("blocks an incomplete preview and preserves the restore identity after an uncertain response", async () => {
    let incomplete = true;
    const postControl = vi.fn().mockImplementation((path: string) => path.endsWith("/preview")
      ? Promise.resolve({ ...preview(), preview: { ...preview().preview, truncated: incomplete } })
      : Promise.reject(new Error("connection lost before confirmation")));
    const client = { get: vi.fn().mockResolvedValue(timeline()), postControl,
      hasWorkspaceCheckpointControl: true } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn(() => true));
    const user = userEvent.setup();
    renderPanel(client);
    await user.click(await screen.findByRole("button", { name: "预览 Undo" }));
    expect(await screen.findByRole("button", { name: "确认执行 撤销" })).toBeDisabled();
    expect(globalThis.confirm).not.toHaveBeenCalled();
    incomplete = false;
    await user.click(screen.getByRole("button", { name: "预览 Undo" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "确认执行 撤销" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "确认执行 撤销" }));
    await screen.findByText("connection lost before confirmation");
    await user.click(screen.getByRole("button", { name: "确认上次恢复" }));
    await waitFor(() => expect(postControl.mock.calls.filter(([path]) => path.endsWith("/undo"))).toHaveLength(2));
    const attempts = postControl.mock.calls.filter(([path]) => path.endsWith("/undo"));
    expect(attempts[1]).toEqual(attempts[0]);
  });

  it("browses provenance, previews impact, and confirms an auditable Rewind", async () => {
    const get = vi.fn().mockResolvedValue(timeline());
    const postControl = vi.fn().mockImplementation((path: string) =>
      Promise.resolve(path.endsWith("/preview") ? preview() : restored("completed", "rewind")));
    const client = { get, postControl, hasWorkspaceCheckpointControl: true } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn(() => true));
    const user = userEvent.setup();
    renderPanel(client);

    await user.click(await screen.findByRole("button", { name: /Before shell/ }));
    expect(screen.getByText("filesystem watcher unavailable")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "预览 Rewind" }));
    expect(await screen.findByText("src/parser.go")).toBeInTheDocument();
    expect(screen.getByText(/1 项影响/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认执行 Rewind" }));

    await waitFor(() => expect(postControl).toHaveBeenCalledWith(
      "/runs/run-1/workspace-checkpoints/rewind",
      expect.objectContaining({
        target_checkpoint_id: "checkpoint-before",
        expected_current_checkpoint_id: "checkpoint-current",
        confirm: true,
      }), expect.any(String),
    ));
    expect(globalThis.confirm).toHaveBeenCalled();
  });

  it("keeps restore controls closed without control authority or a paused Run", async () => {
    const client = {
      get: vi.fn().mockResolvedValue(timeline()),
      postControl: vi.fn(),
      hasWorkspaceCheckpointControl: false,
    } as unknown as CyberAgentClient;
    renderPanel(client, "running");

    expect(await screen.findByText(/当前连接只能浏览时间线/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "立即检查点" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "预览 Undo" })).toBeDisabled();
    expect(client.postControl).not.toHaveBeenCalled();
  });

  it("ignores an older preview when the operator selects another checkpoint while it is loading", async () => {
    let complete!: (result: WorkspaceCheckpointRestoreView) => void;
    const data = timeline();
    data.checkpoints.push(checkpoint("checkpoint-earlier", "Earlier work", "2026-08-17T00:00:00Z"));
    const postControl = vi.fn(() => new Promise<WorkspaceCheckpointRestoreView>((resolve) => { complete = resolve; }));
    const client = { get: vi.fn().mockResolvedValue(data), postControl,
      hasWorkspaceCheckpointControl: true } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderPanel(client);
    await user.click(await screen.findByRole("button", { name: /Before shell/ }));
    await user.click(screen.getByRole("button", { name: "预览 Rewind" }));
    await user.click(screen.getByRole("button", { name: /Earlier work/ }));
    await act(async () => complete(preview()));
    expect(screen.getByRole("button", { name: /Earlier work/ })).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: "确认执行 Rewind" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("恢复预览")).not.toBeInTheDocument();
  });

  it("preserves an unknown restore across unmount and cursor changes and confirms the exact original request", async () => {
    let data = timeline();
    let restoreCalls = 0;
    const postControl = vi.fn(async (path: string) => {
      if (path.endsWith("/preview")) return preview();
      if (++restoreCalls === 1) {
        data = { ...data, current: { ...data.current!, current_checkpoint_id: "checkpoint-new-cursor" } };
        throw new Error("restore response lost");
      }
      return restored();
    });
    const client = { get: vi.fn(async () => data), postControl, hasWorkspaceCheckpointControl: true } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const user = userEvent.setup();
    vi.stubGlobal("confirm", vi.fn(() => true));
    const first = renderPanel(client, "paused", queryClient);
    await user.click(await screen.findByRole("button", { name: "预览 Undo" }));
    await user.click(await screen.findByRole("button", { name: "确认执行 撤销" }));
    await screen.findByText("restore response lost");
    first.unmount();
    renderPanel(client, "running", queryClient);
    expect(await screen.findByRole("button", { name: "预览 Undo" })).toBeDisabled();
    await user.click(await screen.findByRole("button", { name: "确认上次恢复" }));
    expect(await screen.findByRole("status")).toHaveTextContent("项目恢复已完成");
    const attempts = postControl.mock.calls.filter(([path]) => path.endsWith("/undo"));
    expect(attempts).toHaveLength(2);
    expect(attempts[1]).toEqual(attempts[0]);
    expect(queryClient.getQueryData(["run", "run-1", "workspace-restore-intent"])).toBeNull();
    expect(screen.queryByRole("button", { name: "确认上次恢复" })).not.toBeInTheDocument();
  });

  it("treats a replayed failed transaction as a confirmed failure and allows a fresh preview", async () => {
    const postControl = vi.fn(async (path: string) => path.endsWith("/preview") ? preview() : restored("failed"));
    const client = { get: vi.fn().mockResolvedValue(timeline()), postControl,
      hasWorkspaceCheckpointControl: true } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    vi.stubGlobal("confirm", vi.fn(() => true));
    renderPanel(client);
    await user.click(await screen.findByRole("button", { name: "预览 Undo" }));
    await user.click(await screen.findByRole("button", { name: "确认执行 撤销" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("已确认本次恢复失败");
    expect(screen.queryByText(/项目恢复已完成/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "确认上次恢复" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "预览 Undo" })).toBeEnabled();
  });

  it("forks through a Go-derived worktree path without renderer path input", async () => {
    const postControl = vi.fn().mockResolvedValue({ run: { id: "run-fork" } });
    const client = {
      get: vi.fn().mockResolvedValue(timeline()),
      postControl,
      hasWorkspaceCheckpointControl: true,
    } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn(() => true));
    const user = userEvent.setup();
    renderPanel(client);

    await screen.findByText("不可变时间线");
    await user.type(screen.getByLabelText("新 Workspace 名称"), "parser fork");
    await user.type(screen.getByLabelText("新 Git 分支"), "codex/parser-fork");
    await user.click(screen.getByRole("button", { name: "确认 Fork" }));

    await waitFor(() => expect(postControl).toHaveBeenCalledWith(
      "/runs/run-1/workspace-checkpoints/fork",
      expect.objectContaining({
        workspace_name: "parser fork",
        branch: "codex/parser-fork",
        confirm: true,
      }), expect.any(String),
    ));
    const body = postControl.mock.calls[0]?.[1] as Record<string, unknown>;
    expect(body).not.toHaveProperty("workspace_root");
  });
});
