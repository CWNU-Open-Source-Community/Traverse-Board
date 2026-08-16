# Issue 55 visual evidence

The screenshots in this directory are sanitized manual evidence from the isolated
`Issue55-Win10-22H2` Hyper-V guest. They contain no credentials, private paths,
chat content, or model output.

## Pre-fix observation

Both pre-fix screenshots use candidate `v0.1.0-issue55`, revision
`f9e991628873c0a4e11d877da3b5b17dce44be62`, SHA-256
`c1b20abb82301098cda68716758fb1938d2b3d32711d2bf9d384e3967d77d6c3`, on
Windows 10 Pro 22H2 build `19045.2965` with a 1024x768 display.

- `win10-22h2-1024x768-100dpi.png` records the usable cold-start surface at
  100% DPI.
- `win10-22h2-1024x768-200dpi-layout-blocker.png` records the 200% DPI layout
  blocker discovered during the matrix: the 1024x680 native minimum window and
  600px CSS minimum shell exceeded the 512x384 logical viewport, clipping the
  sidebar and composer.

Post-fix screenshots and the candidate-bound result table are added only after
the rebuilt clean revision passes the same real-VM checks.
