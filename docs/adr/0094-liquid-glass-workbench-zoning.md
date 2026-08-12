# ADR 0094: Liquid-Glass Workbench Zoning

Date: 2026-08-13

## Status

Accepted.

## Context

Prayu already had the required Run, workspace, review, terminal, side-task, and
future-browser surfaces, but their visual grouping was weak. The composer used
an opaque beige surface that did not match the desktop glass theme, the font
stack varied by host, and opening the right sidecar at a narrow width could
compress the central content into unreadable vertical columns.

The user supplied three references. `agegr/pi-web` demonstrates useful
functional zoning for an agent workbench. `greyd097/yzrt` and
`u7663394/LGGC-liquid-glass` demonstrate translucent material treatments.
These are design references, not dependencies or sources to copy.

## Decision

1. Group existing sidecar actions into Workspace, Run, and Coming Soon. Review
   and Files remain workspace surfaces; Terminal and Side Tasks remain Run
   surfaces. Browser is a disabled reserved item until its Go admission gates
   are satisfied.
2. Implement Prayu's glass material locally with CSS variables, translucent
   fills, `backdrop-filter`, inset highlights, and restrained shadows. Apply it
   to the composer, public stream, and related popovers without changing their
   behavior or authority.
3. Provide readable fallbacks for reduced transparency, forced colors, and
   environments where backdrop filtering is unavailable.
4. Bundle JetBrains Mono Variable locally and configure Vite to emit font files
   as same-origin assets. Do not weaken `font-src 'self'` or add a remote font
   dependency.
5. At narrow widths, render the right sidecar as a bounded overlay instead of a
   third grid track. Preserve a stable composer width and avoid horizontal
   overflow.

## Security And Capability Boundary

This decision is presentation-only. It does not add a browser launch path,
CDP transport, network access, process execution, file authority, terminal
ownership, or Tool capability. Disabled future entries cannot be selected.
The Go control plane remains authoritative.

No code or assets were copied from the reference projects. `pi-web` and LGGC
were inspected under their MIT licenses. The yzrt reference was used only for
general visual observation; its source is not incorporated and no licensing
claim is made for it.

## Verification

- Production CSS retains standard and WebKit backdrop filters.
- Production fonts are emitted as same-origin WOFF2 assets, not data URLs.
- Desktop and narrow Playwright views have no horizontal overflow or console
  errors; the sidecar overlay does not collapse composer text.
- Focus, reduced-transparency, and forced-colors states remain operable.
- SQLite remains schema v96 and no WFP, Docker, or paid Provider probe is
  required by this visual change.

## Consequences

The interface gains clearer ownership boundaries and a consistent material
system without implying unfinished functionality. The self-hosted font adds
static assets to the desktop bundle, and the existing Monaco chunk remains a
separate build-size warning to address in a future performance slice.
