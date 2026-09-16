import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import { LocaleProvider } from "../../lib/locale";
import { V2Settings } from "./settings";
import type { V2SettingsSection } from "./sidebar";
import { useConnectionStore } from "../../state/connection";

function mount(client: Partial<CyberAgentClient>, section: V2SettingsSection, threadID = "", desktop = false) {
  window.localStorage.setItem("prayu.locale.v1", "zh-CN");
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rendered = render(<QueryClientProvider client={queryClient}><LocaleProvider>
    <V2Settings client={client as CyberAgentClient} desktop={desktop} onOpenInspector={vi.fn()}
      onSelectSection={vi.fn()} section={section} threadID={threadID} workspaces={[]} />
  </LocaleProvider></QueryClientProvider>);
  return { ...rendered, queryClient };
}

const emptyExtensions = { mcp_servers: [], mcp_calls: [], plugins: [], workspace_id: "workspace-one" };
const emptyCodeIntel = { servers: [], qualifications: [] };

describe("shared advanced settings", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => useConnectionStore.getState().disconnect());

  it("resolves the current Thread before reading its extension scope", async () => {
    const get = vi.fn().mockResolvedValue({ thread: { id: "thread-one", title: "Current task" },
      active_run: { id: "run-one" }, last_run: { id: "old-run" } });
    const extensionInventory = vi.fn().mockResolvedValue(emptyExtensions);
    const codeIntelInventory = vi.fn().mockResolvedValue(emptyCodeIntel);
    mount({ get, extensionInventory, codeIntelInventory }, "extensions", "thread-one");
    await waitFor(() => expect(codeIntelInventory).toHaveBeenCalledWith("workspace-one", expect.any(AbortSignal)));
    expect(get).toHaveBeenCalledWith("/threads/thread-one", {}, expect.any(AbortSignal));
    expect(extensionInventory).toHaveBeenCalledWith("run-one", expect.any(AbortSignal));
    expect(extensionInventory).not.toHaveBeenCalledWith("", expect.anything());
    expect(screen.getByText(/关闭操作作用于该扩展或安装/)).toBeInTheDocument();
  });

  it("does not silently fall back to global extension scope when the selected Thread fails to load", async () => {
    const extensionInventory = vi.fn();
    const codeIntelInventory = vi.fn();
    mount({ get: vi.fn().mockRejectedValue(new Error("not found")), extensionInventory, codeIntelInventory },
      "extensions", "missing-thread");
    expect(await screen.findByRole("alert")).toHaveTextContent("无法确定当前任务的扩展范围");
    expect(extensionInventory).not.toHaveBeenCalled();
    expect(codeIntelInventory).not.toHaveBeenCalled();
  });

  it("keeps the old plugins address mapped to the real global inventory when no task is selected", async () => {
    const extensionInventory = vi.fn().mockResolvedValue(emptyExtensions);
    const codeIntelInventory = vi.fn().mockResolvedValue(emptyCodeIntel);
    mount({ extensionInventory, codeIntelInventory }, "plugins");
    await waitFor(() => expect(extensionInventory).toHaveBeenCalledWith("", expect.any(AbortSignal)));
    expect(codeIntelInventory).toHaveBeenCalledWith("", expect.any(AbortSignal));
    expect(screen.getByText(/未选择任务/)).toBeInTheDocument();
  });

  it("requires explicit untrusted registration before web installation and keeps its key on retry", async () => {
    const user = userEvent.setup();
    const installSkillPackage = vi.fn().mockRejectedValueOnce(new Error("response unavailable"))
      .mockResolvedValueOnce({});
    mount({ hasSkillInstallation: true, installSkillPackage }, "skills");
    const file = new File(["zip"], "review.zip", { type: "application/zip" });
    Object.defineProperty(file, "arrayBuffer", { value: async () => new TextEncoder().encode("zip").buffer });
    fireEvent.change(screen.getByLabelText("选择 Skill ZIP 包"), { target: { files: [file] } });
    expect(installSkillPackage).not.toHaveBeenCalled();
    const install = screen.getByRole("button", { name: "安装 Skill 包" });
    expect(install).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /确认按不受信任包登记到 Code/ }));
    await user.click(install);
    await screen.findByText("response unavailable");
    await user.click(install);
    await screen.findByText("Skill 包已安装");
    expect(installSkillPackage).toHaveBeenCalledTimes(2);
    expect(installSkillPackage.mock.calls[0]).toEqual(installSkillPackage.mock.calls[1]);
    expect(installSkillPackage.mock.calls[0][0]).toEqual({ version: "skill_package_installation.v1",
      archive_base64: btoa("zip"), surface: "code", confirm_untrusted: true });
    expect(install).toBeDisabled();
  });

  it("keeps read-only installation closed while the desktop preview remains available", async () => {
    const user = userEvent.setup();
    const installSkillPackage = vi.fn();
    mount({ hasSkillInstallation: false, installSkillPackage }, "skills", "", true);
    expect(screen.getByText(/当前连接只允许预览/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "预览 Skill 包" }));
    expect(screen.getByRole("dialog", { name: "Skill 包预览" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "安装" })).not.toBeInTheDocument();
    expect(installSkillPackage).not.toHaveBeenCalled();
  });

  it("reuses the named global route and price controls without a second credential editor", async () => {
    const user = userEvent.setup();
    const providerCredentialStatuses = vi.fn();
    const selectModelRoute = vi.fn().mockResolvedValue({});
    const importPriceSnapshot = vi.fn().mockResolvedValue({});
    mount({ hasModelControl: true, hasControl: true, hasProviderCredentials: true,
      providerCredentialStatuses, selectModelRoute, importPriceSnapshot,
      modelAvailability: vi.fn().mockResolvedValue({ providers: [{ name: "sample", status: "available",
        models: ["one", "two"], harnesses: [] }], routes: [{ name: "code", provider: "sample",
        model: "one", available: true, harness_ready: true }] }),
      listPriceSnapshots: vi.fn().mockResolvedValue({ items: [] }) }, "advanced-models");
    await user.selectOptions(await screen.findByRole("combobox", { name: "code 模型路由" }), "sample/two");
    await user.click(screen.getByRole("button", { name: "保存 code 路由" }));
    await waitFor(() => expect(selectModelRoute).toHaveBeenCalledWith("code", {
      version: "model_route_control.v1", provider: "sample", model: "two" }));
    fireEvent.change(screen.getByRole("textbox", { name: "价格文档" }), { target: { value: '{"source":"review"}' } });
    await user.click(screen.getByRole("button", { name: "导入" }));
    await waitFor(() => expect(importPriceSnapshot).toHaveBeenCalledWith({ version: "price_snapshot.v1",
      document: '{"source":"review"}' }, expect.any(String)));
    expect(providerCredentialStatuses).not.toHaveBeenCalled();
    expect(screen.queryByRole("heading", { name: "系统凭证" })).not.toBeInTheDocument();
  });

  it("uses existing local Inspector preferences without making a service request", async () => {
    const user = userEvent.setup();
    mount({}, "inspector");
    await user.click(screen.getByRole("button", { name: "紧凑" }));
    await user.click(screen.getByRole("button", { name: "完整诊断" }));
    expect(window.localStorage.getItem("prayu.ui-density")).toBe("compact");
    expect(document.documentElement.dataset.prayuDensity).toBe("compact");
    expect(window.localStorage.getItem("prayu.run-navigation.v1")).toBe("diagnostic");
  });

  it("reports unknown versions on health failure instead of displaying a fabricated development version", async () => {
    const safeWebReadiness = vi.fn();
    mount({ health: vi.fn().mockRejectedValue(new Error("offline")), safeWebReadiness }, "about");
    expect(await screen.findByRole("alert")).toHaveTextContent("版本未知");
    expect(screen.queryByText("dev")).not.toBeInTheDocument();
    expect(safeWebReadiness).not.toHaveBeenCalled();
  });

  it("clears only the web interface connection and its cache without sending a task stop", async () => {
    const user = userEvent.setup();
    useConnectionStore.setState({ token: "test-read-connection", controlToken: "test-control-connection" });
    const interruptThread = vi.fn();
    const controlRunLifecycle = vi.fn();
    const { queryClient } = mount({ health: vi.fn().mockResolvedValue({ status: "ok", app_version: "test",
      api_version: "api.v1", schema_version: 157 }), interruptThread, controlRunLifecycle }, "about");
    queryClient.setQueryData(["test-connection-cache"], { id: "saved" });
    await screen.findByText("test");
    expect(screen.getByText(/不会停止任务或撤销已受理的操作/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "断开连接" }));
    expect(queryClient.getQueryData(["test-connection-cache"])).toBeUndefined();
    expect(useConnectionStore.getState().token).toBe("");
    expect(useConnectionStore.getState().controlToken).toBe("");
    expect(interruptThread).not.toHaveBeenCalled();
    expect(controlRunLifecycle).not.toHaveBeenCalled();
  });

  it("does not offer web disconnection in the native settings surface", async () => {
    mount({ health: vi.fn().mockResolvedValue({ status: "ok", app_version: "native-test",
      api_version: "api.v1", schema_version: 157 }) }, "about", "", true);
    await screen.findByText("native-test");
    expect(screen.queryByRole("button", { name: "断开连接" })).not.toBeInTheDocument();
  });
});
