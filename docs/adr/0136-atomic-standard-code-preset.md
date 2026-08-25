# ADR 0136: Atomic Standard Code Preset and Pause-and-configure

- Status: Accepted
- Date: 2026-08-25
- Scope: GitHub Issue #135; SQLite schema v133

## Context

A user previously had to coordinate Run lifecycle, Surface, Phase, execution
Profile, execution Interaction, execution Permission, browser-CDP permission,
Workspace Trust, Drydock creation, and process-local backend readiness. Each
individual control was valid, but sequencing them in a client could leave a Run
with only part of the intended Standard Code policy.

The product needs one ordinary “Start coding” operation without turning React,
CLI scripts, a model, a Skill, MCP, or repository configuration into a second
authority owner.

## Decision

`standard_code_preset.v1` is a Go-owned, idempotent application operation. Its
complete configured tuple is:

```text
Surface       code
Phase         plan
Profile       local | docker
Interaction   controlled
Permission    workspace_access
Browser CDP   restricted
Workspace     exact ready trusted Drydock
Network       disabled
Credentials   none
```

Delivery remains a separate explicit Plan-to-Deliver transition. The preset
does not enable Full Access, Debug, Full CDP, a user or Agent terminal, network,
credentials, or a process. Every persisted snapshot and receipt fixes
`capability_grant=false`.

`backend_intent` is closed to `auto|local|docker`. `auto` selects Local only when
the exact Local Command Runtime adapter and its current proof are ready, and
records `auto_local_ready`. It never silently falls back to Docker or a host
runner. Docker is selected only by explicit intent and records
`explicit_docker`; if Local is unavailable, the result may offer explicit Docker
or Approval as next steps. Neither path can select `full_access`.

## Target and lifecycle rules

Without a Run ID, Application prepares a new Code/Plan Run graph and its preset
intent in one SQLite transaction. An existing Code Run may be configured only in
`created|paused` with no active execution lease. An incompatible Surface creates
a new Code/Plan Run and records the original requested Run identity; it never
changes the existing Run's immutable Surface identity.

A running Run first returns the distinct `pause_and_configure` action. That
action persists a durable `waiting_for_pause` intent before it waits. The same
operation key may be retried while a live execution lease or Supervisor work is
active. Once the Run is genuinely quiescent, pause and the complete preset tuple
commit in one transaction. A different request under the same key conflicts.

## Trust, transaction, and recovery

Before a new Drydock is created, Go inspects the exact registered source
Workspace. The first request returns only a trust digest. Confirmation must pin
that exact digest in a new operation because the confirmed request is a different
intent. Source drift, symlink/reparse entries, and submodule gitlinks fail closed.

The preset operation is a non-authorizing write-ahead receipt. Drydock creation
uses the existing recoverable ownership protocol. The final SQLite transaction
relocks the Run, rechecks the absence of an active lease, requires the exact
current ready Drydock generation and Checkpoint, validates every current snapshot
and its exact next revision, appends only necessary snapshots/events, and updates
the receipt to `configured`. Any insert, event, or receipt failure rolls the
whole tuple back; a prepared intent can be retried without reconstructing partial
client state.

Schema v133 widens `controlled` interaction only for the exact Docker profile and
`docker_sandbox_gate`, preserving all existing rows and triggers. The immutable
`standard_code_preset_operations` ledger binds the operation digest, request
fingerprint, Run/Mission/Workspace, backend reason, Drydock, complete snapshot
tuple, and event range. SQL triggers reject a completion that is not the latest
exact Code/Plan/controlled/workspace-access/restricted tuple.

## Surfaces and authority boundary

CLI calls Application directly. Control-token HTTP/OpenAPI exposes three narrow
POST routes, and Windows Desktop uses the same in-process HTTP handler. React
submits one preset request; it does not chain legacy mutation endpoints. The
response contains the actual selection, Local/Docker readiness, stable blockers,
next steps, trust digest when review is required, and `capability_grant=false`.
It contains no token, lease, owner, host path, Docker endpoint, credential, or
process identity.

The preset is not registered as a model tool or extension hook. HTTP fixes the
requester to its authenticated control surface, while the Application rejects
model/Agent/Skill/MCP/plugin/hook/repository-config requester classes. Persisted
model or extension data cannot invoke the operation or widen its tuple.

A Drydock or Git worktree is an ownership and recovery boundary, not a security
sandbox. Local AppContainer/WFP/Job/ACL or the fixed Docker `network=none`
container provides the process, filesystem, credential, and network isolation.
Execution separately revalidates the exact current adapter generation, Drydock,
snapshots, lease, Policy, and budget.

## Consequences

Users have one consistent Start-coding control across CLI, API, Desktop, and
React. Pending trust and pause operations are explicit recoverable states rather
than hidden partial configuration. The additional immutable ledger and migration
increase schema complexity, but make replay, failure injection, and event
reconstruction independently auditable.

## Verification

Tests cover Local creation/configuration and exact replay, changed-intent
conflict, explicit Docker selection with no automatic fallback, incompatible
Surface succession, running/active-lease pause recovery, SQL commit fault
rollback, v132-to-v133 preservation and trigger references, authenticated strict
HTTP routing/OpenAPI, Desktop capability wiring, CLI behavior, strict TypeScript
decoding, trust confirmation, and one-request React dispatch.

## 中文结论

`standard_code_preset.v1` 把 Code/Plan、Local 或显式 Docker、controlled、
`workspace_access`、restricted CDP 与可信 Drydock 作为一个 Go-owned 幂等操作提交；任一提交
失败不会留下半套策略。运行中的 Run 先持久化独立“暂停并配置”意图，等待 lease 与 Supervisor
真正静止后再把暂停和完整策略放进同一事务。自动选择只会选已就绪 Local，绝不静默降级到
Docker、宿主执行或 `full_access`。

CLI、control-token HTTP/OpenAPI、Desktop 与 React 使用同一 Application 合同；响应只给出实际
选择、readiness、阻塞原因和下一步，不携带 authority bearer。Drydock/worktree 只负责归属与
恢复，真正隔离来自 Local OS 后端或固定 `network=none` Docker 容器。
