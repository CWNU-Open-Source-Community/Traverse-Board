# ADR 0139: Transactional clean-install consolidated schema baseline

- Status: Accepted
- Date: 2026-08-26
- Scope: GitHub issue #164; SQLite initialization only
- Parent policy: [ADR 0135](0135-pre-1-0-product-convergence.md)
- Schema evidence: v136, generated from the complete v1-to-v136 plan

## Context

The append-only migration chain is executable compatibility evidence for audit,
authority snapshots, receipts, recovery, cleanup, and exactly-once operations. It
must remain available for user databases, but replaying every historical schema
construction and table rebuild is unnecessary for a new database with no identity or
data. ADR 0135 approved a consolidated latest-schema baseline only after exact empty
database detection and representative equivalence fixtures existed.

## Decision

Use a generated schema-only snapshot for a proven-empty SQLite main schema. The Store
reserves the single connection and begins the existing `_txlock=immediate` transaction
before checking `main.sqlite_schema`. Eligibility requires zero non-`sqlite_%`
objects, including absence of `schema_migrations`. Any object or uncertainty selects
the legacy path.

The generated artifact is derived from a temporary database built exclusively through
the full historical plan. Generation fails if any application table contains a row.
It records the latest version, SQL digest, final schema digest, and full migration-plan
digest. Runtime verifies those proofs before applying the artifact and then, in the
same transaction:

1. creates the consolidated tables, explicit indexes, triggers, and views;
2. inserts every original migration version, name, and canonical checksum;
3. re-validates plan contiguity and the complete ledger;
4. re-computes the final ordered `sqlite_schema` digest;
5. runs `PRAGMA foreign_key_check`; and
6. commits once.

There is no baseline completion flag, new migration number, or alternative database
identity. The complete canonical ledger plus equivalent schema is the completion
state. Existing databases still call `applyMigrations`; accepted v30/v97 legacy
checksums remain exact-version exceptions and are never rewritten.

## Failure and concurrency semantics

The emptiness proof and construction share one immediate write transaction. Concurrent
openers serialize: the winner commits the complete baseline, and later openers see a
non-empty schema and validate it through the historical path. SQL errors, cancellation,
simulated disk-full errors, and pre-commit process exits roll back schema and ledger
together. A stale generated artifact is not executed; safe historical replay remains
available.

Commit errors are returned rather than guessed. On restart, SQLite recovery exposes
either the committed complete schema or the rolled-back empty schema. The next opener
then validates the ledger or performs a new clean-install attempt respectively.

## Equivalence and compatibility evidence

Automated fixtures compare the ordered final SQLite schema digest after:

- consolidated clean install;
- v1 to latest upgrade;
- v97 to latest upgrade;
- v128 to latest upgrade; and
- v132 to latest upgrade.

Because the digest covers every table/view/index/trigger SQL definition, the comparison
includes declared foreign keys and CHECK constraints; every fixture also runs
`foreign_key_check` and validates all migration names/checksums. A populated v132
browser-runtime fixture separately preserves immutable Run audit events, the closed
execution-authority snapshot, exactly-once launch preparation, success and recovery
receipts, process/network/profile cleanup facts, and the restart recovery projection
through the v133-v136 legacy upgrade.

On a Windows Intel Core Ultra 9 275HX development host, the original v135 artifact's
five-iteration non-gating benchmarks measured approximately 171 ms/op for consolidated
construction versus 2.275 s/op for historical v1-to-v135 replay (about 13.3x faster).
This observation is not an admission threshold and cannot override any equivalence or
failure test.

## Rollback and recovery

A baseline-created database is readable by an older binary only when that binary
already supports the same latest version and identical historical plan. The regenerated
v136 artifact is schema- and ledger-equivalent to historical v1-to-v136 replay; a v135
binary must reject that newer database and requires the offline pre-upgrade backup.

Operators must take an offline database backup before a version-changing upgrade.
Recovery never deletes a non-empty database or edits its ledger. Detailed steps are in
the [SQLite baseline runbook](../schema-baseline.md).

## Rejected alternatives

- **Delete or squash v1-v134:** rejected because it removes compatibility and recovery
  evidence and prevents exact old-database validation.
- **Treat a zero-byte filename as empty proof:** rejected because file metadata does
  not prove SQLite schema or ownership state and races with another opener.
- **Create a new baseline migration row:** rejected because it invents a second history
  identity and breaks same-version rollback.
- **Replay all migration statements inside one large transaction and call it a
  baseline:** rejected because it retains the historical construction cost and does
  not provide an independent final-schema artifact.
- **Fall back after a partially committed baseline:** rejected. Construction is atomic;
  an ambiguous commit is an error and must be resolved by SQLite recovery on restart.

## Non-goals

- No business schema, authority, reader retirement, data rewrite, seed change, or
  migration renumbering.
- No conversion of existing profiles to a different history.
- No automatic database deletion/reset and no claim that performance proves safety.
