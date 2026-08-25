# Surface inventory / 产品 Surface 清单

- Inventory version: `surface-inventory.v1`
- Evidence baseline: `main@0751fadbe74feb7640750e4944643e14c41acaa2`
- Authority: [ADR 0135](../adr/0135-pre-1-0-product-convergence.md)

## Classification

“Surface” means a supported user or control entry point. A provider, execution
backend, integration, or extension seam can have a support tier without becoming a
second product surface.

| Tier | Product commitment | Allowed change before reassessment |
| --- | --- | --- |
| `active` | Core product work and release evidence; shared Go Application contract | Features, security, compatibility, accessibility, recovery, and release work |
| `maintenance-only` | Existing users and automation remain supported | Security, data-loss, compatibility, and severe defect fixes; no new workflow without an explicit tier change |
| `extension-only` | The seam is supported; an optional implementation is not a core release requirement | Contract/security fixes and independently approved extension work |
| `deferred` | No current implementation or release promise | Research and ADR work only; implementation requires entry review |

## Inventory

| Item | Kind | Tier | Current boundary and release obligation |
| --- | --- | --- | --- |
| Windows and macOS Wails Desktop | User surface | `active` | Primary packaged product. Uses the same in-process authenticated Go HTTP handler and React Thread workbench; Windows/macOS build and startup checks are release-blocking. |
| React/Vite Thread workbench | User surface | `active` | One renderer shared by Desktop and authenticated loopback Web. Thread is the main route; TypeScript is presentation-only and cannot mint authority. |
| `cyberagent` CLI | User/control surface | `active` | Stable automation and operator entry. The executable name is a compatibility ID, but its Thread/Run/Workspace controls remain active. |
| Loopback HTTP and OpenAPI `/api/v1` | Control surface and external contract | `active` | Shared Go Application adapter used by Web/Desktop clients and integrations. Loopback binding, token separation, generated OpenAPI, and compatibility tests are required. It is not a multi-tenant remote service. |
| Bubble Tea TUI | User surface | `maintenance-only` | Retains read/control compatibility and critical fixes. New workflows target the Thread workbench and CLI first; TUI parity is not a beta release gate. |
| `headless` event/CI commands | Automation surface | `maintenance-only` | Keeps deterministic CI and existing scripts working. It is not a separate interactive product or authority path. |
| `/runs/{id}` and `/sessions/{id}` diagnostic views/routes | Compatibility surface | `maintenance-only` | Remain available for diagnostics and API compatibility. They are not alternate stable task identities and receive no new primary workflow. |
| MCP client and Go-controlled MCP server | Extension seam | `extension-only` | Discovery, review, negotiation, and invocation remain behind Go policy. Core use and release cannot require an MCP server. |
| Signed Plugin and restricted Hook runtime | Extension seam | `extension-only` | Optional, inert-by-default integrations. Plugin/Hook content never grants process, network, credential, or file authority. |
| Skill packages and registries | Extension seam | `extension-only` | Built-in Skills support the core; third-party packages are untrusted optional extensions with signed/pinned provenance where required. |
| Rust/WASI and local analyzer contracts | Extension/backend seam | `extension-only` | Go remains the sole control plane. Analyzer formats and golden vectors are supported contracts, but analyzers cannot own Agent, Session, secret, or host-process lifecycle. |
| Windows AppContainer and fixed Docker Standard Code | Execution backend | `active` | Active backends reached only through active surfaces. Readiness, ownership, recovery, `network=none`/WFP, exact cleanup, and real-platform CI remain release evidence; they are not new UI surfaces. |
| Browser/CDP runtime and real UI evidence | Capability/backend | `active` | Active, bounded capability behind Go permission and containment gates. A new standalone browser product, arbitrary remote browsing, or renderer-owned CDP is not implied. |
| Workspace/Git/GitHub Review | Capability/integration | `active` | Active Code workflow capabilities reached through the same Application contract. GitHub network writes remain optional, credential-scoped, approval-gated integrations rather than a separate product surface. |
| Checkpoints, batch delivery, and multi-agent coordination | Capability | `active` | Active Run/Workspace capabilities. Child agents, worktrees, mailbox, merge review, and recovery remain Go-owned; they do not create another control plane or client. |
| `ctf` command, `cyber` labels, and offensive workflow skeletons | Compatibility/extension seam | `extension-only` | Existing identifiers remain readable. CTF solving and offensive automation are not core features and require a separately approved removable extension. |
| Cloud/multi-tenant daemon, account sync, hosted control plane | Product surface | `deferred` | No authentication, tenancy, data residency, operations, or support commitment exists. Loopback HTTP cannot be rebranded as a hosted service. |
| Native Linux Desktop, mobile clients, editor-native full clients | Product surface | `deferred` | No release promise. A thin integration may use the external API, but a first-party client requires entry review. |
| Public extension marketplace and autonomous offensive packs | Product/extension distribution | `deferred` | Requires independent supply-chain, moderation, legal, threat-model, and removal policy decisions. |

## Entry criteria

A new top-level Surface, or promotion to `active`, requires all of the following in
one bounded ADR before implementation:

1. named product/maintenance owner and an identified user problem not already served;
2. reuse of the Go Application contract, or a precise reason and threat model for a
   new adapter; no second state machine or renderer-owned authority;
3. Scope, policy, approval, credential, network, process, persistence, privacy, and
   recovery impact;
4. supported OS/runtime matrix, accessibility and bilingual copy obligations;
5. normal, refusal, timeout, cancel, restart, upgrade, downgrade, and cleanup evidence
   proportional to the authority it exposes;
6. release cost and CI owner, including which checks become release-blocking;
7. compatibility contract, telemetry-free support signal, deprecation window, export
   path, and removal/rollback plan; and
8. an independently removable implementation slice that does not make optional
   extensions a core startup dependency.

Backends and capabilities use the same security/recovery criteria but must not be
marketed or counted as additional user surfaces.

## Tier changes and removal

- `active -> maintenance-only` requires an ADR, release notes, and proof that active
  workflows have a supported replacement.
- `maintenance-only -> removed` requires at least 90 days **and** two tagged
  prereleases after the deprecation notice, unless an emergency security advisory
  documents a shorter window.
- External durable formats, HTTP routes, CLI contracts, database identities, and
  exported receipts cannot be removed by a Surface tier change; their protocol class
  governs retirement separately.
- Removal must preserve export/recovery of user data and include old-client/old-store
  fixtures. No usage telemetry is assumed, so absence of reports is not evidence of
  zero use.
- `extension-only` means optional, not unsafe or unmaintained. Its boundary and parser
  still receive security and compatibility fixes.
