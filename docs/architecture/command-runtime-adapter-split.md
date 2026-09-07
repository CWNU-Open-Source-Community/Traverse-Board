# Command Runtime adapter split (issue #134)

`command-runtime.v2` remains one adapter-neutral model schema and one shared Job
state machine. The model, repository, Skill, MCP server, persisted Job, and restart
recovery path cannot request an adapter. Go selects at most one installed adapter
from the current Run profile and permission, and advertises it only for a running
root Code/Deliver turn with an active execution lease.

## Adapter identities and permissions

Every advertisement, durable Supervisor call, Job, output page, Artifact, and
execution result carries one exact identity:

- `kind`: `sandboxed_workspace`, `host_unsandboxed`, or the non-executable
  historical projection `legacy_unbound`;
- backend family, installed backend identity, and process generation;
- effective isolation grade, network policy, and credential policy.

`sandboxed_workspace` accepts only `workspace_access` and reports
`workspace_sandbox`, `network=denied`, and `credentials=none`.
`host_unsandboxed` accepts `full_access` or its strict superset `debug` behind the
danger startup gate and
truthfully reports that host network and host credentials remain available. The
input fields `network=disabled` and `credentials=none` are intent and Policy facts
for that host adapter, not isolation evidence. A receipt cannot change kind without
also satisfying the complete identity consistency check.

The authority snapshot is issued when the tool is advertised and is stored in the
Supervisor tool-call ledger. Gateway execution decodes that closed JSON object and
requires the same Run, adapter kind, backend identity, and generation. The
Application layer then rechecks Run, Mission, Session, root Agent, Code/Deliver
mode, profile and permission revisions, Drydock/root fingerprint, lease identity
and generation, and process-owned adapter generation before every operation.
Full Access authority is activated dynamically for the current task and is fenced
on permission drift; it does not require an application restart. Debug uses the
same adapter and checks, then adds its separately startup-gated persistent terminal,
background, and bounded terminal-input capabilities.

## Sandboxed backends

The Windows Local adapter compiles the normalized command into the existing
AppContainer/LPAC Local backend. It is installed only after the AppContainer, WFP,
Job Object, ACL, and runtime-generation readiness proof succeeds. A current Run is
advertised only for `local + workspace_access + controlled`; execution mounts the
exact Run-owned Drydock at `/workspace` and a read-only executable toolchain root.

The Docker adapter compiles only the fixed Go, Node, Python, or Rust Standard Code
toolchains into the existing fixed-image `network=none` backend. Image, endpoint,
user, mounts, environment, resource limits, and Docker flags remain Go-owned. It
uses a persisted v2 sandbox execution candidate bound to the exact active Run lease;
candidate validation, lifecycle creation, preflight, evidence, admission, and the
final pre-start check all reject lease ID, owner, generation, status, or expiry
drift. The ordinary operator flow continues to use a quiescent candidate, so this
bridge does not weaken the existing Sandbox transaction.

Both backends reuse foreground/background Job creation, cursor output, timeout,
cancel/kill, owner heartbeat, bounded Artifact capture, and restart reconciliation.
Sandbox Jobs deliberately persist no host PID or process group. Backend cancellation
must return only after its complete process/container tree is reaped. The shared Job
manager owns a bounded stdin pipe for `stdin_policy=pipe`, serializes initial and
interactive writes, and closes it on EOF, timeout, cancel, kill, or owner shutdown.
Windows Local copies that pipe into the AppContainer child's sole inherited stdin
handle. Docker binds the policy into `standard-code-docker-runner.v2`, opens only the
exact owned running container's input attachment, and fences the metadata-only
`attach_stdin` action in schema v132's lifecycle WAL. Input bytes and handles are
never persisted, so restart cannot adopt or replay them and instead converges through
normal container cleanup.

Local commands use the shared Workspace Checkpoint boundary. Docker Standard Code
already owns the exact Drydock checkpoint transaction, so the outer Command Runtime
does not create a second competing checkpoint.

## Restart and legacy data

Schema v131 adds the complete adapter identity to the v116 Job ledger and permits
PID-less sandbox Job states. All pre-v131 rows are migrated to `legacy_unbound`.
They remain listable/readable evidence but cannot count as active capacity, acquire
process ownership, execute, accept stdin, or be adopted after restart. New active
Jobs also are never adopted from a persisted PID: owner loss converges them to
`interrupted` through the existing reconciliation rules. Schema v132 changes no Job
payload and stores no input; it only admits the fenced Docker lifecycle attach verb.

## Product projections

CLI, HTTP, and Desktop expose separate facts:

- the `command-runtime.v2` protocol is compiled into the product;
- one or more exact adapters are installed and their backends are ready;
- the current Run is actually granted one adapter under its present durable
  snapshots and active lease.

Global runtime capabilities list installed adapter receipts without granting them.
`run_capability_readiness.v1` adds the current-Run projection. Persisted readiness,
an installed backend, or a selected permission never becomes execution authority.
