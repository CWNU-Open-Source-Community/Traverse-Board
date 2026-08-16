# Prayu Windows Desktop 测试矩阵 / Windows Desktop Test Matrix

版本 / Version: desktop-test-matrix.v1

状态 / Status: 就绪，待实机证据 / ready, awaiting on-host evidence

对应 Issue / Issue: #55

## 目标 / Goal

在正式便携或签名发行前，用可复现的脚本 + 人工 checklist 覆盖 Windows 10/11、WebView2 runtime、DPI/多显示器、第二实例与强制结束/重开，并把失败分类为产品 bug、环境不支持或发布 blocker。本文档是版本化矩阵的单一事实来源，脚本与判定由仓库提供，实机启动与脱敏证据由人工配合补齐。

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
| `scripts/desktop-test-matrix.ps1` | 采集环境（OS/WebView2/DPI/显示器）、跑冷启动/第二实例/kill-重开、脱敏并输出 JSON 记录 |
| `scripts/smoke-desktop-operator-preview.ps1` | 既有 operator-preview 冷启动烟测（隔离数据目录 + store 创建） |
| `scripts/check-windows-compat.ps1` | 既有便携产物兼容性检查（SHA-256/PE/launcher/guide） |

## 场景与判定 / Scenarios and pass criteria

| # | 场景 | 通过判据 | 证据 |
|---|---|---|---|
| S1 | 冷启动 | 进程存活、创建本地 store、主窗口渲染非空白 | 脚本记录 + 脱敏截图 |
| S2 | 第二实例 | 第二实例把参数/工作目录让位给主实例，自身退出（不产生第二个窗口） | 脚本记录 PID + 退出码 |
| S3 | 正常退出 | 关闭窗口 → 进程退出 → 退出后 `PRAGMA integrity_check` = ok | 脚本记录 + integrity_check |
| S4 | 强制 kill 后重开 | `Stop-Process -Force` → 重开成功 → 本地数据保留（Run/设置仍在） | 脚本记录 + 重开后 store 存在 |
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
- [ ] 审批队列
- [ ] 设置页（含"高度敏感权限"Full CDP 标签）

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

1. 构建候选 EXE（`scripts/build-desktop.ps1`）。
2. 跑 `scripts/check-windows-compat.ps1` 与 `scripts/desktop-test-matrix.ps1`。
3. 按 UI checklist 在 100/200% DPI、单/多显示器上人工核验并附脱敏截图。
4. 把 JSON 记录 + 脱敏截图 + 版本信息附到 Issue。
