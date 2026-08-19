# Workspace Checkpoints, Rewind, and Fork

`workspace-checkpoint.v1` provides a bounded, auditable recovery layer for files
and the Git index inside one Run's exact Workspace. It complements Git; it does not
commit automatically, rewrite Git history, or claim to reverse effects outside the
Workspace.

## Recovery grades

| Grade | Meaning |
|---|---|
| `complete` | Every changed item represented by the manifest has recoverable bytes and the boundary has complete attribution. |
| `partial` | The known in-root state is recorded, but at least one item or attribution source is excluded or uncertain. Command batches are partial while no filesystem watcher is installed. |
| `unavailable` | The root/Git identity, case, index, links, or required content cannot be materialized safely. Restore/Fork is blocked. |

Manifest storage policies include `stored`, `missing`, `excluded_ignored`,
`excluded_generated`, `excluded_large`, `excluded_sensitive`, `excluded_link`,
`excluded_special`, and `unreadable`. Every exclusion has a bounded reason. Binary
files are supported when within quota; they are never converted to text. LF, CRLF,
mixed, and no-newline content is hashed and restored byte-for-byte.

The global store is bounded to 2 GiB of blobs, 10,000 checkpoints, 2,000,000
manifest entries, and 20,000 mutation transactions. SQLite enforces these limits
under concurrent writers. Reaching a hard metadata limit returns
`RESOURCE_EXHAUSTED`; immutable retained history is not silently pruned.

## Safe operating sequence

1. Pause the Run and ensure no execution lease or background mutation remains.
2. Read the timeline and retain `current.current_checkpoint_id`.
3. Preview the chosen target. Review every path, index change, recovery grade, and
   conflict.
4. Confirm with that exact expected-current checkpoint and a new stable operation
   key. The service re-runs all checks; a stale preview conflicts.
5. Inspect the appended post-checkpoint and transaction. The old timeline remains
   unchanged.

Manual capture returns `CONFLICT` while a mutation boundary for the same Run is
open. Finish or reconcile that writer first; capture never advances the cursor
underneath an in-flight file, command, Git, or merge operation.

Restore also requires Code/Deliver mode and a current `approval`, `full_access`, or
`debug` permission allowed by the process startup gates. A historical permission is
never enough.

## CLI

Read-only and capture operations:

```powershell
cyberagent workspace checkpoint timeline --run <run-id> --limit 100
cyberagent workspace checkpoint capture --run <run-id> `
  --operation-key checkpoint-before-refactor --title "Before parser refactor"
cyberagent workspace checkpoint preview --run <run-id> `
  --checkpoint <target-checkpoint-id> --expected-current <current-checkpoint-id>
```

Confirmed restore uses the process flags matching the Run's current permission.
For an `approval` Run:

```powershell
cyberagent workspace checkpoint rewind --run <run-id> `
  --checkpoint <target-checkpoint-id> --expected-current <current-checkpoint-id> `
  --operation-key rewind-parser-1 --confirm --enable-permission-control

cyberagent workspace checkpoint undo --run <run-id> `
  --expected-current <current-checkpoint-id> --operation-key undo-parser-1 `
  --confirm --enable-permission-control

cyberagent workspace checkpoint redo --run <run-id> `
  --expected-current <current-checkpoint-id> --operation-key redo-parser-1 `
  --confirm --enable-permission-control
```

Add `--enable-danger-full-access` for `full_access`, and both that flag and
`--enable-debug-maximum-access` for `debug`.

Fork requires an absent destination under an existing real parent directory and a
new valid Git branch:

```powershell
cyberagent workspace checkpoint fork --run <run-id> `
  --checkpoint <target-checkpoint-id> --expected-current <current-checkpoint-id> `
  --operation-key fork-parser-1 --workspace-name parser-fork `
  --workspace-root D:\worktrees\parser-fork --branch codex/parser-fork `
  --goal "Continue from the reviewed checkpoint" --confirm `
  --enable-permission-control
```

`--workspace-root` is an operator-only CLI input. HTTP/Desktop Fork requests do
not accept an absolute host path: Go deterministically selects an absent sibling
named `prayu-fork-<operation-digest-prefix>` from the trusted source Workspace.
The renderer receives only the new Workspace/Run IDs and never the root path.
For crash recovery, SQLite privately retains the normalized destination and branch
on the prepared Fork transaction. Those fields have no JSON representation and are
not projected by timeline, HTTP, OpenAPI, Desktop, or events. If a crash occurs
before Run registration, startup verifies source/destination/branch/full commit,
the complete manifest, and the raw Git index before using Git's registered-worktree
removal; any drift stops cleanup, preserves later user edits, and leaves the
transaction open for a safe retry.
Startup also refuses to reconcile any open transaction whose Run still has an
unexpired execution lease. This prevents a second process from closing a live
writer's boundary; after a real crash, retry startup after that lease expires.
If a crash lands between boundary preparation and the `before` cursor CAS, startup
advances only the exact expected cursor before recording the interrupted partial
result. If a non-Fork transaction is already terminal but its final cursor CAS was
not committed, startup advances only from its exact `before` checkpoint to its
terminal `after` checkpoint. Fork never advances the source cursor.

CLI results are JSON so checkpoint IDs, recovery reasons, conflicts, and replay
status remain machine-readable.

## HTTP/OpenAPI

```text
GET  /api/v1/runs/{run_id}/workspace-checkpoints?limit=100
POST /api/v1/runs/{run_id}/workspace-checkpoints
POST /api/v1/runs/{run_id}/workspace-checkpoints/preview
POST /api/v1/runs/{run_id}/workspace-checkpoints/rewind
POST /api/v1/runs/{run_id}/workspace-checkpoints/undo
POST /api/v1/runs/{run_id}/workspace-checkpoints/redo
POST /api/v1/runs/{run_id}/workspace-checkpoints/fork
```

GET uses the read bearer. POST uses the distinct control bearer, strict JSON with
duplicate/unknown-field rejection, and bounded bodies. Rewind, Undo, Redo, and Fork
require `confirm: true`; operation keys are carried in the body and may also be
bound to the normal `Idempotency-Key` header by clients. See `docs/openapi.json` for
the exact generated schemas.

## Desktop

Open a Run and choose **工作区检查点 / Checkpoints**. The left column is the
immutable timeline; the detail pane explains trigger receipt, attempt, capability
generation, Git identity, stored bytes, recovery level, and incomplete reasons.
Preview lists affected paths and index drift before the confirmation button is
enabled. Conflicts remain visible and disable confirmation. Fork switches to the
new Run only after the Go-derived independent worktree and final checkpoint are
verified; React does not submit or receive an absolute worktree path.

## Failure interpretation

- `CONFLICT`: the cursor or a touched path/index changed after review. Refresh and
  preview again; never retry with a fabricated expected cursor. This also covers a
  manual capture attempted while another mutation boundary is open.
- `FAILED_PRECONDITION`: the Run is not paused, a lease is live, there is no
  reversible boundary, or the checkpoint cannot be materialized.
- `POLICY_DENIED`: current mode/permission/process capability does not authorize a
  restore. Changing a historical checkpoint cannot change this decision.
- `RESOURCE_EXHAUSTED`: a per-file, per-checkpoint, preview, conflict, or global
  content-store bound was reached. Exclusions remain explicit; the implementation
  does not silently truncate into a successful complete restore.

Full design and residual risks are recorded in
[ADR 0118](adr/0118-transactional-workspace-checkpoints.md).
