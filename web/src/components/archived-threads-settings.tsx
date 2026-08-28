import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArchiveRestore, MessagesSquare, Search, Trash2, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ThreadView } from "../api/types";
import { usePagedResource } from "../hooks/use-paged-resource";
import { formatDate } from "../lib/format";
import { useLocale } from "../lib/locale";
import { useModalFocusTrap } from "../hooks/use-modal-focus-trap";
import { ErrorState, LoadMoreButton, LoadingState } from "./common";

type ThreadAction = "restore" | "delete";

export function ArchivedThreadsSettings({ client }: { client: CyberAgentClient }) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [deleteCandidate, setDeleteCandidate] = useState<ThreadView | null>(null);
  const query = usePagedResource<ThreadView>(client, ["threads", "archived"], "/threads",
    { limit: 50, status: "archived" }, true);
  const threads = useMemo(() => query.data?.pages.flatMap((page) => page.items) ?? [],
    [query.data]);
  const normalizedSearch = search.trim().toLocaleLowerCase();
  const visible = useMemo(() => threads.filter((thread) => !normalizedSearch ||
    `${thread.title} ${thread.id} ${thread.workspace_id ?? ""}`.toLocaleLowerCase()
      .includes(normalizedSearch)), [normalizedSearch, threads]);
  const transition = useMutation({
    mutationFn: ({ thread, action }: { thread: ThreadView; action: ThreadAction }) =>
      client.transitionThread(thread.id, action, {
        version: "thread_lifecycle.v1", expected_version: thread.version,
      }, `settings-thread-${action}-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      setDeleteCandidate(null);
      void queryClient.invalidateQueries({ queryKey: ["threads"] });
      void queryClient.invalidateQueries({ queryKey: ["thread", result.thread.id] });
    },
  });
  const deleteDialogRef = useModalFocusTrap<HTMLElement>(Boolean(deleteCandidate),
    () => setDeleteCandidate(null), transition.isPending);

  return <section className="settings-page-section archived-threads-settings">
    <div className="archived-threads-title">
      <div><h1>{t("已归档的聊天", "Archived chats")}</h1>
        <p>{t("归档会隐藏 Thread，但保留消息、运行记录与审计证据。",
          "Archiving hides a Thread while retaining messages, runs, and audit evidence.")}</p></div>
      <span>{threads.length} {t("个 Thread", "Threads")}</span>
    </div>
    <label className="archived-threads-search">
      <Search aria-hidden="true" size={16} />
      <input aria-label={t("搜索已归档的聊天", "Search archived chats")}
        onChange={(event) => setSearch(event.target.value)}
        placeholder={t("搜索已归档的聊天", "Search archived chats")} type="search"
        value={search} />
    </label>
    {query.isLoading && <LoadingState label={t("加载已归档的聊天", "Loading archived chats")} />}
    {query.isError && <ErrorState error={query.error} />}
    {!query.isLoading && !query.isError && visible.length === 0 &&
      <div className="archived-threads-empty"><MessagesSquare aria-hidden="true" size={22} />
        <strong>{normalizedSearch ? t("没有匹配的归档聊天", "No matching archived chats") :
          t("暂无已归档的聊天", "No archived chats")}</strong>
        <span>{normalizedSearch ? t("换一个关键词试试", "Try another search") :
          t("在侧栏归档的 Thread 会出现在这里", "Threads archived from the sidebar appear here")}</span>
      </div>}
    <div className="archived-thread-list">
      {visible.map((thread) => <article className="archived-thread-row" key={thread.id}>
        <MessagesSquare aria-hidden="true" size={17} />
        <div><strong>{thread.title}</strong>
          <span>{thread.archived_at ? formatDate(thread.archived_at) : formatDate(thread.updated_at)}
            {thread.workspace_id ? ` · ${thread.workspace_id}` : ""}</span></div>
        <div className="archived-thread-actions">
          <button className="settings-action" disabled={transition.isPending}
            onClick={() => transition.mutate({ thread, action: "restore" })} type="button">
            <ArchiveRestore aria-hidden="true" size={15} />{t("取消归档", "Restore")}
          </button>
          <button aria-label={`${t("删除", "Delete")} ${thread.title}`}
            className="settings-action danger" disabled={transition.isPending}
            onClick={() => { transition.reset(); setDeleteCandidate(thread); }} type="button">
            <Trash2 aria-hidden="true" size={15} />{t("删除", "Delete")}
          </button>
        </div>
      </article>)}
    </div>
    <LoadMoreButton hasNextPage={Boolean(query.hasNextPage)}
      isFetching={query.isFetchingNextPage}
      onClick={() => void query.fetchNextPage()} />
    {transition.isError && !deleteCandidate && <p className="connection-error" role="alert">
      {transition.error instanceof Error ? transition.error.message :
        t("更新归档状态失败", "Could not update archived chat")}</p>}
    {deleteCandidate && <div className="desktop-dialog-backdrop" role="presentation">
      <section aria-labelledby="delete-archived-thread-title" aria-modal="true"
        className="desktop-dialog archive-session-dialog" ref={deleteDialogRef}
        role="dialog" tabIndex={-1}>
        <header><div><span className="dialog-icon"><Trash2 aria-hidden="true" size={17} /></span>
          <div><h2 id="delete-archived-thread-title">{t("删除已归档的聊天", "Delete archived chat")}</h2>
            <small>{deleteCandidate.title}</small></div></div>
          <button aria-label={t("关闭", "Close")} className="icon-button"
            disabled={transition.isPending} onClick={() => setDeleteCandidate(null)} type="button">
            <X aria-hidden="true" size={16} /></button></header>
        <div className="desktop-dialog-body archive-session-copy">
          <p>{t("此 Thread 将从应用列表中删除。为了诊断与审计，本地消息和运行记录不会被擦除。",
            "This Thread will be removed from app lists. Local messages and run records remain for diagnostics and audit.")}</p>
          {transition.isError && <p className="connection-error" role="alert">
            {transition.error instanceof Error ? transition.error.message :
              t("删除失败", "Could not delete")}</p>}
        </div>
        <footer><span /><div className="desktop-dialog-actions">
          <button className="dialog-secondary" disabled={transition.isPending}
            onClick={() => setDeleteCandidate(null)} type="button">{t("取消", "Cancel")}</button>
          <button className="dialog-danger" disabled={transition.isPending}
            onClick={() => transition.mutate({ thread: deleteCandidate, action: "delete" })}
            type="button"><Trash2 aria-hidden="true" size={15} />{t("删除", "Delete")}</button>
        </div></footer>
      </section>
    </div>}
  </section>;
}
