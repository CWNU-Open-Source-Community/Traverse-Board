# 交付与 Inspector 信息层级收敛：验收记录

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-11。**本批限定收敛已完成：117 项整合测试、真实浏览器交互/布局、独立数据核对及 Windows 本地包静态验收通过。** 原生窗口与高 DPI 未实测；这不是全部 Inspector 或完整产品成熟度声明。

## 1. 本批目标与实施边界

用户查看实际界面后要求收敛交付视图，并检查 Inspector 是否存在入口过多、技术信息抢眼及 Git/worktree 呈现不清的问题。本批改进已有功能的入口、默认信息层级、展开详情与焦点往返：任务交付以“查看改动 → 提交与推送 → PR 状态”为主线，历史与诊断工具次级可达。

实施树为 `<workspace>`，分支 `codex/ux-journey-convergence`，基线 HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。开工已有 662 项 dirty，按既有工作约定保留。本批没有提交或推送本实现仓库，也没有授权把这些既有改动发布到远端。

范围覆盖任务审阅、Git、PR、Inspector 事件与记录入口，以及已存在的内层 Run/Session 页面导航和输入层级。**不是全面重写全部 Inspector**，没有另建外壳、配置存储、执行框架或恢复机制；不改后端 API、权限、审批及 Git/PR 执行语义。主对话与 Inspector 继续并行导航，共用既有设置与回跳。字体家族沿用 JetBrains/Harmony，本批通过分组、正文权重、宽度和逐步展开改善可读性。

## 2. 形成的界面结构

| 区域 | 默认展示与次级入口 | 必须保留的能力与事实 |
| --- | --- | --- |
| 任务审阅 | 默认三项交付导航；“记录与恢复”容纳编辑明细、检查与交付、执行记录、参考资料、撤销与恢复。任务目录/版本以摘要起步，完整位置和身份可展开。 | 跨 Run 改动归属、已应用/未应用状态、无版本绑定或过期的检查、历史 Run 选择与原请求核对。未应用区域不能笼统写成“待应用”而把拒绝等状态说成待办。正文跳转后聚焦对应导航。 |
| Git | 提交与推送优先；分支、独立 worktree 和暂存管理为展开的高级操作。目录用短名称、真实作用域、分支和短提交显示，完整身份可查。 | 默认不选文件；精确预览后才确认。作者、目标分支/远端、hooks/签名限制与操作标识说明仍可见。目录分类依据来源/当前工作区绑定；不按路径或名字猜 worktree。创建 worktree 是可选独立目录操作，不是提交步骤。 |
| PR | PR 标题/状态、仓库与分支方向、刷新入口、CI/作业和审阅意见优先。连接/凭据、完整 SHA/快照、创建表单和长日志可展开。 | unknown/failed、审批状态、原请求核对、缺本地完成收据、head/base 变化和刷新失败保持直接可见。CI 对应实际提交，不能替未提交工作树背书；评论引用使用完整原始路径与版本证据。 |
| Inspector 外层 | 事件行保留状态、标题、来源、Run、时间与失败说明；阶段/序号和精确身份放入既有详情。全局记录浏览优先，其他工具与资源设置次级可达。 | 单 Run 无筛选时省略冗余范围控件；已有但暂未加载的历史筛选仍显示精确 ID，可主动清除。不吞记录、异常、审批或工具结果；详情、图片证据、虚拟列表、分页、阅读锚点仍保留。相同失败分页只留一个重试入口。 |
| 内层 Run/Session | Run 常用导航优先，其余原页面分组展开；既有仓库页的 Git/GitHub、历史/diff 分开披露。局部输入放入明确说明作用域的高级区域。 | 保留原 tab、能力过滤、组件身份及键盘导航；选中高级页可展开到达，折叠不取消选择。输入仍挂载，草稿和原 mutation 身份不丢；Run/Session 状态、失败、待处理输入队列和取消操作不藏进输入披露项。 |

Inspector 的任务页直接提供“全部运行与会话”，三项当前资源入口收在“诊断工具”；已处于 Inspector 时不再显示无效的“打开 Inspector”。内层 Run 仓库操作仍明确属于当前 Run，没有推断或伪造整个 Thread 的操作入口。

审阅引用与预览“让 Agent 启动项目应用”复用草稿追加路径：Inspector 编辑器默认收起时会显式展开，面板关闭后通过实际 composer 容器 ref 回焦。一次回焦请求绑定 Thread，消费后不因重渲染反复夺焦，切换任务/视图取消旧请求。原草稿及完整引用来源保留；这些按钮只形成草稿，不发送消息、不批准或启动应用。

## 3. 组件、类型与构建证据

最终合并回归：[integration-tests-verified.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/integration-tests-verified.log`），**13 文件、117 项通过，13.85s，exit 0**。包含末次异步失败修正与 SessionComposer 既有行为；此前 12 文件 104 项通过记录仍保留在 integration-tests-final.log，两次不可累加。这是限定前端集合，不是全仓 CI。

下列领域报告记录各自的交互和边界。它们与最终集合及彼此之间可能重叠，**不可把次数相加作为新增或唯一测试总数**。

| 定向领域 | 结果 | 证据与重点 |
| --- | --- | --- |
| Git | 12 项通过；typecheck、局部 diffcheck 退出 0 | [Git 报告]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/git-ui/validation.md`）。精确选择/确认、原 key 重开只 GET、新任务/迟到结果隔离、worktree 回调，及目录绑定、作者/限制可见、切换操作使旧预览失效。 |
| PR | 16 项通过；typecheck 退出 0 | [PR 报告]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/pr-ui/validation.md`）。保留原 14 项审批/未知结果/临时凭据/版本漂移等行为，新验证状态优先、精确引用与披露往返不清稿。 |
| Inspector 外层与记录入口 | 3 文件、18 项通过；typecheck 退出 0 | [Inspector 报告]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inspector/validation.md`）。精确来源、历史筛选、分页重试、状态可见、懒 GET、身份/草稿隔离。 |
| 对话与 Inspector 回填 | 3 文件、40 项通过；新 6 项最终单独重跑通过；typecheck 退出 0 | [回填报告]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inspector/conversation-feedback-validation.md`）。真实 Conversation/Composer/TaskReview/Preview 组件；两个视图回焦、保留原草稿、精确来源和无自动写，既有未知提交/失败恢复回归保持。 |
| 内层 Run/Session | 末次 3 文件、21 项通过；typecheck、局部 diffcheck 退出 0 | [内层 Inspector 报告]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inner-inspector/validation.md`）及[异步失败修正]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inner-inspector/collapsed-error-validation.md`）。可见 tab 键盘导航、精确高级页选择、选中面板保留、折叠草稿和外露失败/队列状态；收起期间的发送失败自动展开，summary 仍呈现处理中或未完成状态。 |
| 任务主导航与总览 | 2 文件、10 项修正后通过 | [root-tests-2.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/root-tests-2.log`）。历史入口适配新的二级结构，保留原执行/交付核对与未知结果断言。 |

最终前端构建 [frontend-build-verified.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/frontend-build-verified.log`） 及其 .exit 均记录成功，入口为 **`index-DyYABdba.js`**，CSS 为 **`index-BueCov_q.css`**。此前 index-BEUMqjRi.js 不含最后的输入错误展示修正，保留但已被替代。构建包含 TypeScript 检查；日志仍有大 chunk 提示，不能据构建通过宣称加载性能已经验收。

各领域报告旁保留源码冻结记录；回填为 [conversation-feedback-freeze.json]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inspector/conversation-feedback-freeze.json`）。最终包应绑定最后完整构建输入，不能仅用某一代理的早期冻结文件代表整个工作区。

## 4. 保留的失败过程与修正归因

- [root-tests-1.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/root-tests-1.log`） 保留 7 失败、3 通过的初次结果：旧测试直接查找原一级历史按钮，且总览断言仍使用旧“Git 页面”文案。修正测试的真实导航步骤与对应文案后，原行为断言保留，[root-tests-2.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/root-tests-2.log`） 的 10 项通过，后续合并集合通过。不能把原失败记录改写成首次全绿。
- Git、PR、Inspector 的部分初次红测把关闭原生 `details` 当作节点卸载，Inspector 另有旧英文默认语言断言差异；按真实可见性/既有语言修正断言，未为测试卸载表单或放宽执行守卫。各领域报告链接保留原日志。
- 新回填测试的首次类型检查缺少夹具必填 `handoff_url`；只补夹具，最终 6 项重跑与类型检查通过，初次 exit 1 仍保留。该失败不归因生产 API。
- 本批确实修正了交互缺口：正文跳转后的导航焦点、Inspector 隐藏编辑器接不到可见引用/启动草稿、失载历史范围无法识别、重复清除/重试入口，以及拒绝提案被“待应用”概括。测试适配与这些产品修正应分别说明。
- 独立复核找到新输入折叠区会隐藏稍后返回的发送失败。真实组件用例先复红，再添加仅展示用的状态回调：pending 在 summary 可见，新发送/phase 错误展开原错误区域；不改请求键、执行或草稿。末次 21 项及整合 117 项通过，独立复核冻结哈希吻合。
- 浏览器 PR 刷新故障脚本初次使用了错误的中文等待选择器而超时（browser-final-pr.log），实际 503 及旧快照均已显示；随后 browser-inspector-feedback.log 按实际文案核对旧快照/SHA，保留失败记录。初次数据核对将 GraphQL 查询 POST 误归写请求，保留 final-data-verification.json；修正为明确检查路径与证据范围，final-data-verified.json 通过，没有掩盖新增请求。

## 5. 早期构建的真实浏览器观察

以下为 **build-1 / `index-Btu4tl4S.js`** 的观察，不替代最终 `index-DyYABdba.js` 的浏览器验收：

- [browser-main-navigation.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-main-navigation.log`）：三主入口、历史可达、返回总览/跳到提交页后的焦点均符合预期，记录的 API 写请求为零。
- [browser-git-layout.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-git-layout.log`）：深色 1280/960/390 宽度，页面/面板横向溢出为 0，无 U+FFFD；正文 14px、400 权重，完整路径/提交及精确所选文件、作者可核对。仅出现一次 Git 预览 POST，`executed=false`，没有执行提交或推送。
- [browser-pr-layout.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-pr-layout.log`）：同三宽深色无横向溢出或 U+FFFD；缺本地收据说明、原 key 核对和完整快照/SHA 可见。仅出现一次 PR 刷新 POST，`created=false`，未创建 PR。远端仍为既有隔离协议夹具，不是实际 GitHub 账号验收。

该早期构建不包含最后的 Inspector 回填与内层 Run/Session 收敛。截图/检查仅证明所观察区域与状态，不表示所有历史内容、主题或功能均已全面验收。

## 6. 真实浏览器验收

使用隐藏 Chrome 会话 uxconverge，API 18890/环回 GitHub 协议夹具 18891；数据沿用上一批独立验收仓库，不是用户项目操作或实际 GitHub 账号。未伪造 Transcript；Inspector 使用夹具库已有的实际审批/检查点记录。浏览器访问产品前端，截图不是设计稿。

- 最终入口 index-DyYABdba.js 已从实际 document scripts 核对。[browser-verified-delivery.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-verified-delivery.log`）：Git/PR 浅色 1280/960/390 无页面或面板横向溢出、无 U+FFFD；worktree 选项可展开；原 PR #17 缺本地收据仍直接可见，重开原键只 GET；引用反馈展开实际 Inspector 编辑器、回焦并保留原稿，写请求为空。
- [browser-verified-inner.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-verified-inner.log`）：最终 Run 浅色三宽无溢出；四个常用 tab，高级三组实际显示 27 个可用原 tab（分析器按现有能力过滤）；收起保持当前高级页，资源 ID 可展开，Run/Session 往返与输入次级入口可达，全程无写。
- [browser-verified-dark.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-verified-dark.log`）：最终 Git、PR、Inspector 和内层 Run 的 1280/390 深色无横向溢出、无 U+FFFD。初次 390 Inspector 截图保留了展开的覆盖侧栏，不作为正文可读性证据；随后主动关闭侧栏，[browser-narrow-content.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-narrow-content.log`） 与截图验证实际正文及展开高级组位于 0–390px，均无溢出。宽窄图片已人工查看。
- index-BEUMqjRi.js 上 [browser-inspector-records.log]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/browser-inspector-records.log`）：真实记录详情有完整 ID/Run/序号，三宽无溢出，Escape 回焦、单一清除筛选、单 Run 省略范围控件。该区域源码/CSS随后未变；最后 bundle 也实测记录可达与深色正文。正文导航回焦在 browser-final-navigation.log 通过，Git 精确预览/作者/完整位置在 browser-final-git.log 通过，仅 POST 预览，未执行。
- 中间构建的刷新 503 是浏览器明确注入的故障，未向远端传递。browser-inspector-feedback.log 核对保留原快照 ghs-7812b7bac61298a7c1bfe2ddbdfcfc12 和完整 SHA，失败文案可见；最终只读核对/引用再次通过。应用预览启动草稿本批由真实组件测试覆盖，不另声称本批浏览器启动过应用。

实际截图：[Git 提交]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/git-verified-light-1280.png`）、[可选独立目录]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/git-verified-worktree-option.png`）、[PR 状态]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/pr-verified-light-1280.png`）、[Inspector 来源详情]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inspector-records-light-1280.png`）、[内层分组]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inspector-inner-verified-expanded.png`）、[窄屏内层正文]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/inspector-inner-verified-dark-narrow-content.png`）。

## 7. Windows 本地预览包

[TraverseBoard-UX-Delivery-UI.exe]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/delivery-final/TraverseBoard-UX-Delivery-UI.exe`），**104,932,864 字节**，SHA256 **`9922f18e8bb573c6b1cae01e6de01d28d8cfc2f16f17afba449cd076ac82fba3`**。v0.1.0-ux-delivery-ui，为本地未提交预览。

构建 exit 0；20 项静态核验通过，1722 项构建输入前后不变，116 个前端文件按实际字节内嵌，最终入口正确。amd64 GUI/manifest/PerMonitorV2/asInvoker/图标/生产构建标记通过。[完整证据]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/delivery-final/native-artifact-validation.json`）及[独立只读核对]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/delivery-final/independent-validation.md`）一致。没有启动该 EXE；Windows 原生窗口、DPI 和签名发布不由此验收。旧 V3 包保留且不含本批收敛。

## 8. 数据核对与现场收尾

[final-data-verified.json]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/final-data-verified.json`）：372 张表中 370 张完整行哈希不变；仅手动 PR 刷新使 github_review_snapshots / github_review_evidence_graphs 各增加一行（3→4）。4 个工作区文件完全不变，实际 HEAD d53b80bf62bc4e420756bc7e96d8a66a3b5c453f、feature/delivery 与 unrelated-staged.txt 原暂存保留。原任务 Run 仍 paused，无活动 lease，外键正常。Git 操作、审批、权限、消息、原 PR 操作身份及缺失本地收据所在表未变。

本批协议线记录 46 个请求，只有一次非 GET 为刷新审阅线程的 POST /graphql；由刷新路径和 read.go 的 reviewThreadsQuery 对应，线日志不含正文，不能称独立捕获核验了该查询的 body。未新增 REST 写路径；累计远端 PR 创建 POST 仍仅上一批的一次。本批前端只有两次 Git 预览 POST、一次真实 PR 刷新 POST，以及一次未离开浏览器的 503 注入刷新。没有执行 Git 提交/推送/worktree 创建、批准或发送模型消息。

自有 fixture 9036→33520→35864 每次均按实际路径确认后正常关闭并重启最终资产；最后 35864 已正常退出，18890/18891 无监听，[fixture-shutdown.json]（本地保留证据，未公开：`output/playwright/ux-delivery-convergence/fixture-shutdown.json`）。uxconverge 已关闭，profile、草稿、请求和库保留，见 browser-close.log。未动旧 18884/18885 或用户原生窗口；这些旧服务的当前状态不在本批关停声明中。

## 9. 验收结论边界

本批有限前端收敛、117 项整合集合、最终构建、上述实际浏览器路径、数据保留和本地包静态核验完成。没有把界面分组替换成新的权限/执行系统，也没有删除原历史与诊断能力。

本批不新增或重新宣称真实 GitHub 账号推送/PR/外部 CI、模型按评论自主修复、原生高 DPI 或全部 Inspector 重构已经通过。此前任务 Git/PR、图片、恢复和上下文能力沿用各自原报告的完成范围；本次界面收敛不能扩大其证据，也不把已有能力重新列为从未实现。
