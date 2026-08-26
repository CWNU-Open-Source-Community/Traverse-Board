# Standard Code Delivery Truth Gate

`standard_code_delivery.v1` is the single public completion contract for a
Standard Code Run. It answers what changed, which commands actually reached a
terminal state, whether their output is complete, which final Workspace
Checkpoint is recoverable, and whether that evidence still describes the
current Drydock Workspace revision.

Agent prose and CI check names are presentation hints only. Neither can create
or upgrade a delivery conclusion.

## Closed statuses

| Status | Meaning |
| --- | --- |
| `passed` | Every selected verification Job completed with exit code `0`, its process tree was reaped, its bounded output Artifacts are present, and its post-command revision equals the final Checkpoint revision. |
| `failed` | Terminal command evidence proves a non-successful verification result. |
| `partial` | Evidence is incomplete, such as truncated output, a missing output Artifact, an incomplete Checkpoint, mixed results, or declared uncovered work. |
| `not_run` | No verification was run, including `no_applicable_tests` and `user_skipped`. |
| `blocked` | Verification could not reach a usable conclusion, including cancellation, timeout, missing dependency, exhausted budget, approval denial, or a non-terminal Job. |
| `stale` | The verification, Workspace revision, permission/backend generation, Drydock generation, or Supervisor mutation epoch no longer matches the receipt binding. |

Only `passed` sets `verified=true`. A later Workspace mutation never rewrites
the immutable receipt: the read projection retains `receipt_status` and adds a
fresh observation whose public `status` becomes `stale`.

## Exact evidence binding

Recording performs a bounded alignment loop:

1. Capture the final Run-owned Drydock Workspace Checkpoint.
2. Render a read-only combined Git patch from base commit through committed,
   index, worktree, conflict, and untracked state.
3. Re-observe the Workspace and accept the capture only when HEAD, branch,
   index, manifest, root, and root-path fingerprints still align.
4. Bind selected Command Runtime Jobs to their post-command Checkpoints,
   executable/environment digests, permission revision, adapter/backend
   generation, exit code, tree-reaped fact, output digests and Artifacts.
5. Evaluate one closed status and seal an immutable SQLite receipt plus
   `standard_code.delivery_recorded` Run event.

The receipt SHA-256 covers the complete metadata-only payload, including Run,
Mission, Session, source Workspace, Drydock Workspace/id/generation, base/head,
Diff digest, final Checkpoint revision, verification facts, reasons and
provenance digests. Store triggers independently align the receipt columns,
Run event, Drydock and final Checkpoint, and reject update or deletion.

## CLI and HTTP

Inspect the current freshness-aware projection:

```powershell
cyberagent run delivery report <run-id>
cyberagent run delivery report <run-id> --json
```

Record selected terminal Command Runtime Jobs:

```powershell
cyberagent run delivery record <run-id> `
  --operation-key <stable-key> `
  --verification-jobs <job-id-1>,<job-id-2> `
  --json
```

When no test can be run, declare the reason instead of fabricating evidence:

```powershell
cyberagent run delivery record <run-id> `
  --operation-key <stable-key> `
  --declaration no_applicable_tests
```

Other declarations are `user_skipped`, `budget_exhausted`,
`missing_dependency`, and `approval_denied`. A declaration and verification
Job list are mutually exclusive.

The authenticated read route and control-token record route share one path:

```text
GET  /api/v1/runs/{run_id}/standard-code-delivery
POST /api/v1/runs/{run_id}/standard-code-delivery
```

The POST body contains `operation_key`, `verification_job_ids`,
`uncovered_items`, and an optional `declaration`. OpenAPI is authoritative for
the bounded request and response shapes.

## Shared surfaces and navigation

Desktop Delivery, CLI JSON, HTTP, Code Handoff, Handoff export, GitHub Review,
and the Standard Code final response consume the same Go-owned `Report`.
Desktop links an affected relative path to Workspace Explorer, verification
output to the Artifact inventory, and Checkpoint/Undo/Rewind/Fork to the
Checkpoint panel. The report also exposes authenticated API links for the exact
file, Artifact, final Checkpoint timeline, and recovery operations.

A Standard Code `finish` action and lifecycle completion are denied unless the
latest projection is currently `passed` and `verified`. Any mutation clears the
Supervisor's prior verification and delivery binding.

## Privacy and recovery boundary

Public paths are normalized repository-relative paths. Unsafe paths are omitted
while their SHA-256 identity remains. Control characters, secret-like values,
and absolute host paths are removed from uncovered-item summaries; output is
represented only by bounded, redacted Artifact metadata and digests. Raw
environment values, raw or unbounded terminal output, credentials, private
reasoning, and source roots are not persisted in this protocol.

The final Checkpoint covers recorded Workspace content and Git index state. It
does not promise to reverse effects outside that Workspace. Recording does not
commit, push, merge, overwrite source files, or delete unattributed files. A
Run-owned Drydock/worktree is an ownership and recovery boundary; process,
network, and filesystem isolation comes from the separately qualified Local or
Docker backend.
