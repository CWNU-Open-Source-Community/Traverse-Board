# 图片输入与应用预览：实施和验收记录

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-11。当前状态：本批图片输入与应用预览限定实现、真实旅程及最终本地桌面包均已完成。用户授权来自本对话“那进行补齐吧”，额度恢复后继续。最终结论、软件路径和未验边界以 [验收记录](UX_IMAGES_PREVIEW_VALIDATION.md) 为准。

## 本批目标

支持用户直接粘贴、选择、拖入截图，查看/移除待发送图片，发送后的历史显示原附件；模型收到图片真实字节。主对话能够打开项目的本机应用、观察页面，并在真实浏览器中点击/输入、核对实际变化。复用现有模块，不新建浏览器或任务执行框架。

## 已确定的接口与行为

- 图片立即保存为项目作用域的不可变附件。PNG/JPEG/WebP，每条最多4张，每张5MiB，单边8192、总16Mi像素；使用成熟解码器校验。名称只展示。
- POST `/workspaces/{id}/image-attachments`，上传使用幂等键；返回ID、项目、SHA256、媒体、大小和尺寸。认证GET读取原图，前端核对响应头/字节数/SHA后才生成预览URL。
- Thread消息的有序`images:[{id,sha256}]`与原消息请求身份绑定。图片数据不存localStorage；恢复记录只存小型元数据。首次创建、未知结果核对、成功清稿都绑定原图；后来添加的图片保留。支持空正文加真实图片，不伪造用户指令。
- OpenAI Chat/Responses、Anthropic、Ollama均发送各自原生图像内容。视觉能力区分supported/unsupported/unknown；兼容服务由用户对精确模型声明，Ollama可用模型元信息。声明支持不代表视觉理解实测。
- 所有图像估计纳入上下文规划，原始图像不能静默裁掉。估算是本地规划值，不是未知供应商token计费保证；远端超限必须准确反馈。
- 预览复用现有command_runtime托管开发服务、FullCDP隔离浏览器。API入口补齐与桌面相同的服务装配；权限与进程归属保持。仅接受明确literal loopback项目地址，不扫描个人浏览器或任意端口。
- 主对话预览可显式打开、刷新、操作DOM已识别控件、关闭。操作绑定当次session和一次性snapshot标识；丢响应后只重新观察，不能自动重发点击/输入。
- 预览PNG是当前页面观察，不冒充持久验收报告；撤权、关闭、过期、hash不符不能继续显示为当前结果。模型截图也须通过现有证据身份读取真实图像。

## 验收要求

1. 真正上传、读取、发送图片；从供应商接收的请求解码并比对原始hash。分别记录协议夹具和真实视觉模型理解，不能混称。
2. 粘贴/选择/拖入、预览/删除、图片only、格式/体积拒绝、中文文件名；不支持/未知能力时保留附件并清楚提示。
3. 刷新和退出重开恢复图片草稿；原发送结果未知时GET核对原key，禁止自动补POST，后续草稿图片不丢；两项目不串图。
4. 由产品托管命令真实启动独立项目服务，等待就绪，再打开浏览器预览，真实点击和输入，核对页面及工具证据；关闭/撤权后旧图和操作失效。
5. 定向前后端回归、类型/构建、最终浏览器加载检查。未完成Windows原生高DPI或真实模型视觉测试时明确列出，不用静态构建替代。

## 当前分工及现场

root：前端输入/持久化/能力/预览集成与最终验收。dependency_path：Go图片存储/HTTP/Thread/纯图/摘要预算。real_journey_verification：四协议/模型能力及新夹具。import_backend：FullCDP/CLI装配/动作/模型截图读取。

仅在`<workspace>`改生产。原树仅同步工作必读，无提交推送合并发布。新验收目录`output/playwright/ux-images-preview`，预留API18884/Provider18885/App18886，旧restart/S/Q实例不动。

## 真实图片旅程（已验证）

独立 Chrome 持久 profile、API 18884、Provider 18885，均无真实供应商凭据。`browser/` 指 `output/playwright/ux-images-preview/browser/`。

- 中文名称1920×1080 PNG经实际文件选择上传、认证读取和原尺寸预览；删除多选项后只留原图。正文的前后空白和换行、原图ID/SHA经过刷新与完整浏览器退出重开保持。`restored-after-reload.log`、`restored-after-reopen.log`。
- 真实浏览器 File/ClipboardEvent/DragEvent 经产品粘贴、拖入处理，上传的原字节SHA一致，随后可移除。`paste-drop-final.log`。这是浏览器事件验收，不冒充Windows物理剪贴板操作。
- 既有Thread的纯图和图文消息实际202，Provider接收的1/2/3张历史图按原顺序匹配原文件；Inspector详情可预览并核对原ID/SHA。`pure-image-submit.log`、`image-api-audit.json`、`inspector-image.log`。
- 实际提交成功后丢弃两次响应：第一次执行，第二次现场同key重试未转发到后端；未确认记录保留。完整退出重开，仅GET原key核对；新草稿和后来添加的小图保留。实际发现并修复观察器漏算images_json导致409，修后复用同一原请求通过，未重新发送。`unknown-until-reopen.log`、`reopen-unknown-fixed.log`。
- 两项目切换不串草稿/图片。另新建“图片对话”直接以空正文提交第一张原图，实际创建与第一条消息均202。`workspace-isolation.log`、`new-pure-image.log`。新增纯图Thread对应第4个图片协议请求，不计入旧Thread的3次。

预览已通过产品command_runtime实际启动预置Node页面，Windows Job、API→PS7→Node父子链、端口和stdout一致。普通wait保持后台Job；初次导航、Chromium临时节点身份和静态权限过滤缺口已经修复。真实输入/点击/input事件、模型截图磁盘与请求原字节匹配、关闭/撤权旧图和动作拒绝、最后托管进程回收全部有证据。

主界面最后入口为index-C7c50Emn.js，包含面板显示与旧缓存状态修复；快速重开135ms无多余capture或旧图。最终包在`delivery-final/`，构建退出0、19项静态检查通过；`final/`与`delivery/`旧包仅是中间证据。Windows原生窗口/高DPI、系统物理剪贴板及真实模型视觉理解未实测，不以浏览器或固定Provider替代。完整证据与使用边界见验收记录。
