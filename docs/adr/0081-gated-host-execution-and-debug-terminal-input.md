# ADR 0081: Gated Host Execution And Debug Terminal Input

- Status: Accepted
- Date: 2026-07-30
- Scope: P12-E1, P12-E2, and P12-E3 on schema v90

## Context

Schema v89 gives the root Agent a safe way to request four Go-owned
diagnostics. It must not be widened in place into a general Shell protocol:
fixed diagnostic requests and arbitrary host commands have different threat
models, review requirements, and failure consequences.

The four schema-v88 permission ceilings also need distinct implementations:

- `approval` needs an exact, independently reviewed one-shot proposal;
- `full_access` needs an explicit operator-only host executor;
- `debug` needs a short, revocable way to write to an existing user terminal.

Persisting a selected permission mode is not sufficient authority. A model,
Agent, Skill, repository document, or old database row must not be able to
restore elevated process rights after Prayu restarts.

## Decision

### P12-E1: separate approval contract

Prayu adds `host_command.v1`, `host_command_proposal.v1`, and
`host_command_review.v1` as Go domain contracts. They are deliberately
separate from `controlled_command_proposal.v1`.

The command specification freezes:

- an absolute executable path and SHA-256;
- bounded argv without Shell parsing;
- an absolute working directory;
- sanitized environment keys and a digest of process-local values;
- the explicit `network_intent=host`, which means there is no network sandbox;
- a bounded timeout and redacted purpose;
- exact Run, Mission, Session, Workspace, interaction, execution-profile, and
  permission snapshot revisions.

Only the root `run_supervisor` under the `approval` permission ceiling may
construct the non-authorizing proposal. A separate non-model operator review
binds the exact proposal fingerprint and can authorize only one execution.
The review never creates a capability grant.

P12-E1 is a domain and threat-model boundary only. It adds no proposal
persistence, model Tool, HTTP route, Desktop route, or execution service.

### P12-E2: full-access one-shot host execution

Schema v90 adds a separate write-ahead intent, digest-keyed operation, and
immutable metadata-only receipt for an operator CLI command:

```text
cyberagent run host-execute
```

The operation requires all of the following:

- a created or paused Code Run;
- a trusted `controlled` interaction bound to the Local profile;
- a durable `full_access` permission snapshot;
- current-process `permission-control` and `danger-full-access` gates;
- exact danger-full-access and non-sandboxed-host confirmations.

On Windows, Go starts the exact executable through `CreateProcess`, not a
Shell. The executable must be a regular non-reparse PE and match the requested
SHA-256 while an open handle prevents ordinary replacement or deletion. Go
rebuilds a sanitized environment, closes stdin, assigns the process to a Job
Object at creation, limits the tree to 32 processes and 2 GiB aggregate Job
memory, bounds captured and observed output, and terminates and reaps the
whole tree on completion, timeout, cancellation, or output overflow.

Raw stdout/stderr and environment values are transient. Durable records retain
only exact command metadata, environment names/digest, output counts/digests,
exit and boundary facts. An intent without a receipt is an uncertain outcome
and must never be retried automatically.

This executor is intentionally non-sandboxed. It runs as the current Windows
user and can reach that user's host filesystem and host network. Text Policy
still rejects known destructive command text, but it cannot prove that an
arbitrary compiled binary is safe. The CLI is therefore default-off and
operator-only. No HTTP, Desktop, model, Skill, repository, or automatic path
can invoke it.

### P12-E3: Debug terminal Agent input

Debug Agent input is a Go-only controller over an already running,
user-created ConPTY session. It does not create, replace, resize, retarget, or
persist a terminal.

A grant requires:

- an exact trusted Code/Local/Debug Run and current terminal;
- a durable `debug` permission snapshot;
- current-process debug maximum-access and user-terminal gates;
- a non-model operator plus separate debug and Agent-input confirmations;
- a process-local bearer with a TTL from 15 seconds to 15 minutes.

The bearer binds Workspace, Run, terminal, interaction snapshot/revision,
profile revision, permission snapshot/revision, and permission mode. It is
memory-only, is never returned to the renderer, and disappears on restart.
At most 64 controller bindings and 256 operations per binding are retained.

Each write accepts exactly one complete UTF-8 command line, rejects embedded
control/newline ambiguity, and runs permanent Shell Policy before any write.
A metadata-only `granted`, `prepared`, `completed`, or `revoked` Run event may
contain operation/data digests and byte counts, but never the bearer or raw
input. Any partial write or uncertain audit result disables automatic retry.

The controller continuously rechecks the current Run, interaction, profile,
permission, process gates, lease expiry, and terminal identity. Host lock,
disconnect, logoff, suspend/resume, terminal replacement, Run termination,
binding drift, expiry, and shutdown revoke the grant. It exists only inside
the Go Desktop `ControlPlane`; renderer, HTTP, Skills, repository content, and
models have no grant or write route.

## Consequences

- Conservative fixed diagnostics remain isolated from arbitrary command
  contracts and retain their smaller injection surface.
- A persisted permission selection cannot restore elevated runtime authority.
- `full_access` is useful for explicitly trusted operator workflows but is
  not a filesystem or network sandbox.
- Debug input can support a future supervised Agent workflow without handing
  the renderer or model a permanent terminal bearer.
- Approval-mode arbitrary execution remains incomplete until its proposal,
  persistence, product review, and execution adapter receive a separate
  audited slice.
- Docker PTY, Cyber persistent terminal, browser launch, request mutation,
  background-process product controls, and a general Agent Shell remain
  unavailable.

## Verification

Go tests cover strict command construction, unknown or secret-like
environment input, path and digest binding, independent review identity,
single-use authorization, schema-v90 migration, immutable intent/receipt
storage, replay and uncertain-result behavior, permission and interaction
drift, process startup gates, exact Windows launch boundaries, timeout,
cancellation, output limits, Job-tree reap, terminal grant/write/revoke,
short TTLs, Policy denial, operation idempotency, metadata-only audit, host
lifecycle revocation, and cross-platform disabled behavior.

The completed release gate ran the uncached serial repository-wide Go suite
in 576.3 seconds and the full race suite in 717.6 seconds with no races.
Vet, zero-warning staticcheck, govulncheck with zero reachable
vulnerabilities, module verify/tidy, secure Desktop tags, 48-file/165-test
React, strict TypeScript, deterministic OpenAPI, Vite production build, npm
audit, Rust format/7+2 tests/clippy, privacy/authority scans, and diff review
all passed. The reproducible Windows Desktop executable is 42,757,120 bytes
with SHA-256
`801bda9b5343b72999827beeb3bfecd6fdd907b9795736f540637b77a26cb771`;
`release_ready=false` remains correct.

A separate opt-in Windows adapter test launched only the current Go test
binary through its exact path and SHA-256, with a controlled environment and
no Shell. Ordinary and race variants passed. No release test invoked an
arbitrary user program or the product `host-execute` command. The audit found
no unresolved high- or medium-severity issue. Accepted low residuals are that
`full_access` cannot prove a compiled executable's behavior, another process
can change the working directory after validation, and Debug revoke/write
still has a very narrow concurrency window. Explicit high privilege,
write-ahead/metadata audit, and no automatic retry after uncertainty remain
mandatory.

## 中文结论

P12-E 没有把 v89 的四种固定诊断命令偷偷扩成通用 Shell。`approval` 得到的是
独立且尚未接产品入口的任意命令申请合同；`full_access` 得到的是操作者双确认后
才可使用的 Windows 一次性宿主执行器；`debug` 得到的是仅存在 Go 控制平面的
短期终端输入绑定。

完全访问按当前 Windows 用户运行，能访问宿主文件系统和网络，不是沙箱。Debug
令牌不落库、不进 renderer，输入只记录摘要且不确定结果禁止重试。数据库选择、
模型、Skill、README 和仓库内容都不能自行提权。
