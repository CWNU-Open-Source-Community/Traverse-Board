# Code Intelligence / LSP 语义代码智能

`code-intel-lsp.v1` 是由 Go 控制的只读 Language Server Protocol 运行时。它只启动操作者明确审查并写入进程配置的本地 Server，不扫描仓库配置、不自动安装扩展，也不接受模型提供的 executable、argv、环境变量或 Workspace 根目录。

The Go-owned `code-intel-lsp.v1` runtime starts only explicitly reviewed local language servers. It never discovers or installs a server from repository content, and a model cannot supply or widen its process configuration.

## 模式和工具 / Mode and tools

语义工具与 `agent-code-tools.v1` 的只读 Workspace authority 使用同一边界：仅 Code Surface、root、Plan 或 Deliver 可用；Cyber 和 Specialist 始终拒绝。Profile 不会把只读语义证据变成写入、Shell、Git、网络或审批能力。

| Tool | LSP operation | Required input |
|---|---|---|
| `code_workspace_symbols` | `workspace/symbol` | bounded query |
| `code_document_symbols` | `textDocument/documentSymbol` | Workspace-relative file |
| `code_definition` | `textDocument/definition` | file and zero-based position |
| `code_references` | `textDocument/references` | file, position, declaration choice |
| `code_implementation` | `textDocument/implementation` | file and position |
| `code_hover` | `textDocument/hover` | file and position |
| `code_signature_help` | `textDocument/signatureHelp` | file and position |
| `code_diagnostics` | pull diagnostics, with bounded publish fallback | file |
| `code_call_hierarchy` | prepare plus incoming/outgoing calls | file, position, direction |
| `code_type_hierarchy` | prepare plus super/subtypes | file, position, direction |

只有 Server 在 `initialize` 中真实协商成功的 capability 才进入模型工具定义。缺少某项 capability 会让对应工具保持不可见；它不会用猜测或静态语言表补齐。

## 操作者配置 / Operator configuration

先取得注册 Workspace 的稳定 ID，再计算 Server executable 的 SHA-256：

```powershell
cyberagent workspace show demo
Get-FileHash -Algorithm SHA256 C:\Tools\gopls.exe
```

```bash
cyberagent workspace show demo
sha256sum /opt/prayu-tools/gopls
```

配置文件必须是显式选择的绝对、规范、非链接普通文件。`source` 不能由配置提供；Go 根据实际配置文件名和内容摘要生成该字段。以下是 gopls 示例，所有占位值都必须替换：

```json
{
  "protocol_version": "code-intel-config.v1",
  "servers": [
    {
      "protocol_version": "code-intel-lsp.v1",
      "id": "gopls-main",
      "name": "gopls",
      "workspace_id": "workspace-replace-with-real-id",
      "languages": [
        { "id": "go", "extensions": [".go"] }
      ],
      "executable": "C:\\Tools\\gopls.exe",
      "arguments": ["serve"],
      "executable_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
      "request_timeout_ms": 15000,
      "reviewed_by": "operator-name",
      "reviewed_at": "2026-08-20T00:00:00Z"
    }
  ]
}
```

TypeScript Language Server 使用 Node 时，Node 是受 SHA-256 约束的 executable，CLI 和 `tsserver.path` 是描述符指纹的一部分：

```json
{
  "protocol_version": "code-intel-config.v1",
  "servers": [
    {
      "protocol_version": "code-intel-lsp.v1",
      "id": "typescript-main",
      "name": "TypeScript Language Server",
      "workspace_id": "workspace-replace-with-real-id",
      "languages": [
        { "id": "typescript", "extensions": [".ts", ".tsx"] }
      ],
      "executable": "C:\\Program Files\\nodejs\\node.exe",
      "arguments": [
        "C:\\PrayuTools\\typescript-language-server\\lib\\cli.mjs",
        "--stdio"
      ],
      "executable_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
      "initialization_options": {
        "tsserver": {
          "path": "C:\\PrayuTools\\typescript\\lib\\tsserver.js"
        }
      },
      "request_timeout_ms": 15000,
      "reviewed_by": "operator-name",
      "reviewed_at": "2026-08-20T00:00:00Z"
    }
  ]
}
```

对于解释器型 Server，executable hash 只证明 Node 本身；argv 和 initialization options 虽进入 descriptor fingerprint，但运行时不会为整个 JavaScript 依赖树建立内容证明。生产配置应把精确版本的 Server 和 TypeScript 包放在操作者管理的只读目录中，独立验证包完整性，并在任一文件变化后重新审查。需要完整依赖树隔离时应再叠加 OS Sandbox；本协议不宣称提供该隔离。

配置最多包含 32 个 Server。语言、扩展名、argv、初始化 JSON、审查身份、超时和重复 Workspace/Server identity 都经过严格有界校验。Server executable 必须是绝对路径下的非链接普通文件；资格检查和每次启动前都会重新计算摘要。

## CLI、API 与 Desktop

```powershell
# 只加载配置并显示 metadata；不启动 Server。
cyberagent code-intel status --config C:\Prayu\code-intel.json

# 只做 Workspace、审查和 executable hash 资格检查。
cyberagent code-intel qualify --config C:\Prayu\code-intel.json `
  --workspace demo --start=false --json

# 资格检查后真实 initialize，并显示协商后的 capability/generation。
cyberagent code-intel qualify --config C:\Prayu\code-intel.json `
  --workspace demo --json

# 普通 CLI/API 进程也可预先选择同一配置。
$env:CYBERAGENT_CODE_INTEL_CONFIG = "C:\Prayu\code-intel.json"
cyberagent api serve --code-intel-config C:\Prayu\code-intel.json

# Desktop 使用独立启动参数。
cyberagent-desktop --code-intel-config C:\Prayu\code-intel.json
```

认证只读 API 提供：

```text
GET /api/v1/code-intel
GET /api/v1/code-intel?workspace_id=<registered-workspace-id>
```

响应只投影 source label/hash、语言、health、Server version、capabilities、model-visible tools、generation、capability fingerprint、有界错误和资格布尔值。它从不返回 executable、argv、环境变量、凭证、原始 Server 日志或 Workspace 内容。`GET /api/v1/capabilities` 的 `code_intel_enabled` 只说明当前进程加载了显式配置，不是模型调用授权。

Desktop 设置页使用同一 API 显示 Server 和资格状态，不增加原生进程旁路。模型工具仍由每轮 Supervisor 使用当前 Run 和 Workspace 重新生成。

传入 `workspace_id` 时，Server inventory 与资格均只返回该精确 Workspace；未选择 Run 时 Desktop 才显示进程级全局 inventory。

## 证据和失效 / Evidence and invalidation

每个结果固定以下来源：

- Workspace ID、root fingerprint；
- Git repository availability、commit、branch、dirty 状态和 dirty digest；
- document URI、相对路径、SHA-256 和 LSP document version；
- Server ID、process generation 和 capability fingerprint；
- query fingerprint、稳定分页 cursor/offset、截断和 warning。

编辑文件、切换 branch/commit、改变 dirty 集合、换 Workspace root、重启或崩溃恢复 Server、改变协商 capability，都会使旧证据成为 `stale`。Server 不健康或运行时不存在时为 `unavailable`；Server 返回了被丢弃的越界项、fallback diagnostics、截断页或其他 warning 时为 `partial`。只有所有绑定仍相同才是 `current`。

`review@1.5.0` 和 `focused-checks@1.2.0` 会消费这些状态及 GitHub Review evidence graph，但语义 Server 结果始终只是 `semantic_language_server` 级证据，不是代码正确性证明、测试通过、审阅批准或写入授权。

## 生命周期与安全边界 / Lifecycle and trust boundary

- Go 负责 initialize、`didOpen`/`didChange`/`didClose`、shutdown/exit、请求取消、超时、崩溃重启和进程树清理。
- Windows 使用 kill-on-close Job Object；POSIX 使用独立 process group，并在 Linux 设置 parent-death signal。
- 环境只保留最小运行字段，覆盖临时 HOME/cache，关闭 Go/npm 网络解析，并不加载 Shell Profile 或传递凭证。
- 单帧、结果、日志、Markdown、link、diagnostic、hierarchy、页数和请求时间都有硬上限。
- Server 返回的每个 file URI 都重新通过 Workspace 路径策略；外部路径、链接/reparse point、大小写混淆、远程 URI 和非法转义被拒绝或降级为 partial。
- Markdown/link、控制字符和 secret-shaped 文本会被清洗；远程 link 不进入证据。

显式审查的 LSP Server 仍是本地用户权限下的真实进程。`network_access_granted=false`、去凭证环境和 `GOPROXY=off`/npm offline 是 Prayu 不授予网络意图的事实，不是 OS 防火墙或文件系统 Sandbox 的证明。不要把未知仓库提供的二进制、脚本或配置写入本配置；需要更强隔离时使用独立低权限账户、容器或其他 OS 级 Sandbox。

Git 来源绑定复用仓库的 exact-root 检查；带重定向 `.git` 文件的 linked worktree 当前会明确失败关闭，而不会悄悄省略 commit/branch 证明。脏状态摘要包含有界 Git index 与脏文件内容哈希；路径被清洗、状态被截断或内容超过绑定上限时，同样拒绝生成可能误标为 current 的语义证据。

## 验证 / Verification

普通测试使用协议级假 Server 覆盖崩溃、hang、cancel、oversize、非法 JSON-RPC、restart、进程回收、恶意 URI/link/secret 和稳定分页。CI 另在 Linux、macOS、Windows 安装固定版本的 `gopls v0.21.1`、`typescript-language-server 6.0.0` 和 `typescript 5.9.3`：gopls 必须协商并执行全部十项工具；TypeScript Server 必须执行其实际声明的九项工具，完整集合由两者共同覆盖。Linux 还运行 race/fault suite。
