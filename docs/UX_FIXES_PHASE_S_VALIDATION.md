# S：失败反馈、命令结果层级与 Windows DPI 资源

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-10，实施树 `<workspace>`，分支 `codex/ux-journey-convergence`，基线 HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。用户在 R 剩余项截图上要求继续处理，随后明确“先完成后台验证，原生窗口稍后检查”。本批后台与浏览器限定验收已完成；原生高 DPI 显示尚未验收。所有 A–S 修改仍未提交、推送、合并或发布。

## 结果与实现边界

- 同一次失败只保留一处主要解释。普通失败按 Thread、Run、原消息和事件序号精确关联；审批失败按 Run 与原 handoff 关联。不同历史失败、工具退出码与审批状态仍独立保存。不以同文案或时间接近猜身份，不修改历史事件。
- 已提交但执行失败的原消息不再留作待发送草稿。只有当前新请求收到合法、属于当前 Thread 的封存失败引用才按“输入已受理”清除原稿；仍显示执行失败。复用 Composer 现有条件清理，保留等待期间新写的内容和新增文件引用。未知提交、旧无引用错误、跨 Thread 错误及原 key 确认仍保留草稿和核对路径。
- 新普通 Host 结果默认显示独立 stdout/stderr、保存字节数、脱敏与截断事实；原始封装和技术信息可展开。对话提案与代码交接复用一个小组件，退出码、超时/取消等事实不隐藏。已审历史的环境说明折叠，待审批命令的范围和校验信息继续完整显示。
- 在已有 result JSON 中增加可选 `saved_output`，与真实 result/request 绑定，不新增表或迁移。两路共享 16 KiB UTF-8 保存预算；`utf8_bytes` 表示保存后的公开文本字节数，不能与原始收据字节数混同。`redacted:true` 表示经过现有脱敏器，不保证识别全部秘密。输出里的 `stderr_begin` 等字符串只是数据。
- 旧结果及独立的 risk_escalation 结果协议没有此结构时保留原始记录，不从文字分隔符倒推 stdout/stderr；不回填旧记录。既有 CR→LF 脱敏规则会使 CRLF 输出保留空行，本批未修改该规则。
- Windows 原生基线 PE 确实没有 RT_MANIFEST。现在 amd64/arm64 共用 Wails 的 PerMonitorV2 manifest，并保持 asInvoker。原 15 个图标图像完整；新增 manifest 使资源 ID 顺移，不能称原资源字节完全不变。

详细实现与定向证据：[失败身份](../output/playwright/ux-phase-s/failure-feedback/validation.md)、[草稿补修](../output/playwright/ux-phase-s/failure-feedback/sealed-draft-followup.md)、[Host 契约](../output/playwright/ux-phase-s/host-output/validation.md)、[DPI 资源](../output/playwright/ux-phase-s/dpi-manifest-validation.md)。

## 实际交互与命令验收

在全新 S home/workspace，通过正常 API 注册本机固定响应模型、导入项目及创建对话；通过生产 UI 设置本地执行、受控/信任项目和逐次审批。根助手独占业务驾驶；子助手只读核验。固定模型响应验证交互，不是新一轮真实模型编程能力评测。实际文件工具、Host 进程、审批、保存与读取均经过产品正常路径。

| 场景 | 实际结果 |
| --- | --- |
| 普通空白回复 | 首次失败只显示一条解释；暴露旧稿残留后窄修并新建一次空白场景复验。新请求 HTTP 412 带精确失败引用，原稿为空、空发送按钮禁用；刷新后仍保留失败。 |
| Host 成功 | 一次审批、一次真实 PS7 执行，exit 0；stdout 中文与字面 `stderr_begin`，stderr 中文独立保存。 |
| Host 失败 | 新提案一次审批、一次真实 PS7 执行，exit 7，界面标失败，未伪报通过。 |
| 文件审批后空白回复 | 审批已保存，后续执行准确报模型空答复；文件仍 approved/not applied，目标不存在、无 Apply operation。关闭审阅面板后主对话一处具体失败说明；重启服务并刷新后保持。 |
| 同对话继续 | 普通下一条消息实际完成 `workspace_read README.md`，无新命令/应用/文件写入，输入区恢复可继续。 |

真实 Host：proposal `host-command-proposal-53151cc10579c7fc2347a814` → receipt `host-exec-ed08e52e9dfa04045d920453`（0）；proposal `host-command-proposal-b9f618ddceab0c5d61f8f250` → receipt `host-exec-b03978d5dbce0682aec663db`（7）。保存 stdout 46 B、stderr 20 B，中文无 U+FFFD、均未截断。原始收据 SHA 与实际预期 UTF-8/CRLF 相符；4 个 Host 表加精确关联 Session 共 10 行前后完整行 hash 相同。[Host 只读审计 33/33](../output/playwright/ux-phase-s/host-output/actual-host-audit.md)。

[终局只读审计 78/78](../output/playwright/ux-phase-s/ui/final-audit-validation.md)：六份 UI 发送脚本正文与六条原消息逐字、顺序及 SHA 匹配；普通空白失败两次、审批后空白失败一次，均精确归属。三个审批 handoff 有原 wait/attempt/终局。两种分页读取均 115 公共项、对象完全一致，用户/失败各出现一次。最终 checkpoint idle/next_turn 10，pending 输入/工具、活跃租约、未知 Host/Apply、开放工作区事务和未完成 handoff/job/terminal 均 0；FK 正常，README/user-note 原 hash 不变。此处不是全库所有历史行 hash 审计。

## UI 显示检查

生产前端实际加载最终 `index-q3edfXHv.js`，不是旧服务缓存。实际目视与 DOM 检查包括：

- 浅/深两主题各 1036×905、390×905 的新 Host 输出；无页面横向溢出，两路输出为 13px，深色实际 computed font-weight 为 400。输出文字、标签及折叠项可辨认，中文完整。
- 390 深色原始记录展开、收起；长 ID 在块内换行/滚动，文档宽度仍 390。原封装内容保持。
- 1440×960 深色“检查与交付”中的真实 Host 保存输出，stdout/stderr 与主对话相同；执行详情和原记录默认折叠。
- 最终浅色 1440×960，全部较早记录加载后，两个不同的普通失败各一条、审批失败一条，原六用户消息保留；空草稿、发送按钮禁用。

截图：[浅色窄窗](../output/playwright/ux-phase-s/ui/light-390-final.png)、[深色窄窗](../output/playwright/ux-phase-s/ui/dark-390-final.png)、[原记录展开](../output/playwright/ux-phase-s/ui/dark-390-raw-final.png)、[交付页输出](../output/playwright/ux-phase-s/ui/handoff-dark-final.png)、[最终对话](../output/playwright/ux-phase-s/ui/final-conversation.png)。不是每页面×每尺寸×每主题全组合验收，也不把夹具英文回复当界面乱码。

## 定向验证与真实失败记录

- 失败反馈初版前端 7 文件 73 项通过；清稿补修最终 6 文件 69 项通过。这两组重叠，不相加。原稿、新稿/引用、未知结果及不同任务保护均有回归。
- Host 展示最终 3 文件 19 项通过；Host API parser 定向运行是一个包含多个子场景的测试，不按子场景虚增测试数。
- 失败相关 application/threadtranscript/httpapi 定向与 race 通过；Host runner/store/httpapi/application 定向与 race 通过；OpenAPI 三项最终检查通过。
- typecheck、两次前端生产构建、API build、原生资源检查及普通 Windows 构建通过，最终 diff 检查通过。Vite 大 chunk 警告仍在，未声称优化首屏性能或全仓 CI 通过。
- 保留早期红测：并发共享文件缺 unicode 导入、前端身份校验/测试夹具问题、原稿确实未清等；修复后的结果见各子报告，不删除原日志。
- 终局初审唯一红来自脚本把 checkpoint 保留的历史 lease ID 当活跃租约；实际租约已 released。修审计为验证同 ID/generation 的真实状态后 78 项通过，产品没有为审计改状态。
- UI 驾驶异常另记：首次 send-empty 监听错 `/messages` 而真实接口为 `/turns`，已发一次但监听超时，没有重放；390 下点击不可见顶栏设置超时，恢复宽度后正常；最终分页按钮已卸载而循环再次点击超时，随后只读快照确认记录已加载完并独立取证。均不据工具退出码宣称产品结果。

## 原生边界及真实剩余

最初 R 前端诊断基线曾在独立 home、dummy 本机模型配置下启动；隐藏窗口无句柄，后续可见基线激活失败，捕获出现其他全屏应用内容，无法据此判断原生渲染是否正常。没有输入到用户的其他应用、没有操作密钥授权窗口。用户要求后台优先后，核对 PID/路径并关闭了我们自己的基线窗口。没有改变系统缩放或安全设置。

最新普通包 `TraverseBoard-UX-S-native-2.exe` SHA256 `e3661d9b14f2991f1c795e40b6897a63228e6de040dc500b1cbad821273ca0db`，不含 devtools、未启动。2,372 源/资源文件（含 116 个 dist 文件）构建前后无变化，最终 PE 含准确 manifest、完整 15 图像；arm64 仅验证资源，不是平台运行验证。[包冻结证据](../output/playwright/ux-phase-s/native-artifact-freeze-2.json)。

后续仍需：实际 Windows/WebView2 高 DPI、多屏切换与 Acrylic；全键盘/屏幕阅读器；权限与交付页其余内部术语/枚举、交付页静态 Run“运行中”与当前工作轮 idle 的表达、未配置 Standard Code 时的冗余信息。此次没有清空所有内部信息，也没有处理旧 risk_escalation 输出结构、历史乱码恢复、真实 LSP、全文搜索/大历史性能和其余 F01–F14。原生检查由用户明确延后，不再强行打开窗口。

## 当前环境与续接

- 当前 API PID **150464**，`127.0.0.1:18874`，S `cyberagent-ux-s.exe`，SHA256 `540517a6591f2de89d30b4c1de3ddaea62685d16c73bab375df51dfff93d9d25`。
- S provider PID **125240**，18875；脚本化本机响应，无真实供应商密钥。Q 旧 provider PID 130732/18871 仍空闲；R API PID 105128 已在 Q 只读 117 项通过并核身份后停止。S API 131720 是前一实例，已在空闲核对后被 150464 替代。
- S Thread `thread-run-20260910151911-eb9055a4f0b5`，Run `run-20260910151911-eb9055a4f0b5`，Session `sess-20260910151911-503158cb08f3`，Workspace `ws-import-686b3524b2074fe692d18ff6`。
- 最终 JS SHA256 `bcc22f596e7ee06ed05c6939cc3ffcaf25aa14af81c515cb32bdd50c5d924de0`。API 会启动时缓存前端，后续换包需核当前实际 JS 和空闲状态，不能只重建 dist 就称已验新前端。
- 终局浏览器 `uxq` 留在浅色 1440×960 的 S 原对话、侧栏显示、全部较早记录已加载。原生测试窗口已关闭；所有子助手冻结。后续操作先核身份，不照抄旧 PID 盲停。原工作树仅同步工作必读。
