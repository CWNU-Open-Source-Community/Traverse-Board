import { create } from "zustand";
import type { ClientCapabilities } from "../api/client";
import type { HealthView } from "../api/types";

type ResourceKind = "run" | "session";

interface ConnectionState {
  health: HealthView | null;
  resourceKind: ResourceKind;
  selectedRunID: string;
  selectedSessionID: string;
  token: string;
  controlToken: string;
  runControlEnabled: boolean;
  executionPermissionControlEnabled: boolean;
  browserCDPPermissionControlEnabled: boolean;
  fullCDPDebugEnabled: boolean;
  operatorApprovalEnabled: boolean;
  dangerFullAccessEnabled: boolean;
  debugMaximumAccessEnabled: boolean;
  commandRuntimeEnabled: boolean;
  runCreationEnabled: boolean;
  sessionMessageEnabled: boolean;
  sessionSteeringControlEnabled: boolean;
  runLifecycleEnabled: boolean;
  runExecutionEnabled: boolean;
  planDeliveryControlEnabled: boolean;
  approvalControlEnabled: boolean;
  controlledCommandProposalControlEnabled: boolean;
  hostCommandProposalControlEnabled: boolean;
  modelControlEnabled: boolean;
  providerCredentialEnabled: boolean;
  fileEditReviewEnabled: boolean;
  fileEditProposalEnabled: boolean;
  fileEditApplyEnabled: boolean;
  runWakeControlEnabled: boolean;
  runWakeExecutionEnabled: boolean;
  runWakeWorkerEnabled: boolean;
  scheduledJobControlEnabled: boolean;
  scheduledJobWorkerEnabled: boolean;
  skillInstallationEnabled: boolean;
  evidenceAttachmentEnabled: boolean;
  verificationEvidenceEnabled: boolean;
  uiEvidenceControlEnabled: boolean;
  embeddedAnalyzerExecutionEnabled: boolean;
  gitAdvancedControlEnabled: boolean;
  githubReviewControlEnabled: boolean;
  workspaceCheckpointControlEnabled: boolean;
  dockerExecutionEnabled: boolean;
  agentCodeToolsEnabled: boolean;
  codeIntelEnabled: boolean;
  connect: (token: string, health: HealthView, controlToken?: string,
    capabilities?: ClientCapabilities) => void;
  disconnect: () => void;
  selectRun: (runID: string) => void;
  selectSession: (sessionID: string) => void;
  setHealth: (health: HealthView) => void;
  setResourceKind: (kind: ResourceKind) => void;
}

const initialSelection = {
  resourceKind: "run" as const,
  selectedRunID: "",
  selectedSessionID: "",
};

export const useConnectionStore = create<ConnectionState>((set) => ({
  ...initialSelection,
  health: null,
  token: "",
  controlToken: "",
  runControlEnabled: false,
  executionPermissionControlEnabled: false,
  browserCDPPermissionControlEnabled: false,
  fullCDPDebugEnabled: false,
  operatorApprovalEnabled: false,
  dangerFullAccessEnabled: false,
  debugMaximumAccessEnabled: false,
  commandRuntimeEnabled: false,
  runCreationEnabled: false,
  sessionMessageEnabled: false,
  sessionSteeringControlEnabled: false,
  runLifecycleEnabled: false,
  runExecutionEnabled: false,
  planDeliveryControlEnabled: false,
  approvalControlEnabled: false,
  controlledCommandProposalControlEnabled: false,
  hostCommandProposalControlEnabled: false,
  modelControlEnabled: false,
  providerCredentialEnabled: false,
  fileEditReviewEnabled: false,
  fileEditProposalEnabled: false,
  fileEditApplyEnabled: false,
  runWakeControlEnabled: false,
  runWakeExecutionEnabled: false,
  runWakeWorkerEnabled: false,
  scheduledJobControlEnabled: false,
  scheduledJobWorkerEnabled: false,
  skillInstallationEnabled: false,
  evidenceAttachmentEnabled: false,
  verificationEvidenceEnabled: false,
  uiEvidenceControlEnabled: false,
  embeddedAnalyzerExecutionEnabled: false,
  gitAdvancedControlEnabled: false,
  githubReviewControlEnabled: false,
  workspaceCheckpointControlEnabled: false,
  dockerExecutionEnabled: false,
  agentCodeToolsEnabled: false,
  codeIntelEnabled: false,
  connect: (token, health, controlToken = "", capabilities = {}) => {
    const present = controlToken.trim() !== "";
    set({ token, health, controlToken,
      runControlEnabled: present && (capabilities.runControlEnabled ?? true),
      executionPermissionControlEnabled: present &&
        (capabilities.executionPermissionControlEnabled ?? false),
      browserCDPPermissionControlEnabled: present &&
        (capabilities.browserCDPPermissionControlEnabled ?? false),
      fullCDPDebugEnabled: present &&
        (capabilities.browserCDPPermissionControlEnabled ?? false) &&
        (capabilities.fullCDPDebugEnabled ?? false),
      operatorApprovalEnabled: present && (capabilities.operatorApprovalEnabled ?? false),
      dangerFullAccessEnabled: present && (capabilities.dangerFullAccessEnabled ?? false),
      debugMaximumAccessEnabled: present && (capabilities.debugMaximumAccessEnabled ?? false),
      commandRuntimeEnabled: present && (capabilities.commandRuntimeEnabled ?? false),
      runCreationEnabled: present && (capabilities.runCreationEnabled ?? true),
      sessionMessageEnabled: present && (capabilities.sessionMessageEnabled ?? true),
      sessionSteeringControlEnabled: present &&
        (capabilities.sessionSteeringControlEnabled ?? true),
      runLifecycleEnabled: present && (capabilities.runLifecycleEnabled ?? true),
      runExecutionEnabled: present && (capabilities.runExecutionEnabled ?? true),
      planDeliveryControlEnabled: present &&
        (capabilities.planDeliveryControlEnabled ?? true),
      approvalControlEnabled: present && (capabilities.approvalControlEnabled ?? true),
      controlledCommandProposalControlEnabled: present &&
        (capabilities.controlledCommandProposalControlEnabled ?? false),
	  hostCommandProposalControlEnabled: present &&
	    (capabilities.hostCommandProposalControlEnabled ?? false) &&
	    (capabilities.operatorApprovalEnabled ?? false),
	  modelControlEnabled: present && (capabilities.modelControlEnabled ?? true),
	  providerCredentialEnabled: present && (capabilities.providerCredentialEnabled ?? false),
	  fileEditReviewEnabled: present && (capabilities.fileEditReviewEnabled ?? true),
	  fileEditProposalEnabled: present && (capabilities.fileEditProposalEnabled ?? false),
	  fileEditApplyEnabled: present && (capabilities.fileEditApplyEnabled ?? true),
	  runWakeControlEnabled: present && (capabilities.runWakeControlEnabled ?? true),
	  runWakeExecutionEnabled: present && (capabilities.runWakeExecutionEnabled ?? true),
	  runWakeWorkerEnabled: present && (capabilities.runWakeWorkerEnabled ?? false),
	  scheduledJobControlEnabled: present &&
	    (capabilities.scheduledJobControlEnabled ?? false),
	  scheduledJobWorkerEnabled: present &&
	    (capabilities.scheduledJobWorkerEnabled ?? false),
	  skillInstallationEnabled: present && (capabilities.skillInstallationEnabled ?? true),
	  evidenceAttachmentEnabled: present && (capabilities.evidenceAttachmentEnabled ?? true),
	  verificationEvidenceEnabled: present &&
	    (capabilities.verificationEvidenceEnabled ?? false),
	  uiEvidenceControlEnabled: present &&
	    (capabilities.uiEvidenceControlEnabled ?? false),
	  embeddedAnalyzerExecutionEnabled: present &&
	    (capabilities.embeddedAnalyzerExecutionEnabled ?? false),
	  gitAdvancedControlEnabled: present &&
	    (capabilities.gitAdvancedControlEnabled ?? false),
	  githubReviewControlEnabled: present &&
	    (capabilities.githubReviewControlEnabled ?? false),
	  workspaceCheckpointControlEnabled: present &&
	    (capabilities.workspaceCheckpointControlEnabled ?? false),
	  dockerExecutionEnabled: present &&
	    (capabilities.dockerExecutionEnabled ?? false),
	  agentCodeToolsEnabled: present &&
	    (capabilities.agentCodeToolsEnabled ?? false),
	  codeIntelEnabled: capabilities.codeIntelEnabled ?? false,
    });
  },
  disconnect: () => set({ token: "", controlToken: "", health: null,
    runControlEnabled: false, runCreationEnabled: false, sessionMessageEnabled: false,
    executionPermissionControlEnabled: false, operatorApprovalEnabled: false,
    browserCDPPermissionControlEnabled: false, fullCDPDebugEnabled: false,
    dangerFullAccessEnabled: false, debugMaximumAccessEnabled: false,
    commandRuntimeEnabled: false,
    sessionSteeringControlEnabled: false,
    runLifecycleEnabled: false, runExecutionEnabled: false,
	planDeliveryControlEnabled: false, approvalControlEnabled: false,
	controlledCommandProposalControlEnabled: false,
	hostCommandProposalControlEnabled: false,
	modelControlEnabled: false, providerCredentialEnabled: false,
	fileEditReviewEnabled: false, fileEditProposalEnabled: false, fileEditApplyEnabled: false,
	runWakeControlEnabled: false, runWakeExecutionEnabled: false, runWakeWorkerEnabled: false,
	scheduledJobControlEnabled: false, scheduledJobWorkerEnabled: false,
	skillInstallationEnabled: false,
	evidenceAttachmentEnabled: false,
	verificationEvidenceEnabled: false,
	uiEvidenceControlEnabled: false,
	embeddedAnalyzerExecutionEnabled: false,
	gitAdvancedControlEnabled: false,
	githubReviewControlEnabled: false,
	workspaceCheckpointControlEnabled: false,
	dockerExecutionEnabled: false,
	agentCodeToolsEnabled: false,
	codeIntelEnabled: false,
    ...initialSelection }),
  selectRun: (selectedRunID) => set({ selectedRunID, resourceKind: "run" }),
  selectSession: (selectedSessionID) => set({ selectedSessionID, resourceKind: "session" }),
  setHealth: (health) => set({ health }),
  setResourceKind: (resourceKind) => set({ resourceKind }),
}));
