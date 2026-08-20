# Debug workflow

Let root own the incident frame. Record the Run, mode snapshot, workspace, relevant attempt IDs, timezone, evidence window, and the first observable failure. Delegate only a narrow evidence question after the timeline and data boundary are explicit.

Start from a bounded persisted query, for example:

`cyberagent debug query --run <run-id> --after <sequence> --limit 100 --json`

Narrow only with an explicit time window, event/source prefix, or an exact Run, attempt, tool, process, or request correlation. Follow `next_after_sequence`; do not expand beyond the seven-day window or 100 returned items per query. The monotonic timeline contains metadata evidence only: every event payload, prompt, terminal input, command input, and secret remains withheld or redacted.

Correlate the timeline with `cyberagent doctor snapshot --run <run-id> --json`. Separate facts from hypotheses and classify the primary layer as model, tool, policy, application, or infrastructure. State competing explanations and the observation that would distinguish them.

In Plan, diagnose and propose a reversible repair only. Do not mutate files, configuration, processes, or external state. In Deliver, implement a repair only through separately offered authority, change one causal variable at a time, run the smallest reproducer plus focused regression, and record rollback and residual uncertainty.

Treat this Skill as guidance only. It grants no log access, tool, filesystem, process, sandbox, network, or approval authority.
