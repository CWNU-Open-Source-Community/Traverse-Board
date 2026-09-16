import { render, screen } from "@testing-library/react";
import { LocaleProvider } from "../lib/locale";
import { StatusBadge, StatusLabel } from "./common";

it.each([
  ["approve", "已批准", "approved"], ["deny", "已拒绝", "denied"],
  ["create", "创建", "create"], ["in_progress", "进行中", "in-progress"],
])("labels %s without changing its meaning", (status, text, semanticClass) => {
  localStorage.setItem("prayu.locale.v1", "zh-CN");
  render(<LocaleProvider><StatusBadge status={status} /></LocaleProvider>);
  expect(screen.getByText(text)).toHaveClass(`status-${semanticClass}`);
  if (status === "create") expect(screen.queryByText("已应用")).not.toBeInTheDocument();
});

it("preserves unknown status text and an explicit recorded-result label", () => {
  localStorage.setItem("prayu.locale.v1", "zh-CN");
  render(<LocaleProvider><StatusLabel status="future_status" />
    <StatusBadge status="failed" label="已记录执行结果" /></LocaleProvider>);
  expect(screen.getByText("future status")).toBeInTheDocument();
  expect(screen.getByText("已记录执行结果")).toHaveClass("status-failed");
  expect(screen.queryByText("通过")).not.toBeInTheDocument();
});
