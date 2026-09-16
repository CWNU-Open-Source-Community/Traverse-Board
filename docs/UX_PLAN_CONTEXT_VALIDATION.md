# 主对话规划、确认执行与上下文查看

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-12。本批所列范围已完成，本地未提交实现，实施树 `<workspace>`。真实模型与原生验证边界见末节。

## 用户可见行为

1. 新对话和当前对话输入区可选择“先规划／直接执行”。复用已有 Plan/Deliver 模式。两种模式切换本身不发送消息；直接执行可处理小修改，不要求先生成和批准计划。
2. “查看计划”展示方案、步骤和验收标准。未发送的正文、文件、图片或上传会阻止忽略新要求直接确认；修改要求在原对话发送，计划更新后需核对新版。确认使用明确可查看的消息，在同 Thread 继续。
3. “上下文”展示当前执行已固定的项目指令来源、附加引用、已保存摘要与前序执行继承内容。来源、版本、公开文本截断及压缩时的计数可查看；不把这些说成当前模型窗口的完整清单。纠正追加原草稿并回焦，用户发送后才提交。

没有另建计划、记忆、审批或执行框架。复用既有 PlanDelivery、ThreadTurn、后继执行、恢复存储、项目指令和摘要账本；新增薄层 Thread Plan 控制与只读摘要投影。

## 真实产品链路（本机协议模型）

隔离 API 18898、Provider 18899 和独立小仓库，未复用用户凭据或工程数据。Provider 只产生固定协议响应；实际消息、SQLite、工具、审批和磁盘写入由产品执行。资格探测与业务调用分别计数，不当作真实模型能力测评。

主界面创建的 Thread 为 `thread-run-20260912020216-1ef644e2a52d`，首 Run 为 `run-20260912020216-1ef644e2a52d`。

| 步骤 | 实际结果与证据 |
| --- | --- |
| 需求与初稿 | UI 选择先规划，实际读取 requirements.txt 后形成初稿；项目未写入。`browser-first-plan.log` |
| 修改要求 | 未发送的新要求阻止确认，返回保留草稿；发送后形成两行中文新版。`browser-revise-plan.log` |
| 确认新版 | 精确选择 `plan-proposal-20260912020532-feb3460a63c9`；一个 selection、一次阶段切换、一条可见确认消息。 |
| 确认回包丢失 | 浏览器将真实 POST 转发后丢弃响应。刷新后需要重新输入内存中的连接令牌；重连仅 GET 原 key `thread-plan-ae8ea306-056b-4eb2-a4fd-45c3deb1c6f0`，没有自动 POST，后来草稿保留。`browser-reconnect-recovery.log`、`confirm-response-lost-*` |
| 审阅与写入 | 实际差异仅新增 plan-result.txt；独立批准编辑意图后自动续跑 Apply→Read→Finish。目标恰为 `PLAN_CONTEXT_SOURCE_V1\n中文验证\n`，唯一 Apply 与收据匹配，四个种子文件、Git HEAD/index 未变。`browser-approve-file-2.log`、`after-apply-verified-*` |
| 同对话继续 | 上下文纠正先追加原稿，零业务 POST；随后显式发送第四条消息并引用 requirements.txt，同 Thread/Run 接收。没有新增 Apply；上下文面板显示实际引用。`browser-context-correction.log`、`browser-same-thread-followup.log`、`after-same-thread-followup-*` |
| 再次规划／直接执行 | 真实 UI enter_plan 在原 Thread 建立后继 Run `run-20260912023057-71513895c2db`，继承 15 条历史，未复制旧 proposal/selection；随后 enter_deliver 仅切阶段。各一次 POST，零新增消息/模型/工具，草稿保留。`browser-replan-same-thread.log`、`browser-direct-without-plan.log`、`final-mode-*` |

证据目录：`output/playwright/ux-plan-context`，独立账本/目录审计位于其 `acceptance-host` 子目录。四条输入均 committed，审批续跑没有伪造额外用户气泡。后续请求真实保留原目标、两行修正、文件保护限制和精确方案选择。交互回合结束后计划工作项仍 Pending，不以模型 finish 虚构模块全部验收完成。

## 边界修复与定向验证

- Plan 确认绑定最新提案、输入消费顺序和模式版本；跨进程修正、旧选择、两个确认 key 及 Stop 不得产生错误执行。原 key 跨 action/Run/正文禁止串用；GET 使用 DEFERRED 只读连接，未知结果不自动重发。重新规划使用同 Thread 后继，保目标/限制，不复制旧 selection 或批准。
- Plan 原有 Host 执行工具漏门已修；已封存操作原 key 回读和 Plan 下拒绝仍可用。交互 Thread 的 finish 按当前回合处理，不自动完成工作项，非交互与 Plan 完成门保持。
- 真实模型请求中的 root/work_board 旧提示也按相同精确 Thread 绑定同步，首次组装与上下文压力后的重组一致；不再用“所有待办完成”阻止交互回复正常结束。`thread-prompt-validation.md` 的 6 项 race 通过，保留旧提示红复现；与其他集合重叠，不累加。
- 实际回归发现模型菜单延迟焦点会抢输入，已同步收敛；菜单 pending 禁用导致焦点回 body 的情况仍正确归还。实际计划工具记录归入可展开活动，保留独立兼容记录和真实失败；不把重复协议阶段当多次计划执行。
- 实际截图发现新面板透明背景透字，已使用现有不透明主题表面；项目根指令空 scope 显示“项目根目录”。

| 验证 | 范围与结果 |
| --- | --- |
| 前端联合 | 9 文件 110 项通过，`frontend-joint-verified-2.log`；之后仅根指令范围文案修正，Context 9 项再通过。集合重叠，不累计。 |
| Plan 后端 | 最终 Plan 三包 21 项、相关兼容 37 项及后继 2 项 race 通过；集合重叠。详见 `plan-backend/validation.md`。 |
| Host / 结束回合 | Host 定向 11 项及交互结束回合 10 项 race 通过，详见 `host-plan` 报告；与前述集合有交叉。 |
| 上下文摘要 | 3 项 HTTP/真实 SQLite 底座测试；其中真实保存两条摘要后只读查询并核数据不变。继承/过长/错误来源使用明确投影夹具。Context 9 项和 Composer 实际附件边界 3 项通过。 |
| HTTP 契约 | 三项 OpenAPI 检查，包括 238 路由子项通过；补全测试装配和六处旧成功状态码说明，没有改变 Git/PR 运行时。协议登记检查通过。 |

早期失败日志保留：模型菜单焦点失败是真实缺陷；首个浏览器刷新脚本停在连接页，后续正常重连完成；首个编辑批准脚本因按钮完整可访问名称包含文件名而没有发出 POST，修正选择器后的唯一 POST 才算批准。没有把这些失败记录改写为通过。

## 交付与未验范围

最终 `web-build-final-2.log/.exit` 类型/生产构建退出 0。API4 载入 JS `index-ClWGqPRn.js`、CSS `index-BmsFIHiF.css`；实际 HTTP 字节与磁盘一致。前一个 API 在启动时缓存目录资产，不能把浏览器刷新说成更新到最终包；最终视觉验收是在 API4 明确切换后执行。

Plan/Context 两面板 × 浅深色 × 1280/390 共 8 组真实布局检查通过：页面及面板无横向溢出，无 U+FFFD，面板 14px、说明 13px，背景为不透明主题色。已目视代表截图，文字不再透底。主对话窄屏及 Inspector 往返也已实测；原草稿和上下文入口保留，零业务 POST。截图以 `final-plan-*`、`final-context-*`、`final-inspector-*` 命名，完整命令在 `browser-final-light.log`、`browser-final-dark.log`、`browser-inspector-roundtrip.log`。页面标题为实际验收需求，长文本按宽度换行或省略，不以造假短标题代替现场。

最终本地包：[TraverseBoard-UX-Plan-Context.exe]（本地保留证据，未公开：`output/playwright/ux-plan-context/delivery-final/TraverseBoard-UX-Plan-Context.exe`）。105146368 字节，SHA256 `a461b95939c40a4e6d1a2ecac165c1f4d8c5c59ddde283142650d57caf488758`，root 独立核同。构建退出 0，20 项静态检查通过，1752 个源码输入构建期间未变，116 个前端资产精确嵌入。PE AMD64 GUI、PerMonitorV2/asInvoker、图标和本批版本信息静态核验通过；**最终 EXE 未启动**。

最后独立审计为 1 Thread / 2 Run / 4 条 committed 输入 / 11 次业务模型请求（另 2 次资格探测）。唯一 Apply 保持，原文件/HEAD/index 不变；新 Run 没有旧批准或运行授权，继承 15 条上下文保留目标和修正。原 Run 的模块状态保持 Pending；idle/live0/FK0。浏览器 `uxplancontext` 已关闭；核对可执行文件、命令行与端口归属后只关闭自有 API48728 / Provider69268，18898/18899 无监听。停前停后 24 张业务表 hash 相同，所有数据与 profile 保留，旧服务未动。证据见 `acceptance-host/final-audit.md` 与 `cleanup-final.json`。

本批不代表任意真实外部模型的规划质量、自然发生的超长对话压缩、所有 Drydock/权限组合或 Windows 原生高 DPI 已验。公开摘要显示和边界通过数据/组件测试，真实短任务没有强行制造压缩。未提交、推送或发布本项目；既有大量工作树改动保留。
