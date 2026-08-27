<!-- windows-two-deliverable-contract.v1 -->

# Traverse Board {{VERSION}}

## Windows 下载 / Windows downloads

本版本面向 Windows 用户只定义两个成品：

1. 从本 GitHub Release 下载唯一的直发入口 `TraverseBoard.exe`，直接双击启动；无需 ZIP、
   CMD、PowerShell、命令行参数或单独启动后端。新版本不交付
   `Start-Prayu-Operator-Preview.cmd` 或任何改名后的 Start helper。
2. Microsoft Store 安装；只有 Store 页面明确显示本版本且认证完成时才可视为可用。

This version defines exactly two Windows user products:

1. Download the sole direct entry, `TraverseBoard.exe`, from this GitHub Release and double-click
   it. No ZIP, CMD, PowerShell, command-line flag, or separately started backend is required. New
   releases ship neither `Start-Prayu-Operator-Preview.cmd` nor a renamed Start helper.
2. Install from Microsoft Store. It is available only when the Store listing explicitly shows
   this version and certification has completed.

直接 EXE 信任状态 / Direct-EXE trust state: **{{TRUST_STATE}}**

`SHA256SUMS`、SBOM、NOTICE、release metadata、Standard Code 报告、签名证据与 attestation
bundle 是伴随验证材料，不是额外产品或启动入口。稳定版公开 signing request、handoff
与验证结果 sidecar；签名者返回的 `TraverseBoard-signed.exe` 仅用于受控接收，不作为第二个
EXE 发布。Development MSIX、内部 portable ZIP 和 `.msixupload` 不面向普通用户发布。

`SHA256SUMS`, SBOM, NOTICE, release metadata, Standard Code reports, signing evidence, and
attestation bundles are verification sidecars, not additional products or launch entries. A stable
release publicly retains the signing request, handoff, and verified result sidecars; the
signer-returned `TraverseBoard-signed.exe` is intake-only and is not a second published EXE. The
development MSIX, internal portable ZIP, and `.msixupload` are not published for ordinary users.

Microsoft Store 的最终可用性、隐私政策、年龄分级与中英文 listing 以该版本 Store 页面为
准；若与本说明不一致，发布必须暂停并修正，不得把内部候选冒充 Store 成品。

Final Microsoft Store availability, privacy policy, age rating, and bilingual listing are
controlled by the Store page for this version. If it conflicts with these notes, publication must
stop for correction; an internal candidate must never be presented as a Store product.

Store 完成态还必须由仓库 completion evidence 绑定实际安装包的 `SignatureKind=Store`、精确
identity/version/architecture/payload，以及 Windows 10/11 × 100%/200% DPI × `zh-CN` IME
四条生命周期 row。Partner Center export、Store 截图和操作者生命周期记录只是经 reviewer
确认并做 hash 绑定的外部证据；它们不是加密证明。

Store completion also requires repository completion evidence bound to an actually installed
package with `SignatureKind=Store`, the exact identity/version/architecture/payload, and the four
Windows 10/11 × 100%/200% DPI × `zh-CN` IME lifecycle rows. Partner Center exports, Store
screenshots, and operator lifecycle records are reviewer-attested, hash-bound external evidence;
they are not cryptographic proof.
