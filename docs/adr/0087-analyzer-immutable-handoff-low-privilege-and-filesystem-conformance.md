# ADR 0087: Analyzer Immutable Handoff, Low-Privilege Context, And Filesystem Conformance

- Status: Accepted
- Date: 2026-08-04

## Context

ADR 0086 demonstrated bounded process and network-resource controls only from
platform test files. It deliberately left three launch-plan requirements
unverified: a caller-owned immutable executable handoff, a separate
low-privilege execution context, and read-only input plus private result
staging. Those controls must be exercised without adding a production process
starter or turning test observations into product authority.

The threat model includes path replacement between validation and process
start, analyzer mutation of its input or unrelated workspace files, result
replacement during handoff, inherited administrator or ambient privileges,
and residue left after validation. It does not assume that paths, model output,
repository instructions, or the child process are trustworthy.

## Decision

### Caller-owned immutable handle handoff

The Windows conformance test opens a read-only handle with explicit sharing,
makes only that handle inheritable, replaces the original path, and starts the
helper with an exact inherited-handle allowlist. The Linux test passes one
already-open read-only file descriptor through `ExtraFiles`. On both platforms,
the child hashes the original object after the path has been replaced and is
never given the path as authority.

This proves the handoff mechanism against the exercised path-replacement race.
It does not prove a production executable loader, publisher identity, platform
signature, or product start authorization.

### Separate low-privilege execution context

Windows creates a primary token with maximum privileges disabled, explicitly
disables the `BUILTIN\Administrators` SID, and lowers its mandatory integrity
level before starting the helper. The observation requires effective
administrator membership to be false, Low Integrity below the caller, and no
more than one enabled token privilege. `TokenElevation` is retained only as
diagnostic UAC provenance because hosted administrator runners can report it
as true after effective administrator authority has been removed. SID
sub-authority parsing uses a copied, bounds-checked byte representation so the
test also passes Go race/checkptr instrumentation.

Linux starts the helper in a new user namespace, maps only the caller UID/GID
to namespace UID/GID 65534, disables supplementary groups, sets
`PR_SET_NO_NEW_PRIVS`, and verifies an empty effective capability set.

These are dedicated execution contexts, not provisioned dedicated operating
system accounts. Windows retains the caller's account SID, and the Linux
namespace maps back to the caller account. `DedicatedAccountObserved` remains
false, so a future product adapter must not claim that the stronger
dedicated-account requirement is satisfied.

### Read-only input and private staging

Windows protects a caller-created staging directory with a non-inherited DACL
for the caller and SYSTEM plus a Low Integrity mandatory label. The Low
Integrity helper can read but cannot modify the Medium Integrity input, cannot
write to a separate Medium Integrity directory, and can write only its private
staging output.

Linux combines the user namespace with Landlock. The ruleset grants read-only
access to the exact input and full access only beneath a mode-0700 staging
directory; a write outside those paths is denied. Both platforms hand the
validated result to a new destination with create-only hard-link semantics,
verify that a conflicting replacement cannot win, and explicitly remove the
staged and destination files.

This is scoped filesystem conformance, not a complete filesystem sandbox.
`CompleteFilesystemSandbox` remains false because the tests do not prove that
every host path, device, namespace, or kernel interface is unavailable.

## Security Invariants

- Every real child process introduced by this decision exists only in
  platform `_test.go` files.
- The child receives an already-open object, not a path lookup capability, for
  immutable-image verification.
- Test helpers receive a minimal explicit environment and cannot convert an
  observation into execution, product invocation, persistence, or Artifact
  authority.
- Result publication is no-replace and digest-checked; cleanup is observable.
- A low-integrity token or user namespace is never described as a provisioned
  dedicated account.
- Platform-specific evidence is not generalized to the other platform or to a
  production adapter.
- Production analyzer files still contain no process starter. CLI, HTTP,
  Desktop, Tool, Skill, Store, Run/Event, and Artifact surfaces remain absent.

## Verification

Windows locally passes immutable-handle, low-privilege identity-context, and
read-only/private-staging conformance. The complete Analyzer package passes
ordinary and race testing, vet, and zero-warning staticcheck. Linux test code
cross-compiles locally with CGO disabled, and all three tests passed the native
Ubuntu analyzer gate in GitHub Actions run `30867231130`. That run exposed the
Windows UAC-provenance mismatch described above; the effective-membership fix
is covered by local repetitions and the package race gate.

Follow-up run `30873766068` proved the Administrators SID was actually removed,
then the hosted runner failed before helper initialization with Windows status
`0xc0000142`: its checkout-resident test executable depended on workspace ACLs
that were no longer available through the disabled group. The conformance test
now copies the exact test image into a private directory with a protected DACL
for the caller SID and SYSTEM plus an explicit Medium Integrity label. The
filesystem fixture root uses the same ACL and label, while only result staging
is relabeled Low Integrity. Administrator membership remains disabled; no
privilege was restored to make the test pass.

Run `30877113396` passed the full Go, Web, and native Ubuntu gates but showed
that the GitHub-hosted Windows service session still returns the same
`STATUS_DLL_INIT_FAILED` before the verified Low Integrity helper initializes,
even from the private directory. The test therefore verifies the restricted
primary token in the parent before spawn: same user SID, effective
Administrators membership false, Low Integrity, and at most one enabled
privilege. Membership inspection duplicates only the primary token into a
query-only impersonation token, leaving the launch token unchanged.

The Windows Actions command is verbose and skips only when all of these facts
hold: GitHub Actions, a Windows runner, empty child output, and the exact
`0xc0000142` status after the parent-side token verification. Every other
startup error remains a failure. This explicit hosted-service skip is not
child-process or production evidence. Local Windows still executes the real
restricted child and passes all three H tests for ten repetitions. Product
authority remains closed.

A local Windows host whose security software or system policy rejects the
verified Low Integrity token before helper initialization can acknowledge
that exact host limitation explicitly: setting
`CYBERAGENT_ANALYZER_ISOLATION_CONFORMANCE_ACCEPT_HOST_LIMITATION=1` converts
the otherwise-fatal conformance failure into a documented skip with the
STATUS_DLL_INIT_FAILED reason. The variable is read only from `_test.go`
files, accepts exactly the value `1`, never widens the skip past the exact
`0xc0000142` empty-output condition that follows the parent-side token
verification, and leaves product authority closed. Every other startup error
remains a failure, and the acknowledgment is not child-process or production
evidence.

The focused tests fail closed on path replacement, child-handle drift,
elevation, integrity or privilege drift, input mutation, outside writes,
staging privacy, replacement handoff, cleanup residue, and any nonzero product
authority flag.

## Consequences

P10-H1/H2/H3 close the three test-conformance slices, while preserving the
stronger product gates. In particular, this decision does not authorize a
product process starter and does not satisfy the provisioned dedicated-account
or complete-filesystem-sandbox requirements. A later product-adapter review
must independently define how these controls are installed, authenticated,
recovered, and audited in production before any controlled start capability
can be considered.
