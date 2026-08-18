# ADR 0113: 外部 Skill 模式账本与生成候选人工审查

Date: 2026-08-18

## Status

Accepted for the local external-Skill Registry and the Code Deliver root
Supervisor. Candidate review/import is operator-only through the CLI; no Web,
HTTP, model-review, automatic installation, or automatic selection entry is
introduced.

## 背景 / Context

`skill.v1` 已能声明 Profile、Surface、Phase、Role 与调用策略，但 schema v69 的外部
安装表只保存 Profile 和工具依赖。若导入 mode-aware 包后丢弃这些字段，回读、重放与
后续选择便无法证明它仍是操作者看过的同一份意图。另一方面，模型可以协助起草复用
工作流，但模型生成的 `SKILL.md` 本身是不可信指令，不能因生成成功而进入 Registry。

## 决策 / Decision

### 1. schema v111 保存完整模式元数据

`skill_package_installations` 新增规范化的 `surfaces_json`、`phases_json`、`roles_json`
以及 `user_invocable`、`model_invocable`、`explicit_only`。Go、SQLite、CLI、Desktop
预览和 HTTP/OpenAPI 投影使用同一字段集合。新 mode-aware 安装意图使用
`skill_package_installation_intent.v2`，将全部模式字段绑定到请求指纹；已有 legacy
行继续使用空数组/全 false 的原始存储和精确 v1 指纹，回读时才投影保守的
`user=true, model=false, explicit=true` 有效策略。

安装仍然只写入惰性、不可信 Registry，不选择、不注入正文、不给工具或网络能力。
schema v70 的外部 selection 是一次性不可变集合，尚不能像 schema v110 的内置
上下文那样按阶段记录空子集。因此 mode-aware 外部包只有在 root（以及被指定时的
Specialist）同时支持 Plan 与 Deliver 才能进入当前 selection；Plan-only 或
Deliver-only 包可以安装和检查，但选择会失败关闭，直至外部上下文账本获得独立的
phase-subset 升级。

### 2. 生成只创建不可信候选

新增显式用户 Skill `run-skill-generator`。只有 Code Surface、Deliver Phase、root
角色实际收到该 Skill 正文时，Provider 才能看到 `skill_candidate_propose`。工具接受
严格、无未知/重复字段、规范排序的 mode-aware manifest 与最多 4096 字节 UTF-8
Markdown；Cyber、内置名称覆盖、秘密模式、非法控制字符和不兼容工具依赖均拒绝。

成功调用只创建 `skill_candidate.v1`：候选绑定当前 Run/Session/Workspace/root、真实
`run_tool_calls` invocation、内容/包摘要和操作幂等键。候选正文以有界不可信数据存入
SQLite，普通列表和工具结果只显示元数据；操作者必须显式使用 `--show-content` 才能
看到带不可信分隔标记的正文。每个 Run 最多 4 个、全 Registry 最多 64 个。

### 3. 状态由只追加事实推导

schema v112 建立三个不可变表，而不保存可改写的 status：

```text
proposed -> approved -> imported
         -> rejected
```

- `skill_candidates`：模型提案事实；不安装、不选择；
- `skill_candidate_reviews`：一个候选最多一个 exact-fingerprint 人工决定；拒绝必须有
  原因，`agent|llm|model|skill|supervisor|run_supervisor` 等身份不能充当 reviewer；
- `skill_candidate_imports`：只接受 approved review、同一 candidate/review 指纹和已完成
  的精确安装收据。

拒绝是终态，不能改成批准。导入还要求第二次 `--confirm-untrusted-skill`，以确定性方式
重建 ZIP 后复用现有安装服务；崩溃发生在安装与 import receipt 之间时，同一操作键会
回放安装并补齐收据。导入后仍未被 Run 选择，选择需要原有独立确认。

### 4. 人工流程

操作者使用 `skill candidates` 或 `skill candidate list/show` 检查候选，并把界面显示的
完整 `candidate_fingerprint` 原样提交给 `approve|reject|import`。审批操作与导入操作各
自需要独立稳定幂等键。模型、Skill 正文、仓库文件和工具输出没有审查或导入入口。

## 后果 / Consequences

- 外部安装回读和重放不再丢失模式信息，legacy 数据与指纹保持稳定。
- 内置 Registry 增至十二项；`run-skill-generator` 是 root/Code/Deliver/explicit-only。
- “生成”“审批”“安装”“选择”成为四个可审计且互不替代的事实。
- 当前没有通用 Skill 自动生成器、自动审查器、Marketplace 发布或 Web 审批 UI；这些
  能力若新增，必须另行设计权限、来源证明和不可信内容展示边界。
