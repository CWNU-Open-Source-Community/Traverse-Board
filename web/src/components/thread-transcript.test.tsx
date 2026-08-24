import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { PublicModelStreamSnapshot, ThreadTranscriptItemView } from "../api/types";
import { mergeThreadTranscriptItems, ThreadTranscript } from "./thread-transcript";

const createdAt = "2026-08-24T00:00:00Z";

function item(overrides: Partial<ThreadTranscriptItemView> = {}): ThreadTranscriptItemView {
  return {
    version: "thread_transcript.v1", id: "event-1", canonical_id: "event-1",
    run_id: "run-1", run_ordinal: 1, sequence: 1, activity_type: "checkpoint",
    stage: "result", kind: "harness_status", source: "harness", title: "Recorded fact",
    status: "completed", verifiable: true, instruction_authorized: false,
    provisional: false, durable: true, created_at: createdAt, ...overrides,
  };
}

function snapshot(toolName = "read_file"): PublicModelStreamSnapshot {
  return {
    version: "model_public_stream.v3",
    call: { run_id: "run-1", session_id: "session-1", attempt_id: "attempt-1",
      model_attempt: 1, transport_attempt: 1, max_attempts: 1, protocol_repair: 0,
      tool_round: 0, provider: "test", model: "test", started_at: createdAt,
      stream_chunks: 1, stream_bytes: 10, cancel_requested: false },
    revision: 2, response_id: "resp-1", event_sequence: 4,
    items: [{ response_id: "resp-1", id: "item-tool", type: "tool_call",
      status: "ready_for_validation", call_id: "call-tool", tool_name: toolName,
      argument_bytes: 128, provisional: true, durable: false }],
    content_kind: "tool_commentary", text: "", message_complete: false,
    provisional: true, updated_at: "2026-08-24T00:00:01Z",
  };
}

describe("ThreadTranscript", () => {
  it("deterministically replaces provisional tool and Composer items with durable identities", () => {
    const durable = item({ id: "durable-tool", canonical_id: "item-tool",
      stream_item_id: "item-tool", stream_call_id: "call-tool", kind: "tool_call",
      activity_type: "read", stage: "arguments_ready", title: "Arguments ready" });
    const pending = item({ id: "pending", canonical_id: "item-tool", source: "operator",
      kind: "operator_input", activity_type: "message", provisional: true, durable: false });
    const merged = mergeThreadTranscriptItems([durable], [pending], snapshot(), "live");
    expect(merged).toHaveLength(1);
    expect(merged[0]).toEqual(durable);
  });

  it("classifies live tools by exact structured names instead of natural-language parsing", () => {
    const read = mergeThreadTranscriptItems([], [], snapshot("read_file"), "live");
    const search = mergeThreadTranscriptItems([], [], snapshot("workspace_grep"), "live");
    const edit = mergeThreadTranscriptItems([], [], snapshot("workspace_apply"), "live");
    const verify = mergeThreadTranscriptItems([], [], snapshot("code_diagnostics"), "live");
    const checkpoint = mergeThreadTranscriptItems([], [], snapshot("work_item_create"), "live");
    const lookalike = mergeThreadTranscriptItems([], [], snapshot("please_read_everything"), "live");
    expect(read[0]).toMatchObject({ activity_type: "read", stage: "arguments_ready",
      canonical_id: "item-tool" });
    expect(search[0]).toMatchObject({ activity_type: "search" });
    expect(edit[0]).toMatchObject({ activity_type: "edit" });
    expect(verify[0]).toMatchObject({ activity_type: "verify" });
    expect(checkpoint[0]).toMatchObject({ activity_type: "checkpoint" });
    expect(lookalike[0]).toMatchObject({ activity_type: "execute" });
  });

  it("keeps confirmed cross-Run order while appending provisional content at the tail", () => {
    const confirmed = [
      item({ id: "run-2-message", canonical_id: "run-2-message", run_id: "run-2",
        run_ordinal: 2, sequence: 3 }),
      item({ id: "run-1-message", canonical_id: "run-1-message", sequence: 9 }),
    ];
    const pending = item({ id: "pending", canonical_id: "pending", source: "operator",
      run_id: "run-2", run_ordinal: 2, kind: "operator_input", activity_type: "message",
      sequence: Number.MAX_SAFE_INTEGER,
      provisional: true, durable: false });
    expect(mergeThreadTranscriptItems(confirmed, [pending], null, "stopped")
      .map((entry) => entry.id)).toEqual(["run-1-message", "run-2-message", "pending"]);
  });

  it("keeps a live successor at the tail before that Run has a projected durable item", () => {
    const confirmed = [item({ id: "run-2-message", canonical_id: "run-2-message",
      run_id: "run-2", run_ordinal: 2, sequence: 4 })];
    const live = snapshot();
    live.call.run_id = "run-3";
    live.items = [];
    live.text = "successor update";
    const merged = mergeThreadTranscriptItems(confirmed, [], live, "live");
    expect(merged.map((entry) => entry.id)).toEqual([
      "run-2-message", "live-message:attempt-1:1:1",
    ]);
    expect(merged[1]).toMatchObject({ run_id: "run-3", run_ordinal: 3 });
  });

  it("bounds the DOM for a 10,000-item fixture and exposes source labels without color alone", () => {
    const items = Array.from({ length: 10_000 }, (_, index) => item({
      id: `event-${index + 1}`, canonical_id: `event-${index + 1}`, sequence: index + 1,
      title: `Fact ${index + 1}`,
    }));
    const { container } = render(<ThreadTranscript durableItems={items} hasOlder={false}
      isFetchingOlder={false} onLoadOlder={() => undefined} />);
    expect(container.querySelectorAll("[data-transcript-id]").length).toBeLessThanOrEqual(80);
    expect(screen.getAllByText("Harness fact").length).toBeGreaterThan(0);
    expect(screen.getByTestId("thread-transcript-viewport")).toHaveAttribute("tabindex", "0");
  });

  it("collapses long public content into a native keyboard-focusable disclosure", async () => {
    const long = item({ id: "model-long", canonical_id: "model-long", source: "model",
      kind: "model_update", activity_type: "message", verifiable: false,
      detail: "公开结果 ".repeat(180) });
    const { container } = render(<ThreadTranscript durableItems={[long]} hasOlder={false}
      isFetchingOlder={false} onLoadOlder={() => undefined} />);
    const summary = screen.getByText("Expand full content");
    const disclosure = container.querySelector("details.thread-transcript-disclosure");
    expect(disclosure).not.toHaveAttribute("open");
    summary.focus();
    expect(summary).toHaveFocus();
    await userEvent.click(summary);
    expect(disclosure).toHaveAttribute("open");
    expect(screen.getByText(/Public model content may contain judgments/u)).toBeInTheDocument();
  });
});
