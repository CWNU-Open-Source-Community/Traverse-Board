import { afterEach, expect, it, vi } from "vitest";
import { desktopClipboardFilesAvailable, desktopClipboardFilesProtocol, inspectDesktopClipboardFiles,
  pasteDesktopClipboardFiles } from "./desktop-bridge";

const attachment = { id: "attachment-original", workspace_id: "workspace-a", name: "requirements.md", mime_type: "text/markdown",
  sha256: "a".repeat(64), byte_size: 7, readability: "text", text_sha256: "b".repeat(64), text_bytes: 7, redacted: false };
const image = { id: "image-original", workspace_id: "workspace-a", name: "capture.png", mime_type: "image/png", sha256: "c".repeat(64), byte_size: 100, width: 2, height: 2 };
const response = (extra: Record<string, unknown> = {}) => ({ version: desktopClipboardFilesProtocol, workspace_id: "workspace-a",
  status: "processed", batch_complete: true, images: [image], attachments: [attachment], rejected: [], ...extra });
function native(pasteResult: unknown = response(), inspectResult: unknown = response({ status: "partial", batch_complete: false })) {
  const paste = vi.fn().mockResolvedValue(pasteResult), inspect = vi.fn().mockResolvedValue(inspectResult);
  window.go = { desktop: { DesktopBridge: { Bootstrap: vi.fn(), InstallSkillPackage: vi.fn(), SelectSkillPackage: vi.fn(), PreviewSkillPackage: vi.fn(),
    PasteClipboardFiles: paste, InspectClipboardFiles: inspect } } };
  return { paste, inspect };
}
afterEach(() => { delete window.go; });

it("uses the exact workspace and original key and returns only validated receipts", async () => {
  const { paste, inspect } = native();
  expect(desktopClipboardFilesAvailable()).toBe(true);
  const result = await pasteDesktopClipboardFiles("workspace-a", "clipboard-original-event");
  expect(paste).toHaveBeenCalledExactlyOnceWith({ version: desktopClipboardFilesProtocol, workspace_id: "workspace-a", operation_key: "clipboard-original-event" });
  expect(result.attachments).toEqual([attachment]); expect(result.images).toEqual([image]);
  expect(inspect).not.toHaveBeenCalled();
});

it("observes saved items without pasting or claiming original batch completeness", async () => {
  const { paste, inspect } = native();
  const result = await inspectDesktopClipboardFiles("workspace-a", "clipboard-original-event");
  expect(result.status).toBe("partial"); expect(result.batch_complete).toBe(false);
  expect(inspect).toHaveBeenCalledExactlyOnceWith({ version: desktopClipboardFilesProtocol, workspace_id: "workspace-a", operation_key: "clipboard-original-event" });
  expect(paste).not.toHaveBeenCalled();
});

it("rejects another workspace, damaged receipts, unexpected paths and impossible outcomes", async () => {
  for (const value of [response({ workspace_id: "workspace-b" }), response({ images: [{ ...image, workspace_id: "workspace-b" }] }),
    response({ attachments: [{ ...attachment, sha256: "bad" }] }), response({ images: [{ ...image, width: 0 }] }),
    response({ path: "C:\\private.txt" }), response({ batch_complete: false }), response({ status: "empty" }),
    response({ rejected: [{ name: "../private", code: "INVALID_ARGUMENT", message: "invalid" }] }), response({ status: "other" })]) {
    native(value); await expect(pasteDesktopClipboardFiles("workspace-a", "clipboard-original-event")).rejects.toThrow("无法可靠核对");
  }
});

it("does not treat incomplete observations as a completed batch or no receipt as a stored item", async () => {
  for (const value of [response({ status: "partial" }), response({ status: "unknown", batch_complete: false }), response({ status: "processed", batch_complete: false })]) {
    const { paste } = native(undefined, value);
    await expect(inspectDesktopClipboardFiles("workspace-a", "clipboard-original-event")).rejects.toThrow("无法可靠核对");
    expect(paste).not.toHaveBeenCalled();
  }
});

it("distinguishes unsupported, empty and a failed native call without guessing success", async () => {
  expect(desktopClipboardFilesAvailable()).toBe(false);
  await expect(pasteDesktopClipboardFiles("workspace-a", "clipboard-original-event")).rejects.toThrow("不支持");
  native(response({ status: "unsupported", batch_complete: false, images: [], attachments: [] }));
  expect((await pasteDesktopClipboardFiles("workspace-a", "clipboard-original-event")).status).toBe("unsupported");
  const { paste } = native(response({ status: "empty", images: [], attachments: [] }));
  expect((await pasteDesktopClipboardFiles("workspace-a", "clipboard-original-event")).status).toBe("empty");
  paste.mockRejectedValueOnce(new Error("native reply lost"));
  await expect(pasteDesktopClipboardFiles("workspace-a", "clipboard-original-event")).rejects.toThrow("native reply lost");
});

it("rejects invalid request identity before any native clipboard access", async () => {
  const { paste, inspect } = native();
  await expect(pasteDesktopClipboardFiles("../workspace", "clipboard-original-event")).rejects.toThrow("身份无效");
  await expect(inspectDesktopClipboardFiles("workspace-a", "short")).rejects.toThrow("身份无效");
  expect(paste).not.toHaveBeenCalled(); expect(inspect).not.toHaveBeenCalled();
});
