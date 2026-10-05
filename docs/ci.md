# CI checks and execution time

PRs and main pushes select checks from their changed inputs. **Go control
plane** aggregates the selected Go checks, Store shards, and authority race
checks. **Full CI verification** is a separate result produced only by a complete
run; releases require that result for their exact source commit.

## What runs

| Change | Central CI | Desktop release workflow |
| --- | --- | --- |
| Documentation | Module, protocol, Surface and documentation contract checks | No PR package build |
| Frontend / OpenAPI | Frontend types, API drift, tests, build, dependency audit, Go bundle tests and desktop asset embedding | No PR package build |
| Go source or resources | Direct package tests, affected consumer tests or compilation, and relevant integration tests | No PR package build |
| Store or its dependencies | The applicable Go checks plus all eight Store shards | No PR package build |
| LSP, analyzer, browser or native execution inputs | Their corresponding real runtime / platform checks | No package build unless packaging inputs change |
| Packaging / release tooling | Native boundaries and packaging checks | Existing PR package validation |
| Go module dependencies, CI machinery or unclassified inputs | Full checks | Only if release tooling also changes |
| Nightly or manual CI | Full checks, platform matrix, packaging and Go vulnerability audit | Version tags / manual releases still use the release workflow |

The selection rules live in `scripts/ci/classify_changes.py`. Main uses the push
before/after commits, so merging a frontend change does not rerun the entire
backend matrix. Nightly CI runs at 19:23 UTC (03:23 Asia/Hong_Kong). The Actions
page's **CI → Run workflow** starts a full run for a selected branch.

An unreadable or empty change set fails selection. Failed selection, a failed
required job, cancellation, or an unexpected required-job skip cannot become a
successful aggregate result. Unclassified build inputs select full checks.

## Go impact selection

`scripts/ci/run_go_checks.py` uses `go list -json ./...` to identify package
ownership, embedded resources and the real import graph. Production imports
propagate impact; test imports add the corresponding test consumer without
turning a test-only dependency into a production dependency. A change confined
to test files or testdata stays with its package.

Directly changed packages run their complete tests. Indirect consumers also run
their tests except the four large integration packages: `application`, `app`,
`httpapi` and `desktop`. Those compile their tests and run explicitly selected
existing provider / MCP integration tests where relevant. Store runs separately
through its existing shards. Vet covers the entire affected set; subsequent
tests disable duplicate automatic vet.

This deliberately reserves the entire large integration suites for direct
changes and full CI. A direct `application` change can still take significantly
longer than a frontend or leaf-package change. A removed package or an input
with no current Go owner selects full checks, including Store and all platforms.

The native jobs retain their real platform checks. On a runtime-only change they
run desktop boundary and tagged adapter tests without producing release archives
or running the second reproducibility build. These adapter tests still build the
renderer because they consume the embedded production assets.

## Release verification

Ordinary Go, frontend, documentation and dependency changes no longer launch the
separate Desktop release workflow on every PR. Changes to actual release tools,
packaging scripts, packaging assets and branding still do.

Before a tag or manual release, run full CI for that exact commit (or use a
successful nightly run of the same commit). The release workflow checks a
successful **Full CI verification** job from the same run attempt and commit.
A successful partial run, a PR run, or a skipped full gate is not release evidence.

## Store tests

Eight Linux jobs enumerate the compiled package with `go test -list`, then run
disjoint exact-name partitions. Every Test, Example and Fuzz seed entry belongs
to one shard; each shard must produce one terminal result for every selected
root and a successful package result. Existing conditional skips are recorded.
Subtests stay with their parent. The normal uncached execution and 60 minute
package deadline remain in effect. Race checks run separately as before.

Each shard uploads its inventory, partition, raw JSON events and summary for
five days. Splitting tests reduces the longest sequential job; it does not
remove the underlying work and uses additional runners and compilation time.
The slowest shard and runner queue still determine completion time.

For a local shard:

```sh
python scripts/ci/run_store_shard.py --index 0 --count 8 --output-dir /tmp/store-shard-0
```

Use `--index 0 --count 1` for a verified full-package run. This mode omits the
name filter to avoid Windows command-line length limits, but still checks every
compiled test's terminal result. Configure Go and temporary directories for
the local development environment before running either command.

## Test database setup

Only opted-in fixture helpers reuse closed schema snapshots. Every test gets a
new independent database file, and current-schema copies still call production
`Open` to create distinct recovery identities. Historical copies are built with
real migrations; the migration under test still executes against each copy.

Production database code, raw migration oracles, fresh-open, rollback, disk-full,
identity, permission and foreign-key checks retain their original paths. The
inverse historical fixture must reject modern queue revision or attachment
history that cannot be represented in the requested old schema.

## Superseded PR runs

Central CI and PR-only release validation cancel superseded runs. Heavy jobs use
`!cancelled()` so cancellation does not start further work. Tag and manually
requested release runs retain their non-cancelling behavior.

PR/push, nightly and manual runs use separate concurrency groups, so a new PR
commit does not cancel a full nightly or manually requested run.
