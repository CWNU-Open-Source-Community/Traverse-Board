import { CyberAgentClient } from "./client";

const reference = { thread_id: "thread-1", run_id: "run-1", message_id: "message-1", event_sequence: 42 };
const client = () => new CyberAgentClient("read-secret", "/api/v1", "control-secret");
const submit = () => client().submitThreadTurn("thread-1", { version: "thread_message_submission.v1", content: "Keep this request" }, "failure-reference-key");
const respond = (error: object) => vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
  version: "api.v1", request_id: "reference-response", error: { code: "FAILED_PRECONDITION", message: "This turn failed", ...error },
}), { status: 412, headers: { "Content-Type": "application/json" } })));

it("carries the sealed failure reference without treating an older response as referenced", async () => {
  respond({ turn_failed: true, turn_failure: reference });
  await expect(submit()).rejects.toMatchObject({ turnFailed: true, turnFailure: reference });
  respond({ turn_failed: true });
  await expect(submit()).rejects.toMatchObject({ turnFailed: true, turnFailure: undefined });
});

it.each([null, {}, { ...reference, thread_id: "thread-other" }, { ...reference, run_id: "" },
  { ...reference, message_id: " message-1" }, { ...reference, event_sequence: 0 }, { ...reference, event_sequence: 1.5 },
  { ...reference, event_sequence: Number.MAX_SAFE_INTEGER + 1 }, { ...reference, extra: true }])(
  "rejects invalid or cross-Thread failure provenance: %j", async (turn_failure) => {
    respond({ turn_failed: true, turn_failure });
    await expect(submit()).rejects.toMatchObject({ code: "INVALID_RESPONSE", turnFailed: undefined });
  });

it("does not trust a reference without the sealed failure marker", async () => {
  respond({ turn_failure: reference });
  await expect(submit()).rejects.toMatchObject({ code: "INVALID_RESPONSE", turnFailed: undefined });
});
