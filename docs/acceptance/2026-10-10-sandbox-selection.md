# Sandbox selection integration

Base: main `6a4402e7ddae2e3159c792f1eda4d29da1002088`, after #296.
Branch: `codex/sandbox-backend-selection` in an isolated worktree.

The original change merged as #297 at main
`accd4c47dd7b882064e98f030e15e4c737237a00`. Its tree matches the verified
`39349b3b` head. The native-host compatibility follow-up continues from that
latest main in the same isolated worktree on `codex/sandbox-runtime-compatibility`.

The requested design has three explicit environments with Local as the default.
Local and Docker Engine reuse the existing executable backends. Settings, task
selection, fixed native restart and SBX lifecycle integration are implemented.
Production SBX execution remains blocked by `mcp_isolation_unverified`; the
supported boundary and remaining proof are in [execution environments](../sandbox-environments.md).

## Verification

- Full remote [CI at `39349b3b`](https://github.com/CWNU-Open-Source-Community/Universal-Code/actions/runs/38040097864)
  completed successfully: all 22 jobs passed. TypeScript covered 193 files /
  1,920 tests; repository Go vet and 75 tested packages passed, together with all
  eight Store shards, authority race checks, Windows/macOS Desktop and Edge.
  Windows actually ran and passed all six added Local process/cleanup proof
  tests and all seven fixed-operator subcases, including pagination. Five
  child-only helpers skipped their top-level invocation as intended. Two
  Analyzer conformance tests still skipped because the hosted service session
  rejected their low-integrity helper with `STATUS_DLL_INIT_FAILED`.
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
established cause. `native-timeout` passed on this run. Both subcases passed on
the subsequent `39349b3b` CI run with unchanged backend code, assertions and
deadlines. That success establishes the latest run's result; the intermittent
database delay still needs phase-level evidence if it recurs.

The same CI run exposed two model-menu focus failures. Deterministic local
regressions reproduced restoration depending on a menu item's disabled flag
after asynchronous completion, and a delayed opening frame taking focus from
the composer. The fix retains the original focus owner through pending-state
changes, relinquishes it when focus moves elsewhere, and focuses an opened menu
at DOM commit. All 20 focused tests and 69 app/composer integration tests passed;
the production frontend build passed. The subsequent complete frontend CI at
`39349b3b` also passed all 1,920 tests.

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

### Native host preparation

Docker Desktop 4.85.0 initially stopped while creating its Inference manager
socket. The stale `Docker/run/dockerInference` entry was a zero-byte Windows
reparse point that returned error 1920. After preserving its parent directory,
startup reached a second stale socket, `docker-secrets-engine/engine.sock`.
The affected IPC directories were renamed and retained after an official
Desktop shutdown. The original images, containers, volumes and WSL disk were
preserved. The fixed local named-pipe endpoint then returned Docker Engine
29.6.2, Linux/amd64, API 1.55. This proves that the local daemon recovered;
application container execution has its own acceptance below.

Official Docker Sandboxes v0.47.0 was installed from the Docker.sbx winget
package. Its Docker Inc. signature and installer digest were verified. The CLI
and hypervisor capability checks pass. Real CLI probing exposed the adapter's
original overlong app-name: sbx accepts at most 20 characters. The fixed
`traverse-runtime` namespace passes the installed CLI; configuration regressions
cover the length and character boundaries.

The dedicated daemon initially started. Its namespace settings now persist
`ssh.agentForwardingEnabled=false` and `mcp.forceLocalGateway=true`. The required
restart encountered a stale `containerd.sock.ttrpc` reparse point. The daemon
is currently unreachable, so effective settings and authentication have not
been verified. Automatic approval review rejected the attempted state-directory
backup/restart command with `blocked by policy`; that command did not execute.
No namespace reset or alternative repair was attempted after that rejection.

SBX now scans the granted workspace after the final authority callback and
before creating a VM. Multi-link regular files, symlinks, Windows reparse
points and special files are refused; the scan has an entry limit and observes
cancellation. Real Windows filesystem regressions cover an outside hard link,
a hard-linked `.git`, junctions, and a link inserted by the authority callback.
They confirm the outside file is unchanged and no VM lifecycle mutation is
dispatched. Ordinary nested files still pass the controlled transport lifecycle.
All SBX race tests passed locally (3.829 seconds), and Linux amd64 / Darwin arm64
test binaries cross-compiled. Windows CI now includes the SBX suite explicitly.
These are pre-dispatch checks; they do not establish VM isolation under later
host-side concurrent mutations.
The final Windows CI command passed locally for sandbox and desktop; the
existing desktop symlink-privilege test skipped on this unelevated host, while
the new junction tests ran successfully. The application SBX integration suite
also passed (13.036 seconds). Namespace UI tests passed 7/7, the production
frontend build passed, and release/documentation contract tests passed.
The Linux test binary also ran successfully in an owned restricted Linux
container, including actual Unix hard-link and symlink regressions with no
skips. An initial command-argument quoting error exited before test execution;
the corrected invocation passed. This verifies the Unix preflight code, while
SBX lifecycle calls in that binary still use the controlled transport.

No SBX login, template pull or real microVM execution has completed. The
production gate remains closed pending the MCP, effective daemon settings,
workspace synchronization and resource-identity evidence in the environment
guide. The candidate MCP probe is a separately tested, zero-tool local fixture;
it has not been registered with a daemon or exercised from a VM.

### Real Docker Engine acceptance

The restored fixed local Engine ran the existing opt-in lifecycle, network,
readiness, read-only observation and Standard Code tests successfully (24.780
seconds, no skips). The four real toolchains were Go, Node, Python and Rust.
Checks also exercised denied DNS/IPv4/IPv6/host routes, forced-timeout cleanup,
the 16 MiB single-file limit and the 4,096-entry workspace growth limit.

An ignored Go test overlay then exercised the application Standard Code service
with a real SQLite store, owned Git/Drydock fixtures, the real local lifecycle
and I/O transports, and existing pinned images. All three cases passed (86.722
seconds): successful output returned exit 0; failed output retained exit 7;
running cancellation returned `cancelled` with exit 143. Each case verified
stdout/stderr, a new checkpoint, confirmed cleanup and exact-request replay
without a second container start. The guest also checked absence of selected
host credential environment names, host paths and Docker socket, and the fixed
read-only `.git` mask. No model call or native UI journey was involved.

The local Standard Code image was
`sha256:5f5fea90318d0b0a0e4bbbe66e167e59851158d96557b5b5993792b64ff5b7c1`;
the lifecycle fixture was
`sha256:a7036de5fb3b5d40324f877d09b3c8c62d56ea9928dd697238785a97d41a9edd`.
No image pull or rebuild was required. Comparing identical inventory scopes
before and after shows the same nine container IDs and 29 listed image IDs,
with no additions or removals. An intermediate `image ls --all` listing used a
different scope and is retained separately, outside that comparison.

These Windows runs do not include the three write/handoff tests guarded by
`!windows`, and do not establish real macOS Docker or SBX execution. Detailed
commands and raw evidence are retained locally in
`build/sandbox-backend-selection/docker-real-20261010/`.
