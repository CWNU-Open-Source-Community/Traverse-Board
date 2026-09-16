import type { ThreadRunRecoveryView, ThreadTranscriptItemView, ThreadTurnFailureReferenceView } from "../../api/types";
import { projectThreadNarrative } from "./narrative";
import { narrativeRepresentsFailedSubmission, recoveryRepresentsNotice } from "./failure-feedback";

const failure = (overrides: Partial<ThreadTranscriptItemView> = {}): ThreadTranscriptItemView => ({
  version: "thread_transcript.v1", id: "event-1", canonical_id: "event-1", run_id: "run-1", run_ordinal: 1,
  sequence: 42, source_ref: "handoff-1", activity_type: "message", stage: "blocked", kind: "harness_status",
  source: "harness", title: "审批后执行失败", detail: "审批已保存，但本轮模型没有答复。", status: "failed",
  verifiable: true, instruction_authorized: false, provisional: false, durable: true, created_at: "2026-09-10T12:00:00Z",
  ...overrides,
});
const recovery = { run_id: "run-1", handoff_operation_id: "handoff-1" } as ThreadRunRecoveryView;
const ref: ThreadTurnFailureReferenceView = { thread_id: "thread-1", run_id: "run-1", message_id: "handoff-1", event_sequence: 42 };

describe("exact failed-turn feedback", () => {
  it("combines only the exact recovery notice and preserves same-worded history and tool outcomes", () => {
    const transcript = [failure(), failure({ id: "old-event", canonical_id: "old-event", source_ref: "old-handoff", sequence: 40 }),
      failure({ id: "other-run", canonical_id: "other-run", run_id: "run-2" }),
      failure({ id: "tool-result", canonical_id: "tool-result", kind: "tool_call", activity_type: "execute", tool_name: "command_exec" }),
      failure({ id: "approved-edit", canonical_id: "approved-edit", kind: "file_change", activity_type: "edit", status: "approved" })];
    const before = JSON.stringify(transcript);
    const entries = projectThreadNarrative(transcript);
    const visible = entries.filter((entry) => !recoveryRepresentsNotice(entry, recovery));
    expect(entries.filter((entry) => recoveryRepresentsNotice(entry, recovery))).toHaveLength(1);
    expect(visible.filter((entry) => entry.kind === "notice")).toHaveLength(2);
    expect(visible.filter((entry) => entry.kind === "activity")).toHaveLength(2);
    expect(JSON.stringify(transcript)).toBe(before);
    expect(entries.filter((entry) => !recoveryRepresentsNotice(entry, undefined))).toEqual(entries);
  });

  it.each([{ source_ref: undefined }, { source: "model" }, { kind: "model_call" }, { provisional: true }, { durable: false },
    { status: "running" }, { stage: "result" }])("does not merge an unproven notice: %j", (change) => {
    const entries = projectThreadNarrative([failure(change as Partial<ThreadTranscriptItemView>)]);
    expect(entries.some((entry) => recoveryRepresentsNotice(entry, recovery))).toBe(false);
    expect(narrativeRepresentsFailedSubmission(entries, "thread-1", ref)).toBe(false);
  });

  it("requires the exact Thread, Run, message and event sequence for a failed submission", () => {
    const entries = projectThreadNarrative([failure()]);
    expect(narrativeRepresentsFailedSubmission(entries, "thread-1", ref)).toBe(true);
    for (const changed of [{ thread_id: "other-thread" }, { run_id: "other-run" }, { message_id: "other-message" }, { event_sequence: 41 }]) {
      expect(narrativeRepresentsFailedSubmission(entries, "thread-1", { ...ref, ...changed })).toBe(false);
    }
    expect(narrativeRepresentsFailedSubmission(entries, "thread-1", undefined)).toBe(false);
    expect(narrativeRepresentsFailedSubmission([], "thread-1", ref)).toBe(false);
  });

  it("preserves cancellation as a distinct confirmed outcome and does not deduplicate different failures by prose", () => {
    const entries = projectThreadNarrative([failure(), failure({ id: "event-2", canonical_id: "event-2", sequence: 43, source_ref: "handoff-2" }),
      failure({ id: "event-cancel", canonical_id: "event-cancel", sequence: 44, source_ref: "cancelled", status: "cancelled" })]);
    expect(entries).toHaveLength(3);
    expect(entries.filter((entry) => recoveryRepresentsNotice(entry, recovery))).toHaveLength(1);
  });
});
