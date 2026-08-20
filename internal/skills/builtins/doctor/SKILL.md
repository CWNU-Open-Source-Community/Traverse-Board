# Doctor workflow

Inspect the active Run before implementation. Bind observations to its Surface, Phase, Profile, Scope, workspace, and selected Skill versions. Prefer the persisted snapshot:

`cyberagent doctor snapshot --run <run-id> --json`

Check provider routing/health, model harness availability, workspace and repository identity, sandbox admission, network mode/targets, offered tools/approvals, and Skill-mode fit. Use only bounded read-only probes; do not start programs, install dependencies, change configuration, widen Scope, or access unapproved targets.

Keep `not_configured`, `degraded`, and `not_probed` distinct. Report evidence, impact, and the smallest operator-visible next step. Never turn a diagnosis into an automatic repair. For a shareable artifact, use `cyberagent doctor bundle --run <run-id>`; its bounded timeline withholds event payloads, prompts, terminal/command input, and secrets.

Treat this Skill as guidance only. It grants no provider, tool, filesystem, process, sandbox, network, or approval authority.
