# macOS 下载与发行边界

Mac 交付包含 Apple Silicon（arm64）和 Intel（amd64）两份 ZIP。文件名绑定完整版本、
架构与源码 revision 前 12 位；manifest 内保存完整 revision。包内应用名为
`TraverseBoard.app`，原 bundle identifier `workbench.prayu.desktop` 和数据目录不变。

## 获取候选包

在当前 PR 的 **Desktop release** 检查页面下载两个 `TraverseBoard-macos-<arch>-<revision>`
artifact。artifact 外层是 GitHub 的传输 ZIP；解开后取得产品 ZIP、manifest 和 checksum。
CI artifact 保留 30 天，仍不是 GitHub Release。预发布 tag 的流程成功后，同一 publisher
才会把两架构文件发布到同一个 Release 的 Assets 中。

解开产品 ZIP 后打开 `TraverseBoard.app`，无需编译前端或单独启动后端。包内保留历史名称的
`Start-Prayu-Operator-Preview.command` 作为兼容入口，并提供中英文使用说明。
当前包只有 ad-hoc 签名；macOS 可能阻止首次启动。没有公证和真机验收证据时，不称正式 Mac 发行。

## 构建与核验

在干净的 Mac checkout 中先运行 `cd web && npm ci && npm run build`，再回到仓库根目录：

```sh
bash scripts/release-desktop-darwin.sh v1.0.0-rc.1 "$(git rev-parse HEAD)" "$(go env GOARCH)"
```

流水线固定 `macos-15` ARM64 与 `macos-15-intel` Intel runner；检查原生 CPU、Go target、
Mach-O CPU 三者一致。构建两次比较二进制，再 ad-hoc 签名。`CFBundleVersion` 和
`CFBundleShortVersionString` 使用数字版本，完整预发布名称保留在 build metadata。
最低构建目标为 macOS 11；不代表已在所有 macOS 11+ 真机上通过验收。

产物位于 `build/macos-public`。ZIP 包含完整应用、指南、兼容入口、元数据、兼容报告、
SBOM、NOTICE 与 LICENSE。`ditto` 保留 POSIX 权限；新目录解压后再次核对全部文件、
可执行权限、Mach-O、部署目标、Go metadata 和 codesign。
manifest 与 checksum 绑定完整 ZIP。Windows publisher 复验 ZIP 内的实际文件、架构、
版本、revision 和全部哈希，再汇总两架构的精确资产白名单；只有 publisher 可写 Release。

## 正式版尚缺什么

PR 与预发布生成明确标注的 Mac preview。stable prepare 可以生成内部预览候选，
并不等于完成签名请求或正式发布。**stable finalize 当前会明确失败**：尚未实现有证据支持的
Developer ID 签名、公证、staple 验证及绑定最终分发包的真机验收接收流程。
不会因设置 `release_ready=true` 或通过 CI 编译而开放。

后续正式 Mac 接入至少需要：Apple Developer 身份及受保护签名/公证配置；验证实际
签名者、公证票据和最终包；在干净 Mac 验证 Finder 启动、模型配置（含失败提示）、退出与
重启后数据恢复，两架构分别记录。macOS 系统凭证库、Local Sandbox 和其他平台能力缺口
仍应单独实现或在交付范围中明确说明。不得用明文凭据存储或默认提升权限绕过。

现有 Windows 签名、Store、精确 CI revision、Draft evidence 和 attestation 门保持。
Mac job 不预先向 Draft 添加附件，避免破坏 Windows 的精确证据清单。

参考：[GitHub 原生 runner 清单](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)、
[Apple 版本字段](https://developer.apple.com/documentation/bundleresources/information-property-list/cfbundleshortversionstring)、
[Apple 公证流程](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)。
