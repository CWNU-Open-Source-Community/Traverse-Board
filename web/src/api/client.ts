import { consumeSSE } from "./sse";
import {
  isGitAdvancedExecuteResult,
  isGitAdvancedProjection,
  isGitAdvancedReviewResult,
  validGitAdvancedSpec,
} from "./git-advanced";
import {
  parseGitHubDeviceAuthorization,
  parseGitHubDevicePoll,
  parseGitHubEvidence,
  parseGitHubFetch,
  parseGitHubProjection,
  parseGitHubQualification,
  parseGitHubReviewConfigure,
  parseGitHubReviewConnections,
  parseGitHubReviewCredential,
  parseGitHubWriteExecute,
  parseGitHubWriteReview,
} from "./github-review";
import { parseStandardCodeDelivery } from "./standard-code-delivery";
import type {
  ApprovalDecisionControlRequestView,
  ChildTaskAdmitRequestView,
  DockerSandboxAdmissionRequestView,
  DockerSandboxAdmissionView,
  DockerSandboxCancelRequestView,
  DockerSandboxCancellationView,
  DockerSandboxStartRequestView,
  DockerSandboxStatusView,
  PriceSnapshotImportRequestView,
  PriceSnapshotImportView,
  PriceSnapshotListView,
  ChildTaskProposalsListView,
  ChildTaskProposalView,
  ChildTaskReviewRequestView,
  BatchDeliveriesListView,
  BatchDeliverySnapshotView,
  BatchDeliveryReviewRequestView,
  BatchDeliveryReviewControlView,
  BatchDeliveryMergeRequestView,
  BatchDeliveryMergeControlView,
  BatchDeliveryCancelRequestView,
  BatchDeliveryCancelView,
  BatchDeliveryReconcileRequestView,
  BatchDeliveryReconcileView,
  FanoutExecutionCancelRequestView,
  FanoutExecutionsListView,
  FanoutExecutionView,
  ApprovalDecisionControlView,
  ApprovalQueueView,
  ArtifactView,
  AvailableModelRouteCollectionView,
  BrowserSafeWebReadiness,
  CodeHandoffView,
  CodeHandoffExportView,
  CodeIntelInventoryView,
  ControlledCommandProposalReviewRequestView,
  ControlledCommandProposalView,
  HostCommandProposalReviewRequestView,
  HostCommandProposalView,
  ErrorEnvelope,
  ExtensionInventoryView,
  ExtensionMCPReviewRequestView,
  ExtensionMCPServerView,
  ExtensionPluginInstallationView,
  ExtensionPluginReviewRequestView,
  EvidenceAttachmentRequestView,
  EvidenceAttachmentView,
  EvidenceInventoryView,
  ExternalSkillProjectionView,
  FileEditApplyRequestView,
  FileEditApplyView,
  FileEditChangeSetView,
  FileEditProposalRequestView,
  FileEditProposalRecoveryView,
  FileEditProposalSourceView,
  FileEditProposalView,
  FileEditPreviewView,
  FileEditQueueView,
  FileEditReviewRequestView,
  FileEditReviewView,
  FullCDPSessionCloseRequestView,
  FullCDPSessionControlView,
  FullCDPSessionOpenRequestView,
  FullCDPSessionView,
  HealthView,
  GitAdvancedExecuteResultView,
  GitAdvancedProjectionView,
  GitAdvancedReviewResultView,
  GitAdvancedScopeView,
  GitAdvancedSpecView,
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
  GitHubReviewWriteSpecView,
  ModelAvailabilityView,
  ModelHarnessAvailabilityView,
  ModelHarnessQualificationRequestView,
  ModelHarnessQualificationView,
  ModelRouteControlRequestView,
  ModelCancellationRequestView,
  ModelCancellationView,
  PublicModelStreamSnapshot,
  SpecialistModelCancellationView,
  NoteView,
  OperationReceiptView,
  OperationReceiptHistoryView,
  OperatorActionCenterView,
  Page,
  PageResult,
  PlanModeTransitionControlRequestView,
  PlanModeTransitionControlView,
  PlanDeliveryTransitionControlRequestView,
  PlanDeliveryTransitionControlView,
  PlanDirectionControlRequestView,
  PlanDirectionControlView,
  ProviderDiagnosticRequestView,
  ProviderDiagnosticView,
  ProviderCredentialListView,
  ProviderCredentialRequestView,
  ProviderCredentialStatusView,
  ProviderDefinitionCollectionView,
  ProviderDefinitionDeleteRequestView,
  ProviderDefinitionMutationView,
  ProviderDefinitionUpsertRequestView,
  ProviderDefinitionView,
  ProviderSearchReadinessView,
  RepositoryStateView,
  RepositoryDiffView,
  RepositoryHistoryView,
  RepositoryFileHistoryView,
  RepositoryCommitDetailView,
  RepositoryCommitComparisonView,
  RepositoryCommitFilePreviewView,
  RunCreationControlRequestView,
  RunCreationControlView,
  RunCapabilityReadinessView,
  StandardCodePresetControlRequestView,
  StandardCodePresetControlView,
  StandardCodeDeliveryRecordRequestView,
  StandardCodeDeliveryRecordResultView,
  StandardCodeDeliveryView,
  RunExecutionControlRequestView,
  RunExecutionControlView,
  RunLifecycleControlRequestView,
  RunLifecycleControlView,
  RunNetworkAuthorityControlRequestView,
  RunNetworkAuthorityControlView,
  RunWakeCancelRequestView,
  RunWakeControlView,
  RunWakeScheduleRequestView,
  RunWakeStateView,
  RunWakeExecutionRequestView,
  RunWakeExecutionView,
  RuntimeCapabilitiesView,
  ThreadCreationControlRequestView,
  ThreadCreationControlView,
  ThreadMessageControlRequestView,
  ThreadMessageControlView,
  ThreadLifecycleControlRequestView,
  ThreadLifecycleControlView,
  ThreadRunRecoveryControlRequestView,
  ThreadRunRecoveryControlView,
  ThreadModelRouteControlRequestView,
  ThreadModelRouteView,
  ThreadExecutionPermissionControlRequestView,
  ThreadExecutionPermissionControlView,
  ThreadActivityArtifactView,
  ThreadActivityDetailView,
  ThreadTranscriptItemView,
  ThreadView,
  ScheduledJobControlView,
  ScheduledJobCreateRequestView,
  ScheduledJobDetailView,
  ScheduledJobListView,
  ScheduledJobTransitionRequestView,
  ScheduledJobView,
  DiagnosticBundleView,
  UIEvidenceArtifactMetadata,
  UIEvidenceAttempt,
  UIEvidenceBundle,
  UIEvidenceStartView,
  SkillPackageInstallRequestView,
  SkillPackageInstallView,
  RunEventPollView,
  RunEventStreamView,
  SessionMessageControlRequestView,
  SessionMessageControlView,
  SessionArchiveControlRequestView,
  SessionArchiveControlView,
  SessionSteeringCancellationRequestView,
  SessionSteeringCancellationView,
  SuccessEnvelope,
  WorkspaceExplorerView,
  WorkspaceSearchView,
  WorkItemView,
  VerificationEvidenceControlView,
  VerificationEvidenceInventoryView,
  VerificationEvidenceRequestView,
  VerificationPlanControlView,
  VerificationPlanInventoryView,
  VerificationPlanRequestView,
  VerificationAssociationRequestView,
  VerificationAssociationControlView,
  VerificationPlanCoverageInventoryView,
  VerificationPlanItemCoverageDetailView,
  VerificationPlanItemCoveragePage,
  VerificationSnapshotExportView,
  VerificationSnapshotReceiptRequestView,
  VerificationSnapshotReceiptView,
  VerificationSnapshotReceiptControlView,
  VerificationSnapshotReceiptInventoryView,
  VerificationSnapshotReceiptReviewRequestView,
  VerificationSnapshotReceiptReviewView,
  VerificationSnapshotReceiptReviewControlView,
  VerificationSnapshotReceiptReviewInventoryView,
  EmbeddedAnalyzerExecutionRequestView,
  EmbeddedAnalyzerExecutionControlView,
} from "./types";

export type QueryValue = boolean | number | string | undefined;

export interface ClientCapabilities {
  runControlEnabled?: boolean;
  executionPermissionControlEnabled?: boolean;
  workspaceSandboxEnabled?: boolean;
  browserCDPPermissionControlEnabled?: boolean;
  fullCDPDebugEnabled?: boolean;
  fullCDPSessionControlEnabled?: boolean;
  operatorApprovalEnabled?: boolean;
  dangerFullAccessEnabled?: boolean;
  debugMaximumAccessEnabled?: boolean;
  commandRuntimeEnabled?: boolean;
  commandRuntimeProtocolAvailable?: boolean;
  commandRuntimeAdapterInstalled?: boolean;
  commandRuntimeAdapterReady?: boolean;
  runCreationEnabled?: boolean;
  standardCodePresetEnabled?: boolean;
  sessionMessageEnabled?: boolean;
  threadControlEnabled?: boolean;
  sessionSteeringControlEnabled?: boolean;
  runLifecycleEnabled?: boolean;
  runExecutionEnabled?: boolean;
  planDeliveryControlEnabled?: boolean;
  approvalControlEnabled?: boolean;
  controlledCommandProposalControlEnabled?: boolean;
  hostCommandProposalControlEnabled?: boolean;
  modelControlEnabled?: boolean;
  providerCredentialEnabled?: boolean;
  fileEditReviewEnabled?: boolean;
  fileEditProposalEnabled?: boolean;
  fileEditApplyEnabled?: boolean;
  runWakeControlEnabled?: boolean;
  runWakeExecutionEnabled?: boolean;
  runWakeWorkerEnabled?: boolean;
  scheduledJobControlEnabled?: boolean;
  scheduledJobWorkerEnabled?: boolean;
  skillInstallationEnabled?: boolean;
  evidenceAttachmentEnabled?: boolean;
  verificationEvidenceEnabled?: boolean;
  uiEvidenceControlEnabled?: boolean;
  embeddedAnalyzerExecutionEnabled?: boolean;
  workspaceCheckpointControlEnabled?: boolean;
  gitAdvancedControlEnabled?: boolean;
  githubReviewControlEnabled?: boolean;
  batchDeliveryControlEnabled?: boolean;
  batchDeliveryHostValidationEnabled?: boolean;
  extensionControlEnabled?: boolean;
  dockerExecutionEnabled?: boolean;
  agentCodeToolsEnabled?: boolean;
  codeIntelEnabled?: boolean;
}

export class APIRequestError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly status: number,
    readonly requestID = "",
  ) {
    super(message);
    this.name = "APIRequestError";
  }
}

function normalizeBaseURL(baseURL: string): string {
  const resolved = new URL(baseURL, window.location.origin);
  if (resolved.origin !== window.location.origin) {
    throw new Error("CyberAgent API must use the current browser origin");
  }
  const path = resolved.pathname.replace(/\/+$/, "");
  if (path !== "/api/v1") {
    throw new Error("CyberAgent API base path must be /api/v1");
  }
  return path;
}

function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const candidate = value as Partial<ErrorEnvelope>;
  return candidate.version === "api.v1" && typeof candidate.request_id === "string" &&
    typeof candidate.error?.code === "string" && typeof candidate.error.message === "string";
}

function isSuccessEnvelope<T>(value: unknown): value is SuccessEnvelope<T> {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const candidate = value as Partial<SuccessEnvelope<T>>;
  return candidate.version === "api.v1" && typeof candidate.request_id === "string" &&
    Object.prototype.hasOwnProperty.call(candidate, "data");
}

function parseStreamFrame(value: unknown, expectedRunID: string, expectedRequestID = ""): RunEventStreamView {
  if (typeof value !== "object" || value === null) {
    throw new Error("SSE frame is not an object");
  }
  const frame = value as Partial<RunEventStreamView>;
  if (frame.version !== "run-events.v1" || frame.run_id !== expectedRunID ||
    typeof frame.request_id !== "string" || frame.request_id === "" ||
    (expectedRequestID !== "" && frame.request_id !== expectedRequestID) ||
    typeof frame.cursor !== "string" || frame.cursor === "" || frame.cursor.length > 512 ||
    typeof frame.sequence !== "number" || !Number.isSafeInteger(frame.sequence) || frame.sequence <= 0 ||
    typeof frame.event !== "object" || frame.event === null ||
    frame.event.version !== "v1" || frame.event.run_id !== expectedRunID ||
    frame.event.sequence !== frame.sequence || typeof frame.event.event_id !== "string" ||
    frame.event.event_id === "" || typeof frame.event.mission_id !== "string" ||
    typeof frame.event.type !== "string" || frame.event.type === "" ||
    typeof frame.event.source !== "string" || frame.event.source === "" ||
    typeof frame.event.created_at !== "string") {
    throw new Error("SSE frame does not match run-events.v1");
  }
  return frame as RunEventStreamView;
}

function parseEventPoll(value: unknown, expectedRunID: string, requestID: string): RunEventPollView {
  if (typeof value !== "object" || value === null) {
    throw new Error("Event poll response is not an object");
  }
  const poll = value as Partial<RunEventPollView>;
  if (poll.version !== "run-event-poll.v1" || poll.run_id !== expectedRunID ||
    typeof poll.cursor !== "string" || poll.cursor === "" || poll.cursor.length > 512 ||
    !Array.isArray(poll.frames) || typeof poll.has_more !== "boolean" ||
    (poll.has_more && poll.frames.length === 0) || poll.frames.length > 100) {
    throw new Error("Event poll response does not match run-event-poll.v1");
  }
  const frames = poll.frames.map((frame) => parseStreamFrame(frame, expectedRunID, requestID));
  for (let index = 1; index < frames.length; index++) {
    if (frames[index]!.sequence !== frames[index - 1]!.sequence + 1) {
      throw new Error("Event poll response contains a sequence gap");
    }
  }
  if (frames.length > 0 && frames[frames.length - 1]!.cursor !== poll.cursor) {
    throw new Error("Event poll response cursor does not match its final frame");
  }
  return { ...poll, frames } as RunEventPollView;
}

function parseRunCreationControl(value: unknown,
  request: RunCreationControlRequestView, requestedModelRoute = ""): RunCreationControlView {
  if (!hasExactKeys(value, ["mission", "mode", "replayed", "run", "session"]) ||
    typeof value.replayed !== "boolean" || !isRecord(value.mission) ||
    !isRecord(value.mode) || !isRecord(value.run) || !isRecord(value.session) ||
    !isRecord(value.run.config) || !isRecord(value.run.budget) || !isRecord(value.mission.scope) ||
    !isRecord(value.mode.scope)) {
    throw new APIRequestError("Run creation response is invalid", "INVALID_RESPONSE", 502);
  }
  const missionID = boundedIdentity(value.mission.id);
  const runID = boundedIdentity(value.run.id);
  const sessionID = boundedIdentity(value.session.id);
  const workspaceID = boundedIdentity(value.mission.workspace_id);
  const expectedProfile = request.profile ?? "code";
  const expectedModelRoute = requestedModelRoute || expectedProfile;
  const expectedSurface = request.surface ?? "code";
  const expectedPhase = request.phase ?? "deliver";
  const expectedNetwork = normalizeRequestedNetworkAuthority(request);
  if (!missionID || !runID || !sessionID || !workspaceID ||
    workspaceID !== request.workspace_id || value.mission.goal !== request.goal ||
    value.run.mission_id !== missionID || value.run.session_id !== sessionID ||
    value.session.workspace_id !== workspaceID ||
    value.mission.scope.workspace_id !== workspaceID || value.mode.scope.workspace_id !== workspaceID ||
    value.run.status !== "created" || value.session.status !== "active" ||
    value.run.config.interactive !== true || value.mission.profile !== expectedProfile ||
    value.mode.profile !== expectedProfile ||
    value.run.config.model_route !== expectedModelRoute || value.session.route !== expectedModelRoute ||
    value.session.title !== value.mission.goal ||
    !scopeMatchesRequestedNetworkAuthority(value.mission.scope, expectedNetwork) ||
    !scopeMatchesRequestedNetworkAuthority(value.mode.scope, expectedNetwork) ||
    value.run.budget.max_turns !== 100 || value.run.budget.max_tool_calls !== 100 ||
    (value.run.budget.max_tokens ?? 0) !== 0 || (value.run.budget.max_cost_usd ?? 0) !== 0 ||
    (value.run.budget.timeout_seconds ?? 0) !== 0 ||
    value.mode.capability_grant !== false || value.mode.protocol_version !== "run_mode.v1" ||
    value.mode.policy_version !== "mode_policy.v1" || value.mode.revision !== 1 ||
    value.mode.surface !== expectedSurface || value.mode.phase !== expectedPhase) {
    throw new APIRequestError("Run creation response violated its closed authority contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as RunCreationControlView;
}

function parseThreadView(value: unknown, expectedThreadID = ""): ThreadView {
  const required = ["composer_state", "created_at", "id", "last_run_id", "mission_id",
    "protocol_version", "status", "title", "updated_at", "version"];
  const optional = ["active_run_id", "archived_at", "deleted_at", "workspace_id"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "thread.v1" || !boundedIdentity(value.id) ||
    (expectedThreadID !== "" && value.id !== expectedThreadID) ||
    !boundedIdentity(value.mission_id) || !boundedIdentity(value.last_run_id) ||
    !boundedText(value.title, 4_096) || !safePositiveInteger(value.version) ||
    !validDate(value.created_at) || !validDate(value.updated_at) ||
    (value.workspace_id !== undefined && !boundedIdentity(value.workspace_id)) ||
    (value.active_run_id !== undefined && !boundedIdentity(value.active_run_id)) ||
    (value.archived_at !== undefined && !validDate(value.archived_at)) ||
    (value.deleted_at !== undefined && !validDate(value.deleted_at)) ||
    !["active", "archived", "deleted"].includes(String(value.status)) ||
    !["ready", "waiting_approval", "successor_required", "unavailable"]
      .includes(String(value.composer_state)) ||
    Date.parse(String(value.updated_at)) < Date.parse(String(value.created_at))) {
    throw new APIRequestError("Thread response is invalid", "INVALID_RESPONSE", 502);
  }
  if ((value.active_run_id !== undefined && value.active_run_id !== value.last_run_id) ||
    (value.status === "active" && (value.archived_at !== undefined ||
      value.deleted_at !== undefined ||
      (value.active_run_id === undefined && value.composer_state !== "successor_required") ||
      (value.active_run_id !== undefined && value.composer_state !== "ready" &&
        value.composer_state !== "waiting_approval"))) ||
    (value.status === "archived" && (!validDate(value.archived_at) ||
      value.deleted_at !== undefined || value.composer_state !== "unavailable")) ||
    (value.status === "deleted" && (!validDate(value.archived_at) ||
      !validDate(value.deleted_at) || value.composer_state !== "unavailable"))) {
    throw new APIRequestError("Thread response violated its lifecycle projection",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ThreadView;
}

function parseThreadCreationControl(value: unknown,
  request: ThreadCreationControlRequestView): ThreadCreationControlView {
  if (!hasExactKeys(value, ["mission", "mode", "replayed", "run", "session", "thread"])) {
    throw new APIRequestError("Thread creation response is invalid", "INVALID_RESPONSE", 502);
  }
  const requestedModelRoute = request.provider && request.model
    ? `${request.provider}/${request.model}` : "";
  const runCreation = parseRunCreationControl({ mission: value.mission, mode: value.mode,
    replayed: value.replayed, run: value.run, session: value.session }, {
    version: "run_creation.v1", goal: request.goal, workspace_id: request.workspace_id,
    profile: request.profile, surface: request.surface, phase: request.phase,
    network_mode: request.network_mode, allowed_targets: request.allowed_targets,
  }, requestedModelRoute);
  const thread = parseThreadView(value.thread);
  if (thread.status !== "active" || thread.workspace_id !== request.workspace_id ||
    thread.mission_id !== runCreation.mission.id || thread.title !== request.goal ||
    thread.active_run_id !== runCreation.run.id || thread.last_run_id !== runCreation.run.id ||
    thread.composer_state !== "ready") {
    throw new APIRequestError("Thread creation response violated its identity binding",
      "INVALID_RESPONSE", 502);
  }
  return { ...runCreation, thread } as ThreadCreationControlView;
}

function isValidOperatorSteeringMessage(value: unknown): value is Record<string, unknown> {
  if (!isRecord(value) || !hasOnlyKeys(value,
    ["cancelled_at", "committed_at", "created_at", "id", "prepared", "sequence", "status"]) ||
    !boundedIdentity(value.id) || value.prepared !== false ||
    !safePositiveInteger(value.sequence) || !validDate(value.created_at) ||
    !["pending", "committed", "cancelled"].includes(String(value.status))) {
    return false;
  }
  const committedAt = value.committed_at;
  const cancelledAt = value.cancelled_at;
  return (committedAt === undefined || validDate(committedAt)) &&
    (cancelledAt === undefined || validDate(cancelledAt)) &&
    (value.status !== "pending" || (committedAt === undefined && cancelledAt === undefined)) &&
    (value.status !== "committed" || (committedAt !== undefined && cancelledAt === undefined)) &&
    (value.status !== "cancelled" || (cancelledAt !== undefined && committedAt === undefined));
}

function parseThreadMessageControl(value: unknown, expectedThreadID: string,
  executionAllowed = false): ThreadMessageControlView {
  const required = ["capability_grant", "execution_started", "model_called", "replayed",
    "run_id", "session_id", "steering", "successor_created", "thread", "tool_called", "version"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, "predecessor_run_id"]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.version !== "thread_message_submission.v1" || !boundedIdentity(value.run_id) ||
    !boundedIdentity(value.session_id) || typeof value.successor_created !== "boolean" ||
    typeof value.replayed !== "boolean" || typeof value.execution_started !== "boolean" ||
    typeof value.model_called !== "boolean" || typeof value.tool_called !== "boolean" ||
    value.capability_grant !== false ||
    !isValidOperatorSteeringMessage(value.steering) ||
    (value.predecessor_run_id !== undefined && !boundedIdentity(value.predecessor_run_id))) {
    throw new APIRequestError("Thread message response is invalid", "INVALID_RESPONSE", 502);
  }
  if ((!executionAllowed && (value.execution_started || value.model_called || value.tool_called)) ||
    ((value.model_called || value.tool_called) && !value.execution_started)) {
    throw new APIRequestError("Thread message response is invalid at its execution boundary",
      "INVALID_RESPONSE", 502);
  }
  const thread = parseThreadView(value.thread, expectedThreadID);
  if (thread.status !== "active" || thread.last_run_id !== value.run_id ||
    (thread.active_run_id !== undefined && thread.active_run_id !== value.run_id) ||
    (value.successor_created && (value.predecessor_run_id === undefined ||
      value.predecessor_run_id === value.run_id)) ||
    (!value.successor_created && value.predecessor_run_id !== undefined)) {
    throw new APIRequestError("Thread message response violated its continuation contract",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, thread } as unknown as ThreadMessageControlView;
}

function parseThreadLifecycleControl(value: unknown, expectedThreadID: string,
  action: "archive" | "restore" | "delete", expectedVersion: number): ThreadLifecycleControlView {
  if (!hasExactKeys(value, ["capability_grant", "thread", "version"]) ||
    value.version !== "thread_lifecycle.v1" || value.capability_grant !== false) {
    throw new APIRequestError("Thread lifecycle response is invalid", "INVALID_RESPONSE", 502);
  }
  const thread = parseThreadView(value.thread, expectedThreadID);
  const expectedStatus = action === "archive" ? "archived" : action === "restore" ? "active" : "deleted";
  if (thread.status !== expectedStatus || thread.version < expectedVersion) {
    throw new APIRequestError("Thread lifecycle response violated its transition contract",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, thread } as unknown as ThreadLifecycleControlView;
}

function parseThreadRunRecoveryControl(value: unknown, expectedThreadID: string,
  request: ThreadRunRecoveryControlRequestView): ThreadRunRecoveryControlView {
  if (!hasExactKeys(value, ["failed_run", "replayed", "successor_required", "thread", "version"]) ||
    value.version !== "thread_run_recovery.v1" || typeof value.replayed !== "boolean" ||
    value.successor_required !== true || !isRecord(value.failed_run) ||
    !hasOnlyKeys(value.failed_run, ["budget", "config", "created_at", "finished_at", "id",
      "mission_id", "session_id", "started_at", "status", "updated_at"]) ||
    value.failed_run.id !== request.run_id || !boundedIdentity(value.failed_run.id) ||
    !boundedIdentity(value.failed_run.mission_id) ||
    (value.failed_run.session_id !== undefined && !boundedIdentity(value.failed_run.session_id)) ||
    value.failed_run.status !== "failed" || !validDate(value.failed_run.created_at) ||
    !validDate(value.failed_run.updated_at) || !validDate(value.failed_run.finished_at) ||
    !isRecord(value.failed_run.config) || !isRecord(value.failed_run.budget)) {
    throw new APIRequestError("Thread Run recovery response is invalid", "INVALID_RESPONSE", 502);
  }
  const thread = parseThreadView(value.thread, expectedThreadID);
  if (thread.status !== "active" || thread.active_run_id !== undefined ||
    thread.last_run_id !== request.run_id || thread.composer_state !== "successor_required") {
    throw new APIRequestError("Thread Run recovery widened its continuation contract",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, thread } as unknown as ThreadRunRecoveryControlView;
}

const threadTranscriptTypes = ["message", "search", "read", "edit", "execute", "verify",
  "approval", "checkpoint", "delivery"];
const threadTranscriptStages = ["started", "arguments_ready", "running", "result", "blocked"];
const threadTranscriptKinds = ["harness_status", "model_update", "operator_input", "model_call",
  "tool_call", "approval", "file_change", "plan", "dependency", "browser"];
const threadTranscriptSources = ["harness", "model", "operator"];

function validWebEvidenceURL(value: unknown): value is string {
  if (typeof value !== "string" || value.length === 0 || value.length > 4_096 ||
    /[\u0000-\u001f\u007f]/u.test(value)) return false;
  try {
    const parsed = new URL(value);
    return parsed.protocol === "https:" && parsed.hostname.length > 0 &&
      parsed.username === "" && parsed.password === "" &&
      (parsed.port === "" || parsed.port === "443") && parsed.hash === "";
  } catch {
    return false;
  }
}

function validThreadWebEvidence(value: unknown): boolean {
  const required = ["citeable", "digest", "fetched_at", "instruction_authorized", "partial",
    "snapshot_id", "source_id", "stale", "stale_at", "state", "untrusted", "url", "version"];
  const optional = ["citation_id", "title"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.version !== "web_evidence_presentation.v1" || !boundedIdentity(value.source_id) ||
    !boundedIdentity(value.snapshot_id) ||
    (value.citation_id !== undefined && !boundedIdentity(value.citation_id)) ||
    !validWebEvidenceURL(value.url) ||
    (value.title !== undefined && !boundedText(value.title, 1_024)) ||
    !isSHA256(value.digest) || !validDate(value.fetched_at) || !validDate(value.stale_at) ||
    Date.parse(String(value.stale_at)) < Date.parse(String(value.fetched_at)) ||
    typeof value.partial !== "boolean" || typeof value.stale !== "boolean" ||
    typeof value.citeable !== "boolean" || value.untrusted !== true ||
    value.instruction_authorized !== false) return false;
  switch (value.state) {
    case "fetched":
      return !value.partial && !value.stale && value.citeable;
    case "partial":
      return value.partial && !value.stale && value.citeable;
    case "stale":
      return value.stale && value.citeable;
    case "blocked":
    case "failed":
      return !value.partial && !value.stale && !value.citeable;
    default:
      return false;
  }
}

function parseThreadTranscriptItem(value: unknown): ThreadTranscriptItemView {
  const required = ["activity_type", "canonical_id", "created_at", "durable", "id",
    "instruction_authorized", "kind", "provisional", "run_id",
    "run_ordinal", "sequence", "source", "stage", "title", "verifiable", "version"];
  const optional = ["activity_detail_ref", "activity_summary", "attempt_id", "boundary_reason", "detail",
    "detail_available", "durable_call_id",
    "model_attempt", "position", "source_ref", "status", "stream_call_id", "stream_item_id",
    "stream_response_id", "tool_name", "tool_round", "web_evidence"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.version !== "thread_transcript.v1" || !boundedIdentity(value.id) ||
    !boundedIdentity(value.canonical_id) || !boundedIdentity(value.run_id) ||
    !safePositiveInteger(value.run_ordinal) ||
    !safeBoundedCount(value.sequence, Number.MAX_SAFE_INTEGER) ||
    !threadTranscriptTypes.includes(String(value.activity_type)) ||
    !threadTranscriptStages.includes(String(value.stage)) ||
    !threadTranscriptKinds.includes(String(value.kind)) ||
    !threadTranscriptSources.includes(String(value.source)) ||
    !boundedText(value.title, 512) || !validDate(value.created_at) ||
    typeof value.verifiable !== "boolean" || typeof value.instruction_authorized !== "boolean" ||
    (value.detail_available !== undefined && value.detail_available !== true) ||
    value.provisional !== false || value.durable !== true) {
    throw new APIRequestError("Thread transcript item is invalid", "INVALID_RESPONSE", 502);
  }
  for (const field of ["activity_detail_ref", "attempt_id", "boundary_reason", "durable_call_id", "source_ref", "status",
    "stream_call_id", "stream_item_id", "stream_response_id", "tool_name"]) {
    if (value[field] !== undefined && !boundedIdentity(value[field])) {
      throw new APIRequestError("Thread transcript identity is invalid", "INVALID_RESPONSE", 502);
    }
  }
  if (value.detail !== undefined && (typeof value.detail !== "string" ||
    value.detail.length === 0 || value.detail.length > 16_384 || value.detail.includes("\0")) ||
    value.position !== undefined && !safePositiveInteger(value.position) ||
    value.model_attempt !== undefined && !safePositiveInteger(value.model_attempt) ||
    value.tool_round !== undefined && !safeBoundedCount(value.tool_round, 100)) {
    throw new APIRequestError("Thread transcript metadata is invalid", "INVALID_RESPONSE", 502);
  }
  if ((value.sequence === 0) !== (value.boundary_reason !== undefined) ||
    (value.stream_item_id !== undefined && value.canonical_id !== value.stream_item_id) ||
    ((value.detail_available === true) !== (value.activity_detail_ref !== undefined)) ||
    (value.detail_available === true && value.activity_detail_ref !== value.durable_call_id) ||
    (value.source === "harness" && value.verifiable !== true) ||
    (value.source === "model" && value.verifiable !== false)) {
    throw new APIRequestError("Thread transcript provenance is invalid", "INVALID_RESPONSE", 502);
  }
  if (value.activity_summary !== undefined) {
    const summary = value.activity_summary;
    const requiredSummary = ["activity_ref", "command", "command_count",
      "duration_milliseconds", "status", "version"];
    if (value.tool_name !== "command_runtime" || !isRecord(summary) ||
      !hasOnlyKeys(summary, [...requiredSummary, "exit_code"]) ||
      requiredSummary.some((key) => !Object.prototype.hasOwnProperty.call(summary, key)) ||
      summary.version !== "thread_activity_summary.v1" ||
      summary.activity_ref !== value.activity_detail_ref ||
      !validThreadActivityString(summary.command, 4_096) ||
      !threadActivityCommandStatuses.includes(String(summary.status)) ||
      !safePositiveInteger(summary.command_count) || summary.command_count > 32 ||
      !safeBoundedCount(summary.duration_milliseconds, Number.MAX_SAFE_INTEGER) ||
      (summary.exit_code !== undefined && (typeof summary.exit_code !== "number" ||
        !Number.isSafeInteger(summary.exit_code)))) {
      throw new APIRequestError("Thread activity summary is invalid", "INVALID_RESPONSE", 502);
    }
  }
  if (value.web_evidence !== undefined && (!validThreadWebEvidence(value.web_evidence) ||
    value.kind !== "tool_call" || value.source !== "harness" || value.stage !== "result" ||
    value.verifiable !== true || value.instruction_authorized !== false ||
    !["web_fetch", "web_citation"].includes(String(value.tool_name)) ||
    (value.tool_name === "web_fetch" &&
      isRecord(value.web_evidence) && value.web_evidence.citation_id !== undefined) ||
    (value.tool_name === "web_citation" &&
      (!isRecord(value.web_evidence) || !boundedIdentity(value.web_evidence.citation_id))))) {
    throw new APIRequestError("Thread Web evidence is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ThreadTranscriptItemView;
}

function parseThreadTranscriptItems(values: unknown[]): ThreadTranscriptItemView[] {
  const items = values.map(parseThreadTranscriptItem);
  for (let index = 1; index < items.length; index++) {
    const left = items[index - 1];
    const right = items[index];
    if (right.run_ordinal < left.run_ordinal ||
      (right.run_ordinal === left.run_ordinal && right.sequence < left.sequence) ||
      (right.run_ordinal === left.run_ordinal && right.sequence === left.sequence &&
        (right.position ?? 0) < (left.position ?? 0))) {
      throw new APIRequestError("Thread transcript page is out of order", "INVALID_RESPONSE", 502);
    }
  }
  return items;
}

function parseSessionMessageControl(value: unknown,
  expectedSessionID: string): SessionMessageControlView {
  if (!hasExactKeys(value, ["capability_grant", "execution_started", "model_called", "replayed",
    "run_id", "session_id", "steering", "tool_called", "version"]) ||
    value.version !== "session_message_submission.v1" ||
    value.session_id !== expectedSessionID || !boundedIdentity(value.run_id) ||
    !boundedIdentity(value.session_id) || typeof value.replayed !== "boolean" ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false ||
    !isValidOperatorSteeringMessage(value.steering)) {
    throw new APIRequestError("Run message response is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as SessionMessageControlView;
}

function parseSessionArchiveControl(value: unknown,
  expectedSessionID: string): SessionArchiveControlView {
  if (!hasExactKeys(value, ["replayed", "session_id", "status", "version"]) ||
    value.version !== "session_archive.v1" || value.session_id !== expectedSessionID ||
    !boundedIdentity(value.session_id) || value.status !== "archived" ||
    typeof value.replayed !== "boolean") {
    throw new APIRequestError("Run-local Session archive response is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as SessionArchiveControlView;
}

function parseSessionSteeringCancellation(value: unknown,
  expectedSessionID: string, expectedMessageID: string): SessionSteeringCancellationView {
  if (!hasExactKeys(value, ["cancellation_id", "cancellation_kind", "capability_grant",
    "execution_started", "model_called", "replayed", "run_id", "session_id", "steering",
    "tool_called", "version"]) ||
    value.version !== "session_steering_cancellation.v1" ||
    value.session_id !== expectedSessionID || !boundedIdentity(value.run_id) ||
    !boundedIdentity(value.session_id) || !boundedIdentity(value.cancellation_id) ||
    value.cancellation_kind !== "operator" || typeof value.replayed !== "boolean" ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false ||
    !isRecord(value.steering) || !hasOnlyKeys(value.steering,
      ["cancelled_at", "committed_at", "created_at", "id", "prepared", "sequence", "status"]) ||
    value.steering.id !== expectedMessageID || value.steering.status !== "cancelled" ||
    value.steering.prepared !== false ||
    typeof value.steering.sequence !== "number" ||
    !Number.isSafeInteger(value.steering.sequence) || value.steering.sequence <= 0 ||
    typeof value.steering.created_at !== "string" ||
    !Number.isFinite(Date.parse(value.steering.created_at)) ||
    typeof value.steering.cancelled_at !== "string" ||
    !Number.isFinite(Date.parse(value.steering.cancelled_at)) ||
    value.steering.committed_at !== undefined) {
    throw new APIRequestError("Run-local Session steering cancellation response is invalid",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as SessionSteeringCancellationView;
}

function parseRunLifecycleControl(value: unknown, expectedRunID: string,
  request: RunLifecycleControlRequestView): RunLifecycleControlView {
  if (!hasExactKeys(value, ["action", "applied_status", "capability_grant",
    "event_sequence_end", "event_sequence_start", "execution_started", "expected_status",
    "model_called", "replayed", "run", "tool_called", "version"]) ||
    value.version !== "run_lifecycle_control.v1" || value.action !== request.action ||
    !isRecord(value.run) || value.run.id !== expectedRunID || !boundedIdentity(value.run.id) ||
    typeof value.run.status !== "string" || typeof value.replayed !== "boolean" ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false ||
    !safePositiveInteger(value.event_sequence_start) ||
    !safePositiveInteger(value.event_sequence_end)) {
    throw new APIRequestError("Run lifecycle response is invalid", "INVALID_RESPONSE", 502);
  }
  const transitions = {
    start: ["created", "running", 2],
    pause: ["running", "paused", 1],
    resume: ["paused", "running", 1],
  } as const;
  const transition = transitions[request.action];
  if (!transition || value.expected_status !== transition[0] ||
    value.applied_status !== transition[1] ||
    (!value.replayed && value.run.status !== transition[1]) ||
    (value.replayed && !isRunStatus(value.run.status)) ||
    value.event_sequence_end - value.event_sequence_start + 1 !== transition[2]) {
    throw new APIRequestError("Run lifecycle response violated its transition contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as RunLifecycleControlView;
}

function isModelCancellationStatus(value: unknown): boolean {
  return value === "pending" || value === "observed" || value === "resolved";
}

function parseModelCancellation(value: unknown, expectedRunID: string,
  request: ModelCancellationRequestView): ModelCancellationView {
  if (!hasExactKeys(value, ["attempt_id", "id", "model_attempt", "replayed",
    "requested_at", "run_id", "status"]) ||
    !boundedIdentity(value.id) || value.run_id !== expectedRunID ||
    !boundedIdentity(value.run_id) || value.attempt_id !== request.attempt_id ||
    !boundedIdentity(value.attempt_id) ||
    !safePositiveInteger(value.model_attempt) ||
    value.model_attempt !== request.model_attempt ||
    typeof value.replayed !== "boolean" || !validDate(value.requested_at) ||
    !isModelCancellationStatus(value.status)) {
    throw new APIRequestError("Model cancellation response is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ModelCancellationView;
}

function parsePublicModelStream(value: unknown,
  expectedRunID: string): PublicModelStreamSnapshot {
  const callKeys = ["attempt_id", "cancel_requested", "max_attempts", "model",
    "model_attempt", "protocol_repair", "provider", "run_id", "session_id",
    "started_at", "stream_bytes", "stream_chunks", "tool_round", "transport_attempt"];
  const required = ["call", "content_kind", "event_sequence", "items", "message_complete",
    "provisional", "revision", "text", "updated_at", "version"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, "response_id"]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.version !== "model_public_stream.v3" || value.provisional !== true ||
    !["root_message", "tool_commentary"].includes(String(value.content_kind)) ||
    typeof value.message_complete !== "boolean" || !safePositiveInteger(value.revision) ||
    typeof value.text !== "string" ||
    new TextEncoder().encode(value.text).byteLength > 64 * 1024 ||
    !safeBoundedCount(value.event_sequence, 16 * 1024) || !Array.isArray(value.items) ||
    value.items.length > 64 || !validDate(value.updated_at) ||
    !hasExactKeys(value.call, callKeys)) {
    throw new APIRequestError("Public model stream response is invalid", "INVALID_RESPONSE", 502);
  }
  const responseID = value.response_id === undefined ? "" : boundedIdentity(value.response_id);
  const itemIDs = new Set(value.items.map((item) => isRecord(item) && typeof item.id === "string"
    ? item.id : ""));
  if ((value.response_id !== undefined && !responseID) ||
    (!responseID && (value.event_sequence !== 0 || value.items.length !== 0)) ||
    (responseID && !safePositiveInteger(value.event_sequence)) ||
    itemIDs.size !== value.items.length ||
    value.items.some((item) => !validPublicOutputItem(item, responseID))) {
    throw new APIRequestError("Public model stream item projection is invalid",
      "INVALID_RESPONSE", 502);
  }
  const call = value.call;
  if (call.run_id !== expectedRunID || !boundedIdentity(call.run_id) ||
    !boundedIdentity(call.session_id) || !boundedIdentity(call.attempt_id) ||
    !boundedIdentity(call.provider) || !boundedIdentity(call.model) ||
    !safePositiveInteger(call.model_attempt) || !safePositiveInteger(call.transport_attempt) ||
    !safePositiveInteger(call.max_attempts) || call.transport_attempt > call.max_attempts ||
    !safeBoundedCount(call.protocol_repair, 1) || !safeBoundedCount(call.tool_round, 4) ||
    !safeBoundedCount(call.stream_chunks, 64 * 1024) ||
    !safeBoundedCount(call.stream_bytes, 64 * 1024) ||
    typeof call.cancel_requested !== "boolean" || !validDate(call.started_at)) {
    throw new APIRequestError("Public model stream binding is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as PublicModelStreamSnapshot;
}

const threadActivityToolStatuses = ["pending", "completed", "denied", "failed"];
const threadActivityCommandStatuses = [...threadActivityToolStatuses, "prepared", "running",
  "stopping", "timed_out", "cancelled", "killed", "interrupted"];
const threadActivityEnvironments = ["Workspace Sandbox", "Host · Full Access",
  "Legacy execution boundary"];
const threadActivityKinds = ["command", "web_search", "web_fetch", "file_read", "file_edit",
  "verification", "mcp", "browser"];
const threadActivityToolNames = ["command_runtime", "workspace_list", "workspace_read",
  "workspace_glob", "workspace_grep", "workspace_change", "workspace_apply", "workspace_delete",
  "web_search", "web_fetch", "web_citation", "mcp_tool_call", "code_workspace_symbols",
  "code_document_symbols", "code_definition", "code_references", "code_implementation",
  "code_hover", "code_signature_help", "code_diagnostics", "code_call_hierarchy",
  "code_type_hierarchy", "github_review_evidence_list", "github_review_evidence_read",
  "browser_status", "browser_navigate", "browser_snapshot", "browser_click", "browser_type",
  "browser_screenshot"];
const maxThreadActivityArtifactBytes = 4 * 1024 * 1024;

function validThreadActivityString(value: unknown, maximum: number,
  optional = false): value is string {
  if (typeof value !== "string" || value.includes("\0")) return false;
  const codePoints = Array.from(value).length;
  return codePoints <= maximum && (optional || codePoints > 0);
}

function validThreadActivityFactText(value: unknown, maximum: number,
  optional = false): value is string {
  if (!validThreadActivityString(value, maximum, optional)) return false;
  if (value === "") return optional;
  return value.trim() === value && !Array.from(value)
    .some((character) => /[\p{Cc}\p{Cf}]/u.test(character) && character !== "\n" && character !== "\t");
}

function validThreadActivityFactIdentity(value: unknown, optional = false): value is string {
  if (!validThreadActivityString(value, 256, optional)) return false;
  if (value === "") return optional;
  return value.trim() === value && !/[\p{Cc}\p{Cf}]/u.test(value);
}

function validThreadActivityJSONFields(value: unknown): boolean {
  if (!Array.isArray(value) || value.length > 16) return false;
  const names = new Set<string>();
  for (const field of value) {
    if (!isRecord(field) || !hasExactKeys(field, ["name", "summary", "type"]) ||
      !validThreadActivityFactIdentity(field.name) || names.has(field.name) ||
      !validThreadActivityFactIdentity(field.type) ||
      !validThreadActivityFactText(field.summary, 2_048)) return false;
    names.add(field.name);
  }
  return true;
}

function validThreadActivityBoundary(value: unknown): boolean {
  return isRecord(value) && hasExactKeys(value, ["authorization", "error_code",
    "failure_reason", "truncated", "untrusted"]) &&
    ["policy_checked", "pending", "denied"].includes(String(value.authorization)) &&
    validThreadActivityFactIdentity(value.error_code, true) &&
    validThreadActivityFactText(value.failure_reason, 2_048, true) &&
    typeof value.truncated === "boolean" && typeof value.untrusted === "boolean";
}

function validThreadActivityArtifactReference(value: unknown): boolean {
  return isRecord(value) && hasExactKeys(value,
    ["artifact_ref", "mime", "size_bytes", "stream", "truncated"]) &&
    Boolean(boundedIdentity(value.artifact_ref)) &&
    ["stdout", "stderr"].includes(String(value.stream)) &&
    value.mime === "text/plain; charset=utf-8" &&
    safeBoundedCount(value.size_bytes, maxThreadActivityArtifactBytes) &&
    Number(value.size_bytes) > 0 && typeof value.truncated === "boolean";
}

function validThreadActivityCommand(value: unknown): boolean {
  const required = ["artifacts", "command", "duration_milliseconds", "execution_environment",
    "network", "status", "stderr_preview", "stdout_preview", "truncated",
    "working_directory"];
  return isRecord(value) && hasOnlyKeys(value, [...required, "exit_code"]) &&
    required.every((key) => Object.prototype.hasOwnProperty.call(value, key)) &&
    validThreadActivityString(value.command, 4_096) &&
    validThreadActivityString(value.working_directory, 1_024) &&
    threadActivityEnvironments.includes(String(value.execution_environment)) &&
    value.network === "disabled" &&
    threadActivityCommandStatuses.includes(String(value.status)) &&
    (value.exit_code === undefined ||
      (typeof value.exit_code === "number" && Number.isSafeInteger(value.exit_code))) &&
    safeBoundedCount(value.duration_milliseconds, Number.MAX_SAFE_INTEGER) &&
    validThreadActivityString(value.stdout_preview, 8_192, true) &&
    validThreadActivityString(value.stderr_preview, 8_192, true) &&
    typeof value.truncated === "boolean" && Array.isArray(value.artifacts) &&
    value.artifacts.length <= 2 && value.artifacts.every(validThreadActivityArtifactReference);
}

function validThreadActivityJSONSummary(value: unknown): boolean {
  return isRecord(value) && hasExactKeys(value, ["count", "fields", "summary", "type"]) &&
    safeBoundedCount(value.count, Number.MAX_SAFE_INTEGER) &&
    validThreadActivityFactIdentity(value.type) &&
    validThreadActivityFactText(value.summary, 2_048) &&
    validThreadActivityJSONFields(value.fields);
}

function validSafeHTTPSActivityURL(value: unknown, optional = false): boolean {
  if (!validThreadActivityFactText(value, 2_048, optional)) return false;
  if (value === "") return optional;
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "https:" || parsed.username !== "" || parsed.password !== "" ||
      parsed.port !== "" || parsed.hash !== "") return false;
    for (const [key, queryValue] of parsed.searchParams) {
      const compact = key.trim().toLowerCase().replace(/[-. _[\]]/gu, "");
      if (["key", "sig", "jwt", "session", "sessionid", "auth"].includes(compact) ||
        ["apikey", "accesstoken", "refreshtoken", "idtoken", "secret", "password",
          "passwd", "privatekey", "signature", "authorization", "credential", "cookie",
          "setcookie", "bearertoken"].some((part) => compact.includes(part)) ||
        compact.startsWith("auth") || compact.endsWith("token") ||
        /(?:bearer\s+|\bsk-(?:proj-)?[a-z0-9_-]{12,})/iu.test(queryValue)) return false;
    }
    return true;
  } catch {
    return false;
  }
}

function validThreadActivityTypedDetail(value: unknown): boolean {
  if (!isRecord(value) || !threadActivityKinds.includes(String(value.kind))) return false;
  switch (value.kind) {
    case "command": {
      if (!hasExactKeys(value, ["command", "kind"]) || !isRecord(value.command) ||
        !hasExactKeys(value.command, ["commands"]) || !Array.isArray(value.command.commands) ||
        value.command.commands.length > 32) return false;
      return value.command.commands.every(validThreadActivityCommand);
    }
    case "web_search": {
      if (!hasExactKeys(value, ["kind", "web_search"]) || !isRecord(value.web_search)) return false;
      const detail = value.web_search;
      if (!hasExactKeys(detail, ["boundary", "citeable", "limit", "operation", "provider",
        "query", "search_policy", "selection_reason", "source_count", "sources"]) ||
        !validThreadActivityFactText(detail.operation, 2_048) ||
        !validThreadActivityFactText(detail.query, 2_048) ||
        !safeBoundedCount(detail.limit, 10) || Number(detail.limit) < 1 ||
        !validThreadActivityFactText(detail.provider, 2_048, true) ||
        !validThreadActivityFactIdentity(detail.search_policy, true) ||
        !validThreadActivityFactText(detail.selection_reason, 2_048, true) ||
        !safeBoundedCount(detail.source_count, Number.MAX_SAFE_INTEGER) ||
        typeof detail.citeable !== "boolean" || !Array.isArray(detail.sources) ||
        detail.sources.length > 10 || !validThreadActivityBoundary(detail.boundary)) return false;
      return detail.sources.every((source, index) => isRecord(source) &&
        hasExactKeys(source, ["citeable", "provider", "rank", "state", "title", "url"]) &&
        source.rank === index + 1 && validThreadActivityFactText(source.title, 2_048, true) &&
        validSafeHTTPSActivityURL(source.url) &&
        validThreadActivityFactText(source.provider, 2_048, true) &&
        validThreadActivityFactIdentity(source.state) && typeof source.citeable === "boolean");
    }
    case "web_fetch": {
      if (!hasExactKeys(value, ["kind", "web_fetch"]) || !isRecord(value.web_fetch)) return false;
      const detail = value.web_fetch;
      return hasExactKeys(detail, ["boundary", "citeable", "http_status", "operation",
        "partial", "redirects", "robots", "robots_policy", "state", "url"]) &&
        validThreadActivityFactText(detail.operation, 2_048) &&
        (detail.url === "已发现的网页来源" || detail.url === "已抓取的网页快照" ||
          validSafeHTTPSActivityURL(detail.url)) && validThreadActivityFactIdentity(detail.state, true) &&
        safeBoundedCount(detail.http_status, 599) &&
        (detail.http_status === 0 || Number(detail.http_status) >= 100) &&
        validThreadActivityFactIdentity(detail.robots, true) &&
        validThreadActivityFactIdentity(detail.robots_policy, true) &&
        safeBoundedCount(detail.redirects, 32) && typeof detail.partial === "boolean" &&
        typeof detail.citeable === "boolean" && validThreadActivityBoundary(detail.boundary);
    }
    case "file_read": {
      if (!hasExactKeys(value, ["file_read", "kind"]) || !isRecord(value.file_read)) return false;
      const detail = value.file_read;
      return hasExactKeys(detail, ["boundary", "end_line", "limit", "operation", "path",
        "pattern", "query", "result_count", "start_line", "summary", "truncated"]) &&
        validThreadActivityFactText(detail.operation, 2_048) &&
        validThreadActivityFactText(detail.path, 2_048, true) &&
        validThreadActivityFactText(detail.pattern, 2_048, true) &&
        validThreadActivityFactText(detail.query, 2_048, true) &&
        safeBoundedCount(detail.start_line, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.end_line, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.limit, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.result_count, Number.MAX_SAFE_INTEGER) &&
        typeof detail.truncated === "boolean" &&
        validThreadActivityFactText(detail.summary, 2_048) &&
        validThreadActivityBoundary(detail.boundary);
    }
    case "file_edit": {
      if (!hasExactKeys(value, ["file_edit", "kind"]) || !isRecord(value.file_edit)) return false;
      const detail = value.file_edit;
      return hasExactKeys(detail, ["action", "applied", "apply_status", "boundary",
        "destination_path", "diff", "diff_available", "edit_id", "file_written", "operation",
        "path", "replayed"]) &&
        validThreadActivityFactText(detail.operation, 2_048) &&
        validThreadActivityFactIdentity(detail.action, true) &&
        validThreadActivityFactText(detail.path, 2_048, true) &&
        validThreadActivityFactText(detail.destination_path, 2_048, true) &&
        validThreadActivityFactIdentity(detail.apply_status, true) &&
        typeof detail.diff_available === "boolean" &&
        validThreadActivityFactIdentity(detail.edit_id, !detail.diff_available) &&
        (!detail.diff_available || detail.edit_id !== "") &&
        typeof detail.applied === "boolean" && typeof detail.file_written === "boolean" &&
        typeof detail.replayed === "boolean" && isRecord(detail.diff) &&
        hasExactKeys(detail.diff, ["added_lines", "hunks", "removed_lines", "summary"]) &&
        safeBoundedCount(detail.diff.added_lines, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.diff.removed_lines, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.diff.hunks, Number.MAX_SAFE_INTEGER) &&
        validThreadActivityFactText(detail.diff.summary, 2_048, true) &&
        validThreadActivityBoundary(detail.boundary);
    }
    case "mcp": {
      if (!hasExactKeys(value, ["kind", "mcp"]) || !isRecord(value.mcp)) return false;
      const detail = value.mcp;
      return hasExactKeys(detail, ["arguments", "boundary", "operation", "result", "server",
        "tool"]) && validThreadActivityFactText(detail.operation, 2_048) &&
        validThreadActivityFactIdentity(detail.server) && validThreadActivityFactIdentity(detail.tool) &&
        validThreadActivityJSONFields(detail.arguments) &&
        validThreadActivityJSONSummary(detail.result) &&
        validThreadActivityBoundary(detail.boundary);
    }
    case "verification": {
      if (!hasExactKeys(value, ["kind", "verification"]) || !isRecord(value.verification)) return false;
      const detail = value.verification;
      return hasExactKeys(detail, ["boundary", "direction", "limit", "operation", "path",
        "position", "query", "result_count", "summary", "tool", "truncated"]) &&
        validThreadActivityFactText(detail.operation, 2_048) &&
        validThreadActivityFactIdentity(detail.tool) &&
        validThreadActivityFactText(detail.path, 2_048, true) &&
        validThreadActivityFactText(detail.query, 2_048, true) &&
        validThreadActivityFactIdentity(detail.position, true) &&
        validThreadActivityFactIdentity(detail.direction, true) &&
        safeBoundedCount(detail.limit, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.result_count, Number.MAX_SAFE_INTEGER) &&
        typeof detail.truncated === "boolean" &&
        validThreadActivityFactText(detail.summary, 2_048) &&
        validThreadActivityBoundary(detail.boundary);
    }
    case "browser": {
      if (!hasExactKeys(value, ["browser", "kind"]) || !isRecord(value.browser)) return false;
      const detail = value.browser;
      return hasExactKeys(detail, ["action", "artifact_bytes", "boundary", "input_length",
        "operation", "selector", "summary", "url"]) &&
        validThreadActivityFactText(detail.operation, 2_048) &&
        validThreadActivityFactIdentity(detail.action) &&
        validThreadActivityFactText(detail.url, 2_048, true) &&
        validThreadActivityFactText(detail.selector, 2_048, true) &&
        safeBoundedCount(detail.input_length, Number.MAX_SAFE_INTEGER) &&
        safeBoundedCount(detail.artifact_bytes, maxThreadActivityArtifactBytes) &&
        validThreadActivityFactText(detail.summary, 2_048) &&
        validThreadActivityBoundary(detail.boundary);
    }
    default:
      return false;
  }
}

function expectedThreadActivityKind(tool: string): string {
  if (tool === "command_runtime") return "command";
  if (tool === "web_search") return "web_search";
  if (tool === "web_fetch" || tool === "web_citation") return "web_fetch";
  if (["workspace_list", "workspace_read", "workspace_glob", "workspace_grep"].includes(tool)) return "file_read";
  if (["workspace_change", "workspace_apply", "workspace_delete"].includes(tool)) return "file_edit";
  if (tool === "mcp_tool_call") return "mcp";
  if (tool.startsWith("browser_")) return "browser";
  return "verification";
}

function parseThreadActivityDetail(value: unknown, expectedThreadID: string,
  expectedActivityRef: string): ThreadActivityDetailView {
  if (!hasExactKeys(value, ["activity_ref", "run_id", "tools", "version"]) ||
    value.version !== "thread_activity_detail.v2" ||
    boundedIdentity(value.activity_ref) !== expectedActivityRef ||
    !boundedIdentity(value.run_id) || !Array.isArray(value.tools) || value.tools.length !== 1) {
    throw new APIRequestError("Thread activity detail is invalid", "INVALID_RESPONSE", 502);
  }
  const tool = value.tools[0];
  const toolRequired = ["agent_id", "agent_label", "agent_role", "detail",
    "duration_milliseconds", "label", "name", "started_at", "status"];
  if (!isRecord(tool) || !hasOnlyKeys(tool, [...toolRequired, "completed_at"]) ||
    toolRequired.some((key) => !Object.prototype.hasOwnProperty.call(tool, key)) ||
    !threadActivityToolNames.includes(String(tool.name)) || !validThreadActivityString(tool.label, 128) ||
    !validThreadActivityString(tool.agent_label, 128) ||
    !boundedIdentity(tool.agent_id) || !["root", "specialist", "unknown"].includes(String(tool.agent_role)) ||
    !threadActivityToolStatuses.includes(String(tool.status)) || !validDate(tool.started_at) ||
    (tool.completed_at !== undefined && (!validDate(tool.completed_at) ||
      Date.parse(String(tool.completed_at)) < Date.parse(String(tool.started_at)))) ||
    !safeBoundedCount(tool.duration_milliseconds, Number.MAX_SAFE_INTEGER) ||
    !validThreadActivityTypedDetail(tool.detail) ||
    !isRecord(tool.detail) || tool.detail.kind !== expectedThreadActivityKind(String(tool.name))) {
    throw new APIRequestError("Thread activity tool detail is invalid", "INVALID_RESPONSE", 502);
  }
  // The endpoint is Thread-scoped at the URL and store join. Keep the parameter
  // in this parser contract so callers cannot accidentally reuse a response
  // under another Thread cache key.
  if (boundedIdentity(expectedThreadID) !== expectedThreadID) {
    throw new APIRequestError("Thread activity owner is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ThreadActivityDetailView;
}

function parseThreadActivityArtifact(value: unknown, expectedActivityRef: string,
  expectedArtifactRef: string): ThreadActivityArtifactView {
  if (!isRecord(value) || !hasExactKeys(value, ["activity_ref", "artifact_ref", "content",
    "instruction_authorized", "mime", "redacted", "sha256", "size_bytes", "stream",
    "truncated", "untrusted", "version"]) ||
    value.version !== "thread_activity_artifact.v1" ||
    value.activity_ref !== expectedActivityRef || value.artifact_ref !== expectedArtifactRef ||
    !["stdout", "stderr"].includes(String(value.stream)) ||
    value.mime !== "text/plain; charset=utf-8" ||
    !validThreadActivityString(value.content, maxThreadActivityArtifactBytes, true) ||
    new TextEncoder().encode(value.content as string).byteLength !== value.size_bytes ||
    !safeBoundedCount(value.size_bytes, maxThreadActivityArtifactBytes) ||
    !/^[0-9a-f]{64}$/u.test(String(value.sha256)) || typeof value.redacted !== "boolean" ||
    typeof value.truncated !== "boolean" || value.untrusted !== true ||
    value.instruction_authorized !== false) {
    throw new APIRequestError("Thread activity artifact is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ThreadActivityArtifactView;
}

function parsePublicModelStreamPoll(value: unknown,
  expectedRunID: string): PublicModelStreamSnapshot | null {
  if (!isRecord(value) || !hasOnlyKeys(value, ["active", "snapshot", "version"]) ||
    value.version !== "model_public_stream_poll.v1" || typeof value.active !== "boolean") {
    throw new APIRequestError("Public model stream poll response is invalid",
      "INVALID_RESPONSE", 502);
  }
  if (!value.active) {
    if (value.snapshot !== undefined) {
      throw new APIRequestError("Inactive public model stream poll returned a snapshot",
        "INVALID_RESPONSE", 502);
    }
    return null;
  }
  if (value.snapshot === undefined) {
    throw new APIRequestError("Active public model stream poll omitted its snapshot",
      "INVALID_RESPONSE", 502);
  }
  return parsePublicModelStream(value.snapshot, expectedRunID);
}

function validPublicOutputItem(value: unknown, responseID: string): boolean {
  const required = ["durable", "id", "provisional", "response_id", "status", "type"];
  const allowed = [...required, "argument_bytes", "call_id", "durable_call_id", "tool_name"];
  if (!isRecord(value) || !hasOnlyKeys(value, allowed) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.response_id !== responseID || !boundedIdentity(value.response_id) ||
    !boundedIdentity(value.id) || value.provisional !== true || value.durable !== false ||
    !["message", "tool_call"].includes(String(value.type)) ||
    !["in_progress", "ready_for_validation", "completed", "failed", "cancelled"]
      .includes(String(value.status))) {
    return false;
  }
  if (value.type === "message") {
    return value.call_id === undefined && value.durable_call_id === undefined &&
      value.tool_name === undefined && value.argument_bytes === undefined &&
      value.status !== "ready_for_validation";
  }
  return Boolean(boundedIdentity(value.call_id) && boundedIdentity(value.tool_name)) &&
    value.durable_call_id === undefined &&
    (value.argument_bytes === undefined || safeBoundedCount(value.argument_bytes, 256 * 1024));
}

function parseSpecialistModelCancellation(value: unknown, expectedRunID: string,
  expectedAgentID: string, request: ModelCancellationRequestView): SpecialistModelCancellationView {
  if (!hasExactKeys(value, ["agent_id", "attempt_id", "id", "model_attempt", "replayed",
    "requested_at", "run_id", "status"]) ||
    !boundedIdentity(value.id) || value.run_id !== expectedRunID ||
    !boundedIdentity(value.run_id) || value.agent_id !== expectedAgentID ||
    !boundedIdentity(value.agent_id) || value.attempt_id !== request.attempt_id ||
    !boundedIdentity(value.attempt_id) ||
    !safePositiveInteger(value.model_attempt) ||
    value.model_attempt !== request.model_attempt ||
    typeof value.replayed !== "boolean" || !validDate(value.requested_at) ||
    !isModelCancellationStatus(value.status)) {
    throw new APIRequestError("Specialist model cancellation response is invalid",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as SpecialistModelCancellationView;
}

function isRunStatus(value: unknown): boolean {
  return typeof value === "string" && ["created", "preparing", "running",
    "waiting_approval", "paused", "completed", "failed", "cancelled"].includes(value);
}

function parseRunExecutionControl(value: unknown, expectedRunID: string,
  request: RunExecutionControlRequestView): RunExecutionControlView {
  if (!isRecord(value) || !hasOnlyKeys(value, ["cancelled_count", "capability_grant",
    "committed_count", "completion_event_sequence", "error_code", "execution_started",
    "max_steps", "model_called", "operation_id", "pending_count", "prepared_count",
    "replayed", "run_id", "run_status", "selected_count", "session_id", "status",
    "steps_completed", "stop_reason", "tool_called", "version"]) ||
    value.version !== "run_execution_handoff.v1" || value.run_id !== expectedRunID ||
    !boundedIdentity(value.run_id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.operation_id) || value.max_steps !== request.max_steps ||
    !safeBoundedCount(value.selected_count, request.max_steps) ||
    !safeBoundedCount(value.steps_completed, value.selected_count) ||
    !safeBoundedCount(value.pending_count, value.selected_count) ||
    !safeBoundedCount(value.prepared_count, value.selected_count) ||
    !safeBoundedCount(value.committed_count, value.selected_count) ||
    !safeBoundedCount(value.cancelled_count, value.selected_count) ||
    value.pending_count + value.prepared_count + value.committed_count +
      value.cancelled_count !== value.selected_count ||
    !safePositiveInteger(value.completion_event_sequence) ||
    (value.status !== "completed" && value.status !== "failed") ||
    !isRunStatus(value.run_status) || typeof value.stop_reason !== "string" ||
    value.stop_reason.length === 0 || value.stop_reason.length > 64 ||
    typeof value.replayed !== "boolean" || typeof value.execution_started !== "boolean" ||
    typeof value.model_called !== "boolean" || typeof value.tool_called !== "boolean" ||
    value.capability_grant !== false || (value.tool_called && !value.model_called) ||
    value.execution_started !== (value.selected_count > 0) ||
    (value.status === "completed" && value.error_code !== undefined) ||
    (value.status === "failed" && (typeof value.error_code !== "string" ||
      value.error_code.length === 0 || value.error_code.length > 64))) {
    throw new APIRequestError("Run execution response is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as RunExecutionControlView;
}

function parseModelAvailability(value: unknown): ModelAvailabilityView {
  if (!hasExactKeys(value, ["generation", "protocol_version", "providers", "routes"]) ||
    value.protocol_version !== "model_availability.v2" || !Array.isArray(value.providers) ||
    !safeBoundedCount(value.generation, Number.MAX_SAFE_INTEGER) || value.generation < 1 ||
    !Array.isArray(value.routes) || value.providers.length > 70 || value.routes.length > 64) {
    throw new APIRequestError("Model availability response is invalid", "INVALID_RESPONSE", 502);
  }
  const providerNames = new Set<string>();
  const availableProviderNames = new Set<string>();
  const harnessReadiness = new Map<string, boolean>();
  for (const provider of value.providers) {
    if (!hasExactKeys(provider, ["configuration_error", "credential_source", "custom",
      "definition_revision", "display_name", "enabled", "harnesses", "kind", "models",
      "name", "native_web_search_capability", "native_web_search_runtime_enabled",
      "network_required", "search_mode", "status", "transport"]) ||
      !boundedText(provider.name, 128) || !boundedText(provider.display_name, 128) ||
      !["local", "anthropic_compatible", "openai_compatible", "ollama"].includes(
        String(provider.kind)) ||
      (provider.status !== "available" && provider.status !== "not_configured" &&
        provider.status !== "invalid_configuration") ||
      (provider.credential_source !== "none" && provider.credential_source !== "environment" &&
        provider.credential_source !== "system") ||
      typeof provider.network_required !== "boolean" || typeof provider.custom !== "boolean" ||
      typeof provider.enabled !== "boolean" ||
      !safeBoundedCount(provider.definition_revision, Number.MAX_SAFE_INTEGER) ||
      !["mock", "anthropic_messages", "openai_chat_completions", "openai_responses",
        "ollama_chat"].includes(
        String(provider.transport)) ||
      !["disabled", "auto", "searxng", "provider_native"].includes(
        String(provider.search_mode)) ||
      !["unsupported", "declared_unverified"].includes(
        String(provider.native_web_search_capability)) ||
      provider.native_web_search_runtime_enabled !== false ||
      (provider.custom ? (!validCustomProviderID(provider.name) ||
        provider.definition_revision < 1) : (provider.definition_revision !== 0 ||
        provider.display_name !== provider.name || provider.enabled !== true ||
        provider.search_mode !== "disabled" ||
        provider.native_web_search_capability !== "unsupported")) ||
      typeof provider.configuration_error !== "boolean" || !Array.isArray(provider.models) ||
      provider.models.length > 128 || !provider.models.every((model) => boundedText(model, 256)) ||
      !Array.isArray(provider.harnesses) || provider.harnesses.length !== provider.models.length ||
      providerNames.has(provider.name)) {
      throw new APIRequestError("Model Provider availability is invalid", "INVALID_RESPONSE", 502);
    }
    providerNames.add(provider.name);
    if (provider.status === "available") {
      availableProviderNames.add(provider.name);
    }
    const harnessModels = new Set<string>();
    for (const harness of provider.harnesses) {
      const parsed = parseModelHarnessAvailability(harness);
      if (!provider.models.includes(parsed.model) || harnessModels.has(parsed.model)) {
        throw new APIRequestError("Model Harness availability binding is invalid",
          "INVALID_RESPONSE", 502);
      }
      harnessModels.add(parsed.model);
      harnessReadiness.set(`${provider.name}\u0000${parsed.model}`, parsed.root_eligible);
    }
  }
  const routeNames = new Set<string>();
  for (const route of value.routes) {
    if (!hasExactKeys(route, ["available", "harness_ready", "model", "name", "provider"]) ||
      !boundedText(route.name, 128) || !boundedText(route.provider, 128) ||
      !boundedText(route.model, 256) || typeof route.available !== "boolean" ||
      typeof route.harness_ready !== "boolean" ||
      (route.available && !availableProviderNames.has(route.provider)) ||
      (route.harness_ready &&
        harnessReadiness.get(`${route.provider}\u0000${route.model}`) !== true) ||
      routeNames.has(route.name)) {
      throw new APIRequestError("Model route availability is invalid", "INVALID_RESPONSE", 502);
    }
    routeNames.add(route.name);
  }
  return value as unknown as ModelAvailabilityView;
}

function parseModelHarnessAvailability(value: unknown): ModelHarnessAvailabilityView {
  if (!hasExactKeys(value, ["expires_at", "json_strategy", "latest_qualification_status",
    "model", "protocol_version", "qualification_checked_at", "qualification_source",
    "qualification_status", "qualified_at", "root_eligible", "streaming_qualified",
    "strict_json_qualified", "structured_json_eligible", "tool_calls_qualified",
    "tool_results_qualified", "tool_strategy", "transport_protocol"]) ||
    value.protocol_version !== "model_harness.v1" || !boundedText(value.model, 256) ||
    !["mock", "anthropic_messages", "openai_chat_completions", "openai_responses",
      "ollama_chat", "provider_contract"].includes(
      String(value.transport_protocol)) ||
    !["native", "none"].includes(String(value.tool_strategy)) ||
    !["native", "prompt", "none"].includes(String(value.json_strategy)) ||
    !["trusted_builtin", "qualification_required", "verified"].includes(
      String(value.qualification_status)) ||
    !qualificationStatusValid(value.latest_qualification_status) ||
    typeof value.qualification_checked_at !== "string" ||
    typeof value.qualification_source !== "string" ||
    (value.latest_qualification_status === ""
      ? (value.qualification_checked_at !== "" || value.qualification_source !== "")
      : (value.qualification_checked_at !== "" &&
        !validDate(value.qualification_checked_at)) ||
        !["diagnostic", "harness_qualification", "availability"].includes(
          String(value.qualification_source))) ||
    typeof value.tool_calls_qualified !== "boolean" ||
    typeof value.tool_results_qualified !== "boolean" ||
    typeof value.strict_json_qualified !== "boolean" ||
    typeof value.streaming_qualified !== "boolean" ||
    typeof value.root_eligible !== "boolean" ||
    typeof value.structured_json_eligible !== "boolean" ||
    typeof value.qualified_at !== "string" || typeof value.expires_at !== "string" ||
    value.structured_json_eligible !== value.strict_json_qualified ||
    value.root_eligible !== (value.tool_strategy === "native" &&
      value.tool_calls_qualified && value.tool_results_qualified &&
      value.strict_json_qualified && value.streaming_qualified) ||
    (value.qualification_status === "verified"
      ? (!validDate(value.qualified_at) || !validDate(value.expires_at) ||
        Date.parse(value.expires_at) <= Date.parse(value.qualified_at))
      : (value.qualified_at !== "" || value.expires_at !== "")) ||
    (value.tool_strategy === "none" && (value.tool_calls_qualified ||
      value.tool_results_qualified || value.streaming_qualified)) ||
    (value.json_strategy === "none" && value.strict_json_qualified)) {
    throw new APIRequestError("Model Harness availability is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ModelHarnessAvailabilityView;
}

function parsePlanDirectionControl(value: unknown, expectedRunID: string,
  request: PlanDirectionControlRequestView): PlanDirectionControlView {
  if (!hasExactKeys(value, ["capability_grant", "direction", "execution_started", "model_called",
    "note_id", "phase_changed", "proposal_id", "replayed", "run_id", "selection_id",
    "tool_called", "version", "work_item_count"]) ||
    value.version !== "plan_delivery_control.v1" || value.run_id !== expectedRunID ||
    value.proposal_id !== request.proposal_id || value.direction !== request.direction ||
    !boundedIdentity(value.run_id) || !boundedIdentity(value.proposal_id) ||
    !boundedIdentity(value.selection_id) || !boundedIdentity(value.note_id) ||
    !safeBoundedCount(value.work_item_count, 32) || value.work_item_count < 1 ||
    typeof value.replayed !== "boolean" || value.phase_changed !== false ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false) {
    throw new APIRequestError("Plan direction response violated its closed authority contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as PlanDirectionControlView;
}

function parsePlanModeTransition(value: unknown,
  expectedRunID: string): PlanModeTransitionControlView {
  if (!hasExactKeys(value, ["applied_mode", "capability_grant", "current_mode",
    "execution_started", "model_called", "replayed", "run_id", "tool_called", "version"]) ||
    value.version !== "plan_delivery_control.v1" || value.run_id !== expectedRunID ||
    !boundedIdentity(value.run_id) || !isRecord(value.applied_mode) ||
    !isRecord(value.current_mode) || value.applied_mode.phase !== "plan" ||
    value.current_mode.phase !== "plan" || value.applied_mode.capability_grant !== false ||
    value.current_mode.capability_grant !== false || typeof value.replayed !== "boolean" ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false) {
    throw new APIRequestError("Plan mode response violated its closed authority contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as PlanModeTransitionControlView;
}

function parsePlanDeliveryTransition(value: unknown,
  expectedRunID: string): PlanDeliveryTransitionControlView {
  if (!hasExactKeys(value, ["applied_mode", "capability_grant", "current_mode",
    "execution_started", "model_called", "replayed", "run_id", "selection_id", "tool_called",
    "version"]) || value.version !== "plan_delivery_control.v1" ||
    value.run_id !== expectedRunID || !boundedIdentity(value.run_id) ||
    !boundedIdentity(value.selection_id) || !isRecord(value.applied_mode) ||
    !isRecord(value.current_mode) || value.applied_mode.phase !== "deliver" ||
    value.current_mode.phase !== "deliver" || value.applied_mode.capability_grant !== false ||
    value.current_mode.capability_grant !== false || typeof value.replayed !== "boolean" ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false) {
    throw new APIRequestError("Plan delivery response violated its closed authority contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as PlanDeliveryTransitionControlView;
}

function parseApprovalQueue(value: unknown, expectedRunID: string): ApprovalQueueView {
  if (!hasExactKeys(value, ["capability_grant", "items", "process_execution_enabled",
    "protocol_version", "run_id", "session_grant_created", "truncated"]) ||
    value.protocol_version !== "approval_queue.v1" || value.run_id !== expectedRunID ||
    !boundedIdentity(value.run_id) || !Array.isArray(value.items) || value.items.length > 100 ||
    typeof value.truncated !== "boolean" || value.process_execution_enabled !== false ||
    value.session_grant_created !== false || value.capability_grant !== false) {
    throw new APIRequestError("Approval queue response is invalid", "INVALID_RESPONSE", 502);
  }
  const identities = new Set<string>();
  for (const item of value.items) {
    const itemID = isRecord(item) ? boundedIdentity(item.id) : "";
    const requiredKeys = ["action_class", "allowed_actions", "capability_grant", "created_at",
      "id", "mode", "process_execution_enabled", "proposal_id", "run_id", "session_id",
      "status", "tool_name", "updated_at", "version", "workspace_id"];
    const optionalKeys = ["canonical_url", "exact_target"];
    if (!isRecord(item) || !hasOnlyKeys(item, [...requiredKeys, ...optionalKeys]) ||
      requiredKeys.some((key) => !Object.prototype.hasOwnProperty.call(item, key)) ||
      item.run_id !== expectedRunID ||
      !["pending", "approved", "denied"].includes(String(item.status)) || !itemID ||
      !boundedIdentity(item.proposal_id) || !boundedIdentity(item.run_id) ||
      !boundedIdentity(item.session_id) ||
      (item.workspace_id !== "" && !boundedIdentity(item.workspace_id)) ||
      !boundedText(item.tool_name, 128) || !boundedText(item.action_class, 128) ||
      !boundedText(item.mode, 64) || !Array.isArray(item.allowed_actions) ||
      item.allowed_actions.length > 3 ||
      !item.allowed_actions.every((action) => action === "approve_once" ||
        action === "approve_for_thread" || action === "deny") ||
      new Set(item.allowed_actions).size !== item.allowed_actions.length ||
      !safePositiveInteger(item.version) || !validDate(item.created_at) ||
      !validDate(item.updated_at) || item.process_execution_enabled !== false ||
      item.capability_grant !== false || identities.has(itemID)) {
      throw new APIRequestError("Approval queue item is invalid", "INVALID_RESPONSE", 502);
    }
    if (item.tool_name === "replace_file" && item.allowed_actions.includes("approve_once")) {
      throw new APIRequestError("File approval exposed write authority", "INVALID_RESPONSE", 502);
    }
    const webFetch = item.tool_name === "web_fetch";
    const recovering = item.status === "approved" || item.status === "denied";
    const validWebFetchActions = item.status === "pending"
      ? item.allowed_actions.length === 3 &&
        item.allowed_actions.includes("approve_once") &&
        item.allowed_actions.includes("approve_for_thread") &&
        item.allowed_actions.includes("deny")
      : item.allowed_actions.length === 1 && (item.status === "approved"
        ? ["approve_once", "approve_for_thread"].includes(String(item.allowed_actions[0]))
        : item.allowed_actions[0] === "deny");
    if (webFetch ? item.action_class !== "public_https_fetch" ||
      !boundedText(item.canonical_url, 4_096) || !boundedText(item.exact_target, 253) ||
      !item.canonical_url.startsWith("https://") ||
      !validWebFetchActions
      : item.canonical_url !== undefined || item.exact_target !== undefined ||
        item.allowed_actions.includes("approve_for_thread") || recovering) {
      throw new APIRequestError("Web fetch approval projection is invalid", "INVALID_RESPONSE", 502);
    }
    identities.add(itemID);
  }
  return value as unknown as ApprovalQueueView;
}

function parseApprovalDecision(value: unknown, expectedRunID: string, expectedApprovalID: string,
  request: ApprovalDecisionControlRequestView): ApprovalDecisionControlView {
  const expectedStatus = request.action === "deny" ? "denied" : "approved";
  if (!hasExactKeys(value, ["action", "approval_id", "capability_grant",
    "docker_execution_enabled", "execution_resumed", "process_execution_enabled", "proposal_id",
    "replayed", "retry_completed", "retry_scheduled", "run_id", "session_grant_created",
    "shell_execution_enabled", "status", "tool_name", "version", "workspace_write_applied"]) ||
    value.version !== "approval_control.v1" ||
    value.run_id !== expectedRunID || value.approval_id !== expectedApprovalID ||
    value.action !== request.action || value.status !== expectedStatus ||
    !boundedIdentity(value.run_id) || !boundedIdentity(value.approval_id) ||
    !boundedIdentity(value.proposal_id) || !boundedText(value.tool_name, 128) ||
    typeof value.replayed !== "boolean" || typeof value.execution_resumed !== "boolean" ||
    typeof value.retry_completed !== "boolean" || typeof value.retry_scheduled !== "boolean" ||
    value.execution_resumed !== false || value.retry_completed !== false ||
    (value.tool_name !== "web_fetch" && value.retry_scheduled !== false) ||
    value.process_execution_enabled !== false ||
    value.shell_execution_enabled !== false || value.docker_execution_enabled !== false ||
    value.workspace_write_applied !== false || value.session_grant_created !== false ||
    value.capability_grant !== false) {
    throw new APIRequestError("Approval decision violated its closed authority contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ApprovalDecisionControlView;
}

function parseControlledCommandProposalReceipt(value: unknown): boolean {
  if (!hasExactKeys(value, ["active_process_limit", "backend", "cancelled", "completed_at",
    "environment_inherited", "exit_code", "job_assigned_at_creation", "kill_on_job_close",
    "low_integrity_token", "network_requested", "output_limit_exceeded", "persistent_process",
    "process_memory_limit", "product_execution_enabled", "request_id", "restricted_token",
    "started_at", "stderr_captured_bytes", "stderr_observed_bytes", "stderr_prefix_sha256",
    "stderr_truncated", "stdin_closed", "stdout_captured_bytes", "stdout_observed_bytes",
    "stdout_prefix_sha256", "stdout_truncated", "timed_out", "tree_reaped"]) ||
    !boundedIdentity(value.request_id) || value.backend !== "windows-controlled-v1" ||
    typeof value.exit_code !== "number" || !Number.isSafeInteger(value.exit_code) ||
    !safeBoundedCount(value.stdout_observed_bytes, 64 * 1024 * 1024) ||
    !safeBoundedCount(value.stdout_captured_bytes, 64 * 1024) ||
    !safeBoundedCount(value.stderr_observed_bytes, 64 * 1024 * 1024) ||
    !safeBoundedCount(value.stderr_captured_bytes, 64 * 1024) ||
    value.stdout_captured_bytes > value.stdout_observed_bytes ||
    value.stderr_captured_bytes > value.stderr_observed_bytes ||
    !isSHA256(value.stdout_prefix_sha256) || !isSHA256(value.stderr_prefix_sha256) ||
    typeof value.stdout_truncated !== "boolean" ||
    typeof value.stderr_truncated !== "boolean" ||
    !validDate(value.started_at) || !validDate(value.completed_at) ||
    Date.parse(value.completed_at) < Date.parse(value.started_at) ||
    typeof value.timed_out !== "boolean" || typeof value.cancelled !== "boolean" ||
    typeof value.output_limit_exceeded !== "boolean" || value.tree_reaped !== true ||
    value.restricted_token !== true || value.low_integrity_token !== true ||
    value.job_assigned_at_creation !== true || value.kill_on_job_close !== true ||
    value.active_process_limit !== 1 || value.process_memory_limit !== 512 * 1024 * 1024 ||
    value.stdin_closed !== true || value.environment_inherited !== false ||
    value.network_requested !== false || value.persistent_process !== false ||
    value.product_execution_enabled !== true) {
    return false;
  }
  return true;
}

function parseControlledCommandProposal(value: unknown, expectedRunID: string,
  expectedProposalID = ""): ControlledCommandProposalView {
  const required = ["capability_grant", "created_at", "evidence_instruction_authorized",
    "execution_authorized", "fingerprint", "id", "instruction_authorized", "kind",
    "mission_id", "operator_review_required", "permission_mode", "permission_revision",
    "policy_version", "protocol_version", "purpose", "run_id", "session_id",
    "timeout_milliseconds", "workspace_id"];
  const optional = ["execution_replayed", "receipt", "relative_path", "result", "review",
    "review_replayed", "untrusted_evidence"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "controlled_command_proposal.v1" ||
    value.policy_version !== "controlled_command_proposal_policy.v1" ||
    value.run_id !== expectedRunID ||
    (expectedProposalID !== "" && value.id !== expectedProposalID) ||
    !boundedIdentity(value.id) || !boundedIdentity(value.run_id) ||
    !boundedIdentity(value.mission_id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) ||
    !["git-status", "git-diff-check", "go-version", "powershell-workspace-list"]
      .includes(String(value.kind)) ||
    !boundedText(value.purpose, 4_800) ||
    !["conservative", "approval", "full_access", "debug"]
      .includes(String(value.permission_mode)) ||
    !safePositiveInteger(value.permission_revision) ||
    !safePositiveInteger(value.timeout_milliseconds) ||
    value.timeout_milliseconds > 120_000 ||
    value.operator_review_required !== true || value.instruction_authorized !== false ||
    value.execution_authorized !== false || value.capability_grant !== false ||
    !isSHA256(value.fingerprint) || !validDate(value.created_at) ||
    value.evidence_instruction_authorized !== false ||
    (value.relative_path !== undefined &&
      (!boundedText(value.relative_path, 1024) ||
        !validWorkspaceRelativePath(value.relative_path))) ||
    (value.review_replayed !== undefined && typeof value.review_replayed !== "boolean") ||
    (value.execution_replayed !== undefined && typeof value.execution_replayed !== "boolean") ||
    (value.untrusted_evidence !== undefined &&
      (typeof value.untrusted_evidence !== "string" ||
        value.untrusted_evidence.length > 16 * 1024 ||
        !value.untrusted_evidence.startsWith("UNTRUSTED GO COMMAND RESULT")))) {
    throw new APIRequestError("Controlled command proposal response is invalid",
      "INVALID_RESPONSE", 502);
  }
  if (value.review !== undefined) {
    const review = value.review;
    if (!hasExactKeys(review, ["capability_grant", "created_at", "decision", "id", "reason",
      "reviewed_by", "single_use_execution_authorized"]) ||
      !boundedIdentity(review.id) || !boundedIdentity(review.reviewed_by) ||
      !boundedText(review.reason, 4_096) || !validDate(review.created_at) ||
      (review.decision !== "approve" && review.decision !== "deny") ||
      review.single_use_execution_authorized !== (review.decision === "approve") ||
      review.capability_grant !== false) {
      throw new APIRequestError("Controlled command review response is invalid",
        "INVALID_RESPONSE", 502);
    }
  }
  if (value.result !== undefined) {
    const result = value.result;
    if (!hasExactKeys(result, ["automatic_retry_allowed", "content_sha256", "created_at", "id",
      "instruction_authorized", "raw_output_persisted", "source_kind", "source_ref", "status"]) ||
      !boundedIdentity(result.id) || !boundedIdentity(result.source_ref) ||
      (result.status !== "completed" && result.status !== "failed") ||
      result.source_kind !== "go_command_result" || !isSHA256(result.content_sha256) ||
      result.instruction_authorized !== false || result.raw_output_persisted !== false ||
      result.automatic_retry_allowed !== false || !validDate(result.created_at)) {
      throw new APIRequestError("Controlled command result response is invalid",
        "INVALID_RESPONSE", 502);
    }
  }
  if ((value.result === undefined) !== (value.receipt === undefined) ||
    (value.receipt !== undefined && !parseControlledCommandProposalReceipt(value.receipt)) ||
    value.result !== undefined && value.review === undefined ||
    value.untrusted_evidence !== undefined && value.result === undefined) {
    throw new APIRequestError("Controlled command proposal response crossed its execution boundary",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ControlledCommandProposalView;
}

function parseHostCommandProposalReceipt(value: unknown): boolean {
  return hasExactKeys(value, ["active_process_limit", "backend", "cancelled", "completed_at",
    "environment_inherited", "exit_code", "job_assigned_at_creation", "job_memory_limit",
    "kill_on_job_close", "low_integrity_token", "network_requested", "non_sandboxed",
    "output_limit_exceeded", "persistent_process", "product_execution_enabled", "request_id",
    "restricted_token", "started_at", "stderr_captured_bytes", "stderr_observed_bytes",
    "stderr_prefix_sha256", "stderr_truncated", "stdin_closed", "stdout_captured_bytes",
    "stdout_observed_bytes", "stdout_prefix_sha256", "stdout_truncated", "timed_out",
    "tree_reaped"]) &&
    boundedIdentity(value.request_id) === value.request_id && value.backend === "windows-host-job-v1" &&
    typeof value.exit_code === "number" && Number.isSafeInteger(value.exit_code) &&
    safeBoundedCount(value.stdout_observed_bytes, 64 * 1024 * 1024) &&
    safeBoundedCount(value.stdout_captured_bytes, 64 * 1024) &&
    safeBoundedCount(value.stderr_observed_bytes, 64 * 1024 * 1024) &&
    safeBoundedCount(value.stderr_captured_bytes, 64 * 1024) &&
    value.stdout_captured_bytes <= value.stdout_observed_bytes &&
    value.stderr_captured_bytes <= value.stderr_observed_bytes &&
    isSHA256(value.stdout_prefix_sha256) && isSHA256(value.stderr_prefix_sha256) &&
    typeof value.stdout_truncated === "boolean" && typeof value.stderr_truncated === "boolean" &&
    validDate(value.started_at) && validDate(value.completed_at) &&
    Date.parse(value.completed_at) >= Date.parse(value.started_at) &&
    typeof value.timed_out === "boolean" && typeof value.cancelled === "boolean" &&
    typeof value.output_limit_exceeded === "boolean" && value.tree_reaped === true &&
    value.non_sandboxed === true && value.restricted_token === false &&
    value.low_integrity_token === false && value.job_assigned_at_creation === true &&
    value.kill_on_job_close === true && value.active_process_limit === 32 &&
    value.job_memory_limit === 2 * 1024 * 1024 * 1024 && value.stdin_closed === true &&
    value.environment_inherited === false && value.network_requested === true &&
    value.persistent_process === false && value.product_execution_enabled === true;
}

const hostCommandRiskKinds = ["network", "credential", "host_path", "policy_denial",
  "non_whitelisted_tool", "other_high_risk"] as const;

const hostCommandRiskFields = ["state", "supervisor_turn", "supervisor_tool_call_id",
  "tool_invocation_id", "mode_snapshot_id", "mode_revision", "interaction_snapshot_id",
  "interaction_revision", "execution_profile_snapshot_id", "execution_profile_revision",
  "permission_snapshot_id", "workspace_root_fingerprint", "capability_generation",
  "scope_fingerprint", "risk_kinds", "network_targets", "network_purpose",
  "credential_kinds", "host_paths", "policy_code", "policy_reason", "requested_tool",
  "other_risk_reason", "max_output_bytes", "active_process_limit", "process_memory_bytes",
  "approval_id", "approval_status", "grant_id", "grant_generation", "grant_max_uses",
  "grant_uses_remaining", "grant_expires_at", "grant_consumption_id",
  "invalidation_reason", "uncertain"];

function boundedRiskText(value: unknown, maximum: number): value is string {
  return boundedText(value, maximum) && !/[\u0000-\u001f\u007f]/u.test(value);
}

function parseRiskList(value: unknown, maximum: number): string[] | null {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length === 0 || value.length > maximum) return null;
  const result: string[] = [];
  for (const item of value) {
    if (!boundedRiskText(item, 512)) return null;
    result.push(item);
  }
  if (new Set(result).size !== result.length ||
    result.some((item, index) => index > 0 && result[index - 1]! > item)) return null;
  return result;
}

function parseHostCommandProposal(value: unknown, expectedRunID: string,
  expectedProposalID = ""): HostCommandProposalView {
  const required = ["argv", "automatic_retry_allowed", "capability_grant", "created_at",
    "environment_keys", "environment_policy", "environment_sha256",
    "evidence_instruction_authorized", "executable_path", "executable_sha256",
    "execution_authorized", "fingerprint", "id", "instruction_authorized", "mission_id",
    "network_intent", "non_sandboxed", "operator_review_required", "permission_mode",
    "permission_revision", "policy_version", "protocol_version", "purpose", "run_id",
    "session_id", "spec_fingerprint", "timeout_milliseconds", "working_directory",
    "workspace_id"];
  const optional = ["execution_replayed", "receipt", "result", "review", "review_replayed",
    "untrusted_evidence", ...hostCommandRiskFields];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    (value.protocol_version !== "host_command_proposal.v1" &&
      value.protocol_version !== "risk_escalation.v1") || value.run_id !== expectedRunID ||
    (expectedProposalID !== "" && value.id !== expectedProposalID) ||
    boundedIdentity(value.id) !== value.id || boundedIdentity(value.run_id) !== value.run_id ||
    boundedIdentity(value.mission_id) !== value.mission_id ||
    boundedIdentity(value.session_id) !== value.session_id ||
    boundedIdentity(value.workspace_id) !== value.workspace_id ||
    !boundedText(value.executable_path, 32_768) || !isSHA256(value.executable_sha256) ||
    !Array.isArray(value.argv) || value.argv.length > 64 ||
    !value.argv.every((argument) => boundedText(argument, 16 * 1024)) ||
    !boundedText(value.working_directory, 32_768) ||
    value.environment_policy !== "sanitized_host_environment.v1" ||
    !Array.isArray(value.environment_keys) || value.environment_keys.length > 48 ||
    !value.environment_keys.every((key) => boundedText(key, 256) && !key.includes("=")) ||
    !isSHA256(value.environment_sha256) || value.network_intent !== "host" ||
    !safePositiveInteger(value.timeout_milliseconds) || value.timeout_milliseconds > 600_000 ||
    !boundedText(value.purpose, 4_800) || !isSHA256(value.spec_fingerprint) ||
    !safePositiveInteger(value.permission_revision) ||
    typeof value.operator_review_required !== "boolean" || value.non_sandboxed !== true ||
    value.automatic_retry_allowed !== false || value.instruction_authorized !== false ||
    typeof value.execution_authorized !== "boolean" ||
    typeof value.capability_grant !== "boolean" || !isSHA256(value.fingerprint) ||
    !validDate(value.created_at) || value.evidence_instruction_authorized !== false ||
    (value.review_replayed !== undefined && typeof value.review_replayed !== "boolean") ||
    (value.execution_replayed !== undefined && typeof value.execution_replayed !== "boolean") ||
    (value.untrusted_evidence !== undefined &&
      (typeof value.untrusted_evidence !== "string" || value.untrusted_evidence.length > 16 * 1024))) {
    throw new APIRequestError("Host command proposal response is invalid", "INVALID_RESPONSE", 502);
  }

  const risk = value.protocol_version === "risk_escalation.v1";
  if (!risk) {
    if (value.policy_version !== "host_command_policy.v1" || value.permission_mode !== "approval" ||
      value.operator_review_required !== true || value.execution_authorized !== false ||
      value.capability_grant !== false || hostCommandRiskFields.some((field) =>
        Object.prototype.hasOwnProperty.call(value, field)) ||
      (value.untrusted_evidence !== undefined &&
        !value.untrusted_evidence.startsWith("UNTRUSTED HOST COMMAND RESULT"))) {
      throw new APIRequestError("Legacy host command proposal widened its authority",
        "INVALID_RESPONSE", 502);
    }
  } else {
    validateRiskEscalationProposal(value);
  }

  if (value.review !== undefined) {
    const review = value.review;
    if (!hasExactKeys(review, ["capability_grant", "created_at", "decision", "id", "reason",
      "reviewed_by", "single_use_execution_authorized"]) ||
      boundedIdentity(review.id) !== review.id ||
      boundedIdentity(review.reviewed_by) !== review.reviewed_by ||
      !boundedText(review.reason, 4_096) || !validDate(review.created_at) ||
      (review.decision !== "approve" && review.decision !== "deny") ||
      (risk
        ? (review.decision === "deny"
          ? review.single_use_execution_authorized !== false || review.capability_grant !== false
          : review.single_use_execution_authorized !== !value.capability_grant ||
            review.capability_grant !== value.capability_grant)
        : review.single_use_execution_authorized !== (review.decision === "approve") ||
          review.capability_grant !== false) ||
      (risk && review.id !== value.approval_id)) {
      throw new APIRequestError("Host command review response is invalid", "INVALID_RESPONSE", 502);
    }
  }
  if (value.result !== undefined) {
    const result = value.result;
    if (!hasExactKeys(result, ["automatic_retry_allowed", "content_sha256", "created_at", "id",
      "instruction_authorized", "raw_output_persisted", "source_kind", "source_ref", "status"]) ||
      boundedIdentity(result.id) !== result.id ||
      boundedIdentity(result.source_ref) !== result.source_ref ||
      (result.status !== "completed" && result.status !== "failed") ||
      result.source_kind !== "go_command_result" || !isSHA256(result.content_sha256) ||
      result.instruction_authorized !== false || result.raw_output_persisted !== false ||
      result.automatic_retry_allowed !== false || !validDate(result.created_at)) {
      throw new APIRequestError("Host command result response is invalid", "INVALID_RESPONSE", 502);
    }
  }
  if ((value.result === undefined) !== (value.receipt === undefined) ||
    (value.receipt !== undefined && !parseHostCommandProposalReceipt(value.receipt)) ||
    value.result !== undefined && value.review === undefined ||
    value.untrusted_evidence !== undefined && value.result === undefined ||
    (risk && ((value.approval_status === "pending") !== (value.review === undefined) ||
      value.result !== undefined && value.state !==
        (isRecord(value.result) ? value.result.status : undefined) ||
      value.untrusted_evidence !== undefined &&
        !value.untrusted_evidence.startsWith("UNTRUSTED APPROVED RISK ESCALATION RESULT")))) {
    throw new APIRequestError("Host command proposal response crossed its execution boundary",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as HostCommandProposalView;
}

function validateRiskEscalationProposal(value: Record<string, unknown>): void {
  const riskRequired = ["state", "supervisor_turn", "supervisor_tool_call_id",
    "tool_invocation_id", "mode_snapshot_id", "mode_revision", "interaction_snapshot_id",
    "interaction_revision", "execution_profile_snapshot_id", "execution_profile_revision",
    "permission_snapshot_id", "workspace_root_fingerprint", "capability_generation",
    "scope_fingerprint", "risk_kinds", "max_output_bytes", "active_process_limit",
    "process_memory_bytes", "approval_id", "approval_status"];
  const kinds = parseRiskList(value.risk_kinds, 6);
  const networkTargets = parseRiskList(value.network_targets, 16);
  const credentialKinds = parseRiskList(value.credential_kinds, 16);
  const hostPaths = parseRiskList(value.host_paths, 16);
  const validStates = ["waiting_approval", "approved", "denied", "completed", "failed",
    "invalidated"];
  const validApprovalStatuses = ["pending", "approved", "denied"];
  const grantPresent = value.grant_id !== undefined;
  if (riskRequired.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.policy_version !== "risk_escalation_policy.v1" ||
    value.permission_mode !== "workspace_access" || !validStates.includes(String(value.state)) ||
    !safePositiveInteger(value.supervisor_turn) ||
    boundedIdentity(value.supervisor_tool_call_id) !== value.supervisor_tool_call_id ||
    boundedIdentity(value.tool_invocation_id) !== value.tool_invocation_id ||
    boundedIdentity(value.mode_snapshot_id) !== value.mode_snapshot_id ||
    !safePositiveInteger(value.mode_revision) ||
    boundedIdentity(value.interaction_snapshot_id) !== value.interaction_snapshot_id ||
    !safePositiveInteger(value.interaction_revision) ||
    boundedIdentity(value.execution_profile_snapshot_id) !== value.execution_profile_snapshot_id ||
    !safePositiveInteger(value.execution_profile_revision) ||
    boundedIdentity(value.permission_snapshot_id) !== value.permission_snapshot_id ||
    !isSHA256(value.workspace_root_fingerprint) || !isSHA256(value.capability_generation) ||
    !isSHA256(value.scope_fingerprint) || kinds === null || kinds.length === 0 ||
    !kinds.every((kind) => hostCommandRiskKinds.includes(
      kind as typeof hostCommandRiskKinds[number])) || networkTargets === null ||
    credentialKinds === null || hostPaths === null ||
    value.max_output_bytes !== 64 * 1024 * 1024 || value.active_process_limit !== 32 ||
    value.process_memory_bytes !== 2 * 1024 * 1024 * 1024 ||
    boundedIdentity(value.approval_id) !== value.approval_id ||
    !validApprovalStatuses.includes(String(value.approval_status)) ||
    value.operator_review_required !== (value.approval_status === "pending") ||
    value.execution_authorized !== (value.approval_status === "approved") ||
    value.capability_grant !== grantPresent ||
    kinds.includes("network") !==
      (networkTargets.length > 0 && boundedRiskText(value.network_purpose, 1_200)) ||
    kinds.includes("credential") !== (credentialKinds.length > 0) ||
    kinds.includes("host_path") !== (hostPaths.length > 0) ||
    kinds.includes("policy_denial") !==
      (boundedRiskText(value.policy_code, 512) && boundedRiskText(value.policy_reason, 1_200)) ||
    kinds.includes("non_whitelisted_tool") !== boundedRiskText(value.requested_tool, 512) ||
    kinds.includes("other_high_risk") !== boundedRiskText(value.other_risk_reason, 1_200) ||
    (value.invalidation_reason !== undefined &&
      (!boundedRiskText(value.invalidation_reason, 512) || value.state !== "invalidated")) ||
    (value.state === "invalidated" && value.invalidation_reason === undefined) ||
    (value.uncertain !== undefined && typeof value.uncertain !== "boolean") ||
    (value.uncertain === true && value.state !== "invalidated" && value.state !== "failed") ||
    (value.state === "waiting_approval" && value.approval_status !== "pending") ||
    (value.state === "denied" && value.approval_status !== "denied") ||
    (["approved", "completed", "failed"].includes(String(value.state)) &&
      value.approval_status !== "approved")) {
    throw new APIRequestError("Risk escalation proposal response is invalid",
      "INVALID_RESPONSE", 502);
  }
  if (grantPresent) {
    if (boundedIdentity(value.grant_id) !== value.grant_id ||
      !safePositiveInteger(value.grant_generation) ||
      !safePositiveInteger(value.grant_max_uses) || value.grant_max_uses > 8 ||
      !safeBoundedCount(value.grant_uses_remaining, Number(value.grant_max_uses)) ||
      !validDate(value.grant_expires_at) ||
      boundedIdentity(value.grant_consumption_id) !== value.grant_consumption_id) {
      throw new APIRequestError("Bounded risk escalation grant response is invalid",
        "INVALID_RESPONSE", 502);
    }
  } else if (["grant_generation", "grant_max_uses", "grant_uses_remaining",
    "grant_expires_at", "grant_consumption_id"].some((field) =>
    Object.prototype.hasOwnProperty.call(value, field))) {
    throw new APIRequestError("Risk escalation response contains detached grant metadata",
      "INVALID_RESPONSE", 502);
  }
}

const availableRouteCredentialStatuses = ["not_required", "configured", "not_configured",
  "invalid_configuration", "disabled", "unavailable"] as const;
const availableRouteQualificationStatuses = ["unavailable", "not_configured", "available",
  "protocol_mismatch", "auth_failed", "network_failed", "rate_limit", "capacity",
  "model_unsupported", "trusted_builtin", "qualification_required", "verified"] as const;
const unavailableRouteReasons = ["", "provider_disabled", "credential_not_configured",
  "invalid_configuration", "provider_unavailable", "harness_qualification_required",
  "not_configured", "protocol_mismatch", "auth_failed", "network_failed", "rate_limit",
  "capacity", "model_unsupported"] as const;

function parseAvailableModelRoute(value: unknown): AvailableModelRouteCollectionView["routes"][number] {
  if (!hasExactKeys(value, ["credential_status", "default_for_routes", "enabled",
    "harness_ready", "model", "provider_id", "provider_name", "qualification_status",
    "selectable", "unavailable_reason"]) ||
    !boundedIdentity(value.provider_id) || !boundedText(value.provider_name, 256) ||
    !boundedIdentity(value.model) || typeof value.enabled !== "boolean" ||
    typeof value.harness_ready !== "boolean" || typeof value.selectable !== "boolean" ||
    !availableRouteCredentialStatuses.includes(value.credential_status as never) ||
    !availableRouteQualificationStatuses.includes(value.qualification_status as never) ||
    !unavailableRouteReasons.includes(value.unavailable_reason as never) ||
    !Array.isArray(value.default_for_routes) || value.default_for_routes.length > 32 ||
    !value.default_for_routes.every((route) => Boolean(boundedIdentity(route)))) {
    throw new APIRequestError("Available model route response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const defaults = value.default_for_routes as string[];
  const selectableQualification = ["available", "trusted_builtin", "verified"]
    .includes(String(value.qualification_status));
  if (new Set(defaults).size !== defaults.length ||
    (value.selectable && (!value.enabled || !value.harness_ready ||
      !["configured", "not_required"].includes(String(value.credential_status)) ||
      value.unavailable_reason !== "" || !selectableQualification)) ||
    (!value.selectable && value.unavailable_reason === "")) {
    throw new APIRequestError("Available model route response violated its eligibility contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as AvailableModelRouteCollectionView["routes"][number];
}

function parseAvailableModelRouteCollection(value: unknown): AvailableModelRouteCollectionView {
  if (!hasExactKeys(value, ["generation", "protocol_version", "routes"]) ||
    value.protocol_version !== "model_route_catalog.v1" ||
    !safeBoundedCount(value.generation, Number.MAX_SAFE_INTEGER) || !Array.isArray(value.routes) ||
    value.routes.length > 1_024) {
    throw new APIRequestError("Available model route catalog is invalid", "INVALID_RESPONSE", 502);
  }
  const routes = value.routes.map(parseAvailableModelRoute);
  const keys = routes.map((route) => `${route.provider_id}\0${route.model}`);
  if (new Set(keys).size !== keys.length) {
    throw new APIRequestError("Available model route catalog contains duplicate routes",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, routes } as AvailableModelRouteCollectionView;
}

function parseThreadModelRoute(value: unknown, threadID: string,
  request?: ThreadModelRouteControlRequestView): ThreadModelRouteView {
  const required = ["active_run_unchanged", "applies_to", "model", "protocol_version",
    "provider", "replayed", "source", "thread_id"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, "effective_run_id"]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "thread_model_route.v1" || value.thread_id !== threadID ||
    !boundedIdentity(value.thread_id) || !boundedIdentity(value.provider) ||
    !boundedIdentity(value.model) ||
    !["thread_preference", "default", "active_run"].includes(String(value.source)) ||
    !["next_run", "current_and_next"].includes(String(value.applies_to)) ||
    typeof value.active_run_unchanged !== "boolean" || typeof value.replayed !== "boolean" ||
    (value.effective_run_id !== undefined && !boundedIdentity(value.effective_run_id))) {
    throw new APIRequestError("Thread model route response is invalid", "INVALID_RESPONSE", 502);
  }
  const currentAndNext = value.applies_to === "current_and_next";
  if (currentAndNext !== (value.effective_run_id !== undefined) ||
    (value.active_run_unchanged && currentAndNext) ||
    (value.source === "active_run" && !currentAndNext) ||
    (request?.action === "select" &&
      (value.provider !== request.provider || value.model !== request.model)) ||
    (request?.action === "reset" && value.source === "thread_preference")) {
    throw new APIRequestError("Thread model route response violated its exact binding",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ThreadModelRouteView;
}

const providerSearchStates = ["network_disabled", "missing_allowlist",
  "provider_unqualified", "provider_unavailable", "ready"] as const;
const providerSearchReasons = ["run_network_disabled", "search_endpoint_not_allowlisted",
  "provider_native_qualification_required", "provider_native_qualification_failed",
  "no_active_run", "model_provider_unavailable", "provider_search_policy_disabled",
  "search_backend_not_configured", "provider_search_configuration_invalid",
  "search_backend_ready"] as const;
const providerSearchRemediations = ["enable_network_allowlist", "add_required_target",
  "qualify_provider_search", "submit_to_create_successor", "configure_search_provider",
  "enable_provider_search", "repair_provider_configuration", "none"] as const;

function validSearchTarget(value: unknown): value is string {
  if (typeof value !== "string" || value.length < 1 || value.length > 253 ||
    value !== value.trim() || value !== value.toLowerCase()) return false;
  try {
    const parsed = new URL(`https://${value}`);
    return parsed.hostname === value && parsed.username === "" && parsed.password === "" &&
      parsed.port === "" && parsed.pathname === "/" && parsed.search === "" && parsed.hash === "";
  } catch {
    return false;
  }
}

function parseProviderSearchReadiness(value: unknown,
  threadID: string): ProviderSearchReadinessView {
  const required = ["capability_grant", "network_mode", "protocol_version", "reason",
    "remediation", "runtime_ready", "state", "thread_id"];
  const optional = ["detail_code", "mode_revision", "model", "model_route", "provider",
    "required_target", "run_id", "search_policy"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "provider_search_readiness.v1" || value.thread_id !== threadID ||
    !boundedIdentity(value.thread_id) ||
    !providerSearchStates.includes(value.state as never) ||
    !providerSearchReasons.includes(value.reason as never) ||
    !providerSearchRemediations.includes(value.remediation as never) ||
    !["", "disabled", "allowlist"].includes(String(value.network_mode)) ||
    typeof value.runtime_ready !== "boolean" || value.capability_grant !== false ||
    (value.run_id !== undefined && !boundedIdentity(value.run_id)) ||
    (value.model_route !== undefined && !boundedText(value.model_route, 512)) ||
    (value.provider !== undefined && !boundedIdentity(value.provider)) ||
    (value.model !== undefined && !boundedIdentity(value.model)) ||
    (value.search_policy !== undefined && !["disabled", "searxng", "provider_native", "auto"]
      .includes(String(value.search_policy))) ||
    (value.detail_code !== undefined && !boundedText(value.detail_code, 256)) ||
    (value.required_target !== undefined && !validSearchTarget(value.required_target)) ||
    (value.mode_revision !== undefined && !safePositiveInteger(value.mode_revision))) {
    throw new APIRequestError("Provider search readiness response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const ready = value.state === "ready";
  const noActiveRun = value.reason === "no_active_run";
  if (value.runtime_ready !== ready || (ready && value.remediation !== "none") ||
    (!ready && value.remediation === "none") ||
    (value.state === "network_disabled" && value.network_mode !== "disabled") ||
    (noActiveRun !== (value.run_id === undefined)) ||
    (!noActiveRun && (value.mode_revision === undefined || value.run_id === undefined))) {
    throw new APIRequestError("Provider search readiness violated its binding",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ProviderSearchReadinessView;
}

function parseModelRouteControl(value: unknown, route: string,
  request: ModelRouteControlRequestView): ModelAvailabilityView["routes"][number] {
  if (!hasExactKeys(value, ["available", "harness_ready", "model", "name", "provider"]) ||
    value.name !== route || value.provider !== request.provider || value.model !== request.model ||
    value.available !== true || typeof value.harness_ready !== "boolean") {
    throw new APIRequestError("Model route response violated its exact binding",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ModelAvailabilityView["routes"][number];
}

function parseModelHarnessQualification(value: unknown,
  request: ModelHarnessQualificationRequestView): ModelHarnessQualificationView {
  if (!hasExactKeys(value, ["duration_ms", "failure_reason", "harness", "model", "model_calls",
    "network_request_attempted", "outcome", "protocol_version", "provider",
    "qualification_status", "response_content_returned", "retryable", "status",
    "synthetic_tool_calls", "tool_executed"]) ||
    value.protocol_version !== "model_harness_qualification.v1" ||
    value.provider !== request.provider || value.model !== request.model ||
    !["qualified", "incompatible", "unreachable"].includes(String(value.status)) ||
    !qualificationStatusValid(value.qualification_status) ||
    !providerFailureReasonValid(value.failure_reason) ||
    !providerOutcomeValid(value.outcome) || typeof value.retryable !== "boolean" ||
    typeof value.network_request_attempted !== "boolean" ||
    !safeBoundedCount(value.model_calls, 2) ||
    !safeBoundedCount(value.synthetic_tool_calls, 16) ||
    value.tool_executed !== false || value.response_content_returned !== false ||
    !safeBoundedCount(value.duration_ms, 60_000) ||
    !qualificationResultSemanticsValid(value.status, value.outcome, value.failure_reason,
      value.retryable, value.network_request_attempted, value.model_calls)) {
    throw new APIRequestError("Model Harness qualification response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const harness = parseModelHarnessAvailability(value.harness);
  if (harness.model !== request.model ||
    (value.status === "qualified" && !harness.root_eligible)) {
    throw new APIRequestError("Model Harness qualification binding is invalid",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ModelHarnessQualificationView;
}

function parseProviderDiagnostic(value: unknown, request: ProviderDiagnosticRequestView): ProviderDiagnosticView {
  if (!hasExactKeys(value, ["duration_ms", "failure_reason", "model", "model_called",
    "network_request_attempted", "outcome", "protocol_version", "provider",
    "qualification_status", "response_content_returned", "retryable", "status",
    "tool_called"]) ||
    value.protocol_version !== "provider_diagnostic.v1" || value.provider !== request.provider ||
    value.model !== request.model || (value.status !== "reachable" && value.status !== "unreachable") ||
    !qualificationStatusValid(value.qualification_status) ||
    !providerFailureReasonValid(value.failure_reason) ||
    !providerOutcomeValid(value.outcome) || typeof value.retryable !== "boolean" ||
    typeof value.network_request_attempted !== "boolean" || typeof value.model_called !== "boolean" ||
    value.tool_called !== false || value.response_content_returned !== false ||
    !safeBoundedCount(value.duration_ms, 60_000) ||
    !diagnosticResultSemanticsValid(value.status, value.outcome, value.failure_reason,
      value.retryable, value.network_request_attempted, value.model_called ? 1 : 0)) {
    throw new APIRequestError("Provider diagnostic response violated its content-free contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ProviderDiagnosticView;
}

function providerFailureReasonValid(value: unknown): boolean {
  return typeof value === "string" &&
    ["none", "not_configured", "authentication", "network", "rate_limit", "capacity",
      "model_not_found", "protocol_incompatible"].includes(value);
}

// qualificationStatusValid accepts the closed per-endpoint qualification
// taxonomy. The empty string means the model was never diagnosed: the field
// is omitted server-side until a diagnostic or Harness qualification observes
// the endpoint.
function qualificationStatusValid(value: unknown): boolean {
  return typeof value === "string" &&
    ["", "not_configured", "available", "protocol_mismatch", "auth_failed",
      "network_failed", "rate_limit", "capacity", "model_unsupported"].includes(value);
}

function providerOutcomeValid(value: unknown): boolean {
  return typeof value === "string" &&
    ["success", "retryable", "rate_limited", "invalid_response", "cancelled",
      "permanent"].includes(value);
}

function qualificationResultSemanticsValid(status: unknown, outcome: unknown, reason: unknown,
  retryable: unknown, networkAttempted: unknown, modelCalls: unknown): boolean {
  if (status === "qualified") {
    return outcome === "success" && reason === "none" && retryable === false &&
      (modelCalls === 0 || modelCalls === 2);
  }
  if (reason === "not_configured") {
    return status === "unreachable" && outcome === "permanent" && retryable === false &&
      networkAttempted === false && modelCalls === 0;
  }
  if (status === "incompatible") {
    return outcome === "invalid_response" && reason === "protocol_incompatible" &&
      retryable === false;
  }
  if (status !== "unreachable" || reason === "none" || outcome === "success" ||
    outcome === "invalid_response") {
    return false;
  }
  return retryable === (outcome === "retryable" || outcome === "rate_limited");
}

function diagnosticResultSemanticsValid(status: unknown, outcome: unknown, reason: unknown,
  retryable: unknown, networkAttempted: unknown, modelCalls: unknown): boolean {
  if (status === "reachable") {
    return outcome === "success" && reason === "none" && retryable === false && modelCalls === 1;
  }
  if (reason === "not_configured") {
    return outcome === "permanent" && retryable === false && networkAttempted === false &&
      modelCalls === 0;
  }
  if (reason === "none" || outcome === "success" || modelCalls !== 1) {
    return false;
  }
  return retryable === (outcome === "retryable" || outcome === "rate_limited");
}

function parseProviderCredentialStatus(value: unknown,
  expectedProvider = ""): ProviderCredentialStatusView {
  if (!hasExactKeys(value, ["configured", "plaintext_returned", "protocol_version",
    "provider", "registry_generation", "registry_reloaded", "restart_required",
    "store_available", "store_kind"]) ||
    value.protocol_version !== "provider_credential.v1" ||
    !validProviderCredentialName(value.provider) ||
    (expectedProvider !== "" && value.provider !== expectedProvider) ||
    typeof value.configured !== "boolean" || typeof value.store_available !== "boolean" ||
    !boundedText(value.store_kind, 128) || value.plaintext_returned !== false ||
    typeof value.restart_required !== "boolean" || typeof value.registry_reloaded !== "boolean" ||
    !safeBoundedCount(value.registry_generation, Number.MAX_SAFE_INTEGER) ||
    (value.registry_reloaded && (value.restart_required || value.registry_generation < 1))) {
    throw new APIRequestError("Provider credential status violated its plaintext-free contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as ProviderCredentialStatusView;
}

function parseProviderCredentialList(value: unknown): ProviderCredentialListView {
  if (!hasExactKeys(value, ["items", "protocol_version"]) ||
    value.protocol_version !== "provider_credential.v1" || !Array.isArray(value.items) ||
    value.items.length < 4 || value.items.length > 68) {
    throw new APIRequestError("Provider credential list is invalid", "INVALID_RESPONSE", 502);
  }
  const items = value.items.map((item) => parseProviderCredentialStatus(item));
  if (new Set(items.map((item) => item.provider)).size !== items.length ||
    items.some((item) => item.restart_required || item.registry_reloaded) ||
    new Set(items.map((item) => item.registry_generation)).size !== 1) {
    throw new APIRequestError("Provider credential list widened status authority",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as ProviderCredentialListView;
}

const customProviderDefinitionKeys = ["advanced_config", "default_model", "display_name",
  "enabled", "endpoint_url", "id", "models", "native_web_search_capability", "note",
  "revision", "search_mode", "transport", "version", "website_url"];

function validProviderCredentialName(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 64 &&
    /^[\p{L}\p{N}_-]+$/u.test(value);
}

function validCustomProviderID(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 64 &&
    /^[a-z][a-z0-9_-]*$/u.test(value) &&
    !["anthropic", "deepseek", "mimo", "mock", "ollama", "openai"].includes(value);
}

function validProviderAdvancedConfig(value: unknown): boolean {
  if (!isRecord(value)) return false;
  try {
    if (new TextEncoder().encode(JSON.stringify(value)).length > 65_536) return false;
  } catch {
    return false;
  }
  let nodes = 0;
  const visit = (current: unknown, depth: number): boolean => {
    nodes += 1;
    if (nodes > 2_048 || depth > 16) return false;
    if (current === null || typeof current === "boolean" || typeof current === "number" ||
      typeof current === "string") return true;
    if (Array.isArray(current)) return current.every((child) => visit(child, depth + 1));
    return isRecord(current) && Object.entries(current).every(([key, child]) =>
      key.length > 0 && key.length <= 128 && visit(child, depth + 1));
  };
  return visit(value, 1);
}

function parseProviderDefinition(value: unknown): ProviderDefinitionView {
  if (!hasExactKeys(value, customProviderDefinitionKeys) ||
    value.version !== "provider_definition.v1" || !validCustomProviderID(value.id) ||
    !boundedText(value.display_name, 128) ||
    typeof value.note !== "string" || value.note.length > 2_048 ||
    typeof value.website_url !== "string" || value.website_url.length > 2_048 ||
    typeof value.endpoint_url !== "string" || value.endpoint_url.length === 0 ||
    value.endpoint_url.length > 2_048 || !boundedIdentity(value.default_model) ||
    !Array.isArray(value.models) || value.models.length < 1 || value.models.length > 128 ||
    !value.models.every((model) => Boolean(boundedIdentity(model))) ||
    new Set(value.models).size !== value.models.length ||
    !value.models.includes(value.default_model as string) ||
    !["openai_chat_completions", "openai_responses", "anthropic_messages"]
      .includes(String(value.transport)) ||
    !["disabled", "auto", "searxng", "provider_native"].includes(String(value.search_mode)) ||
    !["unsupported", "declared_unverified"].includes(
      String(value.native_web_search_capability)) ||
    (value.search_mode === "provider_native" &&
      value.native_web_search_capability !== "declared_unverified") ||
    typeof value.enabled !== "boolean" || !safeBoundedCount(value.revision, Number.MAX_SAFE_INTEGER) ||
    !validProviderAdvancedConfig(value.advanced_config)) {
    throw new APIRequestError("Custom Provider definition is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ProviderDefinitionView;
}

function parseProviderDefinitionCollection(value: unknown): ProviderDefinitionCollectionView {
  if (!hasExactKeys(value, ["providers", "revision", "version"]) ||
    value.version !== "provider_definition_collection.v1" ||
    !safeBoundedCount(value.revision, Number.MAX_SAFE_INTEGER) ||
    !Array.isArray(value.providers) || value.providers.length > 64) {
    throw new APIRequestError("Custom Provider collection is invalid", "INVALID_RESPONSE", 502);
  }
  const providers = value.providers.map(parseProviderDefinition);
  if (new Set(providers.map((provider) => provider.id)).size !== providers.length ||
    providers.some((provider, index) => index > 0 && providers[index - 1].id >= provider.id)) {
    throw new APIRequestError("Custom Provider collection order is invalid", "INVALID_RESPONSE", 502);
  }
  return { ...value, providers } as ProviderDefinitionCollectionView;
}

function parseProviderDefinitionMutation(value: unknown,
  expectedProvider: string, remove: boolean): ProviderDefinitionMutationView {
  if (!hasRequiredOnlyKeys(value, ["collection", "protocol_version", "registry_generation",
    "registry_reloaded"], ["definition", "deleted_id"]) ||
    value.protocol_version !== "provider_definition_control.v1" ||
    value.registry_reloaded !== true ||
    !safePositiveInteger(value.registry_generation)) {
    throw new APIRequestError("Custom Provider mutation result is invalid", "INVALID_RESPONSE", 502);
  }
  const collection = parseProviderDefinitionCollection(value.collection);
  if (remove) {
    if (value.deleted_id !== expectedProvider || value.definition !== undefined ||
      collection.providers.some((provider) => provider.id === expectedProvider)) {
      throw new APIRequestError("Custom Provider deletion returned the wrong identity",
        "INVALID_RESPONSE", 502);
    }
    return { ...value, collection } as ProviderDefinitionMutationView;
  }
  const definition = parseProviderDefinition(value.definition);
  if (definition.id !== expectedProvider || value.deleted_id !== undefined ||
    !collection.providers.some((provider) => provider.id === expectedProvider &&
      provider.revision === definition.revision)) {
    throw new APIRequestError("Custom Provider upsert returned the wrong identity",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, collection, definition } as ProviderDefinitionMutationView;
}

const uiEvidenceStatuses = ["not_run", "running", "passed", "failed", "cancelled",
  "timed_out", "interrupted"] as const;
const uiEvidenceFailureStages = ["none", "build", "launch", "readiness", "navigation",
  "selector", "assertion", "console", "network", "capture", "cleanup"] as const;
const uiEvidenceStepKinds = ["navigate", "click", "type", "assert_present", "assert_absent",
  "capture"] as const;

function validUIEvidenceText(value: unknown, maximum: number, lines = false): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= maximum &&
    value.trim() === value && !value.includes("\0") && (lines || !/[\r\n]/u.test(value));
}

function hasRequiredOnlyKeys(value: unknown, required: string[], optional: string[] = []):
  value is Record<string, unknown> {
  return isRecord(value) && hasOnlyKeys(value, [...required, ...optional]) &&
    required.every((key) => Object.prototype.hasOwnProperty.call(value, key));
}

function validUIEvidenceLoopbackURL(value: unknown): value is string {
  if (typeof value !== "string" || value.length > 4_096 || /[\r\n\t?#]/u.test(value)) return false;
  const match = /^(https?):\/\/(\[[^\]]+\]|[^/:@]+):(\d{1,5})(\/.*)$/u.exec(value);
  if (!match) return false;
  const port = Number(match[3]);
  const host = match[2].replace(/^\[|\]$/gu, "").toLowerCase();
  const ipv4 = /^127(?:\.\d{1,3}){3}$/u.test(host) &&
    host.split(".").every((part) => Number(part) <= 255);
  const ipv6 = host === "::1" || /^::ffff:127(?:\.\d{1,3}){3}$/u.test(host);
  if ((!ipv4 && !ipv6) || port < 1 || port > 65_535) return false;
  try {
    const parsed = new URL(value);
    return parsed.username === "" && parsed.password === "" && parsed.pathname !== "" &&
      parsed.search === "" && parsed.hash === "";
  } catch {
    return false;
  }
}

function sameUIEvidenceOrigin(left: string, right: string): boolean {
  try {
    const leftURL = new URL(left);
    const rightURL = new URL(right);
    return leftURL.protocol === rightURL.protocol &&
      leftURL.hostname.toLowerCase() === rightURL.hostname.toLowerCase() &&
      leftURL.port === rightURL.port;
  } catch {
    return false;
  }
}

function validUIEvidenceSource(value: unknown): boolean {
  const required = ["commit", "dirty", "dirty_digest", "index_sha256", "manifest_sha256",
    "repository_kind", "root_fingerprint"];
  if (!hasRequiredOnlyKeys(value, required, ["branch"]) ||
    !["git", "non_git"].includes(String(value.repository_kind)) ||
    typeof value.dirty !== "boolean" || !isSHA256(value.dirty_digest) ||
    !isSHA256(value.root_fingerprint) || !isSHA256(value.index_sha256) ||
    !isSHA256(value.manifest_sha256) ||
    (Object.prototype.hasOwnProperty.call(value, "branch") &&
      !validUIEvidenceText(value.branch, 255))) return false;
  return value.repository_kind === "git"
    ? typeof value.commit === "string" && /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/u.test(value.commit)
    : value.commit === "non-git";
}

function validUIEvidenceCommandRecipe(value: unknown): boolean {
  const keys = ["canonical_argv", "credentials", "environment_names", "environment_sha256",
    "executable_name", "executable_path_sha256", "executable_sha256", "fingerprint", "network",
    "profile", "protocol_version", "purpose", "timeout_milliseconds", "working_directory"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "command-runtime.v2" ||
    !["powershell", "bash", "process"].includes(String(value.profile)) ||
    !validUIEvidenceText(value.executable_name, 512) || /[\\/]/u.test(value.executable_name) ||
    !isSHA256(value.executable_path_sha256) || !isSHA256(value.executable_sha256) ||
    !isSHA256(value.environment_sha256) || !isSHA256(value.fingerprint) ||
    !safePositiveInteger(value.timeout_milliseconds) || value.timeout_milliseconds > 1_800_000 ||
    value.network !== "disabled" || value.credentials !== "none" ||
    !validUIEvidenceText(value.working_directory, 4_096) ||
    !validUIEvidenceText(value.purpose, 1_200) || !Array.isArray(value.canonical_argv) ||
    value.canonical_argv.length < 1 || value.canonical_argv.length > 128 ||
    value.canonical_argv.some((entry) => !validUIEvidenceText(entry, 65_536, true)) ||
    !Array.isArray(value.environment_names) || value.environment_names.length > 32 ||
    value.environment_names.some((entry) => !validUIEvidenceText(entry, 128))) return false;
  const names = value.environment_names as string[];
  return names.every((name, index) => index === 0 ||
    name.toLowerCase() > names[index - 1].toLowerCase());
}

function validUIEvidenceReadiness(value: unknown): boolean {
  if (!hasExactKeys(value, ["expected_status", "interval_milliseconds", "method",
    "timeout_milliseconds", "url"]) || !validUIEvidenceLoopbackURL(value.url) ||
    value.method !== "GET" || !safePositiveInteger(value.timeout_milliseconds) ||
    value.timeout_milliseconds < 100 || value.timeout_milliseconds > 120_000 ||
    !safePositiveInteger(value.interval_milliseconds) || value.interval_milliseconds < 10 ||
    value.interval_milliseconds > 5_000 || !Array.isArray(value.expected_status) ||
    value.expected_status.length < 1 || value.expected_status.length > 16) return false;
  return value.expected_status.every((status, index, statuses) => Number.isInteger(status) &&
    status >= 100 && status <= 599 && (index === 0 || status > statuses[index - 1]));
}

function validUIEvidenceEnvironment(value: unknown): boolean {
  if (!hasExactKeys(value, ["locale", "reduced_motion", "theme", "viewport"]) ||
    !hasExactKeys(value.viewport, ["dpr", "height", "width"]) ||
    !safePositiveInteger(value.viewport.width) || !safePositiveInteger(value.viewport.height) ||
    value.viewport.width < 320 || value.viewport.width > 7_680 ||
    value.viewport.height < 240 || value.viewport.height > 4_320 ||
    typeof value.viewport.dpr !== "number" || !Number.isFinite(value.viewport.dpr) ||
    value.viewport.dpr < 0.5 || value.viewport.dpr > 4 ||
    value.viewport.width * value.viewport.dpr > 7_680 ||
    value.viewport.height * value.viewport.dpr > 4_320 ||
    !["light", "dark"].includes(String(value.theme)) ||
    typeof value.reduced_motion !== "boolean" || !validUIEvidenceText(value.locale, 64)) return false;
  const parts = value.locale.split("-");
  return parts.length >= 1 && parts.length <= 3 && parts[0].length >= 2 &&
    parts[0].length <= 8 && parts.every((part) => /^[A-Za-z0-9]*$/u.test(part));
}

function validUIEvidenceFixture(value: unknown): boolean {
  return hasExactKeys(value, ["data_sha256", "deterministic", "name", "page_state", "seed",
    "synthetic"]) && validUIEvidenceText(value.name, 256) &&
    validUIEvidenceText(value.seed, 256) && validUIEvidenceText(value.page_state, 8_192, true) &&
    isSHA256(value.data_sha256) && value.deterministic === true &&
    typeof value.synthetic === "boolean";
}

function validUIEvidenceSteps(value: unknown): boolean {
  if (!Array.isArray(value) || value.length < 1 || value.length > 128) return false;
  const identifiers = new Set<string>();
  for (const entry of value) {
    if (!hasRequiredOnlyKeys(entry, ["capture_after", "id", "kind"],
      ["input_sha256", "selector"]) || !boundedIdentity(entry.id) ||
      !uiEvidenceStepKinds.includes(String(entry.kind) as typeof uiEvidenceStepKinds[number]) ||
      typeof entry.capture_after !== "boolean") return false;
    const requiresSelector = ["click", "type", "assert_present", "assert_absent"]
      .includes(String(entry.kind));
    const hasSelector = Object.prototype.hasOwnProperty.call(entry, "selector");
    const hasInput = Object.prototype.hasOwnProperty.call(entry, "input_sha256");
    if (requiresSelector !== hasSelector ||
      (hasSelector && !validUIEvidenceText(entry.selector, 2_048)) ||
      ((entry.kind === "type") !== hasInput) || (hasInput && !isSHA256(entry.input_sha256)) ||
      identifiers.has(entry.id as string)) return false;
    identifiers.add(entry.id as string);
  }
  return value[0].kind === "navigate";
}

function validUIEvidenceBrowser(value: unknown): boolean {
  return hasExactKeys(value, ["driver_protocol", "executable_sha256", "headless", "product",
    "temporary_profile", "version"]) && ["chrome", "edge"].includes(String(value.product)) &&
    validUIEvidenceText(value.version, 128) && isSHA256(value.executable_sha256) &&
    value.driver_protocol === "restricted-cdp-ui-evidence.v1" &&
    typeof value.headless === "boolean" && value.temporary_profile === true;
}

function parseUIEvidenceAttempt(value: unknown, expectedRunID = ""): UIEvidenceAttempt {
  const required = ["artifact_bytes", "artifact_count", "cleanup", "created_at", "diagnostics",
    "failure_stage", "manifest", "operation_digest", "protocol_version", "request_fingerprint",
    "status", "updated_at", "version"];
  const optional = ["completed_at", "failure_code", "failure_message", "started_at"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "ui-evidence-attempt.v1" ||
    !uiEvidenceStatuses.includes(String(value.status) as typeof uiEvidenceStatuses[number]) ||
    !uiEvidenceFailureStages.includes(String(value.failure_stage) as typeof uiEvidenceFailureStages[number]) ||
    !isSHA256(value.operation_digest) || !isSHA256(value.request_fingerprint) ||
    !safePositiveInteger(value.version) || !safeBoundedCount(value.artifact_count, 10_000) ||
    !safeBoundedCount(value.artifact_bytes, 128 * 1024 * 1024) ||
    !validDate(value.created_at) || !validDate(value.updated_at) ||
    !isRecord(value.manifest) || !isRecord(value.cleanup) || !isRecord(value.diagnostics)) {
    throw new APIRequestError("UI evidence attempt is invalid", "INVALID_RESPONSE", 502);
  }
  const manifest = value.manifest;
  if (!hasExactKeys(manifest, ["attempt_id", "authority", "browser", "capture", "created_at",
    "environment", "failure_policy", "fingerprint", "fixture", "mission_id",
    "protocol_version", "readiness", "route", "run_id", "session_id", "source", "start",
    "steps", "url", "workspace_id"].concat(Object.prototype.hasOwnProperty.call(manifest, "build") ? ["build"] : [])) ||
    manifest.protocol_version !== "ui-evidence.v1" || !boundedIdentity(manifest.attempt_id) ||
    !boundedIdentity(manifest.run_id) || !boundedIdentity(manifest.mission_id) ||
    !boundedIdentity(manifest.session_id) || !boundedIdentity(manifest.workspace_id) ||
    (expectedRunID !== "" && manifest.run_id !== expectedRunID) ||
    !validDate(manifest.created_at) || !isSHA256(manifest.fingerprint) ||
    manifest.fingerprint !== value.request_fingerprint || !validUIEvidenceLoopbackURL(manifest.url) ||
    !validUIEvidenceText(manifest.route, 4_096) || !manifest.route.startsWith("/") ||
    manifest.route.startsWith("//") || new URL(manifest.url).pathname !== manifest.route ||
    !isRecord(manifest.authority) || !isRecord(manifest.browser) ||
    !isRecord(manifest.capture) || !isRecord(manifest.environment) ||
    !isRecord(manifest.failure_policy) || !isRecord(manifest.fixture) ||
    !isRecord(manifest.readiness) || !isRecord(manifest.source) || !isRecord(manifest.start) ||
    !validUIEvidenceSource(manifest.source) || !validUIEvidenceCommandRecipe(manifest.start) ||
    (Object.prototype.hasOwnProperty.call(manifest, "build") &&
      !validUIEvidenceCommandRecipe(manifest.build)) || !validUIEvidenceReadiness(manifest.readiness) ||
    !sameUIEvidenceOrigin(manifest.url, manifest.readiness.url as string) ||
    !validUIEvidenceBrowser(manifest.browser) || !validUIEvidenceEnvironment(manifest.environment) ||
    !validUIEvidenceFixture(manifest.fixture) || !validUIEvidenceSteps(manifest.steps)) {
    throw new APIRequestError("UI evidence manifest is invalid", "INVALID_RESPONSE", 502);
  }
  const capture = manifest.capture as Record<string, unknown>;
  if (!hasExactKeys(manifest.authority, ["credential_access", "network_access",
    "personal_profile", "process_start", "request_mutation", "verification_pass"]) ||
    Object.values(manifest.authority).some((entry) => entry !== false) ||
    !hasExactKeys(manifest.capture, ["accessibility", "console", "dom", "mask_selectors",
      "network", "performance", "screenshot", "video"]) ||
    ["accessibility", "console", "dom", "network", "performance", "screenshot"]
      .some((key) => capture[key] !== true) ||
    !hasExactKeys(manifest.failure_policy, ["fail_on_console_error", "fail_on_http_status",
      "fail_on_page_error", "fail_on_request_error"]) ||
    Object.values(manifest.failure_policy).some((entry) => entry !== true) ||
    manifest.capture.video !== false || !Array.isArray(manifest.capture.mask_selectors) ||
    manifest.capture.mask_selectors.length > 32 ||
    manifest.capture.mask_selectors.some((entry) => !validUIEvidenceText(entry, 2_048)) ||
    new Set(manifest.capture.mask_selectors).size !== manifest.capture.mask_selectors.length) {
    throw new APIRequestError("UI evidence widened its evidence authority", "INVALID_RESPONSE", 502);
  }
  const cleanupKeys = ["application_tree_reaped", "browser_tree_reaped", "network_released",
    "port_released", "profile_removed"];
  const diagnosticKeys = ["allowed_requests", "blocked_requests", "console_errors",
    "console_warnings", "failed_requests", "http_failures", "page_errors"];
  const cleanup = value.cleanup as Record<string, unknown>;
  const diagnostics = value.diagnostics as Record<string, unknown>;
  if (!hasExactKeys(cleanup, cleanupKeys) ||
    cleanupKeys.some((key) => typeof cleanup[key] !== "boolean") ||
    !hasExactKeys(diagnostics, diagnosticKeys) ||
    diagnosticKeys.some((key) => !safeBoundedCount(diagnostics[key], 1_000_000))) {
    throw new APIRequestError("UI evidence cleanup or diagnostics are invalid", "INVALID_RESPONSE", 502);
  }
  const status = String(value.status);
  const hasStarted = Object.prototype.hasOwnProperty.call(value, "started_at");
  const hasCompleted = Object.prototype.hasOwnProperty.call(value, "completed_at");
  const hasFailureCode = Object.prototype.hasOwnProperty.call(value, "failure_code");
  const hasFailureMessage = Object.prototype.hasOwnProperty.call(value, "failure_message");
  const started = hasStarted && typeof value.started_at === "string" && validDate(value.started_at);
  const completed = hasCompleted && typeof value.completed_at === "string" && validDate(value.completed_at);
  const hasExecutionResidue = diagnosticKeys.some((key) => diagnostics[key] !== 0) ||
    cleanupKeys.some((key) => cleanup[key] !== false) || value.artifact_count !== 0 ||
    value.artifact_bytes !== 0;
  const createdAt = Date.parse(String(value.created_at));
  const updatedAt = Date.parse(String(value.updated_at));
  const startedAt = started ? Date.parse(String(value.started_at)) : 0;
  const completedAt = completed ? Date.parse(String(value.completed_at)) : 0;
  if (updatedAt < createdAt || (started && startedAt < createdAt) ||
    (completed && (!started || completedAt < startedAt || updatedAt < completedAt)) ||
    (hasStarted && !started) || (hasCompleted && !completed) ||
    (hasFailureCode && (typeof value.failure_code !== "string" || value.failure_code === "")) ||
    (hasFailureMessage && typeof value.failure_message !== "string") ||
    ((value.artifact_count === 0) !== (value.artifact_bytes === 0)) ||
    (status === "not_run" && (value.version !== 1 || started || completed || hasFailureCode || hasFailureMessage ||
      value.failure_stage !== "none" || hasExecutionResidue)) ||
    (status === "running" && (value.version !== 2 || !started || completed || hasFailureCode || hasFailureMessage ||
      value.failure_stage !== "none" || hasExecutionResidue)) ||
    (status === "passed" && (value.version !== 3 || !started || !completed || value.failure_stage !== "none" ||
      hasFailureCode || hasFailureMessage || value.artifact_count < 1 || value.artifact_bytes < 1 ||
      cleanupKeys.some((key) => cleanup[key] !== true) ||
      ["console_errors", "page_errors", "failed_requests", "http_failures", "blocked_requests"]
        .some((key) => diagnostics[key] !== 0))) ||
    (!["not_run", "running", "passed"].includes(status) &&
      (value.version !== 3 || !started || !completed || value.failure_stage === "none" ||
        typeof value.failure_code !== "string" || value.failure_code === ""))) {
    throw new APIRequestError("UI evidence status is not fail-closed", "INVALID_RESPONSE", 502);
  }
  return value as unknown as UIEvidenceAttempt;
}

function parseUIEvidenceArtifactMetadata(value: unknown, attempt: UIEvidenceAttempt):
  UIEvidenceArtifactMetadata {
  const required = ["attempt_id", "bytes", "created_at", "fingerprint", "id", "kind", "mime",
    "protocol_version", "redacted", "retention_policy", "run_id", "sha256", "source_commit",
    "step_id", "untrusted", "viewport"];
  const optional = ["height", "width"];
  const attemptStartedAt = attempt.started_at ? Date.parse(attempt.started_at) : Number.NaN;
  const attemptCompletedAt = attempt.completed_at ? Date.parse(attempt.completed_at) : undefined;
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "ui-evidence-artifact.v1" ||
    value.attempt_id !== attempt.manifest.attempt_id || value.run_id !== attempt.manifest.run_id ||
    !boundedIdentity(value.id) || !boundedIdentity(value.step_id) || !isSHA256(value.sha256) ||
    !isSHA256(value.fingerprint) || value.source_commit !== attempt.manifest.source.commit ||
    !safePositiveInteger(value.bytes) ||
    value.bytes > 32 * 1024 * 1024 || typeof value.mime !== "string" ||
    !["screenshot", "dom", "accessibility", "console", "network", "performance"]
      .includes(String(value.kind)) || typeof value.redacted !== "boolean" ||
    value.retention_policy !== "run_history" || value.untrusted !== true ||
    !validDate(value.created_at) || !Number.isFinite(attemptStartedAt) ||
    Date.parse(String(value.created_at)) < attemptStartedAt ||
    (attemptCompletedAt !== undefined && Date.parse(String(value.created_at)) > attemptCompletedAt) ||
    !hasExactKeys(value.viewport, ["dpr", "height", "width"]) ||
    !safePositiveInteger(value.viewport.width) || !safePositiveInteger(value.viewport.height) ||
    typeof value.viewport.dpr !== "number" || !Number.isFinite(value.viewport.dpr) ||
    value.viewport.width !== attempt.manifest.environment.viewport.width ||
    value.viewport.height !== attempt.manifest.environment.viewport.height ||
    value.viewport.dpr !== attempt.manifest.environment.viewport.dpr ||
    (value.kind === "screenshot" && (value.mime !== "image/png" ||
      !safePositiveInteger(value.width) || !safePositiveInteger(value.height) ||
      value.width > 7_680 || value.height > 4_320 ||
      Math.abs(value.width - value.viewport.width * value.viewport.dpr) > 1 ||
      Math.abs(value.height - value.viewport.height * value.viewport.dpr) > 1)) ||
    (value.kind !== "screenshot" &&
      (Object.prototype.hasOwnProperty.call(value, "width") ||
        Object.prototype.hasOwnProperty.call(value, "height")))) {
    throw new APIRequestError("UI evidence artifact metadata is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as UIEvidenceArtifactMetadata;
}

function parseUIEvidenceBundle(value: unknown, attemptID: string): UIEvidenceBundle {
  if (!hasExactKeys(value, ["artifacts", "attempt", "steps"]) ||
    !Array.isArray(value.artifacts) || !Array.isArray(value.steps)) {
    throw new APIRequestError("UI evidence bundle is invalid", "INVALID_RESPONSE", 502);
  }
  const attempt = parseUIEvidenceAttempt(value.attempt);
  if (attempt.manifest.attempt_id !== attemptID || value.artifacts.length > 10_000 ||
    value.steps.length > 128) {
    throw new APIRequestError("UI evidence bundle identity is invalid", "INVALID_RESPONSE", 502);
  }
  const artifacts = value.artifacts.map((entry) => parseUIEvidenceArtifactMetadata(entry, attempt));
  const artifactIDs = new Set(artifacts.map((entry) => entry.id));
  const artifactFingerprints = new Set(artifacts.map((entry) => entry.fingerprint));
  const artifactBytes = artifacts.reduce((sum, entry) => sum + entry.bytes, 0);
  const manifestSteps = attempt.manifest.steps;
  const manifestStepIDs = new Set(manifestSteps.map((entry) => entry.id));
  const terminal = !["not_run", "running"].includes(attempt.status);
  if (artifactIDs.size !== artifacts.length || artifactFingerprints.size !== artifacts.length ||
    artifacts.some((entry) => !manifestStepIDs.has(entry.step_id)) ||
    (terminal && (artifacts.length !== attempt.artifact_count ||
      artifactBytes !== attempt.artifact_bytes)) ||
    (attempt.status === "passed" &&
      ["screenshot", "dom", "accessibility", "console", "network", "performance"]
        .some((kind) => !artifacts.some((entry) => entry.kind === kind)))) {
    throw new APIRequestError("UI evidence bundle artifact totals are inconsistent",
      "INVALID_RESPONSE", 502);
  }
  const steps = value.steps.map((entry) => {
    const required = ["attempt_id", "completed_at", "failure_stage", "fingerprint", "kind",
      "protocol_version", "sequence", "started_at", "status", "step_id"];
    const optional = ["message"];
    if (!isRecord(entry) || !hasOnlyKeys(entry, [...required, ...optional]) ||
      required.some((key) => !Object.prototype.hasOwnProperty.call(entry, key)) ||
      entry.protocol_version !== "ui-evidence-step.v1" ||
      entry.attempt_id !== attemptID || !boundedIdentity(entry.step_id) ||
      !safePositiveInteger(entry.sequence) || entry.sequence > 128 ||
      !["navigate", "click", "type", "assert_present", "assert_absent", "capture"]
        .includes(String(entry.kind)) ||
      !["passed", "failed", "cancelled", "timed_out"].includes(String(entry.status)) ||
      !uiEvidenceFailureStages.includes(String(entry.failure_stage) as typeof uiEvidenceFailureStages[number]) ||
      !isSHA256(entry.fingerprint) || !validDate(entry.started_at) || !validDate(entry.completed_at) ||
      !attempt.started_at || Date.parse(String(entry.started_at)) < Date.parse(attempt.started_at) ||
      (attempt.completed_at !== undefined &&
        Date.parse(String(entry.completed_at)) > Date.parse(attempt.completed_at)) ||
      Date.parse(String(entry.completed_at)) < Date.parse(String(entry.started_at)) ||
      (Object.prototype.hasOwnProperty.call(entry, "message") &&
        (typeof entry.message !== "string" || entry.message.length > 2_048)) ||
      ((entry.status === "passed") !== (entry.failure_stage === "none"))) {
      throw new APIRequestError("UI evidence step receipt is invalid", "INVALID_RESPONSE", 502);
    }
    const expected = manifestSteps[Number(entry.sequence) - 1];
    if (!expected || expected.id !== entry.step_id || expected.kind !== entry.kind) {
      throw new APIRequestError("UI evidence step receipt does not match its manifest",
        "INVALID_RESPONSE", 502);
    }
    return entry;
  });
  if (new Set(steps.map((entry) => entry.sequence)).size !== steps.length ||
    new Set(steps.map((entry) => entry.step_id)).size !== steps.length ||
    new Set(steps.map((entry) => entry.fingerprint)).size !== steps.length ||
    (attempt.status === "passed" &&
      (steps.length !== manifestSteps.length || steps.some((entry) => entry.status !== "passed")))) {
    throw new APIRequestError("UI evidence step receipts are inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { attempt, artifacts, steps } as unknown as UIEvidenceBundle;
}

function parseRuntimeCapabilities(value: unknown): RuntimeCapabilitiesView {
  const capabilityKeys = ["agent_code_tools_enabled", "approval_control_enabled",
    "command_runtime_enabled", "command_runtime_protocol_available",
    "command_runtime_adapter_installed", "command_runtime_adapter_ready",
    "command_runtime_adapters", "docker_execution_enabled",
    "code_intel_enabled",
    "browser_cdp_permission_control_enabled", "full_cdp_debug_enabled",
    "full_cdp_session_control_enabled",
    "controlled_command_proposal_control_enabled",
    "host_command_proposal_control_enabled",
    "execution_permission_control_enabled", "workspace_sandbox_enabled",
    "operator_approval_enabled",
    "danger_full_access_enabled", "debug_maximum_access_enabled",
    "evidence_attachment_enabled", "verification_evidence_enabled",
    "embedded_analyzer_execution_enabled", "workspace_checkpoint_control_enabled",
    "git_advanced_control_enabled", "github_review_control_enabled",
    "batch_delivery_control_enabled", "batch_delivery_host_validation_enabled",
    "ui_evidence_control_enabled",
    "file_edit_apply_enabled", "file_edit_proposal_enabled",
    "file_edit_review_enabled", "model_control_enabled", "plan_delivery_control_enabled",
    "process_execution_enabled", "provider_credential_enabled", "protocol_version",
    "run_control_enabled", "run_creation_enabled", "standard_code_preset_enabled",
    "run_execution_enabled",
    "run_lifecycle_enabled", "run_wake_control_enabled", "run_wake_execution_enabled",
    "run_wake_worker_enabled", "scheduled_job_control_enabled",
    "scheduled_job_worker_enabled", "scheduled_job_worker", "session_message_enabled",
    "session_steering_control_enabled", "shell_execution_enabled",
    "skill_installation_enabled", "thread_control_enabled", "wake_worker"];
  if (!hasExactKeys(value, capabilityKeys) || value.protocol_version !== "runtime_capabilities.v1") {
    throw new APIRequestError("Runtime capability response is invalid", "INVALID_RESPONSE", 502);
  }
  for (const key of capabilityKeys) {
    if (key !== "protocol_version" && key !== "wake_worker" && key !== "scheduled_job_worker" &&
      key !== "command_runtime_adapters" &&
      typeof value[key] !== "boolean") {
      throw new APIRequestError("Runtime capability flag is invalid", "INVALID_RESPONSE", 502);
    }
  }
  if (value.command_runtime_protocol_available !== true ||
    !Array.isArray(value.command_runtime_adapters) ||
    value.command_runtime_adapters.length > 8) {
    throw new APIRequestError("Command Runtime adapter capabilities are invalid",
      "INVALID_RESPONSE", 502);
  }
  const commandRuntimeAdapters = value.command_runtime_adapters.map((adapter) => {
    if (!hasExactKeys(adapter, ["backend", "backend_identity", "credential_policy",
      "isolation_grade", "kind", "network_policy", "ready"]) ||
      !boundedIdentity(adapter.backend) || !boundedIdentity(adapter.backend_identity) ||
      typeof adapter.ready !== "boolean") {
      throw new APIRequestError("Command Runtime adapter receipt is invalid",
        "INVALID_RESPONSE", 502);
    }
    const sandboxed = adapter.kind === "sandboxed_workspace" &&
      adapter.isolation_grade === "workspace_sandbox" &&
      adapter.network_policy === "denied" && adapter.credential_policy === "none";
    const host = adapter.kind === "host_unsandboxed" &&
      adapter.isolation_grade === "host_unsandboxed" &&
      adapter.network_policy === "host_available" &&
      adapter.credential_policy === "host_available";
    if (!sandboxed && !host) {
      throw new APIRequestError("Command Runtime adapter receipt is inconsistent",
        "INVALID_RESPONSE", 502);
    }
    return adapter;
  });
  if (new Set(commandRuntimeAdapters.map((adapter) => adapter.backend)).size !==
      commandRuntimeAdapters.length ||
    value.command_runtime_adapter_installed !== (commandRuntimeAdapters.length > 0) ||
    value.command_runtime_adapter_ready !==
      commandRuntimeAdapters.some((adapter) => adapter.ready) ||
    value.command_runtime_enabled !==
      (value.run_execution_enabled && value.command_runtime_adapter_ready)) {
    throw new APIRequestError("Command Runtime adapter capability summary is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  const worker = value.wake_worker;
  const scheduledWorker = value.scheduled_job_worker;
  if (!hasExactKeys(worker, ["active", "concurrency", "enabled", "max_steps",
    "persistent_service", "poll_interval_ms", "protocol_version",
    "runtime_enable_supported", "state"]) ||
    worker.protocol_version !== "run_wake_worker_health.v1" ||
    typeof worker.enabled !== "boolean" || typeof worker.active !== "boolean" ||
    worker.concurrency !== 1 || worker.max_steps !== 1 ||
    worker.runtime_enable_supported !== false || worker.persistent_service !== false ||
    value.run_wake_worker_enabled !== worker.enabled ||
    (worker.enabled && (!["ready", "running", "draining", "stopped"].includes(String(worker.state)) ||
      !safeBoundedCount(worker.poll_interval_ms, 60_000) || worker.poll_interval_ms < 250)) ||
    ((worker.state === "ready" || worker.state === "stopped") && worker.active) ||
    (!worker.enabled && (worker.state !== "disabled" || worker.active || worker.poll_interval_ms !== 0)) ||
    !hasExactKeys(scheduledWorker, ["active", "authority_escalation", "concurrency",
      "enabled", "persistent_service", "poll_interval_ms", "protocol_version",
      "runtime_enable_supported", "state"]) ||
    scheduledWorker.protocol_version !== "scheduled-job-worker-health.v1" ||
    typeof scheduledWorker.enabled !== "boolean" || typeof scheduledWorker.active !== "boolean" ||
    scheduledWorker.concurrency !== 1 || scheduledWorker.runtime_enable_supported !== false ||
    scheduledWorker.persistent_service !== false || scheduledWorker.authority_escalation !== false ||
    value.scheduled_job_worker_enabled !== scheduledWorker.enabled ||
    (scheduledWorker.enabled &&
      (!['ready', 'running', 'draining', 'stopped'].includes(String(scheduledWorker.state)) ||
        !safeBoundedCount(scheduledWorker.poll_interval_ms, 60_000) ||
        scheduledWorker.poll_interval_ms < 250)) ||
    ((scheduledWorker.state === "ready" || scheduledWorker.state === "stopped") &&
      scheduledWorker.active) ||
    (!scheduledWorker.enabled && (scheduledWorker.state !== "disabled" ||
      scheduledWorker.active || scheduledWorker.poll_interval_ms !== 0)) ||
    (value.full_cdp_debug_enabled && (!value.browser_cdp_permission_control_enabled ||
      !value.danger_full_access_enabled)) ||
    (value.full_cdp_session_control_enabled && (!value.full_cdp_debug_enabled ||
      !value.browser_cdp_permission_control_enabled || !value.danger_full_access_enabled)) ||
    (value.workspace_sandbox_enabled && !value.execution_permission_control_enabled) ||
    value.thread_control_enabled !==
      (value.run_creation_enabled && value.session_message_enabled) ||
    (value.host_command_proposal_control_enabled && !value.operator_approval_enabled) ||
    (value.batch_delivery_host_validation_enabled &&
      (!value.batch_delivery_control_enabled || !value.execution_permission_control_enabled ||
        !value.operator_approval_enabled || !value.danger_full_access_enabled)) ||
    (value.ui_evidence_control_enabled && (!value.command_runtime_enabled ||
      !value.browser_cdp_permission_control_enabled)) ||
    value.process_execution_enabled !== value.command_runtime_enabled ||
    value.shell_execution_enabled !== value.command_runtime_enabled ||
    (value.docker_execution_enabled && !value.execution_permission_control_enabled) ||
    (value.git_advanced_control_enabled &&
      (!value.execution_permission_control_enabled || !value.operator_approval_enabled ||
        !value.workspace_checkpoint_control_enabled)) ||
    (value.github_review_control_enabled &&
      (!value.execution_permission_control_enabled || !value.operator_approval_enabled))) {
    throw new APIRequestError("Run wake worker capability response is invalid",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as RuntimeCapabilitiesView;
}

function parseStrictRunMode(value: unknown): Record<string, unknown> {
  const keys = ["capability_grant", "created_at", "phase", "policy_version", "profile",
    "protocol_version", "reason", "requested_by", "revision", "scope", "surface"];
  if (!hasExactKeys(value, keys) || value.capability_grant !== false ||
    value.protocol_version !== "run_mode.v1" || value.policy_version !== "mode_policy.v1" ||
    !safePositiveInteger(value.revision) || typeof value.surface !== "string" ||
    !["code", "cyber"].includes(value.surface) || typeof value.phase !== "string" ||
    !["plan", "deliver"].includes(value.phase) || typeof value.profile !== "string" ||
    !["code", "review", "learn", "script"].includes(value.profile) ||
    !boundedText(value.requested_by, 256) || !boundedText(value.reason, 1_024) ||
    !validDate(value.created_at) || !isRecord(value.scope) ||
    !hasOnlyKeys(value.scope, ["allowed_targets", "network_mode", "workspace_id"]) ||
    !Object.prototype.hasOwnProperty.call(value.scope, "network_mode") ||
    (value.scope.workspace_id !== undefined && !boundedIdentity(value.scope.workspace_id))) {
    throw new APIRequestError("Run mode response is invalid", "INVALID_RESPONSE", 502);
  }
  return value;
}

function parseRunNetworkAuthorityControl(value: unknown, expectedRunID: string,
  request: RunNetworkAuthorityControlRequestView): RunNetworkAuthorityControlView {
  const mode = isRecord(value) && Object.prototype.hasOwnProperty.call(value, "mode")
    ? parseStrictRunMode(value.mode) : null;
  if (!hasExactKeys(value, ["added_targets", "capability_grant", "mode", "replayed",
    "run_id", "version"]) ||
    value.version !== "run_network_authority_control.v1" ||
    value.run_id !== expectedRunID || typeof value.replayed !== "boolean" ||
    value.capability_grant !== true || mode === null ||
    mode.revision !== request.expected_mode_revision + 1 ||
    !isRecord(mode.scope) || mode.scope.network_mode !== "allowlist" ||
    !Array.isArray(value.added_targets) ||
    value.added_targets.some((target) => typeof target !== "string") ||
    !Array.isArray(mode.scope.allowed_targets) ||
    mode.scope.allowed_targets.some((target) => typeof target !== "string")) {
    throw new APIRequestError("Run network authority response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const expected = normalizeRequestedNetworkAuthority({ network_mode: "allowlist",
    allowed_targets: request.add_allowed_targets });
  const rawAdded = value.added_targets as string[];
  const rawAllowed = mode.scope.allowed_targets as string[];
  const added = rawAdded.map((target) => canonicalExactNetworkTarget(target));
  const allowed = rawAllowed.map((target) => canonicalExactNetworkTarget(target));
  if (added.length !== expected.targets.length || added.length > 256 ||
    rawAdded.some((target, index) => target !== added[index]) ||
    rawAllowed.some((target, index) => target !== allowed[index]) ||
    added.some((target, index) => target !== expected.targets[index]) ||
    allowed.length < added.length || allowed.length > 256 ||
    allowed.some((target, index) => index > 0 && allowed[index - 1]! >= target) ||
    added.some((target) => !allowed.includes(target))) {
    throw new APIRequestError("Run network authority response changed its exact target grant",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as RunNetworkAuthorityControlView;
}

function parseFullCDPSession(value: unknown, expectedRunID: string): FullCDPSessionView {
  const required = ["cdp_closed", "process_tree_quiescent", "profile_cleaned",
    "profile_released", "run_id", "runtime_available", "state", "version"];
  const optional = ["browser", "close_reason", "completed_at", "expires_at", "failure_code",
    "session_id", "started_at", "target_origin"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.version !== "full_cdp_session.v1" || value.run_id !== expectedRunID ||
    !boundedIdentity(value.run_id) ||
    !["none", "starting", "ready", "closing", "closed", "failed"]
      .includes(String(value.state)) ||
    typeof value.runtime_available !== "boolean" || typeof value.cdp_closed !== "boolean" ||
    typeof value.process_tree_quiescent !== "boolean" ||
    typeof value.profile_released !== "boolean" || typeof value.profile_cleaned !== "boolean" ||
    (value.session_id !== undefined && !boundedIdentity(value.session_id)) ||
    (value.target_origin !== undefined && !boundedText(value.target_origin, 2_048)) ||
    (value.started_at !== undefined && !validDate(value.started_at)) ||
    (value.expires_at !== undefined && !validDate(value.expires_at)) ||
    (value.completed_at !== undefined && !validDate(value.completed_at)) ||
    (value.failure_code !== undefined && !/^[a-z_]{1,96}$/u.test(String(value.failure_code))) ||
    (value.close_reason !== undefined && !["operator_closed", "expired", "permission_revoked",
      "process_exited", "run_terminal", "desktop_shutdown", "open_failed"]
      .includes(String(value.close_reason)))) {
    throw new APIRequestError("Full CDP session response is invalid", "INVALID_RESPONSE", 502);
  }
  const noSession = value.state === "none";
  if (noSession !== (value.session_id === undefined) ||
    noSession !== (value.browser === undefined) ||
    (value.profile_cleaned && !value.profile_released) ||
    ((value.state === "ready" || value.state === "closed") &&
      (!validDate(value.started_at) || !validDate(value.expires_at) ||
        !boundedText(value.target_origin, 2_048))) ||
    (value.state === "closing" &&
      [value.started_at, value.expires_at, value.target_origin].filter(
        (item) => item !== undefined).length !== 0 &&
      (!validDate(value.started_at) || !validDate(value.expires_at) ||
        !boundedText(value.target_origin, 2_048))) ||
    ((value.state === "closed" || value.state === "failed") !==
      (value.completed_at !== undefined)) ||
    (value.state === "closed" && (!value.cdp_closed || !value.process_tree_quiescent ||
      !value.profile_released || !value.profile_cleaned || value.failure_code !== undefined ||
      value.close_reason === undefined))) {
    throw new APIRequestError("Full CDP session lifecycle response is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  if (value.browser !== undefined && (!hasExactKeys(value.browser, ["channel", "product"]) ||
    !["chrome", "edge"].includes(String(value.browser.product)) ||
    !["stable", "beta", "dev", "canary"].includes(String(value.browser.channel)))) {
    throw new APIRequestError("Full CDP browser selection response is invalid",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as FullCDPSessionView;
}

function parseFullCDPSessionControl(value: unknown, expectedRunID: string):
  FullCDPSessionControlView {
  if (!hasExactKeys(value, ["replayed", "session"]) || typeof value.replayed !== "boolean") {
    throw new APIRequestError("Full CDP session control response is invalid",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, session: parseFullCDPSession(value.session, expectedRunID) } as
    unknown as FullCDPSessionControlView;
}

const capabilityReadinessBlockers = [
  "run_not_quiescent", "execution_lease_active", "startup_gate_closed",
  "capability_not_implemented", "surface_mismatch", "profile_mismatch",
  "permission_mismatch", "workspace_untrusted", "sandbox_unproven",
  "docker_unavailable", "backend_not_ready",
] as const;

const capabilityReadinessRemediations = [
  "pause_run", "create_new_run", "wait_for_execution_lease",
  "restart_with_startup_gate", "upgrade_application", "select_required_surface",
  "select_required_profile", "select_required_permission", "trust_workspace",
  "verify_sandbox", "install_or_start_docker", "retry_backend_readiness",
] as const;

type ReadinessBlocker = typeof capabilityReadinessBlockers[number];
type ReadinessRemediation = typeof capabilityReadinessRemediations[number];

const readinessRemediationForBlocker: Record<ReadinessBlocker,
  ReadinessRemediation | readonly ReadinessRemediation[]> = {
  run_not_quiescent: ["pause_run", "create_new_run"],
  execution_lease_active: "wait_for_execution_lease",
  startup_gate_closed: "restart_with_startup_gate",
  capability_not_implemented: "upgrade_application",
  surface_mismatch: "select_required_surface",
  profile_mismatch: "select_required_profile",
  permission_mismatch: "select_required_permission",
  workspace_untrusted: "trust_workspace",
  sandbox_unproven: "verify_sandbox",
  docker_unavailable: "install_or_start_docker",
  backend_not_ready: "retry_backend_readiness",
};

function parseCapabilityReadinessOption(value: unknown,
  expectedValue: string): Record<string, unknown> {
  if (!hasExactKeys(value, ["blocked_by", "remediation", "restart_required",
    "runtime_available", "selectable", "selected", "value"]) ||
    value.value !== expectedValue || typeof value.selected !== "boolean" ||
    typeof value.selectable !== "boolean" || typeof value.runtime_available !== "boolean" ||
    typeof value.restart_required !== "boolean" || !Array.isArray(value.blocked_by) ||
    !Array.isArray(value.remediation)) {
    throw new APIRequestError("Capability readiness option is invalid", "INVALID_RESPONSE", 502);
  }
  const blockers = value.blocked_by;
  const remediation = value.remediation;
  if (blockers.length > capabilityReadinessBlockers.length ||
    remediation.length > capabilityReadinessRemediations.length ||
    blockers.some((entry) => typeof entry !== "string" ||
      !capabilityReadinessBlockers.includes(entry as ReadinessBlocker)) ||
    remediation.some((entry) => typeof entry !== "string" ||
      !capabilityReadinessRemediations.includes(entry as ReadinessRemediation)) ||
    new Set(blockers).size !== blockers.length || new Set(remediation).size !== remediation.length ||
    blockers.some((entry, index) => index > 0 &&
      capabilityReadinessBlockers.indexOf(entry as ReadinessBlocker) <=
        capabilityReadinessBlockers.indexOf(blockers[index - 1] as ReadinessBlocker)) ||
    remediation.some((entry, index) => index > 0 &&
      capabilityReadinessRemediations.indexOf(entry as ReadinessRemediation) <=
        capabilityReadinessRemediations.indexOf(remediation[index - 1] as ReadinessRemediation)) ||
    (blockers.length === 0) !== (remediation.length === 0) ||
    value.restart_required !== blockers.includes("startup_gate_closed")) {
    throw new APIRequestError("Capability readiness disposition is invalid",
      "INVALID_RESPONSE", 502);
  }
  for (const blocker of blockers as ReadinessBlocker[]) {
    const expected = readinessRemediationForBlocker[blocker];
    const accepted = Array.isArray(expected) ? expected : [expected];
    if (!accepted.some((entry) => remediation.includes(entry))) {
      throw new APIRequestError("Capability readiness remediation is incomplete",
        "INVALID_RESPONSE", 502);
    }
  }
  for (const action of remediation as ReadinessRemediation[]) {
    const matched = (blockers as ReadinessBlocker[]).some((blocker) => {
      const expected = readinessRemediationForBlocker[blocker];
      const accepted = Array.isArray(expected) ? expected : [expected];
      return accepted.includes(action);
    });
    if (!matched) {
      throw new APIRequestError("Capability readiness remediation is unrelated",
        "INVALID_RESPONSE", 502);
    }
  }
  const runtimeFailureBlockers: ReadinessBlocker[] = [
    "capability_not_implemented", "surface_mismatch", "profile_mismatch",
    "permission_mismatch", "workspace_untrusted", "sandbox_unproven",
    "docker_unavailable", "backend_not_ready",
  ];
  if (value.runtime_available &&
    blockers.some((entry) => runtimeFailureBlockers.includes(entry as ReadinessBlocker))) {
    throw new APIRequestError("Capability readiness claims a blocked runtime",
      "INVALID_RESPONSE", 502);
  }
  return value;
}

function parseRunCapabilityReadiness(value: unknown,
  expectedRunID: string): RunCapabilityReadinessView {
  if (!hasExactKeys(value, ["browser_cdp_permissions", "capability_grant",
    "command_runtime", "interactions", "permissions", "presets", "profiles",
    "protocol_version", "run_id"]) ||
    value.protocol_version !== "run_capability_readiness.v1" ||
    value.run_id !== expectedRunID || !boundedIdentity(value.run_id) ||
    value.capability_grant !== false) {
    throw new APIRequestError("Run capability readiness response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const commandRuntime = value.command_runtime;
  const commandRuntimeRequired = ["adapter_installed", "adapter_ready",
    "current_run_granted", "protocol_available"];
  const commandRuntimeAllowed = [...commandRuntimeRequired, "adapter_kind", "backend"];
  if (!isRecord(commandRuntime) || !hasOnlyKeys(commandRuntime, commandRuntimeAllowed) ||
    commandRuntimeRequired.some((key) =>
      !Object.prototype.hasOwnProperty.call(commandRuntime, key)) ||
    commandRuntime.protocol_available !== true ||
    typeof commandRuntime.adapter_installed !== "boolean" ||
    typeof commandRuntime.adapter_ready !== "boolean" ||
    typeof commandRuntime.current_run_granted !== "boolean" ||
    (commandRuntime.adapter_ready && !commandRuntime.adapter_installed) ||
    (commandRuntime.current_run_granted && (!commandRuntime.adapter_ready ||
      !["sandboxed_workspace", "host_unsandboxed"].includes(
        String(commandRuntime.adapter_kind)) || !boundedIdentity(commandRuntime.backend))) ||
    (!commandRuntime.current_run_granted &&
      (Object.prototype.hasOwnProperty.call(commandRuntime, "adapter_kind") ||
        Object.prototype.hasOwnProperty.call(commandRuntime, "backend")))) {
    throw new APIRequestError("Command Runtime readiness response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const groups: Array<[unknown, readonly string[], boolean]> = [
    [value.permissions, ["conservative", "workspace_access", "approval", "full_access", "debug"], true],
    [value.profiles, ["preview", "docker", "local"], true],
    [value.interactions, ["preview", "controlled", "debug", "cyber"], true],
    [value.browser_cdp_permissions, ["restricted", "full_debug"], true],
    [value.presets, ["standard_code"], false],
  ];
  for (const [rawOptions, expected, requiresSelection] of groups) {
    if (!Array.isArray(rawOptions) || rawOptions.length !== expected.length) {
      throw new APIRequestError("Run capability readiness option group is incomplete",
        "INVALID_RESPONSE", 502);
    }
    const parsed = rawOptions.map((option, index) =>
      parseCapabilityReadinessOption(option, expected[index]!));
    const selected = parsed.filter((option) => option.selected === true).length;
    if (selected > 1 || (requiresSelection && selected !== 1)) {
      throw new APIRequestError("Run capability readiness selection is invalid",
        "INVALID_RESPONSE", 502);
    }
  }
  return value as unknown as RunCapabilityReadinessView;
}

const standardCodeNextSteps = [
  "confirm_workspace_trust", "pause_and_configure", "wait_for_quiescence",
  "select_docker", "select_approval", "retry_readiness", "create_new_run",
] as const;

function parseStandardCodeBackendReadiness(value: unknown,
  backend: "local" | "docker"): void {
  if (!hasExactKeys(value, ["available", "backend", "blocked_by", "remediation"]) ||
    value.backend !== backend || typeof value.available !== "boolean" ||
    !Array.isArray(value.blocked_by) || !Array.isArray(value.remediation) ||
    value.blocked_by.length > capabilityReadinessBlockers.length ||
    value.remediation.length > capabilityReadinessRemediations.length ||
    value.blocked_by.some((entry) => typeof entry !== "string" ||
      !capabilityReadinessBlockers.includes(entry as ReadinessBlocker)) ||
    value.remediation.some((entry) => typeof entry !== "string" ||
      !capabilityReadinessRemediations.includes(entry as ReadinessRemediation)) ||
    new Set(value.blocked_by).size !== value.blocked_by.length ||
    new Set(value.remediation).size !== value.remediation.length ||
    value.available !== (value.blocked_by.length === 0)) {
    throw new APIRequestError("Standard Code backend readiness is invalid",
      "INVALID_RESPONSE", 502);
  }
}

function parseStandardCodePreset(value: unknown,
  expectedAction: "configure" | "pause_and_configure"): StandardCodePresetControlView {
  const required = ["action", "backend_intent", "blocked_by", "capability_grant",
    "credentials", "docker_readiness", "drydock_ready", "local_readiness", "network",
    "next_steps", "protocol_version", "replayed", "status", "trust_required",
    "workspace_id"];
  const optional = ["browser_cdp_permission", "execution_interaction",
    "execution_permission", "execution_profile", "mode", "run", "run_id",
    "selected_backend", "selection_reason", "trust_digest"];
  if (!isRecord(value) || !hasOnlyKeys(value, [...required, ...optional]) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    value.protocol_version !== "standard_code_preset.v1" ||
    value.action !== expectedAction ||
    !["auto", "local", "docker"].includes(String(value.backend_intent)) ||
    !["blocked", "waiting_for_pause", "configured"].includes(String(value.status)) ||
    !boundedIdentity(value.workspace_id) || value.network !== "disabled" ||
    value.credentials !== "none" || value.capability_grant !== false ||
    typeof value.drydock_ready !== "boolean" || typeof value.replayed !== "boolean" ||
    typeof value.trust_required !== "boolean" || !Array.isArray(value.blocked_by) ||
    !Array.isArray(value.next_steps) ||
    value.blocked_by.length > capabilityReadinessBlockers.length ||
    value.next_steps.length > standardCodeNextSteps.length ||
    value.blocked_by.some((entry) => typeof entry !== "string" ||
      !capabilityReadinessBlockers.includes(entry as ReadinessBlocker)) ||
    value.next_steps.some((entry) => typeof entry !== "string" ||
      !standardCodeNextSteps.includes(entry as typeof standardCodeNextSteps[number])) ||
    new Set(value.blocked_by).size !== value.blocked_by.length ||
    new Set(value.next_steps).size !== value.next_steps.length) {
    throw new APIRequestError("Standard Code preset response is invalid",
      "INVALID_RESPONSE", 502);
  }
  parseStandardCodeBackendReadiness(value.local_readiness, "local");
  parseStandardCodeBackendReadiness(value.docker_readiness, "docker");

  const selected = value.selected_backend;
  const reason = value.selection_reason;
  const selectionValid = (selected === undefined && reason === undefined) ||
    (selected === "local" &&
      ((value.backend_intent === "auto" && reason === "auto_local_ready") ||
        (value.backend_intent === "local" && reason === "explicit_local"))) ||
    (selected === "docker" && value.backend_intent === "docker" &&
      reason === "explicit_docker");
  const trustDigestPresent = Object.prototype.hasOwnProperty.call(value, "trust_digest");
  if (!selectionValid ||
    (Object.prototype.hasOwnProperty.call(value, "run_id") && !boundedIdentity(value.run_id)) ||
    trustDigestPresent !== value.trust_required ||
    (trustDigestPresent && !isSHA256(value.trust_digest)) ||
    (value.trust_required && (value.status !== "blocked" ||
      !value.next_steps.includes("confirm_workspace_trust"))) ||
    (value.status === "waiting_for_pause" &&
      (!boundedIdentity(value.run_id) || selected === undefined)) ||
    (value.status === "configured" &&
      (!boundedIdentity(value.run_id) || selected === undefined || !value.drydock_ready ||
        value.blocked_by.length !== 0 || value.next_steps.length !== 0 ||
        !isRecord(value.run) || !isRecord(value.mode) || !isRecord(value.execution_profile) ||
        !isRecord(value.execution_interaction) || !isRecord(value.execution_permission) ||
        !isRecord(value.browser_cdp_permission)))) {
    throw new APIRequestError("Standard Code preset response is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as StandardCodePresetControlView;
}

const safeWebReadinessBlockingReasons = [
  "evidence_missing", "evidence_version_mismatch", "executable_identity_mismatch",
  "acceptance_mismatch", "policy_version_mismatch", "adapter_mismatch",
  "platform_mismatch", "evidence_not_passed", "review_missing",
  "review_binding_mismatch", "review_not_accepted", "evidence_expired",
];

function parseSafeWebReadiness(value: unknown): BrowserSafeWebReadiness {
  if (!isRecord(value)) {
    throw new APIRequestError("Safe Web readiness response is invalid", "INVALID_RESPONSE", 502);
  }
  if (value.protocol_version !== "browser_safe_web_readiness.v1" ||
    typeof value.ready !== "boolean" ||
    !isSHA256(value.executable_identity_fingerprint) ||
    !isSHA256(value.acceptance_fingerprint) || !isSHA256(value.fingerprint)) {
    throw new APIRequestError("Safe Web readiness response is invalid", "INVALID_RESPONSE", 502);
  }
  const blockingReason = typeof value.blocking_reason === "string" &&
    value.blocking_reason !== "" ? value.blocking_reason : null;
  if (value.ready === (blockingReason !== null)) {
    throw new APIRequestError("Safe Web readiness blocking reason disagrees with ready",
      "INVALID_RESPONSE", 502);
  }
  if (blockingReason !== null && !safeWebReadinessBlockingReasons.includes(blockingReason)) {
    throw new APIRequestError("Safe Web readiness blocking reason is unrecognized",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as BrowserSafeWebReadiness;
}

function parseEmbeddedAnalyzerExecution(value: unknown,
  runID: string): EmbeddedAnalyzerExecutionControlView {
  if (!hasExactKeys(value, [
    "analyzer", "artifact_atomic", "artifact_id", "bearer_token_included",
    "capability_consumed", "execution_id", "filesystem_mounted",
    "host_process_authorized", "input_bytes", "line_count", "media_type",
    "metadata_only", "network_enabled", "raw_request_included", "replayed",
    "run_id", "session_id", "sha256", "status", "subprocess_enabled", "utf8",
    "version", "workspace_id",
  ]) || value.version !== "embedded_analyzer_execution_control.v1" ||
    value.run_id !== runID || boundedIdentity(value.execution_id) !== value.execution_id ||
    boundedIdentity(value.artifact_id) !== value.artifact_id ||
    boundedIdentity(value.session_id) !== value.session_id ||
    boundedIdentity(value.workspace_id) !== value.workspace_id ||
    value.analyzer !== "fixture.digest.v1" || value.status !== "succeeded" ||
    value.media_type !== "text/plain" ||
    !safeBoundedCount(value.input_bytes, 65_536) || value.input_bytes < 1 ||
    !safeBoundedCount(value.line_count, 65_536) || !isSHA256(value.sha256) ||
    value.utf8 !== true || value.metadata_only !== true ||
    value.capability_consumed !== true || value.artifact_atomic !== true ||
    value.filesystem_mounted !== false || value.network_enabled !== false ||
    value.subprocess_enabled !== false || value.host_process_authorized !== false ||
    value.raw_request_included !== false || value.bearer_token_included !== false ||
    typeof value.replayed !== "boolean") {
    throw new APIRequestError("Embedded analyzer response violated its fixed boundary",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as EmbeddedAnalyzerExecutionControlView;
}

export function clientCapabilitiesFromRuntime(value: RuntimeCapabilitiesView): ClientCapabilities {
  return {
    runControlEnabled: value.run_control_enabled,
    executionPermissionControlEnabled: value.execution_permission_control_enabled,
    workspaceSandboxEnabled: value.workspace_sandbox_enabled,
    browserCDPPermissionControlEnabled: value.browser_cdp_permission_control_enabled,
    fullCDPDebugEnabled: value.full_cdp_debug_enabled,
    fullCDPSessionControlEnabled: value.full_cdp_session_control_enabled,
    operatorApprovalEnabled: value.operator_approval_enabled,
    dangerFullAccessEnabled: value.danger_full_access_enabled,
    debugMaximumAccessEnabled: value.debug_maximum_access_enabled,
    commandRuntimeEnabled: value.command_runtime_enabled,
    commandRuntimeProtocolAvailable: value.command_runtime_protocol_available,
    commandRuntimeAdapterInstalled: value.command_runtime_adapter_installed,
    commandRuntimeAdapterReady: value.command_runtime_adapter_ready,
    runCreationEnabled: value.run_creation_enabled,
    standardCodePresetEnabled: value.standard_code_preset_enabled,
    sessionMessageEnabled: value.session_message_enabled,
    threadControlEnabled: value.thread_control_enabled,
    sessionSteeringControlEnabled: value.session_steering_control_enabled,
    runLifecycleEnabled: value.run_lifecycle_enabled,
    runExecutionEnabled: value.run_execution_enabled,
    planDeliveryControlEnabled: value.plan_delivery_control_enabled,
    approvalControlEnabled: value.approval_control_enabled,
    controlledCommandProposalControlEnabled:
      value.controlled_command_proposal_control_enabled,
    hostCommandProposalControlEnabled:
      value.host_command_proposal_control_enabled,
    modelControlEnabled: value.model_control_enabled,
    providerCredentialEnabled: value.provider_credential_enabled,
    fileEditReviewEnabled: value.file_edit_review_enabled,
    fileEditProposalEnabled: value.file_edit_proposal_enabled,
    fileEditApplyEnabled: value.file_edit_apply_enabled,
    runWakeControlEnabled: value.run_wake_control_enabled,
    runWakeExecutionEnabled: value.run_wake_execution_enabled,
    runWakeWorkerEnabled: value.run_wake_worker_enabled,
    scheduledJobControlEnabled: value.scheduled_job_control_enabled,
    scheduledJobWorkerEnabled: value.scheduled_job_worker_enabled,
    skillInstallationEnabled: value.skill_installation_enabled,
    evidenceAttachmentEnabled: value.evidence_attachment_enabled,
    verificationEvidenceEnabled: value.verification_evidence_enabled,
    embeddedAnalyzerExecutionEnabled: value.embedded_analyzer_execution_enabled,
    workspaceCheckpointControlEnabled: value.workspace_checkpoint_control_enabled,
    gitAdvancedControlEnabled: value.git_advanced_control_enabled,
    githubReviewControlEnabled: value.github_review_control_enabled,
    batchDeliveryControlEnabled: value.batch_delivery_control_enabled,
    batchDeliveryHostValidationEnabled: value.batch_delivery_host_validation_enabled,
    uiEvidenceControlEnabled: value.ui_evidence_control_enabled,
    dockerExecutionEnabled: value.docker_execution_enabled,
    agentCodeToolsEnabled: value.agent_code_tools_enabled,
    codeIntelEnabled: value.code_intel_enabled,
  };
}

function parseFileEditProposalSource(value: unknown, runID: string,
  expectedPath: string): FileEditProposalSourceView {
  if (!hasExactKeys(value, ["content", "content_sha256", "editable", "expires_at",
    "file_write", "path", "protocol_version", "run_id", "source_handle", "workspace_id"]) ||
    value.protocol_version !== "file_edit_proposal.v1" || value.run_id !== runID ||
    value.path !== expectedPath || !boundedIdentity(value.run_id) ||
    !boundedIdentity(value.workspace_id) || !validWorkspaceRelativePath(value.path) ||
    typeof value.content !== "string" || new TextEncoder().encode(value.content).length > 131_072 ||
    !isSHA256(value.content_sha256) || typeof value.source_handle !== "string" ||
    !/^[A-Za-z0-9_-]{43}$/u.test(value.source_handle) || !validDate(value.expires_at) ||
    Date.parse(value.expires_at) <= Date.now() || value.editable !== true ||
    value.file_write !== false) {
    throw new APIRequestError("File edit source violated its exact no-write contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as FileEditProposalSourceView;
}

function parseFileEditProposal(value: unknown, runID: string): FileEditProposalView {
  if (!hasExactKeys(value, ["approval_required", "edit", "file_written",
    "protocol_version", "replayed", "run_id"]) ||
    value.protocol_version !== "file_edit_proposal.v1" || value.run_id !== runID ||
    value.approval_required !== true || value.file_written !== false ||
    typeof value.replayed !== "boolean") {
    throw new APIRequestError("File edit proposal widened write authority", "INVALID_RESPONSE", 502);
  }
  const edit = parseFileEditPreview(value.edit);
  if (edit.status !== "proposed" || edit.apply_enabled !== false ||
    edit.allowed_actions.length > 2) {
    throw new APIRequestError("File edit proposal result is not pending review",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, edit } as unknown as FileEditProposalView;
}

function parseFileEditProposalRecovery(value: unknown, runID: string,
  editID: string): FileEditProposalRecoveryView {
  if (!hasExactKeys(value, ["current_content_sha256", "edit_id", "editable", "file_write",
    "original_content", "original_sha256", "path", "proposed_content", "proposed_sha256",
    "protocol_version", "review_required", "run_id", "stale", "status", "workspace_id"]) ||
    value.protocol_version !== "file_edit_proposal_recovery.v1" || value.run_id !== runID ||
    value.edit_id !== editID || !boundedIdentity(value.workspace_id) ||
    !validWorkspaceRelativePath(value.path) || typeof value.original_content !== "string" ||
    typeof value.proposed_content !== "string" ||
    new TextEncoder().encode(value.original_content).length > 256 * 1024 ||
    new TextEncoder().encode(value.proposed_content).length > 256 * 1024 ||
    !(isSHA256(value.original_sha256) ||
      (value.original_sha256 === "missing" && value.original_content === "")) ||
    !isSHA256(value.proposed_sha256) ||
    !(isSHA256(value.current_content_sha256) || value.current_content_sha256 === "missing") ||
    value.status !== "proposed" || value.stale !==
      (value.current_content_sha256 !== value.original_sha256) ||
    value.review_required !== true || value.editable !== false || value.file_write !== false) {
    throw new APIRequestError("File edit proposal recovery widened authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as FileEditProposalRecoveryView;
}

function parseFileEditPreview(value: unknown): FileEditPreviewView {
  if (!isRecord(value) || !hasOnlyKeys(value, ["allowed_actions", "apply_enabled", "created_at",
    "destination_original_hash", "destination_path", "destination_proposed_hash", "diff", "id",
    "operation", "original_hash", "path", "proposed_hash", "reason", "secrets_redacted",
    "session_id", "status", "updated_at", "workspace_id"]) ||
    !["proposed", "approved", "applied", "denied", "failed"].includes(String(value.status)) ||
    !["replace", "create", "move", "delete"].includes(String(value.operation)) ||
    !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !validWorkspaceRelativePath(value.path) ||
    value.path === "." ||
    typeof value.diff !== "string" || value.diff.length > 1_100_000 ||
    !(isSHA256(value.original_hash) || value.original_hash === "missing") ||
    !(isSHA256(value.proposed_hash) || value.proposed_hash === "missing") ||
    typeof value.secrets_redacted !== "boolean" || typeof value.apply_enabled !== "boolean" ||
    !Array.isArray(value.allowed_actions) || value.allowed_actions.length > 2 ||
    !value.allowed_actions.every((action) => action === "approve_intent" || action === "deny") ||
    !validDate(value.created_at) || !validDate(value.updated_at) ||
    (value.reason !== undefined && typeof value.reason !== "string") ||
    (value.operation === "move" ?
      (!validWorkspaceRelativePath(value.destination_path) || value.destination_path === "." ||
        value.destination_path === value.path || value.destination_original_hash !== "missing" ||
        !isSHA256(value.destination_proposed_hash)) :
      (value.destination_path !== undefined || value.destination_original_hash !== undefined ||
        value.destination_proposed_hash !== undefined)) ||
    (value.apply_enabled === true &&
      (value.status !== "approved" || value.allowed_actions.length !== 0))) {
    throw new APIRequestError("File edit preview violated its metadata-only contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as FileEditPreviewView;
}

function parseFileEditQueue(value: unknown, runID: string): FileEditQueueView {
  if (!hasExactKeys(value, ["apply_enabled", "items", "protocol_version", "run_id", "truncated"]) ||
    value.protocol_version !== "file_edit_review.v1" || value.run_id !== runID ||
    typeof value.apply_enabled !== "boolean" || typeof value.truncated !== "boolean" ||
    !Array.isArray(value.items) || value.items.length > 100) {
    throw new APIRequestError("File edit queue response is invalid", "INVALID_RESPONSE", 502);
  }
  const items = value.items.map(parseFileEditPreview);
  if (!value.apply_enabled && items.some((item) => item.apply_enabled)) {
    throw new APIRequestError("File edit queue widened apply authority", "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as FileEditQueueView;
}

function parseFileEditChangeSet(value: unknown, runID: string): FileEditChangeSetView {
  const keys = ["applied_count", "apply_independent", "approved_count", "atomic_apply",
    "batch_mutation_supported", "denied_count", "diff_content_included", "failed_count",
    "items", "partial_apply_visible", "proposed_count", "protocol_version",
    "returned_count", "review_independent", "run_id", "session_id", "total_diff_bytes",
    "truncated", "workspace_id"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "file_edit_change_set.v1" ||
    value.run_id !== runID || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !Array.isArray(value.items) ||
    value.items.length > 100 || !safeBoundedCount(value.proposed_count, 100) ||
    !safeBoundedCount(value.approved_count, 100) ||
    !safeBoundedCount(value.applied_count, 100) ||
    !safeBoundedCount(value.denied_count, 100) ||
    !safeBoundedCount(value.failed_count, 100) ||
    !safeBoundedCount(value.returned_count, 100) ||
    !safeBoundedCount(value.total_diff_bytes, 100 * 1_064_960) ||
    typeof value.truncated !== "boolean" || value.review_independent !== true ||
    value.apply_independent !== true || value.atomic_apply !== false ||
    value.batch_mutation_supported !== false || value.partial_apply_visible !== true ||
    value.diff_content_included !== false) {
    throw new APIRequestError("File edit change set widened batch mutation authority",
      "INVALID_RESPONSE", 502);
  }
  const items = value.items.map((item) => {
    if (!hasOnlyKeys(item, ["allowed_actions", "apply_enabled", "destination_path", "diff_bytes",
      "id", "operation", "path", "secrets_redacted", "status", "updated_at"]) ||
      !boundedIdentity(item.id) ||
      !validWorkspaceRelativePath(item.path) || item.path === "." ||
      !["replace", "create", "move", "delete"].includes(String(item.operation)) ||
      (item.operation === "move" ?
        (!validWorkspaceRelativePath(item.destination_path) || item.destination_path === "." ||
          item.destination_path === item.path) : item.destination_path !== undefined) ||
      !["proposed", "approved", "applied", "denied", "failed"].includes(String(item.status)) ||
      !safeBoundedCount(item.diff_bytes, 1_064_960) ||
      typeof item.secrets_redacted !== "boolean" || typeof item.apply_enabled !== "boolean" ||
      !Array.isArray(item.allowed_actions) || item.allowed_actions.length > 2 ||
      !item.allowed_actions.every((action: unknown) =>
        action === "approve_intent" || action === "deny") ||
      !validDate(item.updated_at) ||
      (item.status === "proposed" ?
        !(item.allowed_actions.length === 0 ||
          (item.allowed_actions.length === 2 &&
            item.allowed_actions.includes("approve_intent") &&
            item.allowed_actions.includes("deny"))) : item.allowed_actions.length !== 0) ||
      (item.apply_enabled && item.status !== "approved")) {
      throw new APIRequestError("File edit change set item violated per-file authority",
        "INVALID_RESPONSE", 502);
    }
    return item;
  });
  const counts = new Map<string, number>([
    ["proposed", value.proposed_count], ["approved", value.approved_count],
    ["applied", value.applied_count], ["denied", value.denied_count],
    ["failed", value.failed_count],
  ]);
  if (value.returned_count !== items.length ||
    [...counts.values()].reduce((sum, count) => sum + count, 0) !== items.length ||
    items.some((item) => counts.get(String(item.status)) === undefined) ||
    value.total_diff_bytes !== items.reduce((sum, item) => sum + Number(item.diff_bytes), 0)) {
    throw new APIRequestError("File edit change set contains inconsistent partial state",
      "INVALID_RESPONSE", 502);
  }
  for (const status of counts.keys()) {
    if (items.filter((item) => item.status === status).length !== counts.get(status)) {
      throw new APIRequestError("File edit change set contains inconsistent partial state",
        "INVALID_RESPONSE", 502);
    }
  }
  return { ...value, items } as unknown as FileEditChangeSetView;
}

function parseFileEditReview(value: unknown, runID: string, editID: string,
  request: FileEditReviewRequestView): FileEditReviewView {
  if (!hasExactKeys(value, ["action", "edit", "file_written", "protocol_version", "replayed", "run_id"]) ||
    value.protocol_version !== "file_edit_review.v1" || value.run_id !== runID ||
    value.action !== request.action || value.file_written !== false || typeof value.replayed !== "boolean") {
    throw new APIRequestError("File edit review response violated its no-write contract",
      "INVALID_RESPONSE", 502);
  }
  const edit = parseFileEditPreview(value.edit);
  const expected = request.action === "approve_intent" ? "approved" : "denied";
  if (edit.id !== editID || edit.status !== expected || edit.apply_enabled !== false) {
    throw new APIRequestError("File edit review result does not match the requested decision",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, edit } as unknown as FileEditReviewView;
}

function parseFileEditApply(value: unknown, runID: string, editID: string): FileEditApplyView {
  if (!hasExactKeys(value, ["edit", "file_written", "policy_rechecked", "protocol_version",
    "receipt", "replayed", "run_id", "status"]) || value.protocol_version !== "file_edit_apply.v1" ||
    value.run_id !== runID || (value.status !== "applied" && value.status !== "failed") ||
    typeof value.replayed !== "boolean" || typeof value.file_written !== "boolean" ||
    value.policy_rechecked !== true) {
    throw new APIRequestError("File edit apply response violated its audited contract",
      "INVALID_RESPONSE", 502);
  }
  const edit = parseFileEditPreview(value.edit);
  if (edit.id !== editID || edit.apply_enabled !== false ||
    (value.status === "applied" && edit.status !== "applied") ||
    (value.status === "failed" && edit.status !== "failed")) {
    throw new APIRequestError("File edit apply result does not match the requested edit",
      "INVALID_RESPONSE", 502);
  }
  const receipt = parseOperationReceipt(value.receipt, "file_edit_apply", value.status,
    value.replayed);
  return { ...value, edit, receipt } as unknown as FileEditApplyView;
}

function parseRunWakeIntent(value: unknown, runID: string): NonNullable<RunWakeStateView["intent"]> {
  if (!isRecord(value) || !hasOnlyKeys(value, ["attempt_count", "background_loop_enabled",
    "base_backoff_seconds", "cancelled_at", "created_at", "deadline_at", "execution_enabled",
    "id", "initial_delay_seconds", "max_attempts", "max_backoff_seconds",
    "max_elapsed_seconds", "next_wake_at", "protocol_version", "run_id", "session_id",
    "status", "updated_at"]) || value.protocol_version !== "run_wake_intent.v1" ||
    value.run_id !== runID || !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !["queued", "leased", "completed", "cancelled", "exhausted"].includes(String(value.status)) ||
    !safeBoundedCount(value.attempt_count, 8) || !safePositiveInteger(value.max_attempts) ||
    Number(value.attempt_count) > Number(value.max_attempts) ||
    !safeBoundedCount(value.initial_delay_seconds, 3600) ||
    !safeBoundedCount(value.base_backoff_seconds, 21_600) ||
    !safeBoundedCount(value.max_backoff_seconds, 21_600) ||
    !safeBoundedCount(value.max_elapsed_seconds, 86_400) || value.execution_enabled !== false ||
    value.background_loop_enabled !== false || !validDate(value.next_wake_at) ||
    !validDate(value.deadline_at) || !validDate(value.created_at) || !validDate(value.updated_at) ||
    (value.cancelled_at !== undefined && !validDate(value.cancelled_at))) {
    throw new APIRequestError("Run wake intent violated its closed authority contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as NonNullable<RunWakeStateView["intent"]>;
}

function parseRunWakeState(value: unknown, runID: string): RunWakeStateView {
  if (!isRecord(value) || !hasOnlyKeys(value, ["found", "intent", "protocol_version", "run_id"]) ||
    value.protocol_version !== "run_wake_intent.v1" || value.run_id !== runID ||
    typeof value.found !== "boolean" || (value.found !== (value.intent !== undefined))) {
    throw new APIRequestError("Run wake state response is invalid", "INVALID_RESPONSE", 502);
  }
  return value.found
    ? { ...value, intent: parseRunWakeIntent(value.intent, runID) } as unknown as RunWakeStateView
    : value as unknown as RunWakeStateView;
}

function parseRunWakeControl(value: unknown, runID: string,
  expectedAction: "cancel" | "schedule"): RunWakeControlView {
  if (!hasExactKeys(value, ["action", "execution_started", "intent", "model_called",
    "protocol_version", "replayed", "tool_called"]) ||
    value.protocol_version !== "run_wake_control.v1" || value.action !== expectedAction ||
    typeof value.replayed !== "boolean" || value.execution_started !== false ||
    value.model_called !== false || value.tool_called !== false) {
    throw new APIRequestError("Run wake response widened execution authority",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, intent: parseRunWakeIntent(value.intent, runID) } as unknown as RunWakeControlView;
}

function parseRunWakeExecution(value: unknown, runID: string): RunWakeExecutionView {
  if (!isRecord(value) || !hasOnlyKeys(value, ["background_loop_enabled", "consumption_status",
    "execution_started", "intent", "model_called", "protocol_version", "receipt", "replayed", "run_id",
    "stop_reason", "tool_called"]) || value.protocol_version !== "run_wake_consumer.v1" ||
    value.run_id !== runID || value.consumption_status !== "completed" ||
    value.execution_started !== true || typeof value.model_called !== "boolean" ||
    typeof value.tool_called !== "boolean" || value.background_loop_enabled !== false ||
    typeof value.replayed !== "boolean" ||
    (value.stop_reason !== undefined && !boundedText(value.stop_reason, 64))) {
    throw new APIRequestError("Foreground Run wake response violated its bounded contract",
      "INVALID_RESPONSE", 502);
  }
  const intent = parseRunWakeIntent(value.intent, runID);
  if (intent.status !== "completed") {
    throw new APIRequestError("Foreground Run wake did not settle its exact intent",
      "INVALID_RESPONSE", 502);
  }
  const receipt = parseOperationReceipt(value.receipt, "run_wake_consume", "completed",
    value.replayed);
  return { ...value, intent, receipt } as unknown as RunWakeExecutionView;
}

function parseSkillPackageInstall(value: unknown,
  request: SkillPackageInstallRequestView): SkillPackageInstallView {
  if (!hasExactKeys(value, ["archive_sha256", "context_injection_authorized",
    "import_command_execution", "import_network_access", "import_provider_calls", "name",
    "package_fingerprint", "protocol_version", "receipt", "recovered_pending", "replayed",
    "run_selection_authorized", "surface", "tool_capability_grant", "trust_class", "version"]) ||
    value.protocol_version !== "skill_package_installation.v1" ||
    value.surface !== request.surface || value.trust_class !== "operator_installed_untrusted" ||
    !boundedText(value.name, 128) || !boundedText(value.version, 64) ||
    !isSHA256(value.archive_sha256) || !isSHA256(value.package_fingerprint) ||
    typeof value.replayed !== "boolean" || typeof value.recovered_pending !== "boolean" ||
    value.import_command_execution !== false || value.import_network_access !== false ||
    value.import_provider_calls !== false || value.tool_capability_grant !== false ||
    value.run_selection_authorized !== false || value.context_injection_authorized !== false) {
    throw new APIRequestError("Skill package installation widened inert Registry authority",
      "INVALID_RESPONSE", 502);
  }
  const receipt = parseOperationReceipt(value.receipt, "skill_package_install", "installed",
    value.replayed);
  return { ...value, receipt } as unknown as SkillPackageInstallView;
}

function parseOperationReceipt(value: unknown, kind: OperationReceiptView["kind"],
  outcome: OperationReceiptView["outcome"], replayed: boolean): OperationReceiptView {
  if (!hasExactKeys(value, ["cleanup_state", "durable", "kind", "outcome", "protocol_version",
    "recovery_action", "replayed", "retry_safe", "retry_strategy"]) ||
    value.protocol_version !== "operation_receipt.v1" || value.kind !== kind ||
    value.outcome !== outcome || value.durable !== true || value.replayed !== replayed ||
    value.retry_safe !== true) {
    throw new APIRequestError("Operation receipt violated its durable recovery contract",
      "INVALID_RESPONSE", 502);
  }
  const apply = kind === "file_edit_apply";
  if ((apply && value.retry_strategy !== "same_operation_key") ||
    (kind === "run_wake_consume" && value.retry_strategy !== "same_wake_generation") ||
    (kind === "skill_package_install" && value.retry_strategy !== "same_operation_key") ||
    (apply && !["complete", "pending_review"].includes(String(value.cleanup_state))) ||
    (!apply && value.cleanup_state !== "not_applicable") ||
    (value.cleanup_state === "pending_review" &&
      value.recovery_action !== "retry_after_cleanup_grace") ||
    (value.cleanup_state !== "pending_review" && value.recovery_action !== "none")) {
    throw new APIRequestError("Operation receipt widened recovery authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as OperationReceiptView;
}

function parseWorkspaceExplorer(value: unknown, workspaceID: string,
  expectedPath: string): WorkspaceExplorerView {
  if (!hasExactKeys(value, ["content", "entries", "kind", "path", "protocol_version",
    "provenance", "redaction_count", "returned_bytes", "root_path_exposed", "total_bytes",
    "truncated", "workspace_id"]) || value.protocol_version !== "workspace_explorer.v1" ||
    value.workspace_id !== workspaceID || value.path !== expectedPath ||
    (value.kind !== "directory" && value.kind !== "file") || !Array.isArray(value.entries) ||
    value.entries.length > 200 || typeof value.content !== "string" ||
    value.content.length > 131_072 || !safeBoundedCount(value.total_bytes, Number.MAX_SAFE_INTEGER) ||
    !safeBoundedCount(value.returned_bytes, 131_072) ||
    !safeBoundedCount(value.redaction_count, 65_536) || typeof value.truncated !== "boolean" ||
    value.root_path_exposed !== false || !isRecord(value.provenance) ||
    !hasExactKeys(value.provenance, ["content_sha256", "instruction_authorized", "source_kind",
      "source_ref", "version"]) || value.provenance.version !== "context_provenance.v1" ||
    value.provenance.source_ref !== expectedPath || !isSHA256(value.provenance.content_sha256) ||
    value.provenance.instruction_authorized !== false ||
    (value.kind === "directory" && (value.content !== "" || value.total_bytes !== 0 ||
      value.returned_bytes !== 0 || value.provenance.source_kind !== "workspace_listing")) ||
    (value.kind === "file" && (value.entries.length !== 0 ||
      value.provenance.source_kind !== "workspace_file" ||
      new TextEncoder().encode(value.content).length !== value.returned_bytes))) {
    throw new APIRequestError("Workspace explorer response violated its bounded evidence contract",
      "INVALID_RESPONSE", 502);
  }
  const entries = value.entries.map((entry) => {
    if (!hasExactKeys(entry, ["kind", "name", "path", "readable", "size_bytes"])) {
      throw new APIRequestError("Workspace explorer entry widened renderer path authority",
        "INVALID_RESPONSE", 502);
    }
    const expectedEntryPath = expectedPath === "." ? String(entry.name) :
      `${expectedPath}/${String(entry.name)}`;
    if (!validWorkspaceEntryName(entry.name) || !validWorkspaceRelativePath(entry.path) ||
      entry.path !== expectedEntryPath ||
      !["directory", "file", "blocked"].includes(String(entry.kind)) ||
      !safeBoundedCount(entry.size_bytes, Number.MAX_SAFE_INTEGER) ||
      typeof entry.readable !== "boolean" ||
      (entry.kind === "blocked" ? entry.readable !== false : entry.readable !== true) ||
      String(entry.name).startsWith(".cyberagent-edit-")) {
      throw new APIRequestError("Workspace explorer entry widened renderer path authority",
        "INVALID_RESPONSE", 502);
    }
    return entry;
  });
  return { ...value, entries } as unknown as WorkspaceExplorerView;
}

function parseWorkspaceSearch(value: unknown, workspaceID: string): WorkspaceSearchView {
  if (!hasExactKeys(value, ["protocol_version", "results", "root_path_exposed",
    "scanned_bytes", "scanned_entries", "scanned_files", "truncated", "workspace_id"]) ||
    value.protocol_version !== "workspace_search.v1" || value.workspace_id !== workspaceID ||
    !Array.isArray(value.results) || value.results.length > 50 ||
    !safeBoundedCount(value.scanned_entries, 1000) ||
    !safeBoundedCount(value.scanned_files, 64) ||
    !safeBoundedCount(value.scanned_bytes, 64 * (64 * 1024 + 4)) ||
    typeof value.truncated !== "boolean" || value.root_path_exposed !== false) {
    throw new APIRequestError("Workspace search response violated its bounded evidence contract",
      "INVALID_RESPONSE", 502);
  }
  const results = value.results.map((item) => {
    if (!hasExactKeys(item, ["content_truncated", "line", "match_kind", "path",
      "provenance", "snippet"]) || !validWorkspaceRelativePath(item.path) ||
      !["filename", "content", "filename_and_content"].includes(String(item.match_kind)) ||
      !safeBoundedCount(item.line, Number.MAX_SAFE_INTEGER) ||
      typeof item.snippet !== "string" || new TextEncoder().encode(item.snippet).length > 512 ||
      typeof item.content_truncated !== "boolean" || !hasExactKeys(item.provenance,
        ["content_sha256", "instruction_authorized", "source_kind", "source_ref", "version"]) ||
      item.provenance.version !== "context_provenance.v1" ||
      item.provenance.source_kind !== "workspace_file" ||
      item.provenance.source_ref !== item.path || !isSHA256(item.provenance.content_sha256) ||
      item.provenance.instruction_authorized !== false ||
      (item.match_kind === "filename" ? item.line !== 0 || item.snippet !== "" : item.line < 1)) {
      throw new APIRequestError("Workspace search result widened renderer or instruction authority",
        "INVALID_RESPONSE", 502);
    }
    return item;
  });
  return { ...value, results } as unknown as WorkspaceSearchView;
}

function parseRepositoryState(value: unknown, workspaceID: string): RepositoryStateView {
  const keys = ["available", "branch", "changes", "clean", "conflicted_count",
    "content_included", "detached", "head", "hooks_executed", "kind", "network_used",
    "process_started", "protocol_version", "read_only", "redaction_count",
    "remote_config_included", "root_path_exposed", "staged_count", "truncated",
    "untracked_count", "workspace_id", "worktree_count"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "repository_state.v1" ||
    value.workspace_id !== workspaceID || !["none", "git"].includes(String(value.kind)) ||
    typeof value.available !== "boolean" || typeof value.clean !== "boolean" ||
    typeof value.detached !== "boolean" || typeof value.truncated !== "boolean" ||
    value.read_only !== true || value.root_path_exposed !== false ||
    value.content_included !== false || value.remote_config_included !== false ||
    value.process_started !== false || value.network_used !== false ||
    value.hooks_executed !== false || typeof value.branch !== "string" ||
    value.branch.length > 255 || /[\u0000-\u001f\u007f]/u.test(value.branch) ||
    typeof value.head !== "string" || !/^(?:[0-9a-f]{12})?$/u.test(value.head) ||
    !safeBoundedCount(value.staged_count, 10_000) ||
    !safeBoundedCount(value.worktree_count, 10_000) ||
    !safeBoundedCount(value.untracked_count, 10_000) ||
    !safeBoundedCount(value.conflicted_count, 10_000) ||
    !safeBoundedCount(value.redaction_count, 10_000) || !Array.isArray(value.changes) ||
    value.changes.length > 200) {
    throw new APIRequestError("Repository state violated its read-only bounded contract",
      "INVALID_RESPONSE", 502);
  }
  const changes = value.changes.map((change) => {
    if (!hasExactKeys(change, ["path", "staging", "worktree"]) ||
      !validWorkspaceRelativePath(change.path) || change.path === "." ||
      !["unmodified", "untracked", "modified", "added", "deleted", "renamed", "copied",
        "conflicted"].includes(String(change.staging)) ||
      !["unmodified", "untracked", "modified", "added", "deleted", "renamed", "copied",
        "conflicted"].includes(String(change.worktree)) ||
      (change.staging === "unmodified" && change.worktree === "unmodified")) {
      throw new APIRequestError("Repository change widened path or status authority",
        "INVALID_RESPONSE", 502);
    }
    return change;
  });
  const total = value.staged_count + value.worktree_count + value.untracked_count;
  if ((value.available !== (value.kind === "git")) ||
    (!value.available && (value.clean || value.detached || value.branch !== "" ||
      value.head !== "" || changes.length !== 0 || total !== 0 ||
      value.conflicted_count !== 0 || value.redaction_count !== 0 || value.truncated)) ||
    (value.available && value.clean !== (total === 0)) ||
    value.conflicted_count > value.staged_count + value.worktree_count) {
    throw new APIRequestError("Repository state contains inconsistent status facts",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, changes } as unknown as RepositoryStateView;
}

function parseRepositoryDiff(value: unknown, workspaceID: string): RepositoryDiffView {
  const keys = ["authority_granted", "available", "base_head", "hooks_executed",
    "instruction_authorized", "items", "kind", "mutation_supported", "network_used",
    "omitted_count", "patch_content_included", "process_started", "protocol_version",
    "raw_content_included", "read_only", "redaction_count", "remote_config_included",
    "returned_count", "root_path_exposed", "total_patch_bytes", "truncated", "workspace_id"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "repository_diff.v1" ||
    value.workspace_id !== workspaceID || !["none", "git"].includes(String(value.kind)) ||
    typeof value.available !== "boolean" || typeof value.truncated !== "boolean" ||
    value.read_only !== true || value.instruction_authorized !== false ||
    value.mutation_supported !== false || value.authority_granted !== false ||
    value.root_path_exposed !== false || value.raw_content_included !== false ||
    value.patch_content_included !== value.available || value.remote_config_included !== false ||
    value.process_started !== false || value.network_used !== false || value.hooks_executed !== false ||
    typeof value.base_head !== "string" || !/^(?:[0-9a-f]{12})?$/u.test(value.base_head) ||
    !Array.isArray(value.items) || value.items.length > 50 ||
    !safeBoundedCount(value.returned_count, 50) || value.returned_count !== value.items.length ||
    !safeBoundedCount(value.omitted_count, 10_000) ||
    !safeBoundedCount(value.redaction_count, 10_000) ||
    !safeBoundedCount(value.total_patch_bytes, 512 * 1024) ||
    value.available !== (value.kind === "git")) {
    throw new APIRequestError("Repository diff violated its bounded read-only contract",
      "INVALID_RESPONSE", 502);
  }
  const status = ["unmodified", "untracked", "modified", "added", "deleted", "renamed",
    "copied", "conflicted"];
  const contentStates = ["text", "binary_or_unsupported", "size_limited", "linked", "unavailable"];
  let totalBytes = 0;
  let redactedItems = 0;
  const paths = new Set<string>();
  const items = value.items.map((item) => {
    if (!hasExactKeys(item, ["added_lines", "content_state", "deleted_lines", "patch",
      "patch_bytes", "path", "redacted", "staging", "truncated", "worktree"]) ||
      !validWorkspaceRelativePath(item.path) || item.path === "." || paths.has(String(item.path)) ||
      !status.includes(String(item.staging)) || !status.includes(String(item.worktree)) ||
      !contentStates.includes(String(item.content_state)) || typeof item.patch !== "string" ||
      !safeBoundedCount(item.patch_bytes, 64 * 1024) ||
      new TextEncoder().encode(item.patch).length !== item.patch_bytes ||
      !safeBoundedCount(item.added_lines, 64 * 1024) ||
      !safeBoundedCount(item.deleted_lines, 64 * 1024) ||
      typeof item.redacted !== "boolean" || typeof item.truncated !== "boolean" ||
      (item.content_state !== "text" &&
        (item.patch !== "" || item.patch_bytes !== 0 || item.added_lines !== 0 ||
          item.deleted_lines !== 0 || item.truncated))) {
      throw new APIRequestError("Repository diff item widened content or path authority",
        "INVALID_RESPONSE", 502);
    }
    paths.add(String(item.path));
    totalBytes += Number(item.patch_bytes);
    redactedItems += item.redacted ? 1 : 0;
    return item;
  });
  if (totalBytes !== value.total_patch_bytes || redactedItems > value.redaction_count ||
    ((items.some((item) => item.truncated) || value.omitted_count > 0) && !value.truncated) ||
    (!value.available && (items.length !== 0 || value.base_head !== "" ||
      value.total_patch_bytes !== 0 || value.omitted_count !== 0 || value.redaction_count !== 0))) {
    throw new APIRequestError("Repository diff contains inconsistent bounded facts",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as RepositoryDiffView;
}

function parseRepositoryHistory(value: unknown, workspaceID: string): RepositoryHistoryView {
  const keys = ["author_identity_included", "available", "branches", "commit_body_included",
    "commits", "detached", "first_parent_only", "head", "hooks_executed", "kind",
    "network_used", "omitted_branch_count", "process_started", "protocol_version",
    "read_only", "redaction_count", "remote_config_included", "returned_branch_count",
    "returned_commit_count", "root_path_exposed", "truncated", "workspace_id"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "repository_history.v1" ||
    value.workspace_id !== workspaceID || !["none", "git"].includes(String(value.kind)) ||
    typeof value.available !== "boolean" || value.available !== (value.kind === "git") ||
    typeof value.detached !== "boolean" || typeof value.truncated !== "boolean" ||
    value.first_parent_only !== true || value.read_only !== true ||
    value.root_path_exposed !== false || value.author_identity_included !== false ||
    value.commit_body_included !== false || value.remote_config_included !== false ||
    value.process_started !== false || value.network_used !== false ||
    value.hooks_executed !== false || typeof value.head !== "string" ||
    !/^(?:[0-9a-f]{12})?$/u.test(value.head) || !Array.isArray(value.commits) ||
    !Array.isArray(value.branches) || value.commits.length > 50 || value.branches.length > 64 ||
    !safeBoundedCount(value.returned_commit_count, 50) ||
    value.returned_commit_count !== value.commits.length ||
    !safeBoundedCount(value.returned_branch_count, 64) ||
    value.returned_branch_count !== value.branches.length ||
    !safeBoundedCount(value.omitted_branch_count, 1024) ||
    !safeBoundedCount(value.redaction_count, 10_000)) {
    throw new APIRequestError("Repository history violated its bounded local contract",
      "INVALID_RESPONSE", 502);
  }
  const hashes = new Set<string>();
  const objectIDs = new Set<string>();
  const commits = value.commits.map((commit) => {
    if (!hasExactKeys(commit, ["committed_at", "hash", "object_id", "parent_count", "redacted",
      "subject", "subject_bounded"]) || typeof commit.hash !== "string" ||
      !/^[0-9a-f]{12}$/u.test(commit.hash) || hashes.has(commit.hash) ||
      typeof commit.object_id !== "string" || !/^[0-9a-f]{40}$/u.test(commit.object_id) ||
      !commit.object_id.startsWith(commit.hash) || objectIDs.has(commit.object_id) ||
      typeof commit.subject !== "string" || commit.subject.length === 0 ||
      [...commit.subject].length > 512 || /[\u0000-\u001f\u007f]/u.test(commit.subject) ||
      !safeBoundedCount(commit.parent_count, 10_000) || !validDate(commit.committed_at) ||
      typeof commit.redacted !== "boolean" || typeof commit.subject_bounded !== "boolean") {
      throw new APIRequestError("Repository commit widened history or privacy authority",
        "INVALID_RESPONSE", 502);
    }
    hashes.add(commit.hash);
    objectIDs.add(commit.object_id);
    return commit;
  });
  const branchNames = new Set<string>();
  let currentBranches = 0;
  const branches = value.branches.map((branch) => {
    if (!hasExactKeys(branch, ["current", "head", "name"]) ||
      typeof branch.name !== "string" || branch.name.length === 0 ||
      [...branch.name].length > 255 || /[\u0000-\u001f\u007f]/u.test(branch.name) ||
      branchNames.has(branch.name) || typeof branch.head !== "string" ||
      !/^[0-9a-f]{12}$/u.test(branch.head) || typeof branch.current !== "boolean") {
      throw new APIRequestError("Repository branch widened local metadata authority",
        "INVALID_RESPONSE", 502);
    }
    branchNames.add(branch.name);
    currentBranches += branch.current ? 1 : 0;
    return branch;
  });
  if (currentBranches > 1 ||
    (!value.available && (value.head !== "" || value.detached || commits.length !== 0 ||
      branches.length !== 0 || value.omitted_branch_count !== 0 ||
      value.redaction_count !== 0 || value.truncated)) ||
    (value.detached && currentBranches !== 0) ||
    (value.head === "" && commits.length !== 0)) {
    throw new APIRequestError("Repository history contains inconsistent local facts",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, commits, branches } as unknown as RepositoryHistoryView;
}

function parseRepositoryFileHistory(value: unknown, workspaceID: string,
  path: string): RepositoryFileHistoryView {
  const keys = ["author_identity_included", "authority_granted", "available",
    "checkout_performed", "commit_body_included", "entries", "file_content_included",
    "first_parent_only", "head", "hooks_executed", "kind", "metadata_only", "network_used",
    "observed", "patch_included", "path", "process_started", "protocol_version", "read_only",
    "redaction_count", "reference_updated", "remote_config_included", "rename_inferred",
    "returned_entry_count", "root_path_exposed", "scanned_commit_count", "truncated",
    "workspace_id"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "repository_file_history.v1" ||
    value.workspace_id !== workspaceID || value.path !== path ||
    !validWorkspaceRelativePath(value.path) || value.path === "." ||
    !["none", "git"].includes(String(value.kind)) ||
    typeof value.available !== "boolean" || value.available !== (value.kind === "git") ||
    typeof value.head !== "string" || !/^(?:[0-9a-f]{12})?$/u.test(value.head) ||
    !Array.isArray(value.entries) || value.entries.length > 50 ||
    !safeBoundedCount(value.scanned_commit_count, 512) ||
    !safeBoundedCount(value.returned_entry_count, 50) ||
    value.returned_entry_count !== value.entries.length ||
    !safeBoundedCount(value.redaction_count, 512 * 240) ||
    typeof value.observed !== "boolean" || value.observed !== (value.entries.length > 0) ||
    typeof value.truncated !== "boolean" || value.first_parent_only !== true ||
    value.rename_inferred !== false || value.metadata_only !== true || value.read_only !== true ||
    value.authority_granted !== false || value.root_path_exposed !== false ||
    value.author_identity_included !== false || value.commit_body_included !== false ||
    value.file_content_included !== false || value.patch_included !== false ||
    value.remote_config_included !== false || value.checkout_performed !== false ||
    value.reference_updated !== false || value.process_started !== false ||
    value.network_used !== false || value.hooks_executed !== false) {
    throw new APIRequestError("Repository file history violated its exact metadata contract",
      "INVALID_RESPONSE", 502);
  }
  const objectIDs = new Set<string>();
  const kinds = ["", "regular", "executable", "symlink", "submodule"];
  let redactedEntries = 0;
  const entries = value.entries.map((entry) => {
    if (!hasExactKeys(entry, ["change", "committed_at", "content_changed", "current_kind",
      "hash", "mode_changed", "object_id", "previous_kind", "redacted", "subject",
      "subject_bounded"]) || typeof entry.object_id !== "string" ||
      !/^[0-9a-f]{40}$/u.test(entry.object_id) || objectIDs.has(entry.object_id) ||
      typeof entry.hash !== "string" || !/^[0-9a-f]{12}$/u.test(entry.hash) ||
      !entry.object_id.startsWith(entry.hash) || typeof entry.subject !== "string" ||
      entry.subject.length === 0 || [...entry.subject].length > 512 ||
      /[\u0000-\u001f\u007f]/u.test(entry.subject) || !validDate(entry.committed_at) ||
      !["added", "modified", "deleted"].includes(String(entry.change)) ||
      !kinds.includes(String(entry.previous_kind)) || !kinds.includes(String(entry.current_kind)) ||
      typeof entry.content_changed !== "boolean" || typeof entry.mode_changed !== "boolean" ||
      (!entry.content_changed && !entry.mode_changed) || typeof entry.redacted !== "boolean" ||
      typeof entry.subject_bounded !== "boolean" ||
      (entry.change === "added" && (entry.previous_kind !== "" || entry.current_kind === "" ||
        !entry.content_changed || !entry.mode_changed)) ||
      (entry.change === "deleted" && (entry.previous_kind === "" || entry.current_kind !== "" ||
        !entry.content_changed || !entry.mode_changed)) ||
      (entry.change === "modified" &&
        (entry.previous_kind === "" || entry.current_kind === ""))) {
      throw new APIRequestError("Repository file history entry widened content authority",
        "INVALID_RESPONSE", 502);
    }
    objectIDs.add(entry.object_id);
    redactedEntries += entry.redacted ? 1 : 0;
    return entry;
  });
  if (redactedEntries > value.redaction_count ||
    (!value.available && (value.head !== "" || value.scanned_commit_count !== 0 ||
      entries.length !== 0 || value.redaction_count !== 0 || value.observed || value.truncated)) ||
    (value.head === "" && (value.scanned_commit_count !== 0 || entries.length !== 0)) ||
    (entries.length > value.scanned_commit_count)) {
    throw new APIRequestError("Repository file history contains inconsistent bounded facts",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, entries } as unknown as RepositoryFileHistoryView;
}

function parseRepositoryCommitDetail(value: unknown, workspaceID: string,
  objectID: string): RepositoryCommitDetailView {
  const keys = ["author_identity_included", "available", "changed_file_count", "changes",
    "checkout_performed", "commit_body_included", "committed_at", "file_content_included",
    "first_parent_only", "hash", "hooks_executed", "kind", "network_used", "object_id",
    "omitted_change_count", "parent_count", "patch_included", "process_started",
    "protocol_version", "read_only", "redaction_count", "reference_updated",
    "remote_config_included", "returned_change_count", "root_path_exposed", "subject",
    "truncated", "workspace_id"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "repository_commit_detail.v1" ||
    value.workspace_id !== workspaceID || value.object_id !== objectID ||
    !/^[0-9a-f]{40}$/u.test(String(value.object_id)) ||
    !["none", "git"].includes(String(value.kind)) ||
    typeof value.available !== "boolean" || value.available !== (value.kind === "git") ||
    value.first_parent_only !== true || value.read_only !== true ||
    value.root_path_exposed !== false || value.author_identity_included !== false ||
    value.commit_body_included !== false || value.file_content_included !== false ||
    value.patch_included !== false || value.remote_config_included !== false ||
    value.checkout_performed !== false || value.reference_updated !== false ||
    value.process_started !== false || value.network_used !== false ||
    value.hooks_executed !== false || typeof value.truncated !== "boolean" ||
    !Array.isArray(value.changes) || value.changes.length > 200 ||
    !safeBoundedCount(value.changed_file_count, 40_000) ||
    !safeBoundedCount(value.returned_change_count, 200) ||
    value.returned_change_count !== value.changes.length ||
    !safeBoundedCount(value.omitted_change_count, 40_000) ||
    value.changed_file_count !== value.returned_change_count + value.omitted_change_count ||
    !safeBoundedCount(value.redaction_count, 40_000) ||
    !safeBoundedCount(value.parent_count, 1024)) {
    throw new APIRequestError("Repository commit detail violated its exact read-only contract",
      "INVALID_RESPONSE", 502);
  }
  const paths = new Set<string>();
  const kinds = ["", "regular", "executable", "symlink", "submodule"];
  const changes = value.changes.map((change) => {
    if (!hasExactKeys(change, ["change", "content_changed", "current_kind", "mode_changed",
      "path", "previous_kind"]) || !validWorkspaceRelativePath(change.path) ||
      change.path === "." || paths.has(String(change.path)) ||
      !["added", "modified", "deleted"].includes(String(change.change)) ||
      !kinds.includes(String(change.previous_kind)) || !kinds.includes(String(change.current_kind)) ||
      typeof change.content_changed !== "boolean" || typeof change.mode_changed !== "boolean" ||
      (!change.content_changed && !change.mode_changed) ||
      (change.change === "added" && (change.previous_kind !== "" || change.current_kind === "")) ||
      (change.change === "deleted" && (change.previous_kind === "" || change.current_kind !== "")) ||
      (change.change === "modified" &&
        (change.previous_kind === "" || change.current_kind === ""))) {
      throw new APIRequestError("Repository commit file metadata widened content authority",
        "INVALID_RESPONSE", 502);
    }
    paths.add(String(change.path));
    return change;
  });
  if ((!value.available && (value.hash !== "" || value.subject !== "" ||
    value.committed_at !== "0001-01-01T00:00:00Z" || value.parent_count !== 0 ||
    value.changed_file_count !== 0 || value.omitted_change_count !== 0 ||
    value.redaction_count !== 0 || value.truncated)) ||
    (value.available && (typeof value.hash !== "string" || !/^[0-9a-f]{12}$/u.test(value.hash) ||
      !objectID.startsWith(value.hash) || typeof value.subject !== "string" ||
      value.subject.length === 0 || [...value.subject].length > 512 ||
      /[\u0000-\u001f\u007f]/u.test(value.subject) || !validDate(value.committed_at))) ||
    (value.omitted_change_count > 0 && !value.truncated)) {
    throw new APIRequestError("Repository commit detail contains inconsistent bounded facts",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, changes } as unknown as RepositoryCommitDetailView;
}

function parseRepositoryCommitComparison(value: unknown, workspaceID: string,
  baseObjectID: string, headObjectID: string): RepositoryCommitComparisonView {
  const keys = ["ancestor_required", "author_identity_included", "authority_granted",
    "available", "base_committed_at", "base_hash", "base_object_id", "base_redacted",
    "base_subject", "base_subject_bounded", "changed_file_count", "changes",
    "checkout_performed", "commit_body_included", "file_content_included", "head_committed_at",
    "head_hash", "head_object_id", "head_redacted", "head_subject", "head_subject_bounded",
    "hooks_executed", "kind", "metadata_only", "network_used", "omitted_change_count",
    "patch_included", "process_started", "protocol_version", "read_only", "redaction_count",
    "reference_updated", "remote_config_included", "rename_inferred", "returned_change_count",
    "root_path_exposed", "same_object", "truncated", "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "repository_commit_comparison.v1" ||
    value.workspace_id !== workspaceID || value.base_object_id !== baseObjectID ||
    value.head_object_id !== headObjectID || !/^[0-9a-f]{40}$/u.test(baseObjectID) ||
    !/^[0-9a-f]{40}$/u.test(headObjectID) || !["none", "git"].includes(String(value.kind)) ||
    typeof value.available !== "boolean" || value.available !== (value.kind === "git") ||
    value.same_object !== (baseObjectID === headObjectID) || value.metadata_only !== true ||
    value.read_only !== true || value.rename_inferred !== false ||
    value.ancestor_required !== false || value.authority_granted !== false ||
    value.root_path_exposed !== false || value.author_identity_included !== false ||
    value.commit_body_included !== false || value.file_content_included !== false ||
    value.patch_included !== false || value.remote_config_included !== false ||
    value.checkout_performed !== false || value.reference_updated !== false ||
    value.process_started !== false || value.network_used !== false ||
    value.hooks_executed !== false || typeof value.truncated !== "boolean" ||
    typeof value.base_redacted !== "boolean" || typeof value.base_subject_bounded !== "boolean" ||
    typeof value.head_redacted !== "boolean" || typeof value.head_subject_bounded !== "boolean" ||
    !Array.isArray(value.changes) || value.changes.length > 200 ||
    !safeBoundedCount(value.changed_file_count, 40_000) ||
    !safeBoundedCount(value.returned_change_count, 200) ||
    value.returned_change_count !== value.changes.length ||
    !safeBoundedCount(value.omitted_change_count, 40_000) ||
    value.changed_file_count !== value.returned_change_count + value.omitted_change_count ||
    !safeBoundedCount(value.redaction_count, 40_000)) {
    throw new APIRequestError("Repository commit comparison violated its metadata contract",
      "INVALID_RESPONSE", 502);
  }
  const paths = new Set<string>();
  const kinds = ["", "regular", "executable", "symlink", "submodule"];
  const changes = value.changes.map((change) => {
    if (!hasExactKeys(change, ["change", "content_changed", "current_kind", "mode_changed",
      "path", "previous_kind"]) || !validWorkspaceRelativePath(change.path) ||
      change.path === "." || paths.has(String(change.path)) ||
      !["added", "modified", "deleted"].includes(String(change.change)) ||
      !kinds.includes(String(change.previous_kind)) || !kinds.includes(String(change.current_kind)) ||
      typeof change.content_changed !== "boolean" || typeof change.mode_changed !== "boolean" ||
      (!change.content_changed && !change.mode_changed) ||
      (change.change === "added" && (change.previous_kind !== "" || change.current_kind === "")) ||
      (change.change === "deleted" && (change.previous_kind === "" || change.current_kind !== "")) ||
      (change.change === "modified" &&
        (change.previous_kind === "" || change.current_kind === ""))) {
      throw new APIRequestError("Repository comparison file metadata widened content authority",
        "INVALID_RESPONSE", 502);
    }
    paths.add(String(change.path));
    return change;
  });
  const redactedSubjects = Number(value.base_redacted) + Number(value.head_redacted);
  if ((!value.available && (value.base_hash !== "" || value.head_hash !== "" ||
      value.base_subject !== "" || value.head_subject !== "" ||
      value.base_committed_at !== "0001-01-01T00:00:00Z" ||
      value.head_committed_at !== "0001-01-01T00:00:00Z" || value.changed_file_count !== 0 ||
      value.redaction_count !== 0 || value.truncated)) ||
    (value.available && (typeof value.base_hash !== "string" ||
      !/^[0-9a-f]{12}$/u.test(value.base_hash) || !baseObjectID.startsWith(value.base_hash) ||
      typeof value.head_hash !== "string" || !/^[0-9a-f]{12}$/u.test(value.head_hash) ||
      !headObjectID.startsWith(value.head_hash) || typeof value.base_subject !== "string" ||
      value.base_subject.length === 0 || [...value.base_subject].length > 512 ||
      /[\u0000-\u001f\u007f]/u.test(value.base_subject) ||
      typeof value.head_subject !== "string" || value.head_subject.length === 0 ||
      [...value.head_subject].length > 512 || /[\u0000-\u001f\u007f]/u.test(value.head_subject) ||
      !validDate(value.base_committed_at) || !validDate(value.head_committed_at))) ||
    redactedSubjects > value.redaction_count ||
    ((value.base_subject_bounded || value.head_subject_bounded ||
      value.omitted_change_count > 0) && !value.truncated) ||
    (value.same_object && (value.changed_file_count !== 0 || changes.length !== 0))) {
    throw new APIRequestError("Repository comparison contains inconsistent exact facts",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, changes } as unknown as RepositoryCommitComparisonView;
}

async function parseRepositoryCommitFilePreview(value: unknown, workspaceID: string,
  objectID: string, path: string): Promise<RepositoryCommitFilePreviewView> {
  const keys = ["authority_granted", "checkout_performed", "content", "hash",
    "hooks_executed", "kind", "mutation_supported", "network_used", "object_id", "path",
    "process_started", "protocol_version", "provenance", "raw_blob_included", "read_only",
    "redacted", "redacted_content_included", "redaction_count", "reference_updated",
    "remote_config_included", "returned_bytes", "root_path_exposed", "total_bytes",
    "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "repository_commit_file_preview.v1" ||
    value.workspace_id !== workspaceID || value.object_id !== objectID || value.path !== path ||
    !/^[0-9a-f]{40}$/u.test(String(value.object_id)) ||
    typeof value.hash !== "string" || !/^[0-9a-f]{12}$/u.test(value.hash) ||
    !objectID.startsWith(value.hash) || !validWorkspaceRelativePath(value.path) ||
    value.path === "." || !["regular", "executable"].includes(String(value.kind)) ||
    typeof value.content !== "string" ||
    /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/u.test(value.content) ||
    !safeBoundedCount(value.total_bytes, 64 * 1024) ||
    !safeBoundedCount(value.returned_bytes, 128 * 1024) ||
    !safeBoundedCount(value.redaction_count, 64 * 1024) ||
    typeof value.redacted !== "boolean" || value.redacted !== (value.redaction_count > 0) ||
    value.read_only !== true || value.mutation_supported !== false ||
    value.authority_granted !== false || value.root_path_exposed !== false ||
    value.raw_blob_included !== false || value.redacted_content_included !== true ||
    value.remote_config_included !== false || value.checkout_performed !== false ||
    value.reference_updated !== false || value.process_started !== false ||
    value.network_used !== false || value.hooks_executed !== false ||
    !hasExactKeys(value.provenance, ["content_sha256", "instruction_authorized", "source_kind",
      "source_ref", "version"]) || value.provenance.version !== "context_provenance.v1" ||
    value.provenance.source_kind !== "repository_commit_file" ||
    value.provenance.source_ref !== path || !isSHA256(value.provenance.content_sha256) ||
    value.provenance.instruction_authorized !== false) {
    throw new APIRequestError("Repository commit preview violated its redacted evidence contract",
      "INVALID_RESPONSE", 502);
  }
  const encoded = new TextEncoder().encode(value.content);
  if (encoded.length !== value.returned_bytes) {
    throw new APIRequestError("Repository commit preview byte count is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  const digest = new Uint8Array(await globalThis.crypto.subtle.digest("SHA-256", encoded));
  const digestHex = [...digest].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  if (digestHex !== value.provenance.content_sha256) {
    throw new APIRequestError("Repository commit preview digest verification failed",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as RepositoryCommitFilePreviewView;
}

function validVerificationText(value: unknown, maximum: number, multiline: boolean): value is string {
  if (typeof value !== "string" || value === "" || value.trim() !== value ||
    [...value].length > maximum || value.includes("\0")) {
    return false;
  }
  return multiline ? !/[\u0001-\u0008\u000b-\u001f\u007f]/u.test(value) :
    !/[\u0000-\u001f\u007f]/u.test(value);
}

function parseVerificationEvidenceItem(value: unknown, runID: string,
  sessionID = "", workspaceID = "", control = false): VerificationEvidenceControlView {
  const keys = ["approval", "authority_granted", "command_executed", "id", "immutable",
    "model_assertion", "operator_supplied", "outcome", "protocol_version", "recorded_at",
    "redacted", "run_id", "session_id", "summary", "summary_sha256", "title", "workspace_id"];
  if (control) keys.push("replayed");
  if (!hasExactKeys(value, keys) || value.protocol_version !== "operator_verification_evidence.v1" ||
    value.run_id !== runID || !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || (sessionID !== "" && value.session_id !== sessionID) ||
    (workspaceID !== "" && value.workspace_id !== workspaceID) ||
    !["pass", "fail", "unknown"].includes(String(value.outcome)) ||
    !validVerificationText(value.title, 160, false) ||
    !validVerificationText(value.summary, 2048, true) || !isSHA256(value.summary_sha256) ||
    typeof value.redacted !== "boolean" || !validDate(value.recorded_at) ||
    value.immutable !== true || value.operator_supplied !== true ||
    value.command_executed !== false || value.model_assertion !== false ||
    value.approval !== false || value.authority_granted !== false ||
    (control && typeof value.replayed !== "boolean")) {
    throw new APIRequestError("Verification evidence widened observation authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as VerificationEvidenceControlView;
}

function parseVerificationEvidenceInventory(value: unknown,
  runID: string): VerificationEvidenceInventoryView {
  if (!hasExactKeys(value, ["fail_count", "items", "pass_count", "protocol_version", "run_id",
    "session_id", "truncated", "unknown_count", "workspace_id"]) ||
    value.protocol_version !== "operator_verification_inventory.v1" || value.run_id !== runID ||
    !boundedIdentity(value.session_id) || !boundedIdentity(value.workspace_id) ||
    !Array.isArray(value.items) || value.items.length > 100 ||
    !safeBoundedCount(value.pass_count, 100) || !safeBoundedCount(value.fail_count, 100) ||
    !safeBoundedCount(value.unknown_count, 100) || typeof value.truncated !== "boolean" ||
    value.pass_count + value.fail_count + value.unknown_count !== value.items.length) {
    throw new APIRequestError("Verification inventory violated its immutable bounded contract",
      "INVALID_RESPONSE", 502);
  }
  const ids = new Set<string>();
  const items = value.items.map((item) => {
    const parsed = parseVerificationEvidenceItem(item, runID, String(value.session_id),
      String(value.workspace_id));
    if (ids.has(parsed.id)) {
      throw new APIRequestError("Verification inventory repeated an immutable identity",
        "INVALID_RESPONSE", 502);
    }
    ids.add(parsed.id);
    return parsed;
  });
  if (value.truncated && items.length !== 100) {
    throw new APIRequestError("Verification inventory truncation is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as VerificationEvidenceInventoryView;
}

function parseVerificationPlan(value: unknown, runID: string, sessionID = "",
  workspaceID = "", control = false): VerificationPlanControlView {
  const keys = ["approval", "authority_granted", "command_executed", "created_at",
    "guidance_only", "id", "immutable", "item_count", "items", "model_assertion",
    "operator_supplied", "plan_sha256", "protocol_version", "redacted", "result_inferred",
    "run_id", "session_id", "summary", "title", "workspace_id"];
  if (control) keys.push("replayed");
  if (!hasExactKeys(value, keys) || value.protocol_version !== "operator_verification_plan.v1" ||
    value.run_id !== runID || !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || (sessionID !== "" && value.session_id !== sessionID) ||
    (workspaceID !== "" && value.workspace_id !== workspaceID) ||
    !validVerificationText(value.title, 160, false) ||
    !validVerificationText(value.summary, 2048, true) || !isSHA256(value.plan_sha256) ||
    typeof value.redacted !== "boolean" || !validDate(value.created_at) ||
    !Array.isArray(value.items) || value.items.length < 1 || value.items.length > 32 ||
    value.item_count !== value.items.length || value.immutable !== true ||
    value.operator_supplied !== true || value.guidance_only !== true ||
    value.command_executed !== false || value.model_assertion !== false ||
    value.result_inferred !== false || value.approval !== false ||
    value.authority_granted !== false || (control && typeof value.replayed !== "boolean")) {
    throw new APIRequestError("Verification plan widened guidance or result authority",
      "INVALID_RESPONSE", 502);
  }
  let itemRedacted = false;
  const items = value.items.map((item, index) => {
    if (!hasExactKeys(item, ["expected_observation", "item_sha256", "ordinal", "redacted",
      "title"]) || item.ordinal !== index + 1 ||
      !validVerificationText(item.title, 160, false) ||
      !validVerificationText(item.expected_observation, 1024, true) ||
      !isSHA256(item.item_sha256) || typeof item.redacted !== "boolean") {
      throw new APIRequestError("Verification plan item violated its bounded checklist contract",
        "INVALID_RESPONSE", 502);
    }
    itemRedacted = itemRedacted || item.redacted;
    return item;
  });
  if (itemRedacted && !value.redacted) {
    throw new APIRequestError("Verification plan redaction state is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as VerificationPlanControlView;
}

function parseVerificationPlanInventory(value: unknown,
  runID: string): VerificationPlanInventoryView {
  if (!hasExactKeys(value, ["items", "protocol_version", "run_id", "session_id", "truncated",
    "workspace_id"]) || value.protocol_version !== "operator_verification_plan_inventory.v1" ||
    value.run_id !== runID || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !Array.isArray(value.items) ||
    value.items.length > 50 || typeof value.truncated !== "boolean") {
    throw new APIRequestError("Verification plan inventory violated its immutable bounded contract",
      "INVALID_RESPONSE", 502);
  }
  const identities = new Set<string>();
  const items = value.items.map((item) => {
    const parsed = parseVerificationPlan(item, runID, String(value.session_id),
      String(value.workspace_id));
    if (identities.has(parsed.id)) {
      throw new APIRequestError("Verification plan inventory repeated an immutable identity",
        "INVALID_RESPONSE", 502);
    }
    identities.add(parsed.id);
    return parsed;
  });
  if (value.truncated && items.length !== 50) {
	throw new APIRequestError("Verification plan inventory truncation is inconsistent",
	  "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as VerificationPlanInventoryView;
}

function parseVerificationAssociation(value: unknown, runID: string,
  control = false): VerificationAssociationControlView {
  const keys = ["approval", "associated_at", "association_event_sequence",
    "authority_granted", "command_executed", "evidence_event_sequence", "evidence_id",
    "evidence_outcome", "id", "immutable", "metadata_only", "model_assertion",
    "operator_supplied", "plan_id", "plan_item_ordinal", "plan_item_sha256",
    "protocol_version", "record_rewritten", "result_inferred", "run_id", "session_id",
    "workspace_id"];
  if (control) keys.push("replayed");
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_evidence_association.v1" ||
    value.run_id !== runID || !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !boundedIdentity(value.plan_id) ||
    !safePositiveInteger(value.plan_item_ordinal) || value.plan_item_ordinal > 32 ||
    !isSHA256(value.plan_item_sha256) || !boundedIdentity(value.evidence_id) ||
    !["pass", "fail", "unknown"].includes(String(value.evidence_outcome)) ||
    !safePositiveInteger(value.evidence_event_sequence) ||
    !safePositiveInteger(value.association_event_sequence) ||
    value.association_event_sequence <= value.evidence_event_sequence ||
    !validDate(value.associated_at) || value.immutable !== true ||
    value.operator_supplied !== true || value.metadata_only !== true ||
    value.command_executed !== false || value.model_assertion !== false ||
    value.result_inferred !== false || value.record_rewritten !== false ||
    value.approval !== false || value.authority_granted !== false ||
    (control && typeof value.replayed !== "boolean")) {
    throw new APIRequestError("Verification association widened result or mutation authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as VerificationAssociationControlView;
}

function parseVerificationPlanCoverage(value: unknown,
  runID: string): VerificationPlanCoverageInventoryView {
  const keys = ["approval", "associated_evidence_count", "associations",
    "associations_truncated", "authority_granted", "command_executed", "metadata_only",
    "model_assertion", "observed_plan_item_count", "plan_count", "plan_item_count", "plans",
    "plans_truncated", "protocol_version", "read_only", "record_rewritten",
    "result_inferred", "run_id", "session_id", "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_coverage.v1" ||
    value.run_id !== runID || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !Array.isArray(value.plans) ||
    value.plans.length > 50 || value.plan_count !== value.plans.length ||
    !safeBoundedCount(value.plan_item_count, 1600) ||
    !safeBoundedCount(value.observed_plan_item_count, 1600) ||
    value.observed_plan_item_count > value.plan_item_count ||
    !safeBoundedCount(value.associated_evidence_count, 1_000_000_000) ||
    !Array.isArray(value.associations) || value.associations.length > 100 ||
    typeof value.plans_truncated !== "boolean" ||
    typeof value.associations_truncated !== "boolean" || value.metadata_only !== true ||
    value.read_only !== true || value.result_inferred !== false ||
    value.command_executed !== false || value.model_assertion !== false ||
    value.record_rewritten !== false || value.approval !== false ||
    value.authority_granted !== false) {
    throw new APIRequestError("Verification coverage widened metadata-only authority",
      "INVALID_RESPONSE", 502);
  }
  const planIDs = new Set<string>();
  const planItems = new Map<string, Map<number, string>>();
  let itemTotal = 0;
  let observedTotal = 0;
  let associationTotal = 0;
  const plans = value.plans.map((plan) => {
    if (!hasExactKeys(plan, ["associated_evidence_count", "item_count", "items",
      "observed_item_count", "plan_id", "plan_sha256"]) ||
      !boundedIdentity(plan.plan_id) || planIDs.has(String(plan.plan_id)) ||
      !isSHA256(plan.plan_sha256) || !Array.isArray(plan.items) ||
      plan.items.length < 1 || plan.items.length > 32 || plan.item_count !== plan.items.length ||
      !safeBoundedCount(plan.observed_item_count, 32) ||
      !safeBoundedCount(plan.associated_evidence_count, 1_000_000_000)) {
      throw new APIRequestError("Verification coverage plan metadata is invalid",
        "INVALID_RESPONSE", 502);
    }
    const ordinals = new Map<number, string>();
    let observed = 0;
    let associated = 0;
    const items = plan.items.map((item, index) => {
      if (!hasExactKeys(item, ["associated_evidence_count", "fail_count", "item_sha256",
        "latest_association_event_sequence", "ordinal", "pass_count", "unknown_count"]) ||
        item.ordinal !== index + 1 || !isSHA256(item.item_sha256) ||
        !safeBoundedCount(item.associated_evidence_count, 1_000_000_000) ||
        !safeBoundedCount(item.pass_count, 1_000_000_000) ||
        !safeBoundedCount(item.fail_count, 1_000_000_000) ||
        !safeBoundedCount(item.unknown_count, 1_000_000_000) ||
        item.pass_count + item.fail_count + item.unknown_count !==
          item.associated_evidence_count ||
        !safeBoundedCount(item.latest_association_event_sequence, Number.MAX_SAFE_INTEGER) ||
        ((item.associated_evidence_count === 0) !==
          (item.latest_association_event_sequence === 0))) {
        throw new APIRequestError("Verification coverage item inferred a non-explicit result",
          "INVALID_RESPONSE", 502);
      }
      if (item.associated_evidence_count > 0) observed += 1;
      associated += Number(item.associated_evidence_count);
      ordinals.set(Number(item.ordinal), String(item.item_sha256));
      return item;
    });
    if (plan.observed_item_count !== observed || plan.associated_evidence_count !== associated) {
      throw new APIRequestError("Verification coverage plan counts are inconsistent",
        "INVALID_RESPONSE", 502);
    }
    planIDs.add(String(plan.plan_id));
    planItems.set(String(plan.plan_id), ordinals);
    itemTotal += Number(plan.item_count);
    observedTotal += observed;
    associationTotal += associated;
    return { ...plan, items };
  });
  const associationIDs = new Set<string>();
  const evidenceIDs = new Set<string>();
  const associations = value.associations.map((association) => {
    if (!hasExactKeys(association, ["associated_at", "association_event_sequence",
      "evidence_event_sequence", "evidence_id", "evidence_outcome", "id", "plan_id",
      "plan_item_ordinal", "plan_item_sha256"]) || !boundedIdentity(association.id) ||
      associationIDs.has(String(association.id)) || !boundedIdentity(association.plan_id) ||
      !safePositiveInteger(association.plan_item_ordinal) || association.plan_item_ordinal > 32 ||
      !isSHA256(association.plan_item_sha256) || !boundedIdentity(association.evidence_id) ||
      evidenceIDs.has(String(association.evidence_id)) ||
      !["pass", "fail", "unknown"].includes(String(association.evidence_outcome)) ||
      !safePositiveInteger(association.evidence_event_sequence) ||
      !safePositiveInteger(association.association_event_sequence) ||
      association.association_event_sequence <= association.evidence_event_sequence ||
      !validDate(association.associated_at)) {
      throw new APIRequestError("Verification coverage association metadata is invalid",
        "INVALID_RESPONSE", 502);
    }
    const knownItems = planItems.get(String(association.plan_id));
    if (knownItems && knownItems.get(Number(association.plan_item_ordinal)) !==
      association.plan_item_sha256) {
      throw new APIRequestError("Verification association escaped its exact plan item",
        "INVALID_RESPONSE", 502);
    }
    associationIDs.add(String(association.id));
    evidenceIDs.add(String(association.evidence_id));
    return association;
  });
  if (itemTotal !== value.plan_item_count || observedTotal !== value.observed_plan_item_count ||
    associationTotal !== value.associated_evidence_count ||
    (value.plans_truncated && plans.length !== 50) ||
    (value.associations_truncated && associations.length !== 100)) {
    throw new APIRequestError("Verification coverage aggregate is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, plans, associations } as unknown as VerificationPlanCoverageInventoryView;
}

function parseResponsePage(value: unknown, expectedLimit: number): Page {
  if (!isRecord(value) || !hasOnlyKeys(value, ["limit", "next_cursor", "truncated"]) ||
    !safePositiveInteger(value.limit) || value.limit > 100 || value.limit !== expectedLimit ||
    (value.next_cursor !== undefined && (!boundedText(value.next_cursor, 512) ||
      value.next_cursor.trim() !== value.next_cursor)) ||
    (value.truncated !== undefined && typeof value.truncated !== "boolean") ||
    (Boolean(value.next_cursor) && value.truncated === true)) {
    throw new APIRequestError("API pagination metadata is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as Page;
}

function parseVerificationPlanItemCoverage(value: unknown, runID: string, planID: string,
  ordinal: number, page: Page, firstPage: boolean): VerificationPlanItemCoverageDetailView {
  const keys = ["approval", "associated_evidence_count", "associations",
    "associations_truncated", "authority_granted", "command_executed", "fail_count",
    "latest_association_event_sequence", "metadata_only", "model_assertion",
    "operator_identity_included", "pass_count", "plan_id", "plan_item_ordinal",
    "plan_item_sha256", "plan_sha256", "private_evidence_bodies_included",
    "private_plan_body_included", "protocol_version", "read_only", "record_rewritten",
    "result_inferred", "run_id", "session_id", "unknown_count", "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_item_coverage.v1" ||
    value.run_id !== runID || value.plan_id !== planID || value.plan_item_ordinal !== ordinal ||
    !boundedIdentity(value.session_id) || !boundedIdentity(value.workspace_id) ||
    !isSHA256(value.plan_sha256) || !isSHA256(value.plan_item_sha256) ||
    !safeBoundedCount(value.associated_evidence_count, 1_000_000_000) ||
    !safeBoundedCount(value.pass_count, 1_000_000_000) ||
    !safeBoundedCount(value.fail_count, 1_000_000_000) ||
    !safeBoundedCount(value.unknown_count, 1_000_000_000) ||
    value.pass_count + value.fail_count + value.unknown_count !==
      value.associated_evidence_count ||
    !safeBoundedCount(value.latest_association_event_sequence, Number.MAX_SAFE_INTEGER) ||
    ((value.associated_evidence_count === 0) !==
      (value.latest_association_event_sequence === 0)) ||
    !Array.isArray(value.associations) || value.associations.length > page.limit ||
    typeof value.associations_truncated !== "boolean" || value.metadata_only !== true ||
    value.read_only !== true || value.private_plan_body_included !== false ||
    value.private_evidence_bodies_included !== false ||
    value.operator_identity_included !== false || value.result_inferred !== false ||
    value.command_executed !== false || value.model_assertion !== false ||
    value.record_rewritten !== false || value.approval !== false ||
    value.authority_granted !== false) {
    throw new APIRequestError("Verification item coverage widened its read-only boundary",
      "INVALID_RESPONSE", 502);
  }
  const associationIDs = new Set<string>();
  const evidenceIDs = new Set<string>();
  let previousSequence = Number.MAX_SAFE_INTEGER;
  let returnedPass = 0;
  let returnedFail = 0;
  let returnedUnknown = 0;
  const associations = value.associations.map((association, index) => {
    if (!hasExactKeys(association, ["associated_at", "association_event_sequence",
      "evidence_event_sequence", "evidence_id", "evidence_outcome", "id", "plan_id",
      "plan_item_ordinal", "plan_item_sha256"]) || !boundedIdentity(association.id) ||
      associationIDs.has(String(association.id)) || association.plan_id !== planID ||
      association.plan_item_ordinal !== ordinal ||
      association.plan_item_sha256 !== value.plan_item_sha256 ||
      !boundedIdentity(association.evidence_id) || evidenceIDs.has(String(association.evidence_id)) ||
      !["pass", "fail", "unknown"].includes(String(association.evidence_outcome)) ||
      !safePositiveInteger(association.evidence_event_sequence) ||
      !safePositiveInteger(association.association_event_sequence) ||
      association.association_event_sequence <= association.evidence_event_sequence ||
      association.association_event_sequence > Number(value.latest_association_event_sequence) ||
      (index > 0 && association.association_event_sequence >= previousSequence) ||
      !validDate(association.associated_at)) {
      throw new APIRequestError("Verification item association escaped its exact binding",
        "INVALID_RESPONSE", 502);
    }
    associationIDs.add(String(association.id));
    evidenceIDs.add(String(association.evidence_id));
    previousSequence = Number(association.association_event_sequence);
    if (association.evidence_outcome === "pass") returnedPass += 1;
    if (association.evidence_outcome === "fail") returnedFail += 1;
    if (association.evidence_outcome === "unknown") returnedUnknown += 1;
    return association;
  });
  const pageHasMore = Boolean(page.next_cursor) || page.truncated === true;
  if ((firstPage && associations.length > 0 &&
      associations[0]?.association_event_sequence !== value.latest_association_event_sequence) ||
    value.associations_truncated !== pageHasMore ||
    (value.associations_truncated && page.truncated !== true &&
      associations.length !== page.limit) ||
    (page.truncated === true && associations.length > page.limit) ||
    (firstPage && value.associations_truncated !==
      (associations.length < Number(value.associated_evidence_count))) ||
    (firstPage && !value.associations_truncated &&
      (associations.length !== value.associated_evidence_count ||
      returnedPass !== value.pass_count || returnedFail !== value.fail_count ||
      returnedUnknown !== value.unknown_count)) ||
    returnedPass > value.pass_count || returnedFail > value.fail_count ||
    returnedUnknown > value.unknown_count) {
    throw new APIRequestError("Verification item coverage counts are inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, associations } as unknown as VerificationPlanItemCoverageDetailView;
}

async function parseVerificationSnapshotExport(value: unknown, runID: string, planID: string,
  ordinal: number, format: "json" | "markdown"): Promise<VerificationSnapshotExportView> {
  const keys = ["approval", "associated_evidence_count", "associations_truncated",
    "authority_granted", "command_executed", "content", "content_bytes", "content_sha256",
    "download_only", "execution_started", "fail_count", "filename", "format", "metadata_only",
    "mime_type", "model_assertion", "mutation_supported", "operator_identity_included",
    "pass_count", "plan_id", "plan_item_ordinal", "plan_item_sha256", "plan_sha256",
    "private_evidence_bodies_included", "private_plan_body_included", "protocol_version",
    "read_only", "record_rewritten", "result_inferred", "returned_association_count", "run_id",
    "session_id", "snapshot_high_water_event_sequence", "snapshot_protocol_version",
    "unknown_count", "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_item_snapshot_export.v1" ||
    value.snapshot_protocol_version !== "operator_verification_plan_item_snapshot.v1" ||
    value.run_id !== runID || value.plan_id !== planID || value.plan_item_ordinal !== ordinal ||
    value.format !== format || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !isSHA256(value.plan_sha256) ||
    !isSHA256(value.plan_item_sha256) ||
    !safeBoundedCount(value.associated_evidence_count, 1_000_000_000) ||
    !safeBoundedCount(value.pass_count, 1_000_000_000) ||
    !safeBoundedCount(value.fail_count, 1_000_000_000) ||
    !safeBoundedCount(value.unknown_count, 1_000_000_000) ||
    value.pass_count + value.fail_count + value.unknown_count !==
      value.associated_evidence_count ||
    !safeBoundedCount(value.snapshot_high_water_event_sequence, Number.MAX_SAFE_INTEGER) ||
    ((value.associated_evidence_count === 0) !==
      (value.snapshot_high_water_event_sequence === 0)) ||
    !safeBoundedCount(value.returned_association_count, 100) ||
    value.returned_association_count !== Math.min(Number(value.associated_evidence_count), 100) ||
    value.associations_truncated !== (value.associated_evidence_count > 100) ||
    typeof value.filename !== "string" || value.filename.length < 1 || value.filename.length > 255 ||
    /[\\/:*?"<>|\u0000-\u001f]/u.test(value.filename) || typeof value.mime_type !== "string" ||
    typeof value.content !== "string" || !safePositiveInteger(value.content_bytes) ||
    value.content_bytes > 256 * 1024 || !isSHA256(value.content_sha256) ||
    value.metadata_only !== true || value.read_only !== true || value.download_only !== true ||
    value.private_plan_body_included !== false ||
    value.private_evidence_bodies_included !== false ||
    value.operator_identity_included !== false || value.result_inferred !== false ||
    value.command_executed !== false || value.model_assertion !== false ||
    value.record_rewritten !== false || value.approval !== false ||
    value.authority_granted !== false || value.mutation_supported !== false ||
    value.execution_started !== false) {
    throw new APIRequestError("Verification snapshot export widened its read-only boundary",
      "INVALID_RESPONSE", 502);
  }
  const expectedMIME = format === "json" ? "application/json" : "text/markdown; charset=utf-8";
  const expectedSuffix = format === "json" ? ".json" : ".md";
  const encoded = new TextEncoder().encode(value.content);
  if (value.mime_type !== expectedMIME || !value.filename.endsWith(expectedSuffix) ||
    encoded.length !== value.content_bytes) {
    throw new APIRequestError("Verification snapshot metadata does not match its content",
      "INVALID_RESPONSE", 502);
  }
  const digest = new Uint8Array(await globalThis.crypto.subtle.digest("SHA-256", encoded));
  const digestHex = [...digest].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  if (digestHex !== value.content_sha256) {
    throw new APIRequestError("Verification snapshot digest verification failed",
      "INVALID_RESPONSE", 502);
  }
  if (format === "json") {
    let document: unknown;
    try {
      document = JSON.parse(value.content);
    } catch {
      throw new APIRequestError("Verification snapshot JSON is invalid", "INVALID_RESPONSE", 502);
    }
    const documentKeys = ["approval", "associated_evidence_count", "associations",
      "associations_truncated", "authority_granted", "command_executed", "execution_started",
      "fail_count", "metadata_only", "model_assertion", "mutation_supported",
      "operator_identity_included", "pass_count", "plan_id", "plan_item_ordinal",
      "plan_item_sha256", "plan_sha256", "private_evidence_bodies_included",
      "private_plan_body_included", "protocol_version", "read_only", "record_rewritten",
      "result_inferred", "returned_association_count", "run_id", "session_id",
      "snapshot_high_water_event_sequence", "unknown_count", "workspace_id"];
    if (!hasExactKeys(document, documentKeys) ||
      document.protocol_version !== "operator_verification_plan_item_snapshot.v1" ||
      document.run_id !== runID || document.session_id !== value.session_id ||
      document.workspace_id !== value.workspace_id || document.plan_id !== planID ||
      document.plan_sha256 !== value.plan_sha256 || document.plan_item_ordinal !== ordinal ||
      document.plan_item_sha256 !== value.plan_item_sha256 ||
      document.snapshot_high_water_event_sequence !== value.snapshot_high_water_event_sequence ||
      document.associated_evidence_count !== value.associated_evidence_count ||
      document.pass_count !== value.pass_count || document.fail_count !== value.fail_count ||
      document.unknown_count !== value.unknown_count ||
      document.returned_association_count !== value.returned_association_count ||
      document.associations_truncated !== value.associations_truncated ||
      !Array.isArray(document.associations) ||
      document.associations.length !== value.returned_association_count ||
      document.metadata_only !== true || document.read_only !== true ||
      document.private_plan_body_included !== false ||
      document.private_evidence_bodies_included !== false ||
      document.operator_identity_included !== false || document.result_inferred !== false ||
      document.command_executed !== false || document.model_assertion !== false ||
      document.record_rewritten !== false || document.approval !== false ||
      document.authority_granted !== false || document.mutation_supported !== false ||
      document.execution_started !== false) {
      throw new APIRequestError("Verification snapshot JSON escaped its exact source binding",
        "INVALID_RESPONSE", 502);
    }
    const associationIDs = new Set<string>();
    const evidenceIDs = new Set<string>();
    let previousSequence = Number.MAX_SAFE_INTEGER;
    let returnedPass = 0;
    let returnedFail = 0;
    let returnedUnknown = 0;
    for (const [index, association] of document.associations.entries()) {
      if (!hasExactKeys(association, ["associated_at", "association_event_sequence",
        "evidence_event_sequence", "evidence_id", "evidence_outcome", "id", "plan_id",
        "plan_item_ordinal", "plan_item_sha256"]) || !boundedIdentity(association.id) ||
        associationIDs.has(String(association.id)) || !boundedIdentity(association.evidence_id) ||
        evidenceIDs.has(String(association.evidence_id)) || association.plan_id !== planID ||
        association.plan_item_ordinal !== ordinal ||
        association.plan_item_sha256 !== value.plan_item_sha256 ||
        !["pass", "fail", "unknown"].includes(String(association.evidence_outcome)) ||
        !safePositiveInteger(association.evidence_event_sequence) ||
        !safePositiveInteger(association.association_event_sequence) ||
        association.association_event_sequence <= association.evidence_event_sequence ||
        association.association_event_sequence > value.snapshot_high_water_event_sequence ||
        (index > 0 && association.association_event_sequence >= previousSequence) ||
        (index === 0 && association.association_event_sequence !==
          value.snapshot_high_water_event_sequence) || !validDate(association.associated_at)) {
        throw new APIRequestError("Verification snapshot association escaped its exact binding",
          "INVALID_RESPONSE", 502);
      }
      associationIDs.add(String(association.id));
      evidenceIDs.add(String(association.evidence_id));
      previousSequence = Number(association.association_event_sequence);
      if (association.evidence_outcome === "pass") returnedPass += 1;
      if (association.evidence_outcome === "fail") returnedFail += 1;
      if (association.evidence_outcome === "unknown") returnedUnknown += 1;
    }
    if (returnedPass > value.pass_count || returnedFail > value.fail_count ||
      returnedUnknown > value.unknown_count || (!value.associations_truncated &&
        (returnedPass !== value.pass_count || returnedFail !== value.fail_count ||
          returnedUnknown !== value.unknown_count))) {
      throw new APIRequestError("Verification snapshot outcome counts are inconsistent",
        "INVALID_RESPONSE", 502);
    }
  } else if (!value.content.startsWith("# CyberAgent Verification Snapshot\n") ||
    !value.content.includes(`Run: \`${runID}\``) ||
    !value.content.includes(`Plan: \`${planID}\``) ||
    !value.content.includes(`Item: \`${ordinal}\``) ||
    !value.content.includes(`Snapshot event high-water: \`${value.snapshot_high_water_event_sequence}\``) ||
    !value.content.includes("This is a read-only metadata snapshot.")) {
    throw new APIRequestError("Verification snapshot Markdown omitted its exact source binding",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as VerificationSnapshotExportView;
}

function parseVerificationSnapshotReceipt(value: unknown, runID: string,
  sessionID = "", workspaceID = "", control = false): VerificationSnapshotReceiptControlView {
  const keys = ["approval", "associated_evidence_count", "associations_truncated",
    "authority_granted", "content_bytes", "content_included", "content_sha256",
    "execution_started", "fail_count", "format", "id", "immutable", "metadata_only",
    "operator_identity_included", "operator_recorded", "pass_count", "plan_id",
    "plan_item_ordinal", "plan_item_sha256", "plan_sha256", "private_bodies_included",
    "protocol_version", "read_only", "receipt_event_sequence", "record_rewritten",
    "recorded_at", "result_accepted", "result_inferred", "returned_association_count",
    "run_id", "session_id", "snapshot_accepted", "snapshot_high_water_event_sequence",
    "unknown_count", "workspace_id"];
  if (control) keys.push("replayed");
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_item_snapshot_receipt.v1" ||
    value.run_id !== runID || !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || (sessionID !== "" && value.session_id !== sessionID) ||
    (workspaceID !== "" && value.workspace_id !== workspaceID) ||
    !boundedIdentity(value.plan_id) || !isSHA256(value.plan_sha256) ||
    !safePositiveInteger(value.plan_item_ordinal) || value.plan_item_ordinal > 32 ||
    !isSHA256(value.plan_item_sha256) || !["json", "markdown"].includes(String(value.format)) ||
    !safeBoundedCount(value.snapshot_high_water_event_sequence, Number.MAX_SAFE_INTEGER) ||
    !safeBoundedCount(value.associated_evidence_count, 1_000_000_000) ||
    !safeBoundedCount(value.pass_count, 1_000_000_000) ||
    !safeBoundedCount(value.fail_count, 1_000_000_000) ||
    !safeBoundedCount(value.unknown_count, 1_000_000_000) ||
    value.pass_count + value.fail_count + value.unknown_count !==
      value.associated_evidence_count ||
    (value.associated_evidence_count === 0) !==
      (value.snapshot_high_water_event_sequence === 0) ||
    !safeBoundedCount(value.returned_association_count, 100) ||
    value.returned_association_count !== Math.min(Number(value.associated_evidence_count), 100) ||
    value.associations_truncated !== (value.associated_evidence_count > 100) ||
    !isSHA256(value.content_sha256) || !safePositiveInteger(value.content_bytes) ||
    value.content_bytes > 256 * 1024 || !safePositiveInteger(value.receipt_event_sequence) ||
    value.receipt_event_sequence <= value.snapshot_high_water_event_sequence ||
    !validDate(value.recorded_at) || value.immutable !== true ||
    value.operator_recorded !== true || value.metadata_only !== true || value.read_only !== true ||
    value.content_included !== false || value.private_bodies_included !== false ||
    value.operator_identity_included !== false || value.snapshot_accepted !== false ||
    value.result_accepted !== false || value.result_inferred !== false ||
    value.record_rewritten !== false || value.approval !== false ||
    value.authority_granted !== false || value.execution_started !== false ||
    (control && typeof value.replayed !== "boolean")) {
    throw new APIRequestError("Verification snapshot receipt widened acceptance or authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as VerificationSnapshotReceiptControlView;
}

function parseVerificationSnapshotReceiptInventory(value: unknown,
  runID: string): VerificationSnapshotReceiptInventoryView {
  const keys = ["approval", "authority_granted", "execution_started", "items",
    "metadata_only", "protocol_version", "read_only", "record_rewritten", "result_accepted",
    "result_inferred", "run_id", "session_id", "snapshot_accepted", "truncated",
    "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_item_snapshot_receipt_inventory.v1" ||
    value.run_id !== runID || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !Array.isArray(value.items) ||
    value.items.length > 100 || typeof value.truncated !== "boolean" ||
    value.metadata_only !== true || value.read_only !== true ||
    value.snapshot_accepted !== false || value.result_accepted !== false ||
    value.result_inferred !== false || value.record_rewritten !== false ||
    value.approval !== false || value.authority_granted !== false ||
    value.execution_started !== false) {
    throw new APIRequestError("Verification snapshot receipt history widened its read-only boundary",
      "INVALID_RESPONSE", 502);
  }
  const identities = new Set<string>();
  let previousSequence = Number.MAX_SAFE_INTEGER;
  const items = value.items.map((item) => {
    const parsed = parseVerificationSnapshotReceipt(item, runID, String(value.session_id),
      String(value.workspace_id));
    if (identities.has(parsed.id) || parsed.receipt_event_sequence >= previousSequence) {
      throw new APIRequestError("Verification snapshot receipt history is duplicated or unordered",
        "INVALID_RESPONSE", 502);
    }
    identities.add(parsed.id);
    previousSequence = parsed.receipt_event_sequence;
    return parsed as unknown as VerificationSnapshotReceiptView;
  });
  if (value.truncated && items.length !== 100) {
    throw new APIRequestError("Verification snapshot receipt history truncation is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as VerificationSnapshotReceiptInventoryView;
}

function parseVerificationSnapshotReceiptReview(value: unknown, runID: string,
  sessionID = "", workspaceID = "", control = false): VerificationSnapshotReceiptReviewControlView {
  const keys = ["approval", "authority_granted", "content_included", "decision",
    "execution_started", "id", "immutable", "metadata_only", "operator_identity_included",
    "operator_reviewed", "private_bodies_included", "protocol_version", "read_only",
    "receipt_content_sha256", "receipt_event_sequence", "receipt_id", "record_rewritten",
    "result_accepted", "result_inferred", "review_event_sequence", "review_non_authorizing",
    "reviewed_at", "run_id", "session_id", "snapshot_accepted", "workspace_id"];
  if (control) keys.push("replayed");
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !== "operator_verification_plan_item_snapshot_receipt_review.v1" ||
    value.run_id !== runID || !boundedIdentity(value.id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !boundedIdentity(value.receipt_id) ||
    (sessionID !== "" && value.session_id !== sessionID) ||
    (workspaceID !== "" && value.workspace_id !== workspaceID) ||
    !isSHA256(value.receipt_content_sha256) ||
    !safePositiveInteger(value.receipt_event_sequence) ||
    !safePositiveInteger(value.review_event_sequence) ||
    value.review_event_sequence <= value.receipt_event_sequence ||
    !["metadata_confirmed", "metadata_disputed"].includes(String(value.decision)) ||
    !validDate(value.reviewed_at) || value.immutable !== true ||
    value.operator_reviewed !== true || value.metadata_only !== true || value.read_only !== true ||
    value.review_non_authorizing !== true || value.content_included !== false ||
    value.private_bodies_included !== false || value.operator_identity_included !== false ||
    value.snapshot_accepted !== false || value.result_accepted !== false ||
    value.result_inferred !== false || value.record_rewritten !== false ||
    value.approval !== false || value.authority_granted !== false ||
    value.execution_started !== false || (control && typeof value.replayed !== "boolean")) {
    throw new APIRequestError("Verification snapshot receipt review widened acceptance or authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as VerificationSnapshotReceiptReviewControlView;
}

function parseVerificationSnapshotReceiptReviewInventory(value: unknown,
  runID: string): VerificationSnapshotReceiptReviewInventoryView {
  const keys = ["approval", "authority_granted", "execution_started", "items",
    "metadata_only", "protocol_version", "read_only", "record_rewritten",
    "result_accepted", "result_inferred", "review_non_authorizing", "run_id", "session_id",
    "snapshot_accepted", "truncated", "workspace_id"];
  if (!hasExactKeys(value, keys) ||
    value.protocol_version !==
      "operator_verification_plan_item_snapshot_receipt_review_inventory.v1" ||
    value.run_id !== runID || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !Array.isArray(value.items) ||
    value.items.length > 100 || typeof value.truncated !== "boolean" ||
    value.metadata_only !== true || value.read_only !== true ||
    value.review_non_authorizing !== true || value.snapshot_accepted !== false ||
    value.result_accepted !== false || value.result_inferred !== false ||
    value.record_rewritten !== false || value.approval !== false ||
    value.authority_granted !== false || value.execution_started !== false) {
    throw new APIRequestError(
      "Verification snapshot receipt review history widened its non-authorizing boundary",
      "INVALID_RESPONSE", 502);
  }
  const identities = new Set<string>();
  const receiptIDs = new Set<string>();
  let previousSequence = Number.MAX_SAFE_INTEGER;
  const items = value.items.map((item) => {
    const parsed = parseVerificationSnapshotReceiptReview(item, runID, String(value.session_id),
      String(value.workspace_id));
    if (identities.has(parsed.id) || receiptIDs.has(parsed.receipt_id) ||
      parsed.review_event_sequence >= previousSequence) {
      throw new APIRequestError(
        "Verification snapshot receipt review history is duplicated or unordered",
        "INVALID_RESPONSE", 502);
    }
    identities.add(parsed.id);
    receiptIDs.add(parsed.receipt_id);
    previousSequence = parsed.review_event_sequence;
    return parsed as unknown as VerificationSnapshotReceiptReviewView;
  });
  if (value.truncated && items.length !== 100) {
    throw new APIRequestError("Verification snapshot receipt review truncation is inconsistent",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, items } as unknown as VerificationSnapshotReceiptReviewInventoryView;
}

function parseCodeHandoff(value: unknown, runID: string): CodeHandoffView {
  const keys = ["change_set", "composite_mutation", "durable_sources", "execution_started",
    "generated_at", "mission_id", "mode_revision", "pending_action_count", "pending_actions",
    "pending_actions_truncated", "phase", "plan", "private_bodies_included", "protocol_version",
    "queue", "regenerable", "report_references", "report_references_truncated",
    "resume_authorized", "run_id", "run_status", "session_id", "source_event_sequence",
    "surface", "verification", "verification_coverage", "verification_plans",
    "verification_snapshot_receipt_reviews", "workspace_id"];
  const hasStandardCodeDelivery = isRecord(value) &&
    Object.prototype.hasOwnProperty.call(value, "standard_code_delivery");
  if (hasStandardCodeDelivery) keys.push("standard_code_delivery");
  if (!hasExactKeys(value, keys) || value.protocol_version !== "code_handoff.v1" ||
    value.run_id !== runID || !boundedIdentity(value.mission_id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || value.surface !== "code" ||
    !["plan", "deliver"].includes(String(value.phase)) ||
    !["created", "preparing", "running", "waiting_approval", "paused", "completed", "failed",
      "cancelled"].includes(String(value.run_status)) || !safePositiveInteger(value.mode_revision) ||
    !safePositiveInteger(value.source_event_sequence) || !validDate(value.generated_at) ||
    value.regenerable !== true || value.durable_sources !== true ||
    value.private_bodies_included !== false || value.composite_mutation !== false ||
    value.resume_authorized !== false || value.execution_started !== false ||
    !isRecord(value.plan) || !isRecord(value.queue) || !isRecord(value.change_set) ||
    !isRecord(value.verification) || !isRecord(value.verification_plans) ||
    !isRecord(value.verification_coverage) ||
    !isRecord(value.verification_snapshot_receipt_reviews) ||
    !Array.isArray(value.pending_actions) ||
    value.pending_actions.length > 20 || !Array.isArray(value.report_references) ||
    value.report_references.length > 20 || !safeBoundedCount(value.pending_action_count, 100) ||
    typeof value.pending_actions_truncated !== "boolean" ||
    typeof value.report_references_truncated !== "boolean") {
    throw new APIRequestError("Code handoff violated its metadata-only boundary",
      "INVALID_RESPONSE", 502);
  }
  const planKeys = ["blocked_count", "cancelled_count", "completed_count", "direction_count",
    "in_progress_count", "module_count", "pending_count", "proposal_id", "selected_direction",
    "selection_id", "state"];
  const plan = value.plan;
  const planCounts = [plan.pending_count, plan.in_progress_count, plan.blocked_count,
    plan.completed_count, plan.cancelled_count];
  if (!hasExactKeys(plan, planKeys) || !["none", "proposed", "selected"].includes(String(plan.state)) ||
    typeof plan.proposal_id !== "string" || typeof plan.selection_id !== "string" ||
    !safeBoundedCount(plan.direction_count, 3) || !safeBoundedCount(plan.selected_direction, 3) ||
    !safeBoundedCount(plan.module_count, 8) ||
    planCounts.some((count) => !safeBoundedCount(count, 8)) ||
    planCounts.reduce<number>((total, count) => total + Number(count), 0) !== plan.module_count ||
    (plan.state === "none" && (plan.proposal_id !== "" || plan.selection_id !== "" ||
      plan.direction_count !== 0 || plan.selected_direction !== 0 || plan.module_count !== 0)) ||
    (plan.state === "proposed" && (!boundedIdentity(plan.proposal_id) || plan.selection_id !== "" ||
      plan.direction_count !== 3 || plan.selected_direction !== 0 || plan.module_count !== 0)) ||
    (plan.state === "selected" && (!boundedIdentity(plan.proposal_id) ||
      !boundedIdentity(plan.selection_id) || plan.direction_count !== 3 ||
      plan.selected_direction < 1))) {
    throw new APIRequestError("Code handoff Plan summary is inconsistent", "INVALID_RESPONSE", 502);
  }
  const queue = value.queue;
  if (!hasExactKeys(queue, ["cancelled", "committed", "pending", "prepared"]) ||
    [queue.pending, queue.prepared, queue.committed, queue.cancelled]
      .some((count) => !safeBoundedCount(count, Number.MAX_SAFE_INTEGER))) {
    throw new APIRequestError("Code handoff queue summary is invalid", "INVALID_RESPONSE", 502);
  }
  const changeSet = value.change_set;
  const changeCounts = [changeSet.proposed, changeSet.approved, changeSet.applied,
    changeSet.denied, changeSet.failed];
  if (!hasExactKeys(changeSet, ["applied", "approved", "denied", "failed", "proposed",
    "returned_count", "total_diff_bytes", "truncated"]) ||
    changeCounts.some((count) => !safeBoundedCount(count, 100)) ||
    !safeBoundedCount(changeSet.returned_count, 100) ||
    changeCounts.reduce<number>((total, count) => total + Number(count), 0) !==
      changeSet.returned_count ||
    !safeBoundedCount(changeSet.total_diff_bytes, 110 * 1024 * 1024) ||
    typeof changeSet.truncated !== "boolean") {
    throw new APIRequestError("Code handoff change-set summary is invalid", "INVALID_RESPONSE", 502);
  }
  const verification = value.verification;
  if (!hasExactKeys(verification, ["fail_count", "pass_count", "references", "returned_count",
    "truncated", "unknown_count"]) || !Array.isArray(verification.references) ||
    verification.references.length > 20 || !safeBoundedCount(verification.returned_count, 100) ||
    !safeBoundedCount(verification.pass_count, 100) || !safeBoundedCount(verification.fail_count, 100) ||
    !safeBoundedCount(verification.unknown_count, 100) ||
    verification.pass_count + verification.fail_count + verification.unknown_count !==
      verification.returned_count || typeof verification.truncated !== "boolean" ||
    verification.references.length !== Math.min(Number(verification.returned_count), 20) ||
    verification.truncated !== (verification.returned_count > 20)) {
    throw new APIRequestError("Code handoff verification summary is invalid", "INVALID_RESPONSE", 502);
  }
  const verificationIDs = new Set<string>();
  for (const reference of verification.references) {
    if (!hasExactKeys(reference, ["id", "outcome", "recorded_at", "redacted"]) ||
      !boundedIdentity(reference.id) || verificationIDs.has(String(reference.id)) ||
      !["pass", "fail", "unknown"].includes(String(reference.outcome)) ||
      typeof reference.redacted !== "boolean" || !validDate(reference.recorded_at)) {
      throw new APIRequestError("Code handoff verification reference is invalid",
        "INVALID_RESPONSE", 502);
    }
    verificationIDs.add(String(reference.id));
  }
  const verificationPlans = value.verification_plans;
  if (!hasExactKeys(verificationPlans, ["references", "returned_count", "truncated"]) ||
    !Array.isArray(verificationPlans.references) || verificationPlans.references.length > 20 ||
    !safeBoundedCount(verificationPlans.returned_count, 50) ||
    typeof verificationPlans.truncated !== "boolean" ||
    verificationPlans.references.length !== Math.min(Number(verificationPlans.returned_count), 20) ||
    verificationPlans.truncated !== (verificationPlans.returned_count > 20)) {
    throw new APIRequestError("Code handoff verification plan summary is invalid",
      "INVALID_RESPONSE", 502);
  }
  const verificationPlanIDs = new Set<string>();
  for (const reference of verificationPlans.references) {
    if (!hasExactKeys(reference, ["created_at", "id", "item_count", "plan_sha256", "redacted"]) ||
      !boundedIdentity(reference.id) || verificationPlanIDs.has(String(reference.id)) ||
      !isSHA256(reference.plan_sha256) || !safeBoundedCount(reference.item_count, 32) ||
      reference.item_count < 1 || typeof reference.redacted !== "boolean" ||
      !validDate(reference.created_at)) {
      throw new APIRequestError("Code handoff verification plan reference is invalid",
        "INVALID_RESPONSE", 502);
    }
    verificationPlanIDs.add(String(reference.id));
  }
  const coverage = value.verification_coverage;
  const coverageKeys = ["associated_evidence_count", "contradictory_item_count", "items",
    "metadata_only", "observed_plan_item_count", "plan_count", "plan_item_count",
    "private_bodies_included", "protocol_version", "read_only", "result_inferred",
    "returned_item_count", "truncated", "unobserved_plan_item_count"];
  if (!hasExactKeys(coverage, coverageKeys) ||
    coverage.protocol_version !== "operator_verification_plan_coverage.v1" ||
    !safeBoundedCount(coverage.plan_count, 50) ||
    !safeBoundedCount(coverage.plan_item_count, 50 * 32) ||
    !safeBoundedCount(coverage.observed_plan_item_count, 50 * 32) ||
    !safeBoundedCount(coverage.unobserved_plan_item_count, 50 * 32) ||
    coverage.observed_plan_item_count + coverage.unobserved_plan_item_count !==
      coverage.plan_item_count ||
    !safeBoundedCount(coverage.associated_evidence_count, 1_000_000_000) ||
    !safeBoundedCount(coverage.contradictory_item_count, 50 * 32) ||
    coverage.contradictory_item_count > coverage.observed_plan_item_count ||
    !safeBoundedCount(coverage.returned_item_count, 100) || !Array.isArray(coverage.items) ||
    coverage.items.length !== coverage.returned_item_count || coverage.items.length > 100 ||
    coverage.returned_item_count !== Math.min(Number(coverage.plan_item_count), 100) ||
    typeof coverage.truncated !== "boolean" ||
    (coverage.plan_item_count > 100 && !coverage.truncated) ||
    coverage.metadata_only !== true || coverage.read_only !== true ||
    coverage.result_inferred !== false || coverage.private_bodies_included !== false) {
    throw new APIRequestError("Code handoff verification coverage widened result authority",
      "INVALID_RESPONSE", 502);
  }
  const coverageItems = new Set<string>();
  let returnedObserved = 0;
  let returnedEvidence = 0;
  let returnedContradictory = 0;
  for (const item of coverage.items) {
    if (!hasExactKeys(item, ["associated_evidence_count", "fail_count", "item_sha256",
      "latest_association_event_sequence", "ordinal", "pass_count", "plan_id", "plan_sha256",
      "unknown_count"]) || !boundedIdentity(item.plan_id) || !isSHA256(item.plan_sha256) ||
      !safePositiveInteger(item.ordinal) || item.ordinal > 32 || !isSHA256(item.item_sha256) ||
      !safeBoundedCount(item.associated_evidence_count, 1_000_000_000) ||
      !safeBoundedCount(item.pass_count, 1_000_000_000) ||
      !safeBoundedCount(item.fail_count, 1_000_000_000) ||
      !safeBoundedCount(item.unknown_count, 1_000_000_000) ||
      item.pass_count + item.fail_count + item.unknown_count !== item.associated_evidence_count ||
      !safeBoundedCount(item.latest_association_event_sequence, Number.MAX_SAFE_INTEGER) ||
      (item.associated_evidence_count === 0) !==
        (item.latest_association_event_sequence === 0)) {
      throw new APIRequestError("Code handoff verification coverage item is inconsistent",
        "INVALID_RESPONSE", 502);
    }
    const key = `${String(item.plan_id)}:${String(item.ordinal)}`;
    if (coverageItems.has(key)) {
      throw new APIRequestError("Code handoff verification coverage duplicated a plan item",
        "INVALID_RESPONSE", 502);
    }
    coverageItems.add(key);
    returnedObserved += item.associated_evidence_count > 0 ? 1 : 0;
    returnedEvidence += Number(item.associated_evidence_count);
    returnedContradictory += item.pass_count > 0 && item.fail_count > 0 ? 1 : 0;
  }
  if (returnedObserved > coverage.observed_plan_item_count ||
    returnedEvidence > coverage.associated_evidence_count ||
    returnedContradictory > coverage.contradictory_item_count ||
    (!coverage.truncated && (returnedObserved !== coverage.observed_plan_item_count ||
      returnedEvidence !== coverage.associated_evidence_count ||
      returnedContradictory !== coverage.contradictory_item_count))) {
    throw new APIRequestError("Code handoff verification coverage totals are inconsistent",
      "INVALID_RESPONSE", 502);
  }
  const reviews = value.verification_snapshot_receipt_reviews;
  const reviewKeys = ["approval", "authority_granted", "content_included", "execution_started",
    "metadata_confirmed_count", "metadata_disputed_count", "metadata_only",
    "operator_identity_included", "private_bodies_included", "protocol_version", "read_only",
    "record_rewritten", "references", "result_accepted", "result_inferred",
    "returned_count", "review_non_authorizing", "snapshot_accepted", "truncated"];
  if (!hasExactKeys(reviews, reviewKeys) || reviews.protocol_version !==
      "operator_verification_plan_item_snapshot_receipt_review_inventory.v1" ||
    !safeBoundedCount(reviews.metadata_confirmed_count, 100) ||
    !safeBoundedCount(reviews.metadata_disputed_count, 100) ||
    !safeBoundedCount(reviews.returned_count, 100) ||
    reviews.metadata_confirmed_count + reviews.metadata_disputed_count !== reviews.returned_count ||
    !Array.isArray(reviews.references) || reviews.references.length > 20 ||
    reviews.references.length !== Math.min(reviews.returned_count, 20) ||
    reviews.truncated !== (reviews.returned_count > 20) || reviews.metadata_only !== true ||
    reviews.read_only !== true || reviews.review_non_authorizing !== true ||
    reviews.content_included !== false || reviews.private_bodies_included !== false ||
    reviews.operator_identity_included !== false || reviews.snapshot_accepted !== false ||
    reviews.result_accepted !== false || reviews.result_inferred !== false ||
    reviews.record_rewritten !== false || reviews.approval !== false ||
    reviews.authority_granted !== false || reviews.execution_started !== false) {
    throw new APIRequestError("Code handoff receipt reviews widened authority",
      "INVALID_RESPONSE", 502);
  }
  const reviewIDs = new Set<string>();
  const reviewedReceiptIDs = new Set<string>();
  let previousReviewSequence: number | null = null;
  let returnedConfirmed = 0;
  let returnedDisputed = 0;
  for (const review of reviews.references) {
    if (!hasExactKeys(review, ["decision", "id", "receipt_content_sha256",
      "receipt_event_sequence", "receipt_id", "review_event_sequence", "reviewed_at"]) ||
      !boundedIdentity(review.id) || reviewIDs.has(String(review.id)) ||
      !boundedIdentity(review.receipt_id) || reviewedReceiptIDs.has(String(review.receipt_id)) ||
      !isSHA256(review.receipt_content_sha256) ||
      !safePositiveInteger(review.receipt_event_sequence) ||
      !safePositiveInteger(review.review_event_sequence) ||
      review.review_event_sequence <= review.receipt_event_sequence ||
      review.review_event_sequence > value.source_event_sequence ||
      (previousReviewSequence !== null && review.review_event_sequence >= previousReviewSequence) ||
      !["metadata_confirmed", "metadata_disputed"].includes(String(review.decision)) ||
      !validDate(review.reviewed_at)) {
      throw new APIRequestError("Code handoff receipt review reference is invalid",
        "INVALID_RESPONSE", 502);
    }
    reviewIDs.add(String(review.id));
    reviewedReceiptIDs.add(String(review.receipt_id));
    previousReviewSequence = review.review_event_sequence;
    returnedConfirmed += review.decision === "metadata_confirmed" ? 1 : 0;
    returnedDisputed += review.decision === "metadata_disputed" ? 1 : 0;
  }
  if (returnedConfirmed > reviews.metadata_confirmed_count ||
    returnedDisputed > reviews.metadata_disputed_count ||
    (!reviews.truncated && (returnedConfirmed !== reviews.metadata_confirmed_count ||
      returnedDisputed !== reviews.metadata_disputed_count))) {
    throw new APIRequestError("Code handoff receipt review totals are inconsistent",
      "INVALID_RESPONSE", 502);
  }
  const actionMapping = {
    steering_pending: ["pending", "queue"], approval_pending: ["pending", "approvals"],
    file_edit_review: ["proposed", "diffs"], file_edit_apply: ["approved", "diffs"],
    wake_due: ["queued", "wake"],
  } as const;
  if (value.pending_actions.length !== Math.min(Number(value.pending_action_count), 20) ||
    value.pending_actions_truncated !== (value.pending_action_count > 20) ||
    (value.report_references_truncated && value.report_references.length !== 20)) {
    throw new APIRequestError("Code handoff reference summary is invalid", "INVALID_RESPONSE", 502);
  }
  const actionIDs = new Set<string>();
  for (const action of value.pending_actions) {
    const hasDueAt = Object.prototype.hasOwnProperty.call(action, "due_at");
    if (!hasExactKeys(action, hasDueAt ?
      ["available_at", "destination", "due_at", "id", "kind", "state"] :
      ["available_at", "destination", "id", "kind", "state"]) ||
      !boundedIdentity(action.id) || !String(action.id).startsWith("action-") ||
      actionIDs.has(String(action.id)) ||
      !Object.prototype.hasOwnProperty.call(actionMapping, String(action.kind)) ||
      !validDate(action.available_at)) {
      throw new APIRequestError("Code handoff action reference is invalid",
        "INVALID_RESPONSE", 502);
    }
    const expected = actionMapping[action.kind as keyof typeof actionMapping];
    if (action.state !== expected[0] || action.destination !== expected[1] ||
      (action.kind === "wake_due" ? !validDate(action.due_at) : hasDueAt)) {
      throw new APIRequestError("Code handoff action reference widened navigation",
        "INVALID_RESPONSE", 502);
    }
    actionIDs.add(String(action.id));
  }
  const reportIDs = new Set<string>();
  for (const report of value.report_references) {
    if (!hasExactKeys(report, ["created_at", "finding_count", "id", "status"]) ||
      !boundedIdentity(report.id) || reportIDs.has(String(report.id)) ||
      report.status !== "generated" || !safeBoundedCount(report.finding_count, 10_000) ||
      !validDate(report.created_at)) {
      throw new APIRequestError("Code handoff report reference is invalid",
        "INVALID_RESPONSE", 502);
    }
    reportIDs.add(String(report.id));
  }
  if (hasStandardCodeDelivery) {
    return { ...value,
      standard_code_delivery: parseStandardCodeDelivery(value.standard_code_delivery, runID),
    } as unknown as CodeHandoffView;
  }
  return value as unknown as CodeHandoffView;
}

async function parseCodeHandoffExport(value: unknown, runID: string,
  format: "json" | "markdown"): Promise<CodeHandoffExportView> {
  const keys = ["content", "content_bytes", "content_sha256", "download_only",
    "execution_started", "filename", "format", "generated_at", "mime_type",
    "mutation_supported", "private_bodies", "protocol_version", "read_only",
    "report_acceptance", "resume_authorized", "run_id", "source_event_sequence"];
  if (!hasExactKeys(value, keys) || value.protocol_version !== "code_handoff_export.v1" ||
    value.run_id !== runID || value.format !== format || !validDate(value.generated_at) ||
    !safePositiveInteger(value.source_event_sequence) || typeof value.filename !== "string" ||
    value.filename.length < 1 || value.filename.length > 255 || /[\\/:*?"<>|\u0000-\u001f]/u.test(value.filename) ||
    typeof value.mime_type !== "string" || typeof value.content !== "string" ||
    !safePositiveInteger(value.content_bytes) || value.content_bytes > 256 * 1024 ||
    !isSHA256(value.content_sha256) || value.read_only !== true || value.download_only !== true ||
    value.private_bodies !== false || value.resume_authorized !== false ||
    value.mutation_supported !== false || value.report_acceptance !== false ||
    value.execution_started !== false) {
    throw new APIRequestError("Code handoff export violated its download-only boundary",
      "INVALID_RESPONSE", 502);
  }
  const expectedMIME = format === "json" ? "application/json" : "text/markdown; charset=utf-8";
  const expectedSuffix = format === "json" ? ".json" : ".md";
  const encoded = new TextEncoder().encode(value.content);
  if (value.mime_type !== expectedMIME || !value.filename.endsWith(expectedSuffix) ||
    encoded.length !== value.content_bytes) {
    throw new APIRequestError("Code handoff export metadata does not match its content",
      "INVALID_RESPONSE", 502);
  }
  const digest = new Uint8Array(await globalThis.crypto.subtle.digest("SHA-256", encoded));
  const digestHex = [...digest].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  if (digestHex !== value.content_sha256) {
    throw new APIRequestError("Code handoff export digest verification failed",
      "INVALID_RESPONSE", 502);
  }
  if (format === "json") {
    let document: unknown;
    try {
      document = JSON.parse(value.content);
    } catch {
      throw new APIRequestError("Code handoff JSON export is invalid", "INVALID_RESPONSE", 502);
    }
    if (!isRecord(document) || document.protocol_version !== "code_handoff.v1" ||
      document.run_id !== runID || document.source_event_sequence !== value.source_event_sequence ||
      !isRecord(document.verification_coverage) ||
      document.verification_coverage.protocol_version !==
        "operator_verification_plan_coverage.v1" ||
      document.verification_coverage.result_inferred !== false ||
      !isRecord(document.verification_snapshot_receipt_reviews) ||
      document.verification_snapshot_receipt_reviews.protocol_version !==
        "operator_verification_plan_item_snapshot_receipt_review_inventory.v1" ||
      document.verification_snapshot_receipt_reviews.metadata_only !== true ||
      document.verification_snapshot_receipt_reviews.read_only !== true ||
      document.verification_snapshot_receipt_reviews.review_non_authorizing !== true ||
      document.verification_snapshot_receipt_reviews.content_included !== false ||
      document.verification_snapshot_receipt_reviews.private_bodies_included !== false ||
      document.verification_snapshot_receipt_reviews.operator_identity_included !== false ||
      document.verification_snapshot_receipt_reviews.snapshot_accepted !== false ||
      document.verification_snapshot_receipt_reviews.result_accepted !== false ||
      document.verification_snapshot_receipt_reviews.result_inferred !== false ||
      document.verification_snapshot_receipt_reviews.record_rewritten !== false ||
      document.verification_snapshot_receipt_reviews.approval !== false ||
      document.verification_snapshot_receipt_reviews.authority_granted !== false ||
      document.verification_snapshot_receipt_reviews.execution_started !== false) {
      throw new APIRequestError("Code handoff JSON export escaped its source binding",
        "INVALID_RESPONSE", 502);
    }
  } else if (!value.content.startsWith("# CyberAgent Code Handoff\n") ||
    !value.content.includes(`Source event high-water: \`${value.source_event_sequence}\``) ||
    !value.content.includes("Coverage:") ||
    !value.content.includes("Receipt metadata reviews:")) {
    throw new APIRequestError("Code handoff Markdown export omitted its source binding",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as CodeHandoffExportView;
}

function parseEvidenceAttachment(value: unknown, runID: string,
  request: EvidenceAttachmentRequestView): EvidenceAttachmentView {
  if (!hasExactKeys(value, ["attachment_id", "capability_grant", "content_sha256",
    "execution_started", "instruction_authorized", "model_called", "protocol_version",
    "replayed", "run_id", "session_id", "session_message_id", "source_kind", "source_ref",
    "tool_called", "workspace_id"]) ||
    value.protocol_version !== "session_evidence_attachment.v1" || value.run_id !== runID ||
    value.source_kind !== "workspace_file" || value.source_kind !== request.source_kind ||
    value.source_ref !== request.source_ref || value.content_sha256 !== request.content_sha256 ||
    !boundedIdentity(value.attachment_id) || !boundedIdentity(value.session_id) ||
    !boundedIdentity(value.workspace_id) || !safePositiveInteger(value.session_message_id) ||
    value.instruction_authorized !== false || typeof value.replayed !== "boolean" ||
    value.execution_started !== false || value.model_called !== false ||
    value.tool_called !== false || value.capability_grant !== false) {
    throw new APIRequestError("Evidence attachment response widened document authority",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as EvidenceAttachmentView;
}

function parseEvidenceInventory(value: unknown, runID: string): EvidenceInventoryView {
  if (!hasExactKeys(value, ["items", "protocol_version", "run_id", "truncated"]) ||
    value.protocol_version !== "session_evidence_inventory.v1" || value.run_id !== runID ||
    !Array.isArray(value.items) || value.items.length > 100 ||
    typeof value.truncated !== "boolean") {
    throw new APIRequestError("Evidence inventory response violated its metadata-only contract",
      "INVALID_RESPONSE", 502);
  }
  const identities = new Set<string>();
  const items = value.items.map((item) => {
    if (!hasExactKeys(item, ["attached_at", "attachment_id", "content_sha256",
      "instruction_authorized", "run_id", "session_id", "source_kind", "source_ref",
      "workspace_id"]) || !boundedIdentity(item.attachment_id) ||
      identities.has(String(item.attachment_id)) || item.run_id !== runID ||
      !boundedIdentity(item.session_id) || !boundedIdentity(item.workspace_id) ||
      item.source_kind !== "workspace_file" || !validWorkspaceRelativePath(item.source_ref) ||
      !isSHA256(item.content_sha256) || item.instruction_authorized !== false ||
      !validDate(item.attached_at)) {
      throw new APIRequestError("Evidence inventory item widened document or renderer authority",
        "INVALID_RESPONSE", 502);
    }
    identities.add(String(item.attachment_id));
    return item;
  });
  return { ...value, items } as unknown as EvidenceInventoryView;
}

function parseOperatorActionCenter(value: unknown, runID: string): OperatorActionCenterView {
  if (!hasExactKeys(value, ["generated_at", "items", "protocol_version", "run_id",
    "truncated"]) || value.protocol_version !== "operator_action_center.v1" ||
    value.run_id !== runID || !validDate(value.generated_at) || !Array.isArray(value.items) ||
    value.items.length > 100 || typeof value.truncated !== "boolean") {
    throw new APIRequestError("Operator action center response is invalid", "INVALID_RESPONSE", 502);
  }
  const mapping = {
    steering_pending: ["pending", "queue"],
    approval_pending: ["pending", "approvals"],
    file_edit_review: ["proposed", "diffs"],
    file_edit_apply: ["approved", "diffs"],
    wake_due: ["queued", "wake"],
  } as const;
  const identities = new Set<string>();
  const generatedAt = Date.parse(value.generated_at);
  const items = value.items.map((item) => {
    if (!isRecord(item) || !hasOnlyKeys(item, ["available_at", "destination", "due_at", "id",
      "kind", "state"]) || !boundedIdentity(item.id) || !String(item.id).startsWith("action-") ||
      identities.has(String(item.id)) || !validDate(item.available_at) ||
      !Object.prototype.hasOwnProperty.call(mapping, String(item.kind))) {
      throw new APIRequestError("Operator action item exposed invalid metadata",
        "INVALID_RESPONSE", 502);
    }
    const expected = mapping[item.kind as keyof typeof mapping];
    const dueAt = item.due_at;
    if (item.state !== expected[0] || item.destination !== expected[1] ||
      (item.kind === "wake_due"
        ? !validDate(dueAt) || Date.parse(dueAt) > generatedAt
        : dueAt !== undefined)) {
      throw new APIRequestError("Operator action item widened its closed navigation contract",
        "INVALID_RESPONSE", 502);
    }
    identities.add(String(item.id));
    return item;
  });
  return { ...value, items } as unknown as OperatorActionCenterView;
}

function parseOperationReceiptHistory(value: unknown,
  expectedRunID: string): OperationReceiptHistoryView {
  if (!hasExactKeys(value, ["items", "protocol_version", "truncated"]) ||
    value.protocol_version !== "operation_receipt_history.v1" || !Array.isArray(value.items) ||
    value.items.length > 100 || typeof value.truncated !== "boolean") {
    throw new APIRequestError("Operation receipt history response is invalid",
      "INVALID_RESPONSE", 502);
  }
  const identities = new Set<string>();
  const items = value.items.map((item) => {
    if (!isRecord(item) || !hasOnlyKeys(item, ["completed_at", "id", "receipt", "run_id", "scope"]) ||
      !boundedIdentity(item.id) || identities.has(String(item.id)) || !validDate(item.completed_at) ||
      (item.scope !== "run" && item.scope !== "skill_registry") ||
      (item.scope === "run" && (!boundedIdentity(item.run_id) ||
        (expectedRunID !== "" && item.run_id !== expectedRunID))) ||
      (item.scope === "skill_registry" && item.run_id !== undefined)) {
      throw new APIRequestError("Operation receipt history item exposed invalid scope metadata",
        "INVALID_RESPONSE", 502);
    }
    const receiptValue = item.receipt;
    if (!isRecord(receiptValue) || typeof receiptValue.kind !== "string" ||
      typeof receiptValue.outcome !== "string") {
      throw new APIRequestError("Operation receipt history item omitted its durable receipt",
        "INVALID_RESPONSE", 502);
    }
    const validTerminalOutcome =
      (receiptValue.kind === "file_edit_apply" &&
        (receiptValue.outcome === "applied" || receiptValue.outcome === "failed")) ||
      (receiptValue.kind === "run_wake_consume" &&
        (receiptValue.outcome === "completed" || receiptValue.outcome === "failed")) ||
      (receiptValue.kind === "skill_package_install" && receiptValue.outcome === "installed");
    if (!validTerminalOutcome) {
      throw new APIRequestError("Operation receipt history contains an unsupported terminal result",
        "INVALID_RESPONSE", 502);
    }
    const receipt = parseOperationReceipt(receiptValue,
      receiptValue.kind as OperationReceiptView["kind"],
      receiptValue.outcome as OperationReceiptView["outcome"], false);
    if ((item.scope === "skill_registry") !== (receipt.kind === "skill_package_install")) {
      throw new APIRequestError("Operation receipt history scope and kind diverged",
        "INVALID_RESPONSE", 502);
    }
    identities.add(String(item.id));
    return { ...item, receipt };
  });
  return { ...value, items } as unknown as OperationReceiptHistoryView;
}

function validWorkspaceRelativePath(value: unknown): value is string {
  if (typeof value !== "string" || value.length === 0 || Array.from(value).length > 512 ||
    value.trim() !== value || value.startsWith("/") || value.includes("\\") ||
    value.includes(":") || /[\u0000-\u001f\u007f]/u.test(value)) {
    return false;
  }
  if (value === ".") return true;
  return value.split("/").every((part) => part !== "" && part !== "." && part !== "..");
}

function validWorkspaceEntryName(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && Array.from(value).length <= 255 &&
    value.trim() === value && !value.includes("/") && !value.includes("\\") &&
    !value.includes(":") && !/[\u0000-\u001f\u007f]/u.test(value);
}

type RequestedNetworkAuthority = {
  mode: "disabled" | "allowlist";
  targets: string[];
};

function normalizeRequestedNetworkAuthority(request: {
  network_mode?: string;
  allowed_targets?: string[];
}): RequestedNetworkAuthority {
  const mode = request.network_mode ?? "disabled";
  const rawTargets = request.allowed_targets ?? [];
  if (mode === "disabled") {
    if (rawTargets.length !== 0) {
      throw new Error("Disabled network authority cannot include allowed targets");
    }
    return { mode, targets: [] };
  }
  if (mode !== "allowlist" || rawTargets.length === 0 || rawTargets.length > 256) {
    throw new Error("Network authority requires a bounded exact allowlist");
  }
  const targets = [...new Set(rawTargets.map(canonicalExactNetworkTarget))].sort();
  if (targets.length === 0) {
    throw new Error("Network authority requires an exact allowed target");
  }
  return { mode, targets };
}

export function canonicalExactNetworkTarget(raw: string): string {
  const target = raw.trim();
  const lower = target.toLowerCase();
  if (target === "" || target.includes("*") || lower === "public_https" ||
    /[\\?#]/u.test(target) || target.includes("@")) {
    throw new Error("Network allowlist targets must identify exact public HTTPS hosts");
  }
  if (lower.includes("://") && !lower.startsWith("https://")) {
    throw new Error("Network allowlist target origins must use HTTPS");
  }
  let parsed: URL;
  try {
    parsed = new URL(lower.includes("://") ? target : `https://${target}`);
  } catch {
    throw new Error("Network allowlist target is invalid");
  }
  if (parsed.protocol !== "https:" || parsed.username !== "" || parsed.password !== "" ||
    (parsed.port !== "" && parsed.port !== "443") || parsed.pathname !== "/" ||
    parsed.search !== "" || parsed.hash !== "") {
    throw new Error("Network allowlist targets must be exact HTTPS origins or hosts");
  }
  const hostname = parsed.hostname.toLowerCase().replace(/^\[|\]$/gu, "")
    .replace(/\.$/u, "");
  if (hostname === "" || hostname === "localhost" ||
    !hostname.includes(".") || hostname.includes(":") ||
    /^\d+(?:\.\d+){3}$/u.test(hostname) ||
    hostname.split(".").some((label) => label === "" || label.length > 63 ||
      label.startsWith("-") || label.endsWith("-") || !/^[a-z0-9-]+$/u.test(label)) ||
    [".localhost", ".local", ".internal", ".intranet", ".corp", ".home", ".lan",
      ".home.arpa", ".localdomain", ".test", ".invalid", ".example"]
      .some((suffix) => hostname.endsWith(suffix))) {
    throw new Error("Network allowlist target must be a public host");
  }
  return hostname;
}

function scopeMatchesRequestedNetworkAuthority(scope: Record<string, unknown>,
  expected: RequestedNetworkAuthority): boolean {
  if (scope.network_mode !== expected.mode) {
    return false;
  }
  const targets = scope.allowed_targets;
  if (expected.mode === "disabled") {
    return targets === undefined || (Array.isArray(targets) && targets.length === 0);
  }
  return Array.isArray(targets) && targets.length === expected.targets.length &&
    targets.every((target, index) => target === expected.targets[index]);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: unknown, expected: string[]): value is Record<string, unknown> {
  if (!isRecord(value)) {
    return false;
  }
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  return actual.length === wanted.length && actual.every((key, index) => key === wanted[index]);
}

function hasOnlyKeys(value: Record<string, unknown>, allowed: string[]): boolean {
  const accepted = new Set(allowed);
  return Object.keys(value).every((key) => accepted.has(key));
}

function boundedIdentity(value: unknown): string {
  return typeof value === "string" && value.trim() === value && !value.includes("\0") &&
    value.length > 0 && value.length <= 256
    ? value
    : "";
}

function boundedText(value: unknown, maximum: number): value is string {
  return typeof value === "string" && value.trim() === value && value.length > 0 &&
    value.length <= maximum;
}

function validDate(value: unknown): value is string {
  return typeof value === "string" && Number.isFinite(Date.parse(value));
}

function isSHA256(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{64}$/.test(value);
}

function safePositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function safeBoundedCount(value: unknown, maximum: number): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) &&
    value >= 0 && value <= maximum;
}

const scheduledJobStatuses = ["active", "paused", "completed", "failed", "cancelled",
  "exhausted"];

function parseScheduledJob(value: unknown): ScheduledJobView {
  const required = ["active_lease_generation", "consecutive_unchanged", "created_at",
    "created_by", "id", "last_event_sequence", "model_calls", "owner_root_agent_id",
    "owner_run_id", "revision", "rounds_completed", "spec", "status", "updated_at"];
  const allowed = [...required, "active_lease_expires_at", "completed_at", "last_error_code",
    "last_observation_sha256", "last_result", "next_wake_at", "pending_occurrence_at",
    "stop_reason"];
  if (!isRecord(value) || !hasOnlyKeys(value, allowed) ||
    required.some((key) => !Object.prototype.hasOwnProperty.call(value, key)) ||
    !boundedIdentity(value.id) || !boundedIdentity(value.owner_run_id) ||
    !boundedIdentity(value.owner_root_agent_id) || !boundedText(value.created_by, 256) ||
    !scheduledJobStatuses.includes(String(value.status)) || !validDate(value.created_at) ||
    !validDate(value.updated_at) || !safeBoundedCount(value.active_lease_generation,
      Number.MAX_SAFE_INTEGER) || !safeBoundedCount(value.revision, Number.MAX_SAFE_INTEGER) ||
    !safeBoundedCount(value.rounds_completed, 10_000) ||
    !safeBoundedCount(value.consecutive_unchanged, 10_000) ||
    !safeBoundedCount(value.model_calls, 10_000) ||
    !safeBoundedCount(value.last_event_sequence, Number.MAX_SAFE_INTEGER)) {
    throw new APIRequestError("Scheduled job response is invalid", "INVALID_RESPONSE", 502);
  }
  for (const field of ["active_lease_expires_at", "completed_at", "next_wake_at",
    "pending_occurrence_at"]) {
    if (value[field] !== undefined && !validDate(value[field])) {
      throw new APIRequestError("Scheduled job timestamp is invalid", "INVALID_RESPONSE", 502);
    }
  }
  if ((value.last_observation_sha256 !== undefined &&
      !isSHA256(value.last_observation_sha256)) ||
    (value.last_result !== undefined && typeof value.last_result !== "string") ||
    (value.last_error_code !== undefined && !boundedText(value.last_error_code, 128)) ||
    (value.stop_reason !== undefined && typeof value.stop_reason !== "string")) {
    throw new APIRequestError("Scheduled job result metadata is invalid", "INVALID_RESPONSE", 502);
  }
  const spec = value.spec;
  if (!hasExactKeys(spec, ["deadline_at", "execution_mode", "max_elapsed_seconds",
    "max_model_calls", "max_rounds", "notification", "retry", "schedule",
    "stop_on_target_terminal", "target_run_id", "version"]) ||
    spec.version !== "scheduled-job.v1" || !validDate(spec.deadline_at) ||
    !["read_only", "approved_repair"].includes(String(spec.execution_mode)) ||
    !["silent", "on_change", "on_failure", "all"].includes(String(spec.notification)) ||
    typeof spec.stop_on_target_terminal !== "boolean" ||
    boundedIdentity(spec.target_run_id) !== value.owner_run_id ||
    !safePositiveInteger(spec.max_rounds) || !safeBoundedCount(spec.max_model_calls, 10_000) ||
    !safePositiveInteger(spec.max_elapsed_seconds)) {
    throw new APIRequestError("Scheduled job specification is invalid", "INVALID_RESPONSE", 502);
  }
  const retry = spec.retry;
  if (!hasExactKeys(retry, ["initial_backoff_seconds", "max_attempts",
    "max_backoff_seconds"]) || !safePositiveInteger(retry.max_attempts) ||
    !safePositiveInteger(retry.initial_backoff_seconds) ||
    !safePositiveInteger(retry.max_backoff_seconds) ||
    retry.max_backoff_seconds < retry.initial_backoff_seconds) {
    throw new APIRequestError("Scheduled job retry policy is invalid", "INVALID_RESPONSE", 502);
  }
  const schedule = spec.schedule;
  if (!isRecord(schedule) || !hasOnlyKeys(schedule, ["anchor_at", "interval_seconds", "kind",
    "misfire_policy", "timezone"]) || !validDate(schedule.anchor_at) ||
    !["once", "periodic"].includes(String(schedule.kind)) ||
    !["run_once", "skip"].includes(String(schedule.misfire_policy)) ||
    !boundedText(schedule.timezone, 128) ||
    (schedule.kind === "once" && schedule.interval_seconds !== undefined) ||
    (schedule.kind === "periodic" && !safePositiveInteger(schedule.interval_seconds))) {
    throw new APIRequestError("Scheduled job schedule is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as ScheduledJobView;
}

function parseScheduledJobList(value: unknown): ScheduledJobListView {
  if (!hasExactKeys(value, ["items", "protocol_version"]) ||
    value.protocol_version !== "scheduled-job.v1" || !Array.isArray(value.items) ||
    value.items.length > 100) {
    throw new APIRequestError("Scheduled job list is invalid", "INVALID_RESPONSE", 502);
  }
  return { ...value, items: value.items.map(parseScheduledJob) } as ScheduledJobListView;
}

function parseScheduledJobControl(value: unknown, runID: string, jobID: string,
  action: "create" | "pause" | "resume" | "cancel"): ScheduledJobControlView {
  if (!hasExactKeys(value, ["action", "authority_bypass", "execution_started", "job",
    "protocol_version", "replayed"]) ||
    value.protocol_version !== "scheduled-job-control.v1" || value.action !== action ||
    value.authority_bypass !== false || value.execution_started !== false ||
    typeof value.replayed !== "boolean") {
    throw new APIRequestError("Scheduled job control response widened authority",
      "INVALID_RESPONSE", 502);
  }
  const job = parseScheduledJob(value.job);
  if (job.owner_run_id !== runID || (jobID !== "" && job.id !== jobID)) {
    throw new APIRequestError("Scheduled job control response changed identity",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, job } as ScheduledJobControlView;
}

function parseScheduledJobDetail(value: unknown, jobID: string): ScheduledJobDetailView {
  if (!hasExactKeys(value, ["protocol_version", "snapshot"]) ||
    value.protocol_version !== "scheduled-job.v1" || !isRecord(value.snapshot) ||
    !hasOnlyKeys(value.snapshot, ["authorization", "job", "notifications", "rounds"]) ||
    !Array.isArray(value.snapshot.notifications) || !Array.isArray(value.snapshot.rounds) ||
    value.snapshot.notifications.length > 100 || value.snapshot.rounds.length > 100) {
    throw new APIRequestError("Scheduled job detail is invalid", "INVALID_RESPONSE", 502);
  }
  const job = parseScheduledJob(value.snapshot.job);
  const authorization = value.snapshot.authorization;
  if (authorization !== undefined &&
    (!hasExactKeys(authorization, ["approval_bypass", "authorized_at", "authorized_by",
      "execution_bypass", "expires_at", "job_id", "mode_revision", "mode_snapshot_id",
      "network_bypass", "permission_revision", "permission_snapshot_id", "protocol_version",
      "run_id"]) || authorization.protocol_version !== "scheduled-job-authorization.v1" ||
      authorization.job_id !== jobID || authorization.run_id !== job.owner_run_id ||
      authorization.execution_bypass !== false || authorization.network_bypass !== false ||
      authorization.approval_bypass !== false || !validDate(authorization.authorized_at) ||
      !validDate(authorization.expires_at))) {
    throw new APIRequestError("Scheduled job authorization exposed an invalid field",
      "INVALID_RESPONSE", 502);
  }
  const roundRequired = ["attempt", "changed", "claim_generation", "event_sequence", "job_id",
    "model_called", "occurrence_at", "ordinal", "protocol_version", "started_at", "status",
    "tool_called"];
  const roundAllowed = [...roundRequired, "completed_at", "error_code", "observation_sha256",
    "result"];
  const invalidRound = value.snapshot.rounds.some((round) => !isRecord(round) ||
    !hasOnlyKeys(round, roundAllowed) ||
    roundRequired.some((key) => !Object.prototype.hasOwnProperty.call(round, key)) ||
    round.protocol_version !== "scheduled-job-round.v1" || round.job_id !== jobID ||
    !["claimed", "retry_wait", "unchanged", "completed", "failed", "skipped"]
      .includes(String(round.status)) || typeof round.changed !== "boolean" ||
    typeof round.model_called !== "boolean" || typeof round.tool_called !== "boolean" ||
    (round.tool_called && !round.model_called) || !validDate(round.occurrence_at) ||
    !validDate(round.started_at) ||
    (round.completed_at !== undefined && !validDate(round.completed_at)) ||
    (round.observation_sha256 !== undefined && !isSHA256(round.observation_sha256)));
  const invalidNotice = value.snapshot.notifications.some((notice) =>
    !hasExactKeys(notice, ["created_at", "id", "job_id", "kind", "summary"]) ||
    notice.job_id !== jobID || !boundedIdentity(notice.id) || !validDate(notice.created_at) ||
    !["change", "failure", "recovery", "completed"].includes(String(notice.kind)) ||
    !boundedText(notice.summary, 1024));
  if (job.id !== jobID || invalidRound || invalidNotice) {
    throw new APIRequestError("Scheduled job detail changed identity or exposed payload",
      "INVALID_RESPONSE", 502);
  }
  return { ...value, snapshot: { ...value.snapshot, job } } as ScheduledJobDetailView;
}

function validDiagnosticRedaction(value: unknown): boolean {
  return hasExactKeys(value, ["command_input", "event_payloads", "prompts", "secrets",
    "terminal_input"]) && value.command_input === "withheld" &&
    value.event_payloads === "withheld" && value.prompts === "withheld" &&
    value.terminal_input === "withheld" && value.secrets === "redacted";
}

function parseDiagnosticBundle(value: unknown, runID: string): DiagnosticBundleView {
  if (!hasExactKeys(value, ["debug", "doctor", "generated_at", "protocol_version"]) ||
    value.protocol_version !== "diagnostic-bundle.v1" || !validDate(value.generated_at) ||
    !isRecord(value.debug) || value.debug.protocol_version !== "debug-query.v1" ||
    value.debug.run_id !== runID || !Array.isArray(value.debug.items) ||
    value.debug.items.length > 100 || !validDiagnosticRedaction(value.debug.redaction) ||
    value.debug.items.some((item) => !hasExactKeys(item, ["category", "evidence",
      "observed_at", "occurred_at", "payload_state", "sequence", "source", "subject_id",
      "timestamp_adjusted", "type"]) || item.payload_state !== "withheld" ||
      item.evidence !== "persisted_event") || !isRecord(value.doctor) ||
    value.doctor.protocol_version !== "doctor-snapshot.v1" ||
    !hasOnlyKeys(value.doctor, ["build", "checks", "generated_at", "models",
      "protocol_version", "ready", "redaction", "run", "schema_version"]) ||
    ["build", "models", "schema_version"].some((key) =>
      !Object.prototype.hasOwnProperty.call(value.doctor, key)) ||
    !validDate(value.doctor.generated_at) || typeof value.doctor.ready !== "boolean" ||
    !Array.isArray(value.doctor.checks) ||
    value.doctor.checks.some((check) => !hasExactKeys(check,
      ["component", "detail_code", "evidence", "status"]) ||
      !["ready", "degraded", "not_configured", "not_probed"].includes(String(check.status))) ||
    !validDiagnosticRedaction(value.doctor.redaction) ||
    (value.doctor.run !== undefined && (!isRecord(value.doctor.run) ||
      !hasOnlyKeys(value.doctor.run, ["allowed_network_target_count", "execution_permission",
        "model_route", "network_mode", "phase", "process_capability_granted", "profile",
        "read_only_tools_eligible", "repair_tools_eligible", "root_agent_id", "run_id",
        "status", "surface", "workspace_id"]) || value.doctor.run.run_id !== runID ||
      value.doctor.run.process_capability_granted !== false))) {
    throw new APIRequestError("Diagnostic bundle violated its redaction contract",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as DiagnosticBundleView;
}

function boundedStringArray(value: unknown, maximumItems: number,
  maximumBytes = 4_096): value is string[] {
  return Array.isArray(value) && value.length <= maximumItems &&
    value.every((item) => typeof item === "string" && item.length <= maximumBytes &&
      item.trim() === item);
}

function containsForbiddenExtensionField(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(containsForbiddenExtensionField);
  if (!isRecord(value)) return false;
  const forbidden = new Set(["credential", "credential_value", "secret", "token",
    "authorization", "arguments", "result", "publisher_public_key", "archive_bytes", "raw"]);
  return Object.entries(value).some(([key, child]) =>
    forbidden.has(key.toLocaleLowerCase()) || containsForbiddenExtensionField(child));
}

const codeIntelTools = ["code_workspace_symbols", "code_document_symbols",
  "code_definition", "code_references", "code_implementation", "code_hover",
  "code_signature_help", "code_diagnostics", "code_call_hierarchy",
  "code_type_hierarchy"] as const;
const codeIntelCapabilityKeys = ["workspace_symbols", "document_symbols", "definition",
  "references", "implementation", "hover", "signature_help", "diagnostics",
  "call_hierarchy", "type_hierarchy"] as const;
const codeIntelHealthStates = ["configured", "starting", "healthy", "unavailable",
  "crashed", "timed_out", "protocol_error", "stopped"];

function strictCodeIntelIdentity(value: unknown): boolean {
  return boundedIdentity(value) !== "" &&
    !/[\s\u0000-\u001f\u007f]/u.test(String(value));
}

function containsForbiddenCodeIntelField(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(containsForbiddenCodeIntelField);
  if (!isRecord(value)) return false;
  const forbidden = new Set(["argument", "arguments", "argv", "command", "credential",
    "credentials", "environment", "executable", "home", "password", "raw", "secret",
    "stderr", "stdout", "token"]);
  return Object.entries(value).some(([key, child]) =>
    forbidden.has(key.toLocaleLowerCase()) || containsForbiddenCodeIntelField(child));
}

function parseCodeIntelInventory(value: unknown): CodeIntelInventoryView {
  if (!hasExactKeys(value, ["enabled", "protocol_version", "qualifications", "servers"]) ||
    value.protocol_version !== "code-intel-lsp.v1" || typeof value.enabled !== "boolean" ||
    containsForbiddenCodeIntelField(value) || !Array.isArray(value.servers) ||
    value.servers.length > 32 || !Array.isArray(value.qualifications) ||
    value.qualifications.length > 32) {
    throw new APIRequestError("Code intelligence inventory is invalid", "INVALID_RESPONSE", 502);
  }
  const serverIdentities = new Set<string>();
  for (const server of value.servers) {
    const required = ["capabilities", "credentials_granted", "descriptor_fingerprint",
      "health", "languages", "model_visible_tools", "network_access_granted",
      "process_owned", "protocol_version", "read_only", "server_id", "server_name",
      "shell_profile_loaded", "source_kind", "source_label", "source_sha256",
      "workspace_id"];
    const optional = ["capability_fingerprint", "generation", "last_error", "qualified_at",
      "server_version"];
    const capabilities = isRecord(server) && isRecord(server.capabilities)
      ? server.capabilities : null;
    if (!isRecord(server) || !hasOnlyKeys(server, [...required, ...optional]) ||
      required.some((key) => !Object.prototype.hasOwnProperty.call(server, key)) ||
      server.protocol_version !== "code-intel-lsp.v1" ||
      !strictCodeIntelIdentity(server.server_id) || !strictCodeIntelIdentity(server.workspace_id) ||
      !boundedText(server.server_name, 256) || server.source_kind !== "operator_config" ||
      !boundedText(server.source_label, 256) || !isSHA256(server.source_sha256) ||
      !isSHA256(server.descriptor_fingerprint) ||
      !codeIntelHealthStates.includes(String(server.health)) ||
      server.process_owned !== true || server.read_only !== true ||
      server.network_access_granted !== false || server.credentials_granted !== false ||
      server.shell_profile_loaded !== false || !Array.isArray(server.languages) ||
      server.languages.length < 1 || server.languages.length > 16 ||
      server.languages.some((language) => !strictCodeIntelIdentity(language)) ||
      new Set(server.languages).size !== server.languages.length ||
      server.languages.join("\u0000") !== [...server.languages].sort().join("\u0000") ||
      capabilities === null || !hasExactKeys(capabilities, [...codeIntelCapabilityKeys]) ||
      codeIntelCapabilityKeys.some((key) => typeof capabilities[key] !== "boolean") ||
      !boundedStringArray(server.model_visible_tools, codeIntelTools.length, 64) ||
      new Set(server.model_visible_tools).size !== server.model_visible_tools.length ||
      server.model_visible_tools.some((tool) => !codeIntelTools.includes(
        tool as typeof codeIntelTools[number])) ||
      (server.capability_fingerprint !== undefined &&
        !isSHA256(server.capability_fingerprint)) ||
      (server.generation !== undefined && !isSHA256(server.generation)) ||
      (server.last_error !== undefined && !boundedText(server.last_error, 2_048)) ||
      (server.server_version !== undefined && !boundedText(server.server_version, 256)) ||
      (server.qualified_at !== undefined && !validDate(server.qualified_at)) ||
      (server.health === "healthy" && (!isSHA256(server.capability_fingerprint) ||
        !isSHA256(server.generation) || !validDate(server.qualified_at)))) {
      throw new APIRequestError("Code intelligence server projection is invalid",
        "INVALID_RESPONSE", 502);
    }
    const expectedTools = codeIntelCapabilityKeys.flatMap((key, index) =>
      capabilities[key] ? [codeIntelTools[index]] : []);
    if (expectedTools.join("\u0000") !== server.model_visible_tools.join("\u0000")) {
      throw new APIRequestError("Code intelligence tools do not match negotiated capabilities",
        "INVALID_RESPONSE", 502);
    }
    const identity = `${String(server.workspace_id)}\u0000${String(server.server_id)}`;
    if (serverIdentities.has(identity)) {
      throw new APIRequestError("Code intelligence inventory repeats a server",
        "INVALID_RESPONSE", 502);
    }
    serverIdentities.add(identity);
  }
  const qualificationIdentities = new Set<string>();
  for (const qualification of value.qualifications) {
    const required = ["credentials_granted", "descriptor_fingerprint", "eligible",
      "executable_hash_matched", "health", "minimal_environment", "network_access_granted",
      "process_owned", "protocol_version", "reviewed", "server_id", "shell_profile_loaded",
      "workspace_id"];
    if (!isRecord(qualification) || !hasOnlyKeys(qualification, [...required, "reason"]) ||
      required.some((key) => !Object.prototype.hasOwnProperty.call(qualification, key)) ||
      qualification.protocol_version !== "code-intel-lsp.v1" ||
      !strictCodeIntelIdentity(qualification.server_id) ||
      !strictCodeIntelIdentity(qualification.workspace_id) ||
      !isSHA256(qualification.descriptor_fingerprint) ||
      !codeIntelHealthStates.includes(String(qualification.health)) ||
      typeof qualification.eligible !== "boolean" ||
      typeof qualification.executable_hash_matched !== "boolean" ||
      typeof qualification.reviewed !== "boolean" || qualification.process_owned !== true ||
      qualification.minimal_environment !== true ||
      qualification.network_access_granted !== false ||
      qualification.credentials_granted !== false ||
      qualification.shell_profile_loaded !== false ||
      (qualification.reason !== undefined && !boundedText(qualification.reason, 2_048)) ||
      (qualification.eligible && (!qualification.executable_hash_matched ||
        !qualification.reviewed || qualification.health !== "configured" ||
        qualification.reason !== undefined))) {
      throw new APIRequestError("Code intelligence qualification is invalid",
        "INVALID_RESPONSE", 502);
    }
    const identity = `${String(qualification.workspace_id)}\u0000${String(qualification.server_id)}`;
    if (qualificationIdentities.has(identity)) {
      throw new APIRequestError("Code intelligence inventory repeats a qualification",
        "INVALID_RESPONSE", 502);
    }
    qualificationIdentities.add(identity);
  }
  return value as unknown as CodeIntelInventoryView;
}

function parseExtensionInventory(value: unknown): ExtensionInventoryView {
  if (!isRecord(value) || value.protocol_version !== "extension-inventory.v1" ||
    containsForbiddenExtensionField(value) ||
    !Array.isArray(value.mcp_servers) || value.mcp_servers.length > 64 ||
    !Array.isArray(value.mcp_calls) || value.mcp_calls.length > 200 ||
    !Array.isArray(value.plugins) || value.plugins.length > 1_000 ||
    (value.run_id !== undefined && !boundedIdentity(value.run_id)) ||
    (value.workspace_id !== undefined && !boundedIdentity(value.workspace_id))) {
    throw new APIRequestError("Extension inventory response is invalid", "INVALID_RESPONSE", 502);
  }
  for (const item of value.mcp_servers) {
    if (!isRecord(item) || item.protocol_version !== "mcp-client-server.v1" ||
      !boundedIdentity(item.id) || !boundedText(item.name, 256) ||
      (item.transport !== "stdio" && item.transport !== "streamable_http") ||
      !boundedText(item.target, 4_096) || !boundedIdentity(item.workspace_id) ||
      !isSHA256(item.descriptor_fingerprint) || !safePositiveInteger(item.generation) ||
      !validDate(item.created_at) || !validDate(item.updated_at) ||
      !boundedStringArray(item.declared_capabilities, 3, 32) ||
      !isRecord(item.capabilities) ||
      !boundedStringArray(item.capabilities.negotiated, 3, 32) ||
      !boundedStringArray(item.capabilities.tools, 256, 256) ||
      !boundedStringArray(item.capabilities.resources, 256) ||
      !boundedStringArray(item.capabilities.prompts, 128, 256) ||
      !isRecord(item.source) || !boundedText(item.source.kind, 32) ||
      !boundedText(item.source.uri, 4_096)) {
      throw new APIRequestError("MCP server projection is invalid", "INVALID_RESPONSE", 502);
    }
  }
  for (const item of value.mcp_calls) {
    if (!isRecord(item) || !boundedIdentity(item.id) || !boundedIdentity(item.run_id) ||
      !boundedIdentity(item.workspace_id) || !boundedIdentity(item.server_id) ||
      !boundedText(item.tool_name, 256) || !isSHA256(item.capability_fingerprint) ||
      !isSHA256(item.arguments_sha256) || !safeBoundedCount(item.result_bytes, 128 * 1_024) ||
      typeof item.truncated !== "boolean" || !validDate(item.started_at) ||
      !validDate(item.completed_at)) {
      throw new APIRequestError("MCP call audit projection is invalid", "INVALID_RESPONSE", 502);
    }
  }
  for (const item of value.plugins) {
    if (!isRecord(item) || item.protocol_version !== "plugin-installation.v1" ||
      !boundedIdentity(item.id) || !isSHA256(item.archive_sha256) ||
      !isSHA256(item.package_fingerprint) || !safePositiveInteger(item.generation) ||
      typeof item.signature_present !== "boolean" || typeof item.signature_valid !== "boolean" ||
      !boundedStringArray(item.enabled_capabilities, 4, 32) ||
      !boundedIdentity(item.staged_by) ||
      !validDate(item.created_at) || !validDate(item.updated_at) ||
      !isRecord(item.manifest) || !boundedIdentity(item.manifest.id) ||
      !boundedText(item.manifest.name, 256) || !boundedText(item.manifest.publisher, 256) ||
      !boundedStringArray(item.manifest.capabilities, 4, 32) ||
      !isRecord(item.source) || !boundedText(item.source.kind, 32) ||
      !boundedText(item.source.uri, 4_096)) {
      throw new APIRequestError("Plugin installation projection is invalid", "INVALID_RESPONSE", 502);
    }
  }
  return value as unknown as ExtensionInventoryView;
}

function parseExtensionMCPServer(value: unknown): ExtensionMCPServerView {
  return parseExtensionInventory({ protocol_version: "extension-inventory.v1",
    mcp_servers: [value], mcp_calls: [], plugins: [] }).mcp_servers[0];
}

function parseExtensionPlugin(value: unknown): ExtensionPluginInstallationView {
  return parseExtensionInventory({ protocol_version: "extension-inventory.v1",
    mcp_servers: [], mcp_calls: [], plugins: [value] }).plugins[0];
}

export class CyberAgentClient {
  readonly baseURL: string;
  readonly hasControl: boolean;
  readonly hasExecutionPermissionControl: boolean;
  readonly hasBrowserCDPPermissionControl: boolean;
  readonly hasFullCDPDebug: boolean;
  readonly hasFullCDPSessionControl: boolean;
  readonly hasRunCreation: boolean;
  readonly hasStandardCodePreset: boolean;
  readonly hasSessionMessages: boolean;
  readonly hasThreadControl: boolean;
  readonly hasSessionSteeringControl: boolean;
  readonly hasRunLifecycle: boolean;
  readonly hasRunExecution: boolean;
  readonly hasPlanDelivery: boolean;
  readonly hasApprovalControl: boolean;
  readonly hasControlledCommandProposalControl: boolean;
  readonly hasHostCommandProposalControl: boolean;
  readonly hasModelControl: boolean;
  readonly hasProviderDefinitions: boolean;
  readonly hasProviderCredentials: boolean;
  readonly hasFileEditReview: boolean;
  readonly hasFileEditProposals: boolean;
  readonly hasFileEditApply: boolean;
  readonly hasRunWakeControl: boolean;
  readonly hasRunWakeExecution: boolean;
  readonly hasRunWakeWorker: boolean;
  readonly hasScheduledJobControl: boolean;
  readonly hasScheduledJobWorker: boolean;
  readonly hasSkillInstallation: boolean;
  readonly hasEvidenceAttachment: boolean;
  readonly hasVerificationEvidence: boolean;
  readonly hasEmbeddedAnalyzerExecution: boolean;
  readonly hasWorkspaceCheckpointControl: boolean;
  readonly hasGitAdvancedControl: boolean;
  readonly hasGitHubReviewControl: boolean;
  readonly hasBatchDeliveryControl: boolean;
  readonly hasBatchDeliveryHostValidation: boolean;
  readonly hasExtensionControl: boolean;
  readonly hasUIEvidence: boolean;

  constructor(
    private readonly token: string,
    baseURL = import.meta.env.VITE_API_BASE_URL || "/api/v1",
    private readonly controlToken = "",
    capabilities: ClientCapabilities = {},
  ) {
    if (token.trim() === "") {
      throw new Error("A read bearer token is required");
    }
    this.baseURL = normalizeBaseURL(baseURL);
    const controlPresent = controlToken.trim() !== "";
    this.hasControl = controlPresent && (capabilities.runControlEnabled ?? true);
    this.hasExecutionPermissionControl = controlPresent &&
      (capabilities.executionPermissionControlEnabled ?? false);
    this.hasBrowserCDPPermissionControl = controlPresent &&
      (capabilities.browserCDPPermissionControlEnabled ?? false);
    this.hasFullCDPDebug = this.hasBrowserCDPPermissionControl &&
      (capabilities.fullCDPDebugEnabled ?? false);
    this.hasFullCDPSessionControl = this.hasFullCDPDebug &&
      (capabilities.fullCDPSessionControlEnabled ?? false);
    this.hasRunCreation = controlPresent && (capabilities.runCreationEnabled ?? true);
    this.hasStandardCodePreset = controlPresent &&
      (capabilities.standardCodePresetEnabled ?? false);
    this.hasSessionMessages = controlPresent && (capabilities.sessionMessageEnabled ?? true);
    this.hasThreadControl = controlPresent &&
      (capabilities.threadControlEnabled ??
        ((capabilities.runCreationEnabled ?? true) &&
          (capabilities.sessionMessageEnabled ?? true)));
    this.hasSessionSteeringControl = controlPresent &&
      (capabilities.sessionSteeringControlEnabled ?? true);
    this.hasRunLifecycle = controlPresent && (capabilities.runLifecycleEnabled ?? true);
    this.hasRunExecution = controlPresent && (capabilities.runExecutionEnabled ?? true);
    this.hasPlanDelivery = controlPresent && (capabilities.planDeliveryControlEnabled ?? true);
    this.hasApprovalControl = controlPresent && (capabilities.approvalControlEnabled ?? true);
    this.hasControlledCommandProposalControl = controlPresent &&
      (capabilities.controlledCommandProposalControlEnabled ?? false);
    this.hasHostCommandProposalControl = controlPresent &&
      (capabilities.hostCommandProposalControlEnabled ?? false) &&
      (capabilities.operatorApprovalEnabled ?? false);
    this.hasModelControl = controlPresent && (capabilities.modelControlEnabled ?? true);
    this.hasProviderDefinitions = this.hasModelControl;
    this.hasProviderCredentials = controlPresent &&
      (capabilities.providerCredentialEnabled ?? false);
    this.hasFileEditReview = controlPresent && (capabilities.fileEditReviewEnabled ?? true);
    this.hasFileEditProposals = controlPresent &&
      (capabilities.fileEditProposalEnabled ?? false);
    this.hasFileEditApply = controlPresent && (capabilities.fileEditApplyEnabled ?? true);
    this.hasRunWakeControl = controlPresent && (capabilities.runWakeControlEnabled ?? true);
    this.hasRunWakeExecution = controlPresent && (capabilities.runWakeExecutionEnabled ?? true);
    this.hasRunWakeWorker = controlPresent && (capabilities.runWakeWorkerEnabled ?? false);
    this.hasScheduledJobControl = controlPresent &&
      (capabilities.scheduledJobControlEnabled ?? false);
    this.hasScheduledJobWorker = controlPresent &&
      (capabilities.scheduledJobWorkerEnabled ?? false);
    this.hasSkillInstallation = controlPresent && (capabilities.skillInstallationEnabled ?? true);
    this.hasEvidenceAttachment = controlPresent &&
      (capabilities.evidenceAttachmentEnabled ?? true);
    this.hasVerificationEvidence = controlPresent &&
      (capabilities.verificationEvidenceEnabled ?? false);
    this.hasEmbeddedAnalyzerExecution = controlPresent &&
      (capabilities.embeddedAnalyzerExecutionEnabled ?? false);
    this.hasWorkspaceCheckpointControl = controlPresent &&
      (capabilities.workspaceCheckpointControlEnabled ?? false);
    this.hasGitAdvancedControl = controlPresent &&
      (capabilities.gitAdvancedControlEnabled ?? false) &&
      this.hasExecutionPermissionControl &&
      (capabilities.operatorApprovalEnabled ?? false) &&
      this.hasWorkspaceCheckpointControl;
    this.hasGitHubReviewControl = controlPresent &&
      (capabilities.githubReviewControlEnabled ?? false) &&
      this.hasExecutionPermissionControl &&
      (capabilities.operatorApprovalEnabled ?? false);
    this.hasBatchDeliveryControl = controlPresent &&
      (capabilities.batchDeliveryControlEnabled ?? false);
    this.hasBatchDeliveryHostValidation = this.hasBatchDeliveryControl &&
      this.hasExecutionPermissionControl &&
      (capabilities.operatorApprovalEnabled ?? false) &&
      (capabilities.dangerFullAccessEnabled ?? false) &&
      (capabilities.batchDeliveryHostValidationEnabled ?? false);
    this.hasExtensionControl = controlPresent &&
      (capabilities.extensionControlEnabled ?? true);
    this.hasUIEvidence = controlPresent && (capabilities.uiEvidenceControlEnabled ?? false) &&
      this.hasRunExecution && this.hasBrowserCDPPermissionControl;
  }

  async health(signal?: AbortSignal): Promise<HealthView> {
    return this.get<HealthView>("/health", {}, signal);
  }

  async runtimeCapabilities(signal?: AbortSignal): Promise<RuntimeCapabilitiesView> {
    return parseRuntimeCapabilities(await this.get<unknown>("/capabilities", {}, signal));
  }

  async runCapabilityReadiness(runID: string,
    signal?: AbortSignal): Promise<RunCapabilityReadinessView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseRunCapabilityReadiness(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/capability-readiness`, {}, signal), runID);
  }

  async getFullCDPSession(runID: string,
    signal?: AbortSignal): Promise<FullCDPSessionControlView> {
    if (!this.hasFullCDPSessionControl || !boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("Full CDP production session capability and a normalized Run are required");
    }
    return parseFullCDPSessionControl(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/full-cdp-session`, {}, signal), runID);
  }

  async openFullCDPSession(runID: string, body: FullCDPSessionOpenRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<FullCDPSessionControlView> {
    if (!this.hasFullCDPSessionControl || !boundedIdentity(runID) || runID.trim() !== runID ||
      body.version !== "full_cdp_session.v1" || body.confirm_full_cdp !== true ||
      !safePositiveInteger(body.expected_execution_permission_revision) ||
      !safePositiveInteger(body.expected_browser_cdp_permission_revision) ||
      !["chrome", "edge"].includes(body.browser.product) ||
      !["stable", "beta", "dev", "canary"].includes(body.browser.channel) ||
      typeof body.target !== "string" || body.target.trim() !== body.target ||
      body.target.length === 0 || body.target.length > 2_048) {
      throw new Error("A confirmed, revision-bound Full CDP session request is required");
    }
    return parseFullCDPSessionControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/full-cdp-session`, body, idempotencyKey, signal), runID);
  }

  async closeFullCDPSession(runID: string, body: FullCDPSessionCloseRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<FullCDPSessionControlView> {
    if (!this.hasFullCDPSessionControl || !boundedIdentity(runID) || runID.trim() !== runID ||
      body.version !== "full_cdp_session_close.v1" ||
      !boundedIdentity(body.expected_session_id)) {
      throw new Error("An exact Full CDP session cleanup request is required");
    }
    return parseFullCDPSessionControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/full-cdp-session/close`, body,
      idempotencyKey, signal), runID);
  }

  async configureStandardCode(runID: string,
    action: "configure" | "pause_and_configure",
    body: StandardCodePresetControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<StandardCodePresetControlView> {
    if (!this.hasStandardCodePreset) {
      throw new Error("Standard Code preset capability is required for this operation");
    }
    if (!boundedIdentity(runID) || runID.trim() !== runID ||
      body.version !== "standard_code_preset.v1" ||
      !["auto", "local", "docker"].includes(body.backend_intent) ||
      body.workspace_id !== undefined || body.goal !== undefined ||
      body.confirm_workspace_trust !== (body.expected_trust_digest !== undefined) ||
      (body.expected_trust_digest !== undefined && !isSHA256(body.expected_trust_digest))) {
      throw new Error("A valid existing-Run Standard Code preset request is required");
    }
    const suffix = action === "pause_and_configure"
      ? "/standard-code/pause-and-configure" : "/standard-code/preset";
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}${suffix}`, body, idempotencyKey, signal);
    return parseStandardCodePreset(result, action);
  }

  async createStandardCode(body: StandardCodePresetControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<StandardCodePresetControlView> {
    if (!this.hasStandardCodePreset) {
      throw new Error("Standard Code preset capability is required for this operation");
    }
    const workspaceID = boundedIdentity(body.workspace_id);
    const goal = body.goal;
    if (body.version !== "standard_code_preset.v1" ||
      !["auto", "local", "docker"].includes(body.backend_intent) ||
      workspaceID !== body.workspace_id || typeof goal !== "string" ||
      goal.trim() !== goal || goal.length === 0 || goal.includes("\0") ||
      new TextEncoder().encode(goal).byteLength > 4096 ||
      body.confirm_workspace_trust !== (body.expected_trust_digest !== undefined) ||
      (body.expected_trust_digest !== undefined && !isSHA256(body.expected_trust_digest))) {
      throw new Error("A normalized Workspace, goal, and trust intent are required for Standard Code");
    }
    const result = parseStandardCodePreset(await this.sendControl<unknown>(
      "/standard-code/preset", body, idempotencyKey, signal), "configure");
    if (result.workspace_id !== workspaceID ||
      result.backend_intent !== body.backend_intent) {
      throw new APIRequestError("Standard Code preset response changed the requested target",
        "INVALID_RESPONSE", 502);
    }
    return result;
  }

  async codeIntelInventory(workspaceID = "", signal?: AbortSignal):
    Promise<CodeIntelInventoryView> {
    if (workspaceID !== "" && (!strictCodeIntelIdentity(workspaceID) ||
      workspaceID.trim() !== workspaceID)) {
      throw new Error("A normalized Workspace identity is required");
    }
    return parseCodeIntelInventory(await this.get<unknown>(
      "/code-intel", { workspace_id: workspaceID || undefined }, signal,
    ));
  }

  async gitAdvancedProjection(runID: string,
    signal?: AbortSignal): Promise<GitAdvancedProjectionView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const value = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/git-advanced`, { limit: 100 }, signal,
    );
    if (!isGitAdvancedProjection(value, runID)) {
      throw new APIRequestError("Git advanced projection is invalid", "INVALID_RESPONSE", 502);
    }
    return value;
  }

  async discoverGitAdvancedHunks(runID: string, spec: GitAdvancedSpecView,
    signal?: AbortSignal): Promise<GitAdvancedReviewResultView> {
    if (!this.hasGitAdvancedControl || !boundedIdentity(runID) || runID.trim() !== runID ||
      !validGitAdvancedSpec(spec) || !["hunk_stage", "hunk_unstage", "hunk_revert"]
        .includes(spec.operation) || (spec.hunk_ids?.length ?? 0) !== 0) {
      throw new Error("Git advanced hunk discovery requires an enabled capability and closed hunk spec");
    }
    const value = await this.sendReadRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/git-advanced/discover-hunks`, { spec }, signal,
    );
    if (!isGitAdvancedReviewResult(value, runID, true)) {
      throw new APIRequestError("Git advanced hunk discovery is invalid", "INVALID_RESPONSE", 502);
    }
    return value;
  }

  async reviewGitAdvanced(runID: string, body: {
    operation_key: string;
    scope: GitAdvancedScopeView;
    spec: GitAdvancedSpecView;
  }, signal?: AbortSignal): Promise<GitAdvancedReviewResultView> {
    if (!this.hasGitAdvancedControl || !boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedText(body.operation_key, 256) || body.operation_key.length < 16 ||
      !isSHA256(body.scope.capability_generation) || !safePositiveInteger(body.scope.lease_generation) ||
      !validGitAdvancedSpec(body.spec)) {
      throw new Error("Git advanced review requires exact capability, lease, and operation inputs");
    }
    const value = await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/git-advanced/review`, body, signal,
    );
    if (!isGitAdvancedReviewResult(value, runID, false)) {
      throw new APIRequestError("Git advanced review is invalid", "INVALID_RESPONSE", 502);
    }
    return value;
  }

  async executeGitAdvanced(runID: string, body: {
    operation_id: string;
    approval_id: string;
    scope: GitAdvancedScopeView;
  }, signal?: AbortSignal): Promise<GitAdvancedExecuteResultView> {
    if (!this.hasGitAdvancedControl || !boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(body.operation_id) || !boundedIdentity(body.approval_id) ||
      !isSHA256(body.scope.capability_generation) || !safePositiveInteger(body.scope.lease_generation)) {
      throw new Error("Git advanced execution requires exact approved operation authority");
    }
    const value = await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/git-advanced/execute`, body, signal,
    );
    if (!isGitAdvancedExecuteResult(value, body.operation_id)) {
      throw new APIRequestError("Git advanced execution result is invalid", "INVALID_RESPONSE", 502);
    }
    return value;
  }

  async githubReviewConnections(enabledOnly = false,
    signal?: AbortSignal): Promise<GitHubReviewCredentialView[]> {
    return parseGitHubReviewConnections(await this.get<unknown>(
      "/github-review/connections", { enabled_only: enabledOnly ? "true" : undefined }, signal,
    ));
  }

  async githubReviewCredential(connectionID: string,
    signal?: AbortSignal): Promise<GitHubReviewCredentialView> {
    if (!boundedIdentity(connectionID)) throw new Error("A normalized GitHub connection is required");
    return parseGitHubReviewCredential(await this.get<unknown>(
      `/github-review/connections/${encodeURIComponent(connectionID)}`, {}, signal,
    ));
  }

  async configureGitHubReview(body: {
    connection_id?: string;
    repository: { host: "github.com"; owner: string; name: string; full_name: string; private: boolean };
    credential: { name: string; kind: "github_app_device" | "oauth_user" | "fine_grained_pat" };
    client_id?: string;
    allowed_log_hosts: string[];
    write_enabled: boolean;
    enabled: boolean;
    expected_generation: number;
  }, signal?: AbortSignal): Promise<GitHubReviewConfigureResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(body.repository.owner) ||
      !boundedIdentity(body.repository.name) || body.repository.full_name !==
      `${body.repository.owner}/${body.repository.name}` || !boundedIdentity(body.credential.name) ||
      typeof body.write_enabled !== "boolean" || !Number.isSafeInteger(body.expected_generation) ||
      body.expected_generation < 0) {
      throw new Error("A bounded GitHub repository configuration is required");
    }
    return parseGitHubReviewConfigure(await this.sendControlRequest<unknown>(
      "/github-review/connections", body, signal,
    ));
  }

  async beginGitHubReviewDeviceFlow(connectionID: string,
    signal?: AbortSignal): Promise<GitHubReviewDeviceAuthorizationView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(connectionID)) {
      throw new Error("GitHub review control and a connection are required");
    }
    return parseGitHubDeviceAuthorization(await this.sendControlRequest<unknown>(
      `/github-review/connections/${encodeURIComponent(connectionID)}/device`, {}, signal,
    ));
  }

  async pollGitHubReviewDeviceFlow(connectionID: string, sessionID: string,
    signal?: AbortSignal): Promise<GitHubReviewDevicePollResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(connectionID) ||
      !boundedIdentity(sessionID)) throw new Error("A GitHub Device Flow session is required");
    return parseGitHubDevicePoll(await this.sendControlRequest<unknown>(
      `/github-review/connections/${encodeURIComponent(connectionID)}/device-poll`,
      { session_id: sessionID }, signal,
    ));
  }

  async disconnectGitHubReview(connectionID: string,
    signal?: AbortSignal): Promise<GitHubReviewCredentialView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(connectionID)) {
      throw new Error("GitHub review control and a connection are required");
    }
    return parseGitHubReviewCredential(await this.sendControlRequest<unknown>(
      `/github-review/connections/${encodeURIComponent(connectionID)}/disconnect`, {}, signal,
    ));
  }

  async qualifyGitHubReview(connectionID: string, pullRequest: number,
    signal?: AbortSignal): Promise<GitHubReviewQualificationResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(connectionID) ||
      !Number.isSafeInteger(pullRequest) || pullRequest < 1) throw new Error("A valid PR is required");
    return parseGitHubQualification(await this.sendControlRequest<unknown>(
      `/github-review/connections/${encodeURIComponent(connectionID)}/qualify`,
      { pull_request: pullRequest }, signal,
    ));
  }

  async fetchGitHubReview(connectionID: string, pullRequest: number,
    signal?: AbortSignal): Promise<GitHubReviewFetchResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(connectionID) ||
      !Number.isSafeInteger(pullRequest) || pullRequest < 1) throw new Error("A valid PR is required");
    return parseGitHubFetch(await this.sendControlRequest<unknown>(
      `/github-review/connections/${encodeURIComponent(connectionID)}/fetch`,
      { pull_request: pullRequest }, signal,
    ));
  }

  async githubReviewProjection(runID: string, connectionID: string, pullRequest = 0,
    signal?: AbortSignal): Promise<GitHubReviewProjectionView> {
    if (!boundedIdentity(runID) || !boundedIdentity(connectionID) ||
      !Number.isSafeInteger(pullRequest) || pullRequest < 0) throw new Error("Invalid GitHub projection scope");
    return parseGitHubProjection(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/github-review`, {
        connection_id: connectionID, pull_request: pullRequest || undefined, limit: 100,
      }, signal,
    ), runID);
  }

  async buildGitHubReviewEvidence(runID: string, snapshotID: string,
    signal?: AbortSignal): Promise<GitHubReviewEvidenceResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(runID) || !boundedIdentity(snapshotID)) {
      throw new Error("A Run and immutable GitHub snapshot are required");
    }
    return parseGitHubEvidence(await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/github-review/evidence`,
      { snapshot_id: snapshotID }, signal,
    ));
  }

  async reviewGitHubWrite(runID: string, body: { connection_id: string; snapshot_id: string;
    operation_key: string; spec: GitHubReviewWriteSpecView },
  signal?: AbortSignal): Promise<GitHubReviewWriteReviewResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(runID) ||
      !boundedIdentity(body.connection_id) || !boundedIdentity(body.snapshot_id) ||
      !boundedIdentity(body.operation_key)) throw new Error("An exact GitHub write preview is required");
    return parseGitHubWriteReview(await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/github-review/review`, body, signal,
    ));
  }

  async executeGitHubWrite(runID: string, operationID: string, approvalID: string,
    signal?: AbortSignal): Promise<GitHubReviewWriteExecuteResultView> {
    if (!this.hasGitHubReviewControl || !boundedIdentity(runID) ||
      !boundedIdentity(operationID) || !boundedIdentity(approvalID)) {
      throw new Error("An approved GitHub write operation is required");
    }
    return parseGitHubWriteExecute(await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/github-review/execute`,
      { operation_id: operationID, approval_id: approvalID }, signal,
    ));
  }

  async extensionInventory(runID = "", signal?: AbortSignal): Promise<ExtensionInventoryView> {
    if (runID !== "" && (!boundedIdentity(runID) || runID.trim() !== runID)) {
      throw new Error("A normalized Run identity is required");
    }
    return parseExtensionInventory(await this.get<unknown>(
      "/extensions", { run_id: runID || undefined }, signal,
    ));
  }

  async reviewMCPServer(serverID: string, body: ExtensionMCPReviewRequestView,
    signal?: AbortSignal): Promise<ExtensionMCPServerView> {
    if (!this.hasExtensionControl || !boundedIdentity(serverID) ||
      body.version !== "extension-control.v1" || !isSHA256(body.expected_descriptor_fingerprint) ||
      (body.expected_capability_fingerprint !== undefined &&
        !isSHA256(body.expected_capability_fingerprint))) {
      throw new Error("An exact MCP review request and extension control are required");
    }
    return parseExtensionMCPServer(await this.sendControlRequest<unknown>(
      `/extensions/mcp/${encodeURIComponent(serverID)}/review`, body, signal,
    ));
  }

  async refreshMCPServer(serverID: string,
    signal?: AbortSignal): Promise<ExtensionMCPServerView> {
    if (!this.hasExtensionControl || !boundedIdentity(serverID)) {
      throw new Error("A normalized MCP server and extension control are required");
    }
    return parseExtensionMCPServer(await this.sendControlRequest<unknown>(
      `/extensions/mcp/${encodeURIComponent(serverID)}/refresh`,
      { version: "extension-control.v1" }, signal,
    ));
  }

  async reviewPluginInstallation(installationID: string,
    body: ExtensionPluginReviewRequestView,
    signal?: AbortSignal): Promise<ExtensionPluginInstallationView> {
    if (!this.hasExtensionControl || !boundedIdentity(installationID) ||
      body.version !== "extension-control.v1" ||
      !isSHA256(body.expected_package_fingerprint) ||
      !safePositiveInteger(body.expected_generation)) {
      throw new Error("An exact Plugin review request and extension control are required");
    }
    return parseExtensionPlugin(await this.sendControlRequest<unknown>(
      `/extensions/plugins/${encodeURIComponent(installationID)}/review`, body, signal,
    ));
  }

  async safeWebReadiness(product: string, signal?: AbortSignal): Promise<BrowserSafeWebReadiness> {
    return parseSafeWebReadiness(await this.get<unknown>("/browser/safe-web-readiness",
      { product }, signal));
  }

  async uiEvidence(runID: string, signal?: AbortSignal): Promise<UIEvidenceAttempt[]> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const value = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/ui-evidence`, { limit: 100 }, signal);
    if (!Array.isArray(value) || value.length > 500) {
      throw new APIRequestError("UI evidence list is invalid", "INVALID_RESPONSE", 502);
    }
    const attempts = value.map((entry) => parseUIEvidenceAttempt(entry, runID));
    if (new Set(attempts.map((entry) => entry.manifest.attempt_id)).size !== attempts.length) {
      throw new APIRequestError("UI evidence list contains duplicate attempts", "INVALID_RESPONSE", 502);
    }
    return attempts;
  }

  async uiEvidenceBundle(attemptID: string, signal?: AbortSignal): Promise<UIEvidenceBundle> {
    if (!boundedIdentity(attemptID) || attemptID.trim() !== attemptID) {
      throw new Error("A normalized UI evidence attempt identity is required");
    }
    return parseUIEvidenceBundle(await this.get<unknown>(
      `/ui-evidence/${encodeURIComponent(attemptID)}`, {}, signal), attemptID);
  }

  async startUIEvidence(runID: string, body: UIEvidenceStartView,
    signal?: AbortSignal): Promise<UIEvidenceAttempt> {
    if (!this.hasUIEvidence || !boundedIdentity(runID) || runID.trim() !== runID ||
      typeof body.operation_key !== "string" || body.operation_key.trim() !== body.operation_key ||
      body.operation_key.length < 1) {
      throw new Error("UI evidence control, a normalized Run, and an operation key are required");
    }
    return parseUIEvidenceAttempt(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/ui-evidence`, body, body.operation_key, signal), runID);
  }

  async cancelUIEvidence(attemptID: string, signal?: AbortSignal): Promise<UIEvidenceAttempt> {
    if (!this.hasUIEvidence || !boundedIdentity(attemptID) || attemptID.trim() !== attemptID) {
      throw new Error("UI evidence control and a normalized attempt identity are required");
    }
    return parseUIEvidenceAttempt(await this.sendControl<unknown>(
      `/ui-evidence/${encodeURIComponent(attemptID)}/cancel`, { confirm: true },
      `ui-evidence-cancel-${attemptID}`, signal));
  }

  async downloadUIEvidenceArtifact(attemptID: string, metadata: UIEvidenceArtifactMetadata,
    signal?: AbortSignal): Promise<Blob> {
    if (!boundedIdentity(attemptID) || attemptID !== metadata.attempt_id ||
      !boundedIdentity(metadata.id) || !isSHA256(metadata.sha256) || metadata.untrusted !== true) {
      throw new Error("Exact untrusted UI evidence artifact metadata is required");
    }
    const response = await fetch(this.url(`/ui-evidence/${encodeURIComponent(attemptID)}/artifacts/` +
      encodeURIComponent(metadata.id)), {
      method: "GET",
      headers: { ...this.headers(), Accept: metadata.mime },
      signal,
      cache: "no-store",
      credentials: "omit",
      referrerPolicy: "no-referrer",
    });
    if (!response.ok) throw await this.responseError(response);
    if (response.headers.get("content-type") !== metadata.mime ||
      response.headers.get("etag") !== `"${metadata.sha256}"` ||
      response.headers.get("x-cyberagent-content-sha256") !== metadata.sha256 ||
      response.headers.get("x-cyberagent-evidence-untrusted") !== "true") {
      throw new APIRequestError("UI evidence artifact headers are invalid", "INVALID_RESPONSE",
        response.status, response.headers.get("x-request-id") || "");
    }
    const declaredLength = response.headers.get("content-length");
    if (declaredLength !== null && Number(declaredLength) !== metadata.bytes) {
      throw new APIRequestError("UI evidence artifact byte count changed", "INVALID_RESPONSE", 502);
    }
    if (!response.body) {
      throw new APIRequestError("UI evidence artifact body is unavailable", "INVALID_RESPONSE", 502);
    }
    const bytes = new Uint8Array(metadata.bytes);
    const reader = response.body.getReader();
    let offset = 0;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        if (offset + value.byteLength > bytes.byteLength) {
          await reader.cancel();
          throw new APIRequestError("UI evidence artifact exceeded its sealed byte count",
            "INVALID_RESPONSE", 502);
        }
        bytes.set(value, offset);
        offset += value.byteLength;
      }
    } finally {
      reader.releaseLock();
    }
    if (offset !== metadata.bytes) {
      throw new APIRequestError("UI evidence artifact byte count changed", "INVALID_RESPONSE", 502);
    }
    const digest = new Uint8Array(await globalThis.crypto.subtle.digest("SHA-256", bytes));
    const digestHex = [...digest].map((byte) => byte.toString(16).padStart(2, "0")).join("");
    if (digestHex !== metadata.sha256) {
      throw new APIRequestError("UI evidence artifact digest verification failed", "INVALID_RESPONSE", 502);
    }
    return new Blob([bytes], { type: metadata.mime });
  }

  async modelAvailability(signal?: AbortSignal): Promise<ModelAvailabilityView> {
    const value = await this.get<unknown>("/models", {}, signal);
    return parseModelAvailability(value);
  }

  async getArtifact(artifactID: string, signal?: AbortSignal): Promise<ArtifactView> {
    const normalized = boundedIdentity(artifactID);
    if (!normalized || normalized !== artifactID) {
      throw new Error("A normalized artifact identity is required");
    }
    const value = await this.get<unknown>(
      `/artifacts/${encodeURIComponent(artifactID)}`, {}, signal,
    );
    if (!isRecord(value) || value.id !== artifactID || !boundedIdentity(value.run_id)) {
      throw new APIRequestError("Artifact response is invalid", "INVALID_RESPONSE", 502);
    }
    return value as unknown as ArtifactView;
  }

  async getNote(noteID: string, signal?: AbortSignal): Promise<NoteView> {
    const normalized = boundedIdentity(noteID);
    if (!normalized || normalized !== noteID) {
      throw new Error("A normalized note identity is required");
    }
    const value = await this.get<unknown>(
      `/notes/${encodeURIComponent(noteID)}`, {}, signal,
    );
    if (!isRecord(value) || value.id !== noteID || !boundedIdentity(value.run_id)) {
      throw new APIRequestError("Note response is invalid", "INVALID_RESPONSE", 502);
    }
    return value as unknown as NoteView;
  }

  async getWorkItem(workItemID: string, signal?: AbortSignal): Promise<WorkItemView> {
    const normalized = boundedIdentity(workItemID);
    if (!normalized || normalized !== workItemID) {
      throw new Error("A normalized Plan item identity is required");
    }
    const value = await this.get<unknown>(
      `/work-items/${encodeURIComponent(workItemID)}`, {}, signal,
    );
    if (!isRecord(value) || value.id !== workItemID || !boundedIdentity(value.run_id)) {
      throw new APIRequestError("Plan item response is invalid", "INVALID_RESPONSE", 502);
    }
    return value as unknown as WorkItemView;
  }

  async getRunExternalSkills(runID: string,
    signal?: AbortSignal): Promise<ExternalSkillProjectionView> {
    const normalized = boundedIdentity(runID);
    if (!normalized || normalized !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const value = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/external-skills`, {}, signal,
    );
    if (!isRecord(value) || value.run_id !== runID ||
      value.protocol_version !== "external_skill_projection.v1") {
      throw new APIRequestError("External skill projection response is invalid",
        "INVALID_RESPONSE", 502);
    }
    return value as unknown as ExternalSkillProjectionView;
  }

  async workspaceExplore(workspaceID: string, path = ".",
    signal?: AbortSignal): Promise<WorkspaceExplorerView> {
    if (!boundedIdentity(workspaceID) || !validWorkspaceRelativePath(path)) {
      throw new Error("A normalized Workspace identity and Go-issued relative path are required");
    }
    return parseWorkspaceExplorer(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/explore`, { path }, signal,
    ), workspaceID, path);
  }

  async workspaceSearch(workspaceID: string, query: string,
    signal?: AbortSignal): Promise<WorkspaceSearchView> {
    if (!boundedIdentity(workspaceID) || !boundedText(query, 128) ||
      /[\u0000-\u001f\u007f]/u.test(query)) {
      throw new Error("A normalized Workspace identity and bounded query are required");
    }
    return parseWorkspaceSearch(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/search`, { query }, signal,
    ), workspaceID);
  }

  async repositoryState(workspaceID: string,
    signal?: AbortSignal): Promise<RepositoryStateView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID) {
      throw new Error("A normalized Workspace identity is required");
    }
    return parseRepositoryState(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-state`, {}, signal,
    ), workspaceID);
  }

  async repositoryDiff(workspaceID: string,
    signal?: AbortSignal): Promise<RepositoryDiffView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID) {
      throw new Error("A normalized Workspace identity is required");
    }
    return parseRepositoryDiff(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-diff`, {}, signal,
    ), workspaceID);
  }

  async repositoryHistory(workspaceID: string,
    signal?: AbortSignal): Promise<RepositoryHistoryView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID) {
      throw new Error("A normalized Workspace identity is required");
    }
    return parseRepositoryHistory(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-history`, {}, signal,
    ), workspaceID);
  }

  async repositoryCommit(workspaceID: string, objectID: string,
    signal?: AbortSignal): Promise<RepositoryCommitDetailView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID ||
      !/^[0-9a-f]{40}$/u.test(objectID)) {
      throw new Error("A normalized Workspace identity and exact commit object are required");
    }
    return parseRepositoryCommitDetail(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-commits/${objectID}`, {}, signal,
    ), workspaceID, objectID);
  }

  async repositoryCommitComparison(workspaceID: string, baseObjectID: string,
    headObjectID: string, signal?: AbortSignal): Promise<RepositoryCommitComparisonView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID ||
      !/^[0-9a-f]{40}$/u.test(baseObjectID) || !/^[0-9a-f]{40}$/u.test(headObjectID)) {
      throw new Error("Normalized Workspace and exact commit identities are required");
    }
    return parseRepositoryCommitComparison(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-commit-comparison`,
      { base_object_id: baseObjectID, head_object_id: headObjectID }, signal,
    ), workspaceID, baseObjectID, headObjectID);
  }

  async repositoryFileHistory(workspaceID: string, path: string,
    signal?: AbortSignal): Promise<RepositoryFileHistoryView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID ||
      !validWorkspaceRelativePath(path) || path === ".") {
      throw new Error("A normalized Workspace identity and canonical file path are required");
    }
    return parseRepositoryFileHistory(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-file-history`, { path }, signal,
    ), workspaceID, path);
  }

  async repositoryCommitFilePreview(workspaceID: string, objectID: string, path: string,
    signal?: AbortSignal): Promise<RepositoryCommitFilePreviewView> {
    if (!boundedIdentity(workspaceID) || workspaceID.trim() !== workspaceID ||
      !/^[0-9a-f]{40}$/u.test(objectID) || !validWorkspaceRelativePath(path) || path === ".") {
      throw new Error("A normalized Workspace, exact commit object, and canonical file path are required");
    }
    return parseRepositoryCommitFilePreview(await this.get<unknown>(
      `/workspaces/${encodeURIComponent(workspaceID)}/repository-commits/${objectID}/file-preview`,
      { path }, signal,
    ), workspaceID, objectID, path);
  }

  async operationReceiptHistory(runID = "",
    signal?: AbortSignal): Promise<OperationReceiptHistoryView> {
    if (runID !== "" && (!boundedIdentity(runID) || runID.trim() !== runID)) {
      throw new Error("A normalized Run identity is required");
    }
    return parseOperationReceiptHistory(await this.get<unknown>(
      "/operation-receipts", { run_id: runID || undefined, limit: 100 }, signal,
    ), runID);
  }

  async operatorActionCenter(runID: string,
    signal?: AbortSignal): Promise<OperatorActionCenterView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseOperatorActionCenter(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/operator-actions`, {}, signal,
    ), runID);
  }

  async evidenceInventory(runID: string,
    signal?: AbortSignal): Promise<EvidenceInventoryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseEvidenceInventory(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/evidence-attachments`, {}, signal,
    ), runID);
  }

  async verificationEvidence(runID: string,
    signal?: AbortSignal): Promise<VerificationEvidenceInventoryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseVerificationEvidenceInventory(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-evidence`, {}, signal,
    ), runID);
  }

  async recordVerificationEvidence(runID: string, body: VerificationEvidenceRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<VerificationEvidenceControlView> {
    if (!this.hasVerificationEvidence || !boundedIdentity(runID) ||
      body.version !== "operator_verification_evidence.v1" ||
      !["pass", "fail", "unknown"].includes(body.outcome) ||
      !validVerificationText(body.title, 160, false) ||
      !validVerificationText(body.summary, 2048, true)) {
      throw new Error("Verification evidence capability and a bounded observation are required");
    }
    return parseVerificationEvidenceItem(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-evidence`, body, idempotencyKey, signal,
    ), runID, "", "", true);
  }

  async verificationPlans(runID: string,
    signal?: AbortSignal): Promise<VerificationPlanInventoryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseVerificationPlanInventory(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-plan`, {}, signal,
    ), runID);
  }

  async recordVerificationPlan(runID: string, body: VerificationPlanRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<VerificationPlanControlView> {
    if (!this.hasVerificationEvidence || !boundedIdentity(runID) ||
      body.version !== "operator_verification_plan.v1" ||
      !validVerificationText(body.title, 160, false) ||
      !validVerificationText(body.summary, 2048, true) || !Array.isArray(body.items) ||
      body.items.length < 1 || body.items.length > 32 || body.items.some((item) =>
        !validVerificationText(item.title, 160, false) ||
        !validVerificationText(item.expected_observation, 1024, true))) {
      throw new Error("Verification capability and a bounded operator checklist are required");
    }
    return parseVerificationPlan(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-plan`, body,
      idempotencyKey, signal), runID, "", "", true);
  }

  async verificationPlanCoverage(runID: string,
    signal?: AbortSignal): Promise<VerificationPlanCoverageInventoryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseVerificationPlanCoverage(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-plan-coverage`, {}, signal,
    ), runID);
  }

  async verificationPlanItemCoverage(runID: string, planID: string, ordinal: number,
    signal?: AbortSignal): Promise<VerificationPlanItemCoverageDetailView> {
    return (await this.verificationPlanItemCoveragePage(runID, planID, ordinal, "", 50,
      signal)).detail;
  }

  async verificationPlanItemCoveragePage(runID: string, planID: string, ordinal: number,
    cursor = "", limit = 50,
    signal?: AbortSignal): Promise<VerificationPlanItemCoveragePage> {
    if (!boundedIdentity(runID) || runID.trim() !== runID || !boundedIdentity(planID) ||
      planID.trim() !== planID || !safePositiveInteger(ordinal) || ordinal > 32 ||
      !safePositiveInteger(limit) || limit > 100 || typeof cursor !== "string" ||
      cursor.length > 512 || cursor.trim() !== cursor) {
      throw new Error("Normalized Run, plan, and item identities are required");
    }
    const envelope = await this.request<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-plan-coverage/` +
        `${encodeURIComponent(planID)}/items/${ordinal}`,
      { limit: limit === 50 ? undefined : limit, cursor: cursor || undefined }, signal);
    if (!envelope.page) {
      throw new APIRequestError("Verification coverage page metadata is missing",
        "INVALID_RESPONSE", 502, envelope.request_id);
    }
    const page = parseResponsePage(envelope.page, limit);
    return { detail: parseVerificationPlanItemCoverage(envelope.data, runID, planID, ordinal,
      page, cursor === ""), page, requestID: envelope.request_id };
  }

  async verificationPlanItemSnapshotExport(runID: string, planID: string, ordinal: number,
    format: "json" | "markdown", signal?: AbortSignal): Promise<VerificationSnapshotExportView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID || !boundedIdentity(planID) ||
      planID.trim() !== planID || !safePositiveInteger(ordinal) || ordinal > 32 ||
      !["json", "markdown"].includes(format)) {
      throw new Error("Normalized Run, plan, item, and snapshot format are required");
    }
    return parseVerificationSnapshotExport(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-plan-coverage/` +
        `${encodeURIComponent(planID)}/items/${ordinal}/snapshot-export`, { format }, signal,
    ), runID, planID, ordinal, format);
  }

  async verificationSnapshotReceipts(runID: string,
    signal?: AbortSignal): Promise<VerificationSnapshotReceiptInventoryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseVerificationSnapshotReceiptInventory(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-snapshot-receipts`, {}, signal,
    ), runID);
  }

  async recordVerificationSnapshotReceipt(runID: string,
    body: VerificationSnapshotReceiptRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<VerificationSnapshotReceiptControlView> {
    if (!this.hasVerificationEvidence || !boundedIdentity(runID) ||
      body.version !== "operator_verification_plan_item_snapshot_receipt.v1" ||
      !boundedIdentity(body.plan_id) || !safePositiveInteger(body.plan_item_ordinal) ||
      body.plan_item_ordinal > 32 || !["json", "markdown"].includes(body.format) ||
      !safeBoundedCount(body.snapshot_high_water_event_sequence, Number.MAX_SAFE_INTEGER) ||
      !isSHA256(body.content_sha256) || body.confirm_metadata_snapshot !== true) {
      throw new Error("Verification capability and an exact confirmed snapshot digest are required");
    }
    return parseVerificationSnapshotReceipt(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-snapshot-receipts`, body,
      idempotencyKey, signal), runID, "", "", true);
  }

  async verificationSnapshotReceiptReviews(runID: string,
    signal?: AbortSignal): Promise<VerificationSnapshotReceiptReviewInventoryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseVerificationSnapshotReceiptReviewInventory(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-snapshot-receipt-reviews`, {}, signal,
    ), runID);
  }

  async recordVerificationSnapshotReceiptReview(runID: string,
    body: VerificationSnapshotReceiptReviewRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<VerificationSnapshotReceiptReviewControlView> {
    if (!this.hasVerificationEvidence || !boundedIdentity(runID) ||
      body.version !== "operator_verification_plan_item_snapshot_receipt_review.v1" ||
      !boundedIdentity(body.receipt_id) || !isSHA256(body.receipt_content_sha256) ||
      !safePositiveInteger(body.receipt_event_sequence) ||
      !["metadata_confirmed", "metadata_disputed"].includes(body.decision) ||
      body.confirm_non_authorizing_review !== true) {
      throw new Error("Verification capability and an exact non-authorizing review are required");
    }
    return parseVerificationSnapshotReceiptReview(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-snapshot-receipt-reviews`, body,
      idempotencyKey, signal), runID, "", "", true);
  }

  async associateVerificationEvidence(runID: string, body: VerificationAssociationRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<VerificationAssociationControlView> {
    if (!this.hasVerificationEvidence || !boundedIdentity(runID) ||
      body.version !== "operator_verification_plan_evidence_association.v1" ||
      !boundedIdentity(body.plan_id) || !safePositiveInteger(body.plan_item_ordinal) ||
      body.plan_item_ordinal > 32 || !boundedIdentity(body.evidence_id)) {
      throw new Error("Verification capability and exact plan/evidence identities are required");
    }
    return parseVerificationAssociation(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/verification-plan-associations`, body,
      idempotencyKey, signal), runID, true);
  }

  async codeHandoff(runID: string, signal?: AbortSignal): Promise<CodeHandoffView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseCodeHandoff(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/code-handoff`, {}, signal,
    ), runID);
  }

  async standardCodeDelivery(runID: string,
    signal?: AbortSignal): Promise<StandardCodeDeliveryView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseStandardCodeDelivery(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/standard-code-delivery`, {}, signal,
    ), runID);
  }

  async recordStandardCodeDelivery(runID: string, body: StandardCodeDeliveryRecordRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<StandardCodeDeliveryRecordResultView> {
    if (!this.hasWorkspaceCheckpointControl || !boundedIdentity(runID) ||
      runID.trim() !== runID || typeof body.operation_key !== "string" ||
      body.operation_key.trim() !== body.operation_key || body.operation_key.length === 0 ||
      !Array.isArray(body.verification_job_ids) || !Array.isArray(body.uncovered_items)) {
      throw new Error("Standard Code delivery control and a bounded exact intent are required");
    }
    const value = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/standard-code-delivery`, body, idempotencyKey, signal);
    if (!isRecord(value) || !hasExactKeys(value, ["replayed", "report"]) ||
      typeof value.replayed !== "boolean") {
      throw new APIRequestError("Invalid Standard Code delivery record response",
        "INVALID_RESPONSE", 502);
    }
    return { replayed: value.replayed,
      report: parseStandardCodeDelivery(value.report, runID) };
  }

  async codeHandoffExport(runID: string, format: "json" | "markdown",
    signal?: AbortSignal): Promise<CodeHandoffExportView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID ||
      !["json", "markdown"].includes(format)) {
      throw new Error("A normalized Run identity and supported handoff format are required");
    }
    return parseCodeHandoffExport(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/code-handoff/export`, { format }, signal,
    ), runID, format);
  }

  async attachEvidence(runID: string, body: EvidenceAttachmentRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<EvidenceAttachmentView> {
    if (!this.hasEvidenceAttachment || !boundedIdentity(runID) ||
      body.version !== "session_evidence_attachment.v1" ||
      body.source_kind !== "workspace_file" || !validWorkspaceRelativePath(body.source_ref) ||
      !isSHA256(body.content_sha256)) {
      throw new Error("Evidence attachment capability and exact Workspace provenance are required");
    }
    return parseEvidenceAttachment(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/evidence-attachments`, body, idempotencyKey, signal,
    ), runID, body);
  }

  async selectModelRoute(route: string, body: ModelRouteControlRequestView,
    signal?: AbortSignal): Promise<ModelAvailabilityView["routes"][number]> {
    if (!this.hasModelControl || !boundedIdentity(route) || route.trim() !== route) {
      throw new Error("Model control capability and a normalized route are required");
    }
    const result = await this.sendControlRequest<unknown>(
      `/models/routes/${encodeURIComponent(route)}`, body, signal,
    );
    return parseModelRouteControl(result, route, body);
  }

  async availableModelRoutes(signal?: AbortSignal): Promise<AvailableModelRouteCollectionView> {
    if (!this.hasModelControl) {
      throw new Error("Model control capability is required to list selectable routes");
    }
    return parseAvailableModelRouteCollection(await this.get<unknown>(
      "/models/routes/available", {}, signal));
  }

  async threadModelRoute(threadID: string, signal?: AbortSignal): Promise<ThreadModelRouteView> {
    if (!this.hasModelControl || !boundedIdentity(threadID) || threadID.trim() !== threadID) {
      throw new Error("Model control capability and a normalized Thread are required");
    }
    return parseThreadModelRoute(await this.get<unknown>(
      `/threads/${encodeURIComponent(threadID)}/model-route`, {}, signal), threadID);
  }

  async providerSearchReadiness(threadID: string,
    signal?: AbortSignal): Promise<ProviderSearchReadinessView> {
    if (!boundedIdentity(threadID) || threadID.trim() !== threadID) {
      throw new Error("A normalized Thread is required to read search readiness");
    }
    return parseProviderSearchReadiness(await this.get<unknown>(
      `/threads/${encodeURIComponent(threadID)}/search-readiness`, {}, signal), threadID);
  }

  async selectThreadModelRoute(threadID: string, body: ThreadModelRouteControlRequestView,
    signal?: AbortSignal): Promise<ThreadModelRouteView> {
    const select = body.action === "select";
    const reset = body.action === "reset";
    if (!this.hasModelControl || !boundedIdentity(threadID) || threadID.trim() !== threadID ||
      body.version !== "thread_model_route_control.v1" || (!select && !reset) ||
      !boundedIdentity(body.operation_key) || !boundedIdentity(body.requested_by) ||
      (select && (!boundedIdentity(body.provider) || !boundedIdentity(body.model))) ||
      (reset && (body.provider !== undefined || body.model !== undefined))) {
      throw new Error("A normalized, exact Thread model route intent is required");
    }
    const result = await this.sendControlRequest<unknown>(
      `/threads/${encodeURIComponent(threadID)}/model-route`, body, signal, "", "PUT");
    return parseThreadModelRoute(result, threadID, body);
  }

  async diagnoseProvider(body: ProviderDiagnosticRequestView,
    signal?: AbortSignal): Promise<ProviderDiagnosticView> {
    if (!this.hasModelControl || body.confirm_diagnostic !== true) {
      throw new Error("Explicit Provider diagnostic confirmation is required");
    }
    const result = await this.sendControlRequest<unknown>("/models/diagnostics", body, signal);
    return parseProviderDiagnostic(result, body);
  }

  async qualifyModelHarness(body: ModelHarnessQualificationRequestView,
    signal?: AbortSignal): Promise<ModelHarnessQualificationView> {
    if (!this.hasModelControl || body.confirm_qualification !== true) {
      throw new Error("Explicit model Harness qualification confirmation is required");
    }
    const result = await this.sendControlRequest<unknown>(
      "/models/harness-qualifications", body, signal,
    );
    return parseModelHarnessQualification(result, body);
  }

  async providerDefinitions(signal?: AbortSignal): Promise<ProviderDefinitionCollectionView> {
    if (!this.hasProviderDefinitions) {
      throw new Error("Custom Provider definition capability is required");
    }
    return parseProviderDefinitionCollection(await this.get<unknown>(
      "/models/provider-definitions", {}, signal));
  }

  async upsertProviderDefinition(provider: string, body: ProviderDefinitionUpsertRequestView,
    signal?: AbortSignal): Promise<ProviderDefinitionMutationView> {
    if (!this.hasProviderDefinitions || !validCustomProviderID(provider) ||
      body.version !== "provider_definition_control.v1" || body.confirm !== true ||
      body.definition.id !== provider) {
      throw new Error("Confirmed custom Provider definition capability is required");
    }
    parseProviderDefinition(body.definition);
    return parseProviderDefinitionMutation(await this.sendControlRequest<unknown>(
      `/models/provider-definitions/${encodeURIComponent(provider)}`, body, signal,
    ), provider, false);
  }

  async deleteProviderDefinition(provider: string, body: ProviderDefinitionDeleteRequestView,
    signal?: AbortSignal): Promise<ProviderDefinitionMutationView> {
    if (!this.hasProviderDefinitions || !validCustomProviderID(provider) ||
      body.version !== "provider_definition_control.v1" || body.confirm !== true ||
      !safeBoundedCount(body.expected_collection_revision, Number.MAX_SAFE_INTEGER) ||
      !safePositiveInteger(body.expected_definition_revision)) {
      throw new Error("Confirmed custom Provider deletion capability is required");
    }
    return parseProviderDefinitionMutation(await this.sendControlRequest<unknown>(
      `/models/provider-definitions/${encodeURIComponent(provider)}/delete`, body, signal,
    ), provider, true);
  }

  async providerCredentialStatuses(signal?: AbortSignal): Promise<ProviderCredentialListView> {
    if (!this.hasProviderCredentials) {
      throw new Error("Provider credential capability is required");
    }
    return parseProviderCredentialList(await this.get<unknown>("/models/credentials", {}, signal));
  }

  async changeProviderCredential(provider: string, body: ProviderCredentialRequestView,
    signal?: AbortSignal): Promise<ProviderCredentialStatusView> {
    if (!this.hasProviderCredentials ||
      !validProviderCredentialName(provider) ||
      body.version !== "provider_credential.v1" || body.confirm !== true ||
      (body.action === "set" ? typeof body.secret !== "string" || body.secret.length < 8 :
        body.action !== "delete" || body.secret !== "")) {
      throw new Error("Confirmed Provider credential capability is required");
    }
    const result = await this.sendControlRequest<unknown>(
      `/models/credentials/${encodeURIComponent(provider)}`, body, signal,
    );
    const status = parseProviderCredentialStatus(result, provider);
    const applied = status.registry_reloaded && !status.restart_required &&
      status.registry_generation > 0;
    const deferred = !status.registry_reloaded && status.restart_required;
    if (status.configured !== (body.action === "set") || (!applied && !deferred)) {
      throw new APIRequestError("Provider credential change returned the wrong status",
        "INVALID_RESPONSE", 502);
    }
    return status;
  }

  async fileEditQueue(runID: string, signal?: AbortSignal): Promise<FileEditQueueView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseFileEditQueue(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edits`, {}, signal,
    ), runID);
  }

  async fileEdit(runID: string, editID: string,
    signal?: AbortSignal): Promise<FileEditPreviewView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(editID) || editID.trim() !== editID) {
      throw new Error("Normalized Run and file edit identities are required");
    }
    const edit = parseFileEditPreview(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edits/${encodeURIComponent(editID)}`,
      {}, signal,
    ));
    if (edit.id !== editID) {
      throw new APIRequestError("File edit preview returned the wrong identity",
        "INVALID_RESPONSE", 502);
    }
    return edit;
  }

  async fileEditChangeSet(runID: string,
    signal?: AbortSignal): Promise<FileEditChangeSetView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseFileEditChangeSet(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edit-change-set`, {}, signal,
    ), runID);
  }

  async issueFileEditProposalSource(runID: string, path: string,
    signal?: AbortSignal): Promise<FileEditProposalSourceView> {
    if (!this.hasFileEditProposals || !boundedIdentity(runID) ||
      !validWorkspaceRelativePath(path)) {
      throw new Error("File edit proposal capability and a Go-issued path are required");
    }
    return parseFileEditProposalSource(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edit-proposal-source`, { path }, signal,
    ), runID, path);
  }

  async reissueFileEditProposalSource(runID: string, path: string, expectedSHA256: string,
    signal?: AbortSignal): Promise<FileEditProposalSourceView> {
    if (!this.hasFileEditProposals || !boundedIdentity(runID) ||
      !validWorkspaceRelativePath(path) || !isSHA256(expectedSHA256)) {
      throw new Error("An exact previously issued file digest is required");
    }
    return parseFileEditProposalSource(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edit-proposal-source`,
      { path, expected_sha256: expectedSHA256 }, signal,
    ), runID, path);
  }

  async recoverFileEditProposal(runID: string, editID: string,
    signal?: AbortSignal): Promise<FileEditProposalRecoveryView> {
    if (!this.hasFileEditProposals || !boundedIdentity(runID) || !boundedIdentity(editID)) {
      throw new Error("File edit recovery requires exact Run and proposal identities");
    }
    return parseFileEditProposalRecovery(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edit-proposal-recovery/${encodeURIComponent(editID)}`,
      {}, signal,
    ), runID, editID);
  }

  async createFileEditProposal(runID: string, body: FileEditProposalRequestView,
    signal?: AbortSignal): Promise<FileEditProposalView> {
    if (!this.hasFileEditProposals || !boundedIdentity(runID) ||
      body.version !== "file_edit_proposal.v1" ||
      !/^[A-Za-z0-9_-]{43}$/u.test(body.source_handle) ||
      typeof body.proposed_text !== "string" ||
      new TextEncoder().encode(body.proposed_text).length > 256 * 1024) {
      throw new Error("A bounded Go-issued file edit proposal is required");
    }
    return parseFileEditProposal(await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edit-proposals`, body, signal,
    ), runID);
  }

  async reviewFileEdit(runID: string, editID: string, body: FileEditReviewRequestView,
    signal?: AbortSignal): Promise<FileEditReviewView> {
    if (!this.hasFileEditReview || !boundedIdentity(runID) || !boundedIdentity(editID)) {
      throw new Error("File edit review capability and normalized identities are required");
    }
    const result = await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edits/${encodeURIComponent(editID)}/review`,
      body, signal,
    );
    return parseFileEditReview(result, runID, editID, body);
  }

  async applyFileEdit(runID: string, editID: string, body: FileEditApplyRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<FileEditApplyView> {
    if (!this.hasFileEditApply || !boundedIdentity(runID) || !boundedIdentity(editID)) {
      throw new Error("File edit apply capability and normalized identities are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/file-edits/${encodeURIComponent(editID)}/apply`,
      body, idempotencyKey, signal,
    );
    return parseFileEditApply(result, runID, editID);
  }

  async runWakeState(runID: string, signal?: AbortSignal): Promise<RunWakeStateView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    return parseRunWakeState(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/wake-intent`, {}, signal,
    ), runID);
  }

  async scheduleRunWake(runID: string, body: RunWakeScheduleRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<RunWakeControlView> {
    if (!this.hasRunWakeControl) {
      throw new Error("Run wake control capability is required");
    }
    return parseRunWakeControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/wake-intent`, body, idempotencyKey, signal,
    ), runID, "schedule");
  }

  async cancelRunWake(runID: string, body: RunWakeCancelRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<RunWakeControlView> {
    if (!this.hasRunWakeControl) {
      throw new Error("Run wake control capability is required");
    }
    return parseRunWakeControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/wake-intent/cancel`, body, idempotencyKey, signal,
    ), runID, "cancel");
  }

  async consumeRunWake(runID: string, body: RunWakeExecutionRequestView,
    signal?: AbortSignal): Promise<RunWakeExecutionView> {
    if (!this.hasRunWakeExecution || !boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("Foreground Run wake capability and a normalized Run are required");
    }
    return parseRunWakeExecution(await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/wake-intent/consume`, body, signal,
    ), runID);
  }

  async listScheduledJobs(runID = "", limit = 50,
    signal?: AbortSignal): Promise<ScheduledJobListView> {
    if ((runID !== "" && boundedIdentity(runID) !== runID) ||
      !Number.isSafeInteger(limit) || limit < 1 || limit > 100) {
      throw new Error("A normalized Run identity and a schedule limit between 1 and 100 are required");
    }
    return parseScheduledJobList(await this.get<unknown>("/scheduled-jobs", {
      run_id: runID || undefined, limit,
    }, signal));
  }

  async getScheduledJob(jobID: string, signal?: AbortSignal): Promise<ScheduledJobDetailView> {
    if (boundedIdentity(jobID) !== jobID) {
      throw new Error("A normalized scheduled job identity is required");
    }
    return parseScheduledJobDetail(await this.get<unknown>(
      `/scheduled-jobs/${encodeURIComponent(jobID)}`,
      { round_limit: 20, notification_limit: 20 }, signal,
    ), jobID);
  }

  async createScheduledJob(runID: string, body: ScheduledJobCreateRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<ScheduledJobControlView> {
    if (!this.hasScheduledJobControl || boundedIdentity(runID) !== runID ||
      body.version !== "scheduled-job.v1") {
      throw new Error("Scheduled job control and a normalized Run identity are required");
    }
    return parseScheduledJobControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/scheduled-jobs`, body, idempotencyKey, signal,
    ), runID, "", "create");
  }

  async transitionScheduledJob(runID: string, jobID: string,
    action: "pause" | "resume" | "cancel", body: ScheduledJobTransitionRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<ScheduledJobControlView> {
    if (!this.hasScheduledJobControl || boundedIdentity(runID) !== runID ||
      boundedIdentity(jobID) !== jobID || body.version !== "scheduled-job-control.v1" ||
      !Number.isSafeInteger(body.expected_revision) || body.expected_revision < 1) {
      throw new Error("Scheduled job control, normalized identities, and a revision are required");
    }
    return parseScheduledJobControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/scheduled-jobs/${encodeURIComponent(jobID)}/${action}`,
      body, idempotencyKey, signal,
    ), runID, jobID, action);
  }

  async diagnosticBundle(runID: string, signal?: AbortSignal): Promise<DiagnosticBundleView> {
    if (boundedIdentity(runID) !== runID) {
      throw new Error("A normalized Run identity is required for diagnostic export");
    }
    return parseDiagnosticBundle(await this.get<unknown>("/diagnostic-bundle", {
      run_id: runID, limit: 100,
    }, signal), runID);
  }

  async installSkillPackage(body: SkillPackageInstallRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<SkillPackageInstallView> {
    if (!this.hasSkillInstallation || body.confirm_untrusted !== true) {
      throw new Error("Explicit untrusted Skill installation capability is required");
    }
    return parseSkillPackageInstall(await this.sendControl<unknown>(
      "/skills/packages/install", body, idempotencyKey, signal,
    ), body);
  }

  async executeEmbeddedAnalyzer(runID: string,
    body: EmbeddedAnalyzerExecutionRequestView, signal?: AbortSignal,
  ): Promise<EmbeddedAnalyzerExecutionControlView> {
    if (!this.hasEmbeddedAnalyzerExecution) {
      throw new Error("Embedded analyzer execution capability is required");
    }
    const normalizedRunID = boundedIdentity(runID);
    if (!normalizedRunID || normalizedRunID !== runID ||
      body.version !== "embedded_analyzer_operator_request.v1" ||
      body.confirmation !== "RUN-EMBEDDED-ANALYZER" ||
      ((body.text ?? "") === "") === ((body.file ?? "").trim() === "")) {
      throw new Error("A normalized Run and one explicitly confirmed analyzer input are required");
    }
    const result = await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/analyzer-executions`, body, signal,
    );
    return parseEmbeddedAnalyzerExecution(result, runID);
  }

  async get<T>(path: string, query: Record<string, QueryValue> = {}, signal?: AbortSignal): Promise<T> {
    const envelope = await this.request<T>(path, query, signal);
    return envelope.data;
  }

  async getPage<T>(
    path: string,
    query: Record<string, QueryValue> = {},
    cursor = "",
    signal?: AbortSignal,
  ): Promise<PageResult<T>> {
    const envelope = await this.request<T[]>(path, { ...query, cursor: cursor || undefined }, signal);
    if (!envelope.page || !Array.isArray(envelope.data)) {
      throw new APIRequestError("API collection response omitted pagination metadata", "INVALID_RESPONSE", 502,
        envelope.request_id);
    }
    const items = /^\/threads\/[^/]+\/transcript$/u.test(path)
      ? parseThreadTranscriptItems(envelope.data as unknown[]) as T[]
      : envelope.data;
    return { items, page: envelope.page, requestID: envelope.request_id };
  }

  async postControl<T>(
    path: string,
    body: unknown,
    idempotencyKey: string,
    signal?: AbortSignal,
  ): Promise<T> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    return this.sendControl<T>(path, body, idempotencyKey, signal);
  }

  async patchControl<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    return this.sendControlRequest<T>(path, body, signal, "", "PATCH");
  }

  async deleteControl<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    return this.sendControlRequest<T>(path, body, signal, "", "DELETE");
  }

  async createRun(body: RunCreationControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<RunCreationControlView> {
    if (!this.hasRunCreation) {
      throw new Error("Run creation capability is required for this operation");
    }
    normalizeRequestedNetworkAuthority(body);
    const result = await this.sendControl<unknown>("/runs", body, idempotencyKey, signal);
    return parseRunCreationControl(result, body);
  }

  async createThread(body: ThreadCreationControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ThreadCreationControlView> {
    const routePairPresent = body.provider !== undefined && body.model !== undefined;
    const routePairAbsent = body.provider === undefined && body.model === undefined;
    if (!this.hasThreadControl || !this.hasRunCreation || !this.hasSessionMessages ||
      body.version !== "thread_creation.v1" || (!routePairPresent && !routePairAbsent) ||
      (routePairPresent && (!boundedIdentity(body.provider) || !boundedIdentity(body.model) ||
        body.provider!.includes("/") || body.model!.includes("/")))) {
      throw new Error("Thread creation capability and a normalized Provider/model pair are required");
    }
    normalizeRequestedNetworkAuthority(body);
    return parseThreadCreationControl(await this.sendControl<unknown>("/threads", body,
      idempotencyKey, signal), body);
  }

  async submitThreadMessage(threadID: string, body: ThreadMessageControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<ThreadMessageControlView> {
    if (!this.hasThreadControl || !this.hasSessionMessages || !boundedIdentity(threadID) ||
      threadID.trim() !== threadID || body.version !== "thread_message_submission.v1") {
      throw new Error("Thread message capability and a normalized Thread are required");
    }
    return parseThreadMessageControl(await this.sendControl<unknown>(
      `/threads/${encodeURIComponent(threadID)}/messages`, body, idempotencyKey, signal,
    ), threadID);
  }

  async threadActivityDetail(threadID: string, activityRef: string,
    signal?: AbortSignal): Promise<ThreadActivityDetailView> {
    if (boundedIdentity(threadID) !== threadID || boundedIdentity(activityRef) !== activityRef) {
      throw new Error("Normalized Thread and activity identities are required");
    }
    return parseThreadActivityDetail(await this.get<unknown>(
      `/threads/${encodeURIComponent(threadID)}/activities/${encodeURIComponent(activityRef)}`,
      {}, signal), threadID, activityRef);
  }

  async threadActivityArtifact(threadID: string, activityRef: string, artifactRef: string,
    signal?: AbortSignal): Promise<ThreadActivityArtifactView> {
    if (boundedIdentity(threadID) !== threadID || boundedIdentity(activityRef) !== activityRef ||
      boundedIdentity(artifactRef) !== artifactRef) {
      throw new Error("Normalized Thread activity artifact identities are required");
    }
    const value = parseThreadActivityArtifact(await this.get<unknown>(
      `/threads/${encodeURIComponent(threadID)}/activities/${encodeURIComponent(activityRef)}` +
      `/artifacts/${encodeURIComponent(artifactRef)}`, {}, signal), activityRef, artifactRef);
    const digest = new Uint8Array(await globalThis.crypto.subtle.digest(
      "SHA-256", new TextEncoder().encode(value.content)));
    const digestHex = [...digest].map((byte) => byte.toString(16).padStart(2, "0")).join("");
    if (digestHex !== value.sha256) {
      throw new APIRequestError("Thread activity artifact digest verification failed",
        "INVALID_RESPONSE", 502);
    }
    return value;
  }

  async expandRunNetworkAuthority(runID: string,
    body: RunNetworkAuthorityControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<RunNetworkAuthorityControlView> {
    if (!this.hasControl || !boundedIdentity(runID) || runID.trim() !== runID ||
      body.version !== "run_network_authority_control.v1" ||
      !safePositiveInteger(body.expected_mode_revision) ||
      (body.reason !== undefined && body.reason !== "" && (!boundedText(body.reason, 1_024) ||
        /[\u0000-\u001f\u007f]/u.test(body.reason)))) {
      throw new Error("A revision-bound exact Run network authority request is required");
    }
    normalizeRequestedNetworkAuthority({ network_mode: "allowlist",
      allowed_targets: body.add_allowed_targets });
    return parseRunNetworkAuthorityControl(await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/network-authority`, body,
      idempotencyKey, signal), runID, body);
  }

  async submitThreadTurn(threadID: string, body: ThreadMessageControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<ThreadMessageControlView> {
    if (!this.hasThreadControl || !this.hasSessionMessages || !boundedIdentity(threadID) ||
      threadID.trim() !== threadID || body.version !== "thread_message_submission.v1") {
      throw new Error("Thread turn capability and a normalized Thread are required");
    }
    return parseThreadMessageControl(await this.sendControl<unknown>(
      `/threads/${encodeURIComponent(threadID)}/turns`, body, idempotencyKey, signal,
    ), threadID, true);
  }

  async recoverThreadRun(threadID: string, body: ThreadRunRecoveryControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<ThreadRunRecoveryControlView> {
    if (!this.hasThreadControl || !this.hasSessionMessages || !boundedIdentity(threadID) ||
      threadID.trim() !== threadID || body.version !== "thread_run_recovery.v1" ||
      !boundedIdentity(body.run_id) || !boundedIdentity(body.handoff_operation_id)) {
      throw new Error("Thread recovery capability and exact failed boundary identities are required");
    }
    return parseThreadRunRecoveryControl(await this.sendControl<unknown>(
      `/threads/${encodeURIComponent(threadID)}/recovery`, body, idempotencyKey, signal,
    ), threadID, body);
  }

  async getThreadExecutionPermission(threadID: string,
    signal?: AbortSignal): Promise<ThreadExecutionPermissionControlView> {
    if (!boundedIdentity(threadID) || threadID.trim() !== threadID) {
      throw new Error("A normalized Thread is required");
    }
    return this.get<ThreadExecutionPermissionControlView>(
      `/threads/${encodeURIComponent(threadID)}/execution-permission`, {}, signal);
  }

  async changeThreadExecutionPermission(threadID: string,
    body: ThreadExecutionPermissionControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ThreadExecutionPermissionControlView> {
    if (!this.hasExecutionPermissionControl || !boundedIdentity(threadID) ||
      threadID.trim() !== threadID) {
      throw new Error("Thread execution permission capability is required");
    }
    return this.sendControl<ThreadExecutionPermissionControlView>(
      `/threads/${encodeURIComponent(threadID)}/execution-permission`, body,
      idempotencyKey, signal);
  }

  async transitionThread(threadID: string, action: "archive" | "restore" | "delete",
    body: ThreadLifecycleControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ThreadLifecycleControlView> {
    if (!this.hasThreadControl || !this.hasSessionMessages ||
      !boundedIdentity(threadID) || threadID.trim() !== threadID ||
      body.version !== "thread_lifecycle.v1" || !Number.isSafeInteger(body.expected_version) ||
      body.expected_version < 1) {
      throw new Error("Thread lifecycle capability and a current Thread version are required");
    }
    return parseThreadLifecycleControl(await this.sendControl<unknown>(
      `/threads/${encodeURIComponent(threadID)}/${action}`, body, idempotencyKey, signal,
    ), threadID, action, body.expected_version);
  }

  async submitSessionMessage(sessionID: string, body: SessionMessageControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<SessionMessageControlView> {
    if (!this.hasSessionMessages) {
      throw new Error("Run messaging capability is required for this operation");
    }
    const normalizedSessionID = boundedIdentity(sessionID);
    if (!normalizedSessionID || normalizedSessionID !== sessionID) {
      throw new Error("A normalized Run-local Session identity is required");
    }
    const result = await this.sendControl<unknown>(
      `/sessions/${encodeURIComponent(sessionID)}/messages`, body, idempotencyKey, signal,
    );
    return parseSessionMessageControl(result, sessionID);
  }

  async archiveSession(sessionID: string, body: SessionArchiveControlRequestView,
    signal?: AbortSignal): Promise<SessionArchiveControlView> {
    if (!this.hasSessionMessages || !boundedIdentity(sessionID) || sessionID.trim() !== sessionID ||
      body.version !== "session_archive.v1" || body.confirm !== true) {
      throw new Error("Confirmed Run-local Session archive capability and identity are required");
    }
    return parseSessionArchiveControl(await this.sendControlRequest<unknown>(
      `/sessions/${encodeURIComponent(sessionID)}/archive`, body, signal,
    ), sessionID);
  }

  async cancelSessionSteering(sessionID: string, messageID: string,
    body: SessionSteeringCancellationRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<SessionSteeringCancellationView> {
    if (!this.hasSessionSteeringControl) {
      throw new Error("Run-local Session steering cancellation capability is required for this operation");
    }
    const normalizedSessionID = boundedIdentity(sessionID);
    const normalizedMessageID = boundedIdentity(messageID);
    if (!normalizedSessionID || normalizedSessionID !== sessionID ||
      !normalizedMessageID || normalizedMessageID !== messageID) {
      throw new Error("Normalized Run-local Session and steering identities are required");
    }
    const result = await this.sendControl<unknown>(
      `/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/cancel`,
      body, idempotencyKey, signal,
    );
    return parseSessionSteeringCancellation(result, sessionID, messageID);
  }

  async controlRunLifecycle(runID: string, body: RunLifecycleControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<RunLifecycleControlView> {
    if (!this.hasRunLifecycle) {
      throw new Error("Run lifecycle capability is required for this operation");
    }
    const normalizedRunID = boundedIdentity(runID);
    if (!normalizedRunID || normalizedRunID !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/lifecycle`, body, idempotencyKey, signal,
    );
    return parseRunLifecycleControl(result, runID, body);
  }

  async executeRun(runID: string, body: RunExecutionControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<RunExecutionControlView> {
    if (!this.hasRunExecution) {
      throw new Error("Run execution capability is required for this operation");
    }
    const normalizedRunID = boundedIdentity(runID);
    if (!normalizedRunID || normalizedRunID !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/execute`, body, idempotencyKey, signal,
    );
    return parseRunExecutionControl(result, runID, body);
  }

  async getPublicModelStream(runID: string,
    signal?: AbortSignal): Promise<PublicModelStreamSnapshot> {
    const normalizedRunID = boundedIdentity(runID);
    if (!normalizedRunID || normalizedRunID !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/active-call`, {}, signal,
    );
    return parsePublicModelStream(result, runID);
  }

  async pollPublicModelStream(runID: string,
    signal?: AbortSignal): Promise<PublicModelStreamSnapshot | null> {
    const normalizedRunID = boundedIdentity(runID);
    if (!normalizedRunID || normalizedRunID !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/active-call/poll`, {}, signal,
    );
    return parsePublicModelStreamPoll(result, runID);
  }

  async cancelModelCall(runID: string, body: ModelCancellationRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<ModelCancellationView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    const normalizedRunID = boundedIdentity(runID);
    if (!normalizedRunID || normalizedRunID !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    if (!boundedIdentity(body.attempt_id) || !safePositiveInteger(body.model_attempt)) {
      throw new Error("A bound attempt identity and model attempt are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/active-call/cancel`, body, idempotencyKey, signal,
    );
    return parseModelCancellation(result, runID, body);
  }

  async cancelSpecialistModelCall(runID: string, agentID: string,
    body: ModelCancellationRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<SpecialistModelCancellationView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    const normalizedRunID = boundedIdentity(runID);
    const normalizedAgentID = boundedIdentity(agentID);
    if (!normalizedRunID || normalizedRunID !== runID ||
      !normalizedAgentID || normalizedAgentID !== agentID) {
      throw new Error("Normalized Run and Agent identities are required");
    }
    if (!boundedIdentity(body.attempt_id) || !safePositiveInteger(body.model_attempt)) {
      throw new Error("A bound attempt identity and model attempt are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/agents/${encodeURIComponent(agentID)}/active-call/cancel`,
      body, idempotencyKey, signal,
    );
    return parseSpecialistModelCancellation(result, runID, agentID, body);
  }

  async getRunFanoutExecutions(runID: string, planID: string,
    signal?: AbortSignal): Promise<FanoutExecutionsListView> {
    if (!boundedIdentity(runID) || !boundedIdentity(planID)) {
      throw new Error("Normalized Run and Fan-out plan identities are required");
    }
    const result = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/fanout-executions?plan_id=${encodeURIComponent(planID)}`,
      {}, signal,
    );
    return parseFanoutExecutions(result, runID, planID);
  }

  async cancelRunFanoutExecution(runID: string, executionID: string,
    body: FanoutExecutionCancelRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<FanoutExecutionView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (!boundedIdentity(runID) || !boundedIdentity(executionID) ||
      body.version !== "readonly_fanout_cancel.v1" || body.confirm_cancel !== true) {
      throw new Error("Normalized Run/execution identities and explicit confirmation are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/fanout-executions/${encodeURIComponent(executionID)}/cancel`,
      body, idempotencyKey, signal,
    );
    return parseFanoutExecution(result);
  }
  async getRunChildTaskProposals(runID: string,
    signal?: AbortSignal): Promise<ChildTaskProposalsListView> {
    if (!boundedIdentity(runID)) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/child-task-proposals`, {}, signal,
    );
    return parseChildTaskProposals(result, runID);
  }

  async reviewRunChildTaskProposal(runID: string, proposalID: string,
    body: ChildTaskReviewRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ChildTaskProposalView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (!boundedIdentity(runID) || !boundedIdentity(proposalID) ||
      body.version !== "child_task_review.v1" || body.confirm_review !== true ||
      (body.action !== "approve" && body.action !== "deny")) {
      throw new Error("A normalized proposal and explicit confirmed review are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/child-task-proposals/${encodeURIComponent(proposalID)}/review`,
      body, idempotencyKey, signal,
    );
    return parseChildTaskProposal(result, runID);
  }

  async admitRunChildTaskProposal(runID: string, proposalID: string,
    body: ChildTaskAdmitRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ChildTaskProposalView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (!boundedIdentity(runID) || !boundedIdentity(proposalID) ||
      body.version !== "child_task_admit.v1" || body.confirm_admit !== true) {
      throw new Error("A normalized proposal and explicit confirmed admission are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/child-task-proposals/${encodeURIComponent(proposalID)}/admit`,
      body, idempotencyKey, signal,
    );
    return parseChildTaskProposal(result, runID);
  }

  async getRunBatchDeliveries(runID: string,
    signal?: AbortSignal): Promise<BatchDeliveriesListView> {
    if (!boundedIdentity(runID)) {
      throw new Error("A normalized Run identity is required");
    }
    return parseBatchDeliveries(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/batch-deliveries`, {}, signal,
    ), runID);
  }

  async getRunBatchDelivery(runID: string, planID: string,
    signal?: AbortSignal): Promise<BatchDeliverySnapshotView> {
    if (!boundedIdentity(runID) || !boundedIdentity(planID)) {
      throw new Error("Normalized Run and batch delivery identities are required");
    }
    return parseBatchDeliverySnapshot(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/batch-deliveries/${encodeURIComponent(planID)}`,
      {}, signal,
    ), runID, planID);
  }

  async reviewRunBatchDeliveryChild(runID: string, planID: string, ordinal: number,
    body: BatchDeliveryReviewRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<BatchDeliveryReviewControlView> {
    this.requireBatchDeliveryControl(runID, planID, ordinal);
    if (body.version !== "batch_delivery_review_control.v1" ||
      !safePositiveInteger(body.generation) || !boundedText(body.reviewer, 256) ||
      !boundedText(body.summary, 4096) ||
      (body.verdict !== "accepted" && body.verdict !== "changes_requested") ||
      !body.full_diff_reviewed || !body.call_chain_reviewed || !body.tests_reviewed) {
      throw new Error("A complete independent batch delivery review is required");
    }
    const value = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/batch-deliveries/${encodeURIComponent(planID)}` +
        `/children/${ordinal}/review`, body, idempotencyKey, signal,
    );
    return parseBatchDeliveryReviewControl(value, planID, ordinal);
  }

  async mergeRunBatchDelivery(runID: string, planID: string,
    body: BatchDeliveryMergeRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<BatchDeliveryMergeControlView> {
    this.requireBatchDeliveryControl(runID, planID);
    if (body.version !== "batch_delivery_merge.v1" || body.confirm !== true ||
      !Array.isArray(body.ordered_ordinals) || body.ordered_ordinals.length < 1 ||
      body.ordered_ordinals.length > 2 ||
      body.ordered_ordinals.some((ordinal) => !safePositiveInteger(ordinal))) {
      throw new Error("A bounded confirmed batch delivery merge order is required");
    }
    const value = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/batch-deliveries/${encodeURIComponent(planID)}/merge`,
      body, idempotencyKey, signal,
    );
    return parseBatchDeliveryMergeControl(value, planID);
  }

  async cancelRunBatchDelivery(runID: string, planID: string,
    body: BatchDeliveryCancelRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<BatchDeliveryCancelView> {
    this.requireBatchDeliveryControl(runID, planID);
    if (body.version !== "batch_delivery_cancel.v1" || body.confirm !== true ||
      !boundedText(body.reason, 4096)) {
      throw new Error("A bounded confirmed batch delivery cancellation is required");
    }
    const value = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/batch-deliveries/${encodeURIComponent(planID)}/cancel`,
      body, idempotencyKey, signal,
    );
    return parseBatchDeliveryCancel(value, runID, planID);
  }

  async reconcileRunBatchDelivery(runID: string, planID: string,
    body: BatchDeliveryReconcileRequestView,
    signal?: AbortSignal): Promise<BatchDeliveryReconcileView> {
    this.requireBatchDeliveryControl(runID, planID);
    if (body.version !== "batch_delivery_reconcile.v1" || body.confirm !== true) {
      throw new Error("Explicit batch delivery reconciliation confirmation is required");
    }
    const value = await this.sendControlRequest<unknown>(
      `/runs/${encodeURIComponent(runID)}/batch-deliveries/${encodeURIComponent(planID)}/reconcile`,
      body, signal,
    );
    return parseBatchDeliveryReconcile(value, planID);
  }

  private requireBatchDeliveryControl(runID: string, planID: string, ordinal = 1): void {
    if (!this.hasBatchDeliveryControl) {
      throw new Error("Batch delivery control capability is required for this operation");
    }
    if (!boundedIdentity(runID) || !boundedIdentity(planID) ||
      !safePositiveInteger(ordinal) || ordinal > 2) {
      throw new Error("Normalized Run, batch delivery, and child identities are required");
    }
  }
  async listPriceSnapshots(signal?: AbortSignal): Promise<PriceSnapshotListView> {
    const result = await this.get<unknown>("/models/prices", {}, signal);
    return parsePriceSnapshots(result);
  }

  async importPriceSnapshot(body: PriceSnapshotImportRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<PriceSnapshotImportView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (body.version !== "price_snapshot.v1" || !body.document ||
      body.document.length > 64 * 1024) {
      throw new Error("A bounded price_snapshot.v1 document is required");
    }
    const result = await this.sendControl<unknown>("/models/prices", body, idempotencyKey, signal);
    return parsePriceSnapshotImport(result);
  }

  async getDockerSandboxStatus(admissionID: string,
    signal?: AbortSignal): Promise<DockerSandboxStatusView> {
    const result = await this.get<unknown>("/sandbox/docker/status", { admission_id: admissionID }, signal);
    if (!isRecord(result) || !boundedIdentity(String(result.admission_id))) {
      throw new APIRequestError("Docker sandbox status is invalid", "INVALID_RESPONSE", 502);
    }
    return result as unknown as DockerSandboxStatusView;
  }

  async admitDockerSandbox(body: DockerSandboxAdmissionRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<DockerSandboxAdmissionView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (!boundedIdentity(body.plan_id) || !boundedIdentity(body.requested_by) ||
      !isRecord(body.manifest)) {
      throw new Error("A plan identity, requester, and manifest object are required");
    }
    const result = await this.sendControl<unknown>("/sandbox/docker/admissions", body, idempotencyKey, signal);
    if (!isRecord(result) || typeof result.allowed !== "boolean") {
      throw new APIRequestError("Docker sandbox admission is invalid", "INVALID_RESPONSE", 502);
    }
    return result as unknown as DockerSandboxAdmissionView;
  }

  async startDockerSandbox(body: DockerSandboxStartRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<DockerSandboxStatusView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (!boundedIdentity(body.admission_id) || !boundedIdentity(body.requested_by)) {
      throw new Error("An admission identity and requester are required");
    }
    const result = await this.sendControl<unknown>("/sandbox/docker/starts", body, idempotencyKey, signal);
    return result as unknown as DockerSandboxStatusView;
  }

  async cancelDockerSandbox(body: DockerSandboxCancelRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<DockerSandboxCancellationView> {
    if (!this.hasControl) {
      throw new Error("A control bearer token is required for this operation");
    }
    if (!boundedIdentity(body.admission_id) || !boundedIdentity(body.requested_by)) {
      throw new Error("An admission identity and requester are required");
    }
    const result = await this.sendControl<unknown>("/sandbox/docker/cancellations", body, idempotencyKey, signal);
    return result as unknown as DockerSandboxCancellationView;
  }
  async selectPlanDirection(runID: string, body: PlanDirectionControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<PlanDirectionControlView> {
    if (!this.hasPlanDelivery) {
      throw new Error("Plan/Delivery control capability is required for this operation");
    }
    if (!boundedIdentity(runID) || runID.trim() !== runID || body.direction < 1 ||
      body.direction > 3 || !boundedIdentity(body.proposal_id)) {
      throw new Error("A normalized Run, proposal, and direction are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/plan/direction`, body, idempotencyKey, signal,
    );
    return parsePlanDirectionControl(result, runID, body);
  }

  async enterPlanMode(runID: string, body: PlanModeTransitionControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<PlanModeTransitionControlView> {
    if (!this.hasPlanDelivery) {
      throw new Error("Plan/Delivery control capability is required for this operation");
    }
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/plan/enter`, body, idempotencyKey, signal,
    );
    return parsePlanModeTransition(result, runID);
  }

  async enterPlanDelivery(runID: string, body: PlanDeliveryTransitionControlRequestView,
    idempotencyKey: string, signal?: AbortSignal): Promise<PlanDeliveryTransitionControlView> {
    if (!this.hasPlanDelivery) {
      throw new Error("Plan/Delivery control capability is required for this operation");
    }
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/plan/deliver`, body, idempotencyKey, signal,
    );
    return parsePlanDeliveryTransition(result, runID);
  }

  async approvalQueue(runID: string, signal?: AbortSignal): Promise<ApprovalQueueView> {
    if (!boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("A normalized Run identity is required");
    }
    const value = await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/approvals`, {}, signal,
    );
    return parseApprovalQueue(value, runID);
  }

  async controlledCommandProposals(runID: string,
    signal?: AbortSignal): Promise<PageResult<ControlledCommandProposalView>> {
    if (!this.hasControlledCommandProposalControl ||
      !boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("Controlled command proposal capability and normalized Run are required");
    }
    const page = await this.getPage<unknown>(
      `/runs/${encodeURIComponent(runID)}/command-proposals`, { limit: 100 }, "", signal,
    );
    return {
      ...page,
      items: page.items.map((item) => parseControlledCommandProposal(item, runID)),
    };
  }

  async controlledCommandProposal(runID: string, proposalID: string,
    signal?: AbortSignal): Promise<ControlledCommandProposalView> {
    if (!this.hasControlledCommandProposalControl ||
      !boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(proposalID) || proposalID.trim() !== proposalID) {
      throw new Error("Controlled command proposal capability and normalized identities are required");
    }
    return parseControlledCommandProposal(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/command-proposals/${encodeURIComponent(proposalID)}`,
      {}, signal,
    ), runID, proposalID);
  }

  async reviewControlledCommandProposal(runID: string, proposalID: string,
    body: ControlledCommandProposalReviewRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ControlledCommandProposalView> {
    if (!this.hasControlledCommandProposalControl ||
      !boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(proposalID) || proposalID.trim() !== proposalID ||
      body.version !== "controlled_command_proposal_review.v1" ||
      (body.decision !== "approve" && body.decision !== "deny") ||
      body.confirm_execution !== (body.decision === "approve") ||
      (body.reason !== undefined &&
        (!boundedText(body.reason, 4_096) || /[\u0000-\u001f\u007f]/u.test(body.reason)))) {
      throw new Error("An exact controlled command review request is required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/command-proposals/${encodeURIComponent(proposalID)}/review`,
      body, idempotencyKey, signal,
    );
    return parseControlledCommandProposal(result, runID, proposalID);
  }

  async hostCommandProposals(runID: string,
    signal?: AbortSignal): Promise<PageResult<HostCommandProposalView>> {
    if (!this.hasHostCommandProposalControl ||
      !boundedIdentity(runID) || runID.trim() !== runID) {
      throw new Error("Host command proposal capability and normalized Run are required");
    }
    const page = await this.getPage<unknown>(
      `/runs/${encodeURIComponent(runID)}/host-command-proposals`, { limit: 100 }, "", signal,
    );
    return {
      ...page,
      items: page.items.map((item) => parseHostCommandProposal(item, runID)),
    };
  }

  async hostCommandProposal(runID: string, proposalID: string,
    signal?: AbortSignal): Promise<HostCommandProposalView> {
    if (!this.hasHostCommandProposalControl ||
      !boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(proposalID) || proposalID.trim() !== proposalID) {
      throw new Error("Host command proposal capability and normalized identities are required");
    }
    return parseHostCommandProposal(await this.get<unknown>(
      `/runs/${encodeURIComponent(runID)}/host-command-proposals/${encodeURIComponent(proposalID)}`,
      {}, signal,
    ), runID, proposalID);
  }

  async reviewHostCommandProposal(runID: string, proposalID: string,
    body: HostCommandProposalReviewRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<HostCommandProposalView> {
    const boundedGrant = body.decision === "approve" && body.authorization === "run_scope";
    const grantFieldsAbsent = body.grant_ttl_seconds === undefined &&
      body.grant_max_uses === undefined;
    if (!this.hasHostCommandProposalControl ||
      !boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(proposalID) || proposalID.trim() !== proposalID ||
      body.version !== "host_command_review.v1" ||
      (body.decision !== "approve" && body.decision !== "deny") ||
      body.confirm_execution !== (body.decision === "approve") ||
      (body.authorization !== undefined && body.authorization !== "once" &&
        body.authorization !== "run_scope") ||
      (body.decision === "deny" && (body.authorization !== undefined || !grantFieldsAbsent)) ||
      (!boundedGrant && !grantFieldsAbsent) ||
      (boundedGrant && (!safePositiveInteger(body.grant_ttl_seconds) ||
        body.grant_ttl_seconds > 900 || !safePositiveInteger(body.grant_max_uses) ||
        body.grant_max_uses > 8)) ||
      (body.reason !== undefined &&
        (!boundedText(body.reason, 4_096) || /[\u0000-\u001f\u007f]/u.test(body.reason)))) {
      throw new Error("An exact host command review request is required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/host-command-proposals/${encodeURIComponent(proposalID)}/review`,
      body, idempotencyKey, signal,
    );
    return parseHostCommandProposal(result, runID, proposalID);
  }

  async decideApproval(runID: string, approvalID: string,
    body: ApprovalDecisionControlRequestView, idempotencyKey: string,
    signal?: AbortSignal): Promise<ApprovalDecisionControlView> {
    if (!this.hasApprovalControl) {
      throw new Error("Approval control capability is required for this operation");
    }
    if (!boundedIdentity(runID) || runID.trim() !== runID ||
      !boundedIdentity(approvalID) || approvalID.trim() !== approvalID ||
      body.version !== "approval_control.v1" ||
      !["approve_once", "approve_for_thread", "deny"].includes(body.action) ||
      (body.action !== "deny" && body.reason !== undefined) ||
      (body.reason !== undefined && (!boundedText(body.reason, 2_048) ||
        body.reason.trim() !== body.reason || /[\u0000-\u001f\u007f]/u.test(body.reason)))) {
      throw new Error("Normalized Run and approval identities are required");
    }
    const result = await this.sendControl<unknown>(
      `/runs/${encodeURIComponent(runID)}/approvals/${encodeURIComponent(approvalID)}/decision`,
      body, idempotencyKey, signal,
    );
    return parseApprovalDecision(result, runID, approvalID, body);
  }

  private async sendControl<T>(path: string, body: unknown, idempotencyKey: string,
    signal?: AbortSignal): Promise<T> {
    if (idempotencyKey.trim() !== idempotencyKey || idempotencyKey.length < 16) {
      throw new Error("A normalized idempotency key is required");
    }
    return this.sendControlRequest<T>(path, body, signal, idempotencyKey);
  }

  private async sendReadRequest<T>(path: string, body: unknown,
    signal?: AbortSignal): Promise<T> {
    const response = await fetch(this.url(path), {
      method: "POST",
      headers: { ...this.headers(), "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal,
      cache: "no-store",
      credentials: "omit",
      referrerPolicy: "no-referrer",
    });
    const payload = await this.readJSON(response);
    if (!response.ok) {
      if (isErrorEnvelope(payload)) {
        throw new APIRequestError(payload.error.message, payload.error.code, response.status,
          payload.request_id);
      }
      throw new APIRequestError("CyberAgent read request failed", "INVALID_RESPONSE", response.status,
        response.headers.get("x-request-id") || "");
    }
    if (!isSuccessEnvelope<T>(payload)) {
      throw new APIRequestError("CyberAgent API returned an invalid read envelope", "INVALID_RESPONSE",
        response.status, response.headers.get("x-request-id") || "");
    }
    return payload.data;
  }

  private async sendControlRequest<T>(path: string, body: unknown, signal?: AbortSignal,
    idempotencyKey = "", method: "POST" | "PUT" | "PATCH" | "DELETE" = "POST"): Promise<T> {
    const headers: Record<string, string> = {
      Accept: "application/json",
      Authorization: `Bearer ${this.controlToken}`,
      "Content-Type": "application/json",
    };
    if (idempotencyKey) {
      headers["Idempotency-Key"] = idempotencyKey;
    }
    const response = await fetch(this.url(path), {
      method,
      headers,
      body: JSON.stringify(body),
      signal,
      cache: "no-store",
      credentials: "omit",
      referrerPolicy: "no-referrer",
    });
    const payload = await this.readJSON(response);
    if (!response.ok) {
      if (isErrorEnvelope(payload)) {
        throw new APIRequestError(payload.error.message, payload.error.code, response.status, payload.request_id);
      }
      throw new APIRequestError("CyberAgent control request failed", "INVALID_RESPONSE", response.status,
        response.headers.get("x-request-id") || "");
    }
    if (!isSuccessEnvelope<T>(payload)) {
      throw new APIRequestError("CyberAgent API returned an invalid control envelope", "INVALID_RESPONSE",
        response.status, response.headers.get("x-request-id") || "");
    }
    return payload.data;
  }

  async streamRunEvents(
    runID: string,
    options: {
      cursor?: string;
      signal: AbortSignal;
      onFrame: (frame: RunEventStreamView) => void;
    },
  ): Promise<void> {
    const headers = this.headers();
    headers.Accept = "text/event-stream";
    if (options.cursor) {
      headers["Last-Event-ID"] = options.cursor;
    }
    const response = await fetch(this.url(`/runs/${encodeURIComponent(runID)}/events/stream`), {
      method: "GET",
      headers,
      signal: options.signal,
      cache: "no-store",
      credentials: "omit",
      referrerPolicy: "no-referrer",
    });
    if (!response.ok) {
      throw await this.responseError(response);
    }
    if (!response.headers.get("content-type")?.toLowerCase().startsWith("text/event-stream") || !response.body) {
      throw new APIRequestError("API returned an invalid event stream", "INVALID_RESPONSE", response.status,
        response.headers.get("x-request-id") || "");
    }
    await consumeSSE(response.body, (message) => {
      if (message.event !== "run.event") {
        return;
      }
      const frame = parseStreamFrame(JSON.parse(message.data) as unknown, runID);
      if (message.id === "" || message.id !== frame.cursor) {
        throw new Error("SSE id does not match the frame cursor");
      }
      options.onFrame(frame);
    });
  }

  async pollRunEvents(
    runID: string,
    cursor = "",
    limit = 100,
    signal?: AbortSignal,
  ): Promise<RunEventPollView> {
    if (!Number.isSafeInteger(limit) || limit <= 0 || limit > 100) {
      throw new Error("Event poll limit must be between 1 and 100");
    }
    const envelope = await this.request<unknown>(
      `/runs/${encodeURIComponent(runID)}/events/poll`,
      { cursor: cursor || undefined, limit },
      signal,
    );
    return parseEventPoll(envelope.data, runID, envelope.request_id);
  }

  private async request<T>(
    path: string,
    query: Record<string, QueryValue>,
    signal?: AbortSignal,
  ): Promise<SuccessEnvelope<T>> {
    const response = await fetch(this.url(path, query), {
      method: "GET",
      headers: this.headers(),
      signal,
      cache: "no-store",
      credentials: "omit",
      referrerPolicy: "no-referrer",
    });
    const payload = await this.readJSON(response);
    if (!response.ok) {
      if (isErrorEnvelope(payload)) {
        throw new APIRequestError(payload.error.message, payload.error.code, response.status, payload.request_id);
      }
      throw new APIRequestError("CyberAgent API request failed", "INVALID_RESPONSE", response.status,
        response.headers.get("x-request-id") || "");
    }
    if (!isSuccessEnvelope<T>(payload)) {
      throw new APIRequestError("CyberAgent API returned an invalid envelope", "INVALID_RESPONSE", response.status,
        response.headers.get("x-request-id") || "");
    }
    return payload;
  }

  private async responseError(response: Response): Promise<APIRequestError> {
    const payload = await this.readJSON(response);
    if (isErrorEnvelope(payload)) {
      return new APIRequestError(payload.error.message, payload.error.code, response.status, payload.request_id);
    }
    return new APIRequestError("CyberAgent API request failed", "INVALID_RESPONSE", response.status,
      response.headers.get("x-request-id") || "");
  }

  private async readJSON(response: Response): Promise<unknown> {
    try {
      return await response.json() as unknown;
    } catch {
      return null;
    }
  }

  private headers(): Record<string, string> {
    return {
      Accept: "application/json",
      Authorization: `Bearer ${this.token}`,
    };
  }

  private url(path: string, query: Record<string, QueryValue> = {}): string {
    if (!path.startsWith("/") || path.startsWith("//")) {
      throw new Error("API path must be relative to the configured base path");
    }
    const url = new URL(`${this.baseURL}${path}`, window.location.origin);
    if (!url.pathname.startsWith(`${this.baseURL}/`)) {
      throw new Error("API path escaped the configured base path");
    }
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== "") {
        url.searchParams.set(key, String(value));
      }
    }
    return `${url.pathname}${url.search}`;
  }
}
function parseFanoutExecutions(value: unknown, runID: string,
  planID: string): FanoutExecutionsListView {
  if (!isRecord(value) ||
    value.protocol_version !== "readonly_fanout_executions.v1" ||
    value.plan_id !== planID || !Array.isArray(value.items) || value.items.length > 100) {
    throw new APIRequestError("Fan-out execution list is invalid", "INVALID_RESPONSE", 502);
  }
  return { protocol_version: value.protocol_version, plan_id: value.plan_id,
    items: value.items.map(parseFanoutExecution) } as FanoutExecutionsListView;
}

function parseFanoutExecution(value: unknown): FanoutExecutionView {
  if (!isRecord(value) || !boundedIdentity(String(value.id)) ||
    !["running", "completed", "failed", "cancelled"].includes(String(value.status)) ||
    !safeBoundedCount(value.parallelism, 6) || !safeBoundedCount(value.max_output_tokens_per_shard, 4096) ||
    !boundedIdentity(String(value.requested_by)) || !validDate(value.started_at) ||
    !validDate(value.updated_at) || !Array.isArray(value.shards) || value.shards.length > 6) {
    throw new APIRequestError("Fan-out execution is invalid", "INVALID_RESPONSE", 502);
  }
  if (value.finished_at !== undefined && !validDate(value.finished_at)) {
    throw new APIRequestError("Fan-out execution finished_at is invalid", "INVALID_RESPONSE", 502);
  }
  const shards = value.shards.map((shard) => {
    if (!isRecord(shard) || !safeBoundedCount(shard.ordinal, 6) ||
      !["pending", "running", "completed", "failed", "cancelled"].includes(String(shard.status)) ||
      !safeBoundedCount(shard.attempt_count, 64) || !safeBoundedCount(shard.current_attempt, 64) ||
      !safeBoundedCount(shard.input_tokens, 1_000_000_000) ||
      !safeBoundedCount(shard.output_tokens, 1_000_000_000) ||
      !safeBoundedCount(shard.total_tokens, 1_000_000_000) ||
      !safeBoundedCount(shard.elapsed_millis, 1_000_000_000) ||
      !safeBoundedCount(shard.finding_count, 4096)) {
      throw new APIRequestError("Fan-out execution shard is invalid", "INVALID_RESPONSE", 502);
    }
    return shard;
  });
  return { ...value, shards } as FanoutExecutionView;
}
function parseChildTaskProposals(value: unknown, runID: string): ChildTaskProposalsListView {
  if (!isRecord(value) || value.protocol_version !== "child_task_proposals.v1" ||
    !Array.isArray(value.items) || value.items.length > 50) {
    throw new APIRequestError("Child task proposal list is invalid", "INVALID_RESPONSE", 502);
  }
  return { protocol_version: value.protocol_version,
    items: value.items.map((item) => parseChildTaskProposal(item, runID)),
  } as ChildTaskProposalsListView;
}

function parseChildTaskProposal(value: unknown, runID: string): ChildTaskProposalView {
  if (!isRecord(value) || !boundedIdentity(String(value.id)) || value.run_id !== runID ||
    !["proposed", "approved", "denied"].includes(String(value.status)) ||
    !["core", "readonly_fanout"].includes(String(value.surface)) ||
    !boundedIdentity(String(value.root_agent_id)) || !validDate(value.created_at) ||
    !Array.isArray(value.tasks) || value.tasks.length < 1 || value.tasks.length > 6) {
    throw new APIRequestError("Child task proposal is invalid", "INVALID_RESPONSE", 502);
  }
  for (const task of value.tasks) {
    if (!isRecord(task) || !safeBoundedCount(task.ordinal, 6) || !boundedText(task.title, 256) ||
      !boundedText(task.goal, 4096) || !safePositiveInteger(task.turn_limit) ||
      !safePositiveInteger(task.token_limit) || !safeBoundedCount(task.timeout_millis, 1_800_000)) {
      throw new APIRequestError("Child task proposal task is invalid", "INVALID_RESPONSE", 502);
    }
  }
  return value as unknown as ChildTaskProposalView;
}

const batchPlanStatuses = ["preparing", "active", "reviewing", "merging",
  "completed", "blocked", "aborted"];
const batchWorkspaceStatuses = ["preparing", "dispatched", "acknowledged", "working",
  "question", "ready_for_review", "changes_requested", "accepted", "merged",
  "cancelled", "failed", "orphaned"];
const batchMergeStatuses = ["prepared", "running", "blocked", "completed", "aborted"];
const batchMailboxKinds = ["dispatch", "ack", "progress", "question", "evidence",
  "ready_for_review", "changes_requested", "accepted", "aborted"];
const batchValidationKinds = ["git_diff_check", "go_test", "npm_test"];
const forbiddenBatchProjectionFields = new Set([
  "integration_root", "operation_digest", "owner_token", "owner_token_digest",
  "request_fingerprint", "tool_profile_fingerprint", "validation_json", "worktree_root",
]);

function batchProjectionContainsPrivateField(value: unknown, depth = 0): boolean {
  if (value === null || typeof value !== "object") return false;
  if (depth > 12) return true;
  if (Array.isArray(value)) {
    return value.some((item) => batchProjectionContainsPrivateField(item, depth + 1));
  }
  return Object.entries(value).some(([key, child]) =>
    forbiddenBatchProjectionFields.has(key) ||
    batchProjectionContainsPrivateField(child, depth + 1));
}

function parseBatchDeliveries(value: unknown, runID: string): BatchDeliveriesListView {
  if (!isRecord(value) || value.protocol_version !== "batch-deliveries-list.v1" ||
    !Array.isArray(value.items) || value.items.length > 64 ||
    batchProjectionContainsPrivateField(value)) {
    throw new APIRequestError("Batch delivery list is invalid", "INVALID_RESPONSE", 502);
  }
  for (const plan of value.items) parseBatchDeliveryPlan(plan, runID);
  return value as unknown as BatchDeliveriesListView;
}

function isGitObjectID(value: unknown): value is string {
  return typeof value === "string" && /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/u.test(value);
}

function isBatchPath(value: unknown): value is string {
  return boundedText(value, 4_096) && !value.startsWith("/") && !value.startsWith("../") &&
    !value.includes("\\") && !value.includes("\0") && value !== "..";
}

function parseBatchDeliverySpec(value: unknown): number {
  if (!isRecord(value) || value.version !== "batch-delivery.v1" ||
    !Array.isArray(value.tasks) || value.tasks.length < 1 || value.tasks.length > 2 ||
    !isRecord(value.contract) || value.contract.require_clean !== true ||
    value.contract.require_independent_review !== true ||
    value.contract.require_all_validations !== true ||
    !safeBoundedCount(value.contract.max_changed_files, 512) ||
    value.contract.max_changed_files === 0 ||
    !safeBoundedCount(value.contract.max_diff_bytes, 16 * 1024 * 1024) ||
    value.contract.max_diff_bytes === 0) {
    throw new APIRequestError("Batch delivery specification is invalid", "INVALID_RESPONSE", 502);
  }
  const taskCount = value.tasks.length;
  const dependencyGraph = new Map<number, number[]>();
  for (const [index, task] of value.tasks.entries()) {
    if (!isRecord(task) || task.ordinal !== index + 1 ||
      !Array.isArray(task.ownership_hints) || task.ownership_hints.length < 1 ||
      task.ownership_hints.length > 32 || !Array.isArray(task.dependency_ordinals) ||
      task.dependency_ordinals.length > value.tasks.length - 1 || !isRecord(task.budget) ||
      !safePositiveInteger(task.budget.turn_limit) ||
      !safePositiveInteger(task.budget.token_limit) ||
      !safeBoundedCount(task.budget.timeout_millis, 1_800_000) ||
      task.budget.timeout_millis === 0 || !Array.isArray(task.validations) ||
      task.validations.length < 1 || task.validations.length > 16 ||
      !Array.isArray(task.expected_artifacts) || task.expected_artifacts.length > 8) {
      throw new APIRequestError("Batch delivery task is invalid", "INVALID_RESPONSE", 502);
    }
    const ownership = new Set<string>();
    for (const hint of task.ownership_hints) {
      if (!isRecord(hint) || !isBatchPath(hint.path) ||
        (hint.kind !== "file" && hint.kind !== "directory") ||
        ownership.has(`${hint.kind}:${hint.path}`)) {
        throw new APIRequestError("Batch delivery ownership is invalid", "INVALID_RESPONSE", 502);
      }
      ownership.add(`${hint.kind}:${hint.path}`);
    }
    const dependencies = task.dependency_ordinals;
    if (dependencies.some((ordinal) => !safePositiveInteger(ordinal) ||
      ordinal > taskCount || ordinal === task.ordinal) ||
      new Set(dependencies).size !== dependencies.length) {
      throw new APIRequestError("Batch delivery dependencies are invalid", "INVALID_RESPONSE", 502);
    }
    dependencyGraph.set(task.ordinal, dependencies);
    let hasDiffCheck = false;
    const validationIDs = new Set<string>();
    for (const validation of task.validations) {
      if (!isRecord(validation) || !boundedText(validation.id, 96) ||
        !batchValidationKinds.includes(String(validation.kind)) ||
        !isBatchPath(validation.scope) || validationIDs.has(validation.id)) {
        throw new APIRequestError("Batch delivery validation is invalid", "INVALID_RESPONSE", 502);
      }
      validationIDs.add(validation.id);
      hasDiffCheck ||= validation.kind === "git_diff_check";
    }
    if (!hasDiffCheck) {
      throw new APIRequestError("Batch delivery validation is incomplete", "INVALID_RESPONSE", 502);
    }
    for (const artifact of task.expected_artifacts) {
      if (!isRecord(artifact) || !isBatchPath(artifact.path_hint) ||
        !boundedText(artifact.kind, 64)) {
        throw new APIRequestError("Batch delivery artifact is invalid", "INVALID_RESPONSE", 502);
      }
    }
  }
  const visiting = new Set<number>();
  const visited = new Set<number>();
  const visit = (ordinal: number): void => {
    if (visiting.has(ordinal)) {
      throw new APIRequestError("Batch delivery dependency cycle is invalid", "INVALID_RESPONSE", 502);
    }
    if (visited.has(ordinal)) return;
    visiting.add(ordinal);
    for (const dependency of dependencyGraph.get(ordinal) ?? []) visit(dependency);
    visiting.delete(ordinal);
    visited.add(ordinal);
  };
  for (const ordinal of dependencyGraph.keys()) visit(ordinal);
  return taskCount;
}

function parseBatchDeliveryPlan(value: unknown, runID: string, planID = ""): number {
  if (!isRecord(value) || !boundedIdentity(String(value.id)) || value.run_id !== runID ||
    (planID !== "" && value.id !== planID) || !boundedIdentity(String(value.proposal_id)) ||
    !boundedIdentity(String(value.root_agent_id)) || !boundedIdentity(String(value.workspace_id)) ||
    !batchPlanStatuses.includes(String(value.status)) ||
    !isGitObjectID(value.base_commit) ||
    !boundedText(value.source_branch, 256) || !boundedText(value.created_by, 256) ||
    !validDate(value.created_at) || !validDate(value.updated_at) || !isRecord(value.spec) ||
    value.spec.version !== "batch-delivery.v1") {
    throw new APIRequestError("Batch delivery plan is invalid", "INVALID_RESPONSE", 502);
  }
  return parseBatchDeliverySpec(value.spec);
}

function parseBatchDeliverySnapshot(value: unknown, runID: string,
  planID: string): BatchDeliverySnapshotView {
  if (!isRecord(value) || value.protocol_version !== "batch-delivery.v1" ||
    !isRecord(value.plan) || !Array.isArray(value.children) || value.children.length > 2 ||
    !Array.isArray(value.merge_steps) || value.merge_steps.length > 2 ||
    batchProjectionContainsPrivateField(value)) {
    throw new APIRequestError("Batch delivery snapshot is invalid", "INVALID_RESPONSE", 502);
  }
  const taskCount = parseBatchDeliveryPlan(value.plan, runID, planID);
  if (value.children.length !== taskCount) {
    throw new APIRequestError("Batch delivery child count is invalid", "INVALID_RESPONSE", 502);
  }
  const ordinals = new Set<number>();
  for (const child of value.children) {
    if (!isRecord(child) || !isRecord(child.workspace) || !Array.isArray(child.mailbox)) {
      throw new APIRequestError("Batch delivery child is invalid", "INVALID_RESPONSE", 502);
    }
    const workspace = child.workspace;
    if (workspace.plan_id !== planID || !safePositiveInteger(workspace.ordinal) ||
      workspace.ordinal > 2 || ordinals.has(workspace.ordinal) ||
      !boundedIdentity(String(workspace.agent_id)) || !safePositiveInteger(workspace.generation) ||
      !batchWorkspaceStatuses.includes(String(workspace.status)) ||
      !boundedText(workspace.branch, 256) ||
      !isGitObjectID(workspace.base_commit) ||
      (workspace.head_commit !== undefined && workspace.head_commit !== "" &&
        !isGitObjectID(workspace.head_commit)) ||
      !validDate(workspace.lease_expires_at) || !validDate(workspace.last_heartbeat_at) ||
      !validDate(workspace.created_at) || !validDate(workspace.updated_at) ||
      !isClosedBatchToolProfile(workspace.tool_profile) || child.mailbox.length > 512) {
      throw new APIRequestError("Batch delivery workspace is invalid", "INVALID_RESPONSE", 502);
    }
    ordinals.add(workspace.ordinal);
    let priorSequence = 0;
    for (const message of child.mailbox) {
      if (!isRecord(message) || message.ordinal !== workspace.ordinal ||
        !safePositiveInteger(message.generation) || message.generation > workspace.generation ||
        !safePositiveInteger(message.sequence) || message.sequence <= priorSequence ||
        !boundedIdentity(String(message.id)) || !boundedText(message.actor, 256) ||
        !boundedText(message.summary, 4096) || !Array.isArray(message.evidence_refs) ||
        message.evidence_refs.length > 32 ||
        !message.evidence_refs.every((reference) => boundedText(reference, 2_048)) ||
        !batchMailboxKinds.includes(String(message.kind)) || !validDate(message.created_at)) {
        throw new APIRequestError("Batch delivery mailbox is invalid", "INVALID_RESPONSE", 502);
      }
      priorSequence = message.sequence;
    }
    if (child.receipt !== undefined) parseBatchDeliveryReceipt(child.receipt, workspace.ordinal);
    if (child.review !== undefined) parseBatchDeliveryReview(child.review, workspace.ordinal);
  }
  if (value.merge_queue !== undefined) parseBatchDeliveryMergeQueue(value.merge_queue, planID);
  for (const step of value.merge_steps) parseBatchDeliveryMergeStep(step);
  return value as unknown as BatchDeliverySnapshotView;
}

function isClosedBatchToolProfile(value: unknown): boolean {
  if (!isRecord(value) || value.version !== "batch-delivery-tools.v1") return false;
  for (const key of ["workspace_list", "workspace_read", "workspace_search",
    "workspace_change", "workspace_apply", "git_status", "git_diff", "git_commit"]) {
    if (value[key] !== true) return false;
  }
  for (const key of ["workspace_delete", "shell", "process", "network", "credentials",
    "debug_terminal", "approvals", "spawn_children"]) {
    if (value[key] !== false) return false;
  }
  return true;
}

function parseBatchDeliveryReceipt(value: unknown, ordinal: number): void {
  if (!isRecord(value) || value.ordinal !== ordinal || !boundedIdentity(String(value.id)) ||
    !safePositiveInteger(value.generation) || value.protocol_version !== "batch-delivery-receipt.v1" ||
    !isGitObjectID(value.base_commit) || !isGitObjectID(value.head_commit) ||
    !isSHA256(value.diff_sha256) || !isSHA256(value.call_chain_sha256) ||
    !safePositiveInteger(value.diff_bytes) || value.diff_bytes > 16 * 1024 * 1024 ||
    !boundedText(value.diff_stat, 4_096) ||
    !Array.isArray(value.changed_files) || value.changed_files.length < 1 ||
    value.changed_files.length > 512 || !value.changed_files.every(isBatchPath) ||
    new Set(value.changed_files).size !== value.changed_files.length ||
    !Array.isArray(value.test_receipts) || value.test_receipts.length < 1 ||
    value.test_receipts.length > 32 || !Array.isArray(value.evidence_refs) ||
    value.evidence_refs.length > 32 || !Array.isArray(value.limitations) ||
    value.limitations.length > 32 ||
    !value.evidence_refs.every((reference) => boundedText(reference, 2_048)) ||
    !value.limitations.every((limitation) => boundedText(limitation, 1_024)) ||
    !validDate(value.created_at)) {
    throw new APIRequestError("Batch delivery receipt is invalid", "INVALID_RESPONSE", 502);
  }
  for (const receipt of value.test_receipts) {
    if (!isRecord(receipt) || !boundedText(receipt.requirement_id, 96) ||
      !batchValidationKinds.includes(String(receipt.kind)) || !isBatchPath(receipt.scope) ||
      receipt.exit_code !== 0 || !isSHA256(receipt.output_sha256) ||
      !safeBoundedCount(receipt.duration_millis, 10 * 60 * 1_000) ||
      !validDate(receipt.completed_at)) {
      throw new APIRequestError("Batch delivery test receipt is invalid", "INVALID_RESPONSE", 502);
    }
  }
}

function parseBatchDeliveryReview(value: unknown, ordinal: number): void {
  if (!isRecord(value) || value.ordinal !== ordinal || !boundedIdentity(String(value.id)) ||
    !safePositiveInteger(value.generation) || value.protocol_version !== "batch-delivery-review.v1" ||
    !boundedIdentity(String(value.receipt_id)) || !boundedText(value.reviewer, 256) ||
    !boundedText(value.summary, 4096) ||
    (value.verdict !== "accepted" && value.verdict !== "changes_requested") ||
    !isGitObjectID(value.base_commit) || !isGitObjectID(value.head_commit) ||
    !isSHA256(value.diff_sha256) || !isSHA256(value.call_chain_sha256) ||
    value.full_diff_reviewed !== true || value.call_chain_reviewed !== true ||
    value.tests_reviewed !== true || !validDate(value.created_at)) {
    throw new APIRequestError("Batch delivery review is invalid", "INVALID_RESPONSE", 502);
  }
}

function parseBatchDeliveryMergeQueue(value: unknown, planID: string): void {
  if (!isRecord(value) || value.plan_id !== planID || !boundedIdentity(String(value.id)) ||
    value.protocol_version !== "batch-delivery-merge-queue.v1" ||
    !batchMergeStatuses.includes(String(value.status)) ||
    !isGitObjectID(value.base_commit) || !isGitObjectID(value.latest_base_commit) ||
    !boundedText(value.integration_branch, 256) ||
    (value.integration_head !== undefined && value.integration_head !== "" &&
      !isGitObjectID(value.integration_head)) ||
    !Array.isArray(value.ordered_ordinals) || value.ordered_ordinals.length < 1 ||
    value.ordered_ordinals.length > 2 ||
    value.ordered_ordinals.some((ordinal) => !safePositiveInteger(ordinal) || ordinal > 2) ||
    new Set(value.ordered_ordinals).size !== value.ordered_ordinals.length ||
    !safeBoundedCount(value.next_index, value.ordered_ordinals.length) ||
    (value.failure_code !== undefined && value.failure_code !== "" &&
      !boundedText(value.failure_code, 256)) ||
    (value.failure_summary !== undefined && value.failure_summary !== "" &&
      !boundedText(value.failure_summary, 4_096)) || !validDate(value.created_at) ||
    !validDate(value.updated_at)) {
    throw new APIRequestError("Batch delivery merge queue is invalid", "INVALID_RESPONSE", 502);
  }
}

function parseBatchDeliveryMergeStep(value: unknown): void {
  if (!isRecord(value) || !safeBoundedCount(value.step_index, 2) ||
    !safePositiveInteger(value.ordinal) || value.ordinal > 2 ||
    !isGitObjectID(value.input_head) || !isGitObjectID(value.pre_merge_head) ||
    (value.post_merge_head !== undefined && value.post_merge_head !== "" &&
      !isGitObjectID(value.post_merge_head)) ||
    !batchMergeStatuses.includes(String(value.status)) ||
    (value.failure_code !== undefined && value.failure_code !== "" &&
      !boundedText(value.failure_code, 256)) || !validDate(value.created_at) ||
    (value.completed_at !== undefined && !validDate(value.completed_at))) {
    throw new APIRequestError("Batch delivery merge step is invalid", "INVALID_RESPONSE", 502);
  }
}

function parseBatchDeliveryReviewControl(value: unknown, planID: string,
  ordinal: number): BatchDeliveryReviewControlView {
  if (!isRecord(value) || typeof value.replayed !== "boolean" || !isRecord(value.review) ||
    value.review.plan_id !== undefined && value.review.plan_id !== planID) {
    throw new APIRequestError("Batch delivery review response is invalid", "INVALID_RESPONSE", 502);
  }
  parseBatchDeliveryReview(value.review, ordinal);
  return value as unknown as BatchDeliveryReviewControlView;
}

function parseBatchDeliveryMergeControl(value: unknown,
  planID: string): BatchDeliveryMergeControlView {
  if (!isRecord(value) || typeof value.base_drifted !== "boolean" ||
    typeof value.replayed !== "boolean" || !isRecord(value.queue) ||
    !Array.isArray(value.steps) || value.steps.length > 2 ||
    batchProjectionContainsPrivateField(value)) {
    throw new APIRequestError("Batch delivery merge response is invalid", "INVALID_RESPONSE", 502);
  }
  parseBatchDeliveryMergeQueue(value.queue, planID);
  for (const step of value.steps) parseBatchDeliveryMergeStep(step);
  return value as unknown as BatchDeliveryMergeControlView;
}

function parseBatchDeliveryCancel(value: unknown, runID: string,
  planID: string): BatchDeliveryCancelView {
  if (!isRecord(value) || !isRecord(value.snapshot) ||
    !Array.isArray(value.preserved_ordinals) || value.preserved_ordinals.length > 2 ||
    typeof value.integration_preserved !== "boolean" || typeof value.replayed !== "boolean") {
    throw new APIRequestError("Batch delivery cancellation response is invalid", "INVALID_RESPONSE", 502);
  }
  parseBatchDeliverySnapshot(value.snapshot, runID, planID);
  return value as unknown as BatchDeliveryCancelView;
}

function parseBatchDeliveryReconcile(value: unknown,
  planID: string): BatchDeliveryReconcileView {
  if (!isRecord(value) || value.protocol_version !== "batch-delivery.v1" ||
    value.plan_id !== planID || !safeBoundedCount(value.materialized_worktrees, 2) ||
    !safeBoundedCount(value.recovered_worktrees, 2) || typeof value.expired !== "boolean" ||
    typeof value.merge_resumed !== "boolean" || typeof value.merge_completed !== "boolean" ||
    typeof value.needs_operator_attention !== "boolean") {
    throw new APIRequestError("Batch delivery reconciliation response is invalid",
      "INVALID_RESPONSE", 502);
  }
  return value as unknown as BatchDeliveryReconcileView;
}
function parsePriceSnapshots(value: unknown): PriceSnapshotListView {
  if (!isRecord(value) || value.protocol_version !== "price_snapshot_list.v1" ||
    !Array.isArray(value.items) || value.items.length > 32) {
    throw new APIRequestError("Price snapshot list is invalid", "INVALID_RESPONSE", 502);
  }
  for (const item of value.items) {
    if (!isRecord(item) || !boundedIdentity(String(item.id)) || !validDate(item.imported_at) ||
      !Array.isArray(item.entries) || item.entries.length > 512) {
      throw new APIRequestError("Price snapshot item is invalid", "INVALID_RESPONSE", 502);
    }
  }
  return value as unknown as PriceSnapshotListView;
}

function parsePriceSnapshotImport(value: unknown): PriceSnapshotImportView {
  if (!isRecord(value) || value.protocol_version !== "price_snapshot.v1" ||
    !boundedIdentity(String(value.id)) || !boundedIdentity(String(value.fingerprint)) ||
    !safeBoundedCount(value.entry_count, 512) || typeof value.replayed !== "boolean") {
    throw new APIRequestError("Price snapshot import is invalid", "INVALID_RESPONSE", 502);
  }
  return value as unknown as PriceSnapshotImportView;
}
