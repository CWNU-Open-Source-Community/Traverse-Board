# 工具栏模型可读性与分行补齐

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-12。**本批模型可读性、容器分行和字重补齐已完成，17 项回归、最终构建与浏览器复验通过，Toolbar-V2 已产出，现场已收尾。**

工作目录为 `<workspace>`；证据保存在 [ux-composer-toolbar-followup](../output/playwright/ux-composer-toolbar-followup/)。沿用原授权与已有 dirty，不 commit、push 或 release，不修改旧验收数据。

## 上一版结论哪里不足

用户再次指出“模型简称、菜单完整名称、窄窗分行、间距和字重”没有做到位。复核确认上一版完成声明过满：

- 模型只取 ID 的 basename，并在 145 px 后省略；一级菜单中的供应商和完整 ID 也会被 128 px 限宽省略。完整内容存在于 title 不能代替直接可读。
- 分行仅按 560 px 视口判断，没有反映侧栏挤压后的实际输入区宽度。900 px 窗口打开侧栏时也可能需要分行。
- 通用按钮 font 继承覆盖了权限按钮的局部声明，实际权限为 14 px / 400，计划按钮为 13 px / 500；不能从声明的 CSS 就认定呈现一致。
- 上轮浏览器摘要记录过 32 场景和 POST 0，但尺寸输出被 helper 截断，磁盘没有完整保留该轮逐项 computed 记录。也没有足够证据解释深色主题看起来更粗的具体原因。

上一版已完成的＋菜单、独立计划开关、权限入口和真实模式账本仍成立，本次没有重做或改写这些业务验收。

## 本次修法

模型控件采用保守格式化：`deepseek-v4-flash` 显示为 `DeepSeek V4 Flash`，`claude-sonnet-4-5-20250929` 显示为 `Claude Sonnet 4.5`，`gpt-5.6-terra` 显示为 `GPT-5.6 Terra`。只识别有限品牌/版本格式；Thinking、Coder 和其它尾缀保留，未知自定义模型保持原 basename。只有 Claude 版本后处于已知快照位置的合法日历日期才可从简称省略，完整 ID 始终保留。

一级模型菜单新增 `dl.v2-model-route-current`，按“供应商”“模型 ID”独立显示名称、不同的原 provider ID 和完整 raw model。选择模型入口保持可点击。实际路由请求、完整可访问名称/title、可选资格、下一轮生效与异步焦点守卫没有改变；没有新增 API/schema 或更换模型。

根代理在 [styles.css](../web/src/v2/styles.css) 中补齐实际布局：

- 以 `@container composer (max-width: 600px)` 判断输入区，按用途将工具与模型/发送分行，组间间距 8 px。
- 对同类文字控件明确 `font: 500 13px/20px`、基准最小高度 34 px；长名称允许换行增高，不再强制压回固定单行。
- 移除模型触发器 145 px 省略方式，完整身份区和一级菜单模型名称按可用空间换行，弹层受输入容器宽度和可视高度约束。

以上不改全局字体家族，不通过减字号隐藏长名称，也不增加推理档位或业务动作。

## 测试与中间构建

| 检查 | 实际结果 | 证据 |
| --- | --- | --- |
| 模型控件定向回归 | 单文件 17 项通过，exit 0。覆盖友好名、完整身份可见、合法/非法日期、Thinking/Coder、自定义回退、原 provider/model payload、资格、重置、错误与焦点 | [model-tests-final.log](../output/playwright/ux-composer-toolbar-followup/model-tests-final.log) |
| 类型检查 | `tsc -b --pretty false` exit 0 | [model-typecheck.log](../output/playwright/ux-composer-toolbar-followup/model-typecheck.log)、[退出码](../output/playwright/ux-composer-toolbar-followup/model-typecheck.exit) |
| 旧呈现红测 | 11 失败、6 通过，保留原记录；不把更新显示期望造成的失败冒称权限或执行故障 | [model-tests-red.log](../output/playwright/ux-composer-toolbar-followup/model-tests-red.log) |
| build 2 | 成功；JS `index-CtAbXPXV.js`、CSS `index-D_uLbjM2.css`。保留 chunk 超过 500 kB 警告 | [web-build-2.log](../output/playwright/ux-composer-toolbar-followup/web-build-2.log)、[HTTP 资产核对](../output/playwright/ux-composer-toolbar-followup/api/assets-candidate1.json) |

17 项只报告本次模型组件集合，不与上轮 84、31、25 等重叠结果累计。更详细的模型边界见 [model-validation.md](../output/playwright/ux-composer-toolbar-followup/model-validation.md)。

## 已保存的真实浏览器显示证据

[browser-readability-full.log](../output/playwright/ux-composer-toolbar-followup/browser-readability-full.log) 保存完整的 18 个场景结果，包含 DeepSeek、Claude、GPT 和未知自定义长 ID，浅色/深色主题，以及 900 px 侧栏开/关、1280 px 和 320 px 的相关组合。每条记录保留输入区、工具组、动作组、计划/权限/模型控件的矩形、client/scroll 宽度、computed 字体、草稿、完整菜单 ID 与关闭后焦点。

该轮使用 **GET model-route 响应展示夹具**向实际页面提供不同模型元信息；记录的 POST 数为 0。它验证真实浏览器渲染，不是模型路由切换、Provider 可用性或模型执行验证，没有把这些夹具写进后端。根代理没有点击选择新路由，也没有启动 Provider。

已核的实际结果：

- 同为 900 px 窗口，侧栏打开时输入区为 560 px，工具 y=756、动作 y=798，工具高 34 px，两行净距 8 px；侧栏关闭后输入区为 738 px，两组 y=793 同排。浅深主题均记录了该结果。
- 18 场景中，计划、权限、模型文字控件的 computed font-size 均为 13 px、font-weight 均为 500。这里证明的是这些控件的实际样式值一致，不等同于所有字体、系统和显示器上的主观粗细完全相同。
- 未知长 ID `organization/custom-reasoning-coder-enterprise-build-20260912-q8` 的 basename 和完整 ID 均可读，关键变体没有被省略。320 px 深色例中输入区 client/scroll 均为 300 px，模型文字可分三行；一级菜单 client/scroll 均为 274 px，完整 code 文本换行显示。
- 日志保留每场景完整草稿与菜单关闭后的焦点结果；没有仅以 document 无横向滚动代替内部宽度核对。

代表证据：[900 px 侧栏开启](../output/playwright/ux-composer-toolbar-followup/display-fixture-deepseek-light-900-sidebar.png)、[同窗侧栏关闭](../output/playwright/ux-composer-toolbar-followup/display-fixture-deepseek-light-900-full.png)、[320 px 自定义完整模型菜单](../output/playwright/ux-composer-toolbar-followup/display-fixture-custom-dark-320-full-menu.png)。这些截图均为 build 2 中间版本，不替代最后候选复验。

## 最终验收与交付

一级菜单“模型”二字在窄屏折开的末修已完成：固定标签列宽，型号占剩余空间换行。[最终 build 3](../output/playwright/ux-composer-toolbar-followup/web-build-final-3.log) 执行 `tsc -b && vite build`，exit 0；JS `index-D8k6z1Jj.js`、CSS `index-BEMq2-co.css`。实际 HTTP 静态资源逐字节匹配 dist，见 [assets-candidate2.json](../output/playwright/ux-composer-toolbar-followup/api/assets-candidate2.json)。chunk 大小警告仍保留，不称本批完成全仓性能优化。

最终候选重新检查 18 个展示场景，所有上述名称、完整 ID、字体、容器分组、内部宽度、回焦和草稿检查通过，POST 0。[完整可解析 JSON](../output/playwright/ux-composer-toolbar-followup/browser-readability-final.json)包含所有 18 行，非截断终端输出；[独立汇总](../output/playwright/ux-composer-toolbar-followup/browser-readability-final-summary.json)为 passed=true、issues=[]。输入正文在浅深色都为 14px/400，同类工具控件都为13px/500，不声称仅凭数值消除了所有屏幕上的主观视觉差异。

最终截图：[900 px 开侧栏两行](../output/playwright/ux-composer-toolbar-followup/final-display-fixture-deepseek-dark-900-sidebar.png)、[900 px 关侧栏一行](../output/playwright/ux-composer-toolbar-followup/final-display-fixture-deepseek-dark-900-full.png)、[320 px 完整长名称](../output/playwright/ux-composer-toolbar-followup/final-display-fixture-custom-dark-320-full-menu.png)。这些是明确的 GET 显示夹具，不表示实际切换或调用了这几家模型。

随后移除全部响应覆写、重载并连接真实 API，模型恢复原 `composer-input-model`。900/320px 的添加与权限菜单、新对话网络范围均在视口内；新对话计划、网络、项目选择、模型文字同为13px/500；Inspector往返原草稿保持，POST 0。见 [真实接口恢复记录](../output/playwright/ux-composer-toolbar-followup/browser-restore-results.log)。

本地试用包：[TraverseBoard-UX-Composer-Toolbar-V2.exe](../output/playwright/ux-composer-toolbar-followup/delivery-final/TraverseBoard-UX-Composer-Toolbar-V2.exe)，105370624 字节，SHA-256 `7413329f5192350157401a426cd2e77a240d126f7da5d77801f538bdcb2de0a4`。20 项静态检查通过、1786 源码输入构建前后不变、116 资产精确嵌入；Windows GUI、PerMonitorV2、asInvoker、图标与生产构建标记均核对。见 [静态审计](../output/playwright/ux-composer-toolbar-followup/delivery-final/native-artifact-validation.json)及[根代理独立 SHA](../output/playwright/ux-composer-toolbar-followup/delivery-final/root-artifact-check.json)。**最终 EXE 未启动；V2 包包含本批补修，旧 Toolbar.exe 不包含。**

浏览器已正常关闭，见 [browser-close.log](../output/playwright/ux-composer-toolbar-followup/browser-close.log)。15:15 +08 按路径、SHA、启动时间与端口核对归属后，只停本批 API84968；18900/18901/18902 均无监听，Provider 本批从未启动，fixture构建残留0。停前/停后及相对本批基线46/46表全同，2仓库/4文件、原7输入/8业务wire/唯一Job/旧模式2次及原Run paused-deliver rev3全部保持，live9项0/FK0。见 [最终审计](../output/playwright/ux-composer-toolbar-followup/final-audit.md)与[收尾结果](../output/playwright/ux-composer-toolbar-followup/api/cleanup-result.json)。数据、profile、缓存、失败证据与旧包都保留，没有提交、推送或发布。

原生 WebView2、物理剪贴板、系统菜单、IME、高 DPI、任意真实 Provider 和长任务质量未在本批重新验证。已有数据、失败记录、profile 和缓存保留；临时/构建缓存继续使用 D 盘。
