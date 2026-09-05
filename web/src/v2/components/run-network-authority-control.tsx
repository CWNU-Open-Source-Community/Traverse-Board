import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Globe2, LoaderCircle, ShieldCheck } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { ProviderSearchReadinessView, RunDetailView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { browserCDPQueryKey } from "./browser-cdp-control";
import { V2ConfirmDialog } from "./dialog";
import {
  canonicalizeExactNetworkTargets,
  exactNetworkTargetLooksValid,
  parseExactNetworkTargets,
} from "./network-scope-control";

function canExpand(status: RunDetailView["run"]["status"] | undefined): boolean {
  return status === "created" || status === "paused";
}

function readinessLabel(value: ProviderSearchReadinessView | undefined): string {
  if (!value) return "检查搜索…";
  if (value.state === "ready") return "搜索就绪";
  if (value.state === "network_disabled") return "搜索未联网";
  if (value.state === "missing_allowlist") return "搜索缺目标";
  if (value.state === "provider_unqualified") return "搜索待验证";
  return "搜索不可用";
}

function readinessDetail(value: ProviderSearchReadinessView | undefined): string {
  if (!value) return "正在核对当前 Run、Provider 与精确网络范围。";
  switch (value.reason) {
  case "run_network_disabled":
    return "当前 Run 没有为自托管搜索后端开放出站；供应商原生搜索与直接 URL 抓取分别授权。";
  case "search_endpoint_not_allowlisted":
    return value.required_target
      ? `搜索后端需要明确允许 ${value.required_target}；未列出的主机仍会被拒绝。`
      : "当前白名单没有覆盖搜索后端；请追加后端要求的精确 HTTPS 主机。";
  case "provider_native_qualification_required":
    return "Provider 声明支持原生搜索，但当前配置版本尚未观察到成功的搜索工具调用。";
  case "provider_native_qualification_failed":
    switch (value.detail_code) {
    case "transport_unavailable":
      return "供应商原生搜索在连接或超时边界内没有完成；普通对话仍可继续，可重试搜索或配置后备搜索源。";
    case "tool_unsupported":
      return "当前供应商端点不支持已知的 Responses 原生搜索工具类型；普通模型对话仍可继续。";
    case "provider_rejected":
      return "供应商拒绝了原生搜索请求；请检查模型、额度与供应商搜索策略。";
    case "response_invalid":
      return "供应商返回的原生搜索结果不符合可验证协议；该能力已停止发布。";
    default:
      return "原生搜索的有界验证未通过；普通模型对话仍可与搜索能力分开使用。";
    }
  case "no_active_run":
    return "当前没有可绑定搜索权限的 Run；下一次明确提交会创建 successor。";
  case "model_provider_unavailable":
    return "当前 Run 固定的模型供应商不可用，请先验证模型配置。";
  case "provider_search_policy_disabled":
    return "当前供应商明确关闭了搜索策略。";
  case "search_backend_not_configured":
    return "当前供应商没有可用的原生搜索或 SearXNG 后端。";
  case "provider_search_configuration_invalid":
    return "当前供应商的搜索声明与传输配置不一致。";
  default:
    return value.search_policy === "provider_native"
      ? `${value.provider || "当前 Provider"} 的托管搜索已就绪；它只访问供应商 API，不继承或扩大直接 URL 抓取权限。`
      : `${value.provider || "当前 Provider"} · ${value.search_policy || "已选择后端"} 已通过运行时检查。`;
  }
}

function remediationLabel(value: ProviderSearchReadinessView | undefined): string {
  switch (value?.remediation) {
  case "enable_network_allowlist": return "追加搜索后端主机后即可启用";
  case "add_required_target": return "把所需主机加入当前 Run 的白名单";
  case "qualify_provider_search": return "下一次搜索会由 Go 执行有界能力验证";
  case "submit_to_create_successor": return "发送下一条消息创建新的执行边界";
  case "configure_search_provider": return "到模型设置检查供应商与搜索后端";
  case "enable_provider_search": return "到模型设置启用搜索策略";
  case "repair_provider_configuration": return "修正 Provider 传输与搜索声明";
  default: return "Provider、网络范围与搜索后端均已就绪";
  }
}

export function V2RunNetworkAuthorityControl({ client, threadID = "", runID,
  variant = "settings" }: {
  client: CyberAgentClient;
  threadID?: string;
  runID: string;
  variant?: "menu" | "settings";
}) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const shellRef = useRef<HTMLDivElement>(null);
  const menuTriggerRef = useRef<HTMLButtonElement>(null);
  const confirmTriggerRef = useRef<HTMLButtonElement>(null);
  const query = useQuery({
    queryKey: browserCDPQueryKey(runID),
    queryFn: ({ signal }) => client.get<RunDetailView>(
      `/runs/${encodeURIComponent(runID)}`, {}, signal),
    enabled: Boolean(runID),
    staleTime: 5_000,
  });
  const readinessQuery = useQuery({
    queryKey: v2QueryKeys.searchReadiness(threadID),
    queryFn: ({ signal }) => client.providerSearchReadiness(threadID, signal),
    enabled: Boolean(threadID),
    staleTime: 5_000,
    // Qualification and a real hosted-search request can change the in-memory
    // readiness cache while a Turn is running. Polling this read-only local
    // projection prevents the Composer from continuing to show the stale
    // pre-flight "待验证" state after a successful probe or a failed request.
    refetchInterval: 5_000,
  });
  const current = query.data?.mode.scope.allowed_targets ?? [];
  const permissionMode = query.data?.execution_permission.mode;
  const publicHTTPS = permissionMode === "full_access" || permissionMode === "debug";
  const rawRequested = useMemo(() => parseExactNetworkTargets(draft), [draft]);
  const invalid = rawRequested.filter((target) => !exactNetworkTargetLooksValid(target));
  const requested = useMemo(() => invalid.length === 0
    ? canonicalizeExactNetworkTargets(rawRequested) : [], [invalid.length, rawRequested]);
  const additions = requested.filter((target) => !current.includes(target));
  const mutable = canExpand(query.data?.run.status);
  const mutation = useMutation({
    mutationFn: () => client.expandRunNetworkAuthority(runID, {
      version: "run_network_authority_control.v1",
      expected_mode_revision: query.data?.mode.revision ?? 0,
      add_allowed_targets: additions,
      reason: "v2 operator-confirmed exact HTTPS targets",
    }, `v2-run-network-authority-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      queryClient.setQueryData<RunDetailView>(browserCDPQueryKey(runID), (value) =>
        value ? { ...value, mode: result.mode } : value);
      if (threadID) {
        void queryClient.invalidateQueries({ queryKey: v2QueryKeys.searchReadiness(threadID) });
      }
      setDraft("");
      setConfirmOpen(false);
    },
  });
  useEffect(() => {
    if (!open) return;
    const closeOutside = (event: PointerEvent) => {
      if (!shellRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setOpen(false);
        requestAnimationFrame(() => menuTriggerRef.current?.focus());
      }
    };
    window.addEventListener("pointerdown", closeOutside);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOutside);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);
  const begin = () => {
    mutation.reset();
    if (mutable && additions.length > 0 && invalid.length === 0) setConfirmOpen(true);
  };
  const status = !runID ? "先打开一个任务。"
    : query.isLoading ? "正在读取当前执行的网页访问范围…"
      : query.isError ? "无法读取网页访问范围。"
        : publicHTTPS
          ? "当前权限允许匿名访问任意公网 HTTPS；私网、loopback、元数据地址、DNS 重绑定和非 HTTPS 请求仍会被拒绝。"
        : mutable ? "只追加明确的公网 HTTPS 主机；现有授权不可在这里静默扩大或删除。"
          : `当前执行为 ${query.data?.run.status ?? "unknown"}；需在 created/paused 静止边界追加。`;
  const readiness = readinessQuery.data;
  const canPrefillRequiredTarget = Boolean(readiness?.required_target && mutable &&
    !current.includes(readiness.required_target));

  const body = <>
    <header><span><Globe2 aria-hidden="true" size={18} /></span><div>
      <strong>直接 URL 抓取</strong><p>{status}</p></div>
      <span className="v2-network-count">{publicHTTPS ? "公网 HTTPS"
        : current.length === 0 ? "无网络" : `${current.length} 个主机`}</span>
    </header>
    {threadID && <div className={`v2-search-readiness state-${readiness?.state ?? "loading"}`}
      role="status"><span><strong>供应商搜索 · {readinessQuery.isError
        ? "无法检查" : readinessLabel(readiness)}</strong>
        <small>{readinessQuery.isError
          ? "搜索 readiness 接口暂不可用；网络白名单仍按下方事实显示。"
          : readinessDetail(readiness)}</small></span>
      {!readinessQuery.isError && <em>{remediationLabel(readiness)}</em>}</div>}
    {!publicHTTPS && current.length > 0 && <div aria-label="当前允许的 HTTPS 主机" className="v2-network-targets">
      {current.map((target) => <code key={target}>{target}</code>)}
    </div>}
    {!publicHTTPS && canPrefillRequiredTarget && <button className="v2-network-prefill" onClick={() =>
      setDraft(readiness?.required_target ?? "")} type="button">
      使用所需主机：<code>{readiness?.required_target}</code>
    </button>}
    {!publicHTTPS && <div className="v2-network-expander">
      <label><span>追加 HTTPS 主机</span><textarea aria-label="追加允许的 HTTPS 主机"
        disabled={!runID || query.isLoading || query.isError || !mutable || mutation.isPending}
        onChange={(event) => setDraft(event.target.value)}
        placeholder={"search.example.org\ndocs.example.com"} rows={3} spellCheck={false}
        value={draft} /></label>
      <button disabled={!runID || !mutable || additions.length === 0 || invalid.length > 0 ||
        mutation.isPending || !client.hasControl} onClick={begin} ref={confirmTriggerRef} type="button">
        {mutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={14} />
          : <Check aria-hidden="true" size={14} />}审核并追加
      </button>
    </div>}
    {!publicHTTPS && invalid.length > 0 && <p className="v2-network-error" role="alert">
      只接受无路径、查询或通配符的公网 HTTPS 主机：{invalid.join("、")}
    </p>}
    {!publicHTTPS && requested.length > 0 && additions.length === 0 && invalid.length === 0 &&
      <p className="v2-network-note"><ShieldCheck aria-hidden="true" size={14} />这些主机已在当前范围内。</p>}
    {!publicHTTPS && mutation.isError && <p className="v2-network-error" role="alert">
      {mutation.error instanceof Error ? mutation.error.message : "网页访问范围更新失败"}
    </p>}
  </>;

  return <div className={`v2-run-network-control is-${variant}`} ref={shellRef}>
    {variant === "menu" ? <>
      <button aria-expanded={open} aria-haspopup="dialog" aria-label="网页访问状态"
        className="v2-composer-chip" disabled={!runID} onClick={() => setOpen((value) => !value)}
        ref={menuTriggerRef} type="button">
        {publicHTTPS || current.length > 0 ? <Globe2 aria-hidden="true" size={14} />
          : <ShieldCheck aria-hidden="true" size={14} />}
        {readinessQuery.isError ? publicHTTPS ? "公网 HTTPS"
          : current.length === 0 ? "无网络" : `网页访问 · ${current.length}`
          : readinessLabel(readiness)}
        <ChevronDown aria-hidden="true" size={13} />
      </button>
      {open && <section aria-label="当前执行网页访问"
        className="v2-run-network-popover v2-run-network-authority"
        role="dialog">{body}</section>}
    </> : <section className="v2-run-network-authority">{body}</section>}
    <V2ConfirmDialog busy={mutation.isPending} confirmLabel="允许这些主机" danger
      description={`当前任务及其后续执行将能访问 ${additions.length} 个新增公网 HTTPS 主机。后端仍会拒绝私网、元数据地址、DNS 重绑定、未授权重定向和非 HTTPS 请求；网页内容始终作为不可信证据。`}
      onCancel={() => setConfirmOpen(false)} onConfirm={() => mutation.mutate()}
      open={confirmOpen} returnFocusRef={confirmTriggerRef} title="追加网页访问范围？" />
  </div>;
}
