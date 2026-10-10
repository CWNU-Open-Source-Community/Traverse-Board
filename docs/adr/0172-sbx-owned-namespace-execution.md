# ADR 0172: sbx 本地执行的命名空间所有权与条件删除

Date: 2026-10-10

## Status

Accepted for the Windows sbx 0.47.0 / daemon API 0.38.0 adapter. The real
conditional-deletion positive and negative controls passed, including a rerun
through the direct-read production path. Full application lifecycle acceptance
also passed for the tested pinned template and local configuration; its scope
and evidence are recorded below.

## Context

本地 Docker Sandboxes 0.47 的 `exec`、`stop` 和 `rm` CLI 不能按实测 VM UUID
选择资源。此前把“每个操作都必须接受不可变 UUID”作为生产准入条件，使正式创建、
MCP 隔离和微虚拟机执行链即使完成也无法开放。

Docker 的[官方 exec 文档](https://docs.docker.com/reference/cli/sbx/exec/)
只为 cloud 明确提供 ID 或名称选择；Docker 的
[第一方 docker-agent 适配器](https://github.com/docker/docker-agent/blob/main/pkg/sandbox/sandbox.go)
在本地使用名称创建与执行。官方 `run --rm` 描述退出后的自动删除，但文档未给出
不可变身份条件，因此不能仅凭这个 flag 推断其在取消或崩溃后满足本产品的清理合同。

安装的 Docker 签名 `sbx.exe` 0.47.0 中保留了本地 API 的 Go 运行时类型信息。
`sandboxapi.DeleteSandboxParams` 包含 `Force`、`ExpectedId`、
`DeleteScopedSecrets`，对应查询参数 `force`、`expected_id`、
`delete_scoped_secrets`。二进制中还可核对
`DeleteSandboxIfID`、`DeleteRuntimeIfID` 和
`deleteRuntimeWithLifecycleGateHeldIfID`。`ExecRequest` 只包含命令、环境、
终端大小、TTY、用户与工作目录，没有 expected-ID 字段。

这项 API 是所安装官方发行版的实现合同，尚不是 Docker 公开承诺稳定的 SDK。
其版本兼容性由实际 daemon 的 `/daemon/health` 验证，并由本地正反向 VM 验收
补充；二进制符号或字段本身不能证明服务端正确处理同名替换。

## Decision

### Trust boundary

宿主 OS 账户、该账户运行的产品安装和有权管理本地 sbx daemon 的操作者是可信
控制面。来宾命令、项目文件和模型输出是不可信输入。[官方隔离说明](https://docs.docker.com/ai/sandboxes/security/isolation/)
将来宾进程与宿主资源、宿主 Docker daemon 隔离；产品同时固定 deny-all 网络、
关闭 SSH 转发与共享技能，并只暴露验证过的固定零工具 MCP 服务集合。

这里不承诺抵御宿主管理员故意在检查与 `exec` 之间删除 VM、重新使用其名称，或
修改 daemon、策略、凭据、产品可执行文件和日志。这样一个操作者本来就有权控制
宿主执行边界。该限制必须保留在文档中，不能把名称检查写成针对宿主管理员的
原子 ID 选择保证。

### Exclusive product namespace

正式执行只使用固定 `--app-name traverse-runtime`。所有合作的产品写入者在首次
准备、执行或有待清理记录的恢复前获取整个 namespace 的 OS 锁；锁独立于 JournalRoot、安装目录
与用户可设置的路径环境。锁保持到 Close 完成取消和恢复，不能在 CLI 取消后提前
释放。只读 readiness 检查不取得写入控制权。恢复先在 backend/journal 锁内严格
解析日志；空日志、已确认删除和 unused 记录不会取得 namespace，无效日志继续报错。

Windows 使用 `Global` 命名 mutex，其键绑定实际进程 token 的用户 SID 与固定
app-name，并用显式受保护的 SID/SYSTEM DACL。专用、固定 OS 线程的 goroutine
执行 acquire 与 release，避免 Go goroutine 换线程使 mutex 无法释放。Unix 原语
通过 OS 用户数据库选择规范化、属主匹配的 home，在私有目录中锁住不删除的 inode。
当前接受的 daemon API 端点仅在 Windows 建立；Unix API 不猜测 socket 路径。

每次 operation 使用新的 128-bit 加密随机名称；名称不重用。现有 journal 仍在创建前
写入意图，成功取得 UUID 与精确挂载身份后才允许执行。每次执行前重新核对名称、
UUID 和挂载；观察到同名替换或挂载变化时拒绝派发，不接管替代资源。

### Fixed zero-tool MCP binding

CLI 和桌面程序在正常应用入口的最早阶段识别专用 helper 参数，只支持精确的
单参数形式。该执行分支在应用读取数据库、凭据或启动界面前进入固定 stdio 服务，
服务的工具、资源、资源模板和提示列表全部为空，调用和读取请求返回错误。

启用后的原生启动通过 `Prepare` 对实际应用可执行文件进行握手验证，再按其
绝对路径和 SHA-256 派生注册名称。注册绑定固定参数和工作目录，不覆盖旧版本
的名称。已有登记的 `Prepare` 只需 helper 握手与完整注册核对；登记缺失时才调用
`mcp add`。`Readiness` 与创建前检查复核可执行摘要和完整注册 metadata，不再调用
`mcp ls` 或 `mcp inspect`。Windows metadata 路径来自实际进程 token 的账户 profile；完整记录
核对 command、args、Cwd，并要求 Env、EnvOverride、SecretEnv、header、OAuth 等
额外启动或凭据设置为空。文件读取还检查大小、重复字段、reparse point 和硬链接。

正式 VM 创建使用 `--static-mcp <verified-helper-name>`、`--skills off` 和 deny-all
网络。来宾在 payload 前检查实际 SSH 套接字和 `SBX_CRED_*_MODE` 元数据，要求
受支持的凭据模式全部为 `none`，随后以 `env -i` 设置显式命令环境。原先固定返回
`mcp_isolation_unverified` 的生产门禁已由这些正式绑定检查取代。

### Fixed CLI environment

宿主 CLI 调用设置 `SBX_NO_TELEMETRY=1`，使用[官方 usage analytics opt-out](https://docs.docker.com/ai/sandboxes/faq/#does-the-cli-collect-telemetry)。
同一固定环境设置 `DOCKER_CI=true`；在接受的 v0.47 实现中，它跳过自动更新检查和
交互式诊断同意流程，不构成诊断上传授权。它是当前版本的适配合同，不作为其他
版本的通用保证。调用方环境中的默认模板覆盖变量仍被过滤，创建始终显式传入
产品配置的 pinned template。

### Fresh local observations and immutable version proof

Readiness 通过固定 Windows named pipe 读取 `/daemon/health`、`/sandbox` 和
`/daemon/settings/<compiled-key>`，只允许 SSH 转发和本地 MCP gateway 两个设置键。
每次观察重新读取可变 daemon 状态，核对 JSON 唯一字段、条目身份，以及设置键、
布尔类型和错误状态；清单保留挂载顺序和只读属性，供执行与清理的所有权检查使用。
清单和隔离设置不缓存。

CLI 版本只在成功确认 `sbx 0.47.0` 后缓存，每次使用仍重新核对可执行文件 SHA-256。
同一 backend 的版本探测串行合并；短 CLI 调用在不同 backend 实例之间共享可取消
的 gate。长时间的 create/exec 不占此 gate，清理直接使用条件删除 API。该实现减少
版本、清单、设置与 MCP 身份检查反复启动 CLI 造成的初始化等待。

### Conditional whole-VM deletion

取消和恢复不再以名称发送 `stop` 与 `rm`。固定本地 daemon 客户端只发送：

```text
DELETE /sandbox/<owned-random-name>?force=true&expected_id=<recorded-UUID>&delete_scoped_secrets=false
```

`expected_id` 必须为非空、规范 UUID，名称必须符合自有随机名称格式。清理不接受
调用方提供的地址、HTTP 参数、认证信息、策略或其他 sbx namespace。

Windows 客户端固定连接
`\\.\pipe\docker_kaname_sandboxes-traverse-runtime_sandboxd`。连接只授予
Identification 级别，让 daemon 核对用户身份而不能代入客户端权限进行宿主操作。
客户端没有网络 proxy 或 redirect，响应、时间和错误输出均有界；不向产品投影原始
daemon payload。删除前检查实际 daemon 为发行版 `v0.47.0`、API `0.38.0`、
healthy；其他版本保持不可用直到重新验证兼容性。

409/412 身份条件失败保留 ownership 不确定结果；404、服务不可达、响应丢失或
后端清单暂时为空都不能视作删除收据。只有明确成功的条件删除和删除后 UUID、名称
均不在成功清单中，才写入 journal removed 并将 Command Runtime 置为终态。
创建回复不明确、UUID 尚未持久化时仍不自动接管资源，日志保留待处理状态。

## Consequences

原先“所有 CLI 操作必须接受 UUID”的绝对要求由明确的 namespace 所有权模型、
执行前身份核对，以及服务端按 UUID 条件删除取代。合作的产品进程无法绕开 namespace
锁制造替换竞态；条件删除另外约束清理阶段的同名替换。名称执行仍依赖可信宿主管理
边界，不能宣称对任意宿主并发修改提供原子 ID 保护。

本 ADR 不增加公开 bypass 或用户切换安全边界的选项。模型仍只能提交已编译的产品
请求，审批、MCP 身份、模板、挂载、网络、凭据和取消/恢复合同继续由统一后端验证。

SQLite v189 仅在现有 Command Runtime Job 插入约束中补充
`docker_sandboxes` backend 与 `sbx` profile 的映射。它保留现有 Run、mode、permission
和 lease 谓词，不改写历史 Job、用户选择或已保存授权。

验收至少包括跨进程且不同 journal、环境路径的锁排斥；任意 goroutine 关闭与进程
退出后再获取锁；版本不兼容拒绝删除；错误 UUID 对真实自有 VM 的拒绝；正确 UUID
删除正在运行、含 detached 子进程的 VM；成功删除后底层资源、命令后写入与日志终态
核对。受控 transport 测试、跨平台编译和实际 VM 结果分别记录，不能互相替代。

## Validation

最新真实自有 VM 条件删除复跑通过（13.62 秒），使用直接 API 读取的正式路径：错误 UUID 的删除被拒绝
且原 VM 保留；正确 UUID 删除成功；同名重建产生不同 UUID，旧 UUID 的删除被拒绝
且新 VM 保留；正确新 UUID 删除成功，随后恢复没有新增动作。来宾 `none` 模式成功派发、
模拟 `apikey` 模式返回 125 且 stdout 为空也已实测验证。
本地证据位于
`build/sandbox-backend-selection/sbx-product-live/conditional-removal-1055017084/result.json`，
对应运行日志为 `build/sandbox-backend-selection/sbx-product-live/conditional-removal-direct-api-attempt2.log`。

正式 helper 的 `Prepare`、完整 metadata/hash 身份绑定与就绪检测已实测通过。
空、unused、removed 日志在另一个 namespace owner 存在时的恢复与 Close，畸形
日志报错，以及 pending 记录必须取得 namespace 的定向 race 测试通过。
正式应用执行链的第六次验收通过，总计 115.11 秒：

| 用例 | 耗时 | 持久化结果 |
| --- | --- | --- |
| `output-success` | 40.21 秒 | `completed`，退出码 0 |
| `output-exit-seven` | 30.63 秒 | `failed`，退出码 7 |
| `cancel-detached-child` | 44.27 秒 | `cancelled`，记录 CLI 实际退出码 1 |

每个用例只派发一次 backend，stdout 与 stderr 各保留 23 bytes 中文输出，清理收据、
journal removed、工作区 checkpoint 和 exact replay 均通过。取消用例在清理后
继续观察 21 秒，没有出现 post-cleanup marker，detached 子进程 heartbeat 已停止。
取消状态由取消操作与清理结果确定，不把本次 CLI 退出码 1 改写为 143。

每个用例的 `acceptance-summary.json` 位于
`build/sandbox-backend-selection/sbx-product-live/sbx-product-587455417/<case>/`；
运行日志为 `build/sandbox-backend-selection/sbx-product-live/application-acceptance-attempt6.log`。
此验收适用于上述固定模板与开发机配置；全量回归测试范围以验收记录为准。

Readiness 的并发 CLI 观察曾出现约 5/10 秒的初始化等待，独立串行观察曾约
0.5 秒；后续串行复跑也出现约 5 秒等待。因此并发和 telemetry 不能单独解释全部
迟延，telemetry opt-out 不等于超时问题已解决。当前实现已通过固定本地 API、完整
MCP metadata 读取和摘要绑定的版本缓存减少 CLI 调用；新版 readiness 已实测为
0.761 秒与 0.262 秒。各阶段完整范围见[验收记录](../acceptance/2026-10-10-sandbox-selection.md)。
