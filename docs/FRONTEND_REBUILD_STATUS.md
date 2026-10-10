# Frontend rebuild status

Baseline: main `71ede3053b1d71f4c6e4f7053b355ebe301c1a1b`, checked on
2026-10-10. The roadmap is not complete. Merged PRs #288 and #293 finish
the first recovery/connection fixes; #286 contributes scoped journey fixes.
Passing those slices does not establish complete product or platform acceptance.

## Product guidance revision in progress — 2026-10-10

The user's latest direction treats language and information architecture as part
of the rebuild. Everyday screens should lead with the current state, the action
available to the user and the next step. Replace defensive explanations and
chains of negations with concise guidance tied to real controls. Keep decision
information, such as the effect of deleting a credential or an unresolved write,
at the relevant action; put implementation details in expandable diagnostics.

Three parallel lanes cover task/recovery flows, connections/extensions and
review/diagnostic tools. Integration owns the settings structure and consistent
language. Existing identity, approval, recovery and capability checks remain the
behavioral acceptance criteria. Chinese and existing English translations are
updated together. This revision is under implementation and awaits integrated
tests and a real-browser check.

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
  onboarding, guided UI Evidence, Batch preparation/rework and the scoped macOS
  runtime assembly remain separate unfinished work.
- P5: Full project/model-to-delivery, restart/disconnection, long-session,
  keyboard/responsive and native cross-platform acceptance is still outstanding.

## Evidence

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
