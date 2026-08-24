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

## Enforced boundary

- **Filesystem:** each process has one random, run-scoped filesystem capability
  SID. The product-owned Drydock temporarily grants that capability read/write;
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
- **Network:** the token contains exactly the run-scoped filesystem capability
  plus Windows' non-network `registryRead` capability, which LPAC Win32/Go
  runtimes require for provider initialization. It contains no internet,
  private-network, or server capability. Windows
  Filtering Platform therefore applies the AppContainer default-deny boundary.
  Conformance tests exercise DNS, TCP, UDP, host loopback, and proxy-variable
  bypass rather than relying on command text inspection.
- **Credentials:** the environment is constructed from an allowlist rather
  than inherited. HOME/AppData/temp and common cloud, Git, SSH, Docker, npm,
  NuGet, Kubernetes, and Go state point inside Drydock or are disabled.
  Credential Manager access is tested from the real child token.
- **Resources:** Job Object CPU rate, job memory, active-process count,
  kill-on-close, closed-by-default or explicitly piped bounded stdin, wall
  timeout/cancellation, combined output
  budget, write-I/O budget, final Drydock size, artifact paths, and tree entry
  counts are bounded.
- **Recovery:** a private, exclusive owner journal is committed before profile
  creation or ACL grant. Cleanup restores the exact captured DACL/integrity
  label and deletes the profile. Startup recovery verifies canonical path,
  volume/file identity, sealed record, and profile SID; persisted PIDs are never
  accepted as authority.

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
