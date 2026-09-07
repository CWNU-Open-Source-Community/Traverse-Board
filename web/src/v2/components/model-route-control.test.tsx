import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  it("opens an anchored two-level menu and groups selectable and unavailable routes", async () => {
    const user = userEvent.setup();
    const controls = renderControl();
    const trigger = await screen.findByRole("button", {
      name: "模型路由，当前 deepseek-v4-flash",
    });

    expect(controls.client.availableModelRoutes).not.toHaveBeenCalled();
    await user.click(trigger);
    const settings = screen.getByRole("menu", { name: "模型与响应设置" });
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
    expect(trigger).toHaveTextContent("DeepSeek · deepseek-v4-pro下一轮");
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
