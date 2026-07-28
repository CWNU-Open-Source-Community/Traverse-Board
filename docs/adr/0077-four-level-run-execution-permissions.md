# ADR 0077: Four-Level Run Execution Permissions

- Status: Accepted
- Date: 2026-07-27
- Scope: P12-C1, P12-C2, and P12-C3 on schema v88

## Context

ADR 0075 and ADR 0076 separate Code, Debug, and Cyber interaction shapes and
introduce the first closed Windows execution paths. Users also need a familiar
permission selector:

1. a conservative default;
2. exact user approval;
3. Codex-style danger full access;
4. a maximum Debug mode for explicitly trusted environments.

A persisted UI choice must not silently become process authority after restart.
The model, an Agent, a Skill, or repository content must not be able to elevate
the Run. Interaction shape and permission ceiling must remain independent.

## Decision

Schema v88 adds the immutable `run_execution_permission.v1` snapshot and its
idempotent operation record. Every new or migrated Run begins in
`conservative`.

| Mode | Approval | Command ceiling | Filesystem | Network | Persistent terminal |
| --- | --- | --- | --- | --- | --- |
| `conservative` | fixed Go templates | fixed templates | workspace guarded | disabled | no |
| `approval` | exact command | arbitrary one-shot | host | host | no |
| `full_access` | none per command | arbitrary one-shot | host | host | no |
| `debug` | mode confirmation | arbitrary persistent | host | host | yes |

The snapshot is a policy ceiling, not a bearer capability. It always persists:

```text
process_enabled=false
execution_authorized=false
capability_grant=false
```

Every operation calls the Go-owned `executionauth` resolver with both the
current snapshot and process-local startup capabilities. Capability gates are
monotonic:

```text
approval control
  -> danger full access
    -> debug maximum access
```

Debug maximum access also requires the Desktop user-terminal capability. A
process started without the required gate refuses the operation even if SQLite
contains an elevated selection. Restart therefore removes authority rather
than restoring it.

Only an operator may change the mode while a Run is `created` or `paused`.
Elevated modes require their own exact confirmation; confirmation flags cannot
substitute for one another. `agent`, `llm`, `model`, `skill`, `repo`, and
`repository` identities are denied. An idempotency key is bound to Run, target
mode, confirmation, operator, and redacted reason.

CLI, local HTTP, Desktop bootstrap, OpenAPI, and React expose the same policy
and current runtime-gate availability. The HTTP mutation is independently
disabled by default and requires the control bearer. React disables modes that
the current process cannot support and uses a second explicit confirmation
step for every elevated selection.

## Current Execution Boundary

This ADR does not pretend that all four executors already exist.

- `conservative` is connected to the existing four Go-owned controlled command
  templates.
- `approval` has durable selection and exact per-operation authorization
  semantics, but no arbitrary host-command proposal/executor yet.
- `full_access` has selection and startup-gate semantics, but no arbitrary
  one-shot host executor yet.
- `debug` has selection and maximum-capability semantics, but no Agent-owned
  persistent terminal product route yet. The existing ConPTY remains
  user-owned and Agent input still needs a separate short lease.

Future adapters must consume `executionauth` immediately before start and must
record a write-ahead intent plus immutable receipt. They may not infer authority
from the mode string alone.

## Consequences

- The default remains useful and conservative.
- A UI choice cannot survive restart as executable authority.
- Approval, full access, and Debug have explicit, testable contracts before
  dangerous transports are introduced.
- Permission policy is orthogonal to `preview|controlled|debug|cyber`
  interaction intent.
- Full access is intentionally dangerous. It is not described as a sandbox.
- Debug is the largest permission tier, but is unavailable unless every lower
  startup gate and the user terminal are explicitly enabled.

## Verification

Focused tests cover initial and migration backfill, immutable snapshots,
idempotent replay, stale/invalid confirmation, model-origin denial, runtime
gate closure, monotonic startup capabilities, the central operation resolver,
CLI selection, HTTP control, OpenAPI projection, Desktop bootstrap flags, and
React availability/confirmation behavior. The three-slice function gate and
any audit findings are recorded in `docs/PROGRESS_BOOK.md`.

## 中文结论

schema v88 增加保守、用户审批、完全访问和调试四档 Run 权限，但数据库只记录
“操作者允许请求到什么上限”，不保存真正执行权。每次操作仍要重新检查当前进程
是否以对应启动开关运行；重启后没有开关就自动失权。

当前只有保守档接入四种固定命令。用户审批、完全访问和调试档已经具备选择、
审计、确认和统一授权判定，但任意宿主命令执行器与 Agent 持久终端仍未接入。
后续执行器必须消费同一 Go 授权判定，并继续使用写前 intent、不可变回执和独立
运行时隔离，不能把一个模式字符串当成通行证。
