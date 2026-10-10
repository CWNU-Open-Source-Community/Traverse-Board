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
The follow-up wires the packaged static-MCP helper and the version-scoped local
daemon lifecycle contract into production SBX execution. Current support is
Windows with sbx 0.47.0 / daemon API 0.38.0; see
[execution environments](../sandbox-environments.md) and
[ADR 0172](../adr/0172-sbx-owned-namespace-execution.md).

## Production SBX follow-up

The application now dispatches its zero-tool stdio helper before ordinary CLI or
Desktop startup. Preparation verifies its actual handshake, executable hash,
fixed argument and complete stored registration, including working directory and
empty credential/environment overrides. Creation selects only that static set.
The guest checks effective credential modes and SSH socket absence before
entering the explicit, cleared command environment.

All cooperating product writers share an OS-account namespace lock, independent
of installation and journal location. Each operation creates a fresh random
name, persists the returned UUID and exact mounts, and rechecks them before exec.
Cleanup sends the UUID as a server-side `expected_id` condition and confirms
removal before recording a terminal Job. Host administrators remain trusted;
the local exec endpoint itself still selects by name.

The final real wrong-ID and same-name replacement test passed in 13.62 seconds. Both
incorrect deletion requests preserved the current VM; both correct requests
removed their exact VM. Its guest guard accepted `none` and rejected a synthetic
`apikey` credential mode with exit 125 before payload dispatch. Evidence is in
`build/sandbox-backend-selection/sbx-product-live/conditional-removal-1055017084/result.json`.

Real application acceptance passed in 115.11 seconds using the production
backend, SQLite store, Git Drydock, supervisor prepared-call ledger, Run lease
and Command Runtime service. Success returned 0, the failing guest returned 7,
and explicit cancellation retained the cancelled state with actual CLI exit 1.
Each case retained exact Chinese stdout/stderr (23 bytes each), a conditional
whole-VM removal receipt, the Run's after checkpoint, and a terminal replay
without a second dispatch. The cancellation case also observed a stopped
detached-child heartbeat and no delayed write for 21 seconds after cleanup.
Results are in `build/sandbox-backend-selection/sbx-product-live/sbx-product-587455417/`
and `application-acceptance-attempt6.log` in its parent directory.

Repeated CLI startup had exhausted the shared eight-second
readiness budget. Concurrent calls produced five/ten-second waits, while serial
calls also sometimes took five seconds; the complete CLI delay cause remains
unproven. Inventory and both isolation settings now use the fixed local daemon
API, and the complete stored MCP registration replaces the CLI's partial inspect
view. Preparation reuses a verified registration directly and invokes `mcp add`
only for a confirmed missing record. Successful CLI version checks are reused only while a freshly computed
executable hash still matches. Mutable settings, inventory and helper metadata
remain fresh. Actual readiness then passed in 0.761 and 0.262 seconds.

The adapter uses the documented telemetry opt-out and the accepted v0.47
`DOCKER_CI=true` behavior to skip update checks and interactive diagnostics
consent. These do not authorize uploads; caller template overrides stay filtered.
Short CLI calls share a cancellable process-wide gate. The observation budget
and 30-second evidence lifetime remain unchanged.

Fixture validation corrected a short operation key, the public Code/Plan-to-
Deliver transition, and the actual Run checkpoint cursor. The next real attempt
passed readiness and exposed a persisted Job scope trigger that still omitted
the SBX backend/profile pair. Forward migration v189 repairs that production
integration gap while retaining the other authorization predicates and published
migrations. Offline full-Job tests now exercise success, failure and cancellation
through the real SQLite boundary. Failed attempts remain recorded; those attempts
had stopped before creating a VM.

Final inspection found no SBX VMs, backend containers, networks or drift. The
ordinary Docker Engine's nine original container IDs were unchanged. The user's
daemon, authentication, pinned template cache and preserved IPC backups remain.

## Verification

- Current Windows checks: SBX/helper race regressions passed (8.708s / 1.797s),
  the production desktop build passed, and both CLI/Desktop helper executables
  passed all eight handshake, empty-catalog and malformed-input smoke cases.
  Desktop/embed package tests passed (2.776s / 2.214s), and desktop assembly race
  checks passed (5.191s). The frontend's two affected test files passed 25 tests;
  its production build passed. These local results do not replace current-head
  remote CI or live acceptance on another operating system.
- Final Linux amd64 and macOS amd64 sandbox test binaries compiled. Release
  contract checks and product end-to-end checks passed (12.862s / 3.501s).
- Schema v189 upgrade regression passed (18.34s): published v1-v188 checksums,
  historical Jobs/actors/permissions/workspaces and the remaining schema stayed
  intact; SBX prepare/replay succeeded; 13 stale, widened or relabelled SQL cases
  were rejected. Representative clean-install/historical baseline equivalence
  checks passed (67.49s), as did the existing v188 history checks (11.14s).
  Offline full application success/failure/cancellation passed (52.234s).
- The cleanup fix's [CI at `80f4d428`](https://github.com/CWNU-Open-Source-Community/Universal-Code/actions/runs/38059477832)
  completed successfully across all 22 jobs. Windows fixed-operator pagination
  passed in 53.92 seconds and `native-timeout` passed in 15.28 seconds; these
  are complete subtest durations, including fixture and cleanup work. The seven
  operator subcases, SBX regressions, native diagnostic suite and the frontend's
  193 files / 1,920 tests passed. Existing explicit Windows skips remain recorded
  in the local CI audit. The earlier intermittent timeout remains unexplained.
- The follow-up [CI at `d056a894`](https://github.com/CWNU-Open-Source-Community/Universal-Code/actions/runs/38052777174)
  completed successfully across all 22 jobs, including Windows Desktop and the
  original fixed-operator checks (pagination 44.10 seconds; operator group
  57.47 seconds). The subsequent PowerShell case took 22.93 seconds and passed.
  The native diagnostic suite passed in 2.18 seconds; its fixed-manager sample
  was unavailable with a native device error. Successful operator cases
  do not print their sampler history, so this run supplies no pagination CPU/I/O
  timeline. This verifies that head; it does not establish
  a root-cause fix for the intermittent pagination timeout described below.
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
- SBX fake-transport lifecycle and permission tests passed, including helper
  identity refusal, conditional removal and namespace ownership checks. Darwin
  arm64 sandbox/application test binaries compiled. Actual Windows VM results
  are recorded separately from these controlled tests.
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

The compatibility follow-up's [CI at `2adba231`](https://github.com/CWNU-Open-Source-Community/Universal-Code/actions/runs/38047260758)
failed the Windows `multiple-output-pages` subcase after the existing 60-second
command deadline. The native process was created and its tree was reaped;
the parent observed zero stdout/stderr bytes. Ownership checks continued and
lease renewals succeeded. One Job update completed after its context deadline
with a nil store error, but the command continued to its normal timeout. The
record leaves PowerShell startup, in-script enumeration/JSON serialization and
an output read error as separate hypotheses. The runner and application production
sources match the merged main at `accd4c47`.

The unchanged pagination case passed locally in 1.78 seconds and the original
operator-first sequence also passed (9.29 seconds). The intermittent failure
still needs a reproducible cause. A test-only sampler now records CPU/I/O activity for
the test's own child, bound by parent, creation interval and executable image.
A single worker and a nonblocking bounded queue keep the kernel queries outside
the ownership update path. Queries collect only process identity and activity
metadata; determining a specific cause needs additional evidence. Command deadlines, production
environment, output/cleanup assertions and the operator-first CI order remain
unchanged. The new diagnostic check retains the earlier test exit codes.
The final operator and diagnostic tests passed with the Windows race detector
(34.048 seconds; pagination 4.66 seconds), the restricted native runner check
passed (4.127 seconds), and the Linux amd64 application test binary compiled.
The sampler also returned valid counters for a real fixed-manager child.

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
restart encountered a stale `containerd.sock.ttrpc` reparse point. At that point
the daemon was unreachable and effective settings and authentication were
unverified. The execution tool rejected an attempted state-directory
backup/restart command with `blocked by policy`; that command did not execute.
The rejection's policy source remains unidentified. The session has full filesystem
access and `approval_policy=never`; the [Auto-review documentation](https://learn.chatgpt.com/docs/sandboxing/auto-review)
scopes that feature to interactive approval modes.

After the user explicitly requested desktop control, native File Explorer renamed
the dedicated `state/sandboxd` directory to `state/sandboxd.stale-20261010-gui`.
The directory and its original contents are retained. Before the rename, the
daemon processes were absent and the namespace's `runtimes` directory was empty.
Filesystem verification confirmed that the backup exists, its runtime count is
zero, and the original canonical directory is absent. An official
`sbx --app-name traverse-runtime daemon start --detach` invocation was then
rejected by the execution tool before starting. The user then ran that command
successfully and completed Docker device authentication. The CLI confirmed
`qiyuanqiii`; both the sandbox and template inventories were initially empty.
No settings, credentials, Docker objects or VM data were reset.
The installed Docker-signed `sbx-gui.exe` was also launched through native desktop
control, but it exposed no targetable window in either the app or window
inventory. Only the process created for that UI check was closed afterwards.

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

### Real SBX 0.47 acceptance

The dedicated namespace was initialized with `policy init deny-all`. Settings
readback after the user-started daemon confirmed SSH agent forwarding disabled
and the local MCP gateway selected. The official Linux amd64 shell template was
resolved from Docker Hub and pinned to
`docker.io/docker/sandbox-templates@sha256:bbef0c36f33daa5da4b57b7a504e8c945cdac52bcfe94141a51aee63333fc36e`.
One owned VM used `--pull missing` to prepare the namespace cache; a second
owned control VM successfully used `--pull never`. Both used two CPUs, 2 GiB,
`--skills off`, `--deny-network '**'`, one owned Git worktree and its `.git:ro`
file. The actual inventory matched both host mount arguments exactly.

The first VM's static MCP set contained only the audited zero-tool stdio probe.
Guest MCP initialization and all list methods succeeded. Its tools were the
gateway's `code-mode` and `mcp-exec`; resources, resource templates and prompts
were empty. A subsequently registered canary service remained excluded:
dynamic discovery/configuration, direct canary calls and indirect `mcp-exec`
calls failed. `code-mode` rejected the excluded canary, an empty selection, and
the gateway builtin `mcp-exec`. The control VM explicitly allowed the canary;
direct and indirect calls both succeeded and appended exactly two fixed lines
to the owned host marker. No real account secret or model request was involved.

Guest checks confirmed ordinary workspace writes reached the host, while
opening `.git` for writing, unlinking, renaming and replacing it all failed
with `EROFS`. Its hash stayed unchanged. The pointed-to host Git metadata was
unavailable. A host junction to an owned outside sentinel could neither be read
nor written from the guest, and the outside sentinel remained unchanged.

HTTP requests to the tested provider, public and host destinations returned
policy-denied 403 responses. Raw TCP connect alone succeeded to the transparent
forwarder, but an HTTP payload to an owned host canary received EOF; the host
listener recorded only its one host-side positive-control request. External raw
TLS ended with `SSLEOFError`, and external DNS resolution failed. These are the
tested routes, not a claim about every possible network protocol. `SSH_AUTH_SOCK`
was still present in the default guest environment, but its socket was absent
and `ssh-add` returned exit 2 with ENOENT. Credential modes reported `none`;
some provider variable names still held runtime sentinels. The product's
`env -i` invocation removed all six selected credential/gateway/SSH variables.

The initial real CLI exercise exposed two lifecycle limitations:

- `exec`, `stop` and `rm --force` each rejected the actual local VM UUID as
  not found. Name-based calls do not provide an atomic immutable-ID contract.
- `daemon restart` returned success, but subsequent named `stop` and `exec`
  returned backend-unavailable 500 responses. The internal Docker Unix socket
  was unreachable. `ls --json` returned an empty array although both exact-ID
  runtime metadata files still existed and the daemon logged failed SDK clients.
  This is not a valid removal receipt. The recovery adapter now retains its
  journal and returns uncertain cleanup when a previously recorded VM is merely
  absent from the first inventory.

The failed dedicated daemon was stopped through the official CLI. The execution
tool then rejected `daemon start --detach` before process creation with
`blocked by policy`. The user's subsequent PowerShell start restored the same
two sandbox UUIDs, mounts and stopped state. No reset or runtime-state deletion
was needed. This recovery distinguishes the observed failed launch from a
general claim that all sbx restarts fail; its cause remains unestablished.

After that restart, the same static-MCP negative checks passed without changing
the host marker. The allowed control VM successfully called the canary directly
and through `mcp-exec` again. Fixed guest commands returned exit 0 and exit 7
with their expected stdout and stderr. Cancelling an owned foreground CLI left
the guest's detached child alive: its heartbeat advanced from 0 to 4 after the
client exited. Official named `stop` and `rm --force` then succeeded with the
owned identity checked before each operation. This is an operator acceptance
sequence; it does not establish an atomic UUID selection contract.

Cleanup was confirmed through a successful backend scan as well as the empty
CLI inventory: both owned container IDs and network IDs were gone, all three
drift lists were empty, and both exact runtime metadata files were absent.
After 104 seconds, the guest heartbeat remained at 10 and the delayed-child
marker was absent. Both owned MCP registrations were removed after their
command and mode identities were checked. No probe helper process remained.
The original nine Docker Desktop container IDs were unchanged. The authenticated
daemon, deny-all policy, disabled SSH forwarding and pinned template cache remain
available for subsequent work; stale IPC backups were retained.

At that stage the production gate remained closed. The production helper and
conditional-removal implementation described above now replace that fixed gate.
This earlier operator exercise is retained as separate boundary evidence;
it does not turn name-based execution into atomic UUID selection.

Raw, secret-free probe logs and fixture source are retained in
`build/sandbox-backend-selection/sbx-live/run-8d2dc9c14322/`, including the
post-recovery assertions and hashes in `verified-checks-recovered.json`.

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
