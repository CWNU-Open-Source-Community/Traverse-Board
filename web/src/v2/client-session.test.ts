import { useConnectionStore } from "../state/connection";
import { createV2Client } from "./client-session";

afterEach(() => useConnectionStore.getState().disconnect());

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
