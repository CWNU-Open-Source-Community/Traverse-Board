import { useConnectionStore } from "./connection";
import { createV2Client } from "../v2/client-session";

const health = {
  status: "ok" as const,
  api_version: "api.v1" as const,
  app_version: "test",
  schema_version: 37,
};

describe("connection store", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    useConnectionStore.getState().disconnect();
  });

  it("keeps both capability tokens in memory and clears them on disconnect", () => {
    useConnectionStore.getState().connect("ephemeral-token", health, "ephemeral-control-token", {
      githubReviewControlEnabled: true,
      workspaceCheckpointControlEnabled: true,
      commandRuntimeProtocolAvailable: true,
      commandRuntimeAdapterInstalled: true,
      commandRuntimeAdapterReady: true,
    });
    useConnectionStore.getState().selectRun("run-1");

    expect(useConnectionStore.getState().token).toBe("ephemeral-token");
    expect(useConnectionStore.getState().controlToken).toBe("ephemeral-control-token");
    expect(useConnectionStore.getState().runControlEnabled).toBe(true);
    expect(useConnectionStore.getState().runCreationEnabled).toBe(true);
    expect(useConnectionStore.getState().sessionMessageEnabled).toBe(true);
    expect(useConnectionStore.getState().threadControlEnabled).toBe(true);
    expect(useConnectionStore.getState().sessionSteeringControlEnabled).toBe(true);
    expect(useConnectionStore.getState().runLifecycleEnabled).toBe(true);
    expect(useConnectionStore.getState().runExecutionEnabled).toBe(true);
    expect(useConnectionStore.getState().planDeliveryControlEnabled).toBe(true);
    expect(useConnectionStore.getState().approvalControlEnabled).toBe(true);
    expect(useConnectionStore.getState().evidenceAttachmentEnabled).toBe(true);
    expect(useConnectionStore.getState().githubReviewControlEnabled).toBe(true);
    expect(useConnectionStore.getState().workspaceCheckpointControlEnabled).toBe(true);
    expect(useConnectionStore.getState().commandRuntimeProtocolAvailable).toBe(true);
    expect(useConnectionStore.getState().commandRuntimeAdapterInstalled).toBe(true);
    expect(useConnectionStore.getState().commandRuntimeAdapterReady).toBe(true);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);

    useConnectionStore.getState().disconnect();
    expect(useConnectionStore.getState().token).toBe("");
    expect(useConnectionStore.getState().controlToken).toBe("");
    expect(useConnectionStore.getState().runControlEnabled).toBe(false);
    expect(useConnectionStore.getState().runCreationEnabled).toBe(false);
    expect(useConnectionStore.getState().sessionMessageEnabled).toBe(false);
    expect(useConnectionStore.getState().threadControlEnabled).toBe(false);
    expect(useConnectionStore.getState().sessionSteeringControlEnabled).toBe(false);
    expect(useConnectionStore.getState().runLifecycleEnabled).toBe(false);
    expect(useConnectionStore.getState().runExecutionEnabled).toBe(false);
    expect(useConnectionStore.getState().planDeliveryControlEnabled).toBe(false);
    expect(useConnectionStore.getState().approvalControlEnabled).toBe(false);
    expect(useConnectionStore.getState().evidenceAttachmentEnabled).toBe(false);
    expect(useConnectionStore.getState().githubReviewControlEnabled).toBe(false);
    expect(useConnectionStore.getState().workspaceCheckpointControlEnabled).toBe(false);
    expect(useConnectionStore.getState().commandRuntimeProtocolAvailable).toBe(false);
    expect(useConnectionStore.getState().commandRuntimeAdapterInstalled).toBe(false);
    expect(useConnectionStore.getState().commandRuntimeAdapterReady).toBe(false);
    expect(useConnectionStore.getState().selectedRunID).toBe("");
    expect(useConnectionStore.getState().selectedThreadID).toBe("");
  });

  it("carries explicit Web import authority into V2 and clears it on reconnect or disconnect", () => {
    const connect = useConnectionStore.getState().connect;
    connect("read", health, "control", { workspaceImportEnabled: true, runControlEnabled: false });
    expect(useConnectionStore.getState().workspaceImportEnabled).toBe(true);
    expect(createV2Client(useConnectionStore.getState()).hasWorkspaceImport).toBe(true);
    connect("read", health, "control");
    expect(useConnectionStore.getState().workspaceImportEnabled).toBe(false);
    expect(createV2Client(useConnectionStore.getState()).hasWorkspaceImport).toBe(false);
    connect("read", health, "", { workspaceImportEnabled: true });
    expect(useConnectionStore.getState().workspaceImportEnabled).toBe(false);
    connect("read", health, "control", { workspaceImportEnabled: true });
    useConnectionStore.getState().disconnect();
    expect(useConnectionStore.getState().workspaceImportEnabled).toBe(false);
  });

  it("preserves the execution read route independently of control credentials and clears it on reconnect", () => {
    const connect = useConnectionStore.getState().connect;
    connect("read", health, "", { runExecutionEnabled: true, threadExecutionReadEnabled: true });
    const readOnlyClient = createV2Client(useConnectionStore.getState());
    expect(readOnlyClient.hasThreadExecutionRead).toBe(true);
    expect(readOnlyClient.hasRunExecution).toBe(false);
    expect(readOnlyClient.hasThreadControl).toBe(false);
    connect("read", health, "control", { runExecutionEnabled: true, threadExecutionReadEnabled: false });
    expect(createV2Client(useConnectionStore.getState()).hasThreadExecutionRead).toBe(false);
    connect("read", health, "control", { threadExecutionReadEnabled: true });
    useConnectionStore.getState().disconnect();
    expect(useConnectionStore.getState().threadExecutionReadEnabled).toBe(false);
    connect("read", health, "control", { runExecutionEnabled: true });
    expect(createV2Client(useConnectionStore.getState()).hasThreadExecutionRead).toBe(false);
  });
});
