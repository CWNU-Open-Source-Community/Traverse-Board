# D 盘 UI 测试与视觉基线

`scripts/ui-test.ps1` 是 UI 测试的统一进程级入口。它只在脚本运行期间设置环境变量，
退出时恢复原值，不修改 `HOME`、`USERPROFILE`、`CODEX_HOME` 或任何永久用户配置。
脚本拒绝从 D 盘以外的仓库运行，并把受控缓存和产物放在当前 worktree：

- `.tmp/ui-test-runtime/`：`TEMP`/`TMP`、npm、Go、Playwright browser、WebView2
  `APPDATA`/`LOCALAPPDATA` 与 `CYBERAGENT_HOME`；
- `.tmp/reference-baselines/`：哈希验证后的临时权威截图；
- `output/playwright/`：actual、diff、metrics 和后续 Playwright artifacts。

`.tmp/` 与 `output/playwright/` 已被仓库忽略。不要强制添加其中的 PNG、数据库、
浏览器 Profile 或日志。

入口还会在重定向后的 D 盘 `APPDATA` 中执行 `go telemetry off`。这是临时 profile
内的设置，不读取或修改用户原有的 Go telemetry 配置；`GOTELEMETRYDIR` 也会在入口
自检中确认没有逃出 `.tmp/ui-test-runtime/`。

## 权威截图

仓库只保存 `scripts/ui-reference-baselines.json` 中的名称、尺寸和 SHA-256，不保存图片。
暂存动作从当前 Windows 用户的 Local Temp 读取以下两个精确文件：

| case | 临时源文件 | 尺寸 | SHA-256 |
|---|---|---:|---|
| `main-workbench` | `codex-clipboard-825b95c6-b835-4a58-b404-b8e397247936.png` | 2052×1371 | `2db81d5ac7b2d253d630d81fda4e3d4d9e87c92bbe0a17b3ae9f4db194ce6d55` |
| `settings` | `codex-clipboard-b606aee7-6146-4a87-b708-fe25818cd7be.png` | 2050×1357 | `a301e480d370ce5ed9f752dabbf594a64d317843e8f2efe7482d2509ea8c20c9` |

源文件缺失、是 reparse point、尺寸不符或任一字节变化时，暂存立即失败。已有的 D 盘
暂存文件如果哈希漂移也不会被静默覆盖。

```powershell
# 校验并暂存两张图片；不运行测试
./scripts/ui-test.ps1 -Action stage

# 暂存图片并运行像素工具单测
./scripts/ui-test.ps1 -Action verify
```

## 在隔离环境运行任意测试

`command` 动作让子进程继承 D 盘环境，脚本结束后恢复调用者环境：

```powershell
# 前端单测
./scripts/ui-test.ps1 -Action command -Executable npm `
  -ArgumentList @("--prefix", "web", "test")

# 前端生产构建
./scripts/ui-test.ps1 -Action command -Executable npm `
  -ArgumentList @("--prefix", "web", "run", "build")

# Go 测试
./scripts/ui-test.ps1 -Action command -Executable go `
  -ArgumentList @("test", "-count=1", "./internal/domain", "./internal/store")
```

Playwright 配置应读取 `TRAVERSE_PLAYWRIGHT_OUTPUT_DIR`，不能依赖其默认相对目录。
浏览器安装位置由 `PLAYWRIGHT_BROWSERS_PATH` 固定在 D 盘。Wails 当前未显式设置
`WebviewUserDataPath` 时会使用 `%APPDATA%`；本入口为测试子进程把 `APPDATA` 和
`LOCALAPPDATA` 临时定向到 `.tmp/ui-test-runtime/appdata/`，避免 WebView2 Profile
落到 C 盘。

## 像素差

`cmd/visualdiff` 只使用 Go 标准库。每次比较生成：

- `<case>-actual.png`：实际输入的逐字节副本；
- `<case>-diff.png`：红色表示超过 channel threshold 的变化，蓝色表示 mask，
  紫色表示 actual 超出 baseline 的尺寸区域；
- `<case>-metrics.json`：输入哈希/尺寸、有效 ROI/mask、阈值、像素数量、差异比例、
  MAE、RMSE、最大通道差和稳定失败原因。

两个 baseline 各自保留原始尺寸，不缩放、不裁成共同画布。输入尺寸不一致仍会写出
结构化 diff 与 metrics，但结果必定包含 `dimension_mismatch` 并失败。

矩形格式为 `x,y,width,height`，坐标使用 baseline 的物理像素。`-Roi` 和 `-Mask`
可重复。未传 ROI 时比较全图；mask 只从已选 ROI 中剔除像素。

```powershell
./scripts/ui-test.ps1 -Action compare `
  -BaselineCase main-workbench `
  -ActualPath output/playwright/main-capture.png `
  -Roi @("0,0,2052,1371") `
  -Mask @("650,120,1180,820", "20,1260,360,90") `
  -ChannelThreshold 8 `
  -MaxDiffRatio 0.02 `
  -MaxMeanAbsoluteError 2.0

./scripts/ui-test.ps1 -Action compare `
  -BaselineCase settings `
  -ActualPath output/playwright/settings-capture.png
```

清单中的默认阈值全部为零。任何非零容差或 mask 都必须在具体测试调用中显式出现，
并经过截图复核后再固化，避免动态内容遮罩悄悄扩大。

## 自检

```powershell
# Go 像素算法/产物测试
./scripts/ui-test.ps1 -Action self-test

# PowerShell 入口的 D 盘绑定与受保护环境变量恢复测试
pwsh -NoProfile -File scripts/tests/ui-test-entry.tests.ps1
```

真实 UI 验收仍应拆成两层：浏览器/Go API 测试验证输入、浮层、归档和权限的真实状态
变化；固定 Windows 主题、DPI、背景与窗口尺寸的 Wails 捕获验证 Acrylic。普通浏览器
截图不能证明原生 Acrylic 正确，动态会话正文、头像、用户路径和加载状态应通过显式
mask 处理，不能靠提高全图阈值掩盖。
