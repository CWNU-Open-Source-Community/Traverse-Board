import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import { V2ModelRouteControl, type V2ModelRouteCatalog, type V2ThreadModelRoute } from "./model-route-control";

const routeStyles = readFileSync("src/v2/styles.css", "utf8");

const catalog: V2ModelRouteCatalog = {
  protocol_version: "model_route_catalog.v1",
  generation: 7,
  routes: [
    { provider_id: "official-deepseek", provider_name: "DeepSeek", model: "deepseek-v4-flash",
      enabled: true, credential_status: "configured", qualification_status: "verified",
      harness_ready: true, selectable: true, unavailable_reason: "", default_for_routes: ["code"] },
    { provider_id: "official-deepseek", provider_name: "DeepSeek", model: "deepseek-v4-pro",
      enabled: true, credential_status: "configured", qualification_status: "verified",
      harness_ready: true, selectable: true, unavailable_reason: "", default_for_routes: [] },
    { provider_id: "official-openai", provider_name: "OpenAI", model: "gpt-5.6-terra",
      enabled: true, credential_status: "not_configured", qualification_status: "qualification_required",
      harness_ready: false, selectable: false, unavailable_reason: "credential_not_configured",
      default_for_routes: [] },
  ],
};

const current: V2ThreadModelRoute = {
  protocol_version: "thread_model_route.v1",
  thread_id: "thread-1",
  provider: "official-deepseek",
  model: "deepseek-v4-flash",
  source: "thread_preference",
  applies_to: "next_run",
  active_run_unchanged: true,
  replayed: false,
};

type TestRouteClient = CyberAgentClient & {
  availableModelRoutes: ReturnType<typeof vi.fn>;
  threadModelRoute: ReturnType<typeof vi.fn>;
  selectThreadModelRoute: ReturnType<typeof vi.fn>;
};

function routeClient(overrides: Record<string, unknown> = {}): TestRouteClient {
  return {
    hasModelControl: true,
    availableModelRoutes: vi.fn().mockResolvedValue(catalog),
    threadModelRoute: vi.fn().mockResolvedValue(current),
    selectThreadModelRoute: vi.fn().mockImplementation(async (_threadID: string, body: {
      action: "select" | "reset"; provider?: string; model?: string;
    }) => ({ ...current, provider: body.provider ?? "official-deepseek",
      model: body.model ?? "deepseek-v4-flash", source: body.action === "reset" ? "default" : "thread_preference",
      active_run_unchanged: true })),
    ...overrides,
  } as unknown as TestRouteClient;
}

function renderControl(client: TestRouteClient = routeClient(), options: {
  runActive?: boolean; onManageModels?: () => void;
} = {}) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  const onManageModels = options.onManageModels ?? vi.fn();
  render(<QueryClientProvider client={queryClient}>
    <V2ModelRouteControl client={client} onManageModels={onManageModels}
      runActive={options.runActive} threadID="thread-1" />
  </QueryClientProvider>);
  return { client, onManageModels };
}

describe("V2ModelRouteControl", () => {
  it("keeps only the model basename on the toolbar while retaining the full route and reasoning detail", async () => {
    const user = userEvent.setup();
    const client = routeClient({ threadModelRoute: vi.fn().mockResolvedValue({ ...current,
      provider: "custom-provider", model: "organization/model-release-2026" }) });
    renderControl(client);
    const trigger = await screen.findByRole("button", {
      name: "模型路由，当前 custom-provider · organization/model-release-2026；推理强度：随模型",
    });
    expect(trigger).toHaveTextContent(/^model-release-2026$/u);
    expect(trigger.querySelector(".lucide-zap")).toBeNull();
    expect(trigger).toHaveAttribute("title", "custom-provider · organization/model-release-2026；推理强度：随模型");
    expect(client.availableModelRoutes).not.toHaveBeenCalled();
    await user.click(trigger);
    const identity = screen.getByLabelText("当前模型完整路由");
    expect(within(identity).getByText("供应商")).toBeVisible();
    expect(within(identity).getByText("custom-provider")).toBeVisible();
    expect(within(identity).getByText("模型 ID")).toBeVisible();
    expect(within(identity).getByText("organization/model-release-2026")).toBeVisible();
    expect(screen.getByRole("menuitem", { name: /^模型/u })).toHaveTextContent("model-release-2026");
    expect(screen.getByRole("menuitem", { name: /^推理强度/u })).toBeDisabled();
    expect(client.selectThreadModelRoute).not.toHaveBeenCalled();
  });

  it.each([
    ["deepseek-v4-flash", "DeepSeek V4 Flash"],
    ["claude-sonnet-4-5-20250929", "Claude Sonnet 4.5"],
    ["gpt-5.6-terra", "GPT-5.6 Terra"],
    ["claude-sonnet-4-5-20250929-thinking", "Claude Sonnet 4.5 Thinking"],
    ["deepseek-v4-coder-thinking", "DeepSeek V4 Coder Thinking"],
    ["claude-sonnet-4-5-20250230-thinking", "Claude Sonnet 4.5 20250230 Thinking"],
  ])("shows a readable %s while keeping the exact full route visible and unchanged", async (model, friendly) => {
    const rawModel = `team/${model}`;
    const client = routeClient({ threadModelRoute: vi.fn().mockResolvedValue({ ...current,
      provider: "custom-provider", model: rawModel }) });
    renderControl(client); const user = userEvent.setup();
    const description = `custom-provider · ${rawModel}；推理强度：随模型`;
    const trigger = await screen.findByRole("button", { name: `模型路由，当前 ${description}` });
    expect(trigger).toHaveTextContent(friendly);
    expect(trigger).toHaveAttribute("title", description);
    await user.click(trigger);
    expect(within(screen.getByLabelText("当前模型完整路由")).getByText(rawModel)).toBeVisible();
    expect(screen.getByRole("menuitem", { name: /^模型/u })).toHaveTextContent(friendly);
    expect(client.selectThreadModelRoute).not.toHaveBeenCalled();
  });

  it("does not steal focus from typing after a pending model selection closes its menu", async () => {
    const frames = new Map<number, FrameRequestCallback>();
    let nextFrame = 0;
    vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation((callback) => {
      frames.set(++nextFrame, callback);
      return nextFrame;
    });
    vi.spyOn(globalThis, "cancelAnimationFrame").mockImplementation((id) => { frames.delete(id); });
    const flushFrames = () => {
      const pending = [...frames.values()];
      frames.clear();
      for (const callback of pending) callback(performance.now());
    };
    const client = routeClient();
    const onPendingRouteChange = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const user = userEvent.setup();
    render(<QueryClientProvider client={queryClient}>
      <V2ModelRouteControl client={client} threadID="" onManageModels={vi.fn()}
        onPendingRouteChange={onPendingRouteChange} />
      <textarea aria-label="首条消息" />
    </QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: /模型路由/ }));
    act(flushFrames);
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    act(flushFrames);
    await user.click(await screen.findByRole("menuitemradio", { name: /deepseek-v4-pro/ }));
    expect(onPendingRouteChange).toHaveBeenCalledWith({ provider: "official-deepseek", model: "deepseek-v4-pro" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    const composer = screen.getByRole("textbox", { name: "首条消息" });
    await user.click(composer);
    act(flushFrames);
    await user.keyboard("first message");
    expect(composer).toHaveFocus();
    expect(composer).toHaveValue("first message");
    expect(client.selectThreadModelRoute).not.toHaveBeenCalled();
  });

  it("keeps a newer composer focus when an in-flight model change finishes", async () => {
    let resolve!: (value: V2ThreadModelRoute) => void;
    const pending = new Promise<V2ThreadModelRoute>((done) => { resolve = done; });
    const client = routeClient({ selectThreadModelRoute: vi.fn(() => pending) });
    const frames = new Map<number, FrameRequestCallback>();
    let nextFrame = 0;
    vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation((callback) => {
      frames.set(++nextFrame, callback);
      return nextFrame;
    });
    vi.spyOn(globalThis, "cancelAnimationFrame").mockImplementation((id) => { frames.delete(id); });
    const flushFrames = () => {
      const callbacks = [...frames.values()];
      frames.clear();
      for (const callback of callbacks) callback(performance.now());
    };
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const user = userEvent.setup();
    render(<QueryClientProvider client={queryClient}>
      <V2ModelRouteControl client={client} threadID="thread-1" onManageModels={vi.fn()} />
      <textarea aria-label="下一条消息" />
    </QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: /模型路由/ }));
    act(flushFrames);
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    act(flushFrames);
    await user.click(await screen.findByRole("menuitemradio", { name: /deepseek-v4-pro/ }));
    const composer = screen.getByRole("textbox", { name: "下一条消息" });
    await user.click(composer);
    await act(async () => { resolve({ ...current, model: "deepseek-v4-pro" }); await pending; });
    await waitFor(() => expect(screen.getByRole("button", { name: /模型路由/ })).toHaveTextContent("DeepSeek V4 Pro"));
    act(flushFrames);
    await user.keyboard("keep this draft");
    expect(composer).toHaveFocus();
    expect(composer).toHaveValue("keep this draft");
    expect(client.selectThreadModelRoute).toHaveBeenCalledTimes(1);
  });

  it("returns focus after the selected menu item loses focus while disabled by its pending request", async () => {
    let resolve!: (value: V2ThreadModelRoute) => void;
    const pending = new Promise<V2ThreadModelRoute>((done) => { resolve = done; });
    const client = routeClient({ selectThreadModelRoute: vi.fn(() => pending) });
    const user = userEvent.setup();
    renderControl(client);
    const trigger = await screen.findByRole("button", { name: /模型路由/ });
    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    const selected = await screen.findByRole("menuitemradio", { name: /deepseek-v4-pro/ });
    await user.click(selected);
    await waitFor(() => expect(selected).toBeDisabled());
    // Browsers may blur a focused button when it becomes disabled. Keep the
    // real pending mutation and emulate that focus event, without a timer.
    // jsdom keeps disabled controls focused even after blur(), so explicitly
    // reproduce the browser's body-focus state while the button stays disabled.
    document.body.tabIndex = -1;
    document.body.focus();
    document.body.removeAttribute("tabindex");
    expect(document.body).toHaveFocus();
    await act(async () => { resolve({ ...current, model: "deepseek-v4-pro" }); await pending; });
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(trigger).toHaveTextContent("DeepSeek V4 Pro");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(client.selectThreadModelRoute).toHaveBeenCalledTimes(1);
  });

  it("opens an anchored two-level menu and groups selectable and unavailable routes", async () => {
    const user = userEvent.setup();
    const controls = renderControl();
    const trigger = await screen.findByRole("button", {
      name: "模型路由，当前 DeepSeek · deepseek-v4-flash；推理强度：随模型",
    });

    expect(controls.client.availableModelRoutes).not.toHaveBeenCalled();
    await user.click(trigger);
    const settings = screen.getByRole("menu", { name: "模型与响应设置" });
    expect(within(screen.getByLabelText("当前模型完整路由")).getByText("DeepSeek（official-deepseek）")).toBeVisible();
    expect(within(settings).getByRole("menuitem", { name: /推理强度/ })).toBeDisabled();
    expect(within(settings).getByRole("menuitem", { name: /速度/ })).toBeDisabled();

    const modelRow = within(settings).getByRole("menuitem", { name: /^模型/ });
    modelRow.focus();
    await user.keyboard("{ArrowRight}");
    const routes = await screen.findByRole("menu", { name: "选择模型路由" });
    expect(controls.client.availableModelRoutes).toHaveBeenCalledTimes(1);
    expect(within(routes).getByRole("region", { name: "DeepSeek" })).toBeInTheDocument();
    const unavailable = within(routes).getByRole("menuitemradio", { name: /gpt-5.6-terra/ });
    expect(unavailable).toBeDisabled();
    expect(unavailable).toHaveAttribute("title", "API Key 尚未配置");
    expect(within(routes).getByRole("menuitemradio", { name: /deepseek-v4-flash/ }))
      .toHaveAttribute("aria-checked", "true");
  });

  it("selects a provider plus model for the next Run without mutating the active Run", async () => {
    const user = userEvent.setup();
    const controls = renderControl(routeClient(), { runActive: true });
    const trigger = await screen.findByRole("button", { name: /模型路由/ });
    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    await user.click(await screen.findByRole("menuitemradio", { name: /deepseek-v4-pro/ }));

    await waitFor(() => expect(controls.client.selectThreadModelRoute).toHaveBeenCalledWith(
      "thread-1", expect.objectContaining({
        version: "thread_model_route_control.v1",
        action: "select",
        provider: "official-deepseek",
        model: "deepseek-v4-pro",
        requested_by: "desktop-ui",
      }),
    ));
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(trigger).toHaveTextContent("DeepSeek V4 Pro下一轮");
    expect(trigger).not.toHaveTextContent("DeepSeek ·");
    expect(trigger).toHaveAttribute("title", "DeepSeek · deepseek-v4-pro；推理强度：随模型");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("resets the thread override through the explicit reset action", async () => {
    const user = userEvent.setup();
    const controls = renderControl();
    await user.click(await screen.findByRole("button", { name: /模型路由/ }));
    await user.click(screen.getByRole("menuitem", { name: "重置为默认设置" }));

    await waitFor(() => expect(controls.client.selectThreadModelRoute).toHaveBeenCalledWith(
      "thread-1", expect.objectContaining({ action: "reset", requested_by: "desktop-ui" }),
    ));
    const body = vi.mocked(controls.client.selectThreadModelRoute).mock.calls[0]?.[1] as Record<string, unknown>;
    expect(body).not.toHaveProperty("provider");
    expect(body).not.toHaveProperty("model");
  });

  it("shows the backend's safe selection error instead of hiding the cause", async () => {
    const user = userEvent.setup();
    const client = routeClient({
      selectThreadModelRoute: vi.fn().mockRejectedValue(
        new Error("Harness 尚未完成该模型的能力验证"),
      ),
    });
    renderControl(client);
    await user.click(await screen.findByRole("button", { name: /模型路由/ }));
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    await user.click(await screen.findByRole("menuitemradio", { name: /deepseek-v4-pro/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Harness 尚未完成该模型的能力验证");
  });

  it("closes on Escape, restores focus, and keeps management in the model settings module", async () => {
    const user = userEvent.setup();
    const onManageModels = vi.fn();
    renderControl(routeClient(), { onManageModels });
    const trigger = await screen.findByRole("button", { name: /模型路由/ });

    await user.click(trigger);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());

    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    await user.click(await screen.findByRole("menuitem", { name: "管理模型供应商…" }));
    expect(onManageModels).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("dismisses on an outside pointer and supports arrow-key traversal", async () => {
    const user = userEvent.setup();
    renderControl();
    const trigger = await screen.findByRole("button", { name: /模型路由/ });
    await user.click(trigger);
    const first = screen.getByRole("menuitem", { name: /^模型/ });
    await waitFor(() => expect(first).toHaveFocus());
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("menuitem", { name: "重置为默认设置" })).toHaveFocus();

    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("keeps the anchored material legible under reduced transparency and contrast modes", () => {
    expect(routeStyles).toContain(".v2-model-route-popover");
    expect(routeStyles).toContain("@media (prefers-reduced-transparency: reduce)");
    expect(routeStyles).toContain("@media (prefers-contrast: more)");
    expect(routeStyles).toContain("@media (forced-colors: active)");
    expect(routeStyles).toContain("backdrop-filter: none");
    expect(routeStyles).toContain("background: Canvas");
  });
});
/// <reference types="node" />

import { readFileSync } from "node:fs";
