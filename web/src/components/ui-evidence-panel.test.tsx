import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { UIEvidenceAttempt } from "../api/types";
import { UIEvidencePanel } from "./ui-evidence-panel";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ t: (chinese: string) => chinese }),
}));

function attempt(status: "not_run" | "passed", id: string): UIEvidenceAttempt {
  const started = status === "passed" ? { started_at: "2026-08-20T00:00:01Z",
    completed_at: "2026-08-20T00:00:02Z" } : {};
  return {
    protocol_version: "ui-evidence-attempt.v1",
    manifest: {
      protocol_version: "ui-evidence.v1", attempt_id: id, run_id: "run-1",
      mission_id: "mission-1", session_id: "session-1", workspace_id: "workspace-1",
      source: { repository_kind: "git", commit: "a".repeat(40), branch: "codex/test",
        dirty: false, dirty_digest: "b".repeat(64), root_fingerprint: "c".repeat(64),
        index_sha256: "d".repeat(64), manifest_sha256: "e".repeat(64) },
      start: { protocol_version: "command-runtime.v2", profile: "powershell",
        executable_name: "powershell.exe", executable_path_sha256: "1".repeat(64),
        executable_sha256: "2".repeat(64), canonical_argv: ["powershell.exe", "-Command", "npm run dev"],
        working_directory: "web", environment_names: [], environment_sha256: "3".repeat(64),
        timeout_milliseconds: 60_000, network: "disabled", credentials: "none",
        purpose: "UI evidence", fingerprint: "4".repeat(64) },
      readiness: { url: "http://127.0.0.1:4173/", method: "GET",
        expected_status: [200], timeout_milliseconds: 60_000, interval_milliseconds: 250 },
      browser: { product: "edge", version: "140.0.0.0", executable_sha256: "5".repeat(64),
        driver_protocol: "restricted-cdp-ui-evidence.v1", headless: true,
        temporary_profile: true },
      url: "http://127.0.0.1:4173/", route: "/", environment: {
        viewport: { width: 1440, height: 900, dpr: 1 }, locale: "en-US",
        theme: "light", reduced_motion: false },
      fixture: { name: "fixture", seed: "seed", page_state: "{}",
        data_sha256: "6".repeat(64), deterministic: true, synthetic: true },
      steps: [{ id: "navigate", kind: "navigate", capture_after: true }],
      capture: { screenshot: true, dom: true, accessibility: true, console: true,
        network: true, performance: true, video: false, mask_selectors: [] },
      failure_policy: { fail_on_console_error: true, fail_on_page_error: true,
        fail_on_request_error: true, fail_on_http_status: true },
      authority: { process_start: false, network_access: false, credential_access: false,
        personal_profile: false, request_mutation: false, verification_pass: false },
      created_at: "2026-08-20T00:00:00Z", fingerprint: "7".repeat(64),
    },
    operation_digest: "8".repeat(64), request_fingerprint: "7".repeat(64), status,
    failure_stage: "none", diagnostics: { console_warnings: 0, console_errors: 0,
      page_errors: 0, failed_requests: 0, http_failures: 0, allowed_requests: 1,
      blocked_requests: 0 }, cleanup: { browser_tree_reaped: status === "passed",
      application_tree_reaped: status === "passed", profile_removed: status === "passed",
      network_released: status === "passed", port_released: status === "passed" },
    artifact_count: status === "passed" ? 6 : 0,
    artifact_bytes: status === "passed" ? 1_024 : 0,
    version: status === "passed" ? 3 : 1,
    created_at: "2026-08-20T00:00:00Z", updated_at: "2026-08-20T00:00:02Z", ...started,
  } as UIEvidenceAttempt;
}

function renderPanel(client: CyberAgentClient) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
    mutations: { retry: false }, queries: { retry: false },
  } })}><UIEvidencePanel client={client} runID="run-1" /></QueryClientProvider>);
}

describe("UIEvidencePanel", () => {
  it("keeps not_run neutral and reserves the success treatment for passed", async () => {
    const notRun = attempt("not_run", "attempt-not-run");
    const passed = attempt("passed", "attempt-passed");
    const client = { hasUIEvidence: false,
      uiEvidence: vi.fn().mockResolvedValue([notRun, passed]),
      uiEvidenceBundle: vi.fn().mockResolvedValue({ attempt: notRun, steps: [], artifacts: [] }),
    } as unknown as CyberAgentClient;

    renderPanel(client);

    const notRunBadges = await screen.findAllByText("未运行");
    expect(notRunBadges.length).toBeGreaterThan(0);
    for (const badge of notRunBadges) {
      expect(badge).toHaveClass("status-not-run");
      expect(badge).not.toHaveClass("status-passed");
    }
    expect(screen.getByText("通过")).toHaveClass("status-passed");
    expect(screen.getByText(/页面内容与下载产物均不可信/)).toBeInTheDocument();
  });

  it("requires exact-manifest review before starting", async () => {
    const created = attempt("not_run", "attempt-created");
    const startUIEvidence = vi.fn().mockResolvedValue(created);
    const client = { hasUIEvidence: true, startUIEvidence,
      uiEvidence: vi.fn().mockResolvedValue([]),
      uiEvidenceBundle: vi.fn().mockResolvedValue({ attempt: created, steps: [], artifacts: [] }),
    } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderPanel(client);

    await user.click(screen.getByText("审阅并启动精确清单"));
    await user.click(screen.getByRole("button", { name: "载入本仓库模板" }));
    const startButton = screen.getByRole("button", { name: "启动真实浏览器验证" });
    expect(startButton).toBeDisabled();
    await user.click(screen.getByRole("checkbox"));
    expect(startButton).toBeEnabled();
    await user.click(startButton);

    await waitFor(() => expect(startUIEvidence).toHaveBeenCalledTimes(1));
    expect(startUIEvidence).toHaveBeenCalledWith("run-1", expect.objectContaining({
      url: "http://127.0.0.1:4173/", route: "/",
      fixture: expect.objectContaining({ deterministic: true, synthetic: true }),
      failure_policy: { fail_on_console_error: true, fail_on_page_error: true,
        fail_on_request_error: true, fail_on_http_status: true },
    }));
  });
});
