import { webcrypto } from "node:crypto";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, type CyberAgentClient } from "../../api/client";
import type { QueueBinding, QueuedMessage } from "../../api/queued-messages";
import { V2RecoveryProvider } from "../recovery-storage";
import { V2QueuedMessages } from "./queued-messages";

const binding: QueueBinding = { threadID: "thread-a", runID: "run-a", sessionID: "session-a", workspaceID: "workspace-a" };
const time = "2026-09-22T01:00:00Z", sha = "a".repeat(64);
const message = (index: number, patch: Partial<QueuedMessage> = {}): QueuedMessage => ({
  id: `message-${index}`, sequence: index, status: "pending", prepared: false, content: `要求 ${index}`,
  content_sha256: sha, content_redacted: false, revision: 0, created_at: time, images: [], attachments: [], can_edit: true, can_cancel: true, ...patch,
});
function fixture(initial = [message(1), message(2)]) {
  let items = initial, revisionCalls = 0;
  const receipts = new Map<string, unknown>();
  const hooks: { post?: () => Promise<void>; observationError?: boolean; malformed?: boolean; successor?: boolean } = {};
  const postControl = vi.fn(async (_path: string, body: { expected_revision: number; content: string }, key: string) => {
    revisionCalls++; if (hooks.post) await hooks.post();
    const id = _path.split("/").at(-2)!;
    const current = items.find((item) => item.id === id)!;
    const receipt = { version: "session_steering_revision.v1", run_id: binding.runID, session_id: binding.sessionID, message_id: id,
      receipt: { id: `revision-${revisionCalls}`, from_revision: body.expected_revision, to_revision: body.expected_revision + 1,
        old_content_sha256: current.content_sha256, new_content_sha256: "b".repeat(64), created_at: time },
      replayed: false, execution_started: false, model_called: false, tool_called: false, capability_grant: false };
    receipts.set(key, receipt);
    items = items.map((item) => item.id === id ? { ...item, revision: item.revision + 1, content: body.content, content_sha256: "b".repeat(64) } : item);
    return receipt;
  });
  const get = vi.fn(async (path: string) => {
    if (path.includes("/revisions/")) {
      if (hooks.observationError) throw new TypeError("observation unavailable");
      const id = path.split("/").at(-3)!;
      const current = items.find((item) => item.id === id)!;
      const receipt = receipts.get(path.split("/").at(-1)!);
      return { version: "session_steering_revision.v1", session_id: binding.sessionID, message_id: path.split("/").at(-3),
        state: receipt ? "sealed" : "absent", ...(receipt ? { revision: receipt } : { message: { id, run_id: binding.runID,
          session_id: binding.sessionID, revision: current.revision, status: current.status } }), capability_grant: false };
    }
    const other = path.includes("thread-b");
    return { version: "thread_queued_messages.v1", thread_id: hooks.malformed ? "foreign" : other ? "thread-b" : binding.threadID,
      run_id: other ? "run-b" : hooks.successor ? "run-next" : binding.runID,
      session_id: other ? "session-b" : hooks.successor ? "session-next" : binding.sessionID,
      pending: hooks.successor ? 0 : items.filter((item) => !item.prepared).length, prepared: hooks.successor ? 0 : items.filter((item) => item.prepared).length,
      items: hooks.successor ? [] : other ? items.map((item) => ({ ...item, content: `B 的要求 ${item.sequence}` })) : items, capability_grant: false };
  });
  const cancelSessionSteering = vi.fn(async (_session: string, id: string) => {
    items = items.filter((item) => item.id !== id); return { run_id: binding.runID };
  });
  const client = { baseURL: "/api/v1", get, postControl, cancelSessionSteering, hasSessionSteeringControl: true } as unknown as CyberAgentClient;
  return { client, get, postControl, cancelSessionSteering, hooks, receipts, setItems: (next: QueuedMessage[]) => { items = next; } };
}
function mount(client: CyberAgentClient, current = binding) {
  const query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const view = (selected = current) => <V2RecoveryProvider client={client} scopeID="queue-test-store">
    <QueryClientProvider client={query}><V2QueuedMessages client={client} {...selected} running={false} /></QueryClientProvider>
  </V2RecoveryProvider>;
  return { ...render(view()), query, view };
}
async function editFirst(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByText("待处理 2 条");
  await user.click(screen.getAllByRole("button", { name: "编辑" })[0]);
  return screen.getByRole("textbox", { name: "编辑消息 1" });
}
beforeEach(() => { localStorage.clear(); vi.stubGlobal("crypto", webcrypto); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("reads the complete durable queue and changes one exact message without reordering", async () => {
  const f = fixture(Array.from({ length: 25 }, (_, index) => message(index + 1, index === 0 ? { prepared: true, can_edit: false, can_cancel: false } : {})));
  mount(f.client); const user = userEvent.setup();
  await screen.findByText("待处理 24 条 · 正在处理 1 条");
  expect(screen.getAllByRole("button", { name: "编辑" })).toHaveLength(25);
  expect(screen.getAllByRole("button", { name: "编辑" })[0]).toBeDisabled();
  await user.click(screen.getAllByRole("button", { name: "编辑" })[24]);
  const input = screen.getByRole("textbox", { name: "编辑消息 25" });
  await user.clear(input); await user.type(input, "最后一条修改后的要求"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText("修改已保存。");
  expect(f.postControl).toHaveBeenCalledWith("/sessions/session-a/messages/message-25/revise",
    { version: "session_steering_revision.v1", expected_revision: 0, content: "最后一条修改后的要求" }, expect.any(String));
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  await user.click(screen.getAllByRole("button", { name: "撤回" })[1]);
  await screen.findByText("消息已撤回。");
  await screen.findByText("待处理 23 条 · 正在处理 1 条");
});

it("preserves later typing and isolates a late save across task switches", async () => {
  const f = fixture(); let release!: () => void;
  f.hooks.post = () => new Promise<void>((resolve) => { release = resolve; });
  const ui = mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "保存这一版"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await user.type(input, "，这是后来写的");
  ui.rerender(ui.view({ ...binding, threadID: "thread-b", runID: "run-b", sessionID: "session-b" }));
  await screen.findAllByText("B 的要求 1");
  await act(async () => { release(); });
  expect(screen.queryByText("修改已保存。")).not.toBeInTheDocument();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  ui.rerender(ui.view());
  expect(await screen.findByRole("textbox", { name: "编辑消息 1" })).toHaveValue("保存这一版，这是后来写的");
  expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
  expect(f.postControl).toHaveBeenCalledTimes(1);
});

it("keeps an unknown save through reload and confirms only the original receipt without posting", async () => {
  const f = fixture(); const actual = f.postControl.getMockImplementation()!;
  f.postControl.mockImplementation(async (...args) => { await actual(...args); throw new TypeError("connection lost"); });
  const ui = mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "响应丢失但实际保存"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/保存结果待确认。connection lost/u);
  ui.unmount(); mount(f.client);
  await user.click(await screen.findByRole("button", { name: "核对保存结果" }));
  await screen.findByText("修改已保存。");
  expect(f.postControl).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  expect(f.get.mock.calls.some(([path]) => path.includes("/revisions/"))).toBe(true);
});

it("retains rejected edits and requires explicit adoption of the latest revision", async () => {
  const f = fixture();
  f.postControl.mockImplementation(async () => {
    f.setItems([message(1, { revision: 1, content: "另一窗口版本", content_sha256: "b".repeat(64) }), message(2)]);
    throw new APIRequestError("revision conflict", "CONFLICT", 409);
  });
  mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "保留我的版本"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/原消息版本已变化或已处理/u);
  expect(input).toHaveValue("保留我的版本");
  await user.click(screen.getByRole("button", { name: "载入最新正文，替换编辑稿" }));
  expect(screen.getByRole("textbox", { name: "编辑消息 1" })).toHaveValue("另一窗口版本");
  expect(f.postControl).toHaveBeenCalledTimes(1);
});

it("does not turn an absent receipt into rejection or silently retry a newer draft", async () => {
  const f = fixture(); const actual = f.postControl.getMockImplementation()!;
  f.postControl.mockRejectedValueOnce(new TypeError("connection lost")).mockImplementation(actual);
  mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "原保存"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/保存结果待确认。connection lost/u);
  await user.type(input, "以及后来输入");
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await screen.findByText(/尚未查到这次操作的最终记录/u);
  expect(f.postControl).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "重试原保存" }));
  await screen.findByText("修改已保存。");
  expect(f.postControl.mock.calls[1]).toEqual(f.postControl.mock.calls[0]);
  expect(screen.getByRole("textbox", { name: "编辑消息 1" })).toHaveValue("原保存以及后来输入");
});

it("keeps an empty attachment-only edit visible and allows explicit cancellation", async () => {
  const file = { id: "file-a", workspace_id: binding.workspaceID, sha256: sha, name: "notes.txt", mime_type: "text/plain",
    byte_size: 8, readability: "text" as const, text_bytes: 8, redacted: false };
  const f = fixture([message(1, { attachments: [file] }), message(2)]);
  mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input);
  expect(input).toHaveValue(""); expect(screen.getByRole("button", { name: "保存修改" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "取消编辑" }));
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  expect(f.postControl).not.toHaveBeenCalled();
});

it("keeps old-run drafts and unresolved operations reachable when the thread starts a successor run", async () => {
  const f = fixture(); const actual = f.postControl.getMockImplementation()!;
  f.postControl.mockImplementation(async (...args) => { await actual(...args); throw new TypeError("connection lost"); });
  const ui = mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "原执行的编辑稿"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/保存结果待确认。connection lost/u);
  f.hooks.successor = true;
  ui.rerender(ui.view({ ...binding, runID: "run-next", sessionID: "session-next" }));
  expect(await screen.findByRole("textbox", { name: "编辑消息 1" })).toHaveValue("原执行的编辑稿");
  expect(screen.getByText(/上轮编辑稿/u)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await screen.findByText("修改已保存。");
  expect(f.get.mock.calls.some(([path]) => path.startsWith("/sessions/session-a/messages/message-1/revisions/"))).toBe(true);
  expect(f.postControl).toHaveBeenCalledTimes(1);
});

it("does not treat an unauthorized retry as proof that the original unknown save was rejected", async () => {
  const f = fixture(); const actual = f.postControl.getMockImplementation()!;
  f.postControl.mockImplementationOnce(async (...args) => { await actual(...args); throw new TypeError("connection lost"); })
    .mockRejectedValueOnce(new APIRequestError("token expired", "POLICY_DENIED", 401));
  mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "此前已经保存的正文"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/保存结果待确认。connection lost/u);
  f.hooks.observationError = true;
  await user.click(screen.getByRole("button", { name: "重试原保存" }));
  await screen.findByText(/保存结果待确认。token expired/u);
  expect(screen.getByRole("button", { name: "核对保存结果" })).toBeEnabled();
  expect(screen.queryByText(/操作未接受/u)).not.toBeInTheDocument();
  f.hooks.observationError = false;
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await screen.findByText("修改已保存。");
});

it("rejects a queue from a different thread instead of exposing mutation buttons", async () => {
  const f = fixture(); f.hooks.malformed = true; mount(f.client);
  await screen.findByRole("alert");
  expect(within(screen.getByRole("region", { name: "待处理消息" })).queryByRole("button", { name: "编辑" })).not.toBeInTheDocument();
});

it("unlocks a normalized no-op only after an explicit original retry returns exact proof, preserving later edits", async () => {
  const f = fixture();
  f.postControl.mockRejectedValueOnce(new TypeError("connection lost")).mockImplementation(async (_path, body, key) => {
    const hash = async (text: string) => Buffer.from(await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode(text))).toString("hex");
    throw new APIRequestError("unchanged", "INVALID_ARGUMENT", 400, "", undefined, undefined, undefined, undefined,
      { version: "queue_revision_unchanged.v1", run_id: binding.runID, session_id: binding.sessionID, message_id: "message-1", expected_revision: 0,
        operation_key_sha256: await hash(key), request_content_sha256: await hash(body.content), normalized_content_sha256: sha, current_content_sha256: sha,
        execution_started: false, model_called: false, tool_called: false, capability_grant: false });
  });
  mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
  await user.clear(input); await user.type(input, "脱敏后等值的文字"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/保存结果待确认。connection lost/u);
  await user.click(screen.getByRole("button", { name: "核对保存结果" }));
  await screen.findByText(/尚未查到这次操作的最终记录/u);
  expect(f.postControl).toHaveBeenCalledTimes(1);
  await user.type(input, "，后来补充");
  await user.click(screen.getByRole("button", { name: "重试原保存" }));
  await screen.findByText(/未产生修订。编辑稿已保留/u);
  expect(input).toHaveValue("脱敏后等值的文字，后来补充");
  expect(screen.getByRole("button", { name: "保存修改" })).toBeEnabled();
  expect(screen.queryByRole("button", { name: "核对保存结果" })).not.toBeInTheDocument();
  expect(f.postControl.mock.calls[1]).toEqual(f.postControl.mock.calls[0]);
});

it("does not let a late refusal in one window erase a same-key commit in another window", async () => {
  const f = fixture(); const actual = f.postControl.getMockImplementation()!;
  let release!: () => void;
  f.postControl.mockImplementationOnce(async () => { await new Promise<void>((resolve) => { release = resolve; }); throw new APIRequestError("token expired", "POLICY_DENIED", 401); })
    .mockImplementationOnce(async (...args) => { await actual(...args); throw new TypeError("response lost"); });
  const first = mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "两个窗口的同一保存"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  const second = mount(f.client);
  await user.click(await within(second.container).findByRole("button", { name: "重试原保存" }));
  await within(second.container).findByText(/保存结果待确认。response lost/u);
  await act(async () => { release(); });
  await within(first.container).findByText("修改已保存。");
  // JSDOM hosts both simulated windows in one document; deliver the storage
  // notifications that a real second document receives from the first.
  await act(async () => {
    for (let index = 0; index < localStorage.length; index++) {
      const key = localStorage.key(index)!;
      if (decodeURIComponent(key).includes("queue-result:") || decodeURIComponent(key).includes("queue-editor-closed:")) {
        window.dispatchEvent(new StorageEvent("storage", { key, newValue: localStorage.getItem(key), storageArea: localStorage }));
      }
    }
  });
  await waitFor(() => expect(within(second.container).queryByRole("button", { name: "核对保存结果" })).not.toBeInTheDocument());
  expect(f.postControl.mock.calls[1]).toEqual(f.postControl.mock.calls[0]);
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

it("keeps the operation recoverable if persisting a verified unchanged result fails", async () => {
  const f = fixture();
  f.postControl.mockImplementation(async (_path, body, key) => {
    const hash = async (text: string) => Buffer.from(await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode(text))).toString("hex");
    throw new APIRequestError("unchanged", "INVALID_ARGUMENT", 400, "", undefined, undefined, undefined, undefined,
      { version: "queue_revision_unchanged.v1", run_id: binding.runID, session_id: binding.sessionID, message_id: "message-1", expected_revision: 0,
        operation_key_sha256: await hash(key), request_content_sha256: await hash(body.content), normalized_content_sha256: sha, current_content_sha256: sha,
        execution_started: false, model_called: false, tool_called: false, capability_grant: false });
  });
  const original = Storage.prototype.setItem;
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) {
    if (decodeURIComponent(key).includes("queue-result:")) throw new DOMException("quota full", "QuotaExceededError");
    original.call(this, key, value);
  });
  mount(f.client); const user = userEvent.setup(); const input = await editFirst(user);
  await user.clear(input); await user.type(input, "保留编辑稿"); await user.click(screen.getByRole("button", { name: "保存修改" }));
  await screen.findByText(/结果记录暂未保存，原操作和编辑稿已保留/u);
  expect(input).toHaveValue("保留编辑稿");
  expect(screen.getByRole("button", { name: "核对保存结果" })).toBeEnabled();
  expect(f.postControl).toHaveBeenCalledTimes(1);
});
