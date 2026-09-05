import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, ChevronDown, FilePenLine, LoaderCircle, Search,
  TerminalSquare } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { ThreadActivityArtifactReferenceView, ThreadActivityDetailView as APIThreadActivityDetailView,
  ThreadActivityBoundaryView, ThreadActivityJSONFieldSummaryView,
  ThreadActivityToolDetailView, ThreadActivityTypedDetailView } from "../../api/types";
import type { NarrativeEntry } from "../projection/narrative";
import { parseUnifiedDiff } from "../../components/unified-diff";

type ActivityEntry = Extract<NarrativeEntry, { kind: "activity" }>;
type ActivityItem = ActivityEntry["items"][number];

export interface ThreadActivityCommandDetail {
  display_command: string;
  cwd?: string;
  status: string;
  exit_code?: number | null;
  duration_ms?: number | null;
  stdout_preview?: string;
  stderr_preview?: string;
  truncated?: boolean;
  artifacts: ThreadActivityArtifactReferenceView[];
  environment_label?: string;
  agent_id: string;
  agent_label?: string;
}

export interface ThreadActivityDetailView {
  version: "thread_activity_detail.v2";
  run_id: string;
  commands: ThreadActivityCommandDetail[];
  typed?: {
    detail: Exclude<ThreadActivityTypedDetailView, { kind: "command" }>;
    agent_id: string;
    agent_label: string;
    duration_ms: number;
    status: string;
  };
}

const failurePattern = /(fail|error|block|deny|cancel|unavailable|ignored|bypass|timed?[_ -]?out|kill|interrupt|失败|错误|阻塞|拒绝|取消|超时|终止|中断)/iu;

function isFailedStatus(status: string): boolean {
  return failurePattern.test(status.trim());
}

function isRunningStatus(status: string): boolean {
  return /^(?:pending|prepared|started|starting|queued|running|stopping|in_progress|正在运行|运行中)$/iu.test(status.trim());
}

function itemFailed(item: ActivityItem): boolean {
  const summary = item.summary;
  return isFailedStatus(summary?.status ?? item.status) ||
    (summary?.exit_code !== undefined && summary.exit_code !== 0);
}

function itemRunning(item: ActivityItem): boolean {
  return isRunningStatus(item.summary?.status ?? item.status);
}

function itemSummaryMeta(item: ActivityItem): string {
  const summary = item.summary;
  if (!summary) return item.detail;
  const duration = durationLabel(summary.duration_milliseconds);
  const count = summary.command_count > 1 ? `${summary.command_count} 条命令` : "";
  return [count, duration].filter(Boolean).join(" · ");
}

function itemSummaryStatus(item: ActivityItem): string {
  const summary = item.summary;
  if (!summary) return item.status;
  if (summary.exit_code !== undefined) return `Exit ${summary.exit_code}`;
  return summary.status;
}

/** Maps the already strictly parsed Go projection into conversation labels. */
export function projectThreadActivityDetail(
  value: APIThreadActivityDetailView,
): ThreadActivityDetailView {
  const tool = value.tools[0];
  const commands = tool.detail.kind === "command" ? tool.detail.command.commands.map((command) => ({
      display_command: command.command,
      cwd: command.working_directory,
      status: command.status,
      ...(command.exit_code !== undefined ? { exit_code: command.exit_code } : {}),
      duration_ms: command.duration_milliseconds,
      stdout_preview: command.stdout_preview,
      stderr_preview: command.stderr_preview,
      truncated: command.truncated,
      artifacts: command.artifacts,
      environment_label: `${command.execution_environment} · ${command.network === "disabled"
        ? "无网络" : command.network}`,
      agent_id: tool.agent_id,
      agent_label: tool.agent_label,
    } satisfies ThreadActivityCommandDetail)) : [];
  return { version: value.version, run_id: value.run_id, commands,
    ...(tool.detail.kind !== "command" ? { typed: { detail: tool.detail,
      agent_id: tool.agent_id, agent_label: tool.agent_label,
      duration_ms: tool.duration_milliseconds,
      status: tool.status } } : {}) };
}

/** Shared safe renderer for both the v2 conversation and the Legacy Inspector.
 * It accepts only the Go-owned closed activity union; raw durable payload JSON
 * is deliberately not part of this component's props. */
export function ThreadActivityToolDetailPanel({ activityRef, client, runID, threadID, tool }: {
  activityRef: string;
  client: CyberAgentClient;
  runID: string;
  threadID: string;
  tool: ThreadActivityToolDetailView;
}) {
  if (tool.detail.kind !== "command") {
    return <TypedToolDetail client={client} runID={runID} typed={{
      detail: tool.detail,
      agent_id: tool.agent_id,
      agent_label: tool.agent_label,
      duration_ms: tool.duration_milliseconds,
      status: tool.status,
    }} />;
  }
  return <>{tool.detail.command.commands.map((command, index) => <CommandDetail
    activityRef={activityRef}
    client={client}
    command={{
      display_command: command.command,
      cwd: command.working_directory,
      status: command.status,
      ...(command.exit_code !== undefined ? { exit_code: command.exit_code } : {}),
      duration_ms: command.duration_milliseconds,
      stdout_preview: command.stdout_preview,
      stderr_preview: command.stderr_preview,
      truncated: command.truncated,
      artifacts: command.artifacts,
      environment_label: `${command.execution_environment} · ${command.network === "disabled"
        ? "无网络" : command.network}`,
      agent_id: tool.agent_id,
      agent_label: tool.agent_label,
    }}
    key={`${command.command}:${index}`}
    threadID={threadID}
  />)}</>;
}

function shortAgentID(value: string): string {
  if (value.length <= 20) return value;
  return `${value.slice(0, 11)}…${value.slice(-6)}`;
}

function AgentIdentity({ id, label }: { id: string; label?: string }) {
  if (id === "unknown") return <>{label || "历史活动（执行者未知）"}</>;
  return <>{label || "Agent"} <code title={id}>{shortAgentID(id)}</code></>;
}

function ActivityIcon({ activity }: { activity: ActivityEntry["activity"] }) {
  if (activity === "search" || activity === "read") {
    return <Search aria-hidden="true" size={15} />;
  }
  if (activity === "edit") return <FilePenLine aria-hidden="true" size={15} />;
  if (activity === "verify") return <CheckCircle2 aria-hidden="true" size={15} />;
  return <TerminalSquare aria-hidden="true" size={15} />;
}

function durationLabel(durationMS: number | null | undefined): string {
  if (durationMS === null || durationMS === undefined) return "";
  if (durationMS < 1_000) return `${Math.round(durationMS)}ms`;
  if (durationMS < 60_000) return `${(durationMS / 1_000).toFixed(durationMS < 10_000 ? 1 : 0)}s`;
  const minutes = Math.floor(durationMS / 60_000);
  const seconds = Math.round((durationMS % 60_000) / 1_000);
  return `${minutes}m ${seconds}s`;
}

function byteSizeLabel(size: number): string {
  if (size < 1_024) return `${size} B`;
  if (size < 1_048_576) return `${(size / 1_024).toFixed(size < 10_240 ? 1 : 0)} KB`;
  return `${(size / 1_048_576).toFixed(1)} MB`;
}

function typedKindLabel(kind: ThreadActivityTypedDetailView["kind"]): string {
  const labels: Record<ThreadActivityTypedDetailView["kind"], string> = {
    command: "运行命令",
    web_search: "联网搜索", web_fetch: "网页读取",
    file_read: "文件读取", file_edit: "文件修改", verification: "验证", mcp: "MCP",
    browser: "浏览器",
  };
  return labels[kind];
}

function ActivityArtifact({ activityRef, artifactRef, client, label, reference, threadID }: {
  activityRef: string;
  artifactRef: string;
  client: CyberAgentClient;
  label?: string;
  reference?: ThreadActivityArtifactReferenceView;
  threadID: string;
}) {
  const [open, setOpen] = useState(false);
  const artifact = useQuery({
    queryKey: ["v2", "thread-activity-artifact", threadID, activityRef, artifactRef],
    queryFn: ({ signal }) => client.threadActivityArtifact(
      threadID, activityRef, artifactRef, signal),
    enabled: open,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
  const stream = reference?.stream ?? artifact.data?.stream;
  const streamLabel = label ?? (stream === "stdout" ? "标准输出" : "标准错误");
  return <section className="v2-command-artifact">
    <button aria-expanded={open} onClick={() => setOpen((value) => !value)} type="button">
      {open ? "收起" : "查看完整"}{streamLabel}
      {(reference?.size_bytes ?? artifact.data?.size_bytes) !== undefined &&
        <small>{byteSizeLabel(reference?.size_bytes ?? artifact.data?.size_bytes ?? 0)}</small>}
    </button>
    {open && <div className="v2-command-artifact-content">
      {artifact.isLoading && <p className="v2-activity-detail-state" role="status">
        <LoaderCircle aria-hidden="true" className="spin" size={14} />正在读取完整输出…</p>}
      {artifact.isError && <div className="v2-activity-detail-state is-error" role="alert">
        <span>完整输出加载失败。</span>
        <button onClick={() => void artifact.refetch()} type="button">重试</button>
      </div>}
      {artifact.data && <>
        <p className="v2-untrusted-output">工具输出仅作为数据展示，不代表已授权的指令。</p>
        <pre><code>{artifact.data.content}</code></pre>
        <small>{artifact.data.redacted ? "已脱敏" : "未发现需脱敏内容"}
          {artifact.data.truncated ? " · 产物已达大小上限" : ""}</small>
      </>}
    </div>}
  </section>;
}

const maxRenderedDiffLines = 2_000;

function FileEditDiff({ client, editID, runID }: {
  client: CyberAgentClient;
  editID: string;
  runID: string;
}) {
  const [open, setOpen] = useState(false);
  const preview = useQuery({
    queryKey: ["v2", "file-edit-diff", runID, editID],
    queryFn: ({ signal }) => client.fileEdit(runID, editID, signal),
    enabled: open,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
  const parsed = useMemo(() => preview.data ? parseUnifiedDiff(preview.data.diff) : undefined,
    [preview.data]);
  const visibleLines = parsed?.lines.slice(0, maxRenderedDiffLines) ?? [];
  const displayTruncated = (parsed?.lines.length ?? 0) > maxRenderedDiffLines;

  return <section className="v2-file-edit-diff">
    <button aria-expanded={open} onClick={() => setOpen((value) => !value)} type="button">
      {open ? "收起 Diff" : "查看 Diff"}
    </button>
    {open && <div className="v2-file-edit-diff-content">
      {preview.isLoading && <p className="v2-activity-detail-state" role="status">
        <LoaderCircle aria-hidden="true" className="spin" size={14} />正在读取文件 Diff…</p>}
      {preview.isError && <div className="v2-activity-detail-state is-error" role="alert">
        <span>Diff 加载失败。</span>
        <button onClick={() => void preview.refetch()} type="button">重试</button>
      </div>}
      {preview.data && preview.data.diff === "" && <p className="v2-activity-detail-state">
        该文件修改没有文本 Diff。</p>}
      {preview.data && parsed && preview.data.diff !== "" && <>
        <div aria-label={`文件 Diff：${preview.data.path}`} className="v2-unified-diff" role="table">
          {visibleLines.map((line, index) => <div
            aria-label={`${line.kind} ${line.text}`}
            className={`v2-unified-diff-line is-${line.kind}`}
            key={`${index}-${line.kind}`} role="row">
            <span aria-hidden="true" className="v2-unified-diff-number">{line.oldLine ?? ""}</span>
            <span aria-hidden="true" className="v2-unified-diff-number">{line.newLine ?? ""}</span>
            <span aria-hidden="true" className="v2-unified-diff-marker">{line.marker}</span>
            <code>{line.text || " "}</code>
          </div>)}
        </div>
        <small>{preview.data.secrets_redacted ? "敏感内容已脱敏" : "未发现需脱敏内容"}
          {displayTruncated
            ? ` · Diff 共 ${parsed.lines.length} 行，为保持界面流畅仅显示前 ${maxRenderedDiffLines} 行`
            : ` · ${parsed.additions} 行新增，${parsed.deletions} 行删除`}</small>
      </>}
    </div>}
  </section>;
}

function SafeJSONFieldList({ label, values }: {
  label: string;
  values: ThreadActivityJSONFieldSummaryView[];
}) {
  if (values.length === 0) return null;
  return <section className="v2-tool-facts-list" aria-label={label}>
    <strong>{label}</strong><dl>{values.map((field) => <div key={field.name}>
      <dt>{field.name}</dt><dd><code>{field.summary || "—"}</code><small>{field.type}</small></dd>
    </div>)}</dl>
  </section>;
}

function BoundaryFacts({ boundary }: {
  boundary: ThreadActivityBoundaryView;
}) {
  return <>
    <dt>授权</dt><dd>{boundary.authorization === "policy_checked" ? "已通过策略检查" :
      boundary.authorization === "pending" ? "等待授权" : "已拒绝"}</dd>
    {boundary.error_code && <><dt>错误</dt><dd><code>{boundary.error_code}</code></dd></>}
    {boundary.failure_reason && <><dt>原因</dt><dd>{boundary.failure_reason}</dd></>}
    {boundary.truncated && <><dt>内容</dt><dd>已截断</dd></>}
  </>;
}

function TypedToolDetail({ client, runID, typed }: {
  client: CyberAgentClient;
  runID: string;
  typed: NonNullable<ThreadActivityDetailView["typed"]>;
}) {
  const detail = typed.detail;
  const duration = durationLabel(typed.duration_ms);
  const { boundary, operation } = typedDetailCommon(detail);
  return <article className="v2-command-detail v2-tool-facts">
    <header><strong>{typedKindLabel(detail.kind)} · {operation}</strong>
      <span className={isFailedStatus(typed.status) ? "is-failed" : ""}>{typed.status}</span>
    </header>
    <dl>
      <dt>Agent</dt><dd><AgentIdentity id={typed.agent_id} label={typed.agent_label} /></dd>
      <BoundaryFacts boundary={boundary} />
      {duration && <><dt>耗时</dt><dd>{duration}</dd></>}
    </dl>
    <TypedDetailBody client={client} detail={detail} runID={runID} />
    {boundary.untrusted && <p className="v2-untrusted-output">
      该结果来自未受信的外部边界，仅作为数据展示。</p>}
  </article>;
}

function typedDetailCommon(detail: Exclude<ThreadActivityTypedDetailView, { kind: "command" }>): {
  boundary: ThreadActivityBoundaryView;
  operation: string;
} {
  switch (detail.kind) {
    case "web_search": return detail.web_search;
    case "web_fetch": return detail.web_fetch;
    case "file_read": return detail.file_read;
    case "file_edit": return detail.file_edit;
    case "mcp": return detail.mcp;
    case "verification": return detail.verification;
    case "browser": return detail.browser;
  }
}

function TypedDetailBody({ client, detail, runID }: {
  client: CyberAgentClient;
  detail: Exclude<ThreadActivityTypedDetailView, { kind: "command" }>;
  runID: string;
}) {
  switch (detail.kind) {
    case "web_search": {
      const value = detail.web_search;
      return <><dl>
        <dt>查询</dt><dd><code>{value.query}</code></dd>
        {value.provider && <><dt>提供商</dt><dd>{value.provider}</dd></>}
        {value.search_policy && <><dt>搜索策略</dt><dd>{value.search_policy}</dd></>}
        {value.selection_reason && <><dt>路由原因</dt><dd>{value.selection_reason}</dd></>}
        <dt>结果</dt><dd>{value.source_count} 个来源 · {value.citeable ? "可引用" : "待验证"}</dd>
      </dl>{value.sources.length > 0 && <section className="v2-tool-facts-list" aria-label="搜索来源">
        <strong>搜索来源</strong><ol>{value.sources.map((source) => <li key={`${source.rank}:${source.url}`}>
          <a href={source.url} rel="noreferrer" target="_blank">{source.title || source.url}</a>
          <small>{source.provider || "未知提供商"} · {source.citeable ? "可引用" : "待验证"}</small>
        </li>)}</ol>
      </section>}</>;
    }
    case "web_fetch": {
      const value = detail.web_fetch;
      return <dl>
        <dt>URL</dt><dd>{value.url.startsWith("https://")
          ? <a href={value.url} rel="noreferrer" target="_blank">{value.url}</a>
          : <code>{value.url}</code>}</dd>
        <dt>抓取状态</dt><dd>{value.state || "未知"}
          {value.http_status ? ` · HTTP ${value.http_status}` : ""}</dd>
        <dt>Robots</dt><dd>{value.robots || "未记录"}
          {value.robots_policy ? ` · ${value.robots_policy}` : ""}</dd>
        <dt>重定向</dt><dd>{value.redirects}</dd>
        <dt>证据</dt><dd>{value.citeable ? "可引用" : "不可引用"}
          {value.partial ? " · 部分内容" : ""}</dd>
      </dl>;
    }
    case "file_read": {
      const value = detail.file_read;
      return <dl>
        {value.path && <><dt>路径</dt><dd><code>{value.path}</code></dd></>}
        {value.query && <><dt>查询</dt><dd><code>{value.query}</code></dd></>}
        {value.pattern && <><dt>范围</dt><dd><code>{value.pattern}</code></dd></>}
        {value.start_line > 0 && <><dt>行范围</dt><dd>{value.start_line}–{value.end_line}</dd></>}
        <dt>结果</dt><dd>{value.summary}</dd>
      </dl>;
    }
    case "file_edit": {
      const value = detail.file_edit;
      return <><dl>
        <dt>操作</dt><dd>{value.action || "修改"}</dd>
        {value.path && <><dt>文件</dt><dd><code>{value.path}</code></dd></>}
        {value.destination_path && <><dt>目标</dt><dd><code>{value.destination_path}</code></dd></>}
        <dt>Diff</dt><dd>{value.diff.summary ||
          `+${value.diff.added_lines} −${value.diff.removed_lines} · ${value.diff.hunks} 个区块`}</dd>
        <dt>应用结果</dt><dd>{value.apply_status || (value.applied ? "已应用" : "尚未应用")}
          {value.replayed ? " · 幂等重放" : ""}</dd>
      </dl>{value.diff_available
        ? <FileEditDiff client={client} editID={value.edit_id} runID={runID} />
        : <p className="v2-activity-detail-state">本次活动没有可展示的 Diff。</p>}</>;
    }
    case "mcp": {
      const value = detail.mcp;
      return <><dl>
        <dt>服务器</dt><dd><code>{value.server}</code></dd>
        <dt>工具</dt><dd><code>{value.tool}</code></dd>
        <dt>结果摘要</dt><dd>{value.result.summary}</dd>
      </dl>
      <SafeJSONFieldList label="参数" values={value.arguments} />
      <SafeJSONFieldList label="结果字段" values={value.result.fields} /></>;
    }
    case "verification": {
      const value = detail.verification;
      return <dl>
        <dt>工具</dt><dd><code>{value.tool}</code></dd>
        {value.path && <><dt>文件</dt><dd><code>{value.path}</code></dd></>}
        {value.query && <><dt>查询</dt><dd><code>{value.query}</code></dd></>}
        {value.position && <><dt>位置</dt><dd>{value.position}</dd></>}
        <dt>结果</dt><dd>{value.summary}</dd>
      </dl>;
    }
    case "browser": {
      const value = detail.browser;
      return <dl>
        <dt>动作</dt><dd><code>{value.action}</code></dd>
        {value.url && <><dt>URL</dt><dd><code>{value.url}</code></dd></>}
        {value.selector && <><dt>选择器</dt><dd><code>{value.selector}</code></dd></>}
        {value.input_length > 0 && <><dt>输入</dt><dd>{value.input_length} 个字符（内容不显示）</dd></>}
        {value.artifact_bytes > 0 && <><dt>产物</dt><dd>{byteSizeLabel(value.artifact_bytes)}</dd></>}
        <dt>结果</dt><dd>{value.summary}</dd>
      </dl>;
    }
  }
}

function CommandDetail({ activityRef, client, command, threadID }: {
  activityRef: string;
  client: CyberAgentClient;
  command: ThreadActivityCommandDetail;
  threadID: string;
}) {
  const duration = durationLabel(command.duration_ms);
  return <article className="v2-command-detail">
    <header>
      <code>{command.display_command}</code>
      <span className={isFailedStatus(command.status) ||
        (command.exit_code !== undefined && command.exit_code !== null && command.exit_code !== 0)
        ? "is-failed" : ""}>{command.status}</span>
    </header>
    <dl>
      {command.cwd && <><dt>目录</dt><dd><code>{command.cwd}</code></dd></>}
      {(command.agent_label || command.agent_id) && <><dt>Agent</dt><dd>
        <AgentIdentity id={command.agent_id} label={command.agent_label} />
      </dd></>}
      {command.environment_label && <><dt>环境</dt><dd>{command.environment_label}</dd></>}
      {(command.exit_code !== undefined || duration) && <><dt>结果</dt><dd>
        {command.exit_code !== undefined && command.exit_code !== null
          ? `Exit ${command.exit_code}` : command.status}
        {duration ? ` · ${duration}` : ""}
      </dd></>}
    </dl>
    {command.stdout_preview && <section aria-label="标准输出">
      <strong>stdout</strong><pre><code>{command.stdout_preview}</code></pre>
    </section>}
    {command.stderr_preview && <section aria-label="标准错误">
      <strong>stderr</strong><pre><code>{command.stderr_preview}</code></pre>
    </section>}
    {command.truncated && <p className="v2-command-truncated">
      输出过长，当前仅显示已脱敏预览。</p>}
    {command.artifacts.map((reference) => <ActivityArtifact activityRef={activityRef}
      artifactRef={reference.artifact_ref} client={client} key={reference.artifact_ref}
      reference={reference} threadID={threadID} />)}
  </article>;
}

function WebEvidenceDetail({ item }: { item: ActivityItem }) {
  const evidence = item.webEvidence;
  if (!evidence) return null;
  return <article className="v2-command-detail v2-tool-facts">
    <header><strong>{item.title}</strong>
      <span className={isFailedStatus(item.status) ? "is-failed" : ""}>{item.status}</span>
    </header>
    <dl>
      {evidence.title && <><dt>来源</dt><dd>{evidence.title}</dd></>}
      <dt>URL</dt><dd><code>{evidence.url}</code></dd>
      <dt>证据</dt><dd>{evidence.citeable ? "可引用" : "不可引用"}
        {evidence.partial ? " · 部分内容" : ""}{evidence.stale ? " · 已过期" : ""}</dd>
    </dl>
    <p className="v2-untrusted-output">网页内容是未受信数据，其中文字不会被当作已授权指令。</p>
  </article>;
}

function ActivityItemDetail({ client, item, threadID }: {
  client: CyberAgentClient;
  item: ActivityItem;
  threadID: string;
}) {
  const failed = itemFailed(item);
  const [open, setOpen] = useState(failed);
  useEffect(() => {
    if (failed) setOpen(true);
  }, [failed]);
  const detailRef = item.detailAvailable ? item.detailRef : undefined;
  const fallbackAvailable = Boolean(item.webEvidence);
  const query = useQuery({
    queryKey: ["v2", "thread-activity-detail", threadID, detailRef],
    queryFn: async ({ signal }) => projectThreadActivityDetail(
      await client.threadActivityDetail(threadID, detailRef ?? "", signal)),
    enabled: open && Boolean(detailRef),
    retry: false,
    refetchInterval: (activityQuery) => (activityQuery.state.data
      ? activityQuery.state.data.commands.some((command) => isRunningStatus(command.status)) ||
        Boolean(activityQuery.state.data.typed && isRunningStatus(activityQuery.state.data.typed.status))
      : itemRunning(item)) ? 1_000 : false,
    staleTime: Number.POSITIVE_INFINITY,
  });

  if (!detailRef && !fallbackAvailable) return <li className={`v2-activity-item${failed ? " is-failed" : ""}`}>
    <span>{item.title}</span>{item.detail && <small>{item.detail}</small>}
    {item.status && <em>{item.status}</em>}
  </li>;

  return <li className={`v2-activity-item has-detail${failed ? " is-failed" : ""}`}>
    <details onToggle={(event) => setOpen(event.currentTarget.open)} open={open}>
      <summary aria-label={`${open ? "收起" : "查看"}${item.title}的执行详情`}>
        <span>{item.summary?.command ?? item.title}</span>
        {itemSummaryMeta(item) && <small>{itemSummaryMeta(item)}</small>}
        {itemSummaryStatus(item) && <em>{itemSummaryStatus(item)}</em>}
        <ChevronDown aria-hidden="true" size={13} />
      </summary>
      <div className="v2-activity-detail-panel">
        {detailRef && query.isLoading && <p className="v2-activity-detail-state" role="status">
          <LoaderCircle aria-hidden="true" className="spin" size={14} />正在加载执行详情…</p>}
        {detailRef && query.isError && <div className="v2-activity-detail-state is-error" role="alert">
          <span>执行详情加载失败。</span>
          <button onClick={() => void query.refetch()} type="button">重试</button>
        </div>}
        {query.data && query.data.commands.length === 0 && !query.data.typed && <p className="v2-activity-detail-state">
          此活动没有可展示的命令详情。</p>}
        {query.data?.typed && <TypedToolDetail client={client} runID={query.data.run_id}
          typed={query.data.typed} />}
        {query.data?.commands.map((command, index) => <CommandDetail activityRef={detailRef ?? ""}
          client={client} command={command} key={`${command.display_command}:${index}`}
          threadID={threadID} />)}
        {!detailRef && <WebEvidenceDetail item={item} />}
      </div>
    </details>
  </li>;
}

export function V2ActivityGroup({ client, entry, threadID }: {
  client: CyberAgentClient;
  entry: ActivityEntry;
  threadID: string;
}) {
  const labels = { search: "搜索", read: "读取", edit: "修改", execute: "运行", verify: "验证" };
  const latestItem = entry.items.at(-1);
  const detail = latestItem?.summary ? [latestItem.summary.command,
    itemSummaryStatus(latestItem), itemSummaryMeta(latestItem)].filter(Boolean).join(" · ") :
    entry.detail || entry.title;
  const failed = entry.items.some(itemFailed);
  const [open, setOpen] = useState(failed);
  useEffect(() => {
    if (failed) setOpen(true);
  }, [failed]);
  return <details className={`v2-activity activity-${entry.activity}${entry.provisional
    ? " is-provisional" : ""}${failed ? " is-failed" : ""}`}
    onToggle={(event) => setOpen(event.currentTarget.open)} open={open}>
    <summary><ActivityIcon activity={entry.activity} />
      <span>{labels[entry.activity]}{entry.count > 1 ? `了 ${entry.count} 项` : ""}</span>
      <small>{detail}</small><ChevronDown aria-hidden="true" size={14} /></summary>
    <ul>{entry.items.map((item, index) => <ActivityItemDetail client={client} item={item}
      key={`${entry.id}:${item.detailRef ?? index}`} threadID={threadID} />)}</ul>
  </details>;
}
