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
binary data) are retained. Source links, junctions and special files are rejected
as an explicit acquisition limitation; this path does not materialize them.
Case-equivalent paths are rejected to keep one inventory identity on Windows.
The loader can read contained links, but this acquisition path intentionally has
the narrower input boundary. Filesystem confinement is not a process sandbox.

## Integration and old-entry retirement

No production entry is retired by this preparatory increment. The next wiring
increment must use the **same** plugin object/install/state/audit tables and the
existing directory/Git import and `skill_read` entry points:

- Replace the unconditional generic `skills.BuildPackageFromDir` route with
  explicit format dispatch; retain old signed ZIP codecs and archived readers.
- Persist the acquired descriptor beside the retained object, rather than
  applying legacy semver/publisher/content-size constraints to portable files.
- Offer summaries only for currently enabled installation capabilities; copy
  the exact component/revision into the existing `skill_read` request.
- Recheck current Run/session/attempt and installation generation before I/O
  and before returning the result. Snapshot references themselves are not
  authorization. Revocation is not an atomic OS-level operation.
- Restore successful-read guidance using the existing tool-call ledger and the
  exact installed object, never historical stdout or a new Run/Session database.

Tests exercise real unmodified upstream fixtures through capture, serialized
descriptor, reopen, instruction activation and resource read. These establish
the storage/read seam only, not public installation or MCP interoperability.
