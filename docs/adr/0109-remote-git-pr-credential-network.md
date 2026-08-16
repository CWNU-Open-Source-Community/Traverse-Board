# ADR 0109: 远端 Git、Pull Request 与凭证/网络授权

Date: 2026-08-16

## Status

Accepted for the network-scoped remote workflow described below. Desktop
审阅/取消流程与 HTTP/OpenAPI 作为后续 PR 落地。

## 背景 / Context

Prayu 没有 fetch/pull/push 与 PR 产品工作流；网络与凭证必须与本地 mutation
分离，经 Go 的 Scope/Credential/Approval/审计执行，网络默认关闭。

## 决策 / Decision

### 1. 封闭 typed 远端操作 + 网络 Scope

repository_remote.v1 五个操作：fetch、pull_ff（仅 fast-forward）、push_branch
（仅新分支，ls-remote 探测已存在即拒绝）、create_pr、update_pr。force push、
远端分支删除、protected branch 改写不可表达。仅 HTTPS、拒绝 loopback、URL 内
凭据/query/fragment；host/port/protocol/TTL/Run 全部进入 request_fingerprint 与
台账（schema v107）。

### 2. 凭证只按引用存在

spec 只携带 credential NAME；secret 经 credential.Store 按名解析，git 路径经
临时 GIT_ASKPASS + GIT_PASSWORD 子进程环境变量（用后即删），GitHub API 路径仅
Authorization: Bearer 头。argv/日志/SQLite/Activity/OpenAPI/模型上下文均无明文
（测试断言）。stderr 自动脱敏。

### 3. 网络 kill-switch 与可解释错误

http/https.proxy 置空、core.sshCommand 置空（禁 SSH/ProxyCommand）、
protocol.ext.allow=never（禁协议 wrapper）；PR API 仅 github.com，401/403 限流/
422/404/网络失败均映射为可解释错误码。

## 后果 / Consequences

- 操作者可用 cyberagent git-remote 在 Run 上下文中执行网络作用域远端操作，
  全程脱敏审计。
- 非目标：组织级 OAuth/SSO/RBAC、自动合并 PR、模型读取原始 credential、任意
  协议包装器。

