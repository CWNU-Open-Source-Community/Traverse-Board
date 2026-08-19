import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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

function renderPanel(client: CyberAgentClient, runStatus = "paused") {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } })}>
    <WorkspaceCheckpointPanel client={client} runID="run-1" runStatus={runStatus} />
  </QueryClientProvider>);
}

describe("WorkspaceCheckpointPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("browses provenance, previews impact, and confirms an auditable Rewind", async () => {
    const get = vi.fn().mockResolvedValue(timeline());
    const postControl = vi.fn().mockImplementation((path: string) =>
      Promise.resolve(path.endsWith("/preview") ? preview() : {
        ...preview(), confirmed: true,
      }));
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
