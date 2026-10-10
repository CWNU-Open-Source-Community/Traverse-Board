# Sandbox selection integration

Base: main `6a4402e7ddae2e3159c792f1eda4d29da1002088`, after #296.
Branch: `codex/sandbox-backend-selection` in an isolated worktree.

The requested design has three explicit environments with Local as the default.
Local and Docker Engine reuse the existing executable backends. Settings, task
selection, fixed native restart and SBX lifecycle integration are implemented.
Production SBX execution remains blocked by `mcp_isolation_unverified`; the
supported boundary and remaining proof are in [execution environments](../sandbox-environments.md).

## Verification

- Frontend full run: 193 files, 1,910 tests passed before the final browser
  presentation corrections. The final focused run passed 5 files / 246 tests,
  including current four-profile and historical three-profile readiness data,
  and selected Docker/SBX availability. The full count is not extrapolated.
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
