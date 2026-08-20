# GitHub Review Provider / GitHub 审阅集成

Schema v124 adds the default-off `github-review-provider.v1` for Code Surface review work. It is a GitHub-specific evidence and write-back adapter behind the existing Go control plane; it is not a browser-side SDK and it does not give the model a GitHub token, generic HTTP client, Git shell, or remote mutation authority.

Schema v124 新增默认关闭的 `github-review-provider.v1`，用于 Code Surface 的审阅工作。它是既有 Go 控制面之后的 GitHub 证据与回写适配器；不是浏览器端 SDK，也不会向模型提供 GitHub token、通用 HTTP、Git Shell 或远端写权限。

## Official integration shape / 官方集成形态

Prayu prefers a GitHub App with Device Flow enabled. GitHub recommends GitHub Apps over OAuth Apps for fine-grained permissions, short-lived user tokens, installation scoping, and centralized webhooks. Desktop/CLI Device Flow uses the public App client ID; the device code stays in process memory and the resulting access/refresh bundle is written only to the operating-system credential store.

Prayu 优先采用开启 Device Flow 的 GitHub App。GitHub 官方建议新集成优先使用 GitHub App，以获得细粒度权限、短期 user token、installation Scope 与集中 webhook 能力。Desktop/CLI 只使用公开 Client ID；device code 仅保留在进程内，access/refresh bundle 只写入操作系统凭据库。

Official references:

- [Choosing a GitHub App or OAuth App](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/deciding-when-to-build-a-github-app)
- [Generating a user access token with Device Flow](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
- [GitHub REST API versions](https://docs.github.com/en/rest/about-the-rest-api/api-versions)
- [GitHub Developer Program](https://docs.github.com/en/integrations/concepts/github-developer-program)

The provider pins `X-GitHub-Api-Version: 2026-03-10`, `Accept: application/vnd.github+json`, `User-Agent`, bounded responses, and the fixed hosts `github.com` / `api.github.com`. Credential-bearing API/OAuth redirects are not followed across origins. A single Actions log redirect is accepted only when its exact signed download host was predeclared in the connection, and the GitHub credential is stripped before following it. GitHub Enterprise Server is intentionally unavailable in v1.

## GitHub App setup / GitHub App 配置

Create an organization- or user-owned GitHub App, enable Device Flow, and start with these repository permissions:

| Permission | Minimum | Used for |
|---|---:|---|
| Metadata | Read | repository identity and installation qualification |
| Pull requests | Read | PR metadata, files, reviews, comments and threads |
| Checks | Read | check suites and check runs |
| Actions | Read | workflow runs/jobs, bounded failed logs and Artifact metadata |
| Pull requests | Write | replies, resolve/unresolve, submitted reviews and reviewer requests |
| Contents | Read | compare/merge-base evidence used to bind the PR snapshot |
| Contents | Write only in the separate typed Git workflow | branch push; not used by model evidence tools or review-comment write-back |

Install the App only on the repositories Prayu should inspect. Record the public client ID, never the client secret, in the connection. SSO-required organizations must authorize the installed App/token before qualification becomes eligible. Fine-grained PAT and existing OAuth-user credential references remain supported for read-only migration, but their effective write scopes cannot be proven from repository collaborator permissions and therefore never enable write-back in v1. GitHub App Device Flow is the production write path.

## CLI / CLI 操作

All feature entry points are explicit and default closed:

```powershell
cyberagent github-review configure --repository owner/repo `
  --credential prayu-github-app --auth github_app_device --client-id Iv1.example `
  --allow-write --enable-github-review --enable-permission-control --confirm

cyberagent github-review login --connection <connection-id> `
  --enable-github-review --enable-permission-control

cyberagent github-review qualify --connection <connection-id> --pr 118 `
  --enable-github-review --enable-permission-control
cyberagent github-review fetch --connection <connection-id> --pr 118 `
  --enable-github-review --enable-permission-control
cyberagent github-review evidence --run <run-id> --snapshot <snapshot-id> `
  --enable-github-review --enable-permission-control
cyberagent github-review status --run <run-id> --connection <connection-id> --pr 118 `
  --enable-github-review --enable-permission-control
```

For a temporary fine-grained PAT, import from an environment variable without placing the value in argv, JSON, SQLite, events, or output:

```powershell
$env:PRAYU_GITHUB_TOKEN = "<temporary-token>"
cyberagent github-review credential set prayu-review --from-env PRAYU_GITHUB_TOKEN --confirm
Remove-Item Env:PRAYU_GITHUB_TOKEN
```

Omit `--allow-write` for a read-only connection. `write reply|resolve|unresolve|submit_review|request_reviewer` is unreachable unless that connection-level gate is enabled; it then renders an exact preview. `--confirm` creates and consumes a distinct one-time Approval, after which the service rechecks the Code/Deliver Run, network permission, repository/PR/base/head/merge-base identity, credential reference, capability generation and remote state. Branch push and PR create/update remain the existing typed Git remote workflow and retain their own review/permission receipts; they are not silently folded into a review-comment Approval.

## Evidence contract / 证据合同

Fetch stores immutable sanitized snapshots: PR identity/body, all bounded changed-file pages, reviews, comments and threads, check suites/runs, workflow jobs, bounded failed-log excerpts and Artifact metadata. Every remote string is untrusted data. Credentials, request headers, raw responses, redirect URLs and unrestricted logs are never persisted.

`evidence` binds the snapshot to the exact local repository, merge-base, HEAD, index/worktree/status hashes, complete diff, stable hunks, conflicts and optional trusted LSP facts. Its state is one of:

- `verified`: required pages and local mapping are complete and current;
- `partial`: bounded omissions exist;
- `stale`: local or remote identity drifted;
- `unavailable`: a required source could not be obtained;
- `not_run`: no observation was performed.

The model receives only `github_review_evidence_list` and `github_review_evidence_read`, scoped to the current Code/root Run and Workspace. They are read-only, local, lease-fenced, redacted and bounded. They cannot fetch GitHub or perform write-back.

## HTTP and Desktop / HTTP 与 Desktop

Start API/Desktop with `--enable-github-review` plus permission control and an operator-approval capability. Read bearer routes list non-secret connections and Run projections; control bearer routes configure, start/poll Device Flow, disconnect credentials, qualify/fetch, build evidence, preview writes and execute approved writes. The committed [OpenAPI document](openapi.json) is canonical.

The Desktop Repository page shows account/repository status, Device Flow, a bounded PR inbox selected by exact number, threads/checks/jobs, failed logs, stale mappings, pending approvals and terminal receipts. Renderer memory never receives a GitHub token.

## Failure and recovery / 失败与恢复

Offline, rate-limit, SSO, missing installation, private/fork permission, pagination drift, malformed response, source drift and cancellation have stable diagnostic/failure codes. A write persists `proposed` before approval and `running` before network I/O. Hidden idempotency markers allow observation-only startup recovery: Prayu may recognize an already-created reply/review/thread state, but it never repeats a remote mutation during recovery. If no exact marker/state is provable, the operation terminates as `interrupted_no_receipt`.

## Real smoke and Developer Program / 真实联调与开发者计划

Mock/replay and fault-injection tests run without credentials. A real smoke must be opt-in, target a disposable repository/PR, use a dedicated installation, avoid secrets in CI output, and clean up only artifacts it created. Record App ID/installation ID, repository, PR, API version, command revision and receipt IDs—never tokens.

The [GitHub Developer Program](https://docs.github.com/en/integrations/concepts/github-developer-program) accepts teams with a production integration or developers actively building with GitHub APIs plus a support email. It is separate from [GitHub Marketplace listing requirements](https://docs.github.com/en/apps/github-marketplace/creating-apps-for-github-marketplace/requirements-for-listing-an-app). Prayu can technically apply during development; the recommended application gate is: #118 merged, a publicly installable GitHub App, support/privacy/security URLs, a real smoke receipt, least-privilege documentation, and a stable public callback/support surface. Marketplace submission is a later, independent release decision.
