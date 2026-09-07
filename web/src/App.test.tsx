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

vi.mock("./components/resource-sidebar", () => ({
  ResourceSidebar: () => <aside data-testid="resource-sidebar" />,
}));
vi.mock("./components/run-workspace", () => ({
  RunWorkspace: ({ client, runID }: {
    client: {
      hasVerificationEvidence: boolean;
      hasGitHubReviewControl: boolean;
      hasWorkspaceCheckpointControl: boolean;
    };
    runID: string;
  }) => {
    const [draft, setDraft] = useState("");
    const [operationState, setOperationState] = useState("idle");
    return <div>
      <div data-testid="verification-capability">
        {String(client.hasVerificationEvidence)}
      </div>
      <div data-testid="github-review-capability">
        {String(client.hasGitHubReviewControl)}
      </div>
      <div data-testid="workspace-checkpoint-capability">
        {String(client.hasWorkspaceCheckpointControl)}
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
vi.mock("./components/thread-workspace", () => ({
  ThreadWorkspace: ({ threadID }: { threadID: string }) =>
    <div data-testid="thread-identity">{threadID}</div>,
}));
vi.mock("./components/desktop-skill-preview", () => ({
  DesktopSkillPreviewDialog: () => null,
}));
vi.mock("./components/model-availability-dialog", () => ({
  ModelAvailabilityDialog: () => null,
}));
vi.mock("./components/run-creation-dialog", () => ({ RunCreationDialog: () => null }));
vi.mock("./v2", () => ({
  V2WorkbenchEntry: () => <main data-testid="v2-workbench" />,
}));

describe("App capability wiring", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/legacy");
    submitRunDraft.mockClear();
    submitSessionDraft.mockClear();
    useConnectionStore.getState().disconnect();
    useConnectionStore.getState().connect("read-token", {
      status: "ok",
      api_version: "api.v1",
      app_version: "test",
      schema_version: 78,
    }, "control-token", {
      executionPermissionControlEnabled: true,
      githubReviewControlEnabled: true,
      operatorApprovalEnabled: true,
      verificationEvidenceEnabled: true,
      workspaceCheckpointControlEnabled: true,
    });
    useConnectionStore.getState().selectRun("run-test");
  });

  afterEach(() => {
    useConnectionStore.getState().disconnect();
    window.history.replaceState({}, "", "/");
    vi.unstubAllGlobals();
  });

  it("propagates verification evidence authority into the API client", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    expect(screen.getByTestId("verification-capability")).toHaveTextContent("true");
    expect(screen.getByTestId("github-review-capability")).toHaveTextContent("true");
    expect(screen.getByTestId("workspace-checkpoint-capability")).toHaveTextContent("true");
    expect(document.querySelector(".prayu-shell.workspace-mode")).toBeInTheDocument();
    expect(document.querySelector(".prayu-conversation-panel")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(document.querySelector(".prayu-shell.settings-mode")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "常规" })).toHaveClass("active");
  });

  it("starts with the sidebar collapsed in a narrow workspace and allows explicit reveal", () => {
    vi.stubGlobal("matchMedia", vi.fn(() => ({
      matches: true,
      media: "(max-width: 760px)",
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })));
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));

    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    expect(document.querySelector(".shell-body")).toHaveClass("sidebar-hidden");
    expect(screen.queryByTestId("resource-sidebar")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "显示或隐藏侧栏" }));
    expect(screen.getByTestId("resource-sidebar")).toBeInTheDocument();
  });

  it("projects Docker execution into the settings capability surface", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    useConnectionStore.getState().connect("read-token", {
      status: "ok",
      api_version: "api.v1",
      app_version: "test",
      schema_version: 78,
    }, "control-token", { dockerExecutionEnabled: true });
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    fireEvent.click(screen.getByRole("button", { name: "个人资料" }));
    expect(screen.getByRole("img", { name: "Docker 沙箱: 启用" })).toBeInTheDocument();
  });

  it("never targets a stale Thread while viewing Run diagnostics", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    useConnectionStore.getState().selectThread("thread-stale");
    useConnectionStore.getState().selectRun("run-diagnostic");
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    fireEvent.click(screen.getByRole("button", { name: "权限" }));

    expect(screen.getByText("从侧栏打开一个 Thread")).toBeInTheDocument();
    expect(screen.queryByText(/Thread thread-stale/)).not.toBeInTheDocument();
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

  it("restores and navigates the canonical Thread URL projection", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    useConnectionStore.getState().disconnect();
    useConnectionStore.getState().connect("read-token", {
      status: "ok", api_version: "api.v1", app_version: "test", schema_version: 129,
    }, "control-token", { threadControlEnabled: true });
    window.history.replaceState({}, "", "/legacy/threads/thread-from-url");

    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    expect(screen.getByTestId("thread-identity")).toHaveTextContent("thread-from-url");
    expect(useConnectionStore.getState().resourceKind).toBe("thread");
    expect(useConnectionStore.getState().selectedThreadID).toBe("thread-from-url");

    act(() => useConnectionStore.getState().selectThread(""));
    expect(useConnectionStore.getState().selectedThreadID).toBe("");
    expect(window.location.pathname).toBe("/legacy");

    act(() => {
      window.history.pushState({}, "", "/legacy/threads/thread-from-history");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(screen.getByTestId("thread-identity")).toHaveTextContent("thread-from-history");
    expect(useConnectionStore.getState().selectedThreadID).toBe("thread-from-history");
    expect(window.location.pathname).toBe("/legacy/threads/thread-from-history");

    act(() => {
      window.history.pushState({}, "", "/legacy");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(useConnectionStore.getState().resourceKind).toBe("thread");
    expect(useConnectionStore.getState().selectedThreadID).toBe("");
    expect(window.location.pathname).toBe("/legacy");
  });

  it("does not treat the rejected legacy query parameter as a UI route", () => {
    window.history.replaceState({}, "", "/?legacy=1");

    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);

    expect(screen.getByTestId("v2-workbench")).toBeInTheDocument();
    expect(screen.queryByTestId("resource-sidebar")).not.toBeInTheDocument();
  });

  it("switches from v2 to the legacy Inspector route without losing the in-memory connection", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    window.history.replaceState({}, "", "/");
    render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);
    expect(screen.getByTestId("v2-workbench")).toBeInTheDocument();

    act(() => {
      window.history.pushState({}, "", "/legacy/threads/thread-from-v2");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });

    expect(screen.getByTestId("thread-identity")).toHaveTextContent("thread-from-v2");
    expect(useConnectionStore.getState().token).toBe("read-token");
    expect(useConnectionStore.getState().controlToken).toBe("control-token");
    expect(screen.queryByText("连接本地控制面")).not.toBeInTheDocument();
  });
});
