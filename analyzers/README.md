# CyberAgent Deterministic Analyzers

## 中文

此目录承载由 Go 控制平面定义协议、由 Rust 实现的确定性分析工具。Rust 不是 Agent，
不管理 Run、Session、LLM、用户配置、API key、审批、Docker、网络或持久化。

当前 `cyberagent-analyzer-fixture` 实现 `analyzer_protocol.v1` 的两项开发期纯函数：

- 从 stdin 读取最多 96 KiB 的严格 JSON；
- 只接受 Base64 内联输入，不接受路径、URL 或命令；
- filesystem、network、subprocess、environment 四类能力必须全部为 `false`；
- 向 stdout 输出最多 16 KiB 的 metadata-only 结果或稳定错误信封；
- `fixture.digest.v1` 不返回原始内容，只返回媒体类型、字节数、SHA-256、UTF-8 与逻辑行数；
- `archive.zip.inventory.v1` 只遍历内存 ZIP 的中央目录，不打开条目正文、不解压、不写文件；
- 产品入口只允许 Go 调用构建时内嵌且摘要固定的 `fixture.digest.v1`；
  `archive.zip.inventory.v1` 继续仅用于协议/测试符合性。

Go 的只读 `analyzer_descriptor.v1` Registry 目前固定两项 descriptor，且没有动态注册、
executable、command、path 或 starter 字段。ZIP inventory 最多接受 32 个条目、单个 128 字节
名称和合计 2 KiB 名称；8 MiB 单项声明尺寸、32 MiB 合计声明尺寸和 100:1 压缩比属于明确风险
阈值，不会触发解压。路径穿越、绝对路径、反斜杠、重名、目录携带数据及尺寸/压缩比异常都以
排序后的稳定风险码返回。

Go 的历史 `analyzer_invocation.v1` 与 Disabled/Fake bridge 仍保留为失败语义和兼容测试边界；它们
不会被产品入口选作执行后端，也不能扩大真实执行权限。

P10-K 历史阶段选定 `wasm32-wasip1` 与 `wazero v1.12.0` Interpreter，并先完成 compile-only
import/export/签名/memory 评估。P10-L/M 已在该边界上完成固定模块产品闭环：Go 每次创建全新
Interpreter/WASI/guest，以有界内存 stdio、空环境、合成 argv、确定性随机源和 deadline 执行构建时
内嵌模块；不挂载文件系统、不开放网络、不启动子进程或 native process，也不接受调用方模块。
schema v94 的 capability 协议上限五分钟，当前产品服务签发两分钟票据，并且一次性、精确绑定、
原子消费；schema v95 原子提交脱敏
execution、只含元数据摘要 JSON 的 Artifact 与 Run events。CLI、control-token HTTP、Desktop/React
只能调用固定 `fixture.digest.v1`，并要求 Go 定义的显式确认。Rust 仍不拥有 Run、授权或持久化。

Rust 固定使用 `rawzip = 0.5.1` 读取中央目录记录。该 crate 为 MIT 许可、无传递依赖，并在源码
中 `forbid(unsafe_code)`。Rust 不调用本地文件 API；ZIP 字节仍只来自 Go-owned 请求中的 Base64
内联内容。

Go 和 Rust 分别读取
[`testdata/analyzer_protocol_v1_vectors.json`](testdata/analyzer_protocol_v1_vectors.json)，
独立校验版本、限制、错误码、退出码、JSON 语义及 stdout 的精确 bytes/SHA-256。
两种语言还分别读取
[`testdata/archive_inventory_v1_vectors.json`](testdata/archive_inventory_v1_vectors.json)，
校验正常包、路径穿越、重名、声明尺寸过大和压缩比异常五组固定 ZIP 输入及结果。

```powershell
$env:PATH = "$env:USERPROFILE\.cargo\bin;$env:PATH"
Set-Location analyzers
cargo fmt --all -- --check
cargo test --locked
cargo clippy --locked --all-targets -- -D warnings
Set-Location ..
bash ./scripts/build-embedded-wasi.sh
```

构建脚本固定 Linux x86_64 与 Rust 1.97.1，并在编译前把 checkout 与 Cargo registry 路径映射为
稳定虚拟路径。其他宿主会明确拒绝发布构建；不要用裸 Cargo release 命令替代它，否则 WASI 会
嵌入宿主路径或宿主相关 crate identity，导致二进制摘要漂移。CI 漂移检查会短期保存 canonical
重建件，供维护者复核并更新内嵌模块。

## English

This directory contains deterministic Rust analyzers behind a Go-owned protocol. Rust is
not an Agent and does not own Runs, Sessions, LLM calls, user configuration, API keys,
approvals, Docker, networking, or persistence.

The current `cyberagent-analyzer-fixture` implements the pinned
`analyzer_protocol.v1` reference with `fixture.digest.v1` and
`archive.zip.inventory.v1`. The latter uses pinned `rawzip 0.5.1` to iterate only an
in-memory ZIP central directory. It never opens entry content, decompresses, extracts,
or writes a file. Entry count, name bytes, declared sizes, integer compression ratio,
and path risks are bounded and deterministic.

Go owns a fixed `analyzer_descriptor.v1` Registry with no dynamic registration,
executable, command, path, or starter field. Go and Rust independently validate both
versioned golden-vector files, including five fixed ZIP inputs plus exact output JSON
bytes and SHA-256. The product route admits only the build-embedded, digest-pinned
`fixture.digest.v1`; ZIP inventory remains a protocol/test conformance function.

The historical non-starting `analyzer_invocation.v1` and package-sealed Disabled/Fake
bridge remain as compatibility and failure-semantics tests. The product route does not
select either bridge as its execution backend and they grant no authority.

P10-K first compiled and assessed the pinned `wasm32-wasip1` module without
instantiating it. P10-L/M now execute only that build-embedded module in a fresh
wazero Interpreter/WASI/guest with bounded memory stdio, empty environment,
synthetic argv, deterministic randomness, deadline/cancellation close, and no
filesystem, network, subprocess, native process, or caller-supplied module.
Schema v94 provides an exact-bound, one-shot, atomically consumed capability;
schema v95 atomically records redacted execution, metadata-summary Artifact,
and Run events. CLI, control-token HTTP, and Desktop/React expose only the fixed
digest analyzer under explicit Go confirmation. Rust still owns no Agent,
authorization, Run lifecycle, credential, or persistence behavior.

Build the product fixture from the repository root on Linux x86_64 with
`bash ./scripts/build-embedded-wasi.sh`. The script pins Rust 1.97.1 and remaps
checkout and Cargo registry paths before compiling. Other hosts fail closed: a
raw Cargo release build can embed host paths or host-specific crate identities
and produce a different module digest. On drift, CI retains the canonical
rebuilt module briefly so maintainers can review and update the embedded copy.
