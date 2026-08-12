import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import App from "./App";
import { useConnectionStore } from "./state/connection";

const submitRunDraft = vi.hoisted(() => vi.fn());
const submitSessionDraft = vi.hoisted(() => vi.fn());

vi.mock("./lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

vi.mock("./components/resource-sidebar", () => ({ ResourceSidebar: () => null }));
vi.mock("./components/run-workspace", () => ({
  RunWorkspace: ({ client, runID }: {
    client: { hasVerificationEvidence: boolean };
    runID: string;
  }) => {
    const [draft, setDraft] = useState("");
    const [operationState, setOperationState] = useState("idle");
    return <div>
      <div data-testid="verification-capability">
        {String(client.hasVerificationEvidence)}
      </div>
      <div data-testid="run-identity">{runID}</div>
      <div data-testid="run-operation-state">{operationState}</div>
      <textarea aria-label="Run-scoped draft" onChange={(event) => setDraft(event.target.value)}
        value={draft} />
      <button onClick={() => setOperationState("uncertain")} type="button">
        Mark operation uncertain
      </button>
      <button disabled={!draft} onClick={() => submitRunDraft(runID, draft)} type="button">
        Submit run draft
      </button>
    </div>;
  },
}));
vi.mock("./components/session-workspace", () => ({
  SessionWorkspace: ({ sessionID }: { sessionID: string }) => {
    const [draft, setDraft] = useState("");
    const [operationState, setOperationState] = useState("idle");
    return <div>
      <div data-testid="session-identity">{sessionID}</div>
      <div data-testid="session-operation-state">{operationState}</div>
      <textarea aria-label="Session-scoped draft"
        onChange={(event) => setDraft(event.target.value)} value={draft} />
      <button onClick={() => setOperationState("uncertain")} type="button">
        Mark Session operation uncertain
      </button>
      <button disabled={!draft} onClick={() => submitSessionDraft(sessionID, draft)} type="button">
        Submit Session draft
      </button>
    </div>;
  },
}));
vi.mock("./components/desktop-skill-preview", () => ({
  DesktopSkillPreviewDialog: () => null,
}));
vi.mock("./components/model-availability-dialog", () => ({
  ModelAvailabilityDialog: () => null,
}));
vi.mock("./components/run-creation-dialog", () => ({ RunCreationDialog: () => null }));

describe("App capability wiring", () => {
  beforeEach(() => {
    submitRunDraft.mockClear();
    submitSessionDraft.mockClear();
    useConnectionStore.getState().disconnect();
    useConnectionStore.getState().connect("read-token", {
      status: "ok",
      api_version: "api.v1",
      app_version: "test",
      schema_version: 78,
    }, "control-token", {
      verificationEvidenceEnabled: true,
    });
    useConnectionStore.getState().selectRun("run-test");
  });

  afterEach(() => {
    useConnectionStore.getState().disconnect();
    vi.unstubAllGlobals();
  });

  it("propagates verification evidence authority into the API client", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    expect(screen.getByTestId("verification-capability")).toHaveTextContent("true");
    expect(document.querySelector(".prayu-shell.workspace-mode")).toBeInTheDocument();
    expect(document.querySelector(".prayu-conversation-panel")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(document.querySelector(".prayu-shell.settings-mode")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "常规" })).toHaveClass("active");
  });

  it("remounts Run-scoped drafts and operation state when the selected Run changes", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    fireEvent.change(screen.getByLabelText("Run-scoped draft"), {
      target: { value: "private instructions for run A" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Mark operation uncertain" }));
    expect(screen.getByTestId("run-operation-state")).toHaveTextContent("uncertain");

    act(() => useConnectionStore.getState().selectRun("run-b"));

    expect(screen.getByTestId("run-identity")).toHaveTextContent("run-b");
    expect(screen.getByLabelText("Run-scoped draft")).toHaveValue("");
    expect(screen.getByTestId("run-operation-state")).toHaveTextContent("idle");
    expect(screen.getByRole("button", { name: "Submit run draft" })).toBeDisabled();
    expect(submitRunDraft).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Run-scoped draft"), {
      target: { value: "instructions for run B" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit run draft" }));
    expect(submitRunDraft).toHaveBeenCalledOnce();
    expect(submitRunDraft).toHaveBeenCalledWith("run-b", "instructions for run B");
  });

  it("remounts Session-scoped drafts and operation state when the selected Session changes", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    useConnectionStore.getState().selectSession("session-a");
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    fireEvent.change(screen.getByLabelText("Session-scoped draft"), {
      target: { value: "private instructions for session A" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Mark Session operation uncertain" }));
    expect(screen.getByTestId("session-operation-state")).toHaveTextContent("uncertain");

    act(() => useConnectionStore.getState().selectSession("session-b"));

    expect(screen.getByTestId("session-identity")).toHaveTextContent("session-b");
    expect(screen.getByLabelText("Session-scoped draft")).toHaveValue("");
    expect(screen.getByTestId("session-operation-state")).toHaveTextContent("idle");
    expect(screen.getByRole("button", { name: "Submit Session draft" })).toBeDisabled();
    expect(submitSessionDraft).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Session-scoped draft"), {
      target: { value: "instructions for session B" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit Session draft" }));
    expect(submitSessionDraft).toHaveBeenCalledOnce();
    expect(submitSessionDraft).toHaveBeenCalledWith("session-b", "instructions for session B");
  });
});
