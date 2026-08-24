# Canonical vocabulary and compatibility mapping / 规范词汇与兼容映射

- Vocabulary version: `canonical-vocabulary.v1`
- Evidence baseline: `main@0751fadbe74feb7640750e4944643e14c41acaa2`
- Authority: [ADR 0135](../adr/0135-pre-1-0-product-convergence.md)
- Builds on: [ADR 0124](../adr/0124-traverse-board-branding-migration.md),
  [ADR 0132](../adr/0132-thread-identity-run-succession.md), and
  [ADR 0134](../adr/0134-unified-thread-transcript.md)

## User concepts

| Canonical term | Chinese presentation | Exact meaning | Do not use it to mean |
| --- | --- | --- | --- |
| `Thread` | 任务 / 对话任务；首次出现可写“Thread（任务）” | Stable user-facing task, URL, Run succession, and history identity; carries no execution authority | One execution attempt, one model call, a Session, a plan item, or an Agent |
| `Run` | 运行 / 执行尝试 | One finite execution attempt with lifecycle, budgets, events, authority snapshots, leases, process and network work | Stable task history or a reusable permission grant |
| `Step` | 步骤 | A user-readable grouping in a Run narrative, such as inspect/edit/execute/verify; presentation grouping unless a specific protocol says otherwise | A durable task identity, authority object, or automatic proof of completion |
| `Tool Item` / `Tool Call` | 工具项 / 工具调用 | One structured tool lifecycle item within the transcript; state comes from Go/Harness facts | Arbitrary model text, a whole Step, or a verified result without receipt/evidence |
| `Workspace` | 工作区 | Operator-selected source/file/repository scope and identity | A sandbox, a worktree, a Thread, or permission by itself |
| `Plan item` | 计划项 | One bounded item in a Plan/Delivery workflow; compatibility code/DB may call it `WorkItem` | A Thread, generic task, child agent, or completed delivery |
| `Agent` | Agent / 执行角色 | A bounded actor/role coordinated inside a Run | A Thread, process, user account, or independent control plane |

The UI may use natural Chinese sentences instead of repeating English nouns, but
stable navigation, help, logs, and cross-surface documentation must preserve the
semantic distinctions above.

## Internal and compatibility concepts

| Existing term/identifier | Role | Presentation rule | Compatibility rule |
| --- | --- | --- | --- |
| `Mission` | Immutable intent, profile, Workspace and Scope bound to a Thread | Advanced/diagnostic explanation only; ordinary navigation says Thread goal/intent | Go type, SQLite identity, API fields, events and exports remain |
| `Session` | Exactly one Run-local conversation/context boundary | Show only where Run-local context, archive, provenance, authority or diagnostics matter | `/sessions`, DTOs, database and protocol identities remain; never merge Sessions across Runs |
| `Task` | Generic prose; also appears in bounded domain names such as child task and batch task | Do not capitalize/use as a new top-level entity. Qualify child/batch uses explicitly | Existing `child_task`, batch task, issue/task prose and compatibility types remain |
| `WorkItem` / `work_item` | Existing Plan/Delivery domain/API/database identity | User label becomes Plan item | No route, DTO, Go type, table/column, event or fingerprint rename in the presentation phase |
| `/runs/{id}` and `/sessions/{id}` | Diagnostic and compatibility routes | Link from advanced diagnostics, not primary task navigation | Supported throughout API v1; Thread remains canonical URL |
| `cyber` / `ctf` Surface or mode labels | Early compatibility/extension skeleton | Do not imply autonomous offensive features or a second product | Read existing configs/rows; new core UX does not promote them |

## Brand and technical compatibility identifiers

| Identifier | Canonical user message | Retention rule |
| --- | --- | --- |
| `Traverse Board · 针路簿` | Product name | Canonical display brand |
| `cyberagent` | “Traverse Board CLI (`cyberagent`)” on first use | Active CLI compatibility identity; no rename without a separate major migration ADR, aliases, and script fixtures |
| `cyberagent-workbench` | Technical Go module identity | Indefinite compatibility; not user-facing branding debt |
| `CYBERAGENT_*`, `CYBERAGENT_HOME`, `.cyberagent-workbench` | “Traverse Board compatibility environment/data identity” in migration docs | Indefinite until an explicit dual-lookup migration proves credential/data/restart behavior |
| `.prayu/config.yaml`, `.prayu/instructions.md`, `.prayu/rules/**` | “Traverse Board project configuration (`.prayu/...`)” on first use | External durable path; rename requires dual-read, precedence/conflict rules and retirement window |
| `Prayu`, `PrayuDesktop`, `prayu.*`, `prayu-web` | Historical display or package/storage compatibility identity | Keep where package/install/browser/build compatibility requires it; do not revive as a second product |
| `TraverseBoard.exe` | Windows public executable | Canonical from ADR 0125; historical release assets remain immutable |

## Compatibility periods

- **UI and ordinary documentation:** converge through
  [#166](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/166).
  Copy can change immediately because it does not change identity or authority.
- **CLI help:** add canonical explanations first. Existing executable, commands,
  flags, and machine-readable fields remain until a separate ADR provides aliases,
  two tagged prereleases and at least 90 days of deprecation.
- **HTTP/OpenAPI v1:** `/threads` is canonical. Existing `/runs`, `/sessions`, DTO
  fields and enum/protocol values remain for all of API v1. Removal requires an API
  major version, dual-client fixtures, export path, and the same minimum deprecation
  window.
- **SQLite and event identities:** retain indefinitely unless a semantic schema
  change—not presentation preference—requires a versioned migration. Cosmetic table,
  column, event, operation separator, or fingerprint renames are prohibited.
- **Exported/package protocols:** follow their external-durable protocol retirement
  rule; a UI alias cannot shorten it.

## Migration order

1. Publish this glossary and annotate Product Scope/architecture references.
2. Change ordinary React/Desktop copy, navigation, empty states and accessibility
   names to Thread/Run/Step/Tool Item/Workspace; retain advanced diagnostic labels.
3. Align CLI help and human-readable errors while preserving commands, flags, exit
   codes and machine fields.
4. Align user documentation, examples, logs and event descriptions; do not rename
   stable event types or JSON keys.
5. Add API aliases only when a concrete user need exists; keep API v1 readers/routes.
6. Consider persistent identity changes only in a separate semantic migration with
   backup, fixtures, dual-read, rollback and authority-equivalence proof.

## Copy and error rules

- A normal error names the user object first and may add the technical object:
  “Thread cannot continue because its current Run is waiting for approval.”
- Logs and support diagnostics include stable IDs with qualified nouns (`thread_id`,
  `run_id`, `session_id`) rather than calling every identifier a task.
- “Step complete” or model text never means verified delivery. Completion claims use
  Harness state, verification evidence and delivery receipts.
- Workspace, worktree, Drydock and sandbox remain distinct. A worktree is source
  isolation; it does not imply process or network containment.
- Presentation aliases never select a Surface, permission, approval, Profile, lease,
  backend or capability.
