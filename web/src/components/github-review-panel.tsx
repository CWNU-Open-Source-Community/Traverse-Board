import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, GitPullRequest, RefreshCw, ShieldCheck } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { GitHubReviewWriteReviewResultView, GitHubReviewWriteSpecView } from "../api/types";
import { formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, KeyValue, LoadingState, StatusBadge } from "./common";

function operationKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `desktop-github-review-${crypto.randomUUID()}`;
  }
  return `desktop-github-review-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function GitHubReviewPanel({ client, runID, onOpenApprovals,
  onOpenDelivery, retainedReview, onRetainedReviewChange }: {
  client: CyberAgentClient;
  runID: string;
  onOpenApprovals: () => void;
  onOpenDelivery?: () => void;
  retainedReview?: GitHubReviewWriteReviewResultView | null;
  onRetainedReviewChange?: (value: GitHubReviewWriteReviewResultView | null) => void;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [repository, setRepository] = useState("");
  const [credentialName, setCredentialName] = useState("prayu-github-app");
  const [clientID, setClientID] = useState("");
  const [writeEnabled, setWriteEnabled] = useState(false);
  const [connectionID, setConnectionID] = useState("");
  const [pullRequest, setPullRequest] = useState(0);
  const [device, setDevice] = useState<{ session_id: string; user_code: string;
    verification_uri: string } | null>(null);
  const [reviewBody, setReviewBody] = useState("");
  const [reviewEvent, setReviewEvent] = useState("COMMENT");
  const [localReview, setLocalReview] = useState<GitHubReviewWriteReviewResultView | null>(null);
  const review = retainedReview === undefined ? localReview : retainedReview;
  const setReview = (value: GitHubReviewWriteReviewResultView | null) => {
    setLocalReview(value);
    onRetainedReviewChange?.(value);
  };

  const connections = useQuery({
    queryKey: ["github-review", "connections"],
    queryFn: ({ signal }) => client.githubReviewConnections(false, signal),
    enabled: client.hasGitHubReviewControl,
  });
  useEffect(() => {
    if (!connectionID && connections.data?.[0]) setConnectionID(connections.data[0].connection.id);
  }, [connectionID, connections.data]);
  const projection = useQuery({
    queryKey: ["run", runID, "github-review", connectionID, pullRequest],
    queryFn: ({ signal }) => client.githubReviewProjection(runID, connectionID, pullRequest, signal),
    enabled: client.hasGitHubReviewControl && Boolean(runID && connectionID),
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["github-review"] });
    void queryClient.invalidateQueries({ queryKey: ["run", runID, "github-review"] });
  };
  const configure = useMutation({
    mutationFn: () => {
      const [owner, name, extra] = repository.trim().split("/");
      if (!owner || !name || extra) throw new Error(t("仓库必须为 owner/name", "Repository must be owner/name"));
      return client.configureGitHubReview({
        repository: { host: "github.com", owner, name, full_name: `${owner}/${name}`, private: false },
        credential: { name: credentialName.trim(), kind: "github_app_device" },
        client_id: clientID.trim(), allowed_log_hosts: [], write_enabled: writeEnabled,
        enabled: true, expected_generation: 0,
      });
    },
    onSuccess: (value) => { setConnectionID(value.connection.id); invalidate(); },
  });
  const beginDevice = useMutation({
    mutationFn: () => client.beginGitHubReviewDeviceFlow(connectionID),
    onSuccess: (value) => setDevice(value),
  });
  const pollDevice = useMutation({
    mutationFn: () => {
      if (!device) throw new Error("Device Flow session is unavailable");
      return client.pollGitHubReviewDeviceFlow(connectionID, device.session_id);
    },
    onSuccess: (value) => { if (value.configured) setDevice(null); invalidate(); },
  });
  const qualify = useMutation({ mutationFn: () => client.qualifyGitHubReview(connectionID, pullRequest) });
  const fetchSnapshot = useMutation({
    mutationFn: () => client.fetchGitHubReview(connectionID, pullRequest), onSuccess: invalidate,
  });
  const buildEvidence = useMutation({
    mutationFn: (snapshotID: string) => client.buildGitHubReviewEvidence(runID, snapshotID),
    onSuccess: invalidate,
  });
  const reviewWrite = useMutation({
    mutationFn: () => {
      const snapshot = projection.data?.snapshots[0];
      if (!snapshot) throw new Error(t("请先抓取 PR 快照", "Fetch a PR snapshot first"));
      const spec: GitHubReviewWriteSpecView = {
        protocol_version: "github-review-write.v1", operation: "submit_review",
        identity: snapshot.identity, credential: projection.data!.connection.credential,
        capability_generation: snapshot.capability.generation, body: reviewBody,
        review_event: reviewEvent, reviewers: [],
        validation_summary: "Operator-reviewed Traverse Board evidence graph",
      };
      return client.reviewGitHubWrite(runID, { connection_id: connectionID,
        snapshot_id: snapshot.id, operation_key: operationKey(), spec });
    },
    onSuccess: (value) => { setReview(value); invalidate();
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "approvals"] }); },
  });
  const executeWrite = useMutation({
    mutationFn: () => {
      const approvalID = review && "ID" in review.approval ? String(review.approval.ID) : "";
      if (!review || !approvalID) throw new Error("Approval identity is unavailable");
      return client.executeGitHubWrite(runID, review.operation.id, approvalID);
    },
    onSuccess: () => { setReview(null); setReviewBody(""); invalidate(); },
  });
  const pending = configure.isPending || beginDevice.isPending || pollDevice.isPending ||
    qualify.isPending || fetchSnapshot.isPending || buildEvidence.isPending ||
    reviewWrite.isPending || executeWrite.isPending;
  const error = configure.error || beginDevice.error || pollDevice.error || qualify.error ||
    fetchSnapshot.error || buildEvidence.error || reviewWrite.error || executeWrite.error;
  const latest = projection.data?.snapshots[0];
  const connectionWriteEnabled = projection.data?.connection.network.write_enabled === true;
  const failedJobs = useMemo(() => latest?.jobs.filter((job) =>
    job.conclusion && !["success", "skipped", "neutral"].includes(job.conclusion)) ?? [], [latest]);
  const staleMappings = useMemo(() => projection.data?.evidence.flatMap((item) =>
    item.graph.mappings.filter((mapping) => mapping.state !== "verified")) ?? [], [projection.data]);

  if (!client.hasGitHubReviewControl) return <section className="repository-state-panel">
    <header className="panel-header"><div><GitPullRequest size={17} /><h2>GitHub Review</h2></div></header>
    <EmptyState>{t("当前进程未启用 GitHub 审阅控制。", "GitHub review control is disabled for this process.")}</EmptyState>
  </section>;
  if (connections.isLoading) return <LoadingState label={t("加载 GitHub 连接", "Loading GitHub connections")} />;
  if (connections.isError) return <ErrorState error={connections.error} />;

  return <section aria-label="GitHub Review" className="repository-state-panel github-review-panel">
    <header className="panel-header"><div><GitPullRequest size={17} /><h2>GitHub Review</h2></div>
      <button className="icon-button" disabled={pending} onClick={() => { void connections.refetch(); void projection.refetch(); }} type="button">
        <RefreshCw className={pending ? "spin" : ""} size={16} />
      </button></header>
    {error && <ErrorState error={error} />}

    <section className="github-review-section">
      <h3>{t("账户与仓库", "Account & repository")}</h3>
      <div className="github-review-form">
        <select aria-label={t("GitHub 连接", "GitHub connection")} value={connectionID}
          onChange={(event) => setConnectionID(event.target.value)}>
          <option value="">{t("选择连接", "Select connection")}</option>
          {connections.data?.map((item) => <option key={item.connection.id} value={item.connection.id}>
            {item.connection.repository.full_name} · {item.credential.configured ? t("已登录", "signed in") : t("未登录", "signed out")}
          </option>)}
        </select>
        <input aria-label={t("仓库", "Repository")} onChange={(event) => setRepository(event.target.value)}
          placeholder="owner/repository" value={repository} />
        <input aria-label={t("凭据引用", "Credential reference")} onChange={(event) => setCredentialName(event.target.value)}
          placeholder="prayu-github-app" value={credentialName} />
        <input aria-label="GitHub App Client ID" onChange={(event) => setClientID(event.target.value)}
          placeholder="GitHub App Client ID" value={clientID} />
        <label><input checked={writeEnabled} onChange={(event) => setWriteEnabled(event.target.checked)}
          type="checkbox" />{t("允许逐次审批的远端写回", "Allow per-call approved write-back")}</label>
        <button disabled={pending || !repository || !clientID} onClick={() => configure.mutate()} type="button">
          {t("保存连接", "Save connection")}
        </button>
      </div>
      {connectionID && <div className="github-review-actions">
        <button disabled={pending} onClick={() => beginDevice.mutate()} type="button">{t("设备登录", "Device sign-in")}</button>
      </div>}
      {device && <div className="github-review-device"><code>{device.user_code}</code>
        <a href={device.verification_uri} rel="noreferrer" target="_blank">github.com/login/device <ExternalLink size={12} /></a>
        <button disabled={pending} onClick={() => pollDevice.mutate()} type="button">{t("检查授权", "Check authorization")}</button>
      </div>}
    </section>

    {connectionID && <section className="github-review-section">
      <h3>{t("拉取请求证据", "Pull request evidence")}</h3>
      <div className="github-review-form"><input aria-label={t("PR 编号", "PR number")} min={1}
        onChange={(event) => setPullRequest(Number(event.target.value))} type="number" value={pullRequest || ""} />
        <button disabled={pending || pullRequest < 1} onClick={() => qualify.mutate()} type="button">{t("资格诊断", "Qualify")}</button>
        <button disabled={pending || pullRequest < 1} onClick={() => fetchSnapshot.mutate()} type="button">{t("抓取快照", "Fetch snapshot")}</button></div>
      {qualify.data && <div className="github-review-diagnostics"><StatusBadge status={qualify.data.qualification.eligible ? "qualified" : "blocked"} />
        {qualify.data.qualification.diagnostics.map((item) => <small key={item.code}>{item.code}: {item.message}</small>)}</div>}
      {projection.isLoading && <LoadingState />}
      {projection.isError && <ErrorState error={projection.error} />}
      {projection.data?.standard_code_delivery && <div className="github-review-delivery-truth">
        <span><strong>{t("交付真实性", "Delivery truth")}</strong>
          <code>{projection.data.standard_code_delivery.receipt_sha256}</code>
          <small>{projection.data.standard_code_delivery.diff.changed_count} {t("个文件", "files")} · {projection.data.standard_code_delivery.verifications.length} {t("条命令", "commands")}</small></span>
        <StatusBadge status={projection.data.standard_code_delivery.status} />
        {onOpenDelivery && <button className="compact-command" onClick={onOpenDelivery} type="button">
          {t("打开交付页", "Open delivery")}</button>}
      </div>}
      {latest && <><dl className="repository-reference github-review-stats">
        <KeyValue label="PR" value={`#${latest.identity.number} ${latest.title.text}`} />
        <KeyValue label="HEAD" value={shortID(latest.identity.head_sha)} />
        <KeyValue label={t("文件", "Files")} value={latest.files.length} />
        <KeyValue label={t("线程", "Threads")} value={latest.threads.length} />
        <KeyValue label="CI" value={`${latest.check_runs.length} / ${failedJobs.length} failed`} />
        <KeyValue label={t("抓取时间", "Fetched")} value={formatDate(latest.fetched_at)} />
      </dl><div className="github-review-actions"><button disabled={pending}
        onClick={() => buildEvidence.mutate(latest.id)} type="button">{t("绑定本地证据", "Bind local evidence")}</button></div></>}
      {failedJobs.map((job) => <article className="github-review-row" key={job.id}>
        <StatusBadge status={job.conclusion ?? job.status} /><strong>{job.name}</strong>
        <small>{job.failed_log.text || job.log_reason || t("无日志摘录", "No log excerpt")}</small>
      </article>)}
      {staleMappings.length > 0 && <details><summary>{t("非当前映射", "Non-current mappings")} · {staleMappings.length}</summary>
        {staleMappings.map((mapping) => <article className="github-review-row" key={mapping.comment_id}>
          <StatusBadge status={mapping.state} /><code>{mapping.path || mapping.comment_id}</code>
          <small>{mapping.reasons.join(" · ")}</small></article>)}</details>}
    </section>}

    {latest && !connectionWriteEnabled && <section className="github-review-section">
      <h3>{t("审批后回写", "Approval-gated write-back")}</h3>
      <EmptyState>{t(
        "此连接保持只读；重新配置并显式允许写回后，才会显示远端操作。",
        "This connection is read-only. Explicitly enable write-back in its configuration to expose remote operations.",
      )}</EmptyState>
    </section>}
    {latest && connectionWriteEnabled && <section className="github-review-section">
      <h3>{t("审批后回写", "Approval-gated write-back")}</h3>
      <div className="github-review-form"><select value={reviewEvent} onChange={(event) => setReviewEvent(event.target.value)}>
        <option value="COMMENT">COMMENT</option><option value="APPROVE">APPROVE</option>
        <option value="REQUEST_CHANGES">REQUEST_CHANGES</option></select>
        <textarea aria-label={t("审阅正文", "Review body")} onChange={(event) => setReviewBody(event.target.value)}
          placeholder={t("远端内容会被视为不可信数据", "Remote content remains untrusted data")}
          value={reviewBody} /><button disabled={pending || (reviewEvent === "REQUEST_CHANGES" && !reviewBody.trim())}
          onClick={() => reviewWrite.mutate()} type="button">{t("生成精确预览", "Create exact preview")}</button></div>
      {review && <div className="github-review-approval"><ShieldCheck size={15} />
        <code>{review.preview.approval_fingerprint}</code>
        <button onClick={onOpenApprovals} type="button">{t("打开审批", "Open approvals")}</button>
        <button disabled={pending} onClick={() => executeWrite.mutate()} type="button">{t("执行已批准操作", "Execute approved write")}</button></div>}
      {projection.data?.writes.map((item) => <article className="github-review-row" key={item.id}>
        <StatusBadge status={item.status} /><strong>{item.preview.operation}</strong>
        <small>{item.receipt.result_url || item.error_code || item.preview.body_summary || item.id}</small>
      </article>)}
    </section>}
  </section>;
}
