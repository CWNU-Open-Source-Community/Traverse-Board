# ADR 0134: Unified Thread Transcript and Persistent Work Surface

- Status: Accepted
- Date: 2026-08-24
- Scope: GitHub Issue #153; SQLite remains schema v130

## Context

ADR 0132 made `Thread` the stable user-facing task identity and ADR 0133 added
provider-neutral item-level model/tool streaming. The product still rendered the
corresponding evidence in separate Session and Run pages. A user had to reconstruct
the relationship between a request, public model update, tool preparation, Go-owned
execution, approval, verification, successor Run, and delivery. The shared Composer
also needed a bounded layout contract so long history, narrow windows, browser zoom,
and virtual keyboards could not push it out of reach.

Reconstructing Supervisor state in React or persisting provider wire output would
violate the existing authority and privacy boundaries. The primary page therefore
needs one Go-owned, safe projection over existing immutable facts.

## Decision

`thread_transcript.v1` is the read-only narrative projection for one exact Thread.
Its durable ordering key is:

```text
(thread_runs.ordinal, run_events.sequence, projected_item.position)
```

Sequence zero is reserved for the immutable Run boundary. Positive sequences are
existing durable Run events. `position` deterministically orders the multiple tool
items carried by one schema-v130 batch event. A boundary states whether it is the
initial Run or a successor, and records the predecessor identity and terminal status
without copying authority from that predecessor.

`GET /api/v1/threads/{thread_id}/transcript` opens at the newest bounded source page.
Its opaque, route-scoped keyset cursor identifies the oldest durable `(ordinal,
sequence)` source already consumed. Appending an event or creating a successor cannot
shift that cursor. The Store reads at most 101 source records per request; the Handler
returns at most 100 source records and reports whether older history exists. One
bounded schema-v130 tool-batch source may expand into its ordered item cards without
splitting or losing identities.

The closed activity vocabulary is:

- `message`, `search`, `read`, `edit`, `execute`, `verify`, `approval`,
  `checkpoint`, and `delivery`;
- `started`, `arguments_ready`, `running`, `result`, and `blocked`.

Classification uses exact durable event types, `runactivity` kinds, and exact
structured tool names. It never parses model prose. Schema-v130
response/item/call IDs reconcile `arguments_ready`, execution start, and result
cards. Pending Composer input is projected from its durable steering row and is
replaced by the committed Session message through the same stable source identity.

## Live replacement and presentation

The primary `/threads/{thread_id}` page combines the durable projection with the
process-local `model_public_stream.v3` snapshot. Provisional items are always marked
`durable=false` and `provisional=true`. React drops them when a durable item has the
same canonical item ID, source reference, or exact attempt/model/tool-round identity.
Confirmed history keeps its `(Run ordinal, sequence, position)` order; provisional
items can appear only at the current tail.

The same page contains the Run lifecycle controls, current approval queue, delivery
records, and Thread Composer. It therefore supports send, pause/resume, approve/deny,
continue, and delivery inspection without treating an audit page as the ordinary
workflow. Events, Artifacts, and Run/Session views remain available as specialist
diagnostic surfaces.

The Thread root is a bounded flex column. The transcript is the only growing scroll
region and uses a measured variable-height virtualizer with overscan; the Composer is
a non-growing sibling that retains the shared sticky positioning contract and safe
area inset. At low viewport heights the header, metadata, and textarea compact so the
transcript keeps a useful scroll viewport. Narrow-container, 200%-equivalent height,
Chinese IME, Shift+Enter, reduced-motion, forced-colors, and keyboard-focus contracts
are explicit. Long details use native keyboard-operable `details`/`summary` controls.

## Trust, privacy, and bounds

- Harness facts and public model content have distinct visible labels, icons, and
  accessibility names; color is not the only distinction.
- Only allowlisted `runactivity` projections and structured item metadata enter the
  transcript. Tool arguments, raw output, provider bytes, credentials, secrets,
  private reasoning, and unbounded terminal data have no response field.
- Public model text remains a model statement, never a verification fact. Only the
  Go-owned Harness projection may be marked verifiable.
- TypeScript validates a closed response shape, stable ordering, source/verifiability
  combinations, durable/provisional flags, and schema-v130 identity relationships.
- The in-memory 10,000-item fixture may retain bounded DTOs, but the DOM renders only
  the measured visible window (with an 80-row no-measurement fallback).

## Consequences

The Thread page becomes the normal task narrative while SQLite and Go remain the
state and authority owners. Live UI can be responsive without becoming a second
Supervisor. Restart may lose provisional bytes, but durable history and cursors
recover without duplication or reordering.

The packaged Standard Code E2E in #140 should enter through the stable Thread URL and
assert the visible request, structured tool lifecycle, Harness verification, pause or
approval controls, and final delivery. It need not scrape the Events page or infer
state from model text.

## Verification

Contract tests cover cross-Run ordering, item-level lifecycle reconciliation,
pending-to-durable replacement, strict route-scoped cursors, append-stable recovery,
redaction, and a 10,000-record cursor window. React tests cover deterministic live
replacement, exact tool classification, variable-height virtualization, native
disclosure controls, one-page lifecycle/approval/delivery controls, IME, and the
Composer CSS cascade contract. Real Chrome checks exercise the stable Thread route at
desktop, 390-pixel narrow, and 640-by-450 200%-equivalent/virtual-keyboard viewports,
including a send, one Standard Code mock step, durable refresh, and same-page pause.

## 中文结论

`thread_transcript.v1` 以 `(Run 顺序, event sequence, item position)` 把用户消息、公开模型
文本、Harness 事实、结构化工具、审批、验证、检查点、交付和 Run 边界投影为一条稳定叙事。游标
基于不可变 keyset，不会因追加事件或创建后继 Run 漂移；临时 model/tool 卡片只在持久身份到达前
存在，且不能冒充 Harness 验证事实。

Thread 页面现在是普通用户的主工作面：同页发送、暂停/恢复、审批、继续与查看交付；Events 和
Artifacts 仍保留为专业审计面。长历史只滚动和虚拟化 transcript，Composer 是独立、sticky、
safe-area-aware 的根布局区域，并对窄屏、200% 等效缩放、IME、虚拟键盘与 reduced motion 保持可达。
