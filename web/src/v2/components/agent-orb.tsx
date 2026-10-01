import { useLayoutEffect, useRef, useState } from "react";
import type { AgentOrbMode } from "../projection/agent-activity";
import { agentOrbFrame, type OrbDot } from "./agent-orb-geometry";

const paintInterval = 1000 / 30;
const transitionSeconds = 0.18;

/** Decorative; the adjacent status text is the single accessible announcement. */
export function AgentOrb({ mode }: { mode: AgentOrbMode }) {
  const svgRef = useRef<SVGSVGElement>(null);
  const initialMode = useRef(mode).current;
  const [still] = useState(() => agentOrbFrame(initialMode, 0.8));
  const retarget = useRef<((nextMode: AgentOrbMode) => void) | undefined>(undefined);
  useLayoutEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const dots = Array.from(svg.querySelectorAll("circle"));
    let visible = typeof IntersectionObserver === "undefined";
    let frameID: number | undefined;
    let previousTime: number | undefined;
    let nextPaint: number | undefined;
    let elapsed = 0.8;
    let currentMode = initialMode;
    let displayed = still;
    let transition: { from: OrbDot[]; started: number } | undefined;
    let disposed = false;
    const paint = (frame: OrbDot[]) => {
      displayed = frame;
      frame.forEach((dot, index) => {
        const node = dots[index];
        node.setAttribute("cx", dot.x.toFixed(3));
        node.setAttribute("cy", dot.y.toFixed(3));
        node.setAttribute("r", dot.radius.toFixed(3));
        node.setAttribute("opacity", dot.opacity.toFixed(3));
      });
    };
    if (typeof window.matchMedia !== "function" || typeof requestAnimationFrame !== "function") {
      retarget.current = (nextMode) => paint(agentOrbFrame(nextMode, elapsed));
      return () => { retarget.current = undefined; };
    }
    const preference = window.matchMedia("(prefers-reduced-motion: reduce)");
    const paintCurrent = () => {
      let frame = agentOrbFrame(currentMode, elapsed);
      if (transition) {
        const progress = Math.min(1, (elapsed - transition.started) / transitionSeconds);
        const weight = 1 - (1 - progress) ** 3;
        const from = transition.from;
        frame = frame.map((dot, index) => ({
          x: from[index].x + (dot.x - from[index].x) * weight,
          y: from[index].y + (dot.y - from[index].y) * weight,
          radius: from[index].radius + (dot.radius - from[index].radius) * weight,
          opacity: from[index].opacity + (dot.opacity - from[index].opacity) * weight,
        }));
        if (progress === 1) transition = undefined;
      }
      paint(frame);
    };
    const tick = (time: number) => {
      if (disposed) return;
      frameID = undefined;
      if (previousTime !== undefined) elapsed += (time - previousTime) / 1000;
      previousTime = time;
      nextPaint ??= time;
      // Keep the fractional deadline instead of rounding every interval to the
      // latest display frame. Skip missed slots without a catch-up paint burst.
      if (time + 0.001 >= nextPaint) {
        paintCurrent();
        nextPaint += (Math.floor((time + 0.001 - nextPaint) / paintInterval) + 1) * paintInterval;
      }
      frameID = requestAnimationFrame(tick);
    };
    const sync = () => {
      if (frameID !== undefined) cancelAnimationFrame(frameID);
      frameID = undefined;
      previousTime = undefined;
      nextPaint = undefined;
      if (disposed) return;
      if (preference.matches) { transition = undefined; paintCurrent(); }
      else if (visible && document.visibilityState !== "hidden") frameID = requestAnimationFrame(tick);
    };
    retarget.current = (nextMode) => {
      if (nextMode === currentMode) return;
      currentMode = nextMode;
      if (preference.matches || !visible || document.visibilityState === "hidden") {
        transition = undefined;
        paintCurrent();
      } else {
        // Rapid updates start from what is actually on screen, including an
        // interrupted morph. Status text is updated independently by the parent.
        transition = { from: displayed, started: elapsed };
      }
    };
    const observer = typeof IntersectionObserver === "undefined" ? undefined
      : new IntersectionObserver(([entry]) => { visible = entry.isIntersecting; sync(); });
    observer?.observe(svg);
    preference.addEventListener("change", sync);
    document.addEventListener("visibilitychange", sync);
    sync();
    return () => {
      disposed = true;
      retarget.current = undefined;
      sync();
      observer?.disconnect();
      preference.removeEventListener("change", sync);
      document.removeEventListener("visibilitychange", sync);
    };
  }, [initialMode, still]);
  useLayoutEffect(() => { retarget.current?.(mode); }, [mode]);
  return <svg className="v2-agent-orb" viewBox="0 0 40 40" width="20" height="20"
    aria-hidden="true" focusable="false" ref={svgRef} data-mode={mode}>
    {still.map((dot, index) => <circle key={index} cx={dot.x} cy={dot.y} r={dot.radius}
      opacity={dot.opacity} fill="currentColor" />)}
  </svg>;
}
