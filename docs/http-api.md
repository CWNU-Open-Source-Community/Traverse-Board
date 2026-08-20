# 本地 HTTP API / Local HTTP API

CyberAgent Workbench 提供由 Go 控制的本地 `api.v1`，用于检查 SQLite 持久状态并投影可恢复 Run events。独立 capability 允许受控 Run/Session/Plan/审批、固定命令提案审阅、Provider 诊断/路由/系统凭证、FileEdit 提案/只读恢复/审阅/apply、wake 意图/前台消费、不可变操作者验证、metadata-only 快照回执及其不授权复核、惰性 Skill 安装、schema v116 的 Run-owned 普通命令运行时、schema v117 的 Workspace Checkpoint 时间线/恢复/Fork、schema v118 的可交付 child Worktree/复核/本地合并、schema v119-v120 的脱敏 MCP/Plugin 控制面，以及 schema v99 默认关闭的精确 Docker Sandbox 产品执行。只读面还提供 capability/worker health、exact-root Repository 状态与脱敏 Diff、非原子的多文件 FileEdit 汇总、逐验证项确定性快照下载/回执历史和带有界复核元数据的可重建 Code Handoff。API 不直接接受 Shell/argv/stdin 或 Job mutation endpoint；命令只能由认证 Run execution 内的 root Supervisor 通过同一 Tool Gateway/Application 服务发起，仍受 Code/Local/Deliver/full-access、当前租约、Policy、无网络/无凭证与进程启动 capability 约束。

CyberAgent Workbench exposes a Go-controlled local `api.v1` for durable SQLite state and resumable Run-event projections. Independent capabilities permit controlled Run/Session/Plan/approval operations, fixed-command proposal review, Provider diagnostics/routes/system credentials, operator price-snapshot import and listing, FileEdit propose/read-only recovery/review/apply, wake intent/foreground consumption, immutable operator verification, metadata-only snapshot receipts and their non-authorizing review, inert Skill installation, the schema-v116 Run-owned ordinary command runtime, schema-v117 Workspace Checkpoint timeline/restore/Fork operations, schema-v118 deliverable-child worktree/review/local-merge operations, the redacted schema-v119-v120 MCP/Plugin control plane, and schema-v99 exact Docker Sandbox execution that is disabled by default. Read-only surfaces also expose capabilities/worker health, exact-root Repository state and redacted Diffs, non-atomic multi-file FileEdit summaries, deterministic per-check verification snapshot downloads/receipt history, and a regenerable Code handoff with bounded review metadata. There is no direct HTTP Shell/argv/stdin or Job-mutation endpoint: only an authenticated Run execution may let its root Supervisor call the same Tool Gateway/Application service under Code/Local/Deliver/full-access, current-lease, Policy, no-network/no-credential, and process-startup gates.

## 启动 / Start

省略 `CYBERAGENT_API_TOKEN` 时，进程会生成并打印一个临时只读 token。全部 control POST 默认关闭；只有设置不同的 `CYBERAGENT_API_CONTROL_TOKEN` 并启用相应 Go capability 才能访问。两个 token 都必须是 32 到 512 字节的规范 UTF-8，不能包含空白或控制字符，且不能相同；CLI 不会回显环境提供的值。

When `CYBERAGENT_API_TOKEN` is absent, the process generates and prints a temporary read token. All control POST operations are disabled by default; access requires both a distinct `CYBERAGENT_API_CONTROL_TOKEN` and the corresponding Go capability. Both tokens must be 32 to 512 bytes of normalized UTF-8 without whitespace or control characters, and they must differ. The CLI never echoes an environment-provided value.

```powershell
$env:CYBERAGENT_API_TOKEN = "<a-random-token-of-at-least-32-bytes>"
$env:CYBERAGENT_API_CONTROL_TOKEN = "<a-different-random-token-of-at-least-32-bytes>"
go run ./cmd/cyberagent api serve --listen 127.0.0.1:8765 --ui-dir web/dist

# Representative optional independent controls in the current v91 API surface.
go run ./cmd/cyberagent api serve --listen 127.0.0.1:8765 --ui-dir web/dist --enable-file-edit-proposals --enable-provider-credentials --enable-wake-worker

# Four-level permission selector. Higher gates require every lower gate.
go run ./cmd/cyberagent api serve --listen 127.0.0.1:8765 --ui-dir web/dist `
  --enable-permission-control --enable-danger-full-access `
  --enable-debug-maximum-access

# Browser-CDP permission selection. Full CDP additionally requires Debug maximum access.
go run ./cmd/cyberagent api serve --listen 127.0.0.1:8765 --ui-dir web/dist `
  --enable-permission-control --enable-danger-full-access `
  --enable-debug-maximum-access --enable-browser-cdp-control `
  --enable-full-cdp-debug

# Docker Sandbox product admission/start. This still requires an exact per-call
# Sandbox approval and a matching current Run permission/profile.
go run ./cmd/cyberagent api serve --listen 127.0.0.1:8765 --ui-dir web/dist `
  --enable-permission-control --enable-docker-execution
```

权限开关不会让数据库快照自动获得执行权。`full_access` 与当前 Code/Local/Deliver
Run、root Supervisor lease、`--enable-permission-control`、
`--enable-danger-full-access` 同时成立时，API 进程会安装 v116
`command_runtime`；它与 `approval` 的逐条审阅路径和 `debug` 用户终端彼此独立。
未设置 `CYBERAGENT_API_CONTROL_TOKEN` 时，permission control 与 Run execution
都不能启动。

设置独立 control token 后，普通 `api serve` 同时开放固定提案 review route；
Desktop 则必须额外使用 `--enable-command-proposals`。三条 endpoint 为：

```text
GET  /api/v1/runs/{run_id}/command-proposals
GET  /api/v1/runs/{run_id}/command-proposals/{proposal_id}
POST /api/v1/runs/{run_id}/command-proposals/{proposal_id}/review
```

POST 需要 control bearer、稳定 `Idempotency-Key` 和
`controlled_command_proposal_review.v1`。`approve` 必须携带
`confirm_execution=true`；`deny` 必须为 false。批准只执行提案中已编译的同一
Go 固定模板一次，不能提交 Shell、argv、env 或网络字段。一次性响应可返回最多
16 KiB 的脱敏不可信证据；后续 GET 只返回 metadata，不返回 raw stdout/stderr。

With a distinct control token, ordinary `api serve` also enables fixed-proposal
review. Desktop requires `--enable-command-proposals`. Approval requires an
exact idempotency key and `confirm_execution=true`, then revalidates all durable
bindings and current process gates before one restricted execution. No endpoint
accepts Shell, argv, environment, or network intent. The approving response may
return up to 16 KiB of redacted untrusted evidence; later reads return metadata
only.

The command prints:

```text
api_url: http://127.0.0.1:8765/api/v1
api_version: api.v1
api_token_generated: false
api_control_enabled: true
command_runtime_enabled: true
ui_url: http://127.0.0.1:8765/
ui_source: <absolute-path-to-web/dist>
ui_assets: 2
ui_digest: <sha256-bundle-digest>
api_token_source: CYBERAGENT_API_TOKEN
api_control_token_source: CYBERAGENT_API_CONTROL_TOKEN
note: the API is loopback-only; control is separately authorized and tokens are not persisted
```

`--ui-dir` 可选。设置后，Go 会在打开数据库和 listener 前校验 Vite bundle，并把 `index.html` 与 `assets/` 读成不可变内存快照；运行期间磁盘变化不会改变已服务内容。省略该选项时，根路径继续走原有 API 404/鉴权行为，不启动 Web UI。

`--ui-dir` is optional. When set, Go validates the Vite bundle before opening the database or listener and loads `index.html` plus `assets/` into an immutable in-memory snapshot. On-disk changes cannot alter the served process. Without the option, root paths retain the existing authenticated API/404 behavior and no Web UI is enabled.

普通 `api serve` 与 Windows Desktop 现在都通过已认证只读
`GET /api/v1/capabilities` 取得 Go 的精确 capability 与 worker health。React 可据此
显示独立控件，但该响应不含 token、owner、lease、Run 或私有错误，也不能启用 worker、
安装服务或授予 mutation。每条控制 route 仍独立要求 control token 和对应 Go gate。

Ordinary `api serve` and Windows Desktop now read exact Go capabilities and worker
health from authenticated `GET /api/v1/capabilities`. React may use that response to
render independent controls, but it contains no token, owner, lease, Run, or private
error and cannot enable a worker, install a service, or grant a mutation. Every control
route still requires the control token and its corresponding Go gate independently.

The process capability document reports whether the `agent-code-tools.v1` runtime
is compiled in, while `GET /api/v1/runs/{run_id}` carries the authoritative
Run-specific generation and per-tool availability/refusal snapshot. The latter is
derived by Go from persisted scope and current Workspace state; it is informational
and cannot itself authorize a model call or file mutation.

### Windows Desktop 进程内传输 / Windows Desktop In-Process Transport

Desktop 至 D1-G13/V12 复用同一 `api.v1` Handler，但不调用 `ListenAndServe`，也不绑定回环端口。Wails AssetServer 在同一进程内把 React 请求交给 Go；适配层只接受精确 `http://wails.localhost`。默认只生成内存 read token；十九个独立 flag 开放各自窄 route 或进程生命周期内的 wake worker。Repository state/Diff/history/comparison/键盘可访问成对预览、change-set、带有界审计事实的 Journey、带复核元数据和精确 Verify 导航的 Handoff，以及验证分页/快照下载/回执历史不增加 flag；回执及其复核 POST 复用 verification evidence 自己的默认关闭 flag。导航目标只驻留 React 内存并由既有严格 GET 投影复核，不增加 HTTP route。任一 control capability 会生成同一个不同于 read token 的内存 control token，未启用 route 仍返回 404。两个 token 都不写磁盘、日志、Local Storage 或注册表。

Desktop through D1-G13/V12 reuses the same `api.v1` Handler without calling `ListenAndServe` or binding a loopback port. The Wails AssetServer passes React requests to Go in process, and a narrow adapter accepts only exact `http://wails.localhost`. Nineteen independent flags expose narrow routes or the process-lifetime wake worker. Repository state/Diff/history/comparison, keyboard-accessible paired-preview navigation, change-set, Journey with bounded audit facts, Handoff with exact review navigation, verification pagination, snapshot download, and receipt history add no flag; receipt and review POST reuse the existing default-off verification-evidence control flag. The navigation target stays in React memory and is revalidated through existing strict GET projections, so no HTTP route is added. Any control capability creates one in-memory control token distinct from the read token, while disabled routes remain 404; neither token is written to disk, logs, browser storage, or the registry.

普通浏览器继续使用 `/events/stream` SSE。Wails v2 在 Windows 上不支持 AssetServer response streaming，因此 Desktop 使用 `GET /runs/{run_id}/events/poll` 做一秒有界轮询。该 endpoint 与 SSE 共用同一个绑定 Run 与 sequence 的高水位 cursor，单次最多返回 100 帧并明确给出 `has_more`；poll cursor 可续接 SSE，SSE cursor 也可续接 poll。Renderer 最多在模块内存保存 16 个 Run、每个 500 帧，重挂载继续最后 cursor，失效 cursor 每次挂载最多回退一次；不写 Local/Session Storage，也不再生成伪 cursor。它不会建立新的事件真源。原生 Wails bridge 不是通用业务 API 旁路：前三项只提供 connection bootstrap 和路径隔离 Skill 选择/预览，第四项只消费 Go 发放的一次性确认句柄并调用惰性 Registry。

Ordinary browser clients keep `/events/stream` SSE. Wails v2 does not support AssetServer response streaming on Windows, so Desktop polls `GET /runs/{run_id}/events/poll` at a bounded one-second interval. That endpoint shares the SSE Run/sequence high-water cursor, returns at most 100 frames plus explicit `has_more`, and permits cursor interchange in both directions. The renderer retains at most 16 Runs and 500 frames per Run in module memory, resumes the last cursor after remount, and resets a stale cursor at most once per mount. It writes neither Local nor Session Storage and no longer invents synthetic cursors. This creates no new event source. The native Wails bridge is not a general business-API bypass: its fourth method accepts only Go's one-time inert-install confirmation handle; Run, Diff, and wake mutations still use the authenticated Go HTTP Handler.

```powershell
$headers = @{ Authorization = "Bearer $env:CYBERAGENT_API_TOKEN" }
Invoke-RestMethod http://127.0.0.1:8765/api/v1/health -Headers $headers
Invoke-RestMethod http://127.0.0.1:8765/api/v1/capabilities -Headers $headers
Invoke-RestMethod "http://127.0.0.1:8765/api/v1/runs?limit=20" -Headers $headers
Invoke-RestMethod "http://127.0.0.1:8765/api/v1/workspaces?limit=20" -Headers $headers
Invoke-WebRequest http://127.0.0.1:8765/api/v1/openapi.json -Headers $headers
curl.exe -N -H "Authorization: Bearer $env:CYBERAGENT_API_TOKEN" http://127.0.0.1:8765/api/v1/runs/<run-id>/events/stream
$controlHeaders = @{ Authorization = "Bearer $env:CYBERAGENT_API_CONTROL_TOKEN"; "Idempotency-Key" = "cancel-<stable-operation-id>" }
$body = @{ attempt_id = "<active-attempt-id>"; model_attempt = 1; reason = "operator stop" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/active-call/cancel -Headers $controlHeaders -ContentType application/json -Body $body
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/agents/<agent-id>/active-call/cancel -Headers $controlHeaders -ContentType application/json -Body $body
$controlHeaders["Idempotency-Key"] = "profile-<stable-operation-id>"
$profileBody = @{ profile = "docker"; reason = "prefer isolated execution" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/execution-profile -Headers $controlHeaders -ContentType application/json -Body $profileBody
$controlHeaders["Idempotency-Key"] = "create-run-<stable-operation-id>"
$createBody = @{ version = "run_creation.v1"; goal = "Implement parser"; workspace_id = "<workspace-id>"; profile = "code"; surface = "code"; phase = "deliver" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs -Headers $controlHeaders -ContentType application/json -Body $createBody
$controlHeaders["Idempotency-Key"] = "session-message-<stable-operation-id>"
$messageBody = @{ version = "session_message_submission.v1"; content = "continue with the reviewed change" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/sessions/<session-id>/messages -Headers $controlHeaders -ContentType application/json -Body $messageBody
$controlHeaders["Idempotency-Key"] = "cancel-message-<stable-operation-id>"
$messageCancelBody = @{ version = "session_steering_cancellation.v1" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/sessions/<session-id>/messages/<message-id>/cancel -Headers $controlHeaders -ContentType application/json -Body $messageCancelBody
$controlHeaders["Idempotency-Key"] = "run-start-<stable-operation-id>"
$lifecycleBody = @{ version = "run_lifecycle_control.v1"; action = "start" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/lifecycle -Headers $controlHeaders -ContentType application/json -Body $lifecycleBody
$controlHeaders["Idempotency-Key"] = "run-execute-<stable-operation-id>"
$executeBody = @{ version = "run_execution_handoff.v1"; max_steps = 4 } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/execute -Headers $controlHeaders -ContentType application/json -Body $executeBody
Invoke-RestMethod http://127.0.0.1:8765/api/v1/models -Headers $headers
$controlHeaders["Idempotency-Key"] = "plan-direction-<stable-operation-id>"
$planBody = @{ version = "plan_delivery_control.v1"; proposal_id = "<proposal-id>"; direction = 2 } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/plan/direction -Headers $controlHeaders -ContentType application/json -Body $planBody
$controlHeaders["Idempotency-Key"] = "plan-deliver-<stable-operation-id>"
$deliverBody = @{ version = "plan_delivery_control.v1" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/plan/deliver -Headers $controlHeaders -ContentType application/json -Body $deliverBody
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/approvals -Headers $headers
$controlHeaders["Idempotency-Key"] = "approval-<stable-operation-id>"
$approvalBody = @{ version = "approval_control.v1"; action = "approve_once" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/approvals/<approval-id>/decision -Headers $controlHeaders -ContentType application/json -Body $approvalBody
$routeBody = @{ version = "model_route_control.v1"; provider = "mock"; model = "mock-code" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/models/routes/code -Headers $controlHeaders -ContentType application/json -Body $routeBody
$diagnosticBody = @{ version = "provider_diagnostic.v1"; provider = "mock"; model = "mock-code"; confirm_diagnostic = $true } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/models/diagnostics -Headers $controlHeaders -ContentType application/json -Body $diagnosticBody
Invoke-RestMethod http://127.0.0.1:8765/api/v1/models/credentials -Headers $headers
$credentialBody = @{ version = "provider_credential.v1"; action = "set"; secret = "<ephemeral-provider-key>"; confirm = $true } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/models/credentials/mimo -Headers $controlHeaders -ContentType application/json -Body $credentialBody
$source = Invoke-RestMethod "http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edit-proposal-source?path=README.md" -Headers $headers
# Rotate an expired handle only if the Workspace file still matches the old digest.
$source = Invoke-RestMethod "http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edit-proposal-source?path=README.md&expected_sha256=$($source.data.sha256)" -Headers $headers
$proposalBody = @{ version = "file_edit_proposal.v1"; source_handle = $source.data.source_handle; proposed_text = "replacement text" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edit-proposals -Headers $controlHeaders -ContentType application/json -Body $proposalBody
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edits -Headers $headers
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edit-change-set -Headers $headers
Invoke-RestMethod http://127.0.0.1:8765/api/v1/workspaces/<workspace-id>/repository-state -Headers $headers
Invoke-RestMethod http://127.0.0.1:8765/api/v1/workspaces/<workspace-id>/repository-diff -Headers $headers
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/verification-evidence -Headers $headers
$controlHeaders["Idempotency-Key"] = "verification-<stable-operation-id>"
$verificationBody = @{ version = "operator_verification_evidence.v1"; outcome = "pass"; title = "Focused tests"; summary = "Operator observed the focused test suite passing." } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/verification-evidence -Headers $controlHeaders -ContentType application/json -Body $verificationBody
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/code-handoff -Headers $headers
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edit-proposal-recovery/<edit-id> -Headers $headers
$controlHeaders["Idempotency-Key"] = "review-edit-<stable-operation-id>"
$reviewBody = @{ version = "file_edit_review.v1"; action = "approve_intent" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edits/<edit-id>/review -Headers $controlHeaders -ContentType application/json -Body $reviewBody
$controlHeaders["Idempotency-Key"] = "apply-edit-<stable-operation-id>"
$applyBody = @{ version = "file_edit_apply.v1" } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/file-edits/<edit-id>/apply -Headers $controlHeaders -ContentType application/json -Body $applyBody
Invoke-RestMethod http://127.0.0.1:8765/api/v1/runs/<run-id>/wake-intent -Headers $headers
$controlHeaders["Idempotency-Key"] = "wake-<stable-operation-id>"
$wakeBody = @{ version = "run_wake_control.v1"; max_attempts = 3; initial_delay_seconds = 0; base_backoff_seconds = 5; max_backoff_seconds = 60; max_elapsed_seconds = 300 } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/wake-intent -Headers $controlHeaders -ContentType application/json -Body $wakeBody
$controlHeaders["Idempotency-Key"] = "consume-wake-<stable-operation-id>"
$consumeBody = @{ version = "run_wake_consumer.v1"; max_steps = 1 } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/runs/<run-id>/wake-intent/consume -Headers $controlHeaders -ContentType application/json -Body $consumeBody
$archive = [Convert]::ToBase64String([IO.File]::ReadAllBytes("<package.zip>"))
$controlHeaders["Idempotency-Key"] = "install-skill-<stable-operation-id>"
$installBody = @{ version = "skill_package_installation.v1"; archive_base64 = $archive; surface = "code"; confirm_untrusted = $true } | ConvertTo-Json
Invoke-RestMethod -Method Post http://127.0.0.1:8765/api/v1/skills/packages/install -Headers $controlHeaders -ContentType application/json -Body $installBody
```

`Ctrl+C` cancels the command context and performs a bounded graceful shutdown.

## 安全边界 / Security Boundary

- Listener、HTTP `Host` 与客户端地址都必须是 loopback；`0.0.0.0`、空 host 和公网客户端会被拒绝。
- 每个 `/api` 请求必须有且只有一个正确的 `Authorization: Bearer <token>`。GET 与 Docker readiness POST 使用 read token；所有控制 POST 只接受不同的 control token，两种凭据不能互换。Web 静态请求匿名可读，并明确拒绝 Authorization header，避免 bearer 被意外发送到资源路径。
- 普通读取只接受无 body 的 `GET`；Docker readiness 是唯一带严格 `{plan_id,manifest}` body 的 read-bearer POST。其他 POST 只接受契约列出的精确控制；没有 CORS 响应头或浏览器跨源授权。
- 启用 UI 时，只在非 `/api` 命名空间接受无 query、无 body 的 `GET`/`HEAD`。HTML 使用 `no-store`；仅允许类型且文件名带哈希的资源使用一年 immutable cache。bundle 的根目录、`assets/`、软链接、文件类型、数量、单文件/总大小与 SPA fallback 深度均受限。
- UI 与 API 共享 loopback、Host、客户端地址、request-target 和规范路径校验。UI 响应使用无 `unsafe-inline`/`unsafe-eval` 的 CSP、同源 opener/resource policy、`nosniff`、`DENY` frame policy 和禁用敏感浏览器能力的 Permissions Policy。
- request target 最大 8 KiB，query 最大 4 KiB，response 最大 8 MiB，header 上限为 32 KiB。
- HTTP handler 构造后只保留两个 token 的 SHA-256 摘要；明文仍可能存在于启动环境或短期进程内存，但不会写入配置、SQLite 或 Run events。
- Artifact API 只返回 descriptor，不读取或返回正文；Run detail 不返回 checkpoint pending input 或 execution fencing token。租约摘要仅包含 owner、generation、状态与时间。
- read token 可以读取该进程暴露的全部只读资源并评估 Docker readiness；control token 只能调用生成契约中的窄 mutation，不能读取资源。两者都应视为本地管理员凭据。
- 取消请求必须精确绑定 Run/Supervisor/model attempt，或 Run/Specialist Agent/AgentAttempt/model attempt，并携带 16 到 256 字节的 `Idempotency-Key`。客户端不能提交 `lease_id`、generation 或 fencing token；请求 body 上限为 4 KiB，未知字段和尾随 JSON 会被拒绝。
- Session message 请求必须把 path Session 精确绑定到 running/paused Run，使用 `session_message_submission.v1`、1-16384 UTF-8 字节正文和 16-256 字节幂等键。编码 JSON body 上限为 128 KiB，以容纳合法转义；重复/未知字段、尾随数据、非法 UTF-8、query 和重复 header 均被拒绝。响应不返回正文或私有身份。
- Session 取消必须精确绑定 path Session/消息及其 Run，且仅在消息仍为 pending、未 prepared 时接受。生命周期只接受 `start|pause|resume`；有界执行只接受 `max_steps=1..8`，冻结选择后使用私有 lease。两者的响应都不返回正文、模型输出、工具参数或 lease 身份。
- Plan direction 必须绑定 path Run、已持久化 proposal 和 `direction=1..3`；Deliver 必须已有选择。Provider credential 只接受 exact provider、显式确认、2,560-byte 上限并固定不回传明文；候选 Registry/route/credential 全部成功后才原子推进 generation，失败保留旧 generation。FileEdit source 只发给 exact running Run/active Session 的完整安全 UTF-8，五分钟 handle 只创建 pending proposal；带 `expected_sha256` 的换发必须匹配当前文件，recovery 只返回不可编辑 pending Diff。Verification evidence 只记录脱敏操作者观察，不运行命令，也不构成模型断言、审批或授权。控制响应不能携带或设置通用进程、Shell、Session Grant 或 capability authority；Docker 控制只能消费服务端已经精确准入的 v99 admission，FileEdit apply 只能写一个已审批且重新复核的精确目标。
- SSE 使用同一 Authorization header，token 不进入 URL、cursor 或事件数据。默认最多同时 16 条 stream；每条连接最多 32-event 批量、2 MiB 单帧、10,000 events、5 分钟寿命，并对每次写入设置 2 秒 deadline。
- Event poll 只接受 query `cursor` 与 1-100 的 `limit`，拒绝 `Last-Event-ID`、跨 Run cursor、gap 和未知参数；空批次仍返回可继续使用的高水位 cursor，读取本身不写事件。

- The listener, HTTP `Host`, and client address must all be loopback. `0.0.0.0`, an empty host, and public clients are rejected.
- Every `/api` request must contain exactly one valid `Authorization: Bearer <token>` header. GET and the Docker readiness POST use the read token; all control POST routes accept only the distinct control token. The credentials are not interchangeable. Static Web requests are anonymous and explicitly reject authorization headers so a bearer is not accidentally sent to an asset path.
- Ordinary reads accept only bodyless `GET`; Docker readiness is the sole read-bearer POST and accepts only strict `{plan_id,manifest}` JSON. Other POST routes accept only their exact generated control contracts. There are no CORS response headers or browser cross-origin grants.
- When the UI is enabled, only queryless, bodyless GET/HEAD requests outside the reserved `/api` namespace reach it. HTML is `no-store`; only allowlisted, hash-named assets receive a one-year immutable cache. Bundle roots, `assets/`, symlinks, types, counts, per-file/aggregate size, and SPA-fallback depth are bounded.
- UI and API requests share the loopback, Host, client-address, request-target, and canonical-path boundary. UI responses add a CSP without `unsafe-inline` or `unsafe-eval`, same-origin opener/resource policies, `nosniff`, frame denial, and a Permissions Policy disabling sensitive browser features.
- Request targets are capped at 8 KiB, queries at 4 KiB, responses at 8 MiB, and headers at 32 KiB.
- After construction, the HTTP handler retains only SHA-256 digests of both tokens. Plaintext may still exist in the launch environment or short-lived process memory, but is never written to configuration, SQLite, or Run events.
- Artifact routes return descriptors only and never load content. Run detail omits checkpoint pending input and the execution fencing token; its lease summary contains only owner, generation, status, and timestamps.
- The read token can inspect every exposed read resource and evaluate Docker readiness; the control token can invoke only generated narrow mutations and cannot read resources. Treat both as local administrator credentials.
- Cancellation must bind either the exact Run/Supervisor/model attempt or the exact Run/Specialist Agent/AgentAttempt/model attempt and carry a 16-to-256-byte `Idempotency-Key`. Clients cannot submit a lease id, generation, or fencing token. The JSON body is capped at 4 KiB; unknown fields and trailing JSON are rejected.
- Session-message requests must bind the path Session to an exact running or paused Run and use `session_message_submission.v1`, 1-16384 UTF-8 content bytes, and a 16-to-256-byte idempotency key. The encoded JSON body is capped at 128 KiB to permit valid escaping; duplicate/unknown fields, trailing data, invalid UTF-8, query fields, and duplicate headers are rejected. The response returns neither content nor private identities.
- Session cancellation binds the exact path Session/message and Run and is accepted only while the message is pending and unprepared. Lifecycle accepts only `start|pause|resume`; bounded execution accepts only `max_steps=1..8` and uses a private lease after freezing its selection. Neither response exposes content, model output, tool arguments, or lease identity.
- Plan direction binds the path Run, persisted proposal, and `direction=1..3`; Deliver requires an existing selection. Provider credential control accepts an exact provider, explicit confirmation, and at most 2,560 secret bytes and never returns plaintext; a generation advances atomically only after candidate Registry, routes, and credential reads all succeed, otherwise the old generation remains active. FileEdit source is restricted to complete safe UTF-8 for an exact running Run/active Session; its five-minute handle can create only a pending proposal. Reissue with `expected_sha256` must match the current file, and recovery returns only a non-editable pending Diff. Verification evidence records only a redacted operator observation and neither runs a command nor becomes a model assertion, approval, or grant. Verification association exact-binds one earlier plan item and one later observation but does not infer an aggregate outcome. Control responses grant no general filesystem, process, Shell, Docker, Session-Grant, tool, or capability authority; only the separate apply route may write one exact approved and freshly rechecked file.
- SSE uses the same Authorization header; the token never enters the URL, cursor, or event data. Defaults allow at most 16 concurrent streams, 32 events per batch, 2 MiB per frame, 10,000 events per connection, a five-minute lifetime, and a two-second deadline on each write.
- Event polling accepts only query `cursor` and a 1-100 `limit`; it rejects `Last-Event-ID`, cross-Run cursors, sequence gaps, and unknown parameters. An empty batch still returns a reusable high-water cursor, and polling itself writes no event.

## Docker Sandbox 产品接口 / Docker Sandbox Product API

这五条 route 都是同一个 `DockerSandboxService` 的投影，不是第二套 Docker
控制面：

| Method | Path | Token | Meaning |
|---|---|---|---|
| `POST` | `/api/v1/sandbox/docker/readiness` | read | 对 exact plan/Manifest 做无变更、无缓存的 `sandbox.readiness.v1` 检查 |
| `POST` | `/api/v1/sandbox/docker/admissions` | control | 重新验证当前 Profile、权限、精确审批、Policy、预算、readiness 与进程 epoch |
| `POST` | `/api/v1/sandbox/docker/starts` | control | 同步消费一个 exact admission，执行、收集 I/O、提交允许的输出并清理 |
| `POST` | `/api/v1/sandbox/docker/cancellations` | control | 先持久化 sticky cancellation，再取消/接管并收敛清理 |
| `GET` | `/api/v1/sandbox/docker/status?admission_id=...` | read | 返回 `admitted|launched|terminal` 与有界终态投影 |

Readiness 只接受 strict JSON `{plan_id,manifest}`，不能含 `requested_by`、query、
Docker endpoint、flag、host path 或 image override。三个 control POST 都要求
exactly one control bearer、`Content-Type: application/json` 和独立的
`Idempotency-Key`。Admission、Start、Cancellation 各自在自己的 operation domain
内 exact replay；同一 admission 不能用第二个不同 Start key 重新绑定。Status 只接受
一个 `admission_id` query 且无 body。所有响应与 readiness 都使用 `no-store`。

```powershell
$readHeaders = @{ Authorization = "Bearer $env:CYBERAGENT_API_TOKEN" }
$controlHeaders = @{ Authorization = "Bearer $env:CYBERAGENT_API_CONTROL_TOKEN" }
$manifest = Get-Content -Raw "<manifest.json>" | ConvertFrom-Json
$plan = "<docker-plan-id>"
$operator = "cli_operator"

$readinessBody = @{ plan_id = $plan; manifest = $manifest } |
  ConvertTo-Json -Depth 20
Invoke-RestMethod -Method Post `
  http://127.0.0.1:8765/api/v1/sandbox/docker/readiness `
  -Headers $readHeaders -ContentType application/json -Body $readinessBody

$controlHeaders["Idempotency-Key"] = "docker-admit-<stable-id>"
$admissionBody = @{
  plan_id = $plan; manifest = $manifest; requested_by = $operator
} | ConvertTo-Json -Depth 20
$admission = Invoke-RestMethod -Method Post `
  http://127.0.0.1:8765/api/v1/sandbox/docker/admissions `
  -Headers $controlHeaders -ContentType application/json -Body $admissionBody
$admissionID = $admission.data.admission_id

$controlHeaders["Idempotency-Key"] = "docker-start-<stable-id>"
$identityBody = @{
  admission_id = $admissionID; requested_by = $operator
} | ConvertTo-Json
Invoke-RestMethod -Method Post `
  http://127.0.0.1:8765/api/v1/sandbox/docker/starts `
  -Headers $controlHeaders -ContentType application/json -Body $identityBody

$controlHeaders["Idempotency-Key"] = "docker-cancel-<stable-id>"
# Cancellation is an alternative control from another client while Start is
# active; do not send it after the synchronous Start response is terminal.
Invoke-RestMethod -Method Post `
  http://127.0.0.1:8765/api/v1/sandbox/docker/cancellations `
  -Headers $controlHeaders -ContentType application/json -Body $identityBody

Invoke-RestMethod `
  "http://127.0.0.1:8765/api/v1/sandbox/docker/status?admission_id=$admissionID" `
  -Headers $readHeaders
```

`sandbox.readiness.v1` 固定 30 秒 TTL，状态只有
`ready|disabled|unavailable`。其稳定 reason/remediation 为：

| Readiness reason | Remediation |
|---|---|
| `none` | `none` |
| `feature_disabled` | `enable_docker_sandbox` |
| `invalid_request` | `correct_sandbox_request` |
| `daemon_unreachable` | `start_docker_engine` |
| `api_unsupported` | `upgrade_docker_engine` |
| `platform_unsupported` | `use_linux_containers` |
| `pids_limit_unavailable` | `enable_pids_limit` |
| `resource_capacity_insufficient` | `reduce_resource_limits` |
| `image_unavailable` | `provide_compatible_image` |
| `managed_egress_unavailable` | `use_network_disabled` |

Admission 使用更高层、同样稳定的产品 reason/remediation：

| Product reason | Remediation |
|---|---|
| `ready` | `none` |
| `feature_disabled` | `enable_docker_execution` |
| `daemon_unreachable` | `start_local_docker` |
| `api_unsupported` | `update_local_docker` |
| `platform_unsupported` | `use_linux_containers` |
| `resource_unavailable` | `enable_pids_limit`、`reduce_resource_limits` 或 `provide_compatible_image` |
| `managed_egress_unavailable` | `use_network_none` |
| `policy_denied` | `review_policy` 或 `correct_sandbox_request` |
| `approval_required` | `approve_exact_request` |
| `permission_denied` | `select_docker_profile` 或 `retry_with_fresh_request` |
| `budget_exhausted` | `increase_or_free_budget` |
| `authority_changed` | `retry_with_fresh_request` |

产品终态为 `succeeded|timed_out|cancelled|failed`，并分别使用
`completed|timed_out|cancelled` 或有界 failure reason。只有 natural exit 0 且当前
Artifact authority 仍匹配时才可能返回非零 `artifact_count`；所有终态必须
`cleanup_complete=true`。API 不返回容器 ID/name、host path、命令、日志正文、
operation key、lease/owner、请求 fingerprint 或 daemon payload。

当前只支持 environment-free、secret-free、零 target 的 `network=disabled`。
allowlist/scoped egress 需要尚未实现的 Go-owned host/port/protocol guard，故固定返回
`managed_egress_unavailable`；无 Docker 时不会退回宿主执行。见
[ADR 0099](adr/0099-docker-sandbox-product-admission-and-recovery.md)。

## Workspace Checkpoint API

Schema v117 的 checkpoint timeline 使用 read bearer；manual capture、Rewind、Undo、
Redo 与 Fork 使用不同的 control bearer，并要求进程已开启
`workspace_checkpoint_control_enabled`。所有 POST 都严格拒绝未知/重复字段与超限 body。
Preview 是只读的三方比较，但仍使用 control bearer，因为它会采集 live Workspace 状态；
Rewind/Undo/Redo/Fork 还必须提交 `confirm: true`、稳定 operation key 和精确
`expected_current_checkpoint_id`。服务端随后重新检查 paused Code/Deliver Run、active
Session、当前权限、进程 capability、无 execution lease、root/Git identity 与 CAS cursor；
客户端确认不能绕过这些门禁。

Timeline 返回检查点来源、attempt/capability generation、Git/index/manifest 摘要、恢复
等级和不完整原因。Preview 返回有界 path/index change 与冲突；它不写文件。恢复追加新的
transaction/checkpoint/event，不改写历史，不接受任意路径或 Git argv，也不恢复历史授权。
Fork 的 branch 由 Go 规范化、验证和创建；目标路径不进入 HTTP body，而是根据受信 source
Workspace 和 operation key 确定性生成同级 `prayu-fork-<digest-prefix>` worktree。成功响应
只返回安全的 Workspace/Run ID 与状态，不返回 `RootPath`、内部 Run config 或 continuity
正文。精确请求/响应 schema 见 `docs/openapi.json`，操作流程见
[Workspace Checkpoints](workspace-checkpoints.md)。

## Batch Delivery API

Schema v118 exposes durable `batch-delivery.v1` plans through the read bearer and keeps
all mutations behind the distinct control bearer. Prepare binds an approved/admitted
core child-task proposal, a clean source commit, the unchanged DAG/budgets/artifacts,
and a normalized non-overlapping ownership/validation contract. It creates independent
child branches/worktrees but never accepts a host path from HTTP.

`POST /runs/{run_id}/batch-deliveries` returns each raw owner token exactly once. An
idempotent replay returns the same plan with an empty authority list rather than
recovering plaintext. `renew-owner` uses `expected_generation` CAS and returns one new
token; the previous generation is fenced. List/detail and Desktop responses exclude
worktree/integration roots, token digests, operation/request fingerprints, and private
tool-profile fingerprints.

Review recalculates the exact receipt head's full merge-base diff, call-chain digest,
changed paths, and tests; acceptance additionally requires all three explicit reviewer
attestations. Merge uses a separate local integration branch/worktree, requires every
current-generation delivery to be accepted, validates DAG order, detects changed-file
overlap, and stops on base drift until `confirm_replay` is explicitly set. It reruns
focused checks after each step and rolls back only the failing integration step. No
route pushes, opens a PR, chooses a conflict side, or mutates a remote.

Prepare/review/merge/cancel use `Idempotency-Key`. Owner renewal deliberately uses
generation CAS instead of an idempotency key; reconcile is intrinsically convergent.
All JSON bodies are bounded and strict, URL Run ownership is rechecked before mutation,
and control tokens cannot authorize GET.

`batch_delivery_host_validation_enabled` is an explicit runtime capability. It is true
only when batch control, permission control, operator approval, danger-full-access, and
`--enable-batch-validation-execution` all hold. Without it a spec containing `go_test`
or `npm_test` fails before materialization. This is honest host code execution, not an
OS network-sandbox claim. Full lifecycle and threat-model details are in
[Deliverable Multi-Agent Batches](batch-delivery.md).

## Extension control API

Schemas v119-v120 expose `GET /api/v1/extensions` through the read bearer. An optional
`run_id` resolves the exact Run/Workspace scope; the response contains bounded server
health, approved capability fingerprints, metadata-only call audits, and inert Plugin
installation state. It omits bearer values, MCP arguments/results, Plugin archives,
Hook payloads, and publisher public keys.

The control bearer may call `POST /api/v1/extensions/mcp/{server_id}/review`,
`POST /api/v1/extensions/mcp/{server_id}/refresh`, and
`POST /api/v1/extensions/plugins/{installation_id}/review`. Disable actions bind the
current descriptor/package fingerprint and Plugin generation, so a stale Desktop card
cannot disable a replacement by accident. Discovery and capability enablement remain
separate MCP reviews; Plugin MCP contributions still enter the independent MCP staging
state. Full state machines and CLI examples are in [Extensions](extensions.md).

## Endpoints

| Method | Path | Result / Filters |
| --- | --- | --- |
| `GET` | `/api/v1` | API and application versions plus top-level resources |
| `GET` | `/api/v1/health` | Health and SQLite schema version |
| `GET` | `/api/v1/capabilities` | Exact Go capability flags, including `agent_code_tools_enabled`, plus metadata-only bounded worker health; no runtime enablement/token/owner/lease/private error |
| `GET` | `/api/v1/openapi.json` | Raw deterministic OpenAPI 3.1 JSON document |
| `GET` | `/api/v1/extensions?run_id={run_id}` | Scoped, redacted MCP/Plugin inventory and metadata-only call audits |
| `POST` | `/api/v1/extensions/mcp/{server_id}/review` | Control-bearer two-stage review or exact-fingerprint disable/revoke |
| `POST` | `/api/v1/extensions/mcp/{server_id}/refresh` | Control-bearer bounded discovery; drift quarantines before use |
| `POST` | `/api/v1/extensions/plugins/{installation_id}/review` | Control-bearer capability review or exact-generation disable/quarantine/revoke |
| `POST` | `/api/v1/sandbox/docker/readiness` | Read-bearer, strict exact-plan readiness; no mutation or long-lived authority |
| `POST` | `/api/v1/sandbox/docker/admissions` | Control-bearer, idempotent exact Docker Sandbox product admission |
| `POST` | `/api/v1/sandbox/docker/starts` | Control-bearer, synchronous exact admitted execution and cleanup |
| `POST` | `/api/v1/sandbox/docker/cancellations` | Control-bearer, sticky cancellation and exact cleanup convergence |
| `GET` | `/api/v1/sandbox/docker/status?admission_id={admission_id}` | Metadata-only admission/launch/terminal projection |
| `GET` | `/api/v1/workspaces` | Bounded Workspace ID/name/creation metadata; no host root path |
| `GET` | `/api/v1/workspaces/{workspace_id}/explore` | One bounded directory level or redacted UTF-8 file evidence; canonical relative path only, no host root |
| `GET` | `/api/v1/workspaces/{workspace_id}/search` | One bounded deterministic filename/redacted-text search; canonical evidence references only, no indexer |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-state` | Bounded exact-root Git metadata only; no parent discovery, host root, body, remote, process, network, or hook |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-diff` | At most 50 secret-redacted exact-root patches; 64 KiB each/512 KiB total, no raw body, process, remote, network, hook, or mutation |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-history` | At most 50 first-parent commits and 64 local branches; no author/email/body/remote/root/process/network/hook |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-file-history?path={canonical_path}` | At most 50 exact-path changes while scanning 512 first-parent commits; metadata only, no rename inference/raw Git/mutation/process/network/hook |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-commit-comparison?base_object_id={exact_object}&head_object_id={exact_object}` | Bounded metadata comparison of two exact local commit trees; no ancestry requirement, blob/patch/root/mutation/process/network/hook |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-commits/{object_id}` | One exact lowercase SHA-1 commit's bounded changed-path/mode metadata; no blob body, checkout, ref mutation, remote, process, network, or hook |
| `GET` | `/api/v1/workspaces/{workspace_id}/repository-commits/{object_id}/file-preview?path={canonical_path}` | One bounded redacted regular/executable UTF-8 file from the exact commit; no raw blob, root, checkout, mutation, process, network, or hook |
| `GET` | `/api/v1/memories?scope={user|project}&scope_id={id}` | Explicit long-term memories; optional disabled/expired inclusion, no automatic extraction or authority |
| `POST` | `/api/v1/memories` | Control-bearer explicit memory creation with provenance, retention, Secret/source validation, and optional explicit redaction |
| `GET` | `/api/v1/memories/export` | JSON-ready memory lifecycle export including disabled/expired records and fixed `capability_grant=false` |
| `GET` | `/api/v1/memories/{memory_id}` | One exact redacted memory, provenance, retention, digest, status, and optimistic version |
| `PATCH` | `/api/v1/memories/{memory_id}` | Exact-version edit, enable, or disable; repeats Secret/source validation |
| `DELETE` | `/api/v1/memories/{memory_id}` | Exact-version physical deletion with `recoverable=false` |
| `GET` | `/api/v1/operation-receipts` | At most 100 terminal metadata-only receipts; optional exact `run_id`, no operation key/path/private lease |
| `GET` | `/api/v1/models` | Redacted Provider/model-route availability with one Registry generation; no key, Base URL, environment name, probe, or model call |
| `GET` | `/api/v1/models/credentials` | Supported Provider system-store and Registry generation status only; fixed `plaintext_returned=false` |
| `POST` | `/api/v1/models/credentials/{provider}` | Explicitly set/delete one Windows system credential; secret is write-only and a successful atomic generation reload needs no restart |
| `GET` | `/api/v1/models/prices` | Imported operator price-snapshot history (newest first) with integer micro-USD entries; no secret, key, or Provider payload |
| `POST` | `/api/v1/models/prices` | Atomically import one bounded `price_snapshot.v1` operator document; same-content imports replay idempotently and a Provider/README/Skill/repository file can never reach it |
| `POST` | `/api/v1/models/diagnostics` | One explicitly confirmed, content-free, tool-disabled Provider diagnostic |
| `POST` | `/api/v1/models/routes/{profile}` | Persist one validated Provider/model route before updating the in-memory Router |
| `GET` | `/api/v1/runs` | Creation-ordered Runs; `status`, `mission_id`, stable keyset pagination |
| `POST` | `/api/v1/runs` | Idempotently create one closed Mission/Run/Session in a registered Workspace |
| `GET` | `/api/v1/runs/{run_id}` | Run, Mission, immutable execution-mode/profile snapshots, read-only Plan/checkpoint/external-Skill metadata, tool usage, token-free lease summary, and the `agent-code-tools.v1` generation with per-tool availability/refusal |
| `GET` | `/api/v1/runs/{run_id}/batch-deliveries` | Bounded durable delivery plans; no worktree roots, owner tokens/digests, or operation fingerprints |
| `POST` | `/api/v1/runs/{run_id}/batch-deliveries` | Confirmed, idempotent materialization of an admitted core-child DAG; raw narrowed owner tokens returned once |
| `GET` | `/api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}` | Child/mailbox/receipt/review/merge projection with private filesystem and authority fields omitted |
| `POST` | `/api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}/children/{ordinal}/review` | Recomputed full-diff/call-chain/test review with explicit independent attestation |
| `POST` | `/api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}/children/{ordinal}/renew-owner` | Exact-generation retry/owner rotation; fences the old token and returns one replacement |
| `POST` | `/api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}/merge` | Confirmed DAG-valid local integration queue; no source/remote mutation |
| `POST` | `/api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}/cancel` | Fence generations and remove only exact clean worktrees; preserve uncertain evidence |
| `POST` | `/api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}/reconcile` | Converge durable worktree/merge intent without restoring tokens or authority |
| `GET` | `/api/v1/runs/{run_id}/project-instructions` | Pinned/live hierarchical sources, hashes, why-effective/conflict details, history, and non-mutating drift diff |
| `POST` | `/api/v1/runs/{run_id}/project-instructions/refresh` | Pinned-and-reviewed-live dual-fingerprint confirmation that appends a new immutable instruction revision |
| `POST` | `/api/v1/runs/{run_id}/continuity-checkpoints` | Capture one bounded, redacted, provenance-bearing, all-false-authority context checkpoint |
| `GET` | `/api/v1/runs/{run_id}/workspace-checkpoints` | Bounded immutable Workspace checkpoint timeline, cursor, transaction provenance, recovery grade, and reasons |
| `POST` | `/api/v1/runs/{run_id}/workspace-checkpoints` | Control-bearer, operation-keyed manual capture; no file mutation or authority grant |
| `POST` | `/api/v1/runs/{run_id}/workspace-checkpoints/preview` | Fresh live/current/target three-way diff and bounded conflicts; no file mutation |
| `POST` | `/api/v1/runs/{run_id}/workspace-checkpoints/rewind` | Explicitly confirmed, exact-cursor append-only restore to one checkpoint |
| `POST` | `/api/v1/runs/{run_id}/workspace-checkpoints/undo` | Explicitly confirmed undo of the current terminal mutation boundary |
| `POST` | `/api/v1/runs/{run_id}/workspace-checkpoints/redo` | Explicitly confirmed redo available only after the matching Undo terminal state |
| `POST` | `/api/v1/runs/{run_id}/workspace-checkpoints/fork` | Explicitly confirmed independent Git worktree/Workspace/Mission/Run/Session Fork; no authority inheritance |
| `GET` | `/api/v1/runs/{run_id}/external-skills` | Bounded external-Skill provenance and root/Specialist delivery counts; no content, paths, digests, or private identities |
| `GET` | `/api/v1/runs/{run_id}/activity` | Chronological public model updates plus allowlisted Harness facts; redacted/bounded, no private reasoning, raw payload, Prompt, Tool arguments, or Tool output |
| `GET` | `/api/v1/runs/{run_id}/events` | Ordered Run events; pagination |
| `GET` | `/api/v1/runs/{run_id}/events/poll` | Bounded non-streaming event batch; shared Run-bound high-water `cursor` |
| `GET` | `/api/v1/runs/{run_id}/events/stream` | Bounded SSE projection; opaque `cursor` or `Last-Event-ID` resume |
| `GET` | `/api/v1/runs/{run_id}/operator-actions` | At most 100 closed pending steering/approval/FileEdit/due-wake metadata items; no source operation or automatic action |
| `GET` | `/api/v1/runs/{run_id}/agent-graph` | Root/Specialist nodes, budgets, lifecycle, and redacted completion summaries |
| `GET` | `/api/v1/runs/{run_id}/delegations` | Operator-gated proposal/review/application/latest-schedule projection; pagination |
| `GET` | `/api/v1/runs/{run_id}/fanout-plans` | Read-only plan metadata plus latest bounded execution/shard summary; pagination |
| `GET` | `/api/v1/runs/{run_id}/reports` | Finding report metadata and severity counts; pagination |
| `GET` | `/api/v1/runs/{run_id}/reports/{report_id}` | Finding facts, model-assertion provenance, Artifact metadata, and lifecycle timestamps |
| `POST` | `/api/v1/runs/{run_id}/active-call/cancel` | Separately authorized exact active-call cancellation request |
| `POST` | `/api/v1/runs/{run_id}/agents/{agent_id}/active-call/cancel` | Separately authorized exact Specialist-call cancellation request |
| `POST` | `/api/v1/runs/{run_id}/execution-profile` | Select `preview|docker|local` intent; never starts a process or grants authority |
| `POST` | `/api/v1/runs/{run_id}/execution-permission` | Operator-select `conservative|approval|full_access|debug`; exact confirmation and process startup gate required, persisted selection never grants authority |
| `POST` | `/api/v1/runs/{run_id}/browser-cdp-permission` | Operator-select `restricted|full_debug`; full mode requires Debug execution permission, dedicated startup gate and exact confirmation; selection never starts a browser or opens CDP |
| `POST` | `/api/v1/runs/{run_id}/lifecycle` | Idempotent `start|pause|resume` under exact state/quiescence/lease gates |
| `POST` | `/api/v1/runs/{run_id}/execute` | Freeze and execute at most eight pending inputs through the existing RunSupervisor |
| `GET` | `/api/v1/sessions/{session_id}/tree` | Browse stored/derived continuity nodes with memory/Git drift and fixed `capability_grant=false` |
| `POST` | `/api/v1/continuity-nodes/{node_id}/fork` | Create a fresh Mission/Run/Session from bounded context while resetting every runtime authority |
| `POST` | `/api/v1/continuity-nodes/{node_id}/resume` | Create an auditable fresh Run/Session branch; never resurrect approvals, credentials, processes, leases, or network authority |
| `POST` | `/api/v1/runs/{run_id}/plan/direction` | Select one persisted direction and create its bounded WorkItems/Note; no phase change or execution |
| `POST` | `/api/v1/runs/{run_id}/plan/deliver` | Explicitly enter Deliver after selection; no Run resume, model/tool call, or execution |
| `GET` | `/api/v1/runs/{run_id}/approvals` | At most 100 pending metadata records and bounded actions; no command, path, content, fingerprint, or reason |
| `POST` | `/api/v1/runs/{run_id}/approvals/{approval_id}/decision` | Policy-rechecked `approve_once|deny`; no Grant, file write, or real process |
| `GET` | `/api/v1/runs/{run_id}/file-edit-proposal-source` | Five-minute Go-issued handle plus complete safe UTF-8; optional `expected_sha256` permits rotation only while current content still matches |
| `POST` | `/api/v1/runs/{run_id}/file-edit-proposals` | Create one pending FileEdit from a Go-issued handle and proposed text; never writes the file |
| `GET` | `/api/v1/runs/{run_id}/file-edit-proposal-recovery/{edit_id}` | Exact durable pending original/proposed bodies as a read-only Diff; reports stale and returns no source handle or mutation authority |
| `GET` | `/api/v1/runs/{run_id}/file-edits` | At most 100 metadata-only FileEdit previews with bounded redacted Diffs |
| `GET` | `/api/v1/runs/{run_id}/file-edit-change-set` | At most 100 exact-bound FileEdit summaries; per-file authority, partial state, no batch/atomic mutation |
| `GET` | `/api/v1/runs/{run_id}/file-edits/{edit_id}` | One exact metadata-only FileEdit preview; no original/proposed body |
| `POST` | `/api/v1/runs/{run_id}/file-edits/{edit_id}/review` | Exact `approve_intent|deny`; review never writes the file |
| `POST` | `/api/v1/runs/{run_id}/file-edits/{edit_id}/apply` | Separately authorized current-Policy/hash-checked apply; renderer submits no path/body |
| `GET` | `/api/v1/runs/{run_id}/evidence-attachments` | At most 100 metadata-only attachments for the exact Run/Session; fixed false instruction authority, no message body |
| `POST` | `/api/v1/runs/{run_id}/evidence-attachments` | Revalidate and append one exact Workspace file as non-authorizing Session evidence; no execution |
| `GET` | `/api/v1/runs/{run_id}/verification-evidence` | At most 100 immutable operator observations with closed outcomes; no command/model/approval/authority inference |
| `POST` | `/api/v1/runs/{run_id}/verification-evidence` | Record one redacted exact-bound `pass|fail|unknown` operator observation; no verification command or execution |
| `GET` | `/api/v1/runs/{run_id}/verification-plan` | At most 50 immutable guidance-only operator plans and ordered checks; no outcome inference |
| `POST` | `/api/v1/runs/{run_id}/verification-plan` | Record one exact-bound 1-32 item operator checklist; no command/model/execution/authority |
| `GET` | `/api/v1/runs/{run_id}/verification-plan-coverage` | Per-item explicit pass/fail/unknown association counts and unobserved state; no aggregate pass |
| `GET` | `/api/v1/runs/{run_id}/verification-plan-coverage/{plan_id}/items/{ordinal}` | Snapshot-stable event-high-water/keyset pages of exact association metadata; no bodies, verdict inference, mutation, execution, approval, or authority |
| `GET` | `/api/v1/runs/{run_id}/verification-plan-coverage/{plan_id}/items/{ordinal}/snapshot-export?format=markdown\|json` | Deterministic digest-bound download receipt over the current exact-item snapshot; no private bodies, durable acceptance, verdict inference, mutation, execution, approval, or authority |
| `GET` | `/api/v1/runs/{run_id}/verification-snapshot-receipts` | At most 100 newest immutable metadata-only snapshot receipts; no content, recorder identity, acceptance, inference, rewrite, approval, authority, or execution |
| `POST` | `/api/v1/runs/{run_id}/verification-snapshot-receipts` | Rebuild and exact-digest-check one current snapshot before recording a confirmed metadata receipt; recording is not acceptance and starts no execution |
| `POST` | `/api/v1/runs/{run_id}/verification-plan-associations` | Immutably associate one later evidence record with one earlier plan item; no reassignment, execution, approval, or inference |
| `GET` | `/api/v1/runs/{run_id}/code-handoff` | Regenerable Code-only Plan/queue/change/verification/coverage/action/report summary; no private body, inferred aggregate result, resume, execution, or composite mutation |
| `GET` | `/api/v1/runs/{run_id}/code-handoff/export` | Digest-bound Markdown/JSON export of one stable Handoff including explicit coverage metadata; no import, result inference, resume, mutation, acceptance, or execution |
| `GET` | `/api/v1/runs/{run_id}/wake-intent` | Bounded public wake state without owner/lease identity |
| `POST` | `/api/v1/runs/{run_id}/wake-intent` | Schedule bounded digest-idempotent wake intent; no execution |
| `POST` | `/api/v1/runs/{run_id}/wake-intent/cancel` | Cancel the exact active intent without running it |
| `POST` | `/api/v1/runs/{run_id}/wake-intent/consume` | Explicitly consume one due intent through the existing bounded RunSupervisor handoff |
| `GET` | `/api/v1/runs/{run_id}/work-items` | `status`, legacy `owner`, `owner_agent_id`, pagination |
| `GET` | `/api/v1/runs/{run_id}/notes` | `status`, `category`, `visibility`, legacy `owner`, `owner_agent_id`, `tag`, `pinned`, pagination |
| `GET` | `/api/v1/runs/{run_id}/artifacts` | Artifact descriptors; `source_id`, `stream`, pagination |
| `GET` | `/api/v1/runs/{run_id}/tool-rounds` | Historical Supervisor tool rounds and redacted calls; pagination |
| `GET` | `/api/v1/sessions` | Creation-ordered Sessions; stable keyset pagination |
| `GET` | `/api/v1/sessions/{session_id}` | Session and optional bound Run |
| `GET` | `/api/v1/sessions/{session_id}/messages` | Messages; `include_compacted`, pagination |
| `POST` | `/api/v1/sessions/{session_id}/messages` | Idempotently queue one bounded message for the exact Run-bound Session; no execution |
| `POST` | `/api/v1/sessions/{session_id}/messages/{message_id}/cancel` | Exact pending-only steering cancellation; prepared/committed items are immutable |
| `POST` | `/api/v1/skills/packages/install` | Confirm and register one bounded untrusted package; no content execution or Run selection |
| `GET` | `/api/v1/work-items/{work_item_id}` | WorkItem detail |
| `GET` | `/api/v1/notes/{note_id}` | Note detail |
| `GET` | `/api/v1/artifacts/{artifact_id}` | Artifact descriptor only |

Nested routes verify their parent first. A missing Run or Session returns `NOT_FOUND` rather than an empty child collection. Unknown query fields and repeated singleton fields are rejected.

Session message DTOs expose schema v43 provenance metadata: `provenance_version`, `source_kind`, optional `source_ref`, `content_sha256`, and `instruction_authorized`. These are read-only audit fields. A client must not infer capability from `role` or source text; only Go-owned control operations can grant or exercise authority. Legacy `context_provenance.v0` rows may have an empty stored digest, while all new v1 rows carry a verified lowercase SHA-256.

Schema v42 Plan/Delivery data remains embedded in Run detail. The API chooses the accepted proposal when a selection exists, otherwise the latest proposal, and returns bounded directions/modules plus selected direction and projected WorkItems. It omits proposal fingerprints, operation digests, requester/root internals, lease identity, and model text. D1-P1 adds two separate control routes: direction selection atomically reuses the existing v42 selection/WorkItem/Note transaction without changing phase, while Deliver transition reuses the existing Run-mode ledger and requires a persisted selection. Neither route starts/resumes execution, calls a model/tool, or grants capability.

Schema v44 adds read-only Delivery fields to the same Run detail: `delivery_gate_enforced`, required and ready checkpoint counts, plus bounded checkpoint IDs, WorkItem/module coordinates, pinned handoff Note IDs, source revisions, boundary status, readiness, and timestamps. The projection omits verification/audit text, handoff content, fingerprints/digests, operation keys, and requester identity. Evidence remains available through the existing authenticated Note detail when an operator follows the handoff Note ID. No HTTP mutation records or approves a checkpoint.

Schema v45 adds required `operator_steering` metadata to Run detail. It reports pending, prepared, committed, and cancelled counts plus a bounded ordered list of message IDs, sequence numbers, statuses, derived `prepared`, and lifecycle timestamps. It omits message content, digests, keys, requester, Session-message IDs, and delivery-attempt identity. D1-S1 adds enqueue/replay through `POST /sessions/{session_id}/messages`; D1-S2 adds exact pending-only cancellation through `POST /sessions/{session_id}/messages/{message_id}/cancel`. A prepared, committed, or cancelled item cannot be changed. Neither route edits, reorders, wakes, or directly delivers steering.

Schema v64 adds required `execution_profile` metadata to Run detail. Its profile enum maps to Go-owned backend, approval, filesystem/network, risk, and required-gate fields; `process_enabled`, `execution_authorized`, and `capability_grant` are always false. Selection requires a `created` or quiescent `paused` Run, the distinct control bearer, strict JSON, and a 16-to-256-byte idempotency key. The browser submits only `profile` and an optional redacted reason; it cannot submit derived controls or authority fields. Stored requester/reason audit fields are omitted from browser DTOs. Selecting Docker or Local neither contacts a runner nor satisfies the corresponding production/OS-sandbox gate.

Schema v71 在 Run detail 中可选嵌入 `external_skills`，并提供同内容的独立只读 endpoint。投影最多包含四个固定版本条目和一个 Specialist，只公开 surface/profile、模式修订、token 上界、信任类别、声明工具数量以及 root/Specialist 准备/提交计数。正文、文件路径、字节数、全部 hash/digest/fingerprint、选择/安装/模式快照 ID、operation key、操作者/请求者/attempt/agent 身份均不进入 DTO。`operator_confirmed` 与 `context_delivery_authorized` 只是历史事实，`tool_capability_grant` 固定为 false；该 endpoint 不安装、选择、加载或执行 Skill。

Schema v71 optionally embeds `external_skills` in Run detail and exposes the same projection through a dedicated read-only endpoint. The projection is bounded to four pinned-version items and one Specialist and reveals only surface/profile, mode revision, token bounds, trust class, declared-tool count, and root/Specialist preparation and commit counts. Content, file paths, byte sizes, every hash/digest/fingerprint, selection/installation/mode-snapshot IDs, operation keys, and operator/requester/attempt/agent identities never enter the DTO. `operator_confirmed` and `context_delivery_authorized` are historical facts only, while `tool_capability_grant` is fixed false; the endpoint cannot install, select, load, or execute a Skill.

Schema v72 新增 `run_creation.v1`。创建请求必须指定已注册 Workspace，并可选择规范 `code|review|learn|script` Profile、`code|cyber` surface 和 `plan|deliver` phase；省略时分别默认为 `code`、`code`、`deliver`。目标原始输入为 1-4096 UTF-8 字节，持久化前脱敏。Go/SQLite 固定交互式 created Run、active Session、默认预算、Profile model route、disabled network、空 targets、revision-one mode 与 `preview/noop` execution profile。相同 operation key 与语义返回原对象并标记 `replayed=true`，改变语义返回 conflict。该 route 不发送消息、调用模型、启动 Run、取得 lease 或授予 capability。

Schema v72 adds `run_creation.v1`. A request must name a registered Workspace and may choose canonical `code|review|learn|script` Profile, `code|cyber` surface, and `plan|deliver` phase; omitted values default to `code`, `code`, and `deliver`. The raw goal is 1-4096 UTF-8 bytes and is redacted before persistence. Go and SQLite fix an interactive created Run, active Session, default budget, Profile model route, disabled network, empty targets, revision-one mode, and `preview/noop` execution profile. Same-key/same-intent replay returns the original graph with `replayed=true`; changed intent conflicts. The route sends no message, calls no model, starts no Run, acquires no lease, and grants no capability.

Schema v73 adds `run_lifecycle_control.v1` and `run_execution_handoff.v1`. Lifecycle uses immutable digest-idempotency facts for strict start/pause/resume and returns the current Run state on delayed replay. Execution freezes up to eight pending identities before acquiring a private lease, then uses the existing RunSupervisor, Policy, budgets, model/tool ledgers, checkpoints, and events. Later appends cannot join the batch; an item cancelled before delivery is skipped; an empty selection completes without a lease or model call. Terminal persistence is fenced to the exact lease generation. Public results include only bounded status/count/model-tool booleans and omit content, outputs, arguments, keys, and lease identity.

D1-M1 originally added the deterministic no-probe model Registry projection. The current `model_availability.v2` keeps that read behavior and adds one `model_harness.v1` record per exact model plus route-level `harness_ready`. Reading availability still performs no Provider request. Provider keys, Base URLs, environment-variable names, HTTP clients, binding digests, prompts, model output, and raw configuration errors are structurally absent; secret-like model identifiers are rejected or redacted before projection.

`POST /api/v1/models/harness-qualifications` is a distinct control operation and requires `confirm_qualification=true`. Built-in Mock returns immediately without a network request. An external Anthropic-compatible model may receive at most two bounded synthetic requests: exactly one in-memory nonce ToolCall followed by exact strict JSON after its synthetic ToolResult. The Tool is never dispatched. The response exposes only status/outcome, bounded counts/duration, network-attempt/tool-executed/content-returned booleans, and the redacted Harness projection. It never returns prompt text, response content, Tool arguments, keys, Base URLs, or raw Provider errors. Success is bound to the exact Provider/model/configuration for seven days; a changed binding or expiry fails closed. Connectivity diagnostics remain a separate one-call action and do not qualify a model.

D1-A1 adds `approval_queue.v1` and `approval_control.v1`. The queue is bounded to 100 pending records and omits commands, arguments, file paths/content, fingerprints, decision reasons, and operation identities. Approval reloads the exact Run/record/source, rejects terminal pending mutation, rechecks current Policy before approve-once, and permits only dry-run Shell or process-disabled ScriptProcess approval. File replacement can only be denied; permanent Policy denial, Session Grant creation, workspace write, Docker/Shell/Local process execution, and capability grant remain unreachable.

Schema v74 exposes only bounded wake schedule/cancel intent. Schema v75 adds a separate
`run_wake_consumer.v1` POST that claims one due generation and hands at most eight steps
to the existing `run_execution_handoff.v1`; it exposes no private owner/lease identity
and starts no background loop. Schema v76 adds `file_edit_apply.v1`, independently from
review, with exact Run/Workspace/approval/current-Policy/original-and-target-hash checks.
The client submits only protocol version and idempotency key, never a path or file body.
Run-bound legacy approval cannot call this route indirectly.

Non-schema D1-G1 adds read-only `repository_state.v1` at the exact registered
Workspace root. Pure-Go inspection accepts only a real `.git` directory, rejects
parent discovery, redirected worktrees and every nested metadata symlink, caps the
metadata/status/output sets, and returns canonical relative status only. Host roots,
file bodies, remote configuration, subprocesses, network, and hooks are absent.
D1-I3 adds `file_edit_change_set.v1`, a metadata-only summary of at most 100 FileEdits
bound to one Run/Session/Workspace. It fixes review/apply independent, atomic/batch
mutation false, and partial state visible; existing per-file routes remain the only
mutation paths.

D1-B1 `skill_package_installation.v1` accepts one archive of at most 64 KiB encoded as
strict whitespace-free canonical standard base64, an exact Code/Cyber surface, explicit
untrusted confirmation, and a 16-256 byte idempotency key. It returns bounded package
identity and six false authority facts. Import may write the content-addressed Registry
but executes no package content, hook, command, Provider, tool, or network request and
does not select the package for a Run.

D1-U1 adds `operation_receipt.v1` to successful HTTP FileEdit apply, foreground wake
consume, and Skill install responses. The receipt contains a closed operation kind,
outcome, replay flag, retry strategy, recovery action, and cleanup state only. It omits
raw operation keys/digests, path/content, requester, model output, and lease identity.
For FileEdit, uncertain retained staging is reported without changing the durable apply
outcome.

D1-E1 `workspace_explorer.v1` uses the read bearer and registered Workspace identity.
The optional `path` is a canonical slash-separated relative path. Go rejects traversal,
absolute/volume paths, links, redirects, controls, normalization aliases, and ambiguous
names. It scans at most 400 directory entries, returns at most 200, reads at most 64 KiB
of valid UTF-8, and caps the redacted projection at 128 KiB. The response excludes the
host root/internal staging and carries `context_provenance.v1` with
`instruction_authorized=false`.

D1-E2 `workspace_search.v1` searches only those bounded redacted Explorer projections.
It accepts one normalized query of at most 128 Unicode code points and scans at most
128 directories, 1,000 entries, 64 regular files, and 50 results. It follows no links,
creates no persistent index, and returns only canonical relative references, bounded
plain-text snippets, and false-authority provenance.

Schema v77 D1-C1 adds the independently gated `session_evidence_attachment.v1` POST.
The browser submits an exact projected reference/hash and an in-memory idempotency key.
Go reloads the Run/Mission/active Session/registered Workspace, reprojects the file, and
atomically stores one tool-role evidence message, metadata event, and immutable
attachment. Go validation and SQLite both require `instruction_authorized=false`;
document text cannot approve tools, widen Scope, or grant capability. The operation
starts no model, tool, process, or network call.

D1-U2 `operation_receipt_history.v1` derives a newest-first view from terminal FileEdit
apply, foreground wake, and inert Skill installation facts. The optional `run_id` is an
exact filter and `limit` is 1-100. Public IDs are opaque domain-separated hashes; raw
operation identities, paths, content digests, archive details, requesters, and leases
are absent. FileEdit cleanup inspection is read-only and reports uncertainty as
`pending_review` without deleting anything.

D1-O1 `operator_action_center.v1` exact-binds one Run/Mission/Session/Workspace and
returns at most 100 closed metadata items for pending steering, pending approvals,
FileEdit review/apply readiness, and due wake intent. Public IDs are domain-separated
opaque hashes. Source row IDs, messages, commands, arguments, paths, Diffs, operation
identity, requesters, leases, and authority fields are omitted. Listing never approves,
applies, wakes, drains, or executes an item.

D1-C2 `session_evidence_inventory.v1` lists at most 100 immutable attachments for the
exact Run-bound active Session and Workspace. It exposes only the source kind,
canonical relative reference, SHA-256, attachment time, and fixed
`instruction_authorized=false`. Message ID/body, attaching operator, event sequence,
private operation, and capability state remain inside Go/Store. Source navigation must
re-enter the existing Workspace Explorer, which independently revalidates the path.

## OpenAPI Contract

Go DTO 是响应结构的唯一来源。以下命令不启动数据库、不读取 token，并可复现仓库内受测试的 [openapi.json](openapi.json)：

Go DTOs are the single source for response shapes. The following command neither opens the database nor reads an API token, and deterministically reproduces the tested repository [openapi.json](openapi.json):

```powershell
cyberagent api openapi
cyberagent api openapi --output docs/openapi.json
```

运行时的 `/api/v1/openapi.json` 返回同一份原始文档，仍要求 loopback 与 read Bearer 认证，不接受 query 或 body。它使用 `application/vnd.oai.openapi+json`，不套普通 `api.v1` envelope。当前契约有 124 个 path、138 个 operation 和 325 个 schema。测试逐条命中公开 handler，并确认普通 DTO 不包含 Workspace root、Artifact/Skill/Session 正文、模型输出、工具参数、私有 lifecycle、operation/fencing/lease owner、API key、Provider Base URL 或环境变量名。batch delivery DTO 还明确排除 child/integration root、owner-token digest 与 operation/request fingerprint；明文 owner token 只在 Prepare/rotation control 响应中返回一次。

The runtime `/api/v1/openapi.json` returns the same raw document under the loopback and read-bearer boundary and accepts neither a query nor a body. It uses `application/vnd.oai.openapi+json` rather than the ordinary `api.v1` envelope. The contract contains 124 paths, 138 operations, and 325 schemas. Tests exercise every handler and verify that ordinary DTOs omit Workspace roots, Artifact/Skill/Session bodies, model output, Tool arguments, private lifecycle, operation/fencing/lease-owner identities, API keys, Provider base URLs, and environment-variable names. Batch-delivery DTOs additionally omit child/integration roots, owner-token digests, and operation/request fingerprints; a plaintext owner token appears only once in a prepare/rotation control response.

## 主动取消 / Active-Call Cancellation

取消入口写入 schema v18 的 `run_model_cancellations` 与一对一幂等操作账本。首次有效请求返回 `202 Accepted`；相同 key 与相同意图重放原对象，不同意图复用 key 或为同一目标换 key 返回 `409 CONFLICT`。请求只有在 Run 正在运行、execution lease 活跃、Supervisor attempt 完全匹配、目标是最新且尚未终止的 model attempt 时才被接受。

The cancellation route writes schema v18 `run_model_cancellations` plus a one-to-one idempotency-operation ledger. The first valid request returns `202 Accepted`. Replaying the same key and intent returns the original object; changed intent under that key or a different key for the same target returns `409 CONFLICT`. A request is accepted only while the Run is running, its execution lease is active, the Supervisor attempt matches exactly, and the target is the latest non-terminal model attempt.

持有私有 lease 的 worker 每 100 ms 检查当前调用对应的 pending 请求。观察动作事务校验 checkpoint fencing，写入 `model.cancel_observed`，随后才取消进程内 Provider context。模型终态与请求的 `resolved` 状态原子提交；若 worker 崩溃且后续 attempt 接管，旧请求会变为 `resolved/superseded`，绝不会作用到新调用。客户端只能在 SSE/事件中观察进展，不能获得或提交内部 lease token。

The worker holding the private lease checks for a pending request for its current call every 100 ms. Observation transactionally validates checkpoint fencing, appends `model.cancel_observed`, and only then cancels the in-process Provider context. The model terminal event and the request's `resolved` state commit atomically. If a worker crashes and a later attempt takes over, the old request resolves as `superseded` and can never affect the new call. Clients observe progress through SSE/events and can neither obtain nor submit the internal lease token.

schema v29 的 Specialist 路由写入独立的 `specialist_model_cancellations` 与 digest-only operation ledger，不复用按 Run 唯一键控的 root registry。路径中的 `agent_id` 与 body 中的 `attempt_id/model_attempt` 必须精确匹配当前 direct Specialist、running AgentAttempt、最新 started child model call 和活动 Run lease。child worker 先提交 `model.cancel_observed`，再取消该调用自己的 Go context；模型终态与 resolution 原子提交，Attempt crash/interruption/takeover 会将遗留请求解析为 `attempt_terminated` 或 `worker_lost`。响应不包含 reason/requester、内部 subject、模型正文或 fencing 字段。

The schema v29 Specialist route writes a separate `specialist_model_cancellations` table and digest-only operation ledger rather than reusing the Run-keyed root registry. The path `agent_id` and body `attempt_id/model_attempt` must exactly match the current direct Specialist, running AgentAttempt, latest started child model call, and active Run lease. The child worker commits `model.cancel_observed` before cancelling that call's own Go context. Model terminal state and resolution commit atomically, while Attempt crash, interruption, or takeover resolves leftovers as `attempt_terminated` or `worker_lost`. Responses omit reason/requester, internal subjects, model text, and fencing fields.

控制意图只命中所选 child。若该 child 运行在 `SpecialistScheduler` 的并发 round 中，它随后返回的取消错误会触发既有的首错 fan-out，scheduler 可能本地取消同轮 sibling 以保持 round 一致性；这不会为 sibling 创建第二条远程取消请求，也不会扩大 admission、spawn 或工具权限。

The persisted intent targets only the selected child. If that child belongs to a concurrent `SpecialistScheduler` round, its resulting cancellation error activates the existing first-error fan-out and may locally cancel the sibling to preserve round consistency. No second remote request is fabricated, and admission, spawn, and tool authority remain unchanged.

## Run Event Stream

SSE endpoint 只读取 append-only `run_events`。首次连接从 sequence 1 开始；每个 `run.event` frame 同时携带持久化 `sequence` 和不透明、与 Run 绑定的 `id`。断线后把最后一个 `id` 放入 `Last-Event-ID` header，或首次请求的 `cursor` query；两者不能同时出现。cursor 只用于定位，不是授权凭据，跨 Run 复用会在发送 SSE headers 前被拒绝。

The SSE endpoint reads only append-only `run_events`. A fresh connection starts at sequence 1. Every `run.event` frame includes the durable sequence and an opaque Run-bound `id`. Reconnect by sending the final id in `Last-Event-ID`, or use the `cursor` query on an initial request; the two cannot be combined. A cursor is positioning data, not authorization, and cross-Run reuse is rejected before SSE headers are committed.

`EventView.version` is the literal `v1` imported from Go's canonical event-envelope
constant into OpenAPI and generated TypeScript. A client parse or transport failure
cancels the response reader before reconnecting, preserving the original error while
preventing abandoned streams from exhausting browser per-origin connection slots.

```text
: cyberagent run-events.v1
retry: 1000

id: <opaque-run-bound-cursor>
event: run.event
data: {"version":"run-events.v1","request_id":"req-...","run_id":"run-...","cursor":"...","sequence":42,"event":{...}}

: heartbeat
```

心跳只是 SSE comment，不写入数据库，也不会占用 sequence。达到事件/时间上限或客户端过慢时连接关闭，客户端用最后成功 frame 的 id 恢复。另一个进程写入同一 SQLite 数据库的事件可在下一轮 polling 被观察到；服务器关闭会取消 request context，不等待五分钟连接寿命。stream 复用与 `/events` 完全相同的脱敏 `EventView`，第一版不增加模型正文投影。

Heartbeats are SSE comments and consume neither database rows nor sequences. The connection closes at its event/time limit or when a client misses its write deadline; resume from the last successfully received frame id. Events written by another process to the same SQLite database become visible on a later poll. Server shutdown cancels request contexts instead of waiting for the five-minute lifetime. The stream reuses the same redacted `EventView` as `/events` and adds no user-visible model-text projection.

Native browser `EventSource` cannot attach the current Bearer header. The React/Vite console therefore uses authenticated `fetch` streaming and never puts the token in a query string or browser storage. Production assets and `api.v1` now share the Go loopback origin when `--ui-dir` is set; Vite provides the same-origin proxy only during development. CORS remains disabled.

## Repository History, Verification Plans, And Handoff Export

`GET /api/v1/workspaces/{workspace_id}/repository-history` returns
`repository_history.v1`. It accepts no query parameters and reads only the exact
registered Workspace root. The projection is capped at 50 first-parent commits, 64
returned local branches, and 1,024 scanned branch references. It excludes author
identity, email, commit body, remote configuration, host root, subprocess, network,
and hook data. Redirected or linked Git metadata fails closed.

`GET /api/v1/workspaces/{workspace_id}/repository-commits/{object_id}` returns
`repository_commit_detail.v1`. `object_id` must be one exact lowercase 40-character
SHA-1 object ID; symbolic refs and revision expressions are rejected. The pure-Go
reader compares that tree with its first parent under bounded entry/depth/change
limits and returns canonical path plus added/modified/deleted and content/mode-change
metadata. Author/email/body, blob content, remote/root, checkout/ref mutation,
subprocess, network, and hook data remain absent. Missing objects and malformed or
linked metadata fail closed rather than yielding a partial success.

`GET /api/v1/runs/{run_id}/verification-plan` lists at most 50 immutable
operator-authored plans. `POST` on the same path uses the existing distinct verification
control capability, strict `operator_verification_plan.v1` JSON, and an in-memory
`Idempotency-Key`. A request carries only title, summary, and 1-32 title/expected-
observation items. Go and SQLite exact-bind the active Code Session and keep
`guidance_only=true`; command execution, model assertion, result inference, approval,
and authority are always false. Results remain on the separate verification-evidence
route.

`GET /api/v1/workspaces/{workspace_id}/repository-commits/{object_id}/file-preview`
requires exactly one `path` query value. The object must be an exact lowercase SHA-1
identity and the path must be canonical, relative, and present in that commit as a
regular or executable file. Binary, linked, missing, and over-64-KiB files fail closed.
The response is secret-redacted, capped at 128 KiB, and carries a SHA-256 over the
projected UTF-8 bytes plus `instruction_authorized=false`. It never returns the raw
blob or host root and performs no checkout, ref update, process, network, or hook.

`POST /api/v1/runs/{run_id}/verification-plan-associations` records one immutable
`operator_verification_plan_evidence_association.v1`. The request contains only exact
plan, item, and evidence IDs plus an in-memory idempotency key. Go and SQLite require
the same Code Run, active Session, Workspace, an earlier plan item, and one unassociated
later evidence record. It cannot reassign evidence, execute a check, approve an action,
or infer a result. `GET /api/v1/runs/{run_id}/verification-plan-coverage` returns only
bounded per-item pass/fail/unknown counts and unobserved state; contradictory outcomes
remain visible and there is no aggregate pass field.

`GET /api/v1/runs/{run_id}/code-handoff/export?format=markdown|json` returns a
`code_handoff_export.v1` envelope with at most 256 KiB of content, source event
high-water, UTF-8 byte count, SHA-256, safe filename, and fixed MIME type. It uses the
same stable Handoff assembly and cannot resume, apply, accept a report, mutate, or
execute. Handoff and export include at most 100 metadata-only verification coverage
items with explicit pass/fail/unknown counts and contradiction totals. They omit plan
and evidence bodies and never infer an aggregate result. The React client recomputes
the export digest before creating a local download.

`GET /api/v1/workspaces/{workspace_id}/repository-file-history?path=...` requires
exactly one canonical relative `path`. The pure-Go projection starts at current HEAD,
walks first-parent history, scans at most 512 commits, and returns at most 50 commits
where that exact path changed. Each item contains only object/time, a bounded redacted
subject, added/modified/deleted status, previous/current kind, and content/mode-change
flags. It returns no raw blob, patch, identity/body, remote/root, rename inference, or
authority, and performs no checkout, ref mutation, process, network, or hook action.

`GET /api/v1/workspaces/{workspace_id}/repository-commit-comparison` requires exactly
one `base_object_id` and one `head_object_id`. Both must be lowercase 40-character local
commit object IDs in the exact registered Workspace repository. Symbolic refs and
revision expressions are rejected, and no ancestor relationship is required. The
pure-Go bounded projection returns redacted subject/time plus canonical added/modified/
deleted path, kind, content-change, and mode-change metadata. It contains no author,
body, blob, patch, remote, host root, or rename inference and performs no checkout,
reference update, process, network, or hook action.

`GET /api/v1/runs/{run_id}/verification-plan-coverage/{plan_id}/items/{ordinal}` returns
one page of `operator_verification_plan_item_coverage.v1` for one exact Run, immutable
plan, and 1-based item ordinal. It accepts only shared `limit` and opaque `cursor`
parameters. Each page returns the immutable item digest/aggregate counts and up to the
requested number of exact association records in descending association-event order.
The first page freezes the latest association event sequence and aggregate counts at
that high-water. A continuation cursor is bound to the exact route and carries the
snapshot high-water, previous page's final event/association tuple, and consumed row
count. SQLite recomputes the anchor's rank inside the frozen range and Go requires an
exact match before reading the next keyset page. Duplicate/blank/unknown query
parameters, forged positions, missing anchors, and cross-item cursor reuse fail closed.

Associations contain only opaque evidence/association IDs, explicit pass/fail/unknown
outcome, evidence/association event sequences, and time. Private plan/evidence bodies,
operator identity, aggregate verdicts, mutation, command/model execution, approval,
and authority remain absent. Associations appended after the first page have a higher
event sequence and cannot shift or enter the frozen pages. The read window is capped at
100,000 associations without holding a long transaction; when more snapshot rows exist,
the last permitted page sets `page.truncated=true` and emits no next cursor. React loads
25 older references only after an explicit operator action and validates the repeated
snapshot identity, aggregate counts, ordering, uniqueness, and closed-authority fields.

`GET /api/v1/runs/{run_id}/verification-plan-coverage/{plan_id}/items/{ordinal}/snapshot-export`
requires exactly one byte-exact `format=json|markdown` value. It reuses the current
exact-item detail boundary and returns `operator_verification_plan_item_snapshot_export.v1`
around deterministic `operator_verification_plan_item_snapshot.v1` content. The receipt
binds exact Run/Session/Workspace/plan/item identities and digests, snapshot event high-
water, explicit outcome/returned/total counts, at most 100 descending references,
truncation, content SHA-256/UTF-8 bytes, safe filename, fixed MIME, and a 256 KiB limit.

The content has no generated timestamp and therefore reproduces the same bytes while
source facts remain unchanged. It contains no private plan/evidence body or operator
identity, infers no result, persists no acceptance, grants no approval or authority,
rewrites no record, and starts no command, model, or execution. Missing, duplicate,
blank, whitespace-padded, or unknown query values fail closed. React verifies the outer
envelope and inner content before creating the local download.

`GET|POST /api/v1/runs/{run_id}/verification-snapshot-receipts` use
`operator_verification_plan_item_snapshot_receipt.v1`. POST requires the existing
verification control capability, one normalized idempotency key, exact plan/item,
`format`, snapshot event high-water, content SHA-256, and
`confirm_metadata_snapshot=true`. Application rebuilds the deterministic export before
Store obtains a Run writer lock and rechecks the active Code Session, Workspace,
plan/item digests, current association high-water/counts/truncation. Event and receipt
then commit atomically. A stale new intent fails with conflict; an identical committed
intent replays without creating another event.

Only metadata is persisted. The snapshot content is not accepted as input and is never
stored in the receipt table. Public GET returns at most 100 newest records and omits the
private recorder identity. Every item and the inventory explicitly report content
included, private bodies, operator identity included, snapshot accepted, result
accepted/inferred, record rewritten, approval, authority, and execution as false.
Update/delete are rejected by SQLite. Therefore a receipt proves only that one exact
deterministic digest was retained by an operator; it is neither a verification verdict
nor a review decision.

`GET|POST /api/v1/runs/{run_id}/verification-snapshot-receipt-reviews` use
`operator_verification_plan_item_snapshot_receipt_review.v1`. POST reuses the existing
default-off verification-evidence control capability and requires one normalized
idempotency key, exact receipt ID/content SHA-256/receipt event sequence, a closed
`metadata_confirmed|metadata_disputed` decision, and
`confirm_non_authorizing_review=true`. A new write requires the current Code Session to
remain active. Go and SQLite exact-bind Run, Session, Workspace, latest Code mode,
receipt, review event, and chronology in one transaction.

One receipt can be reviewed only once. Exact committed intent replays; a changed intent,
new key for an already reviewed receipt, stale digest/sequence, cross-Run binding, or
update/delete fails closed. GET returns at most 100 newest reviews. The private reviewer
identity remains Store-only, while each public item and inventory fixes metadata-only,
read-only, and non-authorizing to true and content/private body/reviewer inclusion,
snapshot/result acceptance or inference, rewrite, approval, authority, and execution
to false. `metadata_confirmed` therefore confirms only the retained metadata binding;
it is not a verification pass, release approval, or execution grant.

## Envelopes

Except for the raw OpenAPI document and SSE frames, successful responses use one versioned envelope:

```json
{
  "version": "api.v1",
  "request_id": "req-...",
  "data": [],
  "page": {
    "limit": 50,
    "next_cursor": "..."
  }
}
```

Errors never expose internal SQLite details:

```json
{
  "version": "api.v1",
  "request_id": "req-...",
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "page limit must be between 1 and 100"
  }
}
```

The `X-Request-ID` header matches `request_id`. Responses also set `Cache-Control: no-store`, a deny-all Content Security Policy, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, and `X-Frame-Options: DENY`. Stable error meanings and CLI mappings are documented in [errors.md](errors.md).

## Pagination

Collection routes accept `limit` from 1 to 100; the default is 50. `next_cursor` is an opaque, URL-safe cursor bound to the exact route and filter set. A cursor cannot be reused on another endpoint or after changing filters. The Store bounds a cursor window to 100,000 rows; if additional data exists beyond that window, `page.truncated` is `true` and no invalid next cursor is emitted.

Clients must not decode, edit, persist indefinitely, or synthesize cursors. Restart pagination from the first page after a filter change or a rejected cursor.

Most pagination is a bounded live SQLite projection, not a multi-request snapshot. Append-only event/message order remains stable, but updates to descending activity lists can move rows between requests. Top-level Run and Session lists across HTTP, CLI, TUI, and Web use immutable `(created_at, id)` ordering; HTTP continuation pages use that keyset, so ordinary updates and later-created records cannot shift them. Run status-filter membership remains live. The exact verification-item route separately freezes an association-event high-water and aggregate counts, then uses a rank-checked keyset cursor for stable pages within its 100,000-row window. Clients that require a fresh view after a filter-membership change should restart from the first page.

## 当前限制 / Current Limits

- No general unscoped filesystem mutation, install-time Skill execution, runtime worker enable endpoint, or user-visible model-text stream. One exact approved FileEdit can be applied through its dedicated Go capability; a schema-v118 delivery child can mutate only its owned isolated worktree through its closed tools; one package can be registered inertly; Windows may store/delete one exact Provider credential without readback; an explicitly started worker may consume one due intent/one step at a time. Steering edit/reorder and general host/container execution remain absent; schema v99 exposes only the exact, explicitly enabled network-none Docker product profile above.
- Execution-lease rows coordinate workers, but the API exposes neither `lease_id` nor any operation that accepts a fencing token.
- No Artifact content route. Use the authenticated local CLI `artifact read` when content is explicitly required.
- No real general Shell or LocalSandbox execution. Docker execution is limited to schema v99 admissions over an already-compiled exact plan, fresh process-local capability, exact per-call approval, current Policy/permission/budget/readiness, and network none. There is no arbitrary Docker request, scoped egress, pull/build/exec/TTY, daemon endpoint, mount override, host fallback, or renderer-issued capability.
- No per-resource authorization below the process token. Future remote or multi-user use requires a separate identity and authorization design.
- Repository history, exact-file history, exact commit detail, and redacted commit-file preview have no checkout/fetch/push/ref-update or raw-blob endpoint. Verification plans, associations, exact-item drilldown, and Handoff coverage do not run checks or imply aggregate outcomes. Handoff exports have no import/resume endpoint.
