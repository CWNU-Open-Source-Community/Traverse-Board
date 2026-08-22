import { useEffect, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, LoaderCircle } from "lucide-react";
import { CyberAgentClient, clientCapabilitiesFromRuntime } from "../api/client";
import { desktopBridgeAvailable, desktopErrorMessage, loadDesktopBootstrap } from "../lib/desktop-bridge";
import { useConnectionStore } from "../state/connection";
import { useLocale } from "../lib/locale";
import { PrayuBrand } from "./prayu-brand";

export function ConnectionGate() {
  const { t } = useLocale();
  const [token, setToken] = useState("");
  const [controlToken, setControlToken] = useState("");
  const [error, setError] = useState("");
  const [connecting, setConnecting] = useState(false);
  const connect = useConnectionStore((state) => state.connect);
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!desktopBridgeAvailable()) {
      return;
    }
    let active = true;
    setConnecting(true);
    setError("");
    void loadDesktopBootstrap().then(async (bootstrap) => {
      if (!bootstrap || !active) {
        return;
      }
      const client = new CyberAgentClient(bootstrap.read_token, bootstrap.api_base_url,
        bootstrap.control_token);
      const health = await client.health();
      if (!active) {
        return;
      }
      queryClient.clear();
      connect(bootstrap.read_token, health, bootstrap.control_token, {
        runControlEnabled: bootstrap.control_enabled,
        executionPermissionControlEnabled: bootstrap.execution_permission_control_enabled,
        workspaceSandboxEnabled: bootstrap.workspace_sandbox_enabled,
        browserCDPPermissionControlEnabled:
          bootstrap.browser_cdp_permission_control_enabled,
        fullCDPDebugEnabled: bootstrap.full_cdp_debug_enabled,
        operatorApprovalEnabled: bootstrap.operator_approval_enabled,
        dangerFullAccessEnabled: bootstrap.danger_full_access_enabled,
        debugMaximumAccessEnabled: bootstrap.debug_maximum_access_enabled,
        commandRuntimeEnabled: bootstrap.command_runtime_enabled,
        runCreationEnabled: bootstrap.run_creation_enabled,
        sessionMessageEnabled: bootstrap.session_message_enabled,
        sessionSteeringControlEnabled: bootstrap.session_steering_control_enabled,
        runLifecycleEnabled: bootstrap.run_lifecycle_enabled,
        runExecutionEnabled: bootstrap.run_execution_enabled,
        planDeliveryControlEnabled: bootstrap.plan_delivery_control_enabled,
        approvalControlEnabled: bootstrap.approval_control_enabled,
        controlledCommandProposalControlEnabled:
          bootstrap.controlled_command_proposal_control_enabled,
		hostCommandProposalControlEnabled:
		  bootstrap.host_command_proposal_control_enabled,
		modelControlEnabled: bootstrap.model_control_enabled,
		providerCredentialEnabled: bootstrap.provider_credential_enabled,
		fileEditReviewEnabled: bootstrap.file_edit_review_enabled,
		fileEditProposalEnabled: bootstrap.file_edit_proposal_enabled,
		fileEditApplyEnabled: bootstrap.file_edit_apply_enabled,
		runWakeControlEnabled: bootstrap.run_wake_control_enabled,
		runWakeExecutionEnabled: bootstrap.run_wake_execution_enabled,
		runWakeWorkerEnabled: bootstrap.run_wake_worker_enabled,
		scheduledJobControlEnabled: bootstrap.scheduled_job_control_enabled,
		scheduledJobWorkerEnabled: bootstrap.scheduled_job_worker_enabled,
		skillInstallationEnabled: bootstrap.skill_installation_enabled,
		evidenceAttachmentEnabled: bootstrap.evidence_attachment_enabled,
		verificationEvidenceEnabled: bootstrap.verification_evidence_enabled,
		uiEvidenceControlEnabled: bootstrap.ui_evidence_control_enabled,
		embeddedAnalyzerExecutionEnabled: bootstrap.embedded_analyzer_execution_enabled,
		gitAdvancedControlEnabled: bootstrap.git_advanced_control_enabled,
		workspaceCheckpointControlEnabled: bootstrap.workspace_checkpoint_control_enabled,
		batchDeliveryControlEnabled: bootstrap.batch_delivery_control_enabled,
		batchDeliveryHostValidationEnabled: bootstrap.batch_delivery_host_validation_enabled,
		dockerExecutionEnabled: bootstrap.docker_execution_enabled,
		agentCodeToolsEnabled: bootstrap.agent_code_tools_enabled,
		codeIntelEnabled: bootstrap.code_intel_enabled,
      });
    }).catch((caught: unknown) => {
      if (active) {
        setError(desktopErrorMessage(caught));
      }
    }).finally(() => {
      if (active) {
        setConnecting(false);
      }
    });
    return () => {
      active = false;
    };
  }, [connect, queryClient]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const candidate = token.trim();
    if (!candidate || connecting) {
      return;
    }
    setConnecting(true);
    setError("");
    try {
      const client = new CyberAgentClient(candidate);
      const [health, capabilities] = await Promise.all([
        client.health(), client.runtimeCapabilities(),
      ]);
      queryClient.clear();
      connect(candidate, health, controlToken.trim(), clientCapabilitiesFromRuntime(capabilities));
      setToken("");
      setControlToken("");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "无法连接 Go 控制面");
    } finally {
      setConnecting(false);
    }
  };

  return (
    <main className="connection-page">
      <form className="connection-panel" onSubmit={submit}>
        <PrayuBrand className="connection-brand" variant="hero" />
        <div className="connection-heading">
          <h1>{t("连接本地控制面", "Connect to local control plane")}</h1>
          <p>Traverse Board · 针路簿 · Go API / api.v1</p>
        </div>
        {connecting && desktopBridgeAvailable() &&
          <div className="desktop-connecting"><LoaderCircle aria-hidden="true" className="spin" size={16} />{t("启动桌面工作台", "Starting desktop workbench")}</div>}
        <label className="field-label" htmlFor="read-token">{t("只读访问令牌", "Read bearer token")}</label>
        <div className="token-row">
          <input
            autoCapitalize="none"
            autoComplete="off"
            autoCorrect="off"
            id="read-token"
            name="read-token"
            onChange={(event) => setToken(event.target.value)}
            placeholder="CYBERAGENT_API_TOKEN"
            spellCheck={false}
            type="password"
            value={token}
          />
          <button aria-label="连接" disabled={!token.trim() || connecting} title="连接" type="submit">
            {connecting ? <LoaderCircle aria-hidden="true" className="spin" size={18} /> : <ArrowRight aria-hidden="true" size={18} />}
          </button>
        </div>
        <label className="field-label optional-token-label" htmlFor="control-token">
          {t("控制访问令牌", "Control bearer token")} <span>{t("可选", "optional")}</span>
        </label>
        <input
          autoCapitalize="none"
          autoComplete="off"
          autoCorrect="off"
          className="control-token-input"
          id="control-token"
          name="control-token"
          onChange={(event) => setControlToken(event.target.value)}
          placeholder="CYBERAGENT_API_CONTROL_TOKEN"
          spellCheck={false}
          type="password"
          value={controlToken}
        />
        {error && <div className="connection-error" role="alert">{error}</div>}
      </form>
    </main>
  );
}
