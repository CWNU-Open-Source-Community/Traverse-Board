import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { RunEventStreamView } from "../api/types";

const runDetailRefreshDelayMs = 100;

export function useRunDetailEventRefresh(
  runID: string,
  latestFrame: RunEventStreamView | undefined,
): void {
  const queryClient = useQueryClient();
  const refreshTimer = useRef<number | null>(null);

  useEffect(() => {
    if (!runID || !latestFrame || latestFrame.event.type === "model.delta" ||
      refreshTimer.current !== null) {
      return;
    }
    refreshTimer.current = window.setTimeout(() => {
      refreshTimer.current = null;
      void queryClient.invalidateQueries({ queryKey: ["run", runID], exact: true });
    }, runDetailRefreshDelayMs);
  }, [latestFrame, queryClient, runID]);

  useEffect(() => () => {
    if (refreshTimer.current !== null) {
      window.clearTimeout(refreshTimer.current);
      refreshTimer.current = null;
    }
  }, [runID]);
}
