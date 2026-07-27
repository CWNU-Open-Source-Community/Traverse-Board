# ADR 0075: Execution Interaction Trust Model

- Status: Accepted
- Date: 2026-07-26
- Scope: P12-A1, P12-A2, and P12-A3 on schema v86

## Context

Prayu needs both useful code execution and long-lived debugging or cyber
terminals. Treating all three as one permission level would make native Windows
PowerShell the weakest boundary. Codex, Claude Code, and Strix use materially
different execution models:

| Product | Agent commands | User terminal | Persistence | Primary boundary |
| --- | --- | --- | --- | --- |
| Codex | Real commands in an OS sandbox by default | Integrated terminal per task/project | Agent commands do not depend on a persistent shell | OS sandbox, permission profiles, approvals |
| Claude Code | A separate Bash/PowerShell process per command | Direct `! command` path | cwd may persist; process environment does not | Tool rules and optional Bash sandbox |
| Strix | Agent-controlled persistent Bash | TUI exposes terminal activity | cwd, environment, background jobs, and sessions persist | Kali Docker container |

Official references used for this decision:

- Codex permissions, sandboxing, approvals, and integrated terminal:
  <https://learn.chatgpt.com/docs/permissions>,
  <https://learn.chatgpt.com/docs/sandboxing>,
  <https://learn.chatgpt.com/docs/agent-approvals-security>,
  <https://learn.chatgpt.com/docs/integrated-terminal>
- Claude Code tools, interactive mode, permissions, and sandboxing:
  <https://code.claude.com/docs/en/tools-reference>,
  <https://code.claude.com/docs/en/interactive-mode>,
  <https://code.claude.com/docs/en/permissions>,
  <https://code.claude.com/docs/en/sandboxing>
- Strix terminal, sandbox, and CLI:
  <https://docs.strix.ai/tools/terminal>,
  <https://docs.strix.ai/tools/sandbox>,
  <https://docs.strix.ai/usage/cli>

## Decision

Prayu uses different trust models for different work surfaces:

```text
Code:  Codex-style workspace sandbox + Claude-style stateless commands
Debug: user terminal first + separately authorized, time-limited Agent input
Cyber: Strix-style persistent terminal only inside an isolated container
```

Native Windows LocalRunner never gives a model unrestricted PowerShell by
default.

### P12-A1: Durable interaction intent

Schema v86 adds append-only `run_execution_interaction.v1` snapshots:

- `preview`: untrusted, no command form, no process;
- `controlled`: trusted Code surface, Local profile, structured argv;
- `debug`: trusted Code surface, user ConPTY requested, Agent input off;
- `cyber`: trusted Cyber surface, Docker terminal requested, Agent input off.

Only an operator-facing service can select a mode. Controlled, Debug, and Cyber
require explicit boundary confirmations. Models, Agents, Skills, and repository
content are rejected as requesters. Selection is allowed only for a created or
quiescent paused Run, binds the exact latest Run mode and execution-profile
revision, and is blocked by an active execution lease.

Every durable row and event fixes:

```text
agent_input_default=false
network_scope=disabled
process_enabled=false
execution_authorized=false
capability_grant=false
```

Migration backfills every pre-v86 Run as `preview/untrusted` and fabricates no
authority or transition event.

### P12-A2: Process-lifetime Agent-input lease

`TerminalInputBroker` can issue one random bearer only for exact Debug or Cyber
scope:

```text
Workspace + Run + TerminalSession + interaction snapshot ID/revision + mode
```

The bearer is never persisted. The Broker stores only its SHA-256 lookup key,
uses a 15-second to 15-minute TTL, and rejects cross-scope or stale-interaction
use. Active leases and revoked-token summaries are independently capped at 256,
so revocation immediately releases active capacity without allowing unbounded
tombstones. Run cancellation, application lock/sleep/exit, explicit revoke,
and process restart must revoke the grant. Any future independent Workspace
switch path must call the Broker's Workspace-scope revocation before changing
UI ownership. Controlled mode can never receive this persistent-input
capability.

### P12-A3: Closed one-shot command plan

`controlled_command_plan.v1` accepts only four Go-owned command kinds:

- `git-status`
- `git-diff-check`
- `go-version`
- `powershell-workspace-list`

There is no arbitrary executable, raw command, shell concatenation, inherited
environment, stdin, PowerShell Profile, persistent process, or requested
network. The PowerShell form uses one fixed Go-owned `Get-ChildItem
-LiteralPath` template. Go transports the normalized Workspace-relative path
as canonical UTF-8 hex data, and the fixed script validates and decodes it
before constructing the literal path; caller text is never evaluated as a
PowerShell expression. CLI output does not expose the registered host root or
script body.

This batch deliberately fixes `start_blocked=true` and
`product_execution_enabled=false`. It is an authorization and adapter-preflight
batch, not a product process starter.

## Consequences

- A model cannot turn a user terminal into an Agent terminal by changing JSON,
  prompts, Skills, or project configuration.
- Debug can exist as a useful user terminal before Agent typing is enabled.
- Cyber persistence remains unavailable until the Docker process lifecycle is
  independently production-authorized.
- The next product Runner must revalidate the exact v86 snapshot, current
  profile revision, process-lifetime lease where applicable, Policy, budget,
  and Workspace identity immediately before start.
- Windows Job Object tree ownership, restricted OS identity, network
  enforcement, bounded redacted output, and restart/orphan recovery remain
  independent blockers. PowerShell flags alone are not an OS sandbox.

## Verification

Focused tests cover mode/profile/surface compatibility, explicit confirmation,
operation replay, immutable SQL, v85-to-v86 migration, model/Skill requester
rejection, exact terminal-lease scope, expiry/revoke/restart behavior, closed
argv, fixed PowerShell template, traversal rejection, stale profile rejection,
and path-free CLI output. The integrated three-slice gate is recorded in
`docs/TASK_BOOK.md` and `docs/PROJECT_STATUS.md`.

## 中文结论

Prayu 不使用一个“完全访问”开关覆盖所有工作。Code 模式采用工作区受控的
无状态命令；Debug 模式首先是用户终端，Agent 输入必须另行获得短期授权；
Cyber 模式只有在隔离容器内才允许持久终端。原生 Windows 不会默认把完整
PowerShell 交给模型。

schema v86 只持久化操作者意图，不持久化令牌，也不授予进程权限。短期
Agent 输入令牌只存在当前 Go 进程并精确绑定 Workspace、Run、终端会话、
执行交互快照 ID/修订号和模式；活动租约与撤销摘要分别限制为 256 项。
受控 PowerShell 目前只是固定模板的非启动计划，不接受模型提供的任意脚本
文本。真实 Windows Runner、ConPTY 和 Docker PTY 仍需后续独立门禁。
