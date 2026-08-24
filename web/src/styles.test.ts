/// <reference types="node" />

import { readFileSync } from "node:fs";

const styles = readFileSync("src/styles.css", "utf8");

function positionsFor(selector: string): string[] {
  const source = styles.replace(/\/\*[\s\S]*?\*\//gu, "");
  const positions: string[] = [];
  for (const rule of source.matchAll(/([^{}]+)\{([^{}]*)\}/gu)) {
    const selectors = rule[1]?.split(",").map((value) => value.trim()) ?? [];
    if (!selectors.includes(selector)) continue;
    for (const declaration of (rule[2] ?? "").matchAll(
      /(?:^|;)\s*position\s*:\s*([^;]+)/gu)) {
      positions.push(declaration[1]!.trim());
    }
  }
  return positions;
}

describe("workbench Composer layout", () => {
  it("preserves sticky Session and Thread Composers after the glass style layer", () => {
    expect(styles).toContain(".prayu-shell .session-composer");
    expect(positionsFor(".prayu-shell .session-composer")).toEqual(["sticky"]);
    expect(positionsFor(".prayu-shell .prayu-starter-composer")).toEqual(["relative"]);
    expect(positionsFor(".prayu-shell .public-model-stream")).toEqual(["relative"]);
  });

  it("keeps the Thread transcript bounded beside a reachable safe-area Composer", () => {
    expect(styles).toMatch(/\.thread-workspace\s*\{[^}]*height:\s*100%[^}]*min-height:\s*0\s*!important[^}]*overflow:\s*hidden/isu);
    expect(styles).toMatch(/\.thread-transcript-region\s*\{[^}]*min-height:\s*0[^}]*flex:\s*1\s+1\s+auto[^}]*overflow:\s*hidden/isu);
    expect(styles).toMatch(/\.thread-transcript-viewport\s*\{[^}]*overflow:\s*auto[^}]*scrollbar-gutter:\s*stable/isu);
    expect(styles).toMatch(/\.thread-workspace\s*>\s*\.session-composer\s*\{[^}]*env\(safe-area-inset-bottom\)/isu);
  });

  it("defines narrow/high-zoom and reduced-motion Thread fallbacks", () => {
    expect(styles).toContain("@container thread-workspace (max-width: 680px)");
    expect(styles).toContain("@media (max-height: 720px)");
    expect(styles).toMatch(/@media \(max-height:\s*720px\)[\s\S]*?\.thread-workspace > \.workspace-header h1\s*\{[\s\S]*?white-space:\s*nowrap/iu);
    expect(styles).toMatch(/@media \(max-height:\s*720px\)[\s\S]*?\.thread-workspace > \.session-composer textarea\s*\{[^}]*height:\s*44px[^}]*min-height:\s*44px/iu);
    expect(styles).toMatch(/@media \(prefers-reduced-motion:\s*reduce\)[\s\S]*?\.thread-workspace \*[\s\S]*?animation-duration:\s*\.01ms/iu);
  });
});
