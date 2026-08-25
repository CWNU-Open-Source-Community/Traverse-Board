# ADR 0138: Hash-Bound Standard Code Packaged E2E Foundation

- Status: Accepted
- Date: 2026-08-25
- Scope: GitHub Issue #140 foundation; no schema change

## Context

Standard Code already has atomic preset, Windows Local Sandbox, fixed Docker
`network=none`, Drydock, command-runtime, approval, and delivery contracts. Their
unit and integration tests do not prove that the exact downloadable Windows ZIP
can start, recover, and retain those boundaries across representative Go,
Node.js, Python, and Rust repositories.

A release test built from mutable sample repositories or host-specific paths would
not be replayable. A conventional test suite that marks unavailable security cases
as skipped could also present incomplete evidence as a passing release gate. The
foundation therefore needs stable inputs and honest evidence semantics before the
full host/backend attack execution is added.

## Decision

The repository owns two embedded, strictly decoded contracts:

- `standard_code_fixture_manifest.v1` binds every fixture byte, repair patch,
  command, file role, line-ending class, deterministic commit identity, HEAD, and
  tree for four dependency-free repositories.
- `standard_code_attack_matrix.v1` binds 40 required cases across filesystem,
  credential, network, process, prompt, authority replay, approval fallback,
  output, and recovery categories. Every case names its backend, stimulus, expected outcome, stable
  signal, and required evidence.

The Go materializer creates only a previously absent directory below a regular
parent, uses Git with global/system configuration disabled, and checks exact clean
HEAD/tree identity. Its optional oracle observes each baseline failure, applies a
hash-bound patch to a disposable local clone, and requires the retry to pass under
offline dependency settings and bounded output/time.

The Windows packaged harness consumes the release manifest rather than choosing an
EXE by glob. It verifies the ZIP and extracted EXE hashes, uses an isolated
`CYBERAGENT_HOME`, launches both conservative default mode and safe
`--operator-preview`, checks store startup, no host TCP listener, kill/reopen,
an attempted hidden-window close with exact force-cleanup fallback, fixture
immutability, synthetic-secret non-persistence, and owned candidate cleanup. It
never operates on a pre-existing user process or directory. Graceful visible-window
shutdown remains a required full-matrix case rather than a bootstrap claim.

The path-free report separates two states:

- `bootstrap_status=pass` means only the deterministic fixtures and exact packaged
  startup/recovery checks succeeded.
- `release_gate_status=needs_full_matrix` remains mandatory until all 40 cases have
  real immutable evidence. An unexecuted case is never pass, skip, or a waiver.

## Authority and safety boundary

Repository text is explicitly untrusted test data and grants no tool, adapter,
approval, or receipt authority. The materializer is not a sandbox and only runs
the checked-in, hash-bound fixture source. The full attack matrix must run through
the packaged application's public Go-owned call path against the real Local and
Docker backends; direct internal calls cannot provide release evidence.

Credential checks use random synthetic sentinels. Evidence contains hashes and
stable identities, not sentinel values, absolute paths, raw command output, user
HOME data, or ambient credentials. Cleanup is limited to exact processes started
by the harness and an exact random directory beneath repository `.tmp`.

## Consequences

Fixture edits now require deliberate manifest digest and Git identity updates.
The release workflow is slower because it performs four real baseline/repair
toolchain runs plus three packaged Desktop lifecycles. In return, failures identify
whether drift belongs to the fixed oracle, packaging/provenance, startup/recovery,
or the later security matrix.

This ADR does not declare Issue #140 complete. Local AppContainer/WFP/Job/ACL,
Docker `network=none`, approval fallback, concurrent-edit recovery, UI denial, and
immutable event evidence still require full packaged execution. Formal signing and
Microsoft Store distribution remain out of scope.

## Verification

Go tests strictly validate both embedded contracts, asset digests, repository
identity, unsafe path rejection, unknown JSON fields, deterministic materialization,
clean worktrees, and report semantics. The release workflow builds twice, verifies
the portable ZIP, runs the packaged bootstrap on Windows, and uploads its JSON
alongside the exact candidate. Documentation defines the remaining full-matrix gate
so CI cannot reinterpret `needs_full_matrix` as a release pass.

## 中文结论

四个固定仓库和 40 项攻击矩阵都由摘要、Git identity 与闭合 JSON 合同绑定；每个
仓库必须真实复现“初始失败、固定补丁后通过”。Windows CI 只对确切 portable ZIP
执行默认启动、operator-preview 强杀重开、无监听、secret sentinel 不落盘、固件不变
和 owned process 清理。

该结果只是 packaged bootstrap。所有 Local/Docker/Approval/恢复攻击尚未产生不可变
证据时，发布状态必须保持 `needs_full_matrix`，不得用 pass、skip 或 waiver 代替。
