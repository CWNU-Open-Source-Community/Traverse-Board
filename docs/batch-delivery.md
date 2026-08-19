# 可交付多代理批次 / Deliverable Multi-Agent Batches

`batch-delivery.v1` 把已经审批、admission 的核心 child DAG 转换为可审计的本地代码交付：每个 child 拥有独立 Git worktree/branch、缩小工具 profile、generation lease 和持久邮箱；提交经过独立复核后，才可在另一个 integration worktree 中按依赖顺序合并。

`batch-delivery.v1` turns an already reviewed and admitted core-child DAG into auditable local code deliveries. Each child receives its own Git worktree and branch, a narrowed tool profile, a generation lease, and a durable mailbox. Accepted commits are applied in dependency order only after independent review, inside a separate integration worktree.

## 与既有 Specialist 的关系 / Relationship to existing Specialists

现有 `SpecialistRunner` 继续是明确的 no-tool 分析运行时。schema v118 没有把 Shell、文件或 Git 能力塞回旧运行时，也没有改变 1/2/4/6 档只读 fan-out。可交付 child 使用独立的 Application 合同，并且只能绑定以下既有事实：

- 正在运行的 Run 与其注册 Workspace；
- 已由操作者批准的 `child_task_proposal.v1`；
- 已 admission 的 `core` assignments，而不是 readonly fan-out；
- 与 admission 完全一致的 task 数量、ordinal、DAG、预算和 expected artifacts；
- 干净、非 detached、branch-backed 的本地 Git source。

The existing `SpecialistRunner` remains a no-tool analysis runtime. Deliverable children use a separate Application contract and can bind only to the active Run, its registered Workspace, an operator-approved `child_task_proposal.v1`, admitted core assignments, an unchanged DAG/budget/artifact contract, and a clean branch-backed local Git source.

## 批次合同 / Batch contract

一个规范化 `batch-delivery.v1` 包含 1–2 个 task。两个 task 可以没有依赖并行工作，也可以由 ordinal 2 显式依赖 ordinal 1。任务声明：

- 1–32 个互不重叠的 file/directory ownership hints；
- 与原 admission 相同的 turn/token/timeout 预算，timeout 最大 30 分钟；
- 1–16 个验证项，其中 `git_diff_check` 必须存在；
- 最多 8 个 expected artifacts；
- 全局 clean、independent-review、all-validations 三个门禁必须全部为 true；
- 最多 512 个 changed files、16 MiB 完整 diff，调用链最多 256 项。

目录所有权覆盖其后代；不同 task 的 file/directory hint 只要相等或祖先/后代相交就会在创建前拒绝。依赖图在 Go 中做循环检测。以下示例中的预算和 artifacts 必须与被引用 proposal 完全一致：

```json
{
  "version": "batch-delivery.v1",
  "tasks": [
    {
      "ordinal": 1,
      "ownership_hints": [
        { "path": "internal/parser", "kind": "directory" }
      ],
      "dependency_ordinals": [],
      "budget": {
        "turn_limit": 4,
        "token_limit": 12000,
        "timeout_millis": 900000
      },
      "validations": [
        { "id": "diff", "kind": "git_diff_check", "scope": "." }
      ],
      "expected_artifacts": [
        { "path_hint": "internal/parser", "kind": "code" }
      ]
    },
    {
      "ordinal": 2,
      "ownership_hints": [
        { "path": "internal/renderer", "kind": "directory" }
      ],
      "dependency_ordinals": [],
      "budget": {
        "turn_limit": 4,
        "token_limit": 12000,
        "timeout_millis": 900000
      },
      "validations": [
        { "id": "diff", "kind": "git_diff_check", "scope": "." }
      ],
      "expected_artifacts": [
        { "path_hint": "internal/renderer", "kind": "code" }
      ]
    }
  ],
  "contract": {
    "require_clean": true,
    "require_independent_review": true,
    "require_all_validations": true,
    "max_changed_files": 128,
    "max_diff_bytes": 8388608
  }
}
```

## Worktree 与 authority / Worktrees and authority

确认 Prepare 后，Go 在 source Workspace 的确定性 sibling 目录创建每个 child 的 branch-backed worktree。HTTP 不接受绝对目标路径，也不在普通 DTO 中返回 worktree root。每份 authority 固定：

- plan、task ordinal、admitted Agent ID；
- generation（初始为 1，最多 8）；
- child branch、base commit、lease expiry；
- `batch-delivery-tools.v1` 的精确指纹；
- 256-bit 随机 owner token。

owner token 仅在首次 Prepare 或 generation 轮换响应中明文返回；SQLite 只保存 SHA-256 digest，普通 list/detail/Desktop 投影不包含 token 或 digest。Prepare 的幂等 replay 不会恢复已经遗失的明文 token；操作者必须用 exact expected generation 做 CAS 轮换，旧 generation/token 随即失效。token 不是 bearer 的“万能权限”：每次工具调用仍重新检查 Agent、plan 状态、generation、digest、lease、依赖、worktree identity、工具 profile 和 ownership。

## 缩小工具集 / Narrowed tool profile

默认 profile 精确允许：

| 能力 | 边界 |
|---|---|
| `workspace_list` | 只能列出 owned directory 或其祖先视图；结果再次过滤到 owned Scope |
| `workspace_read` | 只能读取 owned file/descendant，沿用 UTF-8、size、hidden/ignore、link/reparse 与 casing 防护 |
| `workspace_glob` / `workspace_grep` | 搜索结果必须全部落在 owned Scope，游标绑定 child worktree identity |
| `workspace_change` | 只创建 owned Scope 内 create/replace 提案；不能 move/delete |
| `workspace_apply` | 仅应用本 child 创建并由 Go 内部批准、hash/CAS 仍匹配的提案 |
| `git_status` / `git_diff` | 只查看当前 child branch/worktree；不读取 remote/config/credential |
| `git_commit` | 只暂存 owned changed paths，拒绝 delete/rename/copy，固定 author/email、禁用 GPG/hook 路径 |

以下字段必须保持 false：`workspace_delete`、`shell`、`process`、`network`、`credentials`、`debug_terminal`、`approvals`、`spawn_children`。一个 child 不能为自己扩充 profile，也不能用 repo 内容、邮箱消息或模型输出制造 authority。

所有 batch Git 写操作把 `core.hooksPath` 与 `core.attributesFile` 指向不可执行 sentinel，忽略 system/global attributes，并拒绝 repository-local clean/smudge/process、diff command/textconv 与 merge driver。创建、恢复、删除和完成前还会核对 worktree 与 source 的 common Git directory；symlink/reparse root 或替换成另一个同 branch/OID 的仓库均不能通过。

每次成功工具调用都会追加一条不可变、operation-keyed 的 mailbox audit，并更新心跳；重放相同意图返回原结果，不同意图复用 operation key 会冲突。

## 邮箱与状态 / Mailbox and lifecycle

邮箱消息按 `(plan, ordinal, generation, sequence)` 单调追加，类型为：

```text
dispatch -> ack -> progress/question/evidence -> ready_for_review
                                            -> changes_requested -> retry generation
                                            -> accepted -> merged
                         cancellation/error -> aborted
```

消息保存 actor、bounded summary、evidence references、operation digest 和 request fingerprint。消息、receipt、review、generation rotation 与 merge step 都在 SQLite 中持久化；相同操作重放不会重复追加。stale generation、过期 lease、未满足 dependency、错误 actor 或重复序列会失败关闭。

## 提交与崩溃窗口 / Commit and crash recovery

child 不能用“我完成了”替代提交。`git_commit` 先把 intent 写入不可变邮箱，再运行固定 Git mutation；成功必须满足：

1. branch/base/worktree identity 仍精确匹配；
2. changed path 全部属于 task；
3. 不存在 delete、rename 或 copy；
4. 暂存 diff 通过 `git diff --cached --check`；
5. commit 使用固定 author/email 与单行、最多 256 字符的消息；
6. 提交后 worktree clean。

若进程在 Git commit 后、SQLite completion audit 前崩溃，启动 reconciliation 只会接纳一个以 intent prior HEAD 为唯一父节点的直属、非 merge commit；还会复核 author/email/message、ownership、无 delete/rename/copy 与 clean HEAD。多个外部提交、merge commit、dirty state 或 metadata 漂移不会被误认成恢复成功。

## 交付收据与独立复核 / Receipt and independent review

Submit 每次重新从 `merge-base(base, HEAD)` 检查完整提交交付，而不是相信作者摘要；声明验证结束后还会再次测量 branch、HEAD、diff 与 clean 状态，只有前后事实完全一致才写 receipt。receipt 固定：

- base/head full object ID、完整 diff SHA-256 与 diffstat；
- 按路径排序的 changed files；
- 由完整 function-context diff 推导的 call-chain SHA-256；
- 每项验证的 kind/scope/exit code/output digest/duration/time；
- bounded evidence references 和 known limitations；
- generation、operation digest 与 request fingerprint。

dirty/uncommitted worktree、非 base 后代、越界路径、超限 diff、失败验证或缺少 evidence/limitations 都不能进入 review。

Review 会再次读取 exact receipt head，重新计算完整 diff/call-chain，并重新运行声明验证；验证返回后同样要求 worktree 与 receipt 的 exact 状态仍然一致。`accepted` 要求 Reviewer 明确确认 `full_diff_reviewed`、`call_chain_reviewed` 与 `tests_reviewed`；Desktop 在 summary 和独立复核勾选完成前禁用按钮。作者 summary 只作线索，不是证据。`changes_requested` 会持久化原因；继续修改需由操作者按 generation retry 合同轮换 owner。

## 验证执行边界 / Validation execution boundary

`git_diff_check` 只执行固定 `git diff --check <base>...HEAD --`，默认可用。`go_test` 和 `npm_test` 会执行 child 提交的仓库代码，因而默认拒绝，甚至在 Prepare 之前就失败关闭。

只有 API 进程持有 control token，并同时满足以下启动条件，且每次执行时关联 Run 仍是 `running`、当前权限仍是 `full_access`（或显式更高的 `debug`），才会接纳这两种验证：

```powershell
cyberagent api serve `
  --enable-permission-control `
  --enable-danger-full-access `
  --enable-batch-validation-execution
```

Desktop 还必须单独启用 `--enable-batch-delivery-control`；仅因其他功能创建了通用 control token 不会开放 Prepare/Review/Merge/Cancel/Reconcile。`--operator-preview` 会显式包含这项本地批量控制能力，但不会自动开启宿主验证。

验证使用固定 argv、10 分钟超时、关闭 stdin、Windows Job Object / Unix inherited process-group 终止与回收、临时 HOME/cache、禁用 Git credential prompt/global config、`GOPROXY=off`、`GOSUMDB=off` 与 npm offline/audit-off 配置。Go 测试固定加入 `-count=1`，避免把缓存成功误作本次执行证据；stdout/stderr 正文不会进入 error、SQLite 或 read DTO，只保留 exit code、完整观察流 SHA-256、字节计数与时间收据。每个非 Git 验证开始前都会重新读取当前 Run 权限，receipt/review/merge 的持久化还会再次检查 Run/lease，因此 Prepare 后降权、暂停、终止或执行期间过期都会 fail closed。

child-authored code 仍在宿主进程中运行，能够尝试读取宿主可见资源或发起网络访问；上述环境与生命周期约束不是 OS filesystem/network sandbox。在 POSIX 上，恶意代码主动建立新 session 并脱离 inherited process group 仍是显式宿主执行的残余风险，不会被误写成可证明的完整树沙箱。对不可信仓库，需要真实隔离证据时应只声明 `git_diff_check`，或在未来接入单独审阅的 Docker/network-none 验证后端。

## 顺序合并 / Ordered local merge queue

Merge 只接受每个 task 当前 generation 的 `accepted` receipt/review，且顺序必须包含所有 ordinal、满足 DAG。创建队列前会检查 source HEAD：

- 与原 base 相同：直接继续；
- 已漂移：第一次返回冲突并报告 drift，只有操作者查看最新 base 后显式提交 `confirm_replay: true` 才继续；
- receipt changed-files 互相重叠：直接 block，不尝试自动解决。

队列在独立 integration worktree/branch 上逐项运行。每一步先验证 pre-head，执行 `git merge --no-ff --no-commit <child-head>`，对文本冲突立即 abort/reset 该 integration step；成功后创建固定 merge commit，再对合并前缀中所有 task 的声明验证做累积重跑，防止后来的交付破坏较早 task 的合同。验证返回后重新证明 source branch/head/clean、integration 的 common repository/branch/head/clean/确定性 merge commit，以及全部 accepted child 的完整 receipt；任何状态漂移都会 block 并原样保留不确定证据。只有精确状态仍成立时，普通语义失败才只回滚当前 step，之前完成步骤保留，source 与所有 child worktree 都不受污染。崩溃恢复也只接受 parent、tree、author/email、message 全部匹配的精确 merge commit，不接受“包含 child 的任意后代”。完成结果是本地 integration branch/head；协议不会 push、创建 PR、合并远端或改写 source branch。

## 取消、过期与重启 / Cancellation, expiry, and restart

取消先在 SQLite 中 fence 全部 active generation，再按精确 identity 清理：

- clean 且仍是 base-only 的 child worktree/branch 可删除；
- clean 但已有 commit 的 worktree 可移除，branch/commit 保留；
- dirty、branch/ancestry/root identity 漂移或无法安全识别的目录标记 `orphaned` 并原样保留；
- integration worktree 只有在 exact clean identity 下清理，否则报告 `integration_preserved`。

启动时最多分批扫描 256 个 recoverable plan，收敛 Prepare materialization、缺失但可验证的 worktree、commit intent、过期 lease 与 prepared/running merge queue。恢复在任何文件或 Git 副作用前重新读取 Run；paused/cancelled/terminal Run 只报告需要操作者处理，不会重建 worktree、执行验证或推进 merge。它不从数据库恢复 token、进程或旧权限，不会按不可信路径递归删除，也不会在不确定状态下自动选择冲突。

## HTTP 与 Desktop / HTTP and Desktop

Read bearer：

```text
GET /api/v1/runs/{run_id}/batch-deliveries?limit=50
GET /api/v1/runs/{run_id}/batch-deliveries/{batch_delivery_id}
```

Control bearer：

```text
POST /api/v1/runs/{run_id}/batch-deliveries
POST /api/v1/runs/{run_id}/batch-deliveries/{id}/children/{ordinal}/review
POST /api/v1/runs/{run_id}/batch-deliveries/{id}/children/{ordinal}/renew-owner
POST /api/v1/runs/{run_id}/batch-deliveries/{id}/merge
POST /api/v1/runs/{run_id}/batch-deliveries/{id}/cancel
POST /api/v1/runs/{run_id}/batch-deliveries/{id}/reconcile
```

Prepare/review/merge/cancel 需要 `Idempotency-Key`；renew-owner 使用 `expected_generation` CAS；reconcile 本身幂等。所有请求严格拒绝未知/重复字段和超限 body，并先绑定 URL 中的 Run 与 plan。普通响应不包含 worktree/integration root、owner-token digest、operation/request fingerprint 或 tool-profile fingerprint；Prepare/renew 仅在当次 control 响应中包含明文 token。

Desktop Run 页的“子任务与交付 / Child tasks & deliveries”展示 plan、child 状态、branch/generation/lease、允许/拒绝工具、邮箱、receipt、测试、review、merge blockers 与 step。Renderer 不持久化 owner token，也没有 child tool 执行入口；复核和合并仍由 Go Application 重新验证所有事实。

完整 wire schema 见 [OpenAPI](openapi.json)，设计与残余风险见 [ADR 0119](adr/0119-deliverable-batch-agents.md)。
