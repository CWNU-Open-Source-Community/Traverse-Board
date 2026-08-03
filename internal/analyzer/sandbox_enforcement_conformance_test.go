//go:build windows || linux

package analyzer

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

const (
	analyzerSandboxHelperModeEnv = "CYBERAGENT_ANALYZER_SANDBOX_HELPER_MODE"
	analyzerSandboxSentinelEnv   = "CYBERAGENT_ANALYZER_SANDBOX_SECRET_SENTINEL"
	analyzerSandboxMemoryEnv     = "CYBERAGENT_ANALYZER_SANDBOX_MEMORY_BYTES"
	analyzerSandboxCPUEnv        = "CYBERAGENT_ANALYZER_SANDBOX_CPU_MS"
)

type analyzerSandboxEnforcementObservation struct {
	BackendCandidate             string
	ResourcePlanSHA256           string
	SandboxPlanSHA256            string
	HardLimitsConfigured         bool
	MemoryLimitConfigured        bool
	CPUTimeLimitConfigured       bool
	ProcessLimitObserved         bool
	EnvironmentScrubbedObserved  bool
	NoNewPrivilegesObserved      bool
	NetworkDenyObserved          bool
	ProcessTreeReapObserved      bool
	ReadOnlyFilesystemObserved   bool
	DedicatedIdentityObserved    bool
	ImmutableHandleObserved      bool
	CompleteSandboxEnforcement   bool
	TestConformanceOnly          bool
	ProductProcessStarterPresent bool
	ExecutionAuthorized          bool
	ProductInvocationAuthorized  bool
	ResultPersistenceAuthorized  bool
	ArtifactCommitAuthorized     bool
}

func TestAnalyzerSandboxEnforcementConformance(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	observation := observeAnalyzerSandboxEnforcement(t, chain.plan)
	resourceDigest, resourceOK := canonicalSHA256(chain.plan.Resources)
	sandboxDigest, sandboxOK := canonicalSHA256(chain.plan.Sandbox)
	if !resourceOK || !sandboxOK ||
		observation.BackendCandidate != chain.plan.Sandbox.BackendCandidate ||
		observation.ResourcePlanSHA256 != resourceDigest ||
		observation.SandboxPlanSHA256 != sandboxDigest ||
		!observation.HardLimitsConfigured || !observation.MemoryLimitConfigured ||
		!observation.CPUTimeLimitConfigured || !observation.EnvironmentScrubbedObserved ||
		!observation.ProcessTreeReapObserved ||
		observation.ReadOnlyFilesystemObserved || observation.DedicatedIdentityObserved ||
		observation.ImmutableHandleObserved || observation.CompleteSandboxEnforcement ||
		!observation.TestConformanceOnly || observation.ProductProcessStarterPresent ||
		observation.ExecutionAuthorized || observation.ProductInvocationAuthorized ||
		observation.ResultPersistenceAuthorized || observation.ArtifactCommitAuthorized {
		t.Fatalf("unsafe or incomplete sandbox conformance observation: %#v", observation)
	}
	switch runtime.GOOS {
	case "windows":
		if !observation.ProcessLimitObserved || observation.NoNewPrivilegesObserved ||
			observation.NetworkDenyObserved {
			t.Fatalf("unexpected Windows conformance observation: %#v", observation)
		}
	case "linux":
		if observation.ProcessLimitObserved || !observation.NoNewPrivilegesObserved ||
			!observation.NetworkDenyObserved {
			t.Fatalf("unexpected Linux conformance observation: %#v", observation)
		}
	default:
		t.Fatalf("unsupported conformance GOOS %q", runtime.GOOS)
	}
}

func TestAnalyzerSandboxEnforcementHelper(t *testing.T) {
	mode := os.Getenv(analyzerSandboxHelperModeEnv)
	if mode == "" {
		return
	}
	if err := runAnalyzerSandboxEnforcementHelper(mode); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
