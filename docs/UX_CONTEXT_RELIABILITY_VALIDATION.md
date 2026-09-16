# 真实长任务失败后的上下文与工具纠错收敛

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-13，本批代码修复与有界验收已收尾，真实完整任务链路未通过。最新授权为“那继续吧”。本批证据目录为 `output/playwright/ux-context-reliability`；上一批 `ux-thread-budget` 的失败数据、模型调用和数据库保持原样，不重放。

## 最新结论（优先于下文过程中的状态）

上下文和工具纠错修复、定向回归及首版本地预览构建已完成；真实模型编程首关仍未通过，两个新的 Go 长任务臂没有开始。最后一次 `smoke-go-followthrough` 已真实创建提案、经审阅自动续接并 Apply 原入口，但命令的前台超时参数两次超出25秒上限，Job为0。这次不再是读取后口头承诺即结束。该命令约束的公开说明和schema已补齐，定向回归通过，没有追加真实模型调用。下面按过程保留每次发现与修订，不能把历史的“准备/正在修复”或已结清的输入理解为当前业务验收通过。

真实试验使用的代码清单为 `root-owner-verification-v6.json`：12份清单、55个唯一文件、0 SHA漂移，含普通Thread执行提示澄清。最后一次独立试验复用原Go请求与种子字节，6次正常调用/估算$0.008848；与前两个失败案例合计14次正常调用、估算$0.021177，非供应商账单。只允许src/total.go的改动已实际写入，测试等保护文件未改；没有Job、没有外部oracle、没有新长臂，也没有再发“继续”或手动Execute来修饰结果。新增命令说明已冻结于 command-runtime-timeout-freeze.json；最终预览已按下述v7构建，真实现场已经正式停服并完成审计。

[最新本地预览 EXE](../output/playwright/ux-context-reliability/delivery-v2/TraverseBoard-UX-Context-Reliability.exe) 已于12:16:49 UTC构建完成，105790464字节，SHA256 `c77125ee53af3d83f055af9708e8e8d8fd1ed0f74bd9749114d152325a89e234`，root独立核对一致。它包含真实试验之后补齐的命令timeout契约，最终 `root-owner-verification-v7.json` 为13份清单/56个唯一文件，SHA `089a1fb3aa5596e2d4eff61cf4f175430367c63c38436f37bd28d8034fb53b5c`。20项静态检查通过，116个前端资产精确嵌入，1810个构建输入及56个冻结文件构建前后一致。**未启动EXE，未产品commit/push/release；这版没有再次实测模型编程，不是完整链路已通过的发布包。** 详见 `delivery-v2/README.md` 与 `delivery-v2/delivery-result.json`。原v6首版、原验收binary和所有失败证据保留。

## 修复目标与实际问题

上一批规则策略在真实任务第6条已明确允许退款后，压缩后的实现又拒绝负数；两次辅助摘要分别为1720/1983字符，超过1600硬上限而回退。另有整批工具数量/参数错误直接结束输入、普通答复缺少重复finish summary等问题。上一批两臂均没有真实命令Job，不能宣称完整任务闭环通过。

本批先修可复现的接入与选择缺口，再以新隔离任务验证模型实际操作。固定协议测试用于核对保存、调用、权限与续接，不代替真实语义质量。

## 已实施的工具与答复调整

- 初次模型请求明确每批最多4个工具；工具结果后可以继续调用当前提供的工具，结束答复时再返回响应。提供command_runtime时提示按实际适配器选择profile，避免与枚举式命令提案混淆。
- 只对执行前整批准备阶段的拒绝复用一次已有协议修复；记录原拒绝轮次，保留当时可用工具并重新检查规范、权限和审批。原坏批次不写工具账本、不执行其中任何成员，不回显未经接纳的参数正文。
- 修复首答复必须提交有效工具批次；没有完成修正工具结果时，纯文字“完成”不能转换成成功。拒绝第二次错误，不加重试额度。持久化轮次约束在SQLite重开后继续生效。
- 成功修正后可正常继续工具和答复，原消息key复核不重放效果。只有一次修复开始/完成事件。无已执行工具时保留“请求未执行”的失败分类；已有实际工具结果后发生格式错误，按真实响应失败分类，不抹掉已执行事实。
- 仅在Go已核实当前Thread/attempt绑定时，finish缺少重复summary可作为普通continue回复；不结束Run或工作项。非交互生命周期、错误版本、未知字段、重复键、尾随JSON等仍按原严格规则拒绝。
- 补丁参数诊断给出具体索引和字段，例如expected_occurrences合法范围，不把文件正文或凭据内容拼进错误。

金额与取消边界继续沿用原账本。复核发现晚到的纯计账响应可能被“尚无修正工具结果”误挡，已加排除；取消后只允许记录已发生用量，不能借此执行工具、写助手消息或复活Run。

## 上下文调整

规则摘录从固定初始/首条澄清槽改为让真实操作者来源共享有限空间，保留初始目标和实际工具来源，容量不足时明确有损并可回读原件。完整短记录的两个相同SHA不再重复写入；仅当正文重新计算SHA与原来源SHA相等时允许恢复省略字段，截取正文仍有独立SHA。总4000字符与12条记录上限不变，不声称无限纠正无损。

生成提示软目标为1200字符，硬限制仍1600；输出token上限2048、单调用和取消/预算/CAS保护不变。真实1720/1983候选仍拒绝，错误现在报告实际长度，不能靠截断解析来冒充合规。新的operator选择还暴露旧装配器为了保留所有来源条目而再次裁短合法生成文本或回退的问题，已改为完整生成文本与关键原始来源共同装配；不适合的候选明确回退。

## 验收与证据边界

已通过的根代理定向结果在 `root-correction-tests-final.jsonl`；后续整合结果单列，不累加重叠测试。首轮真实红和回归红保留：取消fixture原在provider返回前取消，会竞争流接收，现改为修复回执保存后取消以精确验证Pending重开；原事件测试改为JSON字段断言，避免新增金额字段打断字串匹配。这两项是测试定位修正，不宣称生产取消行为被改变。

真实验证先走单条求和任务：模型读取并修改原入口→准确提案审阅→自动续跑实际应用→Local/LPAC里运行3条Node测试。最多8正常调用/$0.10/10分钟。只有真实Job与原入口都通过才做同24输入两臂连续性验证，每臂最多48正常+3辅助调用/$0.90/45分钟；另留资格$0.10，全批最多$2。模型实测、独立外层oracle和本地价格快照估算分列。

生产已冻结并进入本批真实模型首关准备。contextmgr整包51顶层race3.836s，原普通两生成/失败回退2顶层race12.718s（原纠正断言未放宽）；domain/store最终1+10顶层race1.411/4.810s；root最终集成6顶层race5.284s，原17项组和五包vet通过。各集合重叠不累加，初轮广回归77项通过、3项失败的原记录保留，其中2项契约断言已修，实际生成容量问题修后原测试通过。不是全仓CI。不能据此宣称真实编程链路或生成式摘要质量已通过。Windows原生剪贴板、IME、DPI、硬退出草稿等旧范围不并入本批验收。

## 真实首关与运行时修订（进行中）

首个真实smoke在09:39:58.079Z开始，6次正常调用、价格快照估算$0.008744。模型真实读取并修改原src/total.mjs，准确审批后Apply1；然而Job0，原唯一input在两次命令拒绝后失败结清，无pending。模型seq5/6参数符合实际提供的JSON schema，原schema没有告知process按文件名拒绝Node；验收准备还错误地强制指定Node process。责任不能归为模型违反工具参数。原失败保留，未重放、未抬原预算，未启动长臂。

随后无模型实际Local/LPAC探针确认PS7.6.5能启动，但只读工具输入仅映射所选PowerShell目录，无法找到D:/Node/node.exe，exit1；树已回收、workspace未改。仅改用shell不能证明执行测试可用。证据分别在acceptance-host/evidence/smoke与local-capability-probe。

经对现有能力和边界复核，正式修订process契约以支持绝对原生开发运行时及字面argv。既有process本就接受go test、cargo和其他可执行用户代码的原生程序；隔离必须来自权限与LPAC/Job，而非语言程序文件名。仍禁止实际Shell、py代理、系统脚本宿主及提权/命令代理，保留native镜像、非工作区程序、文件SHA、cwd/env、lease、网络关闭与只读工具根目录。共享规范也适用于本已授权的Host入口，但不改变FullAccess/Debug门禁或把Host称为沙箱。另补Local启动前重新验证程序及工作目录，路径摘要不宣称为工具目录内容摘要。未增加多目录配置平台、未复制改名程序、未手调ACL或退到Host。

新契约与启动前核对已冻结：runtime定向runner7项race1.719s/gateway4项race2.246s；Local变化复核3顶层、9子例race3.252s、无跳过；root提示/修复3项race1.501s，application/runner/toolgateway的vet通过。Local首次别名子例因权限跳过，已用实际Windows junction补验；旧日志保留，不将原skip计为pass。6份owner的41个唯一文件逐SHA一致，见root-owner-verification-v2.json。各集合有重叠，不累计为全仓CI。

尚无通过的真实Node Job。接下来先无模型直连Node LPAC实跑，再新neutral-profile smoke，最多8正常调用/$0.09/10分钟，加旧花费仍在smoke$0.10范围内；全批$2不变。旧案例不能与新案例拼为一次成功。桌面构建须使用新41文件核对结果，跨目录工具链布局不扩大为已解决。

**第二个零模型探针实测未通过，继续修复：** 10:09:37Z，正式application Local executor已把D:/Node/node.exe转换为准确manifest，但backend在创建Node进程之前失败。D:/Node为SYSTEM拥有且DACL受保护，当前标准用户仅RX；非系统默认工具目录需要授予本次LPAC只读能力，WRITE_DAC被拒绝。Run错误后的清理又尝试对从未改变的Node原ACL写回，同样失败，导致Close错误和一份owner journal残留。原Node bytes、工作目录及Node目录的ACL与原快照相符；零结果中的默认exit0绝非执行成功。证据在direct-node-capability-probe。当前授权窄修为修改ACL前检查能力和明确诊断，以及原身份/ACL/label/protection确证未变时无需重复写回的恢复；不扩大系统白名单、不降低LPAC、不手工修改目录ACL。另只读查找现有当前用户可用的Node安装，若可用则明确采用不同安装验证，不能据此宣称D:/Node已可执行。付费smoke仍0新调用，桌面Go build继续暂停，41项冻结记录保留为本次新修之前的历史。

**ACL修复已冻结，实际复测进行中：** local-acl/freeze.json记录三个文件。启动前通过只请求WRITE_DAC的句柄核对可操作性，不写权限；恢复只在当前pinned身份、DACL顺序、完整性label和保护位与快照精确一致时跳过写回，其余继续原严格恢复。定向4顶层/3子例race1.902s、零跳过，含原crash-owner恢复；隔离临时目录真实拒绝WRITE_DAC，未对D:/Node执行ACL写入。root再次核7 owner/44文件SHA一致及三包vet。准备使用测试机已有的用户范围Node24.19.0做新LPAC探针，其来源为Codex工具环境；不是产品自带Node，也不宣称SYSTEM安装问题已消失。

**新长臂准备协议（付费前固定）：** 仍逐项保留原24输入的完整内容和SHA、两臂相同。为实际覆盖两次压缩后续接，将上批“第23输入前重开”改为本批“第二份真实摘要出现后的第一个输入已完整结清的边界重开同home，随后发送下一条原输入”，记录实际序号。原第23输入提及已经重开仍须真实成立，不额外发送修复性消息。两臂均执行此策略；不将本批和旧批称作严格同条件比较。全24输入、实际文件/工具结果及最终独立测试标准不放宽，额度或时间用尽时如实保留未完成。

## Node 实际兼容性与现有 Docker 路径

上述 ACL 修复已通过正式 Backend 恢复原 owner：旧 journal 正常移除、Close 无错误、原 D:/Node 程序 SHA 不变，没有手工改 ACL 或删恢复记录。新用户范围 Node 24.19.0 / libuv 1.52.1 能在 LPAC 内输出版本、执行 builtin 脚本，以及启动继承或忽略 stdio 的子进程；但默认管道子进程超时，`node --test` 也在20秒后超时。禁用测试进程隔离的独立诊断又出现 `EPERM lstat D:\\`，文件装载失败，不能算测试通过。

这区分了安装权限、子进程管道和祖先目录元数据三个问题。实际对照与 [libuv 已确认的 AppContainer 管道缺陷](https://github.com/libuv/libuv/issues/5178)及 [已合入开发分支的修复](https://github.com/libuv/libuv/pull/5181)吻合，但本次没有原生线程栈；不将源码推断表述为现场系统调用跟踪。已核版本没有可承诺的正式 Node 升级修复，也未安装或修改 Node。详情为 `output/playwright/ux-context-reliability/node-lpac-compatibility.md` 和 `preparation/node-runtime-diagnostic-summary.json`。这8项用户范围 Node 探针均为0模型/0持久Job，程序内容不变、进程树与ACL/profile清理完成；清理成功不等于业务测试成功。

当前继续检查项目**已有**的固定镜像 Docker 后端。测试机已安装 Docker Desktop，但首次检查引擎未运行，尚不能判断有无可用镜像。已授权通过其官方 CLI 后台启动现有引擎，先核对镜像摘要和存储位置；不提权、不改系统安全设置、不先下载/构建镜像。正式后端要求 `standard-code-docker-runner.v2` 固定镜像，普通 Node 镜像不等价；若可用，须复用生产服务装配及真实 Drydock/权限/审批/Job 回执，并准确验证输出进入模型。不能仅把 LPAC 验收标签改成 Docker，也不回退 Host。

Docker 的实际检查也未通过：官方启动返回后，引擎初始化无法移除既有 `dockerInference` 节点，错误1920。无跟随目录查询确证其0B/reparse tag `0x80000023`，但没有删除、移动或改权限。现有 `docker_data.vhdx` 约25.17GB位于C盘；镜像清单仍未知。普通停止失败后，官方 `desktop stop --force --timeout 20` 成功退出；复核本次8个进程均退出、Docker安装目录下无运行进程、服务仍Stopped/Manual、18906–18908无监听。没有容器创建、镜像下载、WSL或系统服务修改。详情为 `docker-readiness/validation.md` 和 `final-cleanup.json`。

源码核对另外确认 Docker 的实际输出接入缺口：旧 adapter 把 Standard Code result JSON 作为 stdout，但该结果只含退出码、Checkpoint及日志ID/SHA/字节数，既有捕获器的脱敏正文随后被丢弃，模型不能读取TAP或错误正文。这需要独立修复，不能用切换后端掩盖。当前最小修改复用原单次 owned log capture，在同一次解码中保留受限脱敏正文；仅 command_runtime 的当前请求在日志收据成功保存后交给既有 Job manager。标准 API 继续只返回元数据，不新建日志表、持久平台、二次attach或命令重放。异常、截断、未知与重开不可用须如实展示，合计输出遵守原请求 artifact_bytes。底层原golden vectors及新输出/摘要一致性、跨帧UTF-8/脱敏、截断及读取错误4顶层race1.788s通过；应用接线尚在实施，不能据此称真实Docker已运行。

收费新 smoke 和两长臂尚未开始，仍仅首关6调用、快照估算$0.008744。完整编程及两次压缩后续接仍未通过。待输出修复冻结后可构建明确标注未完成真实验收的本地预览包，不能冒称完整交付。长臂驱动已准备，10项离线推进门检查通过，辅助 PowerShell/Python 子进程使用 Windows `CREATE_NO_WINDOW`；这只验证驱动行为，不计为模型任务成功。最新驱动清单为 `preparation/long-driver-freeze-v2.json`，旧清单保留。

## 已验证的 Go 路径与后续验收调整

零模型 Go 对照揭示了另一个应用接入错误：Local adapter 将 Docker 的16MiB项目文件增长限额设为 Local 的整个Job写入预算。Local实际同时约束进程树累计WriteTransferCount以及项目正增长加整个scratch/cache，含Go冷编译缓存；原探针5.83秒后因此被中止，三份源文件仍未变。这不是Node管道问题，也不能把底层手设512MiB的旧Go测试称为普通对话默认可用。

正式修复只让Local caller采用后端已有的 `DefaultLocalDiskWriteLimit`（2GiB），没有修改后端默认数字、测量方式、权限或加新配置字段。预算包含临时写入，不是分配2GiB空间或保留16MiB项目增长限制的承诺。1顶层/3profile回归race1.502s通过。随后同3文件、同argv/env/30秒限制的新run-2经真实LPAC完成 `go test`：7.56秒、TestAnswer通过、exit0；实际缓存/TEMP均在本轮D盘scratch，网络关闭，原Go文件与夹具SHA不变、树/ACL/profile清理完成。它仍是0模型/0持久Job诊断。见 `go-local-capability-probe/validation-v2.md`；旧失败保留。

Docker输出接线也已完成定向核验：8顶层/8子例race140.886s，最后非成功回执但exit0的状态修正4顶层/11子例race2.718s，相关两包vet通过；集合重叠不累加。真实SQLite与生产adapter/Job manager已核双路正文、SHA、同key不重新start/attach；Docker运输仍为固定夹具，不能算真实容器。独立复核发现的残缺帧头误判completed和receipt-only失败被映成成功均已修。最终 `root-owner-verification-v5.json` 核11清单/54个唯一文件、0漂移，包含Local预算与Docker输出；详见 `docker-command-output-validation.md`。

为继续完成用户的长任务连续性目标，下一真实编程门槛改为新 **smoke-go**，使用已实证可用的Go路径和新的求和stub/3测试；替代未启动的smoke-shell，仍8正常调用/$0.09/10分钟，全批$2不增加。原Node失败及未启动seed不覆盖、不宣称Node通过。成功后使用两份相同的Go账目CLI变体，保留原24输入的业务规则、退款/去重修正、±9007199254740991金额边界、禁改文件和第二摘要后重开要求，只翻译运行时/API表述并冻结新SHA。这是新的Go对照，不与原Node结果作同条件或摘要因果比较。原Node输入全文及其hash保留。Go变体、独立oracle与驱动门尚在准备，收费长臂必须等真实smoke成功后开始；结果仍按真实Job/模型接收与外层oracle分别报告。

## 最后一次真实编程验证

`smoke-go-followthrough` 的唯一请求于11:54:24.328 UTC实际发送。前3次调用读取原文件并创建准确补丁，root检查完整diff后通过原审批接口批准；自动续跑的第4次模型调用实际 `workspace_apply`，仅隔离Drydock中的 `src/total.go` 从 `fc27ae6d…8048` 改为 `8e365728…e316`，其他5个实际种子文件及源repo不变。这证明这一次的提案→审阅→自动实际应用，不只是状态字样变成approved。

第5次请求使用正确Go程序和字面测试参数，但为 `action=run` 传300000ms；前台批次原上限是合计25000ms，准备阶段拒绝，未创建或启动Job。第6次已收到准确25秒诊断，仍传120000ms；一次纠错用完后失败结清。公开schema当时只给通用1800000ms，未呈现run特有限制；同时模型没有遵循实际诊断。这两项事实分别成立，不能把失败全归模型或说Go运行失败。

最终6正常调用、估算$0.008848、6已完成工具（4读取/列举、1提案、1应用）、1批准且已应用修改、0 Job/0摘要。没有外部oracle代替缺少的模型测试，没有额外“继续”/Execute/重放，两个Go长臂没有启动。11:57:49 UTC正式关闭自有host，11:58:21核对18906–18908无监听、验收进程为0，无pending/queue/lease。375表停前后逻辑内容一致，integrity正常/FK0，原wire/计数/旧GoDB保持。闭合DB SHA `000d90771e8be51fc9ea3efb84dd7df1076efd39a45c4063ab9c48572973327f`。详细证据见 [最终真实验收报告](../output/playwright/ux-context-reliability/acceptance-host/evidence/smoke-go-followthrough/final-validation.md) 和同目录 `final-cleanup-summary.json`。

最后的命令工具公开契约已补齐：前台run整批超时合计25秒，需要更长时间用已有start创建单个后台Job，再read/wait读取同Job。保留原限额、权限和预算，不自动clamp、不自动转换命令、不增加模型重试。本次失败不因此改判通过，修后没有再次付费实跑。 最终 `go test -race ./internal/toolgateway -run TestCommandRuntime -count=1 -json` 的6项顶层/9项子例通过（2.334秒），相关vet通过；实际发布schema导出与目录查询一致。覆盖两次真实超额参数、25秒边界、跨命令求和、后台原上限/单条限制、没有执行或扣工具预算及没有静默截短；JSON Schema仅约束单项，总和仍由Go检查。完整说明只保留一次，字段说明各自简述，避免重复增加上下文。详见 `command-runtime-timeout-validation.md`。

另有源码确认的用户反馈缺口：模型虽收到具体25秒诊断，用户终态仅获通用 `failed_precondition`/续跑失败。应用保存handoff时降为通用错误码，完成清理后checkpoint原原因被清空；内部repair事件保留原因，但普通活动/Inspector公共投影没有该原因。此次没有Job详情可以补充。此项尚未修，不因工具说明改进而宣称用户解释已完善；没有直接把原始模型参数或内部事件全部公开。具体路径与证据见 `command-runtime-timeout-review.md`。下一步应优先让用户看到“哪一步失败、哪些改动已完成、是否启动命令、下一步怎样继续”的有界事实，再完成真实编程及多次压缩后的续接验证。

## 工作区与提示修改过程

### Go 首次模型请求与提示澄清

smoke-go在11:22:49.801Z发送原唯一请求，2正常调用/估算$0.003585。四次读取成功后，模型虽仍获提供workspace_change/apply/command_runtime等19工具，却只用合法 `continue` 回复“Next I propose...”，无真实提案、Apply或Job；输入结清，产品handoff selection_drained。原10分钟窗口结束后正常关闭，375表逻辑hash/账本和源文件未变、无pending/lease/listener。旧失败不重放；见 `acceptance-host/evidence/smoke-go/final-validation.md`。这不是Go执行失败，也不代表任务已完成。

源码复核发现Thread提示没有说明普通无工具continue会交回操作者，还鼓励“即使任务有剩余，也可以结束本回复”。现在明确区分回复结束和后续工具执行：行动请求先落实可用下一tool，审批先创建实际可审阅提案；信息/只读进度/规划请求和真实阻塞可正常回复。保留Harness明确宣告四轮内部调度边界时的续接例外；不新增自动空转、文字关键词判定或强制工具机制。3项race3.073s通过，包含精确Thread绑定、Plan确认后的真实请求及六依赖工具跨segment，状态机与解析不变。原预检日志保留，最终两文件在thread-execution-guidance-freeze.json。

按新冻结再做最后一个全新smoke-go-followthrough，原Go任务/6seed内容不变，最多6正常调用/$0.086415/新固定10分钟；与旧Go2调用/$0.003585合计仍8/$0.09，不增加整个$2预算。新home/key/binary/evidence独立，不向旧key补继续或重放。若再次提前停如实保留失败，不反复换seed刷成功。两Go长臂仍须真实smoke门槛通过才能开始，当前只有准备证据。

实际实施树为 `<workspace>`，branch `codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。开工849项dirty保留，产品未commit/push/release，原CTF树仅镜像工作必读。Go缓存和临时文件均在D盘。最终预览与现场收尾见本文首节；结束时880项dirty，原有更改保留，旧PID不用于控制新进程。
