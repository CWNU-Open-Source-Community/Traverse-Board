# ADR 0132: Stable Thread Identity and Authority-Free Run Succession

- Status: Accepted
- Date: 2026-08-24
- Scope: GitHub Issue #151 and schema v129

## Context

The product historically exposed a Run as the primary task identity. A Run is finite,
however: it completes, fails, or is cancelled, while the user still expects to keep
typing in the same task. Treating a Session as that identity is also incorrect because
Session content and authority are scoped to one execution attempt. Reusing a terminal
Run or its Session would make lifecycle invariants ambiguous and could resurrect an
approval, execution lease, process, credential, or network grant.

The durable model therefore needs one stable user-facing identity above finite Runs,
without rewriting historical data or moving the Run state machine out of Go.

## Decision

The canonical roles are:

| Object | Role | Lifetime and authority |
| --- | --- | --- |
| `Thread` | Stable user-facing task and URL identity | Owns lifecycle, ordered Run succession, and cross-Run history; never carries execution authority |
| `Mission` | Immutable intent, profile, Workspace, and Scope | One canonical Mission is bound to a Thread and reused by its successor Runs |
| `Run` | One finite execution attempt | Owns the Go lifecycle, budgets, events, permission/profile snapshots, leases, and process work |
| `Session` | Run-local conversation and context boundary | Exactly one Session per Run in a Thread; messages remain immutable history, not authority |

`Thread` has the product lifecycle `active -> archived -> active` and
`active|archived -> deleted`. Delete is a soft, irreversible product transition; it
does not remove a Mission, Run, Session, message, or audit row. Archive and delete are
rejected while a Run is preparing or running. Delete additionally requires no live
Run. All lifecycle mutations use a Thread version fence and a durable idempotency-key
ledger. Restore does not recreate authority; it only makes the stable identity and its
existing Sessions visible again.

`thread_runs` is an immutable ordered binding. One Run and one Session can belong to
exactly one Thread. Database triggers prove Mission, Workspace, Run, and Session scope,
forbid a second live Run, keep `active_run_id` and `last_run_id` synchronized, and clear
the active projection when a Run becomes terminal.

## Succession algorithm

Composer input is accepted for every non-terminal Run, including
`waiting_approval`. If the Thread has a live Run, the message is queued on that same
Run and Session. If the last Run is `completed`, `failed`, or `cancelled`, Go performs
the following under an immediate SQLite transaction:

1. lock and re-read the Thread and its expected last Run;
2. return the already-created live Run if another continuation won the race;
3. otherwise create one new Session and one `created` successor Run under the same
   Mission;
4. initialize all Run-owned permission, execution-profile, browser, approval, lease,
   process, credential, and network state from closed defaults;
5. append the immutable predecessor/successor binding and Thread audit event; and
6. queue the operator message against the resolved Run.

The SQLite DSN uses immediate transactions and a busy timeout. Concurrent processes
therefore serialize before reading the successor decision: exactly one creates it and
all others observe the same Run. A process restart simply re-opens the same Thread,
binding, and operation ledgers.

The successor may receive a bounded, fingerprinted continuity snapshot containing
old summaries, messages, and project instruction/config fingerprints. Its authority
object is structurally all-false. Historical content can inform the model but cannot
restore an approval, capability, execution profile, lease, process, network access,
credential, secret, or terminal ownership.

## Storage, upgrade, backup, and export

Schema v129 adds `threads`, immutable `thread_runs`, immutable `thread_events`, and
`thread_lifecycle_operations`. Upgrade is deliberately conservative: every existing
Run becomes one Thread, so migration never guesses that separate historical Runs were
one user task. A pre-Session Run receives a deterministic recovery Session before the
non-null Thread binding is created. Existing Mission, Run, Session, message, and audit
rows are not deleted or rewritten into a new history.

The migration is forward-only. Rollback means restoring the exact pre-v129 SQLite
backup; tests prove that backup remains schema v128 and contains no future tables.
Foreign-key and orphan checks run after upgrade and after Thread lifecycle operations.

Thread export is lossless rather than UI-page-bounded. It includes the Thread,
Mission, complete ordered Run bindings, every Run and Run-local Session, every compacted
and uncompacted Session message with provenance, every Thread lifecycle event, and every
durable Run audit event.

## Product surfaces and compatibility

The Go HTTP/OpenAPI projection under `/api/v1/threads` is canonical. The CLI exposes
`thread create|list|show|send|history|archive|restore|delete|export`. Desktop bootstrap
and runtime capabilities expose `thread_control_enabled`, derived exactly from Run
creation plus Session message control. React stores `selectedThreadID`, lists Threads
before diagnostic Run/Session history, creates a Thread during onboarding, keeps the
composer visible after a terminal Run, and uses `/threads/{thread_id}` as the durable
navigation identity. `/runs/{run_id}` and `/sessions/{session_id}` remain supported as
compatibility and diagnostic views; they are not alternate task identities.

## Verification

The contract suite covers in-place v128 upgrade, a legacy Run without a Session,
backup restoration, foreign keys, lifecycle replay, exports above 1,000 messages, and
Run audit inclusion. Application tests cover completed, failed, cancelled,
waiting-approval, concurrent two-process continuation, process restart, fresh Session,
all-false continuity authority, conservative permission, preview execution profile,
restricted browser permission, and absent execution lease. HTTP, OpenAPI, CLI,
Desktop bootstrap, React store, URL restoration, and terminal/waiting-approval composer
tests exercise the same projection.

## Non-goals

This decision does not change the streaming protocol, move lifecycle or audit
authority into TypeScript, infer historical Run grouping, reuse a terminal Run,
inherit authority, or destructively rewrite existing data.

## 中文结论

`Thread` 是稳定、面向用户的任务身份；`Mission` 是不可变意图与 Scope；`Run` 是一次有限执行；
`Session` 是该 Run 独占的消息和上下文边界。终态 Run 后输入框仍然可用，但 Go 会在同一 Thread 内
事务化创建一个全新 Run/Session；并发和进程重启都只会得到同一个后继 Run。连续性只继承有界历史
数据，审批、租约、进程、网络、凭证和执行权限全部从关闭状态重新开始。归档、恢复、软删除和完整导出
不会拆散 Run、消息或审计历史。
