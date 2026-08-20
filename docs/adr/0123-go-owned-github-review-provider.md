# ADR 0123: Go-owned GitHub Review Provider

- Status: accepted
- Date: 2026-08-21
- Issue: #118 (child of #107; follows #116 and #117)
- Schema: v124

## Context

Prayu already had typed Git push and Pull Request create/update operations, but review agents lacked a trustworthy loop connecting GitHub PR metadata, review positions, CI evidence and the exact local merge-base diff. Browser-side SDKs, raw tokens, generic HTTP tools, or model-authored mutations would bypass the Go policy/approval/recovery boundary.

## Decision

Add a default-off `github-review-provider.v1` owned by Go. Prefer GitHub App Device Flow; keep device codes process-local and token bundles in the OS credential store by reference. Pin github.com hosts and REST API version, reject redirects outside reviewed log hosts, bound every request/page/archive/text result, and sanitize all remote content as untrusted evidence.

Persist immutable connections with generation CAS, remote snapshots, local evidence graphs, exact write previews, one-time Approval bindings and terminal receipts in schema v124. Only Code/root model turns receive local read-only evidence tools. Network reads and every write remain operator/API/CLI actions; remote writes require an explicit connection-level write gate, Code/Deliver, network-enabled execution permission, current capability and identity, and an exact Approval.

Recovery observes idempotency markers and remote state but never replays an ambiguous mutation. `verified`, `partial`, `stale`, `unavailable` and `not_run` are semantic states, not display hints.

Keep typed branch push and PR create/update in the existing remote Git service because their local Git authority and failure/recovery contracts differ from review-comment operations. Present them as one product workflow without sharing approval records or silently escalating credentials.

## Consequences

- Tokens never cross OpenAPI/Desktop/model/event/SQLite boundaries.
- Review findings can cite exact GitHub and local evidence, with omissions visible.
- GitHub Enterprise Server, webhooks, Marketplace billing and unattended App installation are outside v1.
- Device sessions cannot survive process restart; the operator restarts login.
- A real GitHub smoke remains opt-in and requires a disposable repository.
