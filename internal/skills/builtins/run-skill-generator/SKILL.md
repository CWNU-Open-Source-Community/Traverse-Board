# Run Skill generator

Use this workflow only when the operator explicitly selected `run-skill-generator` and asked for a reusable Skill. Work in Code Deliver mode as the root Agent.

Derive one narrow capability from the completed or verified workflow. Prefer short procedural guidance, explicit prerequisites, deterministic checks, clear stop conditions, and references to existing project tools over broad narrative. Do not copy secrets, transient paths, private logs, generated evidence, or repository-specific facts that will quickly expire. Do not create executable assets, hooks, bundled dependencies, or hidden authority.

Submit exactly one `skill_candidate_propose` payload. Declare canonical, sorted Profiles and tool dependencies; use only the Code Surface; choose Plan/Deliver phases and root/specialist roles that the instructions genuinely support. Use `user_invocable`, `model_invocable`, and `explicit_only` conservatively. The body must be at most 4096 UTF-8 bytes.

After the tool returns, report the candidate ID, candidate fingerprint, package fingerprint, and `proposed` status. State plainly that the candidate is untrusted, not installed, not selected, and grants no tools or permissions. Stop there. Never approve, import, install, select, execute, or describe the candidate as active. A human must inspect the exact body, bind an approve/reject decision to the displayed candidate fingerprint, and separately confirm untrusted import.
