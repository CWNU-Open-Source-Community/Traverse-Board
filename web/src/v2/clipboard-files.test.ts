import { File as NodeFile } from "node:buffer";
import { clipboardFiles, normalizeClipboardFile } from "./clipboard-files";

beforeEach(() => vi.stubGlobal("File", NodeFile));
afterEach(() => vi.unstubAllGlobals());

it("uses FileList once when both browser clipboard representations contain the same mixed files", () => {
  const image = new File(["PNG"], "截图.png", { type: "image/png" });
  const text = new File(["requirements"], "需求.txt", { type: "text/plain" });
  const duplicate = vi.fn(() => image);
  const result = clipboardFiles({ files: [image, text], items: [{ kind: "file", getAsFile: duplicate }] } as unknown as DataTransfer);
  expect(result).toEqual([image, text]);
  expect(result[0]).toBe(image);
  expect(duplicate).not.toHaveBeenCalled();
});

it("uses file items when FileList is empty and leaves ordinary text and null file items out", () => {
  const image = new File(["PNG"], "截图.png", { type: "image/png" });
  const document = new File(["%PDF"], "说明.pdf", { type: "application/pdf" });
  const text = vi.fn();
  const result = clipboardFiles({ files: [], items: [
    { kind: "string", getAsFile: text }, { kind: "file", getAsFile: () => image },
    { kind: "file", getAsFile: () => null }, { kind: "file", getAsFile: () => document },
  ] } as unknown as DataTransfer);
  expect(result).toEqual([image, document]);
  expect(text).not.toHaveBeenCalled();
  expect(clipboardFiles({ files: [], items: [] } as unknown as DataTransfer)).toEqual([]);
});

it.each([
  ["", [0x89, 80, 78, 71, 13, 10, 26, 10, 0, 1], "image/png"],
  ["application/octet-stream", [0xff, 0xd8, 0xff, 1, 2], "image/jpeg"],
  ["", [82, 73, 70, 70, 12, 0, 0, 0, 87, 69, 66, 80, 0], "image/webp"],
] as const)("recognizes an OS image signature with MIME %s without changing bytes, name or timestamp", async (mime, bytes, expected) => {
  const original = new File([new Uint8Array(bytes)], "原文件", { type: mime, lastModified: 1750000000000 });
  const normalized = await normalizeClipboardFile(original);
  expect(normalized.type).toBe(expected);
  expect(normalized.name).toBe(original.name);
  expect(normalized.lastModified).toBe(original.lastModified);
  expect(normalized.size).toBe(original.size);
  expect(new Uint8Array(await normalized.arrayBuffer())).toEqual(new Uint8Array(await original.arrayBuffer()));
});

it("does not guess an image from a filename or rewrite ordinary file bytes", async () => {
  const text = new File(["a plain text file"], "misleading.png", { type: "" });
  const gif = new File(["GIF89a"], "animation.gif", { type: "application/octet-stream" });
  const declared = new File(["not decoded by the input adapter"], "photo.png", { type: "image/png" });
  expect(await normalizeClipboardFile(text)).toBe(text);
  expect(await normalizeClipboardFile(gif)).toBe(gif);
  expect(await normalizeClipboardFile(declared)).toBe(declared);
});
