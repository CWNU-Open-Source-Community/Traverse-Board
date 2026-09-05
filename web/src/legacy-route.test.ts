import { describe, expect, it, vi } from "vitest";

import {
  legacyInspectorPath,
  legacyResourcePath,
  legacyWorkbenchPath,
  openLegacyInspector,
} from "./legacy-route";

describe("legacy workbench routing", () => {
  it("uses a query-free SPA path and preserves the selected Thread", () => {
    expect(legacyInspectorPath("thread / one")).toBe("/legacy/threads/thread%20%2F%20one");
    expect(legacyInspectorPath("")).toBe("/legacy");
    expect(legacyResourcePath("run", "run-1")).toBe("/legacy/runs/run-1");
  });

  it("recognises only the dedicated legacy path prefix", () => {
    expect(legacyWorkbenchPath("/legacy")).toBe("/");
    expect(legacyWorkbenchPath("/legacy/threads/thread-1")).toBe("/threads/thread-1");
    expect(legacyWorkbenchPath("/")).toBeNull();
    expect(legacyWorkbenchPath("/legacy-lookalike")).toBeNull();
  });

  it("navigates without adding query parameters", () => {
    const navigate = vi.fn();

    openLegacyInspector("thread-1", navigate);

    expect(navigate).toHaveBeenCalledOnce();
    expect(navigate).toHaveBeenCalledWith("/legacy/threads/thread-1");
    expect(navigate.mock.calls[0]![0]).not.toContain("?");
  });

  it("uses same-document navigation by default so in-memory credentials survive", () => {
    window.history.replaceState({}, "", "/");
    const onPopState = vi.fn();
    window.addEventListener("popstate", onPopState);

    openLegacyInspector("thread-1");

    expect(window.location.pathname).toBe("/legacy/threads/thread-1");
    expect(window.location.search).toBe("");
    expect(onPopState).toHaveBeenCalledOnce();
    window.removeEventListener("popstate", onPopState);
  });
});
