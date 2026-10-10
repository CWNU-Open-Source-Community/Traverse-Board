# Sandbox selection integration

Base: main `6a4402e7ddae2e3159c792f1eda4d29da1002088`, after #296.
Branch: `codex/sandbox-backend-selection` in an isolated worktree.

The requested design has three explicit environments with Local as the default.
Local and Docker Engine reuse the existing executable backends. Settings, task
selection, fixed native restart and SBX lifecycle integration are implemented.
Production SBX execution remains blocked by `mcp_isolation_unverified`; the
supported boundary and remaining proof are in [execution environments](../sandbox-environments.md).

## Verification

- Frontend remote CI at `d30fd001`: 193 files / 1,917 tests passed, including
  the final selected-backend presentation corrections. The local focused run
  also passed 5 files / 246 tests, covering current four-profile and historical
  three-profile readiness data and selected Docker/SBX availability.
- TypeScript type checking and production Vite build passed.
- Windows desktop and embedded assets: `go test -tags desktop,wv2runtime.error
  ./cmd/cyberagent-desktop ./web -count=1` passed.
- Settings/API tests passed with the race detector. They cover separate saved
  and active values, exact replay, revisions, strict bounded requests and
  read/control token separation.
- Integration recheck passed for application, HTTP and desktop readiness,
  OpenAPI live routes, environment settings and startup/restart paths.
- SQLite v188 upgrade and clean-install tests passed. Historical snapshots,
  latest pre-v188 triggers, Thread continuation and foreign-key validity are retained.
- Runner race regressions passed for sandbox cleanup uncertainty, durable
  stopping state, original-request replay, lease fencing, late renewal and
  repeatable shutdown draining.
- Local/Docker dispatch and output regressions passed (89.396 seconds). They
  distinguish refusal before dispatch, unresolved cleanup after dispatch, and
  confirmed cleanup followed by a result-finalization failure.
- Additional native Windows Local regressions passed (2.008 seconds; expanded
  race run 5.403 seconds). They reach real process creation and owned Job cleanup,
  and distinguish failed creation from failed cleanup confirmation. A failed
  native Job query preserves uncertainty; a pre-creation refusal returns the
  known absence of a command tree. These tests are included in the Windows CI
  filter. Sandbox vet and Darwin arm64 sandbox cross-compilation also passed.
- Docker first-start provenance regressions passed (187.087 seconds) against
  real SQLite admissions. They cover readiness expiry, pre-lifecycle validation,
  lost Start commit results, existing Start/Launch replay, cleanup failure and
  result-finalization failure after cleanup. Final gate/concurrent cancellation
  race tests passed (10.264 seconds), and existing Start/WAL-retry/Cancel/replay
  regressions passed (3.456 seconds). Same-operation cancellation retries reuse
  the original server timestamp; different operations and owners are rejected.
  This uses a controlled Docker transport, with no daemon or real container.
- SBX fake-transport lifecycle and permission tests passed, including production
  MCP fail-closed checks. Darwin arm64 sandbox/application test binaries compiled.
  These runs do not establish real SBX isolation or execution.
- The real Windows Local attachment shell check passed with the installed
  PowerShell 7 path explicitly configured.
- Protocol registry validation against the base commit, Surface registry and
  generated inventory validation, and `git diff --check` passed.
- Documentation/release contract packages `internal/releasegate` and
  `internal/producte2e` passed.

The first wider backend run found a real integration omission: readiness
validation still expected three profiles after SBX added a fourth. It also
identified missing setup for the new OpenAPI live routes. Both were fixed and
the affected tests passed again. The Local shell check initially selected the
legacy shell until its PowerShell 7 prerequisite was supplied. One Docker Ask
dispatch test exceeded its existing 20-second command deadline during that run;
its isolated recheck passed unchanged for Ask / Auto / Full (69.147 seconds total),
without increasing the command deadline. This is recorded in the local integration log.
An additional expanded Docker run timed out in the existing
`TestStandardCodeDockerApprovalPreferencesCheckpointAndRecovery` Git/Drydock
source inspection; that run is not counted as a pass. The final focused cleanup
regressions completed independently.

The first remote CI run also caught an omitted v188 development-history entry
and the Desktop Bridge method allowlist still expecting 19 exports after the
fixed settings restart method was added. Both contracts were updated, and their
targeted regression tests passed locally before the CI rerun.

At `d30fd001`, full-repository Go vet, all non-Store Go packages, all eight
Store shards, frontend, authority race checks, macOS Desktop and real Edge
evidence passed. Windows Desktop failed two existing
fixed-operator subtests: `multiple-output-pages` lost an ownership check when
`GetRun` exceeded its 2-second deadline; `native-timeout` stored a `timed_out`
receipt with `TreeReaped=true`, then returned `context deadline exceeded`.
The latter log did not identify the failing lease operation. Test-only
diagnostics now record Run lease renewal and release timings alongside the
existing ownership diagnostics. The two subtests passed unchanged locally
(4.799 seconds), then passed with the added diagnostics (3.386 seconds). These
rechecks do not establish the remote failure's cause or replace Windows CI.
Production deadlines and lease handling were not relaxed.

At `438fc5fd`, Windows repeated only `multiple-output-pages`. Its diagnostics
showed a lease renewal occupying the sole SQLite connection for 2.84 seconds;
the ownership `GetRun` exhausted its existing 2-second deadline while waiting
for that connection. No output had been persisted at that point. The log does
not identify the slow transaction phase, so disk flush latency is not an
established cause. `native-timeout` passed on this run. The Windows failure
remains under investigation, with its original assertions and deadlines intact.

The same CI run exposed two model-menu focus failures. Deterministic local
regressions reproduced restoration depending on a menu item's disabled flag
after asynchronous completion, and a delayed opening frame taking focus from
the composer. The fix retains the original focus owner through pending-state
changes, relinquishes it when focus moves elsewhere, and focuses an opened menu
at DOM commit. All 20 focused tests and 69 app/composer integration tests passed;
the production frontend build passed. These focused results do not replace the
next complete frontend CI run.

The first Docker start-provenance regression attempt exposed a test fixture
that had not yet created its host mask, and exceeded its 90-second package
budget in Git fixture setup. The fixture was corrected and the package budget
set to 300 seconds for the completed run. Product command and lease deadlines
were unchanged. That attempt also reproduced a real cancellation replay bug:
regenerating the server timestamp changed the persisted request fingerprint.
The sequential and concurrent exact-request cases are covered by the final tests.

## Browser evidence

Playwright opened a real Chromium instance against a static page built from the
production `V2Settings`, `SandboxEnvironmentPanel` and `V2ExecutionSettings`
components. A visible fixture banner identifies in-memory installation and
execution data. The page has no model or native execution connection.

Checks cover 1440-pixel desktop and 390-pixel narrow layout, three visible backend
choices, Local default, saved Docker preference with unchanged active process,
task choice persistence, and unready SBX guidance. Browser inspection caught the
generic Local readiness label being reused for the selected unready backend;
the presentation now follows the selected backend.
Final browser captures confirm both unavailable backends show their own
configuration action. The task and settings pages both have a document width
of 390 pixels at a 390-pixel viewport, with no horizontal overflow.

A further real Edge check used the production `V2ModelRouteControl` with a
labelled 1.2-second in-memory save delay. After completion, DOM inspection
confirmed the model trigger retained focus and the menu closed. When the user
moved to the draft during that delay, completion retained textarea focus and
its exact text. The two screenshots are `model-focus-return.png` and
`model-focus-composer.png` in the same local evidence directory.

Raw screenshots and command logs are local, ignored artifacts under
`output/playwright/sandbox-selection/` and `build/sandbox-backend-selection/`.
They are UI evidence, not a real Docker/SBX execution receipt. The only browser
console error during the static preview was its missing favicon.

## Remaining acceptance

This host has no installed SBX. No SBX installation, login, image pull or real
microVM execution was performed. The SBX production gate remains closed pending
the official MCP, effective daemon settings, workspace synchronization and
resource-identity evidence described in the environment guide. Real user-owned
Docker/SBX acceptance is separate from deterministic integration coverage.
