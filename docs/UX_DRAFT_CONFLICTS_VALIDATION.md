# 多窗口草稿保护：实现与验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-12，Asia/Hong_Kong。对应用户在功能优先级建议后说“开始吧”。

本批完成同一设备恢复存储范围内的多窗口草稿保护。正文、文件引用和图片作为一个完整版本保存；并发修改保留分歧，确认原请求只消费实际发送的版本。它不代表全项目已达到成熟产品的完整能力。

实施树为 `<workspace>`，分支 `codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。开始时 671 项既有未提交内容全部保留。本批没有提交、推送、合并或发布本项目；原工作树只同步工作必读文件。

## 1. 用户可见行为

- 同任务的空闲窗口接收另一窗口的新草稿；正文和附件一起更新。
- 两个窗口从同一旧版本分别编辑时，两个完整版本都留下，不能按修改时间静默覆盖。冲突未解决前可继续编辑，但不能提交新消息。
- “查看版本”可比较完整正文、文件路径与摘要、图片及版本信息；选中一份再确认，只改变草稿，不发送。需要合并文字可先复制，选稿后继续编辑。
- 比较期间版本再次改变，原选项失效，必须重新核对。发送前同步读取当前存储并校验实际看到的版本，不能只依赖稍后到达的 storage 事件。
- 已受理请求只清理它实际发送的草稿版本。后续相同正文加新图片、晚完成的上传、另一个窗口的新稿不会被旧请求清除。
- 丢失响应或重开后，沿用原请求身份查询结果，不用新身份自动重发；纯图片创建新对话也清理正确的新对话来源草稿。
- 同任务的主对话和 Inspector 共享草稿。Inspector 收起消息编辑器时仍显示冲突提示；长比较区可滚动，窄窗口不会与记录区的筛选文字重叠。

## 2. 实现与取舍

`draft-document.ts` 复用设备恢复存储，每个窗口/编辑阶段写自己的分支记录，用精确版本祖先判断继承与分歧。没有共享的最后写入 head 键，不依靠时间戳排序，也不把 localStorage 冒充跨页面事务。`draft-context.tsx` 将完整快照连接到 React，并订阅同存储前缀。

创建与 Turn 的恢复账本增加本地 `draftVersion`，包含草稿作用域与分支/序号；它不进入发往后端的请求体，不改变原权限和幂等身份。新对话首条消息保留其原始新对话草稿作用域，受理后按原版本结清，不把同一草稿再次复制到新 Thread。已有未知创建按实际请求内容匹配，而非仅因本地版本变化另建请求。

图片上传捕获发起时的完整版本和作用域；上传完成不能写到稍后切换的任务或已选定的新分支。项目变更时上传状态分开保存，初次加载项目不重挂载输入框，因此不会丢焦或漏掉正在输入的字符。

有效旧正文/文件/图片恢复数据迁移成完整快照。旧附件损坏时保留可读正文并提示，不能把错误当成空稿。现代草稿不再从旧三键反复覆盖；项目尚未选定时的旧无作用域正文，以及旧无版本请求账本继续兼容。旧无版本请求只能消费未经继续编辑的精确迁移版本。不是删除全部兼容代码，也不承诺新旧应用版本同时写旧键能实时互通。

内存仅保留当前头、选中稿和近期版本，上传持有自己的快照；持久化分支不擅自按年龄删除，避免删除仍被旧窗口使用的版本。本批没有新增服务端草稿服务或协同编辑依赖。协议标识 `v2_draft_document.v1` 登记到既有 Thread/Run/Session 账本协议家族，没有新增后端数据库迁移。

## 3. 定向验证

证据根目录：`output/playwright/ux-draft-conflicts`。

| 范围 | 结果与证据 |
| --- | --- |
| 最终关联前端测试 | 15 文件、165 项通过，`related-tests-final.log`；含纯分支、真实 React 多 Provider、旧存储/恢复、新对话、Turn、图片与冲突组件。不是全仓 CI |
| 类型与生产构建 | `web-build-5.log` exit 0；最后的 CSS 修正后再次 typecheck/build。入口 `index-DsvJhcfW.js`，CSS `index-5WqCM1Dz.css` |
| 协议登记 | `go run ./cmd/protocolregistry -write` 与 `go test ./internal/protocolregistry` 通过；登记文档同步 |
| 双页面真正分歧 | `browser-concurrency-3.log`；同 BrowserContext 两个实际页面共同旧基点写入，完整头分别保留不同正文、文件和图片 |
| 刷新、选稿与跨项目 | `browser-reload-final.log`、`browser-selection.log`；刷新保留两头，比较期间改动使旧选择失效，重新选择后继续编辑，另一项目草稿独立 |
| 原未知请求与晚到附件 | `browser-unknown.log`；一条真实请求已受理后丢响应，另一窗口相同正文补图，原请求只携原小图 |
| 真正关闭再重开浏览器 | `browser-reopen-verified.log`、`reopen-requests.log`、`reopen-original-headers.log`、`reopen-original-response.log`；原 key GET 200 completed，零新的 Thread POST，正文和两图保留 |
| 纯图片新对话 | `browser-pure-image-create.log`；正文为空，一次创建及一次 Turn，来源稿清理，其他任务草稿不变 |
| Inspector 与布局 | `browser-layout-final-3.log`；编辑器隐藏时提示可见，浅/深色各 1280×900、390×844、390×600，无横向溢出、FFFD 和跨区文字重叠；选稿返回主对话，零 Thread POST |

并发验收用真实页面、生产 React 事件和共享 localStorage；为稳定覆盖竞争窗口，暂缓一个页面接收 storage 事件，再同时触发输入。它是明确的调度故障注入，不冒充物理键盘同时输入。主题通过实际页面主题属性切换检查两套 CSS，不作为主题设置持久化的额外验收。

关闭/重开是退出 Chrome 后以同一验收 profile 重启，重新连接产品 API；不是仅重挂载 React，也不是 Windows 原生 WebView2 重启实测。浏览器登录令牌为隔离夹具值，不是用户凭据。

## 4. 请求与附件的独立后端证据

详细只读审计见 `acceptance-host/final-backend-validation.md`、`final-after-browser-audit.json`、`final-pure-image-and-quiescence.json`。

原未知 Turn key 为 `v2-thread-turn-930db34d-8f45-459c-b666-74c2050715bd`，消息 `steer-20260911184323-80301a0f3edb`。丢响应场景有两次浏览器尝试，均同 key/同正文；第二次在网络夹具内阻断，只有一次转发后端。重开后查询通过 `idempotency-key` 请求头携带原身份，后端只存在一个 intent、一条 committed 消息及一次精确完成事件。

后补大图没有混入原请求。第一次模型协议实际收到小图 1973 B，SHA256 `55be83371972aeed5dc26ef739b50d71b5c5bb35b39de747f57f93ea19eedf2c`；大图 SHA256 `da9be5c0ab8e9d9c80bb63870c63e031365d271c3339fccad721edfc86ec3664` 保留在后续草稿。

纯图新对话 `thread-run-20260911184722-96290bc7fc20` 使用 Turn key `v2-thread-create-turn-0ac4fd97-9b13-4738-a8c3-d2db57d07d48`，消息 `steer-20260911184722-f55d5e9309f0`；正文真实为空，精确绑定独立图片 ID `image-9f16b735d177d287090d1a72553afcdee1421c5ef739433bdaa372235ee44c48`。第二次模型请求的原生图片内容实际解码后也是上述 1973 B 小图，不用相同 SHA 代替任务归属检查。

最终累计两条 committed 操作者消息、两次业务模型开始/完成，另两次 qualification 单独计数。原未知请求记录行哈希及 provider 旧日志前缀不变；四个预置 README/user-note 文件 SHA 不变；FK 错误、活跃 lease、待处理消息/工具、Host intent、后台 Job、工作区事务均为零。两个工作轮 checkpoint 为 idle，两条 handoff 已完成。使用真实产品 API/SQLite 加本机固定模型协议，没有调用付费模型，也不由此声明模型实际视觉理解或自主编程能力。

## 5. 实测中修正的问题与证据边界

- 初次把项目变化绑定到整个新对话组件 key，导致输入框重挂载与输入丢失。已改为仅分开上传状态，实际 App/图片测试通过。
- 初次请求匹配把本地版本变化当成新创建；已改成按真实请求内容复用原未知身份。添加和编辑后回到相同正文，不应制造第二次未知创建。
- 初次旧数据迁移把损坏附件当成空稿；已保留正文并阻止不安全发送。旧已知首条执行失败仍属于已受理消息，按已受理规则结清其原稿，保留后续编辑。
- 初次主界面窄屏比较过高，确认按钮在可视范围外；已限制比较 dock 高度并支持滚动。
- 第一轮 Inspector 自动布局检查只核宽度和按钮位置，未捕捉文字交叉。最终目视发现 Inspector 被高 dock 挤小后，固定筛选栏越界绘制，透明背景让文字重叠。已给该冲突状态的记录区保留最小可用高度并裁切滚动，让 dock 可收缩；实际六种布局重新截图、逐项核边界并目视。`inspector-conflict-*.png` 是修正前证据，最终看 `layout-final-inspector-*.png`。
- 初次重开脚本错误地在 URL 查幂等 key；实际 API 放在请求头。保留该失败日志，用真实请求头、响应体和零 POST 记录纠正，不重新发送来凑通过。
- 最后布局脚本的 Windows 多行参数两次发生语法错误、没有执行浏览器步骤；改为单行传参后 `browser-layout-final-3.log` 通过。不是产品发送失败。

测试中已有 canvas mock/React act 提示和构建大 chunk 提示保留，没有把这些当成业务失败，也没有宣称做过全产品性能优化。最后仅 CSS 布局有调整，165 项逻辑测试不重复计数；布局以最终生产构建和真实浏览器重新验收。

## 6. 交付包与收尾

最终包为 [TraverseBoard-UX-Draft-Protection.exe]（本地保留证据，未公开：`output/playwright/ux-draft-conflicts/delivery-verified/TraverseBoard-UX-Draft-Protection.exe`），104952832 B，SHA256 `847a2b61fa023a929576f5e720375a7461bdbb5e148373ca8e45b1cd4a7a20c8`。构建 exit 0，20 项静态检查通过；1731 个构建输入不变，116 项前端资产精确内嵌。root 独立读取当前源码、实际二进制和完整资源再次核对一致，见 `delivery-verified/root-artifact-verification.json`。未启动 EXE；工作必读同步此最终身份。

此前 `delivery-final/TraverseBoard-UX-Draft-Protection.exe`（SHA256 `57782e5788a6f864760f6cacc8c7abb4886866a256b6fbddbce57c9b6297e8d3`）不含末轮 Inspector 布局修正，已被替代，保留追溯，不再推荐试用。

原独立 API/provider 于 02:51 按归属核对关闭；仅为最终 CSS 目视在同 home 以 `layout-final` 标签重开，未重新 setup、qualification 或业务发送。`acceptance-host/final-after-layout-audit.json` 确认消息、事件、intent 全部零增改缺、模型业务仍为两次、四文件不变且无活动执行。03:01 仅关闭重新确认归属的 API 10860/provider 54760，18894/18895 无监听，见 `shutdown-layout-result.json`。专属浏览器 `uxdraftconflicts` 已关闭，数据、profile、截图和请求身份保留；旧服务及用户窗口未操作。

## 7. 尚未覆盖

- 同 backend 存储作用域、同浏览器 origin/profile 才共享本地草稿；不包括不同 profile、跨设备或云同步。
- 浏览器存储容量有限。写入失败会保留本页内容和错误并阻止不安全发送，不能保证刷新后仍有未落盘内容。持久分支没有无限空间保证。
- 不自动合并正文或附件；不承诺混用旧版与新版应用的实时草稿互通。
- Windows 最终 EXE 只完成构建与静态资产检查，未启动；原生窗口、WebView2 退出续接、多屏和高 DPI 仍待实际检查。manifest 不是运行验收。
- 本批不扩入钩子配置、Git 页签往返的文件选择保留、其他 Inspector 生命周期术语、Plan 或全文检索；既有长上下文、恢复、图片/预览、审阅/Git/PR工作按各自既有证据保持。
