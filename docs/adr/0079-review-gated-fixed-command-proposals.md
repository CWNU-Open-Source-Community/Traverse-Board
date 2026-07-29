# ADR 0079: Review-Gated Fixed Command Proposals

- Status: Accepted
- Date: 2026-07-29
- Scope: P12-D1, P12-D2, and P12-D3 on schema v89

## Context

Schema v87 gave an operator a deliberately narrow way to execute four
Go-owned command templates. Schema v88 then separated the Run permission
ceiling from the execution interaction shape. Neither boundary allowed a model
to request a command.

A general Shell tool is not an acceptable first model-facing execution path.
Repository documents, tool output, and model responses are untrusted evidence,
and a prompt injection must not be able to turn arbitrary text into an
executable, argv, environment, network request, or persistent process. Model
compatibility also varies: a provider may understand a structured intent while
being unreliable with a product-specific patch or Shell protocol.

Prayu therefore needs an intermediate capability that lets the root Agent ask
for useful diagnostics without granting it command authority.

## Decision

Schema v89 adds `controlled_command_proposal.v1`. The root RunSupervisor may
call one Tool:

```text
controlled_command_propose
```

The payload is strict JSON with `additionalProperties=false` and contains only:

- protocol version;
- one of `git-status`, `git-diff-check`, `go-version`, or
  `powershell-workspace-list`;
- a bounded redacted purpose;
- a Workspace-relative path only for `powershell-workspace-list`;
- a timeout from 1 to 120,000 milliseconds.

The payload has no executable, raw Shell, argv, environment, stdin, network,
background-process, persistence, or capability field. Specialist Agents and
read-only Fan-out do not receive this Tool. Creating a proposal never starts a
process and returns only metadata with `operator_review_required=true`,
`execution_authorized=false`, and `capability_grant=false`.

Each proposal is bound to the exact Run, Mission, Session, Workspace, root
Agent, active Tool invocation and execution lease, interaction snapshot and
revision, execution-profile revision, permission snapshot and revision,
precompiled plan fingerprint, and command kind. The Tool operation is
idempotent and its requester is fixed to `run_supervisor`.

Schema v89 persists four immutable ledgers:

1. the non-authorizing proposal;
2. the proposal Tool operation;
3. one independent operator review;
4. one metadata-only result projection.

An operator may approve or deny through CLI, HTTP, or Desktop. The review uses
an operator-owned idempotency key and the exact proposal fingerprint. Models,
Agents, Skills, repositories, and the RunSupervisor cannot be reviewers.
Approval requires a separate exact execution confirmation; denial cannot carry
that confirmation.

Before an approved proposal executes, Go reloads and compares the current Run,
Mission, Workspace, interaction, execution profile, permission snapshot, and
Code/Cyber mode. It then regenerates the fixed command plan and compares its
fingerprint. The current process-local permission gates are evaluated again.
A stored permission selection never grants authority by itself.

Execution reuses the schema-v87 restricted one-shot runner. It records the
write-ahead intent before process start, accepts only the same four fixed
templates, and retains the existing low-integrity token, creation-time Job
Object, process, memory, stdin, environment, output, timeout, cancellation, and
tree-reap limits. A prepared intent without a durable result is not retried
automatically because the process outcome may be ambiguous.

The returned command text is evidence, not instruction:

- invalid UTF-8 and control characters are replaced;
- carriage returns are normalized;
- secret redaction runs before storage;
- content is capped at 16 KiB;
- the message begins with `UNTRUSTED GO COMMAND RESULT`;
- Session provenance uses `go_command_result` and
  `instruction_authorized=false`;
- raw stdout and stderr are not persisted;
- the durable result fixes `automatic_retry_allowed=false`.

Read endpoints expose bounded proposal, review, result, and metadata-only
receipt facts. The one-time response to the approving operator may include the
sanitized untrusted evidence. Desktop review is default-off behind
`--enable-command-proposals`; ordinary `api serve` requires the distinct
control bearer. A Run in an elevated permission mode still requires that
mode's current process startup gates.

## Consequences

- A model can request common diagnostics without gaining a general Shell.
- Prompt-injected repository text cannot add a fifth command or smuggle Shell
  syntax through argv or environment fields because those fields do not exist.
- Proposal, review, execution, and result are separately auditable and exact
  replay converges.
- A stale Run binding, changed permission revision, changed plan fingerprint,
  unavailable runner, or missing runtime gate fails closed.
- Approval is single-use and cannot be converted into a durable capability
  grant.
- The workflow remains less flexible than Codex or Claude Code command tools.
  That is intentional for this conservative stage.
- This does not implement arbitrary approval-mode commands, full-access host
  execution, an Agent-owned Debug terminal, a network sandbox, Docker PTY, or
  the operational built-in browser. Those remain separate future boundaries.

## Verification

Go tests cover strict payload decoding, unknown and missing fields, all four
templates, proposal idempotency, immutable SQLite rows, schema-v89 migration,
stale bindings, reviewer identity, exact confirmation, permission runtime
gates, deny-without-execution, single execution, replay, prepared-intent
failure closure, output sanitization/redaction/truncation, Session provenance,
CLI behavior, HTTP bearer/capability separation, OpenAPI, Desktop bootstrap,
and RunSupervisor Tool-loop compatibility.

React tests cover capability hiding, proposal list rendering, deny, explicit
approval confirmation, idempotent review submission, bounded untrusted
evidence display, and strict response parsing. The generated TypeScript
contract remains derived from the Go OpenAPI document.

## 中文结论

Prayu 的 v89 不是给 Agent 一段 Shell，而是给它一张固定格式的命令申请单。
Agent 只能从四个 Go 预定义动作中选择，不能提交 executable、argv、环境变量、
网络或持久进程参数。提案本身不执行；操作者必须独立审批，Go 在启动前重新核对
当前 Run、Workspace、交互、Profile、权限和计划指纹，再复用 v87 的受限一次性
Runner。

执行结果经过限长、控制字符清洗和脱敏，只以“不可信证据”回到 Session，不能
成为新的模型指令。写前 intent 没有结果时永久禁止自动重试。这个切片完成的是
保守固定命令的 Agent 申请链，不是任意 Shell、完全访问执行器或 Agent 持久终端。
