# ADR 0114: Real Shell Transports And Supervised Debug Terminal

- Status: Accepted
- Date: 2026-08-18
- Scope: Code Surface only; schemas v108 and v113
- Supersedes: ADR 0081 and ADR 0110 only where they state that approval cannot carry canonical Shell text or that Desktop/models have no Debug terminal route

## Context

Prayu already executed real typed Git operations, four conservative diagnostics,
one-shot native processes, and a user-owned Windows ConPTY terminal. Two gaps made
the Code workflow materially weaker than Codex- or Claude-Code-style development:

1. Approval mode could review literal argv but could not express ordinary PowerShell
   or Bash pipelines and built-ins.
2. The Debug lease controller existed only inside Go, so an operator could neither
   grant it from Desktop nor let a root Supervisor observe and use the current
   terminal.

Turning either gap into an always-on raw model Shell would erase the existing
permission tiers. The implementation therefore keeps one-shot approval and
persistent Debug ownership as separate contracts.

## Decision

### Canonical one-shot Shell proposals

`host_command_proposal.v1` gains an explicit transport discriminator:

- `process`: absolute executable plus literal argv, preserving the previous form;
- `shell`: `powershell|bash` plus exactly one bounded UTF-8 command line.

For Shell proposals, Go selects the executable and constructs the only accepted
argv. PowerShell uses `-NoLogo -NoProfile -NonInteractive -Command`; Bash uses
`--noprofile --norc -c`. Newlines, NUL/control ambiguity, secret-like material,
environment values, stdin, persistence, and background ownership are absent from
the proposal protocol.

Execution of this review path remains Windows-only because the existing host
executor's creation-time Job Object, executable pinning, output bounds, and receipt
contract are Windows-specific. PowerShell resolves from trusted Windows locations.
Git Bash resolves from a known Git for Windows installation or is derived from the
exact `git.exe` selected by `PATH`; the legacy System32 WSL `bash.exe` shim is never
used as a fallback. The resolved interpreter is hashed, stored as ordinary exact
argv, and shown in the existing approval UI. A separate operator review still
authorizes only one execution.

### User-owned persistent terminal

The terminal remains default-off and user-owned. Windows keeps the existing
PowerShell ConPTY and creation-time kill-on-close Job Object. macOS and the shared
POSIX implementation use Bash without startup files, a real PTY, a new session and
owned process group; closing the terminal sends bounded TERM/KILL cleanup and closes
the PTY. The caller's start context authorizes creation only and does not own the
persistent process lifetime.

### Desktop grant and root Supervisor tool

Desktop exposes three bounded native methods: grant, query, and revoke. A grant
requires the current Code/Local/Debug snapshots, Debug maximum-access process gate,
the already-running terminal, two explicit confirmation fields, and a TTL between
15 seconds and 15 minutes. The UI uses five minutes and displays immediate revoke.
Only Go receives the random bearer; renderer and model projections contain a
token-free binding ID and expiry.

The `debug_terminal.v1` model tool is advertised only when all of these hold:

- root Supervisor;
- Code Surface and Deliver Phase;
- durable `debug` permission;
- installed Debug terminal runtime adapter.

The tool can submit one complete command line or read one cursor-addressed page.
Every call revalidates Run, Workspace, Session, the exact mode revision,
interaction/profile/permission revisions, a process-local Workspace-root digest,
runtime capability, lease identity, and expiry. Every write runs the
ordinary Shell Policy. A denial or command requiring separate per-command approval
does not reach the PTY. Writes are digest-idempotent inside the lease; a partial or
uncertain write cannot be retried automatically.

Reads do not consume the renderer's ring. Model output is capped at 64 KiB, repaired
to UTF-8, stripped of terminal control sequences, secret-redacted, and marked as
untrusted data. Grant captures the current ring watermark and all model reads clamp
to it, so user scrollback from before the lease cannot enter model context. Output
is explicitly incomplete so the model must use the returned cursor for later pages.

### Persistence boundary

The model command is a canonical Supervisor tool argument and the sanitized bounded
result is a Supervisor tool result. Both are durable so a Run can explain and resume
its tool round. Schema v113 transactionally rebuilds the Supervisor tool-call table
to admit `debug_terminal`, copies all v112 calls, and restores its index and guards.
Operators are warned not to put secrets in commands, and secret-like commands are
rejected before persistence/execution.

The random bearer, user keystrokes, raw PTY byte stream, Workspace root path,
process environment, and process identity are not persisted. Agent-input audit events retain identities,
digests, sizes, and state only. Schema v108 defines a `terminal_sessions` Store
contract, but the current Desktop lifecycle does not synchronize or restore from
that table; no row can revive a process or lease.

## Consequences

- Prayu can run real PowerShell and Git Bash workflows instead of only executable
  argv, while keeping per-command review separate from Debug access.
- Debug supports iterative CLI work and background jobs within one owned terminal,
  but only after a visible time-bound grant.
- The POSIX backend owns the Bash process group, not an OS containment boundary;
  a command that deliberately creates a new session/daemon may escape group cleanup.
  Debug maximum access and the UI warning make this residual host risk explicit.
- The behavior is intentionally not identical to an unrestricted always-on Shell:
  Plan, Cyber, Specialist, conservative/approval mismatches, missing runtime gates,
  expired grants, and Policy-sensitive commands fail closed.
- POSIX PTY support does not imply a Linux Desktop product or a POSIX one-shot host
  executor. Those require separate product and containment evidence.
- A future durable terminal metadata adapter must synchronize schema v108 lifecycle
  rows without ever restoring process or input authority.

## Verification

Tests cover canonical Shell argv and tamper rejection, Git Bash distribution
resolution, strict Tool schemas, Code/Deliver/root exposure, Cyber/Plan/permission
denial, dangerous and approval-required command denial, single-live-binding and
expiry replacement, pre-grant output fencing, cursor reads, ANSI stripping/redaction, exactly-once writes,
ambiguous-write refusal, stable replay output cursors, Desktop bridge field allowlists,
visible UI confirmation, v112-to-v113 ledger preservation and admission, Windows
terminal regressions, and Linux/macOS POSIX compile coverage. The release gate also
runs the complete Go, frontend, TypeScript, production-build, dependency, OpenAPI,
and diff checks before merge.
