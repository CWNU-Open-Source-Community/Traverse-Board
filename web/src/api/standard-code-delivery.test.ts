import { standardCodeDeliveryFixture } from "../test/standard-code-delivery";
import { parseGitHubProjection } from "./github-review";
import { parseStandardCodeDelivery } from "./standard-code-delivery";

describe("Standard Code delivery projection", () => {
  it("accepts optional read-source facts without changing the sealed verification conclusion", () => {
    const report = standardCodeDeliveryFixture();
    const available = { job_id: "verification-1", artifact_id: "artifact-stdout", status: "available",
      thread_id: "thread-1", activity_ref: "command-1" };
    expect(parseStandardCodeDelivery({ ...report, output_sources: [available] }, "run-1").output_sources)
      .toEqual([available]);
    const metadataOnly = { job_id: "verification-1", artifact_id: "artifact-stdout", status: "metadata_only",
      reason: "activity_source_unavailable" };
    expect(parseStandardCodeDelivery({ ...report, output_sources: [metadataOnly] }, "run-1").verified).toBe(true);
    expect(parseStandardCodeDelivery(report, "run-1").output_sources).toBeUndefined();
  });

  it.each([
    [],
    [{ job_id: "another-job", artifact_id: "artifact-stdout", status: "available", thread_id: "thread-1", activity_ref: "command-1" }],
    [{ job_id: "verification-1", artifact_id: "another-artifact", status: "available", thread_id: "thread-1", activity_ref: "command-1" }],
    [{ job_id: "verification-1", artifact_id: "artifact-stdout", status: "available", thread_id: "thread-1" }],
    [{ job_id: "verification-1", artifact_id: "artifact-stdout", status: "available", thread_id: "thread-1", activity_ref: "command-1", reason: "output_not_public" }],
    [{ job_id: "verification-1", artifact_id: "artifact-stdout", status: "metadata_only", reason: "output_not_public", thread_id: "thread-1", activity_ref: "command-1" }],
    [{ job_id: "verification-1", artifact_id: "artifact-stdout", status: "metadata_only", reason: "anything_is_readable" }],
    Array(2).fill({ job_id: "verification-1", artifact_id: "artifact-stdout", status: "metadata_only", reason: "activity_source_unavailable" }),
  ])("rejects incomplete, unrelated or contradictory output-source facts: %j", (...sources) => {
    expect(() => parseStandardCodeDelivery({ ...standardCodeDeliveryFixture(), output_sources: sources }, "run-1"))
      .toThrow("Invalid Standard Code delivery truth projection");
  });

  it("accepts the exact shared projection and all closed statuses", () => {
    expect(parseStandardCodeDelivery(standardCodeDeliveryFixture(), "run-1").status).toBe("passed");

    const failed = standardCodeDeliveryFixture();
    failed.status = failed.receipt_status = "failed";
    failed.verified = false;
    failed.verifications[0].conclusion = "failed";
    failed.verifications[0].reason_code = "verification_failed";
    failed.verifications[0].exit_code = 1;
    expect(parseStandardCodeDelivery(failed, "run-1").status).toBe("failed");

    const partial = standardCodeDeliveryFixture();
    partial.status = partial.receipt_status = "partial";
    partial.verified = false;
    partial.verifications[0].conclusion = "partial";
    partial.verifications[0].reason_code = "output_truncated";
    partial.verifications[0].output_truncated = true;
    expect(parseStandardCodeDelivery(partial, "run-1").status).toBe("partial");

    const blocked = standardCodeDeliveryFixture();
    blocked.status = blocked.receipt_status = "blocked";
    blocked.verified = false;
    blocked.verifications[0].conclusion = "blocked";
    blocked.verifications[0].reason_code = "command_cancelled";
    blocked.verifications[0].state = "cancelled";
    expect(parseStandardCodeDelivery(blocked, "run-1").status).toBe("blocked");

    const stale = standardCodeDeliveryFixture();
    stale.status = stale.receipt_status = "stale";
    stale.verified = false;
    stale.verifications[0].conclusion = "stale";
    stale.verifications[0].reason_code = "workspace_modified_after_verification";
    stale.verifications[0].current_revision = false;
    expect(parseStandardCodeDelivery(stale, "run-1").status).toBe("stale");

    const notRun = standardCodeDeliveryFixture();
    notRun.status = notRun.receipt_status = "not_run";
    notRun.verified = false;
    notRun.declaration = "no_applicable_tests";
    notRun.verifications = [];
    expect(parseStandardCodeDelivery(notRun, "run-1").status).toBe("not_run");
  });

  it("marks a passed receipt stale after the observed Workspace revision changes", () => {
    const value = standardCodeDeliveryFixture();
    value.status = "stale";
    value.verified = false;
    value.observation = { revision_sha256: "0".repeat(64),
      reason_code: "workspace_modified_after_verification",
      observed_at: "2026-08-26T08:01:00Z" };
    const parsed = parseStandardCodeDelivery(value, "run-1");
    expect(parsed.status).toBe("stale");
    expect(parsed.receipt_status).toBe("passed");
    expect(parsed.verified).toBe(false);
  });

  it("keeps a passed receipt current when the live observation has the same revision", () => {
    const value = standardCodeDeliveryFixture();
    value.observation = { revision_sha256: value.final_checkpoint.revision_sha256,
      observed_at: "2026-08-26T08:01:00Z" };
    expect(parseStandardCodeDelivery(value, "run-1").verified).toBe(true);
  });

  it("uses the exact same projection inside GitHub Review", () => {
    const delivery = standardCodeDeliveryFixture();
    const projection = {
      protocol_version: "github-review-api.v1", run_id: "run-1",
      connection: { protocol_version: "github-review-connection.v1", id: "connection-1",
        enabled: true, generation: 1,
        repository: { host: "github.com", full_name: "acme/widget" },
        credential: { name: "test", kind: "github_app_device" },
        network: { host: "github.com", api_host: "api.github.com", oauth_host: "github.com",
          read_enabled: true, write_enabled: false, allowed_log_hosts: [] } },
      credential: {}, snapshots: [], evidence: [], writes: [], standard_code_delivery: delivery,
    };
    const parsed = parseGitHubProjection(projection, "run-1");
    expect(parsed.standard_code_delivery?.receipt_sha256).toBe(delivery.receipt_sha256);
    projection.standard_code_delivery = { ...delivery,
      safeguards: { ...delivery.safeguards, absolute_paths_exposed: true } };
    expect(() => parseGitHubProjection(projection, "run-1")).toThrow();
  });

  it.each([
    ["nonterminal passed evidence", (value: ReturnType<typeof standardCodeDeliveryFixture>) => {
      value.verifications[0].state = "running";
      delete value.verifications[0].completed_at;
    }],
    ["mutated observation reported passed", (value: ReturnType<typeof standardCodeDeliveryFixture>) => {
      value.observation = { revision_sha256: "0".repeat(64),
        reason_code: "workspace_modified_after_verification", observed_at: value.created_at };
    }],
    ["secret", (value: ReturnType<typeof standardCodeDeliveryFixture>) => {
      value.uncovered_items = [{ summary: "token=very-secret-delivery-value",
        summary_sha256: "1".repeat(64) }];
    }],
    ["Windows host path", (value: ReturnType<typeof standardCodeDeliveryFixture>) => {
      value.uncovered_items = [{ summary: "C:\\Users\\alice\\private.txt",
        summary_sha256: "1".repeat(64) }];
    }],
    ["Unix host path", (value: ReturnType<typeof standardCodeDeliveryFixture>) => {
      value.uncovered_items = [{ summary: "/home/alice/private.txt",
        summary_sha256: "1".repeat(64) }];
    }],
    ["private reasoning flag", (value: ReturnType<typeof standardCodeDeliveryFixture>) => {
      value.safeguards.private_reasoning_stored = true;
    }],
  ])("rejects %s", (_name, mutate) => {
    const value = standardCodeDeliveryFixture();
    mutate(value);
    expect(() => parseStandardCodeDelivery(value, "run-1")).toThrow();
  });
});
