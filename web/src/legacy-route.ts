export const legacyWorkbenchBasePath = "/legacy";

export type LegacyResourceKind = "thread" | "run" | "session";

export function legacyWorkbenchPath(pathname: string): string | null {
  if (pathname === legacyWorkbenchBasePath || pathname === `${legacyWorkbenchBasePath}/`) {
    return "/";
  }
  if (!pathname.startsWith(`${legacyWorkbenchBasePath}/`)) return null;
  return pathname.slice(legacyWorkbenchBasePath.length);
}

export function legacyResourcePath(kind: LegacyResourceKind, id: string): string {
  return `${legacyWorkbenchBasePath}/${kind}s/${encodeURIComponent(id)}`;
}

export function legacyInspectorPath(threadID: string): string {
  return threadID ? legacyResourcePath("thread", threadID) : legacyWorkbenchBasePath;
}

function navigateWithinWorkbench(path: string): void {
  window.history.pushState({}, "", path);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function openLegacyInspector(threadID: string,
  navigate: (path: string) => void = navigateWithinWorkbench,
): void {
  navigate(legacyInspectorPath(threadID));
}
