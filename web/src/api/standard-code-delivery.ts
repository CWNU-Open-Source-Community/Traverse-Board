import type { StandardCodeDeliveryView } from "./types";

const digest = /^[0-9a-f]{64}$/u;
const gitObject = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/u;
const statuses = new Set(["passed", "failed", "partial", "not_run", "blocked", "stale"]);
const verificationStatuses = new Set(["passed", "failed", "partial", "blocked", "stale"]);
const verificationReasons: Record<string, ReadonlySet<string>> = {
  passed: new Set(["verification_passed"]),
  failed: new Set(["verification_failed"]),
  partial: new Set(["output_truncated", "command_artifact_missing"]),
  blocked: new Set(["command_cancelled", "command_timed_out", "command_interrupted",
    "command_not_terminal"]),
  stale: new Set(["workspace_modified_after_verification", "permission_generation_drift",
    "backend_generation_drift"]),
};
const declarations = new Set(["no_applicable_tests", "user_skipped", "budget_exhausted",
  "missing_dependency", "approval_denied"]);
const jobStates = new Set(["prepared", "running", "stopping", "completed", "failed",
  "timed_out", "cancelled", "killed", "interrupted"]);
const secretLike = /(?:sk-[A-Za-z0-9_-]{16,}|(?:api|access|auth|secret|token|password)[_-]?(?:key|token|secret)?\s*[:=]\s*\S+)/iu;
const redactedMarker = /\[REDACTED:[A-Za-z0-9_-]+\]/gu;
const privateHostPath = /(?:^|[\s"'(=])(?:[A-Za-z]:[\\/][^\s"'<>]*|\\\\[^\\/\s"'<>]+[\\/][^\s"'<>]*|\/[^\s"'<>]+)/iu;

function containsPrivateMaterial(value: string): boolean {
  return secretLike.test(value.replace(redactedMarker, "")) || privateHostPath.test(value);
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function exact(value: unknown, required: readonly string[], optional: readonly string[] = []):
  value is Record<string, unknown> {
  if (!record(value)) return false;
  const allowed = new Set([...required, ...optional]);
  const keys = Object.keys(value);
  return required.every((key) => Object.prototype.hasOwnProperty.call(value, key)) &&
    keys.every((key) => allowed.has(key));
}

function text(value: unknown, maximum = 512, empty = false): value is string {
  return typeof value === "string" && value === value.trim() && value.length <= maximum &&
    (empty || value.length > 0) && !/[\u0000-\u001f\u007f]/u.test(value);
}

function identity(value: unknown): value is string {
  return text(value, 256) && !/[\\/:]/u.test(value);
}

function sha256(value: unknown): value is string {
  return typeof value === "string" && digest.test(value);
}

function safeInteger(value: unknown, minimum = 0): value is number {
  return Number.isSafeInteger(value) && Number(value) >= minimum;
}

function date(value: unknown): value is string {
  return typeof value === "string" && value.length <= 64 &&
    Number.isFinite(Date.parse(value));
}

function publicLink(value: unknown): value is string {
  return text(value, 2048) && value.startsWith("/api/v1/") &&
    !value.includes("..") && !value.includes("\\");
}

function binding(value: unknown, runID?: string): boolean {
  const keys = ["backend", "backend_generation_sha256", "capability_generation_sha256",
    "drydock_generation", "drydock_id", "drydock_workspace_id", "mission_id",
    "permission_revision", "permission_snapshot_id", "preset_operation_sha256", "run_id",
    "session_id", "source_workspace_id", "supervisor_mutation_epoch"];
  return exact(value, keys) && identity(value.run_id) && (!runID || value.run_id === runID) &&
    identity(value.mission_id) && identity(value.session_id) && identity(value.source_workspace_id) &&
    identity(value.drydock_workspace_id) && identity(value.drydock_id) &&
    safeInteger(value.drydock_generation, 1) && sha256(value.preset_operation_sha256) &&
    identity(value.permission_snapshot_id) && safeInteger(value.permission_revision, 1) &&
    text(value.backend, 128) && sha256(value.backend_generation_sha256) &&
    sha256(value.capability_generation_sha256) && safeInteger(value.supervisor_mutation_epoch);
}

function checkpoint(value: unknown): boolean {
  const keys = ["branch_sha256", "created_at", "head_commit", "id",
    "incomplete_reason_sha256", "index_sha256", "manifest_sha256", "recovery_level",
    "revision_sha256", "root_fingerprint", "root_path_sha256"];
  return exact(value, keys) && identity(value.id) && sha256(value.manifest_sha256) &&
    sha256(value.index_sha256) && sha256(value.root_fingerprint) &&
    sha256(value.root_path_sha256) && typeof value.head_commit === "string" &&
    gitObject.test(value.head_commit) && sha256(value.branch_sha256) &&
    sha256(value.revision_sha256) && ["complete", "partial", "unavailable"].includes(
      String(value.recovery_level)) && Array.isArray(value.incomplete_reason_sha256) &&
    value.incomplete_reason_sha256.length <= 64 &&
    value.incomplete_reason_sha256.every(sha256) && date(value.created_at);
}

function changedFile(value: unknown): boolean {
  const required = ["committed", "conflicted", "index_changed", "path_redacted", "path_sha256",
    "tracked", "untracked", "worktree_changed"];
  if (!exact(value, required, ["file_url", "path"]) || !sha256(value.path_sha256) ||
    [value.committed, value.conflicted, value.index_changed, value.path_redacted,
      value.tracked, value.untracked, value.worktree_changed]
      .some((current) => typeof current !== "boolean")) return false;
  const hasPath = Object.prototype.hasOwnProperty.call(value, "path");
  const hasURL = Object.prototype.hasOwnProperty.call(value, "file_url");
  if (value.path_redacted === hasPath || hasPath !== hasURL) return false;
  if (!hasPath) return true;
  return text(value.path, 1024) && !String(value.path).startsWith("/") &&
    !/[\\:]/u.test(String(value.path)) && !String(value.path).split("/").includes("..") &&
    !containsPrivateMaterial(String(value.path)) && publicLink(value.file_url);
}

function diff(value: unknown): boolean {
  const keys = ["bytes", "changed_count", "committed_count", "conflict_count", "files",
    "index_count", "redacted_count", "sha256", "tracked_count", "untracked_count",
    "worktree_count"];
  if (!exact(value, keys) || !Array.isArray(value.files)) return false;
  const files: unknown[] = value.files;
  if (!sha256(value.sha256) || files.length > 2000 || !files.every(changedFile) ||
    !safeInteger(value.bytes) || !safeInteger(value.changed_count) ||
    value.changed_count !== files.length) return false;
  const countFields = ["committed_count", "conflict_count", "index_count", "redacted_count",
    "tracked_count", "untracked_count", "worktree_count"] as const;
  if (countFields.some((field) => !safeInteger(value[field]) || Number(value[field]) > files.length)) {
    return false;
  }
  const predicates: Array<[typeof countFields[number], string]> = [
    ["committed_count", "committed"], ["conflict_count", "conflicted"],
    ["index_count", "index_changed"], ["redacted_count", "path_redacted"],
    ["tracked_count", "tracked"], ["untracked_count", "untracked"],
    ["worktree_count", "worktree_changed"],
  ];
  return predicates.every(([count, property]) => value[count] ===
    files.filter((file) => record(file) && file[property] === true).length);
}

function artifact(value: unknown): boolean {
  return exact(value, ["id", "redacted", "sha256", "size_bytes", "stream", "url"]) &&
    identity(value.id) && typeof value.redacted === "boolean" && sha256(value.sha256) &&
    safeInteger(value.size_bytes, 1) && ["stdout", "stderr"].includes(String(value.stream)) &&
    publicLink(value.url);
}

function verification(value: unknown): boolean {
  const required = ["artifacts", "backend", "backend_generation_sha256", "checkpoint_id",
    "conclusion", "current_revision", "environment_sha256",
    "executable_sha256", "job_id", "output_truncated", "permission_revision", "reason_code",
    "retry_count", "revision_sha256", "spec_sha256", "started_at", "state",
    "stderr_observed_bytes", "stderr_sha256", "stdout_observed_bytes", "stdout_sha256",
    "tree_reaped"];
  const optional = ["completed_at", "exit_code"];
  if (!exact(value, required.filter((key) => key !== "started_at"),
    [...optional, "started_at"]) || !identity(value.job_id) ||
    !verificationStatuses.has(String(value.conclusion)) || !jobStates.has(String(value.state)) ||
    !text(value.reason_code, 128) || !sha256(value.spec_sha256) ||
    !sha256(value.executable_sha256) || !sha256(value.environment_sha256) ||
    !safeInteger(value.permission_revision, 1) || !text(value.backend, 128) ||
    !sha256(value.backend_generation_sha256) || !identity(value.checkpoint_id) ||
    !sha256(value.revision_sha256) || typeof value.current_revision !== "boolean" ||
    !safeInteger(value.retry_count) || !sha256(value.stdout_sha256) ||
    !sha256(value.stderr_sha256) || !safeInteger(value.stdout_observed_bytes) ||
    !safeInteger(value.stderr_observed_bytes) || typeof value.output_truncated !== "boolean" ||
    typeof value.tree_reaped !== "boolean" ||
    !Array.isArray(value.artifacts) || value.artifacts.length > 4 ||
    !value.artifacts.every(artifact)) return false;
  const hasStarted = Object.prototype.hasOwnProperty.call(value, "started_at");
  const hasCompleted = Object.prototype.hasOwnProperty.call(value, "completed_at");
  const terminal = !["prepared", "running", "stopping"].includes(String(value.state));
  if ((hasStarted && !date(value.started_at)) || (hasCompleted && (!hasStarted ||
    !date(value.completed_at) || Date.parse(String(value.completed_at)) <
      Date.parse(String(value.started_at)))) || (terminal && !hasCompleted) ||
    (Object.prototype.hasOwnProperty.call(value, "exit_code") &&
      !Number.isSafeInteger(value.exit_code))) return false;
  const artifacts = value.artifacts.filter(record);
  const stdoutComplete = value.stdout_observed_bytes === 0 || artifacts.some((item) =>
    item.stream === "stdout" && item.sha256 === value.stdout_sha256);
  const stderrComplete = value.stderr_observed_bytes === 0 || artifacts.some((item) =>
    item.stream === "stderr" && item.sha256 === value.stderr_sha256);
  if (artifacts.some((item) => (item.stream === "stdout" && item.sha256 !== value.stdout_sha256) ||
    (item.stream === "stderr" && item.sha256 !== value.stderr_sha256))) return false;
  if (value.conclusion === "passed" && (value.state !== "completed" || value.exit_code !== 0 ||
    value.tree_reaped !== true || value.current_revision !== true || value.output_truncated !== false ||
    !stdoutComplete || !stderrComplete)) return false;
  return !((value.output_truncated || !stdoutComplete || !stderrComplete) &&
    !["partial", "stale"].includes(String(value.conclusion))) &&
    verificationReasons[String(value.conclusion)]?.has(String(value.reason_code)) === true;
}

function links(value: unknown): boolean {
  const keys = ["checkpoint", "checkpoint_timeline", "fork", "rewind", "self", "undo"];
  return exact(value, keys) && keys.every((key) => publicLink(value[key]));
}

function safeguards(value: unknown): boolean {
  const keys = ["absolute_paths_exposed", "automatic_commit", "automatic_merge", "automatic_push",
    "private_reasoning_stored", "raw_environment_stored", "raw_output_stored", "source_overwrite"];
  return exact(value, keys) && keys.every((key) => value[key] === false);
}

function observation(value: unknown): boolean {
  if (!exact(value, [], ["observed_at", "reason_code", "revision_sha256"])) return false;
  if (Object.keys(value).length === 0) return true;
  return date(value.observed_at) &&
    (!Object.prototype.hasOwnProperty.call(value, "reason_code") || text(value.reason_code, 128)) &&
    (!Object.prototype.hasOwnProperty.call(value, "revision_sha256") || sha256(value.revision_sha256));
}

export function parseStandardCodeDelivery(value: unknown, runID?: string): StandardCodeDeliveryView {
  const required = ["base_commit", "binding", "created_at", "diff", "event_sequence",
    "final_checkpoint", "head_commit", "id", "links", "operation_key_sha256",
    "protocol_version", "reasons", "receipt_sha256", "receipt_status", "request_fingerprint",
    "safeguards", "status", "uncovered_items", "verifications", "verified"];
  if (!exact(value, required, ["declaration", "observation"]) ||
    value.protocol_version !== "standard_code_delivery.v1" || !identity(value.id) ||
    !sha256(value.operation_key_sha256) || !sha256(value.request_fingerprint) ||
    !statuses.has(String(value.status)) || !statuses.has(String(value.receipt_status)) ||
    value.verified !== (value.status === "passed") ||
    (Object.prototype.hasOwnProperty.call(value, "declaration") &&
      !declarations.has(String(value.declaration))) || !binding(value.binding, runID) ||
    typeof value.base_commit !== "string" || !gitObject.test(value.base_commit) ||
    typeof value.head_commit !== "string" || !gitObject.test(value.head_commit) ||
    !diff(value.diff) || !checkpoint(value.final_checkpoint) ||
    !record(value.final_checkpoint) || value.final_checkpoint.head_commit !== value.head_commit ||
    !Array.isArray(value.verifications) || value.verifications.length > 64 ||
    !value.verifications.every(verification) || !Array.isArray(value.reasons) ||
    value.reasons.length < 1 || value.reasons.length > 64 ||
    !value.reasons.every((reason) => exact(reason, ["code", "provenance_sha256"]) &&
      text(reason.code, 128) && sha256(reason.provenance_sha256)) ||
    !Array.isArray(value.uncovered_items) || value.uncovered_items.length > 64 ||
    !value.uncovered_items.every((item) => exact(item, ["summary", "summary_sha256"]) &&
      text(item.summary, 512) && !containsPrivateMaterial(String(item.summary)) &&
      sha256(item.summary_sha256)) || !links(value.links) || !safeguards(value.safeguards) ||
    !sha256(value.receipt_sha256) || !safeInteger(value.event_sequence, 1) ||
    !date(value.created_at) || (Object.prototype.hasOwnProperty.call(value, "observation") &&
      !observation(value.observation))) {
    throw new Error("Invalid Standard Code delivery truth projection");
  }
  const observed = record(value.observation) ? value.observation : undefined;
  if (observed && Object.keys(observed).length > 0) {
    const current = observed.revision_sha256 === value.final_checkpoint.revision_sha256;
    const reason = Object.prototype.hasOwnProperty.call(observed, "reason_code") ?
      String(observed.reason_code) : "";
    if ((!current && value.status !== "stale") || (current && value.status !== value.receipt_status) ||
      (current && reason !== "") || (!current && reason === "")) {
      throw new Error("Standard Code delivery freshness projection is inconsistent");
    }
  } else if (value.status !== value.receipt_status) {
    throw new Error("Standard Code delivery receipt status changed without an observation");
  }
  if (value.status === "passed" && (value.verifications.length === 0 ||
    value.verifications.some((item) => !record(item) || item.conclusion !== "passed" ||
      item.current_revision !== true || item.output_truncated !== false))) {
    throw new Error("Standard Code delivery passed without current terminal evidence");
  }
  if (Object.prototype.hasOwnProperty.call(value, "declaration") &&
    value.verifications.length !== 0) {
    throw new Error("Standard Code delivery declaration contains verification Jobs");
  }
  return value as unknown as StandardCodeDeliveryView;
}
