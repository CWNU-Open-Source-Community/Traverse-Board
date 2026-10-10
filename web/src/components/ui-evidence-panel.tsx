import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Ban,
  Camera,
  Download,
  FileJson,
  LoaderCircle,
  Play,
  RefreshCw,
  ShieldAlert,
} from "lucide-react";
import { APIRequestError, type APIClient } from "../api/client";
import type {
  UIEvidenceArtifactMetadata,
  UIEvidenceAttempt,
  UIEvidenceStartView,
} from "../api/types";
import { formatBytes, formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

const emptyFixtureSHA256 =
  "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a";

function templateRequest(): UIEvidenceStartView {
  return {
    operation_key: `desktop-ui-evidence-${globalThis.crypto.randomUUID()}`,
    start: {
      version: "command-runtime.v2",
      profile: "powershell",
      script: "npm run dev -- --host 127.0.0.1 --port 4173",
      working_directory: "web",
      environment: [],
      stdin_policy: "closed",
      close_initial_stdin: true,
      timeout_milliseconds: 1_800_000,
      output: { inline_bytes: 16_384, artifact_bytes: 262_144 },
      network: "disabled",
      credentials: "none",
      purpose: "Launch the reviewed Workspace web application for source-bound UI evidence",
    },
    readiness: {
      url: "http://127.0.0.1:4173/",
      method: "GET",
      expected_status: [200],
      timeout_milliseconds: 60_000,
      interval_milliseconds: 250,
    },
    url: "http://127.0.0.1:4173/",
    route: "/",
    browser: { product: "edge", channel: "stable" },
    environment: {
      viewport: { width: 1440, height: 900, dpr: 1 },
      locale: "en-US",
      theme: "light",
      reduced_motion: false,
    },
    fixture: {
      name: "empty-local-state",
      seed: "ui-evidence-v1",
      page_state: "{}",
      data_sha256: emptyFixtureSHA256,
      deterministic: true,
      synthetic: true,
    },
    steps: [{
      step: { id: "navigate", kind: "navigate", capture_after: true },
    }, {
      step: { id: "app-root", kind: "assert_present", selector: "#root", capture_after: true },
    }],
    capture: {
      screenshot: true,
      dom: true,
      accessibility: true,
      console: true,
      network: true,
      performance: true,
      video: false,
      mask_selectors: [],
    },
    failure_policy: {
      fail_on_console_error: true,
      fail_on_page_error: true,
      fail_on_request_error: true,
      fail_on_http_status: true,
    },
  };
}

export function UIEvidencePanel({ client, runID }: {
  client: APIClient;
  runID: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [selectedID, setSelectedID] = useState("");
  const [requestJSON, setRequestJSON] = useState("");
  const [reviewed, setReviewed] = useState(false);
  const [localError, setLocalError] = useState("");
  const attempts = useQuery({
    queryKey: ["run", runID, "ui-evidence"],
    queryFn: ({ signal }) => client.uiEvidence(runID, signal),
    enabled: Boolean(runID),
    refetchInterval: (query) => query.state.data?.some((attempt) => attempt.status === "running")
      ? 1_500 : false,
  });
  const activeID = selectedID || attempts.data?.[0]?.manifest.attempt_id || "";
  const bundle = useQuery({
    queryKey: ["ui-evidence", activeID],
    queryFn: ({ signal }) => client.uiEvidenceBundle(activeID, signal),
    enabled: Boolean(activeID),
    refetchInterval: (query) => query.state.data?.attempt.status === "running" ? 1_500 : false,
  });

  useEffect(() => {
    if (selectedID && attempts.data && !attempts.data.some(
      (attempt) => attempt.manifest.attempt_id === selectedID)) {
      setSelectedID("");
    }
  }, [attempts.data, selectedID]);

  const refresh = async (attemptID?: string) => {
    await queryClient.invalidateQueries({ queryKey: ["run", runID, "ui-evidence"] });
    if (attemptID) {
      await queryClient.invalidateQueries({ queryKey: ["ui-evidence", attemptID] });
    }
  };
  const start = useMutation({
    mutationFn: async () => {
      const parsed: unknown = JSON.parse(requestJSON);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error(t("启动清单必须是 JSON 对象", "The launch manifest must be a JSON object"));
      }
      return client.startUIEvidence(runID, parsed as UIEvidenceStartView);
    },
    onSuccess: async (attempt) => {
      setSelectedID(attempt.manifest.attempt_id);
      setReviewed(false);
      setLocalError("");
      await refresh(attempt.manifest.attempt_id);
    },
  });
  const cancel = useMutation({
    mutationFn: (attemptID: string) => client.cancelUIEvidence(attemptID),
    onSuccess: async (attempt) => refresh(attempt.manifest.attempt_id),
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setLocalError("");
    if (!client.hasUIEvidence || start.isPending || !reviewed || requestJSON.trim() === "") return;
    try {
      JSON.parse(requestJSON);
    } catch (caught) {
      setLocalError(humanError(caught));
      return;
    }
    start.mutate();
  };

  const download = async (artifact: UIEvidenceArtifactMetadata) => {
    setLocalError("");
    try {
      const content = await client.downloadUIEvidenceArtifact(artifact.attempt_id, artifact);
      const objectURL = URL.createObjectURL(content);
      const anchor = document.createElement("a");
      anchor.href = objectURL;
      anchor.download = artifactFilename(artifact);
      anchor.rel = "noopener";
      anchor.click();
      URL.revokeObjectURL(objectURL);
    } catch (caught) {
      setLocalError(humanError(caught));
    }
  };

  const current = bundle.data?.attempt;
  const mutationError = start.error || cancel.error;
  const historyUnavailable = !client.hasUIEvidence && attempts.isError &&
    attempts.error instanceof APIRequestError && attempts.error.status === 404 &&
    attempts.error.code === "NOT_FOUND";
  return <div className="ui-evidence-panel">
    <header className="operator-list-header">
      <div><Camera aria-hidden="true" size={16} />
        <h2>{t("真实浏览器 UI 证据", "Real-browser UI evidence")}</h2></div>
      <button aria-label={t("刷新 UI 证据", "Refresh UI evidence")} className="icon-button"
        disabled={attempts.isFetching} onClick={() => void refresh(activeID)} type="button">
        <RefreshCw aria-hidden="true" className={attempts.isFetching ? "spin" : ""} size={15} />
      </button>
    </header>
    <div className="ui-evidence-boundary" role="note">
      <ShieldAlert aria-hidden="true" size={16} />
      <span><strong>{t("查看浏览器检查结果", "Inspect browser check results")}</strong>
        {t("截图和下载内容供你核对页面表现。以验证状态判断结果：「通过」表示检查完成通过，「未运行」表示等待验证。进程与凭据操作须单独授权。",
          "Use screenshots and downloads to inspect the page. Follow the verification status: Passed means the checks passed; Not run awaits verification. Process and credential actions require separate authorization.")}</span>
    </div>
    {!client.hasUIEvidence && <div className="inline-warning" role="note">
      <p>{client.uiEvidenceUnavailableReason === "missing_control_credential"
        ? t("先刷新列表以查看历史证据。连接控制凭证后可启动或取消浏览器验证。",
          "Refresh the list to inspect historical evidence. Connect a control credential to start or cancel browser verification.")
        : client.uiEvidenceUnavailableReason === "ui_evidence_disabled"
          ? t("启用 --enable-ui-evidence 并重启 Desktop 后，可运行浏览器验证。",
            "Enable --enable-ui-evidence and restart Desktop to run browser verification.")
          : client.uiEvidenceUnavailableReason === "run_execution_disabled"
            ? t("启用 --enable-run-execution 并重启 Desktop，准备浏览器验证所需的执行能力。",
              "Enable --enable-run-execution and restart Desktop to prepare execution for browser verification.")
            : client.uiEvidenceUnavailableReason === "browser_cdp_control_disabled"
              ? t("启用 --enable-browser-cdp-control 并重启 Desktop，准备浏览器控制能力。",
                "Enable --enable-browser-cdp-control and restart Desktop to prepare browser control.")
              : t("先刷新列表以查看历史记录。按下方配置完成 Windows Desktop 的浏览器验证控制。",
                "Refresh the list to inspect historical records. Use the settings below to configure browser verification control in Windows Desktop.")}</p>
      <details><summary>{t("配置浏览器验证", "Configure browser verification")}</summary>
      <p>{t("在 Windows Desktop 连接控制凭证，并在桌面启动参数中明确启用以下能力：",
        "Connect a control credential in Windows Desktop and explicitly enable these capabilities in its startup options:")}
        {" "}<code>--enable-permission-control --enable-danger-full-access --enable-run-execution --enable-browser-cdp-control --enable-ui-evidence</code>
        {t("。独立 CLI 提供历史证据读取与导出；浏览器启动和取消请使用 Windows Desktop。",
          ". Use the standalone CLI to read and export historical evidence, and Windows Desktop to start or cancel a browser.")}
      </p></details>
    </div>}

    {attempts.isLoading && <LoadingState label={t("正在加载 UI 证据", "Loading UI evidence")} />}
    {attempts.isError && (historyUnavailable
      ? <p className="inline-warning" role="status">{t(
        "历史证据状态待确认。请核对所选执行，或连接支持 UI 取证的 Windows Desktop 后读取。",
        "Historical evidence needs confirmation. Check the selected execution, or read it through a Windows Desktop connection that supports UI evidence.",
      )}</p>
      : <ErrorState error={attempts.error} />)}
    {attempts.isSuccess && attempts.data.length === 0 && <EmptyState>{t("还没有浏览器验证。展开下方启动表单，载入模板并核对步骤后开始。", "No browser verification yet. Expand the launch form below, load a template, and review its steps to begin.")}</EmptyState>}

    {attempts.data && attempts.data.length > 0 && <div className="ui-evidence-layout">
      <section className="ui-evidence-attempts" aria-label={t("UI 证据 Attempts", "UI evidence attempts")}>
        {attempts.data.map((attempt) => <AttemptButton attempt={attempt}
          key={attempt.manifest.attempt_id}
          onSelect={() => setSelectedID(attempt.manifest.attempt_id)}
          selected={attempt.manifest.attempt_id === activeID} />)}
      </section>
      <section className="ui-evidence-detail" aria-live="polite">
        {bundle.isLoading && <LoadingState label={t("正在加载 Attempt", "Loading attempt")} />}
        {bundle.isError && <ErrorState error={bundle.error} />}
        {bundle.data && <AttemptDetail attempt={bundle.data.attempt}
          artifacts={bundle.data.artifacts} onDownload={download} steps={bundle.data.steps} />}
      </section>
    </div>}

    {current?.status === "running" && client.hasUIEvidence && <button className="command-button danger"
      disabled={cancel.isPending} onClick={() => cancel.mutate(current.manifest.attempt_id)} type="button">
      {cancel.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={14} /> :
        <Ban aria-hidden="true" size={14} />}
      {t("取消并清理 Attempt", "Cancel and clean up attempt")}
    </button>}

    <details className="ui-evidence-launch">
      <summary>{t("准备并启动浏览器验证", "Prepare and start browser verification")}</summary>
      <p>{t(
        "模板适用于本仓库的 Vite UI。启动前核对项目内命令、本机端口、测试数据、交互步骤、遮罩和失败处理。原始输入用于本次请求，证据清单保存输入的摘要。",
        "The template targets this repository's Vite UI. Before starting, review project commands, local ports, test data, interactions, masks, and failure handling. Raw input is used for this request; the evidence manifest stores its digest.",
      )}</p>
      <details><summary>{t("核对本次执行条件", "Review execution requirements")}</summary>
        <p>{t("选择 Code / Local / Deliver 执行，并核对当前 Full 进程激活、有效根执行租约和 restricted 浏览器 CDP 权限；这些授权需分别满足。",
          "Choose a Code / Local / Deliver execution and review its current Full process activation, active root execution lease, and restricted browser CDP permission. Each authorization must be satisfied separately.")}</p></details>
      <button className="compact-command" disabled={!client.hasUIEvidence}
        onClick={() => {
          setRequestJSON(JSON.stringify(templateRequest(), null, 2));
          setReviewed(false);
          setLocalError("");
        }} type="button"><FileJson aria-hidden="true" size={13} />
        {t("载入本仓库模板", "Load repository template")}</button>
      <form onSubmit={submit}>
        <textarea aria-label={t("精确 UI 证据启动 JSON", "Exact UI evidence launch JSON")}
          disabled={!client.hasUIEvidence || start.isPending} onChange={(event) => {
            setRequestJSON(event.target.value);
            setReviewed(false);
          }} placeholder="uiEvidenceStartView JSON" spellCheck={false} value={requestJSON} />
        <label><input checked={reviewed} disabled={!client.hasUIEvidence || requestJSON.trim() === ""}
          onChange={(event) => setReviewed(event.target.checked)} type="checkbox" />
          {t("我已核对完整清单；目标是当前审阅源码，且 fixture 不含秘密或个人数据。",
            "I reviewed the complete manifest; it targets the current reviewed source and the fixture contains no secrets or personal data.")}</label>
        <button className="command-button" disabled={!client.hasUIEvidence || !reviewed ||
          requestJSON.trim() === "" || start.isPending} type="submit">
          {start.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={14} /> :
            <Play aria-hidden="true" size={14} />}
          {t("启动真实浏览器验证", "Start real-browser verification")}
        </button>
      </form>
    </details>
    {(localError || mutationError) && <div className="inline-warning" role="alert">
      {localError || humanError(mutationError)}</div>}
  </div>;
}

function AttemptButton({ attempt, onSelect, selected }: {
  attempt: UIEvidenceAttempt;
  onSelect: () => void;
  selected: boolean;
}) {
  const { t } = useLocale();
  return <button aria-pressed={selected} className={selected ? "selected" : ""}
    onClick={onSelect} type="button">
    <span><strong>{attempt.manifest.route}</strong>
      <small>{shortID(attempt.manifest.attempt_id)} · {formatDate(attempt.created_at)}</small></span>
    <StatusBadge status={attempt.status}
      label={attempt.status === "not_run" ? t("未运行", "Not run") : undefined} />
  </button>;
}

function AttemptDetail({ artifacts, attempt, onDownload, steps }: {
  artifacts: UIEvidenceArtifactMetadata[];
  attempt: UIEvidenceAttempt;
  onDownload: (artifact: UIEvidenceArtifactMetadata) => void;
  steps: Array<{
    step_id: string;
    sequence: number;
    kind: string;
    status: string;
    failure_stage: string;
    message?: string;
    completed_at: string;
  }>;
}) {
  const { t } = useLocale();
  const manifest = attempt.manifest;
  const cleanupComplete = Object.values(attempt.cleanup).every(Boolean);
  const sourceLabel = `${shortID(manifest.source.commit)}${manifest.source.dirty ? " + dirty" : ""}`;
  return <>
    <header><div><strong>{manifest.route}</strong><code>{manifest.attempt_id}</code></div>
      <StatusBadge status={attempt.status}
        label={attempt.status === "not_run" ? t("未运行", "Not run") : undefined} /></header>
    <dl className="ui-evidence-facts">
      <div><dt>{t("源码", "Source")}</dt><dd>{sourceLabel}</dd></div>
      <div><dt>{t("Dirty digest", "Dirty digest")}</dt><dd><code>{manifest.source.dirty_digest}</code></dd></div>
      <div><dt>{t("浏览器", "Browser")}</dt><dd>{manifest.browser.product} {manifest.browser.version}</dd></div>
      <div><dt>{t("驱动", "Driver")}</dt><dd><code>{manifest.browser.driver_protocol}</code></dd></div>
      <div><dt>URL / route</dt><dd><code>{manifest.url}</code> · <code>{manifest.route}</code></dd></div>
      <div><dt>{t("视口", "Viewport")}</dt><dd>{manifest.environment.viewport.width} × {manifest.environment.viewport.height} @ {manifest.environment.viewport.dpr}x</dd></div>
      <div><dt>{t("呈现环境", "Presentation")}</dt><dd>{manifest.environment.locale} · {manifest.environment.theme} · {manifest.environment.reduced_motion ? "reduced motion" : "full motion"}</dd></div>
      <div><dt>Fixture / seed</dt><dd>{manifest.fixture.name} · <code>{manifest.fixture.seed}</code></dd></div>
      <div><dt>{t("页面状态", "Page state")}</dt><dd><code>{manifest.fixture.page_state}</code></dd></div>
      <div><dt>{t("产物", "Artifacts")}</dt><dd>{attempt.artifact_count} · {formatBytes(attempt.artifact_bytes)}</dd></div>
      <div><dt>{t("清理", "Cleanup")}</dt><dd>{cleanupComplete ? t("完整", "complete") : t("不完整", "incomplete")}</dd></div>
      <div><dt>{t("失败阶段", "Failure stage")}</dt><dd>{attempt.failure_stage}</dd></div>
    </dl>
    {attempt.failure_message && <div className="ui-evidence-failure" role="alert">
      <strong>{attempt.failure_code}</strong><span>{attempt.failure_message}</span></div>}
    <div className="ui-evidence-diagnostics">
      {Object.entries(attempt.diagnostics).map(([key, value]) => <span key={key}>
        {key.replaceAll("_", " ")} <strong>{value}</strong></span>)}
    </div>
    <details><summary>{t("精确源码、命令与清单绑定", "Exact source, recipe, and manifest binding")}</summary>
      <pre>{JSON.stringify({ source: manifest.source, build: manifest.build,
        start: manifest.start, readiness: manifest.readiness, fixture: manifest.fixture,
        capture: manifest.capture, failure_policy: manifest.failure_policy,
        authority: manifest.authority, fingerprint: manifest.fingerprint }, null, 2)}</pre>
    </details>
    <section className="ui-evidence-steps">
      <h3>{t("步骤收据", "Step receipts")}</h3>
      {steps.length === 0 ? <p>{t("尚无已执行步骤。", "No step has executed.")}</p> : steps.map((step) =>
        <div key={`${step.sequence}:${step.step_id}`}><span><strong>{step.sequence}. {step.step_id}</strong>
          <small>{step.kind} · {formatDate(step.completed_at)}</small></span>
          <StatusBadge status={step.status} />{step.failure_stage !== "none" &&
            <small>{step.failure_stage}{step.message ? ` · ${step.message}` : ""}</small>}</div>)}
    </section>
    <section className="ui-evidence-artifacts">
      <h3>{t("哈希验证的不可信产物", "Hash-verified untrusted artifacts")}</h3>
      {artifacts.length === 0 ? <p>{t("尚无产物。", "No artifacts.")}</p> : artifacts.map((artifact) =>
        <div key={artifact.id}><span><strong>{artifact.kind}</strong>
          <code>{artifact.sha256}</code><small>{artifact.mime} · {formatBytes(artifact.bytes)} · {artifact.step_id} · {formatDate(artifact.created_at)} · {artifact.retention_policy} · {artifact.redacted ? t("已脱敏", "redacted") : t("未标记脱敏", "not marked redacted")}</small></span>
          <button aria-label={t(`下载不可信产物 ${artifact.id}`, `Download untrusted artifact ${artifact.id}`)}
            className="icon-button" onClick={() => void onDownload(artifact)} type="button">
            <Download aria-hidden="true" size={14} /></button></div>)}
    </section>
  </>;
}

function artifactFilename(artifact: UIEvidenceArtifactMetadata): string {
  const extension = artifact.mime === "image/png" ? "png" :
    artifact.mime === "application/json" ? "json" : "txt";
  const safeID = artifact.id.replace(/[^a-zA-Z0-9._-]/gu, "-");
  return `untrusted-${artifact.kind}-${safeID}.${extension}`;
}

function humanError(value: unknown): string {
  return value instanceof Error ? value.message : String(value || "Unknown error");
}
