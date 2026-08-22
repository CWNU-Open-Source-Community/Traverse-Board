# ADR 0129: Run-Owned Drydock Workspaces and Workspace Trust

- Status: Accepted
- Date: 2026-08-22
- Scope: GitHub Issue #131 and schema v127

## Context

Standard Code needs build tools that may change repository files, while model-proposed
commands must not run against the registered source Workspace. A Git worktree alone
does not isolate a process, network, credentials, the host filesystem, or the user
account. What the control plane can provide here is a durable ownership and recovery
boundary around one product-created worktree.

The boundary must survive a crash without guessing which directory belongs to the
product. It also must not treat Workspace Trust, a persisted row, or a branch name as
runtime execution authority.

## Decision

Schema v127 adds `drydock-workspace.v1`. One eligible Code Run may own one Drydock,
which is bound to the exact Run, Mission, Session, source Workspace, source root hash
and fingerprint, Git repository/common-dir identity, attached source branch, base
commit, object format, deterministic product-managed path, managed-worktree registry
row, dedicated local branch, ownership generation, and current Git binding.

The Application service accepts no arbitrary Git argv or destination path for create,
use, delivery, or cleanup. Creation uses the existing closed advanced-Git worktree
templates under the CLI-fixed `$CYBERAGENT_HOME/drydocks`. The limits are 64 active Drydocks in one
installation, eight for one repository, a seven-day default lifetime, and a thirty-day
maximum lifetime.

The source must be an attached branch with no in-progress sequence or conflict. Source
tracked, untracked, ignored, and raw-index state is recorded for review. Dirty source
content is deliberately excluded from the new worktree rather than copied implicitly.
For v1, a source containing Git symlink entries, submodule gitlinks, or a root/repository
path that traverses a symlink or Windows reparse point is rejected explicitly. Paths
with spaces, CJK characters, and platform case rules remain literal identities and are
handled by the existing canonical path and checkpoint contracts.

## Workspace Trust

The first create call is a non-mutating review. It returns an exact
`drydock-workspace-trust.v1` digest over the complete source identity and observed
source state. Creation requires a second call with both an explicit confirmation and
that exact digest. Any intervening source, index, branch, root, or repository drift
changes the digest and fails closed.

The immutable Trust receipt fixes `grants_process_authority=false`. Trust does not
enable Shell, a command runtime, network, credentials, Git remote writes, merge, or
cleanup. A separate runtime adapter and its own current authority gates remain required
before any build or model tool can execute in the Drydock.

## Lifecycle, Checkpoints, and Recovery

The durable lifecycle is:

```text
preparing -> ready -> delivered
     |          |          |
     +----------+----------+-> recovery_required
                                  |        |
               explicit checkpoint        +-> cleaned (exact absence; no deletion)
                       |
                       +-> ready

ready | delivered -> cleaned
```

Creation persists a write-ahead `preparing` owner before `git worktree add`. It then
captures a baseline `workspace-checkpoint.v1` containing tracked and untracked content
plus the exact raw index. A startup reconciler may finish only an exact, clean,
identity-matching materialization. Otherwise it records `recovery_required` and
preserves the directory.

`use`, `checkpoint`, `rewind`, `undo`, `fork`, `deliver`, and `cleanup` revalidate the
source identity, Drydock root fingerprint, registered worktree, branch, base ancestry,
expected binding, Run binding, and ownership generation. Checkpointing changed content
requires explicit operator attribution. Rewind and Undo first produce a three-way
preview and then append a new exact checkpoint; they do not rewrite checkpoint history.
Tracked files, untracked files, staged state, and raw index bytes are verified after
restore. Rewind/Undo require current and target to share the current HEAD, while Fork
may materialize any checkpoint whose commit still descends from the fixed Drydock base.
A conflict or incomplete recovery preserves the worktree and requires operator recovery.

The Drydock's `LastCheckpointID` is its own cursor. It does not replace the same Run's
source-Workspace checkpoint cursor. The package-internal Fork adapter supplies that
exact owned cursor to the existing Fork implementation without changing ordinary
source Timeline, Undo, Redo, or Rewind state.

Fork delegates to the existing checkpoint Fork contract and creates a distinct
Workspace, Mission, Run, Session, branch, and worktree. It inherits no approval,
credential, capability, lease, process, network, terminal, or execution authority.

Every completed or preserved transition appends a `drydock-lifecycle-receipt.v1` and a
Run event. Trust confirmation and create preparation have their own write-ahead events;
create completion, use, checkpoint, rewind, undo, fork, delivery, cleanup, interrupted
recovery, and successful reconciliation have lifecycle receipts and events. Operation
keys are intent-bound and replay-safe.

## Delivery and Cleanup

Delivery recomputes an attributed checkpoint and returns a bounded review patch and
diff receipt against the fixed base commit. The proposal permanently records:

```text
automatic_merge=false
push_authorized=false
force_authorized=false
source_overwrite_allowed=false
```

It never merges, pushes, force-updates, changes the source branch, or copies files over
the source Workspace.

Cleanup requires explicit confirmation and exact current generation. It removes only a
registered, complete-identity, clean Drydock through non-force `git worktree remove`.
The local branch and audit rows remain. Dirty/untracked/ignored content, identity drift,
missing or ambiguous registration, and any Git refusal preserve the directory and move
the owner to `recovery_required`. Expiry GC uses the same path and never scans for or
deletes unknown directories. Temporary delivery indexes are deleted only when their
creation and ownership are known to the current invocation. If Git removal completed
before a crash but the durable transition did not, an explicitly confirmed cleanup may
close the ledger only after both the exact registration and deterministic path are
absent; that recovery path performs no filesystem deletion.

## Consequences

- Model/build execution can be directed away from the source Workspace once a separate
  authorized runtime adapter consumes the Drydock binding.
- Workspace Trust is review evidence, never durable execution authority.
- Crash recovery converges from durable identity and preserves uncertainty instead of
  guessing ownership.
- Disk capacity is bounded without permitting blanket or force cleanup.
- v1 rejects link/reparse-point roots and submodule layouts rather than silently
  weakening exact checkpoint and deletion guarantees.

## Verification

Real Git integration tests cover clean and dirty sources; spaces and CJK paths; tracked,
untracked, staged, and raw-index checkpoint fidelity; Rewind and Undo; independent Fork;
symlink and submodule rejection; source/root/branch/base and cross-Run drift; interrupted
create reconciliation; repeated operation keys; review-only delivery; exact cleanup;
and expiry after a crash with a user file that must remain. Store tests cover v126 to
v127 upgrade, downgrade/re-upgrade, immutable Trust, ownership generations, synthetic
Workspace checkpoint scope, delivery authority constraints, events, and receipts.

## 中文结论

Drydock 是由 Run 持有、可验证身份并可恢复的产品工作目录。首次使用必须审阅并确认精确的来源
Workspace、仓库、分支、base、root 指纹和 dirty/index 状态；这份 Trust 回执始终不授予进程、
网络、凭证或 Git 远端权限。来源未提交内容不会被暗中复制。

崩溃后只能恢复完整匹配的干净目录。任何用户改动、未跟踪文件、身份漂移或未知目录都会被保留，
并要求操作者处理。交付只生成审阅 patch；不会 push、force、merge 或覆盖来源。自动清理同样只
能删除完整证明为产品所有且干净的工作目录。
