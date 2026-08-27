# Standard Code Beta release gate

本文定义 Issue #140 的中央证据聚合与 Beta 发布门。它不重新实现 #181 的安全 executor
或 #182 的产品 collector，而是严格消费它们针对同一 EXE/ZIP/source revision 生成的不可变
报告，并与 packaged bootstrap 一起封存为 `standard_code_release_gate.v1`。

## 只有三份报告同时存在才可通过

| 组件 | 协议 | 必需结论 |
|---|---|---|
| packaged bootstrap | `standard_code_packaged_e2e.v1` | `bootstrap_status=pass`，仍诚实保留 `release_gate_status=needs_full_matrix` |
| product/delivery | `standard_code_product_e2e.v1` | 四语言真实失败/诊断/重试/通过、交付投影、Thread 连续性与 Windows 10/11 × 100%/200% 证据完整 |
| security/runtime | `standard_code_packaged_security_evidence.v1` | 40 case、75/75 backend run、零未执行、完整回收且 self-hash/chain 有效 |

中央 `cmd/releasegate` 使用严格 JSON 解码；未知字段、第二个 JSON value、缺失组件、过大文件、
symlink/reparse input 或无效 producer self-hash 都会返回非零。它重新计算当前 EXE、ZIP、
portable manifest、release metadata 和三份报告的 SHA-256，然后要求以下 identity 完全相同：

- version、source revision、EXE SHA-256、ZIP SHA-256 和文件大小；
- fixture manifest 与 40-case attack matrix SHA-256；
- product 的四语言/Local-Docker/五种 delivery surface/Windows 平台覆盖；
- security 的 40 case、75 个真实 backend run 和 owned cleanup 结论。

最终 aggregate 只保存稳定 identity、计数、布尔 safeguard 与 producer digest，不保存本机路径、
截图、命令输出、Provider secret、凭据或 Docker 原始日志。`evaluated_at` 取三份输入证据中的最晚
时间，因此同一输入可确定性重算；publish job 会在下载 Actions artifact 后再次逐字段验证。

## 两阶段 Draft Release 流程

产品矩阵包含人工 Desktop 与 Windows 10/11 主机证据，普通 GitHub-hosted runner 不能替操作者
完成，也不能用 mock/skip/waiver 伪造。因此候选取证和中央发布是两个明确阶段：

1. 冻结待发布 commit 与版本，生成 clean、reproducible EXE/ZIP。
2. 按 [packaged product E2E](standard-code-product-e2e.md) 生成
   `standard-code-product-e2e.json`；按 [packaged security matrix](standard-code-packaged-e2e.md)
   生成 `standard-code-security-evidence.json`。
3. 为同一版本建立 Draft Release，并先上传这两份 path-free 报告：

   ```powershell
   $revision = (git rev-parse HEAD).Trim()
   $version = "v0.1.0-beta.1"
   gh release create $version --draft --target $revision --generate-notes `
     --title "Traverse Board Desktop $version"
   gh release upload $version `
     build/desktop/standard-code-product-e2e.json `
     build/desktop/standard-code-security-evidence.json --clobber
   ```

4. 稳定版先从该候选 ref 手动运行 `Desktop release` 的 `phase=prepare`，下载隔离的 signing
   request artifact，再由受保护外部签名者对精确 payload 签名。将未改动的
   `direct-exe-signing-request.json`、返回的 `TraverseBoard-signed.exe` 和
   `direct-exe-signing-handoff.json` 上传到同名 Draft。预发布不执行这一步。
5. 等待该 revision 在 `main` 的中央 `CI` push run 成功；随后手动运行 `phase=finalize`，
   或在 exact tag push 前准备好同名 Draft Release。workflow 的 Draft allowlist 按 channel
   固定：预发布恰好两份产品/安全报告，稳定版恰好再加上述三份签名 intake asset；额外、
   缺失、重名或超限文件都会失败关闭。
6. workflow 重新构建候选、运行 bootstrap、生成 aggregate，并在独立权限 job 中为最终
   EXE 与 Store upload 生成 GitHub attestation。publish job 再次验证内部 ZIP、aggregate、
   签名和 attestation，仅公开唯一的 `TraverseBoard.exe` 入口及验证 sidecar；
   `TraverseBoard-signed.exe` 在发布前移除，内部 ZIP 不进入公开 Release。任一步失败都会
   保留 Draft，不会发布。

Pull Request 事件没有发布候选身份，也没有人工产品证据，因此只运行 producer/aggregate 合同
测试和 bootstrap。它不会写 `passed` aggregate，publish job 也不会运行；这不是 skip 或 waiver，
因为 PR 从未请求发布。

## 手工重算

维护者可在证据与候选位于同一 release output 目录时运行：

```powershell
go run ./cmd/releasegate `
  --binary build/desktop/TraverseBoard.exe `
  --archive build/desktop/Prayu-portable-v0.1.0-beta.1-windows-amd64.zip `
  --portable-manifest build/desktop/portable-zip-manifest.json `
  --release-metadata build/desktop/release-metadata.json `
  --bootstrap build/desktop/standard-code-packaged-e2e.json `
  --product build/desktop/standard-code-product-e2e.json `
  --security build/desktop/standard-code-security-evidence.json `
  --expected-revision (git rev-parse HEAD) `
  --report build/desktop/standard-code-release-gate.json
```

`--report` 使用 create-exclusive 写入。使用相同输入复核已有 aggregate 时改为
`--verify-report <path>`；任何字节语义差异、跨候选 replay 或 producer tamper 都会失败。

## 不在本门内的发布声明

本门是预发布与稳定发布共同的候选前置门，但不完成 #123 的正式代码签名、Microsoft Store
分发或更广平台认证。稳定直发 EXE 还必须独立通过 ADR 0145 的 signer/RFC 3161、签名前后
hash、公开 signing handoff 与 GitHub provenance/SBOM attestation 门；Store 候选还必须
通过精确 Partner Center identity、显式 Store version 与架构门。Store 最终验收要求
finalizer 在实际安装包上读到唯一健康的 `SignatureKind=Store`、精确
identity/version/architecture/payload，并接受四条 hash-bound 生命周期 row。

Partner Center export、Store 截图、listing/privacy/age-rating 记录和操作者编写的生命周期
记录仍是 reviewer-attested 外部证据；hash 绑定只能发现后续漂移，不能把它们变成加密
证明。只有 Authenticode 与 GitHub/Sigstore bundle 走独立加密验证。以上真实外部证据
尚未齐备时 #123 保持开启。Full Access、Debug Maximum Access 与 Full CDP 仍是高级、
默认关闭能力。
