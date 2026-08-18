import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, vi } from "vitest";
import { UserTerminalPanel } from "./user-terminal-panel";

const terminalMocks = vi.hoisted(() => ({
  agentEnabled: false,
  close: vi.fn(async () => undefined),
  get: vi.fn(),
  getAgent: vi.fn(),
  grantAgent: vi.fn(),
  read: vi.fn(),
  revokeAgent: vi.fn(async () => undefined),
  start: vi.fn(),
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 120;
    rows = 32;
    loadAddon() {}
    open() {}
    onData() {
      return { dispose() {} };
    }
    clear() {}
    dispose() {}
    focus() {}
    reset() {}
    write() {}
  },
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));

vi.mock("../lib/desktop-bridge", () => ({
  closeDesktopUserTerminal: terminalMocks.close,
  desktopDebugTerminalAgentInputEnabled: () => terminalMocks.agentEnabled,
  desktopErrorMessage: (value: unknown) => String(value),
  desktopUserTerminalEnabled: () => true,
  getDesktopUserTerminal: terminalMocks.get,
  getDesktopDebugTerminalAgentInput: terminalMocks.getAgent,
  grantDesktopDebugTerminalAgentInput: terminalMocks.grantAgent,
  readDesktopUserTerminal: terminalMocks.read,
  resizeDesktopUserTerminal: vi.fn(async () => undefined),
  revokeDesktopDebugTerminalAgentInput: terminalMocks.revokeAgent,
  startDesktopUserTerminal: terminalMocks.start,
  writeDesktopUserTerminal: vi.fn(async () => ({
    protocol_version: "desktop_user_terminal.v1",
    session_id: "unused",
    bytes_written: 1,
  })),
}));

describe("UserTerminalPanel", () => {
  beforeEach(() => {
    terminalMocks.agentEnabled = false;
    vi.clearAllMocks();
  });

  it("closes a terminal whose asynchronous start completes after the Run changed", async () => {
    let resolveStart: (value: unknown) => void = () => undefined;
    terminalMocks.start.mockReturnValueOnce(new Promise((resolve) => {
      resolveStart = resolve;
    }));
    const onSession = vi.fn();
    const view = render(<UserTerminalPanel runID="run-original" sessionID=""
      onSession={onSession} />);

    fireEvent.click(screen.getByRole("button", { name: "启动" }));
    expect(terminalMocks.start).toHaveBeenCalledWith("run-original", 120, 32, false);

    view.rerender(<UserTerminalPanel runID="run-next" sessionID=""
      onSession={onSession} />);
    await act(async () => {
      resolveStart({
        protocol_version: "desktop_user_terminal.v1",
        session_id: "terminal-stale",
        run_id: "run-original",
        state: "running",
        backend: "windows-conpty-user-v1",
        columns: 120,
        rows: 32,
        output_base_cursor: 0,
        output_next_cursor: 0,
        exit_code: 0,
        user_owned: true,
        agent_input_default: false,
        job_assigned_at_creation: true,
        kill_on_job_close: true,
        persistent: true,
        process_local: true,
        raw_output_persisted: false,
      });
    });

    await waitFor(() => {
      expect(terminalMocks.close).toHaveBeenCalledWith("terminal-stale");
    });
    expect(onSession).not.toHaveBeenCalled();
  });

  it("requires a visible confirmation before granting bounded Agent input", async () => {
    terminalMocks.agentEnabled = true;
    terminalMocks.getAgent.mockRejectedValueOnce(new Error("no active binding"));
    terminalMocks.get.mockResolvedValue({
      protocol_version: "desktop_user_terminal.v1",
      session_id: "terminal-1",
      run_id: "run-1",
      state: "running",
      backend: "windows-conpty-user-v1",
      columns: 120,
      rows: 32,
      output_base_cursor: 0,
      output_next_cursor: 0,
      exit_code: 0,
      user_owned: true,
      agent_input_default: false,
      job_assigned_at_creation: true,
      kill_on_job_close: true,
      persistent: true,
      process_local: true,
      raw_output_persisted: false,
    });
    terminalMocks.read.mockResolvedValue({
      protocol_version: "desktop_user_terminal.v1",
      session_id: "terminal-1",
      base_cursor: 0,
      next_cursor: 0,
      data_base64: "",
      data_bytes: 0,
      dropped: false,
      state: "running",
    });
    const binding = {
      protocol_version: "desktop_debug_terminal_agent_input.v1",
      binding_id: "terminal-input-binding-1",
      run_id: "run-1",
      terminal_session_id: "terminal-1",
      issued_at: new Date().toISOString(),
      expires_at: new Date(Date.now() + 300_000).toISOString(),
      process_local: true,
      token_exposed: false,
      raw_input_persisted: false,
    };
    terminalMocks.grantAgent.mockResolvedValue(binding);
    const confirm = vi.spyOn(globalThis, "confirm").mockReturnValue(true);
    render(<UserTerminalPanel runID="run-1" sessionID="terminal-1"
      onSession={vi.fn()} />);

    fireEvent.click(await screen.findByRole("button", { name: "允许 Agent · 5m" }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining("宿主文件与网络"));
    await waitFor(() => {
      expect(terminalMocks.grantAgent).toHaveBeenCalledWith(
        "run-1", "terminal-1", 300,
      );
    });
    fireEvent.click(await screen.findByRole("button", { name: "撤销 Agent" }));
    await waitFor(() => {
      expect(terminalMocks.revokeAgent).toHaveBeenCalledWith(binding.binding_id);
    });
    confirm.mockRestore();
  });
});
