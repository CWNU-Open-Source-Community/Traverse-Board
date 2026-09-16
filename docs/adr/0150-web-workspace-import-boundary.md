# ADR 0150: Opt-in Web workspace import over the existing registry

- Status: Accepted for the local UX convergence implementation
- Date: 2026-09-08
- Scope: F01, existing-directory registration, its metadata projection and local model onboarding

## Decision

The Web entry point could create managed projects only through the CLI; existing
directories were reachable through the native Desktop picker. Reuse
`workspace.Manager.Import` and the existing workspace registry through
`POST /api/v1/workspaces/import`, using the `workspace_import.v1` contract.

`api serve --enable-workspace-import` explicitly enables the capability. It is
off by default and requires the existing, distinct control bearer token. The
request passes through the existing loopback Host/client boundary and strict
JSON decoder. It accepts a confirmed, absolute existing directory on the server
computer, bounded to 4096 UTF-8 bytes. The UI offers directory input only when the
capability is enabled; directory listing, browser uploads and remote filesystem
selection are outside this endpoint.

Desktop does not compose this HTTP capability. Its native picker continues to
pass the selected path within Go and exposes no renderer path parameter. Web
registration does not broaden that native boundary or other startup capabilities.

## Registration and projection

Canonical directory identity provides natural retry idempotence. A repeated
selection returns the existing durable workspace. New registrations use atomic
`INSERT ... ON CONFLICT DO NOTHING`; conflicts cause bounded reload and name
disambiguation, including names hidden by the existing Drydock list filter.
Imports no longer use the historical name-based `SaveWorkspace` upsert, which
could replace another workspace's root. That unrelated legacy method is not
globally redefined by this change.

Registration creates no content in the selected directory, runs no command or
model, creates no task and grants no Agent authority. The response contains only
workspace ID, display name, creation time and the explicit non-modification and
non-authorization flags. The entered absolute path is absent from the response.

HTTP and Desktop share `workspace.ImportDisplayName`, which bounds the import
display name to 128 UTF-8 bytes and marks truncation with `...`. This also covers
previous CLI registrations with longer names. Projection never rewrites their
stored name, ID, root or creation time.

## Tradeoffs and evidence boundary

Reuse avoids a new registration ledger, database migration, directory browser,
dependency or execution service. A separate idempotency-key ledger adds no value
to this directory-identity operation. The Web form is an explicitly enabled
operator entry point; it does not imitate a browser-native folder picker.

Focused Go checks cover concurrent and repeated imports, name conflicts,
unchanged directory contents, authorization and request rejection, CLI startup
wiring, Desktop isolation, and re-import of an existing 140-character CLI name
through HTTP and the Desktop bridge. These checks establish the stated backend
boundaries. They do not constitute complete real UI or native package acceptance;
the current batch's validation record owns those results and remaining limits.

## Local model onboarding

The first-message walkthrough exposed two additional blockers: the endpoint form
reused the website-only HTTPS validator, and every custom Provider required a
system credential even when its loopback service did not use authentication.
Align the endpoint form with the existing Go HTTP(S)/literal-loopback boundary;
website links remain HTTPS-only. The browser URL parser is paired with original
host checks so shorthand, integer, escaped or deceptive hostnames cannot become
trusted loopback addresses through normalization.

Reuse Provider definitions, registry, HTTP request runtime, diagnostics and Harness
qualification. Only an exact loopback endpoint with no recursive `$credential`
reference in the effective `request_headers` or `request_body` may use a missing
credential. Extension data that is not executed does not decide this eligibility.
Credential-store errors and existing invalid credentials still fail; remote
Providers and real credential references retain their authentication checks.
The runtime revalidates the bound endpoint and request configuration, omits default
authentication headers for an empty secret, and reports the existing `none` /
`not_required` projections. It never invents or stores a placeholder API key, and
diagnostics/qualification remain necessary before the route is eligible. This
closes the existing setup path without introducing another Provider protocol,
model adapter, credential store or execution service.
