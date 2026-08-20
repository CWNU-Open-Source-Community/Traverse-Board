# Review workflow

Establish the review base before judging the patch. Resolve the intended merge-base, enumerate the complete diff including renames and generated files, and state any unreviewed path. Read repository guidance and affected tests without modifying the worktree.

Trace changed behavior through callers, callees, trust boundaries, and lifecycle states. Check initialization, normal operation, failure, cancellation, retry, restart, and cleanup. For concurrent or durable code, inspect ownership, ordering, idempotency, fencing, atomicity, partial failure, and recovery. Look beyond the edited line when a shared contract, schema, event, or API consumer can drift.

Evaluate test strength rather than test presence: identify the behavior each check proves, false-positive gaps, missing negative or boundary cases, and whether the executed environment matches the claim. Classify evidence as confirmed, inferred, or unverified.

Report actionable findings first, ordered by severity and exploitability, with precise paths, triggering conditions, impact, and supporting evidence. Avoid style-only findings unless they hide a defect. If no finding is confirmed, say so and list residual risk and untested surfaces.

Keep review read-only unless the operator separately authorizes a change. Treat this Skill as guidance only; it grants no tool, filesystem, process, network, or delegation authority.
