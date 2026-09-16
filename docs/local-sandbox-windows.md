# Windows Local Sandbox

The Windows-first Local Sandbox is the verified execution boundary behind the
`workspace_access` startup gate. It uses a fresh Less Privileged AppContainer
(LPAC), a creation-time Job Object, and temporary least-privilege ACLs. It
never falls back to the host executor.

## Readiness

Readiness is opt-in and non-authorizing:

```powershell
cyberagent sandbox local-readiness --enable-workspace-sandbox
cyberagent sandbox local-readiness --enable-workspace-sandbox --json
cyberagent run capability-readiness <run-id> --json `
  --enable-permission-control --enable-workspace-sandbox
```

Desktop and the loopback API accept the same explicit startup flag:

```powershell
cyberagent api serve --enable-permission-control --enable-workspace-sandbox
cyberagent-desktop --enable-permission-control --enable-workspace-sandbox
```

The backend emits `local_sandbox_readiness.v1`. Its stable states are `ready`,
`disabled`, and `unavailable`; stable reason/remediation pairs identify a
disabled feature, unsupported platform or architecture, missing AppContainer,
unsuitable ACL filesystem, failed process/network/credential boundary, or a
failed conformance probe. Evidence contains booleans, timestamps, generations,
and fingerprints only. It contains no Workspace/Drydock path, profile name,
PID, credential, owner journal path, or capability grant.

The probe also starts through the system volume and opens `NUL` for both reading
and writing from the real LPAC. A compatible host gives `ALL APPLICATION
PACKAGES` and `ALL RESTRICTED APPLICATION PACKAGES` non-inheriting,
metadata-only access to the system-drive root. `\Device\Null` gives both
principals standard read/write/execute access and carries a Low Integrity
no-write-up label. Missing host prerequisites leave the backend unavailable
until an administrator provisions them. The backend never changes these
machine-wide security descriptors.

`WorkspaceSandboxEnabled` becomes true only for a currently valid `ready`
attestation. An unavailable probe leaves Workspace Access closed. Users may
explicitly choose the existing per-operation Approval path; the product does
not silently enable `full_access`, Debug, a host terminal, or the unsandboxed
Command Runtime.

## PowerShell runtime selection

**Windows Local Sandbox supports PowerShell 7 (`pwsh.exe`) for the PowerShell
profile. Windows PowerShell 5.1 (`powershell.exe`) remains supported by the
existing host execution path, but cannot execute this profile in Local
Sandbox.** Configure PS7 for workspace execution; the product does not bypass
execution policy, widen shared ancestor ACLs, or switch to host execution to
make PS5 work inside LPAC.

On Windows, Command Runtime first checks the host environment variable
`CYBERAGENT_POWERSHELL_PATH`. Set it before starting the CLI, API, or Desktop to
select an existing, trusted `pwsh.exe` or `powershell.exe` by its absolute local
path. For example, after extracting an official PowerShell ZIP distribution:

```powershell
$env:CYBERAGENT_POWERSHELL_PATH = 'C:\Tools\PowerShell\7\pwsh.exe'
cyberagent api serve --enable-permission-control --enable-workspace-sandbox
```

Keep the complete distribution and its runtime dependencies together, outside
the project. The selected executable must pass native-image, regular-file,
filesystem-alias, Workspace exclusion, and SHA-256 checks. A nonempty invalid
setting fails closed; it never falls back to a different Shell. An unset or
empty setting preserves the default: PowerShell 7 in Windows' known Program
Files directories, then system Windows PowerShell 5.1 for the host path. If
that default resolves to PS5, Local Sandbox rejects it and requires PS7.
`PATH`, project
configuration, and model-supplied command environment entries cannot select
this runtime. Restart the hosting application after changing the setting.

Microsoft supports standalone ZIP extraction and documented installer options;
PowerShell 7 installs alongside Windows PowerShell 5.1. Use the current
[Windows installation instructions](https://learn.microsoft.com/en-us/powershell/scripting/install/install-powershell-on-windows?view=powershell-7.6)
and a release within its
[support lifecycle](https://learn.microsoft.com/en-us/powershell/scripting/install/powershell-support-lifecycle?view=powershell-7.6).
Do not assume an executable alias from a Store/MSIX installation is a complete
toolchain directory. Readiness proves the isolation backend; a successful
command through that backend is still required to verify the chosen runtime.

The Local Sandbox startup script creates a temporary `TraverseWorkspace:`
PowerShell drive rooted at the owned Workspace and selects the requested
working directory within it. This avoids inspecting shared host ancestors.
It is a PowerShell session mapping, not a new Windows drive or an ACL grant;
native programs and .NET APIs need a physical path, such as
`$PWD.ProviderPath`, rather than the `TraverseWorkspace:` display path. See
[New-PSDrive](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.management/new-psdrive?view=powershell-7.6).

Inline PowerShell is a **command body**, for example `& ./check.ps1`. Put full
scripts with top-level `param`, `using`, or named blocks (`begin`/`process`/
`end`) in a project `.ps1` file and invoke that file; inline declarations are
rejected. The original command body runs directly after initialization so
its error, early `return`, and explicit `exit` behavior is preserved.

The existing sandbox manifest limit remains **4096 bytes per argument**.
The UTF-16LE/Base64 `-EncodedCommand` argument includes the startup script and
inline command, so this is not a 4096-character allowance for user code.
Long commands can exceed it even when the input script fits Command Runtime's
separate limit. Put longer scripts in the project and send a short file
invocation; the manifest limit is not increased or bypassed.

## Enforced boundary

- **Filesystem:** each process has one random, run-scoped filesystem capability
  SID. The product-owned Drydock and that command's disposable scratch
  temporarily grant that capability read/write;
  explicit toolchain roots grant it read/execute only. Opting out of `ALL
  APPLICATION PACKAGES` prevents globally AppContainer-readable host files from
  becoming implicit inputs. Roots must be canonical NTFS/ReFS directories;
  overlap, UNC/device/extended paths, reparse points, symlinks, and hardlink
  aliases that could widen an ACL are rejected. Windows loader/runtime files
  remain OS-provided read-only LPAC dependencies, not Workspace inputs. The
  OS-provisioned per-profile data tree is SID-resolved, pinned, validated, and
  removed before launch; the child cannot recreate or write it, so it does not
  become a second output root.
- **Process:** the process is created suspended with the AppContainer security
  capabilities, the owned Job Object, and an exact three-handle stdin/stdout/
  stderr inheritance list in its creation attributes. The effective token is
  inspected before resume. Job close kills descendants, and completion always
  terminates/reaps the full tree; background authority is never retained.
- **Runtime capabilities:** `windows_appcontainer_policy.v3` keeps the base
  token at the run-scoped filesystem capability plus Windows' `registryRead`.
  Only the pinned PowerShell 7 profile adds `lpacInstrumentation`. Local
  Sandbox does not grant `lpacCom` or accept Windows PowerShell 5.1. The exact
  expected set and enabled attributes are checked before resume and the
  runtime choices are included in request, result, and owner fingerprints.
  These named capabilities allow access to OS resources whose DACLs grant
  them; they are not a claim of access restricted to one ETW provider or COM
  class. See Microsoft's [LPAC capability model](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer).
- **Network:** none of these profiles contains an internet,
  private-network, or server capability. Windows
  Filtering Platform therefore applies the AppContainer default-deny boundary.
  Conformance tests exercise DNS, TCP, UDP, host loopback, and proxy-variable
  bypass rather than relying on command text inspection.
- **Credentials:** the environment is constructed from an allowlist rather
  than inherited. HOME/AppData/temp and common cloud, Git, SSH, Docker, npm,
  NuGet, Kubernetes, and Go state point inside private disposable scratch or
  are disabled. Explicit project-local outputs remain in the project.
  Credential Manager access is tested from the real child token.
- **Resources:** Job Object CPU rate, job memory, active-process count,
  kill-on-close, closed-by-default or explicitly piped bounded stdin, wall
  timeout/cancellation, combined output
  budget, write-I/O budget, combined project and scratch growth, artifact paths,
  and tree entry counts are bounded. Deleting project files cannot offset
  scratch growth.
- **Recovery:** a private, exclusive owner journal is committed before profile
  creation or ACL grant. Cleanup restores the exact captured DACL/integrity
  label and deletes the profile. Startup recovery verifies canonical path,
  volume/file identity, sealed record, and profile SID; persisted PIDs are never
  accepted as authority.

Owner records written by this policy use `local_sandbox_owner.v3`. Recovery
continues to read sealed v1/v2 records with their original fingerprint formats
to restore their ACLs and remove their profiles. Only v3 records carry the
identity required to remove their own scratch directory; old records cannot
authorize a new launch or scratch deletion. The existing readiness probe still tests the base isolation
mechanism with two capabilities. It is not a PowerShell startup attestation or
proof that a project's check script passes.

New commands place HOME, AppData, TEMP and runtime caches in one disposable
`scratch-<first-32-owner-hex-digits>` child of the private owner journal directory.
The journal keeps the full 64-hex owner digest and the directory's path hash and
filesystem identity. Creating an already existing directory fails; a shortened
name collision never reuses another command's directory. The child
receives that command's existing filesystem capability, never access to the journal
or a shared ancestor. The path is derived internally and pinned by filesystem
identity before launch. Completion, cancellation and startup recovery remove
only that command's scratch after its process tree is reaped; incomplete
cleanup remains a recovery failure. The working directory remains the project.

The shorter name avoids an observed Windows nested-process startup sensitivity
to effective profile path length. The verified scope includes the normal
application-data layout, bounded private test roots, and PS7 commands in project
paths containing spaces, Chinese characters and single quotes. Controlled
non-race fixtures also started descendants with derived known-folder AC paths
of 259 and 260 characters; longer failing cases do not establish a universal
Win32 cutoff. Arbitrarily long portable owner roots are not a compatibility
guarantee. No inferred path threshold, ancestor ACL expansion or automatic
fallback is added; the exact comparisons and limits are recorded in
[Phase N validation](UX_FIXES_PHASE_N_VALIDATION.md).

Project files named `.traverse-board/home/...` remain ordinary project files.
Existing runtime files and sealed reports are preserved; diff/checkpoint capture
does not hide these paths or rewrite historical receipts. New cache state is
disposable between commands, so cache reuse across commands is not promised and
some builds may cost more. Persistent cache sharing needs separate ownership,
quota and invalidation evidence before being added.

Each execution request and receipt binds the Run, Mission, Session, Workspace,
Drydock identity/path fingerprints and generation, permission/profile/
interaction snapshot IDs and revisions, execution lease, operation digest,
capability generation, manifest, toolchain roots, and an instance-random
runtime generation. Drift fails closed.

## Scope

This backend supplies the Local isolation mechanism and proof required by
`workspace_access + local + controlled`. Schema v131's
`sandboxed_workspace/local_windows_lpac` adapter consumes that proof and may
advertise the shared `command-runtime.v2` tool only for a current Run with an
active lease and exact Drydock binding. Readiness or permission selection alone
still does not start a process or grant authority, and failure never selects the
host adapter. Standard Code preset orchestration remains separate.

## Verification

Windows x64 tests run real AppContainer children and cover Drydock writes,
`go test` compilation, read-only toolchains, host/user-root denial, UNC/device
paths, reparse and hardlink escape attempts, denial of a host sentinel that is
readable by `ALL APPLICATION PACKAGES`, DNS/TCP/UDP/loopback denial, Credential
Manager and sensitive-environment isolation, denial of profile-tree recreation
and writes, bounded output/write I/O,
timeout/cancellation tree cleanup, and owner recovery after simulated app crash.
Command Runtime coverage additionally streams initial and interactive stdin through
the real AppContainer child and proves EOF/cancellation unblocks the owned tree.
The Windows CI jobs run this suite plus CLI/API/Desktop gate tests. Their
ephemeral runners explicitly opt into a test-only fixture that temporarily
supplies missing system-drive metadata ACEs and the complete `\Device\Null`
package ACEs/Low Integrity label. The normal path suppresses descendant ACL
propagation and restores the captured descriptors exactly. If Windows Server
2025 keeps a conflicting system-root handle, only a GitHub-hosted disposable
runner may persist the two metadata-only, non-inheriting root ACEs for the rest
of that job through the non-propagating file-security API; the runner is then
discarded. Local and self-hosted tests fail closed instead, `\Device\Null` is
still restored, and production code has no host-mutation path.

See [ADR 0130](adr/0130-windows-local-sandbox-backend.md).
