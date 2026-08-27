/// <reference types="node" />

import { readFileSync } from "node:fs";

const styles = readFileSync("src/styles.css", "utf8");
const uncommentedStyles = styles.replace(/\/\*[\s\S]*?\*\//gu, "");

function declarationsFor(selector: string, property: string): string[] {
  const values: string[] = [];
  for (const rule of uncommentedStyles.matchAll(/([^{}]+)\{([^{}]*)\}/gu)) {
    const selectors = rule[1]?.split(",").map((value) => value.trim()) ?? [];
    if (!selectors.includes(selector)) continue;
    const escapedProperty = property.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
    const declarationPattern = new RegExp(`(?:^|;)\\s*${escapedProperty}\\s*:\\s*([^;]+)`, "gu");
    for (const declaration of (rule[2] ?? "").matchAll(declarationPattern)) {
      values.push(declaration[1]!.trim());
    }
  }
  return values;
}

function positionsFor(selector: string): string[] {
  return declarationsFor(selector, "position");
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

describe("product typography roles", () => {
  it("uses proportional platform fonts for interface and display text", () => {
    const uiStack = styles.match(/--prayu-font-ui:\s*([^;]+);/u)?.[1] ?? "";
    const displayStack = styles.match(/--prayu-font-display:\s*([^;]+);/u)?.[1] ?? "";

    expect(uiStack).toContain("system-ui");
    expect(uiStack).toContain("sans-serif");
    expect(uiStack).not.toMatch(/JetBrains|Cascadia|monospace/iu);
    expect(displayStack).toContain("Segoe UI Variable Display");
    expect(displayStack).toContain("Microsoft YaHei UI");
    expect(displayStack).toContain("PingFang SC");
    expect(displayStack).toContain("sans-serif");
  });

  it("keeps the bilingual brand in the display and interface roles", () => {
    expect(declarationsFor(".prayu-brand", "font-family")).toEqual(["var(--prayu-font-display)"]);
    expect(declarationsFor(".prayu-brand-copy small", "font-family")).toEqual(["var(--prayu-font-ui)"]);
    expect(styles).toMatch(/\.prayu-brand-name-latin\s*\{[^}]*font-weight:\s*650[^}]*letter-spacing:\s*-\.018em/isu);
    expect(styles).toMatch(/\.prayu-brand-name-cjk\s*\{[^}]*font-weight:\s*500[^}]*letter-spacing:\s*0/isu);
    expect(styles).toMatch(/\.prayu-brand-compact \.prayu-brand-name\s*\{[^}]*overflow:\s*hidden[^}]*text-overflow:\s*ellipsis[^}]*white-space:\s*nowrap/isu);
    expect(declarationsFor(".prayu-brand-copy small", "color")).toEqual([
      "var(--prayu-muted)",
      "color-mix(in srgb, var(--prayu-text) 64%, var(--prayu-muted))",
    ]);
  });

  it("reserves the mono role for technical content and keyboard shortcuts", () => {
    const monoStack = styles.match(/--prayu-font-mono:\s*([^;]+);/u)?.[1] ?? "";
    expect(monoStack).toContain("JetBrains Mono Variable");
    expect(declarationsFor("code", "font-family").at(-1)).toBe("var(--prayu-font-mono)");
    expect(declarationsFor("kbd", "font-family").at(-1)).toBe("var(--prayu-font-mono)");
    expect(declarationsFor(".unified-diff", "font-family").at(-1)).toBe("var(--prayu-font-mono)");
    expect(declarationsFor(".user-terminal-host", "font-family").at(-1)).toBe("var(--prayu-font-mono)");
    expect(styles.match(/"Cascadia Code"/gu)).toHaveLength(1);
  });
});
