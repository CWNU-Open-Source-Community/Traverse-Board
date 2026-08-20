# Doctor workflow

Inspect the active Run before implementation. Bind every observation to the current Surface, Phase, Profile, Scope, workspace, and selected Skill versions. Prefer the persisted structured snapshot:

`cyberagent doctor snapshot --run <run-id> --json`

Check provider routing and health, model harness availability, workspace identity and repository state, sandbox backend and admission, network mode and allowed targets, offered tools and approval requirements, and whether selected Skills match the active mode. Use bounded read-only probes only; do not start programs, install dependencies, change configuration, widen Scope, or access an unapproved network target.

Treat `not_configured`, `degraded`, and `not_probed` as distinct facts. Report each check with its evidence source, impact, and smallest operator-visible next step. Never turn a diagnosis into an automatic repair. If the operator needs a shareable artifact, use `cyberagent doctor bundle --run <run-id>`; it contains a bounded diagnostic timeline but withholds event payloads, prompts, terminal input, command input, and secrets.

Treat this Skill as guidance only. It grants no provider, tool, filesystem, process, sandbox, network, or approval authority.
