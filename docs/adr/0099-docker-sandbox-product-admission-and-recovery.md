# ADR 0099: Docker Sandbox 产品准入、执行与恢复

Date: 2026-08-14

## Status

Accepted for the exact, network-disabled Docker product profile described
below. Managed egress and general Docker execution remain unavailable.

## 背景 / Context

Schema v97（ADR 0096）已经提供写前生命周期意图、代际租约、精确容器身份、
`start/wait/TERM/KILL/delete` 与崩溃恢复；schema v98（ADR 0098）已经提供只读
输入、有界日志、严格输出暂存与原子输出提交。但这两个内部合同故意不授予产品执行
权限，也没有把 CLI、HTTP、Desktop 或模型提案接到真实容器执行。

Issue #40 需要把这些边界组合成一个默认关闭、失败关闭、可审计的产品入口。关键约束
是：SQLite 中的历史事实不能恢复进程权限；模型不能提交 Docker 参数；网络放宽不能
只靠 Manifest 中的一条 allowlist 声明；Docker 不可用时也不能退回宿主执行。

## 决策 / Decision

### 1. 单一 Application 服务与进程内能力

`DockerSandboxService` 是产品准入、状态、启动、取消和启动恢复的唯一业务服务。
CLI 直接调用该服务；HTTP 和 Desktop 只投影同一服务；模型工具
`sandbox_docker_run_propose` 通过受 fencing 约束的 adapter 只调用 `Admit`，不能调用
`Start`、`Cancel`、Docker transport，亦不能提交 daemon endpoint、Docker flag、镜像
覆盖、宿主 bind、环境变量、代理或网络放宽。

真实执行默认关闭，只有当前进程显式持有 Docker execution capability 和与当前
`approval|full_access|debug` 权限档匹配的 runtime capability 时才可能准入。服务每次
构造都会生成新的随机 runtime epoch，并只把其摘要写入准入记录。重启、复制数据库或
修改 SQLite 都不能重建该 epoch，也不能恢复 start authority。

### 2. `sandbox.readiness.v1`

每次准入和启动都对固定本机 Unix socket 或 Docker Desktop Linux-engine NPipe 做新的
只读检查；结果有效期固定为 30 秒，不作为长期缓存。探针检查 feature flag、固定端点、
daemon/API 兼容性、Linux container 模式、PIDs limit、CPU/内存容量与已存在的精确
OCI digest 镜像。它不接受 TCP、`DOCKER_HOST`、调用方 socket、pull 或 Docker CLI。

状态只有 `ready|disabled|unavailable`。公开的稳定 reason/remediation 组合为：

| Reason | Remediation |
|---|---|
| `none` | `none` |
| `feature_disabled` | `enable_docker_sandbox` |
| `invalid_request` | `correct_sandbox_request` |
| `daemon_unreachable` | `start_docker_engine` |
| `api_unsupported` | `upgrade_docker_engine` |
| `platform_unsupported` | `use_linux_containers` |
| `pids_limit_unavailable` | `enable_pids_limit` |
| `resource_capacity_insufficient` | `reduce_resource_limits` |
| `image_unavailable` | `provide_compatible_image` |
| `managed_egress_unavailable` | `use_network_disabled` |

Docker 不可达或 readiness 过期时拒绝准入/启动；没有 LocalRunner 或宿主进程 fallback。

### 3. Schema v99 产品账本与重新校验

Schema v99 新增不可变 admission、独立 Start WAL、launch、append-only cancellation 与
terminal receipt，并补齐 v98 output-staging entry 的持久化和每 attempt 唯一性。迁移不
回填准入，不修改 v97/v98 的 false-authority 历史事实。Admission、Start 与 Cancellation
分别使用独立的 operation domain 和 `Idempotency-Key`：同 endpoint、同 key、同 intent
精确重放；同一 admission 已写入一个 Start WAL 后，第二个不同 Start key 必须冲突，不能
重新绑定或制造第二次 Docker effect。Start WAL 在任何 lifecycle/daemon 写入前与
metadata-only Run event 一起提交，并绑定本进程 runtime epoch；它仍不是可跨重启恢复的
start authority。

准入必须重新读取并精确绑定当前 Run、Mission、Workspace、v54 plan、Manifest、镜像、
当前 `docker` execution profile、最新权限快照、原始 `sandbox.manifest` 的已批准
`per_call` approval、当前 Policy、剩余 Run wall-clock 和 Tool-call budget，以及未过期的
readiness 和本进程 runtime epoch。`full_access` 或 `debug` 不能替代这份精确 per-call
approval，持久权限快照本身也不是 bearer capability。

Admission 固定以下预算，并由 Go、Docker 配置、I/O 合同和 SQLite 交叉约束：

| 预算 | 产品边界 |
|---|---|
| CPU | `1..8000` CPU quota milliseconds，且不超过 daemon CPU 容量 |
| Memory | `16 MiB..8 GiB`，且不超过 daemon 可用容量 |
| PIDs | `1..512`，daemon 必须支持 PIDs limit |
| Disk/output | `1 byte..16 MiB`；最多 64 个文件、单文件最多 4 MiB |
| Wall clock | `1..3600s`，并收窄到 Run 剩余 execution budget |
| Logs | 每个 stdout/stderr 流最多 256 KiB、4096 行，并受 deadline 约束 |
| Tool calls | 准入时至少剩余 1 次，最多冻结 100 次；模型提案先由 Gateway 原子计费 |

### 4. 网络永久默认关闭；allowlist 当前拒绝

本产品切片只接受 environment-free、secret-free、`network.mode=disabled`、零 target 的
Docker Manifest。Create 明确设置 Docker `NetworkMode=none`；端口暴露/绑定、DNS、
DNS search/options、extra hosts、links、endpoint 配置、代理环境与网络别名都必须为空，
inspect 后再次精确复核网络 namespace 没有 IPv4/IPv6、gateway、MAC、alias 或 DNS name。

现有 Manifest 可以表达 `allowlist`，v54 也能编译目标，但这不等于网络 enforcement。
按 host/port/protocol 精确放行仍需要独立的 Go-owned egress guard、生命周期和恢复证据。
在该 guard 实现前，任何 allowlist 或 `ManagedEgressEnabled=true` 都以
`managed_egress_unavailable/use_network_disabled` 失败关闭；不得宣称 scoped egress
已经实现。

### 5. 退出后 I/O、Artifact 与清理顺序

容器到达精确 `exited` checkpoint 后，服务在当前租约和完整身份 fence 下先采集有界
stdout/stderr。只有 natural exit `0` 且 Run/Profile/权限/Policy/计划与 artifact authority
仍完全匹配时，才从唯一可写 output mount 暂存、复读、重哈希并原子提交输出。随后才
进入精确容器清理。

timeout、取消、非零退出、I/O 失败或 authority 改变均不得提交输出 Artifact；日志仍按
有界、不可信证据处理。无论结果如何，清理都不依赖新的 start capability。产品终态为
`succeeded|timed_out|cancelled|failed`，receipt 必须证明 `cleanup_complete=true`；原始
日志、容器 ID、宿主路径、命令、operation key、lease/owner 与 daemon payload 不进入
公共投影。

### 6. 取消、恢复与可观测性

取消先写入 sticky、不可变 cancellation，再取消活动 context 或接管已释放/过期的精确
生命周期租约。重放不会重复事件或 Docker effect。进程启动恢复只处理已经绑定 launch
的记录：它可以检查、终止和清理精确 owned resource，但不能用旧 runtime epoch 启动
一个尚未启动的容器。只有 admission、尚未形成 launch 的记录保持审计事实，不会在重启
后自动变成执行。

Run Activity 仅从白名单事件投影 `preparing|starting|running|cancelling|cleaning|cleaned`
及有界终态、reason 与计数。CLI、HTTP、Desktop 和模型提案共享上述状态机；任何适配器
都不能另建权限判断或 Docker 请求。

## 后果 / Consequences

- 精确的 network-none Docker Sandbox 可以在显式进程 capability、当前 Policy、精确审批、
  权限和预算都满足时执行，并具有有界 I/O、取消与崩溃恢复。
- 数据库可恢复工作和清理，但不能恢复或伪造 start authority；无 Docker 时失败关闭。
- 模型只能提出结构化、已编译计划的准入请求，不能自行启动容器。
- Managed egress、任意 Docker 参数、stdin/TTY、image pull/build、daemon-wide adoption、
  一般 LocalRunner 与未知容器接管仍不属于本决策。
