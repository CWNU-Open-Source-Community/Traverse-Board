import { useEffect, useState } from "react";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { PublicModelStreamSnapshot } from "../api/types";

export type PublicModelStreamStatus = "waiting" | "live" | "finalizing" |
  "reconnecting" | "stopped";

export interface PublicModelStreamState {
  error: string;
  snapshot: PublicModelStreamSnapshot | null;
  status: PublicModelStreamStatus;
}

const livePollDelayMs = 150;
const reconnectDelayMs = 500;

function delay(milliseconds: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    const finish = () => {
      window.clearTimeout(timeout);
      signal.removeEventListener("abort", finish);
      resolve();
    };
    const timeout = window.setTimeout(finish, milliseconds);
    signal.addEventListener("abort", finish, { once: true });
  });
}

function callIdentity(snapshot: PublicModelStreamSnapshot): string {
  return `${snapshot.call.attempt_id}:${snapshot.call.model_attempt}`;
}

export function usePublicModelStream(client: CyberAgentClient, runID: string,
  enabled: boolean): PublicModelStreamState {
  const [snapshot, setSnapshot] = useState<PublicModelStreamSnapshot | null>(null);
  const [status, setStatus] = useState<PublicModelStreamStatus>("stopped");
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    setSnapshot(null);
    setError("");
    if (!enabled || !runID) {
      setStatus("stopped");
      return () => controller.abort();
    }

    let current: PublicModelStreamSnapshot | null = null;
    const poll = async () => {
      setStatus("waiting");
      while (!controller.signal.aborted) {
        let wait = livePollDelayMs;
        try {
          const next = await client.getPublicModelStream(runID, controller.signal);
          if (controller.signal.aborted) return;
          const replace = current === null || callIdentity(next) !== callIdentity(current) ||
            next.revision > current.revision;
          if (replace) {
            current = next;
            setSnapshot(next);
          }
          setStatus("live");
          setError("");
        } catch (caught) {
          if (controller.signal.aborted) return;
          if (caught instanceof APIRequestError && caught.status === 404) {
            setStatus(current ? "finalizing" : "waiting");
            setError("");
            wait = reconnectDelayMs;
          } else if (caught instanceof APIRequestError &&
            [400, 401, 403].includes(caught.status)) {
            setStatus("stopped");
            setError(caught.message);
            return;
          } else {
            setStatus("reconnecting");
            setError(caught instanceof Error ? caught.message : "Model stream disconnected");
            wait = reconnectDelayMs;
          }
        }
        await delay(wait, controller.signal);
      }
    };
    void poll();
    return () => controller.abort();
  }, [client, enabled, runID]);

  return { error, snapshot, status };
}
