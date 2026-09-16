import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { RunView, SessionView } from "../../api/types";
import { LifecycleStatusLabel } from "../../components/lifecycle-status";
import { formatDate } from "../../lib/format";
import "./inspector-record-browser.css";

type RecordKind = "run" | "session";

export function InspectorRecordBrowser({ client, onOpen }: {
  client: CyberAgentClient;
  onOpen: (kind: RecordKind, id: string) => void;
}) {
  const [kind, setKind] = useState<RecordKind | null>(null);
  return <section className="v2-record-browser" aria-label="全部运行与会话">
    <h2>全部运行与会话</h2>
    <p>查看历史执行与上下文记录。打开记录不会启动执行。</p>
    <div className="v2-record-kinds" role="group" aria-label="记录类型">
      <button aria-pressed={kind === "run"} onClick={() => setKind("run")} type="button">运行记录</button>
      <button aria-pressed={kind === "session"} onClick={() => setKind("session")} type="button">会话记录</button>
    </div>
    {kind && <RecordList client={client} key={kind} kind={kind} onOpen={onOpen} />}
  </section>;
}

function RecordList({ client, kind, onOpen }: {
  client: CyberAgentClient;
  kind: RecordKind;
  onOpen: (kind: RecordKind, id: string) => void;
}) {
  const [search, setSearch] = useState("");
  const query = useInfiniteQuery({
    queryKey: ["v2", "inspector", "records", kind],
    queryFn: ({ signal, pageParam }) => client.getPage<RunView | SessionView>(
      kind === "run" ? "/runs" : "/sessions", { limit: 50 }, pageParam, signal),
    initialPageParam: "", getNextPageParam: (page) => page.page.next_cursor || undefined,
    retry: false,
  });
  const loaded = useMemo(() => [...new Map((query.data?.pages ?? []).flatMap((page) => page.items)
    .map((record) => [record.id, record])).values()], [query.data]);
  const normalized = search.trim().toLocaleLowerCase();
  const visible = loaded.filter((record) => !normalized ||
    `${record.id} ${"title" in record ? record.title : ""}`.toLocaleLowerCase().includes(normalized));
  const label = kind === "run" ? "运行记录" : "会话记录";
  return <>
    <label className="v2-record-search"><Search aria-hidden="true" size={16} />
      <input aria-label={`搜索${label}`} onChange={(event) => setSearch(event.target.value)} type="search"
        placeholder={kind === "run" ? "搜索已加载的运行 ID" : "搜索已加载的会话标题或 ID"} value={search} />
    </label>
    <p className="v2-record-search-scope">搜索 {loaded.length} 条已加载{kind === "run" ? "运行 ID" : "会话标题和 ID"}，不含消息正文。
      {query.isPending ? "正在读取记录。" : query.isError ? "加载未完成，请重试。"
        : query.hasNextPage ? "还有更早记录可加载。" : "当前列表已加载完毕。"}</p>
    {query.isPending && <p role="status">正在读取{label}…</p>}
    {query.isError && <p role="alert">{label}加载失败，已加载记录仍可打开。
      <button onClick={() => void (query.isFetchNextPageError ? query.fetchNextPage() : query.refetch())}
        type="button">重试{label}</button></p>}
    {!query.isPending && !query.isError && visible.length === 0 && <p role="status">
      {normalized ? "已加载记录中没有匹配项" : `暂无${label}`}</p>}
    <ul className="v2-record-list">{visible.map((record) => <li key={record.id}>
      <button aria-label={`打开${label} ${record.id}`} onClick={() => onOpen(kind, record.id)} type="button">
        <span><strong>{"title" in record && record.title ? record.title : record.id}</strong>
          {"title" in record && record.title && <code>{record.id}</code>}</span>
        <small><span aria-label={kind === "run" ? "执行记录状态" : "上下文记录状态"} className={`v2-record-status${record.status === "failed" ? " is-failed" : record.status === "waiting_approval" ? " is-pending" : ""}`}>
          <LifecycleStatusLabel status={record.status} kind={kind} /></span> · {formatDate(record.updated_at)}</small>
      </button>
    </li>)}</ul>
    {query.hasNextPage && !query.isFetchNextPageError && <button className="v2-record-load-more" disabled={query.isFetchingNextPage}
      onClick={() => void query.fetchNextPage()} type="button">
      {query.isFetchingNextPage ? "正在加载更早记录…" : `加载更早${label}`}</button>}
  </>;
}
