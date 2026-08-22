# ADR 0128: Go-Owned Run Capability Readiness

- Status: Accepted
- Date: 2026-08-22
- Scope: GitHub Issue #130; SQLite remains schema v126

## Context

Permission, execution-profile, interaction, and browser-CDP controls previously
used similar disabled styling for different facts: an active Run, an execution
lease, a closed startup gate, an unready backend, an incompatible Surface or
Profile, and the already-selected value. React reconstructed part of this state
from Run detail and process capability documents, so CLI, HTTP, and Desktop could
describe the same Run differently.

The projection must explain availability without becoming authority. Persisted
intent, process-local startup gates, backend proof, and one live execution lease
remain separate facts.

## Decision

Go owns `run_capability_readiness.v1`. For every value in Permission, Profile,
Interaction, browser-CDP permission, and the Standard Code preset, it returns:

| Field | Meaning |
| --- | --- |
| `selected` | The value matches the current persisted Run snapshot. |
| `selectable` | The authenticated control path may record this intent now, subject to its normal mutation authentication and revalidation. |
| `runtime_available` | The required process gates and backend evidence currently pass. This is not execution authorization. |
| `blocked_by[]` | Canonically ordered stable reason codes. |
| `remediation[]` | Canonically ordered operator actions corresponding to those reasons. |
| `restart_required` | Exactly true when `startup_gate_closed` is present. |

The envelope contains the exact `run_id`, the five option groups in fixed order,
and `capability_grant=false`. Permission, Profile, Interaction, and browser-CDP
groups contain exactly one selected value. The preset may contain zero or one
selected value because it is derived atomically from the component snapshots.

`selected`, `selectable`, and `runtime_available` are deliberately independent.
For example, the selected Preview profile can be runtime-ready while a running
Run makes it non-selectable. Conversely, a paused Run can allow Local or Docker
intent selection while their runtime stays unavailable.

## Stable Dispositions

Reasons and their permitted remediation are:

| `blocked_by` | `remediation` |
| --- | --- |
| `run_not_quiescent` | `pause_run` or, for preparing/terminal Runs, `create_new_run` |
| `execution_lease_active` | `wait_for_execution_lease` |
| `startup_gate_closed` | `restart_with_startup_gate` |
| `capability_not_implemented` | `upgrade_application` |
| `surface_mismatch` | `select_required_surface` |
| `profile_mismatch` | `select_required_profile` |
| `permission_mismatch` | `select_required_permission` |
| `workspace_untrusted` | `trust_workspace` |
| `sandbox_unproven` | `verify_sandbox` |
| `docker_unavailable` | `install_or_start_docker` |
| `backend_not_ready` | `retry_backend_readiness` |

Both arrays use the order above, with `pause_run` before `create_new_run` and
`retry_backend_readiness` last in the remediation order. Unknown, duplicate,
unsorted, missing, or unrelated entries are invalid. A runtime-failure blocker
cannot accompany `runtime_available=true`.

## Sources and Runtime Boundary

The Application service loads the current Run, mode, execution Profile,
Permission, Interaction, browser-CDP Permission, and active execution lease from
the Go Store. Its process-local runtime input contains only booleans for control
gates, Permission/CDP capability ceilings, sandbox installation and proof,
Docker availability, and backend readiness. These private facts are evaluated in
Go and are not copied into the public response.

CLI calls the same service and prints either the strict JSON envelope or a stable
human-readable line per option. The authenticated read endpoint is:

```text
GET /api/v1/runs/{run_id}/capability-readiness
```

Browser and Desktop React use that endpoint. Desktop passes its exact startup
configuration into the same HTTP service; configuration that disagrees with the
HTTP control gates is rejected at startup. React does not derive authorization
from client options, Run status, leases, or snapshot runtime fields.

The current product still has no proven Local Sandbox backend. Enabling a startup
gate is not proof: Local remains `sandbox_unproven`; an installed/proven but
unready adapter is `backend_not_ready`. Docker likewise distinguishes a closed
gate, unavailable installation, and a proven-but-unready backend. Standard Code
selection is not implemented in #130, so its preset remains non-selectable with
`capability_not_implemented`; #135 may consume this frozen contract without
changing its meaning.

## Authority, Privacy, and Compatibility

The response is read-bearer data, not a bearer token or a mutation receipt.
`selectable=true` does not bypass the distinct control bearer, route capability,
idempotency, confirmation, state, lease, Policy, or backend checks. Likewise,
`runtime_available=true` does not start a process, browser, terminal, container,
or grant network/filesystem authority.

The DTO has an exact allowlist and excludes Workspace paths, Docker endpoints,
browser Profile paths, credentials, lease/owner identities, operation keys, raw
backend errors, and process identities. OpenAPI fixes the v1 protocol and enum
sets. The TypeScript client accepts only the exact keys, group sizes/order,
selection cardinality, canonical dispositions, Run binding, and
`capability_grant=false`; an unknown future version or extension fails closed as
`INVALID_RESPONSE`. This is the explicit old-client compatibility behavior.

## Consequences

- CLI, HTTP, Desktop, and React consume one Go projection and stable vocabulary.
- The UI can distinguish selected intent, mutation eligibility, and actual
  runtime readiness without implying execution authority.
- Local/Docker backend work and the Standard Code preset can evolve behind the
  same versioned contract.
- Adding or reordering a reason is a protocol change and requires coordinated
  Go/OpenAPI/TypeScript updates.

## Verification

Go tests cover running, paused, active lease, startup gates, Surface/Profile and
Permission mismatches, workspace trust, unproven versus unready Local backends,
Docker absence versus backend failure, selected/runtime distinctions, stable
ordering, remediation pairing, CLI JSON, HTTP privacy, and Desktop wiring.
OpenAPI and TypeScript are regenerated deterministically. React tests verify that
server `selectable` controls actions and that malformed or private response
extensions fail closed.

## 中文结论

`run_capability_readiness.v1` 把“当前已选”“现在可切换”“后端现在可运行”拆成三个独立事实，
并由 Go 返回稳定阻塞码、修复动作和重启要求。CLI、HTTP、Desktop 与 React 共用同一投影；
React 不再根据 Run、lease 或本地 capability 自行推导授权。

该响应只用于说明，不是 bearer，也不会启动进程或授予权限。当前 Local/Docker 仍可在满足状态
条件时记录意图，但没有经过证明的后端就必须显示 runtime unavailable；Standard Code 预设在
#130 中仍明确为尚未实现。
