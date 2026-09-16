# Git 页面状态保留与 Inspector 状态统一：实施与验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-12，Asia/Hong_Kong。用户明确要求这两项并授权实施。实现树 `<workspace>`，分支 `codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。开始时681项既有dirty保留；没有提交、推送或发布本项目。

本批限定实现、相关回归、真实浏览器和本地 Git 验收完成。不是全仓CI、原生Windows或模型能力验收。统一证据目录：`output/playwright/ux-git-state-inspector-status`（下文相对证据路径均指此目录）。

## 1. 实际行为与复用边界

### Git

- 复用每个后端连接已有的 TanStack QueryClient，按API地址、Thread和实际仓库身份保存表单；没有新建全局草稿服务、后端表或API。保存文件勾选、操作类型、提交说明、分支、独立目录名、推送远端和凭据名称。凭据名称不等于凭据密钥。
- Git→PR→Git、关闭审阅、设置往返、切任务再返回，恢复各自仓库的选择和输入。仓库绑定变化时不串用旧目录表单；原先选中的文件/远端失效时明确提示，不悄悄移除或替换成origin。
- **保存的是操作表单，预览确认不随页面恢复。** 返回重新读取Git状态；生成预览和真正执行前各自读取当前状态。核对实际目录、Run/Session、HEAD、分支、远端以及服务端绑定指纹（包含索引、文件内容等）。漂移、读取失败、切任务/目录、离开页面或在等待期间修改表单，均不执行原确认。
- 继续使用既有操作key、未知结果恢复和后端指纹校验；不因返回页面或超时自动重发。成功或明确结清后仅消费确认时的准确表单版本，清除其文件选择和旧预览标记，保留其他输入及后来编辑的新表单。
- 文件选择的新增保留范围是**当前应用会话内的页面往返**。原有提交说明、分支和目录名存储继续兼容；不承诺新增选择跨刷新、完全退出或跨设备保留。已经持久化的未知Git请求仍沿原恢复机制处理，不能与普通表单缓存混为一谈。

主要实现：`web/src/v2/components/git-form-state.ts`、`task-git.tsx`、`task-git.css`。TaskReview仍可正常卸载页面，无须隐藏保活所有面板。

### Inspector 与主对话

- Run生命周期 `running` 展示为“可继续”，Session `active` 展示为“会话开放”，均明确不代表Agent正在执行。列表不为每条历史记录额外请求当前活动。
- 具有准确Thread身份的入口复用已有执行查询：running→“正在工作”、idle→“等待新消息”、stopping→“正在停止”、stop_failed→“停止未完成”。未取得、读取失败或身份不匹配时不猜空闲或工作中。等待批准/暂停保持各自语义。
- 单纯发出HTTP请求显示“正在发送消息”；后端已经确认执行或停止时，该事实优先，即使发送响应尚未返回。主对话和Inspector使用相同活动语义。
- 高级Run/Session页显示“来源任务的Agent”，避免将来源任务当前活动归因给所选历史Run；独立Run/Session DTO没有Thread身份，不按路径、最近记录或不存在的execution.run_id拼凑关联。lease占用显示“执行访问被占用”，不等同模型正在工作。
- 已保存的开始事件标为“记录时执行中”，详情说明只描述当时情况。真实工具、Job、CI及未落盘实时事件保持原语义，没有全局替换所有running，也没有改写数据库历史。

主要实现：`web/src/lib/thread-activity-label.ts`、`web/src/components/lifecycle-status.tsx`，以及主对话、Inspector来源区、Run/Session/Thread页、历史列表与记录详情的展示调用。

## 2. 回归与构建

最终 `related-tests-final.log`：**15文件132项通过，11.81秒，退出0**。包括TaskGit、真实TaskReview与PR来回挂载、主对话、Inspector来源导航及旧Run/Session/Thread/记录组件。覆盖范围包括：

- 选择隔离、旧确认失效、文件消失、远端删除、核对中切任务/离开/改表单、读取失败零execute。
- 后端提交成功后的binding变化不再留下旧确认警告；未知结果明确结清；晚到成功响应不消费后来编辑的表单。
- 生命周期与真实活动分开、历史开始和后续完成并存、执行结果错Thread/读取失败、停止与停止失败、HTTP仍在等待时真实活动优先。

领域测试27项Git、62项状态、20项主对话/来源、28项审阅集成等与最终集合交叉，**不累计相加**。早期红日志保留；没有降低行为断言换取通过。末轮 `web-build-2.log` 类型检查及生产构建退出0，入口 `index-BqisyGxV.js`，CSS `index-DQolp02T.css`。既有大chunk警告仍存在，本批未做包体性能优化。没有Go/API/schema变更，未额外跑全仓Go或CI。

## 3. 真实 Git 与页面旅程

使用普通生产CLI API、真实SQLite和两个独立本地Git仓库。数据位于本证据目录 `acceptance-host/home`、`workspace`、`workspace-other`，不是项目源码树或用户原有工程。端口18896/18897、独立profile；固定本机模型协议，不调用付费模型或GitHub。

主任务 `thread-run-20260911194145-ea8584e8b998`，另一任务 `thread-run-20260911194335-bc2165081f6a`。主仓库HEAD `52c04df4dbe83e101b009082b75a18db08fac91b`。每仓库包含未跟踪chosen、工作区修改tracked及原已暂存unrelated-staged；本地bare目标仅供选择。

| 实测 | 结果与证据 |
| --- | --- |
| Git→PR→Git、设置、关闭审阅、两任务往返 | chosen始终只在原任务勾选；提交说明保留，另一任务未继承；显式第二remote及凭据名称保留。旧预览确认不恢复。`browser-git-navigation-2.log` |
| 预览后文件改变 | root在隔离chosen追加一行，实际点击旧“确认提交所选文件”只GET重核，显示原确认失效，选择保留，无commit/execute。`content-drift-fixture-change.json`、`git-requests-after-stale.txt`、`git-stale-confirmation-blocked.png` |
| 重新预览和操作 | 新预览含追加内容；实际stage一次，仅chosen。原key `task-git-d5291fd8-387a-4872-be4e-2121253cc000`，收据保存。`git-requests-after-stage.txt`、`git-fresh-stage-before-notice-fix.png`及独立数据库审计 |
| 最终构建的完成反馈 | 实际unstage一次，仅chosen；原key `task-git-1ad0cfe6-75b2-4bc7-adc5-16f18f2a59ff`，收据保存，选择结清且无旧预览提示，提交说明仍在；再次勾选并PR往返保留。`browser-git-final.log` |
| 版本/数据保护 | 没有commit、push、分支切换或worktree创建；两仓库HEAD不变，unrelated-staged不被夹带或取消。最终索引和status回到初始构型，chosen正文保留本次显式探针行。独立审计见第6节 |

全程5个Git预览POST（3个commit预览、stage和unstage各1），实际execute只有stage/unstage各1；不是“零写入”验收，也不是本项目已提交发布。

## 4. 真实活动与显示

最终 `browser-activity-verified.log`：发送一条真实Thread消息，固定provider收到请求后短暂等待；主对话、Inspector主标题和来源任务均显示“正在工作”。高级Run同时显示“任务状态：可继续”。约4秒后明确释放同一响应，来源活动变成“等待新消息”，Run仍“可继续”。该时序来自产品执行事实，没有伪造数据库状态。

`browser-inspector-final.log`：对精确Thread执行GET注入503，当前活动显示“执行状态暂不可用”，不继续显示缓存的空闲/工作状态；恢复后通过正常刷新重新读取。历史模型开始记录显示“记录时执行中”，详情有时点说明，而主标题仍“等待新消息”。故障在浏览器网络层注入，非真实服务宕机。

`browser-sidebar-session.log`、`browser-record-lists.log`：真实Session页“会话开放”、绑定Run“可继续”、来源Agent“等待新消息”；全Run/Session列表语义一致，打开/查看不启动执行。

最终Git、Run来源状态及历史详情分别在1280×900、390×844浅/深色截图并检查；无水平溢出、FFFD替代字符或状态文字跨区重叠。root目视检查窄屏历史详情、Run来源、Git选择与完成结果。范围限这些页面，不宣称全部面板、所有字体或Windows各DPI全验。

代表截图：

- [Git 保留选择，窄屏深色](../output/playwright/ux-git-state-inspector-status/git-preserved-dark-390-final.png)
- [真实执行与任务可继续并存](../output/playwright/ux-git-state-inspector-status/inspector-source-agent-running-final.png)
- [等待新消息与历史开始记录](../output/playwright/ux-git-state-inspector-status/inspector-history-idle-dark-390-final.png)
- [实际操作完成且无过期提示](../output/playwright/ux-git-state-inspector-status/git-completed-no-stale-notice-final.png)

## 5. 验收中发现并纠正的事项

1. 成功执行后旧form.basis/previewed未结清，新binding触发错误的“选择仍保留/旧确认失效”。通过真实stage暴露，已修精确表单消费并补27项集合中的相关回归；最终unstage实测确认没有旧提示。
2. 发送响应等待遮住实际running，主标题停在“正在发送消息”。第一次真实慢响应暴露，已修活动优先级；新增组件回归及第二次真实探针均通过。
3. 第一探针等待60秒撞上模型HTTP超时，出现失败后自动重试；是一条输入、两次模型协议请求。第二探针上限改30秒并主动约4秒释放，一条输入一次模型请求。**累计2条业务输入、3次业务模型请求；另2次qualification单列**。旧失败和重试事件原样保留，不当作第二次仍在失败，也不把固定回复当作自主工程/推理能力。
4. 浏览器驱动有三个非产品错误：带datalist的凭据名称实际是combobox；stale请求日志对象把method字符串当函数；stage完成后等待了“未知结果区”的文案。业务动作已发生时先查网络及实际UI，不重放stage；原日志保留。最终成功与收据由独立读取和后续正确定位确认，不能将失败脚本写成全部通过。
5. 窄屏遮罩中心位于侧栏内，默认中心点击被侧栏列表拦截。没有提高遮罩z-index或forceclick：实测侧栏宽299/视口390，正常点击露出遮罩(344.5,439.5)关闭成功。`browser-inspector-before-sidebar-toggle.log`保留原驱动错误，`browser-sidebar-session.log`保留实际边界与成功点击；不是确认的层叠缺陷。

## 6. 最终包、审计与边界

[最新 Windows 本地预览](../output/playwright/ux-git-state-inspector-status/delivery-verified/TraverseBoard-UX-Git-State-Inspector.exe)：版本 `v0.1.0-ux-git-state-inspector`，104961536字节，SHA256 `63ed8736964856956398c7e014a7589de6a670f5180b23e7a601180b46eab058`。

构建退出0、20项静态检查通过；1736个源码/构建输入未变、116个前端资产逐字内嵌。root另行核对当前输入、资产、二进制SHA、amd64/GUI属性，结果见 `root-final-artifact-check.json`。最终HTTP资源与dist逐字一致，见 `acceptance-host/ready-final-assets.json`。此前 `delivery-final` 候选缺末轮两修，原包保留并有SUPERSEDED说明；旧Draft-Protection及更早包也未覆盖。

独立终局审计：`acceptance-host/final-business-verification.json`通过，两Git原key各1操作/精确批准且HEAD不变；两仓库索引原字节、分支及porcelain恢复种子，除root明确追加的chosen外7文件未变。`final-after-readonly-ui-audit.json`核最后只读页面前后16张业务表全行hash不变、无业务增量；所有活动lease、pending工具、prepared输入、Job/终端、未完成工作区事务/Host intent为0，FK0。详见[独立审计](../output/playwright/ux-git-state-inspector-status/acceptance-host/final-audit.md)。

浏览器 `uxgitstatus` 已关闭；04:15核精确归属后只停止本批API57816/provider60552，18896/18897无监听，home/profile/日志保留，旧服务和用户窗口未动。最终EXE没有启动；manifest/DPI静态检查不等于WebView2原生、高DPI或签名安装验收。浏览器与本地协议夹具不代表真实模型长任务能力，也不代表真实GitHub账号push/PR/外部CI。本批不修改hooks/签名限制，不扩入Plan、全文搜索或跨设备同步。
