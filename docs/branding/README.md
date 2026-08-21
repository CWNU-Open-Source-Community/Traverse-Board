# 品牌迁移 / Branding Migration

Status: **Accepted for the first major product-brand migration.**

项目方已批准 **Traverse Board · 针路簿** 作为产品展示品牌，并批准
`Traverse-Board` 作为 GitHub 仓库 slug。此次迁移只改变产品展示与规范仓库地址；
`.prayu`、`cyberagent`、协议、持久化 schema、安装 identity 等兼容接口继续保留。

The owner has approved **Traverse Board · 针路簿** as the product display brand and
`Traverse-Board` as the GitHub repository slug. This migration changes presentation and
canonical repository URLs while preserving compatibility identifiers.

## 文件 / Files

| 文档 | 用途 |
|---|---|
| [Theme and Naming v2](theme-and-naming-v2.md) | 已接受品牌、展示规则、34 项词汇表和 v1 修订说明 |
| [Machine-readable naming map](naming-map.v2.yaml) | 状态明确的机器可读展示映射；不授权兼容标识迁移 |
| [Compatibility and migration matrix](compatibility-matrix.md) | 展示名、仓库地址、协议、数据目录、包身份和发布件的迁移边界 |
| [ADR 0124](../adr/0124-traverse-board-branding-migration.md) | 品牌决策、仓库 slug、后果与兼容边界 |

## 当前结论 / Current Decision

- 历史航海主题与当前 Go-owned、可恢复、可审计架构相符。
- 项目方确认本项目是方向不同的开源 Agent 工作台，接受市场上存在同名商业软件这一
  已知残余风险；该判断是项目命名决策，不构成商标法律意见。
- GitHub 仓库名不能包含空格、中点或中文，因此 slug 使用 `Traverse-Board`；完整双语名
  用于 README、仓库描述和产品界面。
- v2 使用 **Change Track · 变更航迹** 表示 Diff/Code Journey/Handoff trail，避免与
  已有 `run_wake_intent.v1`、Run Wake worker 和 Agent `wake` 控制消息冲突。
- **Kamal · 牵星板** 只保留为未来 Run-state summary 概念；当前没有独立的
  `Trusted Run Fix` 领域对象或 authority source。
- “Rust Analyzer”必须写成“Rust-implemented deterministic WASI Analyzer guest”，
  避免与 `rust-analyzer` 语言服务器混淆。

## 授权边界 / Authorization Boundary

ADR 0124 授权展示品牌与 GitHub slug 迁移，但不授权发布版本、迁移用户数据、替换
兼容标识或执行全局字符串替换。实施变更仍须经过精确 diff、验证和可回退审查。

ADR 0124 authorizes the display-brand and GitHub-slug migration. It does not authorize a
release, user-data migration, compatibility-ID replacement, or global search-and-replace.
