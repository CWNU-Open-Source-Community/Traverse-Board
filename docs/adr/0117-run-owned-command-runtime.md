# ADR 0117: 普通模式 Run-owned 命令运行时 / Ordinary-mode Run-owned Command Runtime

Date: 2026-08-19

## Status / 状态

Accepted for schema v116 and the `command-runtime.v2` root-Supervisor tool.

已接受，用于 schema v116 与 root Supervisor 的 `command-runtime.v2` 工具。

## Context / 背景

ADR 0107 introduced a workspace-scoped native one-shot runner. ADR 0114 then
added reviewed PowerShell/Git Bash and a user-owned Debug terminal. Those paths
deliberately separate fixed or reviewed one-shot execution from maximum-access
interactive debugging, but ordinary Code/Deliver still cannot run a bounded
batch, continue a background process in a later model turn, stream stdout and
stderr by cursor, or close a controlled stdin pipe.

ADR 0107 建立了工作区范围的原生 one-shot；ADR 0114 又增加逐条审阅的
PowerShell/Git Bash 与用户所有的 Debug terminal。两者把固定/审阅的一次性执行与
最高权限交互调试严格分开，但普通 Code/Deliver 仍缺少有界批次、跨模型 turn 的后台
进程、按 cursor 续读 stdout/stderr 和可关闭 stdin 管道。

Persisting a PID is not process ownership. A Supervisor execution lease is also
too short-lived: it fences one turn and is released before a useful background
Job completes. Restoring either value as authority would permit duplicate launch,
PID-reuse termination, or stale input after a phase/root/permission change.

持久化 PID 不等于拥有进程；Supervisor execution lease 只围住一个 turn，也短于后台
Job 生命周期。若把任一记录当成重启后的 authority，会产生重复启动、误杀复用 PID，
或在 phase/root/permission 漂移后恢复过期输入权。

## Decision / 决策

### 1. One strict protocol, three fixed profiles / 单一严格协议、三种固定 profile

`command-runtime.v2` is a tagged command contract. Every launch explicitly
declares one of:

- `powershell`: a bounded script under a trusted resolver and fixed
  `-NoLogo -NoProfile -NonInteractive -Command` argv;
- `bash`: a bounded script under a trusted resolver and fixed
  `--noprofile --norc -c` argv;
- `process`: an absolute, non-workspace, regular native executable plus literal
  argv. Shells and script interpreters are rejected in this branch.

Every command also declares a Workspace-relative cwd, an explicit restricted env
array, `closed|pipe` stdin, initial/close policy, timeout, inline/artifact limits,
`network=disabled`, `credentials=none`, and a bounded purpose. The runtime resolves
symlinks, canonicalizes argv and env, hashes the executable/environment/Workspace
root, and rechecks the executable image, cwd, and canonical root immediately before
process creation.

每条命令还必须显式声明工作区相对 cwd、受限 env、`closed|pipe` stdin、初始输入与关闭
策略、timeout、内联/Artifact 上限、`network=disabled`、`credentials=none` 和目的。
运行时解析符号链接，规范化 argv/env，计算 executable/environment/Workspace root
摘要，并在创建进程前再次核对原生 executable、cwd 与规范 root。

### 2. Authority matrix / 授权矩阵

| Surface | Owner | Required authority | Lifetime | Model access |
|---|---|---|---|---|
| `command_runtime` | Run / Go manager | Code + Local + Deliver + root + durable `full_access` + current Supervisor lease + process-local danger-full-access gate | one-shot or Run-owned Job | yes |
| approval one-shot | operator review | Code + Local + Controlled + `approval` + exact review | one process | proposal/result only |
| Debug terminal | user | Code + Local + Deliver + `debug` + terminal + revocable input grant | user session | only during grant |
| Docker Sandbox | Go container lifecycle | Docker profile + separate admission/capability | container plan/lifecycle | proposal only |

The tool is advertised only to the root Agent in Code/Deliver when the current
permission is `full_access` and a runtime adapter was installed at process start.
Every call rechecks current Run, Mission, Session, Workspace, root Agent, mode,
profile, permission, live Supervisor generation lease, startup capability, and
ordinary Policy. Plan, Cyber, Specialist, stale binding, approval-required policy,
or missing capability fails closed. A model, Skill, repository file, or persisted
permission snapshot cannot enable the process capability.

该工具只在 Code/Deliver、root、当前 `full_access` 且进程已安装 runtime adapter 时进入
Tool schema。每次调用重新检查 Run/Mission/Session/Workspace/root、mode、profile、
permission、当前 Supervisor generation lease、进程启动 capability 与普通 Policy。
Plan、Cyber、Specialist、过期绑定、需另行审批的策略或缺失 capability 全部失败关闭。

### 3. Ordered batches and Job lifecycle / 有序批次与 Job 生命周期

`run` accepts one to four structured commands and executes them sequentially with
an explicit `fail_fast|continue` policy; it never concatenates shell strings.
Foreground aggregate timeout is 25 seconds. `start` creates one background Job.
`list`, `read`, `wait`, `write_stdin`, `cancel`, and `kill` operate only inside the
same Run. Reads use a global monotonic byte cursor across stdout/stderr; a frame
retains stream, timestamp, text range, terminal state, exit code, and truncation
reason. stdin writes are bounded, secret-rejected, serialized, and operation-key
idempotent for the live owner.

`run` 一次接受 1–4 条结构化命令，按 `fail_fast|continue` 明确顺序执行，不拼接 Shell
字符串；前台总 timeout 为 25 秒。`start` 创建一个后台 Job，其他 action 只能操作同一
Run 的 Job。stdout/stderr 共用全局单调字节 cursor，每个 frame 保留通道、时间、区间、
终态、退出码与截断原因。stdin 有界、拒绝 secret-like 内容、串行写入，并在活动 owner
内按 operation key 幂等。

### 4. Two leases, no durable process authority / 两层租约、不恢复进程 authority

The current Supervisor execution lease fences the write-ahead launch intent and
prevents replay under a stale turn generation. Once launched, a separate random
process owner plus positive owner generation and expiring heartbeat owns the live
handles. This owner lease survives release of the turn lease, so a later turn in
the same host process can continue the Job. Desktop/API keep that process alive
across client disconnects; an opt-in CLI invocation owns Jobs only until it exits.
Another process may observe the durable audit row but cannot adopt, read live
state, send stdin, or signal the process while that owner heartbeat remains active.

当前 Supervisor execution lease 只负责启动意图的 write-ahead fencing，阻止旧 turn
generation 重放。启动后由独立随机 process owner、正数 owner generation 与可过期心跳
持有真实 handle。它可跨 turn lease 释放继续存在，因此同一宿主进程的下一 turn 可继续
Job；Desktop/API 可跨客户端断线保活，显式启用的 CLI 则只拥有到本次调用退出。另一进程
只能看到审计行，不能据此收养、读取 live state、写 stdin 或发送信号。

On Windows, the process is created suspended and assigned in the same
`CreateProcess` call to a kill-on-close Job Object with an inherited-handle
allowlist. On POSIX, it runs in an owned process group; Linux adds parent-death
signal protection and every supported POSIX host starts a fixed `/bin/sh`
guardian whose inherited pipe closes on parent crash and kills the group. Normal
completion, timeout, cancel, kill, root/mode/profile/permission drift, owner
heartbeat failure, and application shutdown all converge to cleanup of the owned
Job/process group. A POSIX command that deliberately creates a new session can
escape process-group cleanup; this is an explicit unsandboxed `full_access`
residual risk, not a safely adoptable durable Job.

Restart never re-executes an intent and never signals a persisted PID/process-group
identifier, because it may have been reused. While an owner heartbeat is live,
reconciliation waits. After expiry it records `interrupted`; OS ownership already
performed crash cleanup. Prepared/running/stopping and all terminal transitions are
versioned and constrained in SQLite.

Windows 在同一次 `CreateProcess` 中把 suspended process 分配给 kill-on-close Job
Object，并限制继承 handle；POSIX 使用 owned process group、固定 guardian，Linux
再增加 parent-death signal。正常完成、timeout、cancel、kill、授权漂移、owner 心跳失败
和应用关闭都会清理 owned Job/process group。POSIX 命令若主动创建新 session，则可能
脱离 process group；这是非沙箱 `full_access` 的显式残余风险，不会被持久记录安全收养。

重启绝不重放 intent，也不按持久 PID/process group 发信号，因为编号可能复用。owner
心跳有效时 reconciliation 等待；过期后只记录 `interrupted`，崩溃清理由 OS ownership
完成。SQLite 同时约束 prepared/running/stopping 与全部终态的版本化转换。

### 5. Output, Artifact, and untrusted data / 输出、Artifact 与不可信数据

Collectors are stateful across byte chunks. They repair UTF-8 and remove ANSI
CSI/OSC/DCS/SOS/PM/APC, unsafe C0/C1 controls, Unicode format controls, and secret-like
material before output enters the cursor ring or durable terminal record. The
gateway sanitizes the projection again before it reaches the model.

The inline ring is bounded by bytes and frame count. Cursor eviction is explicit
through `base_cursor`/`dropped`; it is never presented as complete output. Terminal
stdout/stderr are retained only to the declared artifact cap (maximum 4 MiB per
stream), with observed-byte counts, SHA-256, and `inline_window|artifact_limit`
reason. The Gateway commits terminal output through the existing Run Artifact
path and returns only IDs/hashes in metadata.

收集器跨 byte chunk 保持状态，在内容进入 cursor ring 或持久终态前修复 UTF-8，并移除
ANSI、非换行 C0/C1、Unicode format control 与 secret-like 内容；Gateway 返回模型前再清洗
一次。cursor 驱逐通过 `base_cursor`/`dropped` 明示，不能伪装成完整输出。终态输出按
声明上限保存（每个 stream 最大 4 MiB），同时记录 observed bytes、SHA-256 和截断原因，
再通过既有 Run Artifact 路径提交，模型元数据只得到 Artifact ID/hash。

### 6. Network and credential boundary / 网络与凭证边界

The ordinary runtime has no credential-bearing mode. It clears or fixes HOME,
USERPROFILE, SSH_AUTH_SOCK, askpass, Git config/helper variables, shell startup
variables, hooks, prompts, and common proxy paths. User env names/values are bounded,
secret-screened, canonicalized, and persisted as reviewed intent. Explicit network
executables and markers are denied before launch, and commands that ordinary Policy
classifies as requiring approval are sent to the separate reviewed path.
Go, Cargo, npm, pip, and uv receive immutable offline defaults. Git is limited to
file transport with credential helpers, hooks, fsmonitor, external diff, editor,
pager, and interactive prompts disabled.

Native host commands do not have a portable, provable OS network sandbox. Therefore
`network=disabled` is a fail-closed declaration and policy boundary, not a claim of
packet-level containment. `full_access` remains unsandboxed host execution; a command
that needs network or credentials is outside this tool and requires a separate exact
per-call review. The runtime does not replace the host OS user token or prove that a
process cannot read credential files. Docker `network none` remains the containment
choice when isolation evidence, rather than host intent filtering, is required.

普通 runtime 没有携带凭证的模式，并清空/固定 HOME、USERPROFILE、SSH_AUTH_SOCK、
askpass、Git helper/config、Shell startup、hook、prompt 与常见 proxy 入口；Git 固定为
file-only transport，Go/Cargo/npm/pip/uv 使用不可覆盖的 offline 默认值。宿主原生命令
没有跨平台、可证明的 OS 网络沙箱，因此 `network=disabled` 是失败关闭的声明/策略边界，
不是 packet-level containment 证明；需要网络或凭证时必须走独立的逐次精确审阅路径。
运行时也不更换宿主 OS 用户令牌，不能证明进程无法读取磁盘上的凭证文件。

## Alternatives considered / 备选方案

- Reuse the Debug terminal: rejected because it would turn a user-owned maximum-
  access session and temporary input grant into ordinary Deliver authority.
- Keep the Supervisor turn lease for the full Job: rejected because a long process
  would block later workers and would still not prove process-handle ownership.
- Adopt a persisted PID after restart: rejected because PID reuse and lost handles
  make safe adoption impossible without a separate OS broker.
- Concatenate a command batch: rejected because quoting and failure semantics become
  interpreter-dependent and unauditable.
- Claim network isolation from token scanning: rejected; the documented residual
  boundary remains explicit.

## Consequences / 后果

- Ordinary Code/Deliver can use real shells and native processes without entering
  Debug maximum access.
- Background work is resumable across model turns and renderer disconnects while
  the owning application remains alive, but is intentionally interrupted across an
  application crash/restart.
- Schema v116 stores immutable launch material, owner-heartbeat state, bounded
  sanitized terminal evidence, and `command_runtime` Supervisor calls. It stores no
  process handle, raw pre-sanitized output, or startup capability. Normalized,
  bounded, secret-screened stdin remains in the durable Supervisor call payload for
  exact replay; the Job intent itself retains only its byte count and SHA-256.
- Desktop and HTTP capability projections expose a distinct
  `command_runtime_enabled` flag. It is separate from the user-terminal and Debug
  terminal flags; the Settings surface labels it as Command runtime.
- An OS-brokered safe adoption protocol and packet-level host network isolation are
  future work, not implied by this ADR.

## Verification / 验证

Tests cover strict action/profile shapes, canonical argv and hashes, Workspace cwd
escape, env/secret restrictions, Policy/network denial, Code/Deliver/root exposure,
ordered batches, monotonic cursor eviction, sanitization, artifact capture, stdin
replay, timeout/cancel/kill, owner heartbeat failure, cross-turn lease turnover,
startup reconciliation, root/permission drift, SQLite fencing and immutable
transitions, Supervisor recovery, Desktop/API capability contracts, Windows
PowerShell 5/Git Bash smoke, supported PowerShell 7 smoke when installed, POSIX Bash
smoke, and owned process-tree leak detection.
