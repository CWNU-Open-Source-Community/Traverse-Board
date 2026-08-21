# Security Policy / 安全政策

Traverse Board is a local-first, recoverable, and auditable Agent runtime. Its security
model depends on keeping model output, repository content, Skills, Analyzer
results, and tool output untrusted until the Go control plane has applied the
relevant scope, policy, approval, budget, and execution gates.

针路簿是一个本地优先、可恢复、可审计的 Agent 运行时。它的安全模型要求模型输出、
仓库内容、Skill、Analyzer 结果和工具输出始终是不可信证据，直到 Go 控制平面完成
Scope、Policy、人工审批、预算和执行门禁检查。

## Supported versions / 受支持版本

Traverse Board has not made a stable release. There are currently no supported release
lines or guaranteed backports, and the Windows desktop artifact is an unsigned
development/portable-test build with `release_ready=false`.

Security fixes are developed against the current `main` branch. Historical
commits, locally modified builds, forks, and third-party packages are not
maintained by this project. When reporting a problem, identify the exact commit
and build you tested so the maintainers can determine whether current `main` is
affected.

针路簿尚未发布稳定版本，目前没有承诺维护的发行分支或回移计划。Windows 桌面产物
仍是未签名的开发/便携测试构建，且 `release_ready=false`。安全修复以当前 `main`
分支为目标；项目不维护历史提交、本地修改构建、Fork 或第三方打包。报告时请提供实际
测试的提交和构建标识。

## Reporting a vulnerability / 报告漏洞

Do **not** open a public Issue, Discussion, pull request, or social-media post
for a suspected vulnerability.

GitHub Private Vulnerability Reporting is not currently enabled for this
repository, so a private advisory submission form is not presently available.
Check the repository's [Security page][security-page]; if a **Report a
vulnerability** button is available when you report, use that private form.
Otherwise, email the maintainer at [qiiiqiyuan@gmail.com][security-email] with
the subject `Traverse Board security report`. If you cannot safely send the details by
email, send only a request to establish a safer private channel.

请**不要**通过公开 Issue、Discussion、Pull Request 或社交媒体披露疑似漏洞。

本仓库当前未启用 GitHub Private Vulnerability Reporting，因此暂无可用的私密
Advisory 提交表单。报告时请先检查仓库 [Security 页面][security-page]；如果已出现
**Report a vulnerability** 按钮，请优先使用该私密表单。否则，请以
`Traverse Board security report` 为主题发送邮件至 [qiiiqiyuan@gmail.com][security-email]。
如果不适合通过邮件安全传送细节，请只发送建立更安全私密沟通渠道的请求。

Include, where available:

- the affected commit, build, operating system, and installation method;
- the required flags, capability settings, permissions, and other
  preconditions, without including their secret values;
- the security boundary you expected and the behavior you observed;
- a minimal, deterministic reproduction using synthetic data;
- the potential confidentiality, integrity, availability, authorization, or
  audit impact;
- small, redacted evidence excerpts and any suggested mitigation; and
- whether the report has been shared with anyone else.

报告应尽可能包含：受影响的提交/构建、操作系统和安装方式；所需开关、能力设置、
权限与其他前置条件（不含机密值）；预期边界和实际行为；使用合成数据的最小可复现
步骤；对机密性、完整性、可用性、授权或审计性的影响；经过脱敏的最小证据和建议缓解方案；
以及是否已向其他人披露。

The maintainers will confirm receipt when possible, validate and prioritize the
report, and coordinate remediation and disclosure. This project does not
currently promise a response or remediation SLA, and it does not operate a bug
bounty program. Please allow a reasonable private remediation period before any
public disclosure.

维护者会在可能时确认收到、复核并排序问题，与报告者协调修复和披露。本项目目前不承诺响应
或修复 SLA，也没有漏洞赏金计划。请在公开披露前预留合理的私密修复时间。

## Safe reproduction and sensitive data / 安全复现与敏感数据

Test only systems, accounts, workspaces, and network targets that you own or
are explicitly authorized to test. Prefer the built-in mock Provider, a
disposable data directory and workspace, synthetic artifacts, and an isolated
VM. Use the least authority necessary. Do not enable additional execution
capabilities, contact a real target, persist on a machine, or exfiltrate data
merely to prove impact. Stop once the boundary failure is demonstrated.

仅测试你拥有或获得明确授权的系统、账户、Workspace 和网络目标。优先使用内置 Mock
Provider、一次性数据目录和 Workspace、合成 Artifact 与隔离虚拟机，并只使用最小必要
权限。不得仅为证明影响而开启执行边界、访问真实目标、在机器上建立持久化或导出数据。
一旦证明边界失效，立即停止。

Never include unredacted sensitive data in a report, attachment, screenshot,
test fixture, commit, or pull request. In particular, do not share:

- API keys, bearer/control tokens, passwords, cookies, session tokens, private
  keys, environment values, or Windows Credential Manager contents;
- Traverse Board SQLite databases or their `-wal`/`-shm` files;
- absolute local paths, usernames, home/data-directory names, repository
  contents, customer data, target data, or personal information; or
- raw Provider requests, responses, streaming events, prompts, model thinking,
  Tool arguments/output, or Provider error payloads.

不得在报告、附件、截图、测试 Fixture、提交或 PR 中包含未脱敏的敏感数据，尤其是：

- API Key、Bearer/Control Token、密码、Cookie、Session Token、私钥、环境变量值或
  Windows Credential Manager 内容；
- 针路簿 SQLite 数据库及其 `-wal`/`-shm` 文件；
- 本地绝对路径、用户名、Home/数据目录名、仓库内容、客户数据、目标数据或个人信息；
- Provider 的原始请求、响应、流事件、Prompt、模型 Thinking、Tool 参数/输出或原始
  Provider 错误 Payload。

Replace secrets and identifying values with stable placeholders, trim evidence
to the minimum needed, and prefer hashes or bounded redacted excerpts. If
sensitive data was exposed accidentally, revoke or rotate it first and tell the
maintainer privately what category of data was involved; do not repeat the
secret in the follow-up.

请用稳定占位符替换机密值和身份标识，把证据缩减到最小，优先提供 Hash 或有界的脱敏
摘要。如果已意外暴露敏感数据，请先撤销或轮换，再私下告知维护者涉及的数据类别；不要在
后续消息中重复该机密值。

## Security issue or normal bug? / 安全问题还是普通 Bug？

Report privately when a problem could cross or weaken a security boundary, for
example:

- bypassing scope, policy, approval, budget, lease, capability, or
  authentication checks;
- unauthorized process/network access, filesystem mutation, secret access, or
  sandbox/Analyzer/Skill isolation escape;
- disclosure of credentials, private model/Provider data, sensitive local
  state, or insufficiently redacted audit/API/UI output;
- tampering with durable audit facts, receipts, evidence, or recovery semantics;
  or
- a denial of service that crosses an authority boundary or reliably damages
  durable state.

如果问题可能跨越或削弱安全边界，请私下报告。例如：绕过 Scope、Policy、审批、预算、
Lease、Capability 或身份验证；未授权的进程/网络访问、文件系统变更、机密访问或 Sandbox/
Analyzer/Skill 隔离逃逸；泄露凭据、私密模型/Provider 数据或本地状态；篡改审计事实、
Receipt、Evidence 或恢复语义；以及跨越授权边界或可靠损坏持久状态的拒绝服务。

Ordinary crashes, UI/layout or accessibility defects, documentation problems,
test failures, feature requests, and behavior explicitly documented as absent
or disabled are normally public bugs when they have no plausible security
impact. Report those through [GitHub Issues][issues] after removing all
sensitive data. If you are unsure, use the private security route.

如果没有合理的安全影响，普通崩溃、UI/布局/无障碍问题、文档错误、测试失败、功能请求，以及
文档已明确说明尚未实现或默认关闭的能力，通常属于普通 Bug。完成彻底脱敏后，可通过
[GitHub Issues][issues] 公开报告。如果无法判断，请使用私密安全渠道。

[security-page]: https://github.com/Qiyuanqiii/Traverse-Board/security
[security-email]: mailto:qiiiqiyuan@gmail.com
[issues]: https://github.com/Qiyuanqiii/Traverse-Board/issues
