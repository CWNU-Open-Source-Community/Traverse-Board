# Portable package loading

`agentpackages` reads [Agent Skills](https://agentskills.io/specification) and
[Agent Plugins 1.0.0](https://agent-plugins.org/specification) as data. It uses
the shared contracts introduced by `b6275987754eca32fe7f47309c4d185aaa2bab28`.
It does not install packages, grant permissions, expand launch variables, start
MCP, run scripts, or create Run/Session state.

## Host integration

1. The existing import service acquires a pinned, immutable source snapshot and
   supplies its directory, host package/installation identity and `SourceRef` to
   `OpenDirectory`. Preserve the selected directory basename for a standalone
   Skill. A source ref is provenance supplied by the host, not proof that the
   supplied directory matches an upstream revision.
2. `DetectDirectory` distinguishes portable packages from explicit old
   `skill.v1` / `plugin.v1` markers. `OpenDirectory` returns `ErrLegacyFormat` for
   those old formats. The existing codecs, signatures, trust checks and archived
   object readers remain responsible for them. An explicit portable `$schema`
   never falls back to legacy parsing when its validation fails.
3. `Format`, `Name`, `Version`, `Source` and `Manifest` describe provenance.
   Author version is optional, and is not an immutable revision or MCP protocol
   version. `Manifest` is a source reference; `Read` is a Skill content reader,
   not a raw MCP configuration or arbitrary package-file read interface.
4. `Skills` returns `toolcontract.ComponentRef` values; `SkillSummary` returns
   name/description. `Frontmatter` returns a detached copy of original YAML
   delimiters, metadata, encoding marker and line endings. `Instructions`
   supplies the exact original SKILL.md content reference for activation.
5. `Resource` constructs a content reference using a digest from the host's
   acquired snapshot inventory. It performs no file read. `Read` checks component
   membership, path, limit and digest, then returns exact bytes, including empty
   and binary files. The caller owns permission checks and decides when untrusted
   instructions or resource content may enter model context. Resource digests
   must not be accepted from model/plugin arguments as host authority.
6. `Launches` returns deep copies of unexpanded `toolcontract.LaunchDeclaration`.
   `ServerSource` retains the original server key and source document digest.
   B alone resolves commands, placeholders, cwd and environment using C's trusted
   `LaunchContext`; B/C own runtime authorization and frozen launch fingerprints.
   Literal headers/env/query values may be secrets. Never log their subfields.
7. Close the package handle when finished. The host maintains source inventory
   and installation lifetime. This package adds no persistent store.

All component/source/content/launch/diagnostic interchange values come from
`internal/toolcontract`; `Package` is only an opaque loader handle. Component IDs
are the component kind plus SHA-256 of its exact local key, under the host's
package identity. They remain stable across revisions; content digests change.

## Loading and failure boundaries

Discovery reads each SKILL.md once to validate metadata and compute its digest;
it does not retain the body, load resources, or inject the body into model
context. Activation returns the original complete file on demand. Resources,
assets, scripts and additional directories stay byte-for-byte in the source.
Unknown ecosystem-specific frontmatter is not granted native client semantics.

Invalid root plugin metadata is fatal. An invalid `skills` location or `mcp.json`
disables only that component type. Invalid individual Skills or MCP entries are
reported separately; valid siblings remain available. Unknown plugin top-level
fields are reported and ignored. Unimplemented extension namespace values are
ignored. Valid portable SSE is reported as unsupported by the approved host
launch contract, while stdio and streamable HTTP continue.

Host acquisition/read bounds are explicit implementation limits, not additional
format requirements: 1 MiB per document, 16 MiB per resource read, 4096 entries
per inspected directory, 256 immediate Skill directories and 8 MiB of accepted
Skill documents per discovery. Exceeding a discovery bound emits
`resource_limit_exceeded` at the affected boundary. These are not token limits.

The directory reader uses a held `os.Root` for actual opens. Windows junctions
are resolved by inspecting links through that root, because Go's `EvalSymlinks`
does not follow modern Windows junctions. Absolute links are accepted only when
their destination stays in the package. A later replacement still passes through
the held root and digest check. Filesystem containment is not a process sandbox.

## Real, unmodified fixtures

`testdata/upstream/pins.json` records each file's upstream Git blob, byte length
and SHA-256. Tests recheck every pinned file. These copies total 245,595 bytes;
the copied upstream licenses remain beside their respective content.

- `agentplugins/agent-plugins-example` at
  `5f3f5084a821aefa792e79500dd8f0462ab83473`: complete seven-file portable package.
- `anthropics/skills` at `8a1541c4a3ffa5a20a5a91de0dcf3f0bab1d1ef4`,
  `skills/skill-creator/`: complete 18-file Skill, including its 33,168-byte
  SKILL.md, references, assets, scripts, an empty Python file and extra folders.
  Its repository's native marketplace format is not claimed as a portable
  Agent Plugin. No upstream script is executed by tests.

## Old-entry retirement status

This increment supplies the new decoder, read path, format selector and a single
conversion to B's launch contract. It does **not** yet switch a public import or
tool route; those files are owned by C. Required integration retirement list:

- Replace unconditional `skills.BuildPackageFromDir` calls in generic directory
  and Git import with format dispatch in the existing service.
- Remove portable packages' dependence on TB manifests, legacy token bounds and
  the old all-or-nothing `plugin.v1` contribution validator.
- Use `Package.Launches` at the existing plugin registration seam; remove any
  duplicate portable-to-native launch mapping.
- Extend the existing `skill_read` route to use exact portable content refs and
  bounded `Package.Read`, retaining the same host permission/recovery boundary.
- Keep old v1/v2 ZIP codecs, signature verification, archived builtin/object
  readers and unknown-outcome recovery. Do not create a second installation UI,
  Run/Session store or orchestrator.

Until those C-owned changes and a public import-to-read/runtime test land, this
is a tested loading increment, not an end-to-end migration completion claim.
