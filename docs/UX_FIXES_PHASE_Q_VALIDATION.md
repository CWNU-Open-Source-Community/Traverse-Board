# Q：审批续跑、模型设置与失败反馈

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-10；实施树 `<workspace>`，分支 `codex/ux-journey-convergence`，基线 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。本文仅评定本批范围，未提交、推送或发布；不恢复历史“企业级”或完成度百分比。

**状态：本批限定范围已验收。** 三项及实测中新发现的 Host CP936 乱码、审批续跑丢失具体错误类别已修复，完成对应回归、独立复核、真实 API/UI 和最终只读审计。网页嵌套路径为服务集成与 race 证据；全项目 UX、全仓 CI 和全部原生平台不在这一结论内。

## 用户能感知的变化

- 文件、普通 Host 的准确审批决定可交回创建该提案并等待的模型工作轮。批准文件后，模型仍须通过原有批准、哈希和权限校验调用 Apply；批准本身不是写入。Host 续跑使用已保存的真实命令结果，不再次执行命令。拒绝也交给模型，不要求另发“继续”。
- 同一项目切换模型并正常发送时，新执行保留有效的 backend、交互方式和逐次审批偏好。新 Run/Session 使用新快照；旧单次命令批准、Session grant 和 lease 不复制。旧信任确认已失效时回到预览并要求重新确认。
- 工具参数无效、空白答复和根答复格式无效有分别的失败事实。无效工具请求或空白答复不再进入一次无工具的格式修复，由后续“完成”文本掩盖未执行的操作。非空根文本仍保留已有语义格式修复。
- 审批保存和后续工作轮结果分开显示；已批准未应用、真实非零退出、拒绝执行和结果未确认不会混称成功。已审命令留在主对话，已保存输出按需读取。
- 嵌套网页审批复用原逻辑工作轮的精确模型来源，补齐新操作的终局交接结果。旧缺失收据不回填。

## 改动范围与取舍

复用现有 Thread/Turn 所有权、Supervisor、RunExecutionHandoff、SQLite 事务和权限执行闸门；没有引入新的 Agent 引擎、队列框架或通用状态机。审批自动续跑仍受停止、后来的用户输入、待生效模型/权限设置和未知写入结果约束。

审批等候结束时，原用户消息已 committed；原 handoff-item 触发器仅允许 pending 消息，造成真实绑定拒绝。v157 只替换该触发器，保留旧分支；新增分支核实原 wait、当前 Thread/Run/Session、消息及操作身份，不新增表、列或改历史行。同步生成了 clean-install baseline、OpenAPI 和前端类型。

内部四工具分段承接保留原消息、审批 handoff、用量和 lease；第五个工具以及跨段后才发生的等待也在同一逻辑请求中完成。队列退出在锁内只移除自身 owner，避免迟到审批丢失或旧 owner 删除新执行。

实现和分项证据：

| 方面 | 主要入口 | 详细记录 |
| --- | --- | --- |
| 文件/Host 审批与幂等 | `internal/application/approval_continuation.go`、`internal/store/approval_continuation.go`、`internal/store/approval_wait_origin.go` | `output/playwright/ux-phase-q/approval/validation.md` |
| 模型设置 | `internal/store/thread_execution_settings.go` | `output/playwright/ux-phase-q/model-settings/validation.md` |
| 错误分类 | `run_supervisor.go`、`thread_turn_failure.go`、`thread_model_failure_stage.go`、`internal/runactivity/activity.go` | `output/playwright/ux-phase-q/feedback/validation.md` |
| 审批和命令界面 | `web/src/components/approval-continuation-notice.tsx`、`file-edit-panel.tsx`、`host-command-proposal-panel.tsx` | `output/playwright/ux-phase-q/approval-frontend/validation.md` |
| 嵌套网页交接 | `web_fetch_authorization_resume.go`、`web_fetch_authorization_failure.go` | `output/playwright/ux-phase-q/web-handoff/verification.md` |

## 已执行的检查

以下使用明确的隔离临时目录。脚本化模型响应只验证交互和执行契约，不评价真实模型编程能力；模拟 Host executor 的服务测试不代表 OS 进程测试。

| 检查 | 结果及边界 |
| --- | --- |
| 审批、Host review、HTTP、handoff、工具分段的组合 race | application 12.408s、HTTP 1.563s、store 1.347s，exit 0；覆盖文件实际 Apply、拒绝、链式审批、早到审批、停止、未知 Host、原 key、第五工具和跨段等待 |
| 无关手工审批和更新用户输入 | application race 1.940s，exit 0 |
| 模型配置承接及既有续接 race | application 66.128s，exit 0；真实 SQLite、新 Run/Session、陈旧信任和不复制授权 |
| 失败分类及格式修复 race | application 4.997s、runactivity 1.235s，exit 0；既有非空格式修复回归保持 |
| 嵌套网页批准/拒绝/失败/取消/重开及来源匹配 race | application 5.766s、store 1.481s，exit 0；受控 fetch，不是新的公网浏览器验收 |
| 前端定向 | 4 文件 210 项，exit 0，8.05s；typecheck 通过 |
| baseline/OpenAPI/活动投影 | store 19.754s、HTTP 1.645s、runactivity 0.178s，exit 0；后来审批活动新增检查另过 0.300s |
| 协议注册检查 | 33 families / 1009 identifiers / 107 test entries，exit 0；无需重写注册表 |
| 前端生产构建 | tsc + Vite 通过，7.27s；存在大 chunk 警告，不代表全套 CI 已运行 |
| 审批失败具体分类最终 race | application 15.263s、HTTP 1.089s、runactivity 1.238s、store 1.105s，exit 0；无公开 DTO 形状变化 |
| 最终协议与空白检查 | protocol registry synchronized；整树 tracked diff --check exit 0，只有 LF/CRLF 提示 |

原红测、编写中的编译错误和夹具错误日志保留，各分项文件说明归因；只有列明的最终命令计作通过。

## 独立 API/UI 实测

新 home：`output/playwright/ux-phase-q/ui/home`；生产前端与真实 Go API；正常注册/qualification 本机脚本化双模型，再导入独立 workspace、创建对话。工具由产品执行，夹具只返回模型响应，不通过数据库或文件替产品完成操作。初始 README/user-note 明确是人工夹具。

Thread：`thread-run-20260910120040-36bb7e56602c`。普通模型切换由 `run-20260910120040-36bb7e56602c` 创建后继 `run-20260910120930-839cc17f9b80`；Thread 和项目不变。

| 场景 | 实际观察 | 证据，均在 `output/playwright/ux-phase-q/ui` |
| --- | --- | --- |
| 无效工具参数 | 一次模型请求，工具未执行，无伪格式修复；同对话下一条普通消息成功读取 README | `invalid-tool-ui.png`、`capture-after-invalid-read-1.json` |
| 空白答复 | 一次模型请求，记录真实失败，输入可用 | `empty-response-ui.png`、`capture-after-empty-1.json` |
| 模型切换 | 正常选择 model-next/发送，Local/controlled/trusted/逐次审批 rev2 保持，新模型真实 workspace_read 成功 | `model-switch-preserved-settings.png`、`model-switch-preserved-runtime.png`、`capture-after-model-switch-1.json` |
| 文件批准 | 只点批准，后续模型自动调用 Apply 和 Finish；磁盘出现 61B 文件，无额外用户气泡 | `file-approve-applied-ui.png`、`capture-file-approve-applied-2.json` |
| 文件拒绝 | 模型自动收到拒绝，文件不存在，无 Apply | `capture-file-deny-1.json` |
| 同 key 重放批准和拒绝 | 事件总数 356→356、provider 请求 23→23，无重复执行 | `replay-file-verification.json` |
| Host 非零 | 用户批准一次后真实 PS7 exit 7，模型自动读取实际收据；主界面保留失败与输出 | `host-exit7-output-ui.png`、`capture-host-exit7-1.json`；该次仍乱码，保留为修复前证据 |
| Host 拒绝 | 模型收到拒绝，未产生执行结果或进程 | `capture-host-deny-1.json` |
| 新 Host UTF-8 成功 | 全新受审 argv 含 UTF-8 初始化，真实 exit 0，保存输出及界面中文完整，模型自动读到收据 | `host-utf8-zero-ui.png`、`capture-host-utf8-zero-1.json` |
| 新 Host UTF-8 非零 | 全新批准一次，真实 exit 7，界面正确显示失败且中文完整，模型自动读到真实收据 | `host-utf8-nonzero-ui.png`、`capture-host-utf8-nonzero-1.json` |
| 批准后模型空答复，初次 | 编辑保持已批准、文件不存在，审批保存与续跑失败分别显示；普通下一条只读消息仍可继续 | `approval-saved-next-failed.png`、`capture-approved-next-failed-1.json`；当时具体原因泛化，作为新修复依据保留 |
| 最终 API 跨重启文件批准 | 待审期间安全重启 API，批准后自动 Apply/Finish，61B 文件实际写入 | `final-file-applied-ui.png`、`capture-final-file-applied-1.json` |
| 最终审批后空答复与刷新 | 主对话明确“审批已保存，但模型没有返回有效答复”，文件未写入；刷新后仍显示，普通下一条消息成功只读 | `approval-failure-specific-ui.png`、`approval-failure-reloaded-ui.png`、`capture-approval-failure-specific-final-1.json`、`capture-final-after-read-1.json` |

第一份 `q-file-approve-1` 因本机夹具未解开公开工具证据 JSON 封装，模型在批准后再次等待，没有请求 Apply。该文件仍为已批准未应用，磁盘无文件。旧浏览器缓存还曾用旧 JS 报 no-write 契约错误；真实 HTTP 202 内容有效，刷新生产 bundle 后正确读取。两项驾驶问题单独保留，不算产品 Apply 成功，也不删除记录。

## 本批复现并修正的 Host 中文乱码

真实 Windows Host starter 的 pipe/CREATE_NO_WINDOW 路径下，即使选择已验证的 PS7，stdout 中文也曾输出 CP936 字节 `d6d0cec4`，stderr emoji 已变成 `??`。这次原因不同于早期 PS5 启动失败；字符在进程写出时已损坏，事后猜解码不能恢复 emoji。

新 canonical PowerShell 命令在原始受审命令前固定设置 Console/PowerShell UTF-8、Text 输出；初始化进入提案参数与指纹，不在批准后暗改命令。原始命令仍顶层执行，未嵌套 ScriptBlock；原始字节采集、哈希、退出和进程树回收不变。旧五参数封装只按原身份识别，旧批准和保存输出不重写。

真实 runner race 4.561s、应用 Host 定向 0.671s 通过：中文和 emoji 双通道、exit7、普通 early return、native failure、Write-Error、`.ps1` using/param、函数内 param。新 inline 开头 param/using/常规 CmdletBinding 声明明确提示放进 `.ps1`；这不是完整 PowerShell 解析器，也不保证任意 native 程序或显式改编码命令全部为 UTF-8。详细边界和红测见 `output/playwright/ux-phase-q/host-encoding/verification.md`。

新版 `cyberagent-ux-q-utf8.exe` 的 UI 批准两次分别得到 receipt `host-exec-77c0193810bc26ec732b23d4`（exit0）和 `host-exec-16da9d35bbd8d9f9391d1d52`（exit7）。保存输出都显示 `UX_Q_HOST_ACTUAL_OUTPUT 中文`，截图已目视。它们是新操作；修复前 `host-exec-4eae862e25b0d40b92ce076e` 的乱码保持不变。

## 最后封存与现场

审批续跑走独立收口，最初未保留普通 Thread 失败已有的 failure_stage。修正只在精确 handoff、原输入、失败 attempt 和当前 lease 匹配的 completion 事务中封存闭集类别。活动及现有继续提示读取该封存字段，不能借用其他 attempt 的失败解释历史。取消、未知写和旧事件保持原边界；详细记录为 `output/playwright/ux-phase-q/approval-failure-stage/validation.md`。

最终 UI 在新编辑 `edit-295c707ebc8adcb9b50d37c79dbfa20c` 的批准后空答复中实测具体分类；刷新后仍保留。该提案无 Apply，磁盘不存在。此前 `edit-216511cacee647091e39df38bc9c45ba` 的通用错误保持原样。随后普通只读消息执行成功，当前 checkpoint 为 idle/next_turn24。

`ui/final-audit-1.json` 于 2026-09-10 21:02 +08 以 SQLite mode=ro/query_only 执行，117 项断言全部通过：两个 Run 的活跃/未完成操作、pending 工具和用户输入、未知 Host/Apply、开放工作区事务、命令及终端均为 0；10 个审批 handoff 精确关联原消息并有终局，16 条用户消息没有因审批伪造新气泡。4 个 Host 提案包含 3 次真实执行和 1 次拒绝；6 个文件提案包含 2 个已应用、1 个拒绝和 3 个已批准未应用。后者没有 Apply operation，不能混同未知写或称全部应用完成。初始 README/user-note 哈希、9 个明确历史完整行锚点保持，FK 正常；不是全库历史覆盖。

最终服务为 `cyberagent-ux-q-verified.exe`，SHA256 `132d11d31dbbea2823687b122697433743ee0d48799d97c73a47c4dff3a6985b`，API PID141824/18870；provider PID130732/18871。前端 `index-B9a0fYI7.js` SHA256 `b7e2f92a8f9d1666c046f594ccd51d8dd6b0c1f8fcbd94901a57490f21d4d53b`。Host UTF-8 后只增加失败分类投影；最后文件跨重启和失败刷新在此最终二进制验证。API/浏览器保留供查看，操作前重新核进程身份。最终所选源码和产物 SHA 见 `output/playwright/ux-phase-q/final-artifact-manifest.json`。

浏览器换 API 时出现两条连接拒绝日志，先前缓存 bundle 的问题另有证据；最终刷新后此次观察没有新增控制台错误。不能把重新加载后的计数用于抹去此前错误。收尾脚本第一次因把生成类型误写为 schema.ts 而停止；文档已同步，随后独立修正清单为实际 schema.d.ts，不重复执行文档更新；这不是产品测试失败。

## 历史、环境与仍未覆盖的范围

旧真实工程 API 曾持有用户级沙箱锁。确认其 4 个 Run 均无活跃执行、24 表历史和 8 个工程文件未变后，仅停止该空闲隔离测试进程；新 Q API 获锁后真实沙箱 ready。没有改锁文件、APPDATA 或旧数据库。证据见 `ui/old-service-idle-precheck.json`、`old-service-lock-cause.md`、`stopped-idle-test-services.json`。

旧网页 handoff 缺终局收据、旧 Pro 的 `settled_observed:false`、旧已批准未应用文件和本批修复前乱码结果均保留。此前产品 11/11、独立 13/13 的真实工程结果见 [真实工程验收](UX_REAL_AGENT_ACCEPTANCE.md)，不能用来替代本批验收。

本批不等于 F01–F14 全部收敛。仍有失败提示重复、工具行与用户气泡排序、内部标识和旧 Run 术语、多个实例争锁时说明泛化等 UI 问题；原生 Wails/DPI/键盘、真实 LSP、更多 Host/CLI、跨应用重启草稿和未知请求、全文搜索与性能仍按原必读文件排期。取得模型 response 前的适配器/传输错误尚非全部细分；正常非空模型文本的事实正确性仍需工具证据，不能宣称自动识别所有虚报。
