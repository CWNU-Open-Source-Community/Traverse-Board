import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CyberAgentClient } from "../../api/client";
import type { NarrativeEntry } from "../projection/narrative";
import { projectThreadActivityDetail, V2ActivityGroup } from "./activity-detail";

type ActivityEntry = Extract<NarrativeEntry, { kind: "activity" }>;

afterEach(() => cleanup());

function activity(overrides: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    id: "activity-1",
    kind: "activity",
    activity: "execute",
    title: "运行命令",
    detail: "pnpm test session",
    status: "completed",
    createdAt: "2026-09-02T00:00:00Z",
    runId: "run-1",
    count: 1,
    provisional: false,
    items: [{
      title: "pnpm test session",
      detail: "测试已完成",
      status: "completed",
      provisional: false,
      detailRef: "command-1",
      detailAvailable: true,
    }],
    ...overrides,
  };
}

function renderActivity(client: CyberAgentClient, entry = activity()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>
    <V2ActivityGroup client={client} entry={entry} threadID="thread-1" />
  </QueryClientProvider>);
}

const commandDetail = (commands: Array<Record<string, unknown>>) => ({
  kind: "command" as const,
  command: { commands },
});

const boundary = (untrusted = false) => ({ authorization: "policy_checked" as const,
  error_code: "", failure_reason: "", truncated: false, untrusted });

function fileEditActivityDetail(diffAvailable = true) {
  return { kind: "file_edit" as const, file_edit: { operation: "inspect", action: "replace",
    path: "safe-target", destination_path: "", apply_status: "applied", applied: true,
    file_written: true, replayed: false, edit_id: diffAvailable ? "edit-1" : "",
    diff_available: diffAvailable,
    diff: { added_lines: 2, removed_lines: 1, hunks: 1, summary: "修改 safe-target · +2 −1" },
    boundary: boundary() } };
}

function typedDetail(kind: "web_search" | "web_fetch" | "file_read" | "file_edit" |
  "verification" | "mcp" | "browser") {
  switch (kind) {
    case "web_search": return { kind, web_search: { operation: "inspect", query: "safe-target",
      limit: 2, provider: "provider-native", search_policy: "provider_native",
      selection_reason: "configured route", source_count: 1, citeable: true,
      sources: [{ rank: 1, title: "Safe source", url: "https://example.com/reference",
        provider: "provider-native", state: "fetched", citeable: true }], boundary: boundary(true) } };
    case "web_fetch": return { kind, web_fetch: { operation: "inspect",
      url: "https://example.com/reference", state: "fetched", http_status: 200,
      robots: "allowed", robots_policy: "audit_only", redirects: 0, partial: false,
      citeable: true, boundary: boundary(true) } };
    case "file_read": return { kind, file_read: { operation: "inspect", path: "safe-target",
      query: "", pattern: "", start_line: 1, end_line: 2, limit: 0, result_count: 2,
      truncated: false, summary: "返回 2 项结果", boundary: boundary() } };
    case "file_edit": return fileEditActivityDetail();
    case "verification": return { kind, verification: { operation: "inspect",
      tool: "code_diagnostics", path: "safe-target", query: "", position: "",
      direction: "", limit: 10, result_count: 0, truncated: false, summary: "验证已完成",
      boundary: boundary() } };
    case "mcp": return { kind, mcp: { operation: "inspect", server: "safe-target",
      tool: "lookup", arguments: [{ name: "scope", type: "string", summary: "workspace" }],
      result: { type: "object", count: 1, summary: "对象 · 1 个字段（值已隐藏）",
        fields: [{ name: "status", type: "string", summary: "completed" }] },
      boundary: boundary(true) } };
    case "browser": return { kind, browser: { operation: "inspect", action: "browser_snapshot",
      url: "", selector: "#safe-target", input_length: 0, artifact_bytes: 0,
      summary: "浏览器操作已完成", boundary: boundary(true) } };
  }
}

describe("V2ActivityGroup", () => {
  it("loads a sanitized command projection only when its activity item is expanded", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2",
      activity_ref: "command-1",
      run_id: "run-1",
      tools: [{
        name: "command_runtime", label: "运行命令", agent_id: "agent-root", agent_role: "root" as const,
        agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        completed_at: "2026-09-02T00:00:02.400Z", duration_milliseconds: 2_400,
        detail: commandDetail([{
          command: "pnpm test session", working_directory: "packages/core",
          execution_environment: "Workspace Sandbox", network: "disabled",
           status: "completed", exit_code: 0, duration_milliseconds: 2_400,
           stdout_preview: "✓ 42 tests passed", stderr_preview: "", truncated: false, artifacts: [],
        }]),
      }],
    }));
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client);

    expect(threadActivityDetail).not.toHaveBeenCalled();
    await user.click(screen.getByText("运行"));
    expect(threadActivityDetail).not.toHaveBeenCalled();
    await user.click(screen.getByLabelText("查看pnpm test session的执行详情"));

    expect(await screen.findByText("✓ 42 tests passed")).toBeInTheDocument();
    expect(threadActivityDetail).toHaveBeenCalledTimes(1);
    expect(threadActivityDetail).toHaveBeenCalledWith(
      "thread-1", "command-1", expect.any(AbortSignal));
    expect(screen.getByText("packages/core")).toBeInTheDocument();
    expect(screen.getByText("Root Agent")).toBeInTheDocument();
    expect(screen.getByTitle("agent-root")).toHaveTextContent("agent-root");
    expect(screen.getByText("Workspace Sandbox · 无网络")).toBeInTheDocument();
    expect(screen.getByText("Exit 0 · 2.4s")).toBeInTheDocument();
  });

  it("preserves exact Specialist identity and labels historical unknown execution", async () => {
    const specialistID = "agent-specialist-analysis-1234567890";
    const specialist = projectThreadActivityDetail({
      version: "thread_activity_detail.v2", activity_ref: "command-specialist", run_id: "run-1",
      tools: [{ name: "command_runtime", label: "运行命令", agent_id: specialistID,
        agent_role: "specialist", agent_label: "Review Agent", status: "completed",
        started_at: "2026-09-03T00:00:00Z", duration_milliseconds: 1,
        detail: commandDetail([{ command: "go test ./...", working_directory: ".",
          execution_environment: "Workspace Sandbox", network: "disabled", status: "completed",
          exit_code: 0, duration_milliseconds: 1, stdout_preview: "", stderr_preview: "",
          truncated: false, artifacts: [] }]),
      }],
    } as never);
    expect(specialist.commands[0]?.agent_id).toBe(specialistID);

    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2" as const, activity_ref: "command-1", run_id: "run-1",
      tools: [{ name: "command_runtime", label: "运行命令", agent_id: "unknown",
        agent_role: "unknown" as const, agent_label: "历史活动（执行者未知）", status: "completed",
        started_at: "2026-09-03T00:00:00Z", duration_milliseconds: 1,
        detail: commandDetail([{ command: "go test ./...", working_directory: ".",
          execution_environment: "Workspace Sandbox", network: "disabled", status: "completed",
          exit_code: 0, duration_milliseconds: 1, stdout_preview: "", stderr_preview: "",
          truncated: false, artifacts: [] }]),
      }],
    }));
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client);
    await user.click(screen.getByText("运行"));
    await user.click(screen.getByLabelText("查看pnpm test session的执行详情"));
    expect(await screen.findByText("历史活动（执行者未知）")).toBeInTheDocument();
    expect(screen.queryByTitle("unknown")).not.toBeInTheDocument();
  });

  it("opens failed activities by default and offers an inline retry after a detail error", async () => {
    const threadActivityDetail = vi.fn()
      .mockRejectedValueOnce(new Error("sensitive backend failure"))
      .mockResolvedValueOnce({
        version: "thread_activity_detail.v2",
        activity_ref: "command-1", run_id: "run-1", tools: [{
          name: "command_runtime", label: "运行命令", agent_id: "agent-root",
          agent_role: "root" as const, agent_label: "Root Agent",
          status: "failed", started_at: "2026-09-02T00:00:00Z",
          completed_at: "2026-09-02T00:00:00.099Z", duration_milliseconds: 99,
          detail: commandDetail([{ command: "pnpm test session", working_directory: ".",
             execution_environment: "Workspace Sandbox", network: "disabled",
             status: "failed", exit_code: 2, duration_milliseconds: 99,
             stdout_preview: "", stderr_preview: "two tests failed", truncated: true, artifacts: [] }]),
        }],
      });
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    const view = renderActivity(client, activity({
      status: "failed",
      items: [{ ...activity().items[0], status: "failed", detail: "命令执行失败" }],
    }));

    await screen.findByRole("alert");
    expect(view.container.querySelector(".v2-activity")).toHaveAttribute("open");
    expect(view.container.querySelector(".v2-activity-item details")).toHaveAttribute("open");
    expect(screen.queryByText("sensitive backend failure")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("two tests failed")).toBeInTheDocument();
    expect(screen.getByText("Exit 2 · 99ms")).toBeInTheDocument();
    expect(screen.getByText(/输出过长，当前仅显示已脱敏预览/u)).toBeInTheDocument();
    expect(threadActivityDetail).toHaveBeenCalledTimes(2);
  });

  it("uses the command summary for collapsed facts and expands a real Job failure", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2", activity_ref: "command-1", run_id: "run-1",
      tools: [{ name: "command_runtime", label: "运行命令", agent_id: "agent-root",
        agent_role: "root" as const, agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        completed_at: "2026-09-02T00:00:00.125Z", duration_milliseconds: 125,
        detail: commandDetail([{ command: "pnpm test session", working_directory: ".",
           execution_environment: "Workspace Sandbox", network: "disabled",
           status: "timed_out", exit_code: 124, duration_milliseconds: 125,
           stdout_preview: "", stderr_preview: "deadline exceeded", truncated: false, artifacts: [] }]),
      }],
    }));
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const failedEntry = activity({ status: "completed", items: [{ ...activity().items[0],
      status: "completed", detail: "工具批次完成", summary: {
        version: "thread_activity_summary.v1", activity_ref: "command-1",
        command: "pnpm test session", status: "timed_out", exit_code: 124,
        duration_milliseconds: 125, command_count: 1,
      } }],
    });
    const view = renderActivity(client, failedEntry);

    expect(view.container.querySelector(".v2-activity")).toHaveAttribute("open");
    expect(view.container.querySelector(".v2-activity-item details")).toHaveAttribute("open");
    expect(screen.getByText("Exit 124")).toBeInTheDocument();
    expect(screen.getByText("125ms")).toBeInTheDocument();
    expect(await screen.findByText("deadline exceeded")).toBeInTheDocument();
  });

  it("opens a previously collapsed activity when its summary becomes failed", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2", activity_ref: "command-1", run_id: "run-1",
      tools: [{ name: "command_runtime", label: "运行命令", agent_id: "agent-root",
        agent_role: "root" as const, agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        completed_at: "2026-09-02T00:00:00.050Z", duration_milliseconds: 50,
        detail: commandDetail([{ command: "pnpm test session", working_directory: ".",
           execution_environment: "Workspace Sandbox", network: "disabled",
           status: "killed", duration_milliseconds: 50, stdout_preview: "",
           stderr_preview: "process killed", truncated: false, artifacts: [] }]),
      }],
    }));
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const initial = activity({ items: [{ ...activity().items[0], summary: {
      version: "thread_activity_summary.v1", activity_ref: "command-1",
      command: "pnpm test session", status: "completed", exit_code: 0,
      duration_milliseconds: 50, command_count: 1,
    } }] });
    const view = render(<QueryClientProvider client={queryClient}>
      <V2ActivityGroup client={client} entry={initial} threadID="thread-1" />
    </QueryClientProvider>);
    expect(view.container.querySelector(".v2-activity")).not.toHaveAttribute("open");

    const failed = activity({ items: [{ ...activity().items[0], summary: {
      version: "thread_activity_summary.v1", activity_ref: "command-1",
      command: "pnpm test session", status: "killed",
      duration_milliseconds: 50, command_count: 1,
    } }] });
    view.rerender(<QueryClientProvider client={queryClient}>
      <V2ActivityGroup client={client} entry={failed} threadID="thread-1" />
    </QueryClientProvider>);

    await waitFor(() => expect(view.container.querySelector(".v2-activity"))
      .toHaveAttribute("open"));
    await waitFor(() => expect(view.container.querySelector(".v2-activity-item details"))
      .toHaveAttribute("open"));
    expect(await screen.findByText("process killed")).toBeInTheDocument();
  });

  it("polls an expanded pending command until its live tail becomes terminal", async () => {
    const response = (status: "running" | "completed", stdout: string) => ({
      version: "thread_activity_detail.v2" as const, activity_ref: "command-1",
      run_id: "run-1", tools: [{ name: "command_runtime", label: "运行命令",
        agent_id: "agent-root", agent_role: "root" as const, agent_label: "Root Agent",
        status: status === "running" ? "pending" : "completed",
        started_at: "2026-09-02T00:00:00Z", duration_milliseconds: 500,
        detail: commandDetail([{ command: "pnpm test session", working_directory: ".",
          execution_environment: "Workspace Sandbox", network: "disabled",
           status, ...(status === "completed" ? { exit_code: 0 } : {}),
           duration_milliseconds: 500, stdout_preview: stdout, stderr_preview: "",
           truncated: false, artifacts: [] }]),
      }],
    });
    const threadActivityDetail = vi.fn()
      .mockResolvedValueOnce(response("running", "12 tests completed"))
      .mockResolvedValue(response("completed", "42 tests passed"));
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({ items: [{ ...activity().items[0], status: "pending",
      summary: { version: "thread_activity_summary.v1", activity_ref: "command-1",
        command: "pnpm test session", status: "running", duration_milliseconds: 500,
        command_count: 1 } }] }));

    await user.click(screen.getByText("运行"));
    await user.click(screen.getByLabelText("查看pnpm test session的执行详情"));
    expect(await screen.findByText("12 tests completed")).toBeInTheDocument();
    await waitFor(() => expect(threadActivityDetail.mock.calls.length).toBeGreaterThanOrEqual(2),
      { timeout: 2_500 });
    expect(await screen.findByText("42 tests passed")).toBeInTheDocument();
  });

  it("loads a complete sanitized output artifact only after the user asks for it", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2" as const, activity_ref: "command-1", run_id: "run-1",
      tools: [{ name: "command_runtime", label: "运行命令", agent_id: "agent-root",
        agent_role: "root" as const, agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        completed_at: "2026-09-02T00:00:01Z", duration_milliseconds: 1_000,
        detail: commandDetail([{ command: "pnpm test", working_directory: ".",
          execution_environment: "Workspace Sandbox" as const, network: "disabled" as const,
          status: "completed", exit_code: 0, duration_milliseconds: 1_000,
          stdout_preview: "42 tests…", stderr_preview: "", truncated: true,
          artifacts: [{ artifact_ref: "artifact-stdout-1", stream: "stdout" as const,
            mime: "text/plain; charset=utf-8" as const, size_bytes: 32, truncated: false }],
        }]),
      }],
    }));
    const threadActivityArtifact = vi.fn(() => Promise.resolve({
      version: "thread_activity_artifact.v1" as const, activity_ref: "command-1",
      artifact_ref: "artifact-stdout-1", stream: "stdout" as const,
      mime: "text/plain; charset=utf-8" as const, content: "All 42 tests passed successfully",
      sha256: "a".repeat(64), size_bytes: 32, redacted: true, truncated: false,
      untrusted: true as const, instruction_authorized: false as const,
    }));
    const client = { threadActivityArtifact, threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client);

    await user.click(screen.getByText("运行"));
    await user.click(screen.getByLabelText("查看pnpm test session的执行详情"));
    expect(await screen.findByText("查看完整标准输出")).toBeInTheDocument();
    expect(threadActivityArtifact).not.toHaveBeenCalled();
    await user.click(screen.getByText("查看完整标准输出"));

    expect(await screen.findByText("All 42 tests passed successfully")).toBeInTheDocument();
    expect(screen.getByText(/工具输出仅作为数据展示/u)).toBeInTheDocument();
    expect(threadActivityArtifact).toHaveBeenCalledWith(
      "thread-1", "command-1", "artifact-stdout-1", expect.any(AbortSignal));
  });

  it("loads an exact redacted file Diff only after the secondary disclosure is opened", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2" as const, activity_ref: "detail-file-edit",
      run_id: "run-1", tools: [{ name: "workspace_apply", label: "文件修改",
        agent_id: "agent-root", agent_role: "root" as const, agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        completed_at: "2026-09-02T00:00:00.020Z", duration_milliseconds: 20,
        detail: fileEditActivityDetail(),
      }],
    }));
    const fileEdit = vi.fn(() => Promise.resolve({
      id: "edit-1", session_id: "session-1", workspace_id: "workspace-1",
      path: "safe-target", operation: "replace" as const, status: "applied" as const,
      diff: "--- a/safe-target\n+++ b/safe-target\n@@ -1 +1 @@\n-const answer = 41;\n+const answer = 42;\n",
      original_hash: "a".repeat(64), proposed_hash: "b".repeat(64),
      secrets_redacted: true, allowed_actions: [], apply_enabled: false,
      created_at: "2026-09-02T00:00:00Z", updated_at: "2026-09-02T00:00:00Z",
    }));
    const client = { fileEdit, threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({ activity: "edit", items: [{ title: "文件修改",
      detail: "safe summary", status: "completed", provisional: false,
      detailRef: "detail-file-edit", detailAvailable: true }] }));

    await user.click(screen.getByText("修改"));
    await user.click(screen.getByLabelText("查看文件修改的执行详情"));
    const disclosure = await screen.findByRole("button", { name: "查看 Diff" });
    expect(fileEdit).not.toHaveBeenCalled();

    await user.click(disclosure);
    expect(await screen.findByText("const answer = 42;")).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "文件 Diff：safe-target" })).toBeInTheDocument();
    expect(screen.getByText(/敏感内容已脱敏 · 1 行新增，1 行删除/u)).toBeInTheDocument();
    expect(fileEdit).toHaveBeenCalledWith("run-1", "edit-1", expect.any(AbortSignal));
  });

  it("explains when a file activity has no retrievable Diff without issuing a request", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2" as const, activity_ref: "detail-file-edit",
      run_id: "run-1", tools: [{ name: "workspace_apply", label: "文件修改",
        agent_id: "unknown", agent_role: "unknown" as const,
        agent_label: "历史活动（执行者未知）", status: "completed",
        started_at: "2026-09-02T00:00:00Z", duration_milliseconds: 20,
        detail: fileEditActivityDetail(false),
      }],
    }));
    const fileEdit = vi.fn();
    const client = { fileEdit, threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({ activity: "edit", items: [{ title: "文件修改",
      detail: "safe summary", status: "completed", provisional: false,
      detailRef: "detail-file-edit", detailAvailable: true }] }));

    await user.click(screen.getByText("修改"));
    await user.click(screen.getByLabelText("查看文件修改的执行详情"));
    expect(await screen.findByText("本次活动没有可展示的 Diff。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "查看 Diff" })).not.toBeInTheDocument();
    expect(fileEdit).not.toHaveBeenCalled();
  });

  it("keeps a failed lazy Diff request inside the activity and offers retry", async () => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2" as const, activity_ref: "detail-file-edit",
      run_id: "run-1", tools: [{ name: "workspace_apply", label: "文件修改",
        agent_id: "agent-root", agent_role: "root" as const, agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        duration_milliseconds: 20, detail: fileEditActivityDetail(),
      }],
    }));
    const fileEdit = vi.fn().mockRejectedValue(new Error("private storage failure"));
    const client = { fileEdit, threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({ activity: "edit", items: [{ title: "文件修改",
      detail: "safe summary", status: "completed", provisional: false,
      detailRef: "detail-file-edit", detailAvailable: true }] }));

    await user.click(screen.getByText("修改"));
    await user.click(screen.getByLabelText("查看文件修改的执行详情"));
    await user.click(await screen.findByRole("button", { name: "查看 Diff" }));
    expect(await screen.findByText("Diff 加载失败。")).toBeInTheDocument();
    expect(screen.queryByText("private storage failure")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument();
  });

  it.each([
    ["web_search", "联网搜索", "web_search", "safe-target"],
    ["web_fetch", "网页读取", "web_fetch", "https://example.com/reference"],
    ["file_read", "文件读取", "workspace_read", "safe-target"],
    ["file_edit", "文件修改", "workspace_apply", "safe-target"],
    ["verification", "验证", "code_diagnostics", "safe-target"],
    ["mcp", "MCP", "mcp_tool_call", "workspace"],
    ["browser", "浏览器", "browser_snapshot", "#safe-target"],
  ] as const)("renders %s as a typed safe branch instead of raw JSON",
  async (kind, label, toolName, expected) => {
    const threadActivityDetail = vi.fn(() => Promise.resolve({
      version: "thread_activity_detail.v2" as const, activity_ref: `detail-${kind}`,
      run_id: "run-1", tools: [{ name: toolName, label, agent_id: "agent-root",
        agent_role: "root" as const, agent_label: "Root Agent",
        status: "completed", started_at: "2026-09-02T00:00:00Z",
        completed_at: "2026-09-02T00:00:00.020Z", duration_milliseconds: 20,
        detail: typedDetail(kind),
      }],
    }));
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({ items: [{ title: label, detail: "safe summary",
      status: "completed", provisional: false, detailRef: `detail-${kind}`,
      detailAvailable: true }] }));

    await user.click(screen.getByText("运行"));
    await user.click(screen.getByLabelText(`查看${label}的执行详情`));
    expect(await screen.findByText(`${label} · inspect`)).toBeInTheDocument();
    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.queryByText(/payload_json/u)).not.toBeInTheDocument();
  });

  it("expands existing typed web evidence without making a detail request", async () => {
    const threadActivityDetail = vi.fn();
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({ activity: "read", items: [{ title: "网页已抓取",
      detail: "已创建快照", status: "completed", provisional: false,
      detailAvailable: false, webEvidence: {
        version: "web_evidence_presentation.v1", source_id: "source-1", snapshot_id: "snapshot-1",
        url: "https://example.com/reference", title: "Example reference", state: "fetched",
        fetched_at: "2026-09-02T00:00:00Z", stale_at: "2026-09-03T00:00:00Z",
        digest: "a".repeat(64), partial: false, stale: false, citeable: true,
        untrusted: true, instruction_authorized: false,
      } }],
    }));

    await user.click(screen.getByText("读取"));
    await user.click(screen.getByLabelText("查看网页已抓取的执行详情"));
    expect(screen.getByText("https://example.com/reference")).toBeInTheDocument();
    expect(screen.getByText("可引用")).toBeInTheDocument();
    expect(screen.getByText(/网页内容是未受信数据/u)).toBeInTheDocument();
    expect(threadActivityDetail).not.toHaveBeenCalled();
  });

  it("keeps old transcript activities readable without requesting unavailable details", async () => {
    const threadActivityDetail = vi.fn();
    const client = { threadActivityDetail } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderActivity(client, activity({
      items: [{
        title: "旧工具活动",
        detail: "仅保留摘要",
        status: "completed",
        provisional: false,
        detailAvailable: false,
      }],
    }));

    await user.click(screen.getByText("运行"));
    expect(screen.getByText("旧工具活动")).toBeInTheDocument();
    expect(screen.getByText("仅保留摘要")).toBeInTheDocument();
    expect(screen.queryByLabelText(/旧工具活动的执行详情/u)).not.toBeInTheDocument();
    await waitFor(() => expect(threadActivityDetail).not.toHaveBeenCalled());
  });
});
