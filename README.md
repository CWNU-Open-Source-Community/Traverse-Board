<div align="center">
  <h1>Prayu</h1>
  <p><strong>本地优先、可恢复、可审计的通用 AI Agent 工作台</strong></p>
  <p>
    <a href="README.md">简体中文</a> |
    <a href="README.en.md">English</a>
  </p>
  <p>
    <a href="https://github.com/Qiyuanqiii/CTF-CyberAgent-Workbench/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Qiyuanqiii/CTF-CyberAgent-Workbench/ci.yml?branch=main&style=flat-square"></a>
    <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/Qiyuanqiii/CTF-CyberAgent-Workbench?style=flat-square"></a>
    <img alt="Go" src="https://img.shields.io/badge/control%20plane-Go-00ADD8?style=flat-square">
    <img alt="Desktop" src="https://img.shields.io/badge/desktop-Windows-0078D4?style=flat-square">
    <img alt="Desktop macOS" src="https://img.shields.io/badge/desktop-macOS-555555?style=flat-square">
  </p>
</div>

> **命名说明：** 产品与界面名称是 **Prayu**。为保持既有活动记录、收藏和外部链接有效，GitHub 仓库地址暂时保留为 `Qiyuanqiii/CTF-CyberAgent-Workbench`。`cyberagent` CLI、Go module、数据目录和环境变量也暂不迁移；它们是兼容标识，不是第二套产品。

## Prayu 是什么

Prayu 是一个由 Go 主控的本地 AI Agent 工作台。它把模型路由、长任务恢复、工作区、工具调用、审批、预算、记忆和审计事件统一到一个 Run-centric 运行时中，并通过 CLI、TUI、HTTP API、React 控制台和 Windows/macOS Desktop 提供同一套能力。

用户的长期目标是 `Mission`，一次可恢复的执行尝试是 `Run`。模型可以规划和提出动作，但 Go 始终拥有状态机、凭证、权限、持久化与执行边界。仓库文件、网页、模型文字和工具输出都只是不可信证据，不能自行升级为指令或权限。

当前产品重点是**通用 Code Agent 工作流**。CTF/专项网络安全求解已调整为可选附加能力，暂不进入活跃开发计划；仓库只保留通用的 Skill、Tool、Analyzer、Sandbox、Provider 和 Report 扩展接口，供未来独立插件接入。详见[产品范围](docs/PRODUCT_SCOPE.md)。

## 为什么选择 Prayu

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
| 模型与上下文 | Mock、Anthropic-compatible、OpenAI-compatible 与 loopback-only Ollama Provider、模型路由、资格校验、能力探测、流式响应、上下文压缩、结构化记忆 |
| 计划与协作 | Plan/Delivery、工作项、备注、最多两个核心 child、1/2/4/6 档只读 Fan-out、共享预算与取消扇出 |
| 工具与权限 | Tool Gateway、JSON Schema 校验、Policy、Scope、人工审批、四档宿主权限、受控固定命令提案 |
| 代码工作流 | 系统目录选择与 Workspace 导入、工作区浏览、仓库状态、提交历史、Diff 审阅、文件编辑提案、验证计划、Code Journey 与 Handoff |
| 可观测性 | 追加式 Run 事件、Live Activity、公开模型进度、Harness 事实、Artifact、Finding/Evidence/Report、SARIF |
| 扩展 | 惰性 Skill 包、Provider 接口、Tool 接口、Go/Rust JSON 协议、内嵌 WASI Analyzer、Sandbox 合同与默认关闭的 network-none Docker 产品执行 |
| 客户端 | `cyberagent` CLI、Bubble Tea TUI、认证 HTTP/OpenAPI、React/Vite、Windows/macOS Desktop 便携预览 |

### 安全边界

- 不公开 Provider 私有 thinking、原始 Prompt、raw delta、工具参数、工具原始输出或 API key。
- 文件编辑、宿主命令、浏览器 CDP、终端输入和 Sandbox 是彼此独立的授权面。
- 受控命令默认使用 Go 固定模板；通用宿主执行与 Debug 能力不会因模型、Skill 或仓库文档而自动开启。
- Docker Sandbox 产品入口默认关闭。显式进程 capability、当前 `docker` Profile、匹配权限档、精确 per-call 审批、Policy、预算与 30 秒 readiness 必须同时成立；数据库记录不能在重启后恢复 start authority。
- 当前产品执行只接受 environment-free、secret-free 的 `network=disabled` Manifest，并在 Docker create/inspect 两侧固定 `network none`。allowlist/scoped egress 仍缺少 Go-owned host/port/protocol guard，因此一律以 `managed_egress_unavailable` 失败关闭；Docker 不可用时没有宿主 fallback。
- 内置浏览器仍没有产品入口：受限运行时核心存在，但独立 OS/容器网络隔离证据尚未完成。
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
git clone https://github.com/Qiyuanqiii/CTF-CyberAgent-Workbench.git
cd "CTF-CyberAgent-Workbench"

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

请使用操作者预览启动器。直接双击裸 `cyberagent-desktop.exe` 会按设计以最保守权限启动。完整人工测试步骤见 [`packaging/windows/LOCAL-TEST-GUIDE.txt`](packaging/windows/LOCAL-TEST-GUIDE.txt)。

在 Windows Desktop 中点击“新建任务”会直接打开系统文件夹选择器，并按“选择目录 -> 注册 Workspace -> 创建 Run”完成创建；无需先通过 CLI 或设置页注册工作区。取消选择不会创建 Workspace 或 Run，所选绝对路径不会返回 React。

### macOS Desktop 预览

```bash
./scripts/build-desktop-darwin.sh
open build/desktop/Prayu.app
```

请使用操作者预览启动器 `build/desktop/Start-Prayu-Operator-Preview.command`，或直接打开 `Prayu.app`（默认只读）。产物只有 ad-hoc 签名、未公证；从其他机器拷贝后首次打开可能需要在 Finder 中右键选择“打开”。系统凭证库尚未接入 macOS，请使用 `MIMO_API_KEY`、`DEEPSEEK_API_KEY`、`CYBERAGENT_ANTHROPIC_API_KEY` 等环境变量；ConPTY 用户终端、受限浏览器与完整 CDP 在 macOS 保持关闭。完整步骤见 [`packaging/macos/LOCAL-TEST-GUIDE.txt`](packaging/macos/LOCAL-TEST-GUIDE.txt)，边界见 [ADR 0097](docs/adr/0097-macos-desktop-portable-build.md)。

更多命令与边界见[使用手册](docs/usage.md)。

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
| P11-A 至 P11-C | 浏览器权限、Profile、CDP 与 WFP 证据链；产品入口仍关闭 |
| P12-A 至 P12-E | 交互模型、受控 Windows Runner、用户终端、四档权限、固定命令审批与宿主执行账本 |
| P13-A 至 P13-H | Run Activity、公开模型流、连续对话、Markdown、Diff 审阅、Live Activity 与桌面视觉收口 |

完整逐切片原始记录保留在 [`PROGRESS_BOOK.md`](docs/PROGRESS_BOOK.md)，当前检查点与验收证据保留在 [`PROJECT_STATUS.md`](docs/PROJECT_STATUS.md)，恢复上下文见 [`PROJECT_MEMORY.md`](docs/PROJECT_MEMORY.md)。这些账本是历史记录，不应被当作待重新执行的任务列表。

<details>
<summary><strong>SQLite Schema v1-v99 迁移审计表 / Migration ledger</strong></summary>

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
