# Frontend permission and capability alignment (#287)

## Problem and implementation

The backend readiness projection returns Ask / Auto / Full, while the frontend
validator required five retired writable choices. A valid HTTP 200 response was
therefore rejected before execution settings and review checks could render.
The validator and current fixtures now use the canonical three-choice sequence;
missing, duplicate, unknown, reordered and legacy readiness groups are rejected.
Historical Run/Thread permission snapshots retain their existing read support.

`TestRunCapabilityReadinessHTTPCurrentFixtureMatchesGoProjection` creates an
isolated SQLite Run and obtains the response through the real Go HTTP handler.
Only generated request and Run identities are normalized. Every public field is
compared with the committed current fixture during normal Go tests. APIClient
tests parse that same fixture, and optionally the freshly exported response via
`TRAVERSE_TEST_READINESS_OUTPUT`. The original dated five-choice capture remains
unchanged as a negative current-contract specimen.

Execution settings, Debug activation and CDP descriptions distinguish approval
preference from runtime capability. Direct URL fetch status uses the stored
network Scope, including a historical `public_https` target when actually present;
Full or old Debug values do not manufacture a network grant.

Task review now has a first-level **更多交付工具** entry for batch delivery,
advanced Git, GitHub review and UI evidence. Each existing panel loads on demand
with the selected Run identity. Exact Git/GitHub review results survive a visit to
that Run's approval or delivery checks; changing Run clears retained review.
A missing selected historical Run fails closed instead of selecting the current
Run. No execution endpoint or startup default changed.

UI evidence distinguishes missing control credentials from disabled UI evidence,
Run execution and browser-CDP capabilities. An unavailable history reader keeps
history unknown. Batch validation explains its independent startup requirements.
These instructions expose existing configuration paths; they do not provide new
runtime activation APIs or bypass backend checks.

## Verification and limits

- Full frontend suite: 175 files / 1,676 tests passed before the final UI-reader
  404 cases and CDP wording follow-up. The affected final slice passed 23 tests,
  including six new UI-reader error/history cases. TypeScript, production build
  and generated API checks passed; the existing large-chunk build warning remains.
- The Go HTTP/application readiness tests passed. A fresh Go export was also
  consumed by the real TypeScript APIClient. Fixed declared Local adapter facts
  make the fixture portable; this is not evidence of a real OS sandbox launch.
- The complete HTTP API package passed locally, along with its vet check and the
  release/documentation contract packages.
- Read-only cross-checks covered exact network scope, live Full requirements,
  Run/Thread isolation and retention of precise approval targets.
- Chromium used a separate loopback Go API, fresh application home and local
  fixture workspace. The acceptance Run was seeded through `RunService` without
  a model call. Its actual three-choice HTTP response rendered execution settings;
  the four tool entries and disabled UI-evidence reader state were inspected at
  desktop width. The tool selector was also checked at a 390-pixel viewport.

No external model, GitHub review write, repository mutation, host validation,
managed browser startup or native Desktop restart was performed during the
browser check. It verifies entry points and current response consumption, not a
complete delivery or native-runtime acceptance run. Local screenshots and logs
remain in ignored `output/playwright/` and `build/permission-alignment/`.
