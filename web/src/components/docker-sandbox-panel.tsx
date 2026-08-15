import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Box, LoaderCircle, Server } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import { ErrorState, LoadingState, StatusBadge } from "./common";
import { useLocale } from "../lib/locale";

export function DockerSandboxPanel({ client }: { client: CyberAgentClient }) {
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
      try { parsed = JSON.parse(manifest); } catch { throw new Error("manifest must be valid JSON"); }
      return client.admitDockerSandbox({ plan_id: planID, requested_by: "web_operator", manifest: parsed as never },
        `web-docker-admit-${globalThis.crypto.randomUUID()}`);
    },
    onSuccess: (result) => {
      setLastAdmission(result.admission_id ?? "");
      setAdmissionID(result.admission_id ?? "");
      setMessage(result.allowed ? "admission authorized" : "admission denied");
    },
  });
  const start = useMutation({
    mutationFn: () => client.startDockerSandbox({ admission_id: admissionID, requested_by: "web_operator" },
      `web-docker-start-${globalThis.crypto.randomUUID()}`),
    onSuccess: () => setMessage("start request accepted"),
  });
  const cancel = useMutation({
    mutationFn: () => client.cancelDockerSandbox({ admission_id: admissionID, requested_by: "web_operator" },
      `web-docker-cancel-${globalThis.crypto.randomUUID()}`),
    onSuccess: () => setMessage("cancellation accepted"),
  });
  return (
    <section className="detail-section docker-sandbox-section" aria-label={t("Docker 沙箱", "Docker sandbox")}>
      <div className="section-heading"><h2><Server aria-hidden="true" size={15} />{t("Docker 沙箱", "Docker sandbox")}</h2><span>{t("network-none 精确准入", "network-none exact admission")}</span></div>
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
      </div>}
    </section>
  );
}

