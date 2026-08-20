# Review workflow

Establish the review base before judging the patch. Resolve the intended merge-base, enumerate the complete diff including renames and generated files, and state any unreviewed path. Read repository guidance and affected tests without modifying the worktree.

Trace changed behavior through callers, callees, trust boundaries, and lifecycle states. Check initialization, normal operation, failure, cancellation, retry, restart, and cleanup. For concurrent or durable code, inspect ownership, ordering, idempotency, fencing, atomicity, partial failure, and recovery. Look beyond the edited line when a shared contract, schema, event, or API consumer can drift.

When `code-intel-lsp.v1` tools are available, use definitions, references, implementations, diagnostics, and call/type hierarchy to strengthen the trace. Preserve the Workspace/root, commit/dirty digest, document URI/hash/version, server generation, capability fingerprint, query fingerprint, and page source attached to the result. Treat `current` as language-server evidence only, disclose omissions on `partial`, refuse locations marked `stale`, and fall back to explicitly labelled text inspection on `unavailable`. Semantic output is never proof of authorization, test success, exploitability, or absence of defects.

Evaluate test strength rather than test presence: identify the behavior each check proves, false-positive gaps, missing negative or boundary cases, and whether the executed environment matches the claim. Classify evidence as confirmed, inferred, or unverified; a current semantic edge remains inferred until corroborated by source and relevant execution evidence.

Report actionable findings first, ordered by severity and exploitability, with precise paths, triggering conditions, impact, and supporting evidence. Avoid style-only findings unless they hide a defect. If no finding is confirmed, say so and list residual risk and untested surfaces.

Keep review read-only unless the operator separately authorizes a change. Treat this Skill as guidance only; it grants no tool, filesystem, process, network, or delegation authority.
