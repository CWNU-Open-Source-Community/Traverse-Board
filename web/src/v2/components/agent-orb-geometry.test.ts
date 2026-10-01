import { expect, it } from "vitest";
import { agentOrbFrame } from "./agent-orb-geometry";
import type { AgentOrbMode } from "../projection/agent-activity";

it("keeps the composing silhouette legible throughout a long reply", () => {
  let narrowest = Infinity;
  for (let step = 0; step <= 2400; step++) {
    const dots = agentOrbFrame("composing", step / 4);
    const width = (Math.max(...dots.map(dot => dot.x)) - Math.min(...dots.map(dot => dot.x))) / 2;
    narrowest = Math.min(narrowest, width);
  }
  // A 20px indicator must retain a readable horizontal writing gesture.
  expect(narrowest).toBeGreaterThan(12);
});

it("keeps compatible point arrangements inside the indicator throughout long animation", () => {
  const counts = new Set<number>();
  let outside = 0;
  for (const mode of ["waiting", "searching", "executing", "composing"] as AgentOrbMode[]) {
    for (let seconds = 0; seconds <= 600; seconds += 0.25) {
      const dots = agentOrbFrame(mode, seconds);
      counts.add(dots.length);
      for (const dot of dots) {
        if (![dot.x, dot.y, dot.radius, dot.opacity].every(Number.isFinite)
          || dot.radius <= 0 || dot.x - dot.radius < 0 || dot.x + dot.radius > 40
          || dot.y - dot.radius < 0 || dot.y + dot.radius > 40 || dot.opacity < 0 || dot.opacity > 1) outside++;
      }
    }
  }
  expect(counts.size).toBe(1);
  expect(outside).toBe(0);
});

it("keeps particle identity continuous as points cross in depth during a morph", () => {
  const before = agentOrbFrame("executing", 1.01005);
  const after = agentOrbFrame("executing", 1.01010);
  const largestStep = Math.max(...before.map((dot, index) => Math.hypot(after[index].x - dot.x, after[index].y - dot.y) / 2));
  expect(largestStep).toBeLessThan(0.01);
});
