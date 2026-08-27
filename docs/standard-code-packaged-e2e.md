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

## #181 packaged 攻击与恢复 executor

Issue #181 增加独立的 packaged-only executor。入口属于固定 conformance harness，只有
下面三个参数同时且单独出现时才会运行；它不会继承 Desktop 默认 capability bundle：

```powershell
$env:CYBERAGENT_STANDARD_CODE_DOCKER_IMAGE_DIGEST = `
  'sha256:<reviewed-fixed-image-digest>'

& .\TraverseBoard.exe `
  --standard-code-attack-matrix `
  --attack-matrix-root 'D:\evidence\standard-code-attack-<new-id>' `
  --candidate-archive 'D:\release\Prayu-portable-v0.1.0-windows-amd64.zip'
```

`--attack-matrix-root` 必须是不存在的 `standard-code-attack-*` 目录，ZIP 中的
`TraverseBoard.exe` 必须与正在运行的 EXE 逐字节一致，release metadata 必须绑定干净的
40 位 source commit 并证明 EXE/ZIP reproducible。executor 随后在同一候选程序内：

1. 重新 materialize 固定四仓库与 40 项矩阵，固定 manifest/matrix SHA-256；
2. 经公开 `command_runtime` Tool Gateway、Go Application 和当前 Standard Code
   adapter 执行 75 个 case/backend 组合，而不是直接调用底层 sandbox；
3. 对 Local Sandbox 与固定 digest Docker 分别记录 backend identity、generation、
   `network=disabled`、`credentials=none` 与 `full_access_enabled=false`；
4. 用真实 Job、Artifact、Run event、operator projection、Drydock observation 与
   Checkpoint 生成所需 evidence reference；报告仅保留 hash/provenance；
5. 对 permission、mode/profile、Drydock root、backend capability、lease 与跨 Run
   authority snapshot 做调用前 fence；任何漂移必须在创建进程前以稳定冲突失败；
6. 从同一个 packaged EXE 的固定内部 recovery worker 启动后台 Job，注入 renderer
   detach、正常 Desktop 退出、强杀、重启等价、lease 过期及 dirty/untracked 并发修改，
   然后复用同一 SQLite/Drydock 重启两次，核验 `terminal`、`tree_reaped`、Checkpoint
   和用户修改保留；worker 不接受任意命令、环境、权限、Workspace 或 Docker 参数；
7. 只 rewind/cleanup harness 创建的 Drydock、Job、worker 与目录，不按 PID 名称、镜像
   名称或宽泛路径清理其他资源。

Windows 上由 packaged harness 启动的 Git、固定工具链和 recovery worker 均使用
`CREATE_NO_WINDOW`/hidden window；专用 matrix/worker 入口失败时只写入被父进程捕获的
stderr，不弹出 Desktop 原生启动错误框，也不抢占操作者桌面。

最终文件 `standard-code-security-evidence.json` 使用
`standard_code_packaged_security_evidence.v1`，create-exclusive 写入。它固定包含 40 个
case、75 个 backend run 与 append-only SHA-256 chain。最终 `status=passed` 必须同时满足：

- 每个 required case/backend 都真实执行且 evidence 完整；`unexecuted`、`skip`、
  backend unavailable、synthetic result 或 expected outcome/code 不一致均失败；
- Docker 不可用只记录 `approval_required` / `approval_fallback=true`，不会切换到
  Full Access，也不能使整份 #181 报告通过；
- 所有启动的 harness-owned Job 都终态且完整回收，orphan/foreign kill 为零；
- JSON 不含 secret-shaped 内容、控制序列、原始凭据或私有绝对路径；
- report self-hash、逐记录 chain、EXE/ZIP/source/matrix/fixture/backend identity 均可重算。

恢复 worker 的参数是父 executor 生成的内部协议，不是新产品 Surface。它只接受父目录中
已存在且 exact-owner marker 匹配的固定 case/backend root；恢复 receipt 只作为 #181
conformance evidence，不能签发执行权限或宣布发布门通过。架构决定见
[ADR 0143](adr/0143-standard-code-packaged-security-matrix.md)。

## #140 最终发布门

#181 只产生安全切片证据；#140 的中央 owner 将该不可变报告与 bootstrap、#182 的四语言
修复/交付真实性、Windows host/可见窗口及当前 release artifacts 交叉绑定为
`standard_code_release_gate.v1`。非 PR 的中央 workflow 必须从同版本 Draft Release 取得两份
独立 producer report，并在当前 EXE/ZIP 上重新计算 identity；最终 publish job 下载 artifact
后再次验证 aggregate。完整 runbook 见
[Standard Code Beta release gate](standard-code-release-gate.md)。正式代码签名与 Microsoft
Store 分发仍属于独立发布线。

任何一次矩阵 case 缺失、环境不具备、取证不完整或结果不确定，都应使 gate 失败，
而不是改成 skip、expected failure 或手工豁免。测试只能使用合成 sentinel；禁止把
真实 API key、SSH socket、cloud credential、用户 HOME 内容或绝对路径写入报告。
