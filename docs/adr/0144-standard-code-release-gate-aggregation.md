# ADR 0144: Candidate-bound Standard Code release-gate aggregation

## Status

Accepted.

## Context

The packaged bootstrap proves reproducible candidate startup and fixture oracles but deliberately leaves
all 40 attack cases unevidenced. Issue #181 and #182 added independent, fail-closed security and product
producers. Neither producer may declare the parent #140 gate passed, and their reports can be reused safely
only when they identify the same source, executable, archive, fixture, and attack matrix.

The product report includes attended Windows 10/11, DPI, IME, continuity, and delivery-surface evidence.
A GitHub-hosted Windows runner cannot manufacture those observations. Treating their absence as a PR skip,
or replacing them with a synthetic CI report, would turn an evidence boundary into a release waiver.

## Decision

1. `standard_code_release_gate.v1` is the only aggregate verdict. Go strictly decodes and revalidates the
   complete producer reports, recomputes candidate and artifact digests, and derives a deterministic,
   create-exclusive, self-hashed result. Callers cannot supply its status or individual gate booleans.
2. A pass requires bootstrap `pass`, product `pass`, security `passed`, exact candidate and fixture bindings,
   four-language product coverage, four Windows platform rows, all 40 security cases, all 75 applicable
   backend runs, zero unexecuted/failed runs, and exact owned-process cleanup.
3. Missing evidence, unknown fields, trailing JSON, producer hash failure, cross-candidate replay, Full Access,
   Debug, source overwrite, orphan handling, skip, or waiver is a hard failure. The aggregate contains no
   mechanism for overrides, retries, alternate matrices, or reduced counts.
4. A non-PR candidate must be reachable from `origin/main` and have a successful central `CI` push run for
   that exact revision. Candidate evidence then enters the release workflow as two fixed, bounded assets on
   a matching Draft Release. The workflow rebuilds and verifies the candidate before aggregation. A tag
   publication revalidates the downloaded aggregate and turns that same Draft into the release only after
   every gate succeeds.
5. Pull requests validate the gate implementation and packaged bootstrap but do not emit a passing aggregate.
   They request no release and therefore supply no attended evidence. Tag/manual release runs require it.
6. The aggregate is conformance evidence only. It grants no Run, model, tool, process, network, approval,
   container, artifact, signing, or Store-distribution authority.

## Consequences

- Product and security collection remain independently reviewable and can run on the hosts they actually
  require, while the release owner retains the sole final verdict.
- Draft assets are untrusted transport. Their names and size are bounded by the workflow; their content,
  producer self-hashes, candidate identity, and current artifact hashes are revalidated before use.
- An attended evidence run must be repeated whenever the candidate commit or packaged bytes change. Copying
  an older report into a new Draft Release cannot pass the cross-binding checks.
- A failed release run leaves the Draft unpublished. The workflow cannot silently create a release without
  both producer reports and a passing aggregate.
- A passing report cannot compensate for failed or absent repository CI on the candidate revision.
- Formal signing and Microsoft Store publication remain separate work and are not inferred from this gate.
