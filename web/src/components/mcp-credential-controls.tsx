import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { APIClient } from "../api/client";
import type { ExtensionMCPServerView, MCPCredentialBindingView } from "../api/types";
import { validMCPBearerSecret } from "../api/mcp-credentials";
import { useLocale } from "../lib/locale";

const clients = new WeakMap<APIClient, number>();
let nextClient = 0;
function clientIdentity(client: APIClient) {
  let identity = clients.get(client);
  if (identity === undefined) { identity = ++nextClient; clients.set(client, identity); }
  return identity;
}

// Changing connection, descriptor or scope remounts all transient input and
// mutations. An old completion can only update its own metadata query key.
export function MCPCredentialControls({ client, server, capability }: {
  client: APIClient; server: ExtensionMCPServerView; capability?: boolean;
}) {
  const { t } = useLocale();
  if (server.native_source || server.transport !== "streamable_http") return <p>{t(
    "此传输不支持软件内的 MCP Bearer 凭据管理。", "This transport does not support in-app MCP bearer credentials.")}</p>;
  if (!server.credential_ref) return <p>{t(
    "描述符未指定凭据。需要认证时，请使用专用凭据名称登记服务器，再在此输入令牌。",
    "No credential is referenced. For authentication, register the server with a dedicated credential name, then enter its token here.")}</p>;
  if (!capability) return <p role="status">{t("当前服务未开放 MCP 凭据管理。", "MCP credential management is unavailable in this service.")}</p>;
  return <BoundMCPCredentialControls key={`${clientIdentity(client)}/${server.id}/${server.descriptor_fingerprint}/${server.workspace_id}/${server.run_id ?? ""}/${server.target}/${server.credential_ref}`}
    client={client} server={server} />;
}

function BoundMCPCredentialControls({ client, server }: { client: APIClient; server: ExtensionMCPServerView }) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [secretValid, setSecretValid] = useState(false);
  const [confirmed, setConfirmed] = useState("");
  const binding: MCPCredentialBindingView = { server_id: server.id, workspace_id: server.workspace_id,
    ...(server.run_id ? { run_id: server.run_id } : {}), target: server.target, credential_ref: server.credential_ref!,
    expected_descriptor_fingerprint: server.descriptor_fingerprint };
  const queryKey = ["mcp-credential", clientIdentity(client), server.id, server.descriptor_fingerprint];
  const status = useQuery({ queryKey, queryFn: ({ signal }) => client.mcpCredentialStatus(binding, signal), retry: false });
  const change = useMutation({ mutationFn: (action: "set" | "delete") => {
    if (!status.data || confirmed !== status.data.reference_fingerprint) throw new Error(t("请刷新凭据状态并明确确认。", "Refresh credential presence and confirm the action."));
    const secret = action === "set" ? input.current?.value ?? "" : "";
    if (input.current) input.current.value = "";
    setSecretValid(false);
    setConfirmed("");
    // Mutation variables retain only the action. Plaintext is request-local,
    // never React state, query data, browser storage, drafts or error messages.
    return client.changeMCPCredential({ version: "mcp-credential.v1", binding, action, confirm: true,
      expected_reference_fingerprint: status.data.reference_fingerprint, ...(action === "set" ? { secret } : {}) });
  }, retry: false, onSuccess: (data) => queryClient.setQueryData(queryKey, data),
    onError: () => { void queryClient.invalidateQueries({ queryKey }); } });
  const canChange = client.hasExtensionControl && !!status.data?.store_available && !status.data.endpoint_conflict &&
    !status.isFetching && !change.isPending && !status.error;
  const error = status.error || change.error;
  return <details className="extension-onboarding"><summary>{t("管理 MCP 认证令牌", "Manage MCP authentication token")}</summary>
    <p>{t("这里只管理系统凭据库中的 Bearer 令牌。已保存不代表服务器认证、发现或工具调用成功。",
      "Manage the bearer token in the system credential store. Stored presence does not prove authentication, discovery or a successful tool call.")}</p>
    <p>{t("凭据名称", "Credential name")}: {server.credential_ref} · {server.target}</p>
    <p role="status">{status.isPending ? t("正在读取凭据状态…", "Reading credential presence…") : status.error || !status.data ?
      t("凭据状态未知，请刷新。", "Credential presence is unknown; refresh.") : !status.data.store_available ?
      t("系统凭据库不可用，无法读取或保存令牌。", "The system credential store is unavailable; token presence and storage are unavailable.") :
      status.data.configured ? t("本地令牌已保存", "Local token stored") : t("本地尚未保存令牌", "No local token stored")}</p>
    {status.data && <p>{t("系统凭据库", "System store")}: {status.data.store_kind} ·
      {t("引用此名称的 MCP 登记数", "MCP registrations using this name")}: {status.data.registration_count}</p>}
    {status.data && status.data.registration_count > 1 && <p className="inline-warning">{t(
      "此凭据名称由多个 MCP 登记共享。更新或删除会影响这些登记。", "Multiple MCP registrations share this credential. Updating or removing it affects those registrations.")}</p>}
    {status.data?.endpoint_conflict && <p className="inline-warning">{t(
      "此名称同时绑定不同地址，不能安全更改。请为不同服务器登记独立的凭据名称。",
      "This name binds different endpoints and cannot be safely changed. Register a separate credential name for each server endpoint.")}</p>}
    {!client.hasExtensionControl && <p>{t("当前连接为只读，不能保存或删除凭据。", "This connection is read-only; credentials cannot be saved or removed.")}</p>}
    <button className="settings-action" type="button" disabled={status.isFetching || change.isPending} onClick={() => {
      setConfirmed(""); change.reset(); void status.refetch();
    }}>{t("刷新凭据状态", "Refresh credential presence")}</button>
    <fieldset disabled={!canChange} className="extension-form">
      <label>{t("Bearer 令牌（8–2560 字节，无空白）", "Bearer token (8–2560 bytes, no whitespace)")}
        <input ref={input} type="password" autoComplete="off" maxLength={2560} spellCheck={false} onChange={(event) => {
          setSecretValid(validMCPBearerSecret(event.currentTarget.value)); change.reset();
        }} /></label>
      <label><input type="checkbox" checked={!!status.data && confirmed === status.data.reference_fingerprint}
        onChange={(event) => setConfirmed(event.target.checked ? status.data!.reference_fingerprint : "")} />{t(
        "我确认更改此系统凭据名称及其全部引用；此名称也可能用于其他集成。",
        "I confirm changing this system credential name and all its references; other integrations may also use this name.")}</label>
      <div className="extension-actions">
        <button className="settings-action" type="button" disabled={!status.data || confirmed !== status.data.reference_fingerprint || !secretValid} onClick={() => change.mutate("set")}>
          {status.data?.configured ? t("更新本地令牌", "Update local token") : t("保存本地令牌", "Save local token")}</button>
        <button className="settings-action danger" type="button" disabled={!status.data || confirmed !== status.data.reference_fingerprint || !status.data.configured} onClick={() => change.mutate("delete")}>
          {t("删除本地令牌", "Remove local token")}</button>
      </div>
    </fieldset>
    <p>{t("提交后输入框会清空。删除只移除本地令牌，不撤销远端令牌，也不撤销服务器的审查状态。",
      "The input clears on submission. Removal deletes only the local token; it does not revoke the remote token or the server review.")}</p>
    {change.isSuccess && <p role="status">{change.data.configured ? t("令牌已保存，并回读确认存在。", "Token saved and local presence verified.") :
      t("本地令牌已删除，并回读确认不存在。", "Local token removed and absence verified.")}</p>}
    {error && <p className="inline-warning" role="alert">{error instanceof Error ? error.message : t("凭据操作失败，请刷新状态。", "Credential action failed; refresh presence.")}</p>}
  </details>;
}
