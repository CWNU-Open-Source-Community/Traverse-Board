# ADR 0126: Exact Legacy v97 Docker Trigger Compatibility

Date: 2026-08-22

## Status / 状态

**Accepted.** Schema v125 accepts one exact pre-final Windows preview history for migration
v97 and transactionally restores its Docker lifecycle cleanup trigger to the canonical v97
definition.

**已接受。** Schema v125 精确接受一枚正式固化前的 Windows 预览版 v97 历史，并在事务中
把 Docker lifecycle cleanup trigger 恢复为正式 v97 定义。

## Context / 背景

`v0.1.0-rc.2` correctly failed closed when an existing user data home contained migration v97
with checksum `844427411efc98247ebf521badb98a8c96e3a17b8aa5c7d429b7192d4dcad83b` instead of the
canonical checksum `e279b320761a7ae9ff7af17dbb9df0eceb80702d434f9dca30eca6825892559d`.
The affected database was internally consistent and used the exact released migration name.
Schema inspection showed that all 24 v97 SQLite objects matched except
`trg_sandbox_docker_lifecycle_cleanup_receipt_insert`: the preview trigger contained two extra
predicates binding the final transition to the cleanup receipt's lease identity. Those predicates
were stricter than the canonical trigger and did not grant additional authority.

The Windows portable ZIP is install-free, but it deliberately uses the shared user data home
`%USERPROFILE%\.cyberagent-workbench` unless `CYBERAGENT_HOME` is set. Extracting a new ZIP
therefore does not create an isolated database and exposed this upgrade gap.

`v0.1.0-rc.2` 在已有用户数据目录中发现 v97 校验和为
`844427411efc98247ebf521badb98a8c96e3a17b8aa5c7d429b7192d4dcad83b`、而不是正式值
`e279b320761a7ae9ff7af17dbb9df0eceb80702d434f9dca30eca6825892559d` 时正确地失败关闭。
数据库完整性正常，migration 名称也与正式版完全一致。逐对象检查确认 24 个 v97 SQLite
对象中只有 `trg_sandbox_docker_lifecycle_cleanup_receipt_insert` 不同：旧预览 trigger 多了
两项 final transition 与 cleanup receipt lease identity 的绑定条件。旧定义更严格，并未扩大权限。

Windows 便携 ZIP 的含义是免安装；除非显式设置 `CYBERAGENT_HOME`，它仍使用共享用户数据目录
`%USERPROFILE%\.cyberagent-workbench`。因此重新解压不会产生隔离数据库，并暴露了这个升级缺口。

## Decision / 决策

1. `acceptedMigrationChecksum` accepts the legacy checksum only for migration version 97. The
   existing validation still requires the exact canonical migration name and contiguous history.
2. Schema v125 drops the existing cleanup receipt trigger and recreates the byte-pinned canonical
   v97 trigger in the same SQLite migration transaction.
3. The v97 `schema_migrations` row is preserved verbatim. The runtime does not rewrite historical
   checksums to pretend the preview executed different SQL.
4. Unknown v97 checksums, wrong names, gaps, newer schemas, or any other changed migration continue
   to fail closed.
5. Tests pin both v97 checksums, reconstruct the exact preview trigger delta, preserve existing
   Workspace data, verify schema v125 and `PRAGMA integrity_check`, and prove that the stored trigger
   equals the canonical definition after upgrade.
6. Published `v0.1.0-rc.2` assets remain immutable. The repair requires a later prerelease built
   from a commit containing schema v125.

## Consequences / 结果

- Affected users can upgrade in place without deleting, renaming, or manually editing their local
  database.
- Canonical databases also record v125 and receive an equivalent transactional trigger rebuild.
- The exception is narrow, auditable, and cannot become an arbitrary checksum bypass.
- "Portable" remains an application-file packaging property, not an implicit per-directory data
  mode; documentation must state the shared data-home behavior explicitly.

## Rejected alternatives / 拒绝方案

- deleting or silently resetting `cyberagent.db`;
- changing the recorded v97 checksum in place;
- accepting every v97 checksum or matching only by migration name;
- accepting the preview checksum without repairing the trigger;
- replacing or mutating the already published `v0.1.0-rc.2` assets.

These alternatives either lose evidence, falsify migration history, weaken fail-closed validation,
leave divergent runtime semantics, or violate release immutability.

## Rollback / 回滚

Do not remove v125 after a build containing it has been published. If another exact historical
variant is discovered, inspect its complete SQLite schema, add a separately pinned compatibility
case and a new forward migration, and publish a later version. Never broaden this exception at
runtime or repair user data without a reviewed migration.
