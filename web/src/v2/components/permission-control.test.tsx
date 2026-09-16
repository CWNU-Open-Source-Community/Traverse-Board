import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { WorkspaceFileAttachment } from "../../api/file-attachments";
import type { WorkspaceImageAttachment } from "../../api/image-attachments";
import type { ThreadExecutionPermissionControlView,
  ThreadExecutionPermissionView } from "../../api/types";
import { v2AttachmentReferenceKey } from "../attachment-keys";
import { V2Composer } from "./composer";
import { v2ImageReferenceKey } from "./image-input";
import { V2PermissionControl } from "./permission-control";

function permission(overrides: Partial<ThreadExecutionPermissionView> = {}):
ThreadExecutionPermissionView {
  return {
    thread_id: "thread-1", protocol_version: "thread_execution_permission.v1",
    revision: 1, mode: "conservative", approval_policy: "fixed_templates",
    command_scope: "fixed_templates", filesystem_scope: "workspace_guarded",
    network_scope: "disabled", persistent_terminal: false, background_process: false,
    agent_terminal_input: false, risk_tier: "minimal",
    required_gate: "conservative_control", policy_version: "execution_permission_policy.v1",
    operator_confirmed: false, runtime_gate_available: true,
    runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
      danger_full_access_enabled: true, debug_maximum_access_enabled: true },
    capability_matrix: { workspace_read: true, workspace_write: true,
      sandboxed_command_runtime: false, unsandboxed_host_process: false,
      network_access: false, credential_access: false, user_home_access: false,
      persistent_user_terminal: false, persistent_agent_terminal: false,
      full_cdp: false, out_of_scope_policy: "denied" },
    created_at: "2026-08-29T00:00:00Z", process_enabled: false,
    execution_authorized: false, capability_grant: false,
    applies_to_current_run: true, applies_to_future_successor_runs: true,
    ...overrides,
  };
}

function control(executionPermission = permission()): ThreadExecutionPermissionControlView {
  return { execution_permission: executionPermission, current_run_id: "run-1",
    current_run_effect: "applied", current_run_mode: executionPermission.mode,
    current_run_synchronized: true, replayed: false };
}

function renderPermission(initial = control(), changed = control(permission({
  mode: "full_access", risk_tier: "high", operator_confirmed: true, revision: 2,
}))) {
  const getThreadExecutionPermission = vi.fn().mockResolvedValueOnce(initial).mockResolvedValue(changed);
  const changeThreadExecutionPermission = vi.fn().mockResolvedValue(changed);
  const client = { hasExecutionPermissionControl: true, getThreadExecutionPermission,
    changeThreadExecutionPermission } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2PermissionControl client={client} threadID="thread-1" />
  </QueryClientProvider>);
  return { getThreadExecutionPermission, changeThreadExecutionPermission };
}

describe("V2PermissionControl", () => {
  it.each([
    ["full_access", "完全访问", "lucide-shield-off"],
    ["debug", "调试模式", "lucide-bug"],
  ] as const)("shows the saved %s risk icon without suggesting conservative protection", async (mode, label, icon) => {
    renderPermission(control(permission({ mode, risk_tier: "high" })));
    const trigger = await screen.findByRole("button", { name: label });
    expect(trigger.querySelector(`.${icon}`)).not.toBeNull();
    expect(trigger.querySelector(".lucide-shield-check")).toBeNull();
  });

  it("reads webpage and search status only when expanded inside permissions, without granting access", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockResolvedValue({ run: { status: "paused" },
      mode: { scope: { network_mode: "disabled", allowed_targets: [] } }, execution_permission: { mode: "conservative" } });
    const providerSearchReadiness = vi.fn().mockResolvedValue({ state: "provider_unqualified",
      reason: "provider_native_qualification_required", remediation: "qualify_provider_search" });
    const changeThreadExecutionPermission = vi.fn(); const expandRunNetworkAuthority = vi.fn();
    const client = { getThreadExecutionPermission: vi.fn().mockResolvedValue(control()),
      hasExecutionPermissionControl: true, hasControl: true, get, providerSearchReadiness,
      changeThreadExecutionPermission, expandRunNetworkAuthority } as unknown as CyberAgentClient;
    const queries = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queries}><V2PermissionControl client={client} threadID="thread-1" /></QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "保守模式" }));
    expect(get).not.toHaveBeenCalled(); expect(providerSearchReadiness).not.toHaveBeenCalled();
    await user.click(screen.getByText("网页访问与搜索"));
    expect(await screen.findByText("网页搜索 · 搜索待验证")).toBeInTheDocument();
    expect(screen.getByText("直接 URL 抓取")).toBeInTheDocument();
    expect(get).toHaveBeenCalledExactlyOnceWith("/runs/run-1", {}, expect.any(AbortSignal));
    expect(providerSearchReadiness).toHaveBeenCalledExactlyOnceWith("thread-1", expect.any(AbortSignal));
    expect(changeThreadExecutionPermission).not.toHaveBeenCalled();
    expect(expandRunNetworkAuthority).not.toHaveBeenCalled();
    await user.click(screen.getByText("网页访问与搜索"));
    await waitFor(() => expect(screen.queryByText("直接 URL 抓取")).not.toBeInTheDocument());
  });

  it.each(["click", "Enter"] as const)("keeps a real Composer draft and its attachments through cancellation and %s permission confirmation", async (activation) => {
    const user = userEvent.setup();
    const draft = "原回复未确认期间新写的要求，恢复后不要清掉。";
    const workspaceID = "workspace-1";
    const image: WorkspaceImageAttachment = { id: "draft-image", workspace_id: workspaceID,
      name: "未发送.png", sha256: "a".repeat(64), byte_size: 64, mime_type: "image/png", width: 4, height: 4 };
    const files: WorkspaceFileAttachment[] = Array.from({ length: 4 }, (_, index) => ({
      id: `draft-file-${index}`, workspace_id: workspaceID, name: `未发送-${index}.txt`,
      sha256: String(index + 1).repeat(64), byte_size: 12, mime_type: "text/plain",
      readability: "text", text_bytes: 12, redacted: false,
    }));
    const queries = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queries.setQueryData(v2ImageReferenceKey(workspaceID, "thread-1"), [image]);
    queries.setQueryData(v2AttachmentReferenceKey(workspaceID, "thread-1"), files);
    const changed = control(permission({ mode: "full_access", revision: 2, operator_confirmed: true }));
    const changeThreadExecutionPermission = vi.fn().mockResolvedValue(changed);
    const client = { baseURL: "/api/v1", hasThreadControl: true, hasModelControl: true,
      hasExecutionPermissionControl: true,
      getThreadExecutionPermission: vi.fn().mockResolvedValue(control()), changeThreadExecutionPermission,
      threadModelRoute: vi.fn().mockResolvedValue({ vision_capability: { state: "supported", source: "operator_declared" } }),
      downloadWorkspaceImage: vi.fn().mockResolvedValue(new Blob(["verified image"], { type: "image/png" })),
    } as unknown as CyberAgentClient;
    const onSubmit = vi.fn(async (..._args: unknown[]) => {});
    render(<QueryClientProvider client={queries}><V2Composer client={client} threadID="thread-1"
      workspaceID={workspaceID} workspaces={[]} onWorkspaceChange={() => {}} onSubmit={onSubmit} />
    </QueryClientProvider>);
    const textarea = screen.getByRole("textbox", { name: "继续对话" });
    fireEvent.change(textarea, { target: { value: draft } });
    await waitFor(() => expect(screen.getByRole("button", { name: "发送消息" })).toBeEnabled());
    const openConfirmation = async () => {
      await user.click(await screen.findByRole("button", { name: "保守模式" }));
      await user.click(within(screen.getByRole("group", { name: "对话执行权限" }))
        .getByRole("button", { name: /完全访问/u }));
      return screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    };
    const cancelled = await openConfirmation();
    await user.click(within(cancelled).getByRole("button", { name: "取消" }));
    expect(changeThreadExecutionPermission).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(textarea).toHaveValue(draft);

    const dialog = await openConfirmation();
    const confirm = within(dialog).getByRole("button", { name: "启用完全访问" });
    if (activation === "click") await user.click(confirm);
    else { confirm.focus(); await user.keyboard("{Enter}"); }
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(changeThreadExecutionPermission).toHaveBeenCalledExactlyOnceWith("thread-1", {
      mode: "full_access", reason: "v2 Thread permission selection", confirm_danger_full_access: true,
    }, expect.stringMatching(/^v2-thread-permission-/u));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(textarea).toHaveValue(draft);
    expect(queries.getQueryData(v2ImageReferenceKey(workspaceID, "thread-1"))).toEqual([image]);
    expect(queries.getQueryData(v2AttachmentReferenceKey(workspaceID, "thread-1"))).toEqual(files);
    expect(screen.getByRole("button", { name: "移除图片 未发送.png" })).toBeEnabled();
    for (const file of files) expect(screen.getByRole("button", { name: `移除文件 ${file.name}` })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "发送消息" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledExactlyOnceWith(draft, [], [image], undefined, files));
    await waitFor(() => expect(textarea).toHaveValue(""));
  });

  it("searches project files inside the real portal without sending the draft, then permits a Composer keyboard send", async () => {
    const user = userEvent.setup(); const onSubmit = vi.fn(async () => {});
    const queries = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const workspaceSearch = vi.fn().mockResolvedValue({ query: "README", results: [], truncated: false });
    const client = { baseURL: "/api/v1", hasThreadControl: true, hasEvidenceAttachment: true,
      workspaceExplore: vi.fn().mockResolvedValue({ protocol_version: "workspace_explorer.v1",
        workspace_id: "workspace-1", path: ".", kind: "directory", content: "", entries: [],
        redaction_count: 0, truncated: false, returned_bytes: 0, total_bytes: 0,
        provenance: { source_kind: "workspace_file", source_ref: ".", content_sha256: "b".repeat(64), instruction_authorized: false } }),
      workspaceSearch,
    } as unknown as CyberAgentClient;
    render(<QueryClientProvider client={queries}><V2Composer client={client} threadID="thread-1"
      workspaceID="workspace-1" workspaces={[]} onWorkspaceChange={() => {}} onSubmit={onSubmit} />
    </QueryClientProvider>);
    const textarea = screen.getByRole("textbox", { name: "继续对话" });
    fireEvent.change(textarea, { target: { value: "搜索文件不能代我发送" } });
    await user.click(screen.getByRole("button", { name: "添加附件" }));
    await user.click(screen.getByRole("menuitem", { name: /引用项目文件/u }));
    const search = await screen.findByRole("searchbox", { name: "Search Workspace evidence" });
    await user.type(search, "README{Enter}");
    await waitFor(() => expect(workspaceSearch).toHaveBeenCalledExactlyOnceWith("workspace-1", "README", expect.any(AbortSignal)));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(textarea).toHaveValue("搜索文件不能代我发送");
    await user.click(screen.getByRole("button", { name: "关闭文件选择" }));
    await user.click(textarea); await user.keyboard("{Enter}");
    await waitFor(() => expect(onSubmit).toHaveBeenCalledExactlyOnceWith("搜索文件不能代我发送"));
  });

  it("requires explicit confirmation and sends the exact full-access acknowledgement", async () => {
    const user = userEvent.setup();
    const controls = renderPermission();
    const trigger = await screen.findByRole("button", { name: "保守模式" });
    await user.click(trigger);
    const options = screen.getByRole("group", { name: "对话执行权限" });
    await user.click(within(options).getByRole("button", { name: /完全访问/u }));

    expect(controls.changeThreadExecutionPermission).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    expect(within(dialog).getByText(/完整 CDP 默认开启，可单独关闭/u)).toBeInTheDocument();
    expect(within(dialog).getByText(/当前执行必须已暂停并处于静止边界/u)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));

    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "full_access", reason: "v2 Thread permission selection",
        confirm_danger_full_access: true },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(trigger).toHaveFocus();
  });

  it("returns focus to the permission chip when a popover confirmation is cancelled", async () => {
    const user = userEvent.setup();
    renderPermission();
    const trigger = await screen.findByRole("button", { name: "保守模式" });
    await user.click(trigger);
    await user.click(within(screen.getByRole("group", { name: "对话执行权限" }))
      .getByRole("button", { name: /完全访问/u }));
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });

    await user.click(within(dialog).getByRole("button", { name: "取消" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("disables permission levels whose runtime gates are unavailable", async () => {
    const user = userEvent.setup();
    renderPermission(control(permission({ runtime: {
      workspace_sandbox_enabled: false, operator_approval_enabled: true,
      danger_full_access_enabled: false, debug_maximum_access_enabled: false,
    } })));
    await user.click(await screen.findByRole("button", { name: "保守模式" }));
    const options = screen.getByRole("group", { name: "对话执行权限" });
    expect(within(options).getByRole("button", { name: /工作区访问/u })).toBeDisabled();
    expect(within(options).getByRole("button", { name: /完全访问/u })).toBeDisabled();
    expect(within(options).getByRole("button", { name: /调试模式/u })).toBeDisabled();
  });

  it("can reactivate a persisted Full choice after a safe cold start", async () => {
    const user = userEvent.setup();
    const initial = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true,
      runtime_gate_available: false,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const activated = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true, revision: 3,
      runtime_gate_available: true,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const controls = renderPermission(initial, activated);

    const trigger = await screen.findByRole("button", { name: "完全访问" });
    await user.click(trigger);
    expect(screen.getByText("已选择 · 当前进程未授权")).toBeInTheDocument();
    const selected = within(screen.getByRole("group", { name: "对话执行权限" }))
      .getByRole("button", { name: "完全访问" });
    expect(selected).toHaveAttribute("aria-pressed", "true");
    expect(selected).toBeEnabled();
    expect(selected).toHaveTextContent("已保存 · 暂停且静止后确认激活");

    await user.click(selected);
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));

    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "full_access", reason: "v2 Thread permission selection",
        confirm_danger_full_access: true },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await user.click(trigger);
    expect(await screen.findByText("已应用到当前和后续执行")).toBeInTheDocument();
  });

  it("requires Full Access confirmation when a persisted Debug choice is cold", async () => {
    const user = userEvent.setup();
    const initial = control(permission({
      mode: "debug", approval_policy: "none", command_scope: "arbitrary_persistent",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "debug_maximum_access", operator_confirmed: true,
      persistent_terminal: true, background_process: true, agent_terminal_input: true,
      runtime_gate_available: false,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const activated = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true, revision: 3,
      runtime_gate_available: true,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const controls = renderPermission(initial, activated);

    await user.click(await screen.findByRole("button", { name: "调试模式" }));
    await user.click(within(screen.getByRole("group", { name: "对话执行权限" }))
      .getByRole("button", { name: "完全访问" }));

    expect(controls.changeThreadExecutionPermission).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));
    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "full_access", reason: "v2 Thread permission selection",
        confirm_danger_full_access: true },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
  });

  it("drops a high-risk task to conservative mode immediately without another dialog", async () => {
    const user = userEvent.setup();
    const initial = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true,
    }));
    const changed = control(permission({ revision: 3 }));
    const controls = renderPermission(initial, changed);
    await user.click(await screen.findByRole("button", { name: "完全访问" }));

    await user.click(screen.getByRole("button", { name: /立即降为保守模式/u }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "conservative", reason: "v2 Thread permission selection" },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
  });
});
