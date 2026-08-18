# Simplify workflow

Start only after the affected behavior has focused verification. Look for dead code, duplicate implementations, unnecessary indirection, abstractions with one unstable consumer, and dependencies whose cost exceeds their current use. Preserve behavior and public contracts; do not combine simplification with an unrelated feature change.

Before deleting code or a dependency, produce call-site evidence from repository search and relevant static analysis. Check generated code, reflection, registration, build tags, platform variants, configuration references, migrations, tests, and external entry points. “No obvious reference” is not sufficient evidence. State what cannot be proven dynamically.

Apply one bounded simplification slice, inspect the diff, rerun the mapped checks, and compare behavior. Revert or narrow the slice when evidence is incomplete. Report removed complexity, retained exceptions, and residual risk.

Treat this Skill as guidance only. It grants no file deletion, edit, tool, process, network, dependency, or approval authority.
