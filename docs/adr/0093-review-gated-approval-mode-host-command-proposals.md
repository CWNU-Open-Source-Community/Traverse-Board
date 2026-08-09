# ADR 0093: Review-Gated Approval-Mode Host Command Proposals

- Status: Accepted
- Date: 2026-08-09
- Scope: P13-C1, P13-C2, and P13-C3; schema v96

## Context

Prayu already had two deliberately separate execution paths. Schema v89 lets
the Root Agent request four fixed Go command templates under the conservative
permission level. Schema v90 lets an operator directly execute an exact
non-sandboxed host process under `full_access`. The `approval` permission level
had a domain contract but no durable, model-to-operator product path.

Passing model-authored Shell text to either existing path would erase that
separation. It would also make command parsing, environment inheritance,
wrapper interpreters, retries after crashes, and repository prompt injection
part of the execution authority boundary.

## Decision

Schema v96 adds a third, distinct protocol. Only the active Root Supervisor in
a trusted Code/Local/Controlled Run whose immutable permission snapshot is
`approval` may call `host_command_propose`. The request contains one absolute
executable path, separated argv, a Workspace-contained working directory, a
bounded timeout, and a redacted purpose. Go resolves and hashes the executable,
constructs a sanitized environment, and fixes `network_intent=host`.

The proposal protocol rejects Shells and command wrappers. It also rejects the
common inline-code switches of supported interpreters. It never accepts a
command string, environment values, stdin, background or persistent process
intent, automatic retry, a capability bearer, or reviewer identity selected by
the model.

SQLite immutably binds proposal, digest-only creation operation, independent
review, write-ahead execution intent, result, metadata-only receipt, and exact
Session evidence to the Run, Mission, Session, Workspace, root Agent, tool
invocation, interaction snapshot, execution profile, and permission revision.
Review can occur only after the Run is quiescent. Approval requires a separate
control-token operation, explicit execution confirmation, and the current
process-local operator-approval capability.

Immediately before execution, Go reloads every binding, reopens and rehashes the
executable, reconstructs the sanitized environment, and recomputes the exact
request fingerprint. Execution uses the existing Windows host Job boundary,
is explicitly non-sandboxed, may use host networking, closes stdin, has process,
memory, output, and timeout limits, and cannot persist a terminal. If an intent
exists without a durable result, the outcome is uncertain and automatic retry
is permanently refused.

Output is bounded, control-cleaned, redacted, and returned to the exact Session
as `UNTRUSTED HOST COMMAND RESULT` with `instruction_authorized=false`. Raw
output and environment values are not persisted or exposed through HTTP.

## Product Surface

Authenticated GET endpoints list and inspect proposals. A distinct control
token is required to approve or deny. The Desktop approval center presents the
executable SHA-256, argv as separate values, cwd, environment names and digest,
timeout, host-network intent, and a prominent non-sandboxed warning. Approval
requires an explicit checkbox before the action is enabled.

Both ordinary API and Desktop startup keep this feature disabled by default.
It requires permission control plus the dedicated host-command-proposal flag.
The safe operator-preview bundle enables only this per-command approval path;
it does not enable danger-full-access, Debug maximum, Full CDP, or persistent
Agent terminal input.

## Consequences

- `conservative`, `approval`, and `full_access` remain separate trust models.
- Repository text and model output can propose an exact process but cannot
  approve it, alter the reviewed envelope, or trigger a retry.
- Approval-mode execution still has the current user's host filesystem and
  network authority. The UI and API must never describe it as sandboxed.
- Shell-heavy workflows remain unavailable in this mode; users must choose a
  directly executable program with explicit argv.

## Verification

Store tests cover fresh and v95 upgrade migration, immutable rows, exact
bindings, review decisions, intent/result atomicity, replay, and uncertain
recovery. Application tests cover stale Run/profile/permission/environment and
executable identity, denied or missing operator capability, exactly-once
execution, output redaction, and untrusted Session evidence. HTTP/Desktop/Web
tests cover split authentication, strict request fields, default-closed
capabilities, explicit confirmation, and omission of environment values, Shell
text, and raw output.

## 中文结论

schema v96 为“用户审批”档增加了独立的宿主命令提案链。模型只能提交一个精确
可执行文件和分离参数，不能提交 Shell、环境值、后台任务、持久终端或自动重试。
操作者必须通过独立 control token 查看同一不可变请求并显式确认；Go 在执行前重新
核验可执行文件、环境摘要和全部 Run 绑定。该进程明确是非沙箱并可使用宿主网络，
因此界面固定显示高风险警告。输出只以脱敏、不可信、无指令权限的证据返回 Session。
