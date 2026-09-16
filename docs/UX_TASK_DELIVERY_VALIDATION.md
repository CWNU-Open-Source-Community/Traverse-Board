# 任务级审阅、Git 与 PR 验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-11。实现位于 `<workspace>`，分支 `codex/ux-journey-convergence`。本文件对应用户“继续吧，审阅和git，pr流程都搞上”的产品功能授权；实施项目已有改动没有被提交、推送或发布。

## 当前结果

任务审阅、真实 Git 提交/推送/worktree、GitHub 草稿 PR 协议及反馈返回主对话已经接通，完成本批实现和限定验收。末轮差异行引用的文件身份遗漏与 Git 作者配置兼容问题均已修正并复验。真实 GitHub 账号、外部 CI/模型修复和原生窗口仍按下文单列，不能据此称完全对齐成熟产品。

用户从对话右上角「审阅改动」进入：

1. 「任务总览」按整个任务聚合后继执行中的文件编辑与检查，显示实际目录、版本和读取范围。已应用编辑、未应用提案、任务外变化不混为一类；没有版本绑定的旧检查不能证明当前代码通过。
2. 「Git」选择文件、预览精确差异，再确认暂存、取消暂存或提交；也可以创建/切换分支、创建独立 worktree，或推送当前分支已提交代码。预览绑定当时的内容与目录，漂移时拒绝旧预览。
3. 「PR 与反馈」复用 GitHub 连接和系统凭据，查找当前分支的 PR。没有对应 PR 时可预览并明确批准创建草稿；已存在则直接查看。刷新会重新读取远端 CI、评论和提交身份，并显示抓取时间、过期或失败状态。
4. 差异引用、CI 和审阅意见可以追加到同一任务草稿，包含可追溯来源，保留用户已有内容；不因引用而自动发送消息或授予权限。

## 真实浏览器与 Git 证据

验收服务使用生产 HTTP/API、应用服务、SQLite、Git 和 GitHub 客户端。测试目录由明确的验收 host 布置，不是模型生成的项目。GitHub 使用专门的 localhost 协议端点；没有真实 GitHub 账号、远端仓库写入或外部 CI 执行。

| 验证 | 实际结果 |
| --- | --- |
| 选文件提交 | UI 只勾选 `selected.txt`，实际提交 `d53b80bf62bc4e420756bc7e96d8a66a3b5c453f` 只改变此文件。 |
| 无关暂存 | `unrelated-staged.txt` 保持原索引内容和暂存状态，没有进入提交。 |
| 推送 | UI 明确选择 `acceptance` 本地 bare remote，其 `feature/delivery` 指向上述提交。`origin` 没有被推送。 |
| Worktree | UI 创建 `codex/acceptance-worktree`，目录只包含提交内容；正常导入后打开空的新任务编辑器，默认无网络。原任务目录与权限保持原值。 |
| PR 丢回复 | 夹具先持久保存 PR17 再断开创建回复；产品返回 unknown。原请求 key 跨页面、服务重启通过 GET 找回同一 PR，全部日志累计创建 POST 仅一次。 |
| 收据准确性 | 远端只读核实创建成功后仍明确本地完成收据缺失；没有把旧账本回填成已保存。 |
| 远端刷新 | 第一次读到 failure；改变夹具状态和评论后再次刷新读到 success、新评论、新快照和时间。 |
| 反馈保留 | 失败检查的 SHA、原快照和抓取时间追加进原任务草稿；原中文草稿保留。 |
| 刷新失败 | 浏览器仅对一次刷新响应注入 503；页面保留上次资料并明确本次刷新失败。移除注入后实际刷新恢复。 |
| 布局 | 1280、960、390 宽度的浅色/深色主 PR 面板均无页面横向溢出或替代乱码字符；正文 14px，截图已经目视。 |
| 最后差异引用 | `browser-diff-reference-verified.log` 中新旧文件路径、行号、当前提交和原预览指纹完整；原中文草稿与 CI 来源保留。只有预览/引用，没有再提交该无关文件。 |
| 最后作者显示 | 实际预览显示 `Acceptance fixture <fixture@example.invalid>` 与操作 trailer 提示；最后入口 `index-BBdqH4nq.js` 在1280/390 Git 预览中无溢出或乱码。 |

原始证据：[浏览器判定](../output/playwright/ux-task-delivery/browser-validation.json)、[25 项 Git/SQLite 独立只读核对](../output/playwright/ux-task-delivery/browser-git-oracle.md)、[PR 创建及真实请求顺序](../output/playwright/ux-task-delivery/browser-create-pr.log)、[重启只读核对](../output/playwright/ux-task-delivery/browser-delivery-reopen.log)、[反馈草稿](../output/playwright/ux-task-delivery/browser-feedback.log)、[独立目录导入](../output/playwright/ux-task-delivery/browser-import-worktree.log)。

## 后端与组件回归

各集合有交叉，不把重复运行相加成项目完成度，也不是全仓 CI。

- [ThreadReview](../output/playwright/ux-task-review/backend/validation.md)：8 个顶层 race 测试，真实跨 Run 编辑、Drydock/linked worktree、版本漂移、未绑定旧检查、有界读取及只读 HTTP；两组件原 7 项检查。
- [Git](../output/playwright/ux-task-delivery/git-backend/validation.md)：39 个唯一顶层测试最终 race 通过，包括选定文件、索引发布失败、AD/unborn 取消暂存、原操作核对、远端 CAS、真实 HTTP 和 Drydock 自身检查点游标，来源项目不被混用。
- [PR](../output/playwright/ux-task-delivery/pr-backend-validation.md)：12 个新增唯一顶层关键测试。完整 HTTP 审批、断线、SQLite 重开和原 key 只读观察通过；创建之前的 head/base 漂移、marker、最后远端身份读取失败、迁移159和 GET 无正文合同覆盖。
- [PR 面板](../output/playwright/ux-task-delivery/pr-ui/validation.md)：原预览缺审批显式补齐、拒绝后清记录、未知响应只查询、晚响应不能覆盖另一个请求/任务、临时凭据不持久化、目标分支明确查询、CI 不覆盖未提交代码。
- 最终主 UI 集合 **5 文件42项通过**，生产前端构建通过；结果在 `frontend-verified-tests.log/.exit`、`frontend-verified-build.log/.exit`。此前35项和状态文案后的22项为重叠中间检查，不另行相加。
- [Git 作者补充回归](../output/playwright/ux-task-delivery/git-backend/author-final-validation.md)最后 **12个顶层 race 全通过**（与前39项有交叉）：repository29.066s / httpapi25.612s / application53.938s，`git-backend/author-complete-race.jsonl`。涵盖默认全局配置、includeIf、仓库优先、本地 `~/` include 的实际 Review→Prepare→Publish、包含文件中的可执行驱动拒绝、缺身份、身份漂移和旧key已封存结果重放。
- API 生成合同、OpenAPI 金丝雀/Go DTO 一致性检查、CLI 输出、协议注册及兼容性检查通过。迁移159复用既有本地/远端操作表增加一次开始标记，没有建立平行提交账本。装配复核见 [12 项操作入口](../output/playwright/ux-task-delivery/delivery-final/api-wiring-audit.md)。

## 最终本地预览包与收尾

本批使用 [TraverseBoard-UX-Task-Delivery.exe](../output/playwright/ux-task-delivery/delivery-v3/TraverseBoard-UX-Task-Delivery.exe)，是包含当前本地修改的 Windows amd64 桌面预览包，不是已发布版本。用户可在对话右上角「审阅改动」进入「任务总览 / Git / PR 与反馈」。没有自动启动原生窗口或替换用户正在运行的实例。

- 文件大小：104,906,240 字节。SHA256：`6429de5ca4d95a4b3f72d7efaee1d18e5fbf4500e9d43228a52b4721824303c0`。
- [最终构建及静态核验](../output/playwright/ux-task-delivery/delivery-v3/native-artifact-validation.json)20项通过：Windows GUI 子系统、生产标签、manifest/DPI 声明和全部116项前端资源精确嵌入。入口 `index-BBdqH4nq.js`；1718个构建输入在构建期间未变。
- 独立只读复核的实际 EXE 哈希、入口资源和作者修复的7个最终冻结文件均匹配。最终构建包含共享 Git HOME/include 兼容修正。静态 DPI 声明不代表实际高 DPI 体验已经通过。
- [浏览器证据汇总](../output/playwright/ux-task-delivery/browser-validation.json)17项通过；浏览器 `uxdelivery` 已关闭。最终 HOME/include 后端修正由真实 Git/SQLite/HTTP 完整回归覆盖，浏览器 host 未为该 Go-only 修正重启再跑；不把浏览器记录说成运行了 V3 EXE。
- [自有服务收尾](../output/playwright/ux-task-delivery/acceptance-host/final-lifecycle-validation.json)：PID155876已退出，18890/18891无监听，SQLite、仓库、请求记录、草稿和截图保留。其他历史实例和用户窗口未操作。

之前的 `delivery-final` 与 `delivery-verified` 均有 `SUPERSEDED.md`，只保留中间问题和构建证据，请使用上述 V3 包。

## 实现和验收边界

- 面向已有普通 Git 仓库与 github.com，不含新建远端仓库、GitLab/Enterprise、自动合并或冲突自动解决。当前推送连接支持明确的 HTTPS GitHub 目标；SSH 等不支持的远端保留名称并说明不可用。
- 创建 worktree 从已提交版本开始，不迁移当前任务的未提交修改或权限。拥有固定分支的 Drydock 不能在这里切走分支；独立 worktree 创建入口仅适用于直接来源目录。
- 文件提交走有界文本预览；二进制、过大或不可读的文件不能在无法预览内容时被静默混入。未知结果没有精确完成证据时保持未知，不自动重试。
- 内置 Git 不运行本地 hooks（含 pre-push），提交不生成 GPG/SSH 签名，界面在确认前明确提示；项目要求的这些流程需另行完成。提交说明附带 Traverse-Operation 追踪 trailer，用于只读辨认未知提交。作者身份应明确读取和预览，不能依靠系统用户名猜邮箱。
- 推送先验证快进祖先关系，再用精确远端 OID 和固定本地提交执行并发保护。内部使用 `--force-with-lease` 实现条件更新，不提供历史重写/强推操作；不能把实现描述为完全没有 force 旗标。
- 任务总览依赖已有编辑记录。Shell、用户或其他任务造成的未归属变化在 Git 页面展示，不猜测属于哪个任务。旧 Host exit0 没有 revision 绑定时仍显示未绑定。
- GitHub 创建接口无法原子指定 expected SHA；实现前置核对并对返回后的提交变化作明确标记，不用再次创建补救。CI 快照仅证明对应提交的远端状态，未提交文件另行提示。
- 真实 GitHub 凭据/推送/PR、外部 CI 与模型收到评论后实际修复的完整联调尚未实测。当前证据证明协议、真实 Git、正常 UI 反馈交接，不证明新的模型编程能力。
- Windows 原生窗口、高 DPI、物理键盘/剪贴板本批未验。最终 EXE 构建与静态嵌入验证不能替代原生体验检查。

## 中间失败与修正记录

后端/组件报告保留原失败及归因。验收 host 初次遗漏 GitAdvanced 依赖开关，启动被正确拒绝；补全既有 checkpoint 装配后启动。初次 host 未装搜索就绪服务而出现404，已在夹具中装配现有服务；没有为测试放宽产品授权。期间只在自有服务重启时出现连接中断。最终刷新故障的一次503是明确浏览器注入。

首次本地包静态20项通过后，真实差异引用点击发现文件名缺失，故该包保留在 `delivery-final` 并标 `SUPERSEDED.md`，不能作为最终交付。真实失败日志 `browser-diff-reference.log` 保留；完成修正后另建包，不覆盖它的构建证据。

末轮复核也发现已有 Git 环境忽略全局作者配置，而最初 fixture 使用仓库局部配置，不能证明普通全局配置用户可提交。本轮通过 Git 原生只读配置命令解析姓名/邮箱及 include/includeIf，写操作仍只注入已核对身份，不继承其他全局执行设置。先前只读 `~/` include 通过仍不足：实际提交暴露共享 Git 环境缺 HOME，补上 HOME 并按 includes 检查可执行驱动，完整链最终通过；原 hooks、签名及过滤器限制保持。第二个 `delivery-verified` 包也因此保留为中间包。最终包使用 `delivery-v3`，不覆盖旧构建证据。
