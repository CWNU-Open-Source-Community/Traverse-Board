import { useState } from "react";
import type { GitHubReviewProjectionView, GitHubReviewWriteSpecView } from "../api/types";
import { useLocale } from "../lib/locale";
import "./github-review-write-form.css";

type Snapshot = GitHubReviewProjectionView["snapshots"][number];
type WriteOperation = "submit_review" | "reply" | "resolve" | "unresolve" | "request_reviewer";
export type GitHubReviewWriteDraft = Pick<GitHubReviewWriteSpecView,
  "operation" | "body" | "review_event" | "target_id" | "reviewers">;

// These flags only determine which controls are available. Go rechecks authority.
export function githubWriteSupported(snapshot: Snapshot, operation: string): boolean {
  switch (operation) {
    case "reply": return snapshot.capability.reply;
    case "resolve": case "unresolve": return snapshot.capability.resolve;
    case "submit_review": return snapshot.capability.review;
    case "request_reviewer": return snapshot.capability.request_reviewer;
    default: return false;
  }
}

export function GitHubReviewThreads({ snapshot }: { snapshot: Snapshot }) {
  const { t } = useLocale();
  return <section className="github-review-section">
    <h3>{t("审阅讨论", "Review discussions")}</h3>
    {snapshot.threads.length === 0 && <p>{t("当前快照没有审阅讨论。", "No review discussions in this snapshot.")}</p>}
    {snapshot.threads.map((thread) => <details key={thread.id} className="github-review-thread">
      <summary>{thread.path}{thread.line ? `:${thread.line}` : ""} · {thread.resolved ?
        t("已解决", "Resolved") : t("待处理", "Unresolved")}{thread.outdated ? ` · ${t("旧版本", "Outdated")}` : ""}</summary>
      {thread.comments.map((comment) => <article key={comment.node_id}>
        <strong>{comment.author}</strong><p>{comment.body.text}</p>
        {comment.body.truncated && <small>{t("评论内容已截断。", "Comment content is truncated.")}</small>}
      </article>)}
    </details>)}
    {snapshot.requested_reviewers.length > 0 && <p>{t("已请求审阅人", "Requested reviewers")}: {snapshot.requested_reviewers.join(", ")}</p>}
  </section>;
}

export function GitHubReviewWriteForm({ snapshot, disabled, onChange, onPreview }: {
  snapshot: Snapshot;
  disabled: boolean;
  onChange: () => void;
  onPreview: (draft: GitHubReviewWriteDraft) => void;
}) {
  const { t } = useLocale();
  const [operation, setOperation] = useState<WriteOperation>("submit_review");
  const [body, setBody] = useState("");
  const [reviewEvent, setReviewEvent] = useState("COMMENT");
  const [targetID, setTargetID] = useState("");
  const [reviewerInput, setReviewerInput] = useState("");
  const reviewers = [...new Set(reviewerInput.split(/[\s,]+/u).filter(Boolean))].sort();
  const reviewersValid = reviewers.length > 0 && reviewers.length <= 32 && reviewers.every((name) =>
    [...name].length <= 100 && /^[\p{L}\p{N}_-][\p{L}\p{N}_.-]*$/u.test(name) && !name.endsWith("."));
  const threadAction = operation === "reply" || operation === "resolve" || operation === "unresolve";
  const selectedThread = snapshot.threads.find((thread) => thread.id === targetID);
  const targetValid = !threadAction || Boolean(selectedThread &&
    (operation !== "resolve" || !selectedThread.resolved) && (operation !== "unresolve" || selectedThread.resolved));
  const bodyRequired = operation === "reply" || (operation === "submit_review" && reviewEvent === "REQUEST_CHANGES");
  const supported = githubWriteSupported(snapshot, operation);
  const bodyValid = new TextEncoder().encode(body.trim()).length <= 65536;
  const valid = targetValid && bodyValid && (!bodyRequired || Boolean(body.trim())) &&
    (operation !== "request_reviewer" || reviewersValid);
  const change = (update: () => void) => { onChange(); update(); };

  return <div className="github-review-form">
    <label>{t("要执行的操作", "Review action")}
      <select aria-label={t("审阅操作", "Review action")} value={operation}
        onChange={(event) => change(() => { setOperation(event.target.value as WriteOperation); setTargetID(""); })}>
        <option value="submit_review" disabled={!snapshot.capability.review}>{t("提交整体审阅", "Submit review")}</option>
        <option value="reply" disabled={!snapshot.capability.reply}>{t("回复讨论", "Reply to discussion")}</option>
        <option value="resolve" disabled={!snapshot.capability.resolve}>{t("解决讨论", "Resolve discussion")}</option>
        <option value="unresolve" disabled={!snapshot.capability.resolve}>{t("重新打开讨论", "Reopen discussion")}</option>
        <option value="request_reviewer" disabled={!snapshot.capability.request_reviewer}>{t("请求审阅人", "Request reviewers")}</option>
      </select>
    </label>
    {!supported && <small>{t("当前账户没有此操作的能力。", "This account does not support this action.")}</small>}
    {threadAction && <label>{t("讨论", "Discussion")}
      <select aria-label={t("目标讨论", "Target discussion")} value={targetID}
        onChange={(event) => change(() => setTargetID(event.target.value))}>
        <option value="">{t("选择当前 PR 的讨论", "Choose a discussion from this PR")}</option>
        {snapshot.threads.map((thread, index) => <option key={thread.id} value={thread.id}
          disabled={(operation === "resolve" && thread.resolved) || (operation === "unresolve" && !thread.resolved)}>
          {index + 1}. {thread.path}{thread.line ? `:${thread.line}` : ""} · {thread.resolved ? t("已解决", "Resolved") : t("待处理", "Unresolved")}
        </option>)}
      </select>
    </label>}
    {operation === "submit_review" && <select aria-label={t("审阅类型", "Review event")}
      value={reviewEvent} onChange={(event) => change(() => setReviewEvent(event.target.value))}>
      <option value="COMMENT">{t("评论", "Comment")}</option><option value="APPROVE">{t("批准", "Approve")}</option>
      <option value="REQUEST_CHANGES">{t("请求修改", "Request changes")}</option>
    </select>}
    {(operation === "submit_review" || operation === "reply") && <textarea
      aria-label={operation === "reply" ? t("回复正文", "Reply body") : t("审阅正文", "Review body")}
      maxLength={65536} value={body}
      onChange={(event) => change(() => setBody(event.target.value))}
      placeholder={operation === "reply" ? t("写下回复", "Write a reply") : t("填写审阅意见", "Write your review")} />}
    {operation === "request_reviewer" && <label>{t("GitHub 用户名", "GitHub usernames")}
      <input aria-label={t("审阅人用户名", "Reviewer usernames")} maxLength={3232}
        value={reviewerInput} onChange={(event) => change(() => setReviewerInput(event.target.value))}
        placeholder={t("用逗号或空格分隔，最多 32 人", "Comma or space separated, up to 32 people")} />
      {reviewerInput && !reviewersValid && <small role="alert">{t("请输入最多 32 个有效的 GitHub 用户名。", "Enter up to 32 valid GitHub usernames.")}</small>}
    </label>}
    {!bodyValid && <small role="alert">{t("正文超过 64 KiB，请缩短后再预览。", "The body exceeds 64 KiB. Shorten it before previewing.")}</small>}
    <button disabled={disabled || !supported || !valid} type="button" onClick={() => {
      if (disabled || !supported || !valid) return;
      onPreview({ operation, reviewers: operation === "request_reviewer" ? reviewers : [],
        ...(threadAction ? { target_id: targetID } : {}),
        ...(operation === "reply" || operation === "submit_review" ? { body: body.trim() } : {}),
        ...(operation === "submit_review" ? { review_event: reviewEvent } : {}) });
    }}>{t("生成精确预览", "Create exact preview")}</button>
  </div>;
}
