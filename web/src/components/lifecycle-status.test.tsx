import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { CyberAgentClient } from "../api/client";
import type { RunDetailView } from "../api/types";
import { LocaleProvider } from "../lib/locale";
import { StatusBadge } from "./common";
import { LifecycleStatusBadge, LifecycleStatusLabel } from "./lifecycle-status";
import { RunControlPanel } from "./run-workspace";

it("distinguishes record state from a real running tool without changing failures or approval", () => {
  localStorage.removeItem("prayu.locale.v1");
  render(<LocaleProvider>
    <LifecycleStatusBadge status="running" />
    <LifecycleStatusLabel status="active" kind="session" />
    <LifecycleStatusBadge status="failed" />
    <LifecycleStatusBadge status="waiting_approval" />
    <StatusBadge status="running" />
  </LocaleProvider>);
  expect(screen.getByText("未结束")).toHaveClass("status-open");
  expect(screen.getByText("未关闭")).toHaveAttribute("title", "仅描述此上下文记录的状态，不决定对话是否可继续，也不表示 Agent 当前正在执行。");
  expect(screen.getByText("失败")).toHaveClass("status-failed");
  expect(screen.getByText("等待审批")).toHaveClass("status-waiting-approval");
  expect(screen.getAllByText("运行中")).toHaveLength(1);
});

it("does not infer Agent work from an open Run or an occupied execution lease", () => {
  const client = { hasRunLifecycle: true, hasRunExecution: true,
    controlRunLifecycle: vi.fn(), executeRun: vi.fn() } as unknown as CyberAgentClient;
  const detail = { run: { id: "run-open", status: "running" },
    operator_steering: { pending: 0, prepared: 0 }, execution_lease: { active: false } } as RunDetailView;
  const queries = new QueryClient();
  const view = render(<QueryClientProvider client={queries}><RunControlPanel client={client} detail={detail} /></QueryClientProvider>);
  expect(screen.getByText("Not ended")).toBeVisible();
  expect(screen.getByRole("button", { name: "Pause" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Run queue" })).toBeDisabled();
  view.rerender(<QueryClientProvider client={queries}><RunControlPanel client={client}
    detail={{ ...detail, execution_lease: { ...detail.execution_lease!, active: true } }} /></QueryClientProvider>);
  expect(screen.getByText("Execution access in use")).toBeVisible();
  expect(screen.queryByText("running")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Pause" })).toBeDisabled();
  expect(client.controlRunLifecycle).not.toHaveBeenCalled();
  expect(client.executeRun).not.toHaveBeenCalled();
});
