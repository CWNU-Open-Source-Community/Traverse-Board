import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { ProviderSearchReadinessView } from "../../api/types";
import { V2RunNetworkAuthorityControl } from "./run-network-authority-control";

function renderControl(status: "created" | "paused" | "running" = "paused",
  variant: "menu" | "settings" = "settings",
  readiness: Partial<ProviderSearchReadinessView> = {},
  permissionMode: "conservative" | "workspace_access" | "approval" | "full_access" | "debug" =
  "conservative") {
  const mode = {
    protocol_version: "run_mode.v1", policy_version: "mode_policy.v1",
    revision: 3, capability_grant: false, phase: "deliver", profile: "code",
    surface: "code", reason: "test", requested_by: "test",
    created_at: "2026-08-31T00:00:00Z",
    scope: { network_mode: "allowlist", allowed_targets: ["search.example.org"] },
  };
  const get = vi.fn().mockResolvedValue({ run: { status }, mode,
    execution_permission: { mode: permissionMode } });
  const expandRunNetworkAuthority = vi.fn().mockResolvedValue({
    version: "run_network_authority_control.v1", run_id: "run-1", replayed: false,
    capability_grant: true, added_targets: ["docs.example.com"],
    mode: { ...mode, revision: 4, scope: {
      network_mode: "allowlist",
      allowed_targets: ["docs.example.com", "search.example.org"],
    } },
  });
  const providerSearchReadiness = vi.fn().mockResolvedValue({
    protocol_version: "provider_search_readiness.v1", thread_id: "thread-1", run_id: "run-1",
    model_route: "provider/model", provider: "provider", model: "model",
    search_policy: "provider_native", state: "ready", reason: "search_backend_ready",
    remediation: "none", required_target: "search.example.org", network_mode: "allowlist",
    mode_revision: 3, runtime_ready: true, capability_grant: false, ...readiness,
  });
  const client = { hasControl: true, get, expandRunNetworkAuthority,
    providerSearchReadiness } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2RunNetworkAuthorityControl client={client} runID="run-1" threadID="thread-1"
      variant={variant} />
  </QueryClientProvider>);
  return { expandRunNetworkAuthority };
}

describe("V2RunNetworkAuthorityControl", () => {
  it("shows the exact current Run network scope from the Composer chip", async () => {
    const user = userEvent.setup();
    renderControl("paused", "menu");

    const trigger = screen.getByRole("button", { name: "网页访问状态" });
    await waitFor(() => expect(trigger).toHaveTextContent("搜索就绪"));
    await user.click(trigger);

    const popover = screen.getByRole("dialog", { name: "当前执行网页访问" });
    expect(within(popover).getByText("直接 URL 抓取")).toBeInTheDocument();
    expect(within(popover).getByText(/供应商搜索 · 搜索就绪/u)).toBeInTheDocument();
    expect(within(popover).getByText("search.example.org")).toBeInTheDocument();
    expect(within(popover).getByText(/只访问供应商 API/u)).toBeInTheDocument();
    expect(within(popover).getByText(/只追加明确的公网 HTTPS 主机/u)).toBeInTheDocument();
  });

  it("shows safe public HTTPS instead of an empty exact allowlist in Full Access", async () => {
    const user = userEvent.setup();
    renderControl("running", "menu", {}, "full_access");

    const trigger = screen.getByRole("button", { name: "网页访问状态" });
    await waitFor(() => expect(trigger).toHaveTextContent("搜索就绪"));
    await user.click(trigger);

    const popover = screen.getByRole("dialog", { name: "当前执行网页访问" });
    expect(within(popover).getByText("公网 HTTPS")).toBeInTheDocument();
    expect(within(popover).getByText(/允许匿名访问任意公网 HTTPS/u)).toBeInTheDocument();
    expect(within(popover).queryByRole("textbox", { name: "追加允许的 HTTPS 主机" }))
      .not.toBeInTheDocument();
  });

  it("explains and prefills the exact missing Provider target", async () => {
    const user = userEvent.setup();
    renderControl("paused", "menu", {
      state: "missing_allowlist", reason: "search_endpoint_not_allowlisted",
      remediation: "add_required_target", required_target: "api.provider.com",
      runtime_ready: false,
    });

    const trigger = screen.getByRole("button", { name: "网页访问状态" });
    await waitFor(() => expect(trigger).toHaveTextContent("搜索缺目标"));
    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: /使用所需主机/u }));
    expect(screen.getByRole("textbox", { name: "追加允许的 HTTPS 主机" }))
      .toHaveValue("api.provider.com");
  });

  it("distinguishes a native-search transport timeout from model chat readiness", async () => {
    const user = userEvent.setup();
    renderControl("paused", "menu", {
      state: "provider_unavailable", reason: "provider_native_qualification_failed",
      remediation: "qualify_provider_search", detail_code: "transport_unavailable",
      runtime_ready: false,
    });

    const trigger = screen.getByRole("button", { name: "网页访问状态" });
    await waitFor(() => expect(trigger).toHaveTextContent("搜索不可用"));
    await user.click(trigger);
    expect(screen.getByText(/连接或超时边界内没有完成/u)).toBeInTheDocument();
    expect(screen.getByText(/普通对话仍可继续/u)).toBeInTheDocument();
  });

  it("requires a second confirmation and submits an exact revision-bound host grant", async () => {
    const user = userEvent.setup();
    const controls = renderControl();
    const input = await screen.findByRole("textbox", { name: "追加允许的 HTTPS 主机" });
    await waitFor(() => expect(input).toBeEnabled());
    await user.type(input, "HTTPS://Docs.Example.COM:443/\ndocs.example.com");
    expect(input).toHaveValue("HTTPS://Docs.Example.COM:443/\ndocs.example.com");
    const submit = screen.getByRole("button", { name: "审核并追加" });
    await waitFor(() => expect(submit).toBeEnabled());
    await user.click(submit);

    expect(controls.expandRunNetworkAuthority).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "追加网页访问范围？" });
    await user.click(within(dialog).getByRole("button", { name: "允许这些主机" }));

    await waitFor(() => expect(controls.expandRunNetworkAuthority).toHaveBeenCalledWith(
      "run-1", {
        version: "run_network_authority_control.v1",
        expected_mode_revision: 3,
        add_allowed_targets: ["docs.example.com"],
        reason: "v2 operator-confirmed exact HTTPS targets",
      }, expect.stringMatching(/^v2-run-network-authority-/u),
    ));
    expect(await screen.findByText("docs.example.com")).toBeInTheDocument();
  });

  it("refuses mutation outside a created or paused safe boundary", async () => {
    renderControl("running");
    const input = await screen.findByRole("textbox", { name: "追加允许的 HTTPS 主机" });
    await waitFor(() => expect(input).toBeDisabled());
    expect(await screen.findByText(/需在 created\/paused 静止边界追加/u)).toBeInTheDocument();
  });

  it("rejects broad and path-bearing targets before confirmation", async () => {
    const user = userEvent.setup();
    renderControl();
    const input = await screen.findByRole("textbox", { name: "追加允许的 HTTPS 主机" });
    await waitFor(() => expect(input).toBeEnabled());
    await user.type(input, "*.example.com\nhttps://example.net/path\nlocalhost\n10.0.0.1");
    expect(input).toHaveValue("*.example.com\nhttps://example.net/path\nlocalhost\n10.0.0.1");
    expect(await screen.findByRole("alert")).toHaveTextContent("只接受无路径、查询或通配符");
    expect(screen.getByRole("button", { name: "审核并追加" })).toBeDisabled();
  });

  it("returns focus to the Composer chip when Escape closes its popover", async () => {
    const user = userEvent.setup();
    renderControl("paused", "menu");
    const trigger = screen.getByRole("button", { name: "网页访问状态" });
    await waitFor(() => expect(trigger).toHaveTextContent("搜索就绪"));
    await user.click(trigger);
    expect(screen.getByRole("dialog", { name: "当前执行网页访问" })).toBeInTheDocument();

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog", { name: "当前执行网页访问" }))
      .not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
