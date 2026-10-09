import { useQuery } from "@tanstack/react-query";
import type { APIClient } from "../../api/client";
import type { RunDetailView, TaskBudgetSettings, ThreadDetailView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { TaskConfiguration } from "./task-configuration";

export function TaskConfigurationSettings({ client, threadID, sourceRunID, draftWorkspaceID = "", draftBudget,
  onDraftBudgetChange, onDraftValidityChange }: {
  client: APIClient;
  threadID: string;
  sourceRunID?: string;
  draftWorkspaceID?: string;
  draftBudget?: TaskBudgetSettings;
  onDraftBudgetChange?: (budget: TaskBudgetSettings | undefined) => void;
  onDraftValidityChange?: (valid: boolean) => void;
}) {
  const thread = useQuery({ queryKey: v2QueryKeys.thread(threadID), enabled: Boolean(threadID),
    queryFn: async ({ signal }) => {
      const detail = await client.get<ThreadDetailView>(`/threads/${encodeURIComponent(threadID)}`, {}, signal);
      if (detail?.thread.id !== threadID) throw new Error("任务配置来源不匹配。");
      return detail;
    } });
  const standaloneRun = useQuery({ queryKey: ["run", sourceRunID], enabled: !threadID && Boolean(sourceRunID),
    queryFn: async ({ signal }) => {
      const detail = await client.get<RunDetailView>(`/runs/${encodeURIComponent(sourceRunID!)}`, {}, signal);
      if (detail?.run.id !== sourceRunID || detail.run.mission_id !== detail.mission.id) throw new Error("执行配置来源不匹配。");
      return detail;
    } });
  const detail = !thread.isError && thread.data?.thread.id === threadID ? thread.data : undefined;
  const selectedRun = sourceRunID ? detail?.runs.find((item) => item.run.id === sourceRunID)?.run
    ?? (detail?.active_run?.id === sourceRunID ? detail.active_run : detail?.last_run?.id === sourceRunID ? detail.last_run : undefined)
    : detail?.active_run ?? detail?.last_run;
  const directRun = !standaloneRun.isError && standaloneRun.data?.run.id === sourceRunID ? standaloneRun.data : undefined;
  const run = threadID ? selectedRun : directRun?.run;
  const pinned = Boolean(threadID || sourceRunID);
  const source = threadID ? thread : standaloneRun;
  const workspaceID = threadID ? detail?.thread.workspace_id ?? "" : directRun?.mission.workspace_id ?? "";
  return <><h1>任务预算与项目配置</h1><p className="v2-settings-lead">{pinned
    ? "查看所选执行创建时保存的预算和项目配置。每次执行使用固定快照。"
    : "为当前项目的新任务设置预算。设置随草稿保留，项目配置由服务端在创建时再次核对。"}</p>
    {pinned ? run && workspaceID ? <TaskConfiguration key={`run:${run.id}`} client={client} workspaceID={workspaceID} run={run} />
      : <div className="v2-notice" role={source.isPending ? "status" : "alert"}>
        {source.isPending ? "正在核对任务与执行配置来源…" : source.isError ? "无法读取所选执行的配置来源，请重试。"
          : "所选执行或其工作区尚未找到，无法读取固定配置。请返回任务并明确选择执行记录。"}
        {!source.isPending && <button onClick={() => void source.refetch()} type="button">重试执行配置来源</button>}
      </div>
      : <TaskConfiguration key={`draft:${draftWorkspaceID}`} client={client} workspaceID={draftWorkspaceID} profile="code"
        budget={draftBudget} onBudgetChange={onDraftBudgetChange} onValidityChange={onDraftValidityChange}
        disabled={!client.hasThreadControl || !draftWorkspaceID} />}
  </>;
}
