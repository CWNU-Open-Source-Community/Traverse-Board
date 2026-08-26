# Standard Code packaged product E2E

本文定义 Issue #182 的 Product/Delivery 验证切片。它从发布 ZIP 中的真实
`TraverseBoard.exe` 入口执行四语言 Standard Code 闭环，并把候选包、持久化运行事实和
人工 Windows UX 证据收敛为 `standard_code_product_e2e.v1`。该报告只证明 #182，不能
宣布父任务 #140 或 Beta 发布门通过。

## 证据边界

`cmd/producte2e` 不接受 Agent 最终回复作为事实来源。它重新打开已经停止的候选
`CYBERAGENT_HOME/cyberagent.db`，并从以下真实记录重建结论：

- Run、Mission、Session、Provider `route.code`、Standard Code preset、Workspace Trust；
- Supervisor snapshot/ledger、两次以上受审 FileEdit、真实 Command Runtime Job；
- Drydock、当前 revision、完整 Checkpoint、Diff、Artifact 和 delivery receipt；
- 同一 receipt 的 Code Handoff 与最终回复投影，以及同一 Thread 的后继 Run/排队消息。

候选校验会读取 ZIP 内每个 allowlisted entry，逐项核对 size/SHA-256，并要求解压 EXE、
顶层 EXE、ZIP manifest 和 release metadata 指向同一个 clean、reproducible Windows
amd64 revision。固定 oracle 报告必须由 `cmd/packagede2e --verify-toolchains` 生成，且
四个仓库都真实观察到初始失败和固定 repair 后成功。

人工证据只可放在本次 session 的 `evidence` 目录。runbook 使用安全相对路径和
SHA-256 绑定每个文件；路径逃逸、reparse point、文件复用、缺失或内容漂移都会失败。
最终报告不包含绝对路径、截图内容、Provider secret 或命令原始输出。

## 准备真实候选会话

先从待验证 revision 生成 clean、reproducible portable 输出，再在 Windows PowerShell
运行：

```powershell
./scripts/standard-code-product-e2e.ps1 -Mode Prepare `
  -OutputDirectory build/desktop `
  -ExpectedRevision (git rev-parse HEAD)
```

`Prepare` 会：

1. 复用 portable verifier 校验精确 ZIP、EXE、metadata 和 manifest；
2. 在包含中文、空格和长路径的新目录生成四个固定仓库并运行真实 oracle；
3. 在 Go source 中加入一个 tracked 用户修改和一个 untracked binary；
4. 从 ZIP 解压并以空参数启动可见 Desktop，使用全新的 `CYBERAGENT_HOME`；
5. 写入 `standard_code_product_launch.v1`，记录 EXE hash、空参数、PID 和启动时间；
6. 保留 Desktop 和 session，不自动清理、强杀或替操作者完成 UI 步骤。

脚本输出 `product-session.json` 的绝对路径。该文件包含本机路径，只是本地编排状态，
不得上传为 CI/Release artifact。

## 执行四语言闭环

在零参数 Desktop 中完成 Provider 选择、Workspace Trust、backend readiness 和
Standard Code 启动。每个 ready backend 对 Go、Node.js、Python、Rust 都必须使用固定
命令：

| Language | 固定命令 |
|---|---|
| Go | `go test ./...` |
| Node.js | `node --test` |
| Python | `python -m unittest discover -s tests` |
| Rust | `cargo test --offline` |

每个场景需要按真实产品路径完成“初始失败 → 搜索/读取 → 第一次修改 → 测试仍失败 →
诊断 → 第二次受审修改 → 同一命令通过 → 当前 delivery verified”。不能直接应用仓库内
repair patch、调用内部 Go service、伪造 runner、静默 retry、skip 或 waiver。

Go Workspace Trust 完成后、交付前，从另一个 PowerShell 执行：

```powershell
./scripts/standard-code-product-e2e.ps1 -Mode InjectConcurrentEdit `
  -SessionFile <product-session.json>
```

这会在 source `README.md` 追加第二个用户修改；Drydock 交付必须保留它。runbook 的
`concurrent_edit` 使用脚本输出的 baseline/final SHA。`dirty_tracked` 可引用同一文件，
`untracked` 引用 `edge/user-untracked.bin`；CRLF、binary、中文/空格/长路径引用固定仓库
或本次 session 中的实际文件。所有 edge 的 `evidence_sha256` 必须等于文件最终
`expected_sha256`。

Local 与 Docker ready 时都必须各完成四个场景，并且 Job 为
`sandboxed_workspace + workspace_access + network=disabled + credentials=none`。若某个
backend 确实不可用，必须保留一个 `waiting_approval` Run 和 pending Approval；不得批准
宿主执行，也不得改为 Full Access/Debug。该待审批项必须是仍绑定 `workspace_access` 的
`host_command_propose/risk_escalation/per_call` proposal，而不是预先扩大 Run 权限。runbook
将其写成 `approval_required`，并提供 readiness 与 Approval UI 两个独立证据文件。

## runbook 与人工证据

`standard_code_product_runbook.v1` 是严格 JSON：未知字段、第二个 JSON value、重复项、
非安全相对路径都会拒绝。顶层固定字段为：

| 字段 | 要求 |
|---|---|
| `issue` | 必须为 `182` |
| `candidate_sha256` | 与精确 ZIP 内 EXE 相同 |
| `fixture_manifest_sha256` | 与当前内嵌 fixture manifest 相同 |
| `default_launch` | 空 `arguments`、四个入口状态为 true、三个危险开关为 false，并绑定 `launch/default.json` |
| `backends` | 恰好 Local、Docker；每个为四语言 `ready` 或显式 `approval_required` |
| `edges` | 中文、空格、长路径、CRLF、dirty、untracked、binary、concurrent 八类闭集 |
| `continuity` | `completed`、`failed`、`approval_wait`、`restart`，Composer 均保持可用 |
| `platforms` | Windows 10/11 × 100%/200%，`zh-CN`、中文 IME、键盘/焦点/名称/a11y 均通过 |

每个 ready Run 的 `projections` 必须恰好含 `desktop`、`cli`、`http`、`handoff`、
`final`。五行必须写同一 candidate/run/status/receipt/diff/checkpoint，且各自引用不同的
证据文件。collector 会再与当前 SQLite delivery、Handoff 和最终 assistant message
比对；`stale`、`partial`、`blocked`、`not_run` 或旧 revision 无法生成 pass。
collector 还会逐项核对 receipt 的 Checkpoint timeline、Undo、Rewind 和 Fork 链接确实
指向同一个 Run，而不是仅接受格式合法但与当前 Run 无关的恢复入口。

证据文件建议按以下结构保存，文件名可不同但不得复用：

```text
evidence/
  launch/default.json
  surfaces/<scenario>/{desktop,cli,http,handoff,final}.json-or-png
  fallback/{local-or-docker-readiness.json,approval.png}
  continuity/{completed,failed,approval_wait,restart}.png
  windows_10/{100,200}.png
  windows_11/{100,200}.png
```

平台截图/录屏导出的静态证据必须显示候选 hash 或与同一候选的测试记录一同留存；每个
矩阵行需要真实 Windows build、DPI、中文 IME、键盘导航、可见焦点、accessible name
和 critical a11y 检查。`composer_enabled=true` 只能在 terminal、失败、审批等待或重启
后的 Composer 实际可输入时记录。

## 收集与判定

完成所有场景、保存 runbook 和证据文件后，正常关闭候选 Desktop，再运行：

```powershell
./scripts/standard-code-product-e2e.ps1 -Mode Collect `
  -SessionFile <product-session.json> `
  -RunbookPath <standard-code-product-runbook.json> `
  -ReportPath <new-standard-code-product-e2e.json>
```

`Collect` 拒绝仍在运行的原候选进程、被替换的 EXE、重复 report 路径和未执行的并发编辑。
`cmd/producte2e` 只在所有结构事实、文件哈希与人工矩阵同时闭合时写一次 path-free
`standard_code_product_e2e.v1`，并以自身 `evidence_sha256` 封存。任何缺证、过期、失败、
不可读、无法解释或不确定状态都返回非零；不存在“部分通过”报告。

## 与 #140 的集成边界

本切片是独立 evidence producer。中央 `.github/workflows/release-desktop.yml`、
`scripts/standard-code-packaged-e2e.ps1`、聚合报告与最终 release gate 由 #140 的 C owner
串行接线。集成方只能在精确 candidate hash 与 revision 相同时消费本报告；不能把本报告
的 `pass` 改写成 #140 已通过，也不能用缺失报告、Approval fallback 或人工 waiver 代替
完整发布矩阵。
