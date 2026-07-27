# ADR 0076: Controlled Windows Execution And User Terminal

- Status: Accepted
- Date: 2026-07-27
- Scope: P12-B1, P12-B2, and P12-B3 on schema v87

## Context

ADR 0075 separates three trust models:

```text
Code:  closed stateless commands
Debug: user terminal first, Agent input separately leased
Cyber: persistent terminal only inside an isolated container
```

P12-A recorded intent and compiled closed plans without starting a process. The
next step must make useful Windows execution possible without silently turning
the Local profile, PowerShell, the renderer, or a terminal into a general Agent
Shell.

## Decision

Prayu implements three separate authorities. They cannot substitute for each
other.

### P12-B1: Four operator-only one-shot templates

`cyberagent run command-execute` accepts only:

- `git-status`
- `git-diff-check`
- `go-version`
- `powershell-workspace-list`

The command recompiles and validates the exact P12-A plan from the current
Run, Workspace, mode, profile, and interaction snapshot. It requires a stable
operator operation key and `--confirm-execution`. A model, Agent, Skill,
repository document, executable path, raw command, arbitrary argv, environment,
stdin, pipeline, hook, or persistent process is not accepted.

Schema v87 adds:

- `controlled_command_execution_intent.v1`, committed before process creation;
- `controlled_command_execution.v1`, an immutable metadata-only receipt;
- immutable update/delete guards and prepared/completed Run events.

The plan fingerprint derives a stable request ID. Exact completed replay returns
the stored receipt. A prepared intent without a receipt represents an uncertain
start and fails closed; it is never automatically retried.

The Windows starter:

1. obtains the Windows and standard installation roots through Win32 APIs
   instead of trusting `SystemRoot`, `ProgramFiles`, or `LocalAppData`;
2. accepts only a non-reparse regular PE in the fixed candidate set and keeps
   a read handle open through process creation;
3. verifies the Workspace root is a directory and not a reparse point;
4. uses Go `os.Root` to reject a PowerShell relative directory that resolves
   outside the Workspace;
5. converts the relative path to canonical UTF-8 hex data and lets only the
   fixed script validate and decode it before `LiteralPath`, so parentheses,
   quotes, `$`, and other path characters cannot become PowerShell syntax;
6. creates a restricted low-integrity primary token;
7. assigns the process to a Job Object at creation time while suspended;
8. fixes one active process, 512 MiB process memory, kill-on-close, and
   terminate-on-unhandled-exception;
9. inherits only `NUL` stdin plus bounded stdout/stderr pipes;
10. supplies a minimal environment and disables Git system/global config,
   hooks, fsmonitor, optional locks, paging, prompts, external diff, and text
   conversion;
11. captures at most 64 KiB per stream, observes at most 64 MiB, applies a
    maximum two-minute timeout, terminates on cancellation, and verifies that
    the Job is reaped.

Raw stdout/stderr is written only to the current CLI invocation. SQLite stores
byte counts, captured-prefix SHA-256 values, truncation, exit state, timestamps,
backend name, and enforced-boundary booleans. Go and SQLite both require the
exact capture/observed/truncated relation, lower-case SHA-256 shape, valid time
ordering, and supported output-limit evidence.

This is an OS-restricted process boundary, not a complete Windows AppContainer,
workspace-only filesystem capability, or independent network sandbox.
Low-integrity code may still read resources permitted by the host ACL. The
closed templates therefore request no network and cannot be widened. A custom
Go or Git installation outside the fixed OS-derived candidates may be reported
as unavailable.

### P12-B2: User-owned Debug ConPTY

The Desktop flag `--enable-user-terminal` is default-off. When enabled, Go may
start one terminal only after revalidating:

```text
registered Workspace
+ active non-terminal Run
+ Code surface
+ Local execution profile
+ Debug interaction
+ trusted Workspace confirmation
+ exact profile and interaction revisions
+ explicit user start confirmation
```

The Windows backend starts current-user Windows PowerShell with
`-NoLogo -NoProfile` in a ConPTY. It uses a creation-time Job Object with
kill-on-close, at most 32 processes, and 2 GiB aggregate Job memory. The Manager
allows at most eight process-local sessions and retains at most 4 MiB of rolling
output per session. Reads return at most 64 KiB. Input, output, environment, and
process identity are not persisted or published.

The React surface uses exact `@xterm/xterm` 6 and `@xterm/addon-fit` versions.
The bottom panel and right Terminal view share the same session. TypeScript can
submit only Run ID, session ID, bounded terminal dimensions, and user keystroke
bytes through strict Go DTO validation. It cannot submit a Workspace path,
shell executable, startup arguments, environment, or Agent lease.

### P12-B3: Exact lease consumer and revocation

The internal Agent input bridge accepts only a P12-A2 bearer that still matches:

```text
Workspace + Run + terminal session
+ interaction snapshot ID/revision + Debug/Cyber mode
```

It cannot start, replace, resize, or retarget a terminal. Controlled mode is
ineligible. There is no Desktop, HTTP, CLI, Tool, model, or repository-content
route that issues the bearer.

Revocation is layered:

- terminal close, exit, or replacement revokes the terminal scope;
- Run termination revokes and closes its terminal;
- mode, profile, or interaction revision change closes the stale terminal and
  revokes its leases;
- application shutdown revokes all leases and closes all sessions;
- a hidden native Windows monitor revokes all Agent-input leases on lock,
  disconnect, logoff, suspend, and resume.

The Run-to-Workspace identity is immutable. The Manager also provides an
explicit Workspace-scope revocation method for a future independent Workspace
switch surface, but no such renderer lease/switch route exists in this batch.

The WTS subscription has a readiness handshake. If the terminal-enabled Desktop
cannot install the host-boundary monitor, startup fails closed. Host-boundary
events revoke Agent input but do not destroy the user terminal or the Prayu
window.

## Consequences

- Code mode gains useful, auditable fixed commands without an arbitrary Shell.
- Debug gains a real user terminal without making terminal possession an Agent
  capability.
- An Agent input lease is short, process-local, exact-scoped, and revocable,
  but it is not yet user-issuable from the product.
- The general `LocalRunner`, Docker PTY, Cyber terminal, browser, and analyzer
  product adapters remain separate gates.
- Future model use should first create a non-executing closed-command proposal,
  require an independent operator review, and return output as untrusted
  provenance-marked evidence. It must not reuse the user terminal.

## Verification

Tests cover exact-template validation, model/operator boundaries, stale durable
bindings, write-ahead/replay/uncertain-start behavior, immutable SQL, v86-to-v87
and historical upgrade chains, output bounds, cancellation, tree reap,
environment redirection rejection, Workspace symlink escape rejection, user
terminal ownership, strict bridge DTOs, xterm lifecycle, binding invalidation,
Run cancellation, lease expiry/revocation, and native host-boundary dispatch.

Opt-in Windows smokes execute the real restricted `go version` template, create
and close a real ConPTY, and exercise the native host-boundary monitor. The
serialized repository-wide Go suite passed in 796.6 seconds. Vet, warning-free
staticcheck, Runner/Terminal race, module verification/tidy, zero-reachable
govulncheck findings, secure Desktop tags, 151 React tests, strict TypeScript,
the production renderer build, zero-vulnerability npm audit, and a reproducible
Windows double build also passed.

The combined audit fixed the path-expression injection and impossible receipt
relationships described above, plus stale renderer start/cleanup paths. It also
found that a transferred pipe read handle was owned both as a raw Win32 handle
and by `os.File`; a later finalizer could close a reused IOCP handle. The caller
now relinquishes the raw handle before collection, and the single owning
collector closes its `os.File` before publishing the result. A dedicated Windows
test pins this ownership rule. Complete destructive Policy test examples are
assembled at runtime to avoid a Defender ML false positive without changing the
fixtures or Policy outcomes. No unresolved high/medium issue is known on an
enabled path. Detailed results remain in `docs/PROGRESS_BOOK.md` and
`docs/TASK_BOOK.md`.

## 中文结论

P12-B 不是“给模型一个 PowerShell”。它实现的是三条互不替代的边界：

1. 操作者只能执行四种 Go 固定的一次性模板；
2. Debug 终端归用户所有，默认只有用户能输入；
3. Agent 输入只能消费精确短租约，而且当前没有产品签发入口。

schema v87 在启动前记录 intent，在完成后只记录不可变元数据回执；原始
stdout/stderr 不落库。Windows 进程使用受限低完整性令牌和创建时 Job Object，
但这不是独立网络沙箱，也不是只能读取工作区的文件系统能力。因此通用
LocalRunner、任意 Shell、Docker PTY、真实浏览器和 Cyber 持久终端继续关闭。
