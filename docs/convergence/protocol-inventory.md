# Schema and protocol inventory / Schema 与协议清单

- Inventory version: `protocol-inventory.v1`
- Evidence baseline: `main@0751fadbe74feb7640750e4944643e14c41acaa2`
- Current database schema: v132
- Authority: [ADR 0135](../adr/0135-pre-1-0-product-convergence.md)

This inventory classifies compatibility *families*, not every `*.vN` token. A raw
scan also sees tests, golden vectors, policy labels, operation hash separators, and
fixture-only versions. Counts are discovery input and never authorize deletion.

## Freeze classes

| Class | Definition | Change and retirement rule |
| --- | --- | --- |
| `external durable` | Read or written outside one process/database lifetime, exported to users, or consumed by separately versioned clients/extensions | Versioned additive change; old fixtures; dual-read before write-new; documented deprecation; reader removal only after every supported stored/exported value has a migration or replacement path |
| `internal durable` | SQLite state that proves audit, authority, exactly-once, recovery, ownership, or cleanup | Append-only migrations and invariant tests; no cosmetic rewrite; a newer projection cannot replace the source ledger; rollback uses an exact backup or a separately proven reverse migration |
| `projection` | Derived read/presentation state whose authoritative source is identified and retained | May be rebuilt or replaced only from its named source, with cursor/order/redaction compatibility and unknown-version failure tests; user-authored content is never called a projection merely to make deletion easier |
| `ephemeral` | Process-local negotiation, stream, cache, or runtime handle with no restart/storage promise | Can change in lockstep, but cross-process parsers still need version/limit tests; must not be persisted accidentally or treated as authority after restart |

Some features contain more than one class. For example, a browser CDP request is
ephemeral while its redacted execution receipt is internal durable. Classify the
exact object, not the package name.

## External durable families

| Family | Source of truth / examples | Compatibility commitment |
| --- | --- | --- |
| HTTP/OpenAPI v1 | `docs/openapi.json`, `internal/httpapi`, `/api/v1/*` | Generated schema and route tests are authoritative. Add fields/routes compatibly; never reinterpret an existing authority field. Unknown enum/protocol versions fail closed. |
| CLI contract | `cmd/cyberagent`, `internal/app`, exit/error mapping | `cyberagent`, command names used by automation, flags, machine-readable output, and exit categories require aliases or an announced retirement window. Human prose may converge first. |
| Project configuration and instruction discovery | `.prayu/config.yaml`, `.prayu/instructions.md`, `.prayu/rules/**` | Path/precedence/fingerprint changes require dual-read, deterministic conflict rejection, ignore parity, and old-project fixtures. |
| Skill and Plugin packages | `skill.v1`, signed catalog/package manifests, Plugin/Hook manifests | Packages are untrusted external input. New writers use a new version; old readers remain bounded and inert. Signature/provenance meaning cannot be weakened by an alias. |
| Analyzer interchange | `analyzer_protocol.v1/v2`, descriptor/result/provenance formats and golden vectors | Go/Rust/third-party boundary. Shared vectors, bounded parsers, unknown-version rejection, and release allowlists govern compatibility. Test/conformance IDs remain explicitly distinguishable from production formats. |
| MCP interchange | MCP client/server negotiation plus Traverse Board extension DTOs | Upstream MCP compatibility and local Go policy both apply. Negotiated capability metadata never grants local authority. |
| Exported Thread, report, SARIF, evidence, receipt, and handoff documents | `thread_export.v1`, SARIF/report renderers, verification and batch-delivery exports | Export is user data and audit evidence. Additive writer changes require old-reader/new-reader fixtures or a new format; redaction and content hashes remain stable. |
| Release/package identities | Windows/macOS package identity, release manifests, checksums, SBOM | Historical artifacts are immutable. Display filenames may change only under their packaging ADR; install identity and data/credential lookup need upgrade/downgrade evidence. |

## Internal durable families

| Family | Durable responsibility | Why it remains strongly frozen |
| --- | --- | --- |
| Schema migration history v1-v132 | `schema_migrations`, `internal/store/sqlite.go`, `migrations.go`, `migration_v*.go` | Names/checksums prove the exact path applied to a user database. Legacy accepted checksums, FK checks, and contiguous versions are recovery inputs, not source clutter. |
| Thread/Mission/Run/Session/event graph | `threads`, `thread_runs`, Runs, Sessions, messages, run/thread events | Preserves user history, Run succession, immutable ordering, and authority-free continuity. Cosmetic vocabulary changes cannot rewrite identities. |
| Authority and approval ledgers | Scope, permission/profile/interaction snapshots, approvals/grants, leases, credentials references | Proves who could do what at an exact revision/generation. A UI projection or current config cannot recreate historical authority. |
| Tool and mutation ledgers | structured tool operations, file edit apply, evidence, verification, checkpoints, Git operations | Provides write-ahead intent, replay conflict detection, result binding, and cleanup/recovery evidence. |
| Process/runtime lifecycle | command runtime jobs, controlled/host/debug execution, analyzer start/execution | Binds executable/spec hashes, owner generation, cancellation, process-tree reaping, output bounds, and terminal state. |
| Sandbox and Docker lifecycle | admission, readiness, lifecycle intents/actions, container ownership, I/O and cleanup receipts | Container ID, image/config hashes, network mode, lease and cleanup order are safety facts. Generic operation code cannot flatten these triggers. |
| Browser/CDP and UI-evidence ledgers | launch/readiness review, runtime lifecycle, source-bound captures | Preserves permission ceilings, containment proof, source commit/recipe, artifact hashes, and redaction status. |
| Agent, scheduling, wake, batch-delivery, and extension ledgers | child/dependency graphs, schedules, mailboxes, worktree ownership, merge review, installation records | Preserves bounded concurrency, ownership, restart recovery, supply-chain provenance, and exact delivery decisions. |

## Projection families

| Family | Authoritative source | Rebuild/replacement boundary |
| --- | --- | --- |
| `thread_transcript.v1` | Immutable Thread/Run/Session messages, events, model/tool items, approvals, verification, checkpoint and delivery records | Keyset order, durable replacement identity, redaction, and Run boundaries must remain stable. It can be recomputed; source rows cannot be deleted. |
| `run_capability_readiness.v1` and bootstrap capability views | Current persisted Run selections, leases, startup gates, backend proof and Go policy | Informational only (`capability_grant=false`). A new view may replace it, but cannot authorize execution or hide an unavailable backend. |
| public/live activity and event polling views | Durable events and redacted lifecycle records | Cursor/high-water behavior and redaction are contracts. SSE/poll transports may differ while the durable event source remains. |
| operation receipt history | Domain operation rows and receipts | Content-free user guidance. It cannot become the source of truth for transaction success, authority, or cleanup. |
| external Skill, finding/report, code journey, and review summaries | Content-addressed installation/evidence/Git/verification sources | Each projection must name its source and invalidation rule. User annotations and accepted review decisions remain durable source data. |

## Ephemeral families

| Family | Lifetime | Guardrail |
| --- | --- | --- |
| LSP JSON-RPC process/session and process-local capability inventory | One qualified language-server process | Executable/config identity is revalidated; URIs are rebound to Workspace; restart does not inherit a handle or authority. |
| Docker attach/stdin transport and process-local writer registry | Exact live admission/container in one process generation | Durable admission/lifecycle rows are rechecked before attach. Restart closes stdin rather than adopting an unverifiable writer. |
| Browser CDP connection/request handles | One admitted browser runtime/target generation | Permission, target, URL and bounds are checked per request; raw handles are not persisted or replayed. |
| Provider streaming deltas and pending React cards | One model response before durable reconciliation | Private thinking/secrets are not projected. Temporary UI state is replaced only by matching durable item identity. |
| In-memory access/control tokens and renderer bootstrap secrets | One process lifetime | Never logged or stored; restart rotates them. Metadata that says a token exists is not the token. |

## Schema policy

1. **Keep the legacy path.** Existing databases continue through the contiguous
   migration chain. Migration names, checksums, accepted legacy checksums, triggers,
   and old fixtures remain review boundaries.
2. **Permit a clean-install baseline, not a squash.** [#164](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/164)
   may create the latest schema directly only for a proven empty database. It must
   show schema/invariant equivalence with representative legacy upgrades and must not
   change the identity of an existing database.
3. **Separate security facts from UI projections.** Audit, receipt, authority,
   ownership, recovery, and cleanup ledgers receive the strongest internal-durable
   protection. A rebuildable list/filter/cache receives projection rules instead.
4. **No destructive cosmetic migration.** Table, column, route, event, and protocol
   names are not renamed only to match current product vocabulary.

## Version evolution

For a durable v1-to-v2 change:

1. register owner, class, readers, writers, persisted locations, old fixtures, and
   retirement gate;
2. ship a bounded v2 reader beside the v1 reader; unknown versions fail closed;
3. update writers to v2 only after all current readers understand v2;
4. migrate durable v1 values under backup/transaction control, or retain v1 values
   indefinitely when migration has no user benefit;
5. keep v1 fixture coverage while any supported database, export, package, or client
   can contain v1; and
6. remove a v1 reader only through a new ADR that proves no supported source remains,
   documents rollback/export, and respects the Surface deprecation window.

`write-new` never means “ignore old rows,” and `dual-read` never means “guess which
authority applies.” Conflicting representations fail closed.

## Deferred decisions

- No migration deletion, renumbering, or user-database squash is approved.
- No claim is made that every `*.v1` token deserves permanent external compatibility.
- No protocol reader is approved for removal by this inventory.
- A database major-version or public API v2 strategy is deferred until the clean
  baseline and protocol registry work provide evidence.
