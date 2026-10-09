import { useQuery } from "@tanstack/react-query";
import type { APIClient } from "../../api/client";
import type { RunDetailView, ThreadDetailView, WorkspaceView } from "../../api/types";
import { ExecutionInteractionPanel, ExecutionProfilePanel } from "../../components/run-permission-settings";
import { v2QueryKeys } from "../query-keys";

export function V2ExecutionSettings({ client, threadID, workspaces }: {
  client: APIClient;
  threadID: string;
  workspaces: WorkspaceView[];
}) {
  const thread = useQuery({
    queryKey: v2QueryKeys.thread(threadID),
    queryFn: ({ signal }) => client.get<ThreadDetailView>(
      `/threads/${encodeURIComponent(threadID)}`, {}, signal),
    enabled: Boolean(threadID),
  });
  const currentRun = thread.data?.active_run ?? thread.data?.last_run;
  const runID = currentRun?.id ?? "";
  const detail = useQuery({
    queryKey: ["run", runID],
    queryFn: ({ signal }) => client.get<RunDetailView>(`/runs/${encodeURIComponent(runID)}`, {}, signal),
    enabled: Boolean(runID),
  });
  const readiness = useQuery({
    queryKey: ["run", runID, "capability-readiness"],
    queryFn: ({ signal }) => client.runCapabilityReadiness(runID, signal),
    enabled: Boolean(runID),
  });
  const retry = () => {
    void thread.refetch();
    if (runID) {
      void detail.refetch();
      void readiness.refetch();
    }
  };
  const project = workspaces.find(({ id }) => id === thread.data?.thread.workspace_id);
  return <section aria-label="当前任务执行环境" className="v2-settings-section v2-execution-settings">
    <h2>当前任务执行环境</h2>
    {!threadID ? <p className="v2-settings-empty">先打开一个对话，再选择执行环境。</p>
      : thread.isError || detail.isError || readiness.isError
        ? <p role="alert">无法读取当前任务的执行环境。<button onClick={retry} type="button">重试执行环境</button></p>
        : thread.isPending || detail.isPending || readiness.isPending || !detail.data || !readiness.data
          ? <p role="status">正在读取执行环境…</p>
          : <>
            <div className="v2-settings-card v2-execution-help">
              <strong>项目：{project?.name ?? "当前对话的项目"}</strong>
              <p>{detail.data.run.standard_code_preset_configured
                ? "当前任务已配置隔离工作区，文件与沙箱命令使用该工作区；这里切换环境不会把改动应用回原项目。"
                : "普通任务的文件工具使用已接入的项目目录。本地执行可用于创建工程和运行检查；命令按当前审批偏好和运行环境处理，并显示实际工作目录。"}</p>
              <p>先选择执行环境，再确认信任项目并启用 Code。运行中不可更改时，返回对话停止后再设置。请求批准、帮我批准、完全访问是审批偏好，切换它们不会自动准备隔离工作区或启用调试运行时。</p>
              <p>需要批准时，请核对具体操作、目录和用途。网络范围与浏览器能力由各自的设置和后端检查决定。</p>
            </div>
            <ExecutionProfilePanel client={client} detail={detail.data} readiness={readiness.data}
              key={`profile-${runID}`} />
            <ExecutionInteractionPanel client={client} detail={detail.data} readiness={readiness.data}
              key={`interaction-${runID}`} />
          </>}
  </section>;
}
