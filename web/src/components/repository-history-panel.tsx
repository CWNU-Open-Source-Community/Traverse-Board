import { useRef, useState, type KeyboardEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Columns2, FileClock, FileCode2, FileDiff, FileInput,
  FileOutput, GitCommitHorizontal, GitCompareArrows, RefreshCw, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import { formatDate } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

export function RepositoryHistoryPanel({ client, workspaceID }: {
  client: CyberAgentClient;
  workspaceID: string;
}) {
  const { t } = useLocale();
  const kindLabel = (kind: string) => {
    if (!kind || kind === "none") return t("无", "none");
    if (kind === "regular") return t("普通文件", "regular");
    if (kind === "executable") return t("可执行文件", "executable");
    if (kind === "directory") return t("目录", "directory");
    if (kind === "symlink") return t("符号链接", "symlink");
    return kind;
  };
  const [selection, setSelection] = useState({ workspaceID: "", objectID: "" });
  const [fileSelection, setFileSelection] = useState({ workspaceID: "", objectID: "", path: "" });
  const [historySelection, setHistorySelection] = useState({ workspaceID: "", path: "" });
  const [comparisonBase, setComparisonBase] = useState({ workspaceID: "", objectID: "" });
  const [comparisonPreview, setComparisonPreview] = useState({ workspaceID: "", baseObjectID: "",
    baseHash: "", baseAvailable: false, headObjectID: "", headHash: "", headAvailable: false,
    path: "" });
  const comparisonPreviewWorkspaceRef = useRef<HTMLElement | null>(null);
  const comparisonPreviewReturnFocusRef = useRef<HTMLButtonElement | null>(null);
  const selectedObjectID = selection.workspaceID === workspaceID ? selection.objectID : "";
  const comparisonBaseObjectID = comparisonBase.workspaceID === workspaceID ?
    comparisonBase.objectID : "";
  const comparisonHeadObjectID = selectedObjectID && selectedObjectID !== comparisonBaseObjectID ?
    selectedObjectID : "";
  const selectedPreviewObjectID = fileSelection.workspaceID === workspaceID ?
    fileSelection.objectID : "";
  const selectedFilePath = fileSelection.workspaceID === workspaceID ? fileSelection.path : "";
  const selectedHistoryPath = historySelection.workspaceID === workspaceID ?
    historySelection.path : "";
  const activeComparisonPreview = comparisonPreview.workspaceID === workspaceID &&
    comparisonPreview.baseObjectID === comparisonBaseObjectID &&
    comparisonPreview.headObjectID === comparisonHeadObjectID ? comparisonPreview : null;
  const query = useQuery({
    queryKey: ["workspace", workspaceID, "repository-history"],
    queryFn: ({ signal }) => client.repositoryHistory(workspaceID, signal),
    enabled: Boolean(workspaceID),
  });
  const detailQuery = useQuery({
    queryKey: ["workspace", workspaceID, "repository-commit", selectedObjectID],
    queryFn: ({ signal }) => client.repositoryCommit(workspaceID, selectedObjectID, signal),
    enabled: Boolean(workspaceID && selectedObjectID),
  });
  const comparisonQuery = useQuery({
    queryKey: ["workspace", workspaceID, "repository-commit-comparison",
      comparisonBaseObjectID, comparisonHeadObjectID],
    queryFn: ({ signal }) => client.repositoryCommitComparison(workspaceID,
      comparisonBaseObjectID, comparisonHeadObjectID, signal),
    enabled: Boolean(workspaceID && comparisonBaseObjectID && comparisonHeadObjectID),
  });
  const fileQuery = useQuery({
    queryKey: ["workspace", workspaceID, "repository-commit", selectedPreviewObjectID,
      "file-preview", selectedFilePath],
    queryFn: ({ signal }) => client.repositoryCommitFilePreview(workspaceID,
      selectedPreviewObjectID, selectedFilePath, signal),
    enabled: Boolean(workspaceID && selectedPreviewObjectID && selectedFilePath),
  });
  const fileHistoryQuery = useQuery({
    queryKey: ["workspace", workspaceID, "repository-file-history", selectedHistoryPath],
    queryFn: ({ signal }) => client.repositoryFileHistory(workspaceID, selectedHistoryPath, signal),
    enabled: Boolean(workspaceID && selectedHistoryPath),
  });
  const comparisonBasePreviewQuery = useQuery({
    queryKey: ["workspace", workspaceID, "repository-comparison-preview",
      activeComparisonPreview?.baseObjectID, activeComparisonPreview?.path, "base"],
    queryFn: ({ signal }) => client.repositoryCommitFilePreview(workspaceID,
      activeComparisonPreview?.baseObjectID ?? "", activeComparisonPreview?.path ?? "", signal),
    enabled: Boolean(workspaceID && activeComparisonPreview?.baseAvailable),
  });
  const comparisonHeadPreviewQuery = useQuery({
    queryKey: ["workspace", workspaceID, "repository-comparison-preview",
      activeComparisonPreview?.headObjectID, activeComparisonPreview?.path, "head"],
    queryFn: ({ signal }) => client.repositoryCommitFilePreview(workspaceID,
      activeComparisonPreview?.headObjectID ?? "", activeComparisonPreview?.path ?? "", signal),
    enabled: Boolean(workspaceID && activeComparisonPreview?.headAvailable),
  });
  const pairedPreviewCandidates = (comparisonQuery.data?.changes ?? []).filter((change) =>
    ["regular", "executable"].includes(change.previous_kind) ||
    ["regular", "executable"].includes(change.current_kind));
  const pairedPreviewIndex = activeComparisonPreview ? pairedPreviewCandidates.findIndex(
    (change) => change.path === activeComparisonPreview.path) : -1;
  const closePairedPreview = () => {
    setComparisonPreview({ workspaceID: "", baseObjectID: "", baseHash: "",
      baseAvailable: false, headObjectID: "", headHash: "", headAvailable: false, path: "" });
    queueMicrotask(() => {
      if (comparisonPreviewReturnFocusRef.current?.isConnected) {
        comparisonPreviewReturnFocusRef.current.focus();
      }
    });
  };
  const selectPairedPreview = (index: number, toggle = false,
    returnFocus?: HTMLButtonElement) => {
    const comparison = comparisonQuery.data;
    const change = pairedPreviewCandidates[index];
    if (!comparison || !change) return;
    if (returnFocus) comparisonPreviewReturnFocusRef.current = returnFocus;
    if (toggle && activeComparisonPreview?.path === change.path) {
      closePairedPreview();
      return;
    }
    setComparisonPreview({ workspaceID, baseObjectID: comparison.base_object_id,
      baseHash: comparison.base_hash,
      baseAvailable: ["regular", "executable"].includes(change.previous_kind),
      headObjectID: comparison.head_object_id, headHash: comparison.head_hash,
      headAvailable: ["regular", "executable"].includes(change.current_kind),
      path: change.path });
    if (returnFocus) {
      queueMicrotask(() => comparisonPreviewWorkspaceRef.current?.focus());
    }
  };
  const handlePairedPreviewKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    if (event.key === "Escape") {
      event.preventDefault();
      closePairedPreview();
    } else if (event.key === "ArrowLeft" && pairedPreviewIndex > 0) {
      event.preventDefault();
      selectPairedPreview(pairedPreviewIndex - 1);
    } else if (event.key === "ArrowRight" && pairedPreviewIndex >= 0 &&
      pairedPreviewIndex < pairedPreviewCandidates.length - 1) {
      event.preventDefault();
      selectPairedPreview(pairedPreviewIndex + 1);
    }
  };
  if (!workspaceID) return null;
  return <section aria-label={t("仓库历史", "Repository history")} className="repository-history-panel">
    <header className="projection-heading">
      <div><GitCommitHorizontal aria-hidden="true" size={17} /><h2>{t("本地历史", "Local history")}</h2></div>
      <div>{query.data?.truncated && <StatusBadge status="truncated" />}
        <button aria-label={t("刷新仓库历史", "Refresh repository history")} className="icon-button"
          disabled={query.isFetching} onClick={() => void query.refetch()}
          title={t("刷新", "Refresh")} type="button"><RefreshCw aria-hidden="true"
            className={query.isFetching ? "spin" : ""} size={15} /></button></div>
    </header>
    {query.isLoading && <LoadingState label={t("正在加载仓库历史", "Loading repository history")} />}
    {query.isError && <ErrorState error={query.error} />}
    {query.data && !query.data.available &&
      <EmptyState>{t("已登记的工作区根目录中没有 Git 仓库", "No Git repository at the registered Workspace root")}</EmptyState>}
    {query.data?.available && <div className="repository-history-grid">
      <section><h3>{t("分支", "Branches")}</h3>
        {query.data.branches.length === 0 ? <EmptyState>{t("没有本地分支", "No local branches")}</EmptyState> :
          <div className="repository-branch-list">{query.data.branches.map((branch) =>
            <div key={branch.name}><span>{branch.name}</span><code>{branch.head}</code>
              {branch.current && <StatusBadge status="current" />}</div>)}</div>}
      </section>
      <section><h3>{t("第一父提交", "First-parent commits")}</h3>
        {query.data.commits.length === 0 ? <EmptyState>{t("没有提交", "No commits")}</EmptyState> :
          <div className="repository-commit-list">{query.data.commits.map((commit) =>
            <div key={commit.object_id}><code>{commit.hash}</code><span>{commit.subject}</span>
              <time dateTime={commit.committed_at}>{formatDate(commit.committed_at)}</time>
              <span className="repository-commit-flags">
                {commit.redacted && <StatusBadge status="redacted" />}
              </span>
              <button aria-label={t(`检查提交 ${commit.hash}`, `Inspect commit ${commit.hash}`)} aria-pressed={selectedObjectID === commit.object_id}
                className="icon-button" onClick={() => {
                  setFileSelection({ workspaceID: "", objectID: "", path: "" });
                  setSelection((current) =>
                    current.workspaceID === workspaceID && current.objectID === commit.object_id ?
                      { workspaceID: "", objectID: "" } : { workspaceID, objectID: commit.object_id });
                }}
                title={t("检查变更文件", "Inspect changed files")} type="button">
                <FileDiff aria-hidden="true" size={14} />
              </button></div>)}</div>}
      </section>
    </div>}
    {selectedObjectID && <section aria-label={t("精确提交元数据", "Exact commit metadata")} className="repository-commit-detail">
      {detailQuery.isLoading && <LoadingState label={t("正在加载精确提交元数据", "Loading exact commit metadata")} />}
      {detailQuery.isError && <ErrorState error={detailQuery.error} />}
      {detailQuery.data && <>
        <header><span><code>{detailQuery.data.hash}</code><strong>{detailQuery.data.subject}</strong></span>
          <span><StatusBadge status="changed" label={t(`${detailQuery.data.changed_file_count} 个文件已更改`, `${detailQuery.data.changed_file_count} changed`)} />
            {comparisonBaseObjectID === selectedObjectID && <StatusBadge status="comparison-base" label={t("比较基准", "comparison base")} />}
            {detailQuery.data.truncated && <StatusBadge status="truncated" />}
            <button aria-label={comparisonBaseObjectID === selectedObjectID ?
              t(`清除比较基准 ${detailQuery.data.hash}`, `Clear comparison base ${detailQuery.data.hash}`) :
              t(`将 ${detailQuery.data.hash} 设为比较基准`, `Use ${detailQuery.data.hash} as comparison base`)}
              aria-pressed={comparisonBaseObjectID === selectedObjectID} className="icon-button"
              onClick={() => setComparisonBase((current) =>
                current.workspaceID === workspaceID && current.objectID === selectedObjectID ?
                  { workspaceID: "", objectID: "" } : { workspaceID, objectID: selectedObjectID })}
              title={comparisonBaseObjectID === selectedObjectID ?
                t("清除比较基准", "Clear comparison base") : t("设为比较基准", "Use as comparison base")} type="button">
              <GitCompareArrows aria-hidden="true" size={14} />
            </button></span></header>
        {detailQuery.data.changes.length === 0 ? <EmptyState>{t("此提交中没有变更文件", "No changed files in this commit")}</EmptyState> :
          <div className="repository-commit-change-list">{detailQuery.data.changes.map((change) =>
            <div key={`${change.change}:${change.path}`}><StatusBadge status={change.change} />
              <code title={change.path}>{change.path}</code>
              <span>{kindLabel(change.previous_kind)} {t("至", "to")} {kindLabel(change.current_kind)}</span>
              <span>{change.content_changed ? t("内容", "content") : t("仅模式", "mode only")}</span>
              <button aria-label={t(`检查 ${change.path} 的历史`, `Inspect history for ${change.path}`)}
                aria-pressed={selectedHistoryPath === change.path} className="icon-button"
                onClick={() => setHistorySelection((current) =>
                  current.workspaceID === workspaceID && current.path === change.path ?
                    { workspaceID: "", path: "" } : { workspaceID, path: change.path })}
                title={t("检查文件历史", "Inspect file history")} type="button">
                <FileClock aria-hidden="true" size={14} />
              </button>
              {["regular", "executable"].includes(change.current_kind) ?
                <button aria-label={t(`预览 ${detailQuery.data.hash} 中的 ${change.path}`, `Preview ${change.path} at ${detailQuery.data.hash}`)}
                  aria-pressed={selectedPreviewObjectID === selectedObjectID &&
                    selectedFilePath === change.path} className="icon-button"
                  onClick={() => setFileSelection((current) =>
                    current.workspaceID === workspaceID && current.objectID === selectedObjectID &&
                      current.path === change.path ? { workspaceID: "", objectID: "", path: "" } :
                      { workspaceID, objectID: selectedObjectID, path: change.path })}
                  title={t("预览脱敏文件", "Preview redacted file")} type="button">
                  <FileCode2 aria-hidden="true" size={14} />
                </button> : <span aria-hidden="true" className="repository-preview-placeholder" />}
            </div>)}</div>}
        {detailQuery.data.omitted_change_count > 0 &&
          <p className="repository-diff-omitted">{t(`另有 ${detailQuery.data.omitted_change_count} 项变更已省略`, `${detailQuery.data.omitted_change_count} additional changes omitted`)}</p>}
        {selectedFilePath && <section aria-label={t("精确提交文件预览", "Exact commit file preview")}
          className="repository-commit-file-preview">
          {fileQuery.isLoading && <LoadingState label={t("正在加载脱敏提交文件", "Loading redacted commit file")} />}
          {fileQuery.isError && <ErrorState error={fileQuery.error} />}
          {fileQuery.data && <>
            <header><code title={`${fileQuery.data.hash} / ${fileQuery.data.path}`}>
              {fileQuery.data.hash} / {fileQuery.data.path}</code>
              <span><StatusBadge status={fileQuery.data.kind} />
                {fileQuery.data.redacted && <StatusBadge status="redacted" />}</span></header>
            <pre>{fileQuery.data.content}</pre>
          </>}
        </section>}
      </>}
    </section>}
    {comparisonBaseObjectID && comparisonHeadObjectID &&
      <section aria-label={t("精确提交比较", "Exact commit comparison")} className="repository-commit-comparison">
        {comparisonQuery.isLoading && <LoadingState label={t("正在加载精确提交比较", "Loading exact commit comparison")} />}
        {comparisonQuery.isError && <ErrorState error={comparisonQuery.error} />}
        {comparisonQuery.data && <>
          <header><span><GitCompareArrows aria-hidden="true" size={14} />
            <code>{comparisonQuery.data.base_hash}</code><span>{t("至", "to")}</span>
            <code>{comparisonQuery.data.head_hash}</code></span>
            <span><StatusBadge status="changed" label={t(`${comparisonQuery.data.changed_file_count} 个文件已更改`, `${comparisonQuery.data.changed_file_count} changed`)} />
              {comparisonQuery.data.truncated && <StatusBadge status="truncated" />}</span></header>
          <div className="repository-commit-comparison-subjects">
            <span title={comparisonQuery.data.base_subject}>{comparisonQuery.data.base_subject}</span>
            <span title={comparisonQuery.data.head_subject}>{comparisonQuery.data.head_subject}</span>
          </div>
          {comparisonQuery.data.changes.length === 0 ?
            <EmptyState>{t("这些提交之间没有元数据变更", "No metadata changes between these commits")}</EmptyState> :
            <div className="repository-commit-comparison-list">{comparisonQuery.data.changes.map((change) =>
              <div key={`${change.change}:${change.path}`}><StatusBadge status={change.change} />
                <code title={change.path}>{change.path}</code>
                <span>{kindLabel(change.previous_kind)} {t("至", "to")} {kindLabel(change.current_kind)}</span>
                <span>{change.content_changed ? t("内容", "content") : t("仅模式", "mode only")}</span>
                <span className="repository-commit-comparison-actions">
                  {["regular", "executable"].includes(change.previous_kind) ?
                    <button aria-label={t(`预览比较基准 ${comparisonQuery.data.base_hash} 中的 ${change.path}`, `Preview ${change.path} at comparison base ${comparisonQuery.data.base_hash}`)}
                      aria-pressed={selectedPreviewObjectID === comparisonQuery.data.base_object_id &&
                        selectedFilePath === change.path} className="icon-button"
                      onClick={() => setFileSelection((current) =>
                        current.workspaceID === workspaceID &&
                          current.objectID === comparisonQuery.data.base_object_id &&
                          current.path === change.path ?
                          { workspaceID: "", objectID: "", path: "" } :
                          { workspaceID, objectID: comparisonQuery.data.base_object_id,
                            path: change.path })}
                      title={t("预览脱敏基准文件", "Preview redacted base file")} type="button">
                      <FileInput aria-hidden="true" size={14} />
                    </button> : <span aria-hidden="true" className="repository-preview-placeholder" />}
                  {["regular", "executable"].includes(change.current_kind) ?
                    <button aria-label={t(`预览比较目标 ${comparisonQuery.data.head_hash} 中的 ${change.path}`, `Preview ${change.path} at comparison head ${comparisonQuery.data.head_hash}`)}
                      aria-pressed={selectedPreviewObjectID === comparisonQuery.data.head_object_id &&
                        selectedFilePath === change.path} className="icon-button"
                      onClick={() => setFileSelection((current) =>
                        current.workspaceID === workspaceID &&
                          current.objectID === comparisonQuery.data.head_object_id &&
                          current.path === change.path ?
                          { workspaceID: "", objectID: "", path: "" } :
                          { workspaceID, objectID: comparisonQuery.data.head_object_id,
                            path: change.path })}
                      title={t("预览脱敏目标文件", "Preview redacted head file")} type="button">
                      <FileOutput aria-hidden="true" size={14} />
                    </button> : <span aria-hidden="true" className="repository-preview-placeholder" />}
                  {["regular", "executable"].includes(change.previous_kind) ||
                    ["regular", "executable"].includes(change.current_kind) ?
                    <button aria-label={t(`比较 ${comparisonQuery.data.base_hash} 与 ${comparisonQuery.data.head_hash} 之间 ${change.path} 的脱敏预览`, `Compare redacted previews for ${change.path} between ${comparisonQuery.data.base_hash} and ${comparisonQuery.data.head_hash}`)}
                      aria-pressed={activeComparisonPreview?.path === change.path}
                      className="icon-button" onClick={(event) => selectPairedPreview(
                        pairedPreviewCandidates.findIndex((candidate) => candidate.path === change.path),
                        true, event.currentTarget)}
                      title={t("打开成对脱敏预览", "Open paired redacted previews")} type="button">
                      <Columns2 aria-hidden="true" size={14} />
                    </button> : <span aria-hidden="true" className="repository-preview-placeholder" />}
                </span>
              </div>)}</div>}
          {comparisonQuery.data.omitted_change_count > 0 &&
            <p className="repository-diff-omitted">
              {t(`另有 ${comparisonQuery.data.omitted_change_count} 项变更已省略`, `${comparisonQuery.data.omitted_change_count} additional changes omitted`)}
            </p>}
          {activeComparisonPreview &&
            <section aria-keyshortcuts="ArrowLeft ArrowRight Escape"
              aria-label={t("成对脱敏文件预览", "Paired redacted file preview")}
              className="repository-comparison-preview-workspace"
              onKeyDown={handlePairedPreviewKeyDown} ref={comparisonPreviewWorkspaceRef}
              tabIndex={0}>
              <header><span><Columns2 aria-hidden="true" size={14} />
                <code title={activeComparisonPreview.path}>{activeComparisonPreview.path}</code></span>
                <div className="repository-comparison-preview-controls">
                  <span aria-live="polite">{t(`第 ${pairedPreviewIndex + 1} 项，共 ${pairedPreviewCandidates.length} 项`, `${pairedPreviewIndex + 1} of ${pairedPreviewCandidates.length}`)}</span>
                  <button aria-keyshortcuts="ArrowLeft"
                    aria-label={t("上一项成对脱敏预览", "Previous paired redacted preview")} className="icon-button"
                    disabled={pairedPreviewIndex <= 0}
                    onClick={() => selectPairedPreview(pairedPreviewIndex - 1)}
                    title={t("上一个变更文件", "Previous changed file")} type="button">
                    <ChevronLeft aria-hidden="true" size={14} />
                  </button>
                  <button aria-keyshortcuts="ArrowRight"
                    aria-label={t("下一项成对脱敏预览", "Next paired redacted preview")} className="icon-button"
                    disabled={pairedPreviewIndex < 0 ||
                      pairedPreviewIndex >= pairedPreviewCandidates.length - 1}
                    onClick={() => selectPairedPreview(pairedPreviewIndex + 1)}
                    title={t("下一个变更文件", "Next changed file")} type="button">
                    <ChevronRight aria-hidden="true" size={14} />
                  </button>
                  <button aria-keyshortcuts="Escape"
                    aria-label={t("关闭成对脱敏预览", "Close paired redacted preview")} className="icon-button"
                    onClick={closePairedPreview} title={t("关闭", "Close")} type="button">
                    <X aria-hidden="true" size={14} />
                  </button>
                  <StatusBadge status="read-only" />
                </div></header>
              <div>
                <section aria-label={t("基准脱敏文件预览", "Base redacted file preview")}>
                  <header><strong>{t("基准", "Base")}</strong><code>{activeComparisonPreview.baseHash}</code></header>
                  {!activeComparisonPreview.baseAvailable ?
                    <EmptyState>{t("基准提交中不存在此文件", "File is absent at the base commit")}</EmptyState> : <>
                      {comparisonBasePreviewQuery.isLoading &&
                        <LoadingState label={t("正在加载脱敏基准文件", "Loading redacted base file")} />}
                      {comparisonBasePreviewQuery.isError &&
                        <ErrorState error={comparisonBasePreviewQuery.error} />}
                      {comparisonBasePreviewQuery.data && <>
                        <div className="repository-comparison-preview-meta">
                          <code title={`${comparisonBasePreviewQuery.data.hash} / ${comparisonBasePreviewQuery.data.path}`}>
                            {comparisonBasePreviewQuery.data.hash} / {comparisonBasePreviewQuery.data.path}
                          </code><span><StatusBadge status={comparisonBasePreviewQuery.data.kind} />
                            {comparisonBasePreviewQuery.data.redacted &&
                              <StatusBadge status="redacted" />}</span></div>
                        <pre>{comparisonBasePreviewQuery.data.content}</pre>
                      </>}
                    </>}
                </section>
                <section aria-label={t("目标脱敏文件预览", "Head redacted file preview")}>
                  <header><strong>{t("目标", "Head")}</strong><code>{activeComparisonPreview.headHash}</code></header>
                  {!activeComparisonPreview.headAvailable ?
                    <EmptyState>{t("目标提交中不存在此文件", "File is absent at the head commit")}</EmptyState> : <>
                      {comparisonHeadPreviewQuery.isLoading &&
                        <LoadingState label={t("正在加载脱敏目标文件", "Loading redacted head file")} />}
                      {comparisonHeadPreviewQuery.isError &&
                        <ErrorState error={comparisonHeadPreviewQuery.error} />}
                      {comparisonHeadPreviewQuery.data && <>
                        <div className="repository-comparison-preview-meta">
                          <code title={`${comparisonHeadPreviewQuery.data.hash} / ${comparisonHeadPreviewQuery.data.path}`}>
                            {comparisonHeadPreviewQuery.data.hash} / {comparisonHeadPreviewQuery.data.path}
                          </code><span><StatusBadge status={comparisonHeadPreviewQuery.data.kind} />
                            {comparisonHeadPreviewQuery.data.redacted &&
                              <StatusBadge status="redacted" />}</span></div>
                        <pre>{comparisonHeadPreviewQuery.data.content}</pre>
                      </>}
                    </>}
                </section>
              </div>
            </section>}
        </>}
      </section>}
    {selectedHistoryPath && <section aria-label={t("精确文件历史", "Exact file history")}
      className="repository-file-history">
      {fileHistoryQuery.isLoading && <LoadingState label={t("正在加载精确文件历史", "Loading exact file history")} />}
      {fileHistoryQuery.isError && <ErrorState error={fileHistoryQuery.error} />}
      {fileHistoryQuery.data && <>
        <header><code title={fileHistoryQuery.data.path}>{fileHistoryQuery.data.path}</code>
          <span><StatusBadge status="changes" label={t(`${fileHistoryQuery.data.returned_entry_count} 项变更`, `${fileHistoryQuery.data.returned_entry_count} changes`)} />
            {fileHistoryQuery.data.truncated && <StatusBadge status="truncated" />}</span></header>
        {!fileHistoryQuery.data.observed ? <EmptyState>{t("在有界历史中未发现变更", "No changes found in the bounded history")}</EmptyState> :
          <div className="repository-file-history-list">{fileHistoryQuery.data.entries.map((entry) =>
            <div key={entry.object_id}><StatusBadge status={entry.change} />
              <code>{entry.hash}</code><span>{entry.subject}</span>
              <time dateTime={entry.committed_at}>{formatDate(entry.committed_at)}</time>
              <span>{kindLabel(entry.previous_kind)} {t("至", "to")} {kindLabel(entry.current_kind)}</span>
              <span className="repository-file-history-flags">
                {entry.redacted && <StatusBadge status="redacted" />}
              </span>
              <span className="repository-file-history-actions">
                <button aria-label={t(`从 ${fileHistoryQuery.data.path} 的历史中打开提交 ${entry.hash}`, `Open commit ${entry.hash} from history for ${fileHistoryQuery.data.path}`)}
                  aria-pressed={selectedObjectID === entry.object_id} className="icon-button"
                  onClick={() => {
                    setSelection({ workspaceID, objectID: entry.object_id });
                    setFileSelection({ workspaceID: "", objectID: "", path: "" });
                  }} title={t("打开精确提交", "Open exact commit")} type="button">
                  <FileDiff aria-hidden="true" size={14} />
                </button>
                {["regular", "executable"].includes(entry.current_kind) ?
                  <button aria-label={t(`预览历史提交 ${entry.hash} 中的 ${fileHistoryQuery.data.path}`, `Preview ${fileHistoryQuery.data.path} at history commit ${entry.hash}`)}
                    aria-pressed={selectedObjectID === entry.object_id &&
                      selectedFilePath === fileHistoryQuery.data.path} className="icon-button"
                    onClick={() => {
                      setSelection({ workspaceID, objectID: entry.object_id });
                      setFileSelection({ workspaceID, objectID: entry.object_id,
                        path: fileHistoryQuery.data.path });
                    }} title={t("预览精确提交中的脱敏文件", "Preview redacted file at exact commit")} type="button">
                    <FileCode2 aria-hidden="true" size={14} />
                  </button> : <span aria-hidden="true" className="repository-preview-placeholder" />}
              </span>
            </div>)}</div>}
      </>}
    </section>}
  </section>;
}
