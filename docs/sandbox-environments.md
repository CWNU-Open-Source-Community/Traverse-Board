# 执行环境选择

普通桌面启动默认选择 **Local**。新任务先进入预览，点击「开始编码」后，
才按所选环境准备独立工作目录和执行权限。环境选择与 Ask / Auto / Full
审批档位分别保存。

| 选项 | 执行位置 | 当前接入状态 |
| --- | --- | --- |
| Local | 本机操作系统沙箱 | 使用现有 Local 后端；当前 Windows 有实现，其他平台由就绪检测提示可用环境 |
| Docker Engine | 用户本机 Engine 的受限容器 | 已接入现有 Standard Code Docker 执行链，要求本地固定镜像 |
| Docker Sandboxes（sbx） | 官方 sbx 管理的本地微虚拟机 | 已接设置、独立后端、命令生命周期和恢复；生产执行因 MCP 隔离证据缺失而保持不可用 |

选择不可用的环境后，界面保留选择并给出检测结果。旧客户端的 `auto`
继续指向 Local。当前任务沿用自己的环境，修改默认值影响后续准备的任务。

## 在应用中配置

1. 打开侧栏「执行环境」。
2. 选择默认环境，按需启用 Docker Engine 或 Docker Sandboxes。
3. 填写固定镜像，保存并核对「已保存」和「当前进程」的差异。
4. 点击重启，在本机确认窗口确认。应用停止当前工作并清理拥有的执行资源，
   再加载保存的设置。
5. 回到任务选择环境，确认工作区来源，再开始编码。

配置保存在应用目录的 `sandbox-environment.json`，带修订号和原请求重放。
保存结果不明确时，页面保留原请求供重试。修改设置不会切换正在执行的任务，
重启仍需重新建立运行时权限。显式命令行启动继续使用原有启动参数。

## Docker Engine 准备

由用户安装并启动 Docker；Windows 使用 Docker Desktop 的 Linux Engine。
设置中的摘要是本地镜像 ID，格式为 `sha256:<64 位小写十六进制>`。
镜像需要包含项目的 Standard Code runner，构建方式见
[Standard Code Docker](standard-code-docker.md)。普通桌面启动从设置页读取该值；
独立命令行和显式启动模式继续支持原有环境变量。

应用检测固定本地 Engine 与镜像，运行时使用现有网络关闭、凭据隔离、
Drydock 和审批机制。Docker 安装、登录、镜像构建与下载由用户管理。

## Docker Sandboxes 接入边界

官方 `sbx` 是独立微虚拟机工具，安装要求见
[Docker Sandboxes 安装文档](https://docs.docker.com/ai/sandboxes/install/)。
本适配器统一使用 `sbx --app-name traverse-runtime`（16 个字符，符合 sbx 的 20 字符上限）。
Docker 的[第一方测试脚本](https://github.com/docker/sbx-kits-contrib/blob/main/scripts/test-kit-e2e.sh)
说明 `--app-name` 隔离 daemon、sandbox、策略、缓存和凭据存储。
因此该环境需要单独登录和准备模板，默认 sbx 环境中的登录及缓存不作为本适配器的就绪依据。
模板格式为 `仓库名@sha256:<64 位小写十六进制>`；创建使用 `--pull never`。

当前生产探测返回 `mcp_isolation_unverified`。官方文档说明，每个 sandbox
会启动 MCP 网关；动态模式可加载宿主机注册的服务，本地 stdio 服务在宿主机执行。
官方静态模式支持固定服务集合。Windows 实机已验证自有零工具服务的静态集合：
集合外服务的发现、直接调用、`mcp-exec` 和 `code-mode` 请求均被拒绝；
另一个显式允许测试服务的沙箱成功调用了同一服务。生产创建路径仍需接入
经过身份校验的服务注册与固定参数，实机探针通过本身不会开启生产执行。
参见 [MCP gateway](https://docs.docker.com/ai/sandboxes/mcp-gateway/)。
清空进程环境、设置网络拒绝规则、读取 SSH 转发配置，都不足以独立证明
`credentials=none`。SSH 与 MCP 的配置还涉及 daemon 缓存，需验证实际生效状态，
参见 [sbx settings](https://docs.docker.com/ai/sandboxes/configuration/settings/)。

后续启用生产执行需要补齐：

- 将固定零工具 MCP 服务接入正式创建流程。实机重启后的静态集合复验已通过。
- 将实测执行链纳入产品验收。当前已通过命令退出 0/7、取消 CLI 后停止和删除
  沙箱、底层资源清理核对，以及工作目录写入、`.git` 的
  四种写入拒绝、外部 junction 读写拒绝、HTTP 策略拒绝、原始 TCP 数据阻断
  和 SSH 转发套接字不可用检查；这些结果适用于本次固定模板和本机配置。
- 接入本地不可变身份选择或等价条件式清理。已安装的 0.47 CLI 对实际 UUID
  执行 `exec`、`stop` 和 `rm` 均返回找不到沙箱；按名称调用前后的 ID 比对
  仍存在替换竞态。
- 继续定位工具启动 daemon 时的恢复故障。此前内部 Docker 接口不可达时，
  `ls --json` 返回空清单，而原有 runtime 元数据仍存在；现已修复清理误判。
  用户随后从 PowerShell 启动，恢复了原有两个身份并完成检查与清理。

现有生命周期代码保留自有日志，按固定命令创建、执行、取消和恢复。
创建结果不明确且不可变 ID 尚未保存时，记录保持待处理；恢复只处理身份可核对的资源。
清理未确认时，Command Runtime 保留 `stopping` 与不确定结果，确认清理后才写入终态。
sbx 的可执行身份摘要表示「固定模板与来宾路径绑定」，界面与输出会标明该含义。
创建前还会扫描工作目录，拒绝多链接文件、符号链接、Windows reparse point
和特殊文件。扫描支持取消并限制目录项数量；检查范围是派发前已有的工作目录。
宿主随后修改目录的行为仍需纳入实际挂载验收。

## 验证范围

设置保存、权限分离、原请求恢复、后端显式选择、桌面固定重启、SQLite v188
升级与旧数据保留均有回归测试。sbx 生命周期测试使用受控 CLI transport，
界面截图使用标明模拟数据的真实 React 组件。开发机现已安装官方 sbx 0.47.0，
CLI、登录、固定模板缓存、两个真实微虚拟机、重启后的检查及自有资源清理
均已有实测记录。生产辅助服务绑定与按不可变身份操作仍待完成。
本机 Docker Desktop 通信目录修复后，Linux Engine 29.6.2
已恢复响应。各阶段结果见[验收记录](acceptance/2026-10-10-sandbox-selection.md)。
