# Durable operation duplication and risk samples / Durable Operation 重复与风险样本

- Inventory version: `durable-operation-inventory.v1`
- Evidence baseline: `main@0751fadbe74feb7640750e4944643e14c41acaa2`
- Authority: [ADR 0135](../adr/0135-pre-1-0-product-convergence.md)

This is a behavioral sample, not a type-name count. The baseline search found 397 Go
files mentioning a request fingerprint, 551 mentioning an operation identity, 58
mentioning receipts, and 30 fingerprint/operation helper functions in
`internal/runmutation/operation.go`. Those measurements identify review pressure;
the flows below show where semantics actually repeat or differ.

## Common dimensions

| Dimension | Shared question | Must remain domain-owned when |
| --- | --- | --- |
| Operation identity | Is the retry the same logical call? | Domain chooses scope components, generation, actor, and target |
| Request fingerprint | Did the payload/expected revision change under the same key? | Canonicalization, secret handling, or authority facts differ |
| Authority snapshot | Which Scope, permission, approval, lease, credential, network, or process facts were valid? | Nearly always; current config cannot reconstruct historical authority |
| Write-ahead intent | What must be durable before an external or privileged effect starts? | External side effects, filesystem/process/network/Git/container work |
| Receipt/result | What exact effect, output bounds, and cleanup state were observed? | Receipt fields prove backend-specific safety or user data changes |
| Replay | Is same-key/same-request safe to return, resume, or reject as uncertain? | Process-local handles, external state, or partial cleanup may be unrecoverable |
| Recovery/cleanup | Who owns stale work and what exact resource may be removed? | Ownership, generation, path/container/process identity and user changes differ |

## Concrete samples

| Flow and source | Identity / intent / result | Proven replay and recovery behavior | Duplication and abstraction risk |
| --- | --- | --- | --- |
| Run creation (`runmutation`, `store/run_creation_operations.go`) | Domain-separated operation digest plus request fingerprint binds goal, Workspace, profile, Surface, phase, and requester; the operation row binds the created Mission/Run/Session | Same key and same request returns the controlled Run; changed request conflicts; creation and initial events are atomic | Good pilot for a shared identity/replay validator. The validator must not create Runs or choose initial authority. |
| Approval decision/grant (`approval`, `store/approvals.go`, `approval_grants.go`) | Idempotency key, request fingerprint, action, desired/result status and exact proposal/grant | Exact retry replays; key reuse for another action/proposal conflicts inside the approval transaction | Uses raw compatibility keys in places and has review/revocation semantics. Do not force it into a structured-tool target or migrate keys without a schema/compatibility plan. |
| Structured WorkItem/Note mutation (`runmutation.Operation`, structured tool store) | Key digest, request fingerprint, invocation, Run, Session, Workspace, supervisor lease, tool, target kind/id and requester | Stored operation omits the transient lease by design; replay binds the same target and result | `TargetKind` is intentionally limited to WorkItem/Note. Expanding this struct into a universal operation would make domain constraints optional and hide lease differences. |
| Run-owned Command Runtime (`runner/command_runtime_job.go`, store job ledger) | `command_runtime_operation.v2` digest deterministically names a job; request/spec/adapter fingerprints, owner generation and job state are stored before process start | Same request can replay durable state; changed fingerprints/backend fail uncertain; cancellation/kill persist terminal tree-reaped state. Live stdin writer is process-local and restart closes rather than adopts it | Identity comparison can share a validator. Process ownership, output ring, terminal persistence, stdin uncertainty and adapter authority must stay in Runner. |
| FileEdit apply and operation receipt (`store/file_edit_apply.go`, `operationreceipt`) | Independent apply operation/fingerprint binds Run, edit, operator and exact reviewed content; public receipt omits keys/paths/content | Exactly-once apply returns a content-free replay-safe result; cleanup may be `pending_review` after the file effect | A generic “success” receipt could falsely claim cleanup. `operationreceipt` is a projection, not the write authority or WAL. |
| Docker Standard Code (`sandbox`, store migrations v97-v99/v125/v128/v132) | Admission, config/image hashes, lifecycle intent/actions, owner/lease generations, exact container fingerprint, I/O receipts and cleanup receipt are cross-bound by triggers | Recovery may TERM/KILL/delete only the exact owned container; authority drift/cancel fails closed; attach stdin rechecks the live container and cannot be adopted after restart | Shares digest/replay vocabulary, but lifecycle order, network/readiness proof and cleanup are security state machines. Flattening tables or callbacks would weaken database-enforced invariants. |
| Advanced Git and batch delivery (`store/git_advanced.go`, `batch_delivery.go`) | Operations bind base/head/repository/worktree, reviewed hunk or delivery plan, request fingerprint, external Git result, review and merge receipt | Terminal Git receipt must match replay; worktree/merge ownership survives restart and cleanup refuses ambiguous or user-modified paths | External repository state and user changes make “retry” domain-specific. Generic cleanup must never delete a worktree by operation key alone. |
| Scheduled jobs, Run wake, dependencies (`runmutation`, scheduled/wake stores) | Operation digest, expected revision/generation, bounded schedule, authorization snapshot, owner lease and round/consumption receipt | Same generation consumes once; stale owner/revision cannot wake or schedule; restart reconciles durable ownership | Common validators can normalize identity. Timing, budget, dependency graph and lease fencing remain separate state machines. |
| Browser/analyzer execution | Durable start intent binds executable/module/request and permission/readiness; runtime handles and CDP/LSP/WASI invocation details are bounded per backend; terminal receipts are persisted where required | Unknown or changed executable/config fails; process-local handle is never replay authority; restart reacquires only through the domain gate | A generic executor receipt would erase containment, provenance and target-specific proof. Only identity encoding and replay-conflict shape are common. |

## Decision

Do **not** extend `runmutation.Operation` or `operationreceipt.Receipt` into a universal
durable operation kernel.

- `runmutation.Operation` remains the structured WorkItem/Note mutation contract.
- `operationreceipt` remains a content-free presentation projection for its declared
  kinds; it does not open transactions, grant authority, or prove backend cleanup.
- Domain packages continue to own state machines, transactions, authority snapshots,
  WAL placement, receipts, recovery, cleanup, triggers, and public errors.
- [#165](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/165)
  may extract only a small immutable identity/replay validator: versioned domain
  separator, length-delimited hashing, normalized operation digest/request
  fingerprint, and the `same/same=replay`, `same/different=conflict` decision.
- The pilot must migrate two existing low-risk flows without schema/protocol/error
  changes. Expansion requires evidence from another independent flow and a separate
  review; it is not automatic after the pilot.

## Required invariant matrix for any consolidation

| Case | Required outcome |
| --- | --- |
| Same operation key, byte-equivalent canonical request | Replay the exact durable identity/result; do not execute twice |
| Same key, changed request/expected revision/authority binding | Conflict or fail closed; never return the first result as if equivalent |
| Different domain with identical input strings | Different digest (domain separation) |
| Crash before intent commit | No privileged/external effect may have started |
| Crash after intent but before receipt | Domain recovery owns the exact resource or reports an uncertain, non-replayable state |
| Stale lease/generation/revision | Reject without side effect |
| Receipt present but cleanup incomplete | Expose pending/uncertain cleanup; do not synthesize settled success |
| Restart without process-local handle | Revalidate/reacquire through the domain gate or fail closed; never reconstruct authority from metadata |
| Legacy database/fixture | Same exactly-once and conflict result after migration |

## Deferred work

- No approval raw-key migration, Docker table consolidation, generic repository, or
  universal transaction callback is approved.
- No attempt is approved to give every operation an identical intent/lease/receipt
  sequence; read-only, database-only, external, and privileged effects differ.
- Further helper extraction waits for #165 results and the classified protocol
  registry in #167.
