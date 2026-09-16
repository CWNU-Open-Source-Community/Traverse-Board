/** Prefer FileList; items is a fallback, not a second copy of the same files. */
export function clipboardFiles(data: Pick<DataTransfer, "files" | "items">): File[] {
  const files = Array.from(data.files ?? []);
  if (files.length) return files;
  return Array.from(data.items ?? []).filter((item) => item.kind === "file")
    .map((item) => item.getAsFile()).filter((file): file is File => file !== null);
}

/** OS file clipboard entries can omit MIME. Sniff only the three supported
 * image signatures; the upload service still decodes and validates all bytes. */
export async function normalizeClipboardFile(file: File): Promise<File> {
  if (file.type && file.type !== "application/octet-stream") return file;
  const bytes = new Uint8Array(await file.slice(0, 12).arrayBuffer());
  const ascii = (start: number, end: number) => String.fromCharCode(...bytes.slice(start, end));
  const type = bytes.length >= 8 && bytes[0] === 0x89 && ascii(1, 4) === "PNG" && bytes[4] === 13 && bytes[5] === 10 && bytes[6] === 26 && bytes[7] === 10 ? "image/png" :
    bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff ? "image/jpeg" :
      bytes.length >= 12 && ascii(0, 4) === "RIFF" && ascii(8, 12) === "WEBP" ? "image/webp" : "";
  return type ? new File([file], file.name, { type, lastModified: file.lastModified }) : file;
}
