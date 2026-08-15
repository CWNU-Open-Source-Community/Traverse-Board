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
| Models and context | Mock, Anthropic-compatible, OpenAI-compatible, and loopback-only Ollama providers, routing, qualification, capability probing, streaming, context compaction, and structured memory |
| Planning and collaboration | Plan/Delivery, work items, notes, up to two core children, 1/2/4/6 read-only fan-out tiers, shared budgets and cancellation |
| Tools and permissions | Tool Gateway, JSON Schema validation, Policy, Scope, human approval, four host-permission tiers, and fixed-command proposals |
| Code workflows | Native folder selection and Workspace import, workspace browsing, repository state/history, diff review, file-edit proposals, verification plans, Code Journey, and Handoff |
| Observability | Append-only Run events, Live Activity, public model commentary, Harness facts, Artifacts, Findings/Evidence/Reports, and SARIF |
| Extension seams | Inert Skill packages, Provider and Tool interfaces, Go/Rust JSON protocol, embedded WASI Analyzer, Sandbox contracts, and a network-none Docker product execution that is disabled by default |
| Clients | `cyberagent` CLI, Bubble Tea TUI, authenticated HTTP/OpenAPI, React/Vite, and Windows/macOS Desktop portable preview |

### Security boundaries

- Provider-private thinking, raw prompts, raw deltas, tool arguments, raw tool output, and API keys are never exposed as public activity.
- File edits, host commands, browser CDP, terminal input, and Sandbox execution are independent authorization surfaces.
- Conservative commands use Go-owned fixed templates. General host execution and Debug authority cannot be enabled by a model, Skill, or repository document.
- The Docker Sandbox product entry is disabled by default. An explicit process capability, the current `docker` Profile, a matching permission tier, an exact per-call approval, Policy, budgets, and a 30-second readiness check must all hold at once; database records can never restore start authority after a restart.
- Product execution currently accepts only environment-free, secret-free `network=disabled` Manifests and pins `network none` on both the Docker create and inspect sides. Allowlist/scoped egress still lacks a Go-owned host/port/protocol guard, so it always fails closed with `managed_egress_unavailable`; there is no host fallback when Docker is unavailable.
- The built-in browser has no product entry point yet. A restricted runtime core exists, but independent OS/container network-containment evidence is incomplete.
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

Use the operator-preview launcher `build/desktop/Start-Prayu-Operator-Preview.command`, or open `Prayu.app` directly (read-only default). The artifact is only ad-hoc signed and not notarized; after copying it from another machine you may need to right-click and choose Open in Finder on first launch. The macOS system credential store is not wired yet, so use environment variables such as `MIMO_API_KEY`, `DEEPSEEK_API_KEY`, and `CYBERAGENT_ANTHROPIC_API_KEY`; the ConPTY user terminal, restricted browser, and Full CDP stay off on macOS. See [`packaging/macos/LOCAL-TEST-GUIDE.txt`](packaging/macos/LOCAL-TEST-GUIDE.txt) for the full manual test flow and [ADR 0097](docs/adr/0097-macos-desktop-portable-build.md) for the boundaries.

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
| P11-A through P11-C | Browser permissions, Profiles, CDP, and WFP evidence; product entry remains closed |
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
