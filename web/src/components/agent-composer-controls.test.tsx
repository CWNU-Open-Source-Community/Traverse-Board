import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { APIClient } from "../api/client";
import { AgentComposerControls } from "./agent-composer-controls";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

function modelClient(overrides: Partial<APIClient> = {}): APIClient {
  return {
    hasModelControl: true,
    modelAvailability: vi.fn().mockResolvedValue({
      protocol_version: "model_availability.v2",
      generation: 1,
      providers: [{ name: "mock", kind: "local", status: "available",
        models: ["mock-code", "mock-fast"], credential_source: "none",
        network_required: false, configuration_error: false,
        harnesses: ["mock-code", "mock-fast"].map((model) => ({
          protocol_version: "model_harness.v1", model, transport_protocol: "mock",
          tool_strategy: "native", json_strategy: "native",
          qualification_status: "trusted_builtin", tool_calls_qualified: true,
          tool_results_qualified: true, strict_json_qualified: true,
          streaming_qualified: true, root_eligible: true,
          structured_json_eligible: true, qualified_at: "", expires_at: "",
        })) }],
      routes: [{ name: "code", provider: "mock", model: "mock-code", available: true,
        harness_ready: true }],
    }),
    selectModelRoute: vi.fn().mockResolvedValue({
      name: "code", provider: "mock", model: "mock-fast", available: true,
      harness_ready: true,
    }),
    ...overrides,
  } as unknown as APIClient;
}

function renderControls(client: APIClient, props: Record<string, unknown> = {}) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  const view = render(<QueryClientProvider client={queryClient}>
    <AgentComposerControls client={client} route="code" {...props} />
  </QueryClientProvider>);
  return { ...view, queryClient };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("AgentComposerControls", () => {
  it("keeps model discovery lazy and exposes task modes, files, plugins, and context", async () => {
    const user = userEvent.setup();
    const client = modelClient();
    const onOpenFiles = vi.fn();
    const onOpenPlugins = vi.fn();
    const onPlanModeChange = vi.fn();
    const onTargetModeChange = vi.fn();
    renderControls(client, { contextTokens: 8192, onOpenFiles, onOpenPlugins,
      onPlanModeChange, onTargetModeChange });

    expect(client.modelAvailability).not.toHaveBeenCalled();
    expect(screen.getByRole("status", { name: "上下文已用 25%" })).toBeInTheDocument();
    expect(screen.getByText(/已加载约 8.2k 令牌/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "添加" }));
    await user.click(screen.getByRole("menuitem", { name: /文件和文件夹/ }));
    expect(onOpenFiles).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "添加" }));
    await user.click(screen.getByRole("menuitem", { name: /目标/ }));
    expect(onTargetModeChange).toHaveBeenCalledWith(true);

    await user.click(screen.getByRole("button", { name: "添加" }));
    await user.click(screen.getByRole("menuitem", { name: /计划模式/ }));
    expect(onPlanModeChange).toHaveBeenCalledWith(true);

    await user.click(screen.getByRole("button", { name: "添加" }));
    await user.click(screen.getByRole("menuitem", { name: /已安装插件/ }));
    expect(onOpenPlugins).toHaveBeenCalledTimes(1);
  });

  it("loads models on demand, persists a selected route, and does not fake reasoning support", async () => {
    const user = userEvent.setup();
    const client = modelClient();
    renderControls(client);

    await user.click(screen.getByRole("button", { name: "选择模型，当前 code" }));
    const fast = await screen.findByRole("menuitemradio", { name: /mock-fast/ });
    expect(client.modelAvailability).toHaveBeenCalledTimes(1);
    await user.click(fast);
    await waitFor(() => expect(client.selectModelRoute).toHaveBeenCalledWith("code", {
      version: "model_route_control.v1", provider: "mock", model: "mock-fast",
    }));

    await user.click(screen.getByRole("button", { name: "推理强度，当前标准" }));
    expect(screen.getByText("高").closest("button")).toBeDisabled();
    expect(screen.getByText("最高").closest("button")).toBeDisabled();
  });

  it("refreshes the selected model after an uncertain switch without repeating the write", async () => {
    const user = userEvent.setup();
    const client = modelClient({ selectModelRoute: vi.fn().mockRejectedValue(new Error("connection lost")) });
    renderControls(client);
    await user.click(screen.getByRole("button", { name: "选择模型，当前 code" }));
    await user.click(await screen.findByRole("menuitemradio", { name: /mock-fast/ }));
    const refresh = await screen.findByRole("menuitem", { name: "重新读取模型" });
    await user.click(refresh);
    await waitFor(() => expect(screen.queryByRole("menuitem", { name: "重新读取模型" })).not.toBeInTheDocument());
    expect(client.modelAvailability).toHaveBeenCalledTimes(2);
    expect(client.selectModelRoute).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "选择模型，当前 mock-code" })).toBeInTheDocument();
  });

  it("keeps refresh disabled while a model switch is pending after a failed read", async () => {
    const user = userEvent.setup();
    const write = deferred<Awaited<ReturnType<APIClient["selectModelRoute"]>>>();
    const client = modelClient({ selectModelRoute: vi.fn().mockReturnValue(write.promise) });
    const { queryClient } = renderControls(client);
    await user.click(screen.getByRole("button", { name: "选择模型，当前 code" }));
    const fast = await screen.findByRole("menuitemradio", { name: /mock-fast/ });

    vi.mocked(client.modelAvailability).mockRejectedValueOnce(new Error("read failed"));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ["models", "availability"] });
    });
    const refresh = await screen.findByRole("menuitem", { name: "重新读取模型" });
    await user.click(fast);
    await waitFor(() => expect(client.selectModelRoute).toHaveBeenCalledTimes(1));
    expect(refresh).toBeDisabled();
    expect(fast).toBeDisabled();
    await user.click(refresh);
    await user.click(screen.getByRole("menuitemradio", { name: /mock-code/ }));
    expect(client.modelAvailability).toHaveBeenCalledTimes(2);
    expect(client.selectModelRoute).toHaveBeenCalledTimes(1);

    await act(async () => write.resolve({
      name: "code", provider: "mock", model: "mock-fast", available: true, harness_ready: true,
    }));
    await waitFor(() => expect(screen.queryByRole("menu", { name: "选择模型" })).not.toBeInTheDocument());
  });

  it("waits for model readback before enabling another switch", async () => {
    const user = userEvent.setup();
    const read = deferred<Awaited<ReturnType<APIClient["modelAvailability"]>>>();
    const client = modelClient();
    vi.mocked(client.selectModelRoute).mockRejectedValueOnce(new Error("connection lost"));
    const { queryClient } = renderControls(client);
    await user.click(screen.getByRole("button", { name: "选择模型，当前 code" }));
    const fast = await screen.findByRole("menuitemradio", { name: /mock-fast/ });
    const modelData = queryClient.getQueryData<Awaited<ReturnType<APIClient["modelAvailability"]>>>(
      ["models", "availability"],
    )!;
    await user.click(fast);
    const refresh = await screen.findByRole("menuitem", { name: "重新读取模型" });
    vi.mocked(client.modelAvailability).mockReturnValueOnce(read.promise);
    await user.click(refresh);
    expect(refresh).toBeDisabled();
    expect(fast).toBeDisabled();
    await user.click(fast);
    expect(client.selectModelRoute).toHaveBeenCalledTimes(1);

    await act(async () => read.resolve(modelData));
    await waitFor(() => expect(fast).toBeEnabled());
    expect(screen.queryByRole("menuitem", { name: "重新读取模型" })).not.toBeInTheDocument();
    await user.click(fast);
    await waitFor(() => expect(client.selectModelRoute).toHaveBeenCalledTimes(2));
  });
});
