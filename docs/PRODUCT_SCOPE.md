# Traverse Board · 针路簿产品范围 / Product Scope

更新日期 / Last updated: 2026-08-25

本文是当前产品范围的权威说明。历史任务书、进度百分比或兼容命令与本文冲突时，以本文、当前代码和相关 ADR 为准。

This document is the authority for current product scope. If a historical task, percentage, or compatibility command conflicts with it, this document, the current code, and the relevant ADR take precedence.

## 当前核心 / Active Core

针路簿当前开发的是通用、本地优先的 AI Agent Harness 与 Code Agent 产品面：

- Thread、Run、Step/Tool Item、Run-local Session、检查点、恢复、取消、预算和事件流；
- Provider 路由、资格校验、公开流式回复、上下文压缩和结构化记忆；
- Plan/Delivery、Plan item（计划项；兼容 identity 为 `WorkItem`/`work_item`）、备注、受控 child、可交付 child 的隔离 Worktree/交付复核/本地顺序合并，以及只读 Fan-out；
- Workspace、Repository、Diff、模型可调用的有界读取/搜索、人工审查的文件变更、Run-owned Drydock、Standard Code 原子预设、稳定 hunk、审批式 stash/rebase/cherry-pick/bisect、产品受管 worktree、验证、Journey 和 Handoff；
- 默认关闭、Run allowlist 约束的公网 Web Search/Fetch/Citation 与不可变来源快照；
- Tool Gateway、Skill、Policy、Scope、Approval、Capability 和权限档位；
- Artifact、Finding、Evidence、Report、SARIF 和 Live Activity；
- Windows/macOS Desktop、React/Vite Thread 工作台、CLI 与 loopback HTTP/OpenAPI；
- Go 主控的 Browser、Git/GitHub Review、multi-agent 与 Sandbox 能力，以及可选的 MCP、Plugin/Hook、Skill、Rust/WASI Analyzer 扩展合同。

The active product is a general-purpose, local-first AI Agent Harness and Code Agent workflow covering Thread-first resumable execution, models and context, planning, bounded model-callable Workspace reads and reviewed mutations, default-off Run-allowlisted public Web Search/Fetch/Citation with immutable source snapshots, Run-owned Drydocks, the atomic Standard Code preset, approval-gated stable hunks/stash/rebase/cherry-pick/bisect, product-managed worktrees, isolated deliverable-child worktrees and local merge review, permissions, audit/reporting, and shared Desktop/React, CLI, and loopback HTTP/OpenAPI clients.

## 支持等级 / Support tiers

实现存在不等于同一产品承诺。pre-1.0 的完整清单、进入标准、移除窗口和非授权边界见
[ADR 0135](adr/0135-pre-1-0-product-convergence.md) 与
[Surface inventory](convergence/surface-inventory.md)。摘要如下：

| Tier | 当前范围 / Current scope |
|---|---|
| Active | Windows/macOS Desktop、React Thread 工作台、`cyberagent` CLI、loopback HTTP/OpenAPI，以及通过同一 Go Application 合同进入的核心能力 |
| Maintenance-only | Bubble Tea TUI、headless event/CI 命令、旧 Run/Session 诊断视图；只做安全、兼容、数据丢失和严重缺陷修复 |
| Extension-only | MCP、Plugin/Hook、第三方 Skill、Rust/WASI Analyzer、CTF/cyber add-on skeleton；扩展合同受支持，但不是核心发布依赖 |
| Deferred | hosted/multi-tenant、Linux/mobile/editor 原生全客户端、公开扩展市场与自主攻防包 |

Backends such as Docker/AppContainer, Browser/CDP, Git/GitHub Review, and
multi-agent coordination keep their active security and recovery obligations but do
not count as additional user surfaces. A tier label never grants runtime authority.

## 可选附加范围 / Optional Add-on Scope

以下方向不进入当前活跃路线图，也不作为核心产品发布条件：

- CTF 自动解题与题型专用 Planner；
- 自动化渗透、漏洞利用、横向移动、提权或弱口令爆破；
- Nmap、SQLMap、Nuclei、Burp-like 改包等专项攻防工具包；
- CTF 专用浏览器策略、Payload 生成和自动 Writeup；
- 面向漏洞赏金或真实公网目标的自主攻击工作流。

These capabilities are not on the active roadmap and are not release criteria for the core product: automated CTF solving, penetration-testing chains, offensive tool packs, exploit generation, or autonomous workflows against real public targets.

仓库中现有的 `ctf` 命令、`cyber` 标签和早期模式字段属于兼容骨架或通用安全分析边界。它们不证明产品已经实现上述能力，也不会因本次文档调整而删除或改变协议。

Existing `ctf` commands, `cyber` labels, and early mode fields are compatibility scaffolds or generic security-analysis boundaries. They do not claim the capabilities above, and this documentation-only scope change does not remove or mutate their protocols.

## 保留的通用扩展接口 / Retained Generic Extension Seams

未来若重新启动 CTF 或专项安全方向，应作为独立、可卸载的扩展接入以下通用接口，而不是复制一套 Agent 运行时：

| 接口 | 扩展用途 | 不授予的能力 |
|---|---|---|
| `llm.Provider` + Model Harness | 模型接入、资格校验和路由 | 不授予 Tool、网络或文件权限 |
| `tools.Tool` + JSON Schema | 类型化工具提案与结果 | 不绕过 Policy、Scope、Approval 或预算 |
| Skill package/Registry | 领域提示、工作流与声明依赖 | 内容默认不可信，不在导入/安装时执行命令 |
| Analyzer JSON/WASI boundary | 确定性文件、归档或二进制分析 | Rust 不管理 Agent、Session、密钥或宿主进程 |
| `sandbox.Runner` | 隔离执行后端 | 需要独立 OS/容器与网络隔离证据后才可启用 |
| Finding/Evidence/Report | 结构化结果、复核与 SARIF | 模型断言不等于验证事实 |

A future security add-on must remain removable and must reuse these generic seams. It may not create a second control plane.

## 不可变安全原则 / Non-negotiable Safety Principles

- Go 是唯一控制平面，调用方向保持 `TypeScript -> Go -> Rust/Docker/LLM`。
- 目标必须由操作者明确授权并限制 Scope；默认禁止真实公网攻击。
- 模型、网页、README、Issue、日志、工具输出和 Skill 内容均是不可信证据。
- 文件写入、命令、网络、浏览器、终端和容器分别授权，不能用一个“Cyber 模式”总开关替代。
- 高风险操作需要独立 Policy、审批、预算、租约、审计和可恢复失败语义。
- Provider 私有 thinking、凭证、原始 Prompt 和工具原始输出不进入公开活动或持久化投影。
- Worktree 只提供来源分离和可恢复所有权；执行隔离仍需独立运行时证据，无法证明归属或含用户改动的目录不得自动删除。

## 重新启动附加功能的条件 / Reactivation Criteria

只有在维护者显式重新批准路线图后，附加能力才能进入实现。首个实现 PR 之前至少需要：

1. 独立 ADR 和威胁模型；
2. 明确的目标/排除 Scope 与速率、预算、深度上限；
3. 经过验证的容器或 OS 网络隔离，而非只依靠 CDP/应用层过滤；
4. 不读取宿主凭证、不挂载个人浏览器 Profile 的一次性环境；
5. 正常、拒绝、超时、取消、重启恢复和清理测试；
6. 明确的人工验收和合法授权说明。

Until those gates are satisfied, CTF-specific plans remain documentation history only.
