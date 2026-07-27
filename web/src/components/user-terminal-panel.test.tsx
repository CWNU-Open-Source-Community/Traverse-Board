import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";
import { UserTerminalPanel } from "./user-terminal-panel";

const terminalMocks = vi.hoisted(() => ({
  close: vi.fn(async () => undefined),
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
  desktopErrorMessage: (value: unknown) => String(value),
  desktopUserTerminalEnabled: () => true,
  getDesktopUserTerminal: vi.fn(),
  readDesktopUserTerminal: vi.fn(),
  resizeDesktopUserTerminal: vi.fn(async () => undefined),
  startDesktopUserTerminal: terminalMocks.start,
  writeDesktopUserTerminal: vi.fn(async () => ({
    protocol_version: "desktop_user_terminal.v1",
    session_id: "unused",
    bytes_written: 1,
  })),
}));

describe("UserTerminalPanel", () => {
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
});
