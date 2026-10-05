import { act, cleanup, renderHook } from "@testing-library/react";
import { useEffect } from "react";
import type { ClientCapabilities } from "../api/client";
import { useConnectionStore } from "../state/connection";
import { createV2Client, useV2Client } from "./client-session";

afterEach(() => {
  cleanup();
  useConnectionStore.getState().disconnect();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

const health = {
  status: "ok" as const, api_version: "api.v1" as const,
  app_version: "test", schema_version: 129,
};
const batchCapabilities = {
  batchDeliveryControlEnabled: true, batchDeliveryHostValidationEnabled: true,
  executionPermissionControlEnabled: true, operatorApprovalEnabled: true,
  dangerFullAccessEnabled: true,
};

it.each([true, false])("preserves advanced capability wiring while requiring a control token: %s", (control) => {
  useConnectionStore.getState().connect("read-token-fixture", {
    status: "ok", api_version: "api.v1", app_version: "test", schema_version: 129,
  }, control ? "control-token-fixture" : "", {
    executionPermissionControlEnabled: true, operatorApprovalEnabled: true,
    verificationEvidenceEnabled: true, githubReviewControlEnabled: true,
    workspaceCheckpointControlEnabled: true, threadControlEnabled: true,
  });
  const client = createV2Client(useConnectionStore.getState());
  expect(client.hasVerificationEvidence).toBe(control);
  expect(client.hasGitHubReviewControl).toBe(control);
  expect(client.hasWorkspaceCheckpointControl).toBe(control);
  expect(client.hasThreadControl).toBe(control);
});

it.each<keyof ClientCapabilities>([
  "batchDeliveryControlEnabled", "executionPermissionControlEnabled",
  "operatorApprovalEnabled", "dangerFullAccessEnabled", "batchDeliveryHostValidationEnabled",
])("keeps batch host validation closed without %s", (missing) => {
  useConnectionStore.getState().connect("read", health, "control", {
    ...batchCapabilities, [missing]: false,
  });
  const client = createV2Client(useConnectionStore.getState());
  expect(client.hasBatchDeliveryControl).toBe(missing !== "batchDeliveryControlEnabled");
  expect(client.hasBatchDeliveryHostValidation).toBe(false);
});

it("carries batch authority into V2 only with a control credential", () => {
  useConnectionStore.getState().connect("read", health, "control", batchCapabilities);
  const controlled = createV2Client(useConnectionStore.getState());
  expect(controlled.hasBatchDeliveryControl).toBe(true);
  expect(controlled.hasBatchDeliveryHostValidation).toBe(true);
  useConnectionStore.getState().connect("read", health, "", batchCapabilities);
  const readOnly = createV2Client(useConnectionStore.getState());
  expect(readOnly.hasBatchDeliveryControl).toBe(false);
  expect(readOnly.hasBatchDeliveryHostValidation).toBe(false);
});

it("keeps the client and its active effects across navigation, health refresh, and equivalent reconnects", () => {
  const connect = useConnectionStore.getState().connect;
  connect("read", health, "control", batchCapabilities);
  const attach = vi.fn();
  const detach = vi.fn();
  const { result, rerender, unmount } = renderHook(() => {
    const client = useV2Client();
    useEffect(() => {
      attach(client);
      return () => detach(client);
    }, [client]);
    return client;
  });
  const initial = result.current;
  act(() => {
    useConnectionStore.getState().selectThread("thread-1");
    useConnectionStore.getState().selectRun("run-1");
    useConnectionStore.getState().selectSession("session-1");
    useConnectionStore.getState().setResourceKind("thread");
    useConnectionStore.getState().setHealth({ ...health, app_version: "updated" });
  });
  rerender();
  act(() => connect("read", { ...health }, "control", { ...batchCapabilities }));
  expect(result.current).toBe(initial);
  expect(attach).toHaveBeenCalledTimes(1);
  expect(detach).not.toHaveBeenCalled();
  unmount();
  expect(detach).toHaveBeenCalledExactlyOnceWith(initial);
});

it("replaces the client and request credentials after an authentication change", async () => {
  const fetchMock = vi.fn().mockImplementation(async () => new Response(JSON.stringify({
    version: "api.v1", request_id: "req-denied",
    error: { code: "PERMISSION_DENIED", message: "fixture denial" },
  }), { status: 403 }));
  vi.stubGlobal("fetch", fetchMock);
  const connect = useConnectionStore.getState().connect;
  connect("read-old", health, "control-old", batchCapabilities);
  const { result } = renderHook(() => useV2Client());
  const first = result.current;
  act(() => connect("read-new", health, "control-old", batchCapabilities));
  expect(result.current).not.toBe(first);
  await expect(result.current.health()).rejects.toThrow("fixture denial");
  expect(fetchMock.mock.calls[0][1].headers.Authorization).toBe("Bearer read-new");

  const second = result.current;
  act(() => connect("read-new", health, "control-new", batchCapabilities));
  expect(result.current).not.toBe(second);
  await expect(result.current.cancelRunBatchDelivery("run-1", "plan-1", {
    version: "batch_delivery_cancel.v1", confirm: true, reason: "operator cancellation",
  }, "operation-key-fixture")).rejects.toThrow("fixture denial");
  expect(fetchMock.mock.calls[1][1].headers.Authorization).toBe("Bearer control-new");
});

it("replaces the client when capabilities change and immediately applies their new gates", () => {
  const connect = useConnectionStore.getState().connect;
  connect("read", health, "control", batchCapabilities);
  const { result } = renderHook(() => useV2Client());
  const enabled = result.current;
  act(() => connect("read", health, "control", {
    ...batchCapabilities, batchDeliveryHostValidationEnabled: false,
  }));
  expect(result.current).not.toBe(enabled);
  expect(result.current.hasBatchDeliveryControl).toBe(true);
  expect(result.current.hasBatchDeliveryHostValidation).toBe(false);
  const withoutHostValidation = result.current;
  act(() => connect("read", health, "control", {
    ...batchCapabilities, batchDeliveryControlEnabled: false,
  }));
  expect(result.current).not.toBe(withoutHostValidation);
  expect(result.current.hasBatchDeliveryControl).toBe(false);
  expect(result.current.hasBatchDeliveryHostValidation).toBe(false);
});

it("preserves an explicit connection address, tracks its changes, and retains the default", () => {
  vi.stubEnv("VITE_API_BASE_URL", "/api/v1");
  const connect = useConnectionStore.getState().connect;
  connect("read", health, "control");
  const { result, unmount } = renderHook(() => useV2Client());
  const implicit = result.current;
  expect(implicit.baseURL).toBe("/api/v1");
  act(() => connect("read", health, "control", {}, "/api/v1/"));
  expect(result.current).not.toBe(implicit);
  expect(result.current.baseURL).toBe("/api/v1");
  unmount();

  vi.stubEnv("VITE_API_BASE_URL", "https://other-origin.example/api/v1");
  expect(createV2Client(useConnectionStore.getState()).baseURL).toBe("/api/v1");
  connect("read", health, "control");
  expect(() => createV2Client(useConnectionStore.getState())).toThrow("current browser origin");
});
