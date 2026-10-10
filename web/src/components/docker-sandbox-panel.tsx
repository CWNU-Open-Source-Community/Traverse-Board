import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Box, LoaderCircle, Server } from "lucide-react";
import type { APIClient } from "../api/client";
import { ErrorState, LoadingState, StatusBadge } from "./common";
import { useLocale } from "../lib/locale";

export function DockerSandboxPanel({ client }: { client: APIClient }) {
  const { t } = useLocale();
  const [planID, setPlanID] = useState("");
  const [manifest, setManifest] = useState("");
  const [admissionID, setAdmissionID] = useState("");
  const [lastAdmission, setLastAdmission] = useState("");
  const [message, setMessage] = useState("");
  const statusQuery = useQuery({
    queryKey: ["sandbox", "docker", "status", admissionID],
    queryFn: ({ signal }) => client.getDockerSandboxStatus(admissionID, signal),
    enabled: Boolean(admissionID),
  });
  const admit = useMutation({
    mutationFn: () => {
      let parsed: unknown;
      try { parsed = JSON.parse(manifest); } catch { throw new Error(t("清单格式有误，请填写有效的 JSON。", "The manifest format is invalid. Enter valid JSON.")); }
      return client.admitDockerSandbox({ plan_id: planID, requested_by: "web_operator", manifest: parsed as never },
        `web-docker-admit-${globalThis.crypto.randomUUID()}`);
    },
    onSuccess: (result) => {
      setLastAdmission(result.admission_id ?? "");
      setAdmissionID(result.admission_id ?? "");
      setMessage(result.allowed ? t("准入已通过，核对状态后可启动沙箱。", "Admission approved. Check the state, then start the sandbox.") :
        t("准入被拒绝，请核对清单和准入决策。", "Admission denied. Review the manifest and admission decision."));
    },
  });
  const start = useMutation({
    mutationFn: () => client.startDockerSandbox({ admission_id: admissionID, requested_by: "web_operator" },
      `web-docker-start-${globalThis.crypto.randomUUID()}`),
    onSuccess: () => {
      setMessage(t("启动请求已接受，正在核对沙箱状态。", "Start request accepted. Checking sandbox state."));
      void statusQuery.refetch();
    },
  });
  const cancel = useMutation({
    mutationFn: () => client.cancelDockerSandbox({ admission_id: admissionID, requested_by: "web_operator" },
      `web-docker-cancel-${globalThis.crypto.randomUUID()}`),
    onSuccess: () => {
      setMessage(t("取消请求已接受，正在核对清理状态。", "Cancellation accepted. Checking cleanup state."));
      void statusQuery.refetch();
    },
  });
  return (
    <section className="detail-section docker-sandbox-section" aria-label={t("Docker 沙箱", "Docker sandbox")}>
      <div className="section-heading"><h2><Server aria-hidden="true" size={15} />{t("Docker 沙箱", "Docker sandbox")}</h2><span>{t("网络隔离 · 按清单准入", "Network isolated · manifest admission")}</span></div>
      <p>{t("填写计划标识和执行清单，评估通过后再启动。已有准入记录可在下方输入标识查看状态。", "Enter the plan identifier and execution manifest, then start after admission is approved. Enter an existing admission identifier below to check its state.")}</p>
      {!client.hasControl && <p>{t("当前可查询沙箱状态。连接控制凭证后可评估、启动或取消。", "Sandbox state can be inspected. Connect a control credential to evaluate, start, or cancel.")}</p>}
      <div className="run-execution-control">
        <label htmlFor="docker-plan-id">{t("计划 ID", "Plan ID")}</label>
        <input id="docker-plan-id" maxLength={256} onChange={(event) => setPlanID(event.target.value)} value={planID} />
      </div>
      <div className="run-execution-control">
        <label htmlFor="docker-manifest">{t("Manifest JSON", "Manifest JSON")}</label>
        <textarea id="docker-manifest" onChange={(event) => setManifest(event.target.value)} rows={8} value={manifest} />
      </div>
      {client.hasControl && (
        <button className="command-button" disabled={admit.isPending || !planID || !manifest}
          onClick={() => admit.mutate()} type="button">
          {admit.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={15} /> : <Box aria-hidden="true" size={15} />}
          {t("评估并准入", "Evaluate and admit")}
        </button>
      )}
      <div className="run-execution-control">
        <label htmlFor="docker-admission-id">{t("准入 ID", "Admission ID")}</label>
        <input id="docker-admission-id" maxLength={256} onChange={(event) => setAdmissionID(event.target.value)} value={admissionID} />
      </div>
      {statusQuery.data && (
        <dl className="detail-grid compact">
          <div><dt>{t("状态", "Status")}</dt><dd><StatusBadge status={String(statusQuery.data.state)} /></dd></div>
          <div><dt>{t("决策", "Decision")}</dt><dd>{String(statusQuery.data.decision)}</dd></div>
        </dl>
      )}
      {statusQuery.isLoading && <LoadingState label={t("正在查询沙箱状态", "Checking sandbox state")} />}
      {statusQuery.isError && <ErrorState error={statusQuery.error} />}
      {admissionID && <button className="compact-command" disabled={statusQuery.isFetching}
        onClick={() => void statusQuery.refetch()} type="button">{t("刷新沙箱状态", "Refresh sandbox state")}</button>}
      {client.hasControl && admissionID && (
        <div className="run-execution-control">
          <button className="command-button" disabled={start.isPending} onClick={() => start.mutate()} type="button">{t("启动", "Start")}</button>
          <button className="command-button danger" disabled={cancel.isPending} onClick={() => cancel.mutate()} type="button">{t("取消", "Cancel")}</button>
        </div>
      )}
      {message && <div className="projection-placeholder" role="status">{message}</div>}
      {lastAdmission && !admissionID && <div className="projection-placeholder">{t("最近准入", "Latest admission")}: {lastAdmission}</div>}
      {(admit.isError || start.isError || cancel.isError) && <div className="inline-warning" role="alert">
        {String((admit.isError ? admit.error : start.isError ? start.error : cancel.error) ?? t("Docker 沙箱操作失败", "Docker sandbox operation failed"))}
        <p>{t("操作结果待确认时，请先查询原准入记录的状态，再决定后续操作。", "If the outcome needs confirmation, inspect the original admission state before proceeding.")}</p>
      </div>}
    </section>
  );
}

