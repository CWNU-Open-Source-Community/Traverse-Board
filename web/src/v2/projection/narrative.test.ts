import { describe, expect, it } from "vitest";
import type { PublicModelStreamSnapshot, ThreadTranscriptItemView } from "../../api/types";
import { projectThreadNarrative, type ThreadTranscriptActivityItem } from "./narrative";

function item(overrides: Partial<ThreadTranscriptActivityItem>): ThreadTranscriptActivityItem {
  return {
    version: "thread_transcript.v1",
    id: "item-1",
    canonical_id: "canonical-1",
    run_id: "run-1",
    run_ordinal: 1,
    sequence: 1,
    activity_type: "message",
    stage: "result",
    kind: "model_update",
    source: "model",
    title: "更新",
    detail: "我会先检查相关实现。",
    verifiable: true,
    instruction_authorized: false,
    provisional: false,
    durable: true,
    created_at: "2026-08-29T00:00:00Z",
    ...overrides,
  };
}

function snapshot(overrides: Partial<PublicModelStreamSnapshot> = {}): PublicModelStreamSnapshot {
  return {
    version: "public_model_stream.v1",
    call: {
      attempt_id: "attempt-1", cancel_requested: false, max_attempts: 3,
      model: "model-1", model_attempt: 1, protocol_repair: 0, provider: "provider-1",
      run_id: "run-1", session_id: "session-1", started_at: "2026-08-29T00:00:01Z",
      stream_bytes: 12, stream_chunks: 2, tool_round: 0, transport_attempt: 1,
    },
    content_kind: "tool_commentary", event_sequence: 2, items: [],
    message_complete: false, provisional: true, revision: 2,
    text: "我先检查相关实现。", updated_at: "2026-08-29T00:00:02Z",
    ...overrides,
  };
}

describe("projectThreadNarrative", () => {
  it("hides Run boundaries and groups tool activity", () => {
    const projected = projectThreadNarrative([
      item({ id: "boundary", sequence: 0, source: "harness", kind: "harness_status",
        title: "Run boundary", detail: "successor" }),
      item({ id: "search-1", canonical_id: "search-call-1", sequence: 2,
        source: "harness", kind: "tool_call",
        activity_type: "search", title: "搜索 RunSupervisor", detail: "3 个结果",
        activity_detail_ref: "detail-search-1", detail_available: true }),
      item({ id: "search-2", canonical_id: "search-call-2", sequence: 3,
        source: "harness", kind: "tool_call",
        activity_type: "search", title: "搜索 execute", detail: "2 个结果" }),
    ]);
    expect(projected).toHaveLength(1);
    expect(projected[0]).toMatchObject({ kind: "activity", activity: "search", count: 2,
      items: [expect.objectContaining({ detailRef: "detail-search-1", detailAvailable: true }),
        expect.objectContaining({ detailAvailable: false })] });
  });

  it("keeps the user story while omitting benign Harness chatter", () => {
    const projected = projectThreadNarrative([
      item({ id: "user", source: "operator", kind: "operator_input",
        detail: "为什么阻塞了？" }),
      item({ id: "model", sequence: 2, detail: "问题出在执行循环。" }),
      item({ id: "checkpoint", sequence: 3, source: "harness", kind: "harness_status",
        activity_type: "checkpoint", status: "checkpointed", title: "检查点", detail: "已记录" }),
    ]);
    expect(projected.map((entry) => entry.kind)).toEqual(["user", "assistant"]);
  });

  it("collapses identical durable answers across a synthetic continuation", () => {
    const projected = projectThreadNarrative([
      item({ id: "user", source: "operator", kind: "operator_input",
        detail: "只回复：流式链路已连通" }),
      item({ id: "answer-1", sequence: 2, detail: "流式链路已连通" }),
      item({ id: "synthetic", sequence: 3, source: "operator", kind: "operator_input",
        detail: "Continue mission at turn 2 using only the structured tools offered by Go when needed: 只回复：流式链路已连通" }),
      item({ id: "answer-2", sequence: 4, detail: "流式链路已连通" }),
    ]);

    expect(projected).toEqual([
      expect.objectContaining({ kind: "user", text: "只回复：流式链路已连通" }),
      expect.objectContaining({ kind: "assistant", text: "流式链路已连通" }),
    ]);
  });

  it("omits synthetic continuation and all internal turn state while keeping real input and blockers", () => {
    const projected = projectThreadNarrative([
      item({ id: "initial-user", sequence: 1, source: "operator", kind: "operator_input",
        title: "用户消息", detail: "请检查归档权限为什么被阻塞。",
        instruction_authorized: true }),
      item({ id: "model-call-complete", sequence: 2, source: "harness", kind: "model_call",
        activity_type: "checkpoint", status: "completed", title: "模型响应完成",
        detail: "mock / mock-code" }),
      item({ id: "model-call-failed", sequence: 3, source: "harness", kind: "model_call",
        activity_type: "checkpoint", stage: "blocked", status: "failed",
        title: "模型调用失败", detail: "provider internal failure" }),
      item({ id: "agent-turn-failed", sequence: 4, source: "harness", kind: "harness_status",
        activity_type: "checkpoint", stage: "blocked", status: "failed",
        title: "Agent 回合失败", detail: "internal protocol failure" }),
      item({ id: "mock-plan", sequence: 5, source: "model", kind: "model_update",
        title: "针路簿更新",
        detail: "mock plan [mock-code]: inspect workspace context, keep actions scoped" }),
      item({ id: "synthetic-continuation", sequence: 6, source: "operator",
        kind: "operator_input", title: "用户消息",
        detail: "Continue mission at turn 2 using only the structured tools offered by Go when needed: audit archive flow" }),
      item({ id: "harness-checkpoint", sequence: 7, source: "harness", kind: "plan",
        activity_type: "checkpoint", status: "completed", title: "Supervisor 检查点已记录",
        detail: "checkpoint 2" }),
      item({ id: "assistant", sequence: 8, source: "model", kind: "model_update",
        title: "针路簿更新", detail: "已确认阻塞来自权限门。" }),
      item({ id: "blocked", sequence: 9, source: "harness", kind: "dependency",
        activity_type: "checkpoint", stage: "blocked", status: "blocked",
        title: "检测到无进展循环", detail: "同一权限依赖未能取得进展" }),
    ]);

    expect(projected).toEqual([
      expect.objectContaining({ kind: "user", text: "请检查归档权限为什么被阻塞。" }),
      expect.objectContaining({ kind: "assistant", text: "已确认阻塞来自权限门。" }),
      expect.objectContaining({ kind: "notice", tone: "warning",
        text: "同一权限依赖未能取得进展" }),
    ]);
    expect(projected.map((entry) => "text" in entry ? entry.text : "").join("\n"))
      .not.toMatch(/mock plan|Continue mission|Agent 回合|模型响应|检查点/u);
  });

  it("projects safe live commentary and replaces it by stable durable turn identity", () => {
    const live = snapshot();
    const provisional = projectThreadNarrative([], {
      runId: "run-1", snapshot: live, status: "live",
    });
    expect(provisional).toEqual([
      expect.objectContaining({
        id: "live-message:attempt-1:1:1", kind: "assistant",
        text: "我先检查相关实现。", provisional: true,
      }),
    ]);

    const durable = item({
      id: "durable-commentary", attempt_id: "attempt-1", model_attempt: 1,
      tool_round: 1, detail: "我先检查相关实现。", created_at: "2026-08-29T00:00:03Z",
    });
    const replaced = projectThreadNarrative([durable], {
      runId: "run-1", snapshot: live, status: "finalizing",
    });
    expect(replaced).toHaveLength(1);
    expect(replaced[0]).toMatchObject({ id: "durable-commentary", provisional: false });
  });

  it("uses safe tool labels and deduplicates a live tool by stream identity", () => {
    const live = snapshot({
      text: "",
      items: [{
        id: "stream-tool-1", response_id: "response-1", type: "tool_call",
        status: "ready_for_validation", tool_name: "workspace_read", argument_bytes: 128,
        durable: false, provisional: true,
      }],
    });
    const provisional = projectThreadNarrative([], {
      runId: "run-1", snapshot: live, status: "live",
    });
    expect(provisional).toEqual([
      expect.objectContaining({ kind: "activity", activity: "read", title: "读取文件",
        provisional: true }),
    ]);
    expect(JSON.stringify(provisional)).not.toContain("workspace_read");

    const durable = item({
      id: "durable-tool", canonical_id: "durable-tool", source: "harness", kind: "tool_call",
      activity_type: "read", title: "读取完成", detail: "已读取", stream_item_id: "stream-tool-1",
      tool_name: "workspace_read", created_at: "2026-08-29T00:00:03Z",
    });
    const replaced = projectThreadNarrative([durable], {
      runId: "run-1", snapshot: live, status: "finalizing",
    });
    expect(replaced).toHaveLength(1);
    expect(replaced[0]).toMatchObject({ kind: "activity", title: "读取文件",
      provisional: false, count: 1 });
  });

  it("merges durable lifecycle stages by canonical tool-call identity", () => {
    const projected = projectThreadNarrative([
      item({ id: "tool-arguments", canonical_id: "call-workspace-list", sequence: 1,
        source: "harness", kind: "tool_call", activity_type: "search",
        stage: "arguments_ready", status: "arguments_ready", tool_name: "workspace_list",
        detail: "参数已就绪" }),
      item({ id: "tool-running", canonical_id: "call-workspace-list", sequence: 2,
        source: "harness", kind: "tool_call", activity_type: "search",
        stage: "running", status: "running", tool_name: "workspace_list",
        detail: "正在列出工作区" }),
      item({ id: "progress", canonical_id: "progress", sequence: 3,
        source: "model", kind: "model_update", activity_type: "message",
        detail: "我正在读取工作区。" }),
      item({ id: "tool-result", canonical_id: "call-workspace-list", sequence: 4,
        source: "harness", kind: "tool_call", activity_type: "search",
        stage: "result", status: "completed", tool_name: "workspace_list",
        detail: "列出了 4 个条目" }),
      item({ id: "second-result", canonical_id: "call-workspace-search", sequence: 5,
        source: "harness", kind: "tool_call", activity_type: "search",
        stage: "result", status: "completed", tool_name: "workspace_search",
        detail: "找到 2 个结果" }),
    ]);

    const activities = projected.filter((entry) => entry.kind === "activity");
    expect(activities).toHaveLength(2);
    expect(activities[0]).toMatchObject({ count: 1, detail: "列出了 4 个条目",
      status: "completed", items: [{ detail: "列出了 4 个条目", status: "completed" }] });
    expect(activities[1]).toMatchObject({ count: 1, detail: "找到 2 个结果" });
  });

  it("shows Web fetch security outcomes and hides generic batch completion", () => {
    const evidence = {
      version: "web_evidence_presentation.v1", source_id: "source-web",
      snapshot_id: "snapshot-web", url: "https://docs.example.com/report",
      title: "Report", fetched_at: "2026-08-29T00:00:00Z",
      stale_at: "2026-08-30T00:00:00Z", digest: "a".repeat(64),
      partial: false, stale: false, citeable: false, untrusted: true,
      instruction_authorized: false,
    } as const;
    const projected = projectThreadNarrative([
      item({ id: "blocked-fetch", canonical_id: "call-web", source: "harness",
        kind: "tool_call", activity_type: "read", tool_name: "web_fetch",
        title: "Robots 规则阻止抓取", detail: "已记录", status: "blocked",
        web_evidence: { ...evidence, state: "blocked" } }),
      item({ id: "batch-complete", canonical_id: "batch-complete", sequence: 2,
        source: "harness", kind: "tool_call", activity_type: "execute",
        title: "工具批次完成", detail: "", status: "completed" }),
      item({ id: "failed-fetch", canonical_id: "call-web-failed", sequence: 3,
        source: "harness", kind: "tool_call", activity_type: "read", tool_name: "web_fetch",
        title: "工具结果已记录", detail: "Web Fetch", status: "completed",
        web_evidence: { ...evidence, snapshot_id: "snapshot-failed", state: "failed" } }),
      item({ id: "unchecked-fetch", canonical_id: "call-web-unchecked", sequence: 4,
        source: "harness", kind: "tool_call", activity_type: "read", tool_name: "web_fetch",
        title: "网页已抓取（未检查 Robots）", detail: "已记录", status: "robots_ignored",
        web_evidence: { ...evidence, snapshot_id: "snapshot-unchecked", state: "fetched",
          citeable: true } }),
      item({ id: "robots-disallow", canonical_id: "call-web-robots-disallow", sequence: 5,
        source: "harness", kind: "tool_call", activity_type: "read", tool_name: "web_fetch",
        title: "Full Access 已忽略站点 Robots 限制", detail: "已记录", status: "robots_ignored",
        web_evidence: { ...evidence, snapshot_id: "snapshot-disallow", state: "fetched",
          citeable: true } }),
      item({ id: "robots-unknown", canonical_id: "call-web-robots-unknown", sequence: 6,
        source: "harness", kind: "tool_call", activity_type: "read", tool_name: "web_fetch",
        title: "Robots 无法验证，已按 Full Access 继续", detail: "已记录", status: "robots_ignored",
        web_evidence: { ...evidence, snapshot_id: "snapshot-unknown", state: "fetched",
          citeable: true } }),
    ]);

    expect(projected).toHaveLength(1);
	expect(projected[0]).toMatchObject({ kind: "activity", count: 5,
	  title: "Robots 无法验证，已按 Full Access 继续",
	  detail: "未能验证站点 Robots 规则；Full Access 仍继续创建了快照",
      status: "robots_ignored",
      items: [
        expect.objectContaining({ title: "Robots 规则阻止抓取", status: "blocked" }),
        expect.objectContaining({ title: "网页验证不可用",
          status: "verification_unavailable" }),
        expect.objectContaining({ title: "网页已抓取（未检查 Robots）",
          status: "robots_ignored" }),
		expect.objectContaining({ title: "Full Access 已忽略站点 Robots 限制",
		  status: "robots_ignored" }),
		expect.objectContaining({ title: "Robots 无法验证，已按 Full Access 继续",
		  status: "robots_ignored" }),
      ] });
    expect(JSON.stringify(projected)).not.toContain("工具批次完成");
    expect(JSON.stringify(projected)).not.toContain("工具结果已记录");
  });

  it("never carries a previous Run or Attempt projection into the current binding", () => {
    const staleRun = projectThreadNarrative([], {
      runId: "run-2", snapshot: snapshot(), status: "live",
    });
    expect(staleRun).toEqual([]);

    const nextAttempt = snapshot({
      call: { ...snapshot().call, attempt_id: "attempt-2", model_attempt: 2 },
      text: "我正在重试。", revision: 3,
    });
    const projected = projectThreadNarrative([], {
      runId: "run-1", snapshot: nextAttempt, status: "live",
    });
    expect(projected).toEqual([
      expect.objectContaining({ id: "live-message:attempt-2:2:1", text: "我正在重试。" }),
    ]);
    expect(JSON.stringify(projected)).not.toContain("attempt-1");
  });
});
