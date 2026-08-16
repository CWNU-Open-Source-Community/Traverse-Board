# ADR 0108: typed 本地 Git 写工作流与变更审阅

Date: 2026-08-16

## Status

Accepted for the typed mutation service, binding review, and receipt ledger
described below. Desktop 审阅面板与 HTTP/OpenAPI 作为后续 PR 落地。

## 背景 / Context

Repository 读取（status/diff/history/比较）已存在，但没有 Go 拥有的 Git mutation
service；模型不能拼接任意 git 命令，写操作必须在执行前后都能被审阅。

## 决策 / Decision

### 1. 封闭 typed 操作集

repository_mutation.v1 只有五个操作：stage、unstage、commit、create_branch、
switch_branch。协议层面不可表达 reset --hard、clean -fdx、force checkout、rebase、
历史重写；未知操作字符串直接拒绝。路径只接受规范化相对路径（拒绝 ..、前导 -、
反斜杠、绝对路径），分支名独立校验。

### 2. 硬化执行边界

真实 git 二进制 + 全替换环境：GIT_CONFIG_NOSYSTEM=1、GIT_CONFIG_GLOBAL=devnull、
core.hooksPath 指向空目录、pager=cat、editor=false、diff.external 与 credential.helper
置空、fsmonitor=false、GIT_LFS_SKIP_SMUDGE=1、core.autocrlf=false、
--no-optional-locks、--literal-pathspecs；绝不继承 Agent 环境。argv 由固定模板生成。

### 3. 绑定审阅与回读

绑定指纹 = HEAD + branch + index 字节哈希 + 排序 porcelain status + 未跟踪文件内容
哈希。执行前校验未漂移，漂移即拒绝并要求重新审阅。commit 用 pathspec 限定已审阅
路径。执行后回读 HEAD/status/冲突；非零退出码即失败。收据（commit id、冲突、
clean、bounded stderr）写 git_mutation_operations（schema v106）+ run 事件
git.mutation_completed（metadata-only）。operation key 幂等重放。

## 后果 / Consequences

- 操作者可用 cyberagent git-op 在 Run 上下文中审阅并执行本地写操作，全程可审计。
- 非目标：push/pull/fetch/PR、历史重写、远端工作流。

