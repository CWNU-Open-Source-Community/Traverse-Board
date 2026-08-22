# Drydock 工作目录 / Drydock Workspaces

Schema v127 的 `drydock-workspace.v1` 为一个 Standard Code Run 创建独立的产品管理 Git
worktree 和分支。它提供的是身份、所有权、检查点和崩溃恢复合同；进程、网络、凭证和宿主文件
系统隔离必须由另一个运行时能力提供。

## 创建与 Trust

Run 必须是绑定真实 Workspace、Mission 和 Session 的 Code Run，来源必须位于已附着分支。
第一次 `create` 只读取并展示精确来源：Workspace/root 指纹、repository/common-dir、branch、
base commit、raw index、tracked/untracked/ignored dirty 状态以及特殊 Git entry 计数。

```powershell
cyberagent drydock create --run <run-id> --operation-key drydock-review-1

cyberagent drydock create --run <run-id> --operation-key drydock-create-1 `
  --confirm-workspace-trust `
  --expected-trust-digest <reviewed-sha256>
```

第二次调用会重新捕获全部事实；digest 不一致就拒绝创建。Trust 固定
`grants_process_authority=false`，不会开启 Shell、构建、网络、凭证、远端 Git 或清理权限。
来源若有未提交内容，状态会进入 Trust 回执，但内容不会复制进新 worktree。v1 明确拒绝 source
中的 symlink entry、submodule gitlink，以及 root/repository 路径上的 symlink 或 Windows reparse
point。空格、中文路径和平台大小写规则按字面身份处理。

CLI 固定管理根为 `$CYBERAGENT_HOME/drydocks`；产品从受信来源确定性生成目标路径、分支和名称，调用
者不能通过 create 指定任意目标目录，管理根与 source Workspace/Git common-dir 必须完全不相交。每个 repository
最多八个 active Drydock，全局最多 64 个；
默认寿命七天，合同最大寿命三十天。

## 使用、检查点与恢复

`status` 返回 Workspace、generation、current checkpoint、delivery、恢复原因与只追加 receipt。
绝对 Drydock 路径仅在本机 CLI 的明确 create/use 输出中返回；JSON projection 不返回私有路径。

```powershell
cyberagent drydock status --run <run-id>
cyberagent drydock use --run <run-id> --generation <n> --operation-key use-1

cyberagent drydock checkpoint --run <run-id> --generation <n> `
  --operation-key checkpoint-1 --title "tests pass" --confirm-observed-changes
```

每次操作都重验 Run/source/root/repository/branch/base/managed registration/current binding 和
generation。发现漂移即失败关闭。Checkpoint 精确保存 tracked、untracked 和 raw Git index；对
当前改动建检查点必须使用 `--confirm-observed-changes` 明确归属。
Drydock 以自己的 `LastCheckpointID` 维护时间线，不会替换同一 Run 的 source Workspace checkpoint
cursor；普通 Workspace 的 Timeline/Undo/Redo/Rewind 仍指向原来源。

Rewind 和 Undo 都先返回三方预览。只有使用新的 operation key、相同 generation、明确确认并在
需要时确认当前观察到的改动，才会执行恢复：

```powershell
cyberagent drydock rewind --run <run-id> --generation <n> `
  --operation-key rewind-preview-1 --target-checkpoint <checkpoint-id>

cyberagent drydock rewind --run <run-id> --generation <n> `
  --operation-key rewind-apply-1 --target-checkpoint <checkpoint-id> `
  --confirm --confirm-observed-changes

cyberagent drydock undo --run <run-id> --generation <n> `
  --operation-key undo-preview-1
cyberagent drydock undo --run <run-id> --generation <n> `
  --operation-key undo-apply-1 --confirm --confirm-observed-changes
```

恢复会追加新 checkpoint，不改写历史。若内容或 index 无法精确恢复、出现三方冲突或身份在操作中
变化，目录保留并进入 `recovery_required`。Rewind/Undo 只在 current 与 target 共享同一当前 HEAD
时恢复工作树/index，不重写提交历史；Fork 可从固定 Drydock base 的任一可证明后继 checkpoint
建立独立分支。

`fork` 复用 Workspace Checkpoint 的权限与暂停 Run 要求，创建新的 Workspace/Mission/Run/Session、
branch 和 worktree；新 Run 不继承 approval、credential、capability、lease、process、network、
terminal 或 execution authority。完整参数可由 `cyberagent drydock fork -h` 查看，关键绑定包括
target checkpoint、expected current checkpoint、generation、明确确认及新的 Workspace/root/branch。

## 交付

```powershell
cyberagent drydock deliver --run <run-id> --generation <n> `
  --operation-key delivery-1 --confirm
```

Delivery 重新建立归属 checkpoint，验证没有事后漂移，并输出相对固定 base 的完整有界 patch、
diff SHA-256 和 diffstat。结果固定 `automatic_merge=false`、`push_authorized=false`、
`force_authorized=false`、`source_overwrite_allowed=false`。操作者只能把该 patch/receipt 带入独立
review 流程；Drydock 不会修改来源分支、覆盖来源文件、push 或 merge。

## 崩溃恢复、清理与 GC

```powershell
cyberagent drydock reconcile
cyberagent drydock cleanup --run <run-id> --generation <n> `
  --operation-key cleanup-1 --confirm
cyberagent drydock gc --limit 100 --confirm
```

创建在 Git 操作前写入 `preparing`。重启后，`reconcile` 只会完成精确匹配且干净的已创建目录；
否则保留现场并记录 recovery receipt。Cleanup/GC 只对登记、身份完整匹配、当前干净且 generation
正确的 Drydock 使用非 force remove。含 tracked/untracked/ignored 改动、身份不确定、Git 拒绝或
未知来源的目录一律不删除。清理后保留本地分支、Trust、checkpoint、event、delivery 与 lifecycle
receipt，供审计和人工恢复。若非 force Git remove 已完成但数据库提交前崩溃，后续明确确认的
Cleanup 只有在 Git registration 与确定性路径都已不存在时才关闭账本；该路径不执行任何文件删除。

设计、状态机和验收证据见 [ADR 0129](adr/0129-run-owned-drydock-workspaces.md)。
