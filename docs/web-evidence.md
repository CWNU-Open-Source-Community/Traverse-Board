# Web Evidence / Web 证据

这里说明搜索配置、公开来源读取与引用。实际可用性取决于所选供应商、网络及当前任务权限；[v1.0.0 发布说明](releases/v1.0.0.md)单独记录用户链路的验收状态。

## 当前搜索配置 / Current search setup

在桌面 **设置 → 模型** 中连接供应商，在其搜索设置中选择策略：

| 策略 / Strategy | 行为 / Behavior |
| --- | --- |
| 自动 / Auto | 优先使用已声明且通过验证的供应商原生搜索；否则只回退到已配置的 SearXNG。Prefers qualified provider-native search; falls back only to configured SearXNG. |
| 供应商原生 / Provider native | 使用当前模型服务的原生搜索能力；兼容 Responses API 本身不足以证明支持 `web_search`。Requires actual native search support, not just Responses API compatibility. |
| SearXNG | 使用下方显式配置的服务。Uses the explicitly configured endpoint below. |
| DuckDuckGo | 需要明确选择，可能遇到机器人验证；不会静默启用。Explicit selection only; bot challenges can make it unavailable. |

原生搜索首次真实调用仍需验证，可能产生供应商费用。连接诊断失败、额度不足、搜索能力不支持与网页抓取失败是不同原因，应按界面提示分别处理。连接了模型不等于搜索已经可用。

Configure the strategy under **Settings → Models** for the selected provider. Initial native-search validation can incur provider charges. Model connectivity, available quota, search support, and page retrieval are separate checks.

### GitHub、Hacker News 与 RSS/Atom

`source_search` 搜索 GitHub/Hacker News；`web_fetch` 读取 GitHub 公开 Issue/PR 正文及 issue comments（不含 PR review comments）、Hacker News 及最多四层评论，并通过显式 `connector=rss` 读取 RSS/Atom feed。RSS 是读取订阅源，不是全网搜索。来源内容进入快照与引用路径；没有登录平台或视频检索能力的承诺。

These connectors cover public GitHub issue/PR bodies and issue comments, Hacker News with up to four comment levels, and explicit RSS/Atom feeds. RSS is a feed reader, not a general search engine. Retrieved sources use the existing snapshot and citation paths.

底层 Web 证据协议从 schema v134 开始，来源连接器在 v165 接入；架构与边界见 [ADR 0137](adr/0137-go-owned-web-evidence.md)。以下是 SearXNG 和受控任务的高级配置说明。

## Configure SearXNG

The SearXNG backend uses one operator-configured JSON endpoint. The endpoint must
be a public HTTPS URL on port 443 and must support the documented JSON search API.
See the upstream [SearXNG Search API](https://docs.searxng.org/dev/search_api.html)
for instance-side configuration, including enabling JSON output.

```powershell
$env:CYBERAGENT_WEB_SEARCH_ENDPOINT = "https://search.example.org/search"
```

The request uses `GET` with `q`, `format=json`, and `safesearch=1`. Traverse Board
sends no search credential or cookie. If the variable is unset or invalid, this
backend is unavailable. Other explicitly selected search backends have their own
readiness checks. There is no built-in public SearXNG instance, and a SearXNG failure
does not silently enable DuckDuckGo, browser, shell, or another paid provider.

## Create an opted-in Run

This constrained CLI example creates a Run with an explicit network allowlist.
Include the SearXNG host and every destination host that fetch may contact:

```powershell
cyberagent workspace init web-research
cyberagent run create "Review the public specification" `
  --workspace web-research `
  --profile review `
  --network allowlist `
  --allow-target search.example.org `
  --allow-target docs.example.org
```

An exact host, an HTTPS origin such as `https://docs.example.org`, and a wildcard
suffix such as `*.docs.example.org` are accepted. `public_https` explicitly allows
all destinations that pass the public-HTTPS and DNS checks; prefer exact targets
when the source set is known. A wildcard excludes the suffix apex. Targets are
limited to 256 and port 443.

The Run/Mission/Session and Workspace identity, mode and permission revisions,
allowed targets, Provider, and
capability generation are rechecked before every call. Updating any of them
invalidates old call authority. `--allow-target` with `--network disabled` is
rejected.

## Evidence workflow

The model-facing workflow is intentionally ordered:

1. `web_search` returns up to ten results. Ordinary title/snippet/URL stubs are
   untrusted discovery hints, not citations. A qualified hosted provider can also
   return explicitly marked `provider_grounded` citations; those may be cited with
   provider provenance, without claiming they are locally verified snapshots.
2. `web_fetch` accepts one returned `source_id` or one authorized URL. It evaluates
   robots according to the current permission mode, fetches and parses the source,
   and creates an immutable snapshot.
3. `web_citation` accepts a same-Run `source_id` and `snapshot_id`, a bounded claim,
   and an optional body span. It cannot accept a caller-selected URL.

Repeated calls with the same operation key and identical input replay the original
result. Reusing a key with different input conflicts. Search results, snapshots,
citations, and operation results cannot cross Run boundaries.

| Fact | Bound |
|---|---:|
| Search query | 1,024 Unicode code points |
| Search results | 10 |
| Search snippet retained internally | 4 KiB |
| HTTP response | 2 MiB |
| Sanitized snapshot body | 128 KiB |
| Model-facing fetch result | 128 KiB JSON; body excerpt is shortened first and flagged separately |
| Redirects | 3 |
| DNS addresses accepted per request | 32, all of which must be public |
| Request deadline | 15 seconds |
| Transient retries | 1 for 429/502/503/504 only |
| `robots.txt` response | 256 KiB |
| Snapshot freshness | 24 hours |
| Public inventory page | 500 items per collection maximum |

Snapshot and citation status is one of `fetched`, `partial`, `stale`, `blocked`, or
`failed`. PDF extraction is always `partial`; response or parser truncation also
produces `partial`. `stale` means the 24-hour freshness window elapsed—it does not
mean the immutable bytes or digest were changed.

## Inspect sources

The CLI and authenticated HTTP endpoint return the same metadata-only
`web_evidence.v1` inventory:

```powershell
cyberagent web-evidence list --run <run-id> --limit 100
curl.exe -H "Authorization: Bearer $env:CYBERAGENT_API_TOKEN" `
  "http://127.0.0.1:8765/api/v1/runs/<run-id>/web-evidence?limit=100"
```

The Thread transcript displays the same source as a clickable external card with
title, status, fetch time, digest, and partial/stale markers. Public UI, CLI, and API
responses do not contain fetched page bodies, search snippets, citation claims,
operation keys, DNS addresses, or private authority. Every projection explicitly
reports `untrusted=true` and `instruction_authorized=false`.

## Failure and remediation

| Symptom | Meaning | Remediation |
|---|---|---|
| `web_evidence_network_disabled` | The Run has no network authority | Create a new opted-in Run with `--network allowlist` and explicit targets |
| `web_evidence_target_denied` | The search/fetch host is outside the Run allowlist | Add the exact public host when creating the Run; do not broaden an existing call |
| `web_search_provider_unavailable` | No valid SearXNG endpoint was configured | Set `CYBERAGENT_WEB_SEARCH_ENDPOINT`, restart the owning process, and include its host in the Run |
| provider request failed | SearXNG returned an error, bad JSON, or exceeded bounds | Repair the configured instance; no fallback is attempted |
| `blocked` | Robots in Conservative/Workspace/Approval, DNS/public-address checks, redirect authority, or another enforced policy boundary denied fetch | Use an authorized source that permits retrieval or choose an explicitly authorized permission mode; never bypass the hard network boundaries |
| `failed` | TLS, HTTP, MIME, charset, parser, timeout, or size handling failed | Inspect the metadata and choose a compatible public source |
| `partial` | The bounded parser retained incomplete evidence | Cite it only with the visible partial qualification or use another source |
| `stale` | The snapshot is older than 24 hours | Fetch a new immutable snapshot under a new operation key before relying on freshness |

## Security, terms, and copyright

Only public HTTPS on port 443 is expressible. URL userinfo and credential-bearing
query parameters, ambient proxies, cookies, local and private hosts, metadata
services, mixed public/private DNS answers, and unauthorized redirect targets are
rejected. Every redirect is resolved again and pinned to public addresses. These
SSRF, loopback/private/metadata, DNS-rebinding, HTTPS, redirect, response-size, and
timeout boundaries remain hard failures in every permission mode.

`robots.txt` is inspected before the initial page and every redirect destination.
Conservative, Workspace, and Approval enforce the result and fail closed when the
policy is disallowed or indeterminate. Full Access and Debug retain the robots
outcome as an audit fact, but disallow, absence, or an indeterminate result does not
block the fetch. This audit-only behavior changes neither the hard network checks
above nor the untrusted status of fetched content.

Supported content is HTML/XHTML, text/Markdown, JSON, and conservative PDF literal
text. Traverse Board does not execute scripts, PDF actions, downloads, forms, or
embedded content. It does not log in, bypass a paywall, solve a CAPTCHA, or use a
personal browser profile.

Credential-looking sequences are redacted before the bounded snapshot body is
committed, so model excerpts and optional citation spans refer to the same durable,
sanitized text. The digest still identifies the raw response bytes observed at fetch time.

Remote text can contain prompt injection and remains evidence, never instruction or
tool authority. Operators are responsible for the SearXNG and target-site terms,
copyright/licensing, privacy, and database retention. A permissive robots result
does not establish a legal right to copy or reuse content, and Full Access/Debug's
audit-only robots handling does not grant permission under copyright, licensing, or
contract law.
