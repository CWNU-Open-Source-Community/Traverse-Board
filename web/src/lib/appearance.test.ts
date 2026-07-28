import { applyPrayuTheme, readPrayuTheme } from "./appearance";

describe("Prayu appearance", () => {
  beforeEach(() => {
    window.localStorage.clear();
    delete document.documentElement.dataset.prayuTheme;
    document.documentElement.style.colorScheme = "";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("persists and restores the transparent glass mode", () => {
    applyPrayuTheme("glass");

    expect(document.documentElement.dataset.prayuTheme).toBe("glass");
    expect(document.documentElement.style.colorScheme).toBe("dark");
    expect(window.localStorage.getItem("prayu.theme")).toBe("glass");
    expect(readPrayuTheme()).toBe("glass");
  });

  it("keeps appearance changes non-blocking when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("storage disabled", "SecurityError");
    });

    expect(() => applyPrayuTheme("glass")).not.toThrow();
    expect(document.documentElement.dataset.prayuTheme).toBe("glass");
  });
});
