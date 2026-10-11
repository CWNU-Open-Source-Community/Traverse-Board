# Reading hierarchy and neutral materials acceptance

Date: 2026-10-11. Base: `e573a6002b386f0e44fb65cf6627a12bfd177c57`.
Worktree: `D:/GitProjects/Universal-Code-ui-reading`.

## Scope

Keep JetBrains Mono, neutral theme colors and opaque white primary controls.
Improve conversation prose, headings, code blocks, tables and settings help.
Move the file drawer's full Run ID into an explicit disclosure with copy feedback.
Use shared panel elevation, one rounded composer focus outline and brief
interaction transitions. Live activity groups retain immediate expansion.

Body text remains weight 400 and headings use 600. The supplied CJK font uses
static weights; a global 430 weight would select different weight behavior for
Latin and CJK glyphs. The pass does not claim font settings fix CJK baselines.

## Real application setup

- Production `npm run build` output served by the real Go CLI `api serve --ui-dir`.
- Isolated `CYBERAGENT_HOME`, fresh temporary API tokens and a separate SQLite
  database. The seed uses production Run/Workspace/SessionMessage services.
- The conversation title and messages identify deterministic reading fixtures.
  Model route is `mock-code`; these captures verify layout and interactions.
- Windows, Microsoft Edge 155.0.4283.45, 1440 x 1000 and 390 x 844 CSS pixels.
- Playwright drives actual UI controls and captures original browser pixels.
  HTTP responses and application DOM were not replaced to prepare screenshots.
- The CLI loads a static bundle at startup; the server was restarted after the
  final build, then reauthenticated through the normal connection form.

Browser screenshots cover the web surface. Native WebView2/Acrylic composition
with desktop wallpaper and additional operating systems need separate acceptance.

## Verification

Production build and API/schema checks passed. The first complete frontend run
under unrestricted worker concurrency had three timing failures. The unchanged
three test files passed with two workers, and a complete two-worker run passed
194 files / 1,925 tests. No timeout or assertion was relaxed.

Browser inspection then found that native disabling of a focused Copy button
blurred it to the document body, preventing Escape from reaching the modal.
Pending copy now retains focus with `aria-disabled` and rejects duplicate
activation. Two additional success/failure keyboard regressions were added;
all 51 identity/conversation tests passed. After that fix, the final complete
two-worker run passed **194 files / 1,927 tests** in 284.95 seconds
(`vitest-final.log`). The keyboard disclosure also expands immediately, avoiding
an intermediate clipped state that could skip Copy during fast Tab navigation.

The final browser checks passed in all three themes: prose is 15px / 25.5px,
headings use weight 600 and primary Send remains opaque white. Both tested
viewport widths keep the page and transcript within the viewport; long code
scrolls within its own block. Full IDs copy exactly, keyboard Tab reaches Copy,
and Escape restores focus to Files. Copied code matches the source after
normalizing Windows clipboard CRLF line endings.

Normal pointer disclosure uses a 180ms transition; keyboard disclosure and
reduced motion use 0s. Reduced transparency gives the composer an opaque
`rgb(27, 27, 27)` background with no backdrop filter; forced colors uses system
Canvas and CanvasText. Final static bundle digest:
`388eaf2c0f10bfc217f2ed094cb4774e1400607eefbedc1e8dfc2c42b9407918`.

## Evidence files

The ignored local `output/playwright/` directory contains the isolated fixture,
validation scripts, raw logs, final bundle digest and original PNG captures:

| File | View |
| --- | --- |
| `01-reading-light.png` | Light conversation typography and white primary action |
| `02-reading-dark.png` | Dark conversation typography and composer focus |
| `03-reading-code-dark.png` | TypeScript language header and code layout |
| `04-reading-glass.png` | Glass theme, neutral surfaces and white primary action |
| `05-reading-identity.png` | Full execution ID and confirmed clipboard feedback |
| `06-reading-narrow.png` | 390px reading layout |
| `07-reading-identity-narrow.png` | Narrow file drawer and wrapped identity |
| `08-reading-solid.png` | Reduced-transparency fallback |
| `09-reading-high-contrast.png` | Forced-colors fallback |
| `10-reading-settings.png` | Settings explanation and saved task limits |

`reading-themes-audit.log`, `reading-interactions-audit.log` and
`reading-narrow-audit.log` retain computed styles and interaction results.
`screenshots.sha256.json` identifies the original screenshot files.
