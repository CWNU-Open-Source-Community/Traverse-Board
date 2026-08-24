# ADR 0131: Fixed Docker Network-None Backend for Standard Code

- Status: Accepted
- Date: 2026-08-23
- Scope: GitHub Issue #133 and schema v128
- Amended: 2026-08-25; runner v2 and schema v132 sandbox stdin follow-up

## Context

Standard Code needs a container-isolated fallback when the independent Local Sandbox
is unavailable or an operator explicitly selects Docker. The repository already has a
fixed-local-endpoint Docker readiness probe, exact image identity, product admission,
generation-fenced lifecycle ownership, bounded logs/output, cancellation, and startup
recovery. Drydock supplies an exact Run-owned working directory and Checkpoint cursor,
but it is an ownership/recovery boundary, not process, filesystem, network, credential,
or host isolation.

The adapter must reuse those controls without exposing Docker images, daemon endpoints,
mounts, flags, environment, credentials, or network choices in the Supervisor command.
It must never substitute an unsandboxed host process when Docker is unavailable.

## Decision

`standard-code-command.v1` is the backend-neutral Supervisor schema. It contains only a
fixed toolchain selector (`go|node|python|rust`), argument vector, normalized
Drydock-relative working directory, timeout, and purpose. The Local and Docker adapters
return the same `standard-code-command-result.v1` shape with execution identity,
terminal status/exit code, Drydock Checkpoint transition, bounded Artifact receipts,
timestamps, `network=disabled`, and `credentials=none`. Backend selection is product
state, not a command field.

The Docker adapter compiles that command plus current authority facts into the one
recognized `standard-code-docker-runner.v2` manifest (durable v1 manifests remain
readable as closed-stdin recovery evidence):

- one exact current Drydock root projected at `/workspace`, read-write, plus one
  application-owned exact regular file mounted read-only over `/workspace/.git` so a
  linked-worktree control path is not disclosed or usable;
- no command-selected mount, input Artifact, environment entry, secret, device, port,
  Docker socket, endpoint, or user flag;
- ordinary Standard Code fixes stdin closed; the Run-owned Command Runtime may bind
  only `closed|pipe`, with pipe selecting Docker's non-TTY stdin flags and an exact
  owned-container input attachment;
- fixed `/traverse-board/standard-code-runner`, `/workspace`, `65532:65532`, read-only
  container root filesystem, `no-new-privileges`, all capabilities dropped, and init;
- `network=none`, empty allowlist, fixed CPU/memory/PID/output limits, timeout, and
  TERM/KILL grace period; one fixed 128 MiB/16,384-inode cache tmpfs outside the host
  projection; and runner-enforced 16 MiB/4,096-entry aggregate Workspace growth,
  16 MiB file size, and host free-space/inode reserves;
- one pre-existing exact `sha256:` image configured by the operator process. Readiness
  only inspects it and never pulls or builds.

The manifest embeds Run/Mission/Session/source Workspace/Drydock identities, Drydock
generation/current Checkpoint/current Git binding fingerprint, execution profile and
permission snapshot identities/revisions, and a deterministic process-capability
generation. It also carries a digest of the complete backend-neutral Command, including
the operator-visible purpose, so operation replay cannot silently change intent without
exposing that text to the runner. The Application service revalidates all of them at preparation, candidate,
preflight, observation, admission, and immediately before create/start. The current
profile must be `docker`; permission must be `workspace_access`; both the Workspace
Sandbox and Docker startup gates must be present; and the exact per-call approval,
Policy, budgets, readiness, fixed image, and fixed local endpoint must still match.

Before start, any unattributed Drydock content drift fails closed. Once the owned
container starts, its writes are expected. Cleanup/recovery therefore continues to
prove the Drydock root, registered worktree, branch, base ancestry, generation, and
Checkpoint identity without requiring the pre-start content hash. This distinction
allows cancellation and crash recovery to remove only the exact owned container and
then attribute its changes to one deterministic Drydock Checkpoint. A concurrent
generation/Checkpoint change is not guessed or overwritten; cleanup can finish, but
Checkpoint attribution fails for operator recovery.

Standard Code does not export the whole repository through the ordinary bounded Docker
output-directory Artifact path. That would be incomplete and misleading. The exact
Drydock Checkpoint is the file result; stdout/stderr retain the existing bounded,
redacted, content-free log receipt. Ordinary Docker Sandbox manifests continue using
their dedicated read-only input and writable output mounts unchanged.

## Readiness and recovery

`standard-code-backend-readiness.v1` projects the existing
`sandbox.readiness.v1` evidence without granting authority:

| Docker fact | `blocked_by` | `remediation` |
| --- | --- | --- |
| ready | empty | empty |
| startup gate disabled | `startup_gate_closed` | `restart_with_startup_gate` |
| daemon unreachable | `docker_unavailable` | `install_or_start_docker` |
| API/platform/PID/resource/image mismatch | `backend_not_ready` | `retry_backend_readiness` |

Image missing is a stable unavailable result, not an implicit pull. Docker unavailable
is never a signal to execute on the host.

Start, wait, timeout, cancellation, daemon disconnect, application exit, and restart
reuse the existing durable lifecycle WAL, exact labels, lease fencing, deterministic
container name, config inspection, and absence confirmation. Permission/profile/Run or
Drydock metadata drift cancels the active context; terminal cleanup still converges.
Schema v132 records one metadata-only `attach_stdin` action after the started
transition and before daemon mutation. Bytes and handles stay process-local; a
restarted process cannot recover them and cleans the exact owned container instead.
Startup recovery never starts an admission-only record and cannot duplicate a launch.
It only resumes already launched exact-owned records. A bounded query over the fixed
runner protocol also closes the crash window after a Docker terminal receipt but before
its Drydock receipt: records with an existing checkpoint operation are skipped and a
missing checkpoint is finalized idempotently, without invoking start.

## Fixed offline image

`internal/sandbox/testdata/standard-code-docker` contains a static Go runner, pinned
multi-stage Dockerfile, four dependency-free fixtures, and an opt-in real-daemon test.
The build script accepts only pre-existing `name@sha256:digest` bases, builds with
`--pull=false --network=none`, uses a random script-owned tag, strips image Env/Volumes/Labels,
and prints the final exact manifest digest. It does not overwrite a pre-existing tag.
The runner selects fixed absolute tool paths, starts them without a shell, supervises
their process group, and constructs a small fixed environment rather than inheriting
host values. Go disables module network lookup and Cargo is offline. Toolchain cache
and temporary files stay in the bounded tmpfs; only command Workspace changes reach
the Drydock. Runner v2 forwards stdin only for the bound pipe policy; deployments must
rebuild the fixture image rather than using a v1 runner digest for new executions. The
runner establishes its resource monitor before child start, combines
kernel events with aggregate byte/entry scans, forwards cancellation, and fails closed
when accounting is unavailable.

## Consequences

- Docker provides the process/network/root-filesystem isolation boundary; Drydock
  remains only the exact working-directory ownership and recovery boundary.
- The adapter cannot mount HOME, SSH/Git credentials, a browser profile, Docker socket,
  or an undeclared path, and cannot accept managed egress or online installation.
- Windows Local and Docker implement the same Command Runtime stdin/Job/Checkpoint/
  Artifact contract without exposing backend selection in the Supervisor schema.
- Schema v128 performs one compatibility rebuild of the immutable Docker admission
  parent table so its closed permission check accepts `workspace_access`. Historical
  admissions are copied exactly and all binding/lifecycle/immutability triggers are
  restored; no new execution ledger or authority is introduced.

## Verification

Unit tests cover all four toolchain profiles, canonical round-trip, command schema,
Local/Docker result-schema equivalence, readiness dispositions, one-writable-mount
compilation, the exact metadata mask/cache tmpfs, and tampering with network,
environment, Docker socket, extra mount, executable, workdir, resources, and file
export. Git integration proves exact pre-start drift is rejected while already-owned
output can proceed to cleanup/Checkpoint. The opt-in real Docker test runs Go, Node,
Python, and Rust fixtures through the production compiler and lifecycle transport,
verifies host credential/path probes and linked-worktree metadata fail closed, exercises
single-file and mass-entry resource attacks, observes Workspace output, and requires
exact container absence after every run.

The schema-v128 migration test starts from exact v127, verifies the old permission
constraint, upgrades in place, checks every restored admission trigger and foreign key,
and the Application integration persists a real `workspace_access` Docker admission.

## 中文结论

Standard Code 的 Docker 备用后端只接受上层统一的工具链、参数、相对工作目录、超时和目的；镜像、
endpoint、mount、网络、环境与 Docker flags 都由 Go 固定。容器只有一个可写的精确 Drydock 投影，
并以固定只读文件遮住 `.git` 工作树控制路径；工具链缓存位于固定有界 tmpfs。网络为 none，根文件系统
与工具链只读，且不继承宿主凭证。Drydock/Worktree 本身不是安全沙箱；真正的进程、网络和根文件系统
边界来自该固定容器配置。

Docker/daemon/镜像不可用时只返回稳定阻塞与修复动作，绝不回退宿主执行。取消、崩溃、应用退出、
重启或权限漂移复用已有精确所有权清理；文件结果由 Drydock Checkpoint 表示，日志继续使用有界、
脱敏的既有收据。任何无法证明归属的宿主文件、目录或容器都不会被覆盖或删除。
