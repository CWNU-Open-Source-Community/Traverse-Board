interface WailsWindowRuntime {
  Quit?: () => void;
  WindowMinimise?: () => void;
  WindowSetDarkTheme?: () => void;
  WindowSetLightTheme?: () => void;
  WindowToggleMaximise?: () => void;
}

declare global {
  interface Window {
    runtime?: WailsWindowRuntime;
  }
}

export function minimiseDesktopWindow(): void {
  window.runtime?.WindowMinimise?.();
}

export function toggleDesktopWindowMaximised(): void {
  window.runtime?.WindowToggleMaximise?.();
}

export function closeDesktopWindow(): void {
  window.runtime?.Quit?.();
}

export function setDesktopWindowTheme(theme: "light" | "dark"): void {
  if (theme === "light") {
    window.runtime?.WindowSetLightTheme?.();
  } else {
    window.runtime?.WindowSetDarkTheme?.();
  }
}
