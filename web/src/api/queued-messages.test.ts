import { webcrypto } from "node:crypto";
import { APIRequestError, CyberAgentClient } from "./client";
import { provesQueueRevisionUnchanged, type QueueRevisionInput } from "./queued-messages";

const input: QueueRevisionInput = { threadID: "thread-a", runID: "run-a", sessionID: "session-a", workspaceID: "workspace-a",
  messageID: "message-a", expectedRevision: 0, oldSHA256: "a".repeat(64), operationKey: "queue-operation-test-0001", content: "令牌：[隐藏]" };
const hash = async (text: string) => Buffer.from(await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode(text))).toString("hex");
async function proof() {
  return { version: "queue_revision_unchanged.v1", run_id: input.runID, session_id: input.sessionID, message_id: input.messageID,
    expected_revision: input.expectedRevision, operation_key_sha256: await hash(input.operationKey), request_content_sha256: await hash(input.content),
    normalized_content_sha256: input.oldSHA256, current_content_sha256: input.oldSHA256,
    execution_started: false, model_called: false, tool_called: false, capability_grant: false };
}
const failure = (value: unknown, status = 400) => new APIRequestError("unchanged", "INVALID_ARGUMENT", status, "", undefined, undefined, undefined, undefined, value);
beforeEach(() => vi.stubGlobal("crypto", webcrypto));
afterEach(() => vi.unstubAllGlobals());

it("passes the real HTTP error proof through the client and verifies exact immutable request hashes", async () => {
  const bound = await proof();
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ version: "api.v1", request_id: "request-a",
    error: { code: "INVALID_ARGUMENT", message: "正文等值", revision_unchanged: bound } }), { status: 400, headers: { "Content-Type": "application/json" } })));
  const client = new CyberAgentClient("read-test", "/api/v1", "control-test");
  let error: unknown;
  try { await client.postControl("/sessions/session-a/messages/message-a/revise", { content: input.content }, input.operationKey); }
  catch (caught) { error = caught; }
  expect(error).toBeInstanceOf(APIRequestError);
  expect(await provesQueueRevisionUnchanged(error, input)).toBe(true);
  expect(await provesQueueRevisionUnchanged(error, { ...input, content: "后写的正文" })).toBe(false);
  expect(await provesQueueRevisionUnchanged(error, { ...input, operationKey: "another-operation-0001" })).toBe(false);
});

it("retains uncertainty for generic refusals, missing fields and proofs from another message or version", async () => {
  const bound = await proof();
  for (const patch of [{ run_id: "other-run" }, { session_id: "other-session" }, { message_id: "other-message" },
    { expected_revision: 1 }, { operation_key_sha256: "b".repeat(64) }, { request_content_sha256: "b".repeat(64) },
    { normalized_content_sha256: "b".repeat(64) }, { current_content_sha256: "b".repeat(64) }, { model_called: true },
    { capability_grant: undefined }, { version: "unknown.v1" }]) {
    expect(await provesQueueRevisionUnchanged(failure({ ...bound, ...patch }), input)).toBe(false);
  }
  expect(await provesQueueRevisionUnchanged(failure(undefined), input)).toBe(false);
  expect(await provesQueueRevisionUnchanged(failure(bound, 401), input)).toBe(false);
});
