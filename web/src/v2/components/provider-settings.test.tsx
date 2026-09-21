import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import type { CyberAgentClient } from "../../api/client";
import type { ProviderDefinitionView } from "../../api/types";
import { V2ProviderSettings, validProviderEndpointURL, type V2ProviderDraftPreset } from "./provider-settings";

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
  const qualifyModelHarness = vi.fn().mockImplementation(async (body) => ({
    protocol_version: "model_harness_qualification.v1",
    provider: body.provider,
    model: body.model,
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
      model: body.model,
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
  }));
  const availableModelRoutes = vi.fn().mockImplementation(async () => ({
    protocol_version: "model_route_catalog.v1",
    generation: 3,
    routes: providers.flatMap((definition) => definition.models.map((model) => ({
      provider_id: definition.id, provider_name: definition.display_name, model,
	  definition_revision: definition.revision,
      enabled: definition.enabled, credential_status: "configured",
      qualification_status: "available", harness_ready: true, selectable: true,
      unavailable_reason: "", default_for_routes: [],
      vision_capability: { state: "unknown", source: "unknown" },
    }))),
  }));
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
      availableModelRoutes,
    } as unknown as CyberAgentClient,
    providerDefinitions,
    providerCredentialStatuses,
    changeProviderCredential,
    upsertProviderDefinition,
    deleteProviderDefinition,
    diagnoseProvider,
    qualifyModelHarness,
    availableModelRoutes,
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
  it.each(["remove", "rename"])("keeps image declarations aligned when models %s without transferring vision support", async (action) => {
    const controls = createClient([provider({ models: ["acme-pro", "acme-image"],
      advanced_config: { custom_extension: { keep: "unchanged" }, model_capabilities: {
        "acme-pro": { vision: "unknown" }, "acme-image": { vision: "supported" },
      } } })]);
    const user = userEvent.setup();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    fireEvent.change(screen.getByLabelText("模型列表"), { target: { value: action === "remove" ? "acme-pro" : "acme-pro\nacme-renamed" } });
    if (action === "rename") expect(screen.getByRole("combobox", { name: "acme-renamed 图片输入" })).toHaveValue("unknown");
    const expected = { custom_extension: { keep: "unchanged" }, model_capabilities: { "acme-pro": { vision: "unknown" } } };
    expect(JSON.parse((screen.getByRole("textbox", { name: "高级 JSON" }) as HTMLTextAreaElement).value)).toEqual(expected);
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition.advanced_config).toEqual(expected);
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });
  it.each([
    { model_capabilities: "handwritten-invalid" },
    { model_capabilities: { "acme-pro": { vision: "supported", future_detail: "do not discard" } } },
  ])("preserves invalid handwritten image configuration through model edits and blocks lossy controls", async (advancedConfig) => {
    const controls = createClient([provider({ advanced_config: advancedConfig })]);
    const user = userEvent.setup();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    const original = (screen.getByRole("textbox", { name: "高级 JSON" }) as HTMLTextAreaElement).value;
    expect(screen.getByRole("combobox", { name: "acme-pro 图片输入" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("模型列表"), { target: { value: "acme-renamed" } });
    await user.selectOptions(screen.getByLabelText("默认模型"), "acme-renamed");
    expect(screen.getByRole("textbox", { name: "高级 JSON" })).toHaveValue(original);
    expect(screen.getByRole("combobox", { name: "acme-renamed 图片输入" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("原内容已保留");
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it("accepts HTTPS and literal loopback HTTP model endpoints without URL normalization aliases", () => {
    for (const endpoint of ["https://api.example.com/v1/chat/completions",
      "http://127.0.0.1:18867/v1/chat/completions", "http://127.2.3.4:80/v1",
      "http://localhost:18867/v1", "http://LOCALHOST/v1", "http://[::1]:18867/v1",
      "http://[0:0:0:0:0:0:0:1]/v1", "http://[::ffff:127.0.0.1]:18867/v1"]) {
      expect(validProviderEndpointURL(endpoint), endpoint).toBe(true);
    }
    for (const endpoint of ["http://api.example.com/v1", "http://192.168.1.2/v1", "http://10.0.0.1/v1",
      "http://127.0.0.1.example.com/v1", "http://localhost.example.com/v1", "http://localhost./v1",
      "http://127.1/v1", "http://2130706433/v1", "http://0x7f000001/v1", "http://127.00.0.1/v1",
      "http://%6cocalhost/v1", "http://%31%32%37.0.0.1/v1", "http://127.0.0.1@evil.example/v1",
      "http://evil.example@localhost/v1", "http://127.0.0.1\\@evil.example/v1", "http://[::2]/v1",
      "http://[::ffff:192.168.1.2]/v1", "https://user:pass@api.example.com/v1", "https://@api.example.com/v1",
      "https://api.example.com/v1?key=secret", "http://localhost/v1#fragment", "http://localhost:/v1",
      "http://local\nhost/v1", "http:localhost/v1", "ftp://localhost/v1"]) {
      expect(validProviderEndpointURL(endpoint), endpoint).toBe(false);
    }
  });

  it("saves an exact local HTTP endpoint while keeping website links HTTPS-only", async () => {
    const controls = createClient();
    const user = userEvent.setup();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    await fillRequiredProvider(user);
    const endpoint = "http://127.0.0.1:18867/v1/chat/completions";
    fireEvent.change(screen.getByLabelText("请求地址"), { target: { value: endpoint } });
    fireEvent.change(screen.getByLabelText("官网链接"), { target: { value: "http://localhost/" } });
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("官网链接必须是 HTTPS URL");
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("官网链接"), { target: { value: "" } });
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition.endpoint_url).toBe(endpoint);
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it("diagnoses and qualifies a saved keyless local model without writing a credential", async () => {
    const controls = createClient([provider({ endpoint_url: "http://127.0.0.1:18867/v1/chat/completions",
      advanced_config: { operator_notes: { $credential: "unused-extension-data" } } })]);
    controls.providerCredentialStatuses.mockResolvedValue({ protocol_version: "provider_credential.v1", items: [] });
    const user = userEvent.setup();
    renderSettings(controls.client);
    const row = await screen.findByRole("button", { name: /Acme AI/u });
    expect(within(row).getByText("本机 · 可不填密钥")).toBeInTheDocument();
    await user.click(row);
    expect(screen.getByText(/本机模型未引用凭据，可留空并进行连接验证/u)).toBeInTheDocument();
    const verify = screen.getByRole("button", { name: "测试并验证 Harness" });
    expect(verify).toBeEnabled();
    await user.click(verify);
    const dialog = screen.getByRole("dialog", { name: "测试并验证 Harness？" });
    await user.click(within(dialog).getByRole("button", { name: "开始验证" }));
    await waitFor(() => expect(controls.qualifyModelHarness).toHaveBeenCalledTimes(1));
    expect(controls.diagnoseProvider).toHaveBeenCalledTimes(1);
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it.each([
    { endpoint_url: "https://api.example.com/v1", advanced_config: {} },
    { endpoint_url: "http://localhost:18867/v1", advanced_config: {
      request_headers: { Authorization: { $credential: "acme", template: "Bearer ${secret}" } },
    } },
    { endpoint_url: "http://[::1]:18867/v1", advanced_config: {
      request_body: { nested: [{ $credential: "acme" }] },
    } },
  ])("requires missing credentials for remote or credential-referencing models ($endpoint_url)", async (overrides) => {
    const controls = createClient([provider(overrides)]);
    controls.providerCredentialStatuses.mockResolvedValue({ protocol_version: "provider_credential.v1", items: [] });
    const user = userEvent.setup();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    expect(screen.getByRole("button", { name: "测试并验证 Harness" })).toBeDisabled();
    expect(screen.getByText(/请先把 API Key 保存到系统凭据管理器/u)).toBeInTheDocument();
    expect(controls.diagnoseProvider).not.toHaveBeenCalled();
    expect(controls.qualifyModelHarness).not.toHaveBeenCalled();
  });

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

  it("saves and qualifies a first Provider once before returning its exact model to the draft", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    const onReady = vi.fn();
    render(<StrictMode><QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset}
        prepareForDraft onReady={onReady} />
    </QueryClientProvider></StrictMode>);

    await screen.findByRole("heading", { name: "添加供应商" });
    await user.type(screen.getByLabelText("API Key"), "first-model-key-123456");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));

    await waitFor(() => expect(onReady).toHaveBeenCalledWith(expect.objectContaining({
      id: "official-openai", default_model: "gpt-5",
    })));
    expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1);
    expect(controls.changeProviderCredential).toHaveBeenCalledTimes(1);
    expect(controls.diagnoseProvider).not.toHaveBeenCalled();
    expect(controls.qualifyModelHarness).toHaveBeenCalledTimes(1);
    expect(controls.qualifyModelHarness).toHaveBeenCalledWith({
      version: "model_harness_qualification.v1", provider: "official-openai",
      model: "gpt-5", confirm_qualification: true,
    });
    expect(controls.availableModelRoutes).toHaveBeenCalledTimes(1);
  });

  it("completes the first Provider check under the desktop StrictMode root", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    const onReady = vi.fn();
    render(<StrictMode><QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset}
        prepareForDraft onReady={onReady} />
    </QueryClientProvider></StrictMode>);

    await screen.findByRole("heading", { name: "添加供应商" });
    await user.type(screen.getByLabelText("API Key"), "first-model-key-123456");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    await waitFor(() => expect(onReady).toHaveBeenCalledTimes(1));
    expect(controls.qualifyModelHarness).toHaveBeenCalledTimes(1);
  });

  it("retries only the model check after a saved first Provider check fails", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    controls.qualifyModelHarness.mockRejectedValueOnce(new Error("temporary Provider failure"));
    const onReady = vi.fn();
    render(<StrictMode><QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset}
        prepareForDraft onReady={onReady} />
    </QueryClientProvider></StrictMode>);

    await screen.findByRole("heading", { name: "添加供应商" });
    await user.type(screen.getByLabelText("API Key"), "first-model-key-123456");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("temporary Provider failure");
    await user.click(screen.getByRole("button", { name: "重新检查" }));

    await waitFor(() => expect(onReady).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1);
    expect(controls.changeProviderCredential).toHaveBeenCalledTimes(1);
    expect(controls.qualifyModelHarness).toHaveBeenCalledTimes(2);
  });

  it("keeps a saved Provider and reports billing capacity without automatic retry", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    const onReady = vi.fn();
    controls.qualifyModelHarness.mockResolvedValue({
      protocol_version: "model_harness_qualification.v1", provider: "official-openai", model: "gpt-5",
      status: "unreachable", outcome: "permanent", failure_reason: "capacity", retryable: false,
      network_request_attempted: true, model_calls: 1, synthetic_tool_calls: 0, tool_executed: false,
      response_content_returned: false, qualification_status: "capacity", duration_ms: 20,
      harness: { root_eligible: false },
    });
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset}
        prepareForDraft onReady={onReady} />
    </QueryClientProvider>);
    await screen.findByRole("heading", { name: "添加供应商" });
    await user.type(screen.getByLabelText("API Key"), "billing-test-key-123456");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("供应商额度或容量不足，请检查账单或服务状态");
    expect(screen.getByLabelText("供应商 ID")).toHaveValue("official-openai");
    expect(screen.getByRole("button", { name: "重新检查" })).toBeEnabled();
    expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1);
    expect(controls.changeProviderCredential).toHaveBeenCalledTimes(1);
    expect(controls.qualifyModelHarness).toHaveBeenCalledTimes(1);
    expect(controls.availableModelRoutes).not.toHaveBeenCalled();
    expect(onReady).not.toHaveBeenCalled();
  });

  it("locks the saved Provider form while the paid model check is in flight", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    let rejectCheck!: (reason?: unknown) => void;
    const pending = new Promise<never>((_resolve, reject) => { rejectCheck = reject; });
    controls.qualifyModelHarness.mockImplementationOnce(() => pending);
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset}
        prepareForDraft onReady={vi.fn()} />
    </QueryClientProvider>);

    await screen.findByRole("heading", { name: "添加供应商" });
    await user.type(screen.getByLabelText("API Key"), "first-model-key-123456");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    await waitFor(() => expect(controls.qualifyModelHarness).toHaveBeenCalledTimes(1));
    expect(screen.getByLabelText("显示名称")).toBeDisabled();
    expect(screen.getByRole("button", { name: "返回供应商列表" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "正在保存…" })).toBeDisabled();
    rejectCheck(new Error("bounded check stopped"));
    expect(await screen.findByRole("alert")).toHaveTextContent("bounded check stopped");
    expect(screen.getByLabelText("显示名称")).toBeEnabled();
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

  it("does not infer Provider-hosted search from Responses transport and retains an explicit choice", async () => {
    const user = userEvent.setup();
    const controls = createClient();
    renderSettings(controls.client);

    await user.click(await screen.findByRole("button", { name: "添加供应商" }));
    expect(screen.getByLabelText("搜索策略")).toHaveValue("auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();

    await user.selectOptions(screen.getByLabelText("协议"), "openai_responses");

    expect(screen.getByLabelText("搜索策略")).toHaveValue("auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();
    expect(screen.getByText(/兼容 Responses API 不代表支持原生搜索/u)).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("搜索策略"), "provider_native");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).toBeChecked();
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    expect(controls.diagnoseProvider).not.toHaveBeenCalled();
    expect(controls.qualifyModelHarness).not.toHaveBeenCalled();

    await user.selectOptions(screen.getByLabelText("协议"), "openai_chat_completions");
    expect(screen.getByLabelText("搜索策略")).toHaveValue("auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();
  });

  it.each([
    ["https://api.deepseek.com/responses", false],
    ["https://API.DEEPSEEK.COM./responses", false],
    ["https://api.deepseek.com.proxy.example/responses", true],
    ["https://gateway.example/responses", true],
  ] as const)("uses exact endpoint support for a new preset at %s", async (endpointURL, declared) => {
    const controls = createClient();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <V2ProviderSettings client={controls.client} initialPreset={{ ...openAIPreset,
        id: "official-deepseek", displayName: "DeepSeek", endpointURL }} />
    </QueryClientProvider>);
    await screen.findByRole("heading", { name: "添加供应商" });
    expect(screen.getByLabelText("搜索策略")).toHaveValue(declared ? "provider_native" : "auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search").getAttribute("type")).toBe("checkbox");
    if (declared) expect(screen.getByLabelText("声明供应商具备原生 Web Search")).toBeChecked();
    else expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();
    expect(screen.getByLabelText("请求地址")).toHaveValue(endpointURL);
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it("shows a stored official endpoint declaration without silently rewriting it from the preset", async () => {
    const existing = provider({ id: "official-deepseek", endpoint_url: "https://api.deepseek.com/responses",
      transport: "openai_responses", search_mode: "provider_native", native_web_search_capability: "declared_unverified" });
    const controls = createClient([existing]);
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <V2ProviderSettings client={controls.client} initialPreset={{ ...openAIPreset,
        id: "official-deepseek", endpointURL: existing.endpoint_url }} />
    </QueryClientProvider>);
    await screen.findByRole("heading", { name: "编辑供应商" });
    expect(screen.getByLabelText("搜索策略")).toHaveValue("provider_native");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).toBeChecked();
    expect(screen.getByText(/系统不会静默改用 DuckDuckGo/u)).toBeInTheDocument();
    expect(screen.getByLabelText("默认模型")).toHaveValue(existing.default_model);
    expect(screen.getByLabelText("API Key")).toHaveValue("");
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
  });

  it("saves independent DuckDuckGo search without a native declaration or credential changes", async () => {
    const controls = createClient([provider()]);
    const user = userEvent.setup();
    renderSettings(controls.client);
    await user.click(await screen.findByRole("button", { name: /Acme AI/u }));
    await user.selectOptions(screen.getByLabelText("搜索策略"), "web");
    expect(screen.getByRole("option", { name: "普通网页搜索（DuckDuckGo）" })).toBeInTheDocument();
    expect(screen.getByText(/搜索查询会发送到 DuckDuckGo/u)).toHaveTextContent("仍受当前任务的网页访问范围限制");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(controls.upsertProviderDefinition).toHaveBeenCalledTimes(1));
    expect(controls.upsertProviderDefinition.mock.calls[0][1].definition).toMatchObject({
      search_mode: "web", native_web_search_capability: "unsupported", models: ["acme-pro"],
      endpoint_url: "https://api.acme.example/v1/chat/completions",
    });
    expect(controls.changeProviderCredential).not.toHaveBeenCalled();
    expect(controls.diagnoseProvider).not.toHaveBeenCalled();
    expect(controls.qualifyModelHarness).not.toHaveBeenCalled();
  });

  it("clears an inherited native claim when the operator changes to the known unsupported endpoint", async () => {
    const controls = createClient();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <V2ProviderSettings client={controls.client} initialPreset={openAIPreset} />
    </QueryClientProvider>);
    await screen.findByRole("heading", { name: "添加供应商" });
    expect(screen.getByLabelText("搜索策略")).toHaveValue("provider_native");
    fireEvent.change(screen.getByLabelText("请求地址"), { target: { value: "https://api.deepseek.com/responses" } });
    expect(screen.getByLabelText("搜索策略")).toHaveValue("auto");
    expect(screen.getByLabelText("声明供应商具备原生 Web Search")).not.toBeChecked();
    expect(screen.getByRole("option", { name: "供应商原生" })).toBeDisabled();
    expect(screen.getByLabelText("默认模型")).toHaveValue("gpt-5");
    expect(controls.upsertProviderDefinition).not.toHaveBeenCalled();
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
