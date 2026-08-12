import { render, screen } from "@testing-library/react";
import type { RunActivityView } from "../api/types";
import { RunActivityTimeline } from "./run-activity-timeline";

describe("RunActivityTimeline", () => {
  it("separates public model updates from verifiable Harness facts", () => {
    render(<RunActivityTimeline activity={activity()} />);

    expect(screen.getByText("模型公开更新")).toBeInTheDocument();
    expect(screen.getByText("模型调用")).toBeInTheDocument();
    expect(screen.getByText("我会先核对工作区，再运行测试。")).toBeInTheDocument();
    expect(screen.getByText("模型响应完成")).toBeInTheDocument();
    expect(screen.getByText("不包含私有思维链")).toBeInTheDocument();
    expect(screen.getByText(/执行记录以 Harness 事件为准/u)).toBeInTheDocument();
    expect(screen.getByText("模型调用").closest("details")).not.toHaveAttribute("open");
  });

  it("groups consecutive Harness tool facts into one disclosure", () => {
    const value = activity();
    value.items.push(toolItem(13, "工具操作开始", "running"),
      toolItem(14, "工具操作完成", "completed"));
    render(<RunActivityTimeline activity={value} />);

    expect(screen.getByText("运行了 2 个操作")).toBeInTheDocument();
    expect(screen.getAllByText("git diff --check")).toHaveLength(2);
    expect(screen.queryByText("工具操作开始")).not.toBeInTheDocument();
    expect(screen.queryByText("工具操作完成")).not.toBeInTheDocument();
  });

  it("keeps truncation and stream failure visible without inventing activity", () => {
    const value = activity();
    value.items = [];
    value.truncated = true;
    render(<RunActivityTimeline activity={value} streamError="connection lost" />);

    expect(screen.getByText(/最近一段活动/u)).toBeInTheDocument();
    expect(screen.getByText(/connection lost/u)).toBeInTheDocument();
    expect(screen.getByText("还没有公开活动")).toBeInTheDocument();
  });

  it("fails closed if a server ever marks private reasoning as included", () => {
    const value = activity();
    value.private_reasoning_included = true;
    render(<RunActivityTimeline activity={value} />);

    expect(screen.getByText(/活动投影已拒绝/u)).toBeInTheDocument();
    expect(screen.queryByText("Prayu 更新")).not.toBeInTheDocument();
  });

  it("shows provisional public commentary as live Activity", () => {
    const value = activity();
    render(<RunActivityTimeline activity={value} liveCommentary={publicSnapshot()} liveStatus="live" />);

    expect(screen.getByText("Prayu")).toBeInTheDocument();
    expect(screen.getByText("正在检查差异，下一步运行测试。")).toBeInTheDocument();
    expect(screen.getByText("临时")).toBeInTheDocument();
    expect(screen.getByText(/不会写入对话历史/u)).toBeInTheDocument();
  });

  it("replaces provisional commentary when its durable identity arrives", () => {
    const value = activity();
    value.items.push({
      id: "event-13",
      sequence: 13,
      kind: "model_update",
      source: "model",
      title: "Prayu 进度",
      detail: "持久化后的公开进度。",
      verifiable: false,
      instruction_authorized: false,
      created_at: "2026-07-30T01:00:03Z",
      attempt_id: "attempt-live",
      model_attempt: 2,
      tool_round: 4,
    });
    render(<RunActivityTimeline activity={value} liveCommentary={publicSnapshot()} liveStatus="live" />);

    expect(screen.getByText("持久化后的公开进度。")).toBeInTheDocument();
    expect(screen.queryByText("正在检查差异，下一步运行测试。")).not.toBeInTheDocument();
    expect(screen.queryByText("临时")).not.toBeInTheDocument();
  });

  it("does not project a provisional root reply as Live Activity", () => {
    const snapshot = publicSnapshot();
    snapshot.content_kind = "root_message";
    render(<RunActivityTimeline activity={activity()} liveCommentary={snapshot} liveStatus="live" />);

    expect(screen.queryByText("正在检查差异，下一步运行测试。")).not.toBeInTheDocument();
    expect(screen.queryByText("临时")).not.toBeInTheDocument();
  });

  it("condenses a completed tool lifecycle into actual operation rows", () => {
    const value = activity();
    value.items.push(
      toolLifecycleItem(13, "工具调用已请求", "浏览工作区、读取文件", "running"),
      toolLifecycleItem(14, "工具结果已记录", "浏览工作区", "completed"),
      toolLifecycleItem(15, "工具结果已记录", "读取文件", "completed"),
      toolLifecycleItem(16, "工具批次完成", "", "completed"),
    );
    render(<RunActivityTimeline activity={value} />);

    expect(screen.getByText("运行了 2 个操作")).toBeInTheDocument();
    expect(screen.getByText("浏览工作区")).toBeInTheDocument();
    expect(screen.getByText("读取文件")).toBeInTheDocument();
    expect(screen.queryByText("工具批次完成")).not.toBeInTheDocument();
  });
});

function activity(): RunActivityView {
  return {
    version: "run_activity.v1",
    run_id: "run-1",
    through_sequence: 12,
    truncated: false,
    private_reasoning_included: false,
    items: [
      {
        id: "event-11",
        sequence: 11,
        kind: "model_update",
        source: "model",
        title: "Prayu 更新",
        detail: "我会先核对工作区，再运行测试。",
        verifiable: false,
        instruction_authorized: false,
        created_at: "2026-07-30T01:00:00Z",
      },
      {
        id: "event-12",
        sequence: 12,
        kind: "model_call",
        source: "harness",
        title: "模型响应完成",
        detail: "mock / deterministic · 120 tokens",
        status: "completed",
        verifiable: true,
        instruction_authorized: false,
        created_at: "2026-07-30T01:00:01Z",
      },
    ],
  };
}

function publicSnapshot() {
  return {
    version: "model_public_stream.v2",
    revision: 3,
    content_kind: "tool_commentary",
    provisional: true,
    text: "正在检查差异，下一步运行测试。",
    message_complete: false,
    updated_at: "2026-07-30T01:00:02Z",
    call: {
      run_id: "run-1",
      session_id: "session-1",
      attempt_id: "attempt-live",
      model_attempt: 2,
      max_attempts: 3,
      protocol_repair: 0,
      tool_round: 3,
      provider: "deepseek",
      model: "deepseek-v4-flash",
      transport_attempt: 1,
      started_at: "2026-07-30T01:00:02Z",
      stream_chunks: 2,
      stream_bytes: 48,
      cancel_requested: false,
    },
  };
}

function toolItem(sequence: number, title: string, status: "running" | "completed") {
  return {
    id: `event-${sequence}`,
    sequence,
    kind: "tool_call" as const,
    source: "harness" as const,
    title,
    detail: "git diff --check",
    status,
    verifiable: true,
    instruction_authorized: false,
    created_at: `2026-07-30T01:00:${String(sequence).padStart(2, "0")}Z`,
  };
}

function toolLifecycleItem(sequence: number, title: string, detail: string,
  status: "running" | "completed") {
  return {
    ...toolItem(sequence, title, status),
    detail,
  };
}
