package httpapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/plugins"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

func stageLifecyclePlugin(t *testing.T, service *plugins.Service, version, supersedes string, key ed25519.PrivateKey, packageID ...string) plugins.Installation {
	t.Helper()
	manifest := extensionTestPlugin("fixture").Manifest
	if len(packageID) != 0 {
		manifest.ID = packageID[0]
	}
	manifest.Version = version
	manifest.Hooks[0].Action, manifest.Hooks[0].Message = hooks.ActionDeny, "extension-private-message"
	raw, err := plugins.SignPackage(manifest, map[string][]byte{}, key, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	value, _, err := service.Stage(t.Context(), raw, plugins.InstallSource{Kind: "upload", URI: "sha256:" + hex.EncodeToString(digest[:]), SHA256: hex.EncodeToString(digest[:])}, supersedes, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func enableLifecyclePlugin(t *testing.T, service *plugins.Service, value plugins.Installation) plugins.Installation {
	t.Helper()
	for _, action := range []plugins.ReviewAction{plugins.ReviewApprove, plugins.ReviewEnable} {
		var err error
		value, err = service.Review(t.Context(), value.ID, plugins.ReviewRequest{Action: action, ExpectedPackageFingerprint: value.PackageFingerprint, ExpectedGeneration: value.Generation, Capabilities: []plugins.Capability{plugins.CapabilityHooks}, ReviewedBy: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
	}
	return value
}
func TestPluginLifecycleProductionHistoryRollbackAndPublisherRevocation(t *testing.T) {
	f, _, _, service := newExtensionOnboardingFixture(t)
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	old := stageLifecyclePlugin(t, service, "1.0.0", "", key)
	trust, err := service.TrustPublisher(t.Context(), old.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	old = enableLifecyclePlugin(t, service, old)
	current := enableLifecyclePlugin(t, service, stageLifecyclePlugin(t, service, "2.0.0", old.ID, key))
	var history PluginHistoryView
	decodeData(t, f.get(t, "/api/v1/extensions/plugins/"+current.ID+"/history"), &history)
	if history.ProtocolVersion != PluginLifecycleProtocol || history.InstallationID != current.ID || len(history.Installations) != 2 || history.Publisher == nil || history.Publisher.Generation != trust.Generation || len(history.PublisherInstallationIDs) != 1 || history.PublisherInstallationIDs[0] != current.ID {
		t.Fatalf("history=%#v", history)
	}
	for _, item := range history.Installations {
		if item.ID == old.ID {
			old.Generation, old.State = item.Generation, plugins.State(item.State)
		}
	}
	path := "/api/v1/extensions/plugins/" + current.ID + "/rollback"
	request := PluginRollbackRequestView{Version: PluginLifecycleProtocol, TargetInstallationID: old.ID, ExpectedCurrentFingerprint: current.PackageFingerprint, ExpectedCurrentGeneration: current.Generation, ExpectedTargetFingerprint: old.PackageFingerprint, ExpectedTargetGeneration: old.Generation, Capabilities: []plugins.Capability{plugins.CapabilityHooks}}
	stale := request
	stale.ExpectedCurrentGeneration--
	assertAPIError(t, extensionOnboardingRequest(t, f.api, path, stale), http.StatusConflict, "CONFLICT")
	var switched PluginRollbackView
	decodeDataStatus(t, extensionOnboardingRequest(t, f.api, path, request), http.StatusAccepted, &switched)
	if switched.Current.State != "rolled_back" || switched.Target.ID != old.ID || switched.Target.State != "enabled" {
		t.Fatalf("rollback=%#v", switched)
	}
	assertAPIError(t, extensionOnboardingRequest(t, f.api, path, request), http.StatusConflict, "CONFLICT")
	stored, err := f.store.GetPluginInstallation(t.Context(), old.ID)
	if err != nil || stored.Generation != switched.Target.Generation {
		t.Fatalf("retry changed target=%#v %v", stored, err)
	}
	declarations, err := service.ActiveHooks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	engine := hooks.NewEngine(f.store)
	if err := engine.Replace(declarations); err != nil {
		t.Fatal(err)
	}
	if result, err := engine.Execute(t.Context(), hooks.Input{Event: hooks.PreTool, RunID: f.run.ID, WorkspaceID: f.workspace.ID, ToolName: "file_write"}); err != nil || !result.Denied {
		t.Fatalf("actual hook=%#v %v", result, err)
	}
	sibling := enableLifecyclePlugin(t, service, stageLifecyclePlugin(t, service, "1.0.0", "", key, "sibling-package"))
	decodeData(t, f.get(t, "/api/v1/extensions/plugins/"+old.ID+"/history"), &history)
	if history.TotalPublisherInstallations != 2 || len(history.PublisherInstallationIDs) != 2 || history.TotalVersions != 2 {
		t.Fatalf("cross-package publisher impact=%#v", history)
	}
	var diagnostics HookDiagnosticsView
	response := f.get(t, ExtensionHookDiagnosticsPath+"?run_id="+f.run.ID)
	decodeData(t, response, &diagnostics)
	if diagnostics.WorkspaceID != f.workspace.ID || len(diagnostics.Declarations) != 3 || len(diagnostics.Observations) != 1 || diagnostics.Observations[0].Decision != "rejected" || diagnostics.Observations[0].PackageFingerprint != old.PackageFingerprint {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	for _, secret := range []string{"extension-private-message", "public_key", "payload", "SIGNATURE.json"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("diagnostics leaked %s", secret)
		}
	}
	revokePath := "/api/v1/extensions/plugins/" + old.ID + "/publisher-revocation"
	revoke := PluginPublisherRevocationRequestView{Version: PluginLifecycleProtocol, ExpectedPublisherFingerprint: trust.Fingerprint, ExpectedPublisherGeneration: trust.Generation, Confirm: true}
	wrong := revoke
	wrong.ExpectedPublisherFingerprint = strings.Repeat("c", 64)
	assertAPIError(t, extensionOnboardingRequest(t, f.api, revokePath, wrong), http.StatusConflict, "CONFLICT")
	var revoked PluginPublisherRevocationView
	decodeDataStatus(t, extensionOnboardingRequest(t, f.api, revokePath, revoke), http.StatusAccepted, &revoked)
	if revoked.Publisher.State != "revoked" || revoked.Publisher.Generation != trust.Generation+1 {
		t.Fatalf("revocation=%#v", revoked)
	}
	stored, _ = f.store.GetPluginInstallation(t.Context(), old.ID)
	if stored.State != plugins.StateRevoked || len(stored.EnabledCapabilities) != 0 {
		t.Fatalf("publisher revocation left active installation %#v", stored)
	}
	storedSibling, err := f.store.GetPluginInstallation(t.Context(), sibling.ID)
	if err != nil || storedSibling.State != plugins.StateRevoked || len(storedSibling.EnabledCapabilities) != 0 {
		t.Fatalf("publisher revocation left sibling active=%#v %v", storedSibling, err)
	}
	var afterRevocation HookDiagnosticsView
	decodeData(t, f.get(t, ExtensionHookDiagnosticsPath+"?run_id="+f.run.ID), &afterRevocation)
	if len(afterRevocation.Observations) != 1 || afterRevocation.Observations[0] != diagnostics.Observations[0] {
		t.Fatalf("revocation changed historical receipt=%#v", afterRevocation.Observations)
	}
	for _, declaration := range afterRevocation.Declarations {
		if declaration.Active {
			t.Fatalf("revoked declaration remained active=%#v", declaration)
		}
	}
	active, _ := service.ActiveHooks(t.Context())
	if len(active) != 0 {
		t.Fatalf("revoked hook remained active %#v", active)
	}
	assertAPIError(t, extensionOnboardingRequest(t, f.api, revokePath, revoke), http.StatusConflict, "CONFLICT")
}

func TestPluginLifecycleReadOnlyAndScopeBoundaries(t *testing.T) {
	f, _, _, _ := newExtensionOnboardingFixture(t)
	for _, path := range []string{ExtensionHookDiagnosticsPath, "/api/v1/extensions/plugins/missing/history"} {
		r := performRequest(t, f.api, http.MethodGet, path, "", "127.0.0.1:8765", "127.0.0.1:45000", nil)
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized read=%d", r.Code)
		}
		assertAPIError(t, extensionOnboardingRequest(t, f.api, path, map[string]string{}), http.StatusMethodNotAllowed, "INVALID_ARGUMENT")
	}
	for _, query := range []string{"?run_id=", "?run_id=" + f.run.ID + "&run_id=" + f.run.ID, "?workspace_id=missing", "?run_id=" + f.run.ID + "&workspace_id=other", "?secret=yes"} {
		response := f.get(t, ExtensionHookDiagnosticsPath+query)
		if response.Code == http.StatusOK {
			t.Fatalf("invalid scope accepted %s", query)
		}
	}
	for _, suffix := range []string{"rollback", "publisher-revocation"} {
		response := performRequest(t, f.api, http.MethodPost, "/api/v1/extensions/plugins/missing/"+suffix, testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", strings.NewReader(`{"version":"plugin-lifecycle.v1"}`))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("read token write=%d %s", response.Code, response.Body.String())
		}
	}
}
