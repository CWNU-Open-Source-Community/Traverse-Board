# Frontend rebuild status

## Sandbox environment selection — 2026-10-10

PR #297 merged at main `accd4c47`; the compatibility follow-up is PR #298.
Remote CI at `d056a894` passed all 22 jobs, including Windows operator pagination
and 1,920 frontend tests. The asynchronous child-process diagnostics retain the
original command budget and assertions. The earlier intermittent timeout's
root cause remains open.

Docker Engine 29.6.2 is restored and real lifecycle/application checks pass.
Official sbx 0.47.0 now accepts the corrected `traverse-runtime` namespace.
User authentication, a digest-pinned shell template, two real VMs, static-MCP
positive/negative controls, workspace protections and selected network/SSH
checks have completed. SBX local UUID operations remain unsupported by the
installed CLI. Daemon restart also exposed an empty inventory while owned
runtime metadata still existed and the internal backend was unavailable.
The follow-up preserves uncertain cleanup for that case; complete recovery
acceptance remains in the [record](acceptance/2026-10-10-sandbox-selection.md).

The follow-up starts from main `6a4402e7` after #296 merged. The desktop now
separates Local, Docker Engine and official Docker Sandboxes preferences from
task approval modes, with Local as the default and revision-bound settings
applied through a fixed native restart. Existing CLI startup gates remain explicit.
The SBX adapter and recovery integration are implemented, but production readiness
remains closed pending production static-MCP wiring and verified lifecycle recovery. See
[execution environments](sandbox-environments.md) for setup, current support and
the evidence required to enable SBX execution. This is a recorded implementation
boundary, not completed real-SBX acceptance.

## Completion integration — 2026-10-10

The user requested completion of the remaining roadmap after #295 merged.
The integration branch starts from main `010922ba`; separate implementation
worktrees delivered Plugin/Hooks, Docker/UI Evidence/macOS runtime assembly and
Batch preparation/rework. The integrated Batch worker uses the normal accounted
Specialist runtime to propose scoped edits. The integration owner handles configured-model
journeys, restart/reconnection, responsive/keyboard checks and platform evidence.
The neutral palette and product-language conventions remain the design baseline.
P4 implementation is complete; P5 acceptance remains tracked separately. Progress
and actual acceptance results are tracked in
[the completion record](acceptance/2026-10-10-frontend-completion.md).

Baseline: main `71ede3053b1d71f4c6e4f7053b355ebe301c1a1b`, checked on
2026-10-10. The roadmap is not complete. Merged PRs #288 and #293 finish
the first recovery/connection fixes; #286 contributes scoped journey fixes.
Passing those slices does not establish complete product or platform acceptance.

## Neutral glass follow-up — 2026-10-10

PR #294 merged into main `4a5c8909`. The next user-directed visual pass removes
the separate V2 purple accent and decorative yellow/orange/blue/green palettes.
Shared neutral material tokens supply white filled primary controls, dark button
text and high-opacity light/dark/glass surfaces. Semantic errors, code diffs and
terminal ANSI content retain their meaning. The [palette inventory](branding/neutral-glass-palette.md)
records before/after counts, accessibility checks and original browser evidence.
This visual pass leaves the remaining environment, extension and native journey
roadmap below in place.

## Product guidance revision — 2026-10-10

The user's latest direction treats language and information architecture as part
of the rebuild. Everyday screens should lead with the current state, the action
available to the user and the next step. Replace defensive explanations and
chains of negations with concise guidance tied to real controls. Keep decision
information, such as the effect of deleting a credential or an unresolved write,
at the relevant action; put implementation details in expandable diagnostics.

Three parallel lanes updated task/recovery flows, connections/extensions and
review/diagnostic tools. The connection hub now groups task preparation, tools
and collaboration, and runtime settings around a return-to-task action. MCP,
Plugin and LSP setup show their actual stage and next step. Empty and error
states point to available actions; model, Docker and Safe Web refreshes read
state without repeating a write. Chinese and existing English translations are
updated together. [Product language guidance](PRODUCT_LANGUAGE.md) records the
conventions for future work.

Implementation details use expandable records. Commit hook/signing effects,
shared credential impact, partial file changes and approval scope remain visible
at the relevant decision. Independent review corrected guidance for staged MCP
credentials, configured reasoning effort, unavailable browser/UI Evidence
capabilities and already-applied or denied revert proposals. Existing identity,
approval, recovery and capability checks remain the behavioral acceptance criteria.
Model selection waits for refreshes and pending switches to finish, preserving
the in-flight write state. GitHub credential guidance follows the connection's
actual credential type, enabled state and system-store availability.

Verification for this revision:

- Full Vitest: **186 files, 1,834 tests passed** on the integrated code. The full
  run initially found cross-component assertions referring to old text; their
  selectors were updated while retaining request, binding, recovery and authority
  assertions. The final full run passed without retries or increased timeouts.
  Deferred-response regressions cover both refresh/switch orderings; GitHub
  connection tests cover missing PAT/OAuth credentials and unavailable storage.
- TypeScript, production build and generated API/transcript-key checks passed.
  Existing large-chunk build warnings remain.
- An intermediate CI run interrupted the baseline Windows fixed-command paging
  test before its command timeout. The same seven native subcases passed locally;
  the interruption cause remains unconfirmed. Failure-only authority/renewal
  timing diagnostics now preserve the next failure's evidence, with the existing
  permissions, time budgets, output and replay assertions unchanged.
- Production assets served by the real Go API and isolated test database were
  checked at 1280px and 390px. Verified connection navigation, immutable saved
  budgets, staged MCP credential guidance and read-only refresh, model status,
  Copilot return-focus, task draft retention and permission-menu Escape/focus.
  Connection, extension and model pages had no horizontal overflow at 390px;
  browser error logs were empty. Original captures are retained under ignored
  `build/frontend-rebuild/guidance-*` paths.
- The browser pass used a local fixture with no real model credentials. Credential
  forms and approval gates were inspected; remote authentication, MCP discovery,
  permission changes and remote writes were not exercised. Native platform and
  complete model-to-delivery acceptance remain in the roadmap below.

## Current parallel implementation

All implementation worktrees start from the same baseline. Commits use Qiyuanqiii.
The integration owner reviews and combines the independently tested changes.

| Lane | Implementation scope | State |
| --- | --- | --- |
| Workspace | Direct task navigation, context/recovery and observation entries, file/terminal/preview switching, existing connection/environment entries | Implemented and locally verified |
| Task configuration | Shared Go budget/project-config resolution, immutable creation and successor contracts, safe preview and creation UI | Implemented and locally verified |
| MCP credentials | Go-owned credential presence/write/delete for supported MCP descriptors, first-entry/update/removal UI | Implemented; real OS lifecycle/remote authentication not exercised |
| Review and Git | Existing PR thread/reviewer operations through exact previews and approvals; registered bisect recipe selection and bounds | Implemented; remote writes tested with fixtures only |

The existing Thread/Run identities, draft and attachment recovery, original
operation keys, approval bindings, read-only restrictions, process capability
gates, historical routes and conditional module loading remain acceptance
requirements. No renderer-owned execution or credential authority is introduced.

## Remaining roadmap

- P1/P2: The slices above have contract/recovery tests and production-browser
  checks; the full configured-model-to-delivery journey still needs acceptance.
- P3: Task/project configuration, manual HTTPS MCP bearer credentials and PR
  discussion/reviewer actions are implemented. Cost caps require a price snapshot
  and do not represent exact billing. Project exclusion paths, suggested Skills
  and typed command IDs remain recorded metadata without automatic filtering,
  installation or execution; see ADR 0170.
- P4: Plugin version history/rollback/trust revocation, Hooks diagnostics, Docker
  onboarding, guided UI Evidence, Batch preparation/execution/rework and the scoped
  macOS runtime assembly are implemented in the completion branch. Publisher
  revocation and version switches share a transaction fence; Batch executions
  retain actual model accounting, scoped file checks and independent review.
- P5: Full project/model-to-delivery, restart/disconnection, long-session,
  keyboard/responsive and native cross-platform acceptance is still outstanding.

## First implementation evidence

Local verification used Go 1.26.9 and the repository's pinned frontend packages:

- Full Vitest: **184 files, 1,822 tests passed**. The first combined runs exposed
  missing locale providers in new test fixtures and cold real-module compilation
  consuming the recovery test's action timeout. Both were corrected without
  mocking the recovery callback chain or increasing global timeouts; the final
  full run passed.
- TypeScript and production build passed. Settings, configuration and diagnostic
  surfaces remain lazy-loaded. Existing large-chunk build warnings remain.
- Combined Go checks passed for bounded budgets, initial/restart creation replay,
  changed-budget conflict, project rejection, successor inheritance, v185-to-v186
  upgrade, invalid direct SQL inserts, MCP binding and HTTP/OpenAPI contracts.
  Full projectconfig, protocolregistry, surfacegovernance, releasegate and
  producte2e packages passed. Focused MCP race and injected-store Desktop checks
  also passed. This is not a claim of `go test ./...` on every platform.
- Generated OpenAPI/TypeScript and protocol history checks retain old readers and
  default fingerprints. Independent review found and repaired legacy zero/large
  budget reads, explicit-null parsing and known-rejection retention across draft
  edits/remounts.

A real browser used production assets served by an isolated loopback Go process
and a separate test database. Verified task/context navigation, return-draft
retention, file/preview entries, a 390-pixel layout, configuration source display,
reconnection, immutable saved budgets after the project file changed, and the
exact-Run GitHub entry. A staged manual HTTPS MCP descriptor displayed actual
Windows Credential Manager availability and absence of its dedicated test token.
No secret was entered, OS credential changed, MCP discovery approved or remote
GitHub write executed.

The isolated service deliberately had no configured real model. Both UI and HTTP
refused new Thread creation until model setup. A CLI-created fixture supplied the
saved-budget browser check; HTTP creation success is covered by Go/client tests,
not asserted as a real-account browser journey. Original browser JPEGs and local
logs are retained under ignored `build/frontend-rebuild/`; they are not generated
illustrations or repository fixtures.

These checks do not stand in for native desktop, real remote account, Docker or
macOS acceptance. The overall rebuild remains incomplete.
