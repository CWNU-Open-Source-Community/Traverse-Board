package store

import (
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These are frozen schema177 rows under the original FK/immutable triggers.
// They contain no native executor and cannot create current-schema authority.
func historicalProposalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func historicalProposalExec(t *testing.T, s *SQLiteStore, q string, args ...any) {
	t.Helper()
	if v, err := s.SchemaVersion(t.Context()); err != nil || v != 177 {
		t.Fatalf("historical proposal seed rejects schema %d: %v", v, err)
	}
	if _, err := s.db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

type historicalProposalFixture struct {
	state       *SQLiteStore
	path        string
	turn        domain.SupervisorTurn
	permission  domain.RunExecutionPermissionSnapshot
	interaction domain.RunExecutionInteractionSnapshot
	profile     domain.RunExecutionProfileSnapshot
	mode        domain.RunModeSnapshot
	spec        runner.HostCommandSpec
	response    llm.ChatResponse
}

func newHistoricalProposalFixture(t *testing.T, mode domain.RunExecutionPermissionMode) historicalProposalFixture {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "historical-proposal.db")
	st := openHistoricalTestDatabase(t, path, 177)

	t.Cleanup(func() { _ = st.Close() })
	ws := WorkspaceRecord{ID: "history-workspace", Name: "history", RootPath: t.TempDir()}
	if err := st.SaveWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	runs := newMigrationFixtureRunService(t, st)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{Goal: "retain exact historical command outcome", Profile: "code", Surface: "code", Phase: "deliver", WorkspaceID: ws.ID, ModelRoute: "fixture/model", Budget: domain.Budget{MaxTurns: 4, MaxTokens: 16000, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionProfileService(st).Change(ctx, application.ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "local", OperationKey: "history-profile-0001", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionInteractionService(st).Change(ctx, application.ChangeRunExecutionInteractionRequest{RunID: run.ID, Mode: "controlled", Trust: "trusted", ConfirmWorkspaceTrust: true, OperationKey: "history-interaction", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := current.Next("history-permission", mode, true, "operator", "historical authorization", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertRunExecutionPermissionSnapshotTx(ctx, tx, permission); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "original user request", OperationKey: "history-user-input", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorTurn(ctx, acquireTestRunExecutionLease(t, ctx, st, run.ID), "")
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "fixture", Model: "model"}
	if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	action := domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionWait, Message: "Await exact saved outcome", Reason: "operator decision required"}
	response := llm.ChatResponse{Provider: "fixture", Model: "model", Text: historicalProposalJSON(t, action), Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
	turn.Checkpoint, err = st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt, response)
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := st.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := st.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runner.NewHostCommandSpec(runner.HostCommandSpecRequest{ExecutablePath: filepath.Join(ws.RootPath, "historical.exe"), ExecutableSHA256: strings.Repeat("a", 64), Argv: []string{"--version"}, WorkingDirectory: ws.RootPath, Environment: []string{"PATH=" + ws.RootPath}, NetworkIntent: runner.HostNetworkIntentHost, TimeoutMilliseconds: 1000, Purpose: "historical inspection"})
	if err != nil {
		t.Fatal(err)
	}
	return historicalProposalFixture{st, path, turn, permission, interaction, profile, selected, spec, response}
}
func (f historicalProposalFixture) call(t *testing.T, id, payload, authority, result string) {
	cp := f.turn.Checkpoint
	now := ts(time.Now().UTC())
	status := "pending"
	var completed any
	if result != "" {
		status = "completed"
		completed = now
	}
	historicalProposalExec(t, f.state, `INSERT INTO run_supervisor_tool_rounds(run_id,turn,attempt_id,round,model_attempt,created_at,completed_at) VALUES (?,?,?,1,1,?,?)`, cp.RunID, cp.NextTurn, cp.AttemptID, now, completed)
	historicalProposalExec(t, f.state, `INSERT INTO run_supervisor_tool_calls(run_id,turn,attempt_id,round,position,model_attempt,call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at) VALUES (?,?,?,1,1,1,?,'host_command_propose',?,?,?,?, '',?,?)`, cp.RunID, cp.NextTurn, cp.AttemptID, id, payload, authority, status, result, now, completed)
	historicalProposalExec(t, f.state, `INSERT INTO run_supervisor_tool_call_agents(run_id,turn,attempt_id,call_id,agent_id,agent_attempt_id,attribution_source,created_at) VALUES (?,?,?,?,?,?,'recorded',?)`, cp.RunID, cp.NextTurn, cp.AttemptID, id, f.turn.Agent.ID, cp.AttemptID, now)
}
func (f historicalProposalFixture) release(t *testing.T) {
	lease, found, err := f.state.GetRunExecutionLease(t.Context(), f.turn.Run.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, _, err := f.state.ReleaseRunExecutionLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
}
func historicalProposalRows(t *testing.T, s *SQLiteStore, tables []string) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	for _, table := range tables {
		result[table] = legacyFixtureRows(t, s, table)
	}
	return result
}

func TestHistoricalHostProposalUpgradePreservesEvidenceAndContinuation(t *testing.T) {
	for _, scenario := range []string{"completed", "nonzero", "denied", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			f := newHistoricalProposalFixture(t, domain.RunExecutionPermissionApproval)
			st := f.state
			ctx := t.Context()
			now := time.Now().UTC()
			run := f.turn.Run
			proposal := runner.HostCommandProposal{ID: "history-host-proposal", ProtocolVersion: runner.HostCommandProposalProtocolVersion, PolicyVersion: runner.HostCommandPolicyVersion, RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID, WorkspaceID: f.turn.Mission.WorkspaceID, RootAgentID: f.turn.Agent.ID, InteractionSnapshotID: f.interaction.ID, InteractionRevision: f.interaction.Revision, ExecutionProfileRevision: f.profile.Revision, PermissionSnapshotID: f.permission.ID, PermissionRevision: f.permission.Revision, PermissionMode: f.permission.Mode, RequestedBy: "run_supervisor", Spec: f.spec, CreatedAt: now}
			proposal.Fingerprint = runner.HostCommandProposalFingerprint(proposal)
			if err := proposal.Validate(); err != nil {
				t.Fatal(err)
			}
			historicalProposalExec(t, st, `INSERT INTO host_command_proposals(id,run_id,mission_id,session_id,workspace_id,root_agent_id,interaction_snapshot_id,interaction_revision,execution_profile_revision,permission_snapshot_id,permission_revision,permission_mode,spec_fingerprint,requested_by,instruction_authorized,execution_authorized,capability_grant,proposal_fingerprint,payload_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,?,?,?)`, proposal.ID, run.ID, run.MissionID, run.SessionID, proposal.WorkspaceID, proposal.RootAgentID, proposal.InteractionSnapshotID, proposal.InteractionRevision, proposal.ExecutionProfileRevision, proposal.PermissionSnapshotID, proposal.PermissionRevision, proposal.PermissionMode, proposal.Spec.Fingerprint, proposal.RequestedBy, proposal.Fingerprint, historicalProposalJSON(t, proposal), ts(now))
			f.call(t, "historical-host-call", `{"version":"host_command_proposal.v1"}`, "", historicalProposalJSON(t, map[string]any{"version": "supervisor_tool_result.v1", "tool": "host_command_propose", "status": "completed", "metadata": map[string]string{"proposal_id": proposal.ID}}))
			action := domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionWait, Message: "Await saved result", Reason: "operator decision required"}
			if _, _, _, err := st.CompleteSupervisorTurn(ctx, f.turn.Checkpoint, f.response, action, policy.Decision{Allowed: true}, 0); err != nil {
				t.Fatal(err)
			}
			f.release(t)
			decision := runner.HostCommandReviewApprove
			if scenario == "denied" {
				decision = runner.HostCommandReviewDeny
			}
			review := runner.HostCommandReview{ID: "history-host-review", ProtocolVersion: runner.HostCommandReviewProtocolVersion,
				PolicyVersion: runner.HostCommandPolicyVersion, ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint,
				RunID: run.ID, Decision: decision, ReviewedBy: "operator", Reason: "saved review",
				OperationKeyDigest: strings.Repeat("b", 64), SingleUseExecutionAuthorized: decision == runner.HostCommandReviewApprove, CreatedAt: now}
			review.RequestFingerprint = runner.HostCommandReviewRequestFingerprint(review)
			review.Fingerprint = runner.HostCommandReviewFingerprint(review)
			if err := review.Validate(); err != nil {
				t.Fatal(err)
			}
			historicalProposalExec(t, st, `INSERT INTO host_command_proposal_reviews(id,proposal_id,proposal_fingerprint,run_id,decision,reviewed_by,operation_key_digest,request_fingerprint,single_use_execution_authorized,capability_grant,review_fingerprint,payload_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,0,?,?,?)`, review.ID, proposal.ID, proposal.Fingerprint, run.ID, review.Decision, review.ReviewedBy, review.OperationKeyDigest, review.RequestFingerprint, review.SingleUseExecutionAuthorized, review.Fingerprint, historicalProposalJSON(t, review), ts(now))
			var receipt *runner.HostExecutionReceipt
			if scenario != "denied" {
				intent := runner.HostExecutionIntent{
					ProtocolVersion: runner.HostCommandIntentProtocolVersion, PolicyVersion: runner.HostExecutionPolicyVersion,
					OperationKeyDigest: strings.Repeat("c", 64), RunID: run.ID, MissionID: run.MissionID,
					SessionID: run.SessionID, WorkspaceID: proposal.WorkspaceID,
					InteractionSnapshotID: proposal.InteractionSnapshotID, InteractionRevision: proposal.InteractionRevision,
					ExecutionProfileRevision: proposal.ExecutionProfileRevision, PermissionSnapshotID: proposal.PermissionSnapshotID,
					PermissionRevision: proposal.PermissionRevision, PermissionMode: proposal.PermissionMode,
					AuthorizationProposalID: proposal.ID, AuthorizationProposalFingerprint: proposal.Fingerprint,
					AuthorizationReviewID: review.ID, AuthorizationReviewFingerprint: review.Fingerprint,
					Spec: proposal.Spec, RequestedBy: review.ReviewedBy, NonSandboxed: true, CreatedAt: now,
				}
				intent.RequestID = runner.HostExecutionRequestID(intent.RunID, intent.OperationKeyDigest, intent.Spec.Fingerprint)
				if err := intent.Validate(); err != nil {
					t.Fatal(err)
				}
				data := historicalProposalJSON(t, intent)
				historicalProposalExec(t, st, `INSERT INTO host_command_proposal_execution_intents(request_id,proposal_id,review_id,operation_key_digest,run_id,session_id,workspace_id,permission_mode,spec_fingerprint,intent_fingerprint,non_sandboxed,automatic_retry_allowed,payload_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,1,0,?,?)`, intent.RequestID, proposal.ID, review.ID, intent.OperationKeyDigest, run.ID, run.SessionID, proposal.WorkspaceID, proposal.PermissionMode, proposal.Spec.Fingerprint, session.ContentSHA256(data), data, ts(now))
				if scenario != "unknown" {
					saved := historicalReceipt(intent.RequestID, now)
					status := "completed"
					if scenario == "nonzero" {
						saved.ExitCode = 7
						status = "failed"
					}
					receipt = &saved
					evidence := session.NewEvidenceMessage(run.SessionID, session.SourceGoCommandResult, "host-command-proposal:"+proposal.ID, "UNTRUSTED HOST COMMAND RESULT\nverified helper output")
					tx, err := st.db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					message, err := saveSessionMessageTx(ctx, tx, evidence)
					if err != nil {
						_ = tx.Rollback()
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					result := runner.HostCommandProposalResult{
						ID: "history-host-result", ProtocolVersion: runner.HostCommandResultProtocolVersion, PolicyVersion: runner.HostCommandPolicyVersion,
						ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint, ReviewID: review.ID, ReviewFingerprint: review.Fingerprint,
						RequestID: saved.RequestID, RunID: run.ID, SessionID: run.SessionID, Status: status,
						SourceKind: evidence.Provenance.SourceKind, SourceRef: evidence.Provenance.SourceRef,
						ContentSHA256: session.ContentSHA256(evidence.Content), CreatedAt: now,
						SavedOutput: &runner.HostCommandSavedOutput{
							Stdout: runner.HostCommandSavedStream{Text: "verified helper output", Redacted: true},
							Stderr: runner.HostCommandSavedStream{Redacted: true},
						},
					}
					result.Fingerprint = runner.HostCommandProposalResultFingerprint(result)
					if err := result.Validate(); err != nil {
						t.Fatal(err)
					}
					receiptJSON := historicalProposalJSON(t, saved)
					historicalProposalExec(t, st, `INSERT INTO host_command_proposal_results(id,proposal_id,review_id,request_id,run_id,session_id,session_message_id,status,result_fingerprint,receipt_fingerprint,result_json,receipt_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, result.ID, proposal.ID, review.ID, saved.RequestID, run.ID, run.SessionID, message.ID, status, result.Fingerprint, session.ContentSHA256(receiptJSON), historicalProposalJSON(t, result), receiptJSON, ts(now))
				}
			}

			tables := []string{"host_command_proposals", "host_command_proposal_reviews", "host_command_proposal_execution_intents", "host_command_proposal_results"}
			before := historicalProposalRows(t, st, tables)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err := Open(f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			view, err := application.NewHostCommandHistory(st).Get(ctx, proposal.ID)
			if err != nil || view.Uncertain != (scenario == "unknown") {
				t.Fatalf("history unknown=%v: %v", view.Uncertain, err)
			}
			assertHostCommandHandoffHTTP(t, st, run, proposal, receipt, string(decision))
			resumed, ready, err := st.PrepareApprovalContinuation(ctx, run.ID, "host_command", proposal.ID)
			if scenario == "unknown" {
				if err == nil || ready {
					t.Fatalf("unknown execution resumed: %v %v", ready, err)
				}
			} else {
				if err != nil || !ready || resumed.Handoff.Operation.ID == "" {
					t.Fatalf("saved outcome did not resume: %+v %v %v", resumed, ready, err)
				}
				again, found, err := st.PrepareApprovalContinuation(ctx, run.ID, "host_command", proposal.ID)
				if err != nil || !found || again.Handoff.Operation.ID != resumed.Handoff.Operation.ID {
					t.Fatal("continuation replay changed", err)
				}
			}
			if !reflect.DeepEqual(before, historicalProposalRows(t, st, tables)) {
				t.Fatal("history mutated during upgrade/read/resume")
			}
			assertNoForeignKeyViolations(t, st.db)
		})
	}
}
func historicalReceipt(id string, at time.Time) runner.HostExecutionReceipt {
	return runner.HostExecutionReceipt{RequestID: id, ProtocolVersion: runner.HostCommandReceiptProtocolVersion, PolicyVersion: runner.HostExecutionPolicyVersion, Backend: "historical-fixture", StdoutObservedBytes: 22, StdoutCapturedBytes: 22, StdoutPrefixSHA256: session.ContentSHA256("verified helper output"), StderrPrefixSHA256: session.ContentSHA256(""), StartedAt: at, CompletedAt: at, TreeReaped: true, NonSandboxed: true, JobAssignedAtCreation: true, KillOnJobClose: true, ActiveProcessLimit: runner.MaxHostActiveProcesses, JobMemoryLimit: runner.MaxHostProcessMemoryBytes, StdinClosed: true, NetworkRequested: true, ProductExecutionEnabled: true}
}

func TestHistoricalRiskResumeRequiresSavedOutcomeExactTurnAndNoLiveLease(t *testing.T) {
	for _, scenario := range []string{"pending", "denied", "unknown", "completed", "wrong_turn", "wrong_call", "running_wrong_turn", "running_wrong_call"} {
		t.Run(scenario, func(t *testing.T) {
			f := newHistoricalProposalFixture(t, domain.RunExecutionPermissionWorkspaceAccess)
			st := f.state
			ctx := t.Context()
			now := time.Now().UTC()
			run := f.turn.Run
			scope, err := runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{Kinds: []runner.RiskEscalationKind{runner.RiskEscalationNetwork}, NetworkTargets: []string{"example.test:443"}, NetworkPurpose: "historical request"})
			if err != nil {
				t.Fatal(err)
			}
			p := runner.RiskEscalationProposal{
				ID: "risk-escalation-history-proposal", ProtocolVersion: runner.RiskEscalationProtocolVersion, PolicyVersion: runner.RiskEscalationPolicyVersion,
				RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID, WorkspaceID: f.turn.Mission.WorkspaceID,
				RootAgentID: f.turn.Agent.ID, SupervisorTurn: f.turn.Checkpoint.NextTurn, SupervisorToolCallID: "historical-risk-call",
				ToolInvocationID: "historical-risk-invocation", ModeSnapshotID: f.mode.ID, ModeRevision: f.mode.Revision,
				InteractionSnapshotID: f.interaction.ID, InteractionRevision: f.interaction.Revision,
				ExecutionProfileSnapshotID: f.profile.ID, ExecutionProfileRevision: f.profile.Revision,
				PermissionSnapshotID: f.permission.ID, PermissionRevision: f.permission.Revision, PermissionMode: f.permission.Mode,
				WorkspaceRootFingerprint: strings.Repeat("a", 64), CapabilityGeneration: strings.Repeat("b", 64),
				Spec: f.spec, Scope: scope, ResourceBudget: runner.NewRiskEscalationResourceBudget(f.spec), RequestedBy: "run_supervisor", CreatedAt: now,
			}
			p.Fingerprint = runner.RiskEscalationProposalFingerprint(p)
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(scenario, "wrong_turn") {
				p.SupervisorTurn++
				p.Fingerprint = runner.RiskEscalationProposalFingerprint(p)
			}
			historicalProposalExec(t, st, `INSERT INTO run_tool_calls(id,run_id,session_id,workspace_id,tool_name,action_class,sequence,created_at) VALUES (?,?,?,?,'host_command_propose','agent_proposal',1,?)`, p.ToolInvocationID, run.ID, run.SessionID, p.WorkspaceID, ts(now))
			historicalProposalExec(t, st, `INSERT INTO risk_escalation_proposals(id,run_id,mission_id,session_id,workspace_id,root_agent_id,supervisor_turn,supervisor_tool_call_id,tool_invocation_id,mode_snapshot_id,mode_revision,interaction_snapshot_id,interaction_revision,execution_profile_snapshot_id,execution_profile_revision,permission_snapshot_id,permission_revision,permission_mode,workspace_root_fingerprint,capability_generation,spec_fingerprint,scope_fingerprint,proposal_fingerprint,payload_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.RunID, p.MissionID, p.SessionID, p.WorkspaceID, p.RootAgentID, p.SupervisorTurn, p.SupervisorToolCallID, p.ToolInvocationID, p.ModeSnapshotID, p.ModeRevision, p.InteractionSnapshotID, p.InteractionRevision, p.ExecutionProfileSnapshotID, p.ExecutionProfileRevision, p.PermissionSnapshotID, p.PermissionRevision, p.PermissionMode, p.WorkspaceRootFingerprint, p.CapabilityGeneration, p.Spec.Fingerprint, p.Scope.Fingerprint, p.Fingerprint, historicalProposalJSON(t, p), ts(now))
			status := approval.StatusApproved
			reviewer := "operator"
			var decided any = ts(now)
			if scenario == "denied" || strings.Contains(scenario, "wrong_") {
				status = approval.StatusDenied
			}
			if scenario == "pending" {
				status = approval.StatusPending
				reviewer = ""
				decided = nil
			}
			historicalProposalExec(t, st, `INSERT INTO tool_approvals(id,idempotency_key,proposal_id,run_id,session_id,workspace_id,tool_name,action_class,mode,status,request_fingerprint,decision_reason,requested_by,reviewed_by,version,created_at,updated_at,decided_at) VALUES ('historical-risk-approval','historical-risk-key',?,?,?,?,'host_command_propose','risk_escalation','per_call',?,?,'historical decision','run_supervisor',?,1,?,?,?)`, p.ID, run.ID, run.SessionID, p.WorkspaceID, status, strings.Repeat("c", 64), reviewer, ts(now), ts(now), decided)
			if scenario == "unknown" || scenario == "completed" {
				auth := runner.RiskEscalationAuthorization{
					ProtocolVersion: runner.RiskEscalationProtocolVersion, ProposalID: p.ID, ProposalFingerprint: p.Fingerprint,
					ApprovalID: "historical-risk-approval", ApprovalVersion: 1, ApprovalFingerprint: strings.Repeat("c", 64),
					ScopeFingerprint: p.Scope.Fingerprint, ReviewedBy: "operator", AuthorizedAt: now,
				}
				if err := auth.Validate(); err != nil {
					t.Fatal(err)
				}
				intent := runner.HostExecutionIntent{
					ProtocolVersion: runner.HostCommandIntentProtocolVersion, PolicyVersion: runner.HostExecutionPolicyVersion,
					OperationKeyDigest: strings.Repeat("d", 64), RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID,
					WorkspaceID: p.WorkspaceID, InteractionSnapshotID: p.InteractionSnapshotID, InteractionRevision: p.InteractionRevision,
					ExecutionProfileRevision: p.ExecutionProfileRevision, PermissionSnapshotID: p.PermissionSnapshotID,
					PermissionRevision: p.PermissionRevision, PermissionMode: p.PermissionMode,
					AuthorizationProposalID: p.ID, AuthorizationProposalFingerprint: p.Fingerprint,
					AuthorizationReviewID: auth.ApprovalID, AuthorizationReviewFingerprint: runner.RiskEscalationAuthorizationFingerprint(auth),
					Spec: p.Spec, RequestedBy: auth.ReviewedBy, NonSandboxed: true, CreatedAt: now,
				}
				intent.RequestID = runner.HostExecutionRequestID(intent.RunID, intent.OperationKeyDigest, intent.Spec.Fingerprint)
				if err := intent.Validate(); err != nil {
					t.Fatal(err)
				}
				data := historicalProposalJSON(t, intent)
				historicalProposalExec(t, st, `INSERT INTO risk_escalation_execution_intents(request_id,proposal_id,approval_id,grant_id,grant_consumption_id,authorization_fingerprint,intent_fingerprint,payload_json,created_at) VALUES (?,?,?,NULL,NULL,?,?,?,?)`, intent.RequestID, p.ID, auth.ApprovalID, runner.RiskEscalationAuthorizationFingerprint(auth), session.ContentSHA256(data), data, ts(now))
				if scenario == "completed" {
					evidence := session.NewEvidenceMessage(run.SessionID, session.SourceGoCommandResult, "risk-escalation:"+p.ID, "verified helper output")
					tx, err := st.db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					message, err := saveSessionMessageTx(ctx, tx, evidence)
					if err != nil {
						_ = tx.Rollback()
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					result := runner.RiskEscalationResult{
						ID: "history-risk-result", ProtocolVersion: runner.RiskEscalationProtocolVersion,
						ProposalID: p.ID, ProposalFingerprint: p.Fingerprint, ApprovalID: auth.ApprovalID, ApprovalFingerprint: auth.ApprovalFingerprint,
						RequestID: intent.RequestID, RunID: run.ID, SessionID: run.SessionID, Status: "completed",
						SourceKind: evidence.Provenance.SourceKind, SourceRef: evidence.Provenance.SourceRef,
						ContentSHA256: session.ContentSHA256(evidence.Content), CreatedAt: now,
					}
					result.Fingerprint = runner.RiskEscalationResultFingerprint(result)
					if err := result.Validate(); err != nil {
						t.Fatal(err)
					}
					receiptJSON := historicalProposalJSON(t, historicalReceipt(intent.RequestID, now))
					historicalProposalExec(t, st, `INSERT INTO risk_escalation_results(id,proposal_id,approval_id,request_id,run_id,session_id,session_message_id,status,error_code,result_fingerprint,receipt_fingerprint,result_json,receipt_json,created_at) VALUES (?,?,?,?,?,?,?,'completed','',?,?,?,?,?)`, result.ID, p.ID, auth.ApprovalID, intent.RequestID, run.ID, run.SessionID, message.ID, result.Fingerprint, session.ContentSHA256(receiptJSON), historicalProposalJSON(t, result), receiptJSON, ts(now))
				}
			}
			callID := p.SupervisorToolCallID
			if strings.Contains(scenario, "wrong_call") {
				callID = "different-original-call"
			}
			f.call(t, callID, `{"version":"risk_escalation.v1"}`, `{}`, "")
			// A real old waiting state; no current writer creates an escalation.
			tx, err := st.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			parked := run
			if err := transitionSupervisorRunTx(ctx, tx, &parked, domain.RunWaitingApproval, "saved risk wait", now); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if strings.HasPrefix(scenario, "running_") {
				if err := transitionSupervisorRunTx(ctx, tx, &parked, domain.RunRunning, "historical concurrent resume", now); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.ResumeRiskEscalationRun(ctx, p.ID, "resume"); err == nil {
				t.Fatal("live owner allowed a second continuation")
			}
			f.release(t)
			tables := []string{"risk_escalation_proposals", "risk_escalation_execution_intents", "risk_escalation_results", "tool_approvals"}
			before := historicalProposalRows(t, st, tables)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = Open(f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			view, err := application.NewHostCommandHistory(st).Get(ctx, p.ID)
			if err != nil || view.Uncertain != (scenario == "unknown") {
				t.Fatalf("risk history: %+v %v", view, err)
			}
			resumed, replayed, err := st.ResumeRiskEscalationRun(ctx, p.ID, "resume saved risk")
			if strings.Contains(scenario, "wrong_") {
				if apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("mismatched historical call resumed: %v", err)
				}
			} else if scenario == "pending" {
				if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
					t.Fatalf("pending risk resumed: %v", err)
				}
			} else {
				if err != nil || replayed || resumed.Status != domain.RunRunning {
					t.Fatalf("resume: %+v %v %v", resumed, replayed, err)
				}
				if _, replayed, err := st.ResumeRiskEscalationRun(ctx, p.ID, "repeat"); err != nil || !replayed {
					t.Fatal("resume did not replay", err)
				}
			}
			if !reflect.DeepEqual(before, historicalProposalRows(t, st, tables)) {
				t.Fatal("resume changed historical execution or approval")
			}
			assertNoForeignKeyViolations(t, st.db)
		})
	}
}
