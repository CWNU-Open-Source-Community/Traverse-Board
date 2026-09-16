import { webcrypto } from "node:crypto";
import { CyberAgentClient } from "./client";
import { validFileAttachments, type WorkspaceFileAttachment } from "./file-attachments";

const bytes = new TextEncoder().encode("中文附件\noriginal bytes\n");
let receipt: WorkspaceFileAttachment;
const envelope = (data: unknown) => new Response(JSON.stringify({ version: "api.v1", data, request_id: "attachment-test" }),
  { headers: { "Content-Type": "application/json", "X-CyberAgent-API-Version": "api.v1" } });
beforeEach(async () => {
  vi.stubGlobal("crypto", webcrypto);
  const sha256 = Buffer.from(await webcrypto.subtle.digest("SHA-256", bytes)).toString("hex");
  receipt = { id: "attachment-1", workspace_id: "workspace-1", name: "说明.txt", mime_type: "text/plain",
    sha256, byte_size: bytes.length, readability: "text", text_bytes: bytes.length, text_sha256: sha256, redacted: false };
});
afterEach(() => vi.unstubAllGlobals());
it("uploads all original bytes and seals a receipt to the original workspace, bytes and display name", async () => {
  const fetcher = vi.fn().mockImplementationOnce(() => envelope({ attachment: receipt }))
    .mockImplementationOnce(() => envelope({ attachment: { ...receipt, byte_size: receipt.byte_size + 1 } }));
  vi.stubGlobal("fetch", fetcher);
  const file = new File([bytes], receipt.name, { type: receipt.mime_type });
  Object.defineProperty(file, "arrayBuffer", { value: async () => bytes.buffer });
  const client = new CyberAgentClient("read", "/api/v1", "write");
  expect(await client.uploadWorkspaceFile("workspace-1", file, "original-file-upload-key")).toEqual(receipt);
  const request = fetcher.mock.calls[0][1];
  expect(JSON.parse(request.body).data_base64).toBe(Buffer.from(bytes).toString("base64"));
  expect(new Headers(request.headers).get("Authorization")).toBe("Bearer write");
  await expect(client.uploadWorkspaceFile("workspace-1", file, "original-file-upload-key")).rejects.toThrow("原文件不一致");
});
it("observes the original key using GET without retrying upload and rejects a foreign receipt", async () => {
  const fetcher = vi.fn().mockImplementationOnce(() => envelope({ state: "not_received" }))
    .mockImplementationOnce(() => envelope({ state: "stored", attachment: { ...receipt, workspace_id: "other" } }));
  vi.stubGlobal("fetch", fetcher);
  const client = new CyberAgentClient("read", "/api/v1", "write");
  expect(await client.inspectWorkspaceFileUpload("workspace-1", "original-file-upload-key")).toEqual({ state: "not_received" });
  await expect(client.inspectWorkspaceFileUpload("workspace-1", "original-file-upload-key")).rejects.toThrow("无法核对");
  for (const [, request] of fetcher.mock.calls) {
    expect(request.method).toBe("GET"); expect(request.body).toBeUndefined();
    expect(new Headers(request.headers).get("Idempotency-Key")).toBe("original-file-upload-key");
    expect(new Headers(request.headers).get("Authorization")).toBe("Bearer read");
  }
});
it("downloads exact original octet-stream bytes and supports empty files without accepting corruption", async () => {
  const headers = (file: WorkspaceFileAttachment) => ({ "content-type": "application/octet-stream", "content-length": String(file.byte_size),
    etag: `"${file.sha256}"`, "x-cyberagent-content-sha256": file.sha256 });
  const empty = { ...receipt, byte_size: 0, text_bytes: 0, sha256: Buffer.from(await webcrypto.subtle.digest("SHA-256", new Uint8Array())).toString("hex") };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response(bytes, { headers: headers(receipt) }))
    .mockResolvedValueOnce(new Response(null, { headers: headers(empty) }))
    .mockResolvedValueOnce(new Response(new Uint8Array(bytes.length), { headers: headers(receipt) })));
  const client = new CyberAgentClient("read");
  expect((await client.downloadWorkspaceFile(receipt)).size).toBe(bytes.length);
  expect((await client.downloadWorkspaceFile(empty)).size).toBe(0);
  await expect(client.downloadWorkspaceFile(receipt)).rejects.toThrow("内容验证失败");
  expect(validFileAttachments([receipt, receipt], "workspace-1")).toBe(false);
});
