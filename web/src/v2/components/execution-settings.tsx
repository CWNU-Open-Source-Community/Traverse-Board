import { useQuery } from "@tanstack/react-query";
import type { CyberAgentClient } from "../../api/client";
import type { RunDetailView, ThreadDetailView, WorkspaceView } from "../../api/types";
import { ExecutionInteractionPanel, ExecutionProfilePanel } from "../../components/run-permission-settings";
import { v2QueryKeys } from "../query-keys";

export function V2ExecutionSettings({ client, threadID, workspaces }: {
  client: CyberAgentClient;
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
                : "普通任务的文件工具使用已接入的项目目录。本地执行配合逐次审批，可创建工程和运行检查；每条宿主命令仍需在对话中批准，并显示实际工作目录。"}</p>
              <p>先选择执行环境，再确认信任项目并启用 Code。运行中不可更改时，返回对话停止后再设置；选择工作区访问不会自动准备隔离工作区。</p>
              {detail.data.execution_permission.mode === "approval" && <p>逐次审批命令在宿主机执行，可以访问主机网络。批准前请核对命令、目录和用途。</p>}
            </div>
            <ExecutionProfilePanel client={client} detail={detail.data} readiness={readiness.data}
              key={`profile-${runID}`} />
            <ExecutionInteractionPanel client={client} detail={detail.data} readiness={readiness.data}
              key={`interaction-${runID}`} />
          </>}
  </section>;
}
