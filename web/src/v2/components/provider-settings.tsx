import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity, ArrowLeft, Check, ChevronRight, CircleAlert, KeyRound, LoaderCircle,
  Plus, Save, Server, ShieldCheck, Trash2,
} from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type {
  ModelHarnessQualificationView,
  ProviderDiagnosticView,
  ProviderDefinitionCollectionView,
  ProviderDefinitionMutationView,
  ProviderDefinitionView,
} from "../../api/types";
import { V2ConfirmDialog } from "./dialog";

const definitionQueryKey = ["v2", "provider-definitions"] as const;
const credentialQueryKey = ["v2", "provider-credentials"] as const;
const reservedProviderIDs = new Set([
  "anthropic", "deepseek", "mimo", "mock", "ollama", "openai",
]);

type ProviderDraft = {
  existing: ProviderDefinitionView | null;
  id: string;
  displayName: string;
  note: string;
  websiteURL: string;
  endpointURL: string;
  transport: "openai_chat_completions" | "openai_responses" | "anthropic_messages";
  models: string;
  defaultModel: string;
  enabled: boolean;
  searchMode: "disabled" | "auto" | "searxng" | "provider_native";
  nativeSearchDeclared: boolean;
  advancedJSON: string;
  apiKey: string;
  syncCredentialReference: boolean;
};

export type V2ProviderDraftPreset = {
  id: string;
  displayName: string;
  note: string;
  websiteURL: string;
  endpointURL: string;
  transport: ProviderDraft["transport"];
  models: readonly string[];
  defaultModel: string;
  searchMode: ProviderDraft["searchMode"];
  nativeSearchDeclared: boolean;
  advancedConfig: Record<string, unknown>;
};

type V2ProviderSettingsProps = {
  client: CyberAgentClient;
  initialPreset?: V2ProviderDraftPreset;
  onExit?: () => void;
  onSaved?: (definition: ProviderDefinitionView) => void;
};

type SecretMigration = {
  definition: ProviderDefinitionView;
  sanitizedJSON: string;
};

type SecretScan = {
  secret: string;
  sanitized: unknown;
  count: number;
};

function blankDraft(): ProviderDraft {
  return {
    existing: null,
    id: "",
    displayName: "",
    note: "",
    websiteURL: "",
    endpointURL: "",
    transport: "openai_chat_completions",
    models: "",
    defaultModel: "",
    enabled: true,
    searchMode: "auto",
    nativeSearchDeclared: false,
    advancedJSON: "{}",
    apiKey: "",
    syncCredentialReference: true,
  };
}

function draftFromDefinition(definition: ProviderDefinitionView): ProviderDraft {
  return {
    existing: definition,
    id: definition.id,
    displayName: definition.display_name,
    note: definition.note,
    websiteURL: definition.website_url,
    endpointURL: definition.endpoint_url,
    transport: definition.transport as ProviderDraft["transport"],
    models: definition.models.join("\n"),
    defaultModel: definition.default_model,
    enabled: definition.enabled,
    searchMode: definition.search_mode as ProviderDraft["searchMode"],
    nativeSearchDeclared: definition.native_web_search_capability === "declared_unverified",
    advancedJSON: JSON.stringify(definition.advanced_config, null, 2),
    apiKey: "",
    syncCredentialReference: true,
  };
}

function draftFromPreset(preset: V2ProviderDraftPreset): ProviderDraft {
  return {
    existing: null,
    id: preset.id,
    displayName: preset.displayName,
    note: preset.note,
    websiteURL: preset.websiteURL,
    endpointURL: preset.endpointURL,
    transport: preset.transport,
    models: preset.models.join("\n"),
    defaultModel: preset.defaultModel,
    enabled: true,
    searchMode: preset.searchMode,
    nativeSearchDeclared: preset.nativeSearchDeclared,
    advancedJSON: JSON.stringify(preset.advancedConfig, null, 2),
    apiKey: "",
    syncCredentialReference: true,
  };
}

function normalizeModels(value: string): string[] {
  return [...new Set(value.split(/[\n,]/u).map((model) => model.trim()).filter(Boolean))];
}

function validHTTPSURL(value: string, optional = false): boolean {
  if (optional && value === "") return true;
  try {
    const parsed = new URL(value);
    return parsed.protocol === "https:" && parsed.username === "" && parsed.password === "";
  } catch {
    return false;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function rejectDuplicateJSONKeys(source: string): void {
  let cursor = 0;
  const whitespace = () => {
    while (cursor < source.length && /\s/u.test(source[cursor])) cursor += 1;
  };
  const stringToken = (): string => {
    const start = cursor;
    if (source[cursor] !== '"') throw new Error("expected string");
    cursor += 1;
    while (cursor < source.length) {
      if (source[cursor] === "\\") {
        cursor += 2;
        continue;
      }
      if (source[cursor] === '"') {
        cursor += 1;
        return JSON.parse(source.slice(start, cursor)) as string;
      }
      cursor += 1;
    }
    throw new Error("unterminated string");
  };
  const value = (): void => {
    whitespace();
    if (source[cursor] === "{") {
      cursor += 1;
      whitespace();
      const keys = new Set<string>();
      if (source[cursor] === "}") {
        cursor += 1;
        return;
      }
      while (cursor < source.length) {
        whitespace();
        const key = stringToken();
        if (keys.has(key)) throw new Error(`高级 JSON 包含重复字段“${key}”。`);
        keys.add(key);
        whitespace();
        if (source[cursor] !== ":") throw new Error("expected colon");
        cursor += 1;
        value();
        whitespace();
        if (source[cursor] === "}") {
          cursor += 1;
          return;
        }
        if (source[cursor] !== ",") throw new Error("expected comma");
        cursor += 1;
      }
      throw new Error("unterminated object");
    }
    if (source[cursor] === "[") {
      cursor += 1;
      whitespace();
      if (source[cursor] === "]") {
        cursor += 1;
        return;
      }
      while (cursor < source.length) {
        value();
        whitespace();
        if (source[cursor] === "]") {
          cursor += 1;
          return;
        }
        if (source[cursor] !== ",") throw new Error("expected comma");
        cursor += 1;
      }
      throw new Error("unterminated array");
    }
    if (source[cursor] === '"') {
      stringToken();
      return;
    }
    const primitive = /(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/uy;
    primitive.lastIndex = cursor;
    const match = primitive.exec(source);
    if (!match) throw new Error("invalid value");
    cursor = primitive.lastIndex;
  };
  value();
  whitespace();
  if (cursor !== source.length) throw new Error("trailing data");
}

function parseAdvancedJSON(source: string): { value?: Record<string, unknown>; error?: string } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(source);
    rejectDuplicateJSONKeys(source);
  } catch (reason) {
    const message = reason instanceof Error && reason.message.startsWith("高级 JSON")
      ? reason.message : "高级 JSON 不是有效的 JSON。";
    return { error: message };
  }
  if (!isRecord(parsed)) return { error: "高级 JSON 的顶层必须是对象。" };
  return { value: parsed };
}

function sensitiveKey(key: string): boolean {
  if (key === "$credential") return false;
  return /(?:api[-_]?key|authorization|auth[-_]?token|access[-_]?token|password|secret|token|credential)/iu
    .test(key);
}

function secretValue(value: string): { secret: string; template?: string } | null {
  const trimmed = value.trim();
  if (trimmed.length < 8 || trimmed.includes("${") || trimmed.startsWith("$credential")) {
    return null;
  }
  const bearer = /^Bearer\s+(.+)$/iu.exec(trimmed);
  if (bearer?.[1] && bearer[1].length >= 8) {
    return { secret: bearer[1], template: "Bearer ${secret}" };
  }
  return { secret: trimmed };
}

function scanPlaintextSecrets(value: unknown, providerID: string): SecretScan | null {
  const candidates: Array<{ secret: string; template?: string }> = [];
  const collect = (current: unknown): void => {
    if (Array.isArray(current)) {
      current.forEach(collect);
      return;
    }
    if (!isRecord(current)) return;
    for (const [key, child] of Object.entries(current)) {
      if (sensitiveKey(key) && typeof child === "string") {
        const candidate = secretValue(child);
        if (candidate) candidates.push(candidate);
      } else {
        collect(child);
      }
    }
  };
  collect(value);
  if (candidates.length === 0) return null;
  const secrets = new Set(candidates.map((candidate) => candidate.secret));
  if (secrets.size !== 1) return { secret: "", sanitized: value, count: secrets.size };
  const secret = candidates[0].secret;
  const replace = (current: unknown): unknown => {
    if (Array.isArray(current)) return current.map(replace);
    if (!isRecord(current)) return current;
    return Object.fromEntries(Object.entries(current).map(([key, child]) => {
      if (!sensitiveKey(key) || typeof child !== "string") return [key, replace(child)];
      const candidate = secretValue(child);
      if (!candidate || candidate.secret !== secret) return [key, child];
      return [key, candidate.template
        ? { $credential: providerID, template: candidate.template }
        : { $credential: providerID }];
    }));
  };
  return { secret, sanitized: replace(value), count: 1 };
}

function hasOwnedCredentialReference(value: unknown, providerID: string): boolean {
  if (Array.isArray(value)) return value.some((child) => hasOwnedCredentialReference(child, providerID));
  if (!isRecord(value)) return false;
  if (value.$credential === providerID) return true;
  return Object.values(value).some((child) => hasOwnedCredentialReference(child, providerID));
}

function withSyncedCredentialReference(value: unknown, draft: ProviderDraft): {
  value?: Record<string, unknown>;
  error?: string;
} {
  if (!isRecord(value)) return { error: "高级 JSON 的顶层必须是对象。" };
  if (hasOwnedCredentialReference(value, draft.id.trim())) return { value };
  const currentHeaders = value.request_headers;
  if (currentHeaders !== undefined && !isRecord(currentHeaders)) {
    return { error: "要同步凭据引用，request_headers 必须是 JSON 对象。" };
  }
  const headerName = draft.transport === "anthropic_messages" ? "x-api-key" : "Authorization";
  const headers = { ...(currentHeaders ?? {}) } as Record<string, unknown>;
  const conflictingHeader = Object.keys(headers).find((key) => key.toLowerCase() === headerName.toLowerCase());
  if (conflictingHeader) {
    return { error: `高级 JSON 已定义 ${conflictingHeader}；请改为 $credential 引用，或关闭“同步凭据引用”。` };
  }
  headers[headerName] = draft.transport === "anthropic_messages"
    ? { $credential: draft.id.trim() }
    : { $credential: draft.id.trim(), template: "Bearer ${secret}" };
  return { value: { ...value, request_headers: headers } };
}

function definitionFromDraft(draft: ProviderDraft): { definition?: ProviderDefinitionView; error?: string } {
  const id = draft.id.trim();
  if (!/^[a-z][a-z0-9_-]{0,63}$/u.test(id) || reservedProviderIDs.has(id)) {
    return { error: "供应商 ID 需以小写字母开头，只能包含小写字母、数字、_ 或 -，且不能使用内置名称。" };
  }
  if (draft.displayName.trim() === "") return { error: "请填写供应商名称。" };
  if (!validHTTPSURL(draft.endpointURL.trim())) return { error: "请求地址必须是无内嵌凭据的 HTTPS URL。" };
  if (!validHTTPSURL(draft.websiteURL.trim(), true)) return { error: "官网链接必须是 HTTPS URL。" };
  const models = normalizeModels(draft.models);
  if (models.length === 0) return { error: "请至少填写一个模型名称。" };
  const defaultModel = draft.defaultModel.trim() || models[0];
  if (!models.includes(defaultModel)) return { error: "默认模型必须存在于模型列表中。" };
  if (draft.searchMode === "provider_native" && !draft.nativeSearchDeclared) {
    return { error: "选择原生搜索前，请明确声明该供应商提供此能力。此声明仍需后续资格验证。" };
  }
  const advanced = parseAdvancedJSON(draft.advancedJSON);
  if (!advanced.value) return { error: advanced.error };
  const advancedConfig = advanced.value;
  return { definition: {
    version: "provider_definition.v1",
    id,
    display_name: draft.displayName.trim(),
    note: draft.note.trim(),
    website_url: draft.websiteURL.trim(),
    endpoint_url: draft.endpointURL.trim(),
    default_model: defaultModel,
    models,
    transport: draft.transport,
    search_mode: draft.searchMode,
    native_web_search_capability: draft.nativeSearchDeclared
      ? "declared_unverified" : "unsupported",
    enabled: draft.enabled,
    revision: draft.existing?.revision ?? 0,
    advanced_config: advancedConfig,
  } };
}

function searchModeLabel(mode: string): string {
  return ({ disabled: "关闭", auto: "自动选择", searxng: "SearXNG", provider_native: "供应商原生" })[mode]
    ?? mode;
}

function definitionFingerprint(definition: ProviderDefinitionView): string {
  return JSON.stringify({
    id: definition.id,
    display_name: definition.display_name,
    note: definition.note,
    website_url: definition.website_url,
    endpoint_url: definition.endpoint_url,
    default_model: definition.default_model,
    models: definition.models,
    transport: definition.transport,
    search_mode: definition.search_mode,
    native_web_search_capability: definition.native_web_search_capability,
    enabled: definition.enabled,
    advanced_config: definition.advanced_config,
  });
}

function harnessStatusLabel(status: string): string {
  return ({ reachable: "连接正常", unreachable: "连接失败", qualified: "验证通过",
    incompatible: "协议不兼容", available: "可用", not_configured: "尚未配置",
    protocol_mismatch: "协议不匹配", auth_failed: "API Key 验证失败",
    network_failed: "网络连接失败", rate_limit: "供应商限流", capacity: "容量不足",
    model_unsupported: "模型不受支持" } as Record<string, string>)[status] ?? status;
}

function harnessFailureLabel(reason: string): string {
  return ({ none: "无", not_configured: "尚未配置", authentication: "API Key 验证失败",
    network: "网络连接失败", rate_limit: "供应商限流", capacity: "容量不足",
    model_not_found: "模型不存在", protocol_incompatible: "协议不兼容" } as Record<string, string>)[reason]
    ?? reason;
}

function timestampLabel(value: string): string {
  if (!value) return "未记录";
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf()) ? value : parsed.toLocaleString();
}

export function V2ProviderSettings({ client, initialPreset, onExit, onSaved }: V2ProviderSettingsProps) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<ProviderDraft | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [migration, setMigration] = useState<SecretMigration | null>(null);
  const [harnessConfirmOpen, setHarnessConfirmOpen] = useState(false);
  const [harnessBusy, setHarnessBusy] = useState(false);
  const [harnessError, setHarnessError] = useState("");
  const [diagnostic, setDiagnostic] = useState<ProviderDiagnosticView | null>(null);
  const [qualification, setQualification] = useState<ModelHarnessQualificationView | null>(null);
  const migrationSecretRef = useRef("");
  const addButtonRef = useRef<HTMLButtonElement>(null);
  const deleteButtonRef = useRef<HTMLButtonElement>(null);
  const saveButtonRef = useRef<HTMLButtonElement>(null);
  const harnessButtonRef = useRef<HTMLButtonElement>(null);
  const firstFieldRef = useRef<HTMLInputElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const initializedPresetIDRef = useRef<string | null>(null);

  const definitions = useQuery({
    queryKey: definitionQueryKey,
    queryFn: ({ signal }) => client.providerDefinitions(signal),
    enabled: Boolean(client.hasProviderDefinitions),
  });
  const credentials = useQuery({
    queryKey: credentialQueryKey,
    queryFn: ({ signal }) => client.providerCredentialStatuses(signal),
    enabled: Boolean(client.hasProviderCredentials),
  });

  const credentialByProvider = useMemo(() => new Map(
    (credentials.data?.items ?? []).map((status) => [status.provider, status]),
  ), [credentials.data?.items]);

  useEffect(() => {
    if (!initialPreset) {
      initializedPresetIDRef.current = null;
      return;
    }
    if (!definitions.data || initializedPresetIDRef.current === initialPreset.id) return;
    initializedPresetIDRef.current = initialPreset.id;
    const existing = definitions.data.providers.find((provider) => provider.id === initialPreset.id);
    setError("");
    setNotice("");
    setDraft(existing ? draftFromDefinition(existing) : draftFromPreset(initialPreset));
  }, [definitions.data, initialPreset]);

  useEffect(() => {
    if (!draft) return;
    const frame = requestAnimationFrame(() => firstFieldRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [draft?.existing?.id]);

  const openEditor = (next: ProviderDraft, trigger: HTMLElement) => {
    returnFocusRef.current = trigger;
    setError("");
    setNotice("");
    setHarnessError("");
    setDiagnostic(null);
    setQualification(null);
    setDraft(next);
  };
  const closeEditor = () => {
    const target = returnFocusRef.current;
    migrationSecretRef.current = "";
    setMigration(null);
    setDeleteOpen(false);
    setHarnessConfirmOpen(false);
    setDraft(null);
    setError("");
    if (initialPreset && onExit) {
      onExit();
      return;
    }
    requestAnimationFrame(() => target?.focus());
  };
  const update = <K extends keyof ProviderDraft>(key: K, value: ProviderDraft[K]) => {
    setDraft((current) => current ? { ...current, [key]: value } : current);
    setError("");
    setNotice("");
    setHarnessError("");
    setDiagnostic(null);
    setQualification(null);
  };

  const verifyHarness = async () => {
    if (!draft?.existing || harnessBusy) return;
    setHarnessConfirmOpen(false);
    setHarnessBusy(true);
    setHarnessError("");
    setDiagnostic(null);
    setQualification(null);
    const provider = draft.existing.id;
    const model = draft.existing.default_model;
    try {
      const diagnosticResult = await client.diagnoseProvider({
        version: "provider_diagnostic.v1", provider, model, confirm_diagnostic: true,
      });
      setDiagnostic(diagnosticResult);
      if (diagnosticResult.status !== "reachable" || diagnosticResult.failure_reason !== "none") {
        setHarnessError(`连接诊断未通过：${harnessFailureLabel(diagnosticResult.failure_reason)}。未继续执行 Harness 合成验证。`);
        return;
      }
      const qualificationResult = await client.qualifyModelHarness({
        version: "model_harness_qualification.v1", provider, model,
        confirm_qualification: true,
      });
      setQualification(qualificationResult);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["models", "availability"] }),
        queryClient.invalidateQueries({ queryKey: ["v2", "models", "available-routes"] }),
      ]);
    } catch (reason) {
      setHarnessError(reason instanceof Error ? reason.message : "Harness 验证失败。请检查供应商配置与网络权限。");
    } finally {
      setHarnessBusy(false);
    }
  };

  const persist = async (definition: ProviderDefinitionView, oneTimeSecret = "") => {
    if (!definitions.data) {
      setError("供应商定义尚未加载完成。");
      return;
    }
    if (oneTimeSecret && !client.hasProviderCredentials) {
      setError("系统凭据存储当前不可用，未保存任何明文密钥。");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const setCredential = () => client.changeProviderCredential(definition.id, {
        version: "provider_credential.v1" as const, action: "set" as const, confirm: true,
        secret: oneTimeSecret,
      });
      const upsert = () => client.upsertProviderDefinition(definition.id, {
        version: "provider_definition_control.v1",
        expected_collection_revision: definitions.data.revision, definition, confirm: true,
      });
      let result: ProviderDefinitionMutationView;
      if (definition.revision === 0) {
        // A first-time credential is not addressable until the definition joins the registry.
        // The definition is already sanitized, so persisting it before the transient secret is safe.
        result = await upsert();
        queryClient.setQueryData<ProviderDefinitionCollectionView>(definitionQueryKey,
          result.collection);
        if (oneTimeSecret) {
          try {
            await setCredential();
          } catch {
            const saved = result.definition ?? { ...definition, revision: 1 };
            setDraft(draftFromDefinition(saved));
            setError("供应商定义已保存，但 API Key 未写入系统凭据。请确认 Windows Credential Manager 可用，然后在此页重新输入密钥并再次保存；定义与高级 JSON 已保留，不会自动回滚。");
            await queryClient.invalidateQueries({ queryKey: credentialQueryKey });
            return;
          }
        }
      } else {
        if (oneTimeSecret) await setCredential();
        result = await upsert();
      }
      queryClient.setQueryData<ProviderDefinitionCollectionView>(definitionQueryKey,
        result.collection);
      await queryClient.invalidateQueries({ queryKey: credentialQueryKey });
      setNotice(`已保存 ${definition.display_name}`);
      setDraft(null);
      onSaved?.(result.definition ?? definition);
      if (onExit) {
        onExit();
        return;
      }
      requestAnimationFrame(() => returnFocusRef.current?.focus());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存供应商失败。");
    } finally {
      setBusy(false);
    }
  };

  const save = () => {
    if (!draft || busy) return;
    const parsed = definitionFromDraft(draft);
    if (!parsed.definition) {
      setError(parsed.error ?? "供应商配置无效。");
      return;
    }
    const scan = scanPlaintextSecrets(parsed.definition.advanced_config, parsed.definition.id);
    if (scan?.count && scan.count > 1) {
      setError("高级 JSON 中检测到多个不同的明文密钥。请分别写入系统凭据后，再用 $credential 引用替换。未保存配置。");
      return;
    }
    if (scan?.count === 1) {
      if (draft.apiKey && draft.apiKey !== scan.secret) {
        setError("API Key 输入框与高级 JSON 中的明文密钥不同。请只保留一个值，避免把错误凭据写入系统凭据库。");
        return;
      }
      migrationSecretRef.current = scan.secret;
      const sanitizedJSON = JSON.stringify(scan.sanitized, null, 2);
      setMigration({
        definition: { ...parsed.definition, advanced_config: scan.sanitized },
        sanitizedJSON,
      });
      return;
    }
    let definition = parsed.definition;
    if (draft.apiKey && draft.syncCredentialReference) {
      const synced = withSyncedCredentialReference(definition.advanced_config, draft);
      if (!synced.value) {
        setError(synced.error ?? "无法把凭据引用同步到高级 JSON。");
        return;
      }
      definition = { ...definition, advanced_config: synced.value };
    }
    const oneTimeSecret = draft.apiKey;
    if (oneTimeSecret) update("apiKey", "");
    void persist(definition, oneTimeSecret);
  };

  const confirmMigration = () => {
    if (!draft || !migration || busy) return;
    const secret = migrationSecretRef.current;
    migrationSecretRef.current = "";
    const sanitized = migration.sanitizedJSON;
    const definition = migration.definition;
    setDraft({ ...draft, advancedJSON: sanitized, apiKey: "" });
    setMigration(null);
    if (!secret) {
      setError("迁移密钥已失效，请重新输入。未保存配置。");
      return;
    }
    void persist(definition, secret);
  };

  const insertCredentialReference = () => {
    if (!draft) return;
    const providerID = draft.id.trim();
    if (!/^[a-z][a-z0-9_-]{0,63}$/u.test(providerID)) {
      setError("请先填写有效的供应商 ID，再插入凭据引用。");
      return;
    }
    try {
      const parsed = parseAdvancedJSON(draft.advancedJSON);
      if (!parsed.value) {
        setError(parsed.error ?? "请先修复高级 JSON，再插入凭据引用。");
        return;
      }
      const value = parsed.value;
      const synced = withSyncedCredentialReference(value, draft);
      if (!synced.value) {
        setError(synced.error ?? "无法插入凭据引用。");
        return;
      }
      update("advancedJSON", JSON.stringify(synced.value, null, 2));
      setNotice("已把当前供应商凭据引用插入 request_headers；运行时只解析引用，不会把密钥写入 JSON。");
    } catch {
      setError("请先修复高级 JSON，再插入凭据引用。");
    }
  };

  const removeCredential = async () => {
    if (!draft || !client.hasProviderCredentials || busy) return;
    setBusy(true);
    setError("");
    try {
      await client.changeProviderCredential(draft.id, {
        version: "provider_credential.v1", action: "delete", confirm: true, secret: "",
      });
      await queryClient.invalidateQueries({ queryKey: credentialQueryKey });
      setNotice("系统凭据已移除。供应商定义未改变。");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "移除系统凭据失败。");
    } finally {
      setBusy(false);
    }
  };

  const removeDefinition = async () => {
    if (!draft?.existing || !definitions.data || busy) return;
    setDeleteOpen(false);
    setBusy(true);
    setError("");
    try {
      if (configuredCredential) {
        if (!client.hasProviderCredentials) {
          throw new Error("该供应商仍有系统凭据，但当前会话没有凭据删除能力；供应商定义保持不变。");
        }
        const status = await client.changeProviderCredential(draft.id, {
          version: "provider_credential.v1", action: "delete", confirm: true, secret: "",
        });
        if (status.configured) {
          throw new Error("系统凭据未确认删除；供应商定义保持不变。");
        }
      }
      const result = await client.deleteProviderDefinition(draft.id, {
        version: "provider_definition_control.v1",
        expected_collection_revision: definitions.data.revision,
        expected_definition_revision: draft.existing.revision,
        confirm: true,
      });
      queryClient.setQueryData<ProviderDefinitionCollectionView>(definitionQueryKey,
        result.collection);
      await queryClient.invalidateQueries({ queryKey: credentialQueryKey });
      setDraft(null);
      if (initialPreset && onExit) {
        onExit();
        return;
      }
      requestAnimationFrame(() => addButtonRef.current?.focus());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除供应商失败。");
    } finally {
      setBusy(false);
    }
  };

  if (!client.hasProviderDefinitions) {
    return <><h1>自定义配置</h1><p className="v2-settings-lead">当前桌面后端未启用供应商定义控制。</p>
      <section className="v2-settings-card v2-provider-empty"><Server aria-hidden="true" size={24} />
        <strong>自定义供应商不可用</strong><p>需要控制令牌与模型控制能力，页面不会把密钥降级写入本地 JSON。</p>
      </section></>;
  }

  if (initialPreset && !draft && initializedPresetIDRef.current !== initialPreset.id) {
    return <><div className="v2-provider-editor-heading">
      {onExit && <button aria-label="返回模型目录" onClick={onExit} type="button">
        <ArrowLeft aria-hidden="true" size={17} /></button>}
      <div><h1>连接 {initialPreset.displayName}</h1>
      <p>正在读取已有供应商定义与系统凭据状态。</p></div></div>
      {definitions.isError
        ? <section className="v2-settings-card v2-provider-empty" role="alert">
          <CircleAlert aria-hidden="true" size={22} /><strong>无法读取供应商</strong>
          <p>{definitions.error instanceof Error ? definitions.error.message : "请求失败"}</p>
          <button onClick={() => void definitions.refetch()} type="button">重试</button></section>
        : <section className="v2-settings-card v2-provider-empty"><p>正在读取供应商…</p></section>}
    </>;
  }

  if (!draft) {
    const providers = definitions.data?.providers ?? [];
    return <><div className="v2-provider-heading"><div><h1>自定义配置</h1>
      <p>在模型模块中连接兼容供应商；定义与系统凭据相互分离。</p></div>
      <button className="primary" disabled={definitions.isLoading || definitions.isError}
        onClick={(event) => openEditor(blankDraft(), event.currentTarget)} ref={addButtonRef} type="button">
        <Plus aria-hidden="true" size={15} />添加供应商</button></div>
      {notice && <p aria-live="polite" className="v2-provider-notice"><Check aria-hidden="true" size={15} />{notice}</p>}
      {definitions.isLoading && <div className="v2-settings-card v2-provider-empty"><p>正在读取供应商…</p></div>}
      {definitions.isError && <div className="v2-settings-card v2-provider-empty" role="alert">
        <CircleAlert aria-hidden="true" size={22} /><strong>无法读取供应商</strong>
        <p>{definitions.error instanceof Error ? definitions.error.message : "请求失败"}</p>
        <button onClick={() => void definitions.refetch()} type="button">重试</button></div>}
      {!definitions.isLoading && !definitions.isError && <section aria-label="自定义供应商" className="v2-provider-list">
        {providers.length === 0 && <div className="v2-settings-card v2-provider-empty"><Server aria-hidden="true" size={24} />
          <strong>还没有自定义供应商</strong><p>添加兼容端点、模型与搜索策略。API Key 只保存到系统凭据库。</p></div>}
        {providers.map((provider) => {
          const credential = credentialByProvider.get(provider.id);
          return <button className="v2-provider-row" key={provider.id}
            onClick={(event) => openEditor(draftFromDefinition(provider), event.currentTarget)} type="button">
            <span className="v2-provider-mark"><Server aria-hidden="true" size={18} /></span>
            <span className="v2-provider-row-copy"><strong>{provider.display_name}</strong>
              <small>{provider.id} · {provider.default_model}</small></span>
            <span className="v2-provider-tags"><i className={provider.enabled ? "is-ready" : ""}>
              {provider.enabled ? "已启用" : "已停用"}</i>
              <i>{credential?.configured ? "密钥已存储" : "无系统密钥"}</i>
              <i>{searchModeLabel(provider.search_mode)}</i></span>
            <ChevronRight aria-hidden="true" size={16} /></button>;
        })}
      </section>}
    </>;
  }

  const configuredCredential = credentialByProvider.get(draft.id)?.configured ?? false;
  const modelOptions = normalizeModels(draft.models);
  const parsedDraftDefinition = definitionFromDraft(draft).definition;
  const savedConfiguration = Boolean(draft.existing && parsedDraftDefinition && !draft.apiKey &&
    definitionFingerprint(parsedDraftDefinition) === definitionFingerprint(draft.existing));
  const harnessControlAvailable = client.hasModelControl &&
    typeof client.diagnoseProvider === "function" && typeof client.qualifyModelHarness === "function";
  const harnessBlocker = !draft.existing ? "先保存供应商配置后才能验证。"
    : !savedConfiguration ? "当前更改尚未保存；请先保存，再验证已持久化的精确配置。"
      : !configuredCredential ? "请先把 API Key 保存到系统凭据管理器。"
        : !harnessControlAvailable ? "当前桌面启动未开放模型诊断与 Harness 验证。"
          : !draft.enabled ? "供应商已停用；启用并保存后才能验证。" : "";
  return <><div className="v2-provider-editor-heading">
    <button aria-label={initialPreset && onExit ? "返回模型目录" : "返回供应商列表"}
      disabled={busy} onClick={closeEditor} type="button">
      <ArrowLeft aria-hidden="true" size={17} /></button>
    <div><h1>{draft.existing ? "编辑供应商" : "添加供应商"}</h1>
      <p>高级 JSON 可自由编辑；受保护字段与明文密钥会在保存边界被拒绝。</p></div></div>
    <form className="v2-provider-form" onSubmit={(event) => { event.preventDefault(); save(); }}>
      <section className="v2-settings-card v2-provider-fields" aria-labelledby="provider-basics-title">
        <header><div><h2 id="provider-basics-title">连接</h2><p>请求只发送到你明确填写的 HTTPS 端点。</p></div>
          <label className="v2-provider-enabled"><input checked={draft.enabled}
            onChange={(event) => update("enabled", event.target.checked)} type="checkbox" />
            <span aria-hidden="true"><i /></span>启用</label></header>
        <div className="v2-provider-grid">
          <label>供应商 ID<input autoComplete="off" disabled={Boolean(draft.existing)}
            maxLength={64} onChange={(event) => update("id", event.target.value)}
            placeholder="例如 acme-ai" ref={firstFieldRef} required spellCheck={false}
            value={draft.id} /></label>
          <label>显示名称<input autoComplete="organization" maxLength={128}
            onChange={(event) => update("displayName", event.target.value)}
            placeholder="例如 Acme AI" ref={draft.existing ? firstFieldRef : undefined}
            required value={draft.displayName} /></label>
          <label className="is-wide">备注<input maxLength={2048}
            onChange={(event) => update("note", event.target.value)}
            placeholder="可选，例如团队账号" value={draft.note} /></label>
          <label className="is-wide">官网链接<input inputMode="url"
            onChange={(event) => update("websiteURL", event.target.value)}
            placeholder="https://example.com（可选）" type="url" value={draft.websiteURL} /></label>
          <label className="is-wide">请求地址<input inputMode="url"
            onChange={(event) => update("endpointURL", event.target.value)}
            placeholder="https://api.example.com/v1/chat/completions" required type="url"
            value={draft.endpointURL} /></label>
          <label>协议<select onChange={(event) => {
            const transport = event.target.value as ProviderDraft["transport"];
            setDraft((current) => current ? {
              ...current,
              transport,
              // Responses-compatible routes get the Provider-hosted search
              // adapter by default. This remains a declaration, not trust: the
              // first real search still has to pass Go's bounded qualification.
              ...(transport === "openai_responses" ? {
                searchMode: "provider_native" as const,
                nativeSearchDeclared: true,
              } : {
                searchMode: current.searchMode === "provider_native" ? "auto" : current.searchMode,
                nativeSearchDeclared: false,
              }),
            } : current);
            setError("");
            setNotice("");
            setHarnessError("");
            setDiagnostic(null);
            setQualification(null);
          }} value={draft.transport}>
            <option value="openai_chat_completions">OpenAI Chat Completions</option>
            <option value="openai_responses">OpenAI Responses</option>
            <option value="anthropic_messages">Anthropic Messages</option>
          </select></label>
          <label>默认模型<select disabled={modelOptions.length === 0}
            onChange={(event) => update("defaultModel", event.target.value)}
            value={modelOptions.includes(draft.defaultModel) ? draft.defaultModel : ""}>
            <option value="">{modelOptions.length ? "选择默认模型" : "先填写模型列表"}</option>
            {modelOptions.map((model) => <option key={model} value={model}>{model}</option>)}</select></label>
          <label className="is-wide">模型列表<textarea aria-describedby="provider-models-help"
            aria-label="模型列表"
            onChange={(event) => update("models", event.target.value)}
            placeholder={"model-a\nmodel-b"} rows={3} spellCheck={false} value={draft.models} />
            <small id="provider-models-help">每行或逗号分隔；名称会原样传给供应商。</small></label>
        </div>
      </section>

      <section className="v2-settings-card v2-provider-fields" aria-labelledby="provider-search-title">
        <header><div><h2 id="provider-search-title">网页搜索</h2>
          <p>选择 Responses API 时会默认注册供应商原生搜索；首次真实搜索由 Go 做有界资格验证，不支持时会明确降级为不可用。</p></div></header>
        <div className="v2-provider-grid">
          <label className="is-wide">搜索策略<select aria-label="搜索策略" onChange={(event) => {
            const mode = event.target.value as ProviderDraft["searchMode"];
            setDraft((current) => current ? { ...current, searchMode: mode,
              nativeSearchDeclared: mode === "provider_native" || current.nativeSearchDeclared } : current);
            setError("");
            setNotice("");
            setHarnessError("");
            setDiagnostic(null);
            setQualification(null);
          }} value={draft.searchMode}>
            <option value="disabled">关闭</option><option value="auto">自动选择</option>
            <option value="searxng">SearXNG</option><option value="provider_native">供应商原生</option>
          </select><small>{draft.searchMode === "searxng" || draft.searchMode === "auto"
            ? "SearXNG endpoint 当前由 Desktop 启动配置提供；本页只选择策略，不会自动扩大 Run 网络范围。"
            : "选择供应商原生时会绑定当前模型、端点、定义 revision 与系统凭据代际。"}</small></label>
          <label className="v2-provider-check is-wide"><input aria-label="声明供应商具备原生 Web Search"
            checked={draft.nativeSearchDeclared}
            onChange={(event) => update("nativeSearchDeclared", event.target.checked)} type="checkbox" />
            <span><strong>声明供应商具备原生 Web Search</strong>
              <small>该声明本身不扩大网络权限。供应商原生模式会在当前 Run 已精确授权端点后立即注册受控搜索工具，首次真实搜索再完成有界验证，可能产生一次供应商 API 调用费用。</small></span></label>
        </div>
      </section>

      <section className="v2-settings-card v2-provider-fields" aria-labelledby="provider-credential-title">
        <header><div><h2 id="provider-credential-title">系统凭据</h2>
          <p>密钥只写入操作系统凭据管理器，读取接口永不返回明文。</p></div>
          <span className={configuredCredential ? "v2-provider-status is-ready" : "v2-provider-status"}>
            {configuredCredential ? "已存储" : "未存储"}</span></header>
        <div className="v2-provider-credential-row"><label>API Key
          <input aria-describedby="provider-key-help" autoComplete="new-password"
            disabled={!client.hasProviderCredentials} onChange={(event) => update("apiKey", event.target.value)}
            placeholder={configuredCredential ? "留空以保留现有密钥" : "一次性输入；保存后不会再次显示"}
            type="password" value={draft.apiKey} /></label>
          {configuredCredential && <button className="secondary" disabled={busy || !client.hasProviderCredentials}
            onClick={() => void removeCredential()} type="button"><KeyRound aria-hidden="true" size={14} />移除密钥</button>}</div>
        <label className="v2-provider-check v2-provider-sync"><input checked={draft.syncCredentialReference}
          onChange={(event) => update("syncCredentialReference", event.target.checked)} type="checkbox" />
          <span><strong>把凭据引用同步到高级 JSON</strong>
            <small>输入新 API Key 时，默认写入对应协议的 request_headers。这里只保存 $credential 引用；你仍可自由移动、改模板或关闭同步。</small></span></label>
        <p id="provider-key-help" className="v2-provider-help">保存后输入框会立即清空。高级 JSON 应使用 <code>$credential</code> 引用。</p>
      </section>

      <section className="v2-settings-card v2-provider-fields v2-provider-harness"
        aria-labelledby="provider-harness-title">
        <header><div><h2 id="provider-harness-title">连接与 Harness 验证</h2>
          <p>先做一次最小连接诊断；通过后再验证工具调用、结果回传与结构化 JSON 合同。</p></div>
          <button className="secondary" disabled={Boolean(harnessBlocker) || busy || harnessBusy}
            onClick={() => setHarnessConfirmOpen(true)} ref={harnessButtonRef} type="button">
            {harnessBusy ? <LoaderCircle aria-hidden="true" className="spin" size={14} />
              : <ShieldCheck aria-hidden="true" size={14} />}
            {harnessBusy ? "正在验证…" : "测试并验证 Harness"}</button></header>
        {harnessBlocker && <p className="v2-provider-help">{harnessBlocker}</p>}
        {!harnessBlocker && !diagnostic && !qualification && !harnessBusy &&
          <p className="v2-provider-help">验证绑定当前供应商、默认模型、端点、定义 revision 与系统凭据代际；任一项变化后需重新验证。</p>}
        {harnessBusy && <p aria-live="polite" className="v2-provider-harness-progress">
          <Activity aria-hidden="true" size={14} />正在执行有界供应商调用，请勿关闭应用…</p>}
        {diagnostic && <article aria-label="连接诊断结果" className="v2-provider-harness-result" role="status">
          <div><strong>连接诊断</strong><span className={diagnostic.status === "reachable" ? "is-ready" : "is-error"}>
            {harnessStatusLabel(diagnostic.status)}</span></div>
          <dl><div><dt>模型</dt><dd>{diagnostic.provider} / {diagnostic.model}</dd></div>
            <div><dt>结果</dt><dd>{diagnostic.failure_reason === "none"
              ? harnessStatusLabel(diagnostic.qualification_status) : harnessFailureLabel(diagnostic.failure_reason)}</dd></div>
            <div><dt>耗时</dt><dd>{diagnostic.duration_ms} ms</dd></div></dl>
        </article>}
        {qualification && <article aria-label="Harness 验证结果" className="v2-provider-harness-result" role="status">
          <div><strong>Harness 验证</strong><span className={qualification.status === "qualified" ? "is-ready" : "is-error"}>
            {harnessStatusLabel(qualification.status)}</span></div>
          <dl><div><dt>资格状态</dt><dd>{harnessStatusLabel(qualification.qualification_status)}</dd></div>
            <div><dt>模型调用</dt><dd>{qualification.model_calls} 次</dd></div>
            <div><dt>验证时间</dt><dd>{timestampLabel(qualification.harness.qualified_at)}</dd></div>
            <div><dt>有效期至</dt><dd>{timestampLabel(qualification.harness.expires_at)}</dd></div></dl>
        </article>}
        {harnessError && <p className="v2-inline-error v2-provider-harness-error" role="alert">
          <CircleAlert aria-hidden="true" size={15} />{harnessError}</p>}
      </section>

      <section className="v2-settings-card v2-provider-fields" aria-labelledby="provider-json-title">
        <header><div><h2 id="provider-json-title">高级 JSON</h2>
          <p>完整可编辑；HTTP 运行时解释 request_headers、request_body 与 model_mapping，其余扩展原样保留，但不能覆盖 Harness 核心字段。</p></div>
          <button className="secondary" onClick={insertCredentialReference} type="button">
            <KeyRound aria-hidden="true" size={14} />插入凭据引用</button></header>
        <label className="v2-provider-json">
          <textarea aria-label="高级 JSON" onChange={(event) => update("advancedJSON", event.target.value)}
            spellCheck={false} value={draft.advancedJSON} /></label>
        <p className="v2-provider-help">示例：<code>{'{"request_headers":{"Authorization":{"$credential":"' + (draft.id || "provider-id") + '","template":"Bearer ${secret}"}}}'}</code>。如粘贴一个明文密钥，保存时会先要求二次确认并迁移。</p>
      </section>

      {error && <p className="v2-inline-error v2-provider-error" role="alert">
        <CircleAlert aria-hidden="true" size={16} />{error}</p>}
      {notice && <p aria-live="polite" className="v2-provider-notice"><Check aria-hidden="true" size={15} />{notice}</p>}
      <footer className="v2-provider-actions">
        {draft.existing && <button className="danger-quiet" disabled={busy}
          onClick={() => setDeleteOpen(true)} ref={deleteButtonRef} type="button">
          <Trash2 aria-hidden="true" size={15} />删除供应商</button>}
        <span />
        <button disabled={busy} onClick={closeEditor} type="button">取消</button>
        <button className="primary" disabled={busy} ref={saveButtonRef} type="submit">
          <Save aria-hidden="true" size={15} />{busy ? "正在保存…" : "保存"}</button>
      </footer>
    </form>

    <V2ConfirmDialog busy={busy} confirmLabel="删除" danger
      description={configuredCredential
        ? "系统凭据会先从操作系统凭据管理器删除，确认清除后才会删除供应商定义。若凭据删除失败或当前路由仍在使用该定义，操作会停止且不会导出明文。"
        : "供应商定义将从可用模型路由中移除。若当前路由仍在使用它，后端会拒绝删除；系统凭据不会被明文导出。"}
      onCancel={() => setDeleteOpen(false)} onConfirm={() => void removeDefinition()}
      open={deleteOpen} returnFocusRef={deleteButtonRef} title={`删除 ${draft.displayName || draft.id}`} />
    <V2ConfirmDialog busy={busy} confirmLabel="迁移并保存" danger
      description="高级 JSON 中检测到一个明文密钥。确认后，它会写入操作系统凭据管理器，JSON 中的所有对应位置会替换为当前供应商的 $credential 引用；页面不会显示或持久化原值。"
      onCancel={() => { migrationSecretRef.current = ""; setMigration(null); }}
      onConfirm={confirmMigration} open={Boolean(migration)} returnFocusRef={saveButtonRef}
      title="迁移明文密钥？" />
    <V2ConfirmDialog busy={harnessBusy} confirmLabel="开始验证"
      description={`将对 ${draft.displayName || draft.id} 的默认模型 ${draft.defaultModel || "未选择"} 发起一次最小连接诊断；诊断通过后会继续执行 Harness 合成验证，当前协议通常还需要两次模型调用。供应商可能按这些调用计费，且请求会发送到已保存的端点。`}
      onCancel={() => setHarnessConfirmOpen(false)} onConfirm={() => void verifyHarness()}
      open={harnessConfirmOpen} returnFocusRef={harnessButtonRef}
      title="测试并验证 Harness？" />
  </>;
}
