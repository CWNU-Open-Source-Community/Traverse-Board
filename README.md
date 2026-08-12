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
  </p>
</div>

> **命名说明：** 产品与界面名称是 **Prayu**。为保持既有活动记录、收藏和外部链接有效，GitHub 仓库地址暂时保留为 `Qiyuanqiii/CTF-CyberAgent-Workbench`。`cyberagent` CLI、Go module、数据目录和环境变量也暂不迁移；它们是兼容标识，不是第二套产品。

## Prayu 是什么

Prayu 是一个由 Go 主控的本地 AI Agent 工作台。它把模型路由、长任务恢复、工作区、工具调用、审批、预算、记忆和审计事件统一到一个 Run-centric 运行时中，并通过 CLI、TUI、HTTP API、React 控制台和 Windows Desktop 提供同一套能力。

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
| 模型与上下文 | Mock 与 Anthropic-compatible Provider、模型路由、资格校验、流式响应、上下文压缩、结构化记忆 |
| 计划与协作 | Plan/Delivery、工作项、备注、最多两个核心 child、1/2/4/6 档只读 Fan-out、共享预算与取消扇出 |
| 工具与权限 | Tool Gateway、JSON Schema 校验、Policy、Scope、人工审批、四档宿主权限、受控固定命令提案 |
| 代码工作流 | 工作区浏览、仓库状态、提交历史、Diff 审阅、文件编辑提案、验证计划、Code Journey 与 Handoff |
| 可观测性 | 追加式 Run 事件、Live Activity、公开模型进度、Harness 事实、Artifact、Finding/Evidence/Report、SARIF |
| 扩展 | 惰性 Skill 包、Provider 接口、Tool 接口、Go/Rust JSON 协议、内嵌 WASI Analyzer、Sandbox 合同 |
| 客户端 | `cyberagent` CLI、Bubble Tea TUI、认证 HTTP/OpenAPI、React/Vite、Windows Desktop 便携预览 |

### 安全边界

- 不公开 Provider 私有 thinking、原始 Prompt、raw delta、工具参数、工具原始输出或 API key。
- 文件编辑、宿主命令、浏览器 CDP、终端输入和 Sandbox 是彼此独立的授权面。
- 受控命令默认使用 Go 固定模板；通用宿主执行与 Debug 能力不会因模型、Skill 或仓库文档而自动开启。
- 内置浏览器仍没有产品入口：受限运行时核心存在，但独立 OS/容器网络隔离证据尚未完成。
- Windows Desktop 当前是未签名的开发者/操作者便携预览，不是正式安装包。

## 快速开始

### 环境要求

- Go 1.25+
- Git 2.41+
- Node.js 24（构建 Web/Desktop 时）
- Windows Desktop 需要 Windows 10/11 与 Edge WebView2 Evergreen Runtime
- Rust 1.97.1 仅在修改 Analyzer 时需要

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

### Windows Desktop 预览

```powershell
./scripts/build-desktop.ps1
./build/desktop/Start-Prayu-Operator-Preview.cmd
```

请使用操作者预览启动器。直接双击裸 `cyberagent-desktop.exe` 会按设计以最保守权限启动。完整人工测试步骤见 [`packaging/windows/LOCAL-TEST-GUIDE.txt`](packaging/windows/LOCAL-TEST-GUIDE.txt)。

更多命令与边界见[使用手册](docs/usage.md)。

## 项目结构

| 路径 | 说明 |
|---|---|
| `cmd/cyberagent` | CLI/TUI/API 入口 |
| `cmd/cyberagent-desktop` | Windows Desktop 壳 |
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
| P6-P8 | Sandbox 证据合同、Skill Registry、Finding/Evidence/Report、SARIF 与 CI 投影 |
| P9 / Desktop D0-D1 | HTTP/OpenAPI、React/TUI/Desktop、仓库/Diff/编辑/验证/Handoff 与液态玻璃工作台 |
| P10-A 至 P10-M | Go/Rust Analyzer 协议、共享向量、内嵌 WASI 执行、一次性能力与产品接入 |
| P11-A 至 P11-C | 浏览器权限、Profile、CDP 与 WFP 证据链；产品入口仍关闭 |
| P12-A 至 P12-E | 交互模型、受控 Windows Runner、用户终端、四档权限、固定命令审批与宿主执行账本 |
| P13-A 至 P13-H | Run Activity、公开模型流、连续对话、Markdown、Diff 审阅、Live Activity 与桌面视觉收口 |

完整逐切片原始记录保留在 [`PROGRESS_BOOK.md`](docs/PROGRESS_BOOK.md)，当前检查点与验收证据保留在 [`PROJECT_STATUS.md`](docs/PROJECT_STATUS.md)，恢复上下文见 [`PROJECT_MEMORY.md`](docs/PROJECT_MEMORY.md)。这些账本是历史记录，不应被当作待重新执行的任务列表。

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
