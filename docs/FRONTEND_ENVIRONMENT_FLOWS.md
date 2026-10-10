# Docker 与浏览器证据工作流

本页记录环境工作流的实现与验收入口。执行能力仍由 Go 的启动配置、当前 Run 快照、来源信任、执行租约与每次操作审批决定。

## Docker 编码环境

普通桌面版现在可从侧栏「执行环境」保存 Local、Docker Engine、Docker Sandboxes
偏好，并通过固定重启流程应用。默认选择 Local；官方 sbx 的生产执行门控状态和
准备要求见 [执行环境选择](sandbox-environments.md)。下述 Docker 工作流继续复用同一执行链。

在任务设置展开「准备 Docker 编码环境」，读取固定镜像、Docker Engine 和当前任务的实际状态。界面分别给出启用启动选项、准备镜像、启动 Linux Engine、刷新或重启以装配运行时的下一步。镜像构建与离线工具链要求见 [Standard Code Docker](standard-code-docker.md)。

环境就绪后，在当前任务选择「使用 Docker 开始编码」，核对 Go 返回的工作区来源摘要并确认。该入口复用 Standard Code 原子预设、暂停后的静止检查、来源信任和原请求恢复；自动使用当前 Run 与任务标识。配置完成后回到任务发送要执行的工作。已经配置并进入 Deliver 的任务沿用现有计划与后端；修改环境需使用已有重新配置或新任务流程。

`GET /api/v1/sandbox/docker/environment` 仅需读取凭证，不接受查询参数。服务使用进程配置的镜像摘要与固定本机 daemon，探测最多 5 秒；不拉取镜像、不创建容器。响应仅投影原有就绪协议，保留端点摘要，不输出原始 daemon 信息。就绪检查确认镜像安全形态，实际工具链是否适合本项目仍由编码执行与验证确认。

桌面与独立 `api serve` 将 Local 与 Docker 后端分开装配。请求 Workspace Sandbox、显式启用 Docker、固定镜像有效且 Go 就绪证据有效时，Docker 可以建立 Drydock 与 Standard Code Command Runtime。不可用的 Local 后端不会关闭这条路径，也不会绑定为可执行 Local 适配器。镜像或 daemon 在启动后才准备好时，环境页说明需要重启以装配适配器。

「高级：精确沙箱清单与历史准入」保留原有诊断接口；日常编码流程无需填写 Plan、Manifest 或 Admission 标识。

## 真实浏览器证据

在任务审阅打开 UI 证据。选取当前执行的应用服务后，可读取其关联活动中唯一的启动命令、项目工作目录和本机地址；其他 Run 的服务不会进入选择列表。该记录仅用于填入待审阅启动表单。按照 ADR0120，本次验证会创建独立拥有的应用进程与临时浏览器，不附着正在运行的服务。

填写应用名称、PowerShell 启动命令、项目目录和本次专用本机端口，选择尺寸、主题，添加点击、测试输入、元素存在/消失或截图步骤。预览会展示确切启动请求与清单；勾选核对后才能执行。任何修改都会使已审阅清单失效。完整 JSON 可在高级入口编辑，不是普通流程的前提。

丢失响应时保存原请求与操作键，重新打开面板后可「核对原请求」，只恢复同一次验证。成功后需要重新准备并审阅下一次请求。历史验证显示步骤收据、失败阶段、清理与产物；截图预览调用原有 MIME、长度和内容哈希校验下载接口，并清理对象 URL。

启动与取消仍限支持该能力的 Windows Desktop。连接缺少能力时，界面给出所需启动选项和当前可进行的历史读取操作；macOS 不会获得未实现的 UI Evidence 执行能力。

## 验收入口与范围

- 前端：`docker-sandbox-panel.test.tsx`、`ui-evidence-panel.test.tsx`、`docker-environment.test.ts`，加上现有执行权限和 V2 任务设置回归。
- HTTP/OpenAPI：`TestDockerEnvironment*`、`TestOpenAPI*`；读取认证、方法限制、无查询参数与生成类型同步。
- 桌面装配：`TestDesktopDockerAssemblyWorksIndependentlyOfLocalBackend`、`TestDesktopWorkspaceSandboxAvailabilityKeepsDockerProofAndLocalIndependent`。
- 非 Windows：`TestDesktopUnsupportedLocalBackendDoesNotPreventDockerComposition`、`TestAPIServeDockerRuntimeAssemblesWithUnsupportedLocalBackend`；真实平台 Local 不可用与受控 Docker 探测的组合测试。

以上装配测试使用受控 Docker 只读传输，证明平台组装行为；不等于实际 macOS daemon、Docker 工具链执行或 Windows Edge 浏览器验收。实际平台证据由最终集成验收记录，未执行的场景不得标记为成功。

2026-10-10 Windows 定向验收：39 项前端测试、TypeScript 检查和生产构建通过；Desktop、HTTP、App、Application 的相关 Go 回归、桌面启动参数测试、协议与界面治理检查通过。`internal/desktop` 与 `internal/app` 的 Darwin/arm64 测试可执行文件以 `CGO_ENABLED=0` 交叉编译通过，仅记录编译结果，未在 macOS 执行。日志保存在忽略目录 `build/frontend-completion/environment/`。
