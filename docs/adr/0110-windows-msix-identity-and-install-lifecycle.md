# ADR 0110: Windows MSIX Identity And Install Lifecycle

Status: accepted

Date: 2026-08-16

## Context

Prayu ships an unsigned portable preview but no installer, code signature, or
verifiable upgrade/uninstall flow. A per-user MSIX is the natural Windows
package for a local-first desktop app: it sandboxes install files under the
package directory, keeps user data out of the install directory, and gives the
OS a stable identity for upgrade/downgrade-reject and cleanup.

## Decision

The Windows installer is a **per-user MSIX**. Its identity is fixed in a
versioned `AppxManifest.xml` (`packaging/windows/AppxManifest.xml`):

- Identity `Name` is `PrayuDesktop` and `Publisher` is a stable
  `CN=...` subject that must match the code-signing certificate. A self-signed
  test certificate uses a placeholder publisher and is never a release
  signature.
- `Version` follows MSIX `Major.Minor.Build.Revision` and is the single source
  for the package version; the same source revision feeds the portable ZIP
  metadata (ADR 0051 / #56).
- WebView2 Evergreen Runtime is declared as an MSIX framework dependency, but
  the desktop app still runs its own `requireWebView2Runtime` precheck so a
  missing or incompatible runtime always yields the bounded in-app diagnosis,
  never a blank window or a `Forbidden`.

User data (`Workspace`, provider credentials, SQLite store) lives outside the
install directory under the package `LocalState`/`LocalCache` data roots,
surfaced to the app as `CYBERAGENT_HOME`. This is preserved across upgrades and
is removed on uninstall only behind an explicit operator confirmation, never as
a side effect of the default uninstall.

## Consequences

The MSIX gives a stable identity for upgrade and downgrade-reject, and a clean
separation between immutable install files and user data. Signing is
fail-closed: the CI signs only when a protected code-signing certificate (or a
managed signing service) is present in a protected environment, and the private
key is never exported, logged, or committed. Portable ZIP remains a distinct,
explicitly separate distribution artifact. The install/upgrade/downgrade/uninstall
matrix and the formal certificate remain owner-owned tasks; the repository
provides the manifest, packaging, verification scripts, and bilingual guidance.
