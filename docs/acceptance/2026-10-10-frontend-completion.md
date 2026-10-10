# Frontend completion record

Baseline: main `010922ba`, after the neutral-glass change in #295.
This record separates implementation progress from observed acceptance.

## Implementation lanes

| Area | Required outcome | Current state |
| --- | --- | --- |
| Plugin | Version history, reviewed rollback and publisher trust revocation through shared Go contracts | Implemented; key-wide revocation and exact trust generation checked inside the version-switch transaction |
| Hooks | Scope-aware declarations, actual invocation and rejection diagnostics | Implemented; historic audit rows retain unknown rejection status |
| Docker | Environment readiness and a guided path into supported project execution | Implemented; bounded daemon/image check and current-task Standard Code setup |
| UI Evidence | Select a launch recipe and checks, review the exact action, execute and inspect saved evidence | Implemented; reviewed request recovery and hash-verified screenshot previews |
| Batch | Prepare isolated children, execute bounded work, inspect results, revise from feedback and return for review/merge | Implemented; an accounted Specialist turn proposes edits for up to four small owned files, followed by actual Go edit/commit/check/receipt operations |
| macOS | Separate Windows Local readiness from supported Docker runtime assembly | Implemented; non-Windows assembly tests and Darwin test binaries compile |

## Acceptance plan and evidence

- Use isolated application data and disposable Git projects for all new journeys.
- Reuse the configured DeepSeek connection through the Go credential store for
  bounded real-model acceptance. Preflight queried only provider metadata and
  credential presence; no secret was printed or exported.
- Exercise creation, file read/edit, approvals, checks, delivery and continuation
  through production components. Record request/receipt identities and actual
  output. Include restart, connection loss and draft preservation.
- Exercise Plugin/Hooks, Docker/UI Evidence and Batch workflows after integration.
  Separate deterministic contract fixtures from real remote and native evidence.
- Check keyboard focus, narrow layouts, all themes and accessibility media modes
  against the final production assets. Save original browser captures.
- Run the affected Go/frontend suites and exact-head GitHub checks. The macOS
  runner can verify supported native contracts and packaging; actual interactive
  Mac availability is being established separately.

Local evidence is retained in ignored `build/frontend-completion/`. Results and
remaining external conditions will replace the in-progress entries as each
journey completes.

## Integrated checks observed so far

- Go 1.26.9: affected App, Desktop, HTTP/OpenAPI, Store and Plugin checks passed.
  The Hooks package selected no tests with that filter; it is not counted as a
  test pass. New Batch model tests exercise actual SQLite and Git with a
  deterministic provider, including a delivered edit, feedback and a second
  accounted delivery while retaining the original receipt and source checkout.
- Final integrated frontend suite: 191 files and 1,876 tests passed, including the
  signed publisher alias follow-up. Production build and generated API/transcript
  keys passed; existing React test warnings and large bundle warnings remain.
- Batch bridge regression suite passed in 37.981s: real CRLF/BOM source edits,
  complete long briefs, pending-context overflow, escaped/assembled/literal
  secret rejection before writes, delivery/rework accounting and replay.
- Full Hooks, protocol registry and surface governance packages passed.
- The broader Batch delivery/workbench and Specialist accounting regression run
  passed in 126.289s. Vet passed for Application, HTTP, Desktop, Plugins, Hooks
  and Store.
- Protocol registry generation/check passed with all new identifiers registered.
- Plugin lane browser checks used real Go/SQLite lifecycle state and original
  browser captures, including rollback/revocation and 390px layout. These are
  separate from a full real-model project journey.

## Native and real-account conditions

- A read-only preflight confirmed an existing DeepSeek credential through Windows
  Credential Manager. No key was printed or copied into a browser. A disposable
  committed pagination project and independent application data directory are
  prepared for a bounded model journey.
- Automatic approval rejected starting the isolated loopback validation service
  twice, including a retry with reduced capabilities. No detailed reason was
  returned. Explicit user confirmation is pending; no real model call has been
  dispatched in this completion pass.
- Docker Desktop could not start its Secrets Engine because the existing
  `docker-secrets-engine/engine.sock` was inaccessible. A reversible rename also
  failed. The test-owned startup processes were stopped; no Docker data was
  deleted or reset. Real Docker execution remains unverified on this host.
- This host is Windows. Darwin cross-compilation and controlled Docker assembly
  tests do not establish interactive macOS or real macOS Docker acceptance.
- Read-only inspection also confirms that this process is not elevated. A new
  production WFP network probe requires an elevated token; no fake readiness,
  elevation workaround or probe was used. The native UI Evidence acceptance
  helper compiles and remains prepared for an authorized native run.

## First PR CI and packaged candidate

[PR #296](https://github.com/CWNU-Open-Source-Community/Universal-Code/pull/296)
uses Qiyuanqiii for both its commits and author. The first
[CI run](https://github.com/CWNU-Open-Source-Community/Universal-Code/actions/runs/38024147163)
targets `e900bbf6`; its merge checkout `be9d6fcd` has the same tree hash.
Observed successes include the frontend suite, authority race checks, dependency
audit, all three real LSP platforms and the real Edge matrix. macOS Keychain and
POSIX process-tree tests passed. That macOS job then failed two new Docker
assembly fixtures because its temporary home used the `/var` path alias. Store
shard 7 found the missing v187 schema-history documentation row. The follow-up
canonicalizes only test homes and completes the history row; final rerun results
are available from the PR checks.

The Edge CI artifact includes actual light desktop and dark/reduced-motion 390px
captures, a deliberately detected regression and successful browser/process/profile
cleanup. All three image lengths and SHA-256 hashes were verified against the
original receipt. This matrix uses a synthetic page and the real Edge driver;
it does not establish the rebuilt workbench's complete real-account journey.

A clean `e900bbf6` Windows candidate built with Go 1.26.9 and Node 24.16.0 passed
Desktop/WebUI/Wails/assets tests, 15 automated compatibility checks and repeated
EXE/ZIP hash comparison. The packaged application was not launched. Build metadata,
SBOM, notices and checksums remain in the ignored evidence directory; later clean
candidate builds retain their own revision and hashes. Manual WebView2/display and
recovery checks remain separate acceptance items.
