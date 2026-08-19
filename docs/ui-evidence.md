# 真实浏览器 UI 证据 / Real-browser UI Evidence

`ui-evidence.v1` 把一次真实页面验证绑定到精确源码、启动配方、浏览器运行时、呈现矩阵、交互步骤和不可变产物。它解决的是“这个固定源码在这个真实浏览器环境里实际发生了什么”，不是用截图代替测试，也不是让页面内容获得控制权。

`ui-evidence.v1` binds one real-page verification to exact source, launch recipes, browser runtime, presentation matrix, interaction steps, and immutable artifacts. It answers what a fixed source state actually did in a real browser. It does not turn screenshots into tests or page content into authority.

## 结果语义 / Outcome semantics

Attempt 只允许以下状态：

| 状态 | 含义 | 是否通过 |
|---|---|---|
| `not_run` | 清单已持久化，尚未进入运行 | 否；保持中性 |
| `running` | Run-owned 执行与清理责任仍有效 | 否 |
| `passed` | 步骤、强制失败策略和完整清理全部通过 | 是，唯一绿色状态 |
| `failed` | 稳定阶段中的验证失败 | 否 |
| `cancelled` | 操作者取消，清理后终止 | 否 |
| `timed_out` | 有界 deadline 到期 | 否 |
| `interrupted` | 重启发现遗留 `running` Attempt，并收敛为中断 | 否 |

失败阶段固定为 `build`、`launch`、`readiness`、`navigation`、`selector`、`assertion`、`console`、`network`、`capture` 或 `cleanup`。`not_run`、缺少产物、仅源码审阅、build success 和 mock render 都不能被投影成通过。

## 清单绑定 / Manifest binding

每个不可变清单记录：

- Run、Mission、Session、Workspace 和 Attempt；
- Git/non-Git 类型、commit、branch、dirty 标记与 digest、root fingerprint、原始 index digest、确定性 worktree manifest digest；
- 可选 build 与必需 start 的 `command-runtime.v2` 规范化配方，包括可审阅 argv、Workspace 相对 cwd、可执行文件/路径/environment digest、timeout、`network=disabled` 和 `credentials=none`；
- 固定安装位置浏览器的 product、已验证 version、可执行文件 SHA-256、`restricted-cdp-ui-evidence.v1`、headless 与临时 Profile 标记；
- literal `127.0.0.1` URL、route、readiness status/deadline、viewport、DPR、locale、theme、reduced motion；
- fixture name、seed、page state、data SHA-256、deterministic/synthetic 标记；
- 有序 navigate/click/type/assert/capture 步骤、遮罩和强制 console/page/request/HTTP failure policy。

准备后，Application 会在 build 前、应用 readiness 后、浏览器断言后以及 owned application/browser process cleanup 完成后重新捕获 source checkpoint。最后一次复核位于进程树回收之后，避免应用在最后断言与 terminal receipt 之间改写源码。tracked、未忽略的 untracked、index、commit、branch 或 root 发生漂移会失败关闭。新建或更新的 ignored build/cache 目录仍由完整 checkpoint 记录为显式排除项，但不改变内容级 source manifest digest；配方不得修改被绑定的源码。

Fixture 是对由已审阅 start recipe 提供的数据状态的声明，不是浏览器侧任意脚本注入。请在应用自己的测试入口中装载固定 seed，并用真实选择器/交互断言证明该状态。`type` 的原始值只存在于当前请求；清单仅保存 SHA-256，且疑似 secret 的输入会被拒绝。

## 运行所有权与网络边界 / Runtime ownership and network boundary

执行面默认关闭。启用 Windows Desktop 控制需要同时满足：

```powershell
./build/desktop/cyberagent-desktop.exe `
  --enable-permission-control `
  --enable-danger-full-access `
  --enable-run-execution `
  --enable-browser-cdp-control `
  --enable-ui-evidence
```

Run 本身还必须处于 Code/Local/Deliver/root、选择当前 `full_access`、拥有 active execution lease，并有当前 `restricted` browser-CDP permission。启动 flag 只开放进程内 capability；不会替 Run 创建权限、审批或 lease。

Application 在任何启动前探测 readiness 端口；发现已有 listener 就返回 `launch/preexisting_service`，不会收养、停止或等待它。应用由 Run-owned command runtime 管理，浏览器从固定受信安装位置重新校验版本、publisher 与 SHA-256，以新的 disposable Profile 启动，并进入 Safe Web/WFP/Job Object 生命周期。启动、读取和等待仍逐次要求 active Run lease；取消、timeout 或权限撤销后的回收使用只含原 Attempt durable Job/operation/Run/lease identity 的内部 cleanup-only 绑定，不能启动、收养、读写或停止其他 Job。取消和 Desktop 关闭都会等待 owned application/browser tree、Profile、network guard 与端口清理。Profile 只在进程树与网络清理证明完成后进入 exact-owner quarantine；Windows 的短暂文件共享锁采用 5 秒有界重试，超限仍以 `cleanup` 失败而不是误报通过。SQLite 中的 PID、清单或历史 Attempt 不会在重启后恢复启动权。

浏览器只允许精确 loopback origin。每个 request 和 redirect 都经 restricted CDP/网络边界复核；方法集合不包含 `Runtime.evaluate`、cookie API、response body、request mutation/replay 或 `Fetch.fulfillRequest`。页面和所有捕获内容均是不可信输入。

普通 command runtime 的 `network=disabled` 仍是宿主执行策略而非通用 OS 网络沙箱；因此 build/start 还会拒绝已知网络客户端/安装命令意图，依赖应在验证前固定安装。浏览器流量则由 UI-evidence Safe Web 生产路径的独立网络隔离约束。

## 产物与脱敏 / Artifacts and redaction

V1 的执行请求必须同时采集 PNG screenshot、DOM、accessibility tree、console/page errors、network/HTTP metadata 和 performance metrics，避免以像素快照单独替代行为、可访问性与运行时健康验证；video 字段保留但当前必须为 `false`。逻辑 viewport 与 DPR 的乘积不得超过 7680×4320 像素面；PNG 在完整解码/分配前先用 header config 验证 dimensions，且 dimensions 必须与 `viewport × DPR` 在浏览器舍入允许的 1 像素内一致。领域模型、SQLite trigger 与 React 收据解析都会拒绝错配。每个产物绑定 kind、MIME、SHA-256、byte count、source commit、Run/Attempt、source step、capture time、viewport、截图 dimensions、redaction、`retention_policy=run_history` 与 `untrusted=true`。本地证据随 Run 历史保留且不静默过期，单产物、单 Attempt 和全局仓库分别有 32 MiB、128 MiB 和 2 GiB 硬上限；CI 上传副本固定保留 5 天。

文本产物统一做 UTF-8 修复、控制字符移除与 secret redaction。Network 只保留重新通过 exact TargetScope 的脱敏 URL（不含 query/user/fragment）、method、resource type、status、MIME 和失败摘要；`data:`、`file:`、`blob:` 与其他越界 URL 只写入固定 `[blocked-url]`，不读取 header、cookie 或 body。Screenshot 只会遮盖显式 `mask_selectors`；动态或可能含个人/敏感数据的区域必须列出 mask，任一 selector 未匹配即失败。基线差异只能由人工审阅接受，系统不自动更新 baseline。

下载响应带 `Cache-Control: no-store`、ETag、`X-CyberAgent-Content-SHA256` 和 `X-CyberAgent-Evidence-Untrusted: true`。React 在创建 Blob 前复核 MIME、长度与 SHA-256；CLI 也在以 `0600` 独占创建新文件前复核内容。

## 产品入口 / Product surfaces

Desktop 的 Run workspace 包含“真实浏览器 UI 证据”页签。历史清单、步骤和产物在控制 capability 关闭后仍可只读查看；启动前必须加载/编辑完整 JSON，并勾选人工审阅确认。只有 `passed` 使用成功样式，`not_run` 始终中性。

HTTP/OpenAPI 使用 read bearer 读取，distinct control bearer 启动或取消：

```text
GET  /api/v1/runs/{run_id}/ui-evidence?status=passed&limit=100
POST /api/v1/runs/{run_id}/ui-evidence
GET  /api/v1/ui-evidence/{attempt_id}
GET  /api/v1/ui-evidence/{attempt_id}/artifacts/{artifact_id}
POST /api/v1/ui-evidence/{attempt_id}/cancel    {"confirm":true}
```

CLI 是有意设计的只读/导出入口，不复制执行 authority：

```powershell
cyberagent ui-evidence list --run <run-id> --status passed --limit 20
cyberagent ui-evidence show <attempt-id>
cyberagent ui-evidence artifact <attempt-id> <artifact-id> --output ./evidence.png
```

## Desktop 启动模板 / Desktop launch template

面板内置本仓库 Vite 模板。它要求 `web/node_modules` 已固定安装，不会执行 `npm install`：

```json
{
  "operation_key": "desktop-ui-evidence-<stable-unique-id>",
  "start": {
    "version": "command-runtime.v2",
    "profile": "powershell",
    "script": "npm run dev -- --host 127.0.0.1 --port 4173",
    "working_directory": "web",
    "environment": [],
    "stdin_policy": "closed",
    "close_initial_stdin": true,
    "timeout_milliseconds": 1800000,
    "output": {"inline_bytes": 16384, "artifact_bytes": 262144},
    "network": "disabled",
    "credentials": "none",
    "purpose": "Launch the reviewed Workspace web application for source-bound UI evidence"
  },
  "readiness": {
    "url": "http://127.0.0.1:4173/",
    "method": "GET",
    "expected_status": [200],
    "timeout_milliseconds": 60000,
    "interval_milliseconds": 250
  },
  "url": "http://127.0.0.1:4173/",
  "route": "/",
  "browser": {"product": "edge", "channel": "stable"},
  "environment": {
    "viewport": {"width": 1440, "height": 900, "dpr": 1},
    "locale": "en-US",
    "theme": "light",
    "reduced_motion": false
  },
  "fixture": {
    "name": "empty-local-state",
    "seed": "ui-evidence-v1",
    "page_state": "{}",
    "data_sha256": "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
    "deterministic": true,
    "synthetic": true
  },
  "steps": [
    {"step": {"id": "navigate", "kind": "navigate", "capture_after": true}},
    {"step": {"id": "app-root", "kind": "assert_present", "selector": "#root", "capture_after": true}}
  ],
  "capture": {
    "screenshot": true,
    "dom": true,
    "accessibility": true,
    "console": true,
    "network": true,
    "performance": true,
    "video": false,
    "mask_selectors": []
  },
  "failure_policy": {
    "fail_on_console_error": true,
    "fail_on_page_error": true,
    "fail_on_request_error": true,
    "fail_on_http_status": true
  }
}
```

同一 `operation_key` 加同一请求指纹会幂等返回原 Attempt；换载荷复用 key 会冲突。取消必须显式 `confirm=true`。

## CI 与 PR 收据 / CI and PR receipts

Windows CI 的 `TestInstalledEdgeUIEvidenceHeadlessMatrixAndRegression` 从 clean checkout/fixed commit 启动固定 Edge，使用创建时绑定的 Job Object 和临时 Profile，只服务 deterministic `127.0.0.1` fixture。矩阵覆盖：

- 1440×900 @1x、light、en-US、full motion；
- 390×844 @2x、dark、zh-CN、reduced motion。

它执行真实 click/type/selector、DOM、accessibility、performance、console/network 和 PNG 尺寸检查，并访问一个仅缺失 click handler 的 regression route，证明真实页面断言能发现 build/source inspection 不能证明的行为错误。CI 上传 screenshots 与 `receipt.json`，其中记录 commit/dirty digest、clean-checkout、浏览器 version/executable hash、scope/routes、完整 presentation matrix、artifact hash/MIME/dimensions、`regression_caught=true`，以及 Job 进程树、DevTools 端口、临时 Profile 和 fixture server 的清理结果。

PR 的 Verification 段应列出精确命令、平台/runtime、manifest/receipt 或 Attempt ID、矩阵、产物 SHA-256、失败策略、清理结果和未运行项。将 `not_run` 或未覆盖矩阵写成 passed 属于错误报告。内置 `run-verify@1.1.0` 将本流程与 `focused-checks` 和 PR receipt 对齐。

架构与威胁决策见 [ADR 0120](adr/0120-source-bound-real-browser-ui-evidence.md)，API 精确 schema 见 [OpenAPI](openapi.json)。
