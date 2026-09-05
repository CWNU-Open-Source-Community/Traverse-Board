# ADR 0137: Go-Owned Run-Scoped Web Evidence

- Status: Accepted
- Date: 2026-08-25
- Scope: GitHub Issue #154; SQLite schema v134

## Context

The root Supervisor could read local Workspace evidence, but it had no first-party
way to discover and cite public Web sources. Giving a model, Provider SDK, browser,
or generic command unrestricted network access would bypass Run scope, make replay
ambiguous, and mix search snippets with fetched evidence. It would also expose the
control plane to SSRF, DNS rebinding, redirect, parser, prompt-injection, privacy,
and copyright risks.

The product needs a narrow evidence path whose network authority, parser, durable
identity, and public presentation remain owned by Go. Network-disabled Runs must
retain their existing behavior and must not silently fall back to another Provider,
browser, proxy, or host command.

## Decision

The frozen model-facing registry is `web-evidence-tools.v1`. It contains three
closed tools with versioned payloads:

- `web_search` (`web_search.v1`) asks one configured SearXNG JSON endpoint for at
  most ten public HTTPS result stubs. A title or snippet is untrusted discovery
  metadata and is never citeable.
- `web_fetch` (`web_fetch.v1`) accepts exactly one same-Run `source_id` or one public
  HTTPS URL. Go authorizes and fetches it, parses a bounded representation, and
  commits an immutable snapshot in state `fetched`, `partial`, `blocked`, or
  `failed`.
- `web_citation` (`web_citation.v1`) accepts only a same-Run source and snapshot plus
  a bounded claim and optional text span. It cannot accept or substitute a URL. A
  citation is created only from a `fetched` or `partial` snapshot and retains the
  final URL, fetch time, stale time, digest, and partial/stale facts.

Every source identity is stable within one Run and every snapshot and citation is
content- and operation-bound. Search, fetch, and citation operations use a
Run-scoped idempotency digest: an identical replay returns the committed result,
while reuse with changed input conflicts. Schema v134 adds the source, snapshot,
citation, and operation ledgers with database constraints and immutable
update/delete triggers. A source or snapshot from another Run cannot be cited.

## Authority and availability

Web tools are absent unless the active root Run has `network_mode=allowlist`, at
least one allowed target, a current mode and permission revision, and the current
capability generation. The durable Go-issued call authority binds Run, Mission,
Session, Workspace identity, surface, phase, profile, permission, network targets,
Provider, and generation. The Application layer reloads those facts immediately
before I/O. Permission, mode, root, Provider, or target drift fails closed.

`web_fetch` and `web_citation` are available when the Run network boundary is
available. `web_search` additionally requires a valid
`CYBERAGENT_WEB_SEARCH_ENDPOINT`. The only provider adapter is the documented
SearXNG JSON `GET /search?q=...&format=json` contract. It sends no credential or
cookie and has no implicit public instance or fallback Provider. An unset, invalid,
unauthorized, or failed endpoint returns a stable unavailable/precondition error.

Allowed targets are exact public DNS names, `*.public.example` suffixes, HTTPS
origins on port 443, or the explicit broad `public_https` target. Network mode is
disabled by default. A disabled Run does not publish the tools and a forged or
replayed call still fails at the Gateway and Application boundaries.

## Network and parser boundary

All requests use one first-party client with these fixed controls:

- HTTPS and port 443 only; no URL userinfo or credential-bearing query parameters,
  fragments, proxy, cookie jar,
  ambient credential, or non-FQDN/local hostname;
- IDNA normalization and rejection of loopback, private, link-local, multicast,
  carrier-grade NAT, benchmark, reserved, metadata-service, and other non-public
  IPv4/IPv6 addresses;
- resolution immediately before each request; every returned address must be
  public, and the transport is pinned to that resolved set;
- at most three redirects, with URL policy, Run authority, DNS resolution, and
  address pinning repeated for every hop;
- a 15-second request deadline, a 2 MiB response ceiling, and at most one retry,
  only for HTTP 429, 502, 503, or 504;
- a 256 KiB `robots.txt` check before the initial page and each redirect
  destination. HTTP 404/410 means no policy and 401/403 means blocked.
  Conservative, Workspace, and Approval enforce that result and fail closed on
  network, parse, or indeterminate status. Full Access and Debug retain the result
  as an audit fact, while disallow, absence, or indeterminate status does not block
  the fetch;
- a closed MIME set: HTML/XHTML, bounded text/Markdown, JSON, and PDF. The retained
  sanitized body is at most 128 KiB.

HTML parsing ignores scripts, styles, forms, frames, embedded objects, media,
canvas/SVG, and template content. Text is decoded to valid UTF-8 and unsafe controls
are removed. Credential-looking sequences are redacted before the bounded snapshot
body is committed; model excerpts and citation spans therefore use the same
sanitized durable text while the digest continues to identify the raw response bytes.
The PDF path executes no filter, action, JavaScript, form, embedded file, or external
reference; it extracts bounded literal text only and always marks the result `partial`.

Fetched Web text, metadata, and search snippets are evidence, never instructions.
The Supervisor system policy says they cannot authorize tools, broaden scope, or
override Go policy. Tool results and every shared UI/CLI/API projection explicitly
set `untrusted=true` and `instruction_authorized=false`.

## Public presentation and retention

The Thread transcript displays a source card with the canonical final URL, bounded
title, status, fetched time, short digest, and partial/stale state. The link opens as
an external `noopener noreferrer` target. `cyberagent web-evidence list --run ...`
and `GET /api/v1/runs/{run_id}/web-evidence` use the same Go projection and status
derivation. These public surfaces omit the search snippet, fetched body, citation
claim, raw response, operation key, DNS addresses, redirects, and private authority.

The immutable SQLite snapshot remains part of the Run audit record so later review
can distinguish the bytes observed at fetch time from a changed origin. Snapshots
become visibly stale after 24 hours; stale does not rewrite or delete the original
fact. A citation links to the original public source while retaining its snapshot
digest and timestamp.

## Legal, privacy, and operational constraints

The operator remains responsible for the configured SearXNG instance's terms and
the terms applicable to each target site. Traverse Board identifies itself with a
bounded user agent and records the applicable `robots.txt` outcome. Conservative,
Workspace, and Approval enforce that outcome; Full Access and Debug retain it as an
audit fact without making disallow or indeterminate status a fetch blocker. All
modes store only bounded evidence needed for the Run and expose the original source
link and fetch time. The audit-only Full Access/Debug behavior does not relax SSRF,
private/loopback/metadata, DNS-rebinding, HTTPS, redirect, response-size, or timeout
boundaries. Traverse Board does not log in, bypass paywalls, solve CAPTCHAs, accept
browser profiles, submit forms, execute downloads, or claim that either robots
permission or a robots override replaces copyright, license, privacy, or
contractual obligations.

The parser cannot determine whether reuse of a page is legally permitted. Operators
must avoid fetching personal, confidential, or restricted material and must manage
retention of the Run database under their own policy. Search-provider and target
rate limits remain authoritative; the client performs no evasive rotation or
multi-provider retry.

## Consequences and residual risks

Search and fetch are deliberately separate, so a result stub cannot become a
citation without a controlled fetch. A partial PDF or truncated page remains useful
but visibly weaker evidence. Dynamic client-rendered pages may yield little text;
v1 does not launch a browser as a fallback.

### Provider-grounded hosted-search amendment (2026-09-02)

The discovery-only rule above remains true for SearXNG and every ordinary search
adapter. A qualified hosted-search adapter may now emit an immutable
`provider_grounded_citation.v1` inside the durable `web_search` operation. The
record binds Run, Source, canonical URL, title, Provider identity, exact
credential-free Provider/search selection fingerprint, search time, and its own
fingerprint. It is always `provider_qualified=true`, `locally_verified=false`,
`untrusted=true`, and `instruction_authorized=false`.

Such a URL may be cited in an ordinary answer without a second `web_fetch`, but
must be presented as Provider-grounded rather than locally verified. It creates
neither a `web_evidence_snapshot` nor a snapshot-backed `web_citation`; deeper
page reading and independent verification still use `web_fetch` and the existing
snapshot citation path. Existing v134 tables require no migration because the
new record is part of the already immutable, bounded operation response.

Hosted search also uses an exact Provider API egress boundary derived by Go from
the selected model route. This boundary is independent from direct Web fetch
authority: the Provider API hostname is not added to the Run's page-fetch
allowlist, direct-network disablement does not disable an otherwise eligible
hosted search, and hosted search does not authorize fetching any returned URL.

DNS and redirect checks greatly reduce SSRF and rebinding exposure but do not make
remote content trustworthy. A public endpoint may proxy private data, return
malicious text, or change after fetch. TLS trust still depends on the host trust
store, SearXNG quality depends on the configured instance, and the conservative PDF
extractor does not provide full semantic fidelity. These are evidence-quality risks,
not authority grants.

## Verification

Go tests cover URL normalization, IPv4/IPv6 private and metadata rejection, mixed
DNS answers, pinned dialing, redirect revalidation and limits, bounded retry,
robots enforcement in Conservative/Workspace/Approval and audit-only behavior in
Full Access/Debug, controlled MIME/parser behavior, truncation, immutable Run-scoped
storage, idempotent replay/conflict, failed/blocked/partial snapshots, same-Run
citation, and stale projection. Gateway/Application tests cover closed
schemas, capability absence, durable authority, and generation/revision rechecks.
HTTP, CLI, transcript, React, CSS, OpenAPI, and generated-TypeScript tests assert the
same source metadata and absence of page bodies, snippets, claims, credentials, and
private authority.

## 中文结论

`web_search` 只创建不可引用的发现记录，`web_fetch` 在 Run 级 allowlist、SSRF/DNS/重定向、
大小、MIME 与解析器边界内生成不可变快照。Conservative、Workspace 与 Approval 强制执行
robots policy；Full Access 与 Debug 仍记录 robots 审计事实，但 disallow、缺失或无法确认不会阻断
抓取。该审计绕过不会放松私网、loopback、云 metadata、DNS rebinding、HTTPS、重定向、大小或
超时硬边界。`web_citation` 只能引用同一 Run 中已成功或部分抓取的快照。默认网络关闭；未配置
SearXNG 时不提供搜索，也不会回退到浏览器、Shell 或其他 Provider。

网页内容始终是 `untrusted`、`instruction_authorized=false` 的证据。Thread、CLI 与 HTTP 共用
同一 Go 投影，只公开链接、标题、状态、抓取时间与摘要，不公开网页正文、搜索片段或引用 claim。
robots 允许或在 Full Access/Debug 中被作为审计事实绕过，都不等于版权、许可或合同授权；操作者
仍需对目标站点条款、隐私与 Run 数据保留负责。
