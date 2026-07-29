import { render, screen } from "@testing-library/react";
import type { RunActivityView } from "../api/types";
import { RunActivityTimeline } from "./run-activity-timeline";

describe("RunActivityTimeline", () => {
  it("separates public model updates from verifiable Harness facts", () => {
    render(<RunActivityTimeline activity={activity()} />);

    expect(screen.getByText("模型公开更新")).toBeInTheDocument();
    expect(screen.getByText("Harness 事件")).toBeInTheDocument();
    expect(screen.getByText("我会先核对工作区，再运行测试。")).toBeInTheDocument();
    expect(screen.getByText("模型响应完成")).toBeInTheDocument();
    expect(screen.getByText("不包含私有思维链")).toBeInTheDocument();
    expect(screen.getByText(/执行记录以 Harness 事件为准/u)).toBeInTheDocument();
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
