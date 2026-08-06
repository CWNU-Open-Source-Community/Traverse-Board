import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import { EmbeddedAnalyzerPanel } from "./embedded-analyzer-panel";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

describe("EmbeddedAnalyzerPanel", () => {
  it("requires explicit confirmation and renders only the metadata receipt", async () => {
    const executeEmbeddedAnalyzer = vi.fn().mockResolvedValue({
      version: "embedded_analyzer_execution_control.v1",
      execution_id: "execution-1", artifact_id: "artifact-1", run_id: "run-1",
      session_id: "session-1", workspace_id: "workspace-1",
      analyzer: "fixture.digest.v1", status: "succeeded", media_type: "text/plain",
      input_bytes: 5, line_count: 1, sha256: "a".repeat(64), utf8: true,
      metadata_only: true, capability_consumed: true, artifact_atomic: true,
      filesystem_mounted: false, network_enabled: false, subprocess_enabled: false,
      host_process_authorized: false, raw_request_included: false,
      bearer_token_included: false, replayed: false,
    });
    const client = { executeEmbeddedAnalyzer } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
      mutations: { retry: false }, queries: { retry: false },
    } })}>
      <EmbeddedAnalyzerPanel client={client} runID="run-1" />
    </QueryClientProvider>);

    await user.type(screen.getByRole("textbox"), "hello");
    expect(screen.getByRole("button", { name: "执行分析" })).toBeDisabled();
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "执行分析" }));

    await waitFor(() => expect(executeEmbeddedAnalyzer).toHaveBeenCalledTimes(1));
    expect(executeEmbeddedAnalyzer).toHaveBeenCalledWith("run-1", expect.objectContaining({
      text: "hello", confirmation: "RUN-EMBEDDED-ANALYZER",
    }));
    expect(await screen.findByText("artifact-1")).toBeInTheDocument();
    const receipt = screen.getByRole("heading", { name: "分析结果" }).closest("section");
    expect(receipt).not.toBeNull();
    expect(within(receipt!).queryByText("hello")).not.toBeInTheDocument();
  });
});
