import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { APIClient } from "../../api/client";
import type { NarrativeEntry } from "../projection/narrative";
import { V2Narrative } from "./narrative";

const markdown = vi.hoisted(() => vi.fn(({ children }: { children: string }) => <p>{children}</p>));
vi.mock("react-markdown", () => ({ default: markdown }));
afterEach(() => { cleanup(); markdown.mockClear(); });

it("renders only changing output while reusing a thousand historical Markdown rows", () => {
  const client = {} as APIClient;
  const history: NarrativeEntry[] = Array.from({ length: 1_000 }, (_, index) => ({
    id: `history-${index}`, kind: "assistant", text: `Historical ${index}`, createdAt: "", provisional: false,
  }));
  const live = (text: string): NarrativeEntry => ({ id: "live", kind: "assistant", text, createdAt: "", provisional: true });
  const view = render(<V2Narrative client={client} entries={[...history, live("First token")]} threadID="thread-1" />);
  expect(markdown).toHaveBeenCalledTimes(1_001);
  for (let revision = 1; revision <= 20; revision++) {
    view.rerender(<V2Narrative client={client} entries={[...history, live(`Token ${revision}`)]} threadID="thread-1" />);
  }
  expect(markdown).toHaveBeenCalledTimes(1_021);
  expect(screen.getByText("Token 20")).toBeInTheDocument();
  // A refetch can rebuild equal projection objects; Markdown still stays cached.
  view.rerender(<V2Narrative client={client} entries={history.map((entry) => ({ ...entry }))} threadID="thread-1" />);
  expect(markdown).toHaveBeenCalledTimes(1_021);
  const revised = history.map((entry, index) => index === 500 ? { ...entry, text: "Corrected durable answer" } : entry);
  view.rerender(<V2Narrative client={client} entries={revised} threadID="thread-1" />);
  expect(markdown).toHaveBeenCalledTimes(1_022);
  expect(screen.getByText("Corrected durable answer")).toBeInTheDocument();
});
