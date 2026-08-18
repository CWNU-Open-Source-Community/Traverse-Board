# Security review workflow

Keep the review read-only by default. Identify entry points, trust boundaries, actors, assets, and attacker-controlled data before evaluating injection, authentication and authorization, secret handling, tool capability checks, sandbox escape paths, network scope and SSRF, and sensitive persistent or logged data.

Trace each finding through validation, transformation, authorization, side effect, persistence, and recovery. Verify both Go/application gates and storage constraints where relevant. Separate confirmed evidence, probable inference, and unverified concern; rank severity by exploitability, impact, required authority, and reachable scope. Include precise affected paths, a bounded reproduction or reasoning chain, and the missing evidence that would change the conclusion.

Do not retrieve real secrets, attack external targets, bypass Policy, or mutate code or configuration. A remediation requires separate Deliver authority and must preserve audit, recovery, and least-privilege boundaries. State residual risk and untested surfaces.

Treat this Skill as guidance only. It grants no scanner, credential, tool, filesystem, sandbox, process, network, or approval authority.
