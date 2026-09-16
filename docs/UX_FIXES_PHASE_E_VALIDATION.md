# 第五批 UX 修复与验证：首条与后继消息的项目文件引用

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-08，Asia/Hong_Kong。实施目录 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c`，承接四批未提交改动。第五批已在下述受控 Web 环境验收；不代表 F01—F14 收敛完成，也不是主分支发布。

## 目标与选择

补齐新任务首条和停止/取消/完成后继续消息的项目文件引用，保留最多 4 个的范围。使用现有 Workspace Explorer 读取与摘要，不新增文件浏览器；以统一 `/turns` 请求中的路径和预期 SHA-256 绑定消息意图，后端附加资料后才持久入队。失败保留输入与引用，可纠正重试且不重复创建任务。

现有证据记录只绑定单文件与 Run，现有消息幂等记录只有消息入队后才存在。第五批为此补 Thread 级持久提交意图和一个前向迁移，复用证据/入队事务核心；必要性及避免额外执行基础设施的边界见 [ADR 0151](adr/0151-thread-turn-file-intent-and-atomic-enqueue.md)。

执行期间或等待审批时带文件消息暂不接受；纯文本保留已有排队。界面须说明等待或移除引用的实际选择，不自动删除文件。此处是当期并发边界，不能宣称文件也能即时干预正在进行的模型请求。

## 已完成的独立小修

模型列表地址：OpenAI Chat 与 Anthropic 的 `ListModels` 现在识别完整 chat/messages endpoint，并保留网关前缀生成 models 地址。Chat/Stream 的地址、精确 endpoint 的凭据绑定及认证逻辑没有改变；Responses 原实现已支持，无需改写。

执行代理先以旧实现运行 8 个真实 httptest 路径场景，复现 OpenAI 404、Anthropic 错误进入模型回退；修复后 `go test ./internal/llm -count=1` 通过（0.524s）。覆盖根基址、`/v1`、完整 endpoint、带网关前缀的完整 endpoint。原始结果在本对话工具记录；没有真实外部供应商验收结论。

## 实现与恢复约定

`thread_message_submission.v1` 在 `/turns` 增加可选 `files`，只接受最多 4 个规范相对路径与小写 SHA-256。沿用既有附件能力 gate；legacy `/messages` 仍是纯文本形状，但共享提交意图校验。v152 意图先预留内容/清单，成功时才原子绑定实际 Run/message；重放不能因后继已创建而另建 Run 或重复消费。

文件准备失败时，只有在写事务中确认同一意图尚无消息，并永久标为 rejected，才返回 `error.message_queued:false`。此键之后不能再入队；前端纠正后使用新 turn key，保留原 Thread。网络未知结果使用原键与原文件确认，不能靠猜测错误码重新执行。成功后清除明确替代的失败链。

草稿/引用在新任务创建后迁移到对应任务；迟到成功只清除仍与提交时字符串一致的文字、按 ID 匹配的对应引用，没有新增文本 revision 或 ABA 检测。未知提交的 mutation 身份保留至本页面生命周期结束或确认成功；成功后在数据刷新尝试结束时移出缓存，不保证每次刷新成功。创建请求身份以已有 Ref 按请求指纹保存，迟到 A 不能清除并发 B 的未知请求身份。没有新增持久浏览器台账，不承诺刷新/退出后的草稿保存。

第五批夹具在 `output/playwright/ux-phase-e/`。隔离数据库通过 SQLite backup 从已停止的第四批实例复制，用来保留已验证的本机无密钥模型；另建项目文件夹和不同资料标记。该准备不属于新的“空实例接入”验收。第四批数据库与源文件不改写。

## 实际检查与真实验证

最终候选前端 101 文件 / 600 测试通过（23.79s），typecheck 通过；此前 598 项基线保留，之后补入卸载六分钟后的未知请求身份保留与跨项目并发创建乱序测试。生产构建 `index-CH-9J5l_.js` 1811.43 kB / gzip 474.85 kB，既有大 chunk 提示仍在。日志为 `frontend-tests-final.log`、`typecheck-final.log`、`frontend-build-final.log`。

第一条带文件的真实 UI 提交已通过：导入独立项目、显式选择本机模型、创建前挑选 `first-context.txt`、设置往返保留草稿与引用、发送并迁移到唯一任务，HTTP 202 / committed / model_called=true。模型服务记录 `UX_FIRST_CONTEXT_20260908`，认证头为空；成功后对应输入与引用清除。`first-turn.json` 保留请求身份，API 同键同清单重放 202；同键去掉清单和 legacy `/messages` 绕行均 409，新增模型调用为 0。对应 `first-turn.log`、`submission-identity.json` 和 `provider-events.log`。

受控慢响应中，运行时文件入口禁用并说明纯文本仍可排队；点停止，原请求返回 499 / CANCELLED，界面出现真实中断状态。本机服务记录连接取消。随后引用 `next-context.txt` 续聊，202 / successor_created=true，Thread 不变、Run 改变；SQLite 证明 FIRST 附件只属于原 Run，NEXT 附件只属于后继 Run。模型上下文包含 NEXT；其中保留 FIRST 是已有跨执行上下文连续性，不是附件写错 Run。证据：`stop.log`、`successor.log`、`persistence-before-correction.json`，04/05 截图。正常完成和取消后的后继路径另由真实 store/model Go 测试覆盖，未将脚本化 `finish` 响应冒称完整 Standard Code 交付。

先在新任务选择两份文件，再改写夹具中的第二份：真实 `/turns` 返回 409 / message_queued=false。SQLite 显示 rejected intent、0 条消息、0 个部分附件；服务调用数没有增加，草稿与两份引用保留。重选更新后的摘要后，在同一 Thread/Run 使用新 key 成功提交，两份资料进入模型；原失败意图仍 rejected，只有一条 committed 消息，没有额外创建请求，旧错误和成功提交的输入/引用消失。证据：`stale-prepare.log`、`stale-submit.log`、`corrected.log`、`persistence-before-correction.json`，06/07/08 截图；纠正后数据库断言的原始输出在工具记录。

再用 Playwright 仅丢弃一次已完成的真实后端响应，模拟结果未知。界面显示「确认上次提交」；编辑新草稿后确认，仍发送原 key/原清单，响应 replayed=true，无新增模型调用；新草稿保留、已确认引用清除。`unknown-confirm.log`、`final-persistence.json`。本轮共 5 次确定性本机调用，包括那一次被停止的请求；没有外部付费调用。重放响应的 model_called/execution_started 是原操作结果，是否新增调用以服务日志核对。

390×480 文件选择器的关闭与引用按钮在视口内，选择后焦点返回引用入口；宽窗口首条引用可见。最后窄窗口发送按钮也在视口内，后编辑草稿完整保留。`file-picker.log`、`unknown-confirm.log` 与 01/02/03/09 截图均已目视核对；这不代表原生 DPI 或全部无障碍矩阵验收。

Go 必要检查：

- `go test -race ./internal/application ./internal/store -run '^TestThread|^TestConcurrentThread|^TestSessionEvidenceAttachment|^TestSchemaV150|^TestSchemaV151|^TestSchemaV152' -count=1`：application 11.263s / store 21.515s 通过。
- `go test -race -tags 'desktop,wv2runtime.error' ./internal/desktop ./internal/httpapi -run '^TestControlPlaneCompletesARealAnthropicCompatibleDesktopThreadTurn|^TestThreadTurnFilesHTTP|^TestOpenAPI' -count=1`：desktop 1.590s / httpapi 4.100s 通过。Desktop 仅为控制面与 tag 检查，没有原生窗口操作。
- 覆盖真实首次/后继文件输入、旧快照跨连接重放、同 key 准备期间重试、已提交在途重放、拒绝后新 key、事务部分失败回滚、双 store commit/reject 互斥、后继准备窗竞争，以及 HTTP false 投影/能力 gate。
- `go generate ./internal/store` 与 OpenAPI/TS schema 同步；协议登记检查 33 家族 / 1003 标识 / 107 显式项通过。隔离数据库从 v151 升 v152，既有 migration ledger 的版本/名称/checksum/applied_at 与备份源完全一致。生产 Go 构建和 diff check 通过。

Go 原始输出在本对话工具记录，未另存日志。没有跑整个 Go 测试集，也没有将测试数量相加当作覆盖率。先前较广的 store race 通过（26.595s），但同次 application 曾因新加重放短路跳过崩溃恢复而失败；保留该失败事实，收紧短路后重跑完整 ThreadTurn race 及上述最终范围通过，不能把先前整次检查写成全绿。

浏览器控制台 6 条网络错误均对应演练事件：重启 API 的 3 条连接拒绝、停止 499、源变化 409、主动丢响应一次；没有观察到另一个 JS 运行时错误。`console-final.log` 保留原记录。

本轮专用服务已全部关闭：模型 pid 66576 / shell 38198，最终 API pid 77560 / shell 95088，Chrome `uxphasee` pid 69292；18867/18868 无监听。`cleanup-final.json` 保存结果。早期 API 18711 已停且最终后端重新构建后才发送首条消息。生成 TS schema 的最终前后 SHA-256 一致，见 `schema-hashes.json`。不要等待这些已结束服务或关闭其他用户服务。

剩余工作：执行中的带文件消息仍需等空闲（纯文本可排队）；文件类型仍限当前项目文件，最多 4 个。逐文件撤销与较窄恢复权限、完整 Standard Code 交付/真实审批矩阵、原生导入与平台/DPI/无障碍矩阵、历史全文搜索和有实测依据的加载优化不在本批完成范围。
