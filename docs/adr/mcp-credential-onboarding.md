# MCP bearer credential onboarding

Status: implemented for the shared Web/Desktop extension settings.

The existing manual Streamable HTTP MCP descriptor supports one OS credential
reference resolved by Go as a bearer token. Local stdio and native Plugin MCP
descriptors do not accept that reference. This change exposes only that supported
token workflow; it introduces no OAuth login, custom authentication header,
browser-to-MCP request or model-callable credential tool.

The operator first stages a descriptor with a dedicated credential name, then
enters, updates or removes the token in its server card. Registration remains
inert. Credential entry does not approve discovery, enable capabilities, test
authentication or invoke tools. Removal deletes the local store entry and does
not revoke remote tokens or change server review state.

GET/POST `/api/v1/extensions/mcp/{server_id}/credential` reuse the Go extension
service and existing read/control token and extension-control gates. Presence
can be read by a read-only connection. The optional `onboarding.mcp_credentials`
flag identifies a backend with the presence/lifecycle adapter; absent capability
leaves the controls unavailable. An unsupported or inaccessible OS store has no
plaintext fallback. Replies contain only local presence, store metadata, exact
descriptor/endpoint/Workspace/Run binding and MCP reference metadata.

POST requires explicit set/delete confirmation and the current reference
fingerprint. All durable MCP registrations sharing the OS name contribute,
including disabled registrations and other Workspaces. Different endpoints
cannot change the shared credential through this adapter. Sharing metadata
changes require renewed operator acknowledgment. OS names can also be used by
other integrations; the UI labels the count as MCP registrations and explicitly
states that confirmation affects all references to that store name.

In-process registration and credential mutation share a lock. Final presence and
reference readback must still match before success is reported. A separate
process can change SQLite registration or OS credentials between the checks;
the OS store and SQLite do not form a distributed transaction. Failed or changed
final readback reports an unverified outcome and requires refreshing presence;
it does not silently retry or claim rollback.

Tokens remain uncontrolled password input and request-local Go/HTTP values.
They never enter React state, mutation variables, query data, localStorage,
SQLite, events, audit metadata, drafts or reply errors. Backend credential errors
are replaced with fixed public messages. Exact response parsing rejects extra
fields, plaintext flags and binding drift. Connection and descriptor changes
remount transient input; old async completions can update only their original
metadata cache key. Removing this adapter leaves CLI credential management and
MCP descriptor/review storage compatible; no migration is introduced.

Verification uses synthetic tokens and an injected in-memory store: Go service
and HTTP lifecycle/authority/shared-reference tests, concurrent mutation and
final-readback drift regressions under the race detector, Desktop shared-handler
assembly, generated OpenAPI/live-handler checks, frontend binding validation,
explicit entry/update/removal, readonly/unavailable gates, stale async isolation
and failed-refresh behavior. Real OS secrets and remote authentication were not
exercised; local presence is not an authentication acceptance claim.
