# ADR 0110: 持久 Debug 终端与限时 Agent 输入

Date: 2026-08-16

## Status

Accepted for the durable terminal-session ledger and audit surface described
below. 会话/PTY/lease 机制沿用既有实现（session.go、terminal_lease.go、
debug_terminal_agent_input.go），本 ADR 记录其产品边界与新增台账。

## 背景 / Context

用户需要一个类似开发工具的持久终端，但"用户拥有终端"与"Agent 可输入终端"
必须严格分开：默认只有用户可输入；Agent 输入需要 Debug maximum-access 启动
闸门 + 逐次确认 + TTL；普通权限档位无法解锁键盘。

## 决策 / Decision

### 1. 会话与输入权归属

每 Run 一个专用 ConPTY/PTY session（cwd/进程树/Job Object 生命周期）。默认
UserOwned；Agent 输入走 TerminalInputLease：绑定 Run/session/process 与权限
快照修订、单次确认、TTL 15s–15m、进程内存放——应用重启即整体失效，任何数据库
状态都不能恢复输入权。UI 恒显示输入权归属并提供立即撤销/紧急停止。

### 2. 台账与审计（schema v107）

terminal_sessions 持久化 state/cwd/resize/process_pid/agent_input_active；
closed/failed 终态不可静默复活。审计走 run 事件（源 debug_terminal）：closed 事件
类型（started/stopped/agent_input_issued/revoked/expired/resized/cwd_changed/
crashed），actor 归属区分用户与 Agent，payload 有界化——raw output、密码、Secret
绝不落盘。

### 3. 崩溃与重连

后端 I/O 失败 → SessionFailed（测试覆盖：失败会话拒绝 Agent 输入）；输出用 cursor
分页读取，断线重连不重复；进程树由 Job Object 创建时绑定，timeout/cancel/关窗
即终止或明确转用户后台任务。不影响一次性 Runner 的无状态语义。

## 后果 / Consequences

- Desktop 终端面板（user-terminal-panel）与 Go 控制面已具备；本 ADR 补齐台账与
  审计后，剩余 CLI 产品入口作为后续 PR。
- 非目标维持：不接管用户已有终端、普通 Code 模式无持久 Agent Shell、不记录完整
  交互输出与 Secret。

