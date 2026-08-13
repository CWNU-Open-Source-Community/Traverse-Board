# ADR 0095: Non-Authorizing Real Docker Lifecycle Probe

Date: 2026-08-13

## Status

Accepted as a Sandbox engineering probe. It is not a product execution gate.

## Context

The schema-v63 Docker start-gate review defined a future lifecycle but kept all
start, process, output, and Artifact authority false. Later Sandbox work proved
deterministic container configuration and never-started resource ownership, but
Prayu had not exercised a real container process through start, wait, timeout,
cancellation, TERM/KILL escalation, and exact cleanup.

The next safe step is to validate those daemon mechanics without exposing an
Agent, CLI, HTTP, Desktop, or generic Runner entry point and without claiming
that the durable production start gate is complete.

## Decision

1. Add a private, non-authorizing lifecycle transport on the fixed local Docker
   endpoint only: `/var/run/docker.sock` on Unix and Docker Desktop's Linux
   engine named pipe on Windows. It ignores `DOCKER_HOST`, proxies, arbitrary
   sockets, and TCP endpoints and refuses redirects.
2. Reuse the existing strict Stage path to inspect an already-present exact
   image digest, create one deterministic stopped container, and verify its
   non-root, read-only, capability, resource, mount, and network-disabled
   configuration before start.
3. Limit lifecycle HTTP operations to exact container inspect, start,
   `wait?condition=not-running`, `kill?signal=SIGTERM|SIGKILL`, and non-forced
   delete with `v=1`. Pull, build, exec, attach, stdin, logs, export, arbitrary
   signals, generic requests, and network configuration are unavailable.
4. Natural exit verifies the final stopped state before exact deletion. Timeout
   and caller cancellation use an independent bounded cleanup context, send
   SIGTERM, wait for the manifest grace period, escalate to SIGKILL only when
   necessary, verify the final state, delete the same container, and confirm
   name absence.
5. Bind the request to the exact Stage receipt, generation, configuration, and
   explicit `RUN-LOCAL-DOCKER-LIFECYCLE-PROBE` confirmation. Raw container IDs
   remain transient. Request and result validation permanently keep product
   entry, product execution, output export, and Artifact commit authority false.
6. Keep the fixed-endpoint constructor package-private. The existing exported
   Windows Docker writer remains unsupported, so this probe cannot be selected
   by a Run, model, Tool, CLI, API, or Desktop control.

## Verification

Unit tests cover natural exit, timeout escalation, cancellation fan-out,
start-failure cleanup, configuration tampering, exact confirmation, authority
tampering, redirect and duplicate-JSON rejection, response-body cancellation,
and the closed HTTP allowlist.

An opt-in real-daemon test ran against Docker Desktop's Linux engine on Windows
using a pre-existing environment-free image digest. It observed Stage/create,
start, a blocked wait, SIGTERM, grace expiry, SIGKILL, exit code 137, final
inspection, deletion, and confirmed absence. The test includes an exact-resource
cleanup guard and neither enumerates nor mutates unrelated containers.

## Security And Capability Boundary

This probe demonstrates lifecycle mechanics, not a complete Sandbox. It does
not satisfy schema v63's durable write-ahead start intent, generation lease,
fencing, restart reconciliation, orphan ownership, bounded logs, output export,
or Artifact transaction requirements. It also does not prove a product network
policy beyond the existing `network=none` profile.

Therefore every product capability remains disabled. A future slice must move
the exact lifecycle through SQLite-backed ownership and recovery before any
operator preview can be considered; Agent autonomy remains a later, separately
reviewed gate.

## Consequences

Prayu now has a tested real-daemon lifecycle primitive on both fixed local
endpoint classes, including Windows NPipe support, without weakening the public
Sandbox boundary. The next Sandbox slice can focus on durable lifecycle
ownership instead of rediscovering process termination behavior.
