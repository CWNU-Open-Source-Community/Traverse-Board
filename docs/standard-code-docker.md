# Standard Code Docker backend

Schema v128 only extends the existing immutable Docker admission permission check with
`workspace_access`; all execution, lifecycle, log, and Drydock state continues to use
the established ledgers.

The Docker backend is an explicit, fixed `network=none` Standard Code fallback. It is
available only for a Code Run whose current execution profile is `docker`, current
permission is `workspace_access`, exact Drydock is ready, exact per-call approval is
approved, and the current process enables both Workspace Sandbox and Docker execution.
It never falls back to an unsandboxed host command.

## Fixed image

Set one exact, already-present image digest. The product readiness path only inspects
this digest and never pulls:

```powershell
$env:CYBERAGENT_STANDARD_CODE_DOCKER_IMAGE_DIGEST = "sha256:<64-lowercase-hex>"
```

To build the repository fixture, first make the four exact base references named by
`CYBERAGENT_STANDARD_CODE_{GO,NODE,PYTHON,RUST}_IMAGE_DIGEST` available in the
fixed local Engine. Each value must be `name@sha256:<64-hex>`. Then run:

```powershell
powershell -ExecutionPolicy Bypass -File internal/sandbox/testdata/standard-code-docker/build-fixture.ps1
```

The script refuses missing/unpinned bases, disables build-step networking and pulling,
chooses a random tag it owns, strips inherited environment, volume, and label config,
and prints the exact digest for both product and opt-in test vars.

## Readiness and execution

After creating the Drydock and selecting the required Run profile/permission, probe
the current exact generation and Checkpoint:

```powershell
cyberagent run standard-code docker-readiness <run-id> `
  --generation <n> --checkpoint <checkpoint-id> `
  --toolchain go --arg test --arg ./... --purpose "offline tests" `
  --enable-permission-control --enable-workspace-sandbox `
  --enable-docker-execution
```

The JSON response is `standard-code-backend-readiness.v1` and includes stable
`blocked_by` and `remediation`. A missing daemon or image is an expected blocked result,
not host fallback or an image pull.

Preparation creates the existing exact sandbox approval proposal:

```powershell
cyberagent run standard-code docker-prepare <run-id> `
  --generation <n> --checkpoint <checkpoint-id> `
  --toolchain go --arg test --arg ./... --purpose "offline tests" `
  --operation-key <prepare-key> `
  --enable-permission-control --enable-workspace-sandbox `
  --enable-docker-execution
```

Review the returned preparation with the existing `run sandbox request|review` flow.
Then resupply the identical command and exact approved identities:

```powershell
cyberagent run standard-code docker-execute <run-id> `
  --generation <n> --checkpoint <checkpoint-id> `
  --preparation <preparation-id> --approval <approval-id> `
  --toolchain go --arg test --arg ./... --purpose "offline tests" `
  --operation-key <execute-key> `
  --enable-permission-control --enable-workspace-sandbox `
  --enable-docker-execution
```

The result uses `standard-code-command-result.v1`: the file result is the exact new
Drydock Checkpoint and logs are bounded receipt metadata. Raw logs are not persisted.
Use `docker-cancel` for an exact admission and `docker-recover` after restart; both
reuse the existing ownership ledger and never start a new attempt during recovery.

## Boundary

The only writable host projection is the exact current Drydock root at `/workspace`.
A fixed application-owned regular file is mounted read-only over `/workspace/.git`, so
the linked-worktree control path is neither disclosed nor usable in the container; the
host `.git` file is never replaced. The transport rejects a directory, symlink,
unexpected content, or any generic Standard Code request that omits this mask.

Container root filesystem/toolchains are read-only; user is `65532:65532`;
capabilities are dropped; network is none; environment and credential inputs are
absent; Docker socket, HOME, SSH/Git credentials, browser profiles, devices, ports,
and undeclared mounts are not accepted. The Supervisor command has no fields for any
of them. Toolchain cache/temp data uses one fixed 128 MiB/16,384-inode tmpfs outside
the host projection. The runner requires 2 GiB/1,000,000-inode host headroom and
enforces at most 16 MiB and 4,096 entries of aggregate Workspace growth, plus the
16 MiB process file-size ceiling needed by the offline toolchains.

Drydock and its Git worktree are not a process/network/filesystem security sandbox.
They establish ownership, attribution, Checkpoint, and recovery. The fixed Docker
container supplies isolation. Cleanup only touches exact lifecycle-owned containers;
unknown host files/directories and foreign/ambiguous containers are preserved.
