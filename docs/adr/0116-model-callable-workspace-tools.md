# ADR 0116: Model-Callable Workspace Tools

- Status: Accepted
- Date: 2026-08-19
- Scope: Code Surface root Supervisor; schema v115

## Context

Prayu already had a resumable multi-round Supervisor, user-facing Workspace
browsing, FileEdit proposals, Policy, approvals, budgets, and durable tool-call
records. The root model still could not directly inspect a real Workspace during a
tool round. It therefore had to depend on pre-attached context or user-operated
commands, and could not complete a credible search/read/change loop.

Exposing a generic filesystem API would let model output or repository instructions
escape scope, traverse links, read ignored or binary data, or mutate files without
the existing review boundary. A capability calculated only when prompts are built
would also become stale across Run recovery, mode changes, permission changes, or a
Workspace replacement.

## Decision

### Versioned registry and capability matrix

Go owns the `agent-code-tools.v1` registry and JSON Schemas for seven tools:

- `workspace_list`, `workspace_read`, `workspace_glob`, and `workspace_grep`;
- `workspace_change`, `workspace_apply`, and `workspace_delete`.

Code/Plan/root receives only the four reads. Code/Deliver/root receives the reads;
Code and Script Profiles additionally receive the three mutation workflow tools.
Review and Learn Profiles stay read-only. Cyber Surface and every Specialist receive
none of these tools.

Each advertised definition carries an opaque Go-only authority snapshot bound to
the Run, Mission, registered Workspace, canonical root fingerprint, root Agent,
Surface, Phase, Role, Profile, permission mode, mode revision, permission revision,
and registry generation. The authority is attached after model arguments are
decoded, is excluded from provider JSON, and is persisted with the call. Model JSON
cannot mint, alter, or widen it. Execution recomputes every binding and fails closed
on drift.

### Bounded reads

All paths are canonical Workspace-relative paths with exact on-disk casing. Reads
never follow symlinks, junctions, or other reparse points and reject root escape,
ignored entries, special files, binary or non-UTF-8 content, and configured
size/depth/result limits. `.github` is the sole dot-path allowlist because CI,
ownership, and contribution policy are code evidence; `.git` and every other hidden
namespace remain unavailable. Directory, glob, grep, and line reads use stable ordering
and opaque cursors so a model can request the next bounded page without receiving a
host root. Returned text is secret-redacted and explicitly untrusted evidence.

### Reviewed mutations

`workspace_change` never writes. It produces an immutable FileEdit proposal for one
replace, create, or move and records source/destination hashes and a bounded preview.
`workspace_delete` is separate so deletion requires its own explicit confirmation
and review. `workspace_apply` accepts only an approved exact proposal; immediately
before mutation Go re-resolves paths and compares the reviewed hashes, including the
destination's expected absence or identity. A mismatch is a conflict, not an
automatic retry. Mutation itself runs through a verified `os.Root` directory handle,
so a renamed parent or newly inserted link cannot redirect the operation outside the
opened Workspace. Create publishes a completed staging inode without clobbering a
concurrent target; move uses a no-clobber hard-link/remove sequence whose interrupted
two-link state is recognizable and recoverable. Existing FileEdit recovery and
idempotency semantics remain the only write path.

### Durable execution and product visibility

The Supervisor may execute multiple real tool rounds before returning to the model.
Each invocation, authority snapshot, normalized result or refusal, budget charge,
and bounded read Artifact is written to the durable Run ledger. Stable error
envelopes distinguish invalid input, unavailable capability, scope/Policy denial,
conflict, budget exhaustion, and internal failure without exposing roots or private
payloads. Secret-shaped mutation text is rejected during normalization, before model
completion arguments or tool-call records can be persisted.

`cyberagent run show`, the Run Detail HTTP/OpenAPI projection, and the Desktop Run
overview expose the protocol generation and per-tool availability/refusal reason.
The process-level capability endpoint only reports that the runtime exists; neither
projection is an authorization bearer. Legacy Runs without a registered Workspace
remain readable and show all tools unavailable.

## Consequences

- A Code root Agent can now discover and read source before proposing a reviewed
  change, including at least two consecutive real tool rounds.
- Plan remains non-mutating, Review/Learn remain read-only, and Cyber/Specialist do
  not inherit Code filesystem authority.
- File changes remain slower than an unrestricted editor because operator review is
  intentional and every apply performs compare-and-swap validation.
- This protocol grants no Shell, Git, network, browser, Sandbox, or package-install
  authority. Those retain independent gates and ledgers.
- Cursor results are bounded snapshots of an evolving Workspace, not a transactional
  filesystem view. A later page or apply may detect drift and require a fresh read or
  proposal.

## Verification

Tests cover the full Surface/Phase/Role/Profile matrix, strict schemas, root and case
escape, ignore/hidden/link/binary/encoding/size bounds, deterministic pagination,
redaction, replace/create/move/delete proposal review, destination and source
compare-and-swap conflicts, authority tampering and revision drift, budget/accounting
and Artifact persistence, migration of the existing Supervisor ledger, legacy Runs
without Workspaces, and a two-real-round `workspace_list -> workspace_read`
Supervisor conversation. The release gate also runs the complete Go, TypeScript,
frontend, production-build, OpenAPI, vet, and diff checks before merge.
