# ADR 0121: 持久化有界监控与结构化诊断 / Durable Bounded Monitoring and Structured Diagnostics

Date: 2026-08-20

## Status / 状态

Accepted for schema v120, `scheduled-job.v1`, `doctor-snapshot.v1`, and
`debug-query.v1`.

已接受，用于 schema v120、`scheduled-job.v1`、`doctor-snapshot.v1` 与
`debug-query.v1`。

## Context / 背景

Operators need to check a Run later or at a fixed interval, survive an application
restart, and export enough metadata to diagnose failures. Reusing an open model turn,
an arbitrary cron shell command, or a renderer timer would lose durable ownership,
budgets, fencing, and permission evidence. Persisting raw prompts, terminal input, or
event payloads in a support bundle would also create a second unbounded disclosure path.

操作者需要稍后或周期性检查 Run，并要求应用重启后仍可恢复，还要能导出足够的元数据定位
故障。复用未结束的模型 turn、任意 cron shell 或渲染器定时器都无法提供持久所有权、预算、
fencing 与权限证据；把原始 prompt、终端输入或事件 payload 写入支持包则会形成新的无界泄露
通道。

## Decision / 决策

### 1. A scheduled job is a bounded durable state machine

Schema v120 stores one-shot or elapsed-period schedules, UTC anchor plus IANA display
timezone, next wake, hard deadline, stop-on-target-terminal, maximum rounds/model calls/
elapsed seconds, retry/backoff, notification policy, and owner Run/root. SQLite is the
source of truth. The scheduler has an injectable clock, fixed concurrency one, and an
explicit process-local lifecycle; it is never installed as an OS service and cannot be
enabled through a runtime API.

### 2. Claims use leases, generations, and private fencing material

A claim atomically binds one occurrence, attempt, monotonically increasing generation,
owner digest, fence digest, and expiry. Completion rechecks every binding. Restart
reconciliation moves only an exactly expired claimed round to `retry_wait`; a live lease,
stale generation, duplicate completion, or changed revision fails closed. Raw lease owner
and fence values never enter ordinary HTTP/Desktop projections or events.

### 3. Misfires and restart catch-up are explicit

The schedule is UTC-anchored, so local DST changes cannot duplicate or lose ordinals.
`run_once` catches up one overdue occurrence; `skip` records the missed occurrence and
advances. A one-shot executes at most once. Backoff is bounded and retry exhaustion is
terminal. No recovered record authorizes arbitrary process execution.

### 4. Observation is metadata-only and unchanged state is free

The default executor observes a stable digest of persisted Run/event metadata. It does
not read payload JSON. A self-generated scheduled-job event is excluded from the digest.
When the digest is unchanged, the round records `unchanged` and invokes neither a model
nor a tool. Deadline, cancellation, terminal target, elapsed/round/model budgets, stale
authorization, and exhausted retries are checked at durable boundaries.

### 5. Repair cannot be inferred from monitoring

The default mode is `read_only` with a zero model-call budget. `approved_repair` requires
Code/Deliver, root, an explicitly confirmed current execution permission, and an exact,
unexpired authorization bound to the job, Run, mode snapshot/revision, and permission
snapshot/revision. Every repair sink revalidates that binding and ordinary approval.
Monitoring cannot issue, refresh, or widen it. Production supplies no implicit repair
executor, so unsupported repair fails closed.

### 6. Diagnostics are structured, bounded, and redacted

`doctor-snapshot.v1` reports build/schema, provider/model harness state, optional Run,
workspace identity, Surface/Phase/Profile/permission/network facts, tool eligibility, and
explicit `ready|degraded|not_configured|not_probed` checks. It does not probe sandboxes,
browsers, or plugins merely to fill a field.

`debug-query.v1` requires a Run and accepts an exclusive sequence cursor, at most 100
returned items, at most 500 scanned events, a maximum seven-day window, optional type/
source prefixes, and one exact Run/attempt/tool/process/request correlation. It produces
a monotonic metadata timeline classified as model, tool, policy, application, or
infrastructure. Event payloads, prompts, terminal input, command input, and secrets are
always withheld or redacted. `diagnostic-bundle.v1` composes the same two projections
without adding a raw-data path.

### 7. All control surfaces share the same Application contract

CLI, authenticated loopback HTTP/OpenAPI, React, and Desktop call the same service.
Scheduled mutations require the control bearer and an idempotency key; reads and
diagnostics require only the read bearer. Desktop exposes create/pause/resume/cancel,
next wake, last result, notification history, and bundle export. Runtime capability and
worker health projections state that concurrency is one, service persistence and runtime
enable are false, and authority escalation is false.

## Consequences / 结果

Benefits:

- monitoring survives restart without keeping a model turn alive;
- duplicate execution and stale writers are fenced by durable occurrence/generation facts;
- unchanged targets consume no model/tool work;
- support artifacts have stable cursors and explicit source/redaction semantics;
- CLI, API, Web, and Desktop present one lifecycle and one authority boundary.

Costs and residuals:

- process-local scheduling runs only while an explicitly configured Prayu process is alive;
- notifications are durable in-product records, not email/push delivery;
- wall-clock and OS suspend can delay a wake, resolved by the selected misfire policy;
- structured diagnostics intentionally omit raw evidence, so an operator may still need a
  separately authorized local investigation;
- no general cron shell, service autostart, remote scheduling plane, or indefinite
  unattended agent is introduced.

## Rejected alternatives / 被拒绝方案

- **Renderer timer:** rejected because it is neither durable nor a security boundary.
- **Persist an open agent loop:** rejected because it makes stop conditions and spend
  dependent on model behavior.
- **Arbitrary cron command:** rejected because it bypasses Run Policy, approval, and audit.
- **Persist plaintext lease/fence tokens:** rejected because durable state is not authority.
- **Dump raw logs/prompts into bundles:** rejected because it creates an unbounded secret and
  user-content export surface.
- **Automatically repair after detecting a fault:** rejected because observation cannot imply
  write/process/network authority.

Operational details are in
[Scheduled Jobs and Structured Diagnostics](../scheduled-jobs-diagnostics.md).
