package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolcontract"
)

func TestSkillPackageInstallHTTPControlRegistersOnlyInertAuthority(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "skill-install-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	objects, err := skills.NewLocalPackageObjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := skills.BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(state, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, SkillInstallationEnabled: true,
		SkillInstallationController: application.NewSkillPackageRegistryService(
			state, objects, registry)})
	if err != nil {
		t.Fatal(err)
	}
	archive := buildOpenAPISkillPackage(t)
	encoded := base64.StdEncoding.EncodeToString(archive)
	body := `{"version":"skill_package_installation.v1","archive_base64":"` +
		encoded + `","surface":"code","confirm_untrusted":true}`
	operationKey := "http-skill-install-0001"
	first := performSessionMessageRequest(t, api, http.MethodPost,
		SkillPackageInstallPath, testControlToken, operationKey, "application/json",
		strings.NewReader(body))
	if first.Code != http.StatusAccepted ||
		!strings.Contains(first.Body.String(), `"protocol_version":"plugin-installation.v2"`) ||
		!strings.Contains(first.Body.String(), `"surface":"code"`) ||
		strings.Contains(first.Body.String(), encoded) {
		t.Fatalf("Skill install status=%d body=%s", first.Code, first.Body.String())
	}
	var envelope struct {
		Data PluginSkillInstallView `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	installed := envelope.Data.Installation
	if installed.State != "staged" || installed.Generation != 1 || len(installed.EnabledCapabilities) != 0 ||
		installed.ID == "" || installed.Snapshot == nil || installed.Snapshot.Format != "traverse-skill" {
		t.Fatalf("new import did not use inert Plugin staging: %+v", envelope.Data)
	}
	if strings.Contains(first.Body.String(), `"receipt"`) || strings.Contains(first.Body.String(), `"object_key"`) {
		t.Fatal("new import fabricated a legacy receipt")
	}
	legacy, err := state.ListInstalledPackages(t.Context(), "", "", true)
	if err != nil || len(legacy) != 0 {
		t.Fatal("new import dual-wrote legacy installs", err)
	}
	replay := performSessionMessageRequest(t, api, http.MethodPost,
		SkillPackageInstallPath, testControlToken, operationKey, "application/json",
		strings.NewReader(body))
	if replay.Code != http.StatusAccepted ||
		!strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("Skill install replay status=%d body=%s", replay.Code,
			replay.Body.String())
	}
	unconfirmed := performSessionMessageRequest(t, api, http.MethodPost,
		SkillPackageInstallPath, testControlToken, "http-skill-unconfirmed-0001",
		"application/json", strings.NewReader(strings.Replace(body,
			`"confirm_untrusted":true`, `"confirm_untrusted":false`, 1)))
	assertAPIError(t, unconfirmed, http.StatusBadRequest, "INVALID_ARGUMENT")
	readToken := performSessionMessageRequest(t, api, http.MethodPost,
		SkillPackageInstallPath, testAccessToken, "http-skill-read-token-0001",
		"application/json", strings.NewReader(body))
	assertAPIError(t, readToken, http.StatusUnauthorized, "POLICY_DENIED")
}

func TestSkillPackageInstallHTTPNativeSnapshotRetainsLargeResources(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "native-install.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	objects, _ := skills.NewLocalPackageObjectStore(t.TempDir())
	builtins, _ := skills.BuiltinRegistry()
	api, err := New(st, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		SkillInstallationEnabled: true, SkillInstallationController: application.NewSkillPackageRegistryService(st, objects, builtins)})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "http-native")
	if err := os.MkdirAll(filepath.Join(directory, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: http-native\ndescription: Read original resource bytes.\n---\nOriginal instructions.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	asset := make([]byte, 72*1024)
	if _, err := rand.Read(asset); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "data.bin"), asset, 0600); err != nil {
		t.Fatal(err)
	}
	pkg, err := plugins.CapturePortableDirectory(t.Context(), directory, "http-native", toolcontract.SourceRef{URI: directory}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := SkillPackageInstallRequestView{Version: plugins.PortableInstallationProtocol, Snapshot: &pkg.Snapshot,
		ArchiveBase64: base64.StdEncoding.EncodeToString(pkg.Archive()), Surface: "code", ConfirmUntrusted: true}
	body, _ := json.Marshal(request)
	response := performSessionMessageRequest(t, api, http.MethodPost, SkillPackageInstallPath,
		testControlToken, "http-native-snapshot-operation", "application/json", bytes.NewReader(body))
	if response.Code != http.StatusAccepted {
		t.Fatalf("native status=%d body=%s", response.Code, response.Body)
	}
	var envelope struct {
		Data PluginSkillInstallView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	installation, err := st.GetPluginInstallation(t.Context(), envelope.Data.Installation.ID)
	if err != nil || installation.State != plugins.StateStaged || installation.Source.Kind != "upload" ||
		installation.Source.URI != "sha256:"+installation.ArchiveSHA256 {
		t.Fatalf("installation=%+v err=%v", installation, err)
	}
	retained, err := st.LoadPluginObject(t.Context(), installation.ID)
	if err != nil || !bytes.Equal(retained, pkg.Archive()) {
		t.Fatal("native upload archive drift", err)
	}
	reader, err := plugins.OpenPortableSnapshot(t.Context(), *installation.Snapshot, retained, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, _, err := reader.Read(t.Context(), installation.Snapshot.Skills[0].Instructions.Component, "assets/data.bin", len(asset))
	if err != nil || !bytes.Equal(got, asset) {
		t.Fatal("native uploaded resource changed", err)
	}
	request.Snapshot.Skills[0].Description = "forged metadata"
	body, _ = json.Marshal(request)
	invalid := performSessionMessageRequest(t, api, http.MethodPost, SkillPackageInstallPath,
		testControlToken, "http-native-forged-snapshot-key", "application/json", bytes.NewReader(body))
	assertAPIError(t, invalid, http.StatusBadRequest, "INVALID_ARGUMENT")
	values, err := st.ListPluginInstallations(t.Context(), "", 10)
	if err != nil || len(values) != 1 {
		t.Fatal("invalid snapshot created an installation", err)
	}
}
