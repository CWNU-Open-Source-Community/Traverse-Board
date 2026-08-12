import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import { ModelAvailabilityDialog } from "./model-availability-dialog";

function mockHarness(model: string) {
  return {
    protocol_version: "model_harness.v1", model, transport_protocol: "mock",
    tool_strategy: "native", json_strategy: "native",
    qualification_status: "trusted_builtin", tool_calls_qualified: true,
    tool_results_qualified: true, strict_json_qualified: true,
    streaming_qualified: true, root_eligible: true,
    structured_json_eligible: true, qualified_at: "", expires_at: "",
  };
}

function requiredHarness(model: string) {
  return {
    ...mockHarness(model), transport_protocol: "anthropic_messages",
    json_strategy: "prompt", qualification_status: "qualification_required",
    tool_calls_qualified: false, tool_results_qualified: false,
    strict_json_qualified: false, streaming_qualified: false,
    root_eligible: false, structured_json_eligible: false,
  };
}

describe("ModelAvailabilityDialog", () => {
  it("renders redacted provider and route status without configuration secrets", async () => {
    const client = { modelAvailability: vi.fn().mockResolvedValue({
      protocol_version: "model_availability.v2", generation: 1,
      providers: [{ name: "mock", kind: "local", status: "available", models: ["mock-code"],
        harnesses: [mockHarness("mock-code")],
        credential_source: "none", network_required: false, configuration_error: false }],
      routes: [{ name: "code", provider: "mock", model: "mock-code", available: true,
        harness_ready: true }],
    }) } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(<QueryClientProvider client={queryClient}>
      <ModelAvailabilityDialog client={client} open onClose={vi.fn()} />
    </QueryClientProvider>);
    expect(await screen.findByText("mock-code")).toBeInTheDocument();
    expect(screen.getByText("code")).toBeInTheDocument();
    expect(container.textContent).not.toContain("api_key");
    expect(container.textContent).not.toContain("base_url");
  });

  it("keeps diagnostics and route selection behind explicit model control", async () => {
    const user = userEvent.setup();
    const diagnoseProvider = vi.fn().mockResolvedValue({
      protocol_version: "provider_diagnostic.v1", provider: "mock", model: "mock-code",
      status: "reachable", outcome: "success", retryable: false,
      network_request_attempted: false, model_called: true, tool_called: false,
      response_content_returned: false, duration_ms: 2,
    });
    const selectModelRoute = vi.fn().mockResolvedValue({
      name: "code", provider: "mock", model: "mock-fast", available: true,
      harness_ready: true,
    });
    const client = {
      hasModelControl: true, diagnoseProvider, selectModelRoute,
      modelAvailability: vi.fn().mockResolvedValue({
        protocol_version: "model_availability.v2", generation: 1,
        providers: [{ name: "mock", kind: "local", status: "available",
          models: ["mock-code", "mock-fast"], credential_source: "none",
          network_required: false, configuration_error: false,
          harnesses: [mockHarness("mock-code"), mockHarness("mock-fast")] }],
        routes: [{ name: "code", provider: "mock", model: "mock-code", available: true,
          harness_ready: true }],
      }),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } });
    render(<QueryClientProvider client={queryClient}>
      <ModelAvailabilityDialog client={client} open onClose={vi.fn()} />
    </QueryClientProvider>);
    expect(await screen.findByRole("button", { name: "Diagnose mock/mock-code" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Diagnose mock/mock-fast" }));
    await waitFor(() => expect(diagnoseProvider).toHaveBeenCalledWith({
      version: "provider_diagnostic.v1", provider: "mock", model: "mock-fast",
      confirm_diagnostic: true,
    }));
    expect(await screen.findByText("reachable")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("code model route"), "mock/mock-fast");
    await user.click(screen.getByRole("button", { name: "Save code route" }));
    await waitFor(() => expect(selectModelRoute).toHaveBeenCalledWith("code", {
      version: "model_route_control.v1", provider: "mock", model: "mock-fast",
    }));
  });

  it("keeps Harness qualification separate and explicitly confirmed", async () => {
    const user = userEvent.setup();
    const qualifyModelHarness = vi.fn().mockResolvedValue({
      protocol_version: "model_harness_qualification.v1", provider: "mimo",
      model: "model-secondary", status: "qualified", outcome: "success", retryable: false,
      network_request_attempted: true, model_calls: 2, synthetic_tool_calls: 1,
      tool_executed: false, response_content_returned: false, duration_ms: 8,
      harness: { ...mockHarness("model-secondary"), transport_protocol: "anthropic_messages",
        json_strategy: "prompt", qualification_status: "verified",
        qualified_at: "2026-07-25T00:00:00Z", expires_at: "2026-08-01T00:00:00Z" },
    });
    const client = {
      hasModelControl: true, qualifyModelHarness,
      modelAvailability: vi.fn().mockResolvedValue({
        protocol_version: "model_availability.v2", generation: 1,
        providers: [{ name: "mimo", kind: "anthropic_compatible", status: "available",
          models: ["model-primary", "model-secondary"], credential_source: "system",
          network_required: true, configuration_error: false,
          harnesses: [mockHarness("model-primary"), requiredHarness("model-secondary")] }],
        routes: [{ name: "code", provider: "mimo", model: "model-secondary", available: true,
          harness_ready: false }],
      }),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false },
    } });
    render(<QueryClientProvider client={queryClient}>
      <ModelAvailabilityDialog client={client} open onClose={vi.fn()} />
    </QueryClientProvider>);
    expect(await screen.findByRole("button", { name: "Qualify mimo/model-primary Harness" }))
      .toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Qualify mimo/model-secondary Harness" }));
    await waitFor(() => expect(qualifyModelHarness).toHaveBeenCalledWith({
      version: "model_harness_qualification.v1", provider: "mimo", model: "model-secondary",
      confirm_qualification: true,
    }));
    expect(await screen.findByText("2 model calls")).toBeInTheDocument();
  });

  it("submits a Provider secret once and renders status without plaintext", async () => {
    const user = userEvent.setup();
    const statuses = { protocol_version: "provider_credential.v1", items:
      ["anthropic", "deepseek", "mimo"].map((provider) => ({
        protocol_version: "provider_credential.v1", provider, configured: false,
        store_kind: "windows_credential_manager", store_available: true,
        plaintext_returned: false, restart_required: false,
        registry_reloaded: false, registry_generation: 1,
      })) };
    let submittedCredential: unknown;
    const changeProviderCredential = vi.fn().mockImplementation((provider, body) => {
      submittedCredential = { provider, body: { ...body } };
      return Promise.resolve({
        ...statuses.items[2], configured: true, registry_reloaded: true,
        registry_generation: 2,
      });
    });
    const client = { hasModelControl: false, hasProviderCredentials: true,
      providerCredentialStatuses: vi.fn().mockResolvedValue(statuses),
      changeProviderCredential,
      modelAvailability: vi.fn().mockResolvedValue({
        protocol_version: "model_availability.v2", generation: 1,
        providers: [{ name: "mock", kind: "local", status: "available",
          models: ["mock-code"], credential_source: "none", network_required: false,
          configuration_error: false, harnesses: [mockHarness("mock-code")] }],
        routes: [{ name: "code", provider: "mock", model: "mock-code", available: true,
          harness_ready: true }],
      }),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(<QueryClientProvider client={queryClient}>
      <ModelAvailabilityDialog client={client} open onClose={vi.fn()} />
    </QueryClientProvider>);
    const secret = "temporary-provider-key";
    const input = await screen.findByLabelText("mimo API credential");
    await user.type(input, secret);
    await user.click(screen.getByRole("button", { name: "Store mimo credential" }));
    await waitFor(() => expect(submittedCredential).toEqual({ provider: "mimo", body: {
      version: "provider_credential.v1", action: "set", secret, confirm: true,
    } }));
    expect(input).toHaveValue("");
    expect(await screen.findByText("Credential status updated")).toBeInTheDocument();
    expect(screen.getByText("Registry generation 2 active")).toBeInTheDocument();
    expect(container.textContent).not.toContain(secret);
  });
});
