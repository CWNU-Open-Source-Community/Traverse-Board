import type {
  GitHubReviewConfigureResultView,
  GitHubReviewCredentialView,
  GitHubReviewDeviceAuthorizationView,
  GitHubReviewDevicePollResultView,
  GitHubReviewEvidenceResultView,
  GitHubReviewFetchResultView,
  GitHubReviewProjectionView,
  GitHubReviewQualificationResultView,
  GitHubReviewWriteExecuteResultView,
  GitHubReviewWriteReviewResultView,
} from "./types";
import { parseStandardCodeDelivery } from "./standard-code-delivery";

const digest = /^[0-9a-f]{64}$/u;
const objectID = /^[0-9a-f]{40,64}$/u;
const evidenceStates = new Set(["verified", "partial", "stale", "unavailable", "not_run"]);

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function identity(value: unknown): value is string {
  return typeof value === "string" && value === value.trim() && value.length > 0 &&
    value.length <= 256 && !value.includes("\0");
}

function connection(value: unknown): boolean {
  if (!record(value) || value.protocol_version !== "github-review-connection.v1" ||
    !identity(value.id) || typeof value.enabled !== "boolean" ||
    !Number.isSafeInteger(value.generation) || Number(value.generation) < 1 ||
    !record(value.repository) || value.repository.host !== "github.com" ||
    !identity(value.repository.full_name) || !record(value.credential) ||
    !identity(value.credential.name) || !["github_app_device", "oauth_user", "fine_grained_pat"]
      .includes(String(value.credential.kind)) || !record(value.network) ||
    value.network.api_host !== "api.github.com" || value.network.host !== "github.com" ||
    value.network.oauth_host !== "github.com" || typeof value.network.read_enabled !== "boolean" ||
    typeof value.network.write_enabled !== "boolean" || !Array.isArray(value.network.allowed_log_hosts)) {
    return false;
  }
  return true;
}

function credentialView(value: unknown): value is GitHubReviewCredentialView {
  return record(value) && value.protocol_version === "github-review-api.v1" &&
    connection(value.connection) && record(value.credential) &&
    value.credential.protocol_version === "github-review-provider.v1" &&
    typeof value.credential.configured === "boolean" &&
    typeof value.credential.store_available === "boolean" &&
    typeof value.credential.refreshable === "boolean";
}

function snapshot(value: unknown): boolean {
  return record(value) && value.protocol_version === "github-review-snapshot.v1" &&
    identity(value.id) && digest.test(String(value.fingerprint)) && record(value.identity) &&
    Number.isSafeInteger(value.identity.number) && Number(value.identity.number) > 0 &&
    objectID.test(String(value.identity.base_sha)) && objectID.test(String(value.identity.head_sha)) &&
    record(value.capability) && value.capability.api_version === "2026-03-10" &&
    digest.test(String(value.capability.generation)) && Array.isArray(value.files) &&
    Array.isArray(value.threads) && Array.isArray(value.check_runs) && Array.isArray(value.jobs) &&
    Array.isArray(value.artifacts) && Array.isArray(value.omissions);
}

function evidence(value: unknown): boolean {
  return record(value) && identity(value.id) && identity(value.run_id) &&
    identity(value.workspace_id) && record(value.graph) &&
    value.graph.protocol_version === "github-review-evidence.v1" &&
    digest.test(String(value.graph.fingerprint)) && evidenceStates.has(String(value.graph.state)) &&
    Array.isArray(value.graph.mappings) && Array.isArray(value.graph.omissions);
}

function write(value: unknown): boolean {
  return record(value) && value.protocol_version === "github-review-write.v1" &&
    identity(value.id) && ["proposed", "running", "succeeded", "recovered", "failed"]
      .includes(String(value.status)) && record(value.preview) &&
    value.preview.protocol_version === "github-review-write.v1";
}

export function parseGitHubReviewConnections(value: unknown): GitHubReviewCredentialView[] {
  if (!Array.isArray(value) || !value.every(credentialView)) throw new Error("Invalid GitHub connection response");
  return value;
}

export function parseGitHubReviewCredential(value: unknown): GitHubReviewCredentialView {
  if (!credentialView(value)) throw new Error("Invalid GitHub credential response");
  return value;
}

export function parseGitHubReviewConfigure(value: unknown): GitHubReviewConfigureResultView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    typeof value.replayed !== "boolean" || !connection(value.connection)) {
    throw new Error("Invalid GitHub configuration response");
  }
  return value as unknown as GitHubReviewConfigureResultView;
}

export function parseGitHubDeviceAuthorization(value: unknown): GitHubReviewDeviceAuthorizationView {
  if (!record(value) || value.protocol_version !== "github-review-device-flow.v1" ||
    !identity(value.session_id) || !identity(value.user_code) ||
    value.verification_uri !== "https://github.com/login/device" ||
    !Number.isSafeInteger(value.poll_interval_ms) || Number(value.poll_interval_ms) < 1000 ||
    Number(value.poll_interval_ms) > 60_000) throw new Error("Invalid GitHub Device Flow response");
  return value as unknown as GitHubReviewDeviceAuthorizationView;
}

export function parseGitHubDevicePoll(value: unknown): GitHubReviewDevicePollResultView {
  if (!record(value) || value.protocol_version !== "github-review-device-flow.v1" ||
    !identity(value.session_id) || typeof value.configured !== "boolean" || !identity(value.state)) {
    throw new Error("Invalid GitHub Device Flow poll response");
  }
  return value as unknown as GitHubReviewDevicePollResultView;
}

export function parseGitHubQualification(value: unknown): GitHubReviewQualificationResultView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    !connection(value.connection) || !record(value.qualification) ||
    typeof value.qualification.eligible !== "boolean" ||
    !Array.isArray(value.qualification.diagnostics)) throw new Error("Invalid GitHub qualification response");
  return value as unknown as GitHubReviewQualificationResultView;
}

export function parseGitHubFetch(value: unknown): GitHubReviewFetchResultView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    typeof value.replayed !== "boolean" || !snapshot(value.snapshot)) {
    throw new Error("Invalid GitHub snapshot response");
  }
  return value as unknown as GitHubReviewFetchResultView;
}

export function parseGitHubEvidence(value: unknown): GitHubReviewEvidenceResultView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    typeof value.replayed !== "boolean" || !evidence(value.evidence)) {
    throw new Error("Invalid GitHub evidence response");
  }
  return value as unknown as GitHubReviewEvidenceResultView;
}

export function parseGitHubProjection(value: unknown, runID: string): GitHubReviewProjectionView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    value.run_id !== runID || !connection(value.connection) || !record(value.credential) ||
    !Array.isArray(value.snapshots) || !value.snapshots.every(snapshot) ||
    !Array.isArray(value.evidence) || !value.evidence.every(evidence) ||
    !Array.isArray(value.writes) || !value.writes.every(write)) {
    throw new Error("Invalid GitHub review projection");
  }
  if (Object.prototype.hasOwnProperty.call(value, "standard_code_delivery")) {
    return { ...value,
      standard_code_delivery: parseStandardCodeDelivery(value.standard_code_delivery, runID),
    } as unknown as GitHubReviewProjectionView;
  }
  return value as unknown as GitHubReviewProjectionView;
}

export function parseGitHubWriteReview(value: unknown): GitHubReviewWriteReviewResultView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    typeof value.replayed !== "boolean" || !write(value.operation) || !record(value.approval)) {
    throw new Error("Invalid GitHub write preview response");
  }
  return value as unknown as GitHubReviewWriteReviewResultView;
}

export function parseGitHubWriteExecute(value: unknown): GitHubReviewWriteExecuteResultView {
  if (!record(value) || value.protocol_version !== "github-review-api.v1" ||
    typeof value.replayed !== "boolean" || !write(value.operation) || !record(value.receipt)) {
    throw new Error("Invalid GitHub write receipt response");
  }
  return value as unknown as GitHubReviewWriteExecuteResultView;
}
