import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { CyberAgentClient } from "../api/client";
import type { ThreadView } from "../api/types";
import { ArchivedThreadsSettings } from "./archived-threads-settings";

function archivedThread(id: string, title: string, version: number): ThreadView {
  return {
    id,
    protocol_version: "thread.v1",
    workspace_id: `workspace-${id}`,
    mission_id: `mission-${id}`,
    title,
    status: "archived",
    last_run_id: `run-${id}`,
    version,
    composer_state: "unavailable",
    archived_at: "2026-08-28T02:00:00Z",
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T02:00:00Z",
  };
}

function renderArchivedSettings(threads: ThreadView[]) {
  const getPage = vi.fn().mockResolvedValue({
    items: threads,
    page: { limit: 50 },
    requestID: "request-archived-threads",
  });
  const transitionThread = vi.fn().mockImplementation((threadID: string,
    action: "archive" | "restore" | "delete") => Promise.resolve({
      version: "thread_lifecycle.v1",
      thread: {
        ...threads.find((thread) => thread.id === threadID),
        status: action === "restore" ? "active" : "deleted",
      },
      capability_grant: false,
    }));
  const client = { getPage, transitionThread } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={queryClient}>
    <ArchivedThreadsSettings client={client} />
  </QueryClientProvider>);
  return { getPage, transitionThread };
}

describe("ArchivedThreadsSettings", () => {
  it("lists archived Threads and filters them without changing lifecycle state", async () => {
    const controls = renderArchivedSettings([
      archivedThread("thread-alpha", "Alpha investigation", 7),
      archivedThread("thread-beta", "Beta delivery", 11),
    ]);

    expect(await screen.findByRole("heading", { name: "Archived chats" }))
      .toBeInTheDocument();
    expect(await screen.findByText("Alpha investigation")).toBeInTheDocument();
    expect(screen.getByText("Beta delivery")).toBeInTheDocument();
    expect(controls.getPage).toHaveBeenCalledWith("/threads",
      { limit: 50, status: "archived" }, "", expect.any(AbortSignal));

    fireEvent.change(screen.getByRole("searchbox", { name: "Search archived chats" }),
      { target: { value: "beta" } });
    expect(screen.queryByText("Alpha investigation")).not.toBeInTheDocument();
    expect(screen.getByText("Beta delivery")).toBeInTheDocument();
    expect(controls.transitionThread).not.toHaveBeenCalled();
  });

  it("restores and confirms deletion through canonical Thread transitions with exact versions",
    async () => {
      const controls = renderArchivedSettings([
        archivedThread("thread-restore", "Restore me", 7),
        archivedThread("thread-delete", "Delete me", 11),
      ]);
      expect(await screen.findByText("Restore me")).toBeInTheDocument();

      fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[0]);
      await waitFor(() => expect(controls.transitionThread).toHaveBeenCalledWith(
        "thread-restore", "restore",
        { version: "thread_lifecycle.v1", expected_version: 7 },
        expect.stringMatching(/^settings-thread-restore-/u),
      ));

      fireEvent.click(screen.getByRole("button", { name: "Delete Delete me" }));
      const dialog = screen.getByRole("dialog", { name: "Delete archived chat" });
      expect(within(dialog).getByText("Delete me")).toBeInTheDocument();
      expect(controls.transitionThread).toHaveBeenCalledTimes(1);
      fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

      await waitFor(() => expect(controls.transitionThread).toHaveBeenCalledWith(
        "thread-delete", "delete",
        { version: "thread_lifecycle.v1", expected_version: 11 },
        expect.stringMatching(/^settings-thread-delete-/u),
      ));
    });
});
