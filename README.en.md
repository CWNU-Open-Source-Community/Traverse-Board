<div align="center">
  <h1>Prayu</h1>
  <p><strong>A local-first, resumable, and auditable workbench for general-purpose AI agents</strong></p>
  <p>
    <a href="README.md">简体中文</a> |
    <a href="README.en.md">English</a>
  </p>
  <p>
    <a href="https://github.com/Qiyuanqiii/CTF-CyberAgent-Workbench/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Qiyuanqiii/CTF-CyberAgent-Workbench/ci.yml?branch=main&style=flat-square"></a>
    <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/Qiyuanqiii/CTF-CyberAgent-Workbench?style=flat-square"></a>
    <img alt="Go" src="https://img.shields.io/badge/control%20plane-Go-00ADD8?style=flat-square">
    <img alt="Desktop" src="https://img.shields.io/badge/desktop-Windows-0078D4?style=flat-square">
    <img alt="Desktop macOS" src="https://img.shields.io/badge/desktop-macOS-555555?style=flat-square">
  </p>
</div>

> **Naming:** The product and interface are named **Prayu**. The repository remains at `Qiyuanqiii/CTF-CyberAgent-Workbench` so existing activity records, bookmarks, and external links keep working. The `cyberagent` CLI, Go module, data directory, and environment variables also remain compatibility identifiers for now; they are not a second product.

## What is Prayu?

Prayu is a local AI agent workbench controlled by Go. It unifies model routing, resumable long-running tasks, workspaces, tool calls, approvals, budgets, memory, and audit events in a run-centric runtime shared by the CLI, TUI, HTTP API, React console, and Windows/macOS Desktop.

A durable user objective is a `Mission`; one resumable execution attempt is a `Run`. Models may plan and propose actions, but Go owns the state machine, credentials, permissions, persistence, and execution boundaries. Repository files, web pages, model text, and tool output are untrusted evidence rather than instructions or authority.

The active product focus is the **general-purpose Code Agent workflow**. CTF-specific and offensive-security solving has moved to an optional add-on scope and is not on the active implementation roadmap. Only generic Skill, Tool, Analyzer, Sandbox, Provider, and Report extension seams are retained for a future independent plugin. See [Product Scope](docs/PRODUCT_SCOPE.md).

## Why Prayu?

The hard part of a useful agent is not merely allowing a model to call tools. Long tasks must remain recoverable, explainable, and constrained across crashes, restarts, approvals, and collaboration.

### Deterministic engineering x agent

- **Hard constraints in Go:** code validates Run state, budgets, scope, policy, approvals, idempotency, leases, and audit records.
- **Dynamic decisions in the model:** the model interprets goals, proposes plans, selects exposed tools, and writes public user-facing updates.
- **Separated facts:** model commentary and Harness-verified facts have distinct provenance. A model saying “done” is never a verification receipt.
- **Least privilege by default:** elevated capabilities require separate, explicit, revocable operator authorization. Persisted settings do not carry runtime authority.
- **Resumable execution:** SQLite is the source of truth for Runs, Sessions, events, checkpoints, and operation receipts.

### One control plane

```text
CLI / TUI / React / Windows + macOS Desktop / CI
                    |
              Go control plane
       +------------+-------------+
       |            |             |
   LLM Router   Tool Gateway   Run Supervisor
       |            |             |
       +------ Policy / Approval --+
                    |
      SQLite / Workspace / Rust / Sandbox
```

The allowed direction is always `TypeScript -> Go -> LLM/Rust/Docker`. TypeScript is not a security boundary. Rust performs deterministic analysis and never owns Agents, Sessions, or secrets.

## Core capabilities

| Area | Current capability |
|---|---|
| Agent runtime | Mission/Run, resumable Supervisor, strict lifecycle, checkpoints, cancellation, retry, budgets, and execution leases |
| Models and context | Mock, Anthropic-compatible, OpenAI-compatible, and loopback-only Ollama providers, routing, qualification, capability probing, streaming, compaction, hierarchical project instructions, explicit user/project memory, and Session continuity trees |
| Planning and collaboration | Plan/Delivery, work items, notes, up to two core children, `batch-delivery.v1` isolated worktrees/branches/mailboxes/review/ordered merge, and 1/2/4/6 read-only fan-out tiers |
| Tools and permissions | Tool Gateway, JSON Schema validation, Policy, Scope, human approval, four host-permission tiers, fixed commands, an ordinary-mode Run-owned command runtime, per-command PowerShell/Git Bash approval, and time-bound Debug terminal input |
| Code workflows | Native folder selection and Workspace import, workspace browsing, repository state/history, diff review, file-edit proposals, read-only `code-intel-lsp.v1` semantic tools, transactional Workspace Checkpoints, stable hunks, stash/rebase/cherry-pick/bisect, managed worktrees, Undo/Redo/Rewind, independent Forks, verification plans, Code Journey, and Handoff |
| Observability | Append-only Run events, Live Activity, public model commentary, Harness facts, Artifacts, Findings/Evidence/Reports, and SARIF |
| Extension seams | Mode-aware inert Skill packages, human-reviewed generated candidates, a two-stage MCP Client, signed `plugin.v1`, restricted lifecycle hooks, Provider and Tool interfaces, an embedded WASI Analyzer, and network-none Docker product execution disabled by default |
| Clients | `cyberagent` CLI, Bubble Tea TUI, authenticated HTTP/OpenAPI, React/Vite, and Windows/macOS Desktop portable preview |

### Model-callable workspace tools

Schema v115 introduces `agent-code-tools.v1`, allowing the root Supervisor to complete real multi-round `search -> read -> change` workflows without giving filesystem authority to the model. Go derives availability from the exact Run, Mission, Workspace, root fingerprint, Surface, Phase, Role, Profile, permission tier, and their revisions. The model can only submit schema-valid arguments and cannot mint or widen that authority.

| Mode | Available tools |
|---|---|
| Code / Plan / root | `workspace_list`, `workspace_read`, `workspace_glob`, and `workspace_grep` |
| Code / Deliver / root with Code or Script Profile | The read tools plus `workspace_change`, `workspace_apply`, and `workspace_delete` |
| Code / Deliver / root with Review or Learn Profile | Read tools only |
| Cyber Surface or Specialist | No `agent-code-tools.v1` tools are advertised; the capability snapshot records the refusal reason |

Read results are deterministically ordered, paginated, and bounded. Root escape, casing aliases, hidden entries outside the Go allowlist (`.github` is the sole code-evidence exception), ignored entries, links or reparse points, binary/non-UTF-8 data, and oversized files fail closed. `workspace_change` creates replace/create/move proposals only; `workspace_delete` is a separate exactly confirmed deletion proposal; `workspace_apply` can apply only an approved exact revision and rechecks source and destination hashes to detect review-time drift. Calls, results or refusals, authority snapshots, budget charges, and bounded Artifacts enter the resumable Supervisor ledger. `cyberagent run show <run-id>`, the Run Detail API, and the Desktop Run page expose the current generation and per-tool availability. This protocol grants no Shell, Git, network, or Sandbox authority. See the [Usage Guide](docs/usage.md) and [ADR 0116](docs/adr/0116-model-callable-workspace-tools.md).

### Read-only LSP code intelligence

`code-intel-lsp.v1` starts only local language servers selected through an explicit operator-reviewed configuration and pinned executable SHA-256. Go owns initialization, document synchronization, cancellation/timeouts, crash restart, and process-tree cleanup. During Code-surface root Plan/Deliver turns, the model sees only capabilities actually negotiated by the server: workspace/document symbols, definition, references, implementation, hover, signature help, diagnostics, and call/type hierarchy. Cyber, Specialist, rename, code actions, and formatting remain unavailable.

Every semantic result binds the Workspace/root, commit/branch/dirty digest, document URI/hash/version, server generation/capability fingerprint, and query/page. Edits, branch changes, root changes, and server restarts make old evidence explicitly stale; escaping URIs, remote links, secret/control text, and oversized results are rejected, sanitized, or marked partial. `code-intel status|qualify`, authenticated OpenAPI, and Desktop settings expose only source, language, health, capabilities, generation, bounded errors, and model-visible tools—never executable, argv, environment, or credentials. A minimal environment and “no network grant” are not an OS sandbox; the configured server must be trusted. See [LSP Code Intelligence](docs/code-intelligence.md) for configuration, the real gopls/TypeScript matrix, and residual risks.

### Transactional Workspace Checkpoints

Schema v117 `workspace-checkpoint.v1` records immutable checkpoints before and after file tools, Run-owned command batches/background Jobs, typed Git writes, and the agent-merge boundary. Each checkpoint binds the base commit, branch, raw Git index, a deterministic tracked/untracked manifest, content hashes, trigger receipt, attempt/capability generation, and recovery grade. Ordinary file and index bytes are deduplicated by SHA-256; ignored, generated, large, sensitive-looking, linked, and external state is represented explicitly instead of being silently advertised as recoverable. Because no portable filesystem watcher is installed, Shell boundaries are explicitly `partial` and cover only observed Workspace/Git state, not effects outside the root.

The Desktop **Workspace Checkpoints** tab, `cyberagent workspace checkpoint ...`, and authenticated OpenAPI routes call the same Application service. Rewind, Undo, and Redo first perform a live/current/target three-way preview, then require the exact cursor CAS and current authority. A restore is a new append-only write: it does not rewrite history, invoke `git reset --hard`, or blanket-delete untracked files. Fork creates a distinct Git worktree, Workspace, Mission/Run/Session, and does not inherit approval, credential, capability, lease, process, or network authority. HTTP/Desktop neither accept nor return an absolute worktree path; Go derives a deterministic sibling from the trusted source Workspace. See [Workspace Checkpoints](docs/workspace-checkpoints.md) and [ADR 0118](docs/adr/0118-transactional-workspace-checkpoints.md).

### Approval-gated advanced Git

Schema v123 `git-advanced.v1` is a default-off Go-owned control plane for content-addressed hunk stage/unstage/revert, stash state with explicit base/index/worktree/untracked roles, durable rebase/cherry-pick/bisect state machines, and worktree create/lock/unlock/remove/prune below one product-managed root. Every write binds repository/common-dir, HEAD/branch, index/worktree/stash/sequencer/upstream, permission, capability generation, and Workspace lease; it renders the complete preview before creating a one-time Approval and Checkpoint, then recomputes every fact before execution. Terminal receipts, base/ours/theirs conflicts, sequence generations, and managed-worktree records are durable and immutable.

The protocol accepts no raw argv, shell, host path, arbitrary ref/pathspec, or bisect command. Git runs with hooks, credential helpers, external diff, executable filter/merge drivers, and interactive entry points disabled. Protected branches, shared-upstream rebase, detached history mutation, force push, reset-hard, clean-force, and dirty/external-worktree deletion cannot be expressed. Restart observes and terminalizes a `running` operation without replay; a provably exact worktree created before registry persistence may be conservatively registered, but the old operation remains `interrupted`. Desktop, `cyberagent git-advanced`, and authenticated OpenAPI use the same Application service. See [Advanced Git Workflows](docs/git-advanced.md) and [ADR 0122](docs/adr/0122-go-owned-advanced-git-lifecycle.md).

### GitHub App review integration

Schema v124 `github-review-provider.v1` persists immutable, sanitized, untrusted snapshots of PR metadata, complete bounded changed-file pagination, reviews/threads/comments, checks/jobs, failed-log excerpts, and Artifact metadata. It binds them to the exact local merge-base/HEAD, complete diff, stable hunks, file hashes, conflicts, and optional LSP facts as `verified`, `partial`, `stale`, `unavailable`, or `not_run`. Code/root models receive only Run-bound local `github_review_evidence_list/read`; they cannot fetch or write GitHub.

The preferred authentication is a Device-Flow-enabled GitHub App. Device codes remain in memory and access/refresh tokens live only in the OS credential store. Connections are read-only by default; reply, resolve/unresolve, submit-review, and request-reviewer operations additionally require an explicit connection write-back gate, exact preview, and one-time Approval, then recheck Code/Deliver, network permission, installation/SSO, capability generation, and PR/base/head/merge-base identity. OAuth/PAT remains a read-only migration path in v1 and cannot expand into write-back. Startup recovery only observes idempotency markers and never guesses or replays a mutation. API/Desktop require explicit `--enable-github-review`. See [GitHub Review Provider](docs/github-review.md) and [ADR 0123](docs/adr/0123-go-owned-github-review-provider.md).

### Deliverable children and isolated merge

Schema v118 `batch-delivery.v1` materializes an already approved and admitted core-child DAG into at most two independent Git worktrees and branches. Each child receives a closed tool profile bound to its Agent, generation, expiry, and one-time owner token: list/read/glob/grep within owned scope, reviewed change/apply, and fixed status/diff/commit. Delete/rename, Shell, arbitrary process execution, network, credentials, Debug, approvals, and child spawning remain false. The existing Specialist runtime remains no-tool and is not implicitly widened by this protocol.

A child reaches `ready_for_review` only with a clean worktree, a HEAD descending from the assigned base, owned changed paths, successful required checks, and a receipt binding base/head, full-diff and call-chain digests, validation receipts, evidence, and known limitations. Submit and Review both re-attest the exact branch/HEAD/diff/clean state after validation; the reviewer also independently attests to the full merge-base diff, call chain, and tests, and the Desktop never treats the author summary as evidence. An ordered queue applies each accepted head to a separate integration worktree on the latest confirmed base, reruns every cumulative validation declared by the merged prefix, and re-attests the source, integration, and every child receipt after each step. Overlap, base drift, state drift, textual/semantic conflict, or failed validation blocks the queue; it never selects a side, pushes, opens a PR, or changes a remote.

By default the only executable check is `git diff --check`, which does not run repository code. Because `go_test` and `npm_test` execute child-authored code on the host, the relevant control capability must be enabled and the current Run must still be running with `full_access` (or the explicitly higher `debug` mode); Desktop also requires explicit `--enable-batch-delivery-control`, while host validation additionally requires permission control, danger-full-access, and `--enable-batch-validation-execution`. Validation uses a Windows Job Object or Unix process-group lifecycle boundary, bypasses the Go test cache, and persists only complete-stream output digests. The stripped/offline environment still is not an OS network or filesystem sandbox, and deliberate POSIX daemonization outside the inherited process group remains an explicit host-execution residual. See [Deliverable Multi-Agent Batches](docs/batch-delivery.md) and [ADR 0119](docs/adr/0119-deliverable-batch-agents.md).

### MCP Client, plugins, and restricted hooks

Schemas v120-v121 add a Go-owned MCP Client and inert `plugin.v1` packages. A server descriptor first receives discovery approval, then the actual tools/resources/prompts capability fingerprint is reviewed separately. Only an exact `Code/Deliver/root/full_access` Run sees an enabled tool, and every call rediscovers capabilities so drift is quarantined before execution. Remote HTTPS bearer values are injected by reference from the system credential store and stdio/HTTP output remains untrusted evidence. Models and ordinary UI projections never see plaintext credentials, while the dedicated MCP audit stores metadata only. The Supervisor recovery ledger retains only schema-validated, redacted, bounded canonical calls/results, never bearer values or raw transport bytes. Plugin archives support redirect-free SHA-256-pinned HTTPS and exact-commit bare Git import; upgrade/rollback atomically switch the sole enabled version, and explicit publisher revocation cannot be bypassed by untrusted confirmation.

A Plugin ZIP may contribute only declarative Skills, MCP descriptors, UI metadata, and hooks. Staging validates a strict file allowlist, hashes, bounds, formats, and an optional Ed25519 signature; installations are disabled by default and enabled capability by capability after human review. Hooks now execute at real Go-owned Tool, Run, Session, Compaction, Specialist, and Checkpoint transaction boundaries and can only deny, annotate, record, or remove top-level `pre_tool` fields. The Desktop settings view shows scoped health, provenance, review state, and metadata-only call audits, and disables an exact server/plugin fingerprint and generation immediately. See [MCP Client, Plugins, and Restricted Hooks](docs/extensions.md) for commands, state machines, and residual host risks.
### Source-bound real-browser UI evidence

Schema v119 `ui-evidence.v1` binds real-page verification to the commit/dirty digest/index/worktree manifest, exact build/start recipes, fixed browser version and executable SHA-256, literal loopback URL/route, viewport/DPR, locale/theme/reduced motion, deterministic fixture/seed/page state, steps, and capture policy. Application revalidates source before build, after readiness, after browser assertions, and again after owned-process cleanup before terminal completion. It refuses an occupied port and never adopts an existing service or personal browser Profile. Windows Desktop execution is off by default and appears only when Run execution, `full_access`, danger-full-access, restricted CDP, and `--enable-ui-evidence` all hold.

Desktop, authenticated OpenAPI, and the read/export-only CLI share immutable Attempt, step, and artifact semantics. PNG, DOM, accessibility, console/page-error, network/HTTP, and performance evidence retain SHA-256, MIME, dimensions, viewport, source step/commit, Run/Attempt, redaction provenance, and the retention policy; PNG dimensions must match `viewport × DPR`. Page content and artifacts remain untrusted and non-authorizing. `not_run` is always neutral; only exact `passed` is success. Windows CI runs real Edge in a creation-time Job Object and temporary Profile across desktop/mobile, theme/locale/reduced-motion cells, and proves a missing click handler is detected only by a real-page interaction assertion. See the [UI Evidence guide](docs/ui-evidence.md) and [ADR 0120](docs/adr/0120-source-bound-real-browser-ui-evidence.md).

### Durable scheduled monitoring and structured diagnostics

Schema v122 `scheduled-job.v1` provides one-shot and fixed elapsed-period monitoring for an explicit Run. It persists the IANA display timezone, UTC anchor, next wake, hard deadline, stop-on-terminal behavior, round/model/elapsed budgets, retry/backoff, misfire policy, notifications, and owner. The process-local worker has fixed concurrency one and is enabled only at process startup. Atomic occurrence/attempt/generation claims and private fence digests prevent concurrent workers, restart recovery, or late completion from authoritatively executing the same round twice.

The default is Plan/root `read_only` with a zero model-call budget. A metadata digest excludes the monitor's own events, and an unchanged state records an `unchanged` round without calling a model or tool. `approved_repair` is available only as an exact Code/Deliver, operator-confirmed mode/permission contract that is revalidated before every handoff; production scheduling does not invent a repair executor or bypass the ordinary Policy and approval path.

`doctor-snapshot.v1`, `debug-query.v1`, and `diagnostic-bundle.v1` expose Provider/model Harness, Run, Workspace, permission, network, tool, sandbox, browser, and plugin readiness plus a bounded monotonic event timeline. Debug requires a Run, scans at most 500 events, returns at most 100 items in a seven-day window, supports cursor and exact correlation filters, and always withholds event payloads, prompts, terminal/command input, and secrets. CLI, authenticated HTTP/OpenAPI, React, and Desktop share the same Application contracts; Desktop can create, pause, resume, cancel, inspect, and export redacted bundles. No arbitrary cron Shell, OS service/autostart, remote scheduling plane, or indefinite unattended Agent is added. See [Scheduled Jobs and Structured Diagnostics](docs/scheduled-jobs-diagnostics.md) and [ADR 0121](docs/adr/0121-durable-scheduled-monitoring-and-structured-diagnostics.md).

### Real Git, PowerShell, and Bash

Prayu invokes real Git and operating-system shells; it is not a command emulator. It deliberately does not give the model a permanent, unreviewed raw terminal. The Code workflow separates execution by risk:

| Path | Real execution | Authority and limits |
|---|---|---|
| Typed Git | The real `git` process for whole-file and stable-hunk operations, stash, rebase/cherry-pick/bisect, managed worktrees, and separately authorized fetch, fast-forward pull, branch push, and PR create/update | Go constructs arguments, binds repository/authority/lease state, and requires preview, Approval, and Checkpoint. Hooks, external diff, credentials, and custom drivers are disabled; raw argv, force push, and shared-history rewrite are not exposed |
| Run-owned command runtime | Real PowerShell/Bash or an absolute-path native process, with ordered batches, cursor output, bounded stdin, and background Jobs | Code/Local/Deliver/root + `full_access` only, with process-local permission-control/danger-full-access gates. Interpreters have fixed no-profile argv, the environment is restricted, network/credentials are declared disabled/none, and every Tool call rechecks the current Run lease and Policy |
| Approval one-shot shell | Real PowerShell or Git Bash from the selected Git for Windows distribution, with a fixed no-profile, non-interactive one-shot argv | Code/Local/Controlled/Approval only. The model may propose one line; an operator reviews the interpreter hash, complete argv, cwd, and host-network risk for every execution. No persistent or background ownership |
| Persistent Debug terminal | PowerShell + ConPTY + a creation-time Job Object on Windows; Bash + PTY + a separate process group on macOS | Code/Local/Deliver/Debug only. The user starts the terminal and grants a revocable, process-local Agent-input lease for 15 seconds to 15 minutes. Ordinary background jobs share the terminal lifecycle; deliberate POSIX daemonization is a residual host risk |
| Full-access one-shot process | A real Windows executable and literal argv pinned by absolute path and SHA-256 | Operator-only CLI with two confirmations. It is unsandboxed, may run powerful interpreters, and is not model-facing |

`command_runtime` does not share a session or ownership with the user terminal, Debug terminal, reviewed one-shot path, or Docker Sandbox. Schema v116 writes an immutable launch intent under the current Supervisor generation lease, then keeps a background Job alive under a separate expiring process-owner heartbeat. A later turn can continue reading or writing stdin, while a second process cannot adopt the Job from SQLite. On crash, a creation-time Windows Job Object or POSIX guardian/process group reaps the owned process tree; restart only converges an expired owner record to `interrupted` and never re-executes a persisted PID. Deliberate POSIX daemonization into a new session outside the inherited process group remains an unsandboxed `full_access` residual risk. stdout/stderr retain channel and time under a monotonic cursor, and bounded overflow becomes a SHA-256-addressed Artifact. Model-facing bytes are UTF-8 repaired, secret-redacted, and stripped of ANSI/C1/Unicode controls.

Every `debug_terminal` write still passes Shell Policy; commands that require separate per-command approval cannot bypass it through a Debug lease. Grant establishes an output watermark, so the model cannot read terminal scrollback from before the lease. For resumability, the canonical model command and sanitized bounded results after that watermark enter the Supervisor tool transcript; schema v113 admits this tool to the same durable call ledger without losing existing calls. A process-local Workspace-root digest and exact mode revision prevent an old lease from reviving after root or phase drift. User keystrokes, raw PTY bytes, the root path, and the lease bearer are not persisted. Restart terminates the session and invalidates every lease. These host-shell paths are not exposed on the Cyber Surface. See the [Usage Guide](docs/usage.md), [ADR 0114](docs/adr/0114-real-shell-transports-and-supervised-debug-terminal.md), and [ADR 0117](docs/adr/0117-run-owned-command-runtime.md).

### Security boundaries

- Provider-private thinking, raw prompts, raw deltas, tool arguments, raw tool output, and API keys are never exposed as public activity.
- Project instructions, long-term memory, and conversation checkpoints are always untrusted, non-authorizing context; Workspace Checkpoints retain bounded file/index state only. Neither kind of Fork/Resume restores approvals, capabilities, credentials, network access, processes, terminal leases, or execution profiles. See the [bilingual context, threat-model, and deletion guide](docs/context-continuity.md), [Workspace Checkpoints](docs/workspace-checkpoints.md), [ADR 0115](docs/adr/0115-non-authorizing-durable-context-continuity.md), and [ADR 0118](docs/adr/0118-transactional-workspace-checkpoints.md).
- File edits, host commands, browser CDP, terminal input, and Sandbox execution are independent authorization surfaces.
- A delivery-child owner token is returned once at prepare or generation rotation; SQLite and ordinary HTTP/Desktop projections retain only its digest. Losing it requires an exact-generation rotation, which immediately fences the old token.
- The ordinary command runtime accepts only `network=disabled` and `credentials=none`, clears credential-helper/profile/high-risk environment paths, and rejects explicit network intent. It is not a provable OS network sandbox: `full_access` remains host execution, and a command that needs network access must use a separate per-call reviewed path.
- Conservative commands use Go-owned fixed templates. PowerShell/Bash is available only through one of three independent paths: the Code/Deliver/root + `full_access` Run-owned runtime, per-command approval, or a revocable Debug lease. General host execution and Debug authority cannot be enabled by a model, Skill, or repository document.
- The Docker Sandbox product entry is disabled by default. An explicit process capability, the current `docker` Profile, a matching permission tier, an exact per-call approval, Policy, budgets, and a 30-second readiness check must all hold at once; database records can never restore start authority after a restart.
- Product execution currently accepts only environment-free, secret-free `network=disabled` Manifests and pins `network none` on both the Docker create and inspect sides. Allowlist/scoped egress still lacks a Go-owned host/port/protocol guard, so it always fails closed with `managed_egress_unavailable`; there is no host fallback when Docker is unavailable.
- Windows Desktop exposes loopback-only real-browser evidence only when explicit `--enable-ui-evidence` and its Run-execution/danger-full-access/restricted-CDP prerequisites all hold. macOS and the ordinary CLI remain read-only; Full CDP is still a separate, default-off Debug authority surface.
- Windows/macOS Desktop are currently unsigned developer/operator portable previews, not released installers; the macOS artifact is only ad-hoc signed and not notarized.

### Docker Sandbox product entry (disabled by default)

Schema v99 composes the v97 recoverable lifecycle with the v98 bounded I/O contract
behind one Go `DockerSandboxService`. The CLI, HTTP/OpenAPI, Desktop, and the model
proposal all reuse that service; the model tool `sandbox_docker_run_propose` can only
request admission — it cannot start a container or submit Docker flags, a daemon
endpoint, host binds, environment variables, or network relaxation.

```powershell
# Without the capability this returns the stable disabled readiness and never
# touches Docker write endpoints.
cyberagent run sandbox docker-readiness <plan-id> --manifest-file <manifest.json>

# Real admission/start requires Docker and permission capabilities enabled
# explicitly in the same process.
cyberagent run sandbox docker-admit <plan-id> --manifest-file <manifest.json> `
  --operation-key <stable-key> --enable-docker-execution --enable-permission-control
```

Full CLI, HTTP, cancellation/recovery, reason/remediation, and budget documentation:
[usage manual](docs/usage.md), [HTTP API](docs/http-api.md), and
[ADR 0099](docs/adr/0099-docker-sandbox-product-admission-and-recovery.md).
Ordinary Code workflows still do not require Docker.

### Mode-aware Skills and generated candidates

The 13 built-in Skills use a `profiles × surfaces × phases × roles` compatibility
matrix, with `user_invocable`, `model_invocable`, and `explicit_only` as a
separate invocation policy. Schema v111 preserves the same metadata in the
external-Skill installation ledger. Legacy packages retain their exact
fingerprints and conservative explicit-operator policy. Installation remains
inert: it is not selection, context delivery, or a capability grant.

`run-skill-generator` is explicit-only and limited to the Code/Deliver/root
context. The model-facing `skill_candidate_propose` tool can create only an
untrusted candidate bound to the real tool invocation and exact content
fingerprints. Schema v112 derives either `proposed -> approved -> imported` or
`proposed -> rejected` from append-only facts; model, Agent, Skill, and
Supervisor identities cannot act as the human reviewer. Approval and import
are separate operator actions, import requires a second untrusted-instruction
confirmation, and an imported package is still not selected.

```powershell
cyberagent skill candidates --run <run-id>
cyberagent skill candidate show <candidate-id> --show-content
cyberagent skill candidate approve <candidate-id> `
  --candidate-fingerprint <sha256> --operation-key <stable-review-key>
cyberagent skill candidate import <candidate-id> `
  --candidate-fingerprint <sha256> --operation-key <stable-import-key> `
  --confirm-untrusted-skill
```

See the [usage manual](docs/usage.md) and
[ADR 0113](docs/adr/0113-mode-aware-external-skill-ledger-and-generated-candidate-review.md)
for the complete matrix, candidate bounds, and recovery behavior.

## Quick start

### Prerequisites

- Go 1.25+
- Git 2.41+
- Node.js 24 for Web/Desktop builds
- Windows 10/11 and Edge WebView2 Evergreen Runtime for Windows Desktop
- macOS 11+ (bundled WKWebView) and the Xcode command line tools for macOS Desktop
- Rust 1.97.1 only when changing the Analyzer
- Docker Desktop or a Linux Docker Engine only when developing the Sandbox; ordinary Code workflows do not require Docker

### Run from source

```powershell
git clone https://github.com/Qiyuanqiii/CTF-CyberAgent-Workbench.git
cd "CTF-CyberAgent-Workbench"

go run ./cmd/cyberagent version
go run ./cmd/cyberagent provider list
go run ./cmd/cyberagent workspace init demo
go run ./cmd/cyberagent workspace list
go run ./cmd/cyberagent tui
```

The default configuration uses the deterministic Mock Provider and requires no API key. Read [Model and Provider Commands](docs/usage.md#model-and-provider-commands) before connecting an external model. Credentials belong in the OS credential store or process environment, never in the repository.

Local Ollama is the only keyless Provider and enables only when `CYBERAGENT_OLLAMA_BASE_URL` (loopback `http` only, default `http://127.0.0.1:11434`) and `CYBERAGENT_OLLAMA_MODEL` are set explicitly. Non-loopback hosts, HTTPS, redirects, and proxy bypasses are rejected; tools/vision/JSON/context capabilities fail closed from `/api/show` probing, and Prayu never installs Ollama, pulls a model, or scans the LAN.

### Windows Desktop preview

```powershell
./scripts/build-desktop.ps1
./build/desktop/Start-Prayu-Operator-Preview.cmd
```

Use the operator-preview launcher. Opening the bare `cyberagent-desktop.exe` intentionally starts with the most conservative permissions. See [`packaging/windows/LOCAL-TEST-GUIDE.txt`](packaging/windows/LOCAL-TEST-GUIDE.txt) for the full manual test flow.

In Windows Desktop, **New Task** opens the native folder picker and completes `select directory -> register Workspace -> create Run`; no CLI or settings-page pre-registration is required. Cancelling creates neither a Workspace nor a Run, and the selected absolute path is never returned to React.

### macOS Desktop preview

```bash
./scripts/build-desktop-darwin.sh
open build/desktop/Prayu.app
```

Use the operator-preview launcher `build/desktop/Start-Prayu-Operator-Preview.command`, or open `Prayu.app` directly (read-only default). The artifact is only ad-hoc signed and not notarized; after copying it from another machine you may need to right-click and choose Open in Finder on first launch. The macOS system credential store is not wired yet, so use environment variables such as `MIMO_API_KEY`, `DEEPSEEK_API_KEY`, and `CYBERAGENT_ANTHROPIC_API_KEY`. The user terminal stays off by default and uses a local Bash PTY when the corresponding startup gates are enabled; the restricted browser and Full CDP stay off. See [`packaging/macos/LOCAL-TEST-GUIDE.txt`](packaging/macos/LOCAL-TEST-GUIDE.txt), [ADR 0097](docs/adr/0097-macos-desktop-portable-build.md), and [ADR 0114](docs/adr/0114-real-shell-transports-and-supervised-debug-terminal.md).

See the [Usage Guide](docs/usage.md) for more commands and boundaries.

## Repository layout

| Path | Purpose |
|---|---|
| `cmd/cyberagent` | CLI, TUI, and API entry point |
| `cmd/cyberagent-desktop` | Windows/macOS Desktop shell |
| `internal/` | Go domain, application, Policy, Store, Tool, Sandbox, and HTTP control plane |
| `web/` | React/Vite operator UI; owns no authority, secrets, or executor |
| `analyzers/` | Deterministic Rust Analyzer and shared vectors |
| `configs/` | Secret-free configuration templates |
| `docs/` | Architecture, usage, status ledgers, ADRs, and product scope |
| `packaging/` | Local portable preview and test guidance |

## Documentation

- [Documentation Index](docs/README.md)
- [Product Scope and Optional Extensions](docs/PRODUCT_SCOPE.md)
- [MCP Client, Plugins, and Restricted Hooks](docs/extensions.md)
- [LSP Code Intelligence](docs/code-intelligence.md)
- [Architecture](docs/architecture.md)
- [Usage Guide](docs/usage.md)
- [Current Project Status](docs/PROJECT_STATUS.md)
- [Task Book](docs/TASK_BOOK.md)
- [Contributing](CONTRIBUTING.md)
- [Architecture Decision Records](docs/adr/)

## Historical development record

This section archives the slice and percentage vocabulary that previously occupied the top of the README. These values explain the project's evolution; they are not benchmarks, release promises, or proof of current behavior. Code, tests, [PROJECT_STATUS](docs/PROJECT_STATUS.md), and the relevant ADR remain authoritative.

### Legacy dual-metric snapshot

At **2026-08-13 / schema v96 / P13-H1 through P13-H3**, the old task book estimated:

| Historical metric | Legacy estimate | Meaning |
|---|---:|---|
| Architecture completion | about 99% | Coverage of the Go control plane, Run/Session, events, approvals, budgets, Skills, reporting, and language boundaries |
| Product usability | about 98% | General end-to-end workflows in the developer/operator preview, not production-release readiness |
| General Coding Agent | about 98% | Code workspace, chat, planning, review, proposals, verification, and handoff |
| Cyber automation | about 20% | Retired roadmap estimate; this is no longer an active product metric and is now optional add-on scope |

### Phase and slice index

| Phase | Historical delivery theme |
|---|---|
| v0.1 / P0-P2 | CLI scaffold, Workspace, SQLite, Providers, Sessions, and resumable Run-centric Supervisor |
| P3-P5 | Work/Notes, Coordinator, bounded child/fan-out, Tool Gateway, approvals, Artifacts, and structured memory |
| P6-P8 | Sandbox evidence contracts, Skill Registry, Finding/Evidence/Report, SARIF, and CI projection |
| P9 / Desktop D0-D1 | HTTP/OpenAPI, React/TUI/Desktop, repository/diff/editor/verification/Handoff, and liquid-glass workbench |
| P10-A through P10-M | Go/Rust Analyzer protocol, vectors, embedded WASI execution, one-shot capability, and product integration |
| P11-A through P11-C / schema v119 | Browser permissions, Profiles, CDP/WFP evidence, and the gated source-bound UI-evidence product path |
| P12-A through P12-E | Interaction models, controlled Windows Runner, user terminal, four permission tiers, command approval, and host-execution ledger |
| P13-A through P13-H | Run Activity, public model stream, continuous chat, Markdown, diff review, Live Activity, and desktop visual consolidation |

The complete slice ledger remains in [PROGRESS_BOOK](docs/PROGRESS_BOOK.md), current acceptance evidence in [PROJECT_STATUS](docs/PROJECT_STATUS.md), and resume context in [PROJECT_MEMORY](docs/PROJECT_MEMORY.md). These ledgers are history, not a queue of work to repeat.

## Optional add-ons

CTF solving, automated penetration testing, exploit chains, lateral movement, and offensive tool packs are **outside the active core scope**. The existing `ctf` CLI is an early compatibility scaffold, not a claim of automated solving or attack capability.

A future effort must ship as an independent plugin or Profile behind the existing generic seams:

- `llm.Provider` and the model Harness;
- `tools.Tool`, Skill packages, Policy, and Scope;
- the Go/Rust Analyzer JSON protocol;
- `sandbox.Runner` plus independent network-containment evidence;
- Finding/Evidence/Report and SARIF export.

No add-on may bypass the Go control plane or interpret “CTF” as permission for public scanning, credential access, or destructive commands. See [Product Scope](docs/PRODUCT_SCOPE.md).

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes. Work involving permissions, networking, credentials, processes, persistence, or cross-language ownership requires an explicit threat model, failure semantics, and test evidence.

## License

Prayu is licensed under the [Apache License 2.0](LICENSE).
