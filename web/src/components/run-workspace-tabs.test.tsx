import { useState } from "react";
import { Activity, Gauge, History } from "lucide-react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RunWorkspaceTabs, type RunTab } from "./run-workspace";

const items: Array<{ id: RunTab; label: string; icon: typeof Activity }> = [
  { id: "activity", label: "Activity", icon: Activity },
  { id: "overview", label: "Overview", icon: Gauge },
  { id: "events", label: "Events", icon: History },
];

function Harness() {
  const [activeTab, setActiveTab] = useState<RunTab>("activity");
  return <RunWorkspaceTabs activeTab={activeTab} ariaLabel="Run views"
    items={items} onSelect={setActiveTab}>
    <h2>{activeTab}</h2>
  </RunWorkspaceTabs>;
}

const groupedItems: Array<{ id: RunTab; label: string; icon: typeof Activity }> = [
  { id: "activity", label: "Activity", icon: Activity },
  { id: "approvals", label: "Approvals", icon: Activity },
  { id: "overview", label: "Overview", icon: Gauge },
  { id: "events", label: "Events", icon: History },
  { id: "sandbox", label: "Sandbox", icon: Gauge },
];
function GroupedHarness() {
  const [activeTab, setActiveTab] = useState<RunTab>("activity");
  return <>
    <button type="button" onClick={() => setActiveTab("events")}>Open exact event</button>
    <RunWorkspaceTabs activeTab={activeTab} ariaLabel="Run views" grouped items={groupedItems} onSelect={setActiveTab}>
      <h2>{activeTab}</h2>
    </RunWorkspaceTabs>
  </>;
}

describe("RunWorkspaceTabs", () => {
  it("exposes one active tab and connects it to the visible panel", () => {
    render(<Harness />);

    const activity = screen.getByRole("tab", { name: "Activity" });
    const overview = screen.getByRole("tab", { name: "Overview" });
    const panel = screen.getByRole("tabpanel");
    expect(activity).toHaveAttribute("aria-selected", "true");
    expect(activity).toHaveAttribute("tabindex", "0");
    expect(overview).toHaveAttribute("aria-selected", "false");
    expect(overview).toHaveAttribute("tabindex", "-1");
    expect(activity).toHaveAttribute("aria-controls", panel.id);
    expect(panel).toHaveAttribute("aria-labelledby", activity.id);
    expect(panel).toHaveTextContent("activity");
    const overviewPanel = document.getElementById(overview.getAttribute("aria-controls") ?? "");
    expect(overviewPanel).toHaveAttribute("hidden");
    expect(overviewPanel).toHaveAttribute("aria-labelledby", overview.id);
  });

  it("moves focus and activates tabs with arrows, Home, and End", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const activity = screen.getByRole("tab", { name: "Activity" });
    const overview = screen.getByRole("tab", { name: "Overview" });
    const events = screen.getByRole("tab", { name: "Events" });

    activity.focus();
    await user.keyboard("{ArrowRight}");
    expect(overview).toHaveFocus();
    expect(overview).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveTextContent("overview");

    await user.keyboard("{End}");
    expect(events).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(activity).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(events).toHaveFocus();
    await user.keyboard("{Home}");
    expect(activity).toHaveFocus();
  });

  it("keeps common approvals reachable and limits keyboard navigation to expanded diagnostics", async () => {
    const user = userEvent.setup();
    render(<GroupedHarness />);
    const activity = screen.getByRole("tab", { name: "Activity" });
    expect(screen.getByRole("tab", { name: "Approvals" })).toBeVisible();
    expect(screen.queryByRole("tab", { name: "Events" })).not.toBeInTheDocument();
    activity.focus();
    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Approvals" })).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(activity).toHaveFocus();
    await user.click(screen.getByRole("button", { name: /高级诊断|Advanced diagnostics/u }));
    expect(screen.getByRole("tab", { name: "Overview" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Events" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Sandbox" })).toBeVisible();
    activity.focus();
    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Sandbox" })).toHaveFocus();
    expect(screen.getByRole("tabpanel")).toHaveTextContent("sandbox");
    await user.keyboard("{Home}");
    expect(activity).toHaveFocus();
  });

  it("opens a programmatically selected diagnostic without replacing its scope when navigation collapses", async () => {
    const user = userEvent.setup();
    render(<GroupedHarness />);
    await user.click(screen.getByRole("button", { name: "Open exact event" }));
    const tab = screen.getByRole("tab", { name: "Events" });
    expect(tab).toBeVisible();
    const panel = screen.getByRole("tabpanel");
    expect(panel).toHaveTextContent("events");
    expect(panel).toHaveAttribute("aria-labelledby", tab.id);
    const toggle = screen.getByRole("button", { name: /高级诊断|Advanced diagnostics/u });
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveTextContent("Events");
    expect(panel).toBeVisible();
    expect(panel).toHaveTextContent("events");
    expect(screen.getByRole("tab", { name: "Activity" })).toHaveAttribute("tabindex", "0");
  });
});
