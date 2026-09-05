import { act, renderHook, waitFor } from "@testing-library/react";
import { vi } from "vitest";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { EventView, RunEventPollView, RunEventStreamView } from "../api/types";
import { clearDesktopRunEventMemory, useRunEventStream } from "./use-run-event-stream";

vi.mock("../lib/desktop-bridge", () => ({ desktopRuntimeActive: () => true }));

describe("useRunEventStream Desktop polling", () => {
  beforeEach(() => {
    clearDesktopRunEventMemory();
  });

  it("uses bounded stream cursors instead of offset pages or unsupported Windows streaming", async () => {
    const first = frame(1, "opaque-1");
    const second = frame(2, "opaque-2");
    const pollRunEvents = vi.fn()
      .mockResolvedValueOnce(poll([first], "opaque-1", true))
      .mockResolvedValueOnce(poll([second], "opaque-2", false));
    const streamRunEvents = vi.fn();
    const client = { pollRunEvents, streamRunEvents } as unknown as CyberAgentClient;
    const { result, unmount } = renderHook(() => useRunEventStream(client, "run-desktop"));

    await waitFor(() => expect(result.current.frames.map((item) => item.sequence)).toEqual([1, 2]));
    expect(result.current.status).toBe("live");
    expect(streamRunEvents).not.toHaveBeenCalled();
    expect(pollRunEvents).toHaveBeenNthCalledWith(1, "run-desktop", "", 100, expect.any(AbortSignal));
    expect(pollRunEvents).toHaveBeenNthCalledWith(2, "run-desktop", "opaque-1", 100,
      expect.any(AbortSignal));
    unmount();
  });

  it("resumes from bounded module memory after a component remount without browser storage", async () => {
    const localStorageWrite = vi.spyOn(Storage.prototype, "setItem");
    const firstClient = {
      pollRunEvents: vi.fn().mockResolvedValue(poll([frame(1, "opaque-1")], "opaque-1", false)),
    } as unknown as CyberAgentClient;
    const firstHook = renderHook(() => useRunEventStream(firstClient, "run-desktop"));
    await waitFor(() => expect(firstHook.result.current.frames).toHaveLength(1));
    firstHook.unmount();

    const pollRunEvents = vi.fn().mockResolvedValue(poll([frame(2, "opaque-2")], "opaque-2", false));
    const secondClient = { pollRunEvents } as unknown as CyberAgentClient;
    const secondHook = renderHook(() => useRunEventStream(secondClient, "run-desktop"));
    await waitFor(() => expect(secondHook.result.current.frames.map((item) => item.sequence)).toEqual([1, 2]));

    expect(pollRunEvents).toHaveBeenCalledWith("run-desktop", "opaque-1", 100, expect.any(AbortSignal));
    expect(localStorageWrite).not.toHaveBeenCalled();
    secondHook.unmount();
    localStorageWrite.mockRestore();
  });

  it("drops one stale in-memory cursor and restarts exactly once from the durable beginning", async () => {
    const primeClient = {
      pollRunEvents: vi.fn().mockResolvedValue(poll([frame(1, "stale-cursor")], "stale-cursor", false)),
    } as unknown as CyberAgentClient;
    const prime = renderHook(() => useRunEventStream(primeClient, "run-desktop"));
    await waitFor(() => expect(prime.result.current.frames).toHaveLength(1));
    prime.unmount();

    const current = frame(1, "current-cursor");
    const pollRunEvents = vi.fn()
      .mockRejectedValueOnce(new APIRequestError("cursor mismatch", "INVALID_ARGUMENT", 400, "req-stale"))
      .mockResolvedValue(poll([current], "current-cursor", false));
    const client = { pollRunEvents } as unknown as CyberAgentClient;
    const hook = renderHook(() => useRunEventStream(client, "run-desktop"));

    await waitFor(() => expect(hook.result.current.frames).toEqual([current]));
    expect(pollRunEvents).toHaveBeenNthCalledWith(1, "run-desktop", "stale-cursor", 100,
      expect.any(AbortSignal));
    expect(pollRunEvents).toHaveBeenNthCalledWith(2, "run-desktop", "", 100, expect.any(AbortSignal));
    expect(hook.result.current.error).toBe("");
    hook.unmount();
  });
});

describe("useRunEventStream Desktop polling pace", () => {
  beforeEach(() => {
    clearDesktopRunEventMemory();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("backs off after an empty caught-up page instead of polling in a tight loop", async () => {
    const pollRunEvents = vi.fn().mockResolvedValue(poll([], "caught-up", false));
    const client = { pollRunEvents } as unknown as CyberAgentClient;
    const hook = renderHook(() => useRunEventStream(client, "run-empty"));

    await flushMicrotasks();
    expect(pollRunEvents).toHaveBeenCalledTimes(1);

    await flushMicrotasks();
    expect(pollRunEvents).toHaveBeenCalledTimes(1);

    await act(async () => vi.advanceTimersByTime(249));
    expect(pollRunEvents).toHaveBeenCalledTimes(1);

    await act(async () => vi.advanceTimersByTime(1));
    expect(pollRunEvents).toHaveBeenCalledTimes(2);
    hook.unmount();
  });

  it("drains a persistent backlog in bounded immediate bursts", async () => {
    let sequence = 0;
    const pollRunEvents = vi.fn().mockImplementation(() => {
      sequence++;
      return Promise.resolve(poll([frame(sequence, `cursor-${sequence}`)], `cursor-${sequence}`, true));
    });
    const client = { pollRunEvents } as unknown as CyberAgentClient;
    const hook = renderHook(() => useRunEventStream(client, "run-backlog"));

    await flushMicrotasks();
    expect(pollRunEvents).toHaveBeenCalledTimes(4);

    await flushMicrotasks();
    expect(pollRunEvents).toHaveBeenCalledTimes(4);

    await act(async () => vi.advanceTimersByTime(250));
    expect(pollRunEvents).toHaveBeenCalledTimes(8);
    hook.unmount();
  });

  it("aborts the previous poll and delay immediately when the Run changes", async () => {
    const signals = new Map<string, AbortSignal>();
    const pollRunEvents = vi.fn().mockImplementation((runID: string, _cursor: string, _limit: number,
      signal: AbortSignal) => {
      signals.set(runID, signal);
      return Promise.resolve(poll([], `${runID}-caught-up`, false));
    });
    const client = { pollRunEvents } as unknown as CyberAgentClient;
    const hook = renderHook(({ runID }) => useRunEventStream(client, runID), {
      initialProps: { runID: "run-old" },
    });

    await flushMicrotasks();
    expect(pollRunEvents).toHaveBeenCalledTimes(1);
    expect(signals.get("run-old")?.aborted).toBe(false);

    hook.rerender({ runID: "run-new" });
    await flushMicrotasks();
    expect(signals.get("run-old")?.aborted).toBe(true);
    expect(pollRunEvents.mock.calls.filter(([runID]) => runID === "run-new")).toHaveLength(1);

    await act(async () => vi.advanceTimersByTime(250));
    expect(pollRunEvents.mock.calls.filter(([runID]) => runID === "run-old")).toHaveLength(1);
    hook.unmount();
    expect(signals.get("run-new")?.aborted).toBe(true);
  });
});

async function flushMicrotasks(): Promise<void> {
  await act(async () => {
    for (let index = 0; index < 12; index++) {
      await Promise.resolve();
    }
  });
}

function poll(frames: RunEventStreamView[], cursor: string, hasMore: boolean): RunEventPollView {
  return {
    version: "run-event-poll.v1",
    run_id: "run-desktop",
    cursor,
    frames,
    has_more: hasMore,
  };
}

function frame(sequence: number, cursor: string): RunEventStreamView {
  return {
    version: "run-events.v1",
    request_id: `req-${sequence}`,
    run_id: "run-desktop",
    sequence,
    cursor,
    event: event(sequence),
  };
}

function event(sequence: number): EventView {
  return {
    version: "v1",
    event_id: `event-${sequence}`,
    mission_id: "mission-desktop",
    run_id: "run-desktop",
    sequence,
    type: "run.updated",
    source: "test",
    payload: {},
    created_at: "2026-07-18T00:00:00Z",
  };
}
