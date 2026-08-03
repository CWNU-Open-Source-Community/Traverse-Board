package analyzer

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestAnalyzerLaunchPlanAndReviewRemainDesignOnly(t *testing.T) {
	chain := mustAnalyzerReleaseChain(t)
	plan, code := BuildAnalyzerLaunchPlan(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release)
	if code != "" {
		t.Fatal(code)
	}
	if plan.ProtocolVersion != AnalyzerLaunchPlanProtocolVersion || !plan.RequestBound ||
		!plan.ExecutableBound || !plan.ReleasePolicyBound || !plan.OperatorReviewRequired ||
		plan.OperatorReviewed || !plan.DesignCandidateOnly || plan.EnforcementReady ||
		!plan.StartBlocked || plan.PathIncluded || plan.CommandIncluded || plan.ArgvIncluded ||
		plan.EnvironmentIncluded || plan.InputBodyIncluded || plan.ProcessStarterPresent ||
		plan.ExecutionAuthorized || plan.ProductInvocationAuthorized ||
		plan.ResultPersistenceAuthorized || plan.ArtifactCommitAuthorized {
		t.Fatalf("unsafe or incomplete launch plan: %#v", plan)
	}
	if !plan.Resources.HardLimitsRequired || plan.Resources.HardLimitsVerified ||
		plan.Resources.MemoryBytes != AnalyzerLaunchPlanMemoryBytes ||
		plan.Resources.ProcessCount != AnalyzerLaunchPlanMaxProcesses ||
		plan.Resources.CombinedOutputBytes != chain.candidate.Limits.MaxOutputBytes ||
		!plan.Sandbox.NetworkDenyRequired || !plan.Sandbox.EnforcementRequired ||
		plan.Sandbox.EnforcementVerified || !plan.Sandbox.ImmutableHandleRequired ||
		!plan.Sandbox.ProcessTreeReapRequired {
		t.Fatalf("unsafe resource or sandbox candidate: resources=%#v sandbox=%#v",
			plan.Resources, plan.Sandbox)
	}

	reviewer := strings.Repeat("f", 64)
	review, code := ReviewAnalyzerLaunchPlan(plan, chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, reviewer, AnalyzerLaunchPlanReviewConfirmation)
	if code != "" {
		t.Fatal(code)
	}
	if review.ProtocolVersion != AnalyzerLaunchPlanReviewProtocolVersion ||
		!review.OperatorReviewed || !review.DesignReviewOnly || review.ExecutionApproved ||
		review.ProcessStartAuthorized || review.ProductInvocationAuthorized ||
		review.OperatorOverrideAllowed {
		t.Fatalf("review granted authority: %#v", review)
	}

	encodedPlan, code := EncodeAnalyzerLaunchPlan(plan, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release)
	if code != "" {
		t.Fatal(code)
	}
	decodedPlan, code := DecodeAnalyzerLaunchPlan(encodedPlan, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release)
	if code != "" || !reflect.DeepEqual(decodedPlan, plan) {
		t.Fatalf("plan round trip failed: code=%s value=%#v", code, decodedPlan)
	}
	encodedReview, code := EncodeAnalyzerLaunchPlanReview(review, plan, chain.candidate,
		chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
		chain.manifest, chain.allowlist, chain.release, reviewer)
	if code != "" {
		t.Fatal(code)
	}
	decodedReview, code := DecodeAnalyzerLaunchPlanReview(encodedReview, plan, chain.candidate,
		chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
		chain.manifest, chain.allowlist, chain.release, reviewer)
	if code != "" || !reflect.DeepEqual(decodedReview, review) {
		t.Fatalf("review round trip failed: code=%s value=%#v", code, decodedReview)
	}
	assertExactObjectKeys(t, encodedReview, []string{"confirmation_sha256", "decision",
		"design_review_only", "execution_approved", "launch_plan_sha256",
		"operator_override_allowed", "operator_reviewed", "process_start_authorized",
		"product_invocation_authorized", "protocol_version", "release_candidate_sha256",
		"reviewer_identity_sha256"})
}

func TestAnalyzerLaunchPlanRejectsAuthorityAndEnforcementDrift(t *testing.T) {
	chain := mustAnalyzerReleaseChain(t)
	plan, code := BuildAnalyzerLaunchPlan(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release)
	if code != "" {
		t.Fatal(code)
	}
	for name, mutate := range map[string]func(*AnalyzerLaunchPlan){
		"execution": func(value *AnalyzerLaunchPlan) { value.ExecutionAuthorized = true },
		"starter":   func(value *AnalyzerLaunchPlan) { value.ProcessStarterPresent = true },
		"network": func(value *AnalyzerLaunchPlan) {
			value.Sandbox.NetworkDenyRequired = false
		},
		"unverified enforcement": func(value *AnalyzerLaunchPlan) {
			value.Sandbox.EnforcementVerified = true
		},
		"resource widening": func(value *AnalyzerLaunchPlan) { value.Resources.ProcessCount = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			mutated := plan
			mutate(&mutated)
			if got := ValidateAnalyzerLaunchPlan(mutated, chain.candidate, chain.raw,
				chain.executable, chain.identity, chain.preflight, chain.evidence,
				chain.manifest, chain.allowlist, chain.release); got != CodeInvalidResult {
				t.Fatalf("drift code = %s", got)
			}
		})
	}
	if _, code := ReviewAnalyzerLaunchPlan(plan, chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, strings.Repeat("f", 64), "APPROVE-RUN"); code != CodeInvalidContent {
		t.Fatalf("wrong review confirmation code = %s", code)
	}
}

func TestAnalyzerLaunchPlanRejectsSchemaWidening(t *testing.T) {
	chain := mustAnalyzerReleaseChain(t)
	plan, code := BuildAnalyzerLaunchPlan(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release)
	if code != "" {
		t.Fatal(code)
	}
	encoded, code := EncodeAnalyzerLaunchPlan(plan, chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release)
	if code != "" {
		t.Fatal(code)
	}
	text := string(encoded)
	for name, malformed := range map[string]string{
		"future": strings.Replace(text, AnalyzerLaunchPlanProtocolVersion,
			"analyzer_launch_plan.v2", 1),
		"unknown": strings.Replace(text, `"command_included":false`,
			`"command_included":false,"command":"analyzer"`, 1),
		"duplicate": strings.Replace(text, `"execution_authorized":false`,
			`"execution_authorized":false,"execution_authorized":false`, 1),
		"missing false": strings.Replace(text, `,"artifact_commit_authorized":false`, "", 1),
		"nested unknown": strings.Replace(text, `"network_policy":"deny_all"`,
			`"network_policy":"deny_all","proxy":"ambient"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerLaunchPlan([]byte(malformed), chain.candidate, chain.raw,
				chain.executable, chain.identity, chain.preflight, chain.evidence,
				chain.manifest, chain.allowlist, chain.release); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}

type analyzerReleaseChain struct {
	analyzerExecutableEvidenceChain
	manifest  AnalyzerReleaseManifest
	allowlist AnalyzerReleaseAllowlist
	release   AnalyzerReleaseCandidate
}

func mustAnalyzerReleaseChain(t *testing.T) analyzerReleaseChain {
	t.Helper()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skipf("runtime GOOS %q has no launch sandbox candidate", runtime.GOOS)
	}
	chain := mustAnalyzerExecutableEvidenceChain(t)
	manifest := mustAnalyzerReleaseManifest(t, chain.evidence)
	entry, code := AnalyzerReleaseAllowlistEntryForManifest(manifest, chain.evidence)
	if code != "" {
		t.Fatal(code)
	}
	allowlist, code := BuildAnalyzerReleaseAllowlist([]AnalyzerReleaseAllowlistEntry{entry})
	if code != "" {
		t.Fatal(code)
	}
	release, code := BuildAnalyzerReleaseCandidate(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, manifest, allowlist)
	if code != "" {
		t.Fatal(code)
	}
	return analyzerReleaseChain{analyzerExecutableEvidenceChain: chain, manifest: manifest,
		allowlist: allowlist, release: release}
}
