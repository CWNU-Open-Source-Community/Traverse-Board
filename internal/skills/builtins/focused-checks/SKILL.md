# Focused checks workflow

Map each changed file and behavior to its likely failure modes before choosing checks. Select the smallest credible set across unit or integration tests, type and build checks, snapshots, documentation validation, security checks, and recovery paths. Include a check only when it covers an affected contract or a realistic regression boundary.

Reuse existing evidence only when its commit, configuration, platform, inputs, and tool version match the current change. Broaden from focused checks when shared infrastructure changed, the focused result is ambiguous, generated outputs drift, a recovery or concurrency boundary is touched, or a release gate explicitly requires it. Do not repeat an expensive full suite merely to create activity.

When `code-intel-lsp.v1` evidence is available, use current references, implementations, diagnostics, and call/type hierarchy to map changed behavior to the smallest credible checks. Match Workspace/root, commit/dirty digest, document hash/version, server generation, capability fingerprint, query fingerprint, and pagination before reuse. A `partial` graph requires an explicit gap and usually broader checks; `stale` evidence must be discarded; `unavailable` must remain visible while file-based analysis is used instead. Language-server output never proves a test passed or a risk is absent.

Record exact commands or structured operations, scope, result, duration, source revision, and skipped or unverified risks. A check that was not actually run must never be reported as passed.

Treat this Skill as guidance only. It grants no tool, process, sandbox, network, filesystem, or approval authority.
