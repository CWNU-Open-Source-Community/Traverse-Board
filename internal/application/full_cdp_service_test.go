package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
)

type fakeFullCDPStore struct {
	run                 domain.Run
	mission             domain.Mission
	permission          domain.RunBrowserCDPPermissionSnapshot
	executionPermission domain.RunExecutionPermissionSnapshot
}

func (f *fakeFullCDPStore) GetRunExecutionPermission(context.Context,
	string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return f.executionPermission, nil
}

func (f *fakeFullCDPStore) GetRun(context.Context, string) (domain.Run, error) {
	return f.run, nil
}

func (f *fakeFullCDPStore) GetMission(context.Context, string) (domain.Mission, error) {
	return f.mission, nil
}

func (f *fakeFullCDPStore) GetRunBrowserCDPPermission(context.Context,
	string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	return f.permission, nil
}

func fullCDPIdentityAcceptance(t *testing.T) (browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate,
) {
	t.Helper()
	relative := filepath.ToSlash(filepath.Join("Google", "Chrome", "Application", "chrome.exe"))
	identity := browserruntime.BrowserExecutableIdentity{
		ProtocolVersion: browserruntime.BrowserExecutableIdentityProtocolVersion,
		Product:         browserruntime.BrowserProductChrome, Channel: browserruntime.BrowserChannelStable,
		Vendor: "Google", RootID: browserruntime.DiscoveryRootProgramFiles,
		CanonicalPath: filepath.Join(t.TempDir(), filepath.FromSlash(relative)),
		RelativePath:  relative, HostGOOS: runtime.GOOS, HostGOARCH: runtime.GOARCH,
		TargetGOARCH: "amd64", ExecutableBytes: 1024,
		ExecutableSHA256: strings.Repeat("a", 64),
		VersionSource:    browserruntime.VersionSourceUnavailable,
		PEFormatVerified: true, RegularFileVerified: true, SymlinkRejected: true,
		MetadataOnly: true,
	}
	identity.Fingerprint = fullCDPFingerprint(t, identity)
	if err := browserruntime.ValidateBrowserExecutableIdentity(identity); err != nil {
		t.Fatal(err)
	}
	acceptance := browserruntime.BrowserAcceptanceCandidate{
		ProtocolVersion:               browserruntime.BrowserAcceptanceProtocolVersion,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		Product:                       identity.Product, Channel: identity.Channel, RootID: identity.RootID,
		ExecutableSHA256: identity.ExecutableSHA256, ExecutableBytes: identity.ExecutableBytes,
		TargetGOARCH: identity.TargetGOARCH,
		Decision:     browserruntime.BrowserAcceptanceAccepted,
		ReasonCode:   browserruntime.BrowserAcceptanceReasonPublisherVerified,
		Evidence: browserruntime.AuthenticodeEvidence{
			Source: browserruntime.AuthenticodeSourceWindows, Publisher: "Google LLC",
			CertificateSHA256: strings.Repeat("b", 64), SignatureVerified: true,
			SameOpenHandleVerified: true, CacheOnlyVerification: true,
			PublisherPolicyMatched:    true,
			PublisherPolicyVersion:    browserruntime.BrowserPublisherPolicyVersion,
			PublisherEvidenceComplete: true,
		},
		SameHandleBytesRevalidated: true, SameFilePathRevalidated: true,
		PERevalidated: true, ReviewEligible: true, StartBlocked: true, MetadataOnly: true,
	}
	acceptance.Fingerprint = fullCDPFingerprint(t, acceptance)
	if err := browserruntime.ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		t.Fatal(err)
	}
	return identity, acceptance
}

func fullCDPFingerprint(t *testing.T, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case browserruntime.BrowserExecutableIdentity:
		typed.Fingerprint = ""
		value = typed
	case browserruntime.BrowserAcceptanceCandidate:
		typed.Fingerprint = ""
		value = typed
	default:
		t.Fatalf("unsupported full CDP fixture fingerprint type %T", value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func TestFullCDPServiceRefusesWithoutConfirmation(t *testing.T) {
	now := time.Now().UTC().Round(time.Millisecond)
	identity, acceptance := fullCDPIdentityAcceptance(t)
	permission, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-full-service", domain.Run{ID: "run-full-service",
			MissionID: "mission-full-service", Status: domain.RunCreated, CreatedAt: now},
		domain.Mission{ID: "mission-full-service", CreatedAt: now},
		"runtime-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	full, err := permission.Next("browser-cdp-full-debug",
		domain.RunBrowserCDPPermissionFullDebug, true, "runtime-operator",
		"full debug", now)
	if err != nil {
		t.Fatal(err)
	}
	executionInitial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"execution-permission-initial", domain.Run{ID: "run-full-service",
			MissionID: "mission-full-service", Status: domain.RunCreated, CreatedAt: now},
		domain.Mission{ID: "mission-full-service", CreatedAt: now},
		"runtime-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	executionFull, err := executionInitial.Next("execution-permission-full",
		domain.RunExecutionPermissionFullAccess, true, "runtime-operator",
		"full access", now)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	if _, err := authority.ActivateRunFullAccess(executionFull); err != nil {
		t.Fatal(err)
	}
	executionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	store := &fakeFullCDPStore{
		run: domain.Run{ID: "run-full-service", MissionID: "mission-full-service",
			SessionID: "session-full-service", Status: domain.RunCreated, CreatedAt: now},
		mission: domain.Mission{ID: "mission-full-service",
			WorkspaceID: "workspace-full-service", CreatedAt: now},
		permission: full, executionPermission: executionFull,
	}
	service := NewFullCDPServiceWithExecutionCapabilities(store,
		browserruntime.FullCDPRuntimeCapabilities{
			StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true,
		},
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true,
			FullDebugEnabled: true}, executionCapabilities)
	_, err = service.Open(t.Context(), FullCDPOpenRequest{
		RunID: "run-full-service", Target: "http://127.0.0.1:18080",
		Identity: identity, Acceptance: acceptance,
	})
	if err == nil {
		t.Fatal("unconfirmed full CDP unexpectedly opened a session")
	}
}
