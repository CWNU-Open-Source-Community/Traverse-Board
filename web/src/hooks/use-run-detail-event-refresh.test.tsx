import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { EventView, RunEventStreamView } from "../api/types";
import { useRunDetailEventRefresh } from "./use-run-detail-event-refresh";

describe("useRunDetailEventRefresh", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("refreshes the exact authoritative Run detail after a durable event", async () => {
    const queryClient = new QueryClient();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
    renderHook(() => useRunDetailEventRefresh("run-1", frame(1, "run.status_changed")),
      { wrapper });

    await act(async () => vi.advanceTimersByTime(100));

    expect(invalidate).toHaveBeenCalledTimes(1);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["run", "run-1"], exact: true });
  });

  it("coalesces event bursts and ignores transient model deltas", async () => {
    const queryClient = new QueryClient();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
    const hook = renderHook(({ latest }) => useRunDetailEventRefresh("run-1", latest), {
      initialProps: { latest: frame(1, "run.status_changed") }, wrapper,
    });

    hook.rerender({ latest: frame(2, "execution.lease_acquired") });
    hook.rerender({ latest: frame(3, "model.delta") });
    await act(async () => vi.advanceTimersByTime(100));
    expect(invalidate).toHaveBeenCalledTimes(1);

    hook.rerender({ latest: frame(4, "model.delta") });
    await act(async () => vi.advanceTimersByTime(200));
    expect(invalidate).toHaveBeenCalledTimes(1);
  });
});

function frame(sequence: number, type: string): RunEventStreamView {
  return {
    version: "run-events.v1",
    request_id: `request-${sequence}`,
    run_id: "run-1",
    sequence,
    cursor: `cursor-${sequence}`,
    event: event(sequence, type),
  };
}

function event(sequence: number, type: string): EventView {
  return {
    version: "v1",
    event_id: `event-${sequence}`,
    mission_id: "mission-1",
    run_id: "run-1",
    sequence,
    type,
    source: "test",
    payload: {},
    created_at: "2026-08-12T00:00:00Z",
  };
}
