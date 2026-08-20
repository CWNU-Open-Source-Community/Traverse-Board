# ADR 0122: Go-owned 高级 Git 生命周期 / Go-Owned Advanced Git Lifecycle

Date: 2026-08-20

## Status / 状态

Accepted for schema v123 and `git-advanced.v1`.

已接受，用于 schema v123 与 `git-advanced.v1`。

## Context / 背景

The existing repository services safely cover read-only status/diff/history, whole-file
stage/unstage/commit, local branches, scoped fetch/fast-forward pull/push, and PR creation.
Complex repair still needs selective hunks, stash roles, conflict-aware sequences, bounded
bisect, and isolated worktrees. Exposing raw Git or delegating lifecycle decisions to a model
would bypass exact-state review, Run authority, restart recovery, and deletion boundaries.

现有 Repository 服务已经安全覆盖只读状态、整文件/index 写入和有界远端工作流，但复杂
修复仍缺少 hunk、stash、冲突序列、bisect 与隔离 worktree。开放 raw Git 或让模型自行维护
状态机会绕过精确状态审阅、Run 权限、重启恢复和删除边界。

## Decision / 决策

### 1. Advanced Git is a closed protocol, not a command transport

`git-advanced.v1` enumerates every operation and its strict field set. Commit/stash targets
are exact object IDs, hunk IDs are content addressed, worktree names are a single safe
segment, and all paths are literal pathspecs. There is no argv, shell, executable, host path,
environment, ref-expression, or merge-driver field. Unknown operations and cross-operation
fields fail before review.

### 2. Preview identity includes repository and authority state

The repository executor captures repository/common-dir identity, HEAD/branch, raw index,
worktree/status, stash, sequencer, upstream, and object format. Hunk IDs additionally cover
base/index blobs, full worktree-file content, context, and patch. Application review adds the
permission revision, private lease identity/generation, and process-random capability
generation to the approval fingerprint. Execution re-renders all evidence.

### 3. Every mutation is approval-gated, checkpointed, CAS-owned, and audited

Review stores immutable spec/preview evidence and one pending `git.advanced` Approval.
Execution opens a Workspace Checkpoint boundary and uses `proposed -> running` compare and
swap. Only the winner invokes Git. A schema-level partial unique index permits only one
`running` advanced operation per Git common directory, fencing separate Runs and linked
worktrees as well as duplicate callers. A typed receipt plus Run events and durable sequence
or worktree state closes the boundary. Terminal rows are immutable and idempotent retries
return the same receipt.

### 4. Sequences are explicit durable state machines

Rebase, cherry-pick, and bisect store original reference, targets, live HEAD, sequencer digest,
conflicts, generation, and operation ownership. Continue/skip/abort/mark/reset requires the
exact active sequence and live digest. Startup reconciliation observes but never replays a
`running` command. It persists a still-active exact sequence and terminalizes the old operation
as interrupted/conflicted. When no sequencer remains after a crash, it records the observed
HEAD but terminalizes the sequence as failed instead of guessing that the command succeeded;
this also releases a stale active-sequence fence. A later action always requires a fresh
preview and Approval.

### 5. Bisect automation uses only Go-owned process recipes

Automatic bisect permits fixed Go-test or npm-test templates, bounded steps and per-step
timeout. It uses the existing whole-process-tree runner with closed stdin, stripped offline
environment, bounded output evidence, and mandatory tree-reap proof. Repository test code is
still host code; this control plane does not claim OS network isolation.

### 6. Worktree ownership is derived and non-transferable

Go derives destinations below one process-configured real directory using common-dir identity
and a safe name. Registry rows bind path digest, repository/common-dir, branch, commit, Run,
Workspace, and operation. Names are never reused. Symlink/junction traversal, external paths,
existing branches, dirty or externally advanced targets, locked deletion, force removal, and
unregistered prune candidates fail closed. Restart may conservatively register a provably exact
created target, but the interrupted operation never becomes success.

### 7. Implicit Git execution is disabled

Git receives a replaced environment. System/global config, hooks, credential helpers, prompt,
interactive sequence editor/pager, external diff, fsmonitor, attributes, LFS smudge, and
executable local filter/diff/merge drivers are disabled or rejected. A fixed no-op ordinary
editor lets `rebase --continue` reuse the sequencer's existing message without invoking user
configuration. Force push, reset-hard, clean-force, shared-history rebase, and arbitrary merge
conflict choices are not protocol operations.

### 8. One Application service owns CLI, HTTP, and Desktop

The same service supplies discovery, review, execution, projection, and startup reconciliation.
HTTP separates read and control bearer routes; Desktop only renders strict generated contracts;
CLI `--confirm` is the explicit operator action after a non-authorizing preview. Public DTOs
omit raw persistence JSON, private lease IDs, managed paths, commands, and environment.

## Consequences / 结果

Benefits:

- selective Git repair is reviewable and stale hunks cannot be applied by line number;
- stash conflicts retain the original stash and sequence conflicts survive restart;
- duplicate callers cannot invoke Git twice for one operation key;
- bisect execution has explicit budgets and process ownership;
- managed worktrees cannot escape the product root or silently delete unknown work;
- review/focused-checks receive one full merge-base/hunk/conflict/recovery evidence model.

Costs and residuals:

- strict whole-repository binding invalidates previews after unrelated Workspace drift;
- protected/shared history rewrites require a separate manual workflow and are intentionally
  unavailable here;
- Workspace Checkpoint cannot reconstruct deleted refs, external worktree contents, host
  processes, services, or network effects;
- Go/npm test recipes execute untrusted repository code on the host after explicit approval;
- interrupted operations are not guessed successful and may require manual inspection plus a
  fresh approved action;
- binary/combined hunks, submodule/symlink hunk editing, merge commits in cherry-pick input,
  include-ignored stash, arbitrary bisect commands, and remote writes are out of scope.

## Rejected alternatives / 被拒绝方案

- **Raw `git <argv>` tool:** rejected because schema validation cannot describe its side
  effects, ref/path ambiguity, helper execution, or recovery state.
- **`git add -p`/interactive rebase UI:** rejected because terminal prompts are not durable,
  deterministic approval evidence.
- **Apply hunks by stored line ranges:** rejected because line numbers drift independently of
  content and context.
- **Use `stash@{n}` as identity:** rejected because stack ordinals change after unrelated stash
  operations.
- **Replay `running` records on restart:** rejected because process completion may have happened
  before persistence and replay could duplicate commits or deletion.
- **Let callers choose worktree destinations:** rejected because it permits path reuse, root
  escape, and deletion of user-owned worktrees.
- **Run arbitrary bisect shell commands:** rejected because it turns a Git lifecycle API into a
  general host execution channel.
- **Infer remote force push after local rebase:** rejected because local approval does not grant
  remote history-rewrite authority.

## Validation / 验证

After rebasing onto `origin/main@715591a`, the final
`go test -p 2 -count=1 -timeout 30m ./...` release gate passed, including Store in 748.018s,
Application in 499.105s, HTTP in 133.862s, CLI App in 98.970s, and repository in 101.197s.
Focused Advanced-Git/LSP race tests and complete race tests for the small protocol, Checkpoint,
and Desktop packages passed. Ordinary and Desktop-tag vet, new-code scoped staticcheck, module
verification/tidy, 58 Linux/amd64 no-CGO test-binary compilations, 65-file/288-test Web
validation, strict TypeScript, production build, npm audit, Rust fmt/tests/Clippy, the pre-rebase
online RustSec audit, and a final cached audit against 1217 advisories also passed. Cargo.lock is
unchanged.

OpenAPI and TypeScript bindings reproduced the same bytes twice: 144 paths, 160 operations,
419 schemas, OpenAPI SHA-256
`116952142fb55bae2cfbe9b13f7e5742f16d14760989d0618acd0e6a43e240f8`, and TypeScript SHA-256
`2cb61016a393b5160bf6c37a2751f5d80bff5b5184aaddbd35290af860b284a4`. An unfiltered
repository-wide `SA*/S1*/QF*` staticcheck reports eight pre-existing mainline findings in six
files; all six files are byte-identical to `origin/main` for this diff. The first default-parallel
tree run hit a load-sensitive timeout in mainline command-runtime cancellation code; 20 ordinary
and five race-isolated repetitions passed. A later Application rerun hit the mainline UI-evidence
test's 500ms total deadline during source revalidation; 20 ordinary isolated repetitions and the
final `-p 2` tree passed. Local `govulncheck` on Go 1.26.5 reports five reachable standard-library
advisories, all marked fixed in Go 1.26.6. This change adds no Go module.

Operational details are in [Advanced Git Workflows](../git-advanced.md).
