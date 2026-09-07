import { act, renderHook, waitFor } from "@testing-library/react";
import type { CyberAgentClient } from "../api/client";
import type { PublicModelStreamSnapshot } from "../api/types";
import { usePublicModelStream } from "./use-public-model-stream";

const first: PublicModelStreamSnapshot = {
  version: "model_public_stream.v3",
  call: {
    run_id: "run-1", session_id: "sess-1", attempt_id: "attempt-1",
    model_attempt: 1, transport_attempt: 1, max_attempts: 3, protocol_repair: 0,
    tool_round: 0, provider: "deepseek", model: "deepseek-chat",
    started_at: "2026-08-08T00:00:00Z", stream_chunks: 1, stream_bytes: 16,
    cancel_requested: false,
  },
  revision: 1, response_id: "response-1", event_sequence: 3, items: [],
  content_kind: "tool_commentary", text: "First safe text", message_complete: false,
  provisional: true, updated_at: "2026-08-08T00:00:01Z",
};

describe("usePublicModelStream", () => {
  it("uses a bounded idle cadence instead of probing an absent call at live-stream speed", async () => {
    vi.useFakeTimers();
    try {
      const pollPublicModelStream = vi.fn().mockResolvedValue(null);
      const client = { pollPublicModelStream } as unknown as CyberAgentClient;
      const hook = renderHook(() => usePublicModelStream(client, "run-1", true));

      await act(async () => { await Promise.resolve(); });
      expect(pollPublicModelStream).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(999); });
      expect(pollPublicModelStream).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(pollPublicModelStream).toHaveBeenCalledTimes(2);
      hook.unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it("recovers from an initial idle projection and converges on full revision snapshots", async () => {
    const second = { ...first, revision: 2, text: "Second safe text",
      updated_at: "2026-08-08T00:00:02Z" };
    const pollPublicModelStream = vi.fn()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(second)
      .mockResolvedValue(first);
    const client = { pollPublicModelStream } as unknown as CyberAgentClient;
    const { result, rerender } = renderHook(({ enabled }) =>
      usePublicModelStream(client, "run-1", enabled), {
      initialProps: { enabled: true },
    });

    await waitFor(() => expect(result.current.snapshot?.revision).toBe(2), { timeout: 1_500 });
    expect(result.current.snapshot?.text).toBe("Second safe text");
    expect(pollPublicModelStream.mock.calls[0]?.[0]).toBe("run-1");
    expect(pollPublicModelStream.mock.calls[0]?.[1]).toBeInstanceOf(AbortSignal);
    await new Promise((resolve) => window.setTimeout(resolve, 220));
    expect(result.current.snapshot?.revision).toBe(2);

    rerender({ enabled: false });
    await waitFor(() => expect(result.current.status).toBe("stopped"));
    expect(result.current.snapshot).toBeNull();
  });

  it("clears a finished provisional snapshot after a bounded 404 grace period", async () => {
    const pollPublicModelStream = vi.fn()
      .mockResolvedValueOnce(first)
      .mockResolvedValue(null);
    const client = { pollPublicModelStream } as unknown as CyberAgentClient;
    const { result } = renderHook(() => usePublicModelStream(client, "run-1", true));

    await waitFor(() => expect(result.current.snapshot?.revision).toBe(1));
    await waitFor(() => expect(result.current.status).toBe("finalizing"), { timeout: 1_000 });
    await waitFor(() => expect(result.current.snapshot).toBeNull(), { timeout: 2_000 });
    expect(result.current.status).toBe("waiting");
  });
});
