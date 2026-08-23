# ADR 0130: Windows Local Sandbox Backend

- Status: Accepted
- Date: 2026-08-23
- Scope: GitHub Issue #132; no schema change

## Context

ADR 0127 defines `workspace_access` as a policy ceiling, and ADR 0128 defines
the non-authorizing product readiness projection. Neither the fixed-command
Windows runner nor the `full_access` host Command Runtime proves the required
Drydock-only filesystem, default-deny network, credential, process-tree,
resource, and crash-recovery boundary.

## Decision

Windows x64 installs a `LocalBackend` based on an ephemeral Less Privileged
AppContainer (LPAC). The child is created suspended from a low-integrity,
privilege-disabled base token. Its effective token is inspected before resume;
on current Windows, `TokenIsAppContainer`, low integrity, the absence of the
enabled `ALL APPLICATION PACKAGES` group, the exact profile SID, and the exact
two non-network capability SIDs are authoritative observations, while
`IsTokenRestricted` on the effective AppContainer token is recorded truthfully
as false and is not claimed as an additional proof.

The first capability SID is derived from a cryptographically random profile
name and grants only this run's temporary filesystem ACLs. The second is the
built-in non-network `registryRead` capability required for LPAC Win32 and Go
runtime provider initialization. No internet client/server or private-network
capability is present, and real network conformance remains authoritative.

The process is assigned at creation to a Job Object with kill-on-close,
active-process, job-memory, CPU-rate, and UI limits. Only closed stdin and the
two output pipe handles are inherited. Root completion, timeout, cancellation,
or error terminates and reaps the entire Job tree.

One exact Drydock root receives temporary run-scoped capability-SID
read/write/execute access
and an inheritable low-integrity label. Explicit non-system toolchain roots
receive read/execute only; Windows/Program Files toolchains rely on their
existing read-only LPAC ACLs. The process opts out of `ALL APPLICATION
PACKAGES`, so that global group cannot widen its host reads. Canonical file
handles pin all roots and the executable. NTFS/ReFS, final path, volume/file
identity, tree size, reparse/symlink absence, non-overlap, and hardlink safety
are checked before an ACL grant and again after execution where applicable.
The OS-created AppContainer data tree is resolved from the exact profile SID,
validated beneath the per-user Packages root, and removed before process
creation. A real child must fail to recreate or write that tree, preserving
Drydock as the only writable output root.

AppContainer network isolation is Windows Filtering Platform policy. The exact
capability allowlist contains no internet client/server or private-network
capability. Real-child conformance tests prove DNS, TCP, UDP, and loopback
denial. Environment construction is allowlist-only and redirects or disables
credential-bearing helpers and homes; a host Credential Manager entry is
unreadable from the child.

Before profile creation or ACL mutation, the process fsyncs a sealed owner
journal under an exclusive kernel file lock. Normal cleanup and next-owner
recovery restore exact security descriptors and delete profiles only after path
and file identity verification. No persisted PID participates in authority.

Every backend instance has a cryptographically random runtime generation.
Requests and evidence additionally bind all Run/Drydock/snapshot revision,
lease, operation, capability, manifest, resource, and toolchain identities.
Readiness and execution evidence are strict versioned, non-authorizing DTOs.
The real readiness child must also start through the system volume and read and
write `NUL`. Windows hosts can require non-inheriting metadata-only ACEs on the
system-drive root for `ALL APPLICATION PACKAGES` and `ALL RESTRICTED APPLICATION
PACKAGES`, plus read/write/execute ACEs for both principals and a Low Integrity
label on `\Device\Null`. A missing prerequisite fails closed until an
administrator provisions it; the product process never mutates either
machine-wide security descriptor.

## Product gate

`--enable-workspace-sandbox` is accepted only with permission control. CLI,
loopback API, and Desktop call the real backend probe and set
`WorkspaceSandboxEnabled`, `LocalSandboxProven`, and `LocalBackendReady` only
when `local_sandbox_readiness.v1` is valid and ready. Failed or unsupported
conditions retain stable reason/remediation codes. Approval remains an explicit
alternative; no Local failure may select `full_access` or the host runner.

Issue #134 remains responsible for splitting the shared Command Runtime into
sandboxed and host adapters. This ADR neither installs a model command tool nor
changes the Standard Code preset.

## Consequences

- Windows obtains an OS-backed Local boundary with auditable evidence instead
  of command scanning, proxy cleanup, Worktrees, or Job Objects being described
  alone as a complete sandbox.
- AppContainer profile and temporary ACL mutation require exact recovery logic
  and one process-local backend owner.
- Other platforms remain unavailable until an equivalent independently audited
  backend exists.
- Some tools that assume inherited user configuration, online module download,
  interactive stdin, or detached workers intentionally fail in Workspace
  Access and must use reviewed offline inputs or a separate Approval.

## Verification

Real Windows x64 subprocess tests cover write/compile/test success and every
filesystem, network, credential, resource, process-tree, cancellation, timeout,
profile-tree recreation/write, and simulated-crash boundary above. Product
tests prove CLI/API/Desktop gating without opening danger-full-access. Windows
CI executes the same focused suite. Its ephemeral Windows runners explicitly
opt into a test-only host fixture that supplies the two minimum system-drive
metadata ACEs and the null-device package ACEs/Low Integrity label when missing,
then restores every captured security descriptor after testing.
The security diff audit must contain no unresolved high- or medium-severity
finding before the product gate is accepted.
