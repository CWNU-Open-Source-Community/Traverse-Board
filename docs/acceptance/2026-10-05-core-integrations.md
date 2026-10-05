# Core integration acceptance — 2026-10-05

This acceptance covers representative product entry points before the next
frontend iteration. The architecture cleanup through PR #252 is merged in main
`9559e09`. Acceptance used its implementation at `14b5fc02`, plus the MCP fixes
described below, on Windows with isolated `CYBERAGENT_HOME` directories.
The user's normal database was not used.

**Decision:** the core backend is ready for frontend integration with this
patch. The remaining native ZIP and CLI approval entry gaps below have explicit
supported alternatives and do not block the Thread interface.

## Results

| Path | Evidence and result |
| --- | --- |
| DeepSeek model qualification | The real `deepseek-v4-flash` endpoint passed the native tool protocol qualification through the existing system credential. Two model requests, one synthetic tool request, no actual tool execution during qualification. |
| HTTP Thread → model → native Skill | A bounded interactive Code Run completed a real HTTP turn. The model discovered the installed Skill, called `skill_read` for its instructions and then `references/acceptance.txt`, and returned both markers that were absent from the user prompt. Both calls are `completed` in the durable tool ledger. |
| Model transport retry | The first request in that turn encountered a network failure before any stream bytes. The existing retry completed successfully. The transcript preserves the failed attempt and the final successful response. |
| Interrupt and request replay | After a model call became active, Thread interrupt returned 202 / `stopping`; the turn returned 499 / `CANCELLED` with durable `turn_failure`. Repeating the original request key returned the same failure without another model attempt. |
| API restart | After stopping and restarting the API on the same isolated home, messages, transcript, Run history and request observations remained readable. The successful request was `completed`; the canceled request was `failed`, with `last_turn_interrupted: true` and execution `idle`. |
| Skill installation lifecycle | CLI directory import, exact replay, cross-process read and changed-input rejection passed. HTTP stage, approval, enable, disable, stale-generation rejection, re-enable and revoke passed. Disabled state survived an API restart; enabling a revoked installation returned 412. |
| HTTP access and request validation | Read-token mutation returned 401. Unknown Thread creation fields and unsupported versions returned 400; these rejected requests created no Thread. |
| Real stdio MCP server | After the fix, initialization, tool/resource discovery, file read and directory list passed. A path outside the Workspace, an extra Workspace-root field and a shell tool request were rejected. Closing stdin exited the process; a new process could read the same persisted Run. |
| MCP client lifecycle | Actual CLI stage, review, capability refresh and enable passed. Starting a missing server failed as expected; explicit refresh recovered the server after its launcher was restored. The peer was the product MCP server, not a fabricated protocol response. |
| HTTP Thread → model → approval → MCP | With both entry fixes, a fresh bounded Run returned 202 with one pending MCP call. Preview named the exact read-only operation; `approve_once` returned 202 with completed continuation. One completed MCP receipt and the model's final artifact digest were recorded. |

The Skill turn used two tools and one completed agent turn. Reported model usage
was 16,815 tokens. Four model attempts include one network failure with unknown
usage; the known token total is not a complete billing statement. Monetary
tracking was disabled; explicit turn, tool, token and timeout budgets bounded
the acceptance Runs.

## MCP defects found through product entry points

The original `mcp serve` process initialized and advertised tools but could not
execute `read_file` or `list_workspace`. Three entry-point mismatches caused this:

1. The CLI constructed a Gateway without its persisted Workspace root resolver.
2. The server sent read arguments through `Payload` and supplied execution
   identifiers that the current Gateway owns. The Gateway expects read tools in
   `Arguments` and rejects those caller-supplied identifiers.
3. The server omitted the Run's Session identity, preventing successful tool
   output from being recorded as an artifact.

The patch connects the persisted Workspace resolver and adapts the server's
existing read-only tool contract to the current Gateway. It does not add tools
or new execution authority. A subprocess regression exercises the actual
`Execute → mcp serve → SQLite → Gateway` chain from a working directory different
from the selected Workspace.

The first real model request with the dynamic MCP catalog also returned HTTP
400. Its schema root contained only `oneOf`; the patch adds `type: object`
while retaining every exact server/tool/fingerprint branch. This matches the
object input schema in the [Messages API reference](https://platform.claude.com/docs/en/api/messages/create).
With that change, the same DeepSeek integration produced the expected MCP call.
HTTP `approve_once` resumed it, recorded a completed MCP client receipt and
returned the file artifact's digest to the model.

That call exposed a second Thread-entry bug: creating an MCP approval stopped
the handoff at `root_wait`, but the durable Run stayed `running`. The Thread
service opened another handoff while the same input was still pending, producing
HTTP 500 instead of returning the approval boundary. The patch stops the Thread
at `root_wait`; approval decision and continuation retain their existing owners.
The final real HTTP acceptance returned 202 at both the pending and approval
steps and committed the original message after one tool execution.

The focused subprocess test and existing MCP protocol/interoperability tests
passed, as did the CLI build:

```text
go test ./internal/app -run '^TestMCPServeCLIUsesPersistedWorkspaceForReadTools$' -count=1
go test ./internal/mcp -run '^(TestMCP.*|TestSDKLegacyClientInteroperatesWithNativeServer)$' -count=1
go test ./internal/application -run '^TestSupervisorMCPRequiresReviewedSnapshotAndExactRuntimeScope$' -count=1
go test ./internal/application -run '^TestThreadTurnStopsAtPendingMCPApprovalAndResumesSameTurn$' -count=1
go build ./cmd/cyberagent
```

## Frontend handoff

- Use the existing Thread API for the conversation. `Run.status: running`
  together with `Thread execution.state: idle` means the interactive Run is
  ready for another message; it does not mean the model is still generating.
- Merge transcript stages by `canonical_id` / `durable_call_id`. A single tool
  can have pending, running, completed and result-recorded events. Count calls
  using the Run's `tool_usage`, not the number of completed transcript events.
- Render the settled turn result after a successful retry. A historical failed
  model attempt must not overwrite the final reply.
- Interrupt using the current `execution_id`. Inspect a submitted request with
  `GET /threads/{id}/turn-request` and its original `Idempotency-Key`. A sealed
  failure is not retried by resending that key; a new user intent needs a new key.
- Use `GET /runs/{id}` for checkpoint token totals and `tool_usage`.
  `/runs/{id}/usage` is not an HTTP endpoint. The richer `run usage` view is
  currently CLI-only. HTTP Thread creation currently uses default budgets and
  does not accept custom budget fields.
- Skill enablement depends on installation `state` as well as capabilities.
  Disabling retains the approved capability set. Staging is not activation;
  the interface needs separate staged, approved, enabled, disabled and revoked
  states.
- Use the HTTP approval preview and decision endpoints for MCP calls in Ask
  mode. `approve_once` resumes the original pending call; do not submit a new
  turn to repeat it. The CLI approval continuation currently only supports Web
  Fetch and cannot complete an MCP Ask approval by itself.

## Explicit remaining product gap

The current Web ZIP upload uses the legacy package request without a native
snapshot. Uploading an ordinary native `SKILL.md` ZIP through this entry returns
400 `INVALID_ARGUMENT`, even though CLI native-directory import and HTTP import
with a Go-produced snapshot work. The current success copy also says installed
when the result can be only staged.

Do not advertise ordinary native ZIP upload as supported. The frontend can use
the existing Desktop native-directory acquisition path, or keep the Web upload
explicitly limited to legacy packages. Supporting native ZIP directly needs a
Go-owned archive acquisition step before staging; the renderer should not invent
the snapshot. This is a scoped follow-up, not a reason to continue architecture
cleanup before building the Thread interface.

## Evidence

Local acceptance records are under
`D:/CodexAcceptance/universal-code-20261005/`, grouped by `root`, `model`,
`skills` and `mcp`. They include actual HTTP responses, CLI outputs, stdio MCP
transcripts and the two completed Skill tool records. API credentials and the
isolated databases are local only and are not repository artifacts.

Representative checks cover the installed DeepSeek provider, a native Skill
fixture and the product's stdio MCP implementation. They do not certify every
third-party provider, MCP transport, extension format or Desktop interaction.
