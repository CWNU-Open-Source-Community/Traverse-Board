# ADR 0137: Direct Safe First Run and Explainable Readiness

- Status: Accepted
- Date: 2026-08-25
- Scope: GitHub Issue #136; no SQLite schema change

## Context

The rc.3 Desktop executable opened in read-only mode unless the user found and
used a separate `--operator-preview` launcher. The permission page then rendered
several distinct Go-owned states as visually similar disabled buttons. A new
user could not tell whether a choice was selected, temporarily locked, absent at
startup, unavailable in the current backend, incompatible with the Run, or a
high-risk advanced choice.

Issue #135 added the atomic `standard_code_preset.v1` operation. Desktop can now
offer a safe first coding path without recreating permission sequencing in
React or enabling host-level authority.

## Decision

A zero-argument Desktop launch is the normal safe product entry. It enables the
same bounded control surfaces previously exposed by the safe preview bundle and
installs the platform Local Sandbox adapter when available. It does not enable
danger-full-access, maximum Debug, Full CDP, Docker execution, the user terminal,
batch host validation, the wake worker, or the scheduled-job worker. The
historical `--operator-preview` flag remains a compatibility mode; package
launchers with historical names now invoke the zero-argument default.

`--safe-view` is the explicit read-only entry. It creates no control token and
cannot be combined with another Desktop option. Granular `--enable-*` launches
retain their previous explicit dependency checks. A single high-risk flag never
inherits prerequisite authority from the zero-argument product default.

Opening the safe control plane is non-authorizing. It does not start a Run,
resume a persisted lease or terminal, decide an Approval, trust a Workspace, or
change a Run permission snapshot. Persisted high-risk intent remains historical
state and is non-executable unless the current process independently has every
required startup gate. A normal double-click does not start a durable background
worker, so upgrading cannot resume old scheduled work merely by opening the app.

## First-run flow

Desktop asks the Go read API whether any Run exists. The wizard opens only when
the current durable store has zero Runs and the current process exposes the
Standard Code capability. Existing rc.3 users are therefore not forced through
onboarding, and dismissing the wizard creates no durable bypass marker. Safe View
does not expose the capability and never opens the creation flow.

The keyboard-accessible, bilingual stages are:

1. language;
2. Provider credential stored only in the OS credential manager;
3. two-call Harness qualification and a ready model route;
4. native pathless Workspace selection plus a bounded goal;
5. Go-owned Local/Docker readiness;
6. exact Workspace trust digest confirmation;
7. atomically configured Standard Code Run.

Choosing a directory only registers it. React first sends an unconfirmed
`standard_code_preset.v1` request and displays the returned readiness, blockers,
remediation, and trust digest. A changed confirmation uses a separate idempotency
intent and must pin the exact digest. Only the confirmed Go operation may create
the Run and Drydock. Wizard request/response data contains no host path,
credential, API/control token, lease, or process identity; the existing Desktop
bootstrap keeps its tokens in memory only. The wizard persists no completion
bypass and writes no browser storage beyond the existing language preference.

When no Run exists and the wizard is dismissed for the current process, the home
surface offers one “Start coding” entry that reopens the same flow. When durable
Runs exist but none is selected, the same location offers one “Continue Run”
entry for the latest Run. Advanced Run creation remains elsewhere and is not
presented as the ordinary first path.

## Explainable permission states

The permission center consumes `run_capability_readiness.v1` directly. React
does not synthesize runtime availability. Each option retains every ordered
`blocked_by` and `remediation` value and adds only a presentational category:

- selected;
- temporarily locked (`run_not_quiescent`, `execution_lease_active`);
- unavailable at startup (`startup_gate_closed`, `capability_not_implemented`);
- backend unavailable (`backend_not_ready`, `sandbox_unproven`,
  `docker_unavailable`);
- incompatible (Surface/Profile/Permission mismatch);
- action required (Workspace trust);
- advanced risk; or
- available/unavailable.

Disabled controls remain readable instead of being hidden by low opacity. Full
Access, Debug, Full CDP, and Cyber are folded into explicit advanced disclosures
and retain their existing confirmations. A running Standard Code Run may invoke
the Go `pause_and_configure` action even while temporarily locked; the UI states
that configuration is incomplete until the Run is quiescent and its execution
lease is released.

## Consequences

New users can double-click the official executable and reach a bounded coding
Run without discovering command-line preview switches. Existing stores are not
mutated merely because the default process exposes more safe controls. The
compatibility flag and launcher filenames can be removed only in a later release
with an explicit packaging migration.

The first-run surface is larger and must be maintained with model, Workspace,
and readiness contracts. In return, trust, backend failure, and advanced risk
remain observable and automatable rather than being ambiguous grey UI.

## Verification

Go tests cover the zero-argument safe bundle, explicit Safe View, compatibility
mode, granular high-risk gate isolation, and the absence of default persistent
workers. TypeScript/API tests cover strict new-Run Standard Code validation,
target binding, and token non-disclosure. React tests cover explicit native
Workspace selection, Provider/Harness gates, backend remediation, explicit
Docker fallback, exact trust confirmation, completion, all readiness reasons,
advanced disclosures, and incomplete pause/lease state. The Windows matrix runs
the candidate EXE with zero arguments for cold start, second instance, kill, and
resume; first-run, Safe View, Chinese IME, keyboard, high-contrast, narrow-window,
and 200% DPI evidence remain explicit matrix rows rather than inferred results.

## 中文结论

Desktop 无参数启动现在就是安全产品入口；`--safe-view` 是互斥的显式只读入口，旧
`--operator-preview` 只保留兼容。默认启动不打开 Full Access、Debug、Full CDP、Docker、
用户终端或后台 Worker，也不会因升级而自动信任 Workspace、修改既有 Run 权限或恢复旧执行。

零 Run 用户按“语言 → Provider/凭证 → Harness → Workspace → Go readiness → 精确 Trust
摘要 → Standard Code”完成首次配置。目录选择本身不授权，React 不拼接旧权限接口，只有
用户确认摘要后的 Go 原子操作能创建 Run。权限页完整显示 Go 的所有 blocker/remediation，
区分暂时锁定、启动不可用、后端不可用、不兼容与高级风险；危险能力折叠且仍需显式确认。
