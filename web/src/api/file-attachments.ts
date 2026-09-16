import type { components } from "./schema";
/** Uploaded files are immutable attachments, distinct from project path references. */
export type WorkspaceFileAttachment = components["schemas"]["WorkspaceFileAttachment"];
export const maximumFileBytes = 5 * 1024 * 1024;
export const maximumFileAttachments = 4;
const identity = (value: unknown): value is string => typeof value === "string" && /^[\w.-]{1,256}$/u.test(value);
const digest = (value: unknown): value is string => typeof value === "string" && /^[a-f0-9]{64}$/u.test(value);
export function validFileAttachment(value: unknown): value is WorkspaceFileAttachment {
  if (!value || typeof value !== "object") return false;
  const file = value as WorkspaceFileAttachment;
  return identity(file.id) && identity(file.workspace_id) && digest(file.sha256) &&
    typeof file.name === "string" && [...file.name].length > 0 && [...file.name].length <= 160 && !/[\u0000-\u001f\u007f/\\]/u.test(file.name) &&
    typeof file.mime_type === "string" && file.mime_type.length > 0 && file.mime_type.length <= 255 && !/[\r\n]/u.test(file.mime_type) &&
    Number.isSafeInteger(file.byte_size) && file.byte_size >= 0 && file.byte_size <= maximumFileBytes &&
    ["text", "partial_text", "stored_only"].includes(file.readability) &&
    Number.isSafeInteger(file.text_bytes) && file.text_bytes >= 0 && file.text_bytes <= 64 * 1024 &&
    typeof file.redacted === "boolean" && (file.text_sha256 === undefined || digest(file.text_sha256)) &&
    (file.reason === undefined || typeof file.reason === "string" && file.reason.length <= 1024);
}
export function validFileAttachments(value: unknown, workspaceID?: string): value is WorkspaceFileAttachment[] {
  return Array.isArray(value) && value.length <= maximumFileAttachments && value.every((file) =>
    validFileAttachment(file) && (!workspaceID || file.workspace_id === workspaceID)) &&
    new Set(value.map(({ id }) => id)).size === value.length;
}
export const fileAttachmentIdentities = (files: WorkspaceFileAttachment[] = []) =>
  files.map(({ id, workspace_id, sha256, byte_size }) => ({ id, workspace_id, sha256, byte_size }));
