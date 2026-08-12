import { renderHook, waitFor } from "@testing-library/react";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { PublicModelStreamSnapshot } from "../api/types";
import { usePublicModelStream } from "./use-public-model-stream";

const first: PublicModelStreamSnapshot = {
  version: "model_public_stream.v2",
  call: {
    run_id: "run-1", session_id: "sess-1", attempt_id: "attempt-1",
    model_attempt: 1, transport_attempt: 1, max_attempts: 3, protocol_repair: 0,
    tool_round: 0, provider: "deepseek", model: "deepseek-chat",
    started_at: "2026-08-08T00:00:00Z", stream_chunks: 1, stream_bytes: 16,
    cancel_requested: false,
  },
  revision: 1, content_kind: "tool_commentary", text: "First safe text", message_complete: false,
  provisional: true, updated_at: "2026-08-08T00:00:01Z",
};

describe("usePublicModelStream", () => {
  it("recovers from an initial 404 and converges on full revision snapshots", async () => {
    const second = { ...first, revision: 2, text: "Second safe text",
      updated_at: "2026-08-08T00:00:02Z" };
    const getPublicModelStream = vi.fn()
      .mockRejectedValueOnce(new APIRequestError("not active", "NOT_FOUND", 404))
      .mockResolvedValueOnce(second)
      .mockResolvedValue(first);
    const client = { getPublicModelStream } as unknown as CyberAgentClient;
    const { result, rerender } = renderHook(({ enabled }) =>
      usePublicModelStream(client, "run-1", enabled), {
      initialProps: { enabled: true },
    });

    await waitFor(() => expect(result.current.snapshot?.revision).toBe(2), { timeout: 1_500 });
    expect(result.current.snapshot?.text).toBe("Second safe text");
    expect(getPublicModelStream.mock.calls[0]?.[0]).toBe("run-1");
    expect(getPublicModelStream.mock.calls[0]?.[1]).toBeInstanceOf(AbortSignal);
    await new Promise((resolve) => window.setTimeout(resolve, 220));
    expect(result.current.snapshot?.revision).toBe(2);

    rerender({ enabled: false });
    await waitFor(() => expect(result.current.status).toBe("stopped"));
    expect(result.current.snapshot).toBeNull();
  });

  it("clears a finished provisional snapshot after a bounded 404 grace period", async () => {
    const getPublicModelStream = vi.fn()
      .mockResolvedValueOnce(first)
      .mockRejectedValue(new APIRequestError("not active", "NOT_FOUND", 404));
    const client = { getPublicModelStream } as unknown as CyberAgentClient;
    const { result } = renderHook(() => usePublicModelStream(client, "run-1", true));

    await waitFor(() => expect(result.current.snapshot?.revision).toBe(1));
    await waitFor(() => expect(result.current.status).toBe("finalizing"), { timeout: 1_000 });
    await waitFor(() => expect(result.current.snapshot).toBeNull(), { timeout: 2_000 });
    expect(result.current.status).toBe("waiting");
  });
});
