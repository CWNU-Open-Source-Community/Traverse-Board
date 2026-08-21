import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ComponentProps } from "react";
import { CyberAgentClient } from "../api/client";
import { LocaleProvider } from "../lib/locale";
import { SettingsView, type SettingsCapability } from "./settings-view";

const capabilities: SettingsCapability[] = [
  { id: "run-control", label: "执行档位", enabled: true },
  { id: "plan-delivery", label: "计划交付", enabled: true },
  { id: "wake-worker", label: "Wake Worker", enabled: false },
];

const health = {
  status: "ok" as const,
  api_version: "api.v1" as const,
  app_version: "test",
  schema_version: 84,
};
const client = new CyberAgentClient("read-token");

function renderSettings(props: ComponentProps<typeof SettingsView>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>
    <LocaleProvider><SettingsView {...props} /></LocaleProvider>
  </QueryClientProvider>);
}

describe("SettingsView", () => {
  beforeEach(() => {
    window.localStorage.clear();
    delete document.documentElement.dataset.prayuDensity;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("projects real runtime facts and moves the selected state with navigation", () => {
    renderSettings({ capabilities, client, desktop: true, health,
      selectedRunID: "", onBack: vi.fn(), onOpenModels: vi.fn(), onOpenSkills: vi.fn() });

    expect(screen.getByRole("button", { name: "常规" })).toHaveClass("active");
    expect(screen.getByRole("button", { name: "个人资料" })).not.toHaveClass("active");
    expect(screen.getByRole("heading", { name: "常规" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "中文" })).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(screen.getByRole("button", { name: "个人资料" }));
    expect(screen.getByRole("heading", { name: "针路簿" })).toBeInTheDocument();
    expect(screen.getByText("v84")).toBeInTheDocument();
    expect(screen.getByText("2/3")).toBeInTheDocument();
  });

  it("keeps display density local and leaves model and Skill actions explicit", () => {
    const onOpenModels = vi.fn();
    const onOpenSkills = vi.fn();
    renderSettings({ capabilities, client, desktop: true, health,
      selectedRunID: "", onBack: vi.fn(), onOpenModels, onOpenSkills });

    fireEvent.click(screen.getByRole("button", { name: "外观" }));
    fireEvent.click(screen.getByRole("button", { name: "透明玻璃" }));
    expect(screen.getByRole("button", { name: "透明玻璃" }))
      .toHaveAttribute("aria-pressed", "true");
    expect(document.documentElement.dataset.prayuTheme).toBe("glass");
    expect(window.localStorage.getItem("prayu.theme")).toBe("glass");
    fireEvent.click(screen.getByRole("button", { name: "紧凑" }));
    expect(document.documentElement.dataset.prayuDensity).toBe("compact");
    expect(window.localStorage.getItem("prayu.ui-density")).toBe("compact");

    fireEvent.click(screen.getByRole("button", { name: "模型与配置" }));
    fireEvent.click(screen.getByRole("button", { name: "Skill 包" }));
    expect(onOpenModels).toHaveBeenCalledTimes(1);
    expect(onOpenSkills).toHaveBeenCalledTimes(1);
  });

  it("keeps advanced Run diagnostics behind an explicit Workbench preference", () => {
    renderSettings({ capabilities, client, desktop: true, health,
      selectedRunID: "", onBack: vi.fn(), onOpenModels: vi.fn(), onOpenSkills: vi.fn() });

    fireEvent.click(screen.getByRole("button", { name: "工作台" }));
    expect(screen.getByRole("button", { name: "精简" }))
      .toHaveAttribute("aria-pressed", "true");
    fireEvent.click(screen.getByRole("button", { name: "完整" }));
    expect(window.localStorage.getItem("prayu.run-navigation.v1")).toBe("diagnostic");
  });

  it("falls back safely when browser storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("storage disabled", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("storage disabled", "SecurityError");
    });

    expect(() => renderSettings({ capabilities, client, desktop: true, health, selectedRunID: "",
      onBack: vi.fn(), onOpenModels: vi.fn(), onOpenSkills: vi.fn() })).not.toThrow();
    fireEvent.click(screen.getByRole("button", { name: "外观" }));
    expect(screen.getByRole("button", { name: "舒展" })).toHaveAttribute("aria-pressed", "true");
  });

  it("shows scoped extension evidence and uses pinned facts for immediate disable", async () => {
    const controlClient = new CyberAgentClient("read-token", "/api/v1", "control-token",
      { extensionControlEnabled: true });
    const digestA = "a".repeat(64);
    const digestB = "b".repeat(64);
    vi.spyOn(controlClient, "extensionInventory").mockResolvedValue({
      protocol_version: "extension-inventory.v1", run_id: "run-extensions",
      workspace_id: "workspace-extensions", mcp_calls: [],
      mcp_servers: [{ protocol_version: "mcp-client-server.v1", id: "mcp-local",
        name: "Local tools", transport: "stdio", target: "C:\\tools\\mcp.exe",
        credential_ref: "", declared_capabilities: ["tools"], scope: "run",
        run_id: "run-extensions", workspace_id: "workspace-extensions",
        source: { kind: "manual", uri: "operator" }, descriptor_fingerprint: digestA,
        state: "enabled", capabilities: { negotiated: ["tools"], tools: ["inspect"],
          resources: [], prompts: [], fingerprint: digestB },
        approved_capability_fingerprint: digestB, health: "healthy", generation: 4,
        created_at: "2026-08-20T01:00:00Z", updated_at: "2026-08-20T01:01:00Z" }],
      plugins: [{ protocol_version: "plugin-installation.v1", id: "plugin-local",
        manifest: { id: "review-pack", name: "Review Pack", version: "1.0.0",
          publisher: "local", description: "review", capabilities: ["hooks"] },
        source: { kind: "local_file", uri: "C:\\plugins\\review.zip", sha256: digestA },
        archive_sha256: digestA, package_fingerprint: digestB,
        signature_present: false, signature_valid: false, state: "enabled",
        enabled_capabilities: ["hooks"], generation: 3, staged_by: "cli_operator",
        created_at: "2026-08-20T01:00:00Z", updated_at: "2026-08-20T01:01:00Z" }],
    });
    const disableMCP = vi.spyOn(controlClient, "reviewMCPServer").mockResolvedValue({} as never);
    const disablePlugin = vi.spyOn(controlClient, "reviewPluginInstallation")
      .mockResolvedValue({} as never);
    const codeIntelInventory = vi.spyOn(controlClient, "codeIntelInventory").mockResolvedValue({
      protocol_version: "code-intel-lsp.v1", enabled: true,
      qualifications: [{ protocol_version: "code-intel-lsp.v1", server_id: "gopls",
        workspace_id: "workspace-extensions", eligible: true, health: "configured",
        descriptor_fingerprint: digestA, executable_hash_matched: true, reviewed: true,
        process_owned: true, minimal_environment: true, network_access_granted: false,
        credentials_granted: false, shell_profile_loaded: false },
      { protocol_version: "code-intel-lsp.v1", server_id: "typescript-language-server",
        workspace_id: "workspace-extensions", eligible: false, health: "unavailable",
        descriptor_fingerprint: digestB, executable_hash_matched: false, reviewed: true,
        process_owned: true, minimal_environment: true, network_access_granted: false,
        credentials_granted: false, shell_profile_loaded: false,
        reason: "TypeScript server hash mismatch" }],
      servers: [{ protocol_version: "code-intel-lsp.v1", server_id: "gopls",
        server_name: "gopls", workspace_id: "workspace-extensions", languages: ["go"],
        source_kind: "operator_config", source_label: "code-intel.json",
        source_sha256: digestA, descriptor_fingerprint: digestA,
        capability_fingerprint: digestB, generation: digestB, health: "healthy",
        capabilities: { workspace_symbols: true, document_symbols: true, definition: true,
          references: true, implementation: true, hover: true, signature_help: true,
          diagnostics: true, call_hierarchy: true, type_hierarchy: true },
        model_visible_tools: ["code_workspace_symbols", "code_document_symbols",
          "code_definition", "code_references", "code_implementation", "code_hover",
          "code_signature_help", "code_diagnostics", "code_call_hierarchy",
          "code_type_hierarchy"], server_version: "v0.20.0", process_owned: true,
        read_only: true, network_access_granted: false, credentials_granted: false,
        shell_profile_loaded: false, qualified_at: "2026-08-20T01:00:00Z" }],
    });

    renderSettings({ capabilities, client: controlClient, desktop: true, health,
      selectedRunID: "run-extensions", onBack: vi.fn(), onOpenModels: vi.fn(),
      onOpenSkills: vi.fn() });
    fireEvent.click(screen.getByRole("button", { name: "Code Intel、MCP 与 Plugin" }));

    expect(await screen.findByText("gopls")).toBeInTheDocument();
    expect(await screen.findByText("已通过")).toBeInTheDocument();
    expect(await screen.findByText("typescript-language-server")).toBeInTheDocument();
    expect(screen.getByText("TypeScript server hash mismatch")).toBeInTheDocument();
    expect(codeIntelInventory).toHaveBeenCalledWith("workspace-extensions", expect.any(AbortSignal));
    expect(await screen.findByText("Local tools")).toBeInTheDocument();
    expect(screen.getByText("Review Pack")).toBeInTheDocument();
    expect(screen.queryByText("extension-test-token")).not.toBeInTheDocument();
    const disableButtons = screen.getAllByRole("button", { name: "立即关闭" });
    fireEvent.click(disableButtons[0]);
    await waitFor(() => expect(disableMCP).toHaveBeenCalledWith("mcp-local", {
      version: "extension-control.v1", action: "disable",
      expected_descriptor_fingerprint: digestA,
    }));
    fireEvent.click(disableButtons[1]);
    await waitFor(() => expect(disablePlugin).toHaveBeenCalledWith("plugin-local", {
      version: "extension-control.v1", action: "disable",
      expected_package_fingerprint: digestB, expected_generation: 3,
      confirm_untrusted: false,
    }));
  });
});
