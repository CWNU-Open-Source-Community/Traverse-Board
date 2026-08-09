import { applyRunNavigationMode, readRunNavigationMode,
  subscribeRunNavigationMode } from "./run-navigation";

describe("Run navigation preference", () => {
  beforeEach(() => window.localStorage.clear());

  it("defaults closed and notifies the current window when diagnostics are enabled", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeRunNavigationMode(listener);

    expect(readRunNavigationMode()).toBe("compact");
    applyRunNavigationMode("diagnostic");

    expect(readRunNavigationMode()).toBe("diagnostic");
    expect(listener).toHaveBeenCalledWith("diagnostic");
    unsubscribe();
  });

  it("fails closed when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("storage unavailable", "SecurityError");
    });
    expect(readRunNavigationMode()).toBe("compact");
    vi.restoreAllMocks();
  });
});
