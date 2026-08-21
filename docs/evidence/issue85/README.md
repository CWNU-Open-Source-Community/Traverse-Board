# Issue 85 mixed-DPI evidence

This directory contains the investigation history and the final Windows 11
mixed-DPI qualification evidence for issue #85.

## Final result

Issue #85's original acceptance scope passed against one clean, reproducible
candidate:

- version: `v0.1.0-issue85-r4`
- revision: `c531bd2ed96b9c9f5210ab86358c15d647b5e984`
- SHA-256: `9b42e3e57b85954d3add54aa18d8a795738253b60f1c2370594956a9fe957dce`
- size: `58,385,920` bytes
- source tree: `modified=false`
- host: Windows 11 Pro build `26200`, x64
- WebView2 Evergreen Runtime: `151.0.4129.93`

`result-r4.json` is the final machine-readable result. The raw automated
matrix remains in
`desktop-test-matrix-windows11-mixed-dpi-issue85-r4.json`; that script reports
`needs_manual_evidence` by design, while `result-r4.json` records the completed
operator review.

## Display and compact-window coverage

The host remained in Windows Extend mode throughout the test:

- Display 1: 2560x1600, primary, 150% scaling
- Display 2: 3840x2160, secondary, temporarily set to 200% scaling
- Cross-screen route: 150% -> 200% -> 150%, without restarting the candidate

On Display 1 at 150%, the candidate window was explicitly resized with the
native Windows Size operation to exactly 1024x768 logical pixels. The Window
state capture reports width `1024` and height `768`; no device-pixel conversion
is used. At that exact size, theme, language, sidebar resizing/collapse, input
focus, long Markdown, Activity, Approvals, Settings, Full CDP labelling, and
Checkpoints remained readable and operable without overlap or clipping. The
200% captures exercise the same candidate on the second physical display.

Cleanup restored both displays to 150%, kept Display 1 primary, and left Extend
enabled. The candidate and Settings windows were closed.

## Product fixes

The clean r4 revision contains both defects found during the earlier
development pass:

1. `workspaceCheckpointControlEnabled` is retained by the connection store,
   cleared on disconnect, forwarded through `App`, and supplied to
   `CyberAgentClient`.
2. Run-tab buttons use `flex: 0 0 auto`; labels keep their intrinsic width and
   use the existing horizontal scroll container instead of overlapping at
   200%.

Unit tests cover the capability store lifecycle and its propagation into the
Run workspace.

## Formal Checkpoint workflow

All actions used a synthetic Git repository under ignored `.tmp` state.

- Rewind preview reported one recoverable `main.go` modification.
- Native confirmation created COMPLETE preflight checkpoint
  `wcp-d5280694e1b4d3044d3e67f1b70346db`.
- The resulting COMPLETE checkpoint is
  `wcp-f731272ee55e472e83ef4235148a35f9`.
- `main.go` was restored to `issue-85-baseline`, and the synthetic worktree was
  clean after Rewind.
- Fork created independent Run
  `run-20260820204825-6b078c5a2eff` on branch
  `codex/issue85-formal-r4-fork`, with COMPLETE checkpoint
  `wcp-3770c1d251447940dd274654095be7e8`.

## r4 evidence map

Core shell and compact-window review:

- `win11-26200-screen1-150dpi-1024x768-issue85-r4-long-conversation-markdown-acrylic-zh.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-sidebar-resized-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-sidebar-collapsed-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-session-input-focused-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-live-activity-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-approvals-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-checkpoint-rewind-complete-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-tabs-dark-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-long-conversation-markdown-dark-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-sidebar-resized-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-sidebar-collapsed-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-live-activity-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-approval-queue-acrylic-en.jpg`

Appearance, language, and Settings:

- `win11-26200-screen1-150dpi-1024x768-issue85-r4-appearance-light-zh.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-appearance-dark-zh.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-appearance-acrylic-zh.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-settings-general-acrylic-zh.jpg`
- `win11-26200-screen1-150dpi-1024x768-issue85-r4-settings-full-cdp-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-appearance-light-zh.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-appearance-dark-zh.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-appearance-acrylic-zh.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-settings-general-dark-zh.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-settings-full-cdp-acrylic-zh.jpg`

Mixed-DPI and focus retention:

- `win11-26200-screen2-200dpi-issue85-r4-long-conversation-markdown-acrylic-en.jpg`
- `win11-26200-screen2-200dpi-issue85-r4-input-visible-focused-acrylic-en.jpg`
- `win11-26200-screen2-200dpi-issue85-r4-live-activity-acrylic-en.jpg`
- `win11-26200-screen2-200dpi-issue85-r4-approval-queue-acrylic-en.jpg`
- `win11-26200-screen2-200dpi-issue85-r4-settings-general-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-crossback-input-focused-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-crossback-settings-acrylic-en.jpg`

Checkpoint and Fork/Rewind workflow:

- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-timeline-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-rewind-preview-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-rewind-ready-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-rewind-confirmation-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-rewind-complete-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-fork-form-acrylic-en.jpg`
- `win11-26200-screen1-150dpi-issue85-r4-checkpoint-fork-created-acrylic-en.jpg`
- `win11-26200-screen2-200dpi-issue85-r4-checkpoint-layout-acrylic-en.jpg`

`SHA256SUMS.txt` binds every retained report and screenshot.

## Development history

The retained r1/r2-dev/r3-dev evidence documents how the two defects were
found. It is not combined with r4 to claim the final pass:

| Build | Provenance | Result |
|---|---|---|
| `v0.1.0-issue85-r1` | clean revision `715591a015fd12506e1b1459effcf49a30e909cd`, SHA-256 `f7c3b43d2ef67c03566bb3817efb74bb14f4c4d139c1e7f14ade9a97b966bfe6` | Automated matrix passed; interactive review found the dropped Checkpoint control capability. |
| `v0.1.0-issue85-r2-dev` | modified-tree SHA-256 `fdcb24743b8cf9fda297325cb3954472d2b9716a03bfe751e68a1ae9664d55bc` | Checkpoint control and complete write workflow passed; 200% review exposed shrinking Run-tab labels. |
| `v0.1.0-issue85-r3-dev` | modified-tree SHA-256 `8860c164f29d64d5e688e923bda79bae8ab0d47454a97479150080319ae03454` | Non-shrinking, horizontally scrollable tabs passed at 150% and 200%. |

## Scope and sanitization

This result signs off the UI rows named in issue #85 when it was filed.
Advanced Git rows were added later by issue #117 / PR #120 and are not claimed
by this evidence set. Broader Windows 10 and WebView2 fault-injection evidence
remains owned by the parent release matrix.

All retained in-app content is synthetic. Raw captures containing unrelated
desktop overlays remain only under ignored `.tmp` state and are not committed.
