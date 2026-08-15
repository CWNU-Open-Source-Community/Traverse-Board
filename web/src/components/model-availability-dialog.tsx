import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Check, Cpu, KeyRound, LoaderCircle, Route, Save, ShieldCheck, Trash2, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ModelHarnessQualificationView, ProviderDiagnosticView } from "../api/types";
import { ErrorState, LoadingState, StatusBadge } from "./common";
import { useLocale } from "../lib/locale";
import { useModalFocusTrap } from "../hooks/use-modal-focus-trap";
import { PriceSnapshotsSection } from "./price-snapshots-panel";

export function ModelAvailabilityDialog({ client, open, onClose }: {
  client: CyberAgentClient;
  open: boolean;
  onClose: () => void;
}) {
  return <ModelAvailabilitySurface client={client} onClose={onClose} open={open}
    presentation="dialog" />;
}

export function ModelAvailabilityWorkspace({ client }: { client: CyberAgentClient }) {
  return <ModelAvailabilitySurface client={client} onClose={() => undefined} open
    presentation="workspace" />;
}

function ModelAvailabilitySurface({ client, open, onClose, presentation }: {
  client: CyberAgentClient;
  open: boolean;
  onClose: () => void;
  presentation: "dialog" | "workspace";
}) {
  const { t } = useLocale();
  const qualificationStatusLabel = (status: string) => {
    const labels: Record<string, [string, string]> = {
      not_configured: ["未配置", "not configured"],
      available: ["可用", "available"],
      protocol_mismatch: ["协议不兼容", "protocol mismatch"],
      auth_failed: ["身份验证失败", "authentication failed"],
      network_failed: ["网络不可达", "network unreachable"],
      rate_limit: ["达到速率限制", "rate limited"],
      capacity: ["容量不足", "capacity unavailable"],
      model_unsupported: ["模型不支持", "model unsupported"],
    };
    const label = labels[status];
    return label ? t(label[0], label[1]) : status;
  };
  const failureReasonLabel = (reason: string) => {
    const labels: Record<string, [string, string]> = {
      not_configured: ["未配置", "not configured"],
      authentication: ["身份验证失败", "authentication failed"],
      network: ["网络不可达", "network unreachable"],
      rate_limit: ["达到速率限制", "rate limited"],
      capacity: ["Provider 容量不足", "Provider capacity unavailable"],
      model_not_found: ["模型不存在", "model not found"],
      protocol_incompatible: ["协议不兼容", "protocol incompatible"],
    };
    const label = labels[reason];
    return label ? t(label[0], label[1]) : reason;
  };
  const queryClient = useQueryClient();
  const [selections, setSelections] = useState<Record<string, string>>({});
  const [diagnostic, setDiagnostic] = useState<ProviderDiagnosticView | null>(null);
  const [qualification, setQualification] = useState<ModelHarnessQualificationView | null>(null);
  const [credentialBusy, setCredentialBusy] = useState("");
  const [credentialError, setCredentialError] = useState("");
  const [credentialRestart, setCredentialRestart] = useState(false);
  const [credentialGeneration, setCredentialGeneration] = useState<number | null>(null);
  const credentialInputs = useRef(new Map<string, HTMLInputElement>());
  const dialogRef = useModalFocusTrap<HTMLElement>(open && presentation === "dialog", onClose);
  const query = useQuery({
    queryKey: ["models", "availability"],
    queryFn: ({ signal }) => client.modelAvailability(signal),
    enabled: open,
  });
  const credentialQuery = useQuery({
    queryKey: ["models", "credentials"],
    queryFn: ({ signal }) => client.providerCredentialStatuses(signal),
    enabled: open && client.hasProviderCredentials,
  });
  const routeMutation = useMutation({
    mutationFn: ({ route, reference }: { route: string; reference: string }) => {
      const slash = reference.indexOf("/");
      if (slash <= 0 || slash === reference.length - 1) {
        throw new Error(t("请选择可用的 Provider 模型", "Select an available Provider model"));
      }
      return client.selectModelRoute(route, {
        version: "model_route_control.v1",
        provider: reference.slice(0, slash), model: reference.slice(slash + 1),
      });
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["models", "availability"] }),
  });
  const diagnosticMutation = useMutation({
    mutationFn: ({ provider, model }: { provider: string; model: string }) =>
      client.diagnoseProvider({ version: "provider_diagnostic.v1", provider, model,
        confirm_diagnostic: true }),
    onSuccess: setDiagnostic,
  });
  const qualificationMutation = useMutation({
    mutationFn: ({ provider, model }: { provider: string; model: string }) =>
      client.qualifyModelHarness({ version: "model_harness_qualification.v1",
        provider, model, confirm_qualification: true }),
    onSuccess: async (result) => {
      setQualification(result);
      await queryClient.invalidateQueries({ queryKey: ["models", "availability"] });
    },
  });
  const changeCredential = async (provider: string, action: "set" | "delete") => {
    if (credentialBusy) return;
    const input = credentialInputs.current.get(provider);
    const secret = action === "set" ? input?.value ?? "" : "";
    if (action === "set" && secret.length < 8) {
      setCredentialError(t("凭证必须至少包含 8 个非空白字符", "Credential must contain at least 8 non-space characters"));
      return;
    }
    if (input) input.value = "";
    setCredentialBusy(provider);
    setCredentialError("");
    const body = { version: "provider_credential.v1" as const, action,
      secret, confirm: true };
    try {
      const status = await client.changeProviderCredential(provider, body);
      setCredentialRestart(status.restart_required);
      setCredentialGeneration(status.registry_reloaded ? status.registry_generation : null);
      await Promise.all([credentialQuery.refetch(),
        queryClient.invalidateQueries({ queryKey: ["models", "availability"] })]);
    } catch (caught) {
      setCredentialError(caught instanceof Error ? caught.message : t("凭证修改失败", "Credential change failed"));
    } finally {
      body.secret = "";
      setCredentialBusy("");
    }
  };
  if (!open) {
    return null;
  }
  const surface = (
      <section aria-label={presentation === "dialog" ? t("模型可用性", "Model availability") : t("模型切换", "Model selection")}
        aria-modal={presentation === "dialog" ? "true" : undefined}
        className={presentation === "dialog"
          ? "desktop-dialog model-availability-dialog" : "model-control-workspace"}
        ref={dialogRef} role={presentation === "dialog" ? "dialog" : "region"}
        tabIndex={presentation === "dialog" ? -1 : undefined}>
        <header>
          <div>
            <span className="dialog-icon"><Cpu aria-hidden="true" size={18} /></span>
            <div><h2>{presentation === "dialog" ? t("模型", "Models") : t("模型切换", "Model selection")}</h2>
              <small>model_availability.v2</small></div>
          </div>
          {presentation === "dialog" && <button aria-label={t("关闭模型面板", "Close model availability")} className="icon-button"
            onClick={onClose} title={t("关闭", "Close")} type="button">
            <X aria-hidden="true" size={17} />
          </button>}
        </header>
        <div className="desktop-dialog-body model-availability-body">
          {query.isLoading && <LoadingState label={t("加载模型可用性", "Loading model availability")} />}
          {query.isError && <ErrorState error={query.error} />}
          {query.data && (
            <>
              <section className="model-availability-section">
                <h3><Cpu aria-hidden="true" size={14} />{t("提供商", "Provider")}</h3>
                <div className="model-provider-list">
                  {query.data.providers.flatMap((provider) =>
                    (provider.models.length > 0 ? provider.models : [""]).map((model) => {
                    const harness = provider.harnesses.find((candidate) => candidate.model === model);
                    const modelReference = `${provider.name}/${model}`;
                    return <div className="model-provider-row" key={modelReference}>
                      <div><strong>{provider.name}</strong><small>{provider.kind}</small></div>
                      <span>{model || t("未配置模型", "No configured model")}</span>
                      <span>{harness
                        ? `${harness.transport_protocol} · JSON ${harness.json_strategy}`
                        : provider.credential_source}</span>
                      <StatusBadge status={provider.status} />
                      {harness && <StatusBadge status={harness.qualification_status} />}
                      {harness?.latest_qualification_status &&
                        <span title={t("最近一次诊断状态", "Latest diagnostic qualification")}>
                          {qualificationStatusLabel(harness.latest_qualification_status)}
                        </span>}
                      {client.hasModelControl &&
                        (provider.status === "available" ||
                          provider.status === "not_configured") &&
                        model && (
                          <>
                            <button aria-label={t(`诊断 ${modelReference}`, `Diagnose ${modelReference}`)} className="icon-button"
                              disabled={diagnosticMutation.isPending ||
                                qualificationMutation.isPending}
                              onClick={() => diagnosticMutation.mutate({ provider: provider.name,
                                model })}
                              title={t("运行单次连接诊断", "Run one-call connectivity diagnostic")} type="button">
                              {diagnosticMutation.isPending &&
                                diagnosticMutation.variables?.provider === provider.name &&
                                diagnosticMutation.variables.model === model
                                ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                                : <Activity aria-hidden="true" size={15} />}
                            </button>
                            <button aria-label={t(`验证 ${modelReference} Harness`, `Qualify ${modelReference} Harness`)}
                              className="icon-button"
                              disabled={diagnosticMutation.isPending ||
                                qualificationMutation.isPending || harness?.root_eligible === true}
                              onClick={() => qualificationMutation.mutate({
                                provider: provider.name, model,
                              })}
                              title={t("运行两次调用的 Harness 合成验证", "Run two-call synthetic Harness qualification")} type="button">
                              {qualificationMutation.isPending &&
                                qualificationMutation.variables?.provider === provider.name &&
                                qualificationMutation.variables.model === model
                                ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                                : <ShieldCheck aria-hidden="true" size={15} />}
                            </button>
                          </>
                        )}
                    </div>;
                  }))}
                </div>
                {diagnostic && <div className="model-diagnostic-result" role="status">
                  <span>{diagnostic.provider}/{diagnostic.model}</span>
                  <StatusBadge status={diagnostic.status} />
                  {diagnostic.qualification_status &&
                    <span title={t("端点资格状态", "Endpoint qualification status")}>
                      {qualificationStatusLabel(diagnostic.qualification_status)}
                    </span>}
                  <span>{diagnostic.failure_reason !== "none"
                    ? failureReasonLabel(diagnostic.failure_reason)
                    : diagnostic.outcome === "success"
                    ? t("成功", "success")
                    : diagnostic.outcome === "invalid_response"
                      ? t("响应格式不兼容", "invalid response")
                      : diagnostic.outcome}</span>
                  <span>{diagnostic.duration_ms} ms</span>
                </div>}
                {diagnosticMutation.isError && <div className="inline-warning" role="alert">
                  {diagnosticMutation.error instanceof Error
                    ? diagnosticMutation.error.message : t("Provider 诊断失败", "Provider diagnostic failed")}
                </div>}
                {qualification && <div className="model-diagnostic-result" role="status">
                  <span>{qualification.provider}/{qualification.model}</span>
                  <StatusBadge status={qualification.status} />
                  <span>{qualification.harness.transport_protocol}</span>
                  {qualification.qualification_status &&
                    <span title={t("端点资格状态", "Endpoint qualification status")}>
                      {qualificationStatusLabel(qualification.qualification_status)}
                    </span>}
                  {qualification.failure_reason !== "none" &&
                    <span>{failureReasonLabel(qualification.failure_reason)}</span>}
                  <span>{qualification.model_calls} {t("次模型调用", "model calls")}</span>
                </div>}
                {qualificationMutation.isError && <div className="inline-warning" role="alert">
                  {qualificationMutation.error instanceof Error
                    ? qualificationMutation.error.message : t("模型 Harness 验证失败", "Model Harness qualification failed")}
                </div>}
              </section>
              {client.hasProviderCredentials && <section className="model-availability-section">
                <h3><KeyRound aria-hidden="true" size={14} />{t("系统凭证", "System credentials")}</h3>
                {credentialQuery.isLoading && <LoadingState label={t("加载凭证状态", "Loading credential status")} />}
                {credentialQuery.isError && <ErrorState error={credentialQuery.error} />}
                {credentialQuery.data && <div className="provider-credential-list">
                  {credentialQuery.data.items.map((item) => <div className="provider-credential-row"
                    key={item.provider}>
                    <div><strong>{item.provider}</strong><small>{item.store_kind}</small></div>
                    <StatusBadge status={item.configured ? "configured" : "not configured"} />
                    <input aria-label={t(`${item.provider} API 凭证`, `${item.provider} API credential`)} autoCapitalize="none"
                      autoComplete="off" autoCorrect="off"
                      disabled={!item.store_available || credentialBusy === item.provider}
                      maxLength={2560} ref={(element) => {
                        if (element) credentialInputs.current.set(item.provider, element);
                        else credentialInputs.current.delete(item.provider);
                      }} spellCheck={false} type="password" />
                    <button aria-label={t(`保存 ${item.provider} 凭证`, `Store ${item.provider} credential`)} className="icon-button"
                      disabled={!item.store_available || Boolean(credentialBusy)}
                      onClick={() => void changeCredential(item.provider, "set")}
                      title={t("保存到操作系统凭证管理器", "Store in the OS credential manager")} type="button">
                      {credentialBusy === item.provider ?
                        <LoaderCircle aria-hidden="true" className="spin" size={15} /> :
                        <Save aria-hidden="true" size={15} />}
                    </button>
                    <button aria-label={t(`删除 ${item.provider} 凭证`, `Delete ${item.provider} credential`)} className="icon-button"
                      disabled={!item.store_available || !item.configured || Boolean(credentialBusy)}
                      onClick={() => void changeCredential(item.provider, "delete")}
                      title={t("删除操作系统凭证", "Delete OS credential")} type="button">
                      <Trash2 aria-hidden="true" size={15} />
                    </button>
                  </div>)}
                </div>}
                {credentialRestart && <div className="model-diagnostic-result" role="status">
                  <Check aria-hidden="true" size={14} />{t("凭证状态已更新", "Credential status updated")}
                  <span>{t("需要重启才能加载 Provider", "Restart required to load the Provider")}</span>
                </div>}
                {credentialGeneration !== null && <div className="model-diagnostic-result" role="status">
                  <Check aria-hidden="true" size={14} />{t("凭证状态已更新", "Credential status updated")}
                  <span>{t(`注册表代次 ${credentialGeneration} 已生效`, `Registry generation ${credentialGeneration} active`)}</span>
                </div>}
                {credentialError && <div className="inline-warning" role="alert">
                  {credentialError}
                </div>}
              </section>}
              <PriceSnapshotsSection client={client} />
              <section className="model-availability-section">
                <h3><Route aria-hidden="true" size={14} />{t("模型路由", "Routes")}</h3>
                <div className="model-route-list">
                  {query.data.routes.map((route) => (
                    <div className="model-route-row" key={route.name}>
                      <strong>{route.name}</strong>
                      {client.hasModelControl ? <select aria-label={t(`${route.name} 模型路由`, `${route.name} model route`)}
                        onChange={(event) => setSelections((current) => ({ ...current,
                          [route.name]: event.target.value }))}
                        value={selections[route.name] ?? `${route.provider}/${route.model}`}>
                        {query.data.providers.filter((provider) => provider.status === "available")
                          .flatMap((provider) => provider.models.map((model) => (
                            <option key={`${provider.name}/${model}`} value={`${provider.name}/${model}`}>
                              {provider.name}/{model}
                            </option>
                          )))}
                      </select> : <span>{route.provider}/{route.model}</span>}
                      <StatusBadge status={!route.available ? "unavailable"
                        : route.harness_ready ? "harness ready" : "qualification required"} />
                      {client.hasModelControl && <button aria-label={t(`保存 ${route.name} 路由`, `Save ${route.name} route`)}
                        className="icon-button" disabled={routeMutation.isPending}
                        onClick={() => routeMutation.mutate({ route: route.name,
                          reference: selections[route.name] ?? `${route.provider}/${route.model}` })}
                        title={t("持久化路由选择", "Persist route selection")} type="button">
                        {routeMutation.isPending && routeMutation.variables?.route === route.name
                          ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                          : <Check aria-hidden="true" size={15} />}
                      </button>}
                    </div>
                  ))}
                </div>
              </section>
              {routeMutation.isError && <div className="inline-warning" role="alert">
                {routeMutation.error instanceof Error
                  ? routeMutation.error.message : t("模型路由选择失败", "Model route selection failed")}
              </div>}
            </>
          )}
        </div>
      </section>
  );
  if (presentation === "workspace") return surface;
  return <div className="desktop-dialog-backdrop" role="presentation">{surface}</div>;
}
