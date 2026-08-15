# ADR 0104: Go 控制平面的 MCP Server v1

Date: 2026-08-15

## Status

Accepted for the stdio-only, read-first MCP Server v1 described below.
Execute-class typed actions beyond workspace read remain tied to their
own Sandbox/Runner/Git slices.

## 背景 / Context

MCP 是外部客户端（编辑器、桌面 Agent）接入的通用协议，但直接暴露任意
工具或数据库会成为绕过 CLI/HTTP/Run 生命周期的第二控制平面。需要一个 Go
主控、默认只读、全部转发既有 application services 的 MCP Server v1。

## 决策 / Decision

### 1. 仅 stdio、仅本地

`cyberagent mcp serve --run <id> --workspace <id>` 只读写 stdin/stdout
（newline-delimited JSON-RPC 2.0），永不监听 socket，拒绝任何远程监听配置。
每个进程实例绑定一个 Run + Workspace Scope。

### 2. 版本握手与 capability 诚实声明

仅接受协议版本 `2025-06-18`；initialize 严格解码（未知字段拒绝、client
identity 有界）。服务端只声明 resources 与 tools 两类 capability，
且发布清单就是实现清单：第一版工具只有 `read_file`、`list_workspace`
（经 Tool Gateway 的 workspace-read 类、自动审批链路），未实现的工具
永不发布。资源只有 `cyberagent://run/summary` 与
`cyberagent://run/activity`（display-only 投影，绝不含模型私有推理）。

### 3. 边界校验与审计

每条消息 4 MiB、UTF-8、单 JSON 对象；请求 id 会话内唯一（重放拒绝）；
并发上限 8、单请求超时 30s、`notifications/cancelled` 取消在途调用；
capability TTL 24h 过期后要求重新 initialize。所有活动写审计事件
（source=`mcp_server`，closed 字段集，Secret/raw output/私链绝不入事件），
工具结果只回传 redaction 后的 status/exit_code/bounded metadata。

### 4. 不是信任边界

MCP 是适配层：client 不能通过 schema 外字段传递 executable/path/credential/
权限档位（所有 params 严格解码），工具调用全部进入 Tool Gateway 的
Policy/Approval/Budget/redaction 链，与 CLI/HTTP 对同一 typed action 的
语义一致。

## 后果 / Consequences

- 本地 MCP 客户端可读取运行摘要与公开活动、发起 workspace-read 工具；
  任何写/执行类动作在本版直接 method-not-found。
- 无公网监听、无 Shell/Docker/SQLite 暴露、无独立 Agent loop 或权限模型。
- 后续版本可在对应子 Issue 完成后以同样的转发模式添加经审批的 typed actions。

