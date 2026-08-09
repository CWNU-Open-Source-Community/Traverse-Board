export type RunNavigationMode = "compact" | "diagnostic";

const storageKey = "prayu.run-navigation.v1";
const changeEvent = "prayu:run-navigation-change";

export function readRunNavigationMode(): RunNavigationMode {
  if (typeof window === "undefined") return "compact";
  try {
    return window.localStorage.getItem(storageKey) === "diagnostic"
      ? "diagnostic" : "compact";
  } catch {
    return "compact";
  }
}

export function applyRunNavigationMode(mode: RunNavigationMode): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(storageKey, mode);
  } catch {
    // Navigation remains usable with its compact default when storage is unavailable.
  }
  window.dispatchEvent(new CustomEvent<RunNavigationMode>(changeEvent, { detail: mode }));
}

export function subscribeRunNavigationMode(
  listener: (mode: RunNavigationMode) => void,
): () => void {
  if (typeof window === "undefined") return () => undefined;
  const changed = (event: Event) => {
    const mode = (event as CustomEvent<RunNavigationMode>).detail;
    listener(mode === "diagnostic" ? "diagnostic" : "compact");
  };
  const stored = () => listener(readRunNavigationMode());
  window.addEventListener(changeEvent, changed);
  window.addEventListener("storage", stored);
  return () => {
    window.removeEventListener(changeEvent, changed);
    window.removeEventListener("storage", stored);
  };
}

