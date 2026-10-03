# Portable snapshot acquisition

This increment prepares the existing plugin installation/object flow to retain
original Agent Skills and Agent Plugins. It does not add an installer, activate
a plugin, expose a new tool, or switch the existing import route.

`CapturePortableDirectory` freezes accepted file bytes in a deterministic ZIP,
retains the selected root basename, then calls `agentpackages.OpenDirectory`
against the frozen copy. It produces an acquisition descriptor containing the
archive revision, exact inventory, original author version (possibly absent),
component references, summaries and diagnostics. This descriptor is host
evidence, not a fabricated author manifest. Source references remain supplied
provenance; capture does not prove that a local directory matches a remote Git
commit. An explicit old format returns `agentpackages.ErrLegacyFormat` for the
existing codec rather than being converted into the new format.

`OpenPortableSnapshot` requires the saved descriptor and exact object bytes. It
verifies the acquired revision, inventory, and descriptions, and provides a
per-request reader. Resource digests come only from the saved inventory.
`Read` returns exact bytes and a `toolcontract.ContentRef`; output encoding and
redaction remain the application caller's responsibility. Each reader must be
closed. Its temporary extraction is for reading, with no executable file modes
activated. MCP execution will need its separately authorized runtime path.

Host acquisition limits reuse the existing plugin object store's 4 MiB archive,
8 MiB decoded content and 256 KiB metadata limits, with at most 1024 entries.
Ordinary files and directories (including empty files/directories, scripts and
binary data) are retained. Root `.git` administration, including a worktree pointer
file, is excluded before traversal. Source links, junctions and special files are rejected
as an explicit acquisition limitation; this path does not materialize them.
Case-equivalent paths are rejected to keep one inventory identity on Windows.
The loader can read contained links, but this acquisition path intentionally has
the narrower input boundary. Filesystem confinement is not a process sandbox.

## Integration and old-entry retirement

The directory/Git source import entry now selects an explicit format. Its former
unconditional `skills.BuildPackageFromDir` step remains only for legacy Skill
directories; existing signed ZIP codecs, old rows and recovery readers remain.
Native packages are never rebuilt as a fabricated legacy author manifest.

`plugin-installation.v2` stores the acquired descriptor in the existing
`plugin_installations.manifest_json` slot and exact bytes in `plugin_objects`.
Migration 177 extends that table while retaining the v1 JSON, signatures, object
bytes, transition foreign keys and historical migration checksums. The host
revision is not an author version; absent author/version remain absent in the
original source. Source provenance and the selected surface are immutable.
Import confirmation approves/enables instructions only; scripts and MCP launches
remain inert. Current disabled/revoked/quarantined/rolled-back records are never
renewed by an import retry. The operation key is durably bound to exact input.
The existing one-object provenance binding rejects a second installation of the
same archive under a different operation/source/surface instead of aliasing it.

The existing `skill_read` and Supervisor path now offer enabled native summaries
and read by installation/package/component/revision/generation. Resource paths
use only the acquired inventory. Ordinary text, empty and binary resources have
explicit UTF-8/base64 output, original and delivered digests, existing redaction,
a 64 KiB read bound and the existing result envelope ceiling. The reader checks
current Run/session/root attempt/lease, mode and installation state before I/O
and before returning bytes. These checkpoints are not atomic OS revocation.

The existing successful tool-call ledger retains activation references. Resource
reads do not displace instruction activation. Bundled guidance keeps its existing
body-restoration budget; native references survive restart/compaction and require
an exact re-read when needed. Large native bodies are not silently truncated into
the old bundled context budget. Historical stdout never restores activation.
The shared eight-activation bound includes bundled and native reads.

Installed discovery shows at most 32 summaries initially. The previous 33rd-Skill
error, which aborted the whole Supervisor turn, is retired. A visible count and
`skill_read` request lead to metadata pages with exact component references.
Continuation binds the installation inventory revision; an enablement or revision
change requires restarting discovery. Summaries are not an authorization whitelist:
content reads always check the current installation, scope and exact pin. Metadata
pages do not consume activation slots or restore instruction bodies. Descriptions
are bounded independently from the original content. The existing 1000-installation
scan bound emits an explicit partial-inventory diagnostic with package-specific
operator inspection commands; exact current references remain readable. This does
not add content paging: each instruction or resource read still has the 64 KiB bound.

Validation covers two unchanged upstream fixtures through the real source import,
catalog and Supervisor tool path, plus retained-object restart/pending-receipt
recovery, binary/empty resources, source/object drift and disable/revoke/cancel
checks. CLI import and existing plugin review/list paths remain the operator
entry. HTTP extension inventory exposes native identity, optional author version,
revision and selected surface for those same review operations.

This increment retires the generic native-to-legacy repack step, the bundled-only
read identity/catalog assumption, and name-only ledger deduplication. It does not
retire legacy archive readers, create another installation or Run/Session database,
execute Skill scripts or connect an MCP service. Git import currently selects the
repository root only; callers with monorepo Skills must select/materialize the
actual package directory. Native URL archives, GUI import UX, MCP/process production
wiring and the public ask/auto/full writer cutover remain later increments.
