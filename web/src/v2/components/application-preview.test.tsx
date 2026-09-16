import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRef } from "react";
import type { CyberAgentClient } from "../../api/client";
import type { ApplicationPreview } from "../../api/application-preview";
import { V2ApplicationPreview } from "./application-preview";
import { fullCDPSessionQueryKey } from "./browser-cdp-control";

vi.mock("./permission-control", () => ({ V2PermissionControl: () => null }));
const observed: ApplicationPreview = { version: "full_cdp_preview.v1", run_id: "run-preview", session_id: "browser-preview",
  canonical_url: "http://127.0.0.1:18886/", captured_at: "2026-09-11T00:00:00Z",
  image: { media_type: "image/png", bytes: 128, sha256: "a".repeat(64), width: 800, height: 600 },
  page: { snapshot_id: "b".repeat(64), title: "独立项目应用", text: "点击计数 0", accessibility_nodes: 3, truncated: false,
    untrusted_evidence: true, elements: [{ selector: "#counter", tag: "button", name: "增加", disabled: false },
      { selector: "#name", tag: "input", type: "text", name: "称呼", disabled: false },
      { selector: "#hidden", tag: "input", type: "hidden", name: "内部字段", disabled: false }] } };

function fixture(cachedClosedSession = false) {
  let generation = 0;
  const post = vi.fn(async (path: string) => {
    if (path.endsWith("preview-action")) throw new Error("响应中断");
    generation++;
    return { ...observed, page: { ...observed.page, snapshot_id: String(generation).repeat(64) } };
  });
  const client = { hasFullCDPSessionControl: true, hasBrowserCDPPermissionControl: true, hasFullCDPDebug: true,
    get: vi.fn(async () => ({ execution_permission: { mode: "debug", runtime_gate_available: true, revision: 1 },
      browser_cdp_permission: { mode: "full_debug", runtime_gate_available: true, revision: 1 } })),
    getFullCDPSession: vi.fn(async () => ({ session: { run_id: "run-preview", session_id: "browser-preview", state: cachedClosedSession ? "closed" : "ready", target_origin: "http://127.0.0.1:18886", browser: { product: "chrome", channel: "stable" }, process_tree_quiescent: true, profile_cleaned: true } })),
    postControl: post, downloadVerifiedImage: vi.fn(async () => new Blob(["png"])),
    closeFullCDPSession: vi.fn(async () => ({ session: { state: "closed", session_id: "browser-preview", process_tree_quiescent: true, profile_cleaned: true } })),
  } as unknown as CyberAgentClient;
  const queries = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 5000 }, mutations: { retry: false } } });
  if (cachedClosedSession) queries.setQueryData(fullCDPSessionQueryKey("run-preview"), {
    session: { run_id: "run-preview", session_id: "browser-preview", state: "ready", target_origin: "http://127.0.0.1:18886" },
  });
  render(<QueryClientProvider client={queries}><V2ApplicationPreview client={client} runID="run-preview" threadID="thread-preview"
    onClose={() => {}} onRequestStart={() => {}} returnFocusRef={createRef()} /></QueryClientProvider>);
  return { client, post };
}
beforeEach(() => {
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:verified-preview"), revokeObjectURL: vi.fn() }));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("requires a fresh observation after an unknown click and never automatically repeats the action", async () => {
  const { post } = fixture();
  await screen.findByRole("img", { name: "应用页面：独立项目应用" });
  expect(screen.getByRole("textbox", { name: "项目应用地址" })).toHaveValue(observed.canonical_url);
  expect(screen.getByRole("combobox", { name: "预览浏览器" })).toHaveValue("chrome");
  fireEvent.click(screen.getByText("操作页面控件（2）"));
  expect(screen.queryByText("内部字段")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "点击" }));
  await screen.findByRole("alert");
  expect(post.mock.calls.filter(([path]) => path.endsWith("preview-action"))).toHaveLength(1);
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "点击" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "刷新页面预览" }));
  await screen.findByRole("img", { name: "应用页面：独立项目应用" });
  expect(post.mock.calls.filter(([path]) => path.endsWith("preview-action"))).toHaveLength(1);
});

it("sends typed values only to the exact current session and observed selector", async () => {
  const { post } = fixture();
  await screen.findByRole("img", { name: "应用页面：独立项目应用" });
  fireEvent.click(screen.getByText("操作页面控件（2）"));
  fireEvent.change(screen.getByRole("textbox", { name: "页面输入 称呼" }), { target: { value: "测试用户" } });
  fireEvent.click(screen.getByRole("button", { name: "输入到页面" }));
  await waitFor(() => expect(post).toHaveBeenCalledWith(expect.stringMatching(/preview-action$/), {
    version: "full_cdp_preview_action.v1", expected_session_id: "browser-preview", expected_snapshot_id: "1".repeat(64),
    action: "type", selector: "#name", value: "测试用户",
  }, expect.stringMatching(/^v2-preview-action-/)));
});

it("stops showing the old image when the preview is closed", async () => {
  const { client } = fixture();
  await screen.findByRole("img", { name: "应用页面：独立项目应用" });
  fireEvent.click(screen.getByRole("button", { name: "停止预览" }));
  await waitFor(() => expect(screen.queryByRole("img")).not.toBeInTheDocument());
  expect(client.closeFullCDPSession).toHaveBeenCalledWith("run-preview", expect.objectContaining({ expected_session_id: "browser-preview" }), expect.any(String));
});

it("rechecks a cached ready session before reading a preview that was closed elsewhere", async () => {
  const { post } = fixture(true);
  await screen.findByText("预览已关闭，浏览器和临时资料已清理。");
  expect(post).not.toHaveBeenCalled();
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});
