# ADR 0168: Operator extension onboarding through the shared control plane

Status: accepted for issue #276 implementation.

## Context

The extension settings page could inspect and disable existing MCP servers and
Plugins, and inspect reviewed LSP servers, but first use required leaving the
product to prepare CLI state. Registration, review, capability discovery and a
successful call are separate facts and must remain distinguishable in the UI.

## Decision

Add bounded, control-authenticated HTTP adapters to the existing Go services.
Web and Desktop share these routes, generated OpenAPI types and settings forms.
The extension inventory reports the entry points implemented by this process as
`onboarding.mcp_registration`, `plugin_import` and `lsp_configuration`; a missing
capability object in an older backend leaves the new controls unavailable.
These booleans describe available operator actions, not execution authority.

- MCP registration accepts a manual descriptor bound to an existing Workspace
  or Run. The host assigns provenance; callers cannot impersonate an acquired
  Plugin source. The existing durable staging operation retains descriptor
  identity and replay behavior. Discovery approval, bounded discovery and exact
  capability-fingerprint approval remain separate actions.
- Plugin import accepts a ZIP of at most the existing 4 MiB archive limit plus
  its SHA-256. The existing package service validates and stages the archive;
  upload provenance identifies its content digest. Import does not approve or
  activate any contribution. Unsigned package review retains explicit untrusted
  confirmation, and enablement selects the reviewed capabilities.
- LSP configuration stages an operator-authored descriptor for a registered
  Workspace, including executable path and hash, arguments, language mappings
  and bounded initialization options. Staging is inert. A separate fingerprint-
  bound review persists the existing `code-intel-config.v1` format with host
  review metadata. Default Web/Desktop startup reloads the managed configuration;
  an explicitly selected configuration retains its startup precedence and is
  read-only in settings. Editing that mode continues through the selected file
  and CLI, avoiding a successful save into a different file that startup ignores.

Workspace-only extension inventory supports setup before a Run exists. Supplying
both a Run and Workspace requires their exact persisted relationship. Plugin
installation remains host-scoped, and Run call audits do not leak into another
Workspace selection. Existing CLI staging, import, review and LSP qualification
commands remain supported.

## Runtime and evidence

LSP keeps one stable Go manager shared with AgentRunner and Tool Gateway. An empty
manager starts no server and advertises no tools. Publishing reviewed configuration
uses the manager's process ownership boundary, closes previous owned clients and
invalidates stale query results. Persistence failure leaves the active
configuration intact. Incomplete cleanup is an explicit unavailable state, not a
successful activation; process ownership remains with the manager for shutdown.
Review itself never initializes the replacement servers.

Managed-file publication takes a nonblocking OS file lock before comparing the
previous content digest and atomically replacing the file. Competing processes
return a conflict instead of overwriting a review. The empty sidecar remains on
disk, but the lock belongs to an open handle and is released on process exit;
restart never needs to infer ownership from a stale PID or delete a lock marker.

The explicit LSP test action rechecks the reviewed descriptor and executable
binding, initializes only the selected server and runs a bounded read-only symbol
query. Its response preserves Workspace, server, generation, capability, query
and document provenance. It does not expose executable paths, arguments,
environment, private roots or raw process output. The existing model-facing LSP
tools continue to use their Code-surface and Workspace-read authority checks.

MCP invocation continues through an ordinary task and the existing Supervisor,
Approval and Tool Gateway path. Settings may draft that request for the operator,
and read the durable call status, result byte count and error code afterward.
No settings endpoint bypasses call approval or treats discovery as execution.
Plugin enablement identifies available contributions; it is not an execution
receipt. UI success messages name the completed step rather than claiming general
extension readiness. Failed form submissions retain the operator's input.

## Verification and rollback

Use the existing MCP peer and LSP process fixtures for first setup, metadata read,
explicit review and an actual result. Exercise registration/import replay,
Workspace mismatch, strict input bounds, fingerprint drift, failed configuration
publication, process cleanup, restart and unchanged CLI behavior. Generated API
contracts, frontend interaction tests and applicable existing CI remain the gates;
this change adds no duplicate full-suite gate or SQLite migration.

Rollback removes the additive adapters and presentation while leaving MCP and
Plugin ledger rows and the existing reviewed LSP configuration format readable by
their CLI services. It never fabricates reviews, rewrites old protocol checksums
or transfers process ownership to the renderer.
