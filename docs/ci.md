# CI checks and execution time

The Go result keeps the name **Go control plane**. It aggregates the
general Go checks and all applicable Store shards. A failed classifier, failed
shard, cancellation, or unexpected skip cannot produce a successful Go result.

## What runs

| Change | Central CI | Desktop release workflow |
| --- | --- | --- |
| Push to main | Full checks | Runs for version tags or manual release requests |
| PR containing code, build scripts, workflows, generated contracts, or unknown paths | Full checks | Runs when its existing product-input paths match |
| PR changing only the six explicitly listed documentation files | Module, protocol, Surface and documentation release checks | README.md still triggers archive validation because it is packaged |

The exact documentation allowlist is in `scripts/ci/classify_changes.py`.
An empty, invalid, or unreadable diff is an error, never permission to skip tests.
Other documentation, including migration history and generated protocol files,
continues through full central CI. Main pushes never use the documentation shortcut.

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

Desktop release PR triggers exclude documentation that is not an archive input.
This is not cross-workflow artifact reuse: product-input changes still run the
existing real archive and reproducibility checks.
