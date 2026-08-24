import { useConnectionStore } from "./connection";

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
    expect(useConnectionStore.getState().selectedRunID).toBe("");
    expect(useConnectionStore.getState().selectedThreadID).toBe("");
  });
});
