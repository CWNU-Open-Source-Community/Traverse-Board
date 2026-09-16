# 第四批 UX 修复与验证：已有项目导入和本机模型接入

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-08，Asia/Hong_Kong。实施目录 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。本批与前三批改动共同保存在未提交工作树；没有推送、PR 或发布。

本记录对应 F01 主流程，以及复核发现的 F04/F12/F13/F14 边界。**已完成限定 Web 环境下的已有目录导入、本机模型保存、诊断、资格验证和首轮，以及最终 390×480 导入焦点、按钮边界与侧栏导航复验。** 不代表 F01—F14 整体验收完成。

## 实现范围

- Web 主界面在服务器显式启用 `api serve --enable-workspace-import`、连接具有独立控制令牌时，提供已有目录接入表单。输入的是服务所在电脑的绝对目录路径；未启用、只读连接与 Desktop 原生选择器分别展示其实际能力。
- `POST /api/v1/workspaces/import` 复用 `workspace.Manager.Import` 和现有注册表。请求必须明确确认，路径上限为 4096 UTF-8 bytes；响应仅含项目 ID、显示名、创建时间和两个固定为 `false` 的修改/授权标志。路径不回传，客户端错误不保留原始路径或服务器错误详情。
- 注册通过原子 insert 和冲突重读实现；规范化根目录自然幂等，不另建幂等台账。重名、并发和现有隐藏工作区不通过旧的按名称 upsert 覆盖其他根目录。导入不写源目录、不运行命令或模型、不创建任务、不授予 Agent 执行权限。
- 旧 CLI 项目显示名可能超过新导入投影的 128-byte 上限。HTTP 和 Desktop 复用有 `...` 标记的有界显示名；原存储名称、ID、根目录和创建时间不变。
- 真实接入本机模型时发现请求地址误用官网的 HTTPS-only 校验，且自定义 Provider 后端仍强制系统密钥。现在模型地址允许 HTTPS，以及确切 loopback 的 HTTP；官网规则保持 HTTPS。HTTP 回环判定拒绝被 WHATWG 自动归一化的简写、整数、十六进制或转义主机，以及伪回环域名和 LAN/外网 HTTP；模型请求地址均拒绝内嵌凭据、查询参数和片段。
- 本机无密钥资格复用已有 Provider definition、registry、HTTP runtime、诊断和 Harness 验证流程。只允许精确回环 endpoint，且实际生效的 `request_headers` / `request_body` 没有递归 `$credential` 引用；未执行的扩展元数据不参与。缺失凭据可走此路径，凭据库错误、已有无效凭据和真实凭据引用仍拒绝。空密钥不生成假的 `Authorization` / `x-api-key`，路由复用已有 `credential_status=not_required`；模型仍须通过实际资格验证。

设计取舍见 [ADR 0150](adr/0150-web-workspace-import-boundary.md)。未新增目录浏览器、注册台账、数据库 migration、模型协议或依赖。

## 已完成的真实 Web 导入演练

证据目录为 `output/playwright/ux-phase-d/`。环境是生产前端、真实 Go API、隔离 home 和本机脚本响应服务，不使用真实付费模型。以下脚本日志中的 API 返回和断言可以核对；截图只是辅助证据。

| 场景 | 已观察结果 | 证据 |
| --- | --- | --- |
| 空库进入、错误路径、取消 | 导入前项目选项为 0；错误可见，没有注册项目；取消后原需求草稿仍在 | `01-empty-import-dialog.png`、`02-invalid-directory.png`、`import.log` |
| 已有目录成功接入 | 新项目立即选中，草稿保留；对话框有明确可访问名称；返回两个非修改/非授权标志 | `import.log`、`03-project-selected.png` |
| 重复同一目录 | 项目 ID、显示名、created_at 完全一致；项目选项仍只有 1 个 | `import.log` |
| 源目录保持不变 | 原 `user-owned.txt` SHA-256 前后相同，目录中仍只有这个文件 | `import-before.json`、`import-disk-verification.json` |
| 原有 140 字符 CLI 名称 | 接入返回原 ID，显示名不超过 128 UTF-8 bytes并有 `...`；UI 接受该投影并选中，草稿保留 | `long-name-fixture.json`、`long-name-cli.log`、`long-import.log`、`04-legacy-long-name.png` |
| 模型设置往返 | 返回新任务后原需求仍在 | `settings-return.log` |

成功导入的项目为 `ws-import-5c7f3edfef9fca0f2b680107`，首次创建时间为 `2026-09-07T21:21:07.1557218Z`。源文件 SHA-256 为 `e3d1d084914d50d60a84faa6331319306b1682ec453ee0a0e4884c4e177dac34`。这些 ID/hash 只对应本地夹具。

`settings-return.log` 同时记录了为重建主动停止专用 API 期间的请求错误。它能证明草稿保留，不能把该停机记成产品启动失败，也不能据此声称最终连接健康。早期导入截图和日志发生在本机模型修复前，不能冒充最终 bundle 的首轮截图。

侧栏收起修复前构建的 `provider-save.log` 已记录通过表单保存 `ux-local-fixture`：HTTP 202，endpoint 为 `http://127.0.0.1:18867/v1/chat/completions`，显式模型 `ux-scripted-model`，`advanced_config={}`，API Key 输入为空。`provider-verification.log` 记录连接正常、耗时 122 ms，Harness 验证通过、模型调用 2 次；`provider-events.log` 有对应一次诊断及两次 probe 请求。这里验证的是显式模型配置和精确聊天 endpoint；发现的既有 `ListModels` 路径拼接疑点仍待核对，不能把使用基础 server URL 的 Go 模型列表测试扩大成所有精确 endpoint 均已覆盖。

## 停止身份和文件引用边界

本批回归发现：停止提交后切到另一任务，异步回调可能使用新的 observer props 更新错误任务。停止 mutation 现在携带提交时的 Thread ID 和 execution ID，成功缓存及结束刷新都归属于原任务。组件测试覆盖等待期间切换任务后，成功与失败结果均不覆盖另一任务。

后继执行的文件引用仍有功能缺口：任务完成/取消后需要后继 Run 时，当前接口不能先附加资料再原子开始后继。界面因此说明实际下一步，暂时禁用新增引用；已有引用和输入保留，用户可以移除引用后发送，等新执行可接收资料时再引用。等待批准也有对应说明。

这只是准确呈现能力，**没有实现后继首条消息携带文件**。不能以前端顺序调用 `/messages`、evidence、`/turns` 绕过：消息已持久入队后，资料附加失败仍可能被其他执行者消费。完整支持需要在 ThreadTurn 所有权内确定后继、附加资料和入队，并把文件清单纳入持久幂等意图；该工作仍待实施。新任务第一条消息的文件引用也仍未完成。

## 自动验证

| 检查 | 结果与范围 |
| --- | --- |
| `npm test` | **101 文件 / 590 测试通过，26.30s**（末尾侧栏收起修复前）；见 `frontend-final-tests.log`。包含本机 URL/无密钥 UI 和前批回归。jsdom canvas 提示仍在，不代表原生 canvas 已验证 |
| 最终构建与窄窗口回归 | **通过**；侧栏修复后 app/sidebar 2 文件 / 15 测试与 typecheck 通过，见 `narrow-navigation-tests.log`、`narrow-navigation-typecheck.log`；随后 `build-navigation-final.log` 构建通过，主 bundle `index-BG1PuCf-.js` 为 1809.47 kB / gzip 474.15 kB。已有大 chunk 警告仍在，不代表首次加载性能实测 |
| 导入组件与主应用定向回归 | 2 文件 / 12 测试通过，10.32s；见 `import-ui-tests.log` |
| 停止/引用相关定向回归 | 4 文件 / 20 测试通过，4.13s；见 `journey-regressions.log` |
| 模型表单、目录、路由定向回归 | 3 文件 / 35 测试通过，9.33s；随后 TypeScript 检查通过。590 项全量已覆盖这些代码 |
| Go 导入、本机模型与契约 | 下列 G1—G5 通过；区别全包与定向范围，不宣称全量 Go 通过 |
| 协议登记 | `protocol-final.log`：33 families / 1001 identifiers / 107 explicit test/golden entries，同步通过 |

本批早期 `frontend-tests.log` 的 584 项结果已被上述 590 项结果更新；不要把两个运行相加计算覆盖数量。

Go 命令与结果由执行代理核对；原始输出仅在本对话工具记录中，没有另存 Go 日志文件：

```powershell
# G1：含旧 140 字符名称的最后导入回归
go test -race -tags 'desktop,wv2runtime.error' ./internal/workspace ./internal/httpapi ./internal/desktop -run 'TestWorkspaceImport|TestDesktopWorkspaceImport|TestControlPlaneSeparatesRunCreationFromExistingRunControls' -count=1
# G2：旧长名修复之前的导入与 OpenAPI 契约检查
go test -race ./internal/workspace ./internal/httpapi -run 'TestWorkspaceImport|TestRuntimeCapabilitiesAreReadOnlyAndDefaultClosed|TestOpenAPI' -count=1
# G3：旧长名修复之前的 CLI 与 Desktop 能力接线
go test -tags 'desktop,wv2runtime.error' ./internal/desktop ./internal/app -run 'TestDesktopWorkspaceImport|TestControlPlaneSeparatesRunCreationFromExistingRunControls|TestAPIServeCLIStartsAuthenticatedLoopbackServerWithoutPersistingToken|TestAPIServeCLIRejects|TestAPIOpenAPICLI' -count=1
# G4：模型注册与 HTTP 模型传输两个包全量 race
go test -race ./internal/modelregistry ./internal/llm -count=1
# G5：Provider、模型目录和搜索相关定向 race
go test -race ./internal/application ./internal/httpapi -run 'TestThreadModelRouteCatalog|TestProviderSearch|TestProviderDefinition|TestModelAvailability' -count=1
```

| 编号 | 包结果 |
| --- | --- |
| G1 | workspace 2.963s / httpapi 2.625s / desktop 1.779s，均通过 |
| G2 | workspace 3.219s / httpapi 6.222s，均通过；包含全部 `TestOpenAPI`，不代表全部 httpapi |
| G3 | desktop 0.383s / app 0.862s，均通过；后续长名变化由 G1 专项复验 |
| G4 | modelregistry 1.671s / llm 1.611s，两个包全量通过 |
| G5 | application 1.211s / httpapi 2.897s，定向通过 |

最终登记检查使用 `go run ./cmd/protocolregistry -check`。`schema-final.log` 另确认 `npm run generate:api` 前后 schema SHA-256 均为 `D91E804FC0E96877BC5D30F1F7699F8A534B614EBB08107E75FFC5B646D9C793`。初次 keyless 测试夹具遗漏响应 usage，被严格解析拒绝；补齐夹具后获得 G4/G5 通过结果，没有放宽生产响应校验。

## 原生与其余未覆盖项

Windows 主机已确认 WebView2 `152.0.4191.66`。本批 `go test -tags 'desktop,wv2runtime.error' -count=1 -v ./cmd/cyberagent-desktop -run '^TestInstalledWebView2RuntimeIntegrityWhenAvailable$'` 通过，包耗时 0.075s、无 skip；只说明已安装运行时完整性预检通过。桥接、安全 tag 和旧长名导入测试不能证明系统目录对话框实际出现。

当前 CUA 的原生控制 API 不可用，本轮没有原生目录对话框 UI 演练或安装包验收。Desktop 空 home 会自动创建 `default` 项目，固定单实例锁也不按 home 隔离；后续原生走查需要验证实际选择的夹具项目，避免使用默认项目或已有进程替代验收。

仍未覆盖：Windows/macOS 完整原生矩阵、真实 provider 能力矩阵、完整审批和 Standard Code 交付、逐文件撤销、大规模性能及全部 F09/F10/F12/F14 边界。本轮未以调整配置或测试夹具代替这些产品能力。

## 真实首轮、最终窗口复验与清理

`first-turn.log` 已通过真实 API 与页面断言：模型设置往返后原需求和导入项目保持；创建 Thread 返回 202，提交首轮返回 202，`execution_started=true`、`model_called=true`、steering `status=committed`；页面出现明确标注的本机脚本响应。任务地址是 `#/threads/thread-run-20260907215055-27cbec8e7580`，Run 为 `run-20260907215055-27cbec8e7580`，workspace 为 `ws-import-5c7f3edfef9fca0f2b680107`，路由为 `ux-local-fixture/ux-scripted-model`。该轮 `tool_called=false`、`capability_grant=false`，不代表真实工具执行或模型能力评测。

`provider-events.log` 当时共有 4 次本机 POST：1 次诊断、2 次 Harness probe、1 次首轮。自定义 Provider 的 keyless 配置和路由已明确；服务脚本中供内置环境模型使用的合成 key 不等于该自定义 Provider 写入系统密钥。宽窗口截图 `05-import-first-turn-wide.png` 已查看，1440×960 的回答、输入与审阅入口可见。首轮对应侧栏收起修复前 `index-CdEhtMlc.js`；后续仅导航行为修改，最终 `index-BG1PuCf-.js` 重开同一任务并继续窗口/导入复验，不把旧首轮截图冒充最新构建截图。

最终 `import-focus-final.log` 在 **390×480** 实测错误导入 HTTP 400 后焦点回到路径输入、Escape 回到触发按钮、草稿保留，纠正路径后 HTTP 200 且原项目 ID/created_at 不变。关闭按钮、取消按钮与发送按钮的包围盒均在视口内；`07-import-error-narrow.png`、`08-import-success-narrow.png` 已逐张查看。源文件在首轮和最后重复导入后再次核对：仍只有原 56-byte 文件，SHA-256 不变。

实测发现并修复窄侧栏导航缺口：打开新对话、模型/设置分类或返回应用后，抽屉原先仍遮住主界面；现在与打开已有任务一致，仅在宽度不超过 760px 时自动收起。`navigation-final.log` 在最终生产构建中连续验证上述操作后遮罩移除、草稿保留、接入项目可点击；宽窗口偏好保持。此次修改没有新增路由层或状态机。

失败尝试保留为 `layout-locator-failure.log`、`layout-sidebar-failure.log`、`layout-close-label-failure.log`：第一项错用窄窗口隐藏的标题栏入口，第二项暴露真实侧栏遮挡，第三项把关闭按钮名称写错。`layout-final.log` 仍是中途失败尝试，最终成功证据是 `import-focus-final.log` 和 `navigation-final.log`。Playwright CLI 在脚本错误时也可能返回 shell 退出码 0，因此这里按日志断言验收。最终新浏览器中的两条 console 错误对应两次刻意发送的无效目录 HTTP 400；未把中途重建停机的旧 console 当作最终健康状态。

本次专用 Chrome `uxphased`（最终 pid 72480）、API（最终 shell session 45414）和本机模型服务（session 44236）已关闭，18867/18868 无监听；历史 session 不应继续等待。夹具和日志保留，没有关闭用户其他服务。清理与最后源文件核对见 `cleanup-final.json`。原工作目录的既有代码与未跟踪文件保留，产品实现仍在独立工作树，未提交、推送或发布。
