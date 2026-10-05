# ADR 0166: Thread-owned application services and browser preview

Status: accepted for issue #274 implementation.

## Context

The conversation can draft a request to start a project and can open a managed
Full CDP browser, but those two actions previously had no shared presentation of
the resulting Command Runtime Job, its output or its candidate application URLs.
A browser session being ready does not prove that a development service started,
and closing that browser must not stop a different process or the entire task.

## Decision

Keep application startup in the ordinary Thread turn. The operator reviews and
sends the drafted request; the Supervisor uses the existing `command-runtime.v2`
start action, with its normal permission, approval, policy, workspace and runtime
ownership checks. This feature adds no HTTP command-launch endpoint, shell input,
process manager, database migration or new way to activate execution permission.

Add `thread_application_services.v1` as an additive HTTP/OpenAPI projection:

- `GET /api/v1/threads/{thread_id}/application-services` lists bounded Job metadata
  across that Thread's original and successor Runs. It reports truncation and does
  not load every Job's complete stdout/stderr.
- `GET /api/v1/threads/{thread_id}/application-services/{job_id}` reads one exact
  Job, its bounded sanitized output and candidate local URLs. Every response
  retains the source Thread, Run and Job, rather than assigning the current Run
  to an older process.
- `POST /api/v1/threads/{thread_id}/application-services/{job_id}/stop` accepts the
  expected Run and the stable `application-stop-{job_id}` idempotency header. It
  requires control authentication and the existing Run execution control gate.
  It performs cleanup of that exact process-owned Job only.

Public views contain Job state, timestamps, optional exit code and verified
source-call/message identities. They exclude native PIDs, process groups, host
paths, command intent/environment, permission snapshots, leases, owner tokens,
operation digests and launch authority. The original invocation is matched to
its durable Supervisor start call by the existing Job operation identity, then
to the operator message through the existing delivery ledger. Missing historical
provenance stays absent; creation time or a matching URL is not a substitute.

The frontend keeps the draft/submission hint separately from server facts. It
distinguishes a drafted request, pending or unknown submission, accepted input
and a sealed execution failure. A Job is associated with that input only when
both its Run and source message match. Other Jobs remain individually selectable.
No Job is an explicit absence of evidence, not proof of startup or failure.

## Output and preview

Reuse the current Command Runtime output sanitization and closed-stdin read
boundary. Output retains separate stdout/stderr text, page byte cursors and
truncation information; coalesced streams do not claim interleaved frame ordering.
Interactive output that could echo private stdin is not exposed.
Candidate URLs are bounded HTTP(S) loopback addresses extracted from that output;
they contain no userinfo, query or fragment and are always marked unverified.
Discovery performs no network probe and does not grant network or browser access.
The operator explicitly selects one of multiple candidates before opening it.

Reuse the existing Full CDP session and preview APIs. The selected service's Run
owns its browser preview; changing the selection does not silently transfer that
browser to the latest Run. The existing live Full activation, browser permission
revision and loopback navigation checks still apply. Exited or failed services do
not enable candidate-driven preview. Manual address entry remains an explicit
action under the currently selected browser Run's existing authority.

Show service state and browser state separately. Hiding the preview panel is a
presentation action; closing the browser cleans only that session; stopping the
service addresses only its exact Job. A ready browser or text claiming a URL
does not relabel a failed, stopped, interrupted or unobserved service as ready.

## Cleanup, replay and recovery

Service stop is a cleanup-only adapter to the existing manager's cancellation
and process-tree ownership. It cannot launch, resume or adopt a Job, write stdin,
cancel its Run or signal a PID read from persistence. The Thread/Run binding and
full immutable Job/owner identity are checked before reaching the native manager.
Revocation or a terminal Run must not create new authority, but must not prevent
cleanup of a process still owned by this host. A cold manager cannot adopt it.

The stable cleanup key is derived from the immutable Job identity, so uncertain
responses are retried against the same resource. Terminal receipts are readable
and replayable without a new effect. Existing stopping state remains observable;
an unconfirmed or failed stop is not displayed as completed cleanup. Existing
Command Runtime shutdown, heartbeat expiry and crash recovery remain authoritative.

## Alternatives and verification

A direct operator-start HTTP endpoint would broaden the execution surface and
duplicate the conversation's project discovery and approval path. Inferring
services from arbitrary transcript URLs would lose process ownership and confuse
failed output with a running server. Both approaches are rejected.

Verification covers source-message association, cross-Thread and wrong-Run
refusals, cold owners, repeated stop, output bounds/redaction, multiple URL choices,
and independent browser/service actions. Real loopback service fixtures use the
existing authorized Command Runtime, temporary workspaces and owned process
cleanup; browser UI contracts are tested separately from native browser execution.

Rollback removes the new presentation and HTTP adapters while retaining Job,
output and delivery history and the established Command Runtime cleanup paths.
No durable state is rewritten or downgraded.
