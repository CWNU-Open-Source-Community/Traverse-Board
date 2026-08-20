import { beforeEach, describe, expect, it, vi } from "vitest";

import type { DesktopSkillPreview } from "./desktop-bridge";

const bootstrap = {
  protocol_version: "desktop_connection_bootstrap.v1",
  agent_code_tools_enabled: true,
  code_intel_enabled: false,
  api_base_url: "/api/v1",
  api_version: "api.v1",
  app_version: "v0.1.0",
  ui_digest: "a".repeat(64),
  read_token: "read-token-0123456789abcdefghijklmnop",
  control_token: "",
  control_enabled: false,
  execution_permission_control_enabled: false,
  browser_cdp_permission_control_enabled: false,
  full_cdp_debug_enabled: false,
  operator_approval_enabled: false,
  danger_full_access_enabled: false,
  debug_maximum_access_enabled: false,
  command_runtime_enabled: false,
  run_creation_enabled: false,
  session_message_enabled: false,
  session_steering_control_enabled: false,
  run_lifecycle_enabled: false,
  run_execution_enabled: false,
  plan_delivery_control_enabled: false,
  approval_control_enabled: false,
  controlled_command_proposal_control_enabled: false,
  host_command_proposal_control_enabled: false,
  model_control_enabled: false,
  provider_credential_enabled: false,
  file_edit_review_enabled: false,
  file_edit_proposal_enabled: false,
  run_wake_control_enabled: false,
  file_edit_apply_enabled: false,
  run_wake_execution_enabled: false,
  run_wake_worker_enabled: false,
  scheduled_job_control_enabled: false,
  scheduled_job_worker_enabled: false,
  read_only_default: true,
  process_execution_enabled: false,
  shell_execution_enabled: false,
  docker_execution_enabled: false,
  skill_installation_enabled: false,
  evidence_attachment_enabled: false,
  verification_evidence_enabled: false,
  ui_evidence_control_enabled: false,
  embedded_analyzer_execution_enabled: false,
  workspace_checkpoint_control_enabled: false,
  batch_delivery_control_enabled: false,
  batch_delivery_host_validation_enabled: false,
  user_terminal_enabled: false,
  agent_terminal_input_default: false,
  workspace_open_enabled: false,
  workspace_import_enabled: false,
  renderer_path_input_supported: false,
};

const selection = {
  protocol_version: "desktop_file_selection.v1",
  handle: "A".repeat(43),
  expires_at: "2026-07-18T10:00:00Z",
};

const preview: DesktopSkillPreview = {
  protocol_version: "desktop_skill_package_preview.v1",
  package_protocol: "skill_package.v1",
  skill_protocol: "skill.v1",
  name: "review-helper",
  version: "1.0.0",
  profiles: ["review"],
  declared_tools: ["workspace_list"],
  declared_tool_count: 1,
  content_bytes: 128,
  content_token_upper_bound: 32,
  archive_sha256: "b".repeat(64),
  package_fingerprint: "c".repeat(64),
  archive_bytes: 512,
  uncompressed_bytes: 384,
  entry_count: 2,
  trust_class: "operator_installed_untrusted",
  risk_codes: ["untrusted_instructions"],
  executable_asset_count: 0,
  install_hook_count: 0,
  import_command_execution: false,
  import_network_access: false,
  import_provider_calls: false,
  tool_capability_grant: false,
  installation_authorized: false,
  validated: true,
  confirmation_handle: "D".repeat(43),
  confirmation_expires_at: "2026-07-18T10:05:00Z",
};

describe("desktop native bridge", () => {
  beforeEach(() => {
    vi.resetModules();
    delete window.go;
  });

  it("detects absence without changing ordinary browser behavior", async () => {
    const module = await import("./desktop-bridge");
    expect(module.desktopBridgeAvailable()).toBe(false);
    expect(module.desktopRuntimeActive()).toBe(false);
    await expect(module.loadDesktopBootstrap()).resolves.toBeNull();
  });

  it("accepts only a closed-authority same-origin bootstrap", async () => {
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(bootstrap) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(bootstrap);
    expect(module.desktopRuntimeActive()).toBe(true);
  });

  it("accepts Run creation without enabling existing Run controls", async () => {
    const creationOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      control_enabled: false,
      run_creation_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(creationOnly) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(creationOnly);
  });

  it("accepts Session messages without enabling other control capabilities", async () => {
    const messagesOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      session_message_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(messagesOnly) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(messagesOnly);
  });

  it("accepts evidence attachment as an independent capability", async () => {
    const evidenceOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      evidence_attachment_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(evidenceOnly) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(evidenceOnly);
  });

  it("accepts verification evidence as an independent capability", async () => {
    const verificationOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      verification_evidence_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(verificationOnly) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(verificationOnly);
  });

  it("accepts Session steering cancellation without enabling sibling capabilities", async () => {
    const steeringOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      session_steering_control_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(steeringOnly) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(steeringOnly);
  });

  it("accepts Plan and approval controls as independent capabilities", async () => {
    const planOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      plan_delivery_control_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(planOnly) });
    let module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(planOnly);

    vi.resetModules();
    const approvalOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      approval_control_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(approvalOnly) });
    module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(approvalOnly);
  });

  it("accepts fixed command proposal review as an independent capability", async () => {
    const commandProposalOnly = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      controlled_command_proposal_control_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(commandProposalOnly) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(commandProposalOnly);
  });

  it("accepts Docker execution with permission control and operator approval", async () => {
    const dockerEnabled = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      control_enabled: true,
      execution_permission_control_enabled: true,
      operator_approval_enabled: true,
      docker_execution_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(dockerEnabled) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).resolves.toEqual(dockerEnabled);
  });

  it("rejects Docker execution authority without permission control", async () => {
    installBridge({ Bootstrap: vi.fn().mockResolvedValue({
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      docker_execution_enabled: true,
      read_only_default: false,
    }) });
    const module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).rejects.toThrow("rejected");
  });

  it("rejects authority widening and extra local-file fields", async () => {
    installBridge({ Bootstrap: vi.fn().mockResolvedValue({ ...bootstrap, process_execution_enabled: true }) });
    let module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).rejects.toThrow("rejected");

    vi.resetModules();
    installBridge({ Bootstrap: vi.fn().mockResolvedValue({
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      full_cdp_debug_enabled: true,
      read_only_default: false,
    }) });
    module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).rejects.toThrow("rejected");

    vi.resetModules();
    installBridge({ Bootstrap: vi.fn().mockResolvedValue({ ...bootstrap, source_path: "C:\\PRIVATE" }) });
    module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).rejects.toThrow("rejected");

    vi.resetModules();
    const missingPermissionFlag = { ...bootstrap } as Record<string, unknown>;
    delete missingPermissionFlag.execution_permission_control_enabled;
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(missingPermissionFlag) });
    module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).rejects.toThrow("rejected");

    vi.resetModules();
    installBridge({ Bootstrap: vi.fn().mockResolvedValue({
      ...bootstrap,
      control_token: bootstrap.read_token,
      control_enabled: true,
    run_creation_enabled: false,
      read_only_default: false,
    }) });
    module = await import("./desktop-bridge");
    await expect(module.loadDesktopBootstrap()).rejects.toThrow("rejected");
  });

  it("opens the native picker and consumes only its opaque handle", async () => {
    const select = vi.fn().mockResolvedValue({
      protocol_version: "desktop_skill_package_dialog.v1",
      status: "selected",
      selection,
    });
    const consume = vi.fn().mockResolvedValue(preview);
    installBridge({ SelectSkillPackage: select, PreviewSkillPackage: consume });
    const module = await import("./desktop-bridge");
    await expect(module.selectDesktopSkillPreview()).resolves.toEqual(preview);
    expect(select).toHaveBeenCalledWith();
    expect(consume).toHaveBeenCalledWith(selection.handle);
  });

  it("installs only after an enabled bootstrap and validates inert authority", async () => {
    const enabled = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      skill_installation_enabled: true,
      read_only_default: false,
    };
    const result = {
      protocol_version: "desktop_skill_package_install.v1",
      name: preview.name, version: preview.version, surface: "code",
      trust_class: "operator_installed_untrusted",
      archive_sha256: preview.archive_sha256,
      package_fingerprint: preview.package_fingerprint,
      replayed: false, recovered_pending: false,
      import_command_execution: false, import_network_access: false,
      import_provider_calls: false, tool_capability_grant: false,
      run_selection_authorized: false, context_injection_authorized: false,
      receipt: { protocol_version: "operation_receipt.v1", kind: "skill_package_install",
        outcome: "installed", durable: true, replayed: false, retry_safe: true,
        retry_strategy: "same_operation_key", recovery_action: "none",
        cleanup_state: "not_applicable" },
    };
    const install = vi.fn().mockResolvedValue(result);
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(enabled), InstallSkillPackage: install });
    const module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.installDesktopSkillPackage(preview, "code",
      "desktop-install-operation-0001")).resolves.toEqual(result);
    expect(install).toHaveBeenCalledWith({
      protocol_version: "desktop_skill_package_install.v1",
      confirmation_handle: preview.confirmation_handle,
      surface: "code",
      operation_key: "desktop-install-operation-0001",
      confirm_untrusted: true,
    });
  });

  it("does not consume a cancelled selection or path-bearing preview", async () => {
    const consume = vi.fn().mockResolvedValue(preview);
    installBridge({
      SelectSkillPackage: vi.fn().mockResolvedValue({
        protocol_version: "desktop_skill_package_dialog.v1",
        status: "cancelled",
        selection: null,
      }),
      PreviewSkillPackage: consume,
    });
    let module = await import("./desktop-bridge");
    await expect(module.selectDesktopSkillPreview()).resolves.toBeNull();
    expect(consume).not.toHaveBeenCalled();

    vi.resetModules();
    installBridge({
      SelectSkillPackage: vi.fn().mockResolvedValue({
        protocol_version: "desktop_skill_package_dialog.v1",
        status: "selected",
        selection,
      }),
      PreviewSkillPackage: vi.fn().mockResolvedValue({ ...preview, source_path: "C:\\PRIVATE" }),
    });
    module = await import("./desktop-bridge");
    await expect(module.selectDesktopSkillPreview()).rejects.toThrow("rejected");
  });

  it("opens a registered Workspace only through a strict pathless native contract", async () => {
    const enabled = { ...bootstrap, workspace_open_enabled: true };
    const launchers = {
      protocol_version: "desktop_workspace_launcher_list.v1",
      workspace_id: "workspace-1",
      launchers: [
        { id: "file-explorer", label: "File Explorer", kind: "folder" },
        { id: "terminal", label: "Terminal", kind: "terminal" },
      ],
      root_path_exposed: false,
      renderer_path_input_supported: false,
      arbitrary_arguments_accepted: false,
      agent_authority_granted: false,
    };
    const result = {
      protocol_version: "desktop_workspace_open.v1",
      workspace_id: "workspace-1",
      launcher_id: "file-explorer",
      status: "started",
      operator_confirmed: true,
      external_process_started: true,
      arbitrary_arguments_accepted: false,
      command_executed: false,
      root_path_exposed: false,
      agent_authority_granted: false,
    };
    const list = vi.fn().mockResolvedValue(launchers);
    const open = vi.fn().mockResolvedValue(result);
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(enabled),
      WorkspaceLaunchers: list, OpenWorkspace: open });
    const module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.listDesktopWorkspaceLaunchers("workspace-1"))
      .resolves.toEqual(launchers.launchers);
    await expect(module.openDesktopWorkspace("workspace-1", "file-explorer"))
      .resolves.toEqual(result);
    expect(list).toHaveBeenCalledWith("workspace-1");
    expect(open).toHaveBeenCalledWith({
      protocol_version: "desktop_workspace_open.v1",
      workspace_id: "workspace-1",
      launcher_id: "file-explorer",
    });
  });

  it("imports a selected directory through a strict pathless native contract", async () => {
    const enabled = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      run_creation_enabled: true,
      workspace_import_enabled: true,
      read_only_default: false,
    };
    const imported = {
      protocol_version: "desktop_workspace_import.v1",
      status: "registered",
      workspace: { id: "ws-import-0123456789abcdef", name: "project",
        created_at: "2026-08-13T01:02:03Z" },
      root_path_exposed: false,
      renderer_path_input_supported: false,
      directory_content_modified: false,
      agent_authority_granted: false,
    };
    const importWorkspace = vi.fn().mockResolvedValue(imported);
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(enabled),
      ImportWorkspace: importWorkspace });
    const module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.importDesktopWorkspace()).resolves.toEqual(imported.workspace);
    expect(module.desktopWorkspaceImportEnabled()).toBe(true);
    expect(importWorkspace).toHaveBeenCalledWith();

    importWorkspace.mockResolvedValue({ ...imported, root_path: "C:\\PRIVATE" });
    await expect(module.importDesktopWorkspace()).rejects.toThrow("rejected");
  });

  it("treats a cancelled Workspace directory picker as a non-mutating result", async () => {
    const enabled = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      run_creation_enabled: true,
      workspace_import_enabled: true,
      read_only_default: false,
    };
    installBridge({ Bootstrap: vi.fn().mockResolvedValue(enabled),
      ImportWorkspace: vi.fn().mockResolvedValue({
        protocol_version: "desktop_workspace_import.v1",
        status: "cancelled",
        workspace: null,
        root_path_exposed: false,
        renderer_path_input_supported: false,
        directory_content_modified: false,
        agent_authority_granted: false,
      }) });
    const module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.importDesktopWorkspace()).resolves.toBeNull();
  });

  it("rejects path disclosure, arbitrary arguments, and inconsistent open receipts", async () => {
    const enabled = { ...bootstrap, workspace_open_enabled: true };
    installBridge({
      Bootstrap: vi.fn().mockResolvedValue(enabled),
      WorkspaceLaunchers: vi.fn().mockResolvedValue({
        protocol_version: "desktop_workspace_launcher_list.v1",
        workspace_id: "workspace-1",
        launchers: [],
        root_path_exposed: false,
        renderer_path_input_supported: false,
        arbitrary_arguments_accepted: false,
        agent_authority_granted: false,
        root_path: "C:\\PRIVATE",
      }),
    });
    let module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.listDesktopWorkspaceLaunchers("workspace-1")).rejects.toThrow("rejected");

    vi.resetModules();
    installBridge({
      Bootstrap: vi.fn().mockResolvedValue(enabled),
      OpenWorkspace: vi.fn().mockResolvedValue({
        protocol_version: "desktop_workspace_open.v1",
        workspace_id: "workspace-1",
        launcher_id: "terminal",
        status: "started",
        operator_confirmed: false,
        external_process_started: true,
        arbitrary_arguments_accepted: false,
        command_executed: false,
        root_path_exposed: false,
        agent_authority_granted: false,
      }),
    });
    module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.openDesktopWorkspace("workspace-1", "terminal")).rejects.toThrow("rejected");
    await expect(module.openDesktopWorkspace("workspace-1", "terminal -- powershell"))
      .rejects.toThrow("request was rejected");
  });

  it("uses a user-only terminal contract and validates decoded output bytes", async () => {
    const enabled = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      read_only_default: false,
      process_execution_enabled: true,
      shell_execution_enabled: true,
      user_terminal_enabled: true,
    };
    const session = {
      protocol_version: "desktop_user_terminal.v1",
      session_id: "user-terminal-1",
      run_id: "run-1",
      state: "running",
      backend: "windows-conpty",
      columns: 120,
      rows: 32,
      output_base_cursor: 0,
      output_next_cursor: 2,
      exit_code: 0,
      user_owned: true,
      agent_input_default: false,
      job_assigned_at_creation: true,
      kill_on_job_close: true,
      persistent: true,
      process_local: true,
      raw_output_persisted: false,
    };
    const start = vi.fn().mockResolvedValue(session);
    const get = vi.fn().mockResolvedValue(session);
    const read = vi.fn()
      .mockResolvedValueOnce({
        protocol_version: "desktop_user_terminal.v1",
        session_id: session.session_id,
        base_cursor: 0,
        next_cursor: 2,
        data_base64: "b2s=",
        data_bytes: 2,
        dropped: false,
        state: "running",
      })
      .mockResolvedValueOnce({
        protocol_version: "desktop_user_terminal.v1",
        session_id: session.session_id,
        base_cursor: 0,
        next_cursor: 2,
        data_base64: "b2s=",
        data_bytes: 3,
        dropped: false,
        state: "running",
      });
    const write = vi.fn().mockResolvedValue({
      protocol_version: "desktop_user_terminal.v1",
      session_id: session.session_id,
      bytes_written: 2,
    });
    const resize = vi.fn().mockResolvedValue(undefined);
    const close = vi.fn().mockResolvedValue(undefined);
    installBridge({
      Bootstrap: vi.fn().mockResolvedValue(enabled),
      StartUserTerminal: start,
      GetUserTerminal: get,
      ReadUserTerminal: read,
      WriteUserTerminal: write,
      ResizeUserTerminal: resize,
      CloseUserTerminal: close,
    });
    const module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.startDesktopUserTerminal("run-1")).resolves.toEqual(session);
    await expect(module.getDesktopUserTerminal(session.session_id)).resolves.toEqual(session);
    await expect(module.readDesktopUserTerminal(session.session_id, 0))
      .resolves.toMatchObject({ data_base64: "b2s=", data_bytes: 2 });
    await expect(module.writeDesktopUserTerminal(session.session_id, "ok"))
      .resolves.toMatchObject({ bytes_written: 2 });
    await expect(module.resizeDesktopUserTerminal(session.session_id, 100, 30))
      .resolves.toBeUndefined();
    await expect(module.closeDesktopUserTerminal(session.session_id))
      .resolves.toBeUndefined();
    expect(start).toHaveBeenCalledWith({
      protocol_version: "desktop_user_terminal.v1",
      run_id: "run-1",
      columns: 120,
      rows: 32,
      replace_existing: false,
      confirm_debug_boundary: true,
    });
    expect(write).toHaveBeenCalledWith({
      protocol_version: "desktop_user_terminal.v1",
      session_id: session.session_id,
      data: "ok",
      user_confirmed: true,
    });
    await expect(module.readDesktopUserTerminal(session.session_id, 0))
      .rejects.toThrow("output was rejected");
  });

  it("grants only a bounded process-local Debug terminal Agent-input lease", async () => {
    const enabled = {
      ...bootstrap,
      control_token: "control-token-0123456789abcdefghijkl",
      read_only_default: false,
      debug_maximum_access_enabled: true,
      execution_permission_control_enabled: true,
      operator_approval_enabled: true,
      danger_full_access_enabled: true,
      process_execution_enabled: true,
      shell_execution_enabled: true,
      user_terminal_enabled: true,
    };
    const binding = {
      protocol_version: "desktop_debug_terminal_agent_input.v1",
      binding_id: "terminal-input-lease-1",
      run_id: "run-1",
      terminal_session_id: "user-terminal-1",
      issued_at: "2026-08-18T01:00:00Z",
      expires_at: "2026-08-18T01:05:00Z",
      process_local: true,
      token_exposed: false,
      raw_input_persisted: false,
    };
    const grant = vi.fn().mockResolvedValue(binding);
    const get = vi.fn().mockResolvedValue(binding);
    const revoke = vi.fn().mockResolvedValue(undefined);
    installBridge({
      Bootstrap: vi.fn().mockResolvedValue(enabled),
      GrantDebugTerminalAgentInput: grant,
      GetDebugTerminalAgentInput: get,
      RevokeDebugTerminalAgentInput: revoke,
    });
    const module = await import("./desktop-bridge");
    await module.loadDesktopBootstrap();
    await expect(module.grantDesktopDebugTerminalAgentInput(
      "run-1", "user-terminal-1", 300)).resolves.toEqual(binding);
    await expect(module.getDesktopDebugTerminalAgentInput("run-1"))
      .resolves.toEqual(binding);
    await expect(module.revokeDesktopDebugTerminalAgentInput(binding.binding_id))
      .resolves.toBeUndefined();
    expect(grant).toHaveBeenCalledWith({
      protocol_version: "desktop_debug_terminal_agent_input.v1",
      run_id: "run-1",
      terminal_session_id: "user-terminal-1",
      ttl_seconds: 300,
      confirm_debug_maximum_access: true,
      confirm_agent_terminal_input: true,
    });
    expect(revoke).toHaveBeenCalledWith({
      protocol_version: "desktop_debug_terminal_agent_input.v1",
      binding_id: binding.binding_id,
      operator_confirmed: true,
    });
  });
});

function installBridge(overrides: Partial<{
  Bootstrap: () => Promise<unknown>;
  ImportWorkspace: () => Promise<unknown>;
  InstallSkillPackage: (request: unknown) => Promise<unknown>;
  PreviewSkillPackage: (handle: string) => Promise<unknown>;
  SelectSkillPackage: () => Promise<unknown>;
  OpenWorkspace: (request: unknown) => Promise<unknown>;
  WorkspaceLaunchers: (workspaceID: string) => Promise<unknown>;
  StartUserTerminal: (request: unknown) => Promise<unknown>;
  GetUserTerminal: (sessionID: string) => Promise<unknown>;
  ReadUserTerminal: (request: unknown) => Promise<unknown>;
  WriteUserTerminal: (request: unknown) => Promise<unknown>;
  ResizeUserTerminal: (request: unknown) => Promise<unknown>;
  CloseUserTerminal: (request: unknown) => Promise<unknown>;
  GrantDebugTerminalAgentInput: (request: unknown) => Promise<unknown>;
  GetDebugTerminalAgentInput: (runID: string) => Promise<unknown>;
  RevokeDebugTerminalAgentInput: (request: unknown) => Promise<void>;
}>) {
  window.go = {
    desktop: {
      DesktopBridge: {
        Bootstrap: vi.fn().mockResolvedValue(bootstrap),
        InstallSkillPackage: vi.fn().mockRejectedValue(new Error("disabled")),
        ImportWorkspace: vi.fn().mockRejectedValue(new Error("disabled")),
        PreviewSkillPackage: vi.fn().mockResolvedValue(preview),
        OpenWorkspace: vi.fn().mockRejectedValue(new Error("disabled")),
        SelectSkillPackage: vi.fn().mockResolvedValue({
          protocol_version: "desktop_skill_package_dialog.v1",
          status: "cancelled",
          selection: null,
        }),
        WorkspaceLaunchers: vi.fn().mockRejectedValue(new Error("disabled")),
        ...overrides,
      },
    },
  };
}
