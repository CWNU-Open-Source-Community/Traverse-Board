import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { LoaderCircle, Square } from "lucide-react";
import { APIRequestError, type CyberAgentClient } from "../../api/client";
import type { ThreadExecutionView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";

export function useV2ThreadExecution(client: CyberAgentClient, threadID: string) {
  return useQuery({
    queryKey: v2QueryKeys.execution(threadID),
    queryFn: ({ signal }) => client.threadExecution(threadID, signal),
    enabled: Boolean(threadID) && client.hasThreadExecutionRead === true,
    refetchInterval: (query) => query.state.error instanceof APIRequestError &&
      query.state.error.status === 404 ? false : query.state.data?.state === "idle" ? 2_500 : 600,
    retry: false,
  });
}

export function V2ThreadExecutionControl({ client, threadID, execution }: {
  client: CyberAgentClient;
  threadID: string;
  execution: ThreadExecutionView | undefined;
}) {
  const queryClient = useQueryClient();
  const stop = useMutation({
    mutationKey: ["v2", "thread-stop", threadID],
    mutationFn: (input: { threadID: string; executionID: string }) => client.interruptThread(
      input.threadID, input.executionID, `v2-thread-stop-${input.executionID}`),
    // Navigation can change the observer's props while a stop is pending.
    // Its response and refresh still belong to the submitted task.
    onSuccess: (state, input) => queryClient.setQueryData(v2QueryKeys.execution(input.threadID), state),
    onSettled: (_data, _error, input) => queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(input.threadID) }),
  });
  const stopping = stop.isPending || execution?.state === "stopping";
  return <>
    {execution && execution.state !== "idle" && client.hasThreadControl && client.hasRunExecution &&
      <button aria-label={stopping ? "正在停止" : execution.state === "stop_failed" ? "重试停止" : "停止当前执行"} className="v2-stop-button"
        disabled={stopping} onClick={() => execution.execution_id && stop.mutate({ threadID,
          executionID: execution.execution_id })}
        title="停止当前执行；已完成的修改和已受理的补充要求会保留" type="button">
        {stopping ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
          : <Square aria-hidden="true" size={13} />} {stopping ? "正在停止…" : execution.state === "stop_failed" ? "重试停止" : "停止"}
      </button>}
    {stop.isError && <span role="alert">停止请求未确认，请刷新执行状态后重试。</span>}
  </>;
}

export function V2PausedThreadControl({ client, threadID, runID }: {
  client: CyberAgentClient; threadID: string; runID: string;
}) {
  const queryClient = useQueryClient();
  const operationKey = useRef<string | null>(null);
  const resume = useMutation({
    mutationFn: () => client.controlRunLifecycle(runID,
      { version: "run_lifecycle_control.v1", action: "resume" }, operationKey.current ??=
        `v2-resume-${globalThis.crypto.randomUUID()}`),
    onSettled: () => queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(threadID) }),
  });
  return <div className="v2-paused-thread" role="status">本轮已暂停，可发送新消息继续。
    {client.hasRunLifecycle && <button disabled={resume.isPending} onClick={() => resume.mutate()}
      title="仅解除暂停，不会重试失败工具；发送新消息才会继续处理要求" type="button">
      {resume.isPending ? "正在解除…" : "解除暂停"}</button>}
    {resume.isError && <p role="alert">解除暂停未确认，可重试：{resume.error.message}</p>}
  </div>;
}
