package analyzer

import (
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzerScopeLimitsApprovalBindsExactDesignWithoutExecutionAuthority(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	approval := chain.approval
	if approval.ProtocolVersion != AnalyzerScopeLimitsApprovalProtocolVersion ||
		!approval.RequestScopeBound || !approval.ExecutableScopeBound ||
		!approval.ReleaseScopeBound || !approval.ProvenanceScopeBound ||
		!approval.ResourceLimitsBound || !approval.SandboxRequirementsBound ||
		!approval.DesignReviewBound || !approval.OperatorScopeLimitsApproved ||
		approval.ApprovalAuthenticated || approval.DurableGrant ||
		approval.CapabilityGrantIssued || approval.ExecutionAuthorized ||
		approval.ProcessStartAuthorized || approval.ProductInvocationAuthorized ||
		approval.NetworkAuthorized || approval.HostFilesystemAuthorized ||
		approval.ResultPersistenceAuthorized || approval.ArtifactCommitAuthorized ||
		approval.OperatorOverrideAllowed {
		t.Fatalf("unsafe or incomplete scope approval: %#v", approval)
	}
	if !reflect.DeepEqual(approval.Resources, chain.plan.Resources) ||
		!reflect.DeepEqual(approval.Sandbox, chain.plan.Sandbox) {
		t.Fatal("approval did not embed the exact resource and sandbox plans")
	}

	encoded, code := EncodeAnalyzerScopeLimitsApproval(approval, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release, chain.rawStatement, chain.publicKey, chain.signature,
		chain.verification, chain.plan, chain.review, chain.operatorIdentity)
	if code != "" {
		t.Fatal(code)
	}
	decoded, code := DecodeAnalyzerScopeLimitsApproval(encoded, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release, chain.rawStatement, chain.publicKey, chain.signature,
		chain.verification, chain.plan, chain.review, chain.operatorIdentity)
	if code != "" || !reflect.DeepEqual(decoded, approval) {
		t.Fatalf("approval round trip failed: code=%s value=%#v", code, decoded)
	}
	assertExactObjectKeys(t, encoded, []string{"analyzer", "approval_authenticated",
		"artifact_commit_authorized", "candidate_sha256", "capability_grant_issued",
		"confirmation_sha256", "decision", "design_review_bound", "durable_grant",
		"executable_scope_bound", "executable_sha256", "execution_authorized",
		"host_filesystem_authorized", "launch_plan_review_sha256", "launch_plan_sha256",
		"network_authorized", "operator_identity_sha256", "operator_override_allowed",
		"operator_scope_limits_approved", "process_start_authorized",
		"product_invocation_authorized", "protocol_version", "provenance_scope_bound",
		"provenance_verification_sha256", "release_candidate_sha256", "release_scope_bound",
		"request_id", "request_scope_bound", "resource_limits_bound", "resource_plan_sha256",
		"resources", "result_persistence_authorized", "sandbox", "sandbox_plan_sha256",
		"sandbox_requirements_bound", "target_goarch", "target_goos"})
}

func TestAnalyzerScopeLimitsApprovalRejectsIdentityPlanAndAuthorityDrift(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	if _, code := BuildAnalyzerScopeLimitsApproval(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, chain.rawStatement, chain.publicKey, chain.signature, chain.verification,
		chain.plan, chain.review, strings.Repeat("1", 64),
		AnalyzerScopeLimitsApprovalConfirmation); code != CodeInvalidContent {
		t.Fatalf("operator continuity code = %s", code)
	}
	if _, code := BuildAnalyzerScopeLimitsApproval(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, chain.rawStatement, chain.publicKey, chain.signature, chain.verification,
		chain.plan, chain.review, chain.operatorIdentity, "APPROVE-RUN"); code != CodeInvalidContent {
		t.Fatalf("confirmation code = %s", code)
	}

	badVerification := chain.verification
	badVerification.ReleaseApproved = true
	if _, code := BuildAnalyzerScopeLimitsApproval(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, chain.rawStatement, chain.publicKey, chain.signature, badVerification,
		chain.plan, chain.review, chain.operatorIdentity,
		AnalyzerScopeLimitsApprovalConfirmation); code != CodeInvalidResult {
		t.Fatalf("verification drift code = %s", code)
	}
	badPlan := chain.plan
	badPlan.Resources.MemoryBytes++
	if _, code := BuildAnalyzerScopeLimitsApproval(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, chain.rawStatement, chain.publicKey, chain.signature, chain.verification,
		badPlan, chain.review, chain.operatorIdentity,
		AnalyzerScopeLimitsApprovalConfirmation); code != CodeInvalidResult {
		t.Fatalf("plan drift code = %s", code)
	}
	mutated := chain.approval
	mutated.ExecutionAuthorized = true
	if code := ValidateAnalyzerScopeLimitsApproval(mutated, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release, chain.rawStatement, chain.publicKey, chain.signature,
		chain.verification, chain.plan, chain.review,
		chain.operatorIdentity); code != CodeInvalidResult {
		t.Fatalf("authority drift code = %s", code)
	}
}

func TestAnalyzerScopeLimitsApprovalRejectsSchemaWidening(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	encoded, code := EncodeAnalyzerScopeLimitsApproval(chain.approval, chain.candidate,
		chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
		chain.manifest, chain.allowlist, chain.release, chain.rawStatement, chain.publicKey,
		chain.signature, chain.verification, chain.plan, chain.review, chain.operatorIdentity)
	if code != "" {
		t.Fatal(code)
	}
	text := string(encoded)
	for name, malformed := range map[string]string{
		"future": strings.Replace(text, AnalyzerScopeLimitsApprovalProtocolVersion,
			"analyzer_scope_limits_approval.v2", 1),
		"unknown": strings.Replace(text, `"execution_authorized":false`,
			`"execution_authorized":false,"command":"tool"`, 1),
		"duplicate": strings.Replace(text, `"durable_grant":false`,
			`"durable_grant":false,"durable_grant":false`, 1),
		"missing false": strings.Replace(text, `,"operator_override_allowed":false`, "", 1),
		"nested unknown": strings.Replace(text, `"hard_limits_verified":false`,
			`"hard_limits_verified":false,"ambient_limit":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerScopeLimitsApproval([]byte(malformed), chain.candidate,
				chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
				chain.manifest, chain.allowlist, chain.release, chain.rawStatement,
				chain.publicKey, chain.signature, chain.verification, chain.plan, chain.review,
				chain.operatorIdentity); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}

type analyzerScopeApprovalChain struct {
	analyzerSignedReleaseChain
	plan             AnalyzerLaunchPlan
	review           AnalyzerLaunchPlanReview
	operatorIdentity string
	approval         AnalyzerScopeLimitsApproval
}

func mustAnalyzerScopeApprovalChain(t *testing.T) analyzerScopeApprovalChain {
	t.Helper()
	chain := mustAnalyzerSignedReleaseChain(t)
	plan, code := BuildAnalyzerLaunchPlan(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release)
	if code != "" {
		t.Fatal(code)
	}
	operatorIdentity := strings.Repeat("7", 64)
	review, code := ReviewAnalyzerLaunchPlan(plan, chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, operatorIdentity, AnalyzerLaunchPlanReviewConfirmation)
	if code != "" {
		t.Fatal(code)
	}
	approval, code := BuildAnalyzerScopeLimitsApproval(chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release, chain.rawStatement, chain.publicKey, chain.signature,
		chain.verification, plan, review, operatorIdentity,
		AnalyzerScopeLimitsApprovalConfirmation)
	if code != "" {
		t.Fatal(code)
	}
	return analyzerScopeApprovalChain{analyzerSignedReleaseChain: chain, plan: plan,
		review: review, operatorIdentity: operatorIdentity, approval: approval}
}
