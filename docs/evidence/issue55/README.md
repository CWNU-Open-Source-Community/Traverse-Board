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

## First post-layout-fix candidate

The following screenshots use candidate `v0.1.0-issue55-r2`, revision
`c14dcf17974762d36772127323ef0b2661e05403`, SHA-256
`55ca272fc097a728fee9934a7545e03fd83cb86640502356dd047e880bfc1f2c`.
They are retained as intermediate, candidate-bound evidence because the runtime
fault-injection pass subsequently found a separate startup defect.

- `win10-22h2-1024x768-200dpi-fixed.jpg` shows the full title bar, primary
  content, and composer fitting the 512x384 logical viewport at 200% DPI.
- `win10-22h2-1024x768-200dpi-offline.jpg` shows the same bounded surface while
  the guest Ethernet adapter is disabled.
- `win10-22h2-webview2-missing-guidance.jpg` records the bounded recovery dialog
  when the WebView2 registration is absent. No installer or download starts.
- `win10-22h2-webview2-old-guidance.jpg` records the same fail-closed behavior
  for a simulated `93.0.1.0` runtime, below the required `94.0.992.31`.

The malformed-DLL scenario exposed that Wails could terminate before the
application-level startup reporter ran. The adjacent Windows prerequisite
change adds a client-DLL integrity/loadability check. Final release-candidate
evidence is recorded only after that fix is rebuilt and rerun in the guest.
