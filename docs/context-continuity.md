# Project Instructions, Long-Term Memory, And Session Continuity

状态 / Status: implemented in schema v114

协议 / Protocols: `project_instruction_snapshot.v1`, `context_memory.v1`,
`continuity_snapshot.v1`, `session_continuity_node.v1`, `session_tree.v1`

## 中文说明

### 一条不可跨越的边界

“长期存在”不代表“更高优先级”。Prayu 按以下顺序解释上下文：

1. 系统与操作者当前输入；
2. Go 运行时安全策略、Scope、Policy、预算和当前权限快照；
3. Run 的显式模式、配置与用户选择；
4. 固定到 Run 的项目指令与显式 user/project memory；
5. 普通仓库文件、历史消息和工具输出。

项目文本与记忆只能提供工作流、格式、偏好、事实和验证建议。它们不能授予或恢复
Tool、Network、Secret、Debug、Plugin、Hook、MCP、审批、Credential、进程、终端租约、
执行档位或 Policy override。模型声称“恢复权限”不产生任何 Go authority。

### 层级项目指令

发现器只在已注册 Workspace 边界内读取：

- 每一级目录中的 `AGENTS.md` 与 `CLAUDE.md`；
- 每一级目录中的 `.prayu/instructions.md`；
- 每一级目录下 `.prayu/rules/**/*.md` 的稳定字典序集合；
- Workspace 根的 `.prayu/instructions.ignore`，用于排除规范的相对路径 glob。

顺序从 Workspace 根到目标所在目录，越近的目录优先级越高；同一级依次为
`AGENTS.md`、`CLAUDE.md`、Prayu instructions、排序后的 rules。每个 source 保存相对
path、content SHA-256、scope、depth、precedence、加载时间、trust、适用 target、
`why_effective`、脱敏状态和全部 false 的授权字段。冲突保留双方并解释最近目录获胜，
不会静默删除低优先级来源。

边界为最多 64 个文件、单文件 64 KiB、总计 256 KiB、目标/规则深度 32、ignore
16 KiB/128 条。绝对路径、`..` 逃逸、NUL、无效 UTF-8、空文件、特殊文件、symlink、
junction/reparse、并发修改、超限和非法 ignore 均 fail closed。Windows 使用大小写不敏感
的规范路径比较；Linux/macOS 保留大小写语义。内容在 hash 和交付前经过 Secret 脱敏。

Run 创建时把完整快照和 fingerprint 固定在 `RunConfig`，并在 schema v114 的不可变
revision ledger 中保存。磁盘变化只会让检查视图显示 `stale` 和 added/changed/removed/
order diff；运行中的 Run 仍使用旧快照。只有操作者以旧 fingerprint 为乐观并发条件进行
显式确认旧 pinned fingerprint 与已查看的 live fingerprint，才会追加新 revision。

```powershell
cyberagent context instructions --workspace demo --target internal/parser/input.go --json
cyberagent run create --workspace demo --goal "review parser" --profile review
cyberagent context instructions --run <run-id> --json
cyberagent context instructions --run <run-id> --confirm `
  --expected-fingerprint <pinned-sha256> `
  --expected-live-fingerprint <reviewed-live-sha256> --json
```

Desktop 的 **Context / 上下文** 页显示固定/磁盘 fingerprint、为何生效、适用范围、
优先级冲突和漂移确认。OpenAPI 等价入口是：

- `GET /api/v1/runs/{run_id}/project-instructions?target_path=...`
- `POST /api/v1/runs/{run_id}/project-instructions/refresh`

### 显式 user/project memory

长期记忆没有自动写入路径。只有 CLI、Desktop 或带独立 control bearer 的 HTTP 操作者
动作可以创建/编辑/启用/禁用/删除。模型回复、Tool result、仓库文件和 compaction 不会
静默提炼为长期记忆。来源只允许 `operator_explicit` 或显式导入类别。

每条记录保存 scope、title、脱敏后的 content/hash、status、来源引用、排序去重引用、
可选 retention、创建/更新操作者、版本和时间。user scope 固定为 `local-user`；project
scope 固定到 Workspace ID。写操作使用 optimistic `expected_version`，防止两个界面覆盖。
保留期不得早于创建时间，也不得超过十年；到期或 disabled 的记录不进入新 prompt。

```powershell
cyberagent context memory create --scope project --workspace demo `
  --title "Test convention" --content "Run unit tests before packaging" --retention 720h
cyberagent context memory list --scope project --workspace demo --all --json
cyberagent context memory edit <memory-id> --version 1 --content "Run unit and API tests" `
  --reference docs/testing.md
cyberagent context memory disable <memory-id> --version 2
cyberagent context memory enable <memory-id> --version 3
cyberagent context memory export --scope project --workspace demo --all
cyberagent context memory delete <memory-id> --version 4
```

Secret-like content 默认拒绝；只有 `--redact-sensitive` 或 Desktop 的显式脱敏选项允许
保存脱敏结果。`.env`、credentials、private keys、Secret、stdin、terminal input、
keystrokes 等敏感来源/引用始终拒绝，不能用脱敏选项绕过来源边界。正文上限 16 KiB，
引用最多 32 条、每条 512 bytes。

#### 删除与隐私

`delete` 是物理删除，不是 tombstone；成功结果固定 `recoverable=false`。之后的列表、读取、
export 和新 prompt 均无法返回正文。已存在的不可变 checkpoint 只保存 memory ID、版本和
content hash，不保存 memory 正文；树会把缺失引用标为 deleted。数据库文件、用户自行
制作的 export、备份、日志转储或已经发送到外部 Provider 的旧 prompt 不由这次 SQLite
删除反向擦除，操作者需按自己的备份/Provider 保留策略处理。删除 API 需要 exact ID、
current version、control bearer 和明确的 DELETE body。

### Checkpoint、Fork、Resume 与 session tree

每个绑定 Workspace 的新 Run 有一个 root continuity node。显式 checkpoint 捕获一个有界、
脱敏的 `continuity_snapshot.v1`：最新 compaction summary、最多 20 条近期消息及来源、最多
200 条当前 active memory 引用、项目配置/指令 fingerprint、Git branch/full HEAD，以及
明确列出的继承项。快照 fingerprint 不包含加载时间，因此相同语义可稳定比较。

```powershell
cyberagent session checkpoint <run-id> --title "Parser reviewed" --summary "Ready for tests" --json
cyberagent session tree <session-id> --json
cyberagent session fork <continuity-node-id> --goal "Try alternate implementation" --json
cyberagent session resume <continuity-node-id> --json
```

Fork/Resume 原子创建新的 Mission、Run、Session 和 branch marker，并把选定 snapshot 固定到
新 Run。它们继承的只有有界历史上下文、有效 memory 引用、固定项目配置/指令和任务预算/
模式；明确不继承 approvals、capability grants、credentials、debug sessions、execution
leases、network authorization、processes、terminal leases 或 execution profiles。新 Run
从普通 `created` 状态和默认非授权执行快照开始。

树还投影 compaction、关键决策 Note、Artifact 和 Delivery checkpoint，并标注 memory
disabled/expired/deleted/version drift、Git branch/HEAD drift。衍生节点仅用于浏览，不能作为
Fork/Resume authority。Desktop 支持检查点、分支动作和两个节点的 fingerprint/Run/Session/
状态以及 project config/instructions 指纹和 Git branch/HEAD 比较；CLI `--json` 与 OpenAPI
提供等价机器导出。

### 威胁模型

| 威胁 | 控制 |
|---|---|
| 恶意 `AGENTS.md` 声称自己是 system 或要求联网/读 Secret | trust 固定为 untrusted，Go 类型中的授权位必须全 false，安全策略优先 |
| 路径逃逸、symlink/junction 替换、特殊文件或超大 rules tree | exact-root、逐级 `Lstat`/resolve、类型/深度/大小/数量限制，失败不降级 |
| 读取期间文件被替换 | 读取前后 metadata 比较并二次读取字节比较；任何差异拒绝整个发现 |
| 文件修改静默影响 active Run | 创建时不可变快照；refresh 同时绑定旧 pinned 与已查看的 live fingerprint、diff 和显式 confirm |
| Prompt/Tool 自动种植长期记忆 | store service 要求显式 operator actor；model/tool/system actor 被拒绝 |
| 凭据、终端输入或敏感文件被长期保存 | 内容 Secret 检测与默认拒绝；敏感 source/reference 永久拒绝 |
| 过期/已删除记忆继续影响分支 | 新 prompt 只选 active/unexpired；树显示 drift；checkpoint 只存引用/hash |
| Fork 恢复旧审批、网络或终端控制 | snapshot authority 全 false；新 Run 重新创建所有权限/lease/profile 状态 |
| 数据库重启把进程权限复活 | continuity 是数据，不是 runtime handle；进程、bearer、credential 不持久化 |
| 并发编辑覆盖 memory 或 refresh | expected version/fingerprint 乐观并发检查，冲突要求重新读取 |

### 迁移和恢复

schema v114 在一个校验和事务中创建 `context_memories`、
`run_instruction_snapshots`、`session_continuity_nodes` 和索引/不可变触发器；不改写既有
Session 消息、Run Note 或 compaction。旧 Run 没有固定项目指令时可先只读检查 live diff，
再显式确认创建 revision 1。所有新对象从 SQLite 重启后可读取，但 continuity 永远不能恢复
进程内 capability。迁移测试验证 v113 数据保留与 v114 重开。

## English Guide

### Non-authority boundary

Durability does not imply precedence or authority. System/operator input and Go-owned safety
policy remain above explicit Run choices; pinned project instructions and memories remain below
those controls and above ordinary repository/tool evidence. Their Go representations cannot grant
tools, network, secrets, Debug, plugins, hooks, MCP, approvals, credentials, processes, terminal
leases, execution profiles, or policy overrides.

### Hierarchical discovery and immutable Run snapshots

Within the exact registered Workspace, Prayu discovers `AGENTS.md`, `CLAUDE.md`,
`.prayu/instructions.md`, and sorted `.prayu/rules/**/*.md` from root to the target directory.
Nearer directories have higher precedence. Each source retains its canonical relative path,
content digest, scope, depth, precedence, load time, trust class, target applicability,
why-effective explanation, redaction state, and an all-false authority projection. Conflicts remain
visible rather than silently dropping the lower source.

Discovery fails closed on escapes, invalid UTF-8/NUL, special files, symlinks/reparse points,
concurrent modification, malformed ignore rules, or the 64-file/64-KiB-each/256-KiB-total/32-depth
bounds. Run creation pins the complete snapshot and fingerprint. Later disk drift produces an
explainable diff but cannot change an active Run; an operator must confirm against the exact prior
fingerprint and the reviewed live fingerprint to append a new immutable revision.

### Explicit memory and deletion

User/project memory is created only by an explicit operator surface. There is no automatic
conversation, file, model, or tool extraction. Records carry provenance, optimistic version,
status, optional retention, references, redaction metadata, and audit timestamps. Disabled or
expired records do not enter new prompts. Sensitive content is rejected unless explicit redaction
is requested; sensitive sources such as credentials, private keys, `.env`, stdin, and terminal
input are always rejected.

Deletion physically removes the exact current version and is reported as unrecoverable. Existing
immutable checkpoints contain only the memory identity/version/digest, never its body, and report a
deleted-reference warning afterward. Independent exports, backups, and text previously transmitted
to a Provider remain subject to their own retention policy.

### Continuity tree

Checkpoints capture bounded redacted summaries, provenance-bearing recent messages, active memory
references, pinned configuration fingerprints, and exact Git identity. Fork and Resume create a new
Mission/Run/Session atomically from that snapshot. They never inherit approvals, capability grants,
credentials, Debug sessions, execution or terminal leases, network authorization, processes, or
execution profiles. The browsing tree combines stored nodes with compaction, decision, Artifact,
and Delivery projections and reports memory/Git drift. Desktop branch comparison includes context,
project-config, project-instruction, and Git identities; CLI JSON and OpenAPI expose the same
non-authorizing state.

### API surface

- Read: `GET /memories`, `/memories/export`, `/memories/{id}`,
  `/runs/{run_id}/project-instructions`, `/sessions/{session_id}/tree`.
- Control: `POST /memories`, `PATCH|DELETE /memories/{id}`,
  `POST /runs/{run_id}/project-instructions/refresh`,
  `POST /runs/{run_id}/continuity-checkpoints`, and
  `POST /continuity-nodes/{node_id}/fork|resume`.
- All paths live under `/api/v1`; mutations require the distinct control bearer, strict JSON,
  bounded bodies, duplicate/unknown-field rejection, and server-owned operator provenance.

See [ADR 0115](adr/0115-non-authorizing-durable-context-continuity.md) for the design decision and
[http-api.md](http-api.md) for the common authentication/envelope contract.
