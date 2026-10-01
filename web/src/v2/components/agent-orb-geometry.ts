import type { AgentOrbMode } from "../projection/agent-activity";

interface Point { x: number; y: number; z: number; emphasis: number }
export interface OrbDot { x: number; y: number; radius: number; opacity: number }
const tau = Math.PI * 2;

/** Traverse's own four point arrangements, projected into a 40 × 40 SVG. */
export function agentOrbFrame(mode: AgentOrbMode, seconds: number): OrbDot[] {
  const points: Point[] = [];
  const add = (x: number, y: number, z: number, emphasis = 1) => points.push({ x, y, z, emphasis });
  // Each arrangement has 36 points, so a transition can start at the currently
  // displayed positions without adding/removing dots in the 20px footprint.
  if (mode === "searching") {
    // Latitude rings form a globe; one meridian brightens as the scan moves around it.
    for (let latitude = 0; latitude < 4; latitude++) {
      const y = (latitude - 1.5) * 0.5;
      const radius = Math.sqrt(1 - y * y);
      for (let longitude = 0; longitude < 9; longitude++) {
        const angle = longitude * tau / 9 + (latitude % 2) * 0.17;
        const scan = Math.pow(Math.max(0, Math.cos(angle - seconds * 1.6)), 6);
        add(Math.cos(angle) * radius, y, Math.sin(angle) * radius, 0.5 + scan * 0.5);
      }
    }
  } else if (mode === "executing") {
    // Two interlocking tilted hoops; travelling highlights mark activity without a progress fraction.
    for (let ring = 0; ring < 2; ring++) {
      for (let step = 0; step < 18; step++) {
        const angle = step * tau / 18;
        const x = Math.cos(angle), y = Math.sin(angle);
        const tilt = ring === 0 ? 0.75 : -0.75;
        add(x, y * Math.cos(tilt), y * Math.sin(tilt),
          0.5 + 0.5 * Math.pow(Math.max(0, Math.cos(angle - seconds * 2.1 - ring * Math.PI)), 4));
      }
    }
  } else if (mode === "composing") {
    // Four open rows carry a left-to-right wave, like lines being written.
    for (let row = 0; row < 4; row++) {
      for (let column = 0; column < 9; column++) {
        const x = (column - 4) / 4;
        const edge = Math.sqrt(Math.max(0, 1 - x * x));
        const wave = Math.sin(x * 3.2 - seconds * 2.4 + row * 0.3);
        add(x * 0.96, (row - 1.5) * 0.38 + wave * edge * 0.11,
          edge * Math.cos(row * 0.55 + seconds * 0.55) * 0.25, 0.65 + 0.35 * (wave + 1) / 2);
      }
    }
  } else {
    // A gentle breath keeps the neutral working state quieter than tool activity.
    const breath = 0.96 + 0.04 * Math.sin(seconds * tau / 3.2);
    for (let ring = -1; ring <= 1; ring++) {
      for (let step = 0; step < 12; step++) {
        const angle = step * tau / 12 + ring * 0.3;
        const radius = (ring === 0 ? 1 : 0.79) * breath;
        add(Math.cos(angle) * radius, ring * 0.46 * breath,
          Math.sin(angle) * radius, 0.85 + 0.15 * Math.sin(seconds * 1.1 + angle));
      }
    }
  }
  // Writing stays nearly frontal, even during a minutes-long response.
  const turn = mode === "composing" ? 0.15 + Math.sin(seconds * 0.45) * 0.1
    : seconds * (mode === "waiting" ? 0.16 : 0.3) + 0.5;
  const tilt = mode === "composing" ? 0.12 : 0.38;
  return points.map(({ x, y, z, emphasis }) => {
    const rx = x * Math.cos(turn) + z * Math.sin(turn);
    const rz = z * Math.cos(turn) - x * Math.sin(turn);
    const ry = y * Math.cos(tilt) - rz * Math.sin(tilt);
    const depth = y * Math.sin(tilt) + rz * Math.cos(tilt);
    const perspective = 1 / (1 - depth * 0.12);
    return { x: 20 + rx * 14 * perspective, y: 20 + ry * 14 * perspective,
      radius: 1.22 + (depth + 1) * 0.24, opacity: (0.24 + (depth + 1) * 0.38) * emphasis, depth };
  // Keep particle identity stable for morphing. All dots use the same ink, so
  // overlapping alpha blends need no depth sort; size and opacity carry depth.
  });
}
