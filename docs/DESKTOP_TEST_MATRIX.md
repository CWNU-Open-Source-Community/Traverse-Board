# Traverse Board · 针路簿 Windows Desktop 测试矩阵 / Windows Desktop Test Matrix

版本 / Version: desktop-test-matrix.v2

状态 / Status: #85 已用同一 clean r4 SHA 完成 Windows 11 双屏混合 DPI 与原始交互范围签字；更广的 Windows/高级 Git 矩阵仍按各自 Issue 跟踪 / issue #85 passed its original Windows 11 mixed-DPI and interactive scope against one clean r4 SHA; the broader Windows and Advanced Git matrix remains tracked separately

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
- [ ] 高级 Git：capability/permission/lease generation、仓库/分支/upstream、Checkpoint 限制均可见且不显示 host path/private lease
- [ ] Hunk：发现 patch、逐项勾选、漂移后旧 hunk 禁用；revert 必须明显标为 destructive
- [ ] Stash：tracked/index/untracked 角色、exact OID、apply/pop/drop、冲突时原 stash 保留
- [ ] Rebase/cherry-pick：base/ours/theirs 文件、continue/skip/abort 与持久 sequence ID；保护/共享分支显示明确拒绝
- [ ] Bisect：exact current commit、good/bad/skip、固定 recipe/step/timeout、reset 恢复原引用
- [ ] 受管 Worktree：create/lock/unlock/remove/prune 只显示逻辑名称/ID；locked、dirty 或 head drift 禁止删除
- [ ] Approval/审计：preview 不授权，review 后 pending Approval 可见；成功/冲突/中断 receipt 与 Checkpoint 可回读
- [ ] 审批队列
- [ ] 设置页（含"高度敏感权限"Full CDP 标签）

每次人工勾选必须同时记录候选 `sha256`、OS build、WebView2 版本、各显示器分辨率/DPI、窗口尺寸和证据文件名。未实际执行的项写 `not_run`，不得留空或按自动测试结果推断为通过。

高级 Git 项还必须分别用 capability 关闭和完整开启的两个隔离数据目录验证：关闭时面板与 mutation endpoint 不可用；开启时若 permission control、operator Approval、Approval control 或 Workspace Checkpoint control 任一缺失，Desktop 必须在组合阶段失败关闭。强制 kill 场景应停在已经 CAS 为 `running` 的冲突序列或 worktree create 后，重开只允许观察并生成 `interrupted|conflicted` 收据，不得重复应用提交；若精确 worktree 被保守登记，旧操作仍不得显示为成功。截图和 JSON 不得包含绝对受管路径、private lease ID、raw spec/preview/receipt JSON 或 Git argv。

### #85 本机正式签字 / Local #85 formal sign-off

环境：Windows 11 Pro build `26200`、WebView2 `151.0.4129.93`、Extend；主屏 2560×1600 / 150%，外接屏 3840×2160 / 200%。正式候选为 clean revision `c531bd2ed96b9c9f5210ab86358c15d647b5e984`，版本 `v0.1.0-issue85-r4`，SHA-256 `9b42e3e57b85954d3add54aa18d8a795738253b60f1c2370594956a9fe957dce`，`modified=false`，可复现构建。完整机器可读结论见 `docs/evidence/issue85/result-r4.json`。

在 150% 主屏上通过 Windows 原生“大小”操作把候选窗口精确调整为 1024×768 logical pixels，并在该尺寸下逐项复核主题、语言、侧栏、输入框、长对话/Markdown、Live Activity、Approvals、设置与 Checkpoints。另一次连续会话把窗口从 150% 主屏移动到 200% 外接屏，再返回 150% 主屏，全程未重启候选。

| #85 原始人工项 | r4 clean | 证据摘要 |
|---|---|---|
| 主题（浅/深）与 Acrylic | `pass` | 三种外观均实际切换并截图 |
| 语言切换（中/英） | `pass` | 中英文界面均实际观察 |
| 侧栏拖拽/折叠 | `pass` | resized 与 collapsed 状态均无覆盖 |
| Agent 输入框 | `pass` | 200% 输入可见；跨回 150% 后焦点与草稿仍在；精确 1024×768 下输入未提交草稿后清空 |
| 长对话滚动与 Markdown | `pass` | 标题、列表、引用、表格与 Go 代码块在两屏均正常 |
| Live Activity / 事件流 | `pass` | 150%、200% 与精确 1024×768 均正常 |
| Workspace Checkpoints | `pass` | COMPLETE 时间线、Rewind 预览/显式确认/结果、独立 Fork Run 均完成，1024×768 下页签可横向滚动且结果可读 |
| 审批队列 | `pass` | pending、fixed 与 host 分区均可读 |
| 设置页与 Full CDP 高敏标签 | `pass` | 150%/200%/跨回布局正常，Full CDP 保持高风险禁用 |
| 混合 DPI / 跨屏布局 | `pass` | 150% → 200% → 150%，无重叠、截断、失焦、空白或异常缩放 |

Issue #85 创建时未包含后来由 #117 / PR #120 加入的高级 Git 行；本段不对那些独立范围作重复签字。测试结束后保留 Extend，Display 1 仍为主屏，两块屏幕均恢复 150%，候选与 Settings 窗口已退出。

## 证据状态 / Evidence status

- `docs/desktop-test-matrix-windows11-evidence.json` 是 PR #79 的历史 v1 证据，只证明 Windows 11 / 150% / 单屏上的四个自动场景。
- `docs/desktop-test-matrix-windows11-v2-evidence.json` 绑定 clean commit、候选 SHA-256 和逐屏 DPI；当前记录在 Windows 11 / 150% / 单屏上通过 provenance 与四项自动场景，但 `overall_status` 仍为 `needs_manual_evidence`。
- `docs/evidence/issue55/result-r4.json` 是最终 r4 候选清单；同目录四份自动报告覆盖 Windows 10 的 100/125/200% 与 Windows 11 的 150%，并绑定 clean revision、SHA-256 和 WebView2 `151.0.4129.86`。
- `docs/evidence/issue85/result-dev.json` 保留 #85 的 r1/r2-dev/r3-dev 调查历史；`docs/evidence/issue85/result-r4.json` 绑定同一 clean r4 revision、候选 SHA、自动矩阵与全部 #85 原始人工项，并给出正式 `pass`。
- r4 脱敏截图覆盖 1024×768 的 100/200% 单屏 shell、离线启动，以及 WebView2 缺失、`93.0.1.0` 过旧和 malformed-DLL 损坏诊断。所有场景均使用同一 r4 SHA-256。
- v2 自动报告仍把人工项写成 `not_run`；候选清单只把实际观察到的核心 shell 与异常场景记为 `pass`。
- 原先受单显示器/输入条件限制的 #85 场景已在双物理屏上实际执行；r4 结果没有拼接 r1/r2-dev/r3-dev 的跨 SHA 结果。
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
