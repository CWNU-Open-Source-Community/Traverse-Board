import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Box, LoaderCircle, Server } from "lucide-react";
import type { APIClient } from "../api/client";
import type { RunDetailView } from "../api/types";
import { ErrorState, LoadingState, StatusBadge } from "./common";
import { useLocale } from "../lib/locale";
import { StandardCodeReadinessPanel } from "./run-permission-settings";

export function DockerSandboxPanel({ client, runID = "", threadID }: { client: APIClient; runID?: string; threadID?: string }) {
  const { t } = useLocale();
  const environment = useQuery({ queryKey: ["sandbox", "docker", "environment"],
    queryFn: ({ signal }) => client.getDockerEnvironment(signal), retry: false,
    enabled: typeof client.getDockerEnvironment === "function" });
  const detail = useQuery({ queryKey: ["run", runID],
    queryFn: ({ signal }) => client.get<RunDetailView>(`/runs/${encodeURIComponent(runID)}`, {}, signal), enabled: Boolean(runID) });
  const readiness = useQuery({ queryKey: ["run", runID, "capability-readiness"],
    queryFn: ({ signal }) => client.runCapabilityReadiness(runID, signal), enabled: Boolean(runID) });
  const value = environment.data;
  const ready = value?.readiness?.ready === true;
  const configuredDelivery = detail.data?.run.standard_code_preset_configured === true && detail.data.mode.phase === "deliver";
  const remediation: Record<string, [string, string]> = {
    platform_unsupported: ["在 Docker Desktop 选择 Linux 容器，然后刷新环境。", "Select Linux containers in Docker Desktop, then refresh the environment."],
    api_unsupported: ["升级 Docker Engine 到支持的 API 版本，然后刷新环境。", "Upgrade Docker Engine to a supported API version, then refresh the environment."],
    pids_limit_unavailable: ["为 Linux Engine 启用进程数量限制，然后刷新环境。", "Enable process limits for the Linux Engine, then refresh the environment."],
    resource_capacity_insufficient: ["为 Docker Engine 分配足够的 CPU 与内存，然后刷新环境。", "Allocate enough CPU and memory to Docker Engine, then refresh the environment."],
    image_unavailable: ["准备固定摘要对应的安全 Linux 镜像，清空继承的环境变量与卷，然后刷新环境。", "Prepare the safe Linux image matching the pinned digest, clear inherited environment variables and volumes, then refresh the environment."],
  };
  const remediationLabel = remediation[value?.readiness?.reason_code ?? ""];
  const next = !value ? t("读取环境状态后，按结果准备 Docker。", "Read the environment state, then prepare Docker using the result.")
    : !value.feature_enabled ? t("在桌面启动配置中启用 Docker 执行，然后重启。", "Enable Docker execution in Desktop startup settings, then restart.")
      : !value.image_configured ? t("准备包含所需工具链的镜像，将固定摘要加入桌面启动配置，然后重启。", "Prepare an image with the required toolchains, pin its digest in Desktop startup settings, then restart.")
        : !value.readiness?.daemon_reachable ? t("启动 Docker Engine，选择 Linux 容器，然后刷新环境。", "Start Docker Engine, select Linux containers, then refresh the environment.")
          : !ready ? remediationLabel ? t(...remediationLabel) : t("核对固定镜像与 Engine 配置，然后刷新环境。", "Check the pinned image and Engine settings, then refresh the environment.")
            : value.restart_required ? t("镜像已就绪。重启 Desktop 装配 Docker 运行时，然后回到此任务。", "The image is ready. Restart Desktop to install the Docker runtime, then return to this task.")
              : configuredDelivery ? t("当前任务已有编码配置，回任务继续执行现有计划。要换用 Docker，可从新任务选择此环境。", "This task already has a coding configuration. Return to continue its current plan. Choose this environment from a new task to switch to Docker.")
                : t("环境已就绪。确认项目信任并选择 Docker 编码环境，再回任务发送要执行的工作。", "The environment is ready. Confirm project trust and choose Docker coding, then return to the task and send the work to run.");
  const refresh = () => { void environment.refetch(); if (runID) void readiness.refetch(); };
  return <section className="detail-section docker-sandbox-section" aria-label={t("Docker 编码环境", "Docker coding environment")}>
    <div className="section-heading"><h2><Server aria-hidden="true" size={15} />{t("Docker 编码环境", "Docker coding environment")}</h2>
      <button className="compact-command" disabled={environment.isFetching} onClick={refresh} type="button">{t("刷新环境", "Refresh environment")}</button></div>
    <p role="status">{next}</p>
    {environment.isFetching && <LoadingState label={t("正在检查 Docker 环境", "Checking Docker environment")} />}
    {environment.isError && <ErrorState error={environment.error} />}
    {value && <dl className="detail-grid compact">
      <div><dt>{t("启动配置", "Startup configuration")}</dt><dd>{value.feature_enabled ? t("已启用", "Enabled") : t("待启用", "Needs enabling")}</dd></div>
      <div><dt>Docker Engine</dt><dd>{value.readiness?.daemon_reachable ? t("可连接", "Reachable") : t("待连接", "Needs connection")}</dd></div>
      <div><dt>{t("固定镜像", "Pinned image")}</dt><dd>{value.readiness?.image_profile_safe ? t("已检查", "Checked") : value.image_configured ? t("待检查", "Needs checking") : t("待配置", "Needs configuration")}</dd></div>
      <div><dt>{t("执行范围", "Execution scope")}</dt><dd>{t("隔离工作区 · 离线 · 无凭据", "Isolated workspace · offline · no credentials")}</dd></div>
    </dl>}
    <details><summary>{t("准备固定镜像与启动配置", "Prepare a pinned image and startup settings")}</summary>
      <p>{t("使用已审阅的 Linux 镜像，包含要运行的 Go、Node、Python 或 Rust 工具链和 standard-code-docker-runner.v2。项目依赖需预先放入镜像或项目，离线容器使用它们完成工作。镜像配置需清空继承的环境变量与卷。", "Use a reviewed Linux image containing the Go, Node, Python, or Rust toolchains you need and standard-code-docker-runner.v2. Prepare project dependencies in the image or project for offline execution. Clear inherited image environment variables and volumes.")}</p>
      <p>{t("核对镜像后，将实际 sha256 摘要设为 CYBERAGENT_STANDARD_CODE_DOCKER_IMAGE_DIGEST，并在桌面启动参数中启用下列选项。", "After reviewing the image, set its actual sha256 digest as CYBERAGENT_STANDARD_CODE_DOCKER_IMAGE_DIGEST and enable these Desktop startup options.")}</p>
      <code>--enable-profile-control --enable-permission-control --enable-workspace-sandbox --enable-docker-execution --enable-run-execution</code>
      <p>{t("镜像构建步骤见项目文档 docs/standard-code-docker.md。刷新环境会检查现有镜像。", "Image build steps are in docs/standard-code-docker.md. Refresh environment checks the existing image.")}</p>
      {value?.image_digest && <p>{t("当前固定摘要", "Current pinned digest")}: <code>{value.image_digest}</code></p>}
    </details>
    {runID && (detail.isLoading || readiness.isLoading) && <LoadingState label={t("正在检查当前项目", "Checking current project")} />}
    {(detail.isError || readiness.isError) && <ErrorState error={detail.error || readiness.error} />}
    {detail.data && readiness.data && <StandardCodeReadinessPanel client={client} detail={detail.data} readiness={readiness.data}
      threadID={threadID} preferredBackend="docker" configureDisabledReason={!ready || value?.restart_required ? next : undefined} />}
    {!runID && <p>{t("打开项目中的任务，再从任务设置选择 Docker 编码环境。", "Open a task in your project, then choose Docker coding in task settings.")}</p>}
    {value && <details><summary>{t("环境详细记录", "Environment details")}</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>}
    <details><summary>{t("高级：精确沙箱清单与历史准入", "Advanced: exact sandbox manifests and historical admissions")}</summary><DockerAdvancedControls client={client} /></details>
  </section>;
}

function DockerAdvancedControls({ client }: { client: APIClient }) {
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

