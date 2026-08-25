# Web Evidence / Web 证据

Schema v134 adds a default-off, first-party path for a root Supervisor to discover,
fetch, and cite public Web sources without granting a model generic network access.
The architecture and threat boundary are recorded in
[ADR 0137](adr/0137-go-owned-web-evidence.md).

## Configure search

Search uses exactly one operator-configured SearXNG JSON endpoint. The endpoint must
be a public HTTPS URL on port 443 and must support the documented JSON search API.
See the upstream [SearXNG Search API](https://docs.searxng.org/dev/search_api.html)
for instance-side configuration, including enabling JSON output.

```powershell
$env:CYBERAGENT_WEB_SEARCH_ENDPOINT = "https://search.example.org/search"
```

The request uses `GET` with `q`, `format=json`, and `safesearch=1`. Traverse Board
sends no search credential or cookie. If the variable is unset or invalid,
`web_search` is not advertised. Fetch and citation may still be available for an
allowlisted Run. There is no built-in public SearXNG instance, HTML scraper, browser,
shell, or alternate-provider fallback.

## Create an opted-in Run

New Runs remain network-disabled unless the operator creates one with an explicit
allowlist. Include the search host if search is required and every destination host
that fetch may contact:

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

1. `web_search` returns up to ten title/snippet/URL stubs. They are discovery hints,
   `untrusted`, and not citeable.
2. `web_fetch` accepts one returned `source_id` or one authorized URL. It checks
   robots, fetches and parses the source, and creates an immutable snapshot.
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
| `blocked` | Robots, DNS/public-address checks, redirect authority, or another policy boundary denied fetch | Use an authorized source that permits retrieval; do not bypass the policy |
| `failed` | TLS, HTTP, MIME, charset, parser, timeout, or size handling failed | Inspect the metadata and choose a compatible public source |
| `partial` | The bounded parser retained incomplete evidence | Cite it only with the visible partial qualification or use another source |
| `stale` | The snapshot is older than 24 hours | Fetch a new immutable snapshot under a new operation key before relying on freshness |

## Security, terms, and copyright

Only public HTTPS on port 443 is expressible. URL userinfo and credential-bearing
query parameters, ambient proxies, cookies, local and private hosts, metadata
services, mixed public/private DNS answers, and unauthorized redirect targets are
rejected. Every redirect is resolved again and pinned to public addresses.
`robots.txt` is checked before the initial page and every redirect destination;
indeterminate policy fails closed.

Supported content is HTML/XHTML, text/Markdown, JSON, and conservative PDF literal
text. Traverse Board does not execute scripts, PDF actions, downloads, forms, or
embedded content. It does not log in, bypass a paywall, solve a CAPTCHA, or use a
personal browser profile.

Credential-looking sequences are redacted before the bounded snapshot body is
committed, so model excerpts and optional citation spans refer to the same durable,
sanitized text. The digest still identifies the raw response bytes observed at fetch time.

Remote text can contain prompt injection and remains evidence, never instruction or
tool authority. Operators are responsible for the SearXNG and target-site terms,
copyright/licensing, privacy, and database retention. `robots.txt` permission alone
does not establish a legal right to copy or reuse content.
