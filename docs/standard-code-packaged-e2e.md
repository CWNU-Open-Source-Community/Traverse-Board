# Standard Code packaged E2E 基线

本文定义 GitHub Issue #140 的固定仓库、攻击矩阵与 Windows portable ZIP
bootstrap。它是完整 Beta 发布门的可复现地基，不代表 40 项攻击已经全部通过。

## 固定仓库合同

`cmd/packagede2e` 从 `internal/packagede2e/testdata` 中的内嵌资产生成四个独立
Git 仓库。生成器不下载依赖，不读取全局 Git 配置，并固定 SHA-1 object format、
`main` 分支、作者、提交时间（2000-01-01T00:00:00Z）和提交消息。任何资产、补丁、
命令、文件角色或预期 Git identity 变化都会使 manifest 校验失败。

| 仓库 | 初始验证 | 固定 HEAD | 固定 tree |
|---|---|---|---|
| Go | `go test ./...` | `29556f97e714bf3396d35a3492b1cf8d6e676f72` | `67cc3a9967259486939122ba27372a6bbe5a1d2d` |
| Node.js | `node --test` | `731bbef417f13ee5c3294b31061a03f8a0c24eb9` | `0ae71653ff28df8563134ed18dc2c62bdd5c526c` |
| Python | `python -m unittest discover -s tests` | `03598dfa0dd67acc6a29ffc18b9513abe092dd0f` | `c6773735c0fa679dd6d86190306f87cb6858bae8` |
| Rust | `cargo test --offline` | `7b015d6c035137d926a106962369bdfaf0360b80` | `5ed4f705ec66beeaebd6389f9ddca98cc7332452` |

每个初始验证都必须以真实非零退出码失败；应用仓库对应的固定 repair patch 后，
同一命令必须通过并满足 `git diff --check`。这证明 E2E 使用的是可修复的真实失败，
而不是空仓库或永远成功的占位命令。

固件同时覆盖中文、空格和深层路径、CRLF、二进制文件以及明确标为
`UNTRUSTED REPOSITORY ATTACK FIXTURE` 的 prompt-injection 文本。文本是测试数据，
不是给 Agent、操作者或 CI 的指令。dirty/untracked、并发修改及恢复由攻击用例在
materialize 后施加，不能写进固定提交，否则会破坏可复现 identity。

手工准备仓库和 path-free 报告：

```powershell
New-Item -ItemType Directory -Path .tmp/issue140-fixtures
go run ./cmd/packagede2e `
  --output .tmp/issue140-fixtures/repositories `
  --report .tmp/issue140-fixtures/fixture-set.json `
  --verify-toolchains
```

`--output` 必须不存在，父目录必须已存在且不是 symlink/reparse point。报告使用
`standard_code_fixture_set.v1`，只包含稳定 ID、摘要、Git identity 和布尔 oracle
结果，不包含绝对路径、命令输出或 secret。

## 攻击矩阵

`attack-matrix.json` 使用 `standard_code_attack_matrix.v1`，绑定 40 个 required
case。矩阵是闭集；未知字段、重复 ID、缺少 backend、缺少 expected denial code、
缺少 operator UI/immutable event 证据，或把 recovery 写成普通 allow/deny，都会在
Go 测试和 materializer 中失败。

| 类别 | 数量 | 主要边界 |
|---|---:|---|
| `filesystem_escape` | 6 | parent/drive/UNC/device、reparse/symlink、长路径 |
| `credential_access` | 5 | HOME/profile、Credential Manager、Git helper、SSH agent、cloud env |
| `network_escape` | 5 | DNS、TCP、UDP、proxy、loopback；Docker 必须 `network=none` |
| `process_escape` | 4 | detached/background、继承 handle、Job/process-group escape |
| `prompt_injection` | 4 | 提权、adapter 选择、自批准、伪造回执 |
| `authority_replay` | 5 | permission/profile/root/backend generation、cross-Run replay |
| `approval_fallback` | 1 | backend 不可用时只产生显式、未批准的宿主 proposal，不静默 Full Access |
| `output_safety` | 4 | secret、ANSI/control、stream/artifact 上限 |
| `recovery` | 6 | renderer/Desktop/强杀/reboot-equivalent/lease/并发 Drydock 修改 |

Local 和 Docker 用例共享同一个稳定 expected outcome 与 evidence 合同，但 backend
实现可以产生不同的 denial code。完整执行必须从应用的公开工具调用入口进入，不能
通过直接调用内部函数伪造通过。需要 Approval fallback 的场景必须证明：沙箱拒绝后
只生成待操作者逐次批准的 proposal，不自动切换宿主 adapter，不复用旧 approval。

## packaged bootstrap

`scripts/standard-code-packaged-e2e.ps1` 只接受仓库内的 release output，并对
`portable-zip-manifest.json`、`release-metadata.json`、ZIP 和解压后的
`TraverseBoard.exe` 做摘要与 revision 绑定。随后它会：

1. 运行四仓库 fail/repair/pass oracle；
2. 解压 manifest 指定的确切 ZIP，不使用顶层构建 EXE 代替；
3. 在独立 `CYBERAGENT_HOME` 中启动默认模式，确认 SQLite store 非空、宿主 EXE
   没有 TCP listener，尝试隐藏窗口的 `WM_CLOSE`，并保证精确 owned cleanup；
4. 启动安全 `--operator-preview`，强杀后复用同一 store 重开，再次保证精确 owned
   cleanup；隐藏启动下未观察到 graceful exit 时会如实记录 force-cleanup fallback；
5. 证明固定仓库 HEAD/tree/worktree 未变化，注入的 credential/proxy sentinel 未被
   写入 home、固件或 package，且所有本次启动的 candidate 进程都已退出；
6. 只清理由随机 run ID 精确拥有、且位于仓库 `.tmp` 下的目录。

维护者在生成 portable release 后运行：

```powershell
./scripts/standard-code-packaged-e2e.ps1 `
  -ExpectedVersion v0.1.0-rc.2 `
  -ExpectedRevision (git rev-parse HEAD)
```

CI 产物 `standard-code-packaged-e2e.json` 使用
`standard_code_packaged_e2e.v1`。成功 bootstrap 的关键状态是：

```json
{
  "bootstrap_status": "pass",
  "release_gate_status": "needs_full_matrix",
  "attack_matrix": {
    "required_case_count": 40,
    "evidenced_case_count": 0,
    "remaining_required_case_count": 40,
    "unexecuted_cases_are_not_pass_or_skip": true
  }
}
```

这里的 `0` 是有意的：startup、hash、oracle 和清理检查是 packaged bootstrap，不能
冒充某个真实 Local/Docker 攻击调用。脚本失败会写 `bootstrap_status=fail` 并以非零
退出；脚本成功也仍是 `needs_full_matrix`。报告不允许 `skip` 或 waiver 成为发布证据。

## 完整 #140 发布门仍需完成

进入 Beta gate 前，还必须由真实 packaged EXE 对矩阵逐项生成来源绑定、不可变的
call/event/UI evidence，完成 Local Sandbox、Docker `network=none`、Approval fallback
和 Windows VM recovery 组合（包括可见窗口的正常退出），并核对失败码、操作者可见状态、generation fencing、
产物/SBOM/复现说明。正式代码签名与 Microsoft Store 分发仍属于独立发布线。

任何一次矩阵 case 缺失、环境不具备、取证不完整或结果不确定，都应使 gate 失败，
而不是改成 skip、expected failure 或手工豁免。测试只能使用合成 sentinel；禁止把
真实 API key、SSH socket、cloud credential、用户 HOME 内容或绝对路径写入报告。
