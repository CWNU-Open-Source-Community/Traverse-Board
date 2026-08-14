# ADR 0096: macOS Desktop Portable Build

Date: 2026-08-14

## Status

Accepted.

## Context

The Desktop shell has been Windows-only since ADR 0034: cmd/cyberagent-desktop
compiled exclusively under "windows && desktop && wv2runtime.error", packaged as
an unsigned portable .exe, and every platform seam (WebView2 prerequisite
probe, native startup-failure dialog, workspace launcher discovery, ConPTY
terminal, credential store) was Windows-shaped. The Windows portable preview is
stable enough for operator testing, and there is now a request to build and run
the same Desktop on macOS ahead of the Windows Beta distribution work.

macOS cannot reuse three Windows mechanisms directly: there is no WebView2
Runtime to probe (WKWebView ships with the OS), startup failures have no console
when launched from Finder, and workspace launchers are .app bundles launched
through LaunchServices rather than .exe files discovered through known folders
and the uninstall registry. The ConPTY user terminal, Credential Manager, Safe
Web process containment, and WFP network containment remain Windows-only by
design, and macOS must fail closed on those switches instead of inventing
weaker replacements.

## Decision

1. Split cmd/cyberagent-desktop by build tag:
   - "desktop" holds the shared shell: capability flags, in-process API
     handler, bridge wiring, and the Wails application options.
   - "windows && desktop && wv2runtime.error" keeps the WebView2 prerequisite
     probe, native MessageBox failure path, Acrylic window options, and the
     registry/known-folder workspace launcher discovery.
   - "darwin && desktop" adds the macOS counterparts. The wv2runtime.error tag
     stays Windows-only; macOS pins the stable WebView2 failure semantics
     without needing it.
2. On macOS, checkDesktopPrerequisites returns nil: WKWebView is bundled with
   every supported macOS release (Wails v2 requires macOS 10.15 Catalina or
   newer). No download, installer, or URL is ever triggered.
3. macOS startup failures print the normalized error to stderr and then show a
   best-effort "display dialog" through the fixed /usr/bin/osascript. The
   message is bounded, path-free, and strictly escaped into a single
   AppleScript string literal; osascript failure never changes the exit code
   and no installer or download is involved.
4. The macOS workspace launcher uses only the fixed /usr/bin/open gateway. It
   discovers a closed set of .app bundles under /Applications,
   /System/Applications/Utilities, /Applications/Utilities, and
   ~/Applications (Antigravity, PyCharm/PyCharm CE, WebStorm, Visual Studio
   Code, Terminal) plus an always-available Finder entry that opens the
   registered directory itself. Validation requires absolute, existing .app
   directories; the launched process gets only the registered directory, runs
   in its own session (Setsid), and the operator-confirmed pathless boundary
   from ADR 0072 is unchanged. No registry, shell, environment, or arbitrary
   argument is involved.
5. scripts/build-desktop-darwin.sh produces an unsigned, ad-hoc-signed
   build/desktop/Prayu.app from a checked-in packaging/macos/Info.plist
   template, bundles the operator-preview .command launcher and bilingual
   local test guide, pins version/revision/source-date/CGO metadata, supports
   the same consecutive-build SHA-256 reproducibility check as the Windows
   script, and writes portable_release_metadata.v1.
   scripts/check-macos-compat.sh verifies the Mach-O magic/architecture,
   SHA-256 binding, embedded Go module/trimpath metadata, the ad-hoc code
   signature, and the non-installing/operator-preview package boundary. It is
   not notarization: release_ready=false until signing, notarization, and the
   manual macOS matrix are complete.
6. Windows-only capabilities stay Windows-only on macOS and fail closed:
   system credential storage has no plaintext fallback (environment variables
   keep priority per ADR 0045), the ConPTY user terminal backend reports
   unavailable, the Safe Web/WFP browser containment remains product-inert,
   and the controlled/host executors report unsupported. The macOS shell ships
   the same read-only default and the same independent capability gates.
7. The renderer reserves the native traffic-light space in the macOS titlebar
   (mac-titlebar) and lets native controls own minimize/zoom/close; the custom
   window controls remain Windows-only chrome. No renderer authority is added.
8. CI gains a desktop-macos job on macos-latest: it builds the embedded
   renderer, runs the desktop-tagged boundary tests, and runs the portable
   .app build with the reproducibility check. The Windows Desktop job keeps its
   wv2runtime.error tags unchanged.

## Consequences

- "go test -tags desktop ./cmd/cyberagent-desktop" now passes on macOS, and the
  same shared tests run on both platforms; Windows-specific behavior stays
  under "windows && desktop && wv2runtime.error".
- macOS gets a runnable local operator-preview .app with the same Go-owned
  control plane, while signing, notarization, distribution packaging, and the
  manual macOS matrix remain explicitly out of scope for this slice.
- The macOS port does not weaken any renderer, credential, execution, or
  workspace-launch boundary; every platform seam keeps a closed, validated,
  fixed-shape fallback.
