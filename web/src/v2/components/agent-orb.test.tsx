import { act, cleanup, render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AgentOrb } from "./agent-orb";
import { V2AgentActivity } from "./agent-activity";
import { agentOrbFrame, type OrbDot } from "./agent-orb-geometry";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

function installClock() {
  vi.stubGlobal("IntersectionObserver", undefined);
  let change = () => {};
  const media = { matches: false, addEventListener: vi.fn((_event, callback) => { change = callback; }), removeEventListener: vi.fn() };
  vi.stubGlobal("matchMedia", () => media);
  const frames = new Map<number, FrameRequestCallback>();
  let serial = 0;
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frames.set(++serial, callback); return serial; });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => { frames.delete(id); });
  return { frames, media, change: () => act(() => change()), step: (time: number) => {
    expect(frames.size).toBe(1);
    const [id, callback] = [...frames][0];
    frames.delete(id);
    act(() => callback(time));
  } };
}

const painted = (container: HTMLElement) => Array.from(container.querySelectorAll("circle"), circle =>
  ["cx", "cy", "r", "opacity"].map(attribute => circle.getAttribute(attribute)));
const attributes = (frame: OrbDot[]) => frame.map(dot => [dot.x, dot.y, dot.radius, dot.opacity].map(value => value.toFixed(3)));

it.each(["matchMedia", "requestAnimationFrame"])("updates the static shape when %s is unavailable", (capability) => {
  const clock = installClock();
  vi.stubGlobal(capability, undefined);
  const view = render(<AgentOrb mode="waiting" />);
  view.rerender(<AgentOrb mode="composing" />);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("composing", 0.8)));
  expect(clock.frames.size).toBe(0);
});

it.each([60, 120, 144])("maintains its paint cadence on a %i Hz display", (refreshRate) => {
  const clock = installClock();
  const view = render(<AgentOrb mode="searching" />);
  const writes = vi.spyOn(view.container.querySelector("circle")!, "setAttribute");
  const paints: number[] = [];
  for (let frame = 0; frame < refreshRate * 5; frame++) {
    const time = frame * 1000 / refreshRate;
    const before = writes.mock.calls.length;
    clock.step(time);
    if (writes.mock.calls.length !== before) paints.push(time);
  }
  expect(paints.length).toBeGreaterThanOrEqual(149);
  expect(paints.length).toBeLessThanOrEqual(151);
  const longestGap = Math.max(...paints.slice(1).map((time, index) => time - paints[index]));
  expect(longestGap).toBeLessThanOrEqual(1000 / 30 + 1000 / refreshRate + 0.01);
});

it("retargets an interrupted transition from the displayed shape and preserves animation time", () => {
  const clock = installClock();
  const view = render(<AgentOrb mode="searching" />);
  clock.step(0);
  clock.step(1000);
  const before = painted(view.container);
  view.rerender(<AgentOrb mode="composing" />);
  expect(view.container.querySelector("svg")).toHaveAttribute("data-mode", "composing");
  expect(painted(view.container)).toEqual(before);
  clock.step(1066);
  const middle = painted(view.container);
  expect(middle).not.toEqual(before);
  expect(middle).not.toEqual(attributes(agentOrbFrame("composing", 1.866)));
  view.rerender(<AgentOrb mode="waiting" />);
  expect(painted(view.container)).toEqual(middle);
  clock.step(1100);
  expect(painted(view.container)).not.toEqual(middle);
  clock.step(1266);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("waiting", 2.066)));
  expect(clock.frames.size).toBe(1);
});

it("settles on the latest state immediately when reduced motion interrupts a transition", () => {
  const clock = installClock();
  const view = render(<AgentOrb mode="searching" />);
  clock.step(0);
  clock.step(1000);
  view.rerender(<AgentOrb mode="composing" />);
  clock.step(1066);
  clock.media.matches = true;
  clock.change();
  expect(clock.frames.size).toBe(0);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("composing", 1.866)));
  view.rerender(<AgentOrb mode="executing" />);
  expect(clock.frames.size).toBe(0);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("executing", 1.866)));
});

it("freezes time while hidden and resumes in the latest mode without catching up", () => {
  const clock = installClock();
  const view = render(<AgentOrb mode="waiting" />);
  clock.step(0);
  clock.step(1000);
  const visibility = vi.spyOn(document, "visibilityState", "get");
  visibility.mockReturnValue("hidden");
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  view.rerender(<AgentOrb mode="composing" />);
  expect(clock.frames.size).toBe(0);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("composing", 1.8)));
  visibility.mockReturnValue("visible");
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  clock.step(7000);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("composing", 1.8)));
  clock.step(7100);
  expect(painted(view.container)).toEqual(attributes(agentOrbFrame("composing", 1.9)));
});

it.each(["正在停止", "等待批准", "状态读取失败"])("immediately cancels a morph when activity becomes %s", (label) => {
  const clock = installClock();
  const view = render(<V2AgentActivity activity={{ label: "正在搜索", mode: "searching" }} />);
  clock.step(0);
  view.rerender(<V2AgentActivity activity={{ label: "正在回复", mode: "composing" }} />);
  expect(view.getByRole("status")).toHaveTextContent("正在回复");
  clock.step(50);
  const staleCallback = [...clock.frames.values()][0];
  view.rerender(<V2AgentActivity activity={{ label, attention: true }} />);
  expect(view.getByRole("status")).toHaveTextContent(label);
  expect(view.container.querySelector(".v2-agent-orb")).toBeNull();
  expect(clock.frames.size).toBe(0);
  act(() => staleCallback(100));
  expect(clock.frames.size).toBe(0);
});

it("parks offscreen/hidden/reduced motion and removes its frame and observers on unmount", () => {
  let intersection!: IntersectionObserverCallback;
  let change!: () => void;
  const disconnect = vi.fn();
  vi.stubGlobal("IntersectionObserver", class {
    constructor(callback: IntersectionObserverCallback) { intersection = callback; }
    observe = vi.fn();
    disconnect = disconnect;
  });
  const media = { matches: false, addEventListener: vi.fn((_event, callback) => { change = callback; }), removeEventListener: vi.fn() };
  vi.stubGlobal("matchMedia", () => media);
  const frames = new Map<number, FrameRequestCallback>();
  let serial = 0;
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frames.set(++serial, callback); return serial; });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => { frames.delete(id); });
  const view = render(<AgentOrb mode="searching" />);
  const svg = view.container.querySelector("svg")!;
  expect(svg).toHaveAttribute("aria-hidden", "true");
  expect(frames.size).toBe(0);
  const intersect = (isIntersecting: boolean) => act(() => intersection([{ isIntersecting } as IntersectionObserverEntry], {} as IntersectionObserver));
  intersect(true);
  expect(frames.size).toBe(1);
  const [id, callback] = [...frames][0];
  frames.delete(id);
  act(() => callback(100));
  expect(frames.size).toBe(1);
  intersect(false);
  expect(frames.size).toBe(0);
  intersect(true);
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  expect(frames.size).toBe(0);
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  expect(frames.size).toBe(1);
  media.matches = true;
  act(() => change());
  expect(frames.size).toBe(0);
  media.matches = false;
  act(() => change());
  expect(frames.size).toBe(1);
  view.unmount();
  expect(frames.size).toBe(0);
  expect(disconnect).toHaveBeenCalledOnce();
  expect(media.removeEventListener).toHaveBeenCalledWith("change", change);
});
