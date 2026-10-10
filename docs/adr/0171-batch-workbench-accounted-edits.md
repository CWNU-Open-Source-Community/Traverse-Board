# Batch preparation, accounted edits and feedback

Status: implemented, acceptance recorded separately in the frontend completion record.

## User flow

An approved core child-task proposal supplies the child identities, budgets,
dependencies and expected artifacts. The workbench asks the operator to assign
owned paths and select validation recipes, then prepares isolated Git worktrees.
Each explicit execution produces a delivery for independent review. A request
for changes carries its feedback into a new owner generation and a separate
execution. Acceptance enables the existing reviewed merge queue.

The renderer receives capability and state projections. Go retains raw child
owner tokens in memory. After restart, the operator recovers ownership against
the observed generation. Preparing or recovering a child leaves execution as a
separate action. An execution key records durable intent before invoking the
worker; repeating it reads the outcome. An unresolved intent requires inspection
and a new owner generation before another attempt.

## Accounted edit adapter

The initial model adapter accepts bounded coding tasks whose complete owned
source, goal and feedback fit the existing Specialist context. It reads up to
four small UTF-8 text files through the scoped Batch tools. Declared new files
and directories are supported. It reports a smaller-task action if enumeration
is incomplete or context bounds are exceeded. Ownership stays immutable after
preparation; a smaller scope is prepared as a new plan.

The complete brief is divided into bounded parent instructions. Rework replaces
only the bridge's previous parts, using the existing source ID and payload hash
retirement contract. Before model dispatch an internal context check requires
every current part to be present in the prepared durable task brief. The normal
Specialist runner retains its original Store, skill handling, Run lease,
cancellation, retry accounting, monetary reserve/settle and aggregate budgets.
Each explicit execution consumes one Specialist turn, including its ordinary
accounted transport/protocol retries.

The no-tool model returns a `specialist_lifecycle.v1` continuation whose message
contains a `batch-edit-proposal.v1` object: a commit summary and one to four
unique patch/create edits. The Go adapter checks the full proposal against the
observed paths, source SHA-256 values and exact replacement occurrence counts.
The source snapshot reconstructs UTF-8 BOM and LF/CRLF bytes and verifies them
against the authoritative file hash. The bridge rejects a redacted model message,
secret-like decoded fields and secret-like fully reconstructed files before any
proposal is applied. Current instructions must fit the existing four-source
context budget; overflow leaves the inbox and files unchanged.
It then calls the existing propose/apply/commit Batch tools. Owner tokens and
absolute child roots stay in Go. Each side effect rechecks the current owner,
generation, deadline and path scope. The kernel generates the actual delivery
receipt and executes declared validation recipes.

Edits across files are individually durable. A failure after an earlier edit
leaves that edit visible for inspection; recovery does not replay the model or
claim an all-or-nothing transaction. Current-generation receipts and the
original-generation receipts remain independently identifiable. A model report
alone cannot become an accepted delivery.

## Dependency agreement

Batch execution remains gated on accepted current-generation prerequisite
receipts. The store settles only the corresponding admission-created core
dependency edges, bound to the same proposal, admitted Agent pair, declared DAG,
generation and independent accepted review. Other wait edges retain their own
lifecycle. This projection can recover from the accepted durable receipt.

## Validation

Contract tests exercise actual Git worktrees and SQLite with a deterministic
model: first edit, receipt submission, review feedback, owner rotation, rework,
immutable earlier receipt, usage accounting, original checkout isolation and
replay without a second model dispatch. Negative cases cover absent required
context, stale hashes, unread/unowned paths and malformed edits. Native and real
remote-model results are recorded in
[the completion record](../acceptance/2026-10-10-frontend-completion.md).
