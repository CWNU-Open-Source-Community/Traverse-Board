import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { ProviderDefinitionView } from "../../api/types";
import { V2ProviderSettings, type V2ProviderDraftPreset } from "./provider-settings";

const openAIPreset: V2ProviderDraftPreset = {
  id: "official-openai",
  displayName: "OpenAI",
  note: "官方 API",
  websiteURL: "https://platform.openai.com",
  endpointURL: "https://api.openai.com/v1/responses",
  transport: "openai_responses",
  models: ["gpt-5", "gpt-5-mini"],
  defaultModel: "gpt-5",
  searchMode: "provider_native",
  nativeSearchDeclared: true,
  advancedConfig: { request_body: { store: false } },
};

function provider(overrides: Partial<ProviderDefinitionView> = {}): ProviderDefinitionView {
  return {
    version: "provider_definition.v1",
    id: "acme",
    display_name: "Acme AI",
    note: "团队账号",
    website_url: "https://acme.example",
    endpoint_url: "https://api.acme.example/v1/chat/completions",
    default_model: "acme-pro",
    models: ["acme-pro"],
    transport: "openai_chat_completions",
    search_mode: "auto",
    native_web_search_capability: "unsupported",
    enabled: true,
    revision: 4,
    advanced_config: {},
    ...overrides,
  };
}

function createClient(providers: ProviderDefinitionView[] = []) {
  let revision = providers.length ? 7 : 0;
  const providerDefinitions = vi.fn().mockImplementation(async () => ({
    version: "provider_definition_collection.v1", revision, providers,
  }));
  const providerCredentialStatuses = vi.fn().mockResolvedValue({
    protocol_version: "provider_credential.v1",
    items: providers.map((definition) => ({
      protocol_version: "provider_credential.v1", provider: definition.id,
      configured: true, store_available: true, store_kind: "windows_credential_manager",
      plaintext_returned: false, restart_required: false, registry_reloaded: false,
      registry_generation: 2,
    })),
  });
  const changeProviderCredential = vi.fn().mockImplementation(async (id, body) => ({
    protocol_version: "provider_credential.v1", provider: id,
    configured: body.action === "set", store_available: true,
    store_kind: "windows_credential_manager", plaintext_returned: false,
    restart_required: false, registry_reloaded: true, registry_generation: 3,
  }));
  const upsertProviderDefinition = vi.fn().mockImplementation(async (_id, body) => {
    revision += 1;
    const saved = { ...body.definition, revision: body.definition.revision + 1 };
    providers = [...providers.filter((item) => item.id !== saved.id), saved]
      .sort((left, right) => left.id.localeCompare(right.id));
    return {
      protocol_version: "provider_definition_control.v1", registry_reloaded: true,
      registry_generation: 3, definition: saved,
      collection: { version: "provider_definition_collection.v1", revision, providers },
    };
  });
  const deleteProviderDefinition = vi.fn().mockImplementation(async (id) => {
    revision += 1;
    providers = providers.filter((item) => item.id !== id);
    return {
      protocol_version: "provider_definition_control.v1", registry_reloaded: true,
      registry_generation: 3, deleted_id: id,
      collection: { version: "provider_definition_collection.v1", revision, providers },
    };
  });
  const diagnoseProvider = vi.fn().mockResolvedValue({
    protocol_version: "provider_diagnostic.v1",
    provider: "acme",
    model: "acme-pro",
    status: "reachable",
    outcome: "success",
    failure_reason: "none",
    retryable: false,
    network_request_attempted: true,
    model_called: true,
    tool_called: false,
    response_content_returned: false,
    duration_ms: 12,
    qualification_status: "available",
  });
  const qualifyModelHarness = vi.fn().mockResolvedValue({
    protocol_version: "model_harness_qualification.v1",
    provider: "acme",
    model: "acme-pro",
    status: "qualified",
    outcome: "success",
    failure_reason: "none",
    retryable: false,
    network_request_attempted: true,
    model_calls: 2,
    synthetic_tool_calls: 1,
    tool_executed: false,
    response_content_returned: false,
    duration_ms: 25,
    qualification_status: "available",
    harness: {
      protocol_version: "model_harness.v1",
      model: "acme-pro",
      transport_protocol: "openai_chat_completions",
      tool_strategy: "native",
      json_strategy: "native",
      qualification_status: "verified",
      latest_qualification_status: "available",
      qualification_checked_at: "2026-08-31T08:00:00Z",
      qualification_source: "harness_qualification",
      tool_calls_qualified: true,
      tool_results_qualified: true,
      strict_json_qualified: true,
      streaming_qualified: true,
      root_eligible: true,
      structured_json_eligible: true,
      qualified_at: "2026-08-31T08:00:00Z",
      expires_at: "2026-09-07T08:00:00Z",
    },
  });
  return {
    client: {
      hasModelControl: true,
      hasProviderDefinitions: true,
      hasProviderCredentials: true,
      providerDefinitions,
      providerCredentialStatuses,
      changeProviderCredential,
      upsertProviderDefinition,
      deleteProviderDefinition,
      diagnoseProvider,
      qualifyModelHarness,
    } as unknown as CyberAgentClient,
    providerDefinitions,
    providerCredentialStatuses,
    changeProviderCredential,
    upsertProviderDefinition,
    deleteProviderDefinition,
    diagnoseProvider,
    qualifyModelHarness,
  };
}

function renderSettings(client: CyberAgentClient) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  return render(<QueryClientProvider client={queryClient}>
    <V2ProviderSettings client={client} />
  </QueryClientProvider>);
}

async function fillRequiredProvider(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("供应商 ID"), "nova");
  await user.type(screen.getByLabelText("显示名称"), "Nova Cloud");
  await user.type(screen.getByLabelText("请求地址"), "https://api.nova.example/v1/chat/completions");
  await user.type(screen.getByLabelText("模型列表"), "nova-pro");
}

describe("V2 custom Provider settings", () => {
  it("opens a new preset only after definitions load and does not overwrite operator edits", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    const onExit = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } });
    const view = render(<QueryClientProvider client={queryClient}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset} onExit={onExit} />
    </QueryClientProvider>);

    expect(screen.getByRole("heading", { name: "连接 OpenAI" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "添加供应商" })).toBeInTheDocument();
    expect(screen.getByLabelText("供应商 ID")).toHaveValue("official-openai");
    expect(screen.getByLabelText("请求地址")).toHaveValue("https://api.openai.com/v1/responses");
    expect(screen.getByLabelText("协议")).toHaveValue("openai_responses");
    expect(screen.getByLabelText("默认模型")).toHaveValue("gpt-5");
    expect(screen.getByRole("textbox", { name: "高级 JSON" })).toHaveValue(
      JSON.stringify({ request_body: { store: false } }, null, 2),
    );

    await user.clear(screen.getByLabelText("显示名称"));
    await user.type(screen.getByLabelText("显示名称"), "我的 OpenAI");
    const changedPreset = { ...openAIPreset, displayName: "不应覆盖用户输入" };
    view.rerender(<QueryClientProvider client={queryClient}>
      <V2ProviderSettings client={controls.client} initialPreset={changedPreset} onExit={onExit} />
    </QueryClientProvider>);
    expect(screen.getByLabelText("显示名称")).toHaveValue("我的 OpenAI");

    await user.click(screen.getByRole("button", { name: "取消" }));
    expect(onExit).toHaveBeenCalledTimes(1);
  });

  it("edits the stored definition instead of replacing it with a same-ID preset", async () => {
    const user = userEvent.setup();
    const existing = provider({
      id: "official-openai",
      display_name: "团队 OpenAI",
      endpoint_url: "https://gateway.example/v1/responses",
      transport: "openai_responses",
      default_model: "team-model",
      models: ["team-model"],
    });
    const controls = createClient([existing]);
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset} />
    </QueryClientProvider>);

    expect(await screen.findByRole("heading", { name: "编辑供应商" })).toBeInTheDocument();
    expect(screen.getByLabelText("供应商 ID")).toBeDisabled();
    expect(screen.getByLabelText("显示名称")).toHaveValue("团队 OpenAI");
    expect(screen.getByLabelText("请求地址")).toHaveValue("https://gateway.example/v1/responses");
    expect(screen.getByLabelText("默认模型")).toHaveValue("team-model");
    await user.click(screen.getByRole("button", { name: "返回供应商列表" }));
    expect(await screen.findByRole("button", { name: /团队 OpenAI/u })).toBeInTheDocument();
  });

  it("keeps a preset API key in the OS credential path and exits only after a successful save", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    const onExit = vi.fn();
    const onSaved = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset}
        onExit={onExit} onSaved={onSaved} />
    </QueryClientProvider>);

    await screen.findByRole("heading", { name: "添加供应商" });
    await user.type(screen.getByLabelText("API Key"), "preset-one-time-key-123456");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(controls.changeProviderCredential).toHaveBeenCalledWith(
      "official-openai", {
        version: "provider_credential.v1", action: "set", confirm: true,
        secret: "preset-one-time-key-123456",
      },
    ));
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(onSaved.mock.calls[0][0].advanced_config).toEqual({
      request_body: { store: false },
      request_headers: {
        Authorization: { $credential: "official-openai", template: "Bearer ${secret}" },
      },
    });
    expect(JSON.stringify(controls.upsertProviderDefinition.mock.calls[0][1].definition))
      .not.toContain("preset-one-time-key-123456");
    expect(onExit).toHaveBeenCalledTimes(1);
  });

  it("lists providers without redisplaying their stored API key and inserts a credential reference", async () => {
    const user = userEvent.setup();
    const controls = createClient([provider()]);
    renderSettings(controls.client);

    const row = await screen.findByRole("button", { name: /Acme AI/u });
    expect(within(row).getByText("密钥已存储")).toBeInTheDocument();
    await user.click(row);

    const keyInput = screen.getByLabelText("API Key");
    expect(keyInput).toHaveValue("");
    expect(keyInput).toHaveAttribute("type", "password");
    expect(keyInput).toHaveAttribute("placeholder", "留空以保留现有密钥");
    await user.click(screen.getByRole("button", { name: "插入凭据引用" }));
    expect(screen.getByRole("textbox", { name: "高级 JSON" })).toHaveValue(
      JSON.stringify({ request_headers: {
        Authorization: { $credential: "acme", template: "Bearer ${secret}" },
      } }, null, 2),
    );
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it("keeps Harness verification disabled until the exact provider configuration is saved", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);

    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    const verifyButton = screen.getByRole("button", { name: "测试并验证 Harness" });
    expect(verifyButton).toBeDisabled();
    expect(screen.getByText("先保存供应商配置后才能验证。")).toBeInTheDocument();
    expect(controls.diagnoseProvider).not.toHaveBeenCalled();
    expect(controls.qualifyModelHarness).not.toHaveBeenCalled();
  });

  it("enables qualified Provider-hosted search when the operator selects Responses", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);

    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    expect(screen.getByLabelText("搜索策略")).toHaveValue("auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();

    await user.selectOptions(screen.getByLabelText("协议"), "openai_responses");

    expect(screen.getByLabelText("搜索策略")).toHaveValue("provider_native");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).toBeChecked();
    expect(screen.getByText(/首次真实搜索由 Go 做有界资格验证/u)).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("协议"), "openai_chat_completions");
    expect(screen.getByLabelText("搜索策略")).toHaveValue("auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();
  });

  it("requires billing confirmation, diagnoses first, then displays Harness qualification expiry", async () => {
    const user = userEvent.setup();
    const controls = createClient([provider()]);
    renderSettings(controls.client);

    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    const verifyButton = screen.getByRole("button", { name: "测试并验证 Harness" });
    expect(verifyButton).toBeEnabled();
    await user.click(verifyButton);

    const dialog = screen.getByRole("dialog", { name: "测试并验证 Harness？" });
    expect(dialog).toHaveTextContent("通常还需要两次模型调用");
    expect(dialog).toHaveTextContent("可能按这些调用计费");
    expect(controls.diagnoseProvider).not.toHaveBeenCalled();
    expect(controls.qualifyModelHarness).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "开始验证" }));
    await waitFor(() => expect(controls.diagnoseProvider).toHaveBeenCalledWith({
      version: "provider_diagnostic.v1",
      provider: "acme",
      model: "acme-pro",
      confirm_diagnostic: true,
    }));
    await waitFor(() => expect(controls.qualifyModelHarness).toHaveBeenCalledWith({
      version: "model_harness_qualification.v1",
      provider: "acme",
      model: "acme-pro",
      confirm_qualification: true,
    }));
    expect(controls.diagnoseProvider.mock.invocationCallOrder[0])
      .toBeLessThan(controls.qualifyModelHarness.mock.invocationCallOrder[0]);

    const result = await screen.findByRole("status", { name: "Harness 验证结果" });
    expect(result).toHaveTextContent("验证通过");
    expect(result).toHaveTextContent("2 次");
    expect(result).toHaveTextContent("有效期至");
    expect(within(result).queryByText("未记录")).not.toBeInTheDocument();

    await user.clear(screen.getByLabelText("显示名称"));
    await user.type(screen.getByLabelText("显示名称"), "Acme Team");
    expect(screen.getByRole("button", { name: "测试并验证 Harness" })).toBeDisabled();
    expect(screen.getByText(/当前更改尚未保存/u)).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Harness 验证结果" })).not.toBeInTheDocument();
  });

  it("requires a second confirmation, migrates exactly one plaintext secret, and never sends it in the definition", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    const plaintextSecret = "sk-live-super-secret-123456";
    fireEvent.change(screen.getByRole("textbox", { name: "高级 JSON" }), { target: { value:
      JSON.stringify({ request_headers: { Authorization: `Bearer ${plaintextSecret}` } }),
    } });

    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "迁移明文密钥？" });
    expect(within(dialog).getByText(/系统凭据管理器/u)).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(plaintextSecret);

    await user.click(within(dialog).getByRole("button", { name: "迁移并保存" }));
    await waitFor(() => expect(controls.changeProviderCredential).toHaveBeenCalledWith("nova", {
      version: "provider_credential.v1", action: "set", confirm: true,
      secret: plaintextSecret,
    }));
    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.invocationCallOrder[0])
      .toBeLessThan(controls.changeProviderCredential.mock.invocationCallOrder[0]);
    const definition = controls.upsertProviderDefinition.mock.calls[0][1].definition;
    expect(JSON.stringify(definition)).not.toContain(plaintextSecret);
    expect(definition.advanced_config).toEqual({
      request_headers: { Authorization: { $credential: "nova", template: "Bearer ${secret}" } },
    });
    expect(screen.queryByLabelText("API Key")).not.toBeInTheDocument();
    expect(await screen.findByText("Nova Cloud")).toBeInTheDocument();
  });

  it("blocks multi-secret JSON and keeps destructive deletion keyboard-dismissible", async () => {
    const user = userEvent.setup();
    const controls = createClient([provider()]);
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));

    fireEvent.change(screen.getByRole("textbox", { name: "高级 JSON" }), { target: { value:
      JSON.stringify({ api_key: "first-secret-value", password: "second-secret-value" }),
    } });
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("多个不同的明文密钥");
    expect(screen.queryByRole("dialog", { name: "迁移明文密钥？" })).not.toBeInTheDocument();
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();

    const deleteButton = screen.getByRole("button", { name: "删除供应商" });
    await user.click(deleteButton);
    expect(screen.getByRole("dialog", { name: "删除 Acme AI" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "删除 Acme AI" })).not.toBeInTheDocument();
    expect(deleteButton).toHaveFocus();
    expect(controls.deleteProviderDefinition).not.toHaveBeenCalled();
  });

  it("rejects duplicate advanced JSON keys before JSON.parse can collapse them", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    fireEvent.change(screen.getByRole("textbox", { name: "高级 JSON" }), {
      target: { value: '{"region":"one","r\\u0065gion":"two"}' },
    });
    await user.click(screen.getByRole("button", { name: "保存" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("重复字段“region”");
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it("stores one-time API keys separately and clears the editor after save", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    await user.type(screen.getByLabelText("API Key"), "one-time-key-123456");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(controls.changeProviderCredential).toHaveBeenCalledWith("nova", {
      version: "provider_credential.v1", action: "set", confirm: true,
      secret: "one-time-key-123456",
    }));
    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.invocationCallOrder[0])
      .toBeLessThan(controls.changeProviderCredential.mock.invocationCallOrder[0]);
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition)
      .not.toHaveProperty("api_key");
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition.advanced_config)
      .toEqual({ request_headers: {
        Authorization: { $credential: "nova", template: "Bearer ${secret}" },
      } });
    expect(screen.queryByLabelText("API Key")).not.toBeInTheDocument();
  });

  it("lets the operator disable JSON reference synchronization", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    await user.click(screen.getByRole("checkbox", { name: /把凭据引用同步到高级 JSON/u }));
    await user.type(screen.getByLabelText("API Key"), "one-time-key-123456");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(controls.changeProviderCredential).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition.advanced_config)
      .toEqual({});
  });

  it("synchronizes Anthropic credentials as x-api-key references", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    await user.selectOptions(screen.getByLabelText("协议"), "anthropic_messages");
    await user.type(screen.getByLabelText("API Key"), "anthropic-key-123456");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition.advanced_config)
      .toEqual({ request_headers: {
        "x-api-key": { $credential: "nova" },
      } });
  });

  it("keeps a newly registered definition visible when its first credential write fails", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    controls.changeProviderCredential.mockRejectedValueOnce(new Error("credential store unavailable"));
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    await user.type(screen.getByLabelText("API Key"), "one-time-key-123456");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(controls.changeProviderCredential).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.invocationCallOrder[0])
      .toBeLessThan(controls.changeProviderCredential.mock.invocationCallOrder[0]);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "供应商定义已保存，但 API Key 未写入系统凭据",
    );
    expect(screen.getByRole("heading", { name: "编辑供应商" })).toBeInTheDocument();
    expect(screen.getByLabelText("供应商 ID")).toBeDisabled();
    expect(screen.getByLabelText("API Key")).toHaveValue("");
    expect(controls.deleteProviderDefinition).not.toHaveBeenCalled();
  });

  it("removes a configured system credential before deleting its Provider definition", async () => {
    const user = userEvent.setup();
    const controls = createClient([provider()]);
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    await user.click(screen.getByRole("button", { name: "删除供应商" }));
    const dialog = screen.getByRole("dialog", { name: "删除 Acme AI" });
    expect(within(dialog).getByText(/系统凭据会先/u)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "删除" }));

    await waitFor(() => expect(controls.changeProviderCredential).toHaveBeenCalledWith("acme", {
      version: "provider_credential.v1", action: "delete", confirm: true, secret: "",
    }));
    await waitFor(() => expect(controls.deleteProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.changeProviderCredential.mock.invocationCallOrder[0])
      .toBeLessThan(controls.deleteProviderDefinition.mock.invocationCallOrder[0]);
  });

  it("keeps the Provider definition when credential deletion fails", async () => {
    const user = userEvent.setup();
    const controls = createClient([provider()]);
    controls.changeProviderCredential.mockRejectedValueOnce(new Error("系统凭据仍被占用"));
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    await user.click(screen.getByRole("button", { name: "删除供应商" }));
    await user.click(within(screen.getByRole("dialog", { name: "删除 Acme AI" }))
      .getByRole("button", { name: "删除" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("系统凭据仍被占用");
    expect(controls.deleteProviderDefinition).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "编辑供应商" })).toBeInTheDocument();
  });
});
