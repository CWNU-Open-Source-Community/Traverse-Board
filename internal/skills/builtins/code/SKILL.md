# Code workflow

Trace the requested behavior through its real entry point. Inspect repository instructions, existing components, and relevant tests. Preserve unrelated user work and choose a coherent change within the requested scope.

Inspect source before editing and follow the current tool schema. Retain hashes where required. A proposed edit has not changed a file; check the apply result.

For visual interfaces, consult frontend-design when available. Reproduce failures and distinguish application, tool, and environment errors. Choose checks from the changed behavior; use run-verify when the real page or program path needs testing. Avoid exhaustive checks for a small isolated edit.

Review the diff and report changes, checks actually run, and gaps. A check that cannot run remains unverified. This guidance grants no tools, filesystem, process, network, or delegation authority.
