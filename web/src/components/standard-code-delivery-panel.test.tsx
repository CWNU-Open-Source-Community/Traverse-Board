import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import { standardCodeDeliveryFixture } from "../test/standard-code-delivery";
import { StandardCodeDeliveryPanel } from "./standard-code-delivery-panel";

describe("StandardCodeDeliveryPanel", () => {
  it("shows one navigable Diff, Artifact, receipt, and recovery projection", async () => {
    const user = userEvent.setup();
    const standardCodeDelivery = vi.fn().mockResolvedValue(standardCodeDeliveryFixture());
    const onOpenArtifacts = vi.fn();
    const onOpenCheckpoints = vi.fn();
    const onOpenFile = vi.fn();
    const client = { standardCodeDelivery } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <StandardCodeDeliveryPanel client={client} runID="run-1"
        onOpenArtifacts={onOpenArtifacts} onOpenCheckpoints={onOpenCheckpoints}
        onOpenFile={onOpenFile} />
    </QueryClientProvider>);

    expect(await screen.findByText("Current revision verified")).toBeInTheDocument();
    expect(screen.getByText("internal/example.go")).toBeInTheDocument();
    expect(screen.getAllByText("f".repeat(64)).length).toBeGreaterThan(0);
    expect(screen.getByText("completed · exit 0 · retries 1")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "internal/example.go" }));
    expect(onOpenFile).toHaveBeenCalledWith("internal/example.go");
    await user.click(screen.getByRole("button", { name: "Open 1 output artifacts" }));
    expect(onOpenArtifacts).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(onOpenCheckpoints).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/C:\\Users|\/home\/|very-secret-delivery-value/i))
      .not.toBeInTheDocument();
  });
});
