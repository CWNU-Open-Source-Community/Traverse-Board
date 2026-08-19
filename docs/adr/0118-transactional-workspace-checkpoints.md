# ADR 0118: 事务化 Workspace Checkpoint、Rewind 与 Fork / Transactional Workspace Checkpoints, Rewind, and Fork

Date: 2026-08-19

## Status / 状态

Accepted for schema v117 and `workspace-checkpoint.v1`.

已接受，用于 schema v117 与 `workspace-checkpoint.v1`。

## Context / 背景

FileEdit receipts protect one reviewed file operation, while Git records only
committed or explicitly staged state. A shell batch, build tool, model-callable
Workspace tool, typed Git mutation, or future multi-agent merge can change many
tracked and untracked files and the Git index at once. Run checkpoints and context
compaction do not capture those bytes. Prompt conventions therefore cannot provide
a truthful undo boundary or detect an operator editing the same file concurrently.

FileEdit 收据只保护一次已审阅文件操作，Git 也只记录已提交或显式暂存状态。Shell
批次、构建工具、模型工作区工具、typed Git mutation 或未来的多代理合并可以同时改动多个
tracked/untracked 文件与 index；Run checkpoint 和上下文压缩不保存这些字节。因此仅靠
Prompt 约定既不能形成可信 Undo 边界，也无法发现操作者并发修改。

Recovery is a new write, not a time-travel authority grant. A historical state
must never revive a lease, approval, credential, process, network grant, execution
profile, or capability that was valid when the checkpoint was created.

恢复是一次新的写操作，不是历史权限复活。历史状态不得恢复当时有效的 lease、审批、
凭据、进程、网络授权、execution profile 或进程 capability。

## Decision / 决策

### 1. Immutable protocol and bounded content store / 不可变协议与有界内容仓库

`workspace-checkpoint.v1` seals the exact Run, Mission, Session, Workspace,
attempt, capability generation, trigger receipt, parent checkpoint, canonical
root fingerprint/path hash, base commit, branch, raw Git-index hash/blob,
deterministically sorted worktree manifest, recovery grade, reasons, byte counts,
operator/title attribution, and creation time.

Manifest entries distinguish file/directory/link/special state, tracked/staged,
mode, size, worktree hash, index OID/mode, binary and newline classification,
storage policy, recoverability, and a bounded reason. Content is addressed by
SHA-256 and stored once across checkpoints. SQLite reference counts are updated
only by sealing triggers; referenced content is immutable and cannot be collected.
GC removes only zero-reference blobs.

The fixed ceilings are 20,000 entries per checkpoint, 4 MiB per ordinary file,
32 MiB for the raw index, 64 MiB of referenced blobs per checkpoint, 2,000 preview
changes, 256 conflicts, 2 GiB for the global blob store, 10,000 checkpoints,
2,000,000 manifest entries, and 20,000 transactions. SQLite insert triggers enforce
both content and metadata quotas so concurrent writers cannot bypass them. Ignored, generated,
large, sensitive-looking, linked/reparse, special, unreadable, and out-of-root
content is represented explicitly and is never silently advertised as restorable.
There is no at-rest encryption claim: secret-like files are excluded rather than
written to the blob database.

`workspace-checkpoint.v1` 固定 Run/Mission/Session/Workspace、attempt、capability
generation、触发收据、父节点、规范根目录指纹/路径摘要、base commit、branch、原始 Git
index hash/blob、稳定排序 manifest、可恢复等级、原因、字节计数以及操作者/标题。内容以
SHA-256 寻址并跨检查点去重；SQLite sealing trigger 维护引用计数，GC 只删除零引用 blob。
单文件、index、单检查点和全局 blob 仓库分别受 4 MiB、32 MiB、64 MiB、2 GiB 硬上限；
全局 checkpoint、manifest entry 和 transaction 另受 10,000、2,000,000 和 20,000 条上限。
ignored/generated/large/sensitive/link/reparse/special/unreadable/external 内容均显式标记。
本协议不宣称静态加密；疑似敏感文件不会进入 blob 数据库。

### 2. Transaction boundaries and attribution / 事务边界与归因

Every integrated mutation receives one deterministic operation identity and one
pre/post transaction, not one checkpoint per low-level syscall:

| Source | Boundary | Attribution |
|---|---|---|
| FileEdit apply and `agent-code-tools.v1` apply/delete | before the durable apply; after success, denial, failure, or replay | exact edit/tool receipt, invocation, attempt, capability generation, execution lease |
| `command-runtime.v2` foreground batch | once around the ordered batch | command operation and Supervisor binding |
| background command Job | at Start; completed by terminal read/wait/stdin/cancel/kill/reconciliation | Job operation and current owner/lease binding |
| typed Git mutation | around stage/unstage/commit/branch mutation | Git operation receipt |
| agent merge contract | public `BeginBoundary`/`CompleteBoundary` around the future merge writer | merge receipt |

Only a current running Run, active Session, and exact unexpired execution lease can
open an automatic mutation boundary. One Run cannot have two open Workspace
transactions. A terminal transaction binds its post-checkpoint to the CAS cursor;
historical rows remain immutable.

Shell attribution uses before/after manifests plus Git/index state. No portable
filesystem watcher is currently installed, so command boundaries always carry the
explicit reason `filesystem watcher attribution unavailable`; their grade is
partial even if all observed Workspace bytes are stored. Effects outside the root,
registry/database/service changes, escaped descendants, and network effects are
outside the recovery claim.

### 3. Three-way preview and fail-closed restore / 三方预览与失败关闭恢复

Preview captures a fresh, non-persisted observed manifest and compares:

1. the cursor checkpoint reviewed by the operator;
2. the historical target;
3. the live observed Workspace and raw Git index.

Text, binary, create, modify, rename, delete, untracked files, modes, newlines, and
the index are compared by exact hashes. If live state no longer equals the reviewed
current side for any path the target would touch, the result is a bounded conflict,
not an overwrite. Root identity, root path case, base commit, branch, links/reparse
points, unsupported content, and index drift also stop the operation.

Confirmed Undo, Redo, and Rewind require the exact preview cursor, a paused Code/
Deliver Run, active Session, no live execution lease, a non-conservative current
permission, a matching process-start capability, and an explicit operator. The
operation first writes a preflight checkpoint and prepared transaction. Restore
uses root-confined atomic file replacement/removal and exact raw-index replacement
through Git's standard `index.lock`; the current index presence and digest are
rechecked before rename, including the exact missing-index state;
it never calls `git reset --hard`, overwrites the tree wholesale, or blanket-deletes
untracked files. A final capture must match the target manifest/index/base identity
before the new post-checkpoint and transaction become terminal.

### 4. Undo/Redo, Rewind, and independent Fork / Undo、Redo、Rewind 与独立 Fork

Undo targets the `before` side of the boundary whose `after` checkpoint is the
current cursor. Redo is valid only when the current cursor is the terminal result
of an Undo and targets the original mutation's `after` side. Rewind targets any
materializable checkpoint in the same Run. Each is appended as another immutable
transaction; none rewrites older history.

Fork creates a new branch at the historical base commit through typed literal Git
argv, verifies the exact branch and full commit, restores the target into a new
worktree, then atomically registers a distinct Workspace, Mission, Run, Session,
initial events, and continuity node. The new Run begins in `created`; its permission,
profile authority, approvals, credentials, capabilities, leases, terminals,
processes, and network grants are not inherited. The source cursor does not move.
If registration succeeds but final checkpoint persistence is interrupted, the
prepared fork remains replayable and restart reconciliation finishes the exact
new Run instead of creating a second worktree.
If the process stops after worktree creation but before durable Run registration,
the prepared transaction retains private, non-JSON cleanup identity. Reconciliation
verifies the exact source, destination, branch, and commit before asking Git to
remove the orphan. It also re-captures the complete manifest and raw index, so
post-crash user edits are preserved; any drift fails closed and leaves the
transaction retryable.

### 5. WAL, reconciliation, and events / WAL、重启收敛与事件

Blobs, entries, the seal, and the creation event commit in one SQLite transaction;
an unsealed manifest is never readable. Transaction preparation precedes file
application, and terminal transaction/event persistence is atomic. Operation-key
digests plus request fingerprints make exact retries converge and reject changed
intent. Run-state advancement is compare-and-swap.

At startup, reconciliation enumerates prepared/applying transactions. An unfinished
ordinary boundary whose cursor never reached its `before` checkpoint first advances
that exact expected cursor by CAS, then captures the observed partial result with an
explicit restart reason and closes as `interrupted`; an unexpired execution lease
blocks reconciliation instead of letting a second process close a live writer's
boundary. If a non-Fork transaction became terminal but the final cursor update did
not commit, reconciliation CAS-advances the cursor from the exact `before` checkpoint
to the terminal `after` checkpoint. Fork is excluded because its source cursor must
never move. An identical explicit restore retry can resume only while the cursor,
Workspace identity, current authority, and manifests still agree. A registered Fork with
no final cursor re-captures/verifies the existing independent worktree; missing or
drifted identities fail closed. No persisted PID, lease, or capability is restored.
An interrupted pre-registration Fork is closed only after its exact verified
worktree/branch has been removed. Manual capture also conflicts while any mutation
boundary for the same Run remains open, so it cannot move the cursor underneath an
in-flight writer.

### 6. Equivalent product surfaces / 等价产品入口

- Desktop has a bilingual Checkpoints tab with immutable timeline, provenance,
  recovery grade/reasons, affected paths, conflict explanation, explicit preview
  and confirmation, Undo/Redo/Rewind, manual capture, and Fork name/branch fields.
  React neither submits nor receives an absolute worktree path; Go derives a
  deterministic absent sibling from the trusted source root and operation digest.
- CLI exposes `cyberagent workspace checkpoint timeline|capture|preview|rewind|undo|redo|fork`.
- OpenAPI exposes GET/POST timeline/capture plus `/preview`, `/rewind`, `/undo`,
  `/redo`, and `/fork`. Mutations require the control bearer and explicit
  confirmation; timeline uses the read bearer.

All three call the same Application service. UI/CLI/API confirmation does not
bypass server-side authority or CAS checks.

## Alternatives considered / 备选方案

- `git reset --hard` plus `git clean`: rejected because it loses dirty-index and
  untracked user work and cannot represent non-Git Workspaces.
- Copy the whole Workspace per prompt: rejected as unbounded, duplicate-heavy,
  link-unsafe, and unable to explain exclusions.
- Trust only Git status: rejected because status does not preserve untracked or
  binary content, exact index bytes, modes, or concurrent file identity.
- Treat context checkpoint/Fork as Workspace recovery: rejected because bounded
  conversation state neither contains Workspace bytes nor grants write authority.
- Resume silently on restart: rejected because runtime authorization and external
  state may have changed.

## Consequences / 后果

- Common in-root file mutations become inspectable and reversible without silently
  modifying Git history.
- Checkpoints can be partial or unavailable by design; the product explains why.
- Repeated unchanged checkpoints are cheap through content-addressed deduplication,
  but immutable retained history still consumes manifest rows and referenced blobs.
- Host shell execution remains unsandboxed. The checkpoint is evidence and bounded
  file recovery, not a rollback guarantee for all process side effects.

## Verification / 验证

Tests cover text/binary, create/modify/rename/delete, untracked content, dirty raw
index, CRLF, Windows exact casing, links, sensitive and oversized exclusions,
external edits, cursor races, idempotent replay, Undo/Redo/Rewind, independent Git
worktree Fork, pre-registration orphan cleanup, injected post-registration Fork
persistence failure and restart reconciliation, immutable SQLite rows,
refcounts/GC/migrations, strict HTTP auth and
JSON, CLI round trips, Desktop timeline/preview confirmation, generated OpenAPI,
and command/file/Git boundary integrations. Platform-specific Windows casing tests
skip explicitly on non-Windows hosts; POSIX and Windows command-runtime suites cover
their respective process attribution paths.
