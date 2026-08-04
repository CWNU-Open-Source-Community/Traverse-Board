//go:build windows || linux

package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

const (
	analyzerIsolationHelperModeEnv = "CYBERAGENT_ANALYZER_ISOLATION_HELPER_MODE"
	analyzerIsolationHandleEnv     = "CYBERAGENT_ANALYZER_ISOLATION_HANDLE"
	analyzerIsolationInputEnv      = "CYBERAGENT_ANALYZER_ISOLATION_INPUT"
	analyzerIsolationStagingEnv    = "CYBERAGENT_ANALYZER_ISOLATION_STAGING"
	analyzerIsolationOutsideEnv    = "CYBERAGENT_ANALYZER_ISOLATION_OUTSIDE"
)

type analyzerImmutableHandleObservation struct {
	Mechanism                    string
	ScopeApprovalSHA256          string
	CallerHandleRetained         bool
	ChildHandleInherited         bool
	PathReplacedBeforeChildRead  bool
	OriginalBytesObserved        bool
	ReplacementBytesRejected     bool
	PathIncludedInChildAuthority bool
	TestConformanceOnly          bool
	ProductProcessStarterPresent bool
	ExecutionAuthorized          bool
}

type analyzerLowPrivilegeIdentityObservation struct {
	Mechanism                    string
	ScopeApprovalSHA256          string
	SeparateIdentityContext      bool
	NonAdministratorObserved     bool
	AmbientPrivilegesDenied      bool
	NoNewPrivilegesObserved      bool
	DedicatedAccountObserved     bool
	TestConformanceOnly          bool
	ProductProcessStarterPresent bool
	ExecutionAuthorized          bool
}

type analyzerFilesystemIsolationObservation struct {
	Mechanism                    string
	ScopeApprovalSHA256          string
	ReadOnlyInputObserved        bool
	OutsideWriteDenied           bool
	PrivateStagingObserved       bool
	StagingWriteObserved         bool
	NoReplaceHandoffObserved     bool
	ResidueRemoved               bool
	CompleteFilesystemSandbox    bool
	TestConformanceOnly          bool
	ProductProcessStarterPresent bool
	ExecutionAuthorized          bool
	ResultPersistenceAuthorized  bool
	ArtifactCommitAuthorized     bool
}

func TestAnalyzerImmutableHandleHandoffConformance(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	observation := observeAnalyzerImmutableHandleHandoff(t, chain.approval)
	if observation.Mechanism == "" || !validDigest(observation.ScopeApprovalSHA256) ||
		!observation.CallerHandleRetained || !observation.ChildHandleInherited ||
		!observation.PathReplacedBeforeChildRead || !observation.OriginalBytesObserved ||
		!observation.ReplacementBytesRejected || observation.PathIncludedInChildAuthority ||
		!observation.TestConformanceOnly || observation.ProductProcessStarterPresent ||
		observation.ExecutionAuthorized {
		t.Fatalf("unsafe or incomplete immutable-handle observation: %#v", observation)
	}
}

func TestAnalyzerDedicatedLowPrivilegeIdentityConformance(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	observation := observeAnalyzerLowPrivilegeIdentity(t, chain.approval)
	if observation.Mechanism == "" || !validDigest(observation.ScopeApprovalSHA256) ||
		!observation.SeparateIdentityContext || !observation.NonAdministratorObserved ||
		!observation.AmbientPrivilegesDenied || !observation.NoNewPrivilegesObserved ||
		observation.DedicatedAccountObserved || !observation.TestConformanceOnly ||
		observation.ProductProcessStarterPresent || observation.ExecutionAuthorized {
		t.Fatalf("unsafe or incomplete low-privilege identity observation: %#v", observation)
	}
}

func TestAnalyzerReadOnlyFilesystemPrivateStagingConformance(t *testing.T) {
	chain := mustAnalyzerScopeApprovalChain(t)
	observation := observeAnalyzerFilesystemIsolation(t, chain.approval)
	if observation.Mechanism == "" || !validDigest(observation.ScopeApprovalSHA256) ||
		!observation.ReadOnlyInputObserved || !observation.OutsideWriteDenied ||
		!observation.PrivateStagingObserved || !observation.StagingWriteObserved ||
		!observation.NoReplaceHandoffObserved || !observation.ResidueRemoved ||
		observation.CompleteFilesystemSandbox || !observation.TestConformanceOnly ||
		observation.ProductProcessStarterPresent || observation.ExecutionAuthorized ||
		observation.ResultPersistenceAuthorized || observation.ArtifactCommitAuthorized {
		t.Fatalf("unsafe or incomplete filesystem observation: %#v", observation)
	}
}

func TestAnalyzerIsolationBoundaryHelper(t *testing.T) {
	mode := os.Getenv(analyzerIsolationHelperModeEnv)
	if mode == "" {
		return
	}
	if err := runAnalyzerIsolationBoundaryHelper(mode); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func analyzerIsolationScopeDigest(t *testing.T, approval AnalyzerScopeLimitsApproval) string {
	t.Helper()
	digest, ok := canonicalSHA256(approval)
	if !ok {
		t.Fatal("cannot digest analyzer scope approval")
	}
	return digest
}

func analyzerIsolationBytesDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func analyzerNoReplaceHandoff(t *testing.T, stagingFile, destination string,
	expected []byte,
) bool {
	t.Helper()
	if err := os.Link(stagingFile, destination); err != nil {
		t.Fatalf("create no-replace result link: %v", err)
	}
	observed, err := os.ReadFile(destination)
	if err != nil || analyzerIsolationBytesDigest(observed) != analyzerIsolationBytesDigest(expected) {
		t.Fatalf("verify no-replace result: err=%v", err)
	}
	conflict := destination + ".conflict"
	if err := os.WriteFile(conflict, []byte("replacement-must-not-win"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(conflict, destination); err == nil {
		t.Fatal("no-replace handoff overwrote an existing destination")
	}
	after, err := os.ReadFile(destination)
	if err != nil || analyzerIsolationBytesDigest(after) != analyzerIsolationBytesDigest(expected) {
		t.Fatalf("no-replace destination drifted: err=%v", err)
	}
	return true
}
