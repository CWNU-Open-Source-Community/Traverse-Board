import { CyberAgentClient, type ClientCapabilities } from "../api/client";
import { useConnectionStore } from "../state/connection";

type ConnectionSnapshot = ReturnType<typeof useConnectionStore.getState>;

function capabilities(state: ConnectionSnapshot): ClientCapabilities {
  return {
    runControlEnabled: state.runControlEnabled,
    executionPermissionControlEnabled: state.executionPermissionControlEnabled,
    workspaceSandboxEnabled: state.workspaceSandboxEnabled,
    browserCDPPermissionControlEnabled: state.browserCDPPermissionControlEnabled,
    fullCDPDebugEnabled: state.fullCDPDebugEnabled,
    fullCDPSessionControlEnabled: state.fullCDPSessionControlEnabled,
    operatorApprovalEnabled: state.operatorApprovalEnabled,
    dangerFullAccessEnabled: state.dangerFullAccessEnabled,
    debugMaximumAccessEnabled: state.debugMaximumAccessEnabled,
    commandRuntimeEnabled: state.commandRuntimeEnabled,
    commandRuntimeProtocolAvailable: state.commandRuntimeProtocolAvailable,
    commandRuntimeAdapterInstalled: state.commandRuntimeAdapterInstalled,
    commandRuntimeAdapterReady: state.commandRuntimeAdapterReady,
    runCreationEnabled: state.runCreationEnabled,
    standardCodePresetEnabled: state.standardCodePresetEnabled,
    sessionMessageEnabled: state.sessionMessageEnabled,
    threadControlEnabled: state.threadControlEnabled,
    sessionSteeringControlEnabled: state.sessionSteeringControlEnabled,
    runLifecycleEnabled: state.runLifecycleEnabled,
    runExecutionEnabled: state.runExecutionEnabled,
    planDeliveryControlEnabled: state.planDeliveryControlEnabled,
    approvalControlEnabled: state.approvalControlEnabled,
    controlledCommandProposalControlEnabled: state.controlledCommandProposalControlEnabled,
    hostCommandProposalControlEnabled: state.hostCommandProposalControlEnabled,
    modelControlEnabled: state.modelControlEnabled,
    providerCredentialEnabled: state.providerCredentialEnabled,
    fileEditReviewEnabled: state.fileEditReviewEnabled,
    fileEditProposalEnabled: state.fileEditProposalEnabled,
    fileEditApplyEnabled: state.fileEditApplyEnabled,
    runWakeControlEnabled: state.runWakeControlEnabled,
    runWakeExecutionEnabled: state.runWakeExecutionEnabled,
    runWakeWorkerEnabled: state.runWakeWorkerEnabled,
    scheduledJobControlEnabled: state.scheduledJobControlEnabled,
    scheduledJobWorkerEnabled: state.scheduledJobWorkerEnabled,
    skillInstallationEnabled: state.skillInstallationEnabled,
    evidenceAttachmentEnabled: state.evidenceAttachmentEnabled,
    verificationEvidenceEnabled: state.verificationEvidenceEnabled,
    uiEvidenceControlEnabled: state.uiEvidenceControlEnabled,
    embeddedAnalyzerExecutionEnabled: state.embeddedAnalyzerExecutionEnabled,
    workspaceCheckpointControlEnabled: state.workspaceCheckpointControlEnabled,
    gitAdvancedControlEnabled: state.gitAdvancedControlEnabled,
    githubReviewControlEnabled: state.githubReviewControlEnabled,
    dockerExecutionEnabled: state.dockerExecutionEnabled,
    agentCodeToolsEnabled: state.agentCodeToolsEnabled,
    codeIntelEnabled: state.codeIntelEnabled,
  };
}

export function createV2Client(state: ConnectionSnapshot): CyberAgentClient {
  return new CyberAgentClient(state.token, undefined, state.controlToken, capabilities(state));
}
