<div align="center">
  <img src="assets/branding/traverse-board-mark.png" alt="Traverse Board · 针路簿图标" width="180">
  <h1>Traverse Board · 针路簿</h1>
  <p><strong>本地优先、可恢复、可审计的通用 AI Agent 工作台</strong></p>
  <p>
    <a href="README.md">简体中文</a> |
    <a href="README.en.md">English</a>
  </p>
  <p>
    <a href="https://github.com/Qiyuanqiii/Traverse-Board/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Qiyuanqiii/Traverse-Board/ci.yml?branch=main&style=flat-square"></a>
    <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/Qiyuanqiii/Traverse-Board?style=flat-square"></a>
    <img alt="Go" src="https://img.shields.io/badge/control%20plane-Go-00ADD8?style=flat-square">
    <img alt="Desktop" src="https://img.shields.io/badge/desktop-Windows-0078D4?style=flat-square">
    <img alt="Desktop macOS" src="https://img.shields.io/badge/desktop-macOS-555555?style=flat-square">
  </p>
</div>

> **命名说明：** 产品与界面名称是 **Traverse Board · 针路簿**，GitHub 仓库是 `Qiyuanqiii/Traverse-Board`。`cyberagent` CLI、Go module、`.prayu` 配置、数据目录、环境变量、安装 identity 和现有发布件文件名继续作为兼容标识保留；它们不是第二套产品。Windows 对外主程序从 `v0.1.0-rc.2` 起使用 `TraverseBoard.exe`。完整边界见 [ADR 0124](docs/adr/0124-traverse-board-branding-migration.md) 与 [ADR 0125](docs/adr/0125-traverse-board-windows-executable-name.md)。

## 针路簿是什么

针路簿是一个由 Go 主控的本地 AI Agent 工作台。它把模型路由、长任务恢复、工作区、工具调用、审批、预算、记忆和审计事件统一到一个 Run-centric 运行时中，并通过 CLI、TUI、HTTP API、React 控制台和 Windows/macOS Desktop 提供同一套能力。

用户的长期目标是 `Mission`，一次可恢复的执行尝试是 `Run`。模型可以规划和提出动作，但 Go 始终拥有状态机、凭证、权限、持久化与执行边界。仓库文件、网页、模型文字和工具输出都只是不可信证据，不能自行升级为指令或权限。

当前产品重点是**通用 Code Agent 工作流**。CTF/专项网络安全求解已调整为可选附加能力，暂不进入活跃开发计划；仓库只保留通用的 Skill、Tool、Analyzer、Sandbox、Provider 和 Report 扩展接口，供未来独立插件接入。详见[产品范围](docs/PRODUCT_SCOPE.md)。

## 为什么选择针路簿

通用 Agent 的难点不只是“让模型调用工具”，而是让长任务在失败、重启、审批和多人协作条件下仍然可恢复、可解释、可约束。

### 确定性工程与 Agent 协作

- **Go 硬约束：** Run 状态、预算、Scope、Policy、审批、幂等键、租约和审计记录由代码验证。
- **模型动态决策：** 模型负责理解目标、制定计划、选择已公开的工具和生成面向用户的说明。
- **事实分层：** 模型公开进度与 Harness 验证事实分开显示；模型声称“已完成”不会替代工具结果或验证收据。
- **默认最小权限：** 高权限能力需要独立、显式、可撤销的操作者授权，持久配置本身不携带运行 authority。
- **可恢复执行：** SQLite 是状态真源，Run、Session、事件、检查点和操作收据可以跨进程恢复。

### 单一控制平面

```text
CLI / TUI / React / Windows Desktop / CI
                    |
              Go control plane
       +------------+-------------+
       |            |             |
   LLM Router   Tool Gateway   Run Supervisor
       |            |             |
       +------ Policy / Approval --+
                    |
      SQLite / Workspace / Rust / Sandbox
```

允许的调用方向始终是 `TypeScript -> Go -> LLM/Rust/Docker`。TypeScript 不是安全边界；Rust 只做确定性分析，不管理 Agent、Session 或密钥。

## 核心能力

| 领域 | 当前能力 |
|---|---|
| Agent 运行时 | Mission/Run、可恢复 Supervisor、严格生命周期、检查点、取消、重试、预算和执行租约 |
| 模型与上下文 | Mock、Anthropic-compatible、OpenAI-compatible 与 loopback-only Ollama Provider、模型路由、资格校验、能力探测、流式响应、上下文压缩、层级项目指令、显式 user/project 长期记忆与 Session 恢复树 |
| 计划与协作 | Plan/Delivery、工作项、备注、最多两个核心 child、`batch-delivery.v1` 独立 Worktree/分支/邮箱/交付复核/顺序合并，以及 1/2/4/6 档只读 Fan-out |
| 工具与权限 | Tool Gateway、JSON Schema 校验、Policy、Scope、人工审批、五档 Run 权限上限、受控固定命令、普通模式 Run-owned 命令运行时、逐条审批 PowerShell/Git Bash，以及限时 Debug 终端输入 |
| 代码工作流 | 系统目录选择与 Workspace 导入、工作区浏览、仓库状态、提交历史、Diff 审阅、文件编辑提案、只读 `code-intel-lsp.v1` 语义工具、事务化 Workspace Checkpoint、Run-owned Drydock、稳定 hunk、stash/rebase/cherry-pick/bisect、受管 worktree、GitHub App PR/CI 证据与审批回写、Undo/Redo/Rewind、独立 Fork、验证计划、Code Journey 与 Handoff |
| 可观测性 | 追加式 Run 事件、Live Activity、公开模型进度、Harness 事实、Artifact、Finding/Evidence/Report、SARIF、持久化有界计划任务与脱敏结构化诊断包 |
| 扩展 | 模式感知的惰性 Skill 包、生成候选人工审查、两阶段 MCP Client、签名 `plugin.v1`、受限生命周期 Hooks、Provider/Tool 接口、Go/Rust JSON 协议、内嵌 WASI Analyzer、Sandbox 合同与默认关闭的 network-none Docker 产品执行 |
| 客户端 | `cyberagent` CLI、Bubble Tea TUI、认证 HTTP/OpenAPI、React/Vite、Windows/macOS Desktop 便携预览 |

### 模型可调用的工作区工具

Schema v115 引入 `agent-code-tools.v1`，让 root Supervisor 能在真实 Workspace 中完成多轮“搜索 -> 阅读 -> 修改”闭环，同时不把文件系统权限交给模型。可用性由 Go 按 Run、Mission、Workspace、根目录指纹、Surface、Phase、Role、Profile、权限档及各自 revision 生成；模型只能提交符合 JSON Schema 的参数，不能伪造或扩大这份 authority。

| 模式 | 可用工具 |
|---|---|
| Code / Plan / root | `workspace_list`、`workspace_read`、`workspace_glob`、`workspace_grep`；存在当前 Run 的 GitHub 证据时还可用 `github_review_evidence_list/read` |
| Code / Deliver / root（Code 或 Script Profile） | 上述只读工具，加 `workspace_change`、`workspace_apply`、`workspace_delete` |
| Code / Deliver / root（Review 或 Learn Profile） | 仅上述只读工具 |
| Cyber Surface 或 Specialist | 不公开任何 `agent-code-tools.v1` 工具，并在 capability 快照中说明拒绝原因 |

只读结果稳定排序、分页且有界，并拒绝根目录逃逸、大小写别名、未列入 Go allowlist 的隐藏项（仅 `.github` 作为代码证据开放）、忽略项、链接或重解析点、二进制、非 UTF-8 与超限文件。`workspace_change` 只创建 replace/create/move 提案；`workspace_delete` 是独立、需精确确认的删除提案；`workspace_apply` 只能应用已经批准的精确版本，并重新检查原文件与目标文件哈希，避免审阅后内容漂移。每次调用、结果/拒绝、authority 快照、预算消耗与有界 Artifact 都进入可恢复 Supervisor 账本。`cyberagent run show <run-id>`、Run Detail API 和 Desktop Run 页面可查看当前 generation、逐工具可用性与拒绝原因。该协议不授予 Shell、Git、网络或 Sandbox 权限；完整设计见[使用手册](docs/usage.md)和 [ADR 0116](docs/adr/0116-model-callable-workspace-tools.md)。

### 工作区执行权限合同

Schema v126 在 `conservative` 与 `approval` 之间增加 `workspace_access · 工作区执行`，作为 Standard Code 的权限上限。它允许模型在已注册 Workspace 内读写，并允许一个已通过独立 readiness 的沙箱 adapter 运行有界命令；宿主无沙箱进程、网络、凭证、用户主目录、持久用户/Agent 终端和完整 CDP 全部拒绝。任何越界动作必须走另一条精确、一次性的审批链，持久权限快照本身始终不携带执行 authority。

Windows x64 现在提供显式 `--enable-workspace-sandbox` Local backend；只有真实 AppContainer/WFP/Job/ACL readiness 通过后，CLI、API 与 Desktop 才会打开该进程的 Workspace gate。失败或不支持的平台继续显示不可用，且绝不回退到现有宿主 Command Runtime。共享 sandboxed Command Runtime adapter 仍由 #134 负责，因此 readiness/权限选择本身不会启动进程或向模型发布命令工具。切换权限 revision 会原子释放旧 execution lease，并使绑定旧快照的 Job owner 与工具 authority 失效。完整边界见 [ADR 0127](docs/adr/0127-workspace-access-permission-contract.md) 与 [ADR 0130](docs/adr/0130-windows-local-sandbox-backend.md)。

#130 新增 Go-owned `run_capability_readiness.v1`，把当前已选、现在可切换和后端当前可运行拆成独立事实，并为 Permission、Profile、Interaction、CDP 与 Standard Code 返回稳定阻塞码、修复动作和重启要求。CLI、HTTP、Desktop 与 React 共用该投影；响应不含私有路径且始终 `capability_grant=false`。详见 [ADR 0128](docs/adr/0128-go-owned-run-capability-readiness.md)。

Schema v128 随 #133 将既有固定本地 Docker Engine 的 `network=none` 路径接为 Standard Code 的显式备用后端，并只为既有 Docker admission ledger 增加 `workspace_access` 枚举兼容。上层 `standard-code-command.v1` 不含 backend、镜像、endpoint、mount、网络、环境或 Docker flags；Go 只把当前精确 Drydock 投影到 `/workspace`，固定非 root 用户、只读根文件系统/工具链、资源上限和无凭证环境。daemon/镜像不可用只返回稳定 `blocked_by/remediation`，不拉取镜像且不回退宿主执行。Drydock/Worktree 仍只是所有权与恢复边界，不是安全沙箱；进程与网络隔离来自固定容器。操作见 [Standard Code Docker backend](docs/standard-code-docker.md)，设计见 [ADR 0131](docs/adr/0131-standard-code-docker-network-none-backend.md)。

### Run-owned Drydock 工作目录

Schema v127 的 `drydock-workspace.v1` 从精确的 source Workspace、repository/common-dir、branch、base commit、root 指纹和 dirty/index 状态创建产品管理的独立 worktree。首次 create 只返回 Workspace Trust digest；操作者必须把该 digest 原样带入第二次确认，任何来源漂移都会失败关闭。来源未提交内容只进入 Trust 回执，不会被暗中复制。Trust 永远固定 `grants_process_authority=false`；Drydock 本身不提供进程、网络、凭证或宿主文件系统隔离。

每个 Use/Checkpoint/Rewind/Undo/Fork/Deliver/Cleanup 都重新证明 Run、Workspace、root、Git registration、branch/base/current binding 和 ownership generation。Checkpoint 精确保存 tracked、untracked 与 raw index；崩溃、冲突、用户改动、未知目录或身份不确定时一律保留目录并进入人工恢复。Delivery 只生成审阅 patch，固定禁止 merge/push/force/覆盖来源；Cleanup/到期 GC 只以非 force 方式删除完整证明为产品所有且干净的 worktree，并保留分支与全部事件/收据。命令与恢复手册见 [Drydock 工作目录](docs/drydock.md)，设计见 [ADR 0129](docs/adr/0129-run-owned-drydock-workspaces.md)。

### 只读 LSP 语义代码智能

`code-intel-lsp.v1` 只启动操作者显式配置、审查并以 executable SHA-256 固定的本地 Language Server。Go 拥有 initialize、文档同步、请求取消/超时、崩溃重启和进程树清理；模型只在 Code Surface 的 root Plan/Deliver 回合看到 Server 实际协商成功的 workspace/document symbol、definition、references、implementation、hover、signature、diagnostics、call/type hierarchy 工具。Cyber、Specialist、rename、code action 和 format 均不开放。

每份语义结果绑定 Workspace/root、commit/branch/dirty digest、document URI/hash/version、Server generation/capability fingerprint 和 query/page。编辑、切换分支、换根目录或 Server 重启会让旧证据显式变为 stale；越界 URI、远程 link、secret/control 文本和超限结果会被拒绝、清洗或标记 partial。CLI 提供 `code-intel status|qualify`，认证 OpenAPI 和 Desktop 设置页只显示来源、语言、健康、capability、generation、有界错误与模型工具，永不返回 executable、argv、环境或凭证。最小环境和“不授予网络”不是 OS Sandbox；Server 本身必须受信任。配置、真实 gopls/TypeScript 兼容矩阵和残余风险见 [LSP 语义代码智能](docs/code-intelligence.md)。

### 事务化 Workspace Checkpoint

Schema v117 的 `workspace-checkpoint.v1` 在文件工具、Run-owned 命令批次/后台 Job、typed Git 写入与 agent merge 边界前后记录不可变检查点。检查点固定 base commit、branch、原始 Git index、稳定排序的 tracked/untracked manifest、内容哈希、触发收据、attempt/capability generation 与恢复等级；普通文件和 index 以 SHA-256 内容寻址去重，ignored/generated/large/sensitive/link/external 状态会显式标记而不是静默承诺可恢复。Shell 没有可移植 watcher，因此其边界明确降级为 `partial`，只承诺观测到的 Workspace/Git 状态，不宣称回滚根目录外副作用。

Desktop 的 **工作区检查点 / Checkpoints**、CLI 的 `cyberagent workspace checkpoint ...` 和认证 OpenAPI 共用同一个 Application 服务。Rewind、Undo、Redo 先做 live/current/target 三方预览，再以精确 cursor CAS 和当前权限确认写入；恢复本身是一次新的追加式写操作，不改写旧历史、不调用 `git reset --hard`、不批量清理 untracked 文件。Fork 从历史检查点建立独立 Git worktree、Workspace、Mission/Run/Session，且不继承审批、凭据、capability、lease、进程或网络授权；HTTP/Desktop 不接受或返回绝对 worktree 路径，由 Go 从受信源 Workspace 确定性生成同级目标。完整操作说明见 [Workspace Checkpoints](docs/workspace-checkpoints.md)，设计与失败语义见 [ADR 0118](docs/adr/0118-transactional-workspace-checkpoints.md)。

### 审批式高级 Git

Schema v123 的 `git-advanced.v1` 以默认关闭的 Go-owned 控制面提供稳定内容指纹 hunk stage/unstage/revert、明确 base/index/worktree/untracked 角色的 stash、可持续恢复的 rebase/cherry-pick/bisect 状态机，以及只能位于产品管理根内的 worktree create/lock/unlock/remove/prune。每次写入都固定 repository/common-dir、HEAD/branch、index/worktree/stash/sequencer/upstream、permission、capability generation 与 Workspace lease，先展示完整 preview，再创建一次性 Approval 与 Checkpoint；执行前全部重算，任一漂移都会失败关闭。终态收据、冲突 base/ours/theirs、序列 generation 和 worktree 注册表均持久且不可变。

高级 Git 不接受 raw argv、Shell、host path、任意 ref/pathspec 或 bisect 命令；Git 运行时关闭 hook、credential helper、外部 diff、filter/merge driver 和交互入口。保护分支、配置 upstream 的共享 rebase、detached 历史改写、force push、reset-hard、clean-force、脏/外部 worktree 删除都不能表达。重启只观察并终结 `running` 操作，不重放；已精确创建但尚未登记的 worktree 可在全量身份匹配后保守纳入注册表，但原操作仍标记 `interrupted`。Desktop、`cyberagent git-advanced` 与认证 OpenAPI 共用同一 Application 服务；完整命令、限制和恢复流程见[高级 Git 工作流](docs/git-advanced.md)，设计见 [ADR 0122](docs/adr/0122-go-owned-advanced-git-lifecycle.md)。

### GitHub App 审阅集成

Schema v124 的 `github-review-provider.v1` 把 GitHub PR 元数据、完整有界 changed-file 分页、reviews/threads/comments、checks/jobs、失败日志摘录和 Artifact 元数据保存为不可变、已清洗的不可信快照，再与本地精确 merge-base/HEAD、完整 diff、稳定 hunk、文件哈希、冲突及可选 LSP 证据组成 `verified/partial/stale/unavailable/not_run` 证据图。Code/root 模型只获得绑定当前 Run 的本地只读 `github_review_evidence_list/read`；它不能自行联网或回写。

产品优先使用开启 Device Flow 的 GitHub App，device code 仅驻留内存，access/refresh token 只进入操作系统凭据库。连接默认只读；Reply、Resolve/Unresolve、Submit Review 与 Request Reviewer 还需显式连接级写回开关、精确预览和一次性 Approval，执行前重查 Code/Deliver、网络权限、App installation/SSO、能力 generation 与 PR/base/head/merge-base。OAuth/PAT 在 v1 只作为无法扩大到写回的只读迁移路径；重启恢复只观察幂等 marker，绝不猜测重放。API/Desktop 必须显式启用 `--enable-github-review`。配置、最小权限、CLI/OpenAPI、真实联调和 GitHub Developer Program 申请门见 [GitHub Review Provider](docs/github-review.md)，设计见 [ADR 0123](docs/adr/0123-go-owned-github-review-provider.md)。

### 可交付 child 与隔离合并

Schema v118 的 `batch-delivery.v1` 将一个已经审批并 admission 的核心 child DAG 物化为最多两个独立 Git worktree/branch。每个 child 只获得绑定 Agent、generation、过期时间与一次性 owner token 的关闭工具集：owned Scope 内的 list/read/glob/grep、人工提案式 change/apply，以及固定 status/diff/commit；delete/rename、Shell、任意 process、network、credential、Debug、审批和继续派生 child 始终为 false。旧 Specialist 运行时仍保持 no-tool；它不会因为存在此协议而隐式扩权。

child 只有在 worktree clean、HEAD 是 base 的后代、全部 changed path 属于声明 Scope、固定验证通过且交付包含 base/head、完整 diff/call-chain 摘要、测试收据、evidence 与已知限制时，才能进入 `ready_for_review`。Submit 与 Reviewer 都会在验证结束后重新证明 exact branch/HEAD/diff/clean 状态；Reviewer 还必须独立确认完整 merge-base diff、调用链与验证，Desktop 不把作者摘要当证据。顺序 merge queue 在独立 integration worktree 上从最新已确认 base 逐项应用，每步重跑截至当前步骤的全部累积验证并重新证明 source、integration 与所有 child receipt；重叠、base drift、状态漂移、文本/语义冲突或测试失败都会阻止队列，不会自动选一方覆盖，也不会 push、开 PR 或修改远端。

默认验证只执行不会运行仓库代码的 `git diff --check`。`go_test`/`npm_test` 会执行 child 提交的代码，因此只有操作者启用相应控制能力，且当前 Run 仍为 `running` 并持有 `full_access`（或显式更高的 `debug`）时才可在宿主运行；Desktop 还需显式 `--enable-batch-delivery-control`，宿主校验另需 permission control、danger-full-access 与 `--enable-batch-validation-execution`。验证进程使用 Windows Job Object / Unix process-group 生命周期边界，Go 测试禁用缓存，持久层只记录完整输出流摘要；其离线/去凭证环境仍只是降险措施，不是 OS 网络或文件系统沙箱，POSIX 主动脱离 inherited process group 也仍是显式宿主权限的残余风险。完整操作与恢复说明见[可交付多代理](docs/batch-delivery.md)，设计决策见 [ADR 0119](docs/adr/0119-deliverable-batch-agents.md)。

### MCP Client、Plugin 与受限 Hooks

Schema v120-v121 增加 Go-owned MCP Client 和惰性 `plugin.v1` 包。MCP descriptor 先审查是否允许 discovery，再对真实协商得到的 tools/resources/prompts capability fingerprint 单独审查；只有精确 `Code/Deliver/root/full_access` Run 能看到已启用工具，每次调用都会重新发现并在漂移时隔离。远程 HTTPS bearer 仅按引用从系统凭证存储注入，stdio/HTTP 返回值始终作为不可信证据清洗；模型和普通 UI 永远看不到明文凭证，专用 MCP 审计只保存摘要。Supervisor 恢复账本只持久化经过 schema 校验、脱敏和大小限制的规范化调用/结果，不保存 bearer 或 transport 原始字节。

Plugin ZIP 只允许声明式 Skills、MCP descriptors、UI metadata 和 Hooks；严格文件白名单、摘要、大小、格式与可选 Ed25519 签名在 staging 时验证，默认禁用并逐能力人工启用。外部包可由固定 SHA-256 的无 redirect HTTPS 或固定 commit 的 bare Git 导入；升级/回滚原子切换唯一 enabled 版本，publisher revoke 不能由 `confirm-untrusted` 绕过。Hook 已接到 Tool、Run、Session、Compaction、Specialist 和 Checkpoint 的真实 Go 事务边界，只能拒绝、注释、记录或在 `pre_tool` 删除顶层字段。Desktop 设置页可按当前 Run/Workspace 查看健康、来源、审查和 metadata-only 调用审计，并用精确 fingerprint/generation 立即禁用。完整命令、状态机与残余宿主风险见 [MCP Client、Plugin 与受限 Hooks](docs/extensions.md)。
### 源码绑定的真实浏览器 UI 证据

Schema v119 的 `ui-evidence.v1` 把真实页面验证绑定到 commit/dirty digest/index/worktree manifest、精确 build/start recipe、固定浏览器 version/可执行文件 SHA-256、literal loopback URL/route、viewport/DPR、locale/theme/reduced motion、deterministic fixture/seed/page state、步骤与 capture policy。Application 在 build 前、readiness 后、浏览器断言后以及 owned process cleanup 完成后重新核对源码；拒绝已占用端口，不收养既有服务或个人 Browser Profile。Windows Desktop 的执行入口默认关闭，只有 Run execution、`full_access`、danger-full-access、restricted CDP 与 `--enable-ui-evidence` 同时成立才开放。

Desktop、认证 OpenAPI 与只读/导出 CLI 共用同一份不可变 Attempt、step 和 artifact 语义。PNG、DOM、accessibility、console/page error、network/HTTP 与 performance 产物都保存 SHA-256/MIME/尺寸/viewport/source step/commit/Run/Attempt/redaction/retention policy，且 PNG 尺寸必须匹配 `viewport × DPR`；页面和产物始终不可信、不授权。`not_run` 明确保持中性，只有 exact `passed` 才算通过。Windows CI 用 creation-time Job Object、临时 Profile 和 deterministic loopback fixture 跑真实 Edge 的 desktop/mobile、theme/locale/reduced-motion 矩阵，并证明缺失 click handler 的回归只能被真实页面交互断言捕获。详见 [UI Evidence 操作手册](docs/ui-evidence.md)与 [ADR 0120](docs/adr/0120-source-bound-real-browser-ui-evidence.md)。

### 真实 Git、PowerShell 与 Bash

针路簿调用真实的 Git 和操作系统 Shell，不是命令模拟器；但它也不会给模型一个永久、无审阅的裸终端。当前 Code 工作流按风险拆成以下入口：

| 入口 | 实际执行 | 权限与限制 |
|---|---|---|
| 类型化 Git | 真实 `git` 进程；覆盖本地整文件与稳定 hunk 操作、stash、rebase/cherry-pick/bisect、受管 worktree，以及独立授权的 fetch、fast-forward pull、push branch、创建/更新 PR | 参数由 Go 合成，固定仓库/权限/lease 状态并先审批与 Checkpoint；禁用 hook/外部 diff/凭证/自定义 driver，raw argv、force push 与共享历史改写不开放 |
| Run-owned 命令运行时 | 真实 PowerShell/Bash 或绝对路径原生进程；支持有序批次、cursor 输出、受限 stdin 与后台 Job | 仅 Code/Local/Deliver/root + `full_access`，且进程必须显式开启 permission-control/danger-full-access；固定无 Profile 解释器、受限环境、`network=disabled`/`credentials=none`，每次 Tool 调用重新校验当前 Run 租约与 Policy |
| Approval 一次性 Shell | Windows 上的真实 PowerShell 或同一 Git for Windows 发行版中的 Git Bash；命令被固定成无 Profile、非交互的一次性 argv | 仅 Code/Local/Controlled/Approval；模型只能提出一行命令，操作者必须核对解释器哈希、完整 argv、cwd 与宿主网络风险并逐条批准；不支持持久或后台所有权 |
| Debug 持久终端 | Windows 使用 PowerShell + ConPTY + creation-time Job Object；macOS 使用 Bash + PTY + 独立进程组 | 仅 Code/Local/Deliver/Debug；用户先启动终端，再显式授予 15 秒至 15 分钟的进程内 Agent 输入租约，可随时撤销。普通后台 job 随终端清理；主动 POSIX daemonize 仍是宿主残余风险 |
| Full-access 一次性进程 | Windows 上按绝对路径和 SHA-256 启动真实可执行文件与字面 argv | 仅操作者 CLI 双确认；仍是非沙箱宿主执行，可运行高权限解释器，但不向模型公开 |

`command_runtime` 与用户终端、Debug 终端、人工审批 one-shot 和 Docker Sandbox 不共享 session 或所有权。schema v116 先以当前 Supervisor generation lease 写入不可变启动意图，再由独立、可过期的进程所有者心跳维持后台 Job；下一 turn 可继续读取或写 stdin，另一进程不能凭数据库记录收养它。崩溃时 Windows creation-time Job Object 或 POSIX guardian/process group 回收 owned 进程树，重启只把所有者已过期的记录收敛为 `interrupted`，绝不按持久 PID 重新执行；POSIX 上主动新建 session 并脱离 inherited process group 的 daemon 仍是非沙箱 `full_access` 残余风险。stdout/stderr 以单调 cursor 保留通道与时间，内联窗口溢出后仍可生成有 SHA-256 的有界 Artifact；所有返回模型的内容统一去除 ANSI/C1/Unicode 控制序列、修复 UTF-8 并脱敏。

`debug_terminal` 每次写入仍经过 Shell Policy；需要另行逐条审批的命令不会借 Debug 租约绕过审批。授权瞬间会固定输出水位，模型不能读取租约授予前的终端滚动内容。为支持 Run 恢复，模型提交的规范化命令和水位之后脱敏、有界的结果会进入 Supervisor 工具记录；schema v113 让该工具进入同一持久调用账本并保留既有记录。进程内 Workspace 根目录摘要和 mode revision 会阻止目录或阶段漂移后旧租约复活；用户键盘输入、原始 PTY 字节、根目录路径和租约 bearer 均不持久化。应用重启会终止会话并使租约失效。Cyber Surface 不公开这些宿主 Shell 路径。完整边界见[使用手册](docs/usage.md)、[ADR 0114](docs/adr/0114-real-shell-transports-and-supervised-debug-terminal.md)和 [ADR 0117](docs/adr/0117-run-owned-command-runtime.md)。

### 安全边界

- 不公开 Provider 私有 thinking、原始 Prompt、raw delta、工具参数、工具原始输出或 API key。
- 项目指令、长期记忆和对话 Checkpoint 始终是不可信、非授权上下文；Workspace Checkpoint 只保存有界文件/index 状态。两类 Fork/Resume 都不恢复审批、capability、凭据、网络、进程、终端租约或执行档位。详见[双语上下文/威胁模型与删除说明](docs/context-continuity.md)、[Workspace Checkpoints](docs/workspace-checkpoints.md)、[ADR 0115](docs/adr/0115-non-authorizing-durable-context-continuity.md)和 [ADR 0118](docs/adr/0118-transactional-workspace-checkpoints.md)。
- 文件编辑、宿主命令、浏览器 CDP、终端输入和 Sandbox 是彼此独立的授权面。
- 可交付 child 的 owner token 只在创建或 generation 轮换响应中返回一次，SQLite 和普通 HTTP/Desktop 投影仅保留摘要；丢失后必须 CAS 轮换 generation，旧 token 立即失效。
- 普通命令运行时只接受 `network=disabled` 与 `credentials=none`，清空 credential helper/Profile/高风险环境入口并拒绝显式网络意图；它不是可证明的 OS 网络沙箱，`full_access` 仍是宿主执行能力，需要联网的命令必须改走独立逐次审阅路径。
- 受控命令默认使用 Go 固定模板；PowerShell/Bash 只通过 Code/Deliver/root + `full_access` 的 Run-owned runtime、逐条审批，或可撤销 Debug 租约三条独立路径开放。通用宿主执行与 Debug 能力不会因模型、Skill 或仓库文档而自动开启。
- Docker Sandbox 产品入口默认关闭。显式进程 capability、当前 `docker` Profile、匹配权限档、精确 per-call 审批、Policy、预算与 30 秒 readiness 必须同时成立；数据库记录不能在重启后恢复 start authority。
- 当前产品执行只接受 environment-free、secret-free 的 `network=disabled` Manifest，并在 Docker create/inspect 两侧固定 `network none`。allowlist/scoped egress 仍缺少 Go-owned host/port/protocol guard，因此一律以 `managed_egress_unavailable` 失败关闭；Docker 不可用时没有宿主 fallback。
- Windows Desktop 只在显式 `--enable-ui-evidence` 及其 Run execution/danger-full-access/restricted-CDP 前置条件同时成立时开放 loopback-only 真实浏览器证据；macOS 与普通 CLI 保持只读，Full CDP 仍是独立且默认关闭的 Debug 权限面。
- Windows/macOS Desktop 当前都是未签名的开发者/操作者便携预览，不是正式安装包；macOS 产物只有 ad-hoc 签名且未公证。

### Docker Sandbox 产品入口（默认关闭）

Schema v99 把 v97 的可恢复生命周期与 v98 的有界 I/O 组合到同一个 Go
`DockerSandboxService`。CLI、HTTP/OpenAPI、Desktop 和模型提案都复用该服务；模型工具
`sandbox_docker_run_propose` 只能请求准入，不能启动容器，也不能提交 Docker flags、
daemon endpoint、宿主 bind、环境变量或网络放宽。

```powershell
# 未带 capability 时只返回稳定的 disabled readiness，不接触 Docker 写接口。
cyberagent run sandbox docker-readiness <plan-id> --manifest-file <manifest.json>

# 真正准入/启动必须在同一进程显式开启 Docker 与权限 capability。
cyberagent run sandbox docker-admit <plan-id> --manifest-file <manifest.json> `
  --operation-key <stable-key> --enable-docker-execution --enable-permission-control
```

完整 CLI、HTTP、取消/恢复、reason/remediation 与预算说明见
[使用手册](docs/usage.md)、
[HTTP API](docs/http-api.md) 和
[ADR 0099](docs/adr/0099-docker-sandbox-product-admission-and-recovery.md)。普通 Code 工作流
仍不依赖 Docker。

### 模式感知 Skill 与生成候选

当前 13 个内置 Skill 使用 `profiles × surfaces × phases × roles` 四维兼容矩阵，并把
`user_invocable`、`model_invocable` 与 `explicit_only` 作为独立调用策略。schema v111
让外部 Skill 安装账本也完整保存这些字段；legacy 包保持原指纹和“仅操作者显式调用”
策略。安装始终是惰性的，不等于选择、正文注入或能力授权。

`run-skill-generator` 只适用于 Code/Deliver/root，并且必须由操作者显式选择。模型的
`skill_candidate_propose` 只能创建绑定真实工具调用和内容指纹的不可信候选；schema
v112 以只追加记录推导 `proposed -> approved -> imported` 或 `proposed -> rejected`，
模型、Agent 和 Supervisor 身份不能充当人工 reviewer。批准与导入是两步独立操作，
导入还需再次确认不可信指令；导入后仍需原有独立流程才能选择。

```powershell
cyberagent skill candidates --run <run-id>
cyberagent skill candidate show <candidate-id> --show-content
cyberagent skill candidate approve <candidate-id> `
  --candidate-fingerprint <sha256> --operation-key <stable-review-key>
cyberagent skill candidate import <candidate-id> `
  --candidate-fingerprint <sha256> --operation-key <stable-import-key> `
  --confirm-untrusted-skill
```

完整模式矩阵、候选限制和失败恢复语义见[使用手册](docs/usage.md)与
[ADR 0113](docs/adr/0113-mode-aware-external-skill-ledger-and-generated-candidate-review.md)。

### 持久化有界监控与结构化诊断

Schema v122 的 `scheduled-job.v1` 可为一个显式 Run 建立单次或固定间隔监控，并持久化
timezone、next wake、deadline、停止条件、轮次/模型/耗时预算、重试退避、通知和 owner。
进程内 Worker 固定并发度 1，只能通过启动参数开启；租约 generation 与私有 fence 防止
重启、并发 worker 或迟到完成造成重复执行。默认 `read_only` 且模型预算为零，目标状态
没有变化时只记录 `unchanged`，不会调用模型或工具。

`doctor-snapshot.v1`、`debug-query.v1` 和 `diagnostic-bundle.v1` 提供 provider/model harness、
Run/Workspace/权限/网络/工具 readiness 与有界单调事件时间线。事件 payload、prompt、终端
输入、命令输入和 secret 始终 withheld/redacted。CLI、认证 HTTP/OpenAPI、React 与 Desktop
复用同一 Application 服务；Desktop 支持创建、暂停、恢复、取消、查看下次唤醒/最近结果/
通知及导出诊断包。完整说明见[计划任务与结构化诊断](docs/scheduled-jobs-diagnostics.md)和
[ADR 0121](docs/adr/0121-durable-scheduled-monitoring-and-structured-diagnostics.md)。

## 快速开始

### 环境要求

- Go 1.25+
- Git 2.41+
- Node.js 24（构建 Web/Desktop 时）
- Windows Desktop 需要 Windows 10/11 与 Edge WebView2 Evergreen Runtime
- macOS Desktop 需要 macOS 11+（自带 WKWebView）与 Xcode 命令行工具
- Rust 1.97.1 仅在修改 Analyzer 时需要
- Docker Desktop 或 Linux Docker Engine 仅在开发 Sandbox 时需要；普通 Code 工作流不依赖 Docker

### 从源码运行

```powershell
git clone https://github.com/Qiyuanqiii/Traverse-Board.git
cd "Traverse-Board"

go run ./cmd/cyberagent version
go run ./cmd/cyberagent provider list
go run ./cmd/cyberagent workspace init demo
go run ./cmd/cyberagent workspace list
go run ./cmd/cyberagent tui
```

默认配置使用确定性的 Mock Provider，不需要 API key。接入外部模型前，请先阅读 [Provider 与模型配置](docs/usage.md#model-and-provider-commands)；凭证应进入系统凭证存储或进程环境，不能提交到仓库。

OpenAI-compatible 连接使用独立的 `CYBERAGENT_OPENAI_API_KEY`、
`CYBERAGENT_OPENAI_BASE_URL` 与 `CYBERAGENT_OPENAI_MODEL` 环境变量；后两项默认分别为
`https://api.openai.com` 和 `gpt-4.1-mini`。仓库内的 `configs/models.yaml`
只是无秘密示例，不会作为运行时配置源。

本地 Ollama 是唯一无凭证 Provider，只在显式设置 `CYBERAGENT_OLLAMA_BASE_URL`（仅
loopback `http`，默认 `http://127.0.0.1:11434`）与 `CYBERAGENT_OLLAMA_MODEL` 时启用；
非 loopback、HTTPS、redirect 与代理绕过一律拒绝。tools/vision/JSON/context 能力按
`/api/show` 探测结果失败关闭，不自动安装 Ollama、不 pull 模型、不扫描局域网。

### Windows Desktop 预览

```powershell
./scripts/build-desktop.ps1
./build/desktop/Start-Prayu-Operator-Preview.cmd
```

请使用操作者预览启动器。直接双击裸 `TraverseBoard.exe` 会按设计以最保守权限启动。完整人工测试步骤见 [`packaging/windows/LOCAL-TEST-GUIDE.txt`](packaging/windows/LOCAL-TEST-GUIDE.txt)。

在 Windows Desktop 中点击“新建任务”会直接打开系统文件夹选择器，并按“选择目录 -> 注册 Workspace -> 创建 Run”完成创建；无需先通过 CLI 或设置页注册工作区。取消选择不会创建 Workspace 或 Run，所选绝对路径不会返回 React。

### macOS Desktop 预览

```bash
./scripts/build-desktop-darwin.sh
open build/desktop/Prayu.app
```

请使用操作者预览启动器 `build/desktop/Start-Prayu-Operator-Preview.command`，或直接打开 `Prayu.app`（默认只读）。产物只有 ad-hoc 签名、未公证；从其他机器拷贝后首次打开可能需要在 Finder 中右键选择“打开”。系统凭证库尚未接入 macOS，请使用 `MIMO_API_KEY`、`DEEPSEEK_API_KEY`、`CYBERAGENT_ANTHROPIC_API_KEY` 等环境变量。用户终端默认关闭；带相应启动闸门时使用本地 Bash PTY，受限浏览器与完整 CDP 仍保持关闭。完整步骤见 [`packaging/macos/LOCAL-TEST-GUIDE.txt`](packaging/macos/LOCAL-TEST-GUIDE.txt)，边界见 [ADR 0097](docs/adr/0097-macos-desktop-portable-build.md) 与 [ADR 0114](docs/adr/0114-real-shell-transports-and-supervised-debug-terminal.md)。

更多命令与边界见[使用手册](docs/usage.md)。

### 便携 ZIP 下载与验证 / Portable ZIP download and verification

发布候选是未签名的便携 ZIP（`Prayu-portable-<version>-windows-amd64.zip`），包含 `TraverseBoard.exe`、操作者预览启动器、`LOCAL-TEST-GUIDE.txt`、`LICENSE`、第三方 `NOTICE`、CycloneDX `sbom.json` 与 `release-metadata.json`。正式 GitHub Release 也直接附带同一份 `TraverseBoard.exe`。发布件不带 API key、用户数据、缓存、调试日志或源映射。

The release candidate is an unsigned portable ZIP (`Prayu-portable-<version>-windows-amd64.zip`) containing `TraverseBoard.exe`, the operator-preview launcher, `LOCAL-TEST-GUIDE.txt`, `LICENSE`, a third-party `NOTICE`, the CycloneDX `sbom.json`, and `release-metadata.json`. The tagged GitHub Release also attaches the same `TraverseBoard.exe` directly. It carries no API key, user data, cache, debug log, or source map.

> [!NOTE]
> “便携”表示程序文件免安装，并不表示数据跟随解压目录。默认数据仍位于 `%USERPROFILE%\.cyberagent-workbench`，因此重新下载或换目录解压会继续使用同一数据库。`v0.1.0-rc.2` 无法升级一枚正式固化前的 v97 Windows 预览历史；schema v125 在后续构建中精确兼容并事务化修复它，不删除或伪造旧数据。见 [ADR 0126](docs/adr/0126-legacy-v97-docker-trigger-compatibility.md)。

**下载后校验 / Verify after download**（PowerShell）：

```powershell
# 从 GitHub Release 下载 ZIP 与伴随证明文件 / download the ZIP and companions
gh release download v0.1.0-rc.2 --pattern 'TraverseBoard.exe' --pattern 'Prayu-portable-*' --pattern 'SHA256SUMS'

# 与 SHA256SUMS 精确比对 / compare exactly against SHA256SUMS
$zip = 'Prayu-portable-v0.1.0-rc.2-windows-amd64.zip'
$expected = ((Get-Content .\SHA256SUMS | Where-Object { $_ -match "  $([regex]::Escape($zip))$" }) -split '\s+')[0]
$actual = (Get-FileHash ".\$zip" -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -cne $expected) { throw 'portable ZIP checksum mismatch' }

# 同时校验可直接下载的 EXE / verify the directly downloadable EXE too
$exe = 'TraverseBoard.exe'
$expectedExe = ((Get-Content .\SHA256SUMS | Where-Object { $_ -match "  $([regex]::Escape($exe))$" }) -split '\s+')[0]
$actualExe = (Get-FileHash ".\$exe" -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualExe -cne $expectedExe) { throw 'executable checksum mismatch' }

# 解压后使用启动器，不要直接双击裸 EXE / extract and use the launcher
Expand-Archive ".\$zip" -DestinationPath .\Prayu-portable
.\Prayu-portable\Start-Prayu-Operator-Preview.cmd
```

维护者从 clean checkout 生成同一套 ZIP、SBOM、NOTICE、校验和与清单只需一条命令：

```powershell
pwsh -NoProfile -File scripts/release-desktop.ps1 -Version v0.1.0-rc.2
```

Canonical release CI pins Go 1.25.12, Node 24.16.0 (including its bundled npm), and Rust 1.97.1; `release-metadata.json` additionally binds those observed versions plus the Go, npm, Cargo, and embedded-analyzer hashes. 本地命令会记录实际工具链，正式 GitHub Release 只采用上述 CI 固定版本。

`Desktop release` 工作流会在 PR 中重跑依赖边界、可复现构建、ZIP 完整性验证和解压启动冒烟；`workflow_dispatch` 只保留 Actions artifact，只有可追溯到 `main` 的 `v*` tag 才会创建 GitHub Release。带 `-` 的版本会发布为 prerelease。The `Desktop release` workflow reruns dependency boundaries, the reproducible build, ZIP integrity verification, and an extracted-executable startup smoke on pull requests. `workflow_dispatch` keeps only an Actions artifact; only a `v*` tag reachable from `main` creates a GitHub Release, and versions containing `-` are prereleases.

**SmartScreen 预期 / SmartScreen expectations**：便携 ZIP 与 EXE 均未签名，Windows SmartScreen 可能提示“未知发布者”。这是未签名候选的预期限制，不是构建缺陷；正式签名发行（MSIX）在另一条发布线完成。The ZIP and EXE are unsigned, so Windows SmartScreen may warn about an unknown publisher. This is the expected limitation of an unsigned candidate, not a build defect; the signed MSIX release is tracked separately.

### MSIX 安装 / MSIX installation

发布候选的 per-user MSIX（`PrayuDesktop.msix`）是正式安装包：它把安装文件放进包目录、把用户数据（Workspace、凭证、SQLite）留在包数据目录外，升级时保留、卸载时默认不删。便携 ZIP 是明确区分的免安装替代品，适合只读预览；MSIX 适合需要稳定安装/升级/卸载身份的日常使用。

The per-user MSIX (`PrayuDesktop.msix`) is the formal installer: it keeps install files inside the package directory and user data (Workspace, credentials, SQLite) outside it, preserved on upgrade and not deleted by the default uninstall. The portable ZIP is a distinct, install-free alternative for read-only preview; the MSIX suits everyday use that needs a stable install/upgrade/uninstall identity.

**校验签名 / Verify the signature**（PowerShell）：

```powershell
Get-AuthenticodeSignature .\PrayuDesktop.msix | Format-List Status, StatusMessage, SignerCertificate
```

**安装 / Install**：双击 `PrayuDesktop.msix`，或 `Add-AppxPackage .\PrayuDesktop.msix`。**升级 / Upgrade** 用更高 `Version` 的同一 identity 覆盖安装；**降级 / Downgrade** 会被 Windows 拒绝。**卸载 / Uninstall**：`Remove-AppxPackage PrayuDesktop`（默认保留用户数据；删除数据需另作明确确认）。

**WebView2 诊断 / WebView2 diagnosis**：缺 WebView2 或版本过旧时，应用只显示有界本机指导（不空白、不 Forbidden），且不会隐式安装。If WebView2 is missing or too old, the app shows only a bounded local instruction (no blank window, no `Forbidden`) and never installs it implicitly.

**签名 / Signing**：正式发行需要受保护的代码签名证书；未签名 MSIX 只能作为本地开发候选。The formal release requires a protected code-signing certificate; an unsigned MSIX is only a local development candidate.

## 项目结构

| 路径 | 说明 |
|---|---|
| `cmd/cyberagent` | CLI/TUI/API 入口 |
| `cmd/cyberagent-desktop` | Windows/macOS Desktop 壳 |
| `internal/` | Go 领域、应用、Policy、Store、Tool、Sandbox 与 HTTP 控制平面 |
| `web/` | React/Vite 操作界面；不拥有权限、密钥或执行器 |
| `analyzers/` | Rust 确定性 Analyzer 与共享向量 |
| `configs/` | 无秘密的配置模板 |
| `docs/` | 架构、使用手册、状态账本、ADR 与产品范围 |
| `packaging/` | 本地便携预览与测试说明 |

## 文档

- [文档导航](docs/README.md)
- [产品范围与可选扩展](docs/PRODUCT_SCOPE.md)
- [MCP Client、Plugin 与受限 Hooks](docs/extensions.md)
- [LSP 语义代码智能](docs/code-intelligence.md)
- [架构说明](docs/architecture.md)
- [使用手册](docs/usage.md)
- [当前项目状态](docs/PROJECT_STATUS.md)
- [任务书](docs/TASK_BOOK.md)
- [贡献指南](CONTRIBUTING.md)
- [架构决策记录](docs/adr/)

## 历史开发记录

本节归档旧 README 顶部曾使用的切片与百分比口径。它们用于解释项目如何演进，不是性能 Benchmark、版本承诺或发布证明；当前能力以代码、测试、[`PROJECT_STATUS`](docs/PROJECT_STATUS.md) 和具体 ADR 为准。

### 历史双指标快照

截至 **2026-08-13 / schema v96 / P13-H1 至 P13-H3**，旧任务书估算为：

| 历史指标 | 旧估算 | 说明 |
|---|---:|---|
| 架构完成度 | 约 99% | Go 控制平面、Run/Session、事件、审批、预算、Skills、报告与跨语言边界覆盖度 |
| 产品可用度 | 约 98% | 开发者操作者预览中的通用端到端工作流，不代表正式发行就绪 |
| 通用 Coding Agent | 约 98% | 代码工作区、对话、计划、审阅、提案、验证和交接能力 |
| Cyber 自动化 | 约 20% | 旧路线图估算，现已停止作为活跃指标；相关能力转为可选附加范围 |

### 阶段与切片索引

| 阶段 | 历史交付主题 |
|---|---|
| v0.1 / P0-P2 | CLI 骨架、Workspace、SQLite、Provider、Session，以及 Run-centric 可恢复 Supervisor |
| P3-P5 | Work/Note、Coordinator、受控 child/Fan-out、Tool Gateway、审批、Artifact 与结构化记忆 |
| P6-P8 | Sandbox 证据合同、非授权 Docker 生命周期探针、Skill Registry、Finding/Evidence/Report、SARIF 与 CI 投影 |
| P9 / Desktop D0-D1 | HTTP/OpenAPI、React/TUI/Desktop、仓库/Diff/编辑/验证/Handoff 与液态玻璃工作台 |
| P10-A 至 P10-M | Go/Rust Analyzer 协议、共享向量、内嵌 WASI 执行、一次性能力与产品接入 |
| P11-A 至 P11-C / schema v119 | 浏览器权限、Profile、CDP/WFP 证据链与显式门禁的源码绑定 UI-evidence 产品路径 |
| P12-A 至 P12-E | 交互模型、受控 Windows Runner、用户终端、四档权限、固定命令审批与宿主执行账本 |
| P13-A 至 P13-H | Run Activity、公开模型流、连续对话、Markdown、Diff 审阅、Live Activity 与桌面视觉收口 |

完整逐切片原始记录保留在 [`PROGRESS_BOOK.md`](docs/PROGRESS_BOOK.md)，当前检查点与验收证据保留在 [`PROJECT_STATUS.md`](docs/PROJECT_STATUS.md)，恢复上下文见 [`PROJECT_MEMORY.md`](docs/PROJECT_MEMORY.md)。这些账本是历史记录，不应被当作待重新执行的任务列表。

<details>
<summary><strong>SQLite Schema v1-v123 迁移审计表 / Migration ledger</strong></summary>

此表是 Store 防漏迁移测试使用的审计合同。新增 schema 时必须按顺序追加，不得改写或删除既有行。

| Schema | 中文记录 | English record |
|---|---|---|
| v1 | v0.1 基线存储 | v0.1 baseline |
| v2 | Mission/Run 中心化基础 | run-centric foundation |
| v3 | Run 与 Session 投影 | run session projection |
| v4 | 旧 Task 到 Run 的兼容映射 | legacy task run mapping |
| v5 | Supervisor 检查点 | supervisor checkpoints |
| v6 | Supervisor 预算账本 | supervisor budget ledger |
| v7 | Supervisor 待处理输入 | supervisor pending input |
| v8 | Supervisor 协议修复 | supervisor protocol repair |
| v9 | Run 工作看板 | run work board |
| v10 | Run Notes 结构化记忆 | run notes |
| v11 | 持久化工具审批 | durable tool approvals |
| v12 | Session Grant 与工具预算 | session grants and tool budgets |
| v13 | 类型化脚本进程提案 | typed script process proposals |
| v14 | Run 工具输出 Artifact | run tool output artifacts |
| v15 | 结构化记忆工具操作 | structured memory tool operations |
| v16 | Supervisor 结构化工具循环 | supervisor structured tool loop |
| v17 | Run execution lease | run execution leases |
| v18 | 跨进程模型取消 | cross-process model cancellation |
| v19 | 单 root Agent Coordinator | single-root agent coordinator |
| v20 | 幂等 Agent inbox 协议 | idempotent agent inbox protocol |
| v21 | 有界 Specialist 准入 | bounded specialist admission |
| v22 | Agent 归属的工作记忆 | agent-owned work memory |
| v23 | Specialist 完成报告 | specialist completion reports |
| v24 | 受 lease 保护的 Specialist Attempt | leased specialist attempts |
| v25 | root inbox 上下文交付 | root inbox context delivery |
| v26 | Specialist 模型调用账本 | specialist model call ledger |
| v27 | Specialist 上下文交付 | specialist context delivery |
| v28 | Specialist 协议修复 | specialist protocol repair |
| v29 | Specialist 调度与取消控制 | specialist schedule and cancellation control |
| v30 | 审阅门禁的 Specialist 委派提案 | review-gated specialist delegation proposals |
| v31 | 不可变 Specialist 委派审阅 | immutable specialist delegation reviews |
| v32 | 可恢复 Specialist 委派应用 | recoverable specialist delegation application |
| v33 | 不可变只读 Fan-out 计划 | immutable read-only fan-out plans |
| v34 | 有界只读 Fan-out 执行 | bounded read-only fan-out execution |
| v35 | 确定性 Finding 报告投影 | deterministic finding report projection |
| v36 | Artifact 支撑的 Finding 验证 | Artifact-backed finding validation |
| v37 | Finding 接受、修复生命周期 | accepted and fixed finding remediation lifecycle |
| v38 | 操作者控制的 Specialist 调度 | operator-controlled Specialist scheduling |
| v39 | 不可变 Run Skill 选择 | immutable Run Skill selection |
| v40 | root Skill 上下文来源 | root Skill context provenance |
| v41 | 不可变 Run 执行模式 | immutable Run execution mode |
| v42 | 审阅门禁的 Plan/Delivery 工作流 | review-gated Plan Delivery workflow |
| v43 | 不可变 Session 上下文来源 | immutable session context provenance |
| v44 | 不可变 Delivery 检查点门禁 | immutable Delivery checkpoint gates |
| v45 | 持久化操作者引导队列 | durable operator steering queue |
| v46 | 操作者引导队列控制 | operator steering queue controls |
| v47 | 最小化 Specialist Skill 上下文 | minimal Specialist Skill context |
| v48 | Go 主控 Sandbox Manifest 准备 | Go-owned Sandbox Manifest preparation |
| v49 | Sandbox 审批与禁用执行候选 | sandbox approval and disabled execution candidates |
| v50 | 禁用态 Sandbox 生命周期与 Artifact 绑定 | disabled Sandbox lifecycle and Artifact bindings |
| v51 | Sandbox 后端与输出禁用态预检 | disabled Sandbox backend and output preflight |
| v52 | 仅模拟的 Sandbox 后端证据与输出事务 | simulation-only Sandbox backend evidence and output transaction |
| v53 | 只读 Docker 生产环境观测 | read-only Docker production observation |
| v54 | 确定性 Docker 容器计划与假写事务 | deterministic Docker container plans and fake write transactions |
| v55 | 有界 Docker 创建、核验、删除演练 | bounded Docker create-inspect-remove rehearsals |
| v56 | 可恢复 Docker 演练意图、代际租约与检查矩阵 | recoverable Docker rehearsal intents, generation leases, and control matrix |
| v57 | 描述符固定与内核密封的宿主输入演练 | descriptor-pinned and kernel-sealed host-input rehearsal |
| v58 | daemon stage 前持久化宿主输入要求 | durable pre-stage host-input requirement |
| v59 | daemon 托管、回读核验的不可变宿主输入交接 | daemon-owned, readback-verified immutable host-input handoff |
| v60 | 确定性 Docker 运行时输入投影计划 | deterministic Docker runtime input projection plan |
| v61 | 可恢复 Docker 运行时输入卷应用 | recoverable Docker runtime input application |
| v62 | 保留运行时输入资源检查与精确清理 | retained runtime-input resource inspection and exact cleanup |
| v63 | 阻塞态 Docker 进程启动门设计审查 | blocked Docker process start-gate design review |
| v64 | 不可变 Run 执行环境档位选择 | immutable Run execution profile selection |
| v65 | 非授权 Docker 生产证据捕获账本 | non-authorizing Docker production evidence capture ledger |
| v66 | 可恢复 Docker 生产证据捕获 Attempt | recoverable Docker production-evidence capture attempts |
| v67 | Linux 只读 Docker 生产证据探针 | Linux read-only Docker production-evidence harness |
| v68 | 不可变 Docker 生产证据操作员审阅 | immutable Docker production-evidence operator review |
| v69 | 内容寻址惰性用户 Skill 安装账本 | content-addressed inert user Skill installation ledger |
| v70 | 外部 Skill 的 Run 固定选择与最小化上下文 | external-Skill Run selection and minimized context delivery |
| v71 | 有界外部 Skill 来源与交付只读投影 | bounded read-only external-Skill provenance and delivery projection |
| v72 | 幂等受控 Mission/Run/Session 创建账本 | idempotent controlled Mission/Run/Session creation ledger |
| v73 | 幂等 Run 生命周期与有界执行交接 | idempotent Run lifecycle and bounded execution handoff |
| v74 | 持久化 Run wake 重试意图与单一所有权 | durable Run wake retry intents and single-owner fencing |
| v75 | 显式前台 wake 消费与可恢复执行交接 | explicit foreground wake consumption and recoverable execution handoff |
| v76 | 已批准 FileEdit 的幂等独立 apply | idempotent independent apply for approved FileEdits |
| v77 | 非授权 Session 工作区证据挂载 | non-authorizing Session Workspace evidence attachments |
| v78 | 不可变操作者验证证据 | immutable operator verification evidence |
| v79 | 可恢复的 Run 无进展熔断 | recoverable Run livelock progress guard |
| v80 | 不可变操作者验证计划与检查清单 | immutable operator verification plans and checklists |
| v81 | 验证计划项与人工证据的不可变显式关联 | immutable explicit verification plan-item/evidence associations |
| v82 | 不可变累计上下文交接记忆 | immutable cumulative context handoff memory |
| v83 | 不可变验证快照回执历史 | immutable verification snapshot receipt history |
| v84 | 不可变且不授权的验证快照回执复核 | immutable non-authorizing verification snapshot receipt reviews |
| v85 | 可恢复且不启动的浏览器接纳、租约与人工复核门 | durable non-starting browser acceptance, lease, and operator-review gates |
| v86 | 操作者选择且不授权的执行交互边界 | operator-selected non-authorizing execution interaction boundaries |
| v87 | 受控命令的写前 intent 与不可变执行回执 | write-ahead intents and immutable receipts for controlled commands |
| v88 | 操作者选择、运行期重校验的四档执行权限 | operator-selected four-level execution permissions with runtime re-gating |
| v89 | Agent 固定命令提案、独立审批和不可信结果回送 | review-gated Agent fixed-command proposals with untrusted result projection |
| v90 | 非沙箱一次性宿主命令执行账本 | non-sandboxed one-shot host-command execution ledger |
| v91 | 独立的受限/完整调试 CDP 权限快照 | independent restricted/full-debug CDP permission snapshots |
| v92 | 可恢复且只追加的浏览器运行时生命周期记录 | recoverable append-only browser runtime lifecycle records |
| v93 | Analyzer 一次性请求、写前意图与恢复收据 | Analyzer one-shot request, write-ahead intent, and recovery receipts |
| v94 | 一次性 Analyzer 执行授权与原子消费防重放 | one-shot Analyzer execution capabilities with atomic replay-safe consumption |
| v95 | Analyzer 结果、Artifact 与审计事件原子提交 | atomic Analyzer result, Artifact, and audit-event commit |
| v96 | 用户审批档的精确宿主命令提案、审阅与恰好一次执行 | exact approval-mode host-command proposals, reviews, and exactly-once execution |
| v97 | 持久 Docker 生命周期所有权、代际租约与崩溃恢复 | durable Docker lifecycle ownership, generation leases, and crash recovery |
| v98 | 有界 Docker 容器 I/O 合同：只读输入投影、日志限额与原子输出提交 | bounded Docker container I/O contract: read-only input projection, log capture limits, and atomic output commit |
| v99 | 不可变 Docker Sandbox 产品准入、启动绑定、取消与终态回执 | immutable Docker Sandbox product admission, launch binding, cancellation, and terminal receipts |
| v100 | 算子价格快照与 Run 金额预算账本（预留/结算/释放） | operator price snapshots and the run monetary budget ledger (reserve/settle/release) |
| v101 | 结构化 Agent 依赖等待与唯一唤醒收据 | structured agent dependency waiting and unique wake receipts |
| v102 | 模型提议的有界 child 任务调度（core/readonly fan-out 分面、去重与准入） | model-proposed bounded child task scheduling (core/read-only fan-out surfaces, dedup, and admission) |
| v103 | 持久浏览器网络隔离证据与操作者 review | durable browser network containment evidence and operator review |
| v104 | 签名 Skill 包、团队 Catalog 与固定 URL/Git 导入（publisher 信任/撤销、版本 pin、审计） | signed skill packages, team catalog, and pinned URL/Git imports (publisher trust/revoke, version pins, audit) |
| v105 | 工作区一次性命令提案（不可变参数、审批指纹、操作者执行） | workspace one-shot command proposals (immutable parameters, approval fingerprints, operator execution) |
| v106 | typed 本地 Git 写操作台账（绑定指纹、幂等、回读收据） | typed local Git mutation operations (binding fingerprints, idempotency, readback receipts) |
| v107 | 网络作用域远端 Git 与 PR 操作台账（host/port/protocol/TTL/Run 绑定、脱敏收据） | network-scoped remote Git and PR operations (host/port/protocol/TTL/Run binding, redacted receipts) |
| v108 | Debug 终端会话台账（状态/cwd/resize/进程/Agent 输入态） | debug terminal session ledger (state/cwd/resize/process/agent-input status) |
| v109 | 完整 Supervisor 结构化工具注册表（child、Docker 与一次性命令） | complete Supervisor structured-tool registry (child, Docker, and one-shot commands) |
| v110 | 按 Run mode 固定的 root Skill 阶段子集与空交付账本 | Run-mode-bound root Skill phase subsets and empty-delivery ledger |
| v111 | 保存 Surface/Phase/Role 与调用策略的外部 Skill 安装账本 | external-Skill installation ledger preserving Surface/Phase/Role and invocation policy |
| v112 | 工具来源绑定、人工审查门禁的不可信 Skill 候选状态机 | tool-origin-bound, human-review-gated untrusted Skill candidate state machine |
| v113 | 允许 Debug 终端进入 Supervisor 持久工具调用账本 | admit the debug terminal into the durable Supervisor tool-call ledger |
| v114 | 层级项目指令快照、显式长期记忆与非授权会话连续性树 | hierarchical project-instruction snapshots, explicit long-term memory, and non-authorizing session continuity trees |
| v115 | 模型可调用的工作区工具与哈希保护文件变更 | model-callable workspace tools and hash-guarded file mutations |
| v116 | 增加 Run-owned command-runtime.v2 Job 与 Supervisor 调用账本 | add Run-owned command-runtime.v2 jobs and Supervisor call ledger support |
| v117 | 增加事务化 workspace-checkpoint.v1、恢复/Fork 账本与内容寻址 blob | add transactional workspace-checkpoint.v1, restore/Fork ledger, and content-addressed blobs |
| v118 | 增加 batch-delivery.v1、child Worktree/邮箱/交付复核与顺序合并队列 | add batch-delivery.v1, child worktrees/mailbox, delivery review, and ordered merge queues |
| v119 | 增加源码绑定的 ui-evidence.v1 Attempt、步骤与内容寻址真实浏览器产物 | add source-bound ui-evidence.v1 attempts, steps, and content-addressed real-browser artifacts |
| v120 | 增加两阶段 MCP Client Server、能力快照与 metadata-only 调用账本 | add two-stage MCP Client servers, capability snapshots, and metadata-only call audits |
| v121 | 增加签名 plugin.v1 安装、publisher 信任/撤销、回滚与受限 Hook 审计 | add signed plugin.v1 installs, publisher trust/revocation, rollback, and restricted-Hook audits |
| v122 | 增加 scheduled-job.v1、单实例租约/fencing、轮次与通知账本 | add scheduled-job.v1, singleton lease/fencing, round, and notification ledgers |
| v123 | 增加 git-advanced.v1 操作/序列审计与产品受管 worktree 注册表 | add git-advanced.v1 operation/sequence audits and the product-managed worktree registry |
| v124 | 增加 GitHub connection、PR/CI 快照、本地证据图及审批回写/恢复账本 | add GitHub connections, PR/CI snapshots, local evidence graphs, and approved write/recovery ledgers |
| v125 | 精确兼容旧 Windows 预览版 v97，并事务化重建 Docker lifecycle cleanup trigger | accept the exact legacy Windows preview v97 history and transactionally rebuild its Docker lifecycle cleanup trigger |
| v126 | 增加 Workspace Access 工作区执行权限合同与沙箱 readiness 闸门 | add the Workspace Access permission contract and sandbox-readiness gate |
| v127 | 增加 Run-owned Drydock、Workspace Trust、恢复/交付/清理事件与收据 | add Run-owned Drydocks, Workspace Trust, and recovery/delivery/cleanup events and receipts |
| v128 | 允许固定 Docker Standard Code 后端复用 Workspace Access admission | allow the fixed Docker Standard Code backend to reuse Workspace Access admission |

</details>

## 可选附加能力

CTF、自动化渗透、漏洞利用、横向移动和专项攻防工具链**不属于当前核心开发范围**。现有 `ctf` CLI 仅为早期兼容骨架，不代表已经具备自动解题或真实攻击能力。

未来如重新启动该方向，应以独立插件或 Profile 接入现有通用接口：

- `llm.Provider` 与模型 Harness；
- `tools.Tool`、Skill 包和 Policy/Scope；
- Go/Rust Analyzer JSON 协议；
- `sandbox.Runner` 与独立网络隔离证据；
- Finding/Evidence/Report 与 SARIF 导出。

任何附加包都不能绕过 Go 控制平面，也不能把“面向 CTF”解释为默认开放公网扫描、凭证读取或破坏性命令。详见[产品范围](docs/PRODUCT_SCOPE.md)。

## 贡献

提交变更前请阅读[贡献指南](CONTRIBUTING.md)。涉及权限、网络、凭证、进程、持久化或跨语言所有权时，需要先明确威胁模型、失败语义和测试证据。

## 许可证

本项目采用 [Apache License 2.0](LICENSE)。
