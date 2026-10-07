# Workbench journey follow-up — 2026-10-07

This is a bounded follow-up to roadmap #257 after its seven delivery tasks and
the task-history polling follow-up merged. The branch starts from main
`736f47dcae16e0240fb07ec85042e16b68243214`. It fixes four frontend integration
defects; it does not declare the entire roadmap or release matrix accepted.

## Reproduced defects and resulting behavior

| Journey | Failure before this patch | Behavior after this patch | Evidence |
| --- | --- | --- | --- |
| Inspector → extension settings → task input | The explicit input-area button removed only the settings section, retaining Inspector and any historical Run/Session route. | Open the conversation for the same Thread, or the selected project's new conversation. Preserve drafts and attachment references. Ordinary settings Back still returns to its source. | Production browser before/after; four app regressions failed before the fix. |
| A new draft uses a fallback model | Image eligibility consulted an unavailable named default, or no default at all, even though creation selected another available model. | Match creation's selectable `code` default, then first selectable fallback. An explicit provider/model selection keeps its identity. | Three actual Composer regressions failed before the fix; six added cases include explicit text/missing selections and named-default priority. |
| Open a file from an old Run whose directory is unavailable | Failed change-set resolution and a review entry without a workspace fell back to the current target or source workspace under the old Run label. | Require an exact Run binding. Unavailable directories and read failures show a retry action without mounting the file explorer. A successful exact-Run review still works. | Component regression observed `workspaceExplore("current-workspace", "src/index.ts", ...)` for `run-old` before the fix; unavailable, exact-review fallback and retry cases pass afterward. |
| Desktop bootstrap → GitHub review capability | Connection bootstrap dropped `github_review_control_enabled`, hiding an available review capability from the client. | Carry the backend flag through the existing connection store and client gates. | Bootstrap regression failed for enabled GitHub review before the one-line fix; enabled, disabled and read-only cases pass. |

No backend ownership, protocol, execution authority or storage format changes.
The work retains [Go-owned capability readiness](../adr/0128-go-owned-run-capability-readiness.md),
[Run-owned workspaces](../adr/0129-run-owned-drydock-workspaces.md), and
[separate extension onboarding/review](../adr/0168-extension-first-onboarding.md).

## Production browser exercise

On Windows, build the CLI and production Web assets and serve them on loopback
with an isolated `CYBERAGENT_HOME` and separate generated read/control tokens.
Use a disposable local Git project, not the normal project database. Do not
qualify or call external model providers during this exercise.

1. Connect through the actual token form. Type an unsent requirement, import the
   disposable `alpha-demo` Git directory through **接入项目**, and observe that
   project selection and the draft remain intact.
2. Open Inspector, Settings and **扩展与代码智能**. On the base build,
   **打开新任务输入区** leaves the URL at `#/new/inspector` with no composer.
3. Restart the API with the patched production assets, reload the browser and
   reconnect. Select the same project and click the same button. The URL is
   `#/new`; the composer contains the original draft and selected project.
4. In the same settings path, register a local stdio MCP fixture with one
   `lookup` tool, scoped to that project. Through the UI, approve descriptor
   discovery, discover the tool, review its capability fingerprint, and enable.
   Observed states are `staged` → `discovery_approved` →
   `capabilities_pending` → `enabled`; the peer is healthy.
5. Click the enabled server's **打开任务输入区**. It returns to `#/new` with the
   existing draft intact. A subsequent real API read reports zero Threads and
   zero MCP call receipts: discovery and navigation did not execute the task.
6. Close the dedicated browser and stop the isolated API after inspection.

The MCP peer is a local deterministic JSON-RPC fixture supporting initialize
and tool discovery. This proves the real Web → HTTP → Go → stdio onboarding
path, not a model-driven tool call. No browser routes or network responses were
mocked. Local snapshots/screenshots are retained under ignored
`output/playwright/`; test/runtime logs are under ignored `build/journey257/`.

## Verification scope

Final local validation passes:

- `npm test -- --maxWorkers=4`: 174 files, 1,656 tests.
- `npm run build`: strict TypeScript and the production bundle.
- `npm run check:api`: unchanged generated API and transcript keys.
- `go test ./internal/releasegate ./internal/producte2e -count=1`:
  existing documentation/release contracts.

The added cases run the actual app, Composer, file drawer or connection gate,
with API/desktop fixtures for their respective boundaries.

Two initial full Web-suite runs each passed 1,655 of 1,656 tests. One hit the
existing Provider model-merge test's five-second timeout while build and tests
ran concurrently; another hit the existing transcript test's immediate
post-refetch assertion. The latter also reproduced in an isolated run: query
completion does not await React Query's scheduled subscriber notification.
Its test now waits for the observed pagination/render update, keeping every
identity, authority and unchanged-object assertion. No production transcript
logic or test timeout was changed. Both tests pass in the final full suite.

Native WebView2/GitHub login, a real vision-model request, actual deletion of a
Run-owned Drydock in the browser, Plugin/LSP end-to-end execution, application
startup, and model-driven Git/PR delivery were not exercised here. The earlier
[core integration report](2026-10-05-core-integrations.md) is historical evidence,
not a replacement for those checks on this branch.

## Follow-up candidates

Static inspection found two further lifecycle paths worth reproducing before
changing behavior: resetting a Thread to an unavailable profile default when a
custom fallback is ready, and offering Run-scoped MCP registration for a
completed last Run. These are unaccepted candidates, not completed fixes in
this patch. The remaining broader roadmap acceptance should exercise real
multi-turn execution, approvals, preview and delivery together.
