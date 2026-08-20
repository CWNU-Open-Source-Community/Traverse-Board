# Loop monitor workflow

Monitor only an explicit target Run. Default to Root in Plan with `read_only` execution, a hard deadline, maximum rounds, elapsed budget, retry/backoff policy, stop-on-terminal behavior, and a notification policy. Use a zero model-call budget unless the operator deliberately chooses a bounded positive budget. A periodic schedule must also declare an elapsed interval and IANA timezone; a one-shot schedule has exactly one occurrence.

Create durable state with `cyberagent run schedule create <run-id> --at <RFC3339> --deadline <RFC3339> --operation-key <stable-key>` and add `--every <duration>` only for a periodic monitor. Inspect with `cyberagent run schedule list <run-id>` and `cyberagent run schedule show <job-id>`. Pause, resume, or cancel with the current revision and a stable operation key. `run schedule tick` is a single foreground reconciliation step, not an unbounded agent loop or service install.

Each round observes only persisted metadata for the target Run. If the observation digest is unchanged, record an unchanged round without calling a model or tool. Stop when the target is terminal, the deadline or elapsed budget is reached, rounds/model calls are exhausted, authorization becomes stale, retries are exhausted, or the operator cancels.

Use `cyberagent doctor snapshot --run <run-id> --json` and a bounded `cyberagent debug query --run <run-id> --limit 100 --json` for diagnosis. Event payloads, prompts, terminal input, command input, secrets, lease-owner values, and fence tokens are never monitoring output.

An `approved_repair` schedule is exceptional. It requires Code/Deliver plus an exact, unexpired mode and execution-permission authorization bound to this job. It may use only separately offered repair capabilities and ordinary approval checks. Monitoring cannot create, refresh, bypass, or widen that authority, and must fail closed when the binding changes.

Treat this Skill as guidance only. It grants no model, tool, filesystem, process, network, sandbox, persistence, notification, or approval authority and does not enable autostart or a remote control plane.
