# Prayu Windows Desktop 测试矩阵 / Windows Desktop Test Matrix

版本 / Version: desktop-test-matrix.v2

状态 / Status: r4 单屏自动/核心可视证据完成；双屏与完整交互签字由 #85 跟踪 / r4 single-monitor automation and core visual evidence complete; mixed-display and full interaction sign-off tracked by #85

对应 Issue / Issue: #55

## 目标 / Goal

在正式便携或签名发行前，用可复现的脚本 + 人工 checklist 覆盖 Windows 10/11、WebView2 runtime、DPI/多显示器、第二实例与强制结束/重开，并把失败分类为产品 bug、环境不支持或发布 blocker。本文档是版本化矩阵的单一事实来源，脚本与判定由仓库提供，实机启动与脱敏证据由人工配合补齐。自动失败会令 `overall_status=fail`；自动通过仍只得到 `overall_status=needs_manual_evidence`，不会把单机 smoke 误写成整个矩阵通过。

## 支持矩阵 / Supported matrix

| 维度 | 支持范围 |
|---|---|
| 操作系统 | Windows 10 x64（19044+）、Windows 11 x64（22000+） |
| WebView2 Evergreen Runtime | `94.0.992.31` 或更新（`cmd/cyberagent-desktop/main_windows.go` 的 `minimumWebView2RuntimeVersion`） |
| DPI 缩放 | 100% / 125% / 150% / 200% |
| 显示器 | 单显示器（最小 1024×768 窗口）与多显示器（不同缩放 + 窗口跨屏移动） |
| 数据目录 | 默认 `%APPDATA%` 之外，测试时可用 `CYBERAGENT_HOME` 指向隔离目录 |

不支持项：不要求控制用户其他应用/浏览器；不把单一开发机结果作为全部 Windows 支持证明。

## 自动化脚本 / Automated scripts

| 脚本 | 用途 |
|---|---|
| `scripts/desktop-test-matrix.ps1` | 校验候选 EXE 的 SHA-256/版本/commit/clean-build provenance，采集 OS、WebView2、逐屏 DPI/边界，跑冷启动/第二实例/正常退出/kill-重开，并输出脱敏 JSON |
| `scripts/smoke-desktop-operator-preview.ps1` | 既有 operator-preview 冷启动烟测（隔离数据目录 + store 创建） |
| `scripts/check-windows-compat.ps1` | 既有便携产物兼容性检查（SHA-256/PE/launcher/guide） |

## 场景与判定 / Scenarios and pass criteria

| # | 场景 | 通过判据 | 证据 |
|---|---|---|---|
| S1 | 冷启动 | 进程存活、创建本地 store；主窗口非空白由人工截图确认 | 脚本记录 + 脱敏截图 |
| S2 | 第二实例 | 第二实例把参数/工作目录让位给主实例，自身以退出码 0 退出（不产生第二个窗口） | 脚本结构化记录 + 人工窗口确认 |
| S3 | 正常退出 | 关闭窗口 → 进程以退出码 0 退出 → store 仍存在 | 脚本结构化记录 |
| S4 | 强制 kill 后重开 | `Stop-Process -Force` → 非空 store 保留 → 同一候选 EXE 重开成功；Run/设置内容由人工确认 | 脚本记录 + 重开后 UI 确认 |
| S5 | WebView2 缺失/过旧/损坏 | 只显示有界本机指导（`desktopWebView2Messages`），无空白/Forbidden，不隐式安装 | 脚本记录诊断文本 + 截图 |
| S6 | 离线启动 | 断网下冷启动成功（不依赖外部网络） | 脚本记录 + 截图 |

## UI checklist（人工）/ Manual UI checklist

在最小窗口（1024×768）与 200% DPI 两种配置下逐项确认不重叠、不截断：

- [ ] 主题（浅/深）与 Acrylic 玻璃令牌
- [ ] 语言切换（双语）
- [ ] 侧栏拖拽/折叠
- [ ] Agent 输入框
- [ ] 长对话滚动
- [ ] Markdown 渲染
- [ ] Live Activity / 事件流
- [ ] 工作区检查点：时间线来源/恢复等级、Rewind 三方预览、冲突禁用、显式确认、Fork 后切换独立 Run
- [ ] 审批队列
- [ ] 设置页（含"高度敏感权限"Full CDP 标签）

每次人工勾选必须同时记录候选 `sha256`、OS build、WebView2 版本、各显示器分辨率/DPI、窗口尺寸和证据文件名。未实际执行的项写 `not_run`，不得留空或按自动测试结果推断为通过。

## 证据状态 / Evidence status

- `docs/desktop-test-matrix-windows11-evidence.json` 是 PR #79 的历史 v1 证据，只证明 Windows 11 / 150% / 单屏上的四个自动场景。
- `docs/desktop-test-matrix-windows11-v2-evidence.json` 绑定 clean commit、候选 SHA-256 和逐屏 DPI；当前记录在 Windows 11 / 150% / 单屏上通过 provenance 与四项自动场景，但 `overall_status` 仍为 `needs_manual_evidence`。
- `docs/evidence/issue55/result-r4.json` 是最终 r4 候选清单；同目录四份自动报告覆盖 Windows 10 的 100/125/200% 与 Windows 11 的 150%，并绑定 clean revision、SHA-256 和 WebView2 `151.0.4129.86`。
- r4 脱敏截图覆盖 1024×768 的 100/200% 单屏 shell、离线启动，以及 WebView2 缺失、`93.0.1.0` 过旧和 malformed-DLL 损坏诊断。所有场景均使用同一 r4 SHA-256。
- v2 自动报告仍把人工项写成 `not_run`；候选清单只把实际观察到的核心 shell 与异常场景记为 `pass`。
- 多显示器混合 DPI 与完整交互式 UI checklist 受当前单显示器/输入条件限制，明确保留为 `blocked`/`not_run`，并由 #85 跟踪；不能用 Playwright、CSS 缩放或单屏结果替代。
- 所有必需单元格为 `pass` 且无未关闭 blocker 后，才可把 Issue #55 判定为完成。

## 脱敏 / Sanitization

测试日志与截图不得包含：

- API key / control token / 凭证
- 路径中的个人秘密（`%USERPROFILE%`、用户名、Workspace 真实路径）
- 聊天内容 / 模型输出正文

脚本输出中的 `CYBERAGENT_HOME` 一律以隔离临时目录替换，不落盘真实主目录。

## 失败分类 / Failure classification

| 分类 | 定义 | 处置 |
|---|---|---|
| 产品 bug | 在支持矩阵内可复现、违反明确契约 | 独立 Issue，标 `bug` |
| 环境不支持 | 矩阵外 OS/runtime/驱动 | 在本 Issue 记录，不阻塞 |
| 发布 blocker | 矩阵内、影响正式发行 | 独立 Issue，标 `release-blocker`，发行前必须关闭 |

## 复现 / Reproduction

1. 从待发布 commit 构建候选 EXE：`pwsh -File scripts/build-desktop.ps1 -Version <semver> -VerifyReproducible`。
2. 运行自动矩阵：`pwsh -File scripts/desktop-test-matrix.ps1 -BinaryPath build/desktop/cyberagent-desktop.exe`；确认 `automated_status=pass`，并保留 `overall_status=needs_manual_evidence` 直到人工项完成。
3. 按 UI checklist 在 100/200% DPI、单/多显示器上人工核验并附脱敏截图；在隔离 VM 中执行 WebView2 异常和离线场景。
4. 把每台主机的 JSON、脱敏截图和结果表附到 Issue；证据中的 SHA-256 必须与候选产物一致。
