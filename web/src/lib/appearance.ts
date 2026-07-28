import { setDesktopWindowTheme } from "./desktop-window";

export type PrayuTheme = "light" | "dark";

const themeStorageKey = "prayu.theme";

export function readPrayuTheme(): PrayuTheme {
  if (typeof window === "undefined") return "dark";
  try {
    const stored = window.localStorage.getItem(themeStorageKey);
    if (stored === "light" || stored === "dark") return stored;
  } catch {
    // Fall through to the system preference.
  }
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function applyPrayuTheme(theme: PrayuTheme, persist = true): void {
  document.documentElement.dataset.prayuTheme = theme;
  document.documentElement.style.colorScheme = theme;
  setDesktopWindowTheme(theme);
  if (!persist) return;
  try {
    window.localStorage.setItem(themeStorageKey, theme);
  } catch {
    // Display preferences must never block the workbench.
  }
}

export function initializePrayuAppearance(): void {
  applyPrayuTheme(readPrayuTheme(), false);
}
