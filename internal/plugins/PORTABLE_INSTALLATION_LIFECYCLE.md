# Portable installation persistence

`plugin-installation.v2` is a host installation descriptor in the existing
`plugin_installations` table. Its `manifest_json` field holds the acquired
`PortableSnapshot`; the original archive remains in `plugin_objects` under the
existing quota, immutable-object and retention constraints. There is no second
installer database or Run/Session database.

The host acquired revision occupies the existing version index for v2 rows; it
does not become an author version. Native metadata and optional author/version
stay in the original source. Missing author version stays absent. The v1 author
manifest, signature readers, JSON records, state and transition audit remain
readable under their unchanged format contract.

Migration 177 rebuilds only the installation table to extend its versioned
constraints. It preserves the object foreign key and transition references,
recreates the existing indices/triggers, and leaves historical migration
checksums unchanged. The clean-install baseline is generated from that same
history. Test fixture downgrade helpers reject native records before mutating
the schema; fixtures are never a product data migration.

`Service.StageDirectory` captures the selected package root, excluding root
`.git` administration before traversal. It persists source provenance, an
operator-selected surface, original component references and an operation-key
digest. An exact retry returns the current installation state. Rebinding that
operation to different source, content or surface fails. The existing unique
archive/object binding rejects duplicate archive provenance rather than silently
aliasing another installation.

Staging is inert. Existing explicit review, enable, disable, quarantine, revoke
and rollback operations remain the authority. V2 currently exposes only the
Skills capability when source instructions exist. Metadata, scripts and MCP
declarations do not grant execution. Reading retained objects also checks their
actual bytes against the stored acquired digest; callers still have to check
current execution and installation authority at use time.

This persistence increment removes the unconditional assumption that every
installation descriptor is a legacy author manifest. Shared identity/capability
accessors replace those repeated legacy lookups in lifecycle code. It retains
all v1 readers and does not yet replace a public import or `skill_read` entry.
The following wiring increment uses this lifecycle for those existing entries;
it does not make plugin authors supply internal Run leases.

Validation uses a genuine schema-176 prefix containing a signed v1 installation,
enabled state, retained object and transition history, then checks exact readback
after migration. Separate tests cover a versionless native snapshot, persistent
constraint rejection, retained-object corruption, schema-fixture refusal and
existing legacy upgrade/rollback/publisher-revocation behavior. Acquisition
continues to reject source links and special files. No process sandbox or
atomic OS revocation guarantee is implied.
