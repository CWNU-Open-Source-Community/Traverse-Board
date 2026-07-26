# ADR 0074: Model Harness Protocol Profiles And Qualification

- Status: Accepted
- Date: 2026-07-26
- Scope: protocol-specific model requests, workload minimization, and explicit model qualification
- SQLite schema: remains v85
- OpenAPI: advances from `model_availability.v1` to `model_availability.v2`

## Context

Transport compatibility is not Agent Harness compatibility. An Anthropic-compatible
endpoint may accept the same HTTP shape while differing in streamed tool-call
framing, tool-result continuation, strict JSON behavior, or model-specific
capabilities. Sending the full root tool surface to every model wastes tokens and
increases the chance of an ambiguous response.

The project also needs a safe way to test an exact Provider/model pair without
executing a real Tool, shell command, file operation, browser action, Docker
operation, or arbitrary network target request.

## Decision

### 1. Go owns `model_harness.v1`

`llm.ModelHarness` describes the exact Provider/model binding:

- transport: `mock`, `anthropic_messages`, or the legacy in-process
  `provider_contract`;
- tool strategy: `native` or `none`;
- JSON strategy: `native`, `prompt`, or `none`;
- qualification status and bounded expiry;
- tool-call, tool-result, strict-JSON, and streaming qualification facts.

The binding digest includes the Provider/model identity and transport strategy.
Changing the Provider, base URL, model, or strategy invalidates a persisted
qualification.

Built-in Mock is trusted and remains offline. The Anthropic-compatible provider
uses native tool calls plus prompt-directed JSON and starts as
`qualification_required`. Existing in-process Provider implementations receive a
clearly named trusted compatibility profile to preserve source compatibility;
production Providers should implement `ModelHarnessDescriber` explicitly.

### 2. Workload-specific request minimization

Go prepares every Root, Specialist, and read-only Fan-out request immediately
before the Provider call:

- Root may carry the full tool set only after native tool-call and tool-result
  qualification.
- Specialist and read-only Fan-out are no-tool workloads; Go strips tool
  definitions even if a caller supplied them.
- Native JSON mode is enabled only for a native profile. Prompt JSON mode is
  left disabled at the transport layer and is represented by the provider
  contract.
- Unqualified or expired strict-JSON/tool profiles fail closed before model
  invocation.

This keeps the Harness control prompt and metadata bounded. It does not ask the
model to decide which safety controls to disable, and it does not use a document
or model output as authority.

### 3. Explicit two-call qualification

The operator may request `model_harness_qualification.v1` through the CLI, HTTP,
or Desktop model dialog. The qualification performs at most two bounded streamed
model calls:

1. exactly one synthetic in-memory `prayu_harness_echo` tool call with a fresh
   nonce;
2. a tool-result continuation that must return exact
   `model_harness_probe.v1` JSON and must not call a tool again.

The synthetic Tool is never dispatched. The probe has a 30-second timeout,
bounded chunks/bytes, exact JSON decoding, no returned response content, and no
raw Provider error in the public result. Successful records are metadata-only,
persisted under a hashed Provider/model setting with a seven-day TTL. Startup
restores only an exact binding match.

Qualification is an operator-confirmed control action. A read-only availability
snapshot never performs a network request. Its public projection reports
whether a route is `harness_ready`; it does not expose keys, URLs, prompts,
model output, tool arguments, or raw errors.

## Consequences

Positive:

- Mimo, DeepSeek, and other Anthropic-compatible endpoints can be tested against
  the actual Prayu Harness contract instead of being trusted merely because
  they accept an HTTP request.
- Root, Specialist, and Fan-out receive only the tools and JSON behavior their
  workload requires, reducing prompt/token overhead and accidental tool
  exposure.
- Provider/model/base-URL changes invalidate old qualification facts.
- A failed or unqualified model cannot enter the normal Root/tool loop.

Trade-offs:

- A newly configured external model must pass an explicit two-call qualification
  before Root and structured workflows can use it.
- Qualification itself can make up to two paid/remote requests when the operator
  confirms it. The UI and CLI show that network activity is possible.
- Qualification proves only the bounded Harness probe. It does not prove model
  quality, safety, policy compliance, or arbitrary long-context behavior.
- Real Tool, Shell, browser, Docker, Rust analyzer, and host-process execution
  remain separately gated and are not enabled by Harness qualification.

## Verification

The batch includes:

- exact binding, expiry, workload minimization, legacy compatibility, and
  qualification tests in Go;
- persistence/restart and changed-binding fail-closed tests in the Registry;
- HTTP, OpenAPI, CLI, and React projections with content-free qualification
  responses;
- a regression test that verifies Fan-out request preparation is written back
  to every shard rather than lost through a range-value copy.

The database remains schema v85 because qualification is stored in the existing
provider-setting surface; no migration is required.
