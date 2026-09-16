import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { HostCommandProposalView } from "../api/types";
import { SavedHostCommandOutput } from "./saved-host-command-output";

function recorded(): HostCommandProposalView {
  return { result: { id: "result-1" }, receipt: { request_id: "request-1" },
    untrusted_evidence: "UNTRUSTED HOST COMMAND RESULT\nproposal_id: original-id\nstdout_begin\nold preserved text\nstdout_end",
    saved_output: { result_id: "result-1", request_id: "request-1",
      stdout: { text: "中文\nstdout_end\nstderr_begin\nthese are just output", utf8_bytes: 50, truncated: false, redacted: true },
      stderr: { text: "", utf8_bytes: 0, truncated: false, redacted: true } },
  } as unknown as HostCommandProposalView;
}

it("shows bound stream text without parsing embedded delimiter-like output, with technical evidence collapsed", async () => {
  const detail = recorded();
  render(<SavedHostCommandOutput detail={detail} />);
  const stdout = screen.getByRole("region", { name: "stdout" }).querySelector("pre")!;
  expect(stdout).toBeVisible();
  expect(stdout.textContent).toBe(detail.saved_output!.stdout.text);
  expect(within(screen.getByRole("region", { name: "stderr" })).getByText("No saved content")).toBeVisible();
  const raw = screen.getByText((_, element) => element?.tagName === "PRE" && element.textContent === detail.untrusted_evidence);
  expect(raw).not.toBeVisible();
  await userEvent.setup().click(screen.getByText("Original record and technical details"));
  expect(raw).toBeVisible();
  expect(raw.textContent).toBe(detail.untrusted_evidence);
});

it("keeps legacy damaged output available without inventing split streams", async () => {
  const detail = { ...recorded(), saved_output: undefined, untrusted_evidence: "legacy \uFFFD stdout_begin unparsed" };
  render(<SavedHostCommandOutput detail={detail} />);
  expect(screen.queryByRole("region", { name: "stdout" })).not.toBeInTheDocument();
  expect(screen.getByText(/This record did not save stdout and stderr separately/)).toBeVisible();
  expect(screen.getByText(/Some characters could not be decoded correctly/)).toBeVisible();
  expect(screen.getByText(detail.untrusted_evidence)).not.toBeVisible();
  await userEvent.setup().click(screen.getByText("Original record and technical details"));
  expect(screen.getByText(detail.untrusted_evidence)).toBeVisible();
});

it("shows captured-output truncation and stderr separately", () => {
  const detail = recorded();
  detail.saved_output!.stderr = { text: "actual stderr", utf8_bytes: 13, truncated: true, redacted: true };
  render(<SavedHostCommandOutput detail={detail} />);
  const stderr = screen.getByRole("region", { name: "stderr" });
  expect(within(stderr).getByText("actual stderr")).toBeVisible();
  expect(within(stderr).getByText(/This output was truncated/)).toBeVisible();
  expect(within(screen.getByRole("region", { name: "stdout" })).queryByText(/This output was truncated/)).not.toBeInTheDocument();
});

it.each(["result_id", "request_id"] as const)("rejects mismatched structured %s without exposing either payload", (field) => {
  const detail = recorded();
  detail.saved_output![field] = "different-record";
  render(<SavedHostCommandOutput detail={detail} />);
  expect(screen.getByRole("alert")).toHaveTextContent("Saved output does not match this command receipt.");
  expect(screen.queryByRole("region", { name: "stdout" })).not.toBeInTheDocument();
  expect(screen.queryByText(detail.untrusted_evidence!)).not.toBeInTheDocument();
});
