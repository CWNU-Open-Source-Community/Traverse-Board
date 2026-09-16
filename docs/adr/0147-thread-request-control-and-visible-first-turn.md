# ADR 0147: Visible first turns and request-owned Thread interruption

- Status: Accepted for the local UX convergence implementation
- Date: 2026-09-07
- Scope: F02–F05 and the execution-state portion of F14

## Problem and decision

V2 waited for the whole first turn before opening its durable Thread. It also
treated a resumable Run's running status as equivalent to a currently executing
turn, and exposed neither a whole-turn stop nor a clear queued-input path.

Open the Thread after creation and keep submission state in the existing
TanStack Query mutation cache. Preserve drafts and creation intent above the
settings/conversation views. Go still resolves default model routes; TypeScript
validates response shape, exact explicit choices, identity, scope and authority,
without guessing the configured default.

ThreadTurnService tracks the lifetime of its existing Execute requests. There is
no detached execution goroutine or new durable worker. The original request owns
the context passed to the existing lifecycle, lease, handoff, model and tool
execution. A concurrent explicit message is persisted through ThreadService and
acknowledged as pending. The current owner consumes it at a turn boundary using
its accepted Run binding. This is next-turn queuing, not native model steering.

GET /threads/{thread_id}/execution reports this service's idle/running/stopping/
stop_failed state and queue count. While idle, the existing durable failed
handoff also supplies a last_turn_interrupted indication, including after
reconnection. POST /threads/{thread_id}/interrupt uses the existing
Thread control capability and requires the current opaque execution ID. The ID
is a cancellation target, not a bearer, lease, process ID or authorization grant.

The authorized `/turns` handler suspends the ordinary HTTP write deadline while
the existing request executes, then restores a 30-second response write deadline.
The global server timeout is unchanged. Run budgets and request cancellation
remain effective; a long model/tool turn or accepted follow-up must not finish
durably and then lose its response solely because it exceeded 30 seconds.

## Failure, replay and trust boundaries

- SQLite steering, lifecycle and handoff ledgers remain authoritative. Same-key
  input replay must not append or execute a duplicate message.
- Stop cancels only the matching execution context. Delayed requests cannot
  cancel a later execution. Repeating a stop for an already idle Thread is safe.
- Stopping stays visible until the owned execution returns and pending queue
  cancellation succeeds. A cleanup failure retains a stop_failed marker, rejects
  new input, and permits an explicit retry against the same cancellation target.
  Failed handoffs and recovery facts are not
  replaced with a successful Run completion.
- Context cancellation propagates to owned model/tool operations. This contract
  does not promise rollback of completed file changes or external side effects.
- Disconnect or application shutdown cancels the owning request; queued input
  cancellation is persisted separately with a bounded cleanup context. A crash
  leaves the existing handoff/steering recovery evidence. Restart never restores
  process authority or silently launches a worker.
- A service-local idle state says nothing about an execution owned by a separate
  CLI process. Cross-process interruption remains outside this endpoint's scope.
- Operator, model, scope, permission and tool gates remain in Go. The renderer
  cannot supply Run IDs, lease fields, process IDs or tool arguments to stop.

## Alternatives and validation

Only cancelling the active model misses tool work. Calling the existing pause
endpoint while a lease is active violates its quiescence requirement. Detaching
work into an HTTP-handler goroutine would require a new shutdown/recovery owner.
This change instead adds a small cancellation target around the owner already
executing the request and reuses durable message and handoff contracts.

Validate queued input once-only consumption, replay, stopped queue cancellation,
handoff crash recovery, lease release, delayed-stop fencing, HTTP authorization,
and a browser connected to the actual Go API. Native picker wiring uses the
existing pathless desktop import; browser project registration continues through
the service host's CLI and is explained in the empty-project UI.
