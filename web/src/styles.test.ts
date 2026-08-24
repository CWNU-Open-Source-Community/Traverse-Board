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
});
