# Standard Code atomic preset

`standard_code_preset.v1` is the Go-owned entry point behind **Start coding**.
It creates or reconfigures one Run as Code/Plan with `controlled`,
`workspace_access`, restricted browser CDP, a trusted ready Drydock, disabled
network, and no credentials. It does not start execution or grant authority.

## Runtime prerequisites

- A registered Git Workspace accepted by the Drydock source checks.
- A distinct operator/control token for HTTP or Desktop controls.
- Run and execution-permission control enabled.
- `--enable-workspace-sandbox` plus a current ready Windows Local backend, or an
  explicitly selected fixed Docker backend started with
  `--enable-docker-execution` and an exact
  `CYBERAGENT_STANDARD_CODE_DOCKER_IMAGE_DIGEST`.
- No symlink/reparse entry or submodule gitlink in the source Workspace.

`auto` selects only a ready Local adapter. If Local is unavailable, inspect
`docker_readiness`, `blocked_by`, and `next_steps`; choose Docker explicitly or
use the separate Approval workflow. There is no automatic Docker, host-process,
or Full Access fallback.

## CLI

Start with an unconfirmed inspection. This call performs no preset mutation and
returns `trust_required: true` plus an exact `trust_digest`:

```powershell
cyberagent run standard-code preset `
  --workspace <registered-workspace-name> `
  --goal "Implement the requested change" `
  --backend auto `
  --operation-key sc-review-001 `
  --enable-permission-control `
  --enable-workspace-sandbox `
  --json
```

Review the source state, then confirm it with a new operation key because trust
confirmation changes the request intent:

```powershell
cyberagent run standard-code preset `
  --workspace <registered-workspace-name> `
  --goal "Implement the requested change" `
  --backend auto `
  --operation-key sc-configure-001 `
  --enable-permission-control `
  --enable-workspace-sandbox `
  --confirm-workspace-trust `
  --expected-trust-digest <sha256> `
  --json
```

For an existing created or paused Run, pass its ID instead of `--workspace` and
`--goal`. For Docker, add `--backend docker --enable-docker-execution`; Docker is
never selected by `auto` when Local is unavailable.

If a Run is running, the ordinary preset returns `pause_and_configure` as the
next step. Submit the distinct command and keep the same operation key while it
reports `waiting_for_pause`:

```powershell
cyberagent run standard-code pause-and-configure <run-id> `
  --backend auto `
  --operation-key sc-pause-001 `
  --enable-permission-control `
  --enable-workspace-sandbox `
  --confirm-workspace-trust `
  --expected-trust-digest <sha256> `
  --json
```

The intent waits for the execution lease and Supervisor work to become
quiescent. Retrying the exact request is safe; changing any intent field under
the same key returns a conflict.

## HTTP and Desktop

The same control-token operation is available at:

```text
POST /api/v1/standard-code/preset
POST /api/v1/runs/{run_id}/standard-code/preset
POST /api/v1/runs/{run_id}/standard-code/pause-and-configure
```

Every request requires `Authorization: Bearer <control-token>`, a normalized
`Idempotency-Key`, and `Content-Type: application/json`. The create route accepts
`workspace_id` and `goal`; Run routes derive the Run ID from the path.

```json
{
  "version": "standard_code_preset.v1",
  "workspace_id": "workspace-id",
  "goal": "Implement the requested change",
  "backend_intent": "auto",
  "confirm_workspace_trust": false
}
```

The first blocked response may return `trust_digest`. Resubmit with a new
`Idempotency-Key`, `confirm_workspace_trust: true`, and
`expected_trust_digest`. Desktop's Start-coding card follows this same flow
through the in-process handler. If a pause intent is waiting, Desktop retains
its exact operation key for retry.

Responses use one strict shape across CLI JSON, HTTP, and Desktop. Important
fields are `status`, `run_id`, `backend_intent`, `selected_backend`,
`selection_reason`, Local/Docker readiness, `blocked_by`, `next_steps`, the
complete configured snapshot views, `drydock_ready`, `network: disabled`,
`credentials: none`, `replayed`, and `capability_grant: false`. No bearer,
credential, private path, daemon endpoint, lease, owner, or process identity is
returned.

## Failure and recovery

- A preflight failure commits no preset tuple.
- A final insert/event/receipt failure rolls back all policy snapshots and the
  pause transition together.
- A prepared Drydock or waiting pause intent remains an auditable,
  non-authorizing recovery record and converges on exact retry.
- An incompatible Surface produces a new Code/Plan Run; the original Run is not
  rewritten.
- Terminal, approval-waiting, or otherwise incompatible lifecycle states return
  stable blockers and a create-new-Run next step.

The Drydock and its Git worktree establish product ownership and recovery only.
They do not isolate a process. Local AppContainer/WFP/Job/ACL or the fixed Docker
container supplies the execution boundary, and execution rechecks that live
boundary after the preset has been configured.
