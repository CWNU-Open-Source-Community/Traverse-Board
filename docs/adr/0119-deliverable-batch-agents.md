# ADR 0119: 可交付 child、Worktree 隔离与独立合并复核 / Deliverable Children, Worktree Isolation, and Independent Merge Review

Date: 2026-08-20

## Status / 状态

Accepted for schema v118 and `batch-delivery.v1`.

已接受，用于 schema v118 与 `batch-delivery.v1`。

## Context / 背景

The existing core Specialist and read-only fan-out paths can run bounded analysis,
but they deliberately expose no workspace or Git tools. Letting multiple workers
write the source checkout or inherit the root's complete authority would create a
shared mutable index, ambiguous ownership, unaudited credentials/network access,
and unrecoverable crash windows. An author's completion text is also insufficient
evidence for a merge.

现有核心 Specialist 与只读 fan-out 能做有界分析，但明确没有 Workspace/Git 工具。
如果让多个 worker 共写 source checkout 或继承 root 全部 authority，会产生共享可变
index、模糊文件所有权、未审计的网络/凭证入口与无法收敛的崩溃窗口；作者的“完成”文字
也不能作为合并证据。

## Decision / 决策

### 1. Separate delivery contract, not a Specialist privilege escalation

`batch-delivery.v1` is a separate Application contract bound to an approved and
admitted `core` child-task proposal. It reuses the admitted Agent identities, DAG,
budgets, and expected artifacts, and rejects any drift. It does not change the
no-tool `SpecialistRunner` or read-only fan-out runtime. A batch has at most two
tasks, matching the existing core-child ceiling; nesting and child-created children
remain impossible.

### 2. One branch, worktree, generation, lease, and token per mutable child

Go captures the clean source branch and full base commit before creating deterministic
sibling worktrees. Every child record binds the exact Agent, branch, root, base,
tool-profile fingerprint, generation, and expiry. A random owner token is returned
only once; SQLite stores only its digest. Exact-generation rotation fences stale
writers and is capped at eight generations. HTTP/Desktop safe projections omit all
roots, token digests, operation fingerprints, and integration paths.

### 3. Closed tools and path ownership

The child profile is an exact value rather than an extensible allowlist. It permits
bounded owned-scope list/read/search, reviewed create/replace application, Git
status/diff, and a fixed local commit. It denies deletion/rename, Shell, arbitrary
processes, network, credentials, Debug terminals, approvals, and child spawning.
Every invocation revalidates token digest, generation, lease, Agent, plan lifecycle,
DAG dependencies, root/branch/base identity, and ownership. Repository or model text
cannot alter that profile.

Ownership hints are normalized repository-relative file/directory identities. Any
cross-task equal or ancestor/descendant overlap rejects the plan before materialization;
actual changed paths are rechecked during edit, commit, submit, review, and merge.

### 4. Durable mailbox, receipt, and commit WAL

Dispatch, acknowledgement, progress, questions, evidence, review readiness, requested
changes, acceptance, and abort are append-only generation-scoped mailbox facts with
monotonic sequence and idempotent operation identity. Receipts bind base/head, full
merge-base diff and function-context call-chain digests, changed paths, diffstat,
validation output digests, evidence, and limitations. A dirty worktree can never
produce a receipt.

The commit tool persists an immutable intent containing prior HEAD and message digest
before Git mutation. Crash recovery accepts only one clean, direct, non-merge child
commit whose sole parent is that prior HEAD, with the fixed author/email/message,
owned paths, and no delete/rename/copy. External multi-commit or dirty advancement
is preserved for operator inspection rather than being misclassified as completion.

### 5. Independent review and ordered local integration

Submit and Review both reload the exact receipt state, recompute the complete merge-base
diff and call-chain digest, rerun every required validation, and then measure the exact
branch/HEAD/diff/clean state again before recording success. Review additionally requires
explicit reviewer attestation for diff, call chain, and tests before acceptance. The
author summary is non-authoritative. Desktop makes this attestation visible and required.

The merge queue never mutates the source or child worktrees. It creates a separate
integration branch/worktree at the current confirmed base and applies every accepted
head in a DAG-valid order. Source-base drift requires a distinct explicit replay
confirmation. Changed-file overlap blocks before merge. Each step has a durable pre/post
head and reruns the cumulative validations for every task in the merged prefix. After
validation it re-attests the source, deterministic merge commit, common-repository
binding, clean integration state, and every accepted child receipt. State drift blocks
and preserves uncertain evidence; only an exactly attested textual or semantic/test
failure may reset the current step. Crash recovery accepts the exact two-parent merge
tree/metadata, not an arbitrary descendant. Earlier successful integration commits
remain, while all child worktrees remain untouched. The result is a local integration
head; remote push, PR creation, and remote merge are out of scope.

### 6. Validation is an explicit host-execution capability

`git_diff_check` runs fixed Git inspection and is available by default. Go and npm
tests execute child-authored code, so environment filtering cannot truthfully make
them child-no-process or network-contained. They therefore fail closed unless the
API process is explicitly started with permission control, danger-full-access, and
`--enable-batch-validation-execution`, and the current Run remains running with a
fresh `full_access` or explicitly higher `debug` permission at every execution sink. Desktop batch mutation is a
separate `--enable-batch-delivery-control` capability rather than an implication of
holding an unrelated control token. Runtime capabilities expose those facts.

When enabled, validation uses fixed argv, a ten-minute deadline, no Go test cache,
closed stdin, bounded digest-only output evidence, and lifecycle termination through
a Windows Job Object or Unix inherited process group. It also uses temporary HOME/cache, disabled
Git credential prompting/global config, Go proxy/sumdb off, and npm offline mode. These
reduce accidental egress and credential inheritance but are not an OS filesystem or
packet sandbox. Untrusted repositories requiring containment must use only non-executing
checks until a separately reviewed sandbox validation backend exists. On POSIX, deliberate
daemonization into a new session outside the inherited group remains an acknowledged
host-execution residual rather than a claimed process-tree sandbox.

### 7. Restart, cancellation, and evidence preservation

Schema v118 stores plan/workspace/mailbox/receipt/review/merge intent and immutable
operation fingerprints. Startup reconciliation materializes missing exact worktrees,
recovers verified commit intents, fences expired leases, and resumes prepared/running
merge queues. It never restores a raw token, process, capability, or historic authority,
and performs no materialization, Git mutation, or validation while the bound Run is not
currently running.

Cancellation fences durable generations first. It removes only an exact clean
worktree; committed branches are retained, and dirty or identity-drifted directories
become `orphaned`. Uncertain integration state is likewise preserved and reported.
No cleanup follows untrusted paths or recursively deletes an ambiguous directory.

## Consequences / 结果

Benefits:

- two mutable children can work without sharing a checkout or Git index;
- ownership, generation, receipts, review, and merge order survive restart;
- a stale child cannot write after owner rotation, lease expiry, merge start, or cancel;
- review and merge depend on recomputed Git/test evidence rather than prose;
- a failed child or merge step does not contaminate another child's worktree.

Costs and residuals:

- schema v118 adds a substantial persistent ledger and reconciliation surface;
- raw owner tokens are intentionally unrecoverable; losing one requires rotation;
- no autonomous child model loop is added to the legacy Specialist runtime—the
  delivery contract is the tool/runtime boundary used by a trusted child worker;
- host `go_test`/`npm_test` remains danger-full-access code execution when explicitly
  enabled and cannot claim OS containment;
- integration produces a local branch/head only; publishing remains a separately
  authorized user action;
- semantic conflicts not caught by declared validations still require human review.

## Rejected alternatives / 被拒绝方案

- **Shared source checkout:** rejected because mutable files and index have no stable
  owner or rollback boundary.
- **Give Specialists the root tool set:** rejected because multi-agent throughput must
  not widen total authority.
- **Trust child summaries or exit text:** rejected because neither binds the actual
  commit/diff nor proves a clean worktree.
- **Automatic conflict resolution:** rejected because choosing one side discards
  evidence and can violate ownership or intent.
- **Run tests by default with offline environment variables:** rejected because
  child-authored code still executes with host-visible resources.
- **Persist plaintext owner tokens:** rejected because restart persistence is not an
  authorization source.

Operational details and HTTP routes are documented in
[Deliverable Multi-Agent Batches](../batch-delivery.md).
