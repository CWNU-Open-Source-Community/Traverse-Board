import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { ThreadView } from "../../api/types";
import { V2Settings } from "./settings";

afterEach(() => vi.unstubAllGlobals());

function archivedThread(id: string, title: string, version: number): ThreadView {
  return {
    id, protocol_version: "thread.v1", workspace_id: `workspace-${id}`,
    mission_id: `mission-${id}`, title, status: "archived", last_run_id: `run-${id}`,
    version, composer_state: "unavailable", archived_at: "2026-08-29T02:00:00Z",
    created_at: "2026-08-29T00:00:00Z", updated_at: "2026-08-29T02:00:00Z",
  };
}

function renderArchived(threads: ThreadView[]) {
  const getPage = vi.fn().mockResolvedValue({ items: threads, page: { limit: 100 },
    requestID: "request-v2-archived" });
  const transitionThread = vi.fn().mockResolvedValue({});
  const client = { hasThreadControl: true, getPage,
    transitionThread } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2Settings client={client} onOpenInspector={vi.fn()} onSelectSection={vi.fn()}
      section="archived" threadID="thread-current" workspaces={[]} />
  </QueryClientProvider>);
  return { getPage, transitionThread };
}

function renderModels(configuredProviders: string[] = []) {
  const providerDefinitions = vi.fn().mockResolvedValue({
    version: "provider_definition_collection.v1",
    revision: 0,
    providers: [],
  });
  const providerCredentialStatuses = vi.fn().mockResolvedValue({
    protocol_version: "provider_credential.v1",
    items: configuredProviders.map((provider) => ({
      protocol_version: "provider_credential.v1",
      provider,
      configured: true,
      plaintext_returned: false,
      registry_generation: 1,
      registry_reloaded: false,
      restart_required: false,
      store_available: true,
      store_kind: "windows_credential_manager",
    })),
  });
  const client = {
    hasProviderDefinitions: true,
    hasProviderCredentials: true,
    providerDefinitions,
    providerCredentialStatuses,
  } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2Settings client={client} onOpenInspector={vi.fn()} onSelectSection={vi.fn()}
      section="models" threadID="thread-current" workspaces={[]} />
  </QueryClientProvider>);
  return { providerDefinitions, providerCredentialStatuses };
}

describe("V2 archived settings", () => {
  it("loads only archived chats and filters locally without mutating lifecycle state", async () => {
    const controls = renderArchived([
      archivedThread("alpha", "Alpha investigation", 7),
      archivedThread("beta", "Beta delivery", 11),
    ]);
    expect(await screen.findByText("Alpha investigation")).toBeInTheDocument();
    expect(screen.getByText("Beta delivery")).toBeInTheDocument();
    expect(controls.getPage).toHaveBeenCalledWith("/threads",
      { limit: 100, status: "archived" }, "", expect.any(AbortSignal));

    fireEvent.change(screen.getByRole("searchbox", { name: "搜索已归档的聊天" }),
      { target: { value: "beta" } });
    expect(screen.queryByText("Alpha investigation")).not.toBeInTheDocument();
    expect(screen.getByText("Beta delivery")).toBeInTheDocument();
    expect(controls.transitionThread).not.toHaveBeenCalled();
  });

  it("restores with the exact Thread version", async () => {
    const user = userEvent.setup();
    const thread = archivedThread("restore", "Restore me", 7);
    const controls = renderArchived([thread]);
    const article = (await screen.findByText(thread.title)).closest("article");
    expect(article).not.toBeNull();
    await user.click(within(article!).getByRole("button", { name: "取消归档" }));

    await waitFor(() => expect(controls.transitionThread).toHaveBeenCalledWith(
      "restore", "restore", { version: "thread_lifecycle.v1", expected_version: 7 },
      expect.stringMatching(/^v2-archived-restore-/u),
    ));
  });

  it("does not delete until the destructive confirmation is accepted", async () => {
    const user = userEvent.setup();
    const thread = archivedThread("delete", "Delete me", 11);
    const controls = renderArchived([thread]);
    await user.click(await screen.findByRole("button", { name: "删除 Delete me" }));

    expect(controls.transitionThread).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "删除已归档的聊天" });
    expect(within(dialog).getByText(/底层审计记录仍按项目保留策略保存/u)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "删除" }));

    await waitFor(() => expect(controls.transitionThread).toHaveBeenCalledWith(
      "delete", "delete", { version: "thread_lifecycle.v1", expected_version: 11 },
      expect.stringMatching(/^v2-archived-delete-/u),
    ));
  });
});

describe("V2 general permission summary", () => {
  it("uses neutral management actions instead of hard-coded enabled switches", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      "HarmonyOS Sans Fonts License Agreement\nTHIS HARMONYOS SANS FONTS LICENSE AGREEMENT",
      { headers: { "Content-Type": "text/plain; charset=utf-8" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    const onSelectSection = vi.fn();
    const queryClient = new QueryClient();
    render(<QueryClientProvider client={queryClient}>
      <V2Settings client={{} as CyberAgentClient} onOpenInspector={vi.fn()}
        onSelectSection={onSelectSection} section="general" threadID="thread-current"
        workspaces={[]} />
    </QueryClientProvider>);

    const defaultPermissions = screen.getByRole("button", { name: "管理默认权限" });
    const fullAccess = screen.getByRole("button", { name: "管理完整访问权限" });
    expect(defaultPermissions).not.toHaveAttribute("aria-pressed");
    expect(fullAccess).not.toHaveAttribute("aria-pressed");
    const licenseButton = screen.getByRole("button", { name: "查看许可" });
    await user.click(licenseButton);
    const dialog = screen.getByRole("dialog", { name: "HarmonyOS Sans Fonts 许可" });
    expect(await within(dialog).findByText(/THIS HARMONYOS SANS FONTS LICENSE AGREEMENT/u))
      .toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/licenses/HarmonyOS-Sans.txt", {
      cache: "no-store", signal: expect.any(AbortSignal),
    });
    await user.click(within(dialog).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog", { name: "HarmonyOS Sans Fonts 许可" })).not.toBeInTheDocument();
    expect(licenseButton).toHaveFocus();
    await user.click(fullAccess);
    expect(onSelectSection).toHaveBeenCalledWith("permissions");
  });
});

describe("V2 model provider catalog", () => {
  it("opens an official preset as an editable provider draft and restores catalog focus", async () => {
    const user = userEvent.setup();
    const controls = renderModels(["official-openai"]);

    const openAI = await screen.findByRole("button", {
      name: /OpenAI，gpt-5\.6-terra，已保存 API Key/u,
    });
    await user.click(openAI);

    expect(await screen.findByRole("heading", { name: "添加供应商" })).toBeInTheDocument();
    expect(screen.getByLabelText("供应商 ID")).toHaveValue("official-openai");
    expect(screen.getByLabelText("显示名称")).toHaveValue("OpenAI 官方 API");
    expect(screen.getByLabelText("请求地址")).toHaveValue("https://api.openai.com/v1/responses");
    expect(screen.getByLabelText("协议")).toHaveValue("openai_responses");
    expect(screen.getByLabelText("默认模型")).toHaveValue("gpt-5.6-terra");
    expect(screen.getByRole("textbox", { name: "高级 JSON" })).toBeEnabled();
    expect(controls.providerDefinitions).toHaveBeenCalledOnce();
    expect(controls.providerCredentialStatuses).toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "返回模型目录" }));
    expect(await screen.findByRole("heading", { name: "模型" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", {
      name: /OpenAI，gpt-5\.6-terra，已保存 API Key/u,
    })).toHaveFocus());
  });

  it("keeps GitHub Copilot as an honest account connector instead of an API-key form", async () => {
    const user = userEvent.setup();
    renderModels();

    const copilot = screen.getByRole("button", { name: /^GitHub Copilot，/u });
    await user.click(copilot);
    const dialog = screen.getByRole("dialog", { name: "GitHub Copilot 需要账户连接" });
    expect(within(dialog).getByText(/不是通用 API Key 接口/u)).toBeInTheDocument();
    expect(within(dialog).getByText(/尚未完成 Copilot SDK 登录/u)).toBeInTheDocument();
    expect(screen.queryByLabelText("API Key")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "知道了" }));
    expect(screen.queryByRole("dialog", { name: "GitHub Copilot 需要账户连接" }))
      .not.toBeInTheDocument();
    expect(copilot).toHaveFocus();
  });
});

describe("V2 permission settings hierarchy", () => {
  it("keeps Debug visible but makes task Full Access unavailable without a selected task", () => {
    const getThreadExecutionPermission = vi.fn();
    const client = { hasExecutionPermissionControl: true,
      getThreadExecutionPermission } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <V2Settings client={client} onOpenInspector={vi.fn()} onSelectSection={vi.fn()}
        section="permissions" threadID="" workspaces={[]} />
    </QueryClientProvider>);

    expect(screen.getByRole("heading", { name: "调试运行时" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "任务权限" })).toBeInTheDocument();
    expect(screen.getByText("先从侧栏打开一个对话。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /启用完全访问/u })).not.toBeInTheDocument();
    const debugGroup = screen.getByRole("group", { name: "调试运行时能力" });
    expect(within(debugGroup).getByText("调试模式")).toBeInTheDocument();
    expect(within(debugGroup).getByRole("button")).toBeVisible();
    expect(screen.getByRole("switch", { name: "完整 CDP 控制" })).toBeDisabled();
    expect(screen.getByText(/完全访问无需重启，但当前执行需暂停并处于静止边界后才能生效/u))
      .toBeInTheDocument();
    expect(getThreadExecutionPermission).not.toHaveBeenCalled();
  });
});
