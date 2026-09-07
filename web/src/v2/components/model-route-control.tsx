import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ChevronLeft, ChevronRight, CircleAlert,
  LoaderCircle, RotateCcw, Settings, Zap } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { AvailableModelRouteCollectionView, AvailableModelRouteView,
  ThreadModelRouteView } from "../../api/types";

export type V2AvailableModelRoute = AvailableModelRouteView;
export type V2ModelRouteCatalog = AvailableModelRouteCollectionView;
export type V2ThreadModelRoute = ThreadModelRouteView;

export type V2PendingModelRoute = { provider: string; model: string };

const knownProviderNames: Record<string, string> = {
  "official-anthropic": "Claude",
  "official-openai": "OpenAI",
  "official-deepseek": "DeepSeek",
  "official-google-gemini": "Gemini",
  "official-xai": "Grok",
  "official-minimax": "MiniMax",
  "official-mimo": "MiMo",
  "official-kimi": "Kimi",
  "official-kimi-coding": "Kimi for Coding",
  "opencode-go": "OpenCode Go",
  "github-copilot": "GitHub Copilot",
};

function unavailableReason(route: V2AvailableModelRoute): string {
  const reason = route.unavailable_reason.trim();
  const reasons: Record<string, string> = {
    provider_disabled: "供应商已停用",
    credential_not_configured: "API Key 尚未配置",
    invalid_configuration: "供应商配置无效，请检查端点和高级 JSON",
    provider_unavailable: "供应商当前不可用",
    harness_qualification_required: "Harness 尚未通过能力验证",
    not_configured: "尚未完成 Harness 能力验证",
    protocol_mismatch: "API 协议与 Harness 不兼容",
    auth_failed: "API Key 验证失败",
    network_failed: "无法连接模型供应商",
    rate_limit: "供应商正在限流，请稍后重试",
    capacity: "供应商当前容量不足",
    model_unsupported: "供应商不支持该模型",
    unavailable: "Harness 可用性尚未确认",
  };
  if (reason) return reasons[reason] ?? "当前路由不可用，请检查供应商配置";
  if (!route.enabled) return "供应商已停用";
  if (!["configured", "not_required"].includes(route.credential_status)) return "API Key 尚未配置";
  if (!route.harness_ready) return "Harness 尚未通过能力验证";
  if (!["available", "trusted_builtin", "verified"].includes(route.qualification_status)) {
    return reasons[route.qualification_status] ?? "当前模型尚未完成能力验证";
  }
  return "当前路由不可用";
}

function focusMenuItem(root: HTMLElement | null, position: "first" | "last") {
  const items = [...(root?.querySelectorAll<HTMLButtonElement>(
    '[role^="menuitem"]:not(:disabled)',
  ) ?? [])];
  const target = position === "first" ? items[0] : items.at(-1);
  target?.focus();
}

function moveMenuFocus(event: ReactKeyboardEvent<HTMLElement>) {
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  const menu = event.currentTarget;
  const items = [...menu.querySelectorAll<HTMLButtonElement>('[role^="menuitem"]:not(:disabled)')];
  if (!items.length) return;
  event.preventDefault();
  if (event.key === "Home") return items[0]?.focus();
  if (event.key === "End") return items.at(-1)?.focus();
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  const delta = event.key === "ArrowDown" ? 1 : -1;
  const next = current < 0 ? 0 : (current + delta + items.length) % items.length;
  items[next]?.focus();
}

export function V2ModelRouteControl({ client, threadID, pendingRoute, runActive = false,
  onManageModels, onPendingRouteChange }: {
  client: CyberAgentClient;
  threadID: string;
  pendingRoute?: V2PendingModelRoute | null;
  runActive?: boolean;
  onManageModels: () => void;
  onPendingRouteChange?: (route: V2PendingModelRoute | null) => void;
}) {
  const catalogAvailable = Boolean(client.hasModelControl);
  const routeControlAvailable = catalogAvailable && (threadID
    ? true
    : typeof onPendingRouteChange === "function");
  const queryClient = useQueryClient();
  const [level, setLevel] = useState<"closed" | "settings" | "models">("closed");
  const [nextRunNotice, setNextRunNotice] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const settingsMenuRef = useRef<HTMLDivElement>(null);
  const modelsMenuRef = useRef<HTMLDivElement>(null);
  const modelRowRef = useRef<HTMLButtonElement>(null);
  const routeKey = ["v2", "thread", threadID, "model-route"] as const;
  const catalogKey = ["v2", "models", "available-routes"] as const;

  const routeQuery = useQuery({
    queryKey: routeKey,
    queryFn: ({ signal }) => client.threadModelRoute(threadID, signal),
    enabled: routeControlAvailable && Boolean(threadID),
    staleTime: 15_000,
  });
  const catalogQuery = useQuery({
    queryKey: catalogKey,
    queryFn: ({ signal }) => client.availableModelRoutes(signal),
    enabled: routeControlAvailable && level !== "closed",
    staleTime: 30_000,
  });
  const mutation = useMutation({
    mutationFn: (intent: { action: "select"; route: V2AvailableModelRoute } |
      { action: "reset" }) => client.selectThreadModelRoute(threadID, intent.action === "reset"
      ? { version: "thread_model_route_control.v1", action: "reset",
        operation_key: globalThis.crypto.randomUUID(), requested_by: "desktop-ui" }
      : { version: "thread_model_route_control.v1", action: "select",
        provider: intent.route.provider_id, model: intent.route.model,
        operation_key: globalThis.crypto.randomUUID(), requested_by: "desktop-ui" }),
    onSuccess: (route) => {
      queryClient.setQueryData(routeKey, route);
      setNextRunNotice(runActive && route.applies_to === "next_run");
      setLevel("closed");
      requestAnimationFrame(() => triggerRef.current?.focus());
    },
  });

  useEffect(() => {
    if (!runActive) setNextRunNotice(false);
  }, [runActive]);

  useEffect(() => {
    if (level === "closed") return;
    const outside = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setLevel("closed");
    };
    const keyboard = (event: globalThis.KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setLevel("closed");
      requestAnimationFrame(() => triggerRef.current?.focus());
    };
    document.addEventListener("mousedown", outside);
    window.addEventListener("keydown", keyboard);
    return () => {
      document.removeEventListener("mousedown", outside);
      window.removeEventListener("keydown", keyboard);
    };
  }, [level]);

  useEffect(() => {
    if (level === "settings") {
      const frame = requestAnimationFrame(() => focusMenuItem(settingsMenuRef.current, "first"));
      return () => cancelAnimationFrame(frame);
    }
    if (level === "models") {
      const frame = requestAnimationFrame(() => focusMenuItem(modelsMenuRef.current, "first"));
      return () => cancelAnimationFrame(frame);
    }
  }, [level]);

  const groups = useMemo(() => {
    const grouped = new Map<string, { name: string; routes: V2AvailableModelRoute[] }>();
    for (const route of catalogQuery.data?.routes ?? []) {
      const existing = grouped.get(route.provider_id);
      if (existing) existing.routes.push(route);
      else grouped.set(route.provider_id, { name: route.provider_name || route.provider_id, routes: [route] });
    }
    return [...grouped.entries()].map(([id, value]) => ({ id, ...value }));
  }, [catalogQuery.data?.routes]);
  const current = threadID ? routeQuery.data : pendingRoute ? {
    protocol_version: "thread_model_route.v1" as const,
    thread_id: "",
    provider: pendingRoute.provider,
    model: pendingRoute.model,
    source: "composer_override",
    applies_to: "next_run" as const,
  } : undefined;
  const label = current?.model || (routeQuery.isLoading ? "正在读取模型" : "模型");
  const providerLabel = current ? catalogQuery.data?.routes.find((route) =>
    route.provider_id === current.provider)?.provider_name ||
    knownProviderNames[current.provider] || current.provider : "";
  const triggerLabel = providerLabel ? `${providerLabel} · ${label}` : label;
  const selectRoute = (route: V2AvailableModelRoute) => {
    if (!threadID) {
      onPendingRouteChange?.({ provider: route.provider_id, model: route.model });
      setLevel("closed");
      requestAnimationFrame(() => triggerRef.current?.focus());
      return;
    }
    mutation.mutate({ action: "select", route });
  };
  const resetRoute = () => {
    if (!threadID) {
      onPendingRouteChange?.(null);
      setLevel("closed");
      requestAnimationFrame(() => triggerRef.current?.focus());
      return;
    }
    mutation.mutate({ action: "reset" });
  };
  const closeForNavigation = () => {
    setLevel("closed");
    onManageModels();
  };

  return <div className="v2-model-route-control" ref={rootRef}>
    <button aria-expanded={level !== "closed"} aria-haspopup="menu"
      aria-label={`模型路由，当前 ${label}`} className="v2-model-route-trigger"
      disabled={!routeControlAvailable} onClick={() => setLevel((currentLevel) =>
        currentLevel === "closed" ? "settings" : "closed")} ref={triggerRef}
      title={routeControlAvailable ? `${providerLabel || "默认"} · ${label}` : "当前启动未开放模型路由控制"}
      type="button">
      {routeQuery.isFetching || mutation.isPending
        ? <LoaderCircle aria-hidden="true" className="spin" size={14} />
        : <Zap aria-hidden="true" size={14} />}
      <span>{triggerLabel}</span>{nextRunNotice && <em>下一轮</em>}
      <ChevronDown aria-hidden="true" size={14} />
    </button>

    {level === "settings" && <div aria-label="模型与响应设置"
      className="v2-model-route-popover v2-model-route-settings" onKeyDown={moveMenuFocus}
      ref={settingsMenuRef} role="menu">
      <button onClick={() => setLevel("models")} onKeyDown={(event) => {
        if (event.key === "ArrowRight") { event.preventDefault(); setLevel("models"); }
      }} ref={modelRowRef} role="menuitem" type="button">
        <span>模型</span><strong>{label}</strong><ChevronRight aria-hidden="true" size={15} />
      </button>
      <button disabled role="menuitem" title="当前供应商尚未声明 reasoning_effort" type="button">
        <span>推理强度</span><strong>随模型</strong><ChevronRight aria-hidden="true" size={15} />
      </button>
      <button disabled role="menuitem" title="速度由当前模型路由决定" type="button">
        <span>速度</span><strong>自动</strong><ChevronRight aria-hidden="true" size={15} />
      </button>
      <i aria-hidden="true" />
      <button disabled={mutation.isPending || !current || current.source === "default"}
        onClick={resetRoute} role="menuitem" type="button">
        <RotateCcw aria-hidden="true" size={15} /><span>重置为默认设置</span>
      </button>
      {nextRunNotice && <p className="v2-model-route-notice">当前 Run 保持不变，下一轮使用所选模型。</p>}
      {mutation.isError && <p className="v2-model-route-error" role="alert">
        <CircleAlert aria-hidden="true" size={14} />{mutation.error instanceof Error
          ? mutation.error.message : "模型路由更新失败"}</p>}
    </div>}

    {level === "models" && <div aria-label="选择模型路由"
      className="v2-model-route-popover v2-model-route-list" onKeyDown={(event) => {
        if (event.key === "ArrowLeft") {
          event.preventDefault();
          setLevel("settings");
          requestAnimationFrame(() => modelRowRef.current?.focus());
          return;
        }
        moveMenuFocus(event);
      }} ref={modelsMenuRef} role="menu">
      <header><button aria-label="返回模型设置" onClick={() => setLevel("settings")}
        role="menuitem" type="button"><ChevronLeft aria-hidden="true" size={16} /></button>
        <strong>模型</strong></header>
      <div className="v2-model-route-scroll">
        {catalogQuery.isLoading && <p className="v2-model-route-empty">
          <LoaderCircle aria-hidden="true" className="spin" size={15} />正在读取可用模型…</p>}
        {catalogQuery.isError && <p className="v2-model-route-error" role="alert">
          <CircleAlert aria-hidden="true" size={14} />模型列表暂时不可用</p>}
        {!catalogQuery.isLoading && !catalogQuery.isError && groups.length === 0 &&
          <p className="v2-model-route-empty">还没有可用模型。请先配置供应商和 API Key。</p>}
        {groups.map((group) => <section aria-label={group.name} key={group.id}>
          <h3>{group.name}</h3>
          {group.routes.map((route) => {
            const selected = current?.provider === route.provider_id && current.model === route.model;
            const reason = route.selectable ? "" : unavailableReason(route);
            return <button aria-checked={selected} disabled={!route.selectable || mutation.isPending}
              key={`${route.provider_id}\u0000${route.model}`}
              onClick={() => selectRoute(route)}
              role="menuitemradio" title={reason || `${group.name} · ${route.model}`} type="button">
              <span><strong>{route.model}</strong>{reason && <small>{reason}</small>}</span>
              {selected && <Check aria-hidden="true" size={16} />}
            </button>;
          })}
        </section>)}
      </div>
      <footer>{mutation.isError && <span className="v2-model-route-error" role="alert">
          <CircleAlert aria-hidden="true" size={14} />{mutation.error instanceof Error
            ? mutation.error.message : "模型路由更新失败"}</span>}
        <button onClick={closeForNavigation} role="menuitem" type="button">
        <Settings aria-hidden="true" size={15} /><span>{groups.length ? "管理模型供应商…" : "添加模型供应商"}</span>
      </button></footer>
    </div>}
  </div>;
}
