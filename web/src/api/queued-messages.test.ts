import { webcrypto } from "node:crypto";
import { APIRequestError, CyberAgentClient } from "./client";
import { provesQueueRevisionUnchanged, parseQueuePromotion, parseQueuePromotionRejection, inspectQueuePromotion, promoteQueuedMessage, parseQueuedMessages, type QueueRevisionInput, type QueuePromotionInput } from "./queued-messages";

const input: QueueRevisionInput = { threadID: "thread-a", runID: "run-a", sessionID: "session-a", workspaceID: "workspace-a",
  messageID: "message-a", expectedRevision: 0, oldSHA256: "a".repeat(64), operationKey: "queue-operation-test-0001", content: "令牌：[隐藏]" };
const promotionInput: QueuePromotionInput = { ...input, expectedAttemptID: "attempt-a", expectedExecutionID: "execution-a" };
const promotion = () => ({ version: "session_steering_promotion.v1", run_id: input.runID, session_id: input.sessionID, message_id: input.messageID,
  receipt: { id: "promotion-a", replacement_message_id: "message-b", expected_revision: 0, content_sha256: input.oldSHA256,
    target_attempt_id: "attempt-a", execution_id: "execution-a", cancellation_id: "cancellation-a", created_at: "2026-09-29T00:00:00Z" },
  replayed: false, execution_started: false, model_called: false, tool_called: false, capability_grant: false });

it("binds promotion receipts to the captured revision, text, attempt and execution, even after restart", () => {
  expect(parseQueuePromotion(promotion(), promotionInput).receipt.replacement_message_id).toBe("message-b");
  for (const patch of [{ replacement_message_id: input.messageID }, { expected_revision: 1 }, { content_sha256: "b".repeat(64) },
    { target_attempt_id: "other-attempt" }, { execution_id: "restarted-execution" }, { cancellation_id: undefined }]) {
    expect(() => parseQueuePromotion({ ...promotion(), receipt: { ...promotion().receipt, ...patch } }, promotionInput)).toThrow();
  }
  expect(() => parseQueuePromotion({ ...promotion(), run_id: "another-run" }, promotionInput)).toThrow();
  expect(() => parseQueuePromotion({ ...promotion(), capability_grant: true }, promotionInput)).toThrow();
});
it("POSTs only captured identities and observes without retrying or inventing an execution owner", async () => {
  const client = new CyberAgentClient("read-test", "/api/v1", "control-test");
  const post = vi.spyOn(client, "postControl").mockResolvedValue(promotion());
  await promoteQueuedMessage(client, promotionInput);
  expect(post).toHaveBeenCalledWith("/sessions/session-a/messages/message-a/promote", { version: "session_steering_promotion.v1",
    expected_revision: 0, expected_content_sha256: input.oldSHA256, expected_attempt_id: "attempt-a", expected_execution_id: "execution-a" }, input.operationKey);
  const absent = { version: "session_steering_promotion.v1", session_id: input.sessionID, message_id: input.messageID, capability_grant: false,
    state: "absent", message: { id: input.messageID, run_id: input.runID, session_id: input.sessionID, revision: 0, status: "pending" }, execution_observed: false };
  const get = vi.spyOn(client, "get").mockResolvedValue(absent);
  expect(await inspectQueuePromotion(client, promotionInput)).toMatchObject({ state: "absent", executionObserved: false });
  get.mockResolvedValue({ ...absent, execution_observed: true, execution_id: "restarted-execution" });
  expect(await inspectQueuePromotion(client, promotionInput)).toMatchObject({ state: "absent", executionObserved: true, executionID: "restarted-execution" });
  get.mockResolvedValue({ ...absent, execution_id: "unobserved-execution" });
  await expect(inspectQueuePromotion(client, promotionInput)).rejects.toThrow();
  get.mockResolvedValue({ ...absent, message: undefined, state: "sealed", promotion: { ...promotion(), replayed: true } });
  expect(await inspectQueuePromotion(client, promotionInput)).toMatchObject({ state: "sealed" });
  expect(post).toHaveBeenCalledTimes(1);
});
it("requires current queue owner and attempt to appear as one pair", () => {
  const snapshot = { version: "thread_queued_messages.v1", thread_id: input.threadID, run_id: input.runID, session_id: input.sessionID,
    pending: 0, prepared: 0, items: [], capability_grant: false };
  expect(parseQueuedMessages(snapshot, input).items).toEqual([]);
  expect(() => parseQueuedMessages({ ...snapshot, current_attempt_id: "attempt-a" }, input)).toThrow();
  expect(() => parseQueuedMessages({ ...snapshot, execution_id: "execution-a" }, input)).toThrow();
  expect(parseQueuedMessages({ ...snapshot, current_attempt_id: "attempt-a", execution_id: "execution-a" }, input).execution_id).toBe("execution-a");
});
it("only settles an exact sealed rejection and never treats a refusal as an applied promotion", async () => {
  const source=promotion();const { replacement_message_id: _replacement, cancellation_id: _cancel, ...receipt }=source.receipt;
  const rejected={...source, rejected:true, receipt};
  expect(parseQueuePromotionRejection(rejected,promotionInput).rejected).toBe(true);
  expect(()=>parseQueuePromotion(rejected,promotionInput)).toThrow();
  for(const patch of [{expected_revision:1},{content_sha256:"b".repeat(64)},{target_attempt_id:"other-attempt"},{execution_id:"other-execution"},{replacement_message_id:"invented-replacement"}]) {
    expect(()=>parseQueuePromotionRejection({...rejected,receipt:{...receipt,...patch}},promotionInput)).toThrow();
  }
  const client=new CyberAgentClient("read-test","/api/v1","control-test");
  vi.spyOn(client,"postControl").mockResolvedValue(rejected);
  expect(await promoteQueuedMessage(client,promotionInput)).toMatchObject({rejected:true});
  const value={version:source.version,session_id:input.sessionID,message_id:input.messageID,state:"rejected",rejection:rejected,execution_observed:false,capability_grant:false};
  const get=vi.spyOn(client,"get").mockResolvedValue(value);
  expect(await inspectQueuePromotion(client,promotionInput)).toEqual({state:"rejected"});
  get.mockResolvedValue({...value,promotion:source});await expect(inspectQueuePromotion(client,promotionInput)).rejects.toThrow();
  get.mockResolvedValue({...value,rejection:{...rejected,run_id:"another-run"}});await expect(inspectQueuePromotion(client,promotionInput)).rejects.toThrow();
});
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
