# ADR 0098: Bounded Docker Container I/O Contract

Date: 2026-08-14

## Status

Accepted.

## Context

The durable Docker lifecycle slice (schema v97, ADR 0096) proved exact
Stage/create/start/wait/terminate/cleanup ownership, but a container still had
no product-grade I/O contract: the Workspace boundary, log capture, output
extraction, and Artifact commit were not composed into one bounded,
fail-closed transaction set. Issue #39 requires that combination while
keeping product admission closed: no CLI, HTTP, Desktop, Agent, or Tool path
may reach it.

Container output is untrusted evidence. Nothing a container writes may
automatically become prompt context, an approval decision, a command, or host
executable content, and the original Workspace must stay read-only from the
container perspective.

## Decision

1. Read-only input projection (sandbox_docker_input_projection.v1): a sealed
   manifest of canonical container-relative paths, SHA-256, size, and media
   type, bound to the lifecycle attempt/generation, plan, observation, Run,
   Mission, Workspace, and spec fingerprints. The input mount target stays
   fixed at /run/cyberagent/inputs with read-only access; the spec invariant
   of exactly one writable (dedicated output) mount is unchanged. A mount
   isolation verifier checks real inspection mounts: the output target is the
   only writable mount in its tree and the input/workspace trees are
   read-only.
2. Bounded log capture (sandbox_docker_log_capture.v1): stdout/stderr arrive
   only through the fixed POST containers/{id}/attach with
   logs=1&stderr=1&stdout=1&stream=0 (no live streaming). A demuxer bounds
   each stream by bytes (256 KiB) and lines (4096), enforces a wall-clock
   deadline, rejects malformed/oversized frames, replaces invalid UTF-8 and
   counts the violations, redacts secrets, and records only metadata plus
   digest-only receipts. Raw bytes never persist.
3. Output staging (sandbox_docker_output_staging.v1): the dedicated output
   mount is exported only through GET containers/{id}/archive?path=<target>.
   The tar walker rejects absolute/traversal paths, Windows separators,
   drive letters, symlinks/hardlinks/devices, duplicates, and non-canonical
   names; caps file count (64), per-file size (4 MiB), and total size
   (16 MiB); detects media types; redacts text content; and writes only
   validated regular files into a Run-scoped process-local staging directory.
   Rejected archives carry no trusted manifest.
4. Atomic output commit (sandbox_docker_output_commit.v1): an accepted
   manifest must exactly match completed staging entries (path, digest,
   size, media type). The staged files are re-read and re-hashed, then the
   receipt and every entry commit in one SQLite transaction keyed by a
   replay-safe operation key digest. Failure leaves no partial rows.
5. Schema v98 persists the four ledgers with strict CHECKs and foreign keys;
   events record prepared/acquired/taken-over/failed/completed transitions.
   The DockerContainerIOService is exported for tests only; no product entry
   is wired, matching the lifecycle slice boundary.
6. Windows and Linux share one path rule set, so the same adversarial path
   matrix is exercised on both CI hosts.

## Consequences

- Every container I/O step is bounded, digest-bound, replay-safe, and
  fail-closed; the Workspace can only be modified through the already-gated
  FileEdit boundaries, never by the container.
- Product admission (Policy/Approval/Budget/Network gates and CLI/HTTP/
  Desktop wiring) remains a separate slice; this contract does not authorize
  anything.
- Raw logs and staged files are process-local and never reach SQLite or
  events; only receipts and digests are durable.

