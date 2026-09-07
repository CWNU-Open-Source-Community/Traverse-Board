# ADR 0127: Workspace Access Permission Contract

- Status: Accepted
- Date: 2026-08-22
- Scope: GitHub Issue #129 and schema v126

## Context

ADR 0077 defines `conservative|approval|full_access|debug`. That model leaves a
gap for ordinary coding: automatic build and test execution either needs
per-command host approval or the unsandboxed `full_access` ceiling. Neither is
an acceptable default for Standard Code.

The missing level must describe what may be requested without claiming that an
OS sandbox already exists. A persisted Run choice is still only policy data;
runtime readiness and one exact execution authority remain process-local and
short lived.

## Decision

Schema v126 adds the protocol value `workspace_access`, displayed in Chinese as
**工作区执行**. It is ordered between `conservative` and `approval` and uses:

```text
mode                 workspace_access
approval_policy      out_of_scope_exact_once
command_scope        sandboxed_workspace
filesystem_scope     workspace_guarded
network_scope        disabled
required_gate        workspace_sandbox_adapter
risk_tier             elevated
operator_confirmed    true
```

The complete ceiling is frozen as follows:

| Capability | `workspace_access` |
| --- | --- |
| Model reads inside the registered Workspace | allowed |
| Reviewed model writes inside the registered Workspace | allowed |
| Sandboxed, bounded command runtime | allowed by policy, adapter still required |
| Unsandboxed host process | denied |
| Network | denied |
| Credentials or inherited secret environment | denied |
| User home outside the Workspace | denied |
| Persistent user terminal | denied |
| Persistent Agent terminal/input | denied |
| Full CDP | denied |
| Any out-of-scope capability | a separate exact, one-time approval chain is required |

The wider levels retain their existing meaning. Their capability projection is
monotonic: `approval` may request exact approved one-shot host operations;
`full_access` dynamically removes per-command approval for its bounded host path;
`debug` is a strict superset at every Full Access host sink and additionally may
request its startup-gated persistent terminal, background, and bounded terminal
input. Full CDP is a user-controllable sub-permission of Full Access and is
inherited by Debug, not a Debug-only eligibility. Independent execution-profile,
browser-method, network, credential, lease, and adapter gates still apply, so a
wider permission string never grants a transport by itself.

## Runtime Readiness and Fail-Closed Behavior

`ExecutionPermissionRuntimeCapabilities.WorkspaceSandboxEnabled` means that a
verified Workspace Sandbox adapter passed readiness in the current process. It
is never persisted. #129 deliberately installs no such adapter and exposes no
CLI or Desktop startup flag that can set it. Therefore current production
bootstrap, runtime-capability API, Run detail, CLI, and React all project the
new level as unavailable.

There is no fallback. The existing Run-owned host Command Runtime continues to
require `full_access` or its strict superset `debug` plus `danger_full_access`;
`workspace_access` cannot
construct it even when a test supplies Workspace sandbox readiness. The central
authorization resolver accepts a `sandboxed_workspace_command` only when both
the selected policy and the independent adapter readiness are present, and its
decision explicitly carries workspace/sandbox facts while host filesystem,
network, background process, and terminal facts remain false.

## Selection, Transition, and Audit

Only an authenticated operator may change the Run while it is `created` or
`paused`. `workspace_access` requires only its exact
`confirm_workspace_access` confirmation; confirmations for approval, full
access, or Debug cannot substitute for it. Transitions are allowed explicitly
from and to every other valid level, and every transition appends a new
immutable revision and a `run.execution_permission_selected` event containing
the complete capability matrix.

Identifiers representing a model, Agent, Skill, repository/configuration,
recovery data, MCP, plugin, or hook are rejected as selectors. Imported or
recovered history can preserve an old selection but cannot create a new
transition.

Every snapshot, including `workspace_access`, persists:

```text
process_enabled=false
execution_authorized=false
capability_grant=false
```

Changing the permission revision atomically releases an active Run execution
lease. Existing Job ownership and tool-call authority bind the prior snapshot
ID/revision/mode and capability generation; their next use or reconciliation
therefore fails as stale. Historical terminal results remain readable where
their existing audit contract permits, but no live authority survives drift.

## Persistence and Compatibility

Migration v126 uses SQLite's create-copy-drop-rename procedure to widen the
snapshot CHECK constraint. Foreign keys are disabled only on the reserved
migration connection; all triggers that reference the snapshot table are
temporarily removed and restored from their canonical migration definitions.
`PRAGMA foreign_key_check` must be empty before commit, and foreign-key
enforcement is restored before the connection is released.

Old snapshot and idempotency-operation rows are copied byte-for-byte. Existing
Runs remain in their historical mode and never acquire `workspace_access`
automatically. Reopening an upgraded database is idempotent, and an old
operation key still replays the same old snapshot.

The additive enum remains under `run_execution_permission.v1` and
`execution_permission_policy.v1`; no meaning of an existing value changed.

## Consequences

- Standard Code now has a stable permission target without prematurely opening
  a host runner.
- UI and protocol clients can explain exactly why the level is grey today.
- #130 can implement Local Sandbox against the frozen resolver and readiness
  gate without changing the permission meaning.
- #129 alone does not make build/test execution available and must not be
  described as an isolation implementation.

## Verification

Tests cover the five mode definitions, exact confirmation and forbidden
selectors, v125-to-v126 migration, historical mode and replay preservation,
upgrade/reopen idempotency, SQLite CHECKs and foreign keys, lease revocation,
tool-authority permission drift, resolver fail-closed behavior, host Command
Runtime refusal, HTTP/OpenAPI/CLI/Desktop/React projections, and false authority
fields. Repository gates include Go race tests, OpenAPI determinism, generated
TypeScript, typecheck, Web tests, and migration history validation.

## 中文结论

`workspace_access · 工作区执行` 是 Standard Code 未来使用的安全中间档：允许模型在已注册
Workspace 内读写，并允许“已经通过 readiness 的沙箱 adapter”执行有界命令；宿主无沙箱进程、
网络、凭证、用户主目录、持久终端和完整 CDP 全部拒绝。越界操作只能走独立、精确、一次性的
审批链。

#129 只冻结并贯通这份授权合同，不实现沙箱。当前产品没有 adapter，所以该档必须显示为
unavailable，且绝不回退到宿主执行。权限 revision 变化会释放旧租约，并通过 snapshot/revision/
generation fencing 立即使旧 Job owner 与工具 authority 失效。
