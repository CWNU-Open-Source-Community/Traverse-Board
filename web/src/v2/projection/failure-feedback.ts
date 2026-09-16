import type { ThreadRunRecoveryView, ThreadTurnFailureReferenceView } from "../../api/types";
import type { NarrativeEntry } from "./narrative";

// These identities come from the durable outcome, never from equal wording,
// timestamps, the most recent Run failure, or a command's individual exit code.
export function recoveryRepresentsNotice(entry: NarrativeEntry, recovery: ThreadRunRecoveryView | undefined): boolean {
  return entry.kind === "notice" && Boolean(recovery && entry.failureOrigin &&
    entry.failureOrigin.runId === recovery.run_id &&
    entry.failureOrigin.sourceRef === recovery.handoff_operation_id);
}

export function narrativeRepresentsFailedSubmission(entries: NarrativeEntry[], threadID: string,
  failure: ThreadTurnFailureReferenceView | undefined): boolean {
  return Boolean(failure && failure.thread_id === threadID && entries.some((entry) =>
    entry.kind === "notice" && entry.failureOrigin?.runId === failure.run_id &&
    entry.failureOrigin.sourceRef === failure.message_id && entry.failureOrigin.eventSequence === failure.event_sequence));
}
