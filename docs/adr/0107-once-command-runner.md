# ADR 0107: 工作区沙箱内的一次性通用命令 Runner

Date: 2026-08-16

## Status

Accepted for the operator-facing one-shot command runner described below.
Supervisor 内的提案工具（one_shot_command_propose）与 Desktop 控制面作为
后续 PR 落地。

## 背景 / Context

保守档只能执行 Go 固定模板（ADR 0079 受控命令），高权限宿主命令是 CLI-only
路径（host_command.v1）。需要一个适用于 Code 模式的工作区范围、一次性、无持久
Shell 的通用命令 Runner，与四档权限严格对应。

## 决策 / Decision

### 1. 结构化协议 once_command.v1（无 Shell 字符串）

请求 = executable（绝对路径）+ argv（字面量数组）+ cwd + env + timeout + purpose。
协议中不存在拼接 Shell 字符串；argv 逐项直接传给 CreateProcess/exec，隐式
`&&`/`;`/管道/重定向/命令替换无法伪装成 argv。Shell 解释器（cmd/powershell/
pwsh/bash/sh/zsh/fish/wscript/cscript…）整体拒绝，Windows 只允许 .exe/.com
原生二进制，杜绝 .bat/.cmd 包装。

### 2. Workspace 边界与最小环境

cwd 经 EvalSymlinks 后必须落在 Workspace 内（拒绝 ..、symlink/junction/reparse
逃逸）；executable 必须是 Workspace 外的原生二进制（仓库文件永远不能被执行为
命令）。env 仅 allowlist（SystemRoot/WINDIR/TEMP/TMP），不继承 Agent 环境、
不加载 PowerShell/Bash Profile。

### 3. 四档权限

executionauth.EvaluateExecutionPermission（Kind=stateless_command）逐档执行：
conservative 只允许固定模板（直接拒绝）；approval 要求操作者逐条批准；
full_access 允许一次性命令但保留审计；debug 不在此 Issue 建立持久 Shell。

### 4. 进程树终止、证据与指纹

Windows：PROC_THREAD_ATTRIBUTE_JOB_LIST 创建时绑定 kill-on-close Job Object，
超时/取消终止完整进程树。Unix：独立进程组 + SIGKILL。输出 ≤64KiB + UTF-8 修复
+ Secret 脱敏，只返回不可信证据（exit code/duration/计数/前缀哈希）。
Spec 指纹 → Request 指纹（绑定 Run/Workspace）→ Approval 指纹（绑定审批身份），
批准后参数不可变。执行写 run 事件 once_command.executed（metadata-only，不落
raw output/env/参数）。

## 后果 / Consequences

- 操作者可用 `cyberagent once-command run` 在对应权限档内执行工作区范围命令，
  全程结构化、可审计、可终止。
- 后续 PR：Supervisor 提案工具接入（Agent 发起、操作者 CLI/HTTP/Desktop 审阅）、
  Desktop 控制面与 OpenAPI。

