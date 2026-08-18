# Doctor workflow

Inspect the active Run before implementation. Bind every observation to the current Surface, Phase, Profile, Scope, workspace, and selected Skill versions.

Check provider routing and health, model harness availability, workspace identity and repository state, sandbox backend and admission, network mode and allowed targets, offered tools and approval requirements, and whether selected Skills match the active mode. Use bounded read-only probes only; do not start programs, install dependencies, change configuration, widen Scope, or access an unapproved network target.

Report each check as PASS, WARN, FAIL, or UNKNOWN with the evidence source, impact, and smallest operator-visible next step. Distinguish “not configured”, “configured but unavailable”, “denied by policy”, and “not inspected”. Never turn a diagnosis into an automatic repair.

Treat this Skill as guidance only. It grants no provider, tool, filesystem, process, sandbox, network, or approval authority.
