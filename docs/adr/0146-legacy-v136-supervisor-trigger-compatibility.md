# ADR 0146: Exact Legacy v136 Supervisor Trigger Compatibility

Date: 2026-08-28

## Status / 状态

**Accepted.** Schema v138 accepts one exact pre-final Windows Desktop preview history for
migration v136 and transactionally restores the two missing Supervisor authority triggers to
their canonical v136 definitions.

**已接受。** Schema v138 精确接受一枚正式固化前的 Windows Desktop 预览版 v136 历史，
并在事务中把缺少的两枚 Supervisor authority trigger 恢复为正式 v136 定义。

## Context / 背景

An existing user database was healthy, had a contiguous migration ledger through v136, and passed
SQLite integrity and foreign-key checks, but the current Desktop failed to start because its stored
v136 checksum was
`5cccad921f47d44ac37f2206e91b355d55ce221d6c9c61e25e7fc08f1cbb6dbd` instead of the
canonical checksum
`6fdf459cf1fe6ae94370118a983598f4bc6e469d383a1f43aa211d863c034e4d`.

Object-by-object inspection showed that the preview was built from an intermediate v136 definition.
Its v136 schema matched the canonical history except that it did not yet contain:

- `trg_risk_escalation_supervisor_authority_insert`;
- `trg_host_command_supervisor_envelope_immutable`.

These triggers enforce the binding and immutability of Supervisor authority for risk-escalation and
host-command envelopes. Treating the preview checksum as generally equivalent without restoring the
triggers would leave weaker runtime invariants. Deleting or resetting the database would discard
valid user data and audit history even though the database itself is not corrupt.

现有用户数据库本身健康，migration ledger 连续到 v136，SQLite 完整性与外键检查均通过；
Desktop 启动失败的原因是其中记录了上述正式固化前 checksum，而不是正式 v136 checksum。
逐对象比对确认，中间预览版只缺少上列两枚 trigger。仅放行 checksum 而不补齐 trigger 会留下
较弱的 Supervisor 权限约束；删除或重置数据库则会无故丢失有效业务数据与审计历史。

## Decision / 决策

1. Migration validation accepts the legacy checksum only when the recorded version is exactly 136.
   Existing validation still requires the exact canonical v136 migration name and a contiguous
   history.
2. Schema v138 drops the two named triggers if present and recreates both from the canonical v136
   definitions in one SQLite migration transaction. Canonical v136/v137 histories and the exact
   legacy history therefore converge on the same fail-closed schema.
3. The existing v136 `schema_migrations` row is preserved verbatim. The repair does not rewrite its
   checksum or claim that the preview executed SQL it did not execute.
4. The migration does not delete, reset, replace, or reinterpret business rows. If trigger rebuild or
   ledger append fails, the transaction rolls back as a unit.
5. Any other v136 checksum, wrong migration name, version gap, newer unsupported schema, or changed
   history continues to fail closed.
6. Compatibility tests pin the canonical and legacy checksums, reconstruct the exact two-trigger
   delta, preserve existing Workspace data, exercise the repaired trigger behavior, and verify the
   final schema version, ledger preservation, integrity, and foreign-key state.

## Consequences / 结果

- The affected preview database can be upgraded in place without manual SQLite edits or loss of
  local data.
- Canonical databases also record v138 and receive an equivalent transactional trigger rebuild;
  v138 introduces no new business authority or product capability.
- The exception remains exact, reviewable, and incapable of becoming an arbitrary checksum bypass.
- A release containing v138 must be validated against a copy of the exact legacy database shape;
  fresh-profile startup alone is not sufficient upgrade evidence.

## Rejected alternatives / 拒绝方案

- deleting, renaming, or silently resetting the local database;
- rewriting the recorded v136 checksum to the canonical value;
- accepting every v136 checksum or validating by migration name alone;
- accepting the exact preview checksum without restoring both missing triggers;
- mutating an already published release asset in place.

These alternatives would lose user evidence, falsify migration history, weaken fail-closed
validation, preserve divergent authority semantics, or violate release immutability.

## Rollback / 回滚

Do not remove or renumber v138 after a build containing it has been published. If another historical
v136 variant is discovered, first inspect its complete ledger and SQLite schema, then add a
separately pinned compatibility case and a new forward migration. Never broaden the allowlist at
runtime, rewrite historical migration rows, or repair an original user database outside a reviewed
transactional migration.
