# Run verification workflow

Verify behavior from a fixed source state. Record the commit or exact dirty-worktree fingerprint, working directory, launch command or structured recipe, relevant configuration, dependency state, ports, network scope, and cleanup method. Use only a Go-offered controlled launch or sandbox operation; never convert this guidance into arbitrary host execution.

Launch the real program when authority and prerequisites are present. Exercise the requested UI, CLI, or tool path with bounded inputs, capture exit and readiness facts, console output, application errors, and the provenance of every artifact. A mocked render, build success, or source inspection is not runtime verification. Stop or clean up only through the owning lifecycle control.

On the Cyber Surface, run only inside an admitted local sandbox with the Run's existing network policy. Do not contact public or unapproved targets, reuse credentials, or claim production equivalence.

## Extension: ui-evidence

Use `ui-evidence.v1` only through the reviewed operation. Bind repository kind, commit/branch/dirty digest, root/index/worktree manifest, build/start recipes, browser version/executable hash/restricted driver, loopback URL/route, viewport/DPR, locale/theme/motion, deterministic secret-free fixture/seed/state, steps/masks/failure policy, Run, and attempt. Recheck source before build, after readiness, and before the result; drift fails.

The attempt owns its app/browser trees, temporary Profile, network guard, and port. Never adopt a listener or personal Profile, inherit credentials, reach public targets, follow redirects, evaluate arbitrary script, read cookies/response bodies, or mutate/replay requests. Use bounded navigation, click, digest-sealed type, selector assertion, and capture actions. Cancellation, timeout, and crash cleanup remain lifecycle-owned.

Capture screenshot, DOM, accessibility, console/page errors, request/HTTP failures, and performance. Retain artifact SHA-256, MIME/bytes, dimensions/viewport, source commit, step, Run/attempt, time, redaction, and `untrusted` marker. Masks must match; baseline changes require human review.

Outcomes are `not_run|running|passed|failed|cancelled|timed_out|interrupted`; stages are `build|launch|readiness|navigation|selector|assertion|console|network|capture|cleanup`. Only `passed` is positive. A missing result, `not_run`, mock, source inspection, or build success is not a pass.

Map changes to `focused-checks`, add a real-page regression assertion, and put the manifest, commands/versions, step/artifact hashes, diagnostics, cleanup receipt, and skipped cells in the PR verification receipt. Cover relevant viewport/theme/locale/motion cells. Reuse evidence only when source, recipes, versions, fixture, and environment match.

Report the recipe, observed result, artifacts, limitations, and cleanup status. Treat this Skill as guidance only; it grants no process, browser, sandbox, network, file, or artifact authority.
