# ADR 0105: 签名 Skill 包、团队 Catalog 与固定 URL/Git 导入

Date: 2026-08-16

## Status

Accepted for the signed-package, team-catalog, pinned-import slice described
below. Desktop Catalog/详情 UI 与 HTTP 控制面作为后续 PR 落地，本 ADR 覆盖
其协议与决策。

## 背景 / Context

内置 Skill、registry 与安全导入方向已存在（ADR 0024/0031/0041），但缺少可验证的
外部包格式：manifest 有哈希、无签名与 publisher 身份；无团队 Catalog、版本 pin、
升级/回滚/撤销；无经批准的 URL/Git 固定导入。

## 决策 / Decision

### 1. 包协议 skill_package.v2（签名）

v2 ZIP 恰好三个条目：manifest.json（manifest 增加 publisher 字段）、SKILL.md、
SIGNATURE.json。签名 = Ed25519(pk, sha256(manifest.json) || sha256(SKILL.md))；
签名块声明 protocol/publisher/public_key/algorithm/signature/signed_at。publisher
身份 = SHA-256(公钥) 指纹。v2 包签名无效即整体拒绝；v1 无签名包继续支持。
签名只证明 provenance，不授予信任：签名包安装时剥离签名外壳、以与 v1 相同的
operator_installed_untrusted 类落库，签名档案摘要与 publisher 指纹保留在导入
台账（skill_catalog_imports）中。

### 2. 信任与 Catalog（schema v104）

新增四表：skill_catalog_publishers（信任/撤销，trusted_at/revoked_at 审计列）、
skill_catalog_pins（skill+surface → 版本 + enabled，升级/回滚即重 pin）、
skill_catalog_imports（source_kind=url|git|local、source、pin=sha256/commit、
archive_sha256、package_fingerprint、publisher_fingerprint）、
skill_catalog_audit（append-only：catalog.trusted/revoked/pinned/enabled/disabled/
import.completed）。信任决定只能由操作者/管理员做出；签名包在被 pin 前要求其
publisher 为 trusted 且未撤销。存在 pin 时，外部 Skill 选择必须命中 pin 的版本且
enabled（禁用或版本漂移拒绝）；无 pin 保持既有操作者确认流。

### 3. URL/Git 导入与隔离 staging

URL 导入：仅 HTTPS、无凭据、redirect 必须同 host 且保持 HTTPS（最多 5 跳）、
响应体 ≤1MiB、必须匹配调用方提供的 SHA-256 pin，字节不符即拒绝——redirect 或
上游漂移不能改变已审阅内容。Git 导入：仅 HTTPS、完整 40 位小写 commit SHA，
git clone --no-checkout（core.autocrlf=false，保证精确字节）→ 检出 pin commit →
rev-parse HEAD 校验（branch 漂移检测）；不执行 hooks/scripts/submodules/build。
目录/staging 打包只允许根目录的 manifest.json/SKILL.md/（可选 SIGNATURE.json），
拒绝 symlink/junction、子目录（.git 除外）、特殊文件、超限与非法 UTF-8；ZIP 容器
保持既有确定性 profile（固定 Deflate+descriptor、零时间戳）。

### 4. 仍是声明式、低权限扩展

Skill 只是提示/声明式资源；Tool 依赖声明不授予调用权限，全部经 Go Tool Gateway。
包内任何代码、脚本、hook 永不执行。审计不落 Skill 正文、Secret 或 raw output。

## 后果 / Consequences

- 团队可建立 publisher 信任名单与版本 pin，做升级/回滚/启停与撤销，全程可审计。
- 后续 PR：Desktop Catalog/详情/导入 UI 与 HTTP 控制面、Marketplace 体验。
- 非目标维持：不执行 preinstall/postinstall、不装二进制/服务、不给 Skill 读 API key
  或控制 Docker/Shell、不做开放公网付费市场。

