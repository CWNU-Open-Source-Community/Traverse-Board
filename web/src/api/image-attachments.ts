import type { components } from "./schema";
export type WorkspaceImageAttachment = components["schemas"]["WorkspaceImage"];

export const maximumImageBytes = 5 * 1024 * 1024;
export const maximumImages = 4;
export const acceptedImageTypes = ["image/png", "image/jpeg", "image/webp"];
const identity = (value: unknown): value is string => typeof value === "string" && /^[\w.-]{1,256}$/u.test(value);
export function validImageAttachment(value: unknown): value is WorkspaceImageAttachment {
  if (!value || typeof value !== "object") return false;
  const image = value as WorkspaceImageAttachment;
  return identity(image.id) && identity(image.workspace_id) && /^[a-f0-9]{64}$/u.test(image.sha256) &&
    acceptedImageTypes.includes(image.mime_type) && Number.isSafeInteger(image.byte_size) &&
    image.byte_size > 0 && image.byte_size <= maximumImageBytes &&
    Number.isSafeInteger(image.width) && Number.isSafeInteger(image.height) &&
    image.width > 0 && image.height > 0 && image.width <= 8192 && image.height <= 8192 &&
    image.width * image.height <= 16 * 1024 * 1024 &&
    (image.name === undefined || typeof image.name === "string" && [...image.name].length <= 160 && !/[\u0000-\u001f\u007f/\\]/u.test(image.name));
}
export function validImageAttachments(value: unknown, workspaceID?: string): value is WorkspaceImageAttachment[] {
  return Array.isArray(value) && value.length <= maximumImages &&
    value.every((image) => validImageAttachment(image) && (!workspaceID || image.workspace_id === workspaceID)) &&
    new Set(value.map((image) => image.id)).size === value.length;
}

// Requests are compared by immutable, ordered references, never by filenames.
export const imageIdentities = (images: WorkspaceImageAttachment[] = []) => images.map(({ id, sha256 }) => ({ id, sha256 }));
