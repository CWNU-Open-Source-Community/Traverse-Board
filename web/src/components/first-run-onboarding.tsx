import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, FolderPlus, LoaderCircle,
  RefreshCw, ShieldCheck, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { StandardCodePresetControlRequestView,
  StandardCodePresetControlView, WorkspaceView } from "../api/types";
import { useModalFocusTrap } from "../hooks/use-modal-focus-trap";
import { desktopWorkspaceImportEnabled, importDesktopWorkspace } from "../lib/desktop-bridge";
import { useLocale } from "../lib/locale";
import { ModelAvailabilityWorkspace } from "./model-availability-dialog";

type BackendIntent = StandardCodePresetControlRequestView["backend_intent"];

interface RetryIntent {
  fingerprint: string;
  key: string;
}

const blockerLabels: Record<string, [string, string]> = {
  run_not_quiescent: ["Run 尚未暂停", "Run is not paused"],
  execution_lease_active: ["执行租约仍在释放", "Execution lease is still releasing"],
  startup_gate_closed: ["启动时能力门未开启", "Startup capability gate is closed"],
  capability_not_implemented: ["当前版本尚未实现", "Not implemented by this version"],
  surface_mismatch: ["任务工作面不兼容", "Task surface is incompatible"],
  profile_mismatch: ["执行档位不兼容", "Execution profile is incompatible"],
  permission_mismatch: ["权限档位不兼容", "Permission profile is incompatible"],
  workspace_untrusted: ["工作区尚未受信任", "Workspace trust is required"],
  sandbox_unproven: ["本地沙箱尚未通过验证", "Local sandbox is not verified"],
  docker_unavailable: ["Docker 不可用", "Docker is unavailable"],
  backend_not_ready: ["执行后端尚未就绪", "Execution backend is not ready"],
};

const remediationLabels: Record<string, [string, string]> = {
  pause_run: ["暂停 Run 后重试", "Pause the Run and retry"],
  create_new_run: ["创建新的 Run", "Create a new Run"],
  wait_for_execution_lease: ["等待执行租约释放", "Wait for the execution lease to release"],
  restart_with_startup_gate: ["使用所需启动能力重新启动", "Restart with the required startup gate"],
  upgrade_application: ["升级到支持该能力的版本", "Upgrade to a version that supports this capability"],
  select_required_surface: ["选择所需工作面", "Select the required surface"],
  select_required_profile: ["选择所需执行档位", "Select the required execution profile"],
  select_required_permission: ["选择所需权限档位", "Select the required permission profile"],
  trust_workspace: ["核对并确认工作区信任", "Review and confirm Workspace trust"],
  verify_sandbox: ["修复并重新验证本地沙箱", "Repair and verify the local sandbox"],
  install_or_start_docker: ["安装或启动 Docker", "Install or start Docker"],
  retry_backend_readiness: ["重新检查后端就绪状态", "Retry backend readiness"],
};

export function FirstRunOnboarding({ client, open, onDismiss, onComplete }: {
  client: CyberAgentClient;
  open: boolean;
  onDismiss: () => void;
  onComplete: (runID: string) => void;
}) {
  const { locale, setLocale, t } = useLocale();
  const [step, setStep] = useState(0);
  const [workspaceID, setWorkspaceID] = useState("");
  const [workspaceName, setWorkspaceName] = useState("");
  const [goal, setGoal] = useState("");
  const [backendIntent, setBackendIntent] = useState<BackendIntent>("auto");
  const [result, setResult] = useState<StandardCodePresetControlView | null>(null);
  const [trustReviewed, setTrustReviewed] = useState(false);
  const retryIntent = useRef<RetryIntent | null>(null);
  const queryClient = useQueryClient();
  const nativeWorkspaceImport = desktopWorkspaceImportEnabled();
  const availability = useQuery({
    queryKey: ["models", "availability"],
    queryFn: ({ signal }) => client.modelAvailability(signal),
    enabled: open && step >= 1 && step <= 2,
  });
  const workspaces = useQuery({
    queryKey: ["workspaces"],
    queryFn: ({ signal }) => client.getPage<WorkspaceView>(
      "/workspaces", { limit: 100 }, "", signal),
    enabled: open && step === 3 && !nativeWorkspaceImport,
  });
  const workspaceImport = useMutation({
    mutationFn: importDesktopWorkspace,
    onSuccess: (workspace) => {
      if (!workspace) return;
      setWorkspaceID(workspace.id);
      setWorkspaceName(workspace.name);
      setResult(null);
      setTrustReviewed(false);
      retryIntent.current = null;
      void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
    },
  });
  const preset = useMutation({
    mutationFn: ({ request, key }: {
      request: StandardCodePresetControlRequestView;
      key: string;
    }) => client.createStandardCode(request, key),
    onSuccess: (next) => {
      setResult(next);
      setTrustReviewed(false);
      if (next.status === "configured" && next.run_id) {
        void Promise.all([
          queryClient.invalidateQueries({ queryKey: ["onboarding", "runs"] }),
          queryClient.invalidateQueries({ queryKey: ["runs"] }),
          queryClient.invalidateQueries({ queryKey: ["sessions"] }),
          queryClient.invalidateQueries({ queryKey: ["threads"] }),
        ]);
        setStep(6);
      } else if (next.trust_required && next.trust_digest) {
        setStep(5);
      } else {
        setStep(4);
      }
    },
  });
  const busy = preset.isPending || workspaceImport.isPending;
  const dialogRef = useModalFocusTrap<HTMLElement>(open, onDismiss, busy);

  useEffect(() => {
    if (!nativeWorkspaceImport && !workspaceID && workspaces.data?.items[0]) {
      setWorkspaceID(workspaces.data.items[0].id);
      setWorkspaceName(workspaces.data.items[0].name);
    }
  }, [nativeWorkspaceImport, workspaceID, workspaces.data]);

  useEffect(() => {
    if (!open) {
      setStep(0);
      setResult(null);
      setTrustReviewed(false);
      retryIntent.current = null;
    }
  }, [open]);

  if (!open) return null;

  const providerReady = availability.data?.providers.some(
    (provider) => provider.status === "available") === true;
  const harnessReady = availability.data?.routes.some(
    (route) => route.available && route.harness_ready) === true;
  const normalizedGoal = goal.trim();
  const goalBytes = new TextEncoder().encode(normalizedGoal).byteLength;
  const workspaceReady = workspaceID !== "" && goalBytes > 0 && goalBytes <= 4096;
  const stepLabels = [t("语言", "Language"), t("Provider 与凭证", "Provider & credential"),
    t("Harness 验证", "Harness qualification"), t("工作区", "Workspace"),
    t("后端就绪", "Backend readiness"), t("信任确认", "Trust"),
    t("完成", "Complete")];

  const operationKey = (request: StandardCodePresetControlRequestView) => {
    const fingerprint = JSON.stringify(request);
    if (retryIntent.current?.fingerprint !== fingerprint) {
      retryIntent.current = { fingerprint,
        key: `web-standard-code-onboarding-${globalThis.crypto.randomUUID()}` };
    }
    return retryIntent.current.key;
  };
  const configure = (intent: BackendIntent, trustDigest = "") => {
    const request: StandardCodePresetControlRequestView = {
      version: "standard_code_preset.v1",
      workspace_id: workspaceID,
      goal: normalizedGoal,
      backend_intent: intent,
      confirm_workspace_trust: trustDigest !== "",
      ...(trustDigest ? { expected_trust_digest: trustDigest } : {}),
    };
    setBackendIntent(intent);
    preset.mutate({ request, key: operationKey(request) });
  };
  const next = () => {
    if (step < 3) {
      setStep((current) => current + 1);
      return;
    }
    if (step === 3 && workspaceReady) {
      setResult(null);
      setStep(4);
      configure("auto");
    }
  };
  const back = () => {
    if (busy || step === 0 || step === 6) return;
    preset.reset();
    retryIntent.current = null;
    setStep((current) => current - 1);
  };
  const primaryDisabled = busy ||
    (step === 1 && !providerReady) || (step === 2 && !harnessReady) ||
    (step === 3 && !workspaceReady);

  return <div className="desktop-dialog-backdrop first-run-backdrop" role="presentation">
    <section aria-labelledby="first-run-title" aria-modal="true"
      className="desktop-dialog first-run-onboarding" ref={dialogRef} role="dialog" tabIndex={-1}>
      <header>
        <div><span className="dialog-icon"><ShieldCheck aria-hidden="true" size={18} /></span>
          <div><h2 id="first-run-title">{t("首次安全设置", "Safe first-time setup")}</h2>
            <small>Standard Code · {step + 1}/{stepLabels.length}</small></div></div>
        <button aria-label={t("稍后设置", "Set up later")} className="icon-button"
          disabled={busy} onClick={onDismiss}
          title={t("稍后设置（不会创建 Run 或授予权限）",
            "Set up later (no Run or permission will be created)")} type="button">
          <X aria-hidden="true" size={17} />
        </button>
      </header>
      <ol aria-label={t("首次设置步骤", "First-time setup steps")}
        className="first-run-steps">
        {stepLabels.map((label, index) => <li aria-current={index === step ? "step" : undefined}
          className={index < step ? "complete" : index === step ? "active" : ""}
          key={label}><span>{index < step ? <Check aria-hidden="true" size={12} /> : index + 1}</span>
          <small>{label}</small></li>)}
      </ol>
      <div className="desktop-dialog-body first-run-body">
        {step === 0 && <section className="first-run-stage">
          <h3>{t("选择界面语言", "Choose your language")}</h3>
          <p>{t("语言选择只保存在本机界面设置中，不包含凭证或控制令牌。",
            "Language is stored only as a local UI preference; credentials and control tokens are never stored here.")}</p>
          <div aria-label={t("语言", "Language")} className="first-run-language" role="radiogroup">
            <button aria-checked={locale === "zh-CN"} className={locale === "zh-CN" ? "selected" : ""}
              onClick={() => setLocale("zh-CN")} role="radio" type="button">
              <strong>简体中文</strong><small>Chinese (Simplified)</small>
            </button>
            <button aria-checked={locale === "en-US"} className={locale === "en-US" ? "selected" : ""}
              onClick={() => setLocale("en-US")} role="radio" type="button">
              <strong>English</strong><small>英语</small>
            </button>
          </div>
        </section>}
        {(step === 1 || step === 2) && <section className="first-run-stage model-stage">
          <div className="first-run-stage-intro"><h3>{step === 1
            ? t("连接 Provider", "Connect a Provider")
            : t("验证模型 Harness", "Qualify the model Harness")}</h3>
            <p>{step === 1
              ? t("凭证仅写入操作系统凭证管理器。至少一个 Provider 可用后才能继续。",
                "Credentials are written only to the OS credential manager. At least one Provider must be available.")
              : t("运行两次调用的合成验证；至少一条代码路由必须显示 Harness ready。",
                "Run the two-call synthetic qualification. At least one code route must report Harness ready.")}</p></div>
          <ModelAvailabilityWorkspace client={client} />
          {availability.isError && <p className="connection-error" role="alert">
            {errorMessage(availability.error)}</p>}
          {!availability.isLoading && step === 1 && !providerReady &&
            <p className="inline-warning" role="status">{t("尚无可用 Provider。保存凭证并运行诊断。",
              "No Provider is available yet. Store a credential and run diagnostics.")}</p>}
          {!availability.isLoading && step === 2 && !harnessReady &&
            <p className="inline-warning" role="status">{t("尚无已通过验证的 Harness 路由。",
              "No Harness-qualified route is available yet.")}</p>}
        </section>}
        {step === 3 && <section className="first-run-stage">
          <h3>{t("选择工作区并描述目标", "Choose a Workspace and describe the goal")}</h3>
          <p>{t("文件夹路径由原生窗口处理，不会暴露给网页渲染器。选择文件夹不会自动信任它。",
            "The native window handles the folder path; it is not exposed to the web renderer. Choosing it does not trust it.")}</p>
          {nativeWorkspaceImport ? <div className="workspace-import-field">
            <span>{t("工作区", "Workspace")}</span>
            <button aria-label={t("选择工作文件夹", "Choose working folder")}
              className={workspaceID ? "workspace-import-control selected" : "workspace-import-control"}
              disabled={busy} onClick={() => { workspaceImport.reset(); workspaceImport.mutate(); }}
              type="button">
              {workspaceImport.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={18} />
                : <FolderPlus aria-hidden="true" size={18} />}
              <span><strong>{workspaceName || t("选择目录", "Choose folder")}</strong>
                <small>{workspaceID ? t("已注册；仍需单独确认信任", "Registered; trust still requires confirmation")
                  : t("仅注册目录，不修改其中内容", "Registers the directory without changing its contents")}</small></span>
            </button>
          </div> : <label><span>{t("工作区", "Workspace")}</span>
            <select disabled={workspaces.isLoading || !workspaces.data?.items.length}
              onChange={(event) => {
                const workspace = workspaces.data?.items.find((item) => item.id === event.target.value);
                setWorkspaceID(event.target.value);
                setWorkspaceName(workspace?.name ?? "");
                retryIntent.current = null;
              }} value={workspaceID}>
              {(workspaces.data?.items ?? []).map((workspace) =>
                <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
            </select></label>}
          <label><span>{t("编码目标", "Coding goal")}</span>
            <textarea maxLength={4096} onChange={(event) => {
              setGoal(event.target.value); setResult(null); setTrustReviewed(false);
            }} placeholder={t("例如：修复登录页并补充回归测试",
              "For example: fix the login page and add regression tests")}
              rows={5} value={goal} /></label>
          {goalBytes > 4096 && <p className="connection-error" role="alert">
            {t("目标超过 4096 个 UTF-8 字节", "Goal exceeds 4096 UTF-8 bytes")}</p>}
          {workspaceImport.isError && <p className="connection-error" role="alert">
            {errorMessage(workspaceImport.error)}</p>}
          {!nativeWorkspaceImport && workspaces.isError && <p className="connection-error" role="alert">
            {errorMessage(workspaces.error)}</p>}
        </section>}
        {step === 4 && <section className="first-run-stage">
          <h3>{t("检查安全执行后端", "Check the safe execution backend")}</h3>
          <p>{t("就绪状态来自 Go 控制面；界面不会猜测或覆盖后端结果。",
            "Readiness comes from the Go control plane; the UI neither guesses nor overrides it.")}</p>
          {preset.isPending && <div className="first-run-loading" role="status">
            <LoaderCircle aria-hidden="true" className="spin" size={20} />
            {t("正在检查本地沙箱与 Docker…", "Checking Local Sandbox and Docker…")}</div>}
          {result && <div className="first-run-readiness-grid">
            <BackendReadiness result={result} backend="local" />
            <BackendReadiness result={result} backend="docker" />
          </div>}
          {preset.isError && <p className="connection-error" role="alert">
            {errorMessage(preset.error)}</p>}
          <div className="first-run-inline-actions">
            {result?.docker_readiness.available && backendIntent !== "docker" &&
              <button className="dialog-secondary" disabled={busy}
                onClick={() => configure("docker")} type="button">
                {t("改用 Docker", "Use Docker")}</button>}
            <button className="dialog-secondary" disabled={busy}
              onClick={() => { retryIntent.current = null; configure(backendIntent); }} type="button">
              <RefreshCw aria-hidden="true" size={14} />{t("重新检查", "Retry check")}</button>
          </div>
        </section>}
        {step === 5 && result?.trust_digest && <section className="first-run-stage trust-stage">
          <h3>{t("确认工作区信任", "Confirm Workspace trust")}</h3>
          <p>{t("信任只绑定到本次选择的工作区内容摘要。升级不会自动执行这一步。",
            "Trust is bound to this selected Workspace content digest. An upgrade never performs this step automatically.")}</p>
          <dl><div><dt>{t("工作区", "Workspace")}</dt><dd>{workspaceName || workspaceID}</dd></div>
            <div><dt>{t("执行后端", "Execution backend")}</dt>
              <dd>{result.selected_backend ?? backendIntent}</dd></div>
            <div><dt>{t("网络", "Network")}</dt><dd>{t("禁用", "disabled")}</dd></div>
            <div><dt>{t("凭证注入", "Credential injection")}</dt><dd>{t("无", "none")}</dd></div></dl>
          <div className="first-run-trust-digest"><span>{t("待确认摘要", "Digest to confirm")}</span>
            <code>{result.trust_digest}</code></div>
          <label className="confirmation-check"><input checked={trustReviewed}
            onChange={(event) => setTrustReviewed(event.target.checked)} type="checkbox" />
            <span>{t("我已核对工作区与摘要，并同意为此 Standard Code Run 建立受限信任。",
              "I reviewed the Workspace and digest and approve bounded trust for this Standard Code Run.")}</span></label>
          {preset.isError && <p className="connection-error" role="alert">
            {errorMessage(preset.error)}</p>}
        </section>}
        {step === 6 && result?.run_id && <section className="first-run-stage complete-stage" role="status">
          <span className="first-run-complete-icon"><Check aria-hidden="true" size={24} /></span>
          <h3>{t("Standard Code 已就绪", "Standard Code is ready")}</h3>
          <p>{t("Run 已使用受限工作区、禁用网络且不注入凭证。高级宿主机能力仍保持关闭。",
            "The Run uses a bounded Workspace with network disabled and no credential injection. Advanced host capabilities remain off.")}</p>
          <code>{result.run_id}</code>
        </section>}
      </div>
      <footer className="first-run-actions">
        <button className="dialog-secondary" disabled={busy || step === 0 || step === 6}
          onClick={back} type="button"><ChevronLeft aria-hidden="true" size={15} />
          {t("上一步", "Back")}</button>
        <span aria-live="polite" className="first-run-progress-label">{stepLabels[step]}</span>
        {step <= 3 && <button className="dialog-primary" disabled={primaryDisabled}
          onClick={next} type="button">{step === 3
            ? t("检查并继续", "Check and continue") : t("继续", "Continue")}
          <ChevronRight aria-hidden="true" size={15} /></button>}
        {step === 5 && result?.trust_digest && <button className="dialog-primary"
          disabled={busy || !trustReviewed}
          onClick={() => configure(backendIntent, result.trust_digest)} type="button">
          {preset.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
            : <ShieldCheck aria-hidden="true" size={15} />}
          {t("确认并创建", "Confirm and create")}</button>}
        {step === 6 && result?.run_id && <button className="dialog-primary"
          onClick={() => onComplete(result.run_id!)} type="button">
          {t("开始编码", "Start coding")}<ChevronRight aria-hidden="true" size={15} />
        </button>}
      </footer>
    </section>
  </div>;
}

function BackendReadiness({ result, backend }: {
  result: StandardCodePresetControlView;
  backend: "local" | "docker";
}) {
  const { t } = useLocale();
  const readiness = backend === "local" ? result.local_readiness : result.docker_readiness;
  return <section aria-label={backend === "local"
    ? t("本地沙箱就绪状态", "Local Sandbox readiness")
    : t("Docker 就绪状态", "Docker readiness")}
    className={readiness.available ? "first-run-backend ready" : "first-run-backend blocked"}>
    <header><strong>{backend === "local" ? t("本地沙箱", "Local Sandbox") : "Docker"}</strong>
      <span>{readiness.available ? t("可用", "Available") : t("不可用", "Unavailable")}</span></header>
    {readiness.blocked_by.length > 0 && <ReasonList blockers={readiness.blocked_by}
      remediations={readiness.remediation} />}
  </section>;
}

function ReasonList({ blockers, remediations }: {
  blockers: readonly string[];
  remediations: readonly string[];
}) {
  const { t } = useLocale();
  return <div className="first-run-reasons">
    {blockers.map((value) => <p key={value}><strong>{t("原因", "Reason")}:</strong>{" "}
      {label(value, blockerLabels, t)}</p>)}
    {remediations.map((value) => <p key={value}><strong>{t("处理方法", "Remediation")}:</strong>{" "}
      {label(value, remediationLabels, t)}</p>)}
  </div>;
}

function label(value: string, labels: Record<string, [string, string]>,
  t: (chinese: string, english: string) => string): string {
  const translated = labels[value];
  return translated ? t(translated[0], translated[1]) : value;
}

function errorMessage(value: unknown): string {
  return value instanceof Error && value.message.trim() ? value.message : "First-time setup failed";
}
