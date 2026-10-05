# Issue #260：长会话同步与首屏加载

日期：2026-10-06。基线为已合入 #258、#265 的 `main` 提交
`f33ceebd9dca8dd81dfa3a560fa3efaafdc64e59`。本次沿用
[ADR 0134](adr/0134-unified-thread-transcript.md) 的只读 transcript、opaque cursor
和 Go 所有权；不增加依赖、协议、权限或 CI 门禁。

## 长会话

最新持久记录与手动历史分页使用独立查询。普通实时刷新只读最新窗口，断流积累超过
一页时沿服务端 cursor 补到已缓存的记录；历史页保留原 cursor、来源身份和排序。
若已加载的源页全部没有可见投影，则没有可见历史锚点：重新读取最新窗口并重设
空历史的分页起点，更多新旧记录仍由“加载更早记录”完整到达，不扫描未打开的历史。
同一投影身份的新版本替换旧版本，不能把排队消息、Run 状态和运行中命令摘要当作
永久不可变的历史。其定向同步由同一读取流程处理。

Desktop 的空事件页和重复页保持帧数组引用，仍推进服务端游标。Web SSE 在响应验证
成功时报告连接建立。普通事件刷新合并为一次读取，健康事件流下仅保留较慢的最新
记录兜底读取；断流、模型输出结束和 Run 终态继续同步最终持久记录。

流式文字仅投影活动尾部，稳定历史条目由 memo 行复用。较旧的重放快照仍按原有
排序完整投影；工具分组与来源引用保持。阅读锚点等待旧页真正插入后再调整，追加
尾部输出不会提前消费该锚点。

可复现的针对性验证：

```powershell
cd web
npm test -- src/v2/use-thread-transcript.test.tsx src/hooks/use-run-event-stream.test.tsx src/v2/projection/narrative.test.ts src/v2/components/narrative.test.tsx src/v2/components/conversation.test.tsx
```

- 同一 TanStack Query 测试夹具加载 10 页稳定历史后，旧 infinite query 的一次
  refetch 请求 10 页；新查询追加一条记录后请求 1 页。需要更新的可变来源另按其
  窗口读取，不把整个历史作为周期性刷新对象。
- 同样 10 页中，一个旧窗口同时含排队消息与运行中命令摘要时，同步为 2 请求
  （最新窗口 + 该来源窗口），不会为同窗口的两条记录重复请求；状态确认终结后
  再同步回到 1 请求。暂隐的排队消息继续按原身份跟踪，恢复或最终消失后再收束。
  若消息尚未取得固定历史 cursor 就已暂隐，恢复期间可能需要沿多个源页定位原
  位置；重新观察到该身份后复用其 cursor。这类当前态补读不保证恒为 2 请求。
- 一次新增 350 条记录时读取 4 个最新窗口补齐；跨空投影源页继续按 cursor 补齐。
- 1,000 条历史 Markdown 加 1 条活动输出，20 次更新只增加 20 次 Markdown 渲染；
  相同历史投影重新构造后仍复用，真正修改的历史内容正常更新。
- 覆盖重复身份、工具位置、Run 后继、读取失败保留、任务切换、阅读锚点、空/重复
  Desktop 页、SSE 重连、最终落盘，以及首次打开延迟模块的焦点和草稿保持。

## 首屏资源

设置、Inspector Home、Inspector 工具、Thread Inspector 和改动审阅为动态入口。
共享 `PlanDeliveryPanel` 独立于完整 Run 工作区；显示密度偏好独立于设置 UI；设置
与审阅 CSS 随相应模块加载。Monaco 编辑器引擎保留原动态加载，文件选择入口仍可
使用其 React 包装层。

两侧均使用 Node 24.14.0、同一 lockfile、Vite 8.2.1，执行：

```powershell
cd web
npm ci
npm run build -- --manifest
node scripts/measure-initial-assets.mjs dist
```

测量脚本递归累计入口的静态 `imports` 与 CSS，排除动态入口，用 Node `gzipSync`
统一计算每个资源的 gzip 字节后求和。不是只比较改名后的主 chunk；TTF、图片与
尚未打开的模块不计入 JS/CSS。基线用本次新增脚本读取同源基线构建目录。

| 初始静态资源 | 基线字节 | 修改后字节 | 降幅 |
| --- | ---: | ---: | ---: |
| JavaScript（全部初始 chunk） | 2,133,425 | 1,162,300 | 45.5% |
| JavaScript gzip | 572,314 | 319,637 | 44.2% |
| CSS | 346,421 | 287,738 | 16.9% |
| CSS gzip | 56,331 | 47,432 | 15.8% |

修改后的 JavaScript 闭包包括 `index`、`common`、`react` 与 `use-modal-focus-trap`
四个 chunk。Manifest 确认五个高级入口与 `monaco-local` 均为动态入口，且不在
初始静态闭包中；不是把主包大小转移到初始依赖后漏计。Vite 仍提示未打开的
Inspector 工具和 Monaco chunk 较大，本次没有宣称其首次打开的体积也已消除。

## 字体策略

四份 HarmonyOS Sans SC 官方完整 TTF 共 **33,721,860 字节**，保持原文件与字重，
不转换、不裁剪。现有 `font-display: swap`、按使用字重加载、无全字重 preload 与
静态资源 immutable 缓存继续适用。本轮只评估策略；尚无冷缓存实机证据支持新增
preload 或改为 optional，不能把 JS/CSS 的优化比例视为全部冷启动流量的降幅。

## 验证范围

沿用现有 TypeScript console CI：API 生成一致性、Vitest、TypeScript/Vite 构建、
Go Web bundle loader、Desktop embed 与依赖审计。针对性测试先覆盖受影响流程，
没有新增重复全量门禁或执行真实模型任务。

本地结果（Windows，Node 24.14.0，Go 1.26.5）：

- `npm run check:api`：生成类型无漂移，transcript 来源字段检查通过。
- `npm test -- --maxWorkers=4`：169 个文件、1,520 项测试全部通过。
- `npm run build -- --manifest`：TypeScript 检查与生产构建通过。
- `npm audit --audit-level=high`：0 个漏洞。
- `go test -count=1 ./internal/webui` 和 `go test -count=1 -tags desktop ./web`：通过。
- `go test -count=1 -tags "desktop,wv2runtime.error" ./cmd/cyberagent-desktop ./internal/desktop ./internal/webui`：通过。
- 与最新 main 的 CSS 语义对照：1,017 条 selector/声明/条件上下文记录一致，
  同 selector 内顺序不变；保留 #265 的模型接入行为与固定操作栏。

现有 CI 继续使用其固定的 Go 1.25.x；本次没有在本地重跑未改动的完整 Go/Rust 门禁。
