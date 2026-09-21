# Windows 下载与发行说明 / Windows distribution

[首页](../README.md) | [English home](../README.en.md) | [Mac 下载](macos-release.md)

## 下载与启动 / Download and launch

从 [GitHub Releases](https://github.com/CWNU-Open-Source-Community/Traverse-Board/releases) 获取已发布版本的 `TraverseBoard.exe`，双击打开。Windows 10/11 需要 Edge WebView2 Evergreen Runtime。v1.0.0 尚在发布准备中，现有公开预览不代表包含主线的全部功能；最新进度见 [v1.0.0 发布说明](releases/v1.0.0.md)。

Download `TraverseBoard.exe` from a published [GitHub Release](https://github.com/CWNU-Open-Source-Community/Traverse-Board/releases) and double-click it. Windows 10/11 requires Edge WebView2 Evergreen Runtime. v1.0.0 is still being prepared; existing public previews do not necessarily contain current main-branch features.

开发者的只读入口为 `TraverseBoard.exe --safe-view`；完整本机操作说明见[测试指南](../packaging/windows/LOCAL-TEST-GUIDE.txt)。

## 成品与发布合同 / Deliverables and release contract

Windows 面向普通用户只提供两个成品；某个版本实际提供哪一项，必须以该版本的 GitHub
Release 和 Microsoft Store 页面为准：

1. **Microsoft Store 包**：从 Store 安装和更新。提交候选必须使用 Partner Center
   分配的精确 identity 并声明真实处理器架构；只有 Partner Center 接收且认证通过后
   才能称为 Store 成品。仓库生成的 `PrayuDesktop.msix`、Actions artifact 或本地旁加载
   包都不能证明 Store 已通过，也不能证明正式签名已经取得。
2. **唯一的直发入口 `TraverseBoard.exe`**：从 GitHub Release 直接下载并双击，无需
   ZIP、CMD、参数或单独后端。稳定直发版本需要可信 Authenticode 签名；未签名构建
   只能明确标记为预发布候选，并可能出现 SmartScreen“未知发布者”提示。

There are exactly two user-facing Windows deliverables. Availability for a particular version
must be read from that version's GitHub Release and Microsoft Store listing:

1. **Microsoft Store package**, installed and updated through Store. Its submission candidate
   must use the exact Partner Center identity and real processor architecture. A repository-built
   `PrayuDesktop.msix`, Actions artifact, or sideload package does not prove Store certification
   or production signing.
2. **The sole direct entry, `TraverseBoard.exe`**, downloaded from GitHub Release and started by
   double-click, with no ZIP, CMD, flags, or separate backend. A stable direct release requires
   trusted Authenticode signing; an unsigned build remains an explicitly labelled prerelease
   candidate and may trigger SmartScreen.

两者使用同一版本、源码提交、内嵌前端和发布门证据；Authenticode 签名导致字节变化时，
证据同时保留 Store 内可复现 payload hash 与公开签名 EXE 的 artifact hash，并由 GitHub
provenance/SBOM attestation 绑定最终对象。MSIX 的设计合同将安装文件与外部用户数据目录
分开；在称为 Store 成品前，仍必须用 Windows 10/11 实机矩阵验证
升级、卸载与重装时的 Workspace、凭证及 SQLite 行为。WebView2 缺失或版本过旧时，
应用只显示有界本机指导，不隐式安装依赖。

便携 ZIP 仅供 CI、兼容性、packaged E2E 与可复现性验证，不是第三种产品，也不能上传
到 Store。`SHA256SUMS`、SBOM、NOTICE、release metadata、manifest 与 Standard Code
报告属于校验/来源 sidecar，不是可启动产品。稳定版还会公开签名 request、handoff 与
signing evidence sidecar；签名服务返回的 `TraverseBoard-signed.exe` 只用于受控接收，
不会作为第二个 EXE 发布。源码与新 Release 均不再提供
`Start-Prayu-Operator-Preview.cmd` 或其他 Start 脚本；历史 Release 的旧 ZIP、旧启动器
名称和校验和保持不变。

The portable ZIP is retained only for CI, compatibility, packaged E2E, and reproducibility
checks; it is neither a third product nor a Store upload. Checksums, SBOM, NOTICE, metadata,
manifests, and Standard Code reports are verification/provenance sidecars. A stable release also
publishes the signing request, handoff, and signing evidence as public sidecars; the signer-returned
`TraverseBoard-signed.exe` is intake-only and is not published as a second executable. Source and
new releases provide neither `Start-Prayu-Operator-Preview.cmd` nor another Start script, and
historical Release assets remain immutable.

稳定版自动化会消费受保护的外部签名 handoff，复核允许的 signer 与 RFC 3161 时间戳，
记录签名前后 hash，并由独立最小权限 job 生成 GitHub provenance/SBOM attestation。Store
完成态还要求实际安装包为 `SignatureKind=Store`，identity/version/architecture/payload 精确
匹配，并具备四条 Windows 10/11 × 100%/200% DPI × 中文 IME 生命周期 row。Partner Center
export、截图和操作者生命周期记录仍是 reviewer-attested 外部证据；hash 只能发现后续漂移，
不能把它们变成加密证明。因此
[Issue #123](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/123) 会保持开启，
直到同一真实候选补齐并通过全部证据门。

Stable automation consumes a protected external signing handoff, verifies the approved signer and
RFC 3161 timestamp, records pre/post-sign hashes, and generates permission-scoped GitHub
provenance/SBOM attestations. Store completion additionally requires a real installed package with
`SignatureKind=Store`, the exact identity/version/architecture/payload, and four hash-bound Windows
10/11 × 100%/200% DPI × Chinese-IME lifecycle rows. Partner Center exports, screenshots, and
operator lifecycle records remain reviewer-attested external evidence: hashing them detects later
drift but does not turn them into cryptographic proof. [Issue #123](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/123)
therefore remains open until one real candidate supplies and passes all of this evidence.

具体发布边界见 [ADR 0145](adr/0145-windows-two-deliverable-release-contract.md)。
The exact release boundary is defined by [ADR 0145](adr/0145-windows-two-deliverable-release-contract.md).
Partner Center 的 identity、版本、上传与 `runFullTrust` 认证步骤见
[Microsoft Store submission runbook](../packaging/windows/STORE-SUBMISSION.md)。

## CI evidence and data compatibility

The `Desktop release` workflow keeps pull-request evidence honest: its packaged bootstrap remains
`needs_full_matrix`. A non-PR candidate must obtain the independently produced #182 product report
and #181 security report from the matching Draft Release, bind both reports to the current EXE,
internal verification ZIP, source revision, fixture, and attack matrix in
`standard_code_release_gate.v1`, and reverify that aggregate before publication. Missing,
tampered, replayed, skipped, or waived evidence fails closed. Prerelease versions remain explicitly
marked. A stable release additionally consumes a protected external Authenticode handoff, verifies
the approved signer and RFC 3161 timestamp, records pre-sign payload and post-sign artifact hashes,
and requires complete Partner Center identity/version configuration. The stable public Release
retains the signing request, handoff, and verified signing result as public sidecars; the returned
`TraverseBoard-signed.exe` is intake-only. A separate permission-scoped job creates GitHub
provenance and SBOM attestations. Store certification stays external until
`finalize-windows-release.ps1` verifies a real installed package with `SignatureKind=Store`, the
exact identity/version/architecture/payload, and the four Windows/DPI/IME lifecycle rows. See the
[Beta release gate](standard-code-release-gate.md),
[ADR 0144](adr/0144-standard-code-release-gate-aggregation.md), and
[ADR 0145](adr/0145-windows-two-deliverable-release-contract.md).

> [!NOTE]
> Portable means install-free application files, not directory-local data. The default data home remains `%USERPROFILE%\.cyberagent-workbench`, so extracting another ZIP reuses the same database. `v0.1.0-rc.2` cannot upgrade one exact pre-final Windows preview v97 history; schema v125 in later builds accepts that exact history and transactionally restores the canonical trigger without deleting or falsifying old data. See [ADR 0126](adr/0126-legacy-v97-docker-trigger-compatibility.md).
