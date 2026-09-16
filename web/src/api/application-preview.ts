import type { components } from "./schema";
export type ApplicationPreviewElement = components["schemas"]["FullCDPPageElement"];
export type ApplicationPreview = components["schemas"]["FullCDPPreviewView"];
export function localPreviewURL(value: string): boolean {
  try {
    const url = new URL(value);
    return ["http:", "https:"].includes(url.protocol) && ["127.0.0.1", "[::1]"].includes(url.hostname) &&
      !url.username && !url.password;
  } catch { return false; }
}
export function parseApplicationPreview(value: unknown, runID: string, sessionID: string): ApplicationPreview {
  const result = value as ApplicationPreview;
  if (!result || result.version !== "full_cdp_preview.v1" || result.run_id !== runID || result.session_id !== sessionID ||
    !localPreviewURL(result.canonical_url) || !Number.isFinite(Date.parse(result.captured_at)) ||
    !result.page || typeof result.page.snapshot_id !== "string" || !/^[a-f0-9]{64}$/u.test(result.page.snapshot_id) ||
    result.page.untrusted_evidence !== true || typeof result.page.title !== "string" || typeof result.page.text !== "string" ||
    !Array.isArray(result.page.elements) || result.page.elements.length > 256 ||
    result.page.elements.some((element) => !element || typeof element.selector !== "string" || !element.selector ||
      typeof element.tag !== "string" || typeof element.disabled !== "boolean") ||
    !result.image || result.image.media_type !== "image/png" || !/^[a-f0-9]{64}$/u.test(result.image.sha256) ||
    !Number.isSafeInteger(result.image.bytes) || result.image.bytes <= 0 || result.image.bytes > 8 * 1024 * 1024 ||
    !Number.isSafeInteger(result.image.width) || !Number.isSafeInteger(result.image.height) ||
    result.image.width <= 0 || result.image.height <= 0 || result.image.width * result.image.height > 32 * 1024 * 1024) {
    throw new Error("预览结果无法验证，请重新读取当前页面。");
  }
  return result;
}
