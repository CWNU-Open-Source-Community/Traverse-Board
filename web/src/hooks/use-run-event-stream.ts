import { useEffect, useState } from "react";
import { APIRequestError, type APIClient } from "../api/client";
import type { RunEventStreamView } from "../api/types";
import { desktopRuntimeActive } from "../lib/desktop-bridge";

export type StreamStatus = "connecting" | "live" | "reconnecting" | "stopped";

const reconnectDelayMs = 1_000;
const desktopCaughtUpDelayMs = 250;
const maxImmediateDesktopPages = 4;
const maxLiveFrames = 500;
const maxRememberedRuns = 16;

interface RememberedDesktopRun {
  cursor: string;
  frames: RunEventStreamView[];
}

const rememberedDesktopRuns = new Map<string, RememberedDesktopRun>();

export function clearDesktopRunEventMemory(runID = ""): void {
  if (runID) {
    rememberedDesktopRuns.delete(runID);
    return;
  }
  rememberedDesktopRuns.clear();
}

function rememberDesktopRun(runID: string, cursor: string, frames: RunEventStreamView[]): void {
  const remembered = rememberedDesktopRuns.get(runID);
  if (remembered?.cursor === cursor && remembered.frames === frames) return;
  rememberedDesktopRuns.delete(runID);
  rememberedDesktopRuns.set(runID, { cursor, frames });
  while (rememberedDesktopRuns.size > maxRememberedRuns) {
    const oldest = rememberedDesktopRuns.keys().next().value as string | undefined;
    if (!oldest) {
      break;
    }
    rememberedDesktopRuns.delete(oldest);
  }
}

function mergeFrames(current: RunEventStreamView[], incoming: RunEventStreamView[]): RunEventStreamView[] {
  if (incoming.length === 0) return current;
  const sequences = new Set(current.map((frame) => frame.sequence));
  const oldestRetained = current.length === maxLiveFrames ? current[0]!.sequence : 0;
  const added: RunEventStreamView[] = [];
  for (const frame of incoming) {
    // Persisted event sequences are immutable. Replayed envelopes can carry a
    // different request ID or cursor without changing the visible event.
    if (frame.sequence < oldestRetained || sequences.has(frame.sequence)) continue;
    sequences.add(frame.sequence);
    added.push(frame);
  }
  if (added.length === 0) return current;
  const merged = [...current, ...added];
  if (current.length && added[0]!.sequence < current.at(-1)!.sequence) {
    merged.sort((left, right) => left.sequence - right.sequence);
  }
  return merged.slice(-maxLiveFrames);
}

function delay(signal: AbortSignal, delayMs = reconnectDelayMs): Promise<void> {
  if (signal.aborted) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    const onAbort = () => {
      window.clearTimeout(timeout);
      resolve();
    };
    const timeout = window.setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, delayMs);
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

export function useRunEventStream(client: APIClient, runID: string) {
  const [frames, setFrames] = useState<RunEventStreamView[]>([]);
  const [status, setStatus] = useState<StreamStatus>("stopped");
  const [error, setError] = useState("");

  useEffect(() => {
    setError("");
    if (!runID) {
      setFrames((current) => current.length ? [] : current);
      setStatus("stopped");
      return;
    }

    const controller = new AbortController();
    let connectionStatus: StreamStatus | undefined;
    let connectionError = "";
    const publishConnection = (nextStatus: StreamStatus, nextError = "") => {
      if (connectionStatus !== nextStatus) {
        connectionStatus = nextStatus;
        setStatus(nextStatus);
      }
      if (connectionError !== nextError) {
        connectionError = nextError;
        setError(nextError);
      }
    };
    if (desktopRuntimeActive()) {
      const remembered = rememberedDesktopRuns.get(runID);
      let cursor = remembered?.cursor ?? "";
      let currentFrames = remembered?.frames ?? [];
      let cursorResetUsed = false;
      setFrames(currentFrames);
      const poll = async () => {
        publishConnection("connecting");
        let immediatePages = 0;
        while (!controller.signal.aborted) {
          try {
            const page = await client.pollRunEvents(
              runID,
              cursor,
              100,
              controller.signal,
            );
            if (controller.signal.aborted) {
              return;
            }
            cursor = page.cursor;
            if (page.frames.length) {
              const merged = mergeFrames(currentFrames, page.frames);
              if (merged !== currentFrames) {
                currentFrames = merged;
                setFrames(currentFrames);
              }
            }
            rememberDesktopRun(runID, cursor, currentFrames);
            publishConnection("live");
            if (page.has_more) {
              immediatePages++;
              if (immediatePages < maxImmediateDesktopPages) {
                continue;
              }
            }
            immediatePages = 0;
          } catch (caught) {
            if (controller.signal.aborted) {
              return;
            }
            if (caught instanceof APIRequestError && caught.status === 400 && cursor !== "" &&
              !cursorResetUsed) {
              cursorResetUsed = true;
              cursor = "";
              currentFrames = [];
              clearDesktopRunEventMemory(runID);
              setFrames([]);
              continue;
            }
            const message = caught instanceof Error ? caught.message : "Event polling disconnected";
            if (caught instanceof APIRequestError && [400, 401, 403, 404].includes(caught.status)) {
              publishConnection("stopped", message);
              return;
            }
            publishConnection("reconnecting", message);
            await delay(controller.signal);
            continue;
          }
          await delay(controller.signal, desktopCaughtUpDelayMs);
        }
      };
      void poll();
      return () => {
        controller.abort();
      };
    }
    let currentFrames: RunEventStreamView[] = [];
    setFrames((current) => current.length ? [] : current);
    let cursor = "";
    const run = async () => {
      publishConnection("connecting");
      while (!controller.signal.aborted) {
        try {
          await client.streamRunEvents(runID, {
            cursor,
            signal: controller.signal,
            onOpen: () => {
              if (!controller.signal.aborted) publishConnection("live");
            },
            onFrame: (frame) => {
              if (controller.signal.aborted) return;
              cursor = frame.cursor;
              publishConnection("live");
              const merged = mergeFrames(currentFrames, [frame]);
              if (merged !== currentFrames) {
                currentFrames = merged;
                setFrames(currentFrames);
              }
            },
          });
          if (!controller.signal.aborted) {
            publishConnection("reconnecting");
            await delay(controller.signal);
          }
        } catch (caught) {
          if (controller.signal.aborted) {
            return;
          }
          const message = caught instanceof Error ? caught.message : "Event stream disconnected";
          if (caught instanceof APIRequestError && [400, 401, 403, 404].includes(caught.status)) {
            publishConnection("stopped", message);
            return;
          }
          publishConnection("reconnecting", message);
          await delay(controller.signal);
        }
      }
    };
    void run();

    return () => {
      controller.abort();
    };
  }, [client, runID]);

  return { frames, status, error };
}
